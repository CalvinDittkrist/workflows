package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// The outcomes of the vocabulary. A run in fake mode reaches ready, blocked, failed, timeout and
// interrupted; lost is the race another claimer won ([ADR 0024]), cancelled is a run a decision on
// GitHub ended (the routing label taken off its issue, or the issue closed, [ADR 0023]), and quota
// a session that ended in an error on a used-up quota ([ADR 0037]).
//
// [ADR 0023]: ../docs/adr/0023-github-is-the-only-control-surface-of-the-factory.md
// [ADR 0024]: ../docs/adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md
// [ADR 0037]: ../docs/adr/0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md
const (
	outcomeReady       = "ready"
	outcomeBlocked     = "blocked"
	outcomeFailed      = "failed"
	outcomeTimeout     = "timeout"
	outcomeInterrupted = "interrupted"
	outcomeLost        = "lost"
	outcomeCancelled   = "cancelled"
	outcomeQuota       = "quota"
)

// What put a run in the line. routed is an issue taken from the queue of routed issues; the other
// three are work this factory already holds and continues in the worktree of that claim: the one
// automatic resume after an interruption, the resume after the quota reset that a run which ran out
// of it waits for, the run a person asked for by taking the assignee off an issue the factory holds
// ([ADR 0026]), and the run a review that asks for changes on the pull request queues ([ADR 0023]).
// The signals of an issue's runs are what the next run of it is decided from, which is why every run
// records its own.
//
// [ADR 0023]: ../docs/adr/0023-github-is-the-only-control-surface-of-the-factory.md
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
const (
	signalRouted           = "routed"
	signalInterruption     = "interruption"
	signalQuota            = "quota"
	signalRelease          = "release"
	signalChangesRequested = "changes-requested"
)

// The kinds of run the vocabulary names (docs/glossary.md): a first run claims its issue, a resumed
// run continues what an interruption or a release left in the worktree of that claim, and a
// follow-up run answers a review on the pull request a run of the issue opened. The kind is the
// signal read as a word, so the two can never disagree about what a run was.
const (
	kindFirst    = "first"
	kindResumed  = "resumed"
	kindFollowUp = "follow-up"
)

func kindOf(signal string) string {
	switch signal {
	case signalInterruption, signalQuota, signalRelease:
		return kindResumed
	case signalChangesRequested:
		return kindFollowUp
	default:
		return kindFirst
	}
}

