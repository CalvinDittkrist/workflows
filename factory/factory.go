package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Issue is one routed issue, as the queue carries it. The queue is derived on every poll and never
// stored, so an issue exists in the factory only as long as GitHub says it is routed to it.
type Issue struct {
	Repository string    `json:"repository"`
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	Labels     []string  `json:"labels"`
	RoutedAt   time.Time `json:"routedAt"` // when the routing label was set; the queue's order

	scenario string // fake mode only: which scripted worker works this entry
}

func (i Issue) key() string { return fmt.Sprintf("%s#%d", i.Repository, i.Number) }

// Factory is the service: it holds the queue it last derived, the runs it has made, and the one
// worker it runs at a time.
type Factory struct {
	settings Settings
	fake     bool
	source   source
	runs     *Store
	started  time.Time
	self     string        // this binary, which fake mode starts again as the scripted worker
	wake     chan struct{} // a run ended: the next entry need not wait for the next poll
	active   sync.WaitGroup

	mu       sync.Mutex
	queue    []Issue
	polledAt time.Time
	// quotaUntil is served empty until the quota check arrives (ADR 0028); the interface carries the
	// state from the start so the ticket that fills it changes no reader.
	quotaUntil *time.Time
}

// source is where the line comes from on every poll: GitHub, or the canned queue of fake mode. It
// answers with what it could read and reports what it could not, so nothing of it is ever stored.
type source interface {
	queue(ctx context.Context) []Issue
}

// New opens the data directory and takes the runs already in it. Nothing here starts a run and
// nothing here reads GitHub: the caller listens first, so a second factory on this host fails before
// this is reached.
func New(settings Settings, fake bool) (*Factory, error) {
	runs, err := OpenStore(settings.DataDir)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("the factory cannot find its own binary: %w", err)
	}
	f := &Factory{settings: settings, fake: fake, runs: runs, started: time.Now(), self: self, wake: make(chan struct{}, 1)}
	f.source = &gitHub{repositories: settings.Repositories, label: settings.Label}
	if fake {
		f.source = &canned{repositories: settings.Repositories, started: f.started}
	}
	return f, nil
}

// Work derives the queue, starts the run at its head, and does so again on every poll and whenever a
// run ends. It returns when the context is done and the run that was active has ended.
func (f *Factory) Work(ctx context.Context) {
	for ctx.Err() == nil {
		f.refreshQueue(ctx)
		f.dispatch(ctx)
		select {
		case <-ctx.Done():
		case <-f.wake:
		case <-time.After(f.settings.Poll):
		}
	}
	f.active.Wait()
}

