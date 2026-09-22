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
	"path/filepath"
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

	// assignedAt is when an assignee was last put on the issue and unassignedAt when one was last
	// taken off, read from the same event list as the routing time. A removal that is newer than the
	// assignment it undid is the release signal for an issue this factory holds, and its time is
	// where the resumed run stands in the line ([ADR 0026]).
	//
	// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
	assignedAt   time.Time
	unassignedAt time.Time
	scenario     string // fake mode only: which scripted worker works this entry
}

func (i Issue) key() string { return fmt.Sprintf("%s#%d", repositoryKey(i.Repository), i.Number) }

// repositoryKey is a repository name as everything that matches on one spells it. GitHub answers for
// either spelling, so a configuration rewritten from Acme/Repo to acme/repo names the repository
// this host already works: the record of a run keeps the spelling of the day it was written, and a
// factory that told the two apart would drop the work those records hold out of its line and claim
// their issues anew against the branches it owns itself. The clone lies under this name for the same
// reason.
func repositoryKey(name string) string { return strings.ToLower(name) }

// Entry is one place in the line: an issue the factory would claim, or work it already holds and
// would resume in the worktree of that claim. It is what the interface serves as the queue.
type Entry struct {
	Issue
	// Signal is what put this entry in the line, and SignalAt when that signal happened: the routing
	// label for a new issue, the interruption or the release for work the factory holds. Both are
	// the order of the line, work in progress first ([ADR 0025]).
	//
	// [ADR 0025]: ../docs/adr/0025-one-queue-one-worker-work-in-progress-first.md
	Signal   string    `json:"signal"`
	SignalAt time.Time `json:"signalAt"`

	// resume is the run this entry continues: the claim a resume signal resumes under, and for a
	// routed issue the run that once held it and was let go, whose branch the claim takes back
	// instead of claiming the issue anew (waiting, readopt). It is empty for every other routing,
	// which is an issue this factory has never held.
	resume Run
}

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
	user       string          // the login this host's gh is logged in as, read once and kept
	held       map[string]bool // repositories this factory claims nothing from, so the log says it once
	// cancelling is how a poll ends a run that is still going: the cancel of the context that run
	// works under, by run, put there when the run starts and taken out when it ends or is cancelled.
	// Only a run of this factory is in it, so a record of an older start can never be signalled here.
	cancelling map[int]context.CancelCauseFunc
	// askedHeld is when each issue this factory holds was last asked about on GitHub, by the state it
	// was in when it was asked (heldIssues). It is the memory the cadence of those readings rests on
	// and nothing else reads it.
	askedHeld map[string]time.Time
	// quotaUntil is served empty until the quota check arrives (ADR 0028); the interface carries the
	// state from the start so the ticket that fills it changes no reader.
	quotaUntil *time.Time
}

// source is where the line comes from on every poll: GitHub, or the canned queue of fake mode. It
// answers with what it could read and reports what it could not, so nothing of it is ever stored.
// The issues this factory holds are asked about in the same reading, because they are not in the
// line — an issue the factory holds is assigned to this host, which is what takes it out of it.
type source interface {
	queue(ctx context.Context, held []Held) poll
}

// Held is one issue this factory holds, as a poll asks the source about it: the issue, the run that
// holds it, and the pull request a run of it opened if one stands. It is the one reading through
// which a maintainer's decision reaches work in progress — the routing label taken off, the issue
// closed, the pull request merged or closed — and GitHub is the only surface those decisions are
// made on ([ADR 0023]).
//
// [ADR 0023]: ../docs/adr/0023-github-is-the-only-control-surface-of-the-factory.md
type Held struct {
	Repository  string
	Number      int
	PullRequest string // the pull request a run of this issue opened, or empty
}

func (h Held) key() string { return Issue{Repository: h.Repository, Number: h.Number}.key() }

