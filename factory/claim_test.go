package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The remote claim, tested the way the rest of the factory is: the real binary, against the gh shim,
// a scripted worker on PATH and a local bare repository that stands in for the remote. What a claim
// did is read from that repository, from the worktree on disk and from how the worker was started.

// The issue every test below works, and the branch the contract gives it.
const (
	claimedIssue  = 104
	claimedTitle  = "Retry the upload when the broker drops"
	claimedBranch = "feat/104-retry-the-upload-when-the-broker-drops"
)

func TestAClaimCutsTheBranchFromTheFreshlyFetchedBaseAndRunsTheWorkerInItsWorktree(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	// The remote moves on after this host cloned it: only a claim that fetches the base first cuts the
	// branch from what main holds now, and a worker on yesterday's commit is a worker that rebases.
	moved := gh.commitOn(t, "acme/edge-sensors", "main")

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}, "worker_args": []string{"--model", "opus"}})
	run := f.ended(t, 1)

	if run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if run.Branch != claimedBranch || run.Base != "main" {
		t.Errorf("the run records %q off %q, want %q off main", run.Branch, run.Base, claimedBranch)
	}
	if want := fmt.Sprintf("https://github.com/acme/edge-sensors/pull/%d", claimedIssue); run.PullRequest != want {
		t.Errorf("the run carries the pull request %q, want %q", run.PullRequest, want)
	}

	// The claim itself: the branch is on the remote, at the head the base has now.
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != moved {
		t.Errorf("%s of the remote is at %q, want the head of the base after the fetch (%s)", claimedBranch, head, moved)
	}
	// The issue is assigned to the user this host is logged in as, and exactly once.
	assigned := fmt.Sprintf("issue edit %d --repo acme/edge-sensors --add-assignee factory-bot", claimedIssue)
	if made := gh.made(t, assigned); made != 1 {
		t.Errorf("the factory made `gh %s` %d times, want once after it won the claim", assigned, made)
	}

	// The worktree is in this host's clone, under the path the local workflow uses — .claude/worktrees
	// and the branch with its slashes turned into hyphens — and it is what the worker ran in, on the
	// branch the claim created and at the commit it was cut from.
	worktree := filepath.Join(clone, ".claude", "worktrees", "feat-104-retry-the-upload-when-the-broker-drops")
	if _, err := os.Stat(filepath.Join(worktree, "README.md")); err != nil {
		t.Fatalf("the worktree of the claim is not in the clone: %v", err)
	}
	// And the clone does not report its own worktrees as untracked, as the local workflow's clone does
	// not: the same entry in the same file.
	exclude, err := os.ReadFile(filepath.Join(clone, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(exclude), ".claude/worktrees/") {
		t.Errorf("the clone does not exclude .claude/worktrees/ (%v): git would report every worktree of it as untracked\n%s", err, exclude)
	}
	workers := gh.workers(t)
	if len(workers) != 1 {
		t.Fatalf("the factory started %d workers, want one", len(workers))
	}
	worker := workers[0]
	if worker.cwd != resolved(t, worktree) {
		t.Errorf("the worker ran in %s, want the worktree of the claim %s", worker.cwd, resolved(t, worktree))
	}
	if worker.branch != claimedBranch || worker.head != moved {
		t.Errorf("the worker ran on %s at %s, want %s at %s", worker.branch, worker.head, claimedBranch, moved)
	}

	// How it was started: the worker agent, the work skill as its prompt, the stream on its output,
	// the auto permission mode and the configured extra arguments.
	for _, want := range [][]string{
		{"--agent", "worker"},
		{"--output-format", "stream-json"},
		{"--permission-mode", "auto"},
		{"--model", "opus"},
		{"-p", "/worker:work"},
	} {
		if !worker.started(want...) {
			t.Errorf("the worker was started as %v, want %v in it", worker.args, want)
		}
	}
	for _, want := range []string{"--verbose", "--strict-mcp-config"} {
		if !worker.started(want) {
			t.Errorf("the worker was started as %v, want %s in it", worker.args, want)
		}
	}
	settings := worker.settings(t)
	if settings.Env["WF_ISSUE"] != strconv.Itoa(claimedIssue) || settings.Env["WF_MODE"] != "manual" {
		t.Errorf("the worker's settings carry %v, want the issue number and the manual mode: the factory never merges", settings.Env)
	}
	// The base the branch was cut from is the base the worker works against: its review range, its
	// hand-over note and the pull request it opens all ask the pipeline for it.
	if settings.Env["WF_BASE_BRANCH"] != "main" {
		t.Errorf("the worker's settings carry WF_BASE_BRANCH=%q, want main, the base the claim cut the branch from", settings.Env["WF_BASE_BRANCH"])
	}
	// The planner and the orchestrator are switched off, as a local claim switches them off: an
	// unattended session carries no /orchestrator:merge.
	for _, plugin := range []string{"planner@workflows", "orchestrator@workflows"} {
		if on, named := settings.EnabledPlugins[plugin]; !named || on {
			t.Errorf("the worker's settings leave %s enabled (%v); a factory session must not carry it", plugin, settings.EnabledPlugins)
		}
	}
	// The compact pin reaches the session, which is the only safety net it has: nothing hands a
	// factory session over, so it has to compact where the workflow says.
	if settings.AutoCompactWindow != compactWindow || settings.Env["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"] != compactPercentage {
		t.Errorf("the worker compacts at %q%% of %d, want %s%% of %d, the pin the workflow sets",
			settings.Env["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"], settings.AutoCompactWindow, compactPercentage, compactWindow)
	}

	// Its own process group, so that the deadline and the stop reach everything the session starts.
	if worker.pid != worker.pgid {
		t.Errorf("the worker (process %d) is in group %d, want a process group of its own", worker.pid, worker.pgid)
	}
	if worker.pgid == f.cmd.Process.Pid {
		t.Errorf("the worker is in the factory's own process group (%d); ending it would end the factory", worker.pgid)
	}
}

