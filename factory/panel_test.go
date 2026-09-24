package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The review stage is the factory's ([ADR 0043], step 4): the work session stops after the gate, and
// the factory runs the reviewers of the panel as read-only sessions of its own, a fix session for every
// round that asks for fixes, and the gate on the final head before the pr stage.
//
// [ADR 0043]: ../docs/adr/0043-the-migration-runs-from-the-last-stage-to-the-first.md

// panelClaim is ciClaim on a repository whose gate is this make recipe: make check runs it in the
// worktree when the review's fixes moved the branch.
func panelClaim(t *testing.T, recipe string) (*ghShim, string) {
	t.Helper()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerCommits(t, "worked.md")
	gh.env = append(gh.env, "GIT_AUTHOR_NAME=factory", "GIT_AUTHOR_EMAIL=factory@example.com",
		"GIT_COMMITTER_NAME=factory", "GIT_COMMITTER_EMAIL=factory@example.com")
	gh.gateIs(t, "acme/edge-sensors", recipe)
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{})
	return gh, data
}

// gateIs puts a Makefile whose check target runs the recipe on the main branch of the shim's GitHub.
func (g *ghShim) gateIs(t *testing.T, repository, recipe string) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "gate")
	g.git(t, filepath.Dir(work), "clone", "-q", g.remotePath(repository), work)
	writeFile(t, filepath.Join(work, "Makefile"), ".PHONY: check\ncheck:\n\t"+recipe+"\n")
	g.git(t, work, "add", "Makefile")
	g.git(t, work, "commit", "-q", "-m", "the gate")
	g.git(t, work, "push", "-q", "origin", "HEAD:main")
}

// findings is a reviewer's result: fix when any finding is S1 or S2.
func findings(t *testing.T, found ...Finding) string {
	t.Helper()
	verdict := verdictPass
	for _, f := range found {
		if f.Severity != "S3" {
			verdict = verdictFix
		}
	}
	type plain struct {
		Severity string `json:"severity"`
		Path     string `json:"path"`
		Line     int    `json:"line"`
		Claim    string `json:"claim"`
		Why      string `json:"why"`
		Fix      string `json:"fix"`
	}
	out := []plain{}
	for _, f := range found {
		out = append(out, plain{f.Severity, f.Path, f.Line, f.Claim, f.Why, f.Fix})
	}
	return marshal(t, map[string]any{"verdict": verdict, "findings": out})
}

// repairs has the claude shim answer every fix session of the review with this report.
func (g *ghShim) repairs(t *testing.T, report map[string]any) {
	t.Helper()
	g.env = append(g.env, "CLAUDE_SHIM_REPAIR_RESULT="+marshal(t, report))
}

// agentOf is the inline agent a reviewer session was started as: its name and its definition.
func agentOf(t *testing.T, w workerStart) (string, map[string]any) {
	t.Helper()
	i := slices.Index(w.args, "--agent")
	j := slices.Index(w.args, "--agents")
	if i < 0 || j < 0 || i+1 >= len(w.args) || j+1 >= len(w.args) {
		t.Fatalf("the reviewer session was started without --agents and --agent: %v", w.args)
	}
	var defs map[string]map[string]any
	if err := json.Unmarshal([]byte(w.args[j+1]), &defs); err != nil {
		t.Fatalf("the reviewer session's --agents is not JSON: %v", err)
	}
	return w.args[i+1], defs[w.args[i+1]]
}