// poll is one reading of the line: the routed issues the source could read, and the repositories it
// could not, with what stood in the way. The factory serves the second beside the first, because a
// repository nobody can read holds no issues either, and an empty line is otherwise the same sight
// as an idle one — on the one surface an unattended factory is watched through.
type poll struct {
	issues     []Issue
	unreadable map[string]string // repository -> what gh said
	// letGo is the held issues whose reading says this factory is done with them, with the decision
	// that says so. An issue whose reading failed is never in it: a rate limit, an expired login or
	// a repository nobody can reach must not take a worktree apart ([ADR 0026]).
	//
	// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
	letGo map[string]string // issue key -> the decision that ends this factory's part
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
	f := &Factory{settings: settings, fake: fake, runs: runs, started: time.Now(), self: self,
		wake: make(chan struct{}, 1), held: map[string]bool{}, cancelling: map[int]context.CancelCauseFunc{},
		askedHeld: map[string]time.Time{}}
	f.source = &gitHub{repositories: settings.Repositories, label: settings.Label}
	if fake {
		f.source = &canned{repositories: settings.Repositories, started: f.started}
	}
	f.endSurvivors() // before anything of this start can queue a run of an issue one of them is working
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
		read := f.refreshQueue(ctx)
		f.letIssuesGo(ctx, read.letGo)
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
func (f *Factory) refreshQueue(ctx context.Context) poll {
	read := f.source.queue(ctx, f.heldIssues())
	queue := read.issues
	sort.SliceStable(queue, func(a, b int) bool {
		if !queue[a].RoutedAt.Equal(queue[b].RoutedAt) {
			return queue[a].RoutedAt.Before(queue[b].RoutedAt) // work in the order the maintainer routed
		}
		return queue[a].key() < queue[b].key()
	})
	f.mu.Lock()
	f.queue, f.unreadable, f.polledAt = queue, read.unreadable, time.Now()
	f.mu.Unlock()
	return read
}

// heldPolls is how many polls apart an idle holding is asked about. Reading one costs a request for
// the issue and another for its pull request, and an issue whose run is over stays held until a
// person is done with that pull request — days of polling, for every pull request this host has
// waiting at once. Asking about all of them every minute would spend the host's whole hour of
// requests on issues nobody has touched, and the token that runs out is the one the workers use.
//
// A run that is going is the other case and is asked about on every poll: that is the reading a
// cancel arrives through, and there is one such run at a time. So the price of hearing a decision
// within a poll is paid for work in progress, and what is only waiting to be cleaned up is heard a
// few minutes later, which is as fast as a worktree needs to go.
const heldPolls = 10

// heldIssues is what the factory holds on GitHub right now, read from its own records: one entry per
// issue whose claim still stands, with the pull request any run of that issue opened. It is empty
// for a factory that holds nothing, which is what keeps a poll of a quiet line at the one request
// per repository it has always been.
//
// An issue is left out of the reading while it is not due: an idle holding is asked about every
// heldPolls-th poll and not on each one, and the wait starts over whenever the issue is in another
// state than it was last asked in — the run that held it ended, a pull request came of it — so the
// factory hears at once about work that has just changed hands and keeps its questions rare about
// work that lies as it did. A reading that failed counts as asked: GitHub said nothing either way,
// and asking a rate limit again every minute is what ran into it.
//
// It is empty for a paused factory too. A pause is the brake on everything this host does by itself
// ([ADR 0023]): it starts nothing, and it answers no decision about what it holds either, so there
// is nothing to ask GitHub about. What it holds it keeps until it is working again.
//
// Only a connected repository is asked about, for the reason its work stays out of the line: a
// repository the configuration no longer names is one this host is not to work, and nothing of it
// is touched.
//
// [ADR 0023]: ../docs/adr/0023-github-is-the-only-control-surface-of-the-factory.md
func (f *Factory) heldIssues() []Held {
	out := []Held{}
	if f.settings.Paused {
		return out
	}
	held := holdings(f.runs.list())
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	asked := make(map[string]time.Time, len(held)) // only what is held now, so the memory cannot grow
	for _, h := range held {
		connected, ok := f.connected(h.repository())
		if !h.holds || !ok {
			continue
		}
		issue := Held{Repository: connected.Name, Number: h.run.Issue, PullRequest: h.pullRequest}
		state := fmt.Sprintf("%s|%t|%s", issue.key(), h.idle, h.pullRequest)
		last, known := f.askedHeld[state]
		if h.idle && known && now.Sub(last) < time.Duration(heldPolls)*f.settings.Poll {
			asked[state] = last
			continue
		}
		asked[state] = now
		out = append(out, issue)
	}
	f.askedHeld = asked
	sort.Slice(out, func(a, b int) bool { return out[a].key() < out[b].key() })
	return out
}

// letIssuesGo answers what the poll read of the issues this factory holds. A decision that reaches a
// run which is still going cancels it: the worker's process group is ended and the run is recorded
// cancelled. A decision that reaches an issue whose runs are over lets the issue go, which is the
// one path that removes anything ([ADR 0026]) and the same path a cancelled run is let go by on the
// poll after it — so nothing is ever taken apart under a worker that is still writing in it.
//
// It runs in the working loop rather than beside it: letting an issue go pushes, fetches and removes
// a worktree of this host's clone, and one place doing that at a time is what keeps two of them off
// the same clone. The loop waits for it, which is why a handover is bound to the few polls of
// handoverTimeout and not to the hour a transfer has elsewhere. A paused factory does none of it —
// it writes nothing anywhere while it is paused, which is what a pause is for.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func (f *Factory) letIssuesGo(ctx context.Context, letGo map[string]string) {
	if len(letGo) == 0 || f.settings.Paused || ctx.Err() != nil {
		return
	}
	for key, held := range holdings(f.runs.list()) {
		decision, ends := letGo[key]
		if !ends || !held.holds {
			continue
		}
		if !held.idle {
			f.cancel(held.last, decision)
			continue
		}
		f.letGo(ctx, held, decision)
	}
}

