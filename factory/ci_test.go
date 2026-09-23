package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The ci stage, tested the way the rest of the factory is: the real binary against the gh and claude
// shims, the pull request's state moved by the test while the factory waits on it ([ADR 0043]).
//
// [ADR 0043]: ../docs/adr/0043-the-migration-runs-from-the-last-stage-to-the-first.md

// ciClaim is the fixture every test of the ci stage starts from: #104 routed on acme/edge-sensors,
// claimed by factory-bot, whose work session commits and reports its pull request.
func ciClaim(t *testing.T) (*ghShim, string) {
	t.Helper()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	gh.workerCommits(t, "worked.md")
	// A host whose sessions commit has a git identity, which the merge of a conflict round commits as.
	gh.env = append(gh.env, "GIT_AUTHOR_NAME=factory", "GIT_AUTHOR_EMAIL=factory@example.com",
		"GIT_COMMITTER_NAME=factory", "GIT_COMMITTER_EMAIL=factory@example.com")
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	return gh, data
}

// ciConfig is the configuration of a ci test: a fast poll, and the ci knobs given.
func ciConfig(data string, knobs map[string]any) config {
	c := config{"poll": "50ms", "deadline": "90s", "data_dir": data, "repositories": []string{"acme/edge-sensors"}}
	if knobs != nil {
		c["ci"] = knobs
	}
	return c
}

// pullOfTheClaim is the pull request the work session of ciClaim reports.
var pullOfTheClaim = fmt.Sprintf("https://github.com/acme/edge-sensors/pull/%d", claimedIssue)

// saw waits until run 1 has an event of the factory whose title starts with that prefix.
func (f *factory) saw(t *testing.T, prefix string) {
	t.Helper()
	f.sawIn(t, 1, prefix)
}

// sawIn waits until a run has an event of the factory whose title starts with that prefix.
func (f *factory) sawIn(t *testing.T, id int, prefix string) {
	t.Helper()
	f.eventually(t, 60*time.Second, fmt.Sprintf("an event %s… of run %d", prefix, id), func() bool {
		var run apiRun
		response := f.do(t, "GET", fmt.Sprintf("/api/runs/%d", id))
		defer response.Body.Close()
		if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&run) != nil {
			return false
		}
		return len(factoryTitles(run, prefix)) > 0
	})
}

// briefs is the prompts of the fix sessions of a run, as its log carries them.
func briefs(run apiRun) []string {
	out := []string{}
	for _, e := range run.Events {
		if e.Kind == "factory" && e.Title == "briefed a fix session" {
			out = append(out, e.Body)
		}
	}
	return out
}

