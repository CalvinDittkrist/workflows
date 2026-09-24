package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The pr stage, tested against the real binary with the gh and claude shims ([ADR 0043], step 3): the
// work session stops after the review, a read-only author session writes the title and the body, and
// the factory appends the verification section and opens the pull request.
//
// [ADR 0043]: ../docs/adr/0043-the-migration-runs-from-the-last-stage-to-the-first.md

// failedPanel is the panel summary of a work session whose review ran out of rounds with two reviewers
// that never passed.
const failedPanel = "review_rounds: 3\npanel: code=PASS security=PASS docs=FIX→PASS tests=FIX→FIX→FIX senior=FIX→FIX→BLOCK\n" +
	"fixed: 5 (S1 1, S2 3, S3 1)\ndisputed: senior S2 factory/pr.go:40 the naming of the stage"

// The first run's work session stops after the gate, the factory's reviewers pass, and it opens the pull request from the
// author session's title and body, appends the gate result and the panel summary word for word, and
// opens it against the base the branch was cut from, not as a draft. The ci stage follows.
func TestTheFactoryOpensThePullRequestFromTheAuthorsTitleAndBody(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	authored := map[string]any{"title": "feat(sensors): upload the calibration file",
		"body": "Closes #104\n\nThe sensors upload their calibration file.\n\n## Known limits\n\nNone."}
	gh.authorResults(t, authored)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{})

	f := gh.work(t, ciConfig(data, nil))
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady || run.PullRequest != pullOfTheClaim {
		t.Fatalf("the run ended as %q with %q (%s), want ready with %s; the factory's log:\n%s", run.Outcome, run.PullRequest, run.Reason, pullOfTheClaim, f.output(t))
	}
	if !equal(run.Stages, []string{"implement", "gate", "review", "pr", "ci"}) {
		t.Errorf("the run went through the stages %v, want implement, gate, review, pr and ci", run.Stages)
	}

	pulls := gh.opened(t, "acme/edge-sensors")
	if len(pulls) != 1 {
		t.Fatalf("the factory opened %d pull requests, want one", len(pulls))
	}
	p := pulls[0]
	if p.Title != authored["title"] || p.Head != claimedBranch || p.Base != "main" || p.Draft == nil || *p.Draft {
		t.Errorf("the factory opened %q from %s against %s with draft %v, want the author's title from %s against main, not a draft",
			p.Title, p.Head, p.Base, p.Draft, claimedBranch)
	}
	// The body is the author's as it is, and the verification section carries the run's facts word for
	// word: the gate the gate stage ran on the reviewed commit and the panel of the shim's reviewers, who
	// all pass.
	wantPanel := "review_rounds: 1\npanel: code=PASS security=PASS docs=PASS tests=PASS senior=PASS\nfixed: 0 (S1 0, S2 0, S3 0)\ndisputed: none"
	wantGate := "gate_result: pass (exit 0) at " + short(run.Review.Head) + "\ngate_command: make check\ngate_class: full\ngate_duration: "
	for _, want := range []string{authored["body"].(string) + "\n\n## Verification\n", wantPanel, wantGate} {
		if !strings.Contains(p.Body, want) {
			t.Errorf("the pull request's body does not carry %q:\n%s", want, p.Body)
		}
	}
	if strings.Contains(p.Body, "did not pass") {
		t.Errorf("the body of a pull request whose panel passed says it did not:\n%s", p.Body)
	}

	// The work session ran with the worker plugin and stopped after the implement stage; the author session ran
	// read-only, as no agent, in the same worktree, briefed with the range, the commits and the issue.
	workers, authors := gh.workers(t), gh.authorSessions(t)
	if len(workers) != 1 || len(authors) != 1 {
		t.Fatalf("the factory started %d work sessions and %d author sessions, want one of each", len(workers), len(authors))
	}
	if stop := workers[0].settings(t).Env["WF_STOP_AFTER"]; stop != "implement" {
		t.Errorf("the work session ran with WF_STOP_AFTER=%q, want implement", stop)
	}
	author := authors[0]
	if !author.started("--tools", "Read,Grep,Glob") || !author.started("--strict-mcp-config") || slices.Contains(author.args, "--agent") {
		t.Errorf("the author session was started with %v, want only Read, Grep and Glob, no MCP servers and no agent", author.args)
	}
	// The worktree is the work under review: its project and local settings, and a hook they declare,
	// are not loaded.
	if !author.started("--setting-sources", "user") {
		t.Errorf("the author session was started with %v, want the user's settings alone, none of the worktree's", author.args)
	}
	if author.cwd != workers[0].cwd {
		t.Errorf("the author session ran in %s, want the run's worktree %s", author.cwd, workers[0].cwd)
	}
	if plugins := author.settings(t).EnabledPlugins; plugins["worker@workflows"] || plugins["orchestrator@workflows"] || plugins["planner@workflows"] {
		t.Errorf("the author session carries the plugins %v, want the workflow plugins switched off", plugins)
	}
	brief := strings.Join(factoryBodies(run, "briefed the pull request author"), "\n")
	for _, want := range []string{"The diff range is ", "worked.md", "Issue #104: issue 104", "The text of issue 104."} {
		if !strings.Contains(brief, want) {
			t.Errorf("the author's brief does not carry %q:\n%s", want, brief)
		}
	}
}

