package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// How the maintainer learns how a run ended, tested the way the rest of the factory is: the real
// binary, against the gh shim, with the notification read back from the calls it made and from the
// body it wrote to GitHub.

// The logins of the configuration below: nobody watches the host, so these two are told.
var maintainers = []string{"ada", "linus"}

// TestARunThatEndsReadyAsksTheMaintainersForAReviewOfItsPullRequest is the ready path end to end,
// and with it the rule that one ending is notified once however often the factory is started.
func TestARunThatEndsReadyAsksTheMaintainersForAReviewOfItsPullRequest(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	pullRequest := fmt.Sprintf("https://github.com/acme/edge-sensors/pull/%d", claimedIssue)
	gh.reviews(t, pullRequest, maintainers...)

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	settings := config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}, "notify": maintainers}
	f := gh.work(t, settings)
	run := f.ended(t, 1)
	if run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	f.notified(t, 1)
	for _, who := range maintainers {
		if made := gh.made(t, reviewCall(pullRequest, who)); made != 1 {
			t.Errorf("the factory asked %s for a review of %s %d times, want once for every login of the configuration", who, pullRequest, made)
		}
	}
	// A ready run is a pull request to read, not an issue to answer: nothing is said on the issue.
	if said := gh.commented(t, "acme/edge-sensors", claimedIssue); said != "" {
		t.Errorf("the factory commented on the issue of a ready run:\n%s", said)
	}

	// And the ending is notified once: a factory started again on this data directory reads that the
	// run is done with and asks for no second review.
	f.stop(t, syscall.SIGTERM)
	settings["listen"] = freeAddress(t)
	again := gh.work(t, settings)
	again.queue(t, 0) // a poll of the line, so the start is over and whatever it owed is made
	for _, who := range maintainers {
		if made := gh.made(t, reviewCall(pullRequest, who)); made != 1 {
			t.Errorf("the factory asked %s for a review %d times over two starts, want once; the second start's log:\n%s", who, made, again.output(t))
		}
	}
}

// A review request GitHub refuses is refused for one login — somebody who cannot review that
// repository, the author of the pull request — and the maintainers it takes still hear of the run.
func TestAReviewRequestOneLoginIsRefusedStillReachesTheOthers(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	pullRequest := fmt.Sprintf("https://github.com/acme/edge-sensors/pull/%d", claimedIssue)
	gh.reviews(t, pullRequest, maintainers...)
	gh.fail(t, "pr edit * --add-reviewer ada") // the one login GitHub will not take

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}, "notify": maintainers})
	if run := f.ended(t, 1); run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	f.notified(t, 1)
	if made := gh.made(t, reviewCall(pullRequest, "linus")); made != 1 {
		t.Errorf("the factory asked linus for a review %d times, want once although ada was refused", made)
	}
	run := f.ended(t, 1)
	warned := strings.Join(run.Warnings, "\n")
	if !strings.Contains(warned, "ada") || !strings.Contains(warned, "was not asked for a review") {
		t.Errorf("the run carries the warnings %q, want one naming the login GitHub refused", warned)
	}
}

// A review request GitHub takes and never answers spends the deadline of that one call and no more:
// the logins behind it are asked all the same. A deadline over the whole delivery would let the
// first call that stalls silence every login after it, which is the refusal above in its other form.
func TestAReviewRequestThatStallsStillReachesTheLoginsBehindIt(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	pullRequest := fmt.Sprintf("https://github.com/acme/edge-sensors/pull/%d", claimedIssue)
	gh.reviews(t, pullRequest, maintainers...)
	gh.stall(t, "pr edit * --add-reviewer ada") // the first login's call is taken and never answered

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}, "notify": maintainers})
	if run := f.ended(t, 1); run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	// ada's call holds its own deadline and nothing else: linus is asked once it is over.
	f.eventually(t, notifyTimeout+30*time.Second, "linus to be asked for a review while ada's call stalls", func() bool {
		return gh.made(t, reviewCall(pullRequest, "linus")) == 1
	})
}