// The work session stops after the gate. The factory then starts the five reviewers beside each other,
// each read-only, as the inline agent of its own prompt with its tools and its model, in the run's
// worktree, briefed with the gate result the work session reported; they pass, and the pull request
// carries the panel the factory derived from their verdicts. The branch did not move, so the gate does
// not run again.
func TestThePanelRunsTheFactorysReviewersReadOnlyBesideEachOther(t *testing.T) {
	t.Parallel()
	gh, data := panelClaim(t, "@echo the gate ran, which it should not have; exit 1")
	// Every reviewer waits for the other four to have started before it reports.
	gh.env = append(gh.env, "CLAUDE_SHIM_REVIEW_BARRIER=5")
	c := ciConfig(data, nil)
	c["worker_args"] = []string{"--model", "claude-opus-5"}
	f := gh.work(t, c)
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if !equal(run.Stages, []string{"implement", "review", "pr", "ci"}) {
		t.Errorf("the run went through the stages %v, want implement, review, pr and ci", run.Stages)
	}
	workers := gh.workers(t)
	if len(workers) != 1 {
		t.Fatalf("the factory started %d worker sessions, want the work session alone", len(workers))
	}
	if stop := workers[0].settings(t).Env["WF_STOP_AFTER"]; stop != "gate" {
		t.Errorf("the work session ran with WF_STOP_AFTER=%q, want gate", stop)
	}

	reviewers := gh.reviewerSessions(t)
	models := map[string]string{"code": "sonnet", "security": "inherit", "docs": "sonnet", "tests": "sonnet", "senior": "inherit"}
	seen := []string{}
	for _, r := range reviewers {
		agent, def := agentOf(t, r)
		name := strings.TrimSuffix(agent, "-reviewer")
		seen = append(seen, name)
		if !r.started("--tools", "Read,Grep,Glob") || !r.started("--strict-mcp-config") || !r.started("--setting-sources", "user") {
			t.Errorf("the %s reviewer was started with %v, want only Read, Grep and Glob, no MCP server and the user's settings alone", name, r.args)
		}
		if plugins := r.settings(t).EnabledPlugins; plugins["worker@workflows"] {
			t.Errorf("the %s reviewer carries the worker plugin", name)
		}
		if def["model"] != models[name] || !strings.Contains(text(def["prompt"]), "read-only") || !equal(anyStrings(def["tools"]), []string{"Read", "Grep", "Glob"}) {
			t.Errorf("the %s reviewer runs as %v, want its own prompt, Read, Grep and Glob, and the model %s", name, def, models[name])
		}
		// A reviewer whose definition names its model runs on it; one that inherits runs on the model
		// the host's worker_args name.
		if onHost := r.started("--model", "claude-opus-5"); onHost != (models[name] == "inherit") {
			t.Errorf("the %s reviewer, whose model is %s, was started with %v", name, models[name], r.args)
		}
		if r.cwd != workers[0].cwd {
			t.Errorf("the %s reviewer ran in %s, want the run's worktree %s", name, r.cwd, workers[0].cwd)
		}
	}
	slices.Sort(seen)
	if !equal(seen, []string{"code", "docs", "security", "senior", "tests"}) {
		t.Errorf("the factory started the reviewers %v, want the five of the panel once each", seen)
	}
	brief := strings.Join(factoryBodies(run, "briefed the reviewers"), "\n")
	for _, want := range []string{"Review round 1 of 3", "gate_result: pass (exit 0) at c0ffee0", "worked.md", "Issue #104"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the reviewers' brief does not carry %q:\n%s", want, brief)
		}
	}
	if titles := factoryTitles(run, "running the gate"); len(titles) != 0 {
		t.Errorf("the factory ran the gate again on a branch no fix moved: %v", titles)
	}
	pulls := gh.opened(t, "acme/edge-sensors")
	want := "review_rounds: 1\npanel: code=PASS security=PASS docs=PASS tests=PASS senior=PASS\nfixed: 0 (S1 0, S2 0, S3 0)\ndisputed: none"
	if len(pulls) != 1 || !strings.Contains(pulls[0].Body, want) || strings.Contains(pulls[0].Body, "did not pass") {
		t.Errorf("the factory opened %+v, want one pull request with the panel that passed", pulls)
	}
}