// Run is one factory run: one worker session, its record. The record is the file in the data
// directory and the body the HTTP interface serves, so a reader on the host and a reader in a
// browser see the same fields.
type Run struct {
	ID         int    `json:"id"`
	Repository string `json:"repository"`
	Issue      int    `json:"issue"`
	Title      string `json:"title"`
	// Branch is the branch the claim created on the remote, and Base the branch it was cut from. A
	// lost run carries the branch too: it is the one another claimer holds the issue by.
	Branch string `json:"branch"`
	Base   string `json:"base"`
	// Worktree is where the worker ran: the directory the claim made in this host's clone, and the
	// one a resumed run of this issue continues in, on the commits that are there. Empty in fake
	// mode, which claims nothing, and for a claim that never got that far.
	Worktree string `json:"worktree"`
	// Holding says this factory owns the issue on the remote: its claim created the branch, assigned
	// the issue to this host and made the worktree. Only an issue the factory holds is resumed and
	// only such an issue can be released, so a lost claim and a claim that failed half way are left
	// alone, including the branch of another claimer, which this factory never works.
	Holding bool `json:"holding"`
	// LetGoAt is when this factory gave the issue back, and nil for as long as it holds it: the
	// commits pushed, the worktree and the local branch removed, the assignee taken off unless a
	// pull request stands, and every record and log of the issue kept. Holding stays as it was; it
	// is the history that says this factory's claim made that branch, which is what a later routing
	// of the same issue is read against ([ADR 0026]).
	//
	// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
	LetGoAt *time.Time `json:"letGoAt"`
	// Signal is what queued this run: routed, interruption, quota, release or changes-requested.
	// SignalAt is when that signal happened (the routing, the interruption, the end of the run that
	// ran out of quota, the moment the assignee came off, or the moment the review was submitted). It
	// is the answer this run is: a signal of an issue is acted on once, and a signal no later than the
	// one its records already carry has been answered already.
	// That is what keeps the factory from resuming the same release, or answering the same review, for
	// as long as GitHub reports it, without a clock of its own.
	Signal   string    `json:"signal"`
	SignalAt time.Time `json:"signalAt"`
	// Kind is what this run is in the vocabulary: a first, a resumed or a follow-up run. It says the
	// same as the signal in the word a person reads, and kindOf is the one place it is decided.
	Kind        string   `json:"kind"`
	State       string   `json:"state"` // running or ended
	Stage       string   `json:"stage"` // the stage the worker is in, read from its skill calls
	Stages      []string `json:"stages"`
	Outcome     string   `json:"outcome"` // empty while the run is running
	PullRequest string   `json:"pullRequest"`
	// Draft says the pull request is the draft a gate on CI opened, whose title, body and readiness the
	// pr stage has not written yet. It is the factory's record and never GitHub's draft state, which a
	// person may change: the stage a resumed run goes on at is read from it and the branch.
	Draft bool `json:"draft,omitempty"`
	// ReadiedAt is when the pr stage marked that draft ready for review, which a workflow may run on:
	// the ci stage does not read the draft's checks as the ready pull request's until that run is there.
	ReadiedAt *time.Time `json:"readiedAt,omitempty"`
	// RepairRounds is how many repair rounds the ci stage has spent on that pull request, which the
	// budget is held against (ci.repair_rounds). A resumed run on the same pull request carries the
	// count on.
	RepairRounds int `json:"repairRounds"`
	// Review is what the review stage handed on to the pr stage: the panel summary it derived from its
	// recorded rounds, the gate result on the final head, and the commit the branch was at then. A
	// resumed run whose branch is still at that commit and has no pull request open starts at the pr
	// stage with it.
	Review *Review `json:"review,omitempty"`
	// Panel is what the review stage recorded: its rounds, the verdicts and fixes of each, and the gate
	// on the final head. A resumed run goes on from it.
	Panel *Panel `json:"panel,omitempty"`
	// Gates is every run of the gate, in the gate stage and on the final head: the commit, the exit
	// status, the duration and the end of the output of each.
	Gates []Gated `json:"gates,omitempty"`
	// Answered is the reviews asking for changes on that pull request whose summaries the run
	// answered, by their URL. A review stands on GitHub until its author approves, so every later run
	// on the pull request reads these to leave them alone.
	Answered []string `json:"answered,omitempty"`
	// Replied is the review threads on that pull request the run replied to and could not resolve, by
	// their id: every later reading, of this run or a later one, resolves them and asks no session again.
	Replied   []string   `json:"replied,omitempty"`
	Reason    string     `json:"reason"`
	Model     string     `json:"model"`
	SessionID string     `json:"sessionId"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt"`
	Turns     int        `json:"turns"`
	CostUSD   float64    `json:"costUsd"`
	Tokens    Tokens     `json:"tokens"`
	// Totals says where turns, cost and tokens come from: worker when the session's result line
	// reported them, factory while the factory counts them from the assistant lines (during the run,
	// and at its end when the session ended without a result line, as it does whenever the factory
	// ends it). Empty on a record that has none and on one written before this field.
	Totals      string `json:"totals"`
	ContextPeak int    `json:"contextPeak"` // the largest context one message of the worker carried
	// WorkerGroup is the process group the worker session ran in, which is the process group this
	// host ends to end the session. It is recorded so that a factory the host killed rather than
	// stopped can be started again and end the worker that outlived it.
	WorkerGroup int `json:"workerGroup"`
	// Groups is the process groups of the sessions that are running, which are more than one while the
	// reviewers of a round run beside each other: every one of them is ended the way WorkerGroup is.
	Groups     []int    `json:"groups,omitempty"`
	ExitCode   *int     `json:"exitCode"`
	EventCount int      `json:"eventCount"`
	Warnings   []string `json:"warnings"`
	Versions   Versions `json:"versions"`
	// Notified is what this ending owes the maintainer on GitHub: pending while the notification is
	// still owed and done once the factory has tried it: done says it was made, not that GitHub
	// took it, and a call GitHub refused is a warning on the run and done all the same. It is
	// written before the call and again after it, so a host cut off in between makes it on its next
	// start. A run that owes nobody anything (an outcome that notifies nobody, a factory with no
	// logins to notify, a record written before this field) carries none of it.
	Notified string `json:"notified,omitempty"`

	// What the streams said, kept for the moment the run ends. Not part of the record: what one session
	// said is its session's (heard), since the reviewers of a round run beside each other.
	counted        map[string]usage // the usage the factory counted per message id
	unpricedModels map[string]bool  // the models of counted messages that have no price here
	// A run starts a session per stage that needs one, and its totals are the sum of theirs. open is how
	// many of the running ones have not given their own totals yet, and short says one ended without
	// them: the totals are the worker's own only once every session has given its own (provenance).
	open  int
	short bool
	// lock is the factory's copy of the run's lock while the run lasts, which every session's process
	// group inherits (Store.lock).
	lock *os.File
}

