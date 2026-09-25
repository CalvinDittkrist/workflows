package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"syscall"
	"time"
)

// Resuming work the factory already holds. Nothing here deletes anything and nothing here retries by
// itself more than once: the factory resumes an interruption exactly once per issue, and after that
// the issue waits for a person, whose gesture is taking the assignee off it ([ADR 0026]). The resume
// after a quota reset is counted apart, once in a row, because running out of quota once says
// nothing about the issue (quota.go).
//
// Every signal is read from the run records in the data directory rather than from memory, so a
// factory that was stopped, rebooted or cut off from power knows on its next start what it holds and
// what it has already spent.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md

// endSurvivors ends the worker of every run this start found active whose process group is still
// alive. A factory that was stopped ends its worker itself, but one the host killed — the kernel out
// of memory, a `kill -9`, a service manager that does not take the whole group with it — ends
// nothing, and its worker runs on with nobody reading its stream. The run it belongs to is over as
// far as every record goes, so resuming that issue beside it would leave two unattended sessions
// committing in one worktree.
//
// The run's lock is the proof: it is held by that process group and by nothing else, so a lock this
// factory cannot take says a process of the group is there, and a process group whose members are
// alive is one no other process on this host has been given the number of. Only then is the recorded
// number signalled — the same group, ended the same way a stop ends it, and killed if it does not go.
func (f *Factory) endSurvivors() {
	for _, id := range f.runs.cutOff {
		run, ok := f.runs.find(id)
		if !ok || f.runs.free(id) {
			continue
		}
		groups := survivorGroups(*run)
		if len(groups) == 0 {
			continue
		}
		log.Printf("run %d (%s#%d) left a worker behind: ending process groups %v, which no factory has been reading",
			run.ID, run.Repository, run.Issue, groups)
		f.runs.event(run, Event{Kind: "factory", Title: "worker ended after the factory",
			Body: fmt.Sprintf("the process groups %v of this run outlived the factory that started them and were ended before the issue is resumed", groups)})
		for _, group := range groups {
			if err := endGroup(group, syscall.SIGTERM); err != nil {
				log.Printf("error: the worker group %d of run %d could not be ended: %v; end it by hand before this issue is worked again", group, run.ID, err)
			}
		}
		if f.runs.freed(id, workerGrace) {
			continue
		}
		for _, group := range groups {
			_ = endGroup(group, syscall.SIGKILL)
		}
		if !f.runs.freed(id, workerGrace) {
			log.Printf("error: the worker groups %v of run %d are still there after a kill; end what is left of them by hand before this issue is worked again", groups, run.ID)
		}
	}
}

// survivorGroups is the process groups a run records as its sessions': the last one started and every
// one that ran beside it, each once.
func survivorGroups(run Run) []int {
	out := []int{}
	for _, group := range append([]int{run.WorkerGroup}, run.Groups...) {
		if group > 0 && !slices.Contains(out, group) {
			out = append(out, group)
		}
	}
	return out
}

// workerGrace is how long a worker that outlived its factory is given to end, first on the signal a
// stop uses and then on the one nothing survives. It is the room a session needs to write out what
// it holds, which is what the stop gives it too.
const workerGrace = 10 * time.Second

// holding is what this factory's records say about one issue it has worked: the latest run that
// holds the issue on the remote, which is the branch and the worktree a resumed run continues in,
// the latest run of the issue whatever it was, and whether the one automatic resume is unspent.
type holding struct {
	run   Run  // the latest run that holds the issue; only read when holds is true
	holds bool // this factory owns the issue on the remote
	last  Run  // the latest run of the issue
	idle  bool // that run has ended, so another one of this issue may be queued
	// resumes is the signal the factory resumes the issue on by itself, or empty: interruption when
	// that run was interrupted and the one automatic resume is still there to be spent, quota when it
	// ran out of quota, which is resumed whatever that budget says, unless that run was itself the
	// resume after a reset.
	resumes string
	// let is the latest run of the issue this factory let go, and letGo says the issue is out of its
	// hands: the worktree and the local branch are gone, the branch may still be on the remote, and
	// the record of that run is what a routing of the issue after that moment takes it back by.
	let   Run
	letGo bool
	// answered is the newest release this issue's runs have already acted on, as GitHub timed the
	// removal that queued them. A release no newer than this is done with.
	answered time.Time
	// pullRequest is the pull request the claim this factory holds has opened, the latest run of it
	// that named one: the one the factory watches for a review that asks for changes, and the one
	// whose own end — merged or closed — lets the issue go. An issue let go without one loses the
	// assignee this factory put on it; an issue with one keeps it, because from then on the work is
	// with a person and the assignee says who did it. It goes with the claim: what became of it was
	// decided about the runs that opened it, and a run that takes the issue back afterwards is a
	// claim of its own with nothing to show.
	//
	// addressed is the newest review asking for changes that a run of this issue already stands for,
	// as GitHub timed its submission. A review no newer than that is answered ([ADR 0023]).
	//
	// [ADR 0023]: ../docs/adr/0023-github-is-the-only-control-surface-of-the-factory.md
	pullRequest string
	addressed   time.Time
	// drafted says the pull request is still the draft a gate on CI opened, which no pr stage finished:
	// a review on it queues no follow-up run (holding.pull).
	drafted bool
}