// Failed checks are a repair round: a fix session in the run's worktree, briefed with the checks that
// failed and their failed logs, whose push the factory waits to see on the pull request before it
// reads the checks again. Green then ends the run ready.
func TestFailedChecksRunAFixSessionWithTheirLogsAndTheRunEndsReadyOnceGreen(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{checks: []map[string]any{passed("lint"), failed("test", 4242)}})
	gh.answer(t, "run view 4242 --repo acme/edge-sensors --log-failed", "test\tFAIL: TestUploadRetries (0.01s)\n")

	f := gh.work(t, ciConfig(data, nil))
	f.saw(t, "pushed ")
	// Until GitHub shows the push, the pull request still reads failed at the commit the round was
	// spent on, which is no reason for a second round.
	f.never(t, time.Second, "the factory spent a second round on the head the first was spent on", func() bool {
		var going apiRun
		f.get(t, "/api/runs/1", &going)
		return going.RepairRounds > 1 || going.State == "ended"
	})
	// The push reached the pull request, and the checks of the new head passed.
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{head: gh.head(t, "acme/edge-sensors", claimedBranch)})
	run := f.ended(t, 1)

	if run.Outcome != outcomeReady || run.PullRequest != pullOfTheClaim {
		t.Fatalf("the run ended as %q with %q (%s), want ready with %s; the factory's log:\n%s", run.Outcome, run.PullRequest, run.Reason, pullOfTheClaim, f.output(t))
	}
	if run.RepairRounds != 1 {
		t.Errorf("the run took %d repair rounds, want 1", run.RepairRounds)
	}
	// Between the push and the pull request showing it the stage waits, however long that takes.
	if titles := factoryTitles(run, "ci: checks-failed", "repair round", "ci: green"); !equal(titles, []string{"ci: checks-failed", "repair round 1 of 3", "ci: green"}) {
		t.Errorf("the ci stage said %v, want checks-failed, one repair round, green", titles)
	}
	workers := gh.workers(t)
	if len(workers) != 2 {
		t.Fatalf("the factory started %d sessions, want the work session and one fix session", len(workers))
	}
	fix := workers[1]
	if fix.cwd != workers[0].cwd || fix.branch != claimedBranch {
		t.Errorf("the fix session ran in %s on %s, want the run's worktree %s on %s", fix.cwd, fix.branch, workers[0].cwd, claimedBranch)
	}
	brief := strings.Join(briefs(run), "\n")
	for _, want := range []string{"test https://github.com/o/r/actions/runs/4242/job/1", "FAIL: TestUploadRetries"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the fix session's brief does not carry %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "- lint") {
		t.Errorf("the fix session's brief names the check that passed:\n%s", brief)
	}
	// The work session stopped after its pull request: the ci stage is the factory's.
	if stop := workers[0].settings(t).Env["WF_STOP_AFTER"]; stop != "pr" {
		t.Errorf("the work session ran with WF_STOP_AFTER=%q, want pr", stop)
	}
}

// A conflict with the base is first a merge the factory makes itself: one that merges cleanly is
// pushed as it is and costs a round but no session.
func TestAConflictThatMergesCleanlyIsPushedWithoutASession(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{mergeable: "UNKNOWN"})

	f := gh.work(t, ciConfig(data, nil))
	f.saw(t, "ci: waiting")
	moved := gh.commitOn(t, "acme/edge-sensors", "main")
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{mergeable: "CONFLICTING"})
	f.saw(t, "pushed ")
	head := gh.head(t, "acme/edge-sensors", claimedBranch)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{head: head})
	run := f.ended(t, 1)

	if run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if titles := factoryTitles(run, "ci: conflicts", "repair round", "merged main"); !equal(titles, []string{"ci: conflicts", "repair round 1 of 3", "merged main cleanly"}) {
		t.Errorf("the ci stage said %v, want a conflict, one round and a clean merge", titles)
	}
	if workers := gh.workers(t); len(workers) != 1 {
		t.Errorf("the factory started %d sessions, want the work session alone: a clean merge needs nobody", len(workers))
	}
	parents := strings.Fields(gh.git(t, gh.remotePath("acme/edge-sensors"), "rev-list", "--parents", "-n", "1", head))
	if len(parents) != 3 || !slices.Contains(parents[1:], moved) {
		t.Errorf("%s of the remote is at %v, want a merge commit of main's head %s: the base is merged, never rebased", claimedBranch, parents, moved)
	}
}