// release lets go of the factory's copy of the run's lock once the run is over. Callers hold the lock
// of the store.
func (r *Run) release() {
	if r.lock != nil {
		r.lock.Close()
		r.lock = nil
	}
}

// heard is what the stream of one session said, apart from the run it adds to: its structured
// result, why it has none, the error it ended in, and what the factory counted of it until its result
// line gave the session's own totals, which replace that count. Callers of its fields hold the lock
// of the store.
type heard struct {
	// label names the session in the run's log when others run beside it, as a reviewer does, and is
	// empty for a session that runs alone.
	label         string
	read          func(json.RawMessage) (result, error) // readResult when nil
	result        *result                               // the structured result, when the result line carried one that fits
	misfit        string                                // why the result line's structured output does not fit the schema
	lastError     string                                // the last error the session printed, which is why a failed session failed
	resultSummary string                                // what the result line called an error, when the session printed no cause
	reported      bool                                  // the result line gave the session's own totals
	turns         int                                   // what the factory counted of this session until then
	cost          float64
	tokens        Tokens
}

// ungrouped takes a process group that has ended out of Groups. It makes a new slice rather than
// deleting in place: the copies of the record the store hands out (list, get) share the old one and
// are read without the lock. Callers hold the lock.
func (r *Run) ungrouped(pid int) {
	r.Groups = slices.DeleteFunc(slices.Clone(r.Groups), func(g int) bool { return g == pid })
}

// opened readies the record for one more session. Callers hold the lock.
func (r *Run) opened() { r.open++ }

// closed is the end of a session: one that never gave its own totals leaves the factory's count in
// the run's. Callers hold the lock.
func (r *Run) closed(s *heard) {
	if !s.reported {
		r.open--
		r.short = true
	}
	if r.Totals != "" {
		r.Totals = r.provenance()
	}
}

// report takes the totals a session's result line gave in place of what the factory counted of that
// session. Callers hold the lock.
func (r *Run) report(s *heard, turns int, cost float64, tokens Tokens) {
	r.Turns += turns - s.turns
	r.CostUSD += cost - s.cost
	r.Tokens = Tokens{Input: r.Tokens.Input + tokens.Input - s.tokens.Input, Output: r.Tokens.Output + tokens.Output - s.tokens.Output,
		CacheCreation: r.Tokens.CacheCreation + tokens.CacheCreation - s.tokens.CacheCreation,
		CacheRead:     r.Tokens.CacheRead + tokens.CacheRead - s.tokens.CacheRead}
	if !s.reported {
		r.open--
	}
	s.reported = true
	r.Totals = r.provenance()
}

// provenance is where the totals come from now: the worker's own once every session gave its own, and
// the factory's while one of them is still being counted or ended without them. It is decided again
// whenever a session reports or ends, so sessions that run beside each other leave the same answer in
// whichever order they do. Callers hold the lock.
func (r *Run) provenance() string {
	if r.short || r.open > 0 {
		return totalsFactory
	}
	return totalsWorker
}

// Where the totals of a run come from.
const (
	totalsWorker  = "worker"
	totalsFactory = "factory"
)

// Tokens are the totals of the session. The result line's are the worker's own; the ones the factory
// counts from the assistant lines are a floor, because those lines may carry a message's usage from
// before it was written to the end.
type Tokens struct {
	Input         int `json:"input"`
	Output        int `json:"output"`
	CacheCreation int `json:"cacheCreation"`
	CacheRead     int `json:"cacheRead"`
}

// Versions is what a run ran with. Factory is the version of the binary that recorded the run, whose
// prompts the sessions ran on; ClaudeCode is read from the host before the first session starts. It is
// empty in fake mode, which runs no Claude Code, and when it could not be read, and then the run carries
// a warning saying so.
type Versions struct {
	ClaudeCode string `json:"claudeCode"`
	Factory    string `json:"factory"`
	// Worker is the version of the worker plugin a run of an earlier factory drove. No run records it
	// any more; it is kept so that rewriting such a record keeps what its run ran with.
	Worker string `json:"worker,omitempty"`
}