// A run that ends ready and names no pull request has nothing to ask a review of, and is an issue
// the factory still holds and is done with: the maintainer hears of it on the issue, like every
// other ending that waits for a person.
func TestAReadyRunThatNamesNoPullRequestIsSaidOnTheIssue(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReportsReadyWithout(t, "the work is done and pushed; the pull request is on the fork")
	gh.comments(t, "acme/edge-sensors", claimedIssue)

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}, "notify": maintainers})
	run := f.ended(t, 1)
	if run.Outcome != "ready" || run.PullRequest != "" {
		t.Fatalf("the run ended as %q with the pull request %q, want ready with none; the factory's log:\n%s",
			run.Outcome, run.PullRequest, f.output(t))
	}
	f.notified(t, 1)

	said := gh.commented(t, "acme/edge-sensors", claimedIssue)
	for _, want := range []string{
		"@ada @linus",           // the maintainers of the configuration, so GitHub tells them
		"`ready`",               // what became of the run
		"names no pull request", // why there is nothing to review
		"Remove the assignee",   // and the one gesture that hands the issue back ([ADR 0026])
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the comment on the issue is %q, want %q in it", said, want)
		}
	}
	if asked := gh.asked(t, "pr edit"); asked != 0 {
		t.Errorf("the factory asked for a review %d times of a run that names no pull request, want none", asked)
	}
}

// TestARunThatWaitsForAPersonCommentsOnTheIssueWithTheReasonAndTheReleaseGesture is the other path
// end to end: a blocked run, whose reason is the worker's own report.
func TestARunThatWaitsForAPersonCommentsOnTheIssueWithTheReasonAndTheReleaseGesture(t *testing.T) {
	const blocker = "the brief contradicts ADR 0012: it asks for a second control surface."
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerBlocks(t, blocker)
	gh.comments(t, "acme/edge-sensors", claimedIssue)

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}, "notify": maintainers})
	run := f.ended(t, 1)
	if run.Outcome != "blocked" {
		t.Fatalf("the run ended as %q (%s), want blocked; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	f.notified(t, 1)

	said := gh.commented(t, "acme/edge-sensors", claimedIssue)
	for _, want := range []string{
		"@ada @linus",  // the maintainers of the configuration, so GitHub tells them
		"`blocked`",    // what became of the run
		blocker,        // why, in the worker's own words
		claimedBranch,  // what is left on the host and on the remote
		"the assignee", // and the one gesture that hands the issue back ([ADR 0026])
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the comment on the issue is %q, want %q in it", said, want)
		}
	}
	if made := gh.made(t, commentCall("acme/edge-sensors", claimedIssue)); made != 1 {
		t.Errorf("the factory commented on the issue %d times, want once", made)
	}
	// A blocked run has no pull request, and nobody is asked to review one.
	if asked := gh.asked(t, "pr edit"); asked != 0 {
		t.Errorf("the factory asked for a review %d times of a run that ended blocked, want none", asked)
	}
}

// A notification is one small write that may fail like any other call to GitHub: the run keeps its
// outcome, the failure is a warning on it, and the factory works on.
func TestANotificationThatFailsIsAWarningOnTheRunAndChangesNothingElse(t *testing.T) {
	const next, nextTitle = 121, "Document the calibration procedure"
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", next, "factory-bot")
	gh.workerBlocks(t, "the calibration file is nowhere on the host")
	gh.comments(t, "acme/edge-sensors", claimedIssue)
	gh.comments(t, "acme/edge-sensors", next)
	now := time.Now().UTC()
	gh.issues(t, "acme/edge-sensors",
		openIssue(claimedIssue, claimedTitle, now.Add(-72*time.Hour)),
		openIssue(next, nextTitle, now.Add(-72*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", claimedIssue, labeled("factory", now.Add(-6*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", next, labeled("factory", now.Add(-5*time.Hour)))
	gh.fail(t, "issue comment*") // GitHub takes the claim and the work, and refuses the comment

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}, "notify": maintainers})
	run := f.ended(t, 1)
	if run.Outcome != "blocked" || run.Issue != claimedIssue {
		t.Fatalf("run 1 works #%d and ended as %q, want #%d blocked; the factory's log:\n%s",
			run.Issue, run.Outcome, claimedIssue, f.output(t))
	}
	f.notified(t, 1)
	run = f.ended(t, 1)
	if run.Outcome != "blocked" {
		t.Errorf("the run ended as %q after the notification failed, want the outcome it had", run.Outcome)
	}
	warned := strings.Join(run.Warnings, "\n")
	if !strings.Contains(warned, "was not notified") {
		t.Errorf("the run carries the warnings %q, want one saying the notification could not be made", warned)
	}
	// It is not tried again either: the warning is where a failed notification is read.
	if made := gh.made(t, commentCall("acme/edge-sensors", claimedIssue)); made != 1 {
		t.Errorf("the factory tried to comment %d times, want the one attempt", made)
	}
	// And the line goes on: the next issue is claimed and worked as if nothing had happened.
	worked := f.ended(t, 2)
	if worked.Issue != next || worked.Outcome != "blocked" {
		t.Fatalf("run 2 works #%d and ended as %q, want #%d worked; the factory's log:\n%s",
			worked.Issue, worked.Outcome, next, f.output(t))
	}
}

