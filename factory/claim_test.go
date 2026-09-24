package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")

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
	// the auto permission mode, the schema of its result and the configured extra arguments.
	for _, want := range [][]string{
		{"--agent", "worker"},
		{"--output-format", "stream-json"},
		{"--permission-mode", "auto"},
		{"--json-schema", resultSchema},
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
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
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

// A host sets the worker knobs of its runs in its configuration, and they reach the session the way
// a local claim's --env does: in the env block of --settings, where they win over the repository's
// own settings for that session. The knobs of the ci stage are the factory's own, written under "ci",
// and reach no session: the work session stops after the pull request, and the factory counts the
// repair rounds and answers the reviews itself. What the run is stays the factory's whatever the file
// says.
func TestTheHostsWorkerKnobsReachTheSessionBesideWhatTheRunIs(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"},
		"worker_env":   map[string]string{"WF_HANDOFF_TOKENS": "150000"},
		"ci":           map[string]any{"bot_reviewers": []string{}, "review_wait": "5s"}})
	run := f.ended(t, 1)

	if run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	workers := gh.workers(t)
	if len(workers) != 1 {
		t.Fatalf("the factory started %d workers, want one", len(workers))
	}
	env := workers[0].settings(t).Env
	for _, moved := range []string{"WF_PR_BOT_REVIEWERS", "WF_PR_REVIEW_WAIT", "WF_CI_REPAIR_ROUNDS", "WF_CHECKS_GRACE", "WF_REVIEW_MANDATE", "WF_REVIEWERS", "WF_REVIEW_ROUNDS"} {
		if value, set := env[moved]; set {
			t.Errorf("the worker's settings carry %s=%q; no session runs the worker's stage whose knob that is", moved, value)
		}
	}
	if tokens := env["WF_HANDOFF_TOKENS"]; tokens != "150000" {
		t.Errorf("the worker's settings carry WF_HANDOFF_TOKENS=%q, want the host's 150000", tokens)
	}
	if env["WF_MODE"] != "manual" || env["WF_ISSUE"] != strconv.Itoa(claimedIssue) || env["WF_BASE_BRANCH"] != "main" {
		t.Errorf("the worker's settings carry %v; the mode, the issue and the base are the run's own and stay beside the host's knobs", env)
	}
}

// The knobs a host may set are the ones a local claim may set, and no other: the list is the
// orchestrator's (env_accepted in claim.sh), read out of the script, so a knob added to one driver
// fails here until the other follows ([ADR 0022]).
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
func TestTheWorkerKnobsAgreeWithTheOrchestratorsClaim(t *testing.T) {
	t.Parallel()
	script := readFile(t, abs(t, filepath.Join("..", "plugins", "orchestrator", "scripts", "claim.sh")))
	found := regexp.MustCompile(`(?m)^env_accepted="([A-Z0-9_ ]+)"$`).FindStringSubmatch(script)
	if found == nil {
		t.Fatalf("the orchestrator's claim.sh names no env_accepted; the factory restates that list and cannot be held to it")
	}
	if accepted := strings.Fields(found[1]); !slices.Equal(accepted, workerKnobs) {
		t.Errorf("worker_env accepts %v, the local claim's --env accepts %v; the two drivers set the same knobs of the one worker", workerKnobs, accepted)
	}
}

