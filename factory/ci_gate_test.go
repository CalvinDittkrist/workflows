package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The gate on CI, tested against the real binary with the gh and claude shims: a gate command of "ci"
// pushes the branch, opens a draft pull request and reads its checks, and the pr stage makes that
// draft the pull request. The checks are the shim's rollup of the head the branch is at on its GitHub.

// ciGateConfig is ciConfig with the host's gate command.
func ciGateConfig(data string, command any) config {
	c := ciConfig(data, nil)
	c["gate"] = map[string]any{"command": command}
	return c
}

// checksAre is the rollup of the pull request of that number on the commit given, or on every commit
// no rollup of its own is written for when at is empty.
func (g *ghShim) checksAre(t *testing.T, number int, at string, checks ...map[string]any) {
	t.Helper()
	name := fmt.Sprintf("checks-%d", number)
	if at != "" {
		name += "-" + at
	}
	if checks == nil {
		checks = []map[string]any{}
	}
	if err := os.WriteFile(filepath.Join(g.answers, name), []byte(marshal(t, checks)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The calls that finish the draft in the pr stage.
var (
	pullEdited = fmt.Sprintf("api --method PATCH repos/acme/edge-sensors/pulls/%d --input -", claimedIssue)
	pullReady  = fmt.Sprintf("pr ready %d --repo acme/edge-sensors", claimedIssue)
)

// A gate of "ci" pushes the branch and opens a draft pull request against the base with the body
// Closes #N and nothing else, waits for the checks of the pushed head and records what they said; the
// reviewers and the pull request carry that result. The pr stage then writes the author's title and
// body and the verification section into the draft and marks it ready, and opens no second one.
func TestAGateOnCIOpensADraftThatThePRStageMakesReady(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.checksAre(t, claimedIssue, "", passed("check"), passed("browser"))
	authored := map[string]any{"title": "feat(sensors): upload the calibration file", "body": "Closes #104\n\nThe sensors upload their calibration file."}
	gh.authorResults(t, authored)

	f := gh.work(t, ciGateConfig(data, "ci"))
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady || run.PullRequest != pullOfTheClaim || run.Draft {
		t.Fatalf("the run ended as %q with %q, draft %v (%s), want ready with %s, no longer a draft; the factory's log:\n%s",
			run.Outcome, run.PullRequest, run.Draft, run.Reason, pullOfTheClaim, f.output(t))
	}
	if !equal(run.Stages, []string{"implement", "gate", "review", "pr", "ci"}) {
		t.Errorf("the run went through the stages %v, want implement, gate, review, pr and ci", run.Stages)
	}
	pulls := gh.opened(t, "acme/edge-sensors")
	if len(pulls) != 1 || pulls[0].Draft == nil || !*pulls[0].Draft || pulls[0].Body != "Closes #104" || pulls[0].Base != "main" ||
		pulls[0].Head != claimedBranch || pulls[0].Title != claimedTitle {
		t.Fatalf("the factory opened %+v, want one draft of %s against main titled after the issue with the body Closes #104 alone", pulls, claimedBranch)
	}
	head := gh.head(t, "acme/edge-sensors", claimedBranch)
	if len(run.Gates) != 1 || !run.Gates[0].Passed || run.Gates[0].Command != "ci" || run.Gates[0].Head != head || run.Gates[0].Stage != stageGate ||
		len(run.Gates[0].Checks) != 2 || run.Gates[0].Checks[1].Name != "browser" || run.Gates[0].Checks[1].State != checkPass {
		t.Errorf("the run recorded the gates %+v, want one pass on CI of %s with the checks check and browser", run.Gates, short(head))
	}
	pass := "gate_result: pass (2 checks on CI) at " + short(head)
	if reviewers := strings.Join(factoryBodies(run, "briefed the reviewers"), "\n"); !strings.Contains(reviewers, pass) || !strings.Contains(reviewers, "gate_checks: check=pass, browser=pass") {
		t.Errorf("the reviewers were not briefed with the gate on CI:\n%s", reviewers)
	}
	var edit struct{ Title, Body string }
	if err := json.Unmarshal([]byte(gh.wrote(t, pullEdited)), &edit); err != nil {
		t.Fatalf("the pr stage wrote %q into the draft, want its title and body: %v", gh.wrote(t, pullEdited), err)
	}
	if edit.Title != authored["title"] || !strings.HasPrefix(edit.Body, authored["body"].(string)) || !strings.Contains(edit.Body, "## Verification") ||
		!strings.Contains(edit.Body, pass) {
		t.Errorf("the pr stage wrote the title %q and the body\n%s\nwant the author's with the verification section and the gate on CI", edit.Title, edit.Body)
	}
	if made := gh.made(t, pullReady); made != 1 {
		t.Errorf("the factory marked the draft ready %d times, want once", made)
	}
}

// A gate that names its checks reads those alone: a check it does not name may fail, and the run goes on
// to the pull request, whose ci stage reads every check as it always does. A named check that
// has not appeared once the checks grace has passed, or a head without any check, ends the run blocked
// naming what is missing, so a check whose name is mistyped is never read as a pass.
func TestAGateOnCIReadsItsNamedChecksAndBlocksOnOneThatIsMissing(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		command any
		checks  []map[string]any
		outcome string
		reason  string
	}{
		"a check it does not name fails":  {map[string]any{"ci": []string{"check"}}, []map[string]any{passed("check"), failed("codeql", 77)}, outcomeBlocked, "ci.repair_rounds"},
		"a named check that never comes":  {map[string]any{"ci": []string{"check", "browser"}}, []map[string]any{passed("check")}, outcomeBlocked, "names the check(s) browser, which " + pullOfTheClaim + " did not show"},
		"a head without any check at all": {"ci", nil, outcomeBlocked, "the gate on CI read no check on "},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := ciClaim(t)
			gh.checksAre(t, claimedIssue, "", c.checks...)
			cfg := ciGateConfig(data, c.command)
			cfg["ci"] = map[string]any{"checks_grace": "0s"}

			f := gh.work(t, cfg)
			run := f.ended(t, 1)
			if run.Outcome != c.outcome || !strings.Contains(run.Reason, c.reason) {
				t.Fatalf("the run ended as %q (%s), want %s with %q; the factory's log:\n%s", run.Outcome, run.Reason, c.outcome, c.reason, f.output(t))
			}
			if !strings.Contains(c.reason, "ci.repair_rounds") {
				if len(run.Gates) != 0 || len(gh.workers(t)) != 1 {
					t.Errorf("the run recorded the gates %+v and started %d sessions, want no gate result and no fix session for a check that is missing", run.Gates, len(gh.workers(t)))
				}
				return
			}
			if len(run.Gates) != 1 || !run.Gates[0].Passed || len(run.Gates[0].Checks) != 1 || run.Gates[0].Checks[0].Name != "check" ||
				run.Gates[0].Command != "ci: check" || len(run.Stages) < 5 || !equal(run.Stages[:5], []string{"implement", "gate", "review", "pr", "ci"}) {
				t.Errorf("the run went through %v and recorded the gates %+v, want the one check it names read as a pass and the run on to the ci stage", run.Stages, run.Gates)
			}
		})
	}
}