// refreshQueue derives the line of routed issues of all connected repositories. It is asked from the
// source on every poll and only held in memory: the queue is a view of GitHub, never a state of the
// factory ([ADR 0025]).
//
// [ADR 0025]: ../docs/adr/0025-one-queue-one-worker-work-in-progress-first.md
func (f *Factory) refreshQueue(ctx context.Context) {
	queue := f.source.queue(ctx)
	sort.SliceStable(queue, func(a, b int) bool {
		if !queue[a].RoutedAt.Equal(queue[b].RoutedAt) {
			return queue[a].RoutedAt.Before(queue[b].RoutedAt) // work in the order the maintainer routed
		}
		return queue[a].key() < queue[b].key()
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue, f.polledAt = queue, time.Now()
}

// dispatch starts the head of the queue. One worker at a time: while a run is active, nothing else
// starts, and while the factory is paused nothing starts at all.
func (f *Factory) dispatch(ctx context.Context) {
	if f.settings.Paused || ctx.Err() != nil {
		return
	}
	for _, r := range f.runs.list() {
		if r.EndedAt == nil {
			return
		}
	}
	if waiting := f.waiting(); len(waiting) > 0 {
		f.start(ctx, waiting[0])
	}
}

// waiting is the queue without the issues a run has already taken. It is what the factory would
// start next and what the interface serves as the queue. Having a run is final here: the release
// signal, which queues a second run for an issue, arrives with the ticket that resumes runs.
func (f *Factory) waiting() []Issue {
	worked := map[string]bool{}
	for _, run := range f.runs.list() {
		worked[run.key()] = true
	}
	f.mu.Lock()
	queue := f.queue
	f.mu.Unlock()
	out := []Issue{}
	for _, issue := range queue {
		if !worked[issue.key()] {
			out = append(out, issue)
		}
	}
	return out
}

// start records the run and works it in the background, so the loop keeps polling while it runs. A
// factory that is already stopping records nothing: the run would count as worked without ever
// having run.
func (f *Factory) start(ctx context.Context, issue Issue) {
	if ctx.Err() != nil {
		return
	}
	r := &Run{
		Repository: issue.Repository,
		Issue:      issue.Number,
		Title:      issue.Title,
		State:      "running",
		StartedAt:  time.Now(),
		Stages:     []string{},
		Warnings:   []string{},
	}
	r.stage("implement") // the session starts in /worker:work, which invokes no skill for its first stage
	f.runs.add(r)
	f.active.Add(1)
	go func() {
		defer f.active.Done()
		f.execute(ctx, r, issue)
		select { // the next entry starts now, not at the next poll
		case f.wake <- struct{}{}:
		default:
		}
	}()
}

// execute runs one worker session from start to end and reads its stream until the process is gone.
func (f *Factory) execute(parent context.Context, r *Run, issue Issue) {
	ctx, cancel := context.WithTimeout(parent, f.settings.Deadline)
	defer cancel()

	cmd, err := f.worker(ctx, issue)
	if err != nil {
		f.finish(r, outcomeFailed, "no worker could be started: "+err.Error(), nil)
		return
	}
	// The worker starts subprocesses of its own; the deadline and the stop have to reach all of them,
	// so it gets a process group of its own and the group is what is ended.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return endGroup(cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second

	// The pipes belong to the factory, not to exec: a worker that leaves a process behind must not be
	// able to hold the factory in Wait. After the group is ended every writer in it is gone; one that
	// left the group is not, which is why the reading below has an end of its own.
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		f.finish(r, outcomeFailed, "the factory could not open a pipe for the worker: "+err.Error(), nil)
		return
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		stdout.Close()
		stdoutWriter.Close()
		f.finish(r, outcomeFailed, "the factory could not open a pipe for the worker: "+err.Error(), nil)
		return
	}
	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter
	if err := cmd.Start(); err != nil {
		stdout.Close()
		stdoutWriter.Close()
		stderr.Close()
		stderrWriter.Close()
		f.finish(r, outcomeFailed, "the worker could not be started: "+err.Error(), nil)
		return
	}
	stdoutWriter.Close() // the worker holds the only writing ends now
	stderrWriter.Close()
	f.runs.event(r, Event{Kind: "factory", Title: "worker started", Body: fmt.Sprint(cmd.Args)})

	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		f.read(r, "the worker's output", stdout, func(line []byte) { f.ingest(r, line) })
	}()
	go func() {
		defer readers.Done()
		// Every line of the error output counts as the cause of a failure, the last one winning. A
		// real session also writes what is only noise there; the ticket that starts one decides what
		// of it is a cause.
		f.read(r, "the worker's error output", stderr, func(line []byte) {
			f.error(r, Event{Kind: "error", Title: "stderr: " + firstLine(string(line)), Body: string(line)})
		})
	}()

	waitErr := cmd.Wait()
	// Nothing the worker started survives its run, whether it ended by itself, in an error or on the
	// deadline. The group is ended right after the worker was reaped, which also closes the pipes a
	// process it left behind would still hold open.
	_ = endGroup(cmd.Process.Pid, syscall.SIGKILL)
	// What the group wrote before it ended is in the pipes and is read in a moment. A process that
	// took a session of its own — a server started with nohup — is outside the group and keeps the
	// pipes open for as long as it lives, which no deadline ends: the factory stops reading instead,
	// or this run would never end and no other would ever start.
	drained := make(chan struct{})
	go func() { readers.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(drainGrace):
		stdout.Close() // ends the two readers
		stderr.Close()
		<-drained
		warning := "the worker left a process behind that is outside its process group and still held its output; the factory cannot end it, look for it on the host"
		f.runs.update(r, func() { r.Warnings = append(r.Warnings, warning) })
		f.runs.event(r, Event{Kind: "error", Title: "the worker left a process behind", Body: warning})
	}
	stdout.Close()
	stderr.Close()

	exitCode := cmd.ProcessState.ExitCode()
	// A worker that ended by itself and reported is read by its report: a stop or a deadline that
	// arrives in the same moment ended nothing, and its pull request would be lost to the record.
	reported := waitErr == nil && r.reportOutcome != ""
	switch {
	case !reported && parent.Err() != nil:
		f.finish(r, outcomeInterrupted, "the factory stopped while this run was active; its worker was ended", &exitCode)
	case !reported && errors.Is(ctx.Err(), context.DeadlineExceeded):
		f.finish(r, outcomeTimeout, fmt.Sprintf("the deadline of %s passed; the worker's process group was ended", f.settings.Deadline), &exitCode)
	case r.reportOutcome == outcomeReady:
		url, reason := pullRequest(r.reportDetail, r.Repository)
		f.runs.update(r, func() { r.PullRequest = url })
		f.finish(r, outcomeReady, reason, &exitCode)
	case r.reportOutcome == outcomeBlocked:
		f.runs.update(r, func() { r.Reason = r.reportDetail })
		f.finish(r, outcomeBlocked, "", &exitCode)
	case waitErr != nil:
		cause := r.lastError
		if cause == "" {
			cause = r.resultSummary
		}
		f.finish(r, outcomeFailed, strings.TrimSpace(fmt.Sprintf("the session ended in an error (exit %d): %s", exitCode, cause)), &exitCode)
	default:
		f.finish(r, outcomeFailed, "the session ended without a report; a worker ends by reporting ready: or blocked:", &exitCode)
	}
}