// cancel ends the worker of a run that is still going. The cancel is taken out of the map as it is
// used, so a decision that stands on GitHub for as long as the worker takes to die signals it once;
// a run this factory is not working has none and nothing happens.
func (f *Factory) cancel(r Run, decision string) {
	f.mu.Lock()
	stop := f.cancelling[r.ID]
	delete(f.cancelling, r.ID)
	f.mu.Unlock()
	if stop == nil {
		return
	}
	log.Printf("run %d (%s#%d) is cancelled: %s", r.ID, r.Repository, r.Issue, decision)
	if record, ok := f.runs.find(r.ID); ok {
		f.runs.event(record, Event{Kind: "factory", Title: "cancelled on GitHub", Body: decision + "; the worker's process group is ended and the issue is let go"})
	}
	stop(cancelled{decision})
}

// warn puts a warning on a run under a title and says it once. A warning is the record of something
// a person has to look at, and the factory tries most of what it warns about again on every poll:
// one that polls every minute for a week would otherwise write the same sentence ten thousand times
// into one record.
func (f *Factory) warn(r *Run, title, warning string) {
	said := false
	f.runs.update(r, func() {
		for _, already := range r.Warnings {
			if already == warning {
				said = true
				return
			}
		}
		r.Warnings = append(r.Warnings, warning)
	})
	if said {
		return
	}
	log.Printf("error: %s", warning)
	f.runs.event(r, Event{Kind: "error", Title: title, Body: warning})
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
	for _, entry := range f.waiting() {
		if !f.claimable(entry.Repository) {
			continue // the issue keeps its place in the line; nothing of it is started or recorded
		}
		f.start(ctx, entry)
		return
	}
}

// claimable says whether a run of this repository could claim anything at all. A repository whose
// clone is missing — the host could not reach it when the factory connected, and connecting is done
// once per start — has no worktree to give a worker, so every run of it would fail before it touched
// the remote. A run is what takes an issue out of the line for good, so such an issue is left in the
// line instead of being spent on a claim that cannot work, and the operator reads why in the log.
func (f *Factory) claimable(repository string) bool {
	if f.fake { // fake mode claims nothing and clones nothing: its worker runs where the factory does
		return true
	}
	f.mu.Lock()
	held := f.held[repositoryKey(repository)]
	f.mu.Unlock()
	if held {
		return false
	}
	if _, err := os.Stat(filepath.Join(clonePath(f.settings.DataDir, repository), ".git")); err == nil {
		return true
	}
	f.hold(repository, "has no clone on this host; see the error of the clone above")
	return false
}

// hold takes a repository out of the claiming line for as long as this factory lives, and says why
// once. It is the answer to a failure that is the host's or GitHub's and not the issue's: the next
// issue of that repository would fail the same way, and because the next run starts the moment one
// ends, a line of twenty issues would be spent on it in seconds. The factory cannot tell a blip from
// a host that is simply broken and never retries by itself ([ADR 0026]), so what is held waits for
// the person who starts the factory again.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func (f *Factory) hold(repository, reason string) {
	f.mu.Lock()
	said := f.held[repositoryKey(repository)]
	f.held[repositoryKey(repository)] = true
	f.mu.Unlock()
	if !said {
		log.Printf("error: %s %s; its issues keep their place in the line and this factory claims none of them until it is started again", repository, reason)
	}
}