// A gate that reads every check does not pass on the first reading that shows them all passed: GitHub
// registers the checks of a head one workflow at a time, so a check that appears on the next reading
// is read too, and its failure is the gate's.
func TestAGateOnEveryCheckReadsACheckThatAppearsLate(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.checksAre(t, claimedIssue, "", passed("check"))
	cfg := ciConfig(data, nil)
	cfg["gate"] = map[string]any{"command": "ci", "rounds": 0}
	cfg["poll"] = "3s"

	f := gh.work(t, cfg)
	f.saw(t, "gate on CI: waiting")
	gh.checksAre(t, claimedIssue, "", passed("check"), failed("browser", 77))
	run := f.ended(t, 1)
	if run.Outcome != outcomeBlocked || !strings.Contains(run.Reason, "gate_checks: check=pass, browser=fail") {
		t.Fatalf("the run ended as %q (%s), want blocked on the browser check that appeared late; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if len(run.Gates) != 1 || run.Gates[0].Passed {
		t.Errorf("the run recorded the gates %+v, want one failure", run.Gates)
	}
}

// A check that fails is a failed gate: a fix session of the gate stage is briefed with the checks that
// failed and their failed logs, the factory pushes its commit and reads the checks of the new head.
// Checks still running are waited for.
func TestAFailingGateOnCIGoesToAFixSessionWithTheFailedLogs(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.checksAre(t, claimedIssue, "", pending("test"))
	gh.answer(t, "run view 4242 --repo acme/edge-sensors --log-failed", "test\tFAIL: TestUploadRetries (0.01s)\n")

	f := gh.work(t, ciGateConfig(data, "ci"))
	f.saw(t, "gate on CI: waiting")
	first := gh.head(t, "acme/edge-sensors", claimedBranch)
	gh.checksAre(t, claimedIssue, first, failed("test", 4242))
	gh.checksAre(t, claimedIssue, "", passed("test"))
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	workers := gh.workers(t)
	if len(workers) != 2 {
		t.Fatalf("the factory started %d sessions, want the implement session and one fix session of the gate", len(workers))
	}
	brief := strings.Join(workers[1].args, "\n")
	for _, want := range []string{"after the implementation, and it failed", "gate_result: fail (1 of 1 checks on CI) at " + short(first),
		"test https://github.com/o/r/actions/runs/4242/job/1", "FAIL: TestUploadRetries"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the fix session's brief does not carry %q:\n%s", want, brief)
		}
	}
	head := gh.head(t, "acme/edge-sensors", claimedBranch)
	if len(run.Gates) != 2 || run.Gates[0].Passed || run.Gates[0].Head != first || !strings.Contains(run.Gates[0].Tail, "FAIL: TestUploadRetries") ||
		!run.Gates[1].Passed || run.Gates[1].Head != head || head == first {
		t.Errorf("the run recorded the gates %+v, want a failure on %s and a pass on the pushed fix %s", run.Gates, short(first), short(head))
	}
	if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 1 {
		t.Errorf("the factory opened %d pull requests, want the one draft", len(pulls))
	}
}

