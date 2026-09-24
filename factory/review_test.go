package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The follow-up run, tested the way the rest of the factory is: the real binary, against the gh shim
// and a local bare repository that stands in for the remote. Requesting changes is a gesture on
// GitHub, so it is driven through that shim, and what the factory answers with is read from the run
// records, from how the worker was started and from the line it serves.

// TestAReviewThatAsksForChangesRunsTheWorkerOnItInTheSameWorktree drives the whole signal: a run
// ends ready and leaves a pull request, somebody with write access asks for changes on it, and the
// factory runs its address-reviews stage in the worktree of the claim ([ADR 0023]).
//
// [ADR 0023]: ../docs/adr/0023-github-is-the-only-control-surface-of-the-factory.md
func TestAReviewThatAsksForChangesRunsTheWorkerOnItInTheSameWorktree(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	// The pull request that run is going to open, with nobody having reviewed it yet.
	gh.pullRequestIs(t, "acme/edge-sensors", claimedIssue, "open")
	gh.reviews(t, "acme/edge-sensors", claimedIssue)
	gh.mayWrite(t, "acme/edge-sensors", "maintainer", true)
	// The address-reviews session of the follow-up run, which answers the review summary.
	gh.answer(t, pullCommented, pullOfTheClaim+"#issuecomment-5\n")
	gh.env = append(gh.env, `CLAUDE_SHIM_THEN_RESULT={"outcome":"complete","summary":"logged","answer":"Every retry is logged now.","fixed":["log every retry"]}`)

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	first := f.ended(t, 1)
	if first.Outcome != "ready" || first.PullRequest == "" {
		t.Fatalf("the first run ended as %q (%s) with the pull request %q, want a ready run that opened one; the factory's log:\n%s",
			first.Outcome, first.Reason, first.PullRequest, f.output(t))
	}
	if first.Kind != "first" {
		t.Errorf("the run that claimed the issue is of the kind %q, want a first run", first.Kind)
	}
	// What that run left in the worktree, which is what the follow-up run answers the review on.
	worktree := filepath.Join(clone, ".claude", "worktrees", "feat-104-retry-the-upload-when-the-broker-drops")
	writeFile(t, filepath.Join(worktree, "fix.txt"), "the work of the first session\n")
	gh.git(t, worktree, "add", "fix.txt")
	gh.git(t, worktree, "commit", "-q", "-m", "fix: the first session's commit")
	committed := gh.git(t, worktree, "rev-parse", "HEAD")

	// The gesture: a maintainer requests changes on the pull request, after that run ended.
	requestedAt := after(*first.EndedAt)
	gesture := review(7001, "maintainer", "CHANGES_REQUESTED", requestedAt)
	gesture["html_url"] = pullOfTheClaim + "#pullrequestreview-7001"
	gesture["body"] = "Log every retry."
	gh.reviews(t, "acme/edge-sensors", claimedIssue, gesture)

	// The follow-up run answers the review, pushes what the worktree holds, and waits on CI until the
	// pull request shows that push; the review stands on GitHub, answered.
	f.sawIn(t, 2, "answered the review summaries")
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{head: committed, reviews: []map[string]any{gesture}})
	second := f.ended(t, 2)
	if second.Signal != signalChangesRequested || second.Issue != claimedIssue {
		t.Fatalf("run 2 works #%d on the signal %q, want #%d on a review that asks for changes; the factory's log:\n%s",
			second.Issue, second.Signal, claimedIssue, f.output(t))
	}
	if second.Kind != "follow-up" {
		t.Errorf("the run that answers the review is of the kind %q, want a follow-up run", second.Kind)
	}
	if second.Outcome != outcomeReady {
		t.Errorf("the follow-up run ended as %q (%s), want ready again", second.Outcome, second.Reason)
	}
	// A follow-up run stands at the address-reviews stage from its first moment and then waits on CI,
	// where a first run stands at implement and goes through the pr stage into the ci stage the factory
	// runs.
	if !equal(first.Stages, []string{"implement", "pr", "ci"}) || !equal(second.Stages, []string{"address-reviews", "ci"}) {
		t.Errorf("the two runs went through the stages %v and %v, want [implement pr ci] and [address-reviews ci]", first.Stages, second.Stages)
	}
	// The review is a new mandate on the pull request: the follow-up run's count of repair rounds
	// starts at none, and answering the review it was queued for is not one of them.
	if second.RepairRounds != 0 {
		t.Errorf("the follow-up run took %d repair rounds, want none: the round that answers its review is the mandate itself", second.RepairRounds)
	}
	if titles := factoryTitles(second, "answering the review", "repair round", "answered the review summaries", "ci: green"); !equal(titles,
		[]string{"answering the review on " + pullOfTheClaim, "answered the review summaries on " + pullOfTheClaim, "ci: green"}) {
		t.Errorf("the follow-up run said %v, want that it answered the review and went green without a repair round", titles)
	}
	if said := gh.wrote(t, pullCommented); said != "Every retry is logged now." {
		t.Errorf("the factory answered the review with %q, want the session's answer", said)
	}
	if !second.SignalAt.Equal(requestedAt) {
		t.Errorf("the follow-up run stands for a review submitted at %s, want the one at %s", second.SignalAt, requestedAt)
	}
	if second.Branch != claimedBranch || second.Worktree != first.Worktree || !second.Holding {
		t.Errorf("the follow-up run is %s in %s (holding=%v), want the branch and the worktree of the claim (%s in %s)",
			second.Branch, second.Worktree, second.Holding, first.Branch, first.Worktree)
	}
	// It claims nothing and assigns nothing: the claim that holds the issue stands, and this run is
	// under it.
	if made := gh.asked(t, "api --method POST repos/acme/edge-sensors/git/refs"); made != 1 {
		t.Errorf("the factory created a reference %d times, want once: a follow-up run is under the claim that stands", made)
	}
	assigned := fmt.Sprintf("issue edit %d --repo acme/edge-sensors --add-assignee factory-bot", claimedIssue)
	if made := gh.made(t, assigned); made != 1 {
		t.Errorf("the factory made `gh %s` %d times, want once: the issue is this host's already", assigned, made)
	}

	// The session itself: the same worktree, on the commit the work there had reached, briefed with the
	// review by the factory rather than started on a skill of the worker's pipeline.
	workers := gh.workers(t)
	if len(workers) != 2 {
		t.Fatalf("the factory started %d workers, want one per run", len(workers))
	}
	follower := workers[1]
	if follower.cwd != resolved(t, worktree) {
		t.Errorf("the follow-up worker ran in %s, want the worktree of the claim %s", follower.cwd, resolved(t, worktree))
	}
	if follower.branch != claimedBranch || follower.head != committed {
		t.Errorf("the follow-up worker ran on %s at %s, want %s at the commit the worktree holds (%s)",
			follower.branch, follower.head, claimedBranch, committed)
	}
	if follower.started("-p", "/worker:address-reviews") {
		t.Errorf("the follow-up worker was started on the worker's address-reviews skill, want the factory's own brief: %v", follower.args)
	}
	brief := strings.Join(addressBriefs(second), "\n")
	if !strings.Contains(brief, "Log every retry.") || !strings.Contains(brief, pullOfTheClaim+"#pullrequestreview-7001") {
		t.Errorf("the address-reviews session's brief does not carry the review it answers:\n%s", brief)
	}

	// One review is one run, however many polls read it: the review stands on GitHub unanswered as far
	// as the shim is concerned, and the factory polls twenty times a second.
	f.never(t, 3*time.Second, "the factory started a third run, so it answered the one review twice",
		func() bool { return !f.missing(t, 3) })
	if read := gh.made(t, "api --paginate "+reviewsRequest("acme/edge-sensors", claimedIssue)); read < 3 {
		t.Errorf("the reviews were read %d times, want a reading per poll: one run out of many polls is what is being proved", read)
	}
	var line apiLine
	f.get(t, "/api/line", &line)
	if len(line.Queue) != 0 || len(line.Now) != 0 {
		t.Errorf("the factory queues %v and runs %d after the review was answered, want an idle line", keys(line.Queue), len(line.Now))
	}
}

