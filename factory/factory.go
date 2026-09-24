package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	// pull is the pull request a follow-up run answers the review on: the one the claim opened, which
	// the review was read from.
	pull string
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

	// paused is the one setting taken over while the factory runs: the configuration file is read
	// again on every poll for it (followPause), so a pause needs no restart, and the restart's SIGTERM
	// no longer interrupts the run that is going. brake is -paused, which no configuration undoes, and
	// unread the error the last reading of the file failed with, so the log says it once.
	paused atomic.Bool
	config string
	brake  bool
	unread string

	mu    sync.Mutex
	queue []Issue
	// requested is what the last poll found on the pull requests this factory holds open: the newest
	// review that asks for changes, by issue. Like the queue it is a reading of GitHub and never a
	// state of the factory; what has been answered is read from the run records ([ADR 0025]).
	//
	// [ADR 0025]: ../docs/adr/0025-one-queue-one-worker-work-in-progress-first.md
	requested map[string]time.Time
	// requestedEarly says that requested was read while a run was going: a run that ends between
	// that reading and the start of the next one has not been asked about yet.
	requestedEarly bool
	unreadable     map[string]string
	polledAt       time.Time
	connecting     bool
	user           string          // the login this host's gh is logged in as, read once and kept
	held           map[string]bool // repositories this factory claims nothing from, so the log says it once
	// cancelling is how a poll ends a run that is still going: the cancel of the context that run
	// works under, by run, put there when the run starts and taken out when it ends or is cancelled.
	// Only a run of this factory is in it, so a record of an older start can never be signalled here.
	cancelling map[int]context.CancelCauseFunc
	// askedHeld is when each issue this factory holds was last asked about on GitHub, by the state it
	// was in when it was asked (heldIssuesDue). It is the memory the cadence of those readings rests on
	// and nothing else reads it.
	askedHeld map[string]time.Time
	// quotaUntil is the reset the factory waits for when the quota check found too little left, and
	// empty while it does not wait (quota.go).
	quotaUntil *time.Time
	// delivered is the runs whose ending this process has taken on to notify (deliver), so an unpause
	// and the run that ends in the same moment cannot both make the call.
	delivered map[int]bool
}

// source is where the line comes from on every poll: GitHub, or the canned queue of fake mode. It
// answers with what it could read and reports what it could not, so nothing of it is ever stored.
// The issues this factory holds are asked about in the same reading, because they are not in the
// line — an issue the factory holds is assigned to this host, which is what takes it out of it.
type source interface {
	queue(ctx context.Context, held []Held) poll
	// changesRequested is the newest review that asks for changes on one pull request this factory
	// opened, submitted by somebody who may write to the repository, and the zero time when there is
	// none, when the pull request is no longer open and when GitHub could not be read.
	changesRequested(ctx context.Context, repository string, pull int) time.Time
	// pullState is one reading of the pull request a run waits on in the ci stage (ci.go), with the
	// reviews of the bots named; failedLogs the failed logs of the checks a fix session is given; and
	// openPull the open pull request of a held branch, which a resumed run starts its ci stage on.
	pullState(ctx context.Context, held Held, bots []string) (pullReading, error)
	failedLogs(ctx context.Context, repository string, failed []check) string
	openPull(ctx context.Context, repository, branch string) (string, error)
	// issueText is the title and the body of an issue, and createPull opens a pull request and answers
	// with its URL: the two calls of the pr stage (pr.go).
	issueText(ctx context.Context, repository string, number int) (string, string, error)
	createPull(ctx context.Context, repository string, p newPull) (string, error)
	// replyToThread, resolveThread and commentOnPull carry what an address-reviews session answered to
	// GitHub: a reply in one review thread, its resolution, and one comment on the pull request.
	replyToThread(ctx context.Context, id, body string) error
	resolveThread(ctx context.Context, id string) error
	commentOnPull(ctx context.Context, repository string, pull int, body string) error
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
	Branch      string // the branch the claim of this issue holds it by
	PullRequest string // the pull request a run of this issue opened, or empty
	// Running says a worker of this issue is at work. Such an issue is read before the rest: what
	// the reading carries for it is a cancel, which must reach the worker while it is still working,
	// and the issues that only wait to be cleaned up are of no hurry beside it.
	Running bool
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
		askedHeld: map[string]time.Time{}, delivered: map[int]bool{}}
	f.paused.Store(settings.Paused)
	f.source = newGitHub(settings.Repositories, settings.Label)
	if fake {
		f.source = &canned{repositories: settings.Repositories, started: f.started, runs: runs}
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
		f.followPause(ctx)
		read := f.refreshQueue(ctx)
		f.letIssuesGo(ctx, read.letGo)
		f.refreshRequested(ctx)
		f.dispatch(ctx)
		// A factory that waits for quota checks again once the reset has passed, not at the first poll
		// after it.
		next := f.settings.Poll
		if until, ahead := f.waitingForQuota(time.Now()); ahead && time.Until(until) < next {
			next = time.Until(until)
		}
		select {
		case <-ctx.Done():
		case <-f.wake:
		case <-time.After(next):
		}
	}
	f.active.Wait()
}