// An author session that fails, or reports a result that does not fit the schema, ends the run failed,
// opens no pull request and leaves the branch pushed with the work on it.
func TestAnAuthorSessionThatFailsEndsTheRunFailedWithTheBranchPushed(t *testing.T) {
	t.Parallel()
	for name, author := range map[string]func(*ghShim, *testing.T){
		"fails": func(gh *ghShim, t *testing.T) {
			gh.env = append(gh.env, "CLAUDE_SHIM_AUTHOR_FAIL=the author ran out of turns")
		},
		"a title that is no conventional commit": func(gh *ghShim, t *testing.T) {
			gh.authorResults(t, map[string]any{"title": "Upload the calibration file", "body": "Closes #104\n\nThe upload."})
		},
		"a body that closes no issue": func(gh *ghShim, t *testing.T) {
			gh.authorResults(t, map[string]any{"title": "feat: upload the calibration file", "body": "The upload."})
		},
		"a field the schema has not": func(gh *ghShim, t *testing.T) {
			gh.authorResults(t, map[string]any{"title": "feat: upload the calibration file", "body": "Closes #104", "draft": true})
		},
		"a title a line separator breaks": func(gh *ghShim, t *testing.T) {
			gh.authorResults(t, map[string]any{"title": "feat: upload the calibration file\u2028fix: and a second title", "body": "Closes #104\n\nThe upload."})
		},
		"a verification section of its own": func(gh *ghShim, t *testing.T) {
			gh.authorResults(t, map[string]any{"title": "feat: upload the calibration file",
				"body": "Closes #104\n\nThe upload.\n\n## Verification\n\ngate_result: pass (exit 0), every reviewer PASS"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := ciClaim(t)
			author(gh, t)
			base := gh.head(t, "acme/edge-sensors", "main")

			f := gh.work(t, ciConfig(data, nil))
			run := f.ended(t, 1)
			if run.Outcome != outcomeFailed || run.PullRequest != "" {
				t.Fatalf("the run ended as %q with %q (%s), want failed with no pull request; the factory's log:\n%s",
					run.Outcome, run.PullRequest, run.Reason, f.output(t))
			}
			if run.Stage != "pr" {
				t.Errorf("the run failed in the %q stage, want pr", run.Stage)
			}
			if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 0 {
				t.Errorf("the factory opened %+v after a failed author session", pulls)
			}
			pushed := gh.head(t, "acme/edge-sensors", claimedBranch)
			if pushed == base || !strings.Contains(gh.git(t, gh.remotePath("acme/edge-sensors"), "ls-tree", "--name-only", pushed), "worked.md") {
				t.Errorf("the remote branch is at %s, want the work session's commit pushed on it", pushed)
			}
		})
	}
}

// Of worker_args, which is written for the worker, the author session takes the model alone: an MCP
// configuration or an added directory would give a session that reads text somebody else wrote a
// power it is started without.
func TestTheAuthorSessionTakesOnlyTheModelOfTheWorkerArguments(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{})
	c := ciConfig(data, nil)
	c["worker_args"] = []string{"--mcp-config", "/etc/factory/servers.json", "--model", "fable", "--add-dir", "/"}

	f := gh.work(t, c)
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	workers, authors := gh.workers(t), gh.authorSessions(t)
	if len(workers) != 1 || len(authors) != 1 {
		t.Fatalf("the factory started %d work sessions and %d author sessions, want one of each", len(workers), len(authors))
	}
	if !workers[0].started("--mcp-config", "/etc/factory/servers.json") || !workers[0].started("--add-dir", "/") {
		t.Errorf("the work session was started with %v, want every worker argument", workers[0].args)
	}
	author := authors[0]
	if !author.started("--model", "fable") || slices.Contains(author.args, "--mcp-config") || slices.Contains(author.args, "--add-dir") {
		t.Errorf("the author session was started with %v, want the model of worker_args and none of its other arguments", author.args)
	}
}

