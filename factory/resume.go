package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Resuming work the factory already holds. Nothing here deletes anything and nothing here retries by
// itself more than once: the factory resumes an interruption exactly once per issue, and after that
// the issue waits for a person, whose gesture is taking the assignee off it ([ADR 0026]).
//
// Both signals are read from the run records in the data directory rather than from memory, so a
// factory that was stopped, rebooted or cut off from power knows on its next start what it holds and
// what it has already spent.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md

// holding is what this factory's records say about one issue it has worked: the latest run that
// holds the issue on the remote, which is the branch and the worktree a resumed run continues in,
// the latest run of the issue whatever it was, and whether the one automatic resume is unspent.
type holding struct {
	run     Run  // the latest run that holds the issue; only read when holds is true
	holds   bool // this factory owns the issue on the remote
	last    Run  // the latest run of the issue
	idle    bool // that run has ended, so another one of this issue may be queued
	resumes bool // it was interrupted and the one automatic resume is still there to be spent
	// answered is the newest release this issue's runs have already acted on, or the start of its
	// latest run when there was none. A release no newer than this is done with.
	answered time.Time
}

// holdings reads the run records, oldest first, into one entry per issue.
//
// The automatic resume is a budget of one per issue, spent by the resumed run it pays for and given
// back by a release: a person who hands a held issue back to the factory has decided, and what
// follows that decision may be interrupted and resumed once again, exactly as the first claim may.
// Nothing else gives it back, so a factory that loses power twice over one issue stops after the
// second time and waits.
func holdings(runs []Run) map[string]holding {
	out := map[string]holding{}
	budget := map[string]int{}
	for _, run := range runs {
		key := run.key()
		h, seen := out[key]
		if !seen {
			budget[key] = 1 // the claim of an issue carries its one automatic resume
		}
		h.last, h.idle = run, run.EndedAt != nil
		if run.Holding {
			h.run, h.holds = run, true
		}
		switch run.Signal {
		case signalRelease:
			budget[key] = 1
		case signalInterruption:
			budget[key]--
		}
		// Answered stands for what this issue is done with, so it only ever moves forward: the
		// release a run was queued on, and the start of every run.
		for _, at := range []time.Time{run.StartedAt, releaseAt(run)} {
			if at.After(h.answered) {
				h.answered = at
			}
		}
		out[key] = h
	}
	for key, h := range out {
		h.resumes = h.holds && h.idle && h.last.Outcome == outcomeInterrupted && budget[key] > 0
		out[key] = h
	}
	return out
}

// releaseAt is the release a run was queued on, and the zero time for a run that was not.
func releaseAt(run Run) time.Time {
	if run.Signal != signalRelease {
		return time.Time{}
	}
	return run.SignalAt
}

// repository is where this issue is, read from a record rather than from the line, because an issue
// the factory holds is not in the line GitHub answers with.
func (h holding) repository() string { return h.last.Repository }

// issue is the queue entry of an issue the factory holds, as its own records describe it. An issue
// it holds is assigned to this host, so it is not in the line GitHub answers with and there is
// nothing else to read it from: it carries no labels and no routing time, because a resumed run
// claims nothing and needs neither.
func (h holding) issue() Issue {
	return Issue{Repository: h.run.Repository, Number: h.run.Issue, Title: h.run.Title, Labels: []string{}}
}

// resume prepares a run of work this factory already holds: the worktree the claim made is where the
// worker continues, on the commits that are there. Nothing is fetched and no branch is created — the
// claim that decided the issue stands, and this run is under it.
//
// A release is the one signal with something to do on the remote. The person who released the issue
// took the assignee off, which is what made it match the routing rule again; the factory puts itself
// back on before it starts a worker, so the issue is out of every other claimer's line for as long
// as this run lasts.
func (f *Factory) resume(ctx context.Context, r *Run, e Entry) (claimed, error) {
	held := claimed{branch: e.resume.Branch, base: e.resume.Base, worktree: e.resume.Worktree,
		created: true, holding: true, resumed: true}
	if f.fake { // fake mode claims nothing, so it holds no worktree to continue in either
		return held, nil
	}
	if _, err := os.Stat(held.worktree); err != nil {
		return held, fmt.Errorf("the worktree %s of run %d is not on this host: %w; the branch %s still holds the issue, so put the worktree back or let the issue go",
			held.worktree, e.resume.ID, err, held.branch)
	}
	if e.Signal == signalRelease {
		login, err := f.login(ctx)
		if err != nil {
			return held, err
		}
		if _, err := gh(ctx, "issue", "edit", strconv.Itoa(e.Number), "--repo", e.Repository, "--add-assignee", login); err != nil {
			return held, fmt.Errorf("issue #%d of %s was released but could not be assigned to %s again: %w", e.Number, e.Repository, login, err)
		}
	}
	f.runs.event(r, Event{Kind: "factory", Title: "resuming " + held.branch,
		Body: fmt.Sprintf("%s after run %d; the worker continues in %s", resuming[e.Signal], e.resume.ID, held.worktree)})
	return held, nil
}

// resuming says why a resumed run was queued, for the line of its log that says the run continues
// work rather than claiming it.
var resuming = map[string]string{
	signalInterruption: "the one automatic resume after an interruption",
	signalRelease:      "a person released the issue by removing the assignee",
}

// released says that a person handed this held issue back to the factory. The issue is in the line
// GitHub answers with, which for an issue the factory assigned to itself can only mean the assignee
// was taken off, and that removal is newer than everything the issue's runs have answered — the
// release a resumed run already stands for, and the start of the latest run, which is what keeps an
// assignee somebody removed before the factory ever claimed the issue from counting as a release.
//
// A release the factory has answered is compared with the release its run was queued on: two
// readings of the same event on GitHub's own clock, so a poll that still shows the issue unassigned,
// because the assignment of the resumed run has not landed yet, queues nothing twice however far
// this host's clock and GitHub's are apart. The start of the latest run is this host's clock, and
// only guards the gesture that came before any run of the issue, where minutes of drift are nothing
// against the hours such a removal lies back.
func (h holding) released(issue Issue, routed bool) bool {
	return routed && h.holds && h.idle && issue.unassignedAt.After(h.answered)
}

// signalAt is when the interruption this resume answers happened.
func (h holding) signalAt() time.Time {
	if h.last.EndedAt == nil {
		return h.last.StartedAt
	}
	return *h.last.EndedAt
}