// worker is the command of one run. In fake mode it is this binary again, printing the stream of a
// scripted worker: no tokens, no git, no GitHub. The configured worker arguments go to the worker
// command whichever it is; the scripted worker ignores them.
func (f *Factory) worker(ctx context.Context, issue Issue) (*exec.Cmd, error) {
	if !f.fake {
		return nil, errors.New("only fake mode starts workers so far")
	}
	args := []string{"scripted-worker", issue.scenario, issue.Repository, strconv.Itoa(issue.Number)}
	return exec.CommandContext(ctx, f.self, append(args, f.settings.WorkerArgs...)...), nil
}

// endGroup sends a signal to the whole process group of a worker. A group that is already gone is
// not an error: the run ended and nothing is left to end.
func endGroup(pid int, signal syscall.Signal) error {
	if err := syscall.Kill(-pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// drainGrace is how long the factory reads on after the worker and its process group are gone. What
// they wrote is already in the pipes, so this is only ever waited out for a process that left the group.
const drainGrace = 3 * time.Second

// One line of the worker's stream carries a whole tool result, so the reader's buffer is generous; a
// line beyond it is refused rather than split into halves that are not JSON.
const maxStreamLine = 64 << 20

// read takes a worker's stream line by line. A stream that cannot be read to its end says so in the
// run's log — the alternative is a silent reader and a worker that blocks on a pipe nobody empties
// until the deadline ends it — and what is left of it is drained so the worker can finish.
func (f *Factory) read(r *Run, what string, stream io.Reader, line func([]byte)) {
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 64*1024), maxStreamLine)
	for scanner.Scan() {
		line(scanner.Bytes())
	}
	// A stream the factory closed itself is not an error of the stream: see drainGrace.
	if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		f.error(r, Event{Kind: "error", Title: what + " could not be read to its end", Body: err.Error()})
		_, _ = io.Copy(io.Discard, stream)
	}
}

// error records an error event and keeps it as the reason a failed run ended.
func (f *Factory) error(r *Run, e Event) {
	f.runs.update(r, func() {
		if body := firstLine(e.Body); body != "" {
			r.lastError = body
		} else {
			r.lastError = e.Title
		}
	})
	f.runs.event(r, e)
}

// finish ends a run with an event that says why, so the log reads to the end.
func (f *Factory) finish(r *Run, outcome, reason string, exitCode *int) {
	kind := "factory"
	if outcome != outcomeReady {
		kind = "error"
	}
	if reason != "" {
		f.runs.event(r, Event{Kind: kind, Title: outcome, Body: reason})
	}
	f.runs.finish(r, outcome, reason, exitCode)
	log.Printf("run %d (%s#%d) ended: %s", r.ID, r.Repository, r.Issue, outcome)
}