// holdings reads the run records, oldest first, into one entry per issue.
//
// The automatic resume is a budget of one per issue, spent by the resumed run it pays for and given
// back by every signal that is a person's decision: a claim carries one, a release hands the issue
// back with one, and so does the routing of an issue this factory had let go. An interruption is the
// only signal that spends and never gives, so a factory that loses power twice over one issue stops
// after the second time and waits. A quota resume neither spends it nor gives it back: its cause
// passes by itself and has nothing to do with the issue. It is one in a row all the same: a resumed
// run that runs out of quota again is an issue that uses up a whole window by itself, and the next
// one after it is the maintainer's to decide on, so that issue waits for a person too.
func holdings(runs []Run) map[string]holding {
	out := map[string]holding{}
	budget := map[string]int{}
	for _, run := range runs {
		key := run.key()
		h := out[key]
		h.last, h.idle = run, run.EndedAt != nil
		// The pull request of the issue moves forward with the runs that report one — a run that
		// reported none says nothing about it — and is cleared below with the claim it was opened
		// under, which is why it is read here and not after that.
		if run.PullRequest != "" {
			h.pullRequest, h.drafted = run.PullRequest, run.Draft
		}
		switch {
		case run.LetGoAt != nil:
			// The claim of this run stood and stands no more. What it holds is cleared with it, so a
			// reader that forgets to ask holds first meets an empty run rather than a worktree that
			// is not on this host any more — and the pull request of that claim goes with it, so the
			// run that takes the issue back is not decided about by the one before it.
			h.run, h.holds, h.let, h.letGo, h.pullRequest, h.drafted = Run{}, false, run, true, "", false
		case run.Holding:
			h.run, h.holds, h.letGo = run, true, false
		}
		switch run.Signal {
		case signalInterruption:
			budget[key]--
		case signalQuota:
		default:
			budget[key] = 1
		}
		// Answered stands for the releases this issue is done with, so it only ever moves forward.
		if at := releaseAt(run); at.After(h.answered) {
			h.answered = at
		}
		// And so does the review the issue's runs have answered.
		if run.Signal == signalChangesRequested && run.SignalAt.After(h.addressed) {
			h.addressed = run.SignalAt
		}
		out[key] = h
	}
	for key, h := range out {
		switch {
		case !h.holds || !h.idle:
		case h.last.Outcome == outcomeQuota && h.last.Signal != signalQuota:
			h.resumes = signalQuota
		case h.last.Outcome == outcomeInterrupted && budget[key] > 0:
			h.resumes = signalInterruption
		}
		out[key] = h
	}
	return out
}