// A draft that conflicts with the base runs no workflow on GitHub, so the gate merges the base into the
// branch, as the ci stage does, pushes the merge and reads the checks of the merged head.
func TestAGateOnCIMergesTheBaseIntoADraftThatConflicts(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.checksAre(t, claimedIssue, "", pending("test"))

	f := gh.work(t, ciGateConfig(data, "ci"))
	f.saw(t, "gate on CI: waiting")
	first := gh.head(t, "acme/edge-sensors", claimedBranch)
	moved := gh.commitOn(t, "acme/edge-sensors", "main")
	gh.answer(t, fmt.Sprintf("mergeable-%d-%s", claimedIssue, first), "CONFLICTING")
	f.saw(t, "merged main into the branch")
	gh.checksAre(t, claimedIssue, "", passed("test"))
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if titles := factoryTitles(run, "the pull request conflicts with", "merged main"); !equal(titles, []string{"the pull request conflicts with main", "merged main into the branch"}) {
		t.Errorf("the gate said %v, want a conflict and a clean merge", titles)
	}
	merged := gh.head(t, "acme/edge-sensors", claimedBranch)
	parents := strings.Fields(gh.git(t, gh.remotePath("acme/edge-sensors"), "rev-list", "--parents", "-n", "1", merged))
	if len(parents) != 3 || !slices.Contains(parents[1:], moved) || !slices.Contains(parents[1:], first) {
		t.Errorf("%s of the remote is at %v, want a merge of main's head %s into %s", claimedBranch, parents, short(moved), short(first))
	}
	if len(run.Gates) != 1 || !run.Gates[0].Passed || run.Gates[0].Head != merged {
		t.Errorf("the run recorded the gates %+v, want one pass on the merged head %s", run.Gates, short(merged))
	}
	if workers := gh.workers(t); len(workers) != 1 || run.RepairRounds != 0 {
		t.Errorf("the factory started %d sessions and counted %d repair rounds, want the implement session alone and no repair round: a clean merge needs nobody", len(workers), run.RepairRounds)
	}
}

