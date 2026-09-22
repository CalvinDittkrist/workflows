package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The outcomes of the vocabulary. A run in fake mode reaches ready, blocked, failed, timeout and
// interrupted; lost is the race another claimer won ([ADR 0024]), and cancelled and quota arrive
// with the tickets that add them.
//
// [ADR 0024]: ../docs/adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md
const (
	outcomeReady       = "ready"
	outcomeBlocked     = "blocked"
	outcomeFailed      = "failed"
	outcomeTimeout     = "timeout"
	outcomeInterrupted = "interrupted"
	outcomeLost        = "lost"
)

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
	Branch      string     `json:"branch"`
	Base        string     `json:"base"`
	State       string     `json:"state"` // running or ended
	Stage       string     `json:"stage"` // the stage the worker is in, read from its skill calls
	Stages      []string   `json:"stages"`
	Outcome     string     `json:"outcome"` // empty while the run is running
	PullRequest string     `json:"pullRequest"`
	Reason      string     `json:"reason"`
	Model       string     `json:"model"`
	SessionID   string     `json:"sessionId"`
	StartedAt   time.Time  `json:"startedAt"`
	EndedAt     *time.Time `json:"endedAt"`
	Turns       int        `json:"turns"`
	CostUSD     float64    `json:"costUsd"`
	Tokens      Tokens     `json:"tokens"`
	ContextPeak int        `json:"contextPeak"` // the largest context one message of the worker carried
	ExitCode    *int       `json:"exitCode"`
	EventCount  int        `json:"eventCount"`
	Warnings    []string   `json:"warnings"`
	Versions    Versions   `json:"versions"`

	// What the stream said, kept for the moment the run ends. Not part of the record.
	reportOutcome string // ready or blocked, as the worker's final report gave it
	reportDetail  string // the pull request for ready, the reason for blocked
	lastError     string // the last error the session printed, which is why a failed run failed
	resultSummary string // what the result line called an error, when the session printed no cause
}

// Tokens are the totals of the session, taken from the result line only: the assistant lines carry
// the usage at a message's start and undercount the run badly.
type Tokens struct {
	Input         int `json:"input"`
	Output        int `json:"output"`
	CacheCreation int `json:"cacheCreation"`
	CacheRead     int `json:"cacheRead"`
}

// Versions is what a run ran with. Empty here: the ticket that updates the plugins before a run
// fills it, and the record carries the fields from the start so a reader never meets a new shape.
type Versions struct {
	Worker     string `json:"worker"`
	ClaudeCode string `json:"claudeCode"`
	Factory    string `json:"factory"`
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
	dir  string
	mu   sync.Mutex
	runs []*Run
}

// OpenStore reads the runs already in the data directory. A run that was active when the factory
// stopped has no process any more: it is recorded as interrupted, so it neither blocks the next run
// nor claims to be running.
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
			s.finish(r, outcomeInterrupted, reason, nil)
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

// raiseContextPeak keeps the largest context the worker's own messages carried. The comparison and
// the write are made under the lock. A context usually grows with every message, so this writes the
// record about as often as the worker speaks; it stops only once the peak stands, after a handoff or
// a long run of reading.
func (s *Store) raiseContextPeak(r *Run, tokens int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tokens <= r.ContextPeak {
		return
	}
	r.ContextPeak = tokens
	s.write(r)
}

// finish ends a run. The reason survives as the record's reason unless the stream already gave one.
func (s *Store) finish(r *Run, outcome, reason string, exitCode *int) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	r.State, r.Outcome, r.EndedAt, r.ExitCode = "ended", outcome, &now, exitCode
	if r.Reason == "" {
		r.Reason = reason
	}
	s.write(r)
}

// write persists a record. It writes a whole file and renames it, so a reader never meets a half
// written record and a crash of the factory cannot leave one. The bytes are not forced to the disk:
// a power cut can still cost the last writes, which is why nothing depends on a record that the run
// itself did not also report to GitHub. Callers hold the lock.
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
	if err := file.Close(); err != nil {
		failed(err)
		return
	}
	if err := os.Rename(file.Name(), s.recordPath(r.ID)); err != nil {
		failed(err)
	}
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

func (r *Run) key() string { return fmt.Sprintf("%s#%d", r.Repository, r.Issue) }

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