// A repository that does not branch off its default branch: the base of the configuration decides
// where the branch is cut, and the worker is told the same base, because the pipeline inside the
// worktree asks for it again — for the range its reviewers read and for the pull request it opens.
func TestAClaimOfARepositoryWithItsOwnBaseCutsAndWorksFromThatBase(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	// dev is a line of its own on the remote, ahead of main, and the clone's origin/HEAD still names
	// main: nothing but the configuration says dev.
	gh.branchAt(t, "acme/edge-sensors", "dev", gh.head(t, "acme/edge-sensors", "main"))
	dev := gh.commitOn(t, "acme/edge-sensors", "dev")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []any{map[string]any{"name": "acme/edge-sensors", "base": "dev"}}})
	run := f.ended(t, 1)

	if run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if run.Base != "dev" {
		t.Errorf("the run records the base %q, want dev, the base its repository is configured with", run.Base)
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != dev {
		t.Errorf("%s of the remote is at %q, want the head of dev (%s) and not of main", claimedBranch, head, dev)
	}
	workers := gh.workers(t)
	if len(workers) != 1 {
		t.Fatalf("the factory started %d workers, want one", len(workers))
	}
	if base := workers[0].settings(t).Env["WF_BASE_BRANCH"]; base != "dev" {
		t.Errorf("the worker's settings carry WF_BASE_BRANCH=%q; it would review against %[1]s a branch cut from dev, and open its pull request against it", base)
	}
}