// The one interruption the factory answers itself is the one it says nothing about: nobody is
// waiting on the maintainer there. The second has spent that resume and waits, so it is notified
// like a failure ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func TestOnlyAnInterruptionTheFactoryDoesNotResumeItselfIsNotified(t *testing.T) {
	began := time.Now().UTC().Add(-2 * time.Hour)
	// The run this start finds active, which it records as interrupted while it opens the data
	// directory: a factory the host killed left it behind.
	active := func(id int, signal string) Run {
		r := record(id, claimedIssue, claimedTitle, signal, "", true, began, began)
		r.State, r.Outcome, r.EndedAt = "running", "", nil
		return r
	}
	for _, c := range []struct {
		name     string
		runs     []Run
		notified bool
	}{
		{"the first interruption of an issue, which the factory resumes by itself", []Run{
			active(1, signalRouted)}, false},
		{"the second interruption, which waits for a person", []Run{
			record(1, claimedIssue, claimedTitle, signalRouted, outcomeInterrupted, true, began, began.Add(time.Minute)),
			active(2, signalInterruption)}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			gh := newGhShim(t)
			gh.remote(t, "acme/edge-sensors")
			gh.loggedInAs(t, "factory-bot")
			gh.issues(t, "acme/edge-sensors")
			gh.comments(t, "acme/edge-sensors", claimedIssue)
			data := filepath.Join(t.TempDir(), "data")
			gh.cloneInto(t, data, "acme/edge-sensors")
			records(t, data, c.runs...)

			// Paused: what is read here is the ending the start itself records, not the run it queues.
			f := gh.start(t, config{"poll": "50ms", "data_dir": data,
				"repositories": []string{"acme/edge-sensors"}, "notify": maintainers})
			cutOff := len(c.runs)
			if ended := f.ended(t, cutOff); ended.Outcome != outcomeInterrupted {
				t.Fatalf("run %d ended as %q, want interrupted: the start found it active", cutOff, ended.Outcome)
			}
			if c.notified {
				f.notified(t, cutOff)
				if said := gh.commented(t, "acme/edge-sensors", claimedIssue); !strings.Contains(said, "`interrupted`") {
					t.Errorf("the comment on the issue is %q, want the interruption it says nothing about otherwise", said)
				}
				return
			}
			f.never(t, 2*time.Second, "the factory notified an interruption it resumes by itself",
				func() bool { return gh.commented(t, "acme/edge-sensors", claimedIssue) != "" })
		})
	}
}