// A red round of a gate on CI in the gate stage spends the gate's budget, whatever GitHub says of the
// draft: once it is spent, the run is blocked naming the checks that fail, and the draft stays a draft.
func TestAGateOnCIOverItsBudgetBlocksNamingTheFailingChecks(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.checksAre(t, claimedIssue, "", passed("lint"), failed("test", 4242))
	gh.answer(t, "run view 4242 --repo acme/edge-sensors --log-failed", "test\tFAIL: TestUploadRetries (0.01s)\n")
	cfg := ciConfig(data, nil)
	cfg["gate"] = map[string]any{"command": "ci", "rounds": 1}

	f := gh.work(t, cfg)
	run := f.ended(t, 1)
	if run.Outcome != outcomeBlocked {
		t.Fatalf("the run ended as %q (%s), want blocked; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	for _, want := range []string{"the gate fails after 1 of 1 fix sessions (gate.rounds)", "gate_checks: lint=pass, test=fail", "test https://github.com/o/r/actions/runs/4242/job/1"} {
		if !strings.Contains(run.Reason, want) {
			t.Errorf("the run was blocked because %q, want a reason with %q", run.Reason, want)
		}
	}
	if workers := gh.workers(t); len(workers) != 2 || run.RepairRounds != 0 {
		t.Errorf("the factory started %d sessions and counted %d repair rounds, want the implement session, one fix session and no repair round", len(workers), run.RepairRounds)
	}
	if !run.Draft || gh.made(t, pullReady) != 0 || gh.wrote(t, pullEdited) != "" {
		t.Errorf("the draft of a run blocked at the gate was touched after it was opened (draft %v)", run.Draft)
	}
}

// Where the gate runs is the class's: a repository whose gates run in the worktree opens no draft, and
// its pull request is opened in the pr stage, ready from the start. A run whose fix makes the final
// head a change of the class full, whose gate is on CI, opens the draft then, and its red round there
// spends the gate budget of the review.
func TestTheDraftIsOpenedAtTheFirstGateOnCIOfARun(t *testing.T) {
	t.Parallel()
	t.Run("gates in the worktree", func(t *testing.T) {
		t.Parallel()
		gh, data := ciClaim(t)
		cfg := ciGateConfig(data, "ci")
		cfg["repositories"] = []map[string]any{{"name": "acme/edge-sensors", "gate": map[string]any{"command": []string{"sh", "-c", "echo the local gate ran"}}}}

		f := gh.work(t, cfg)
		run := f.ended(t, 1)
		if run.Outcome != outcomeReady {
			t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
		}
		if len(run.Gates) != 1 || run.Gates[0].Command != `sh -c "echo the local gate ran"` || !strings.Contains(run.Gates[0].Tail, "the local gate ran") {
			t.Errorf("the run recorded the gates %+v, want the repository's command run in the worktree", run.Gates)
		}
		if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 1 || pulls[0].Draft == nil || *pulls[0].Draft {
			t.Errorf("the factory opened %+v, want one pull request that is no draft", pulls)
		}
		if gh.made(t, pullReady) != 0 {
			t.Error("the factory marked a pull request ready that it opened ready")
		}
	})
	t.Run("a class that changes to CI on the final head", func(t *testing.T) {
		t.Parallel()
		gh, data := ciClaim(t)
		gh.workerCommits(t, "docs/worked.md")
		gh.env = append(gh.env, "CLAUDE_SHIM_THEN_COMMIT=upload/retry.go")
		gh.verdict(t, "docs", 1, findings(t, Finding{Severity: "S2", Path: "docs/worked.md", Line: 1, Claim: "The heading is wrong.", Why: "It names the old tool.", Fix: "Rename it."}))
		gh.repairs(t, map[string]any{"outcome": "complete", "fixed": []string{"F1"}, "disputed": []map[string]any{}, "skipped": []map[string]any{}, "summary": "Fixed."})
		gh.checksAre(t, claimedIssue, "", failed("test", 4242))
		gh.answer(t, "run view 4242 --repo acme/edge-sensors --log-failed", "test\tFAIL: TestUploadRetries (0.01s)\n")
		cfg := classedConfig(data, docsClass([]string{}))
		cfg["gate"] = map[string]any{"command": "ci"}
		cfg["review"] = map[string]any{"gate_rounds": 0}

		f := gh.work(t, cfg)
		run := f.ended(t, 1)
		if run.Outcome != outcomeBlocked || !strings.Contains(run.Reason, "the gate fails on the final head after 0 of 0 fix sessions (review.gate_rounds)") ||
			!strings.Contains(run.Reason, "gate_checks: test=fail") {
			t.Fatalf("the run ended as %q (%s), want blocked on the gate budget of the review; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
		}
		titles := factoryTitles(run, "review round 1", "opened the draft ")
		if !equal(titles, []string{"review round 1 of 3", "opened the draft " + pullOfTheClaim}) {
			t.Errorf("the run said %v, want the draft opened after the review, at the gate on the final head", titles)
		}
		if len(run.Gates) != 1 || run.Gates[0].Stage != stageReview || run.Gates[0].Class != classFull || run.Gates[0].Command != "ci" {
			t.Errorf("the run recorded the gates %+v, want one on CI on the final head", run.Gates)
		}
		if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 1 || pulls[0].Draft == nil || !*pulls[0].Draft {
			t.Errorf("the factory opened %+v, want the one draft", pulls)
		}
	})
}

// A factory stopped while the gate waits on CI leaves the draft recorded on the run. The resumed run
// takes it on by the key and goes on at the gate stage of that draft, and a person who marked the draft
// ready meanwhile changes nothing: the stage is read from the record and the branch, the draft is not
// opened again and the pr stage still writes it and marks it ready. A pull request of the branch that
// is not the recorded one would have blocked the resume instead.
func TestARestartWhileTheGateWaitsOnCIResumesAtTheGateOfTheRecordedDraft(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.checksAre(t, claimedIssue, "", pending("test"))

	f := gh.work(t, ciGateConfig(data, "ci"))
	f.saw(t, "gate on CI: waiting")
	f.stop(t, syscall.SIGTERM)
	var stopped Run
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(data, "run-1.json"))), &stopped); err != nil {
		t.Fatal(err)
	}
	if stopped.Outcome != outcomeInterrupted || stopped.PullRequest != pullOfTheClaim || !stopped.Draft || stopped.Stage != stageGate {
		t.Fatalf("the run stopped at the gate is recorded %q in %q with %q, draft %v (%s), want interrupted at the gate with its draft",
			stopped.Outcome, stopped.Stage, stopped.PullRequest, stopped.Draft, stopped.Reason)
	}

	gh.issues(t, "acme/edge-sensors")
	gh.issue(t, "acme/edge-sensors", assignedTo(openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour)), "factory-bot"))
	gh.openPullsBy(t, "acme/edge-sensors", claimedBranch, listedPull{claimedIssue, "factory-bot", false})
	gh.checksAre(t, claimedIssue, "", passed("test"))
	again := gh.work(t, ciGateConfig(data, "ci"))
	resumed := again.ended(t, 2)
	if resumed.Outcome != outcomeReady || resumed.PullRequest != pullOfTheClaim {
		t.Fatalf("run 2 ended as %q with %q (%s), want ready with %s; the factory's log:\n%s", resumed.Outcome, resumed.PullRequest, resumed.Reason, pullOfTheClaim, again.output(t))
	}
	if !equal(resumed.Stages, []string{"gate", "review", "pr", "ci"}) || len(factoryTitles(resumed, "going on with the draft "+pullOfTheClaim)) != 1 {
		t.Errorf("the resume went through the stages %v and said %v, want it to go on with the draft at the gate stage", resumed.Stages, factoryTitles(resumed, "going on"))
	}
	if workers := gh.workers(t); len(workers) != 1 {
		t.Errorf("the factory started %d sessions over both runs, want the one implement session", len(workers))
	}
	if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 1 {
		t.Errorf("the factory opened %d pull requests over both runs, want the one draft", len(pulls))
	}
	if gh.made(t, pullReady) != 1 || gh.wrote(t, pullEdited) == "" {
		t.Error("the pr stage of the resume did not write the draft and mark it ready")
	}
}