// A merge that conflicts is left in progress in the worktree, and a fix session is briefed with the
// files it conflicts in; it resolves, commits and pushes, and the factory reads the pull request again.
func TestAConflictingMergeBriefsAFixSessionWithTheConflictedFiles(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{mergeable: "UNKNOWN"})

	f := gh.work(t, ciConfig(data, nil))
	f.saw(t, "ci: waiting")
	// main takes a change to the file the work session wrote, after the branch was cut.
	other := filepath.Join(t.TempDir(), "other")
	gh.git(t, filepath.Dir(other), "clone", "-q", gh.remotePath("acme/edge-sensors"), other)
	writeFile(t, filepath.Join(other, "worked.md"), "somebody else's work\n")
	gh.git(t, other, "add", "worked.md")
	gh.git(t, other, "commit", "-q", "-m", "docs: worked.md")
	gh.git(t, other, "push", "-q", "origin", "HEAD:refs/heads/main")
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{mergeable: "CONFLICTING"})
	f.saw(t, "pushed ")
	// Somebody pushed on top of the merge before GitHub showed it: the new head is judged all the same.
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{head: "somebody-elses-push"})
	run := f.ended(t, 1)

	if run.Outcome != outcomeReady || run.RepairRounds != 1 {
		t.Fatalf("the run ended as %q after %d repair rounds (%s), want ready after one; the factory's log:\n%s", run.Outcome, run.RepairRounds, run.Reason, f.output(t))
	}
	if titles := factoryTitles(run, "the merge of"); !equal(titles, []string{"the merge of main conflicts"}) {
		t.Errorf("the ci stage said %v, want that the merge of main conflicts", titles)
	}
	workers := gh.workers(t)
	if len(workers) != 2 {
		t.Fatalf("the factory started %d sessions, want the work session and one fix session", len(workers))
	}
	if brief := strings.Join(briefs(run), "\n"); !strings.Contains(brief, "worked.md") || !strings.Contains(brief, "origin/main") {
		t.Errorf("the fix session's brief does not name the conflicted file and the base it merged:\n%s", brief)
	}
	if parents := strings.Fields(gh.git(t, gh.remotePath("acme/edge-sensors"), "rev-list", "--parents", "-n", "1", claimedBranch)); len(parents) != 3 {
		t.Errorf("%s of the remote is at %v, want the merge the fix session committed", claimedBranch, parents)
	}
}

// Every repair round counts against the budget the host configured, and a run whose pull request
// still fails once it is spent is blocked, naming what stands.
func TestARunOverItsRepairBudgetIsBlockedNamingTheFailingChecks(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{checks: []map[string]any{failed("test", 4242)}})
	gh.answer(t, "run view 4242 --repo acme/edge-sensors --log-failed", "FAIL: TestUploadRetries\n")

	f := gh.work(t, ciConfig(data, map[string]any{"repair_rounds": 1}))
	f.saw(t, "pushed ")
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{head: gh.head(t, "acme/edge-sensors", claimedBranch),
		checks: []map[string]any{failed("test", 4243)}})
	run := f.ended(t, 1)

	if run.Outcome != outcomeBlocked {
		t.Fatalf("the run ended as %q (%s), want blocked; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	for _, want := range []string{pullOfTheClaim, "1 of 1 repair rounds", "test https://github.com/o/r/actions/runs/4243/job/1"} {
		if !strings.Contains(run.Reason, want) {
			t.Errorf("the blocked run gives the reason %q, want %q in it", run.Reason, want)
		}
	}
	if workers := gh.workers(t); len(workers) != 2 {
		t.Errorf("the factory started %d sessions, want the work session and the one fix session the budget allows", len(workers))
	}
}