// What a poll must not read as a review that asks for changes: one from somebody without write
// access, a plain comment, a dismissed review, and one submitted before the last run of the issue
// ended. The maintainer's own then fills the line, which is what says the fixture was sound and the
// silence before it was the rule at work, and merging the pull request empties it again — after
// which the factory stops reading that pull request at all, because it holds every issue it ever
// claimed and a call per poll for each of them is a rate limit spent on work that is over.
//
// The factory is paused throughout, so what is read is the line alone.
func TestAReviewQueuesNothingUnlessAWriterAskedForChangesOnTheOpenPullRequest(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	gh.issues(t, "acme/edge-sensors")
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	began := time.Now().UTC().Add(-2 * time.Hour)
	ended := began.Add(30 * time.Minute)
	held := record(1, claimedIssue, claimedTitle, signalRouted, outcomeReady, true, began, ended)
	held.PullRequest = fmt.Sprintf("https://github.com/acme/edge-sensors/pull/%d", claimedIssue)
	records(t, data, held)

	gh.pullRequestIs(t, "acme/edge-sensors", claimedIssue, "open")
	gh.mayWrite(t, "acme/edge-sensors", "maintainer", true)
	gh.mayWrite(t, "acme/edge-sensors", "stranger", false)
	ignored := []map[string]any{
		// Before the run ended: what it asked for is the session's that was running, not a new run's.
		review(1, "maintainer", "CHANGES_REQUESTED", ended.Add(-5*time.Minute)),
		// Anybody may review a pull request of a public repository; a run of the factory is not
		// something a stranger starts.
		review(2, "stranger", "CHANGES_REQUESTED", ended.Add(2*time.Minute)),
		// A plain comment and a review that was dismissed ask for nothing, however new they are.
		review(3, "maintainer", "COMMENTED", ended.Add(3*time.Minute)),
		review(4, "maintainer", "DISMISSED", ended.Add(4*time.Minute)),
	}
	gh.reviews(t, "acme/edge-sensors", claimedIssue, ignored...)

	f := gh.start(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	f.queue(t, 0)
	f.never(t, 3*time.Second, "the factory queued a follow-up run on a review that asks nothing of it",
		func() bool { return len(f.line(t).Queue) > 0 })

	// The maintainer asks for changes on the same pull request: the line fills with it.
	requestedAt := ended.Add(10 * time.Minute).Truncate(time.Second) // GitHub times a review to the second
	// A newer review by a login GitHub answers nothing for — an app, a deleted account — is beside it:
	// the maintainer's gesture stands all the same. The shim has no answer for that permission, which
	// is a request that fails, so an author whose access cannot be read must not take the whole
	// reading down with them.
	gh.reviews(t, "acme/edge-sensors", claimedIssue,
		append(ignored, review(5, "maintainer", "CHANGES_REQUESTED", requestedAt),
			review(6, "ghost", "CHANGES_REQUESTED", ended.Add(20*time.Minute)))...)
	head := f.queue(t, 1)[0]
	if head.Number != claimedIssue || head.Signal != signalChangesRequested {
		t.Fatalf("the line opens with #%d on the signal %q, want #%d on a review that asks for changes",
			head.Number, head.Signal, claimedIssue)
	}
	if !head.SignalAt.Equal(requestedAt) {
		t.Errorf("the review stands at %s, want the time it was submitted %s", head.SignalAt, requestedAt)
	}

	// The pull request is merged with the review still on it: what it asked for is nobody's to answer
	// now, and the line empties.
	gh.pullRequestIs(t, "acme/edge-sensors", claimedIssue, "closed")
	f.queue(t, 0)
	// And that pull request is not read again: the issue is held for as long as the records say so,
	// and asking GitHub about it once a poll forever is what must not happen.
	asked := "api " + pullRequestRequest("acme/edge-sensors", claimedIssue) + " --jq .state"
	read := gh.made(t, asked)
	polls := "api " + issuesRequest("acme/edge-sensors", "factory")
	seen := gh.made(t, polls)
	f.eventually(t, 10*time.Second, "several more polls", func() bool { return gh.made(t, polls) >= seen+10 })
	if again := gh.made(t, asked); again != read {
		t.Errorf("the merged pull request was read %d more times over %d polls, want none",
			again-read, gh.made(t, polls)-seen)
	}
}

// What one reviewer says about a pull request is their latest review, not every review they ever
// submitted. GitHub leaves an older entry in the list with the state it carried, so a maintainer who
// asks for changes and then approves without dismissing the first review leaves a CHANGES_REQUESTED
// entry behind that asks for nothing any more; a comment after it states nothing and leaves the
// approval standing. The same maintainer asking again is what says the fixture was sound.
func TestOnlyTheLatestReviewOfAReviewerAsksForChanges(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	gh.issues(t, "acme/edge-sensors")
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	began := time.Now().UTC().Add(-2 * time.Hour)
	ended := began.Add(30 * time.Minute)
	held := record(1, claimedIssue, claimedTitle, signalRouted, outcomeReady, true, began, ended)
	held.PullRequest = fmt.Sprintf("https://github.com/acme/edge-sensors/pull/%d", claimedIssue)
	records(t, data, held)

	gh.pullRequestIs(t, "acme/edge-sensors", claimedIssue, "open")
	gh.mayWrite(t, "acme/edge-sensors", "maintainer", true)
	withdrawn := []map[string]any{
		review(1, "maintainer", "CHANGES_REQUESTED", ended.Add(10*time.Minute)),
		review(2, "maintainer", "APPROVED", ended.Add(12*time.Minute)),
		review(3, "maintainer", "COMMENTED", ended.Add(14*time.Minute)),
	}
	gh.reviews(t, "acme/edge-sensors", claimedIssue, withdrawn...)

	f := gh.start(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	f.queue(t, 0)
	f.never(t, 3*time.Second, "the factory queued a follow-up run on an objection its reviewer withdrew",
		func() bool { return len(f.line(t).Queue) > 0 })

	// The same maintainer asks again, after the approval: that is what a run is queued on.
	againAt := ended.Add(20 * time.Minute).Truncate(time.Second)
	gh.reviews(t, "acme/edge-sensors", claimedIssue,
		append(withdrawn, review(4, "maintainer", "CHANGES_REQUESTED", againAt))...)
	head := f.queue(t, 1)[0]
	if head.Signal != signalChangesRequested || !head.SignalAt.Equal(againAt) {
		t.Errorf("the line opens on %q at %s, want a review that asks for changes at %s",
			head.Signal, head.SignalAt, againAt)
	}
}

// A pull request state the factory does not understand is not read as a closed one: doing so would
// end the watch of an open pull request for the life of the process, and without a word.
func TestAPullRequestStateTheFactoryCannotReadKeepsItWatched(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	gh.issues(t, "acme/edge-sensors")
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	began := time.Now().UTC().Add(-2 * time.Hour)
	ended := began.Add(30 * time.Minute)
	held := record(1, claimedIssue, claimedTitle, signalRouted, outcomeReady, true, began, ended)
	held.PullRequest = fmt.Sprintf("https://github.com/acme/edge-sensors/pull/%d", claimedIssue)
	records(t, data, held)

	requestedAt := ended.Add(10 * time.Minute).Truncate(time.Second)
	gh.mayWrite(t, "acme/edge-sensors", "maintainer", true)
	gh.reviews(t, "acme/edge-sensors", claimedIssue,
		review(1, "maintainer", "CHANGES_REQUESTED", requestedAt))
	gh.pullRequestIs(t, "acme/edge-sensors", claimedIssue, "") // neither open nor closed

	f := gh.start(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	f.queue(t, 0)
	f.never(t, 3*time.Second, "the factory queued a run on a pull request whose state it could not read",
		func() bool { return len(f.line(t).Queue) > 0 })

	// GitHub answers as itself again, and the review is read: the watch was never given up.
	gh.pullRequestIs(t, "acme/edge-sensors", claimedIssue, "open")
	if head := f.queue(t, 1)[0]; !head.SignalAt.Equal(requestedAt) {
		t.Errorf("the line opens on a signal at %s, want the review at %s", head.SignalAt, requestedAt)
	}
}

// Where a follow-up run stands: with the work the factory resumes, before every issue nobody has
// worked yet, and among that work by the time of its signal.
func TestAFollowUpRunStandsWithTheResumedWorkBeforeAnyNewIssue(t *testing.T) {
	t.Parallel()
	const reviewed, next = 112, 121
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	opened := time.Now().UTC().Add(-72 * time.Hour)
	began := time.Now().UTC().Add(-2 * time.Hour)
	interruptedAt := began.Add(30 * time.Minute)
	// After the interruption, so it stands behind it in the line, and on the second GitHub times it to.
	requestedAt := began.Add(50 * time.Minute).Truncate(time.Second)

	// 104 was interrupted and has its one automatic resume; 112 was worked, is held, and its pull
	// request has just been reviewed; 121 is new and was routed long before either.
	held := record(2, reviewed, "Replace the CSV parser", signalRouted, outcomeReady, true, began, began.Add(20*time.Minute))
	held.PullRequest = fmt.Sprintf("https://github.com/acme/edge-sensors/pull/%d", reviewed)
	records(t, data,
		record(1, claimedIssue, claimedTitle, signalRouted, outcomeInterrupted, true, began, interruptedAt),
		held)
	gh.issues(t, "acme/edge-sensors", openIssue(next, "Document the calibration procedure", opened))
	gh.timeline(t, "acme/edge-sensors", next, labeled("factory", opened))
	gh.pullRequestIs(t, "acme/edge-sensors", reviewed, "open")
	gh.mayWrite(t, "acme/edge-sensors", "maintainer", true)
	gh.reviews(t, "acme/edge-sensors", reviewed, review(1, "maintainer", "CHANGES_REQUESTED", requestedAt))

	f := gh.start(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	queue := f.queue(t, 3)
	want := []string{"acme/edge-sensors#104", "acme/edge-sensors#112", "acme/edge-sensors#121"}
	if !equal(keys(queue), want) {
		t.Fatalf("the line is %v, want %v: the interruption, then the review, then the new issue", keys(queue), want)
	}
	for position, signal := range []string{signalInterruption, signalChangesRequested, signalRouted} {
		if queue[position].Signal != signal {
			t.Errorf("#%d stands in the line on the signal %q, want %q", queue[position].Number, queue[position].Signal, signal)
		}
	}
	if !queue[1].SignalAt.Equal(requestedAt) {
		t.Errorf("the reviewed #%d stands at %s, want the time the review was submitted %s",
			reviewed, queue[1].SignalAt, requestedAt)
	}
}

// The rule a poll reads a review by, in the shapes it can meet one in. The gesture is driven end to
// end above; what is read here is what the records of an issue say about a review the last poll
// found — above all that a review is answered once, because the poll that queued it repeats for as
// long as the follow-up run takes to be recorded.
func TestAReviewIsAnsweredOncePerIssueAndOnlyWhileTheIssueIsIdle(t *testing.T) {
	t.Parallel()
	began := time.Now().UTC().Add(-2 * time.Hour)
	ended := began.Add(30 * time.Minute)
	requestedAt := ended.Add(5 * time.Minute) // after the run that opened the pull request
	run := func(id int, signal, outcome string, holding bool) Run {
		r := record(id, 104, "Retry the upload", signal, outcome, holding, began, ended)
		r.PullRequest = "https://github.com/acme/edge-sensors/pull/104"
		return r
	}
	// The follow-up run the review queued, which carries the review it stands for.
	answered := func(id int, at time.Time) Run {
		r := run(id, signalChangesRequested, outcomeReady, true)
		r.SignalAt, r.StartedAt = at, at.Add(time.Second)
		return r
	}
	for _, c := range []struct {
		name    string
		runs    []Run
		at      time.Time // the newest review that asks for changes, as this poll reads it
		follows bool
	}{
		{"a review submitted after the run that opened the pull request", []Run{
			run(1, signalRouted, outcomeReady, true)}, requestedAt, true},
		{"the same review while the run it queued has not been recorded", []Run{
			run(1, signalRouted, outcomeReady, true), answered(2, requestedAt)}, requestedAt, false},
		{"another review after the one that was answered", []Run{
			run(1, signalRouted, outcomeReady, true), answered(2, requestedAt)},
			requestedAt.Add(20 * time.Minute), true},
		{"a review submitted while the run was going", []Run{
			run(1, signalRouted, outcomeReady, true)}, ended.Add(-5 * time.Minute), false},
		{"no review at all", []Run{
			run(1, signalRouted, outcomeReady, true)}, time.Time{}, false},
		{"a claim another claimer won", []Run{
			run(1, signalRouted, outcomeLost, false)}, requestedAt, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			held := holdings(c.runs)["acme/edge-sensors#104"]
			if follows := held.changesRequested(c.at); follows != c.follows {
				t.Errorf("the factory reads this as a review to answer: %v, want %v", follows, c.follows)
			}
		})
	}

	// Which pull request is watched, and when. A run that is still going is nothing to queue beside,
	// and an issue whose runs opened no pull request has none to read a review of.
	watched := []Run{run(1, signalRouted, outcomeReady, true)}
	if pull, ok := holdings(watched)["acme/edge-sensors#104"].pull(); !ok || pull != 104 {
		t.Errorf("the factory watches the pull request %d (%v), want #104, the one the run reported", pull, ok)
	}
	none := []Run{record(1, 104, "Retry the upload", signalRouted, outcomeReady, true, began, ended)}
	if _, ok := holdings(none)["acme/edge-sensors#104"].pull(); ok {
		t.Error("the factory watches a pull request of an issue whose runs opened none")
	}
	active := watched
	active[0].EndedAt, active[0].State = nil, "running"
	if held := holdings(active)["acme/edge-sensors#104"]; held.changesRequested(requestedAt) {
		t.Error("an issue whose run is still going is read as reviewed; the run would be queued beside itself")
	} else if _, ok := held.pull(); ok {
		t.Error("the factory reads the pull request of a run that is still writing to it")
	}
}

// What is said about an author GitHub could not be asked about, which is said once while it lasts and
// again the next time it happens: the factory polls every minute for weeks, so a warning that repeated
// itself would drown the log — and one that was never cleared would silence the next outage for the
// life of the process, leaving a review passed over without a word.
//
// This one is read from the code itself rather than through the running binary: the gesture it belongs
// to is driven end to end above, and what is tested here is the bookkeeping of a log line, which the
// factory shows nowhere else.
func TestAnAuthorGitHubCouldNotBeAskedAboutIsWarnedAboutAgainAfterItCould(t *testing.T) {
	// Serial: it sets this process's PATH to reach its gh and points the package logger at a buffer.
	const repository, pull, author = "acme/edge-sensors", 104, "maintainer"
	dir := t.TempDir()
	reviews, permission := filepath.Join(dir, "reviews.json"), filepath.Join(dir, "permission")
	// A gh of a few lines: the review list and the push permission are files this test writes, and
	// everything else is the open pull request the watch needs.
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\ncase \"$*\" in\n"+
		"*collaborators*) cat "+permission+" 2>/dev/null || { echo 'gh: Not Found (HTTP 404)' >&2; exit 1; } ;;\n"+
		"*reviews*) cat "+reviews+" ;;\n"+
		"*) echo open ;;\nesac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var said bytes.Buffer
	log.SetOutput(&said)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	writeFile(t, reviews, marshal(t, []map[string]any{review(1, author, "CHANGES_REQUESTED", at)}))
	g := newGitHub(nil, "factory")
	warnings := func() int { return strings.Count(said.String(), "whether "+author+" may write") }

	// GitHub cannot be asked, twice: the review queues nothing and the reason is said once.
	for poll := 0; poll < 2; poll++ {
		if _, err := g.newestRequest(context.Background(), repository, pull); err != nil {
			t.Fatalf("the reviews could not be read: %v", err)
		}
	}
	if n := warnings(); n != 1 {
		t.Fatalf("the factory said %d times that %s could not be asked about, want once", n, author)
	}

	// It answers again — for this review the author is no writer, so nothing is queued either.
	writeFile(t, permission, "false\n")
	if _, err := g.newestRequest(context.Background(), repository, pull); err != nil {
		t.Fatalf("the reviews could not be read: %v", err)
	}
	if n := warnings(); n != 1 {
		t.Fatalf("the factory said %d times that %s could not be asked about, want the one from before", n, author)
	}

	// A later review of the same author, and GitHub cannot be asked about them again: that is a new
	// outage and the operator hears of it.
	if err := os.Remove(permission); err != nil {
		t.Fatal(err)
	}
	writeFile(t, reviews, marshal(t, []map[string]any{
		review(1, author, "CHANGES_REQUESTED", at),
		review(2, author, "CHANGES_REQUESTED", at.Add(20*time.Minute))}))
	if _, err := g.newestRequest(context.Background(), repository, pull); err != nil {
		t.Fatalf("the reviews could not be read: %v", err)
	}
	if n := warnings(); n != 2 {
		t.Errorf("the factory said %d times that %s could not be asked about, want a word about each outage", n, author)
	}
}

// ---- the gh shim ----

// pullRequestIs is what GitHub says the state of one pull request is: open or closed, which is what
// it calls a merged pull request too.
func (g *ghShim) pullRequestIs(t *testing.T, repository string, pull int, state string) {
	t.Helper()
	g.answer(t, "api "+pullRequestRequest(repository, pull)+" --jq .state", state+"\n")
}

// reviews is the review list of one pull request, as GitHub's review endpoint answers it.
func (g *ghShim) reviews(t *testing.T, repository string, pull int, reviews ...map[string]any) {
	t.Helper()
	if reviews == nil {
		reviews = []map[string]any{}
	}
	g.answer(t, "api --paginate "+reviewsRequest(repository, pull), marshal(t, reviews))
}

// mayWrite is whether GitHub says one user may push to a repository, which is what makes their
// review the maintainer's gesture.
func (g *ghShim) mayWrite(t *testing.T, repository, login string, may bool) {
	t.Helper()
	g.answer(t, "api "+permissionRequest(repository, login)+" --jq .user.permissions.push",
		strconv.FormatBool(may)+"\n")
}

// review is one review as GitHub's review list carries it. The names are GitHub's, because they are
// the contract the rule reads.
func review(id int, login, state string, at time.Time) map[string]any {
	return map[string]any{"id": id, "user": map[string]any{"login": login}, "state": state,
		"submitted_at": at.Format(time.RFC3339)}
}

// after is a moment GitHub could have timed after this one, on the second GitHub counts in.
func after(at time.Time) time.Time { return at.Add(2 * time.Second).UTC().Truncate(time.Second) }

// line is what the factory serves as its one line of work.
func (f *factory) line(t *testing.T) apiLine {
	t.Helper()
	var line apiLine
	f.get(t, "/api/line", &line)
	return line
}