// Event is one line of a run's log: what the factory did, and what the worker's stream said.
type Event struct {
	Seq   int       `json:"seq"`
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"` // factory, hook, init, text, thinking, tool, result, error
	Title string    `json:"title"`
	Body  string    `json:"body,omitempty"`
	Sub   bool      `json:"sub,omitempty"` // written by a subagent of the worker
}

// A tool call can carry a whole file; the log keeps the head of it and says so. One line of the log
// is that body as JSON, where escaping can inflate it several times over, plus the rest of the
// event; the line the reader accepts is far above that worst case, so the limit is not a limit runs
// reach but a bound that keeps a corrupt line from being read into memory.
const (
	maxEventBody = 8000
	maxEventLine = 1 << 20
)

// Store holds the runs: one JSON record and one append-only JSONL event log per run in the data
// directory. There is no database. It is the only place that writes there, and it guards the run
// records the HTTP interface reads.
type Store struct {
	dir string
	// cutOff is the runs this start found active in the data directory and recorded as interrupted.
	// Their worker had no factory left to end it, which is what the run's lock is read for.
	cutOff []int
	mu     sync.Mutex
	runs   []*Run
}

// OpenStore reads the runs already in the data directory. A run that was active when the factory
// stopped is over whatever became of its worker: it is recorded as interrupted, so it neither blocks
// the next run nor claims to be running, and it is named in cutOff, because a worker of it may still
// be running with no factory reading it.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("the data directory %s cannot be created: %w; name a writable data_dir", dir, err)
	}
	s := &Store{dir: dir}
	records, err := filepath.Glob(filepath.Join(dir, "run-[0-9]*.json"))
	if err != nil {
		return nil, err
	}
	for _, file := range records {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("the run record %s cannot be read: %w; move it aside to start without it", file, err)
		}
		r := &Run{}
		if err := json.Unmarshal(raw, r); err != nil {
			return nil, fmt.Errorf("the run record %s is not a run: %w; move it aside to start without it", file, err)
		}
		if r.ID <= 0 {
			return nil, fmt.Errorf("the run record %s has no id; move it aside to start without it", file)
		}
		// A record that names no kind (one written before the kind was part of it, or by hand) says
		// what it was in its signal, which every record carries.
		if r.Kind == "" {
			r.Kind = kindOf(r.Signal)
		}
		// A run that ended wrote its record after its last event, so its count is good. A run that was
		// active did not: its log is the truth about how much of it was logged. Nothing else is read
		// here, because the logs of every run ever made are never deleted and a restart must not grow
		// with them.
		if r.EndedAt == nil {
			events, err := s.events(r.ID, 0)
			if err != nil {
				return nil, fmt.Errorf("the event log %s cannot be read: %w; move it aside to start without it", s.eventsPath(r.ID), err)
			}
			r.EventCount = len(events)
		}
		s.runs = append(s.runs, r)
	}
	sort.Slice(s.runs, func(a, b int) bool { return s.runs[a].ID < s.runs[b].ID })
	for _, r := range s.runs {
		if r.EndedAt == nil {
			// The log reads to its end like that of every other run: its last event says how it ended.
			reason := "the factory stopped while this run was active"
			s.event(r, Event{Kind: "error", Title: outcomeInterrupted, Body: reason})
			// The marker of such a run is the factory's, in NotifyOwed: what it owes depends on the
			// records as a whole and on the logins this host notifies, and the store knows neither.
			s.finish(r, outcomeInterrupted, reason, nil, false)
			s.cutOff = append(s.cutOff, r.ID)
		}
	}
	return s, nil
}

func (s *Store) recordPath(id int) string {
	return filepath.Join(s.dir, fmt.Sprintf("run-%d.json", id))
}

func (s *Store) eventsPath(id int) string {
	return filepath.Join(s.dir, fmt.Sprintf("run-%d.events.jsonl", id))
}

// The lock of a run says whether its worker is still there. It is taken before the worker starts and
// handed to it, so it is held by that one process group and by nothing else, and the kernel gives it
// back when the last process of the group is gone, whatever ended them, and whether or not the
// factory that started them is still alive to notice ([ADR 0027]).
//
// [ADR 0027]: ../docs/adr/0027-the-factorys-isolation-boundary-is-the-host.md
func (s *Store) lockPath(id int) string {
	return filepath.Join(s.dir, fmt.Sprintf("run-%d.lock", id))
}