// A run that stopped after its gate on CI passed recorded the pass on its panel: the resume goes on at
// the review stage with the recorded draft, runs no gate again, and the pr stage makes the draft ready.
func TestAResumeAfterTheGateOnCIGoesOnAtTheReviewStage(t *testing.T) {
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
	interrupted.Stages = []string{"implement", "gate", "review"}
	interrupted.PullRequest, interrupted.Draft = pullOfTheClaim, true
	gated := Classed{Class: classFull, For: classForGate, Head: head, Files: 1, Gate: gateCommand{CI: true}, Reviewers: defaultReview.Reviewers}
	interrupted.Panel = &Panel{Gate: "gate_result: pass (1 checks on CI) at " + short(head), GatedAt: head, Head: head, Rounds: []Round{}, Classes: []Classed{gated}}
	records(t, data, interrupted)
	gh.issues(t, "acme/edge-sensors")
	gh.issue(t, "acme/edge-sensors", assignedTo(openIssue(claimedIssue, claimedTitle, began.Add(-72*time.Hour)), "factory-bot"))
	gh.openPullsBy(t, "acme/edge-sensors", claimedBranch, listedPull{claimedIssue, "factory-bot", true})

	f := gh.work(t, ciGateConfig(data, "ci"))
	resumed := f.ended(t, 2)
	if resumed.Outcome != outcomeReady || resumed.PullRequest != pullOfTheClaim || resumed.Draft {
		t.Fatalf("run 2 ended as %q with %q, draft %v (%s), want ready with %s; the factory's log:\n%s",
			resumed.Outcome, resumed.PullRequest, resumed.Draft, resumed.Reason, pullOfTheClaim, f.output(t))
	}
	if !equal(resumed.Stages, []string{"review", "pr", "ci"}) || len(resumed.Gates) != 0 {
		t.Errorf("the resume went through the stages %v and ran the gates %+v, want it to start at the review stage and run no gate", resumed.Stages, resumed.Gates)
	}
	if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 0 || gh.made(t, pullReady) != 1 {
		t.Errorf("the resume opened %+v and marked the draft ready %d times, want no pull request opened and the draft made ready once", pulls, gh.made(t, pullReady))
	}
}