// A claim that fails after the branch was created is the one failure that leaves something on the
// remote. The run says so, because nothing rolls a claim back ([ADR 0026]) and the operator is the
// one who decides.
//
// [ADR 0026]: ../docs/adr/0026-what-waits-waits-for-a-person.md
func TestAClaimThatFailsAfterTheBranchWasCreatedSaysWhatItLeftBehind(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	// No answer for the assignment: the claim wins the branch and fails one step later.

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	if run.Outcome != "failed" {
		t.Fatalf("the run ended as %q (%s), want failed; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	for _, want := range []string{claimedBranch, "left behind"} {
		if !strings.Contains(run.Reason, want) {
			t.Errorf("the run says %q, want %q in it: the branch is on the remote and holds the issue", run.Reason, want)
		}
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head == "" {
		t.Errorf("the run says it left %s on the remote, and the branch is not there", claimedBranch)
	}
	if len(gh.workers(t)) != 0 {
		t.Errorf("a claim that failed started a worker: %v", gh.workers(t))
	}
	if _, err := os.Stat(filepath.Join(clone, ".claude", "worktrees")); !os.IsNotExist(err) {
		t.Errorf("a claim that failed made a worktree in %s: %v", clone, err)
	}
}

// The claim decides who works an issue, and a claimer that meets the branch on the remote has lost.
// It touches nothing else and does not come back to the issue while the branch is there ([ADR 0024]).
//
// [ADR 0024]: ../docs/adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md
func TestAClaimAnotherClaimerWonIsRecordedAsLostAndTouchesNothingElse(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	// The other claimer was here first: the branch is on the remote, at the head of the base.
	held := gh.head(t, "acme/edge-sensors", "main")
	gh.branchAt(t, "acme/edge-sensors", claimedBranch, held)

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	if run.Outcome != "lost" {
		t.Fatalf("the run ended as %q (%s), want lost; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if run.Branch != claimedBranch {
		t.Errorf("the lost run records the branch %q, want %q: the branch another claimer holds it by", run.Branch, claimedBranch)
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != held {
		t.Errorf("%s of the remote is at %q, want the commit the other claimer put it at (%s)", claimedBranch, head, held)
	}
	if len(gh.workers(t)) != 0 {
		t.Errorf("a claimer that lost started a worker: %v", gh.workers(t))
	}
	if _, err := os.Stat(filepath.Join(clone, ".claude", "worktrees")); !os.IsNotExist(err) {
		t.Errorf("a claimer that lost made a worktree in %s: %v", clone, err)
	}
	for _, call := range gh.calls(t) {
		if strings.HasPrefix(call, "issue edit ") || strings.HasPrefix(call, "api user") {
			t.Errorf("a claimer that lost called `gh %s`; the issue is the winner's and nothing else was touched", call)
		}
	}

	// And it does not try the issue again while the branch is there: the run is that record.
	asked := "api " + issuesRequest("acme/edge-sensors", "factory")
	f.eventually(t, 20*time.Second, "several more polls", func() bool { return gh.made(t, asked) >= 8 })
	var line apiLine
	f.get(t, "/api/line", &line)
	if len(line.Done) != 1 || len(line.Now) != 0 {
		t.Errorf("after %d polls the factory has %d ended and %d running runs, want the one lost run", gh.made(t, asked), len(line.Done), len(line.Now))
	}
}

// Two factories reach for the same issue in the same moment: the shim holds both inside the one act
// that creates the branch and lets them through together, so the race is run rather than described.
// Exactly one of them may own the issue.
func TestTwoClaimersRacingForOneIssueLeaveExactlyOneWinner(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	gh.holdClaims(t)
	base := gh.head(t, "acme/edge-sensors", "main")

	claimers := []*factory{}
	for i := 0; i < 2; i++ {
		claimers = append(claimers, gh.work(t, config{"poll": "50ms", "deadline": "90s",
			"data_dir": filepath.Join(t.TempDir(), "data"), "repositories": []string{"acme/edge-sensors"}}))
	}
	claimers[0].eventually(t, 60*time.Second, "both claimers inside the creation of the branch", func() bool {
		return gh.claimersWaiting(t) == 2
	})
	gh.openClaims(t)

	outcomes := []string{}
	for _, f := range claimers {
		outcomes = append(outcomes, f.ended(t, 1).Outcome)
	}
	won, lost := 0, 0
	for _, outcome := range outcomes {
		switch outcome {
		case "ready":
			won++
		case "lost":
			lost++
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("the two claimers ended as %v, want one ready and one lost; the logs:\n%s\n%s",
			outcomes, claimers[0].output(t), claimers[1].output(t))
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != base {
		t.Errorf("%s of the remote is at %q, want the head of the base (%s)", claimedBranch, head, base)
	}
	if workers := gh.workers(t); len(workers) != 1 {
		t.Errorf("%d workers were started for one issue, want the winner's alone", len(workers))
	}
}

// The factory may be started from a maintainer's terminal, and a worker that inherited HERDR_ENV
// would act in that person's Herdr session instead of ending blocked ([ADR 0027]). A WF_ variable
// left in that same terminal must not reach it either: the session's settings say what this run is,
// and WF_MODE=yolo would be a worker that merges what it built.
//
// [ADR 0027]: ../docs/adr/0027-the-factorys-isolation-boundary-is-the-host.md
func TestTheWorkerRunsWithNoHerdrAndNoWorkflowVariableOfTheFactorysOwnEnvironment(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	gh.env = append(gh.env, "HERDR_ENV=1", "HERDR_SESSION_ID=7", "HERDR_PANE_ID=%3",
		"WF_MODE=yolo", "WF_BASE_BRANCH=release", "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=99")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	f.ended(t, 1)

	workers := gh.workers(t)
	if len(workers) != 1 {
		t.Fatalf("the factory started %d workers, want one", len(workers))
	}
	for _, name := range workers[0].env {
		if strings.HasPrefix(name, "HERDR_") {
			t.Errorf("the worker's environment carries %s; it would act in the maintainer's Herdr session", name)
		}
		if strings.HasPrefix(name, "WF_") || name == "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE" {
			t.Errorf("the worker's environment carries %s; the session's settings are what say what this run is, and nothing else may answer for that name", name)
		}
	}
	// The rest of the environment is the factory's own, so the check above is not passed by an empty one.
	if !workers[0].carries("PATH") {
		t.Errorf("the worker's environment carries no PATH: %v", workers[0].env)
	}
}

// One worker at a time, and the next one starts when the run before it ended rather than at the next
// poll: a factory that polls once a minute must not leave the line standing for a minute per run.
func TestTheNextRunStartsWhenTheOneBeforeItEndedAndNeverBesideIt(t *testing.T) {
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.issues(t, "acme/edge-sensors",
		openIssue(claimedIssue, claimedTitle, now.Add(-72*time.Hour)),
		openIssue(118, "Document the calibration procedure", now.Add(-40*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", claimedIssue, labeled("factory", now.Add(-6*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 118, labeled("factory", now.Add(-time.Hour)))
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", 118, "factory-bot")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	// The poll is far longer than a run of the scripted worker: the second run can only be one the
	// end of the first started.
	const poll = 30 * time.Second
	f := gh.work(t, config{"poll": poll.String(), "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})

	first, second := f.ended(t, 1), f.ended(t, 2)
	if first.Issue != claimedIssue || second.Issue != 118 {
		t.Errorf("the factory worked #%d and then #%d, want the oldest routing first", first.Issue, second.Issue)
	}
	if second.StartedAt.Before(*first.EndedAt) {
		t.Errorf("run 2 started at %s, before run 1 ended at %s: the factory ran two workers at once",
			second.StartedAt, first.EndedAt)
	}
	if waited := second.StartedAt.Sub(*first.EndedAt); waited > poll/3 {
		t.Errorf("run 2 started %s after run 1 ended, with a poll of %s: it waited for the next poll", waited, poll)
	}
}

// ---- the drift tests: the shell rules this Go restates ----

// The branch a claim creates is the workflow's branch contract, and the orchestrator's claim.sh
// builds it as "$(wf_branch_type "$labels")/$issue-$(wf_slug "$title")". This runs that shell over
// the titles and labels that make a slug hard and holds the Go against it ([ADR 0022]).
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
func TestTheBranchNameAgreesWithTheOrchestratorsShell(t *testing.T) {
	for _, c := range []struct {
		name  string
		issue Issue
	}{
		{name: "a plain title", issue: Issue{Number: 104, Title: "Retry the upload when the broker drops"}},
		{name: "umlauts", issue: Issue{Number: 9, Title: "Überwachung: Füllstand fällt unter den Schwellwert"}},
		{name: "a sharp s", issue: Issue{Number: 12, Title: "Straße messen"}},
		{name: "a URL", issue: Issue{Number: 21, Title: "Follow https://example.com/a/b?c=d in the docs"}},
		{name: "punctuation only", issue: Issue{Number: 33, Title: "!!! ??? ..."}},
		{name: "a title of leading punctuation", issue: Issue{Number: 34, Title: "-- fix the thing --"}},
		{name: "a title far over the cut", issue: Issue{Number: 41, Title: "Rewrite the ingestion pipeline so that late events are folded into the window they belong to"}},
		{name: "a title cut where a hyphen falls", issue: Issue{Number: 42, Title: "Rewrite the ingest pipeline for late a events"}},
		{name: "a bug", issue: Issue{Number: 51, Title: "Crash on start", Labels: []string{readyLabel, "bug"}}},
		{name: "a fix", issue: Issue{Number: 52, Title: "Crash on start", Labels: []string{"fix"}}},
		{name: "docs", issue: Issue{Number: 53, Title: "Explain the gate", Labels: []string{"documentation"}}},
		{name: "a chore", issue: Issue{Number: 54, Title: "Bump the pins", Labels: []string{"maintenance"}}},
		{name: "a label that names none of them", issue: Issue{Number: 55, Title: "Add a knob", Labels: []string{"enhancement"}}},
		{name: "bug and docs at once", issue: Issue{Number: 56, Title: "Wrong example", Labels: []string{"docs", "bug"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			want := shellBranchName(t, c.issue)
			if got := branchName(c.issue); got != want {
				t.Errorf("the factory names the branch of %q %q; the orchestrator's shell names it %q", c.issue.Title, got, want)
			}
		})
	}
}

// The base branch rule is wf_base_branch in the orchestrator's lib.sh: the explicit setting, then the
// head the remote points at, then the repository's default branch on GitHub, then main. The explicit
// setting is WF_BASE_BRANCH there, which a session is given by the repository's own settings file;
// the factory reads that file out of the clone and takes the configuration of its host above it. The
// rest is the same question asked of the same clone ([ADR 0022]).
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
func TestTheBaseBranchRuleAgreesWithTheOrchestratorsShell(t *testing.T) {
	for _, c := range []struct {
		name string
		// The explicit setting, as each side is given it: WF_BASE_BRANCH for the shell, and for the
		// factory either the host's configuration or the settings file of the repository's checkout.
		explicit   string
		declared   bool
		originHead string
		onGitHub   string
		want       string
	}{
		{name: "the explicit setting of the configuration wins", explicit: "dev", originHead: "trunk", onGitHub: "release", want: "dev"},
		{name: "the explicit setting of the repository wins", explicit: "dev", declared: true, originHead: "trunk", onGitHub: "release", want: "dev"},
		{name: "then the head of the remote", originHead: "trunk", onGitHub: "release", want: "trunk"},
		{name: "then the default branch on GitHub", onGitHub: "release", want: "release"},
		{name: "and main when nothing answers", want: "main"},
	} {
		t.Run(c.name, func(t *testing.T) {
			gh := newGhShim(t)
			gh.remote(t, "acme/edge-sensors")
			clone := gh.cloneInto(t, t.TempDir(), "acme/edge-sensors")
			configured := c.explicit
			if c.declared { // the repository says it in its own settings instead
				configured = ""
				declaresBase(t, clone, c.explicit)
			}
			if c.originHead != "" {
				gh.git(t, clone, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+c.originHead)
			} else {
				gh.git(t, clone, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
			}
			if c.onGitHub != "" {
				// The shell asks for the repository of its checkout, the factory for the one it is
				// working; without an answer gh fails, which is the rule's last step.
				gh.answer(t, "repo view --json defaultBranchRef -q .defaultBranchRef.name", c.onGitHub+"\n")
				gh.answer(t, "repo view acme/edge-sensors --json defaultBranchRef --jq .defaultBranchRef.name", c.onGitHub+"\n")
			}

			want := shellBaseBranch(t, gh, clone, c.explicit)
			if want != c.want {
				t.Fatalf("the orchestrator's shell answers %q, want %q: the fixture does not set up the case it means", want, c.want)
			}
			inProcess(t, gh)
			if got := baseBranch(context.Background(), Connected{Name: "acme/edge-sensors", Base: configured}, clone); got != want {
				t.Errorf("the factory branches off %q; the orchestrator's shell branches off %q", got, want)
			}
		})
	}
}

// The two explicit settings meet in one case the shell has no word for: a host that connects a
// repository under a base of its own. The configuration is the operator's and wins, and a settings
// file that names no branch name is read as if the repository had said nothing, because the value
// reaches a ref and a command line.
func TestTheConfiguredBaseWinsOverWhatARepositoryDeclaresAndAnUnusableDeclarationIsNone(t *testing.T) {
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	clone := gh.cloneInto(t, t.TempDir(), "acme/edge-sensors")
	gh.git(t, clone, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	inProcess(t, gh)

	declaresBase(t, clone, "trunk")
	if got := baseBranch(context.Background(), Connected{Name: "acme/edge-sensors", Base: "dev"}, clone); got != "dev" {
		t.Errorf("the factory branches off %q, want dev: this host connected the repository under that base", got)
	}
	for _, unusable := range []string{"-dev", "../../etc", "", "refs/heads/dev "} {
		declaresBase(t, clone, unusable)
		if got := baseBranch(context.Background(), Connected{Name: "acme/edge-sensors"}, clone); got != "main" {
			t.Errorf("a repository that declares %q is branched off %q, want main, the head of the remote: the declaration is no branch name", unusable, got)
		}
	}
	writeSettings(t, clone, "{ this is not JSON\n")
	if got := baseBranch(context.Background(), Connected{Name: "acme/edge-sensors"}, clone); got != "main" {
		t.Errorf("a repository whose settings are not JSON is branched off %q, want main, the head of the remote", got)
	}
}

// The compact pin is the workflow's, not the factory's: the local claim sets the window and the
// percentage ([ADR 0031], [ADR 0034]) and this driver restates them, so a session it starts compacts
// where the workflow says. The numbers are read out of claim.sh itself, which is what makes a change
// to one of them fail here until the other copy follows ([ADR 0022]).
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
// [ADR 0031]: ../docs/adr/0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md
// [ADR 0034]: ../docs/adr/0034-the-compact-trigger-is-raised-through-the-window.md
func TestTheCompactPinAgreesWithTheOrchestratorsClaim(t *testing.T) {
	script := readFile(t, abs(t, filepath.Join("..", "plugins", "orchestrator", "scripts", "claim.sh")))
	pinned := func(name string) string {
		t.Helper()
		found := regexp.MustCompile(`(?m)^` + name + `=([0-9]+)$`).FindStringSubmatch(script)
		if found == nil {
			t.Fatalf("the orchestrator's claim.sh pins no %s; the factory restates that number and cannot be held to it", name)
		}
		return found[1]
	}
	if window := pinned("compact_window"); window != strconv.Itoa(compactWindow) {
		t.Errorf("a worker of the factory compacts at a window of %d, the local claim pins %s", compactWindow, window)
	}
	if percentage := pinned("compact_pct"); percentage != compactPercentage {
		t.Errorf("a worker of the factory compacts at %s%% of its window, the local claim pins %s%%", compactPercentage, percentage)
	}
}

// shellBranchName is the branch the orchestrator's claim.sh names for an issue, built by the shell
// itself out of the helpers in its lib.sh.
func shellBranchName(t *testing.T, issue Issue) string {
	t.Helper()
	return strings.TrimSpace(shell(t, "",
		`. "$1"; printf '%s/%s-%s\n' "$(wf_branch_type "$3")" "$2" "$(wf_slug "$4")"`,
		nil, orchestratorLib(t), strconv.Itoa(issue.Number), strings.Join(issue.Labels, ","), issue.Title))
}

// declaresBase writes the base a repository declares for itself into its checkout: WF_BASE_BRANCH in
// the env block of the .claude/settings.json the repository carries, which is where a local session
// is given the variable wf_base_branch reads.
func declaresBase(t *testing.T, clone, base string) {
	t.Helper()
	writeSettings(t, clone, fmt.Sprintf(`{"env":{"WF_BASE_BRANCH":%q}}`+"\n", base))
}

// writeSettings puts a settings file into a checkout, whatever it holds.
func writeSettings(t *testing.T, clone, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(clone, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(clone, ".claude", "settings.json"), body)
}

// shellBaseBranch is what wf_base_branch answers in a checkout, with the gh shim on its PATH.
func shellBaseBranch(t *testing.T, gh *ghShim, clone, explicit string) string {
	t.Helper()
	return strings.TrimSpace(shell(t, clone, `. "$1"; wf_base_branch`,
		append(gh.env, "WF_BASE_BRANCH="+explicit), orchestratorLib(t)))
}

func orchestratorLib(t *testing.T) string {
	t.Helper()
	return abs(t, filepath.Join("..", "plugins", "orchestrator", "scripts", "lib.sh"))
}

// shell runs one bash script in a directory and answers with its output.
func shell(t *testing.T, dir, script string, env []string, args ...string) string {
	t.Helper()
	bash := exec.Command("bash", append([]string{"-c", "set -eu; " + script, "shell"}, args...)...)
	bash.Dir = dir
	bash.Env = env
	if env == nil {
		bash.Env = gitIsolation()
	}
	var said bytes.Buffer
	bash.Stderr = &said
	out, err := bash.Output()
	if err != nil {
		t.Fatalf("the shell this rule is bound to failed: %v: %s", err, said.String())
	}
	return string(out)
}

// inProcess points this test process at the shim, so that a rule called as a function reaches the
// same gh the binary would.
func inProcess(t *testing.T, gh *ghShim) {
	t.Helper()
	for _, entry := range gh.env {
		name, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "GH_SHIM_") || strings.HasPrefix(name, "GIT_CONFIG") || name == "PATH" {
			t.Setenv(name, value)
		}
	}
}

// ---- reading how the workers were started ----

// workerStart is one start of the scripted worker: where it ran and on what, the process and its
// group, the command line it was given and the names in its environment.
type workerStart struct {
	cwd, branch, head string
	pid, pgid         int
	args              []string
	env               []string
}

// started says whether the arguments appear in the command line in that order, next to each other:
// a flag and its value belong together.
func (w workerStart) started(args ...string) bool {
	for i := 0; i+len(args) <= len(w.args); i++ {
		if equal(w.args[i:i+len(args)], args) {
			return true
		}
	}
	return false
}

// carries says whether the environment holds a variable of that name.
func (w workerStart) carries(name string) bool {
	for _, entry := range w.env {
		if entry == name {
			return true
		}
	}
	return false
}

// sessionSettings is what a worker was started with in --settings: the session's own variables, the
// plugins it carries and the window it compacts at.
type sessionSettings struct {
	Env               map[string]string `json:"env"`
	EnabledPlugins    map[string]bool   `json:"enabledPlugins"`
	AutoCompactWindow int               `json:"autoCompactWindow"`
}

// settings is the session settings the worker was started with.
func (w workerStart) settings(t *testing.T) sessionSettings {
	t.Helper()
	raw := ""
	for i, arg := range w.args {
		if arg == "--settings" && i+1 < len(w.args) {
			raw = w.args[i+1]
		}
	}
	if raw == "" {
		t.Fatalf("the worker was started without --settings: %v", w.args)
	}
	var settings sessionSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		t.Fatalf("the worker's --settings is not JSON: %v: %s", err, raw)
	}
	return settings
}

// workers is every start of the scripted worker, in the order they happened.
func (g *ghShim) workers(t *testing.T) []workerStart {
	t.Helper()
	raw, err := os.ReadFile(g.worker)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	started := []workerStart{}
	for _, line := range strings.Split(string(raw), "\n") {
		field, value, _ := strings.Cut(line, " ")
		if field == "call" {
			started = append(started, workerStart{})
			continue
		}
		if len(started) == 0 {
			continue
		}
		w := &started[len(started)-1]
		switch field {
		case "cwd":
			w.cwd = value
		case "branch":
			w.branch = value
		case "head":
			w.head = value
		case "pid", "pgid":
			number, err := strconv.Atoi(value)
			if err != nil {
				t.Fatalf("the worker wrote %q as its %s", value, field)
			}
			if field == "pid" {
				w.pid = number
			} else {
				w.pgid = number
			}
		case "arg":
			w.args = append(w.args, value)
		case "env":
			w.env = append(w.env, value)
		}
	}
	return started
}

// routed is one repository with one issue routed to the factory, which is the fixture every claim
// test starts from.
func (g *ghShim) routed(t *testing.T, repository string, issue int, title string) {
	t.Helper()
	now := time.Now().UTC()
	g.remote(t, repository)
	g.issues(t, repository, openIssue(issue, title, now.Add(-72*time.Hour)))
	g.timeline(t, repository, issue, labeled("factory", now.Add(-6*time.Hour)))
}

// resolved is a path with every symlink on it followed, which is what a process that asks the kernel
// where it is answers: on macOS a temporary directory is reached through one.
func resolved(t *testing.T, path string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