// lock takes a run's lock and answers with the open file. The caller hands it to the worker and
// closes its own copy: the lock lives on in the process group that inherited the file, which is what
// makes it the group's liveness and not this factory's.
func (s *Store) lock(id int) (*os.File, error) {
	file, err := os.OpenFile(s.lockPath(id), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

// free says that a run's lock can be taken, which is the kernel saying no process of its worker is
// left. A lock this host cannot open at all is read as free and said so: the data directory is then
// unwritable, which the record and the log of every run report anyway.
func (s *Store) free(id int) bool {
	file, err := os.OpenFile(s.lockPath(id), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		log.Printf("error: the lock of run %d cannot be opened: %v; is %s writable?", id, err, s.dir)
		return true
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return true
}

// freed waits for that to happen, and answers false when it has not happened in time.
func (s *Store) freed(id int, within time.Duration) bool {
	for deadline := time.Now().Add(within); ; time.Sleep(50 * time.Millisecond) {
		if s.free(id) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// add starts a run's record. The id continues the ids in the data directory, so a restart never
// writes over a run that is already there.
func (s *Store) add(r *Run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.ID = 1
	for _, other := range s.runs {
		if other.ID >= r.ID {
			r.ID = other.ID + 1
		}
	}
	s.runs = append(s.runs, r)
	s.write(r)
}

// update changes a run and writes its record.
func (s *Store) update(r *Run, change func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change()
	s.write(r)
}

// count reads one assistant line into the record: the totals the factory counts, until the worker's
// result line has reported its own, and the largest context the worker's own messages carried. What
// a message started from is its input plus everything read from the cache: the context it was
// answered with. A subagent has a context of its own, which says nothing about how full the worker's
// is, so only the worker's own messages raise the peak. The record is written with every line, so a
// run that ends without a result line (even with the factory killed under it) keeps what was
// counted.
func (s *Store) count(r *Run, session *heard, msg message, sub bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !session.reported {
		r.tally(session, msg.ID, msg.Model, sub, msg.Usage)
	}
	if u := msg.Usage; !sub && u.Input+u.CacheCreation+u.CacheRead > r.ContextPeak {
		r.ContextPeak = u.Input + u.CacheCreation + u.CacheRead
	}
	s.write(r)
}

// unpriced is the models whose messages the cost a run ends with leaves out, sorted: none when the
// worker reported its own totals, which are what Claude Code billed. The list lives in memory only:
// a run a later start finds cut off keeps the cost counted until then and says nothing of a model it
// left out, which understates a record the host lost anyway.
func (s *Store) unpriced(r *Run) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Totals != totalsFactory {
		return nil
	}
	models := make([]string, 0, len(r.unpricedModels))
	for model := range r.unpricedModels {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

// finish ends a run. The reason survives as the record's reason unless the stream already gave one,
// and an ending that owes the maintainer a word is marked as owing it here, in the one write: the
// marker is what a start after a host that was cut off makes the notification from, and an ending
// that reached the disk without it would be read as one that owed nothing.
func (s *Store) finish(r *Run, outcome, reason string, exitCode *int, owed bool) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	r.State, r.Outcome, r.EndedAt = "ended", outcome, &now
	if exitCode != nil { // an ending outside a session keeps the exit of the last session there was
		r.ExitCode = exitCode
	}
	if r.Reason == "" {
		r.Reason = reason
	}
	if owed {
		r.Notified = notifyPending
	}
	s.write(r)
}

// write persists a record. It writes a whole file, forces it to the disk and renames it, so a reader
// never meets a half written record, a crash of the factory cannot leave one, and a host that lost
// power finds the record as its last write left it. That last part is what the resume rules stand
// on: what the factory holds and what it has already spent is read from these files and from
// nothing else, and an issue whose claim stands on GitHub while its record says otherwise would be
// neither resumed nor released ([ADR 0026]). Callers hold the lock.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
//
// The data directory is the factory's only durable output, and nobody watches the host: a write that
// fails says so in the journal, with the fix, rather than leaving a service that looks healthy and
// records nothing.
func (s *Store) write(r *Run) {
	failed := func(err error) {
		log.Printf("error: the record of run %d could not be written: %v; is %s writable and has it space left?", r.ID, err, s.dir)
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		failed(err)
		return
	}
	file, err := os.CreateTemp(s.dir, fmt.Sprintf(".run-%d.json.*", r.ID))
	if err != nil {
		failed(err)
		return
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(raw, '\n')); err != nil {
		file.Close()
		failed(err)
		return
	}
	if err := file.Sync(); err != nil {
		file.Close()
		failed(err)
		return
	}
	if err := file.Close(); err != nil {
		failed(err)
		return
	}
	if err := os.Rename(file.Name(), s.recordPath(r.ID)); err != nil {
		failed(err)
		return
	}
	// And the rename itself, so the record is under its name after a power cut and not only in it.
	dir, err := os.Open(s.dir)
	if err != nil {
		failed(err)
		return
	}
	if err := dir.Sync(); err != nil {
		failed(err)
	}
	dir.Close()
}

// event appends to a run's log. The log is append-only: it is opened, written and closed per event,
// so a run keeps everything the factory logged before it stopped, however it stopped.
func (s *Store) event(r *Run, e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.EventCount++
	e.Seq, e.At = r.EventCount, time.Now()
	e.Title = firstLine(e.Title) // the title comes from the stream as well, and is one line by contract
	if len(e.Body) > maxEventBody {
		e.Body = cut(e.Body, maxEventBody) + "\n[truncated]"
	}
	failed := func(err error) {
		log.Printf("error: event %d of run %d could not be logged: %v; is %s writable and has it space left?", e.Seq, r.ID, err, s.dir)
	}
	line, err := json.Marshal(e)
	if err != nil {
		failed(err)
		return
	}
	file, err := os.OpenFile(s.eventsPath(r.ID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		failed(err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		failed(err)
	}
}

// list is a copy of every run's record, oldest first.
func (s *Store) list() []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Run, 0, len(s.runs))
	for _, r := range s.runs {
		out = append(out, *r)
	}
	return out
}

// get is a copy of one run's record.
func (s *Store) get(id int) (Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if r.ID == id {
			return *r, true
		}
	}
	return Run{}, false
}

// find is the run of that id as the store holds it, so a caller can write to its record and its log
// rather than to a copy of them.
func (s *Store) find(id int) (*Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if r.ID == id {
			return r, true
		}
	}
	return nil, false
}

// events reads a run's log from disk and serves the events after the sequence number after, so a
// reader that polls asks only for what it has not seen. What it skips it never parses: the sequence
// is the first field of every line the factory writes, so the head of a line decides. A line that is
// not an event is skipped: the log is what the factory wrote last, and a half-written last line must
// not lose the rest.
func (s *Store) events(id, after int) ([]Event, error) {
	file, err := os.Open(s.eventsPath(id))
	if os.IsNotExist(err) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	out := []Event{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), maxEventLine)
	for scanner.Scan() {
		line := scanner.Bytes()
		if seq, ok := sequence(line); ok {
			if seq <= after {
				continue
			}
		}
		var e Event
		if json.Unmarshal(line, &e) != nil || e.Seq <= after {
			continue
		}
		out = append(out, e)
	}
	return out, scanner.Err()
}

// sequence reads the seq of a logged event from the head of its line, without parsing the rest of
// it. A line that does not begin that way is left to the parser.
func sequence(line []byte) (int, bool) {
	const prefix = `{"seq":`
	if !bytes.HasPrefix(line, []byte(prefix)) {
		return 0, false
	}
	seq := 0
	for _, c := range line[len(prefix):] {
		if c < '0' || c > '9' {
			return seq, seq > 0
		}
		seq = seq*10 + int(c-'0')
	}
	return 0, false
}

// stage moves a run to a stage and keeps the stages it has been through, so a finished run still
// shows its line and not only the stage it stopped in.
func (r *Run) stage(name string) {
	if r.Stage == name {
		return
	}
	r.Stage = name
	if len(r.Stages) == 0 || r.Stages[len(r.Stages)-1] != name {
		r.Stages = append(r.Stages, name)
	}
}

func (r *Run) key() string { return fmt.Sprintf("%s#%d", repositoryKey(r.Repository), r.Issue) }

// cut shortens a string to at most n bytes without splitting a character in half. It steps back over
// the bytes of one character at most, so a body that is not valid UTF-8 at all still keeps its head.
func cut(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for i := 0; i < utf8.UTFMax && n > 0 && !utf8.RuneStart(s[n]); i++ {
		n--
	}
	return s[:n]
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if runes := []rune(s); len(runes) > 140 {
		s = string(runes[:140]) + "…"
	}
	return s
}