// Taking the routing label off an issue whose gate on CI is in a fix session ends that session with its
// process group, and the issue is given back.
func TestACancelDuringAFixSessionOfAGateOnCIEndsIt(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.unassigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.checksAre(t, claimedIssue, "", failed("test", 4242))
	gh.answer(t, "run view 4242 --repo acme/edge-sensors --log-failed", "FAIL: TestUploadRetries\n")
	gh.env = append(gh.env, "CLAUDE_SHIM_THEN_SLEEP=600")

	f := gh.work(t, ciGateConfig(data, "ci"))
	f.saw(t, "briefed a fix session of the gate")
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
	if run.Outcome != outcomeCancelled || !strings.Contains(run.Reason, "routing label") || run.Stage != stageGate {
		t.Fatalf("the run ended as %q in %q (%s), want cancelled by the label in the gate stage; the factory's log:\n%s", run.Outcome, run.Stage, run.Reason, f.output(t))
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

// Fake mode works a gate on CI on canned checks: the ready run's draft has its checks running, then
// failing, and passing once a fix session of the gate stage has repaired them; the review's fix moves
// the head, so the final head is gated on CI again. A pass of every check stands on its second reading.
func TestFakeModeGatesOnCannedChecks(t *testing.T) {
	t.Parallel()
	f := start(t, config{"deadline": "30s", "poll": "100ms", "repositories": []any{"acme/edge-sensors"}, "gate": map[string]any{"command": "ci"}})
	var run apiRun
	f.eventually(t, 150*time.Second, "the ready run to end", func() bool {
		run = apiRun{}
		f.get(t, "/api/runs/1", &run)
		return run.EndedAt != nil
	})
	if got := factoryTitles(run, "gate on CI: ", "opened the draft", "briefed a fix session of the gate"); strings.Join(got, " | ") !=
		"opened the draft https://github.com/acme/edge-sensors/pull/204 | gate on CI: waiting | gate on CI: checks-failed | briefed a fix session of the gate, 1 of 3 | "+
			"gate on CI: waiting | gate on CI: green | gate on CI: waiting | gate on CI: green" {
		t.Errorf("run 1 logged its gate as %q, want the draft opened, the checks waited on, failed, a fix session, and a pass read twice on the fix and on the final head", got)
	}
	if run.Outcome != "ready" || len(run.Gates) != 3 || run.Gates[0].Passed || !run.Gates[1].Passed || len(run.Gates[1].Checks) != 2 ||
		run.Gates[2].Stage != stageReview || !run.Gates[2].Passed {
		t.Errorf("run 1 ended %q with the gates %+v, want ready after a failed and a passed gate on the two canned checks and a pass on the final head", run.Outcome, run.Gates)
	}
}

// pushOnMain commits a file with that text on main of the shim's GitHub from a clone of its own, as
// somebody else's push, and answers the commit.
func (g *ghShim) pushOnMain(t *testing.T, file, text string) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	g.git(t, filepath.Dir(other), "clone", "-q", g.remotePath("acme/edge-sensors"), other)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(other, file)), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(other, file), text)
	g.git(t, other, "add", file)
	g.git(t, other, "-c", "user.email=somebody@example.com", "-c", "user.name=somebody", "commit", "-q", "-m", "chore: "+file)
	g.git(t, other, "push", "-q", "origin", "HEAD:refs/heads/main")
	return g.head(t, "acme/edge-sensors", "main")
}