// Follow has the factory read paused from its configuration file again on every poll, with -paused
// as the brake that holds whatever the file says. Without it the pause is what the factory started
// with.
func (f *Factory) Follow(config string, brake bool) {
	f.config, f.brake = config, brake
}

// Paused says whether the factory is paused now. A pause holds everything this host does by itself:
// nothing is claimed, resumed, followed up or let go, and a run that is going finishes.
func (f *Factory) Paused() bool { return f.paused.Load() }

// followPause reads the configuration file again and takes paused from it; every other setting stays
// the one the factory started with and needs a restart (docs/factory-runbook.md). A file that cannot
// be read or is refused changes nothing: the factory goes on as it is and says so once, until the file
// reads again. Only the working loop calls it, so config and unread are its own.
func (f *Factory) followPause(ctx context.Context) {
	if f.config == "" {
		return
	}
	read, err := Load(f.config)
	if err != nil {
		if said := err.Error(); said != f.unread {
			f.unread = said
			log.Printf("error: %v; the factory goes on with the settings it runs with (paused=%v) and reads the file again on the next poll", err, f.Paused())
		}
		return
	}
	f.unread = ""
	paused := read.Paused || f.brake
	if f.paused.Swap(paused) == paused {
		return
	}
	if paused {
		log.Printf("paused by the configuration: a run that is going finishes, and nothing else is started or let go")
		return
	}
	log.Printf("working again: the configuration no longer pauses the factory")
	f.deliverOwed(ctx)
}