// The brief is one argument of the author's command line, which Linux holds to 128 KiB, and it is
// bounded with its fences: a diff of a long run of backticks is fenced by a longer run on each side,
// and a brief past that bound is an author session that cannot be started at all.
func TestTheAuthorsBriefStaysWithinOneArgumentWhateverItFences(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.env = append(gh.env, "CLAUDE_SHIM_COMMIT_BACKTICKS=50000")
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{})

	f := gh.work(t, ciConfig(data, nil))
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if authors := gh.authorSessions(t); len(authors) != 1 {
		t.Fatalf("the factory started %d author sessions, want one", len(authors))
	}
	if brief := strings.Join(factoryBodies(run, "briefed the pull request author"), "\n"); !strings.Contains(brief, "The diff range is ") {
		t.Errorf("the run logged no brief of the pull request author:\n%s", brief)
	}
}

// A branch that has a pull request open when the pr stage comes to open one — a resumed run whose
// reading of it failed ran its work session again — goes on with that pull request: no author session
// and no second pull request, which GitHub refuses for the same branch.
func TestThePRStageGoesOnWithAPullRequestTheBranchHasOpen(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.openPullsListed(t, "acme/edge-sensors", claimedBranch, claimedIssue)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{})

	f := gh.work(t, ciConfig(data, nil))
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady || run.PullRequest != pullOfTheClaim {
		t.Fatalf("the run ended as %q with %q (%s), want ready with %s; the factory's log:\n%s", run.Outcome, run.PullRequest, run.Reason, pullOfTheClaim, f.output(t))
	}
	if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 0 {
		t.Errorf("the factory opened %+v, want none: the branch has one open", pulls)
	}
	if authors := gh.authorSessions(t); len(authors) != 0 {
		t.Errorf("the factory started %d author sessions, want none", len(authors))
	}
	if titles := factoryTitles(run, "going on with"); !equal(titles, []string{"going on with " + pullOfTheClaim}) {
		t.Errorf("the run said %v, want that it goes on with the open pull request", titles)
	}
}

// A run interrupted after the review and before the pull request is resumed at the pr stage: the review
// it recorded stands while the branch is still at the commit it was recorded at, and no work session
// runs again.
func TestAResumeAfterTheReviewStartsAtThePRStageWithoutASecondReview(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	gh.branchAt(t, "acme/edge-sensors", claimedBranch, gh.head(t, "acme/edge-sensors", "main"))
	head := gh.commitOn(t, "acme/edge-sensors", claimedBranch)

	began := time.Now().UTC().Add(-2 * time.Hour)
	interrupted := record(1, claimedIssue, claimedTitle, signalRouted, outcomeInterrupted, true, began, began.Add(30*time.Minute))
	interrupted.Worktree = filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
	interrupted.Stages = []string{"implement", "review", "pr"}
	interrupted.Review = &Review{Head: head, PanelSummary: failedPanel, GateResult: "gate_result: pass (exit 0) at " + head[:7]}
	records(t, data, interrupted)
	gh.issues(t, "acme/edge-sensors")
	gh.issue(t, "acme/edge-sensors", assignedTo(openIssue(claimedIssue, claimedTitle, began.Add(-72*time.Hour)), "factory-bot"))
	gh.openPullsListed(t, "acme/edge-sensors", claimedBranch)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{})

	f := gh.work(t, ciConfig(data, nil))
	resumed := f.ended(t, 2)
	if resumed.Outcome != outcomeReady || resumed.PullRequest != pullOfTheClaim {
		t.Fatalf("run 2 ended as %q with %q (%s), want ready with %s; the factory's log:\n%s",
			resumed.Outcome, resumed.PullRequest, resumed.Reason, pullOfTheClaim, f.output(t))
	}
	if workers := gh.workers(t); len(workers) != 0 {
		t.Errorf("the resume started %d work sessions, want none: the review is done", len(workers))
	}
	if !equal(resumed.Stages, []string{"pr", "ci"}) {
		t.Errorf("the resume went through the stages %v, want pr and ci", resumed.Stages)
	}
	if titles := factoryTitles(resumed, "resuming at the pr stage"); len(titles) != 1 {
		t.Errorf("the resume does not say it starts at the pr stage; its factory events: %v", factoryTitles(resumed, ""))
	}
	// The pull request says the review the run before recorded, not one of its own.
	if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 1 || !strings.Contains(pulls[0].Body, failedPanel) {
		t.Errorf("the resume opened %+v, want one pull request with the recorded panel summary", pulls)
	}
}