// waiting is the line as the factory would work it and as the interface serves it: first the work it
// already holds and resumes, ordered by the time of the signal that queued it, then the routed
// issues nobody has worked yet, in the order the maintainer routed them ([ADR 0025]).
//
// An issue with a run of its own is out of the routed part while a run of it stands: only the two
// resume signals put it back in the line, and only for an issue this factory holds. That is what
// leaves a foreign claim alone — a routed issue whose branch another claimer created is recorded as
// lost, holds nothing, and is never read as a release.
//
// An issue the factory has let go comes back into the routed part, and its entry carries the run
// that held it, so the claim takes that run's branch back rather than claiming the issue anew
// ([ADR 0026]). Only a routing newer than the moment it was let go does that: the label that was on
// the issue all along is the one the maintainer's decision was made under — closing a pull request
// would otherwise start the same work over by itself — and routing the issue again is the gesture
// that asks for another run. It is answered once, by the run it starts: a routing no newer than the
// signal the issue's latest run already stands for is one that has been acted on, whatever became of
// that run, which is what keeps an issue whose take-back was lost from being taken back on every
// poll for ever. That is the one comparison in the factory between GitHub's clock and this host's,
// and a host whose clock is minutes ahead answers a routing made inside that drift only after the
// label is set once more.
//
// Only a connected repository is in the line, held work included: a repository the configuration no
// longer names is one this host is not to work, whatever its records say it once held. Nothing of it
// is touched or deleted — the branch, the worktree and the assignee stay — and connecting it again
// puts what it holds back in the line.
//
// [ADR 0025]: ../docs/adr/0025-one-queue-one-worker-work-in-progress-first.md
func (f *Factory) waiting() []Entry {
	records := f.runs.list()
	worked := map[string]bool{}
	for _, run := range records {
		worked[run.key()] = true
	}
	f.mu.Lock()
	queue := f.queue
	f.mu.Unlock()
	routedNow := map[string]Issue{}
	for _, issue := range queue {
		routedNow[issue.key()] = issue
	}

	kept := holdings(records)
	out := []Entry{}
	for key, held := range kept {
		connected, ok := f.connected(held.repository())
		if !ok {
			continue
		}
		issue, routed := routedNow[key]
		if !routed {
			// An issue the factory holds is assigned to this host, so the line does not carry it and
			// its own record is what the entry is made of, under the name the configuration spells the
			// repository with now rather than the one of the day the record was written. In fake mode
			// the canned line carries every entry either way, which is how a resumed run there finds
			// its scripted worker again.
			issue = held.issue()
			issue.Repository = connected.Name
		}
		switch {
		case held.released(issue, routed):
			out = append(out, Entry{Issue: issue, Signal: signalRelease, SignalAt: issue.unassignedAt, resume: held.run})
		case held.resumes:
			out = append(out, Entry{Issue: issue, Signal: signalInterruption, SignalAt: held.signalAt(), resume: held.run})
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].SignalAt.Equal(out[b].SignalAt) {
			return out[a].SignalAt.Before(out[b].SignalAt)
		}
		return out[a].key() < out[b].key()
	})
	for _, issue := range queue {
		gone := kept[issue.key()]
		switch {
		case !worked[issue.key()]:
			out = append(out, Entry{Issue: issue, Signal: signalRouted, SignalAt: issue.RoutedAt})
		case gone.letGo && issue.RoutedAt.After(*gone.let.LetGoAt) && issue.RoutedAt.After(gone.last.SignalAt):
			out = append(out, Entry{Issue: issue, Signal: signalRouted, SignalAt: issue.RoutedAt, resume: gone.let})
		}
	}
	return out
}

