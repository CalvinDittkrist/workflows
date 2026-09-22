package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// How the maintainer learns how a run ended. Nobody watches the factory host, so an ending that
// waits for a person has to reach them where they already are, and GitHub is the only channel the
// factory has ([ADR 0023]): a review request on the pull request of a run that ended ready, and a
// comment on the issue that mentions the configured logins for an ending that waits.
//
// [ADR 0023]: ../docs/adr/0023-github-is-the-only-control-surface-of-the-factory.md

// What a run's record says about the notification its ending owes. It is written before the call
// and again after it, so an ending survives a host that is cut off in between: the next start makes
// it. GitHub taking the call and the host being cut off before the second write is the one case that
// says it twice, which is the side of the trade the maintainer can live with — a notification that
// came twice is read once, one that never came is an issue nobody answers.
const (
	notifyPending = "pending"
	notifyDone    = "done"
)

// notifyTimeout bounds one call of a notification, and every call has its own: an ending that asks
// several logins for a review is one call each, and a deadline over all of them would let the first
// call that stalls spend the whole budget and silence the logins behind it. It is shorter than a
// read of the line, because a run that ends on a stop is notified while the factory is stopping: the
// maintainer has to hear about that ending above all, and the stop must not wait long for a call.
//
// The logins of a ready run are asked one after another rather than at once, so a stop that lands on
// a GitHub that answers nothing waits one deadline per login. That is the slow side of the trade: the
// factory cannot tell whether GitHub reads two additions of one pull request's reviewers as a union,
// and two concurrent ones that do not would lose a reviewer — the very thing the call per login is
// there to prevent. Nothing is lost to the wait either way: a host that kills the factory first
// leaves the ending pending, and the next start makes it.
const notifyTimeout = 30 * time.Second

// maxNotifyReason is how much of a reason a comment carries. A blocker's text is written by a model
// for a person and is a paragraph or two; a reason far beyond that says more about what went wrong
// than the comment can, and the rest of it is in the run's log on the host.
const maxNotifyReason = 2000

// notifies says whether an ending owes the maintainer a word.
//
// ready and the three endings that wait for a person do. lost does not: another claimer owns the
// issue and this factory touched nothing. Neither does an interruption the factory answers itself —
// the one automatic resume — because nothing waits on the maintainer there; a second interruption
// has spent that resume and waits, exactly as a failure does ([ADR 0026]). The outcomes still to
// come, cancelled and quota, notify nobody: a cancelled run is the maintainer's own gesture and a
// run the quota stopped is queued again by itself.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func notifies(r Run, held holding) bool {
	switch r.Outcome {
	case outcomeReady, outcomeBlocked, outcomeFailed, outcomeTimeout:
		return true
	case outcomeInterrupted:
		return !held.resumes
	default:
		return false
	}
}

// NotifyOwed makes the notifications this start still owes: the endings a factory before it
// recorded and did not get to make, and the runs this start found active, whose ending is decided
// here like any other. It is called once, before the factory takes work.
func (f *Factory) NotifyOwed(ctx context.Context) {
	if !f.notifying() {
		return
	}
	for _, id := range f.runs.cutOff {
		if run, ok := f.runs.find(id); ok {
			f.owe(run)
		}
	}
	for _, record := range f.runs.list() {
		if record.Notified != notifyPending {
			continue
		}
		if ctx.Err() != nil {
			// The factory is stopping before it worked through what it owes. What is still owed
			// stays pending and the next start makes it, which is what the marker is there for: a
			// stop must not burn the endings somebody is waiting on.
			return
		}
		if run, ok := f.runs.find(record.ID); ok {
			f.deliver(run)
		}
	}
}

// notifyEnding tells the maintainer how a run ended, in the moment it ended.
func (f *Factory) notifyEnding(r *Run) {
	if !f.notifying() || !f.owe(r) {
		return
	}
	f.deliver(r)
}

// notifying says whether this factory notifies at all. Fake mode never does: it claims nothing, its
// queue is canned and there is no issue behind it to comment on. Neither does a factory with no
// logins configured, which says so once when it starts.
func (f *Factory) notifying() bool { return !f.fake && len(f.settings.Notify) > 0 }

// owe records on the run that its ending owes a notification, and says whether one is owed. The
// record is written before the call is made, so an ending is never lost to a host that is cut off
// while it notifies.
func (f *Factory) owe(r *Run) bool {
	if r.Notified != "" || !notifies(*r, holdings(f.runs.list())[r.key()]) {
		return false
	}
	f.runs.update(r, func() { r.Notified = notifyPending })
	return true
}

// deliver makes the notification and marks the ending done, whether GitHub took it or not: a
// notification that failed is a warning on the run and changes nothing else. It is not tried again,
// because a factory that started again after a week would otherwise comment on endings the
// maintainer has long since answered; what failed is in the run's warnings and in the journal.
//
// Its deadlines are its own and never the factory's: a run that ends because the factory is stopping
// is the one the maintainer has to hear about, and the factory's own context is cancelled by then.
func (f *Factory) deliver(r *Run) {
	if err := f.deliverTo(r); err != nil {
		f.warn(r, "the notification could not be made",
			fmt.Sprintf("this ending was not notified on GitHub in full: %v; the run itself is unchanged", err))
	}
	f.runs.update(r, func() { r.Notified = notifyDone })
}