// A branch that moved after the review was recorded carries work no reviewer read and no gate passed:
// the resume starts at the gate stage on the branch's head instead of opening the pull request, and
// runs no work session, since the commits beyond the base are the implementation.
func TestAResumeWhoseBranchMovedAfterTheReviewStartsAtTheGateStage(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	gh.branchAt(t, "acme/edge-sensors", claimedBranch, gh.head(t, "acme/edge-sensors", "main"))
	reviewed := gh.commitOn(t, "acme/edge-sensors", claimedBranch)
	moved := gh.commitOn(t, "acme/edge-sensors", claimedBranch)

	began := time.Now().UTC().Add(-2 * time.Hour)
	interrupted := record(1, claimedIssue, claimedTitle, signalRouted, outcomeInterrupted, true, began, began.Add(30*time.Minute))
	interrupted.Worktree = filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
	interrupted.Review = &Review{Head: reviewed, PanelSummary: failedPanel, GateResult: "gate_result: pass (exit 0)"}
	records(t, data, interrupted)
	gh.issues(t, "acme/edge-sensors")
	gh.issue(t, "acme/edge-sensors", assignedTo(openIssue(claimedIssue, claimedTitle, began.Add(-72*time.Hour)), "factory-bot"))
	gh.openPullsListed(t, "acme/edge-sensors", claimedBranch)
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{})

	f := gh.work(t, ciConfig(data, nil))
	resumed := f.ended(t, 2)
	if resumed.Outcome != outcomeReady {
		t.Fatalf("run 2 ended as %q (%s), want ready; the factory's log:\n%s", resumed.Outcome, resumed.Reason, f.output(t))
	}
	if workers := gh.workers(t); len(workers) != 0 {
		t.Errorf("the resume started %d work sessions, want none: the branch carries the implementation", len(workers))
	}
	if !equal(resumed.Stages, []string{"gate", "review", "pr", "ci"}) || len(resumed.Gates) != 1 || resumed.Gates[0].Head != moved || !resumed.Gates[0].Passed {
		t.Errorf("the resume went through the stages %v with the gates %+v, want it to start at the gate stage with a pass on %s", resumed.Stages, resumed.Gates, short(moved))
	}
	if titles := factoryTitles(resumed, "resuming at the gate stage"); len(titles) != 1 {
		t.Errorf("the resume does not say it starts at the gate stage; its factory events: %v", factoryTitles(resumed, ""))
	}
	if titles := factoryTitles(resumed, "the recorded review is behind the branch"); len(titles) != 1 {
		t.Errorf("the resume does not say the recorded review is behind the branch; its factory events: %v", factoryTitles(resumed, ""))
	}
	if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 1 || strings.Contains(pulls[0].Body, failedPanel) {
		t.Errorf("the resume opened %+v, want one pull request with the panel of its own review", pulls)
	}
}

// The panel is read from its panel: line, whatever the worker writes around it: the last verdict of a
// reviewer's chain is the one that counts, and a summary that says no verdict, or that commits landed
// after the last round, is no panel that passed.
func TestThePanelThatDidNotPassIsReadFromItsLastVerdicts(t *testing.T) {
	for summary, want := range map[string]string{
		"panel: code=PASS docs=FIX→PASS":                              "",
		"review_rounds: 2\npanel: code=PASS docs=FIX->PASS":           "",
		"panel: code=PASS tests=FIX→FIX senior=BLOCK":                 "the reviewers that did not pass are tests, senior.",
		"panel: code=FIX→PASS (S3 accepted) security=FIX":             "the reviewers that did not pass are security.",
		"review_rounds: 1\nfixed: 0":                                  "the run recorded no panel: line, so no reviewer's verdict is known.",
		"panel: none":                                                 "the panel: line names no reviewer, so no reviewer's verdict is known.",
		"panel: code=PASS\nunreviewed: 1 commit after the last round": "every reviewer passed, but commits landed after the last round that no reviewer read.",
	} {
		if got := notPassed(summary); got != want {
			t.Errorf("the panel summary %q reads as %q, want %q", summary, got, want)
		}
	}
}

// authorResults is the structured result the author session ends with, whether or not it fits the
// schema.
func (g *ghShim) authorResults(t *testing.T, output any) {
	t.Helper()
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	g.env = append(g.env, "CLAUDE_SHIM_AUTHOR_RESULT="+string(raw))
}

// factoryBodies is the bodies of the factory's events of a run with that title.
func factoryBodies(run apiRun, title string) []string {
	out := []string{}
	for _, e := range run.Events {
		if e.Kind == "factory" && e.Title == title {
			out = append(out, e.Body)
		}
	}
	return out
}