// An ending the factory recorded and did not get to notify — the host lost power between the two —
// is notified on the next start, and by that start alone.
func TestAnEndingThatWasRecordedAndNotNotifiedIsNotifiedOnTheNextStart(t *testing.T) {
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	gh.issues(t, "acme/edge-sensors")
	gh.comments(t, "acme/edge-sensors", claimedIssue)
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	began := time.Now().UTC().Add(-2 * time.Hour)
	owed := record(1, claimedIssue, claimedTitle, signalRouted, outcomeFailed, true, began, began.Add(time.Minute))
	owed.Reason = "the session ended in an error (exit 1): the gate did not pass"
	owed.Notified = notifyPending
	records(t, data, owed)

	settings := config{"poll": "50ms", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}, "notify": maintainers}
	f := gh.start(t, settings)
	f.notified(t, 1)
	said := gh.commented(t, "acme/edge-sensors", claimedIssue)
	if !strings.Contains(said, "`failed`") || !strings.Contains(said, owed.Reason) {
		t.Errorf("the comment on the issue is %q, want the outcome and the reason the record carries", said)
	}

	// And the start after it says nothing: the record is done with.
	f.stop(t, syscall.SIGTERM)
	settings["listen"] = freeAddress(t)
	again := gh.start(t, settings)
	again.queue(t, 0)
	if made := gh.made(t, commentCall("acme/edge-sensors", claimedIssue)); made != 1 {
		t.Errorf("the factory commented %d times over two starts, want once; the second start's log:\n%s", made, again.output(t))
	}
}

// A factory that is stopped before it has worked through what it owes keeps the rest of it: the
// endings stay pending and the next start makes them. A stop must not burn the notification
// somebody is waiting on, which is what the pending mark is there for.
func TestAStopBeforeTheOwedNotificationsAreMadeLeavesThemPending(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	began := time.Now().UTC().Add(-2 * time.Hour)
	owed := record(1, claimedIssue, claimedTitle, signalRouted, outcomeFailed, true, began, began.Add(time.Minute))
	owed.Notified = notifyPending
	records(t, data, owed)

	f, err := New(Settings{DataDir: data, Notify: maintainers}, false)
	if err != nil {
		t.Fatal(err)
	}
	stopping, stop := context.WithCancel(context.Background())
	stop() // the factory was signalled before it could work through what it owes
	f.NotifyOwed(stopping)

	store, err := OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	run, ok := store.find(1)
	if !ok {
		t.Fatal("the record of run 1 is gone")
	}
	if run.Notified != notifyPending {
		t.Errorf("the ending the stop cut off says %q, want it still pending for the next start", run.Notified)
	}
}

// Without logins to notify the factory tells nobody anything, and says that once where everything
// else about its start is said.
func TestWithoutLoginsToNotifyTheFactoryNotifiesNobodyAndSaysSoAtItsStart(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	if run := f.ended(t, 1); run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if said := strings.Count(f.output(t), "notify names nobody"); said != 1 {
		t.Errorf("the factory said that it notifies nobody %d times, want once at its start; its log:\n%s", said, f.output(t))
	}
	for _, call := range []string{"pr edit", "issue comment"} {
		if asked := gh.asked(t, call); asked != 0 {
			t.Errorf("the factory made `gh %s` %d times without a login to notify, want none", call, asked)
		}
	}
	// Nothing is recorded as owed either, so naming a login later does not comment on what is over.
	var run apiRun
	f.get(t, "/api/runs/1", &run)
	if run.Notified != "" {
		t.Errorf("the run says it owes the notification %q, want nothing owed", run.Notified)
	}
}