// A fix session that cannot fix what it was given reports blocked, and the run is blocked on its words.
func TestAFixSessionThatIsBlockedBlocksTheRun(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{checks: []map[string]any{failed("test", 4242)}})
	gh.answer(t, "run view 4242 --repo acme/edge-sensors --log-failed", "FAIL: TestUploadRetries\n")
	gh.env = append(gh.env, `CLAUDE_SHIM_THEN_RESULT={"outcome":"blocked","summary":"the test needs a broker this host does not have"}`)

	f := gh.work(t, ciConfig(data, nil))
	run := f.ended(t, 1)
	if run.Outcome != outcomeBlocked || !strings.Contains(run.Reason, "needs a broker") {
		t.Fatalf("the run ended as %q (%s), want blocked on the fix session's words; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
}

// Review comments are not the factory's to answer yet: a writer's review that asks for changes, with
// or without a thread, and an unresolved thread block the run, named.
func TestReviewCommentsBlockTheRunNamingThem(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	objection := review(9001, "maintainer", "CHANGES_REQUESTED", time.Now().UTC())
	objection["html_url"] = pullOfTheClaim + "#pullrequestreview-9001"
	gh.mayWrite(t, "acme/edge-sensors", "maintainer", true)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{
		reviews: []map[string]any{objection},
		threads: []map[string]any{
			{"isResolved": true, "path": "a.go", "line": 1, "comments": map[string]any{"nodes": []map[string]any{}}},
			{"isResolved": false, "path": "upload.go", "line": 42, "comments": map[string]any{"nodes": []map[string]any{
				{"author": map[string]any{"login": "chatgpt-codex-connector"}, "url": pullOfTheClaim + "#discussion_r7"}}}},
		}})

	f := gh.work(t, ciConfig(data, nil))
	run := f.ended(t, 1)

	if run.Outcome != outcomeBlocked {
		t.Fatalf("the run ended as %q (%s), want blocked; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	for _, want := range []string{"maintainer asked for changes: " + pullOfTheClaim + "#pullrequestreview-9001",
		"unresolved thread on upload.go:42 by chatgpt-codex-connector: " + pullOfTheClaim + "#discussion_r7"} {
		if !strings.Contains(run.Reason, want) {
			t.Errorf("the blocked run gives the reason %q, want %q in it", run.Reason, want)
		}
	}
	if strings.Contains(run.Reason, "a.go") {
		t.Errorf("the blocked run names a resolved thread: %q", run.Reason)
	}
	if workers := gh.workers(t); len(workers) != 1 {
		t.Errorf("the factory started %d sessions, want none after the work session: review comments are no repair round", len(workers))
	}
}

// Pending checks, and green checks without the review of a configured bot inside the review wait,
// are waiting: the run stays in the ci stage. A bot review ends the wait, whichever commit it is on.
func TestTheRunWaitsInTheCIStageForChecksAndTheBotReview(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{checks: []map[string]any{pending("test")}})

	f := gh.work(t, ciConfig(data, map[string]any{"review_wait": "1h"}))
	f.saw(t, "ci: waiting")
	just := passed("test")
	just["completedAt"] = time.Now().UTC().Format(time.RFC3339)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{checks: []map[string]any{just}})
	f.never(t, 2*time.Second, "the run ended while its checks had just passed and the bot had not reviewed", func() bool {
		var run apiRun
		f.get(t, "/api/runs/1", &run)
		return run.State == "ended"
	})
	var waiting apiRun
	f.get(t, "/api/runs/1", &waiting)
	if waiting.Stage != "ci" || waiting.PullRequest != pullOfTheClaim {
		t.Errorf("the waiting run stands at %q with %q, want the ci stage of %s", waiting.Stage, waiting.PullRequest, pullOfTheClaim)
	}

	bot := review(9002, "chatgpt-codex-connector[bot]", "COMMENTED", time.Now().UTC())
	bot["commit_id"] = "an-older-commit"
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{checks: []map[string]any{just}, reviews: []map[string]any{bot}})
	if run := f.ended(t, 1); run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready once the bot reviewed; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
}

