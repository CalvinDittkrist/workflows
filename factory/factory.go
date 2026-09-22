package main

import (
	"bufio"
	"context"
	"encoding/json"
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

	mu         sync.Mutex
	queue      []Issue
	unreadable map[string]string
	polledAt   time.Time
	connecting bool
	user       string // the login this host's gh is logged in as, read once and kept
	// quotaUntil is served empty until the quota check arrives (ADR 0028); the interface carries the
	// state from the start so the ticket that fills it changes no reader.
	quotaUntil *time.Time
}

// source is where the line comes from on every poll: GitHub, or the canned queue of fake mode. It
// answers with what it could read and reports what it could not, so nothing of it is ever stored.
type source interface {
	queue(ctx context.Context) poll
}

// poll is one reading of the line: the routed issues the source could read, and the repositories it
// could not, with what stood in the way. The factory serves the second beside the first, because a
// repository nobody can read holds no issues either, and an empty line is otherwise the same sight
// as an idle one — on the one surface an unattended factory is watched through.
type poll struct {
	issues     []Issue
	unreadable map[string]string // repository -> what gh said
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

// Connect makes sure every connected repository has a clone under the data directory before the
// factory takes work. It says so on the interface while it runs, because a first clone takes minutes
// and a line that has not been polled yet would otherwise be the sight of an idle factory.
func (f *Factory) Connect(ctx context.Context) {
	if f.fake {
		return // fake mode works a canned queue; there is no repository behind it to clone
	}
	f.mu.Lock()
	f.connecting = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.connecting = false
		f.mu.Unlock()
	}()
	connect(ctx, f.settings)
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
	read := f.source.queue(ctx)
	queue := read.issues
	sort.SliceStable(queue, func(a, b int) bool {
		if !queue[a].RoutedAt.Equal(queue[b].RoutedAt) {
			return queue[a].RoutedAt.Before(queue[b].RoutedAt) // work in the order the maintainer routed
		}
		return queue[a].key() < queue[b].key()
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue, f.unreadable, f.polledAt = queue, read.unreadable, time.Now()
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

// execute takes the issue on the remote and runs one worker session from start to end, reading its
// stream until the process is gone. The deadline covers the claim as well: a factory that is stopped
// while it claims leaves the issue rather than starting a worker nobody waits for.
func (f *Factory) execute(parent context.Context, r *Run, issue Issue) {
	ctx, cancel := context.WithTimeout(parent, f.settings.Deadline)
	defer cancel()

	claim, err := f.take(ctx, r, issue)
	if err != nil {
		if errors.Is(err, errLost) {
			// Another claimer created the branch first. Nothing here was touched: no assignee, no
			// worktree, no worker ([ADR 0024]). The run is the record that this factory will not try
			// the issue again.
			f.finish(r, outcomeLost, "another claimer holds "+claim.branch+" on the remote; this run touched nothing else", nil)
			return
		}
		if parent.Err() != nil {
			// The factory was stopped while it claimed, so git and gh were ended under it. That is not
			// a claim that failed, and the record must not name a failure that never happened.
			f.finish(r, outcomeInterrupted, "the factory stopped while this run was claiming the issue"+leftBehind(claim), nil)
			return
		}
		f.finish(r, outcomeFailed, "the issue could not be claimed: "+err.Error()+leftBehind(claim), nil)
		return
	}
	// The session starts in /worker:work, which invokes no skill for its first stage.
	f.runs.update(r, func() { r.stage("implement") })

	cmd, err := f.worker(ctx, issue, claim)
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

// leftBehind says what a claim that did not finish left on the remote, which is what the operator
// needs to decide: a claim that never got to create the branch took nothing, and one that did holds
// the issue by it until somebody removes it — nothing here deletes work ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-what-waits-waits-for-a-person.md
func leftBehind(claim claimed) string {
	if !claim.created {
		return "; nothing was claimed on the remote"
	}
	return "; the branch " + claim.branch + " was created on the remote and is left behind: remove it to work the issue again"
}

// take claims the issue on the remote and records the branch that claim is. Fake mode claims
// nothing: its queue is canned and there is no remote behind it, so its scripted worker runs where
// the factory itself does.
func (f *Factory) take(ctx context.Context, r *Run, issue Issue) (claimed, error) {
	if f.fake {
		return claimed{}, nil
	}
	claim, err := f.claim(ctx, r, issue)
	// The branch is recorded whether the claim was won or lost: it is what the claim was, and for a
	// lost one it names the branch that holds the issue.
	if claim.branch != "" {
		f.runs.update(r, func() { r.Branch, r.Base = claim.branch, claim.base })
	}
	return claim, err
}

// worker is the command of one run: the session a local claim starts, in print mode with the stream
// of events on its output, run in the worktree the claim made ([ADR 0022]). In fake mode it is this
// binary again, printing the stream of a scripted worker: no tokens, no git, no GitHub. The
// configured worker arguments go to the worker command whichever it is; the scripted worker ignores
// them.
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
func (f *Factory) worker(ctx context.Context, issue Issue, claim claimed) (*exec.Cmd, error) {
	if f.fake {
		args := []string{"scripted-worker", issue.scenario, issue.Repository, strconv.Itoa(issue.Number)}
		return exec.CommandContext(ctx, f.self, append(args, f.settings.WorkerArgs...)...), nil
	}
	variables := workerVariables(issue, claim)
	settings, err := workerSettings(variables)
	if err != nil {
		return nil, err
	}
	// The mode is manual: the factory never merges what it built. A finished run waits for the
	// maintainer on GitHub, which is the only surface the factory is steered from ([ADR 0023]).
	// Foreground subagents are the same setting a local claim makes ([ADR 0017]), and the auto
	// permission mode is what being unattended costs: no prompt has anybody to ask ([ADR 0027]).
	args := []string{
		"--agent", "worker",
		"--output-format", "stream-json", "--verbose",
		"--permission-mode", "auto",
		"--strict-mcp-config",
		"--settings", settings,
	}
	args = append(args, f.settings.WorkerArgs...)
	args = append(args, "-p", workSkill)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = claim.worktree
	cmd.Env = workerEnv(os.Environ(), variables)
	return cmd, nil
}

// workSkill is the prompt a worker session starts with, the same one a local claim gives it.
const workSkill = "/worker:work"

// workerSettings is the session-scoped configuration a worker is started with, as JSON for
// --settings. It is the settings object of the local claim (plugins/orchestrator/scripts/claim.sh)
// without the one part only a Herdr pane can carry, its status line: nothing renders a status line in
// print mode, and there is no pane for a checkpoint to hand the stage over to.
//
// That is why the compact pin matters more here than anywhere: a factory session has no hand-over at
// all, so compaction is its only safety net, and it must fire where the workflow says rather than at
// a default Claude Code does not document ([ADR 0031], [ADR 0034]). The window and the percentage are
// the claim's numbers, and a drift test binds them to it.
//
// WF_BASE_BRANCH is the base the claim actually cut the branch from. The pipeline inside the worktree
// asks wf_base_branch for it — the review range, the hand-over note, the pull request's --base — and
// without it a clone whose origin/HEAD names another branch would review and open against a base the
// branch was never cut from.
//
// The plugins the local claim switches off are switched off here too: a worker carries neither the
// planner's nor the orchestrator's skills, which keeps them out of an unattended context that must
// never merge what it built ([ADR 0023]).
//
// [ADR 0023]: ../docs/adr/0023-github-is-the-factorys-only-control-surface.md
// [ADR 0031]: ../docs/adr/0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md
// [ADR 0034]: ../docs/adr/0034-the-compact-trigger-is-raised-through-the-window.md
func workerSettings(env map[string]string) (string, error) {
	settings, err := json.Marshal(map[string]any{
		"env":               env,
		"enabledPlugins":    map[string]bool{"planner@workflows": false, "orchestrator@workflows": false},
		"autoCompactWindow": compactWindow,
	})
	if err != nil {
		return "", fmt.Errorf("the worker's settings could not be written: %w", err)
	}
	return string(settings), nil
}

// workerVariables is the env block of those settings: what this one session is, and nothing a host
// may disagree with.
func workerVariables(issue Issue, claim claimed) map[string]string {
	return map[string]string{
		"WF_MODE":                              "manual",
		"WF_ISSUE":                             strconv.Itoa(issue.Number),
		"WF_BASE_BRANCH":                       claim.base,
		"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS": "1",
		"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE":      compactPercentage,
	}
}

// The compact pin of the workflow, the two numbers the local claim sets and this one restates
// ([ADR 0031], [ADR 0034]). Their product is the compact trigger, 200 000 tokens.
//
// [ADR 0031]: ../docs/adr/0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md
// [ADR 0034]: ../docs/adr/0034-the-compact-trigger-is-raised-through-the-window.md
const (
	compactWindow     = 250000
	compactPercentage = "80"
)

// workerEnv is the environment a worker runs in: the factory's own, with two kinds of variable taken
// out of it. Every Herdr variable, because the factory may be started from a maintainer's terminal
// and a worker that inherits
// HERDR_ENV would act in that person's session instead of ending blocked ([ADR 0027]). And every
// variable of the workflow, WF_*, because the session's settings say what this run is: which of the
// two Claude Code prefers for a name both carry is not documented, and a WF_MODE=yolo left in a
// maintainer's shell must not be the answer.
//
// [ADR 0027]: ../docs/adr/0027-the-factorys-isolation-boundary-is-the-host.md
func workerEnv(env []string, settings map[string]string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "HERDR_") || strings.HasPrefix(name, "WF_") {
			continue
		}
		if _, said := settings[name]; said {
			continue
		}
		out = append(out, entry)
	}
	return out
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