// A repository with a workflow that runs when a draft is marked ready gets checks of its own after the
// pr stage marks the draft ready, and until one of them has finished the checks of the draft are not
// read as the pull request's: a ready-only check that fails is a failed check of the ci stage, one
// that passes lets the run end ready before the checks grace is out.
func TestTheCIStageWaitsForTheChecksOfMarkingTheDraftReady(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		check   map[string]any
		outcome string
	}{
		"a ready check that fails": {check: failed("ready", 88), outcome: outcomeBlocked},
		"a ready check that passes": {check: map[string]any{"name": "ready", "status": "COMPLETED", "conclusion": "SUCCESS",
			"completedAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "detailsUrl": "https://github.com/o/r/actions/runs/88/job/1"}, outcome: outcomeReady},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := ciClaim(t)
			gh.pushOnMain(t, ".github/workflows/ready.yml", "on:\n  pull_request:\n    types: [opened, synchronize, ready_for_review]\n")
			gh.checksAre(t, claimedIssue, "", passed("test"))
			cfg := ciGateConfig(data, "ci")
			cfg["ci"] = map[string]any{"repair_rounds": 1, "checks_grace": "60s", "bot_reviewers": []string{}}

			f := gh.work(t, cfg)
			f.saw(t, "made the draft ready")
			// The draft's checks passed, and still the ready pull request is waited on.
			f.saw(t, "ci: waiting")
			gh.checksAre(t, claimedIssue, "", passed("test"), c.check)
			run := f.ended(t, 1)
			if run.Outcome != c.outcome {
				t.Fatalf("the run ended as %q (%s), want %s on the check of marking the draft ready; the factory's log:\n%s", run.Outcome, run.Reason, c.outcome, f.output(t))
			}
			if c.outcome == outcomeBlocked && !strings.Contains(run.Reason, "ready https://github.com/o/r/actions/runs/88/job/1") {
				t.Errorf("the run was blocked because %q, want the ready check named", run.Reason)
			}
			if run.ReadiedAt == nil {
				t.Error("the run did not record when its draft was marked ready")
			}
		})
	}
}

// A merge of the base that conflicts during the gate on CI of the final head goes to a fix session after
// the last round, so the pull request says its commits are ones no reviewer read.
func TestAConflictDuringTheGateOnCIOfTheFinalHeadIsNamedUnreviewed(t *testing.T) {
	t.Parallel()
	gh, data := ciClaim(t)
	gh.verdict(t, "docs", 1, findings(t, Finding{Severity: "S2", Path: "worked.md", Line: 1, Claim: "The heading is wrong.", Why: "It names the old tool.", Fix: "Rename it."}))
	gh.repairs(t, map[string]any{"outcome": "complete", "fixed": []string{"F1"}, "disputed": []map[string]any{}, "skipped": []map[string]any{}, "summary": "Fixed."})
	gh.checksAre(t, claimedIssue, "", passed("test"))

	f := gh.work(t, ciGateConfig(data, "ci"))
	f.saw(t, "review round 1")
	gated := gh.head(t, "acme/edge-sensors", claimedBranch)
	gh.checksAre(t, claimedIssue, "", pending("test"))
	f.saw(t, "running the gate on the final head")
	var final string
	f.eventually(t, 60*time.Second, "the final head pushed", func() bool {
		final = gh.head(t, "acme/edge-sensors", claimedBranch)
		return final != gated
	})
	gh.pushOnMain(t, "worked.md", "somebody else's work\n")
	gh.answer(t, fmt.Sprintf("mergeable-%d-%s", claimedIssue, final), "CONFLICTING")
	f.saw(t, "the merge of main conflicts")
	gh.checksAre(t, claimedIssue, "", passed("test"))
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if run.Panel == nil || run.Panel.MergeFixes != 1 || run.Panel.StageFixes != 0 {
		t.Errorf("the run recorded the panel %+v, want the merge's fix counted as one of the final head", run.Panel)
	}
	if body := gh.wrote(t, pullEdited); !strings.Contains(body, "unreviewed: the fixes made after round 2") {
		t.Errorf("the pr stage wrote the body\n%s\nwant it to say the fixes after the last round are unreviewed", body)
	}
}