// releaseAt is the release a run has answered, and the zero time for a run that has not. Answering a
// release is taking the issue back — the assignee this factory put on it again — or, short of that,
// ending on the attempt: a run that reached an outcome of its own has answered the gesture whether
// the take-back landed or not. Only the interruption is not an answer, because the factory was
// stopped or cut off under that run rather than done with it: the issue is still lying unassigned
// where the person who released it left it, and the next start takes it back for good.
//
// A failed attempt has to count, or the release is read anew on every poll — nothing about the issue
// changed, so GitHub keeps answering with it — and the factory works the same failing resume again
// the moment it ends, for as long as the issue stands. The issue waits for a person instead, and the
// person's next gesture is a removal newer than this one.
func releaseAt(run Run) time.Time {
	if run.Signal != signalRelease {
		return time.Time{}
	}
	if !run.Holding && (run.EndedAt == nil || run.Outcome == outcomeInterrupted) {
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

// resume prepares a run of work this factory already holds — a resumed run and a follow-up run
// alike: the worktree the claim made is where the worker continues, on the commits that are there.
// Nothing is fetched and no branch is created — the claim that decided the issue stands, and this
// run is under it.
//
// A release is the one signal with something to do on the remote. The person who released the issue
// took the assignee off, which is what made it match the routing rule again; the factory puts itself
// back on before it starts a worker, so the issue is out of every other claimer's line for as long
// as this run lasts.
func (f *Factory) resume(ctx context.Context, r *Run, e Entry) (claimed, error) {
	held := claimed{branch: e.resume.Branch, base: e.resume.Base, worktree: e.resume.Worktree,
		created: true, resumed: true,
		// A run after an interruption or a review holds what the claim under it holds: the issue is
		// assigned to this host and nothing about that has changed. A release holds nothing until the take-back
		// below has put the assignee back on, so a stop in between leaves a record that says the
		// release is still unanswered and the next start answers it.
		holding: e.Signal != signalRelease}
	if f.fake { // fake mode claims nothing, so it holds no worktree to continue in either
		held.holding = true
		return held, nil
	}
	clone := clonePath(f.settings.DataDir, e.Repository)
	if held.worktree == "" {
		held.worktree = worktreePath(clone, held.branch)
	}
	if _, err := os.Stat(held.worktree); err != nil {
		// The worktree of the claim is not on this host: the data directory was moved or lost, or the
		// issue was let go and routed again. The work itself is on the remote — every worktree this
		// factory removes is pushed first ([ADR 0026]) — so the worktree is made again from the branch
		// and the run continues on those commits. The remote is fetched for it, which a resume that
		// finds its worktree does not do: that one continues on the commits that are there.
		if _, err := gitWithin(ctx, clone, fetchTimeout, "fetch", "--quiet", "--prune", "origin"); err != nil {
			return held, fmt.Errorf("the worktree %s of run %d is not on this host and %s could not be fetched to make it again: %w",
				held.worktree, e.resume.ID, e.Repository, err)
		}
		if err := makeWorktree(ctx, clone, held.branch, held.worktree); err != nil {
			return held, fmt.Errorf("the worktree %s of run %d is not on this host and could not be made again from the branch %s: %w",
				held.worktree, e.resume.ID, held.branch, err)
		}
		f.runs.event(r, Event{Kind: "factory", Title: "worktree made again from " + held.branch,
			Body: fmt.Sprintf("the worktree %s of run %d was not on this host; it was made again from the branch on the remote", held.worktree, e.resume.ID)})
	}
	if e.Signal == signalRelease {
		login, err := f.login(ctx)
		if err != nil {
			return held, err
		}
		if err := assignSelf(ctx, e.Repository, e.Number, login); err != nil {
			return held, fmt.Errorf("issue #%d of %s was released but could not be assigned to %s again: %w", e.Number, e.Repository, login, err)
		}
		held.holding = true
		f.runs.update(r, func() { r.Holding = held.holding })
	}
	f.runs.event(r, Event{Kind: "factory", Title: "resuming " + held.branch,
		Body: fmt.Sprintf("%s after run %d; the worker continues in %s", resuming[e.Signal], e.resume.ID, held.worktree)})
	return held, nil
}

// resuming says why a resumed run was queued, for the line of its log that says the run continues
// work rather than claiming it.
var resuming = map[string]string{
	signalInterruption:     "the one automatic resume after an interruption",
	signalQuota:            "the resume after the reset of the quota the run before ran out of",
	signalRelease:          "a person released the issue by removing the assignee",
	signalChangesRequested: "a review asked for changes on the pull request",
}

// released says that a person handed this held issue back to the factory. The issue is in the line
// GitHub answers with, which for an issue the factory assigned to itself can only mean the assignee
// was taken off, and that removal is newer than both the release a resumed run already stands for
// and the assignment this factory made, which is what keeps an assignee somebody removed before the
// claim from counting as a release.
//
// Both comparisons are two readings on GitHub's own clock: the removal against the release a run was
// queued on, so a poll that still shows the issue unassigned — the assignment of the resumed run has
// not landed yet — queues nothing twice, and the removal against the assignment it undid. Nothing
// here is held against this host's clock, which may be minutes from GitHub's in either direction:
// a host running ahead would else answer a genuine release with silence for as long as the drift
// lasts, and there is nobody watching who would notice.
//
// Only when GitHub's event list names no assignment at all is the start of the latest run the guard
// instead. There is no reading to compare with then, and the gesture that case stands for — an
// assignee removed before this factory ever claimed the issue — lies hours behind the run rather
// than minutes.
func (h holding) released(issue Issue, routed bool) bool {
	if !routed || !h.holds || !h.idle || !issue.unassignedAt.After(h.answered) {
		return false
	}
	if issue.assignedAt.IsZero() {
		return issue.unassignedAt.After(h.last.StartedAt)
	}
	return issue.unassignedAt.After(issue.assignedAt)
}

// signalAt is when the interruption or the quota run this resume answers ended.
func (h holding) signalAt() time.Time {
	if h.last.EndedAt == nil {
		return h.last.StartedAt
	}
	return *h.last.EndedAt
}