// A run resumed with its pull request open starts at the ci stage: no session repeats the work, and
// the repair rounds of the run before still count.
func TestAResumeWithAnOpenPullRequestStartsAtTheCIStage(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	gh.branchAt(t, "acme/edge-sensors", claimedBranch, gh.head(t, "acme/edge-sensors", "main"))
	gh.commitOn(t, "acme/edge-sensors", claimedBranch)

	began := time.Now().UTC().Add(-2 * time.Hour)
	interrupted := record(1, claimedIssue, claimedTitle, signalRouted, outcomeInterrupted, true, began, began.Add(30*time.Minute))
	interrupted.Worktree = filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
	interrupted.PullRequest = pullOfTheClaim
	interrupted.RepairRounds = 2
	interrupted.Stages = []string{"implement", "review", "pr", "ci"}
	records(t, data, interrupted)
	gh.issues(t, "acme/edge-sensors")
	gh.issue(t, "acme/edge-sensors", assignedTo(openIssue(claimedIssue, claimedTitle, began.Add(-72*time.Hour)), "factory-bot"))
	gh.openPullsListed(t, "acme/edge-sensors", claimedBranch, claimedIssue)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{checks: []map[string]any{failed("test", 4242)}})

	f := gh.work(t, ciConfig(data, nil))
	f.sawIn(t, 2, "pushed ")
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{head: gh.head(t, "acme/edge-sensors", claimedBranch),
		checks: []map[string]any{failed("test", 4242)}})
	resumed := f.ended(t, 2)
	if resumed.Signal != "interruption" || resumed.Outcome != outcomeBlocked {
		t.Fatalf("run 2 is the %q run and ended as %q (%s), want the resume blocked on the budget the run before spent; the factory's log:\n%s",
			resumed.Signal, resumed.Outcome, resumed.Reason, f.output(t))
	}
	if !strings.Contains(resumed.Reason, "3 of 3 repair rounds") {
		t.Errorf("the resume gives the reason %q, want the third round, its own, counted after the two before", resumed.Reason)
	}
	if workers, fixes := gh.workers(t), briefs(resumed); len(workers) != 1 || len(fixes) != 1 {
		t.Errorf("the resume started %d sessions and briefed %d fix sessions, want the one fix session of its round: the work is not done again", len(workers), len(fixes))
	}
	if titles := factoryTitles(resumed, "resuming at the ci stage"); len(titles) != 1 {
		t.Errorf("the resume does not say it starts at the ci stage; its factory events: %v", factoryTitles(resumed, ""))
	}
	if slices.Contains(resumed.Stages, "implement") {
		t.Errorf("the resume went through the stages %v, want none before ci", resumed.Stages)
	}
}

// Taking the routing label off an issue whose run is in a repair round ends the fix session with its
// process group, and the issue is given back.
func TestACancelDuringARepairRoundEndsTheFixSession(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.unassigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{checks: []map[string]any{failed("test", 4242)}})
	gh.answer(t, "run view 4242 --repo acme/edge-sensors --log-failed", "FAIL: TestUploadRetries\n")
	gh.env = append(gh.env, "CLAUDE_SHIM_THEN_SLEEP=600")

	f := gh.work(t, ciConfig(data, nil))
	f.saw(t, "briefed a fix session")
	var fix workerStart
	f.eventually(t, 30*time.Second, "the fix session to start", func() bool {
		workers := gh.workers(t)
		if len(workers) < 2 {
			return false
		}
		fix = workers[1]
		return fix.pgid > 0
	})
	gh.issue(t, "acme/edge-sensors", assignedTo(
		openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour), readyLabel), "factory-bot"))

	run := f.ended(t, 1)
	if run.Outcome != outcomeCancelled || !strings.Contains(run.Reason, "routing label") {
		t.Fatalf("the run ended as %q (%s), want cancelled by the label; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if groupSurvived(fix.pgid) {
		t.Errorf("the process group %d of the fix session is still there after the cancel", fix.pgid)
	}
	f.eventually(t, 30*time.Second, "the issue to be let go", func() bool {
		var let apiRun
		f.get(t, "/api/runs/1", &let)
		return let.LetGoAt != nil
	})
}

// A factory stopped while it waits on CI interrupts the run rather than failing it, as it does a
// session.
func TestAFactoryStoppedInTheCIStageInterruptsTheRun(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{checks: []map[string]any{pending("test")}})

	f := gh.work(t, ciConfig(data, nil))
	f.saw(t, "ci: waiting")
	f.stop(t, syscall.SIGTERM)
	var stopped Run
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(data, "run-1.json"))), &stopped); err != nil {
		t.Fatal(err)
	}
	if stopped.Outcome != outcomeInterrupted || stopped.PullRequest != pullOfTheClaim {
		t.Errorf("the run stopped in the ci stage is recorded %q with %q (%s), want interrupted with its pull request", stopped.Outcome, stopped.PullRequest, stopped.Reason)
	}
}