// start records the run and works it in the background, so the loop keeps polling while it runs. A
// factory that is already stopping records nothing: the run would count as worked without ever
// having run.
func (f *Factory) start(ctx context.Context, entry Entry) {
	if ctx.Err() != nil {
		return
	}
	r := &Run{
		Repository: entry.Repository,
		Issue:      entry.Number,
		Title:      entry.Title,
		Signal:     entry.Signal,
		SignalAt:   entry.SignalAt,
		State:      "running",
		StartedAt:  time.Now(),
		Stages:     []string{},
		Warnings:   []string{},
		Versions:   Versions{Factory: version},
	}
	// A resumed run says from its first moment which branch and which worktree it continues, so a
	// host that loses power before the worker starts is read from this record alone. What it holds
	// is the claim's for an interruption and not yet its own for a release: the take-back that
	// answers a release is the first thing the run does, and until it has landed the issue lies
	// unassigned where the person who released it left it.
	if entry.Signal != signalRouted {
		r.Branch, r.Base, r.Worktree = entry.resume.Branch, entry.resume.Base, entry.resume.Worktree
		r.Holding = entry.Signal != signalRelease
	}
	f.runs.add(r)
	f.active.Add(1)
	go func() {
		defer f.active.Done()
		f.execute(ctx, r, entry)
		select { // the next entry starts now, not at the next poll
		case f.wake <- struct{}{}:
		default:
		}
	}()
}