// deliverTo is the notification itself: a review request for a run that ended ready on a pull
// request, a comment that mentions the logins for every other ending that owes a word.
//
// A ready run whose report named no pull request of this repository is commented on like an ending
// that waits, and not passed over: there is nothing to ask a review of, but the issue is still held
// by a factory that is done with it, and the run's reason — which says that the report named none —
// is what tells the maintainer where to look.
func (f *Factory) deliverTo(r *Run) error {
	if r.Outcome == outcomeReady && r.PullRequest != "" {
		// One call per login, and not one that names them all: GitHub refuses a whole review
		// request that carries a login it will not take — somebody who cannot review that
		// repository, or the author of the pull request, which the factory's own login can be —
		// and the maintainers it would have taken would then hear nothing of the run.
		var refused []error
		for _, who := range f.settings.Notify {
			if _, err := ghWithin(context.Background(), notifyTimeout, "pr", "edit", r.PullRequest, "--add-reviewer", who); err != nil {
				refused = append(refused, fmt.Errorf("%s was not asked for a review: %w", who, err))
			}
		}
		return errors.Join(refused...)
	}
	_, err := ghInput(context.Background(), notifyTimeout, notifyBody(*r, f.settings.Notify),
		"issue", "comment", strconv.Itoa(r.Issue), "--repo", r.Repository, "--body-file", "-")
	if err != nil {
		return fmt.Errorf("%s was not told of this run: %w", strings.Join(f.settings.Notify, ", "), err)
	}
	return nil
}

// notifyBody is the comment on the issue: who it is for, what became of the run, why, and what the
// maintainer can do about it. The gesture is the one [ADR 0026] gives them — the assignee off the
// issue hands it back to the factory — and it is worth saying, because an unattended run is read
// weeks after it ended and nothing else on the issue says how to answer it.
//
// It is only the gesture of an issue the factory still holds, so a run that ended holding nothing is
// told what it is instead of what it cannot do. Such a run has still left something behind more
// often than not — a claim that created the branch and failed at the assignee, a release resume that
// could not take the issue back over the worktree of the claim under it — and what that is, the run's
// own reason says a paragraph above; the comment does not say it a second time and must not say the
// opposite of it.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func notifyBody(r Run, logins []string) string {
	mentions := make([]string, 0, len(logins))
	for _, who := range logins {
		mentions = append(mentions, "@"+who)
	}
	said := []string{
		strings.Join(mentions, " "),
		fmt.Sprintf("The factory ended run %d of this issue as `%s`, and will not take the issue up again by itself.", r.ID, r.Outcome),
	}
	if reason := verbatim(r.Reason); reason != "" {
		said = append(said, reason)
	}
	if r.Holding {
		said = append(said, fmt.Sprintf("The branch `%s`, its worktree on the factory host and the assignee stay as they are. "+
			"Remove the assignee from this issue to hand it back to the factory: it takes the issue again and resumes the run in that worktree.", r.Branch))
	} else {
		said = append(said, "The factory does not hold this issue and has queued nothing more of it. "+
			"What the run left behind is what its reason says; removing an assignee hands nothing back, "+
			"because only an issue this factory still holds is taken up that way.")
	}
	return strings.Join(said, "\n\n") + "\n"
}

// verbatim is a text of the run's quoted into the comment as the text it is. The reason of a run
// carries what a model wrote — the blocker's text is the worker's own report, and the text of an
// issue can steer what a worker writes — so it is fenced rather than let into the comment as
// markdown: nothing in it becomes a heading, a link or a mention of somebody the factory was never
// asked to notify. The fence is longer than the longest run of backticks in the text, which is what
// keeps the text from ending it ([CommonMark 4.5]).
//
// [CommonMark 4.5]: https://spec.commonmark.org/0.31.2/#fenced-code-blocks
func verbatim(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if len(text) > maxNotifyReason {
		text = cut(text, maxNotifyReason) + "\n[truncated; the whole of it is in the run's log on the host]"
	}
	fence := strings.Repeat("`", max(3, longestRun(text, '`')+1))
	return fence + "text\n" + text + "\n" + fence
}

// longestRun is the longest run of one byte in a string.
func longestRun(s string, of byte) int {
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] != of {
			run = 0
			continue
		}
		run++
		if run > longest {
			longest = run
		}
	}
	return longest
}

// sayNobodyIsNotified reports at the start that this factory tells nobody how its runs end, which
// is what a configuration without logins means. It is said once, where everything else about the
// start is said, because a line per run for weeks is one nobody reads. Fake mode is silent about it:
// it notifies nobody by what it is, not by how it was configured.
func (f *Factory) sayNobodyIsNotified() {
	if f.fake || f.notifying() {
		return
	}
	log.Printf("notify names nobody: no run of this factory tells anybody how it ended; name the logins to notify in the configuration")
}