// refreshQueue derives the line of routed issues of all connected repositories. It is asked from the
// source on every poll and only held in memory: the queue is a view of GitHub, never a state of the
// factory ([ADR 0025]).
//
// [ADR 0025]: ../docs/adr/0025-one-queue-one-worker-work-in-progress-first.md
func (f *Factory) refreshQueue(ctx context.Context) poll {
	read := f.source.queue(ctx, f.heldIssuesDue())
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

// heldPolls is how many poll intervals apart an idle holding is asked about. Reading one costs a
// request for the issue and another for its pull request, and an issue whose run is over stays held until a
// person is done with that pull request — days of polling, for every pull request this host has
// waiting at once. Asking about all of them every minute would spend the host's whole hour of
// requests on issues nobody has touched, and the token that runs out is the one the workers use.
//
// A run that is going is the other case and is asked about on every poll: that is the reading a
// cancel arrives through, and there is one such run at a time. So the price of hearing a decision
// within a poll is paid for work in progress, and what is only waiting to be cleaned up is heard a
// few minutes later, which is as fast as a worktree needs to go.
const heldPolls = 10

// heldIssuesDue is what the factory holds on GitHub and is due to ask about on this poll, read from
// its own records: one entry per issue whose claim still stands, with the pull request the claim
// opened. It is empty for a factory that holds nothing, which is what keeps a poll of a quiet line
// at the one request per repository it has always been.
//
// It is a reading and a step of the poll in one: what it answers with it also marks as asked, which
// is what the cadence below counts. One poll calls it once.
//
// An issue is left out of the reading while it is not due: an idle holding is asked about once
// every heldPolls poll intervals, not on each poll, and the wait starts over whenever the issue is
// in another state than it was last asked in — the run that held it ended, a pull request came of
// it — so the factory hears at once about work that has just changed hands and keeps its questions
// rare about work that lies as it did. A reading that failed counts as asked: GitHub said nothing either way,
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
func (f *Factory) heldIssuesDue() []Held {
	out := []Held{}
	if f.Paused() {
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
		issue := Held{Repository: connected.Name, Number: h.run.Issue, Branch: h.run.Branch,
			PullRequest: h.pullRequest, Running: !h.idle}
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
	// The run that is going first, because the reading of what this factory holds is bounded as a
	// whole (readHeld) and a GitHub that answers slowly may leave the end of it unread: a cancel is
	// what must not wait for that, and a worktree that waits to be cleaned up can.
	sort.Slice(out, func(a, b int) bool {
		if out[a].Running != out[b].Running {
			return out[a].Running
		}
		return out[a].key() < out[b].key()
	})
	return out
}

// letIssuesGo answers what the poll read of the issues this factory holds. A decision that reaches a
// run which is still going cancels it: the worker's process group is ended and the run is recorded
// cancelled. A decision that reaches an issue whose runs are over lets the issue go, which is the
// one path that removes anything ([ADR 0026]) and the same path a cancelled run is let go by on the
// poll after it — so nothing is ever taken apart under a worker that is still writing in it.
//
// The cancels come first and cost nothing: a signal to a process group this host already has. The
// handovers come after them and run in the working loop rather than beside it, because letting an
// issue go pushes, fetches and removes a worktree of this host's clone, and one place doing that at
// a time is what keeps two of them off the same clone. The loop waits for them, so all of them
// together are bound to the few polls of handoverTimeout rather than each to itself: whatever many
// decisions arrive in one poll, the next poll is that deadline away and not a multiple of it. A
// handover the deadline cuts, and every one behind it, is left for a later poll to make again from
// the state of the host — which is what a handover reads at every step anyway.
//
// A paused factory does none of it — it writes nothing anywhere while it is paused, which is what a
// pause is for.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func (f *Factory) letIssuesGo(ctx context.Context, letGo map[string]string) {
	if len(letGo) == 0 || f.Paused() || ctx.Err() != nil {
		return
	}
	held := holdings(f.runs.list())
	handovers := []string{}
	for key, h := range held {
		decision, ends := letGo[key]
		if !ends || !h.holds {
			continue
		}
		if !h.idle {
			f.cancel(h.last, decision)
			continue
		}
		handovers = append(handovers, key)
	}
	sort.Strings(handovers) // so a poll that cannot finish them all works through them in one order
	pass, done := context.WithTimeoutCause(ctx, handoverTimeout, errHandoverCut)
	defer done()
	for _, key := range handovers {
		if pass.Err() != nil {
			return // the deadline is spent; what is left is still held and is let go from a later poll
		}
		f.letGo(pass, held[key], letGo[key])
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

// warn puts a warning on a run under a title and says it once: something that went not quite right,
// on the record the interface serves and in the log, and never the end of the run. The title is the
// line the log shows before it is opened, so it is short and the sentence stays in the body.
//
// It is said once because the factory tries most of what it warns about again on every poll: one
// that polls every minute for a week would otherwise write the same sentence ten thousand times
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
// starts, while the factory is paused nothing starts at all, and while it waits for the quota to
// reset nothing starts either. The quota is checked when there is a run to start and not otherwise:
// an idle line asks nothing of the provider.
func (f *Factory) dispatch(ctx context.Context) {
	if f.Paused() || ctx.Err() != nil {
		return
	}
	for _, r := range f.runs.list() {
		if r.EndedAt == nil {
			return
		}
	}
	// The run that was going when this poll read the reviews has ended since, and a review of its pull
	// request would stand behind whatever the line starts next if it were not read now.
	f.mu.Lock()
	early := f.requestedEarly
	f.mu.Unlock()
	if early {
		f.refreshRequested(ctx)
	}
	if _, waiting := f.waitingForQuota(time.Now()); waiting {
		return
	}
	for _, entry := range f.waiting() {
		if !f.claimable(entry.Repository) {
			continue // the issue keeps its place in the line; nothing of it is started or recorded
		}
		allowed, warning := f.quotaAllows(ctx, entry.Repository)
		if !allowed {
			return // the entry keeps its place; the check runs again after the reset
		}
		f.start(ctx, entry, warning)
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
// already holds — resumed and follow-up runs — ordered by the time of the signal that queued it, then
// the routed issues nobody has worked yet, in the order the maintainer routed them ([ADR 0025]).
//
// An issue with a run of its own is out of the routed part while a run of it stands: only the
// signals of held work put it back in the line, and only for an issue this factory holds. That is
// what leaves a foreign claim alone — a routed issue whose branch another claimer created is
// recorded as lost, holds nothing, and is never read as a release or as a pull request to watch.
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
// A lost claim lets nothing go, because it held nothing, and it stands in the way of the routing it
// answered and of no later one: the issue whose latest run ended lost comes back into the routed
// part when the label is set again after that run's signal, and the claim then finds the branch
// name free, or finds this factory's own branch (orphaned), or loses once more. The label that was
// on the issue when the claim was lost is answered by that run, so a foreign claim is still met
// once per routing and never on every poll.
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
	queue, requested := f.queue, f.requested
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
		case held.resumes != "":
			out = append(out, Entry{Issue: issue, Signal: held.resumes, SignalAt: held.signalAt(), resume: held.run})
		case held.changesRequested(requested[key]):
			out = append(out, Entry{Issue: issue, Signal: signalChangesRequested, SignalAt: requested[key], resume: held.run, pull: held.pullRequest})
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
		case !gone.holds && gone.idle && gone.last.Outcome == outcomeLost && issue.RoutedAt.After(gone.last.SignalAt):
			out = append(out, Entry{Issue: issue, Signal: signalRouted, SignalAt: issue.RoutedAt})
		}
	}
	return out
}

// start records the run and works it in the background, so the loop keeps polling while it runs. A
// factory that is already stopping records nothing: the run would count as worked without ever
// having run. A warning is what the run starts with, such as a quota check that could not answer.
func (f *Factory) start(ctx context.Context, entry Entry, warning string) {
	if ctx.Err() != nil {
		return
	}
	r := &Run{
		Repository: entry.Repository,
		Issue:      entry.Number,
		Title:      entry.Title,
		Signal:     entry.Signal,
		SignalAt:   entry.SignalAt,
		Kind:       kindOf(entry.Signal),
		State:      "running",
		StartedAt:  time.Now(),
		Stages:     []string{},
		Warnings:   []string{},
		Versions:   Versions{Factory: version},
	}
	// A run that continues held work says from its first moment which branch and which worktree it
	// continues, so a host that loses power before the worker starts is read from this record alone.
	// What it holds is the claim's for an interruption and for a review, and not yet its own for a
	// release: the take-back that answers a release is the first thing that run does, and until it
	// has landed the issue lies unassigned where the person who released it left it.
	if entry.Signal != signalRouted {
		r.Branch, r.Base, r.Worktree = entry.resume.Branch, entry.resume.Base, entry.resume.Worktree
		r.Holding = entry.Signal != signalRelease
	}
	f.runs.add(r)
	if warning != "" {
		f.warn(r, "quota not checked", warning)
	}
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
	defer f.runs.update(r, r.release)
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
			// the issue again until it is routed again (waiting).
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
	// The plugin the session is about to run is brought up to date and written down, here and not
	// earlier: the issue is this factory's now, and nothing else of it is running. What the claim
	// holds is on the record already, written where it became true rather than here, so a run this
	// update is cut off in is still the held work the next start resumes.
	f.prepare(ctx, r)
	if ctx.Err() != nil {
		// Updating the plugins reaches over the host's line and takes as long as that line does, so a
		// stop or the deadline lands in it far more often than in the microseconds the claim used to be
		// followed by. No worker was started here, and the record must not name one that failed.
		if parent.Err() != nil {
			f.finish(r, outcomeInterrupted, "the factory stopped while this run was preparing its worker"+leftBehind(claim), nil)
			return
		}
		f.finish(r, outcomeTimeout, fmt.Sprintf("the deadline of %s passed while this run was preparing its worker", f.settings.Deadline)+leftBehind(claim), nil)
		return
	}

	// What the claim or the resume ended up holding, before a worker is started on it.
	f.runs.update(r, func() { r.Worktree, r.Holding = claim.worktree, claim.holding })

	// A resumed run whose branch has a pull request open is past the stages that open one: it starts at
	// the ci stage, with the repair rounds its pull request has had, and nothing before it is done again.
	pull, known := f.openedAlready(ctx, r, entry, claim)
	if pull != "" {
		if pullOf(entry.resume.PullRequest) == pullOf(pull) {
			f.runs.update(r, func() { r.RepairRounds = entry.resume.RepairRounds })
		}
		f.ci(parent, ctx, r, entry, claim, pull, false)
		return
	}
	// A follow-up run answers a review on the pull request the claim opened: it starts at the
	// address-reviews stage, with a repair count of its own that starts at none, and goes on into the
	// ci stage. The URL is rebuilt from the repository and the number, as a session's is.
	if entry.Signal == signalChangesRequested {
		pull, reason := pullRequest(entry.pull, r.Repository)
		if pull == "" {
			f.finish(r, outcomeFailed, "the follow-up run has no pull request to answer the review on: "+reason, nil)
			return
		}
		f.ci(parent, ctx, r, entry, claim, pull, true)
		return
	}

	// A resumed run without a pull request whose branch is where the run before it recorded the review
	// starts at the pr stage.
	if known {
		if review, ok := f.reviewedAlready(ctx, r, entry, claim); ok {
			f.pr(parent, ctx, r, entry, claim, review)
			return
		}
		// One whose run before recorded rounds of the review, on a branch that still carries them, goes
		// on with the review from the last round it recorded.
		if panel, ok := f.reviewingAlready(ctx, r, entry, claim); ok {
			f.review(parent, ctx, r, entry, claim, panel)
			return
		}
	}

	s := workSession.overridden()
	// The session opens in the skill it is given as its prompt, and that prompt is a slash command and
	// no Skill call, so nothing in the worker's stream announces it.
	f.runs.update(r, func() { r.stage(s.stage) })
	got, ok := f.session(parent, ctx, r, s, entry, claim)
	if !ok {
		return
	}
	if got.Outcome == resultBlocked {
		f.runs.update(r, func() { r.Reason = got.Summary })
		f.finish(r, outcomeBlocked, "", nil)
		return
	}
	if s.stopAfter == "" {
		// A session that ran the pipeline to its end has waited on CI itself: the run is ready, and the
		// reason says what it lacks when it names no pull request.
		url, reason := pullRequest(got.PullRequest, r.Repository)
		f.runs.update(r, func() { r.PullRequest = url })
		f.finish(r, outcomeReady, reason, nil)
		return
	}
	panel, err := f.gatedOf(ctx, claim, got)
	if err != nil {
		if !f.halted(parent, ctx, r, "read the gated commit") {
			f.finish(r, outcomeFailed, "the commit the gate ran on could not be read: "+err.Error()+leftBehind(claim), nil)
		}
		return
	}
	f.review(parent, ctx, r, entry, claim, panel)
}

// openedAlready is the open pull request of the branch a resumed run continues, which is where it
// starts, and whether GitHub said so. A first run has opened none, and a follow-up run answers a review
// on its own; a reading that fails starts the run at its first stage, whose session finds the pull
// request itself.
func (f *Factory) openedAlready(ctx context.Context, r *Run, entry Entry, claim claimed) (string, bool) {
	if kindOf(entry.Signal) != kindResumed || claim.branch == "" {
		return "", false
	}
	pull, err := f.source.openPull(ctx, entry.Repository, claim.branch)
	if err != nil {
		if ctx.Err() == nil {
			f.warn(r, "pull request not read", "whether "+claim.branch+" has a pull request open could not be read, so the run starts at its first stage: "+err.Error())
		}
		return "", false
	}
	if pull != "" {
		f.runs.event(r, Event{Kind: "factory", Title: "resuming at the ci stage of " + pull,
			Body: "the branch " + claim.branch + " has this pull request open, so the stages that open it are done"})
	}
	return pull, true
}

// session starts one session and reads it to its end. It answers with the session's result when the
// session ended by itself with a result that fits, and otherwise ends the run the way the session
// ended and answers false. A session that commits and reports complete with changes it did not commit
// fails the run: the factory goes on by the commit the branch is at, so its report would name work
// the branch does not carry, and the next reviewer or gate would read files no push takes along.
func (f *Factory) session(parent, ctx context.Context, r *Run, s session, entry Entry, claim claimed) (result, bool) {
	got, ended := f.runSession(parent, ctx, r, s, entry, claim, "")
	if ended != nil {
		f.end(parent, r, *ended)
		return result{}, false
	}
	if !s.commits || got.Outcome != resultComplete {
		return got, true
	}
	left, err := f.uncommitted(ctx, claim)
	if err != nil {
		if !f.halted(parent, ctx, r, "read the worktree") {
			f.finish(r, outcomeFailed, "the worktree could not be read after the session of the stage "+s.stage+": "+err.Error()+leftBehind(claim), nil)
		}
		return result{}, false
	}
	if len(left) > 0 {
		f.finish(r, outcomeFailed, fmt.Sprintf("the session of the stage %s reported complete and left changes it did not commit, which the branch does not carry; "+
			"they stay in the worktree %s:\n%s", s.stage, claim.worktree, fenced(listed(left, maxListed, "git status")))+leftBehind(claim), nil)
		return result{}, false
	}
	return got, true
}

// uncommitted is the paths of the worktree that differ from its commit, staged or not, and the files
// git does not track and does not ignore. Fake mode has no worktree.
func (f *Factory) uncommitted(ctx context.Context, claim claimed) ([]string, error) {
	if f.fake {
		return nil, nil
	}
	out, err := git(ctx, claim.worktree, "status", "--porcelain")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// ending is how a session ended when it left no result to go on with: the outcome and the reason the
// run ends with, and whether the reason is an error of the session, which may be the quota's
// (endInError). A session that runs beside others answers with it rather than ending the run, so the
// stage decides once which of them ends it.
type ending struct {
	outcome, reason string
	exitCode        *int
	inError         bool
}

// end ends a run the way one of its sessions ended.
func (f *Factory) end(parent context.Context, r *Run, e ending) {
	if e.inError {
		f.endInError(parent, r, e.reason, e.exitCode)
		return
	}
	f.finish(r, e.outcome, e.reason, e.exitCode)
}

// runSession starts one session and reads it to its end, and answers with its result, or with how it
// ended when it left none that fits. label names the session in the run's log when it runs beside
// others, and is empty for one that runs alone.
func (f *Factory) runSession(parent, ctx context.Context, r *Run, s session, entry Entry, claim claimed, label string) (result, *ending) {
	said := &heard{label: label, read: s.read}
	// A session that runs beside others says when its process is up, or that it never will be.
	began := sync.OnceFunc(func() {
		if s.began != nil {
			s.began()
		}
	})
	defer began()
	f.runs.update(r, r.opened)
	defer f.runs.update(r, func() { r.closed(said) })
	// The session's context is derived from the run's, so the run's deadline and the session's own
	// timeout both end it: whichever passes first, and the cause the context carries says which one did.
	sessionCtx, endSession := context.WithTimeoutCause(ctx, s.timeout, overran{s})
	defer endSession()
	cmd, err := f.command(sessionCtx, s, entry, claim)
	if err != nil {
		return result{}, abandoned(ctx, claim, "no worker could be started: "+err.Error())
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
		return result{}, abandoned(ctx, claim, "the factory could not open a pipe for the worker: "+err.Error())
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		stdout.Close()
		stdoutWriter.Close()
		return result{}, abandoned(ctx, claim, "the factory could not open a pipe for the worker: "+err.Error())
	}
	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter
	// The run's lock goes into the process group of every session of the run: the factory takes it
	// before the first one and keeps it until the run ends (release), so a process an earlier session
	// left behind cannot keep the next one from starting, and the kernel gives it back when the factory
	// and the last process of those groups are gone. A factory the host killed ends nothing and holds
	// nothing any more, and this is what the next start reads to find the worker that outlived it
	// (endSurvivors).
	var lockErr error
	f.runs.update(r, func() {
		if r.lock == nil {
			r.lock, lockErr = f.runs.lock(r.ID)
		}
	})
	if lockErr != nil {
		stdout.Close()
		stdoutWriter.Close()
		stderr.Close()
		stderrWriter.Close()
		return result{}, abandoned(ctx, claim, "the factory could not take the lock of this run: "+lockErr.Error())
	}
	cmd.ExtraFiles = []*os.File{r.lock}
	err = cmd.Start()
	if err != nil {
		stdout.Close()
		stdoutWriter.Close()
		stderr.Close()
		stderrWriter.Close()
		return result{}, abandoned(ctx, claim, "the worker could not be started: "+err.Error())
	}
	stdoutWriter.Close() // the worker holds the only writing ends now
	stderrWriter.Close()
	// The process group of the session, recorded before a line of its output is read: it is what ends
	// the session, and a factory that is gone cannot say it afterwards. The sessions that run beside
	// each other are in Groups as long as they run.
	pid := cmd.Process.Pid
	f.runs.update(r, func() { r.WorkerGroup, r.Groups = pid, append(r.Groups, pid) })
	defer f.runs.update(r, func() { r.ungrouped(pid) })
	started := Event{Kind: "factory", Title: "worker started", Body: fmt.Sprint(cmd.Args)}
	if label != "" {
		started.Title = label + ": " + started.Title
	}
	f.runs.event(r, started)
	began()

	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		f.read(r, said, "the worker's output", stdout, func(line []byte) { f.ingest(r, said, line) })
	}()
	go func() {
		defer readers.Done()
		// Every line of the error output counts as the cause of a failure, the last one winning. A
		// real session also writes what is only noise there; the ticket that starts one decides what
		// of it is a cause.
		f.read(r, said, "the worker's error output", stderr, func(line []byte) {
			title := "stderr: " + firstLine(string(line))
			if label != "" {
				title = label + ": " + title
			}
			f.error(r, said, Event{Kind: "error", Title: title, Body: string(line)})
		})
	}()

	waitErr := cmd.Wait()
	// Nothing the worker started survives its run, whether it ended by itself, in an error or on the
	// deadline. The group is ended right after the worker was reaped, which also closes the pipes a
	// process it left behind would still hold open.
	_ = endGroup(pid, syscall.SIGKILL)
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
	var got *result
	var misfit, lastError, resultSummary string
	f.runs.update(r, func() {
		r.ExitCode = &exitCode
		got, misfit, lastError, resultSummary = said.result, said.misfit, said.lastError, said.resultSummary
	})
	// The process and the result are read apart. A session that ended by itself with a result that
	// fits is read by that result: a stop or a deadline that arrives in the same moment ended nothing,
	// and its pull request would be lost to the record. A process that failed is a failed run whatever
	// its result said, and a session that ended well without a result that fits is one as well.
	fitted := waitErr == nil && got != nil
	stopped, was := cancelledBy(ctx)
	var over overran
	switch {
	case !fitted && parent.Err() != nil:
		return result{}, &ending{outcome: outcomeInterrupted, reason: "the factory stopped while this run was active; its worker was ended", exitCode: &exitCode}
	case !fitted && was:
		return result{}, &ending{outcome: outcomeCancelled, reason: stopped.Error() + "; the worker's process group was ended and the issue is let go", exitCode: &exitCode}
	case !fitted && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return result{}, &ending{outcome: outcomeTimeout, reason: fmt.Sprintf("the deadline of %s passed; the worker's process group was ended", f.settings.Deadline), exitCode: &exitCode}
	case !fitted && errors.As(context.Cause(sessionCtx), &over):
		return result{}, &ending{outcome: outcomeFailed, reason: over.Error() + "; the worker's process group was ended", exitCode: &exitCode}
	case waitErr != nil || resultSummary != "":
		// A result line that says the session ended in an error is one whatever the process exited with.
		cause := lastError
		if cause == "" {
			cause = resultSummary
		}
		return result{}, &ending{outcome: outcomeFailed, inError: true, exitCode: &exitCode,
			reason: strings.TrimSpace(fmt.Sprintf("the session ended in an error (exit %d): %s", exitCode, cause))}
	case got != nil:
		return *got, nil
	case misfit != "":
		return result{}, &ending{outcome: outcomeFailed, reason: "the session's result does not fit the schema: " + misfit, exitCode: &exitCode}
	}
	return result{}, &ending{outcome: outcomeFailed, inError: true, exitCode: &exitCode,
		reason: "the session ended without a result line; a session ends by printing its structured result"}
}

// abandoned is how a run whose worker never started ends. A cancel that arrives in that moment — the
// claim stands, the session is a few lines away — ends the run the way every other cancel does, and
// only what is really this host's trouble is recorded as a failure of it.
func abandoned(ctx context.Context, claim claimed, reason string) *ending {
	if stopped, was := cancelledBy(ctx); was {
		return &ending{outcome: outcomeCancelled, reason: stopped.Error() + "; no worker had been started" + leftBehind(claim)}
	}
	return &ending{outcome: outcomeFailed, reason: reason + leftBehind(claim)}
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

// endInError ends a run whose session ended in an error. When the quota the worker spends is used up
// by then, the error is the quota's and not the issue's: the outcome is quota, everything the run
// holds stays as it is, and the factory resumes the issue by itself after the reset, without spending
// the one automatic resume an interruption has ([ADR 0026]) — once in a row, so a quota resume that
// runs out again leaves the issue to a person. Otherwise, and when the check cannot answer, the run
// has failed.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func (f *Factory) endInError(ctx context.Context, r *Run, reason string, exitCode *int) {
	exhausted, scope, until, err := f.quotaExhausted(ctx, r.Repository)
	if err != nil {
		f.warn(r, "quota not checked after the error",
			"the quota could not be checked after the session's error, so the run is failed rather than resumed after a reset: "+err.Error())
	}
	if !exhausted {
		f.finish(r, outcomeFailed, reason, exitCode)
		return
	}
	next := "the factory resumes the issue after the reset"
	if r.Signal == signalQuota {
		next = "the issue waits for a person, because this run was already the resume after a reset and ran out again; removing the assignee hands it back"
	}
	f.finish(r, outcomeQuota, fmt.Sprintf("%s; the Claude quota of the scope %s is exhausted until %s, so the branch, the worktree and the assignee stay and %s",
		reason, scope, until.Format(time.RFC3339), next), exitCode)
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
		return claimed{holding: true, base: "main"}, nil // the base its canned conflicts are with
	}
	claim, err := f.claim(ctx, r, entry)
	// The branch is recorded whether the claim was won or lost: it is what the claim was, and for a
	// lost one it names the branch that holds the issue.
	if claim.branch != "" {
		f.runs.update(r, func() { r.Branch, r.Base = claim.branch, claim.base })
	}
	return claim, err
}

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
		"enabledPlugins":    map[string]bool{"planner@" + marketplace: false, "orchestrator@" + marketplace: false},
		"autoCompactWindow": compactWindow,
	})
	if err != nil {
		return "", fmt.Errorf("the worker's settings could not be written: %w", err)
	}
	return string(settings), nil
}