// A repository moves its line of work after this host cloned it: it declares another base in its own
// settings, the file a local session is given WF_BASE_BRANCH by. The claim reads what the repository
// says now — the clone's working tree is the day it was written and is never checked out again — so
// the branch is cut from the base the repository names today and the worker is told that base.
func TestAClaimReadsTheBaseTheRepositoryDeclaresNowAndNotTheOneItsCloneWasWrittenWith(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	// What this host cloned: a repository that branched off main, and the settings file to prove it.
	writeSettings(t, clone, `{"env":{"WF_BASE_BRANCH":"main"}}`+"\n")
	// What the repository is today: a line of its own, and its settings say so on the remote.
	gh.branchAt(t, "acme/edge-sensors", "dev", gh.head(t, "acme/edge-sensors", "main"))
	declaresBase(t, gh, clone, "acme/edge-sensors", "main", "dev")
	dev := gh.commitOn(t, "acme/edge-sensors", "dev")

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	if run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if run.Base != "dev" {
		t.Errorf("the run records the base %q, want dev, the base the repository declares on the remote now", run.Base)
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != dev {
		t.Errorf("%s of the remote is at %q, want the head of dev (%s): the clone's old working tree still says main", claimedBranch, head, dev)
	}
	workers := gh.workers(t)
	if len(workers) != 1 {
		t.Fatalf("the factory started %d workers, want one", len(workers))
	}
	if base := workers[0].settings(t).Env["WF_BASE_BRANCH"]; base != "dev" {
		t.Errorf("the worker's settings carry WF_BASE_BRANCH=%q, want dev: it would review against %[1]s a branch cut from dev", base)
	}
}

// The remote moves its default branch after this host cloned it: a repository that adopts a line of
// its own, or renames the one it had. A clone remembers the head it was written with and a fetch
// never touches that memory, so the claim asks the remote for it again — the branch is cut from the
// base the remote points at now, and a repository whose old default is gone is still a repository
// this factory works.
func TestAClaimFollowsTheRemoteWhenItMovesItsDefaultBranch(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors") // cloned while the remote's head was main
	// The repository moves its work to dev and lets main go, as a rename does.
	gh.branchAt(t, "acme/edge-sensors", "dev", gh.head(t, "acme/edge-sensors", "main"))
	dev := gh.commitOn(t, "acme/edge-sensors", "dev")
	gh.defaultBranchIs(t, "acme/edge-sensors", "dev")
	gh.dropBranch(t, "acme/edge-sensors", "main")

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	if run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if run.Base != "dev" {
		t.Errorf("the run records the base %q, want dev, the branch the remote points at now", run.Base)
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != dev {
		t.Errorf("%s of the remote is at %q, want the head of dev (%s)", claimedBranch, head, dev)
	}
	// The shim has no answer for `repo view`, so a run that reached the rule's third step would have
	// failed: the head above is the one the remote was asked for, not GitHub's default branch.
	workers := gh.workers(t)
	if len(workers) != 1 {
		t.Fatalf("the factory started %d workers, want one", len(workers))
	}
	if base := workers[0].settings(t).Env["WF_BASE_BRANCH"]; base != "dev" {
		t.Errorf("the worker's settings carry WF_BASE_BRANCH=%q, want dev, the base its branch was cut from", base)
	}
}

// A claim that fails after the branch was created is the one failure that leaves something on the
// remote. The run says so, because nothing rolls a claim back ([ADR 0026]) and the operator is the
// one who decides.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func TestAClaimThatFailsAfterTheBranchWasCreatedSaysWhatItLeftBehind(t *testing.T) {
	t.Parallel()
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

// A claim that fails before it created anything took nothing: the run says so, so that an operator
// reading it knows there is no branch to remove, and the failure is never read as a lost race — an
// issue nobody claimed would otherwise be given away by a factory that could not reach GitHub.
func TestAClaimThatFailsBeforeTheBranchExistsSaysNothingWasClaimed(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		failing  string
		loggedIn bool
	}{
		{name: "the user this host is logged in as cannot be read", failing: "api user*"},
		{name: "the branch cannot be created", failing: "api --method POST repos/acme/edge-sensors/git/refs*", loggedIn: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			gh := newGhShim(t)
			gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
			if c.loggedIn {
				gh.loggedInAs(t, "factory-bot")
			}
			gh.fail(t, c.failing)

			data := filepath.Join(t.TempDir(), "data")
			clone := gh.cloneInto(t, data, "acme/edge-sensors")
			f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
				"repositories": []string{"acme/edge-sensors"}})
			run := f.ended(t, 1)

			if run.Outcome != "failed" {
				t.Fatalf("the run ended as %q (%s), want failed; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
			}
			if !strings.Contains(run.Reason, "nothing was claimed") {
				t.Errorf("the run says %q, want that nothing was claimed on the remote: there is no branch for an operator to remove", run.Reason)
			}
			if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != "" {
				t.Errorf("the run says it claimed nothing and %s is on the remote at %s", claimedBranch, head)
			}
			if len(gh.workers(t)) != 0 {
				t.Errorf("a claim that failed started a worker: %v", gh.workers(t))
			}
			if _, err := os.Stat(filepath.Join(clone, ".claude", "worktrees")); !os.IsNotExist(err) {
				t.Errorf("a claim that failed made a worktree in %s: %v", clone, err)
			}
		})
	}
}