// Which endings notify at all, in every shape a run can reach one in. The two paths themselves are
// driven end to end above; what is read here is the rule that decides whether a given ending is one
// the maintainer has to hear about.
func TestTheEndingsThatNotifyAndTheOnesThatDoNot(t *testing.T) {
	ended := time.Now().UTC()
	run := func(id int, signal, outcome string, holding bool) Run {
		return record(id, claimedIssue, claimedTitle, signal, outcome, holding, ended.Add(-time.Hour), ended)
	}
	for _, c := range []struct {
		name     string
		runs     []Run
		notifies bool
	}{
		{"a run that ended ready", []Run{run(1, signalRouted, outcomeReady, true)}, true},
		{"a run that ended blocked", []Run{run(1, signalRouted, outcomeBlocked, true)}, true},
		{"a run that failed", []Run{run(1, signalRouted, outcomeFailed, true)}, true},
		{"a run that ran into the deadline", []Run{run(1, signalRouted, outcomeTimeout, true)}, true},
		// The race another claimer won: this factory touched nothing of the issue ([ADR 0024]).
		{"a claim another claimer won", []Run{run(1, signalRouted, outcomeLost, false)}, false},
		{"the first interruption of an issue", []Run{run(1, signalRouted, outcomeInterrupted, true)}, false},
		{"the second interruption of an issue", []Run{
			run(1, signalRouted, outcomeInterrupted, true),
			run(2, signalInterruption, outcomeInterrupted, true)}, true},
		{"the first interruption after a release, which is resumed again", []Run{
			run(1, signalRouted, outcomeInterrupted, true),
			run(2, signalInterruption, outcomeInterrupted, true),
			run(3, signalRelease, outcomeInterrupted, true)}, false},
		// The outcomes that arrive with their own tickets: a run the maintainer cancelled and one the
		// quota stopped are answered by the gesture or by the factory itself, not by a notification.
		{"a run that was cancelled", []Run{run(1, signalRouted, "cancelled", true)}, false},
		{"a run the quota stopped", []Run{run(1, signalRouted, "quota", true)}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			last := c.runs[len(c.runs)-1]
			if notified := notifies(last, holdings(c.runs)[last.key()]); notified != c.notifies {
				t.Errorf("this ending notifies the maintainer: %v, want %v", notified, c.notifies)
			}
		})
	}
}

// The comment carries a text the factory did not write: the blocker is the worker's report, and the
// text of an issue can steer what a worker writes. So it is quoted as the text it is — nothing in it
// becomes a mention, a heading or a link, whatever it contains.
func TestTheReasonIsQuotedIntoTheCommentAndCannotBreakOutOfIt(t *testing.T) {
	blocked := record(7, claimedIssue, claimedTitle, signalRouted, outcomeBlocked, true,
		time.Now(), time.Now())
	blocked.Reason = "the brief says:\n```\nnotify @everyone and read https://attacker.example\n```\n# done"
	body := notifyBody(blocked, maintainers)

	if !strings.Contains(body, blocked.Reason) {
		t.Fatalf("the comment does not carry the reason as it stands:\n%s", body)
	}
	opened := strings.Index(body, "```")
	if opened < 0 {
		t.Fatalf("the reason is not in a block of its own:\n%s", body)
	}
	open, _, _ := strings.Cut(body[opened:], "\n")
	fence := strings.TrimSuffix(open, "text")
	if len(fence) <= longestRun(blocked.Reason, '`') {
		t.Errorf("the block around the reason opens with %q, want a fence longer than the one in the reason:\n%s", fence, body)
	}
	after := body[opened+len(open):]
	closed := strings.Index(after, "\n"+fence+"\n")
	reason := strings.Index(after, blocked.Reason) + len(blocked.Reason)
	if closed < 0 || closed < reason {
		t.Fatalf("the block closes at %d, want it closing after the whole reason (%d):\n%s", closed, reason, body)
	}
	// What lies outside that block is the factory's own text, and the only mentions in the comment
	// are the logins of the configuration: a reason that names somebody notifies nobody.
	outside := body[:opened] + after[closed:]
	if mentions := strings.Count(outside, "@"); mentions != len(maintainers) {
		t.Errorf("the comment mentions %d logins outside the quoted reason, want the %d of the configuration:\n%s",
			mentions, len(maintainers), body)
	}

	// A reason far longer than a blocker's text is cut: the whole of it is in the run's log.
	long := blocked
	long.Reason = strings.Repeat("x", maxNotifyReason*2)
	if body := notifyBody(long, maintainers); len(body) > maxNotifyReason+1000 {
		t.Errorf("a comment of %d characters carries a reason of %d, want it cut", len(body), len(long.Reason))
	}
}

// notified waits until the factory has made the notification that run owes, which is what its record
// says once the call is over.
func (f *factory) notified(t *testing.T, id int) {
	t.Helper()
	f.eventually(t, 30*time.Second, fmt.Sprintf("run %d to be notified", id), func() bool {
		var run apiRun
		f.get(t, fmt.Sprintf("/api/runs/%d", id), &run)
		return run.Notified == notifyDone
	})
}