// workerVariables is the env block of those settings: the worker knobs the host's configuration sets
// for every run (worker_env, the same knobs a local claim takes with --env), and then what this one
// session is, which nothing a host writes may disagree with. The knobs go in first, so the session's
// own keys are written over them and stay what this function says, however the accepted names ever
// change — the order the orchestrator's claim.sh keeps.
//
// WF_STOP_AFTER is the stage the session ends after, for the session that stops once the gate has
// recorded its result, where the factory's review stage takes over ([ADR 0043]). No session runs
// the worker's own ci stage any more, so neither its knobs nor the review mandate of its repair count
// reach a session: the factory counts the repair rounds itself.
//
// [ADR 0043]: ../docs/adr/0043-the-migration-runs-from-the-last-stage-to-the-first.md
func workerVariables(entry Entry, claim claimed, knobs map[string]string, s session) map[string]string {
	variables := maps.Clone(knobs)
	if variables == nil {
		variables = map[string]string{}
	}
	if s.stopAfter != "" {
		variables["WF_STOP_AFTER"] = s.stopAfter
	}
	maps.Copy(variables, map[string]string{
		"WF_MODE":                              "manual",
		"WF_ISSUE":                             strconv.Itoa(entry.Issue.Number),
		"WF_BASE_BRANCH":                       claim.base,
		"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS": "1",
		"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE":      compactPercentage,
	})
	return variables
}