// A round in which reviewers ask for fixes has one fix session given every finding of the round, which
// fixes one, disputes one and skips a nit. The next round runs only the reviewers that asked, and when
// they pass the review ends; the gate runs on the head the fixes moved to, and the pull request carries
// the verdicts over both rounds, the fixes by severity and the dispute word for word.
func TestMixedVerdictsTakeASecondRoundOfTheReviewersThatAskedForFixes(t *testing.T) {
	t.Parallel()
	gh, data := panelClaim(t, "@echo the gate ran on $$(git rev-parse --short HEAD)")
	gh.verdict(t, "code", 1, findings(t,
		Finding{Severity: "S2", Path: "upload/retry.go", Line: 42, Claim: "The backoff is never reset.", Why: "The next failure waits too long.", Fix: "Reset it on success."},
		Finding{Severity: "S3", Path: "upload/retry.go", Line: 7, Claim: "The comment is stale.", Why: "It names the old limit.", Fix: "Drop it."}))
	gh.verdict(t, "tests", 1, findings(t,
		Finding{Severity: "S1", Path: "upload/retry_test.go", Line: 18, Claim: "The test sleeps for the backoff.", Why: "It is flaky on a slow host.", Fix: "Inject the clock."}))
	const reason = "the clock is injected already; the sleep is the fake clock's and returns at once"
	gh.repairs(t, map[string]any{"outcome": "complete", "fixed": []string{"F1"},
		"disputed": []map[string]any{{"finding": "F3", "reason": reason}},
		"skipped":  []map[string]any{{"finding": "F2", "reason": "a comment is no bug"}}, "summary": "Fixed the backoff."})

	f := gh.work(t, ciConfig(data, nil))
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	reviewers := gh.reviewerSessions(t)
	second := []string{}
	for _, r := range reviewers[min(5, len(reviewers)):] {
		agent, _ := agentOf(t, r)
		second = append(second, agent)
	}
	slices.Sort(second)
	if len(reviewers) != 7 || !equal(second, []string{"code-reviewer", "tests-reviewer"}) {
		t.Errorf("the factory started %d reviewer sessions, the second round %v, want five and then code and tests", len(reviewers), second)
	}
	workers := gh.workers(t)
	if len(workers) != 2 || !strings.Contains(strings.Join(workers[1].args, " "), `"disputed"`) {
		t.Fatalf("the factory started %d worker sessions, want the work session and one fix session of the review", len(workers))
	}
	repair := strings.Join(factoryBodies(run, "briefed the fix session of review round 1"), "\n")
	for _, want := range []string{"code: F1 [S2] upload/retry.go:42 — The backoff is never reset.", "code: F2 [S3]", "tests: F3 [S1] upload/retry_test.go:18", "round 1 of 3"} {
		if !strings.Contains(repair, want) {
			t.Errorf("the fix session's brief does not carry %q:\n%s", want, repair)
		}
	}
	if brief := strings.Join(factoryBodies(run, "briefed the reviewers"), "\n"); !strings.Contains(brief, "Review round 2 of 3") {
		t.Errorf("the second round's reviewers were not briefed as such:\n%s", brief)
	}
	// The fix moved the branch, so the gate ran on its head.
	head := gh.head(t, "acme/edge-sensors", claimedBranch)
	if titles := factoryTitles(run, "gate_result: "); !equal(titles, []string{"gate_result: pass (exit 0) at " + short(head)}) {
		t.Errorf("the factory logged the gates %v, want one pass on the final head %s", titles, short(head))
	}
	pulls := gh.opened(t, "acme/edge-sensors")
	if len(pulls) != 1 {
		t.Fatalf("the factory opened %d pull requests, want one", len(pulls))
	}
	body := pulls[0].Body
	for _, want := range []string{"review_rounds: 2\npanel: code=FIX→PASS security=PASS docs=PASS tests=FIX→PASS senior=PASS\nfixed: 1 (S1 0, S2 1, S3 0)\n",
		"disputed: round 1, tests F3 [S1] upload/retry_test.go:18 — The test sleeps for the backoff.", "disputed: " + reason,
		"gate_result: pass (exit 0) at " + short(head), "gate_command: make check"} {
		if !strings.Contains(body, want) {
			t.Errorf("the body does not carry %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "did not pass") {
		t.Errorf("the body of a panel whose reviewers passed says it did not:\n%s", body)
	}
}

// A reviewer that still asks for fixes after the last round leaves the panel not passed. The round
// limit is the repository's own, over the host's, and the pull request is opened all the same, not as
// a draft, naming the reviewer.
func TestAFixVerdictThatOutlastsTheRoundsIsNamedInThePullRequest(t *testing.T) {
	t.Parallel()
	gh, data := panelClaim(t, "@true")
	gh.verdict(t, "tests", 0, findings(t, Finding{Severity: "S2", Path: "docs/preview.md", Line: 12, Claim: "No browser test.", Why: "A regression goes unseen.", Fix: "Add one."}))
	gh.repairs(t, map[string]any{"outcome": "complete", "fixed": []string{},
		"disputed": []map[string]any{{"finding": "F1", "reason": "the device has no browser"}}, "skipped": []map[string]any{}, "summary": "Disputed."})
	c := ciConfig(data, nil)
	c["review"] = map[string]any{"rounds": 3}
	c["repositories"] = []map[string]any{{"name": "acme/edge-sensors", "review": map[string]any{"rounds": 2}}}
	f := gh.work(t, c)
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if reviewers := gh.reviewerSessions(t); len(reviewers) != 6 {
		t.Errorf("the factory started %d reviewer sessions, want five and then the tests reviewer once more", len(reviewers))
	}
	if titles := factoryTitles(run, "the review ends after"); !equal(titles, []string{"the review ends after 2 of 2 rounds with a fix verdict standing"}) {
		t.Errorf("the factory said %v at the end of the review, want that the rounds were spent", titles)
	}
	pulls := gh.opened(t, "acme/edge-sensors")
	if len(pulls) != 1 || pulls[0].Draft == nil || *pulls[0].Draft {
		t.Fatalf("the factory opened %+v, want one pull request that is not a draft", pulls)
	}
	for _, want := range []string{"tests=FIX→FIX", "disputed: the device has no browser", "**The reviewer panel did not pass:**", "tests"} {
		if !strings.Contains(pulls[0].Body, want) {
			t.Errorf("the body does not carry %q:\n%s", want, pulls[0].Body)
		}
	}
}

// A reviewer whose result does not fit its schema fails the run, which names the reviewer, and no
// pull request is opened: a verdict that contradicts its findings is no verdict. So does a fix session
// that leaves an S1 or S2 neither fixed nor disputed.
func TestAResultOfTheReviewThatDoesNotFitFailsTheRunNamingIt(t *testing.T) {
	t.Parallel()
	blocking := findings(t, Finding{Severity: "S1", Path: "a.go", Line: 1, Claim: "It panics.", Why: "Nil map.", Fix: "Make it."})
	for name, c := range map[string]struct {
		prepare func(*ghShim, *testing.T)
		reason  []string
	}{
		"a reviewer": {func(gh *ghShim, t *testing.T) {
			gh.verdict(t, "security", 1, strings.Replace(blocking, `"verdict":"fix"`, `"verdict":"pass"`, 1))
			// Every reviewer reports once all five run, and stays a moment after it has, so each reports
			// while the others are still running and the round is the last thing the run does.
			gh.env = append(gh.env, "CLAUDE_SHIM_REVIEW_BARRIER=5", "CLAUDE_SHIM_REVIEW_LINGER=1")
		}, []string{"the security reviewer: ", "the verdict is pass with an S1 or S2 finding standing"}},
		"a reviewer with more findings than it reports": {func(gh *ghShim, t *testing.T) {
			found := []Finding{}
			for range maxFindings + 1 {
				found = append(found, Finding{Severity: "S3", Path: "a.go", Line: 1, Claim: "A nit.", Why: "Taste.", Fix: "Change it."})
			}
			gh.verdict(t, "docs", 1, findings(t, found...))
		}, []string{"the docs reviewer: ", "carries 51 findings, more than the 50 a reviewer reports"}},
		"a fix session": {func(gh *ghShim, t *testing.T) {
			gh.verdict(t, "code", 1, blocking)
			gh.repairs(t, map[string]any{"outcome": "complete", "fixed": []string{}, "disputed": []map[string]any{}, "skipped": []map[string]any{}, "summary": "Nothing."})
		}, []string{"leaves the S1 finding F1 neither fixed nor disputed"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := panelClaim(t, "@true")
			c.prepare(gh, t)
			f := gh.work(t, ciConfig(data, nil))
			run := f.ended(t, 1)
			if run.Outcome != outcomeFailed {
				t.Fatalf("the run ended as %q (%s), want failed; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
			}
			for _, want := range c.reason {
				if !strings.Contains(run.Reason, want) {
					t.Errorf("the run failed because %q, want a reason with %q", run.Reason, want)
				}
			}
			if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 0 {
				t.Errorf("the factory opened %d pull requests after a review that did not fit", len(pulls))
			}
			// Every session printed its result line, which gave its own totals, whether or not its result fit.
			if run.Totals != totalsWorker {
				t.Errorf("the run's totals come from %q, want %s: every session reported its own", run.Totals, totalsWorker)
			}
		})
	}
}

// A panel that reports as much as it may still gives its fix session a brief the command line takes:
// every finding is in it by its id, cut to its share when they are too long together, and the brief
// stays below the 128 KiB Linux holds an argument to.
func TestTheFixSessionOfAFullPanelGetsEveryFindingWithinTheArgumentLimit(t *testing.T) {
	t.Parallel()
	gh, data := panelClaim(t, "@true")
	// 250 findings of this length are far beyond the argument limit together.
	long := strings.Repeat("The retry loop keeps its backoff across successes. ", 16)
	ids := []string{}
	for _, name := range defaultReview.Reviewers {
		found := []Finding{}
		for i := range maxFindings {
			found = append(found, Finding{Severity: "S2", Path: "upload/retry.go", Line: i + 1, Claim: "The backoff is never reset.", Why: long, Fix: "Reset it on success."})
			ids = append(ids, "F"+strconv.Itoa(len(ids)+1))
		}
		gh.verdict(t, name, 1, findings(t, found...))
	}
	gh.repairs(t, map[string]any{"outcome": "complete", "fixed": ids, "disputed": []map[string]any{}, "skipped": []map[string]any{}, "summary": "Fixed them all."})
	c := ciConfig(data, nil)
	c["review"] = map[string]any{"rounds": 1} // the fix session is what this is about, not the rounds after it
	f := gh.work(t, c)
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	workers := gh.workers(t)
	if len(workers) != 2 {
		t.Fatalf("the factory started %d worker sessions, want the work session and one fix session of the review", len(workers))
	}
	if longest := workers[1].longest; longest >= 128*1024 {
		t.Errorf("the fix session's longest argument is %d bytes, which Linux refuses at 128 KiB", longest)
	}
	brief := strings.Join(workers[1].args, "\n")
	for _, id := range ids {
		if !strings.Contains(brief, id+" [S2] upload/retry.go:") {
			t.Fatalf("the fix session's brief does not carry the finding %s", id)
		}
	}
}

// When the gate fails on the head the review's fixes moved to, a fix session is given its output and
// the gate runs again, within the gate's budget; over the budget the run is blocked and says why.
func TestAGateThatFailsOnTheFinalHeadGoesToAFixSessionWithinItsBudget(t *testing.T) {
	t.Parallel()
	// worked.md has a line for every session that worked the branch: the work session's, the review fix
	// session's and then the gate fix session's.
	const gate = `@lines=$$(wc -l < worked.md | tr -d ' '); echo "worked.md has $$lines lines"; test $$lines -ge 3`
	for name, c := range map[string]struct {
		recipe  string
		knobs   map[string]any
		outcome string
		fixes   int
	}{
		"fixed within the budget": {gate, nil, outcomeReady, 1},
		"over the budget":         {"@echo the gate never passes; exit 2", map[string]any{"gate_rounds": 1}, outcomeBlocked, 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := panelClaim(t, c.recipe)
			gh.verdict(t, "code", 1, findings(t, Finding{Severity: "S2", Path: "a.go", Line: 3, Claim: "Off by one.", Why: "It skips the last.", Fix: "Use <=."}))
			gh.repairs(t, map[string]any{"outcome": "complete", "fixed": []string{"F1"}, "disputed": []map[string]any{}, "skipped": []map[string]any{}, "summary": "Fixed."})
			cfg := ciConfig(data, nil)
			if c.knobs != nil {
				cfg["review"] = c.knobs
			}
			f := gh.work(t, cfg)
			run := f.ended(t, 1)
			if run.Outcome != c.outcome {
				t.Fatalf("the run ended as %q (%s), want %s; the factory's log:\n%s", run.Outcome, run.Reason, c.outcome, f.output(t))
			}
			// The work session, the fix session of the review and the fix session of the gate.
			if workers := gh.workers(t); len(workers) != 2+c.fixes {
				t.Errorf("the factory started %d worker sessions, want %d", len(workers), 2+c.fixes)
			}
			brief := strings.Join(factoryBodies(run, "briefed a fix session of the gate, 1 of "+map[bool]string{true: "1", false: "2"}[c.knobs != nil]), "\n")
			if !strings.Contains(brief, "gate_result: fail (exit 2)") {
				t.Errorf("the gate's fix session was not given the failed gate:\n%s", brief)
			}
			if c.outcome == outcomeReady {
				if !strings.Contains(brief, "worked.md has 2 lines") {
					t.Errorf("the gate's fix session was not given the gate's output:\n%s", brief)
				}
				head := gh.head(t, "acme/edge-sensors", claimedBranch)
				if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 1 || !strings.Contains(pulls[0].Body, "gate_result: pass (exit 0) at "+short(head)) {
					t.Errorf("the factory opened %+v, want one pull request with the gate that passed on %s", pulls, short(head))
				}
				return
			}
			for _, want := range []string{"the gate fails on the final head after 1 of 1 fix sessions (review.gate_rounds)", "the gate never passes"} {
				if !strings.Contains(run.Reason, want) {
					t.Errorf("the run was blocked because %q, want a reason with %q", run.Reason, want)
				}
			}
			if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 0 {
				t.Errorf("the factory opened %d pull requests on a gate that fails", len(pulls))
			}
		})
	}
}

// A fix session that reports complete and leaves its changes uncommitted fails the run: the factory
// goes on by the commit the branch is at, so the fix it reports would reach no reviewer's commit, no
// gate's and no push. The changes stay in the worktree, which the reason names with them.
func TestAFixSessionThatLeavesItsChangesUncommittedFailsTheRun(t *testing.T) {
	t.Parallel()
	for name, prepare := range map[string]func(*ghShim, *testing.T){
		"of the review": func(gh *ghShim, t *testing.T) {
			gh.verdict(t, "code", 1, findings(t, Finding{Severity: "S2", Path: "a.go", Line: 3, Claim: "Off by one.", Why: "It skips the last.", Fix: "Use <=."}))
			gh.repairs(t, map[string]any{"outcome": "complete", "fixed": []string{"F1"}, "disputed": []map[string]any{}, "skipped": []map[string]any{}, "summary": "Fixed."})
		},
		"of the gate": func(gh *ghShim, t *testing.T) {
			gh.env = append(gh.env, `CLAUDE_SHIM_RESULT={"outcome":"complete","gateResult":"gate_result: fail (exit 2) at c0ffee0","commits":["c0ffee0 feat: the work"],"summary":"stopped after gate"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := panelClaim(t, "@echo the gate fails; exit 2")
			gh.env = append(gh.env, "CLAUDE_SHIM_THEN_UNCOMMITTED=1")
			prepare(gh, t)
			f := gh.work(t, ciConfig(data, nil))
			run := f.ended(t, 1)
			if run.Outcome != outcomeFailed {
				t.Fatalf("the run ended as %q (%s), want failed; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
			}
			for _, want := range []string{"the session of the stage review reported complete and left changes it did not commit", "M worked.md", run.Worktree} {
				if !strings.Contains(run.Reason, want) {
					t.Errorf("the run failed because %q, want a reason with %q", run.Reason, want)
				}
			}
			if workers := gh.workers(t); len(workers) != 2 {
				t.Errorf("the factory started %d worker sessions, want the work session and the fix session", len(workers))
			}
			if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 0 {
				t.Errorf("the factory opened %d pull requests without the fix the session reported", len(pulls))
			}
		})
	}
}

// A run that stopped during the review is resumed at the review stage with the rounds it recorded: no
// work session and no recorded round runs again. A round whose fix session had not reported yet has
// that session run first.
func TestAResumeDuringTheReviewGoesOnFromTheRecordedRounds(t *testing.T) {
	t.Parallel()
	for name, repaired := range map[string]bool{"after the fix of the round": true, "before the fix of the round": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh := newGhShim(t)
			gh.remote(t, "acme/edge-sensors")
			gh.loggedInAs(t, "factory-bot")
			data := filepath.Join(t.TempDir(), "data")
			clone := gh.cloneInto(t, data, "acme/edge-sensors")
			gh.branchAt(t, "acme/edge-sensors", claimedBranch, gh.head(t, "acme/edge-sensors", "main"))
			head := gh.commitOn(t, "acme/edge-sensors", claimedBranch)

			round := Round{Number: 1, Head: head, Verdicts: []Verdict{
				{Reviewer: "code", Verdict: verdictFix, Findings: []Finding{{ID: "F1", Severity: "S2", Path: "a.go", Line: 3, Claim: "Off by one.", Why: "It skips the last.", Fix: "Use <=."}}},
				{Reviewer: "security", Verdict: verdictPass, Findings: []Finding{}}, {Reviewer: "docs", Verdict: verdictPass, Findings: []Finding{}},
				{Reviewer: "tests", Verdict: verdictPass, Findings: []Finding{}}, {Reviewer: "senior", Verdict: verdictPass, Findings: []Finding{}},
			}}
			if repaired {
				round.Repair = &Repair{Fixed: []string{"F1"}, Disputed: []Excuse{}, Skipped: []Excuse{}, Summary: "Fixed."}
			} else {
				gh.repairs(t, map[string]any{"outcome": "complete", "fixed": []string{"F1"}, "disputed": []map[string]any{}, "skipped": []map[string]any{}, "summary": "Fixed."})
			}
			began := time.Now().UTC().Add(-2 * time.Hour)
			interrupted := record(1, claimedIssue, claimedTitle, signalRouted, outcomeInterrupted, true, began, began.Add(30*time.Minute))
			interrupted.Worktree = filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
			interrupted.Stages = []string{"implement", "review"}
			interrupted.Panel = &Panel{Gate: "gate_result: pass (exit 0) at " + head[:7], GatedAt: head, Head: head, Round: 1, Rounds: []Round{round}}
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
			if !equal(resumed.Stages, []string{"review", "pr", "ci"}) {
				t.Errorf("the resume went through the stages %v, want review, pr and ci", resumed.Stages)
			}
			wantRepairs := map[bool]int{true: 0, false: 1}[repaired]
			if workers := gh.workers(t); len(workers) != wantRepairs {
				t.Errorf("the resume started %d worker sessions, want %d: no work session and no fix of a round that has one", len(workers), wantRepairs)
			}
			reviewers := gh.reviewerSessions(t)
			if len(reviewers) != 1 {
				t.Fatalf("the resume started %d reviewer sessions, want the code reviewer of round 2 alone", len(reviewers))
			}
			if agent, _ := agentOf(t, reviewers[0]); agent != "code-reviewer" {
				t.Errorf("the resume started the %s, want the code reviewer", agent)
			}
			if titles := factoryTitles(resumed, "review round"); !equal(titles, []string{"review round 2 of 3"}) {
				t.Errorf("the resume ran the rounds %v, want round 2 alone", titles)
			}
			if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 1 || !strings.Contains(pulls[0].Body, "review_rounds: 2\npanel: code=FIX→PASS security=PASS") {
				t.Errorf("the resume opened %+v, want one pull request with both rounds", pulls)
			}
		})
	}
}

// anyStrings is a JSON array of strings as Go reads it into an any.
func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := []string{}
	for _, item := range list {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}