// A claim that failed without touching anything failed for a reason of this host or of GitHub, and
// the issue behind it in the line would meet the same one. Because the next run starts the moment one
// ends, a repository whose claims fail would be worked through in seconds, every issue of it spent on
// a run that reached nothing. The repository is held instead, until a person starts the factory again
// ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func TestARepositoryWhoseClaimTouchedNothingIsHeldInsteadOfSpendingItsLine(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.issues(t, "acme/edge-sensors",
		openIssue(claimedIssue, claimedTitle, now.Add(-72*time.Hour)),
		openIssue(105, "Roll the log files", now.Add(-71*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", claimedIssue, labeled("factory", now.Add(-6*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 105, labeled("factory", now.Add(-5*time.Hour)))
	gh.fail(t, "api user*") // this host cannot read the user it is logged in as

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	if run.Outcome != "failed" || run.Issue != claimedIssue {
		t.Fatalf("run 1 is issue #%d ended as %q (%s), want the head of the line failed; the factory's log:\n%s",
			run.Issue, run.Outcome, run.Reason, f.output(t))
	}
	// Several more polls: the issue behind it is offered every one of them and taken by none.
	asked := "api " + issuesRequest("acme/edge-sensors", "factory")
	f.eventually(t, 20*time.Second, "several more polls", func() bool { return gh.made(t, asked) >= 8 })
	var line apiLine
	f.get(t, "/api/line", &line)
	if len(line.Queue) != 1 || line.Queue[0].Number != 105 || len(line.Done) != 1 {
		t.Errorf("after %d polls the line holds %d waiting and %d ended runs, want issue #105 still waiting after the one failure",
			gh.made(t, asked), len(line.Queue), len(line.Done))
	}
	if records, _ := filepath.Glob(filepath.Join(f.data, "run-*.json")); len(records) != 1 {
		t.Errorf("the factory wrote %d run records, want the one failure: the rest of the line is held, not spent", len(records))
	}
	if said := strings.Count(f.output(t), "until it is started again"); said != 1 {
		t.Errorf("the factory says %d times that it holds the repository, want once and not per poll; its log:\n%s", said, f.output(t))
	}
}

// An issue whose repository has no clone on this host cannot be worked at all: the worktree a worker
// runs in is made in that clone. A run is what takes an issue out of the line for good, so the issue
// keeps its place instead — a host that could not reach one repository when it started would
// otherwise spend that repository's whole line on runs that never touched GitHub.
func TestAnIssueOfARepositoryWithoutACloneKeepsItsPlaceInTheLine(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.issues(t, "acme/edge-sensors", openIssue(claimedIssue, claimedTitle, now.Add(-72*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", claimedIssue, labeled("factory", now.Add(-6*time.Hour)))
	// The repository behind it in the line, which this host does have a clone of: its issue is what
	// proves the line is walked past the one that cannot be claimed rather than stopped at it.
	gh.routed(t, "acme/backtest", 118, "Roll the log files")
	gh.timeline(t, "acme/backtest", 118, labeled("factory", now.Add(-5*time.Hour)))
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/backtest", 118, "factory-bot")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/backtest")
	gh.fail(t, "repo clone*") // the host cannot reach the other repository when the factory connects

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors", "acme/backtest"}})
	run := f.ended(t, 1)
	if run.Outcome != "ready" || run.Issue != 118 {
		t.Fatalf("run 1 is issue #%d ended as %q (%s), want the issue behind the unclonable one worked; the factory's log:\n%s",
			run.Issue, run.Outcome, run.Reason, f.output(t))
	}

	// Several more polls: the issue without a clone is offered every one of them and taken by none.
	asked := "api " + issuesRequest("acme/edge-sensors", "factory")
	f.eventually(t, 20*time.Second, "several more polls", func() bool { return gh.made(t, asked) >= 8 })
	var line apiLine
	f.get(t, "/api/line", &line)
	if len(line.Queue) != 1 || line.Queue[0].Number != claimedIssue || len(line.Now) != 0 || len(line.Done) != 1 {
		t.Errorf("after %d polls the line holds %d waiting, %d running and %d ended runs, want issue #%d still waiting",
			gh.made(t, asked), len(line.Queue), len(line.Now), len(line.Done), claimedIssue)
	}
	if records, _ := filepath.Glob(filepath.Join(f.data, "run-*.json")); len(records) != 1 {
		t.Errorf("the factory wrote %d run records, want the one of the repository it can work: a run of the other would take its issue out of the line for good", len(records))
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != "" {
		t.Errorf("the factory created %s on the remote without a clone to work it in", claimedBranch)
	}
	if said := strings.Count(f.output(t), "has no clone on this host"); said != 1 {
		t.Errorf("the factory says %d times that it has no clone of the repository, want once and not per poll; its log:\n%s", said, f.output(t))
	}
}

// A worker that ends in an error is a failed run, and what it said is the cause the record carries:
// the claim itself was won, so the branch stays on the remote and the operator reads why the session
// ended where it did.
func TestAWorkerThatEndsInAnErrorFailsTheRunWithWhatItSaid(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	said := "error: --agent 'worker' not found"
	f := launch(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}}, append(gh.env, "CLAUDE_SHIM_FAIL="+said))
	run := f.ended(t, 1)

	if run.Outcome != "failed" {
		t.Fatalf("the run ended as %q (%s), want failed; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if !strings.Contains(run.Reason, said) {
		t.Errorf("the run says %q, want the worker's own error (%q) in it", run.Reason, said)
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head == "" {
		t.Errorf("%s is not on the remote; the claim was won before the worker failed and nothing rolls it back", claimedBranch)
	}
}

// The claim decides who works an issue, and a claimer that meets the branch on the remote has lost.
// It touches nothing else and does not come back to the issue while the branch is there ([ADR 0024]).
//
// [ADR 0024]: ../docs/adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md
func TestAClaimAnotherClaimerWonIsRecordedAsLostAndTouchesNothingElse(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")

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
	// The creation of the reference is the only thing it tried, and GitHub refused it. Nothing of the
	// issue itself was touched: reading which user this host is is a read of the host, not of GitHub's
	// copy of the issue.
	for _, call := range gh.calls(t) {
		if strings.HasPrefix(call, "issue edit ") {
			t.Errorf("a claimer that lost called `gh %s`; the issue is the winner's and nothing else was touched", call)
		}
	}

	if run.Holding {
		t.Errorf("the lost run says it holds the issue; the branch is the other claimer's and so is the issue")
	}

	// And it does not try the issue again while the branch is there: the run is that record. The
	// issue stays routed and unassigned, which is also what a released issue looks like — and it was
	// unassigned after this run began, so the one thing that keeps this factory away from a claim
	// that is not its own is that it holds nothing here. The label is the one the lost run answered;
	// only setting it again would queue the issue once more.
	gh.timeline(t, "acme/edge-sensors", claimedIssue,
		labeled("factory", run.SignalAt), unassigned("somebody", run.StartedAt.Add(time.Minute)))
	gh.issues(t, "acme/edge-sensors", touched(openIssue(claimedIssue, claimedTitle, run.StartedAt.Add(-72*time.Hour)), run.StartedAt.Add(time.Minute)))
	asked := "api " + issuesRequest("acme/edge-sensors", "factory")
	f.eventually(t, 20*time.Second, "several more polls", func() bool { return gh.made(t, asked) >= 8 })
	var line apiLine
	f.get(t, "/api/line", &line)
	if len(line.Done) != 1 || len(line.Now) != 0 || len(line.Queue) != 0 {
		t.Errorf("after %d polls the factory has %d ended and %d running runs and queues %v, want the one lost run and an empty line",
			gh.made(t, asked), len(line.Done), len(line.Now), keys(line.Queue))
	}
	for _, call := range gh.calls(t) {
		if strings.HasPrefix(call, "issue edit ") {
			t.Errorf("the factory called `gh %s` for a foreign claim; it must be left alone", call)
		}
	}
}

// A claimer loses an issue to the branch another claimer created, not to the name that branch
// happens to spell: a title edited between two polls — or a label that decides the branch type —
// would otherwise give the two claimers two branch names, and GitHub, which refuses the second
// creation of one reference and knows nothing of issues, would answer both of them 201.
func TestAClaimerThatMeetsABranchOfTheIssueUnderAnotherTitleHasLost(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	// The other claimer took the issue while it was still called something shorter: the same issue,
	// the same contract, another slug.
	held := gh.head(t, "acme/edge-sensors", "main")
	const taken = "feat/104-retry-the-upload"
	gh.branchAt(t, "acme/edge-sensors", taken, held)

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	if run.Outcome != "lost" {
		t.Fatalf("the run ended as %q (%s), want lost; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if run.Branch != taken {
		t.Errorf("the lost run records the branch %q, want %q: the branch the issue is already held by", run.Branch, taken)
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != "" {
		t.Errorf("a second branch of the issue was created on the remote (%s at %s); one claim wins an issue, not one name", claimedBranch, head)
	}
	if len(gh.workers(t)) != 0 {
		t.Errorf("a claimer that lost started a worker: %v", gh.workers(t))
	}
	for _, call := range gh.calls(t) {
		if strings.HasPrefix(call, "issue edit ") {
			t.Errorf("a claimer that lost called `gh %s`; the issue is the winner's and nothing else was touched", call)
		}
	}
	if _, err := os.Stat(filepath.Join(clone, ".claude", "worktrees")); !os.IsNotExist(err) {
		t.Errorf("a claimer that lost made a worktree in %s: %v", clone, err)
	}
}

// A won claim is one act on GitHub and then two more: the branch exists from the moment GitHub
// confirms it, and the assignment and the worktree follow. A host that loses power in that gap is
// read afterwards from the run record alone, so the record names the branch before the gap and not
// after it — nothing else on this host says what was left on the remote ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func TestTheRunNamesTheBranchAsSoonAsTheRemoteHasIt(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	// The assignment is reached and never answered, so the claim stands where a power cut would find
	// it: the branch created, nothing else done.
	gh.stall(t, "issue edit *")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})

	f.eventually(t, 30*time.Second, "the branch of the claim on the remote", func() bool {
		return gh.head(t, "acme/edge-sensors", claimedBranch) != ""
	})
	// The record in the data directory, which is all a restart of this host has to go on.
	var record Run
	f.eventually(t, 30*time.Second, "the run record to name the branch it created", func() bool {
		raw, err := os.ReadFile(filepath.Join(data, "run-1.json"))
		if err != nil {
			return false
		}
		record = Run{}
		return json.Unmarshal(raw, &record) == nil && record.Branch != ""
	})
	if record.Branch != claimedBranch || record.Base != "main" {
		t.Errorf("the run record says %q off %q, want %q off main", record.Branch, record.Base, claimedBranch)
	}
	if record.EndedAt != nil {
		t.Fatalf("the run had already ended as %q; this reads the record of a claim still in flight", record.Outcome)
	}
}

// Two factories reach for the same issue in the same moment: the shim holds both inside the one act
// that creates the branch and lets them through together, so the race is run rather than described.
// Exactly one of them may own the issue.
func TestTwoClaimersRacingForOneIssueLeaveExactlyOneWinner(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
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
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
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
	t.Parallel()
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
	t.Parallel()
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
	// Serial: inProcess sets this process's PATH, GH_SHIM_* and GIT_CONFIG_* to reach the shim.
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
				declaresBase(t, gh, clone, "acme/edge-sensors", c.originHead, c.explicit)
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
	// Serial: inProcess sets this process's PATH, GH_SHIM_* and GIT_CONFIG_* to reach the shim.
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	clone := gh.cloneInto(t, t.TempDir(), "acme/edge-sensors")
	gh.git(t, clone, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	inProcess(t, gh)

	declaresBase(t, gh, clone, "acme/edge-sensors", "main", "trunk")
	if got := baseBranch(context.Background(), Connected{Name: "acme/edge-sensors", Base: "dev"}, clone); got != "dev" {
		t.Errorf("the factory branches off %q, want dev: this host connected the repository under that base", got)
	}
	for _, unusable := range []string{"-dev", "../../etc", "", "refs/heads/dev "} {
		declaresBase(t, gh, clone, "acme/edge-sensors", "main", unusable)
		if got := baseBranch(context.Background(), Connected{Name: "acme/edge-sensors"}, clone); got != "main" {
			t.Errorf("a repository that declares %q is branched off %q, want main, the head of the remote: the declaration is no branch name", unusable, got)
		}
	}
	declares(t, gh, clone, "acme/edge-sensors", "main", "{ this is not JSON\n")
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
	t.Parallel()
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
// Which issue a branch belongs to is wf_issue_from_branch in the orchestrator's lib.sh, the rule a
// local claim asks the remote with (wf_remote_branch_for_issue) before it takes an issue somebody
// else already holds. The factory asks it of the references it fetched, so both drivers have to read
// the same branch names the same way ([ADR 0022]).
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
func TestTheIssueABranchBelongsToAgreesWithTheOrchestratorsShell(t *testing.T) {
	t.Parallel()
	for _, branch := range []string{
		"feat/104-retry-the-upload-when-the-broker-drops",
		"feat/104-retry-the-upload", // the same issue under the title it had two polls ago
		"fix/9-crash-on-start",
		"docs/12-explain-the-gate",
		"chore/7-bump-the-pins",
		"feat/104-", // a title that slugs to nothing
		"feat/0104-retry",
		"plan/104-a-plan-branch-carries-a-topic",
		"plan/retry-the-upload",
		"main",
		"release/1.2",
		"Feat/104-upper-case-is-no-branch-type",
		"feat/retry-104-the-upload",
		"feature-104-the-hyphen-is-no-slash",
		"origin/HEAD",
		"HEAD",
	} {
		t.Run(branch, func(t *testing.T) {
			want := strings.TrimSpace(shell(t, "", `. "$1"; wf_issue_from_branch "$2"`, nil, orchestratorLib(t), branch))
			if got := issueFromBranch(branch); got != want {
				t.Errorf("the factory reads %q as the branch of issue %q; the orchestrator's shell reads it as %q", branch, got, want)
			}
		})
	}
}

func shellBranchName(t *testing.T, issue Issue) string {
	t.Helper()
	return strings.TrimSpace(shell(t, "",
		`. "$1"; printf '%s/%s-%s\n' "$(wf_branch_type "$3")" "$2" "$(wf_slug "$4")"`,
		nil, orchestratorLib(t), strconv.Itoa(issue.Number), strings.Join(issue.Labels, ","), issue.Title))
}

// declaresBase puts the base a repository declares for itself on the branch of the shim's GitHub that
// carries it: WF_BASE_BRANCH in the env block of the .claude/settings.json the repository holds,
// which is where a local session is given the variable wf_base_branch reads.
func declaresBase(t *testing.T, gh *ghShim, clone, repository, branch, base string) {
	t.Helper()
	declares(t, gh, clone, repository, branch, fmt.Sprintf(`{"env":{"WF_BASE_BRANCH":%q}}`+"\n", base))
}

// declares commits a settings file on a branch of the shim's GitHub, whatever it holds, and fetches
// it into the clone as a claim does. The file has to live in the repository and not in the clone's
// working tree: that tree is written once, the day this host cloned, and a claim never checks it out.
func declares(t *testing.T, gh *ghShim, clone, repository, branch, body string) {
	t.Helper()
	work := t.TempDir()
	checkout := filepath.Join(work, "checkout")
	gh.git(t, work, "clone", "-q", gh.remotePath(repository), checkout)
	writeSettings(t, checkout, body)
	gh.git(t, checkout, "add", "--force", ".claude/settings.json")
	gh.git(t, checkout, "commit", "-q", "-m", "declare what this repository branches off")
	gh.git(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
	gh.git(t, clone, "fetch", "-q", "--prune", "origin")
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
	longest           int // the size of the longest argument in bytes
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
	return sessionsIn(t, g.worker)
}

// reviewerSessions is every reviewer session of the panel the claude shim was started as, in order.
func (g *ghShim) reviewerSessions(t *testing.T) []workerStart {
	t.Helper()
	return sessionsIn(t, g.reviewer)
}

// verdict has the claude shim answer the reviewer's run with this result: the n-th time it runs, or
// every time for 0.
func (g *ghShim) verdict(t *testing.T, reviewer string, n int, result string) {
	t.Helper()
	name := reviewer
	if n > 0 {
		name += "." + strconv.Itoa(n)
	}
	if err := os.WriteFile(filepath.Join(g.verdicts, name), []byte(result), 0o600); err != nil {
		t.Fatal(err)
	}
}

// authorSessions is every author session of the pr stage the claude shim was started as, in order.
func (g *ghShim) authorSessions(t *testing.T) []workerStart {
	t.Helper()
	return sessionsIn(t, g.authors)
}

// sessionsIn reads one log of the claude shim, a record per session.
func sessionsIn(t *testing.T, log string) []workerStart {
	t.Helper()
	raw, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	started := []workerStart{}
	last := ""
	for _, line := range strings.Split(string(raw), "\n") {
		field, value, _ := strings.Cut(line, " ")
		if field == "call" {
			started = append(started, workerStart{})
			last = field
			continue
		}
		if len(started) == 0 {
			continue
		}
		w := &started[len(started)-1]
		switch field {
		case "cwd", "branch", "head", "pid", "pgid", "longest", "arg", "env":
			last = field
		default:
			// A line of no field is the next line of an argument that has more than one, a brief's.
			if last == "arg" {
				w.args[len(w.args)-1] += "\n" + line
			}
			continue
		}
		switch field {
		case "cwd":
			w.cwd = value
		case "branch":
			w.branch = value
		case "head":
			w.head = value
		case "pid", "pgid", "longest":
			number, err := strconv.Atoi(value)
			if err != nil {
				t.Fatalf("the worker wrote %q as its %s", value, field)
			}
			switch field {
			case "pid":
				w.pid = number
			case "pgid":
				w.pgid = number
			default:
				w.longest = number
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

// pushedBy is who GitHub says acted last on a branch of the shim's GitHub — its creation or a push —
// which is how the factory tells a branch of its own that no record names from somebody else's.
func (g *ghShim) pushedBy(t *testing.T, repository, branch, login string) {
	t.Helper()
	g.answer(t, activityCall(repository, branch), login+"\n")
}

// openPulls is how many pull requests of a branch are open on the shim's GitHub.
func (g *ghShim) openPulls(t *testing.T, repository, branch string, open int) {
	t.Helper()
	owner, _, _ := strings.Cut(repository, "/")
	g.answer(t, "api repos/"+repository+"/pulls?state=open&head="+url.QueryEscape(owner+":"+branch)+"&per_page=1 --jq length",
		strconv.Itoa(open)+"\n")
}

func activityCall(repository, branch string) string {
	return "api repos/" + repository + "/activity?ref=" + url.QueryEscape("refs/heads/"+branch) + "&per_page=1 --jq .[0].actor.login // empty"
}

// A factory whose data directory was lost knows nothing of the issues it held. One of them was
// released and is routed and unassigned again, and its branch is on the remote with the work of the
// runs before: the factory pushed it last, and no pull request of it is open. That is this factory's
// own claim and not a foreign one, so it is taken up again — assigned, its worktree made from the
// branch, and the worker run on the commits there — rather than recorded as lost ([ADR 0024]).
//
// [ADR 0024]: ../docs/adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md
func TestAFactoryThatLostItsDataDirectoryTakesUpItsOwnOrphanedBranch(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	// The host before this one claimed the issue and pushed work on its branch.
	gh.branchAt(t, "acme/edge-sensors", claimedBranch, gh.head(t, "acme/edge-sensors", "main"))
	work := gh.commitOn(t, "acme/edge-sensors", claimedBranch)
	gh.pushedBy(t, "acme/edge-sensors", claimedBranch, "factory-bot")
	gh.openPulls(t, "acme/edge-sensors", claimedBranch, 0)

	// A fresh data directory: no clone, no record.
	data := filepath.Join(t.TempDir(), "data")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	if run.Outcome == outcomeLost || run.Outcome == outcomeFailed {
		t.Fatalf("the run ended as %q (%s), want the worker's own outcome; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if run.Branch != claimedBranch || !run.Holding {
		t.Errorf("the run records %q holding=%v, want %s held", run.Branch, run.Holding, claimedBranch)
	}
	if created := gh.asked(t, "api --method POST repos/acme/edge-sensors/git/refs"); created != 0 {
		t.Errorf("the factory tried to create a branch %d times, want none: the branch of its own claim is there", created)
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != work {
		t.Errorf("%s of the remote is at %q, want the work that was there (%s)", claimedBranch, head, work)
	}
	assigned := fmt.Sprintf("issue edit %d --repo acme/edge-sensors --add-assignee factory-bot", claimedIssue)
	if made := gh.made(t, assigned); made != 1 {
		t.Errorf("the factory made `gh %s` %d times, want once", assigned, made)
	}
	workers := gh.workers(t)
	if len(workers) != 1 {
		t.Fatalf("the factory started %d workers, want one", len(workers))
	}
	worktree := filepath.Join(clonePath(data, "acme/edge-sensors"), ".claude", "worktrees", claimedWorktree)
	if workers[0].cwd != resolved(t, worktree) || workers[0].branch != claimedBranch || workers[0].head != work {
		t.Errorf("the worker ran in %s on %q at %s, want %s on %s at %s: the run resumes on the commits the branch carries",
			workers[0].cwd, workers[0].branch, workers[0].head, resolved(t, worktree), claimedBranch, work)
	}
}

// A branch of the issue that carries work is still somebody else's when GitHub says another login
// pushed it last, or when a pull request of it is open and its work is with a person: the claim ends
// lost and touches nothing, as ADR 0024 decides for every foreign claim.
//
// [ADR 0024]: ../docs/adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md
func TestABranchWithWorkThatIsNotTheFactorysOwnStaysAForeignClaim(t *testing.T) {
	t.Parallel()
	for _, one := range []struct {
		name   string
		pusher string
		open   int
	}{
		{name: "another login pushed it last", pusher: "somebody-else"},
		{name: "a pull request of it is open", pusher: "factory-bot", open: 1},
	} {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			gh := newGhShim(t)
			gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
			gh.loggedInAs(t, "factory-bot")
			gh.branchAt(t, "acme/edge-sensors", claimedBranch, gh.head(t, "acme/edge-sensors", "main"))
			work := gh.commitOn(t, "acme/edge-sensors", claimedBranch)
			gh.pushedBy(t, "acme/edge-sensors", claimedBranch, one.pusher)
			gh.openPulls(t, "acme/edge-sensors", claimedBranch, one.open)

			data := filepath.Join(t.TempDir(), "data")
			clone := gh.cloneInto(t, data, "acme/edge-sensors")
			f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
				"repositories": []string{"acme/edge-sensors"}})
			run := f.ended(t, 1)

			if run.Outcome != outcomeLost || run.Holding {
				t.Fatalf("the run ended as %q (%s) holding=%v, want lost and holding nothing; the factory's log:\n%s",
					run.Outcome, run.Reason, run.Holding, f.output(t))
			}
			if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != work {
				t.Errorf("%s of the remote is at %q, want where the other claimer left it (%s)", claimedBranch, head, work)
			}
			for _, call := range gh.calls(t) {
				if strings.HasPrefix(call, "issue edit ") || strings.HasPrefix(call, "api --method ") {
					t.Errorf("the factory called `gh %s` for a foreign claim; it must be left alone", call)
				}
			}
			if len(gh.workers(t)) != 0 {
				t.Errorf("a claimer that lost started a worker: %v", gh.workers(t))
			}
			if _, err := os.Stat(filepath.Join(clone, ".claude", "worktrees")); !os.IsNotExist(err) {
				t.Errorf("a claimer that lost made a worktree in %s: %v", clone, err)
			}
		})
	}
}

// A lost run answers the routing it was started for and no later one. Once the foreign branch is
// gone and the label is set again, the issue is queued like any routed issue and claimed on the
// branch name that is free now.
func TestAnIssueWhoseClaimWasLostIsClaimedWhenItIsRoutedAgain(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	// Another claimer holds the issue: its branch is cut from the base and carries nothing yet.
	gh.branchAt(t, "acme/edge-sensors", claimedBranch, gh.head(t, "acme/edge-sensors", "main"))

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	if run := f.ended(t, 1); run.Outcome != outcomeLost {
		t.Fatalf("run 1 ended as %q (%s), want lost; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	// The label that was on the issue all along was answered by that run.
	f.never(t, 2*time.Second, "the factory ran the issue again under the routing a lost run answered",
		func() bool { return !f.missing(t, 2) })

	// The other claimer's branch is deleted and the maintainer sets the label again.
	gh.dropBranch(t, "acme/edge-sensors", claimedBranch)
	again := time.Now().UTC()
	gh.issues(t, "acme/edge-sensors", touched(openIssue(claimedIssue, claimedTitle, again.Add(-72*time.Hour)), again))
	gh.timeline(t, "acme/edge-sensors", claimedIssue,
		labeled("factory", again.Add(-6*time.Hour)), unlabeled("factory", again.Add(-time.Minute)), labeled("factory", again))

	run := f.ended(t, 2)
	if run.Outcome == outcomeLost || run.Outcome == outcomeFailed || !run.Holding || run.Branch != claimedBranch {
		t.Fatalf("run 2 ended as %q (%s) on %q holding=%v, want a claim of %s that holds it; the factory's log:\n%s",
			run.Outcome, run.Reason, run.Branch, run.Holding, claimedBranch, f.output(t))
	}
	// The lost claim met the branch in its fetch and created nothing; the second one created it.
	if created := gh.asked(t, "api --method POST repos/acme/edge-sensors/git/refs"); created != 1 {
		t.Errorf("the factory tried to create the branch %d times, want once, by the claim it won", created)
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head == "" {
		t.Errorf("%s is not on the remote after the claim", claimedBranch)
	}
	if len(gh.workers(t)) != 1 {
		t.Errorf("the factory started %d workers, want one for the claim it won", len(gh.workers(t)))
	}
}