// execute takes the issue on the remote and runs one worker session from start to end, reading its
// stream until the process is gone. The deadline covers the claim as well: a factory that is stopped
// while it claims leaves the issue rather than starting a worker nobody waits for.
func (f *Factory) execute(parent context.Context, r *Run, entry Entry) {
	deadline, done := context.WithTimeout(parent, f.settings.Deadline)
	defer done()
	// The run's own end, which a poll reaches for when GitHub says the maintainer is done with the
	// issue (letIssuesGo). It lies under the deadline and over the worker, so one cancel ends the
	// claim, the session and every process of its group, and the cause it carries is what the record
	// says the run ended of.
	ctx, stop := context.WithCancelCause(deadline)
	defer stop(nil)
	f.mu.Lock()
	f.cancelling[r.ID] = stop
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.cancelling, r.ID)
		f.mu.Unlock()
	}()

	claim, err := f.take(ctx, r, entry)
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
			f.finish(r, outcomeInterrupted, "the factory stopped while this run was "+taking(claim)+leftBehind(claim), nil)
			return
		}
		if stopped, was := cancelledBy(ctx); was {
			// The same for a cancel, and the repository is held for nothing either: the issue is one
			// the maintainer took back, not a host or a GitHub that everything of this repository
			// would fail on.
			f.finish(r, outcomeCancelled, stopped.Error()+" while this run was "+taking(claim)+leftBehind(claim), nil)
			return
		}
		if !claim.created {
			// Nothing of this issue was touched, so the reason lies with this host or with GitHub and
			// the issue behind it would meet the same one. The repository is held rather than worked
			// through; this run stays as the record a person reads.
			f.hold(entry.Repository, "could not be claimed from: "+err.Error())
		}
		f.finish(r, outcomeFailed, "the issue could not be "+taken(claim)+": "+err.Error()+leftBehind(claim), nil)
		return
	}
	f.runs.update(r, func() {
		// What the claim or the resume ended up holding, before a worker is started on it.
		r.Worktree, r.Holding = claim.worktree, claim.holding
		// The session starts in /worker:work, which invokes no skill for its first stage.
		r.stage("implement")
	})

	cmd, err := f.worker(ctx, entry.Issue, claim)
	if err != nil {
		f.abandon(ctx, r, claim, "no worker could be started: "+err.Error())
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
		f.abandon(ctx, r, claim, "the factory could not open a pipe for the worker: "+err.Error())
		return
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		stdout.Close()
		stdoutWriter.Close()
		f.abandon(ctx, r, claim, "the factory could not open a pipe for the worker: "+err.Error())
		return
	}
	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter
	// The run's lock goes into the worker's process group and nowhere else: the factory opens it,
	// hands it over and lets go of it, so what holds it from here is that group alone and the kernel
	// gives it back when the last process of it is gone. A factory the host killed ends nothing, and
	// this is what the next start reads to find the worker that outlived it (endSurvivors).
	lock, err := f.runs.lock(r.ID)
	if err != nil {
		stdout.Close()
		stdoutWriter.Close()
		stderr.Close()
		stderrWriter.Close()
		f.abandon(ctx, r, claim, "the factory could not take the lock of this run: "+err.Error())
		return
	}
	cmd.ExtraFiles = []*os.File{lock}
	err = cmd.Start()
	lock.Close() // the worker's group holds it now, and a worker that never started holds nothing
	if err != nil {
		stdout.Close()
		stdoutWriter.Close()
		stderr.Close()
		stderrWriter.Close()
		f.abandon(ctx, r, claim, "the worker could not be started: "+err.Error())
		return
	}
	stdoutWriter.Close() // the worker holds the only writing ends now
	stderrWriter.Close()
	// The process group of the session, recorded before a line of its output is read: it is what ends
	// the session, and a factory that is gone cannot say it afterwards.
	f.runs.update(r, func() { r.WorkerGroup = cmd.Process.Pid })
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
		f.warn(r, "the worker left a process behind",
			"the worker left a process behind that is outside its process group and still held its output; the factory cannot end it, look for it on the host")
	}
	stdout.Close()
	stderr.Close()

	exitCode := cmd.ProcessState.ExitCode()
	// A worker that ended by itself and reported is read by its report: a stop or a deadline that
	// arrives in the same moment ended nothing, and its pull request would be lost to the record.
	reported := waitErr == nil && r.reportOutcome != ""
	stopped, was := cancelledBy(ctx)
	switch {
	case !reported && parent.Err() != nil:
		f.finish(r, outcomeInterrupted, "the factory stopped while this run was active; its worker was ended", &exitCode)
	case !reported && was:
		f.finish(r, outcomeCancelled, stopped.Error()+"; the worker's process group was ended and the issue is let go", &exitCode)
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

// abandon ends a run whose worker never started. A cancel that arrives in that moment — the claim
// stands, the session is a few lines away — ends the run the way every other cancel does, and only
// what is really this host's trouble is recorded as a failure of it.
func (f *Factory) abandon(ctx context.Context, r *Run, claim claimed, reason string) {
	if stopped, was := cancelledBy(ctx); was {
		f.finish(r, outcomeCancelled, stopped.Error()+"; no worker had been started"+leftBehind(claim), nil)
		return
	}
	f.finish(r, outcomeFailed, reason+leftBehind(claim), nil)
}

// cancelled is the cause the context of a cancelled run carries: the decision GitHub was read to
// have taken, so the record names the gesture the run ended on rather than "context canceled".
type cancelled struct{ decision string }

func (c cancelled) Error() string { return "the maintainer ended this run on GitHub: " + c.decision }

// cancelledBy is the decision a run was cancelled with, and whether it was cancelled at all. A
// deadline and a stopped factory carry causes of their own and are not this.
func cancelledBy(ctx context.Context) (cancelled, bool) {
	var stopped cancelled
	return stopped, errors.As(context.Cause(ctx), &stopped)
}

// leftBehind says what a run that did not finish left on the remote, which is what the operator
// needs to decide: a claim that never got to create the branch took nothing, one that did holds the
// issue by it until somebody removes it, and a resumed run leaves the claim it was under exactly as
// it found it — nothing here deletes work ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func leftBehind(claim claimed) string {
	switch {
	case claim.resumed:
		return "; the branch " + claim.branch + " and its worktree stay as they are"
	case !claim.created:
		return "; nothing was claimed on the remote"
	default:
		return "; the branch " + claim.branch + " was created on the remote and is left behind: remove it to work the issue again"
	}
}

// taking and taken name what a run was doing when it failed: claiming the issue, or resuming work
// this factory already holds.
func taking(claim claimed) string {
	if claim.resumed {
		return "resuming the issue"
	}
	return "claiming the issue"
}

func taken(claim claimed) string {
	if claim.resumed {
		return "resumed"
	}
	return "claimed"
}

// take prepares the run: it claims the issue on the remote and records the branch that claim is, or,
// for a resumed run, continues under the claim that already holds it. Fake mode claims nothing: its
// queue is canned and there is no remote behind it, so its scripted worker runs where the factory
// itself does, and it says it holds the issue all the same, because its runs stand in for held ones.
func (f *Factory) take(ctx context.Context, r *Run, entry Entry) (claimed, error) {
	if entry.Signal != signalRouted {
		return f.resume(ctx, r, entry)
	}
	if f.fake {
		return claimed{holding: true}, nil
	}
	claim, err := f.claim(ctx, r, entry)
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
// [ADR 0023]: ../docs/adr/0023-github-is-the-only-control-surface-of-the-factory.md
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