// The compact pin of the workflow, the two numbers the local claim sets and this one restates
// ([ADR 0031], [ADR 0034]). Their product is the compact trigger, 250 000 tokens.
//
// [ADR 0031]: ../docs/adr/0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md
// [ADR 0034]: ../docs/adr/0034-the-compact-trigger-is-raised-through-the-window.md
const (
	compactWindow     = 312500
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
func (f *Factory) read(r *Run, session *heard, what string, stream io.Reader, line func([]byte)) {
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 64*1024), maxStreamLine)
	for scanner.Scan() {
		line(scanner.Bytes())
	}
	// A stream the factory closed itself is not an error of the stream: see drainGrace.
	if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		f.error(r, session, Event{Kind: "error", Title: what + " could not be read to its end", Body: err.Error()})
		_, _ = io.Copy(io.Discard, stream)
	}
}

// error records an error event and keeps it as the reason the session failed, if it did.
func (f *Factory) error(r *Run, session *heard, e Event) {
	f.runs.update(r, func() {
		if body := firstLine(e.Body); body != "" {
			session.lastError = body
		} else {
			session.lastError = e.Title
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
	if models := f.runs.unpriced(r); len(models) > 0 {
		f.warn(r, "cost counted without "+strings.Join(models, ", "),
			"the factory has no price for "+strings.Join(models, ", ")+", so the cost it counted leaves the messages of that model out")
	}
	// Whether the maintainer has to hear of this ending is decided before it is written, so that the
	// record carries both in one write, and the call itself is made once the ending stands.
	owed := f.owes(*r, outcome)
	f.runs.finish(r, outcome, reason, exitCode, owed)
	log.Printf("run %d (%s#%d) ended: %s", r.ID, r.Repository, r.Issue, outcome)
	if owed {
		f.deliver(r)
	}
}
