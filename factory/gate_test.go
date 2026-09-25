package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The gate stage, which the factory runs itself ([ADR 0043], step 5): the implement session stops after the
// implement stage, and the factory merges the base, runs the gate of the change class and hands a
// failure to a fix session within the gate's budget.
//
// [ADR 0043]: ../docs/adr/0043-the-migration-runs-from-the-last-stage-to-the-first.md

// linesGate is a make recipe that prints how many lines worked.md has, one for every session that
// worked the branch, and exits with the test given as its status.
func linesGate(test string) string {
	return `@lines=$$(wc -l < worked.md | tr -d ' '); echo "worked.md has $$lines lines"; test $$lines ` + test
}

// A gate that fails on the implement session's commit goes to a fix session of the gate stage with the end
// of its output, and the gate runs again on the commit that session leaves; the pass is what the
// reviewers and the pull request are given.
func TestAFailingGateGoesToAFixSessionAndRunsAgainOnItsCommit(t *testing.T) {
	t.Parallel()
	gh, data := panelClaim(t, linesGate("-ge 2"))
	f := gh.work(t, ciConfig(data, nil))
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if !equal(run.Stages, []string{"implement", "gate", "review", "pr", "ci"}) {
		t.Errorf("the run went through the stages %v, want implement, gate, review, pr and ci", run.Stages)
	}
	workers := gh.workers(t)
	if len(workers) != 2 {
		t.Fatalf("the factory started %d worker sessions, want the implement session and one fix session of the gate", len(workers))
	}
	brief := strings.Join(workers[1].args, "\n")
	for _, want := range []string{"after the implementation, and it failed", "gate_result: fail (exit 2) at " + short(workers[1].head), "worked.md has 1 lines"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the fix session's brief does not carry %q:\n%s", want, brief)
		}
	}
	head := gh.head(t, "acme/edge-sensors", claimedBranch)
	if len(run.Gates) != 2 || run.Gates[0].Passed || run.Gates[0].Exit != 2 || run.Gates[0].Head != workers[1].head ||
		!strings.Contains(run.Gates[0].Tail, "worked.md has 1 lines") || !run.Gates[1].Passed || run.Gates[1].Head != head ||
		run.Gates[1].Command != "make check" || run.Gates[1].Class != classFull {
		t.Errorf("the run recorded the gates %+v, want a failure with exit 2 on the implement session's commit and a pass of make check on %s", run.Gates, short(head))
	}
	pass := "gate_result: pass (exit 0) at " + short(head)
	if reviewers := strings.Join(factoryBodies(run, "briefed the reviewers"), "\n"); !strings.Contains(reviewers, pass) {
		t.Errorf("the reviewers were not briefed with the gate that passed on %s:\n%s", short(head), reviewers)
	}
	if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 1 || !strings.Contains(pulls[0].Body, pass) {
		t.Errorf("the factory opened %+v, want one pull request with the gate that passed", pulls)
	}
}

// The gate's budget is a host knob a repository overrides: once its fix sessions are spent and the
// gate still fails, the run is blocked naming the failure, and no pull request is opened. A budget of
// none blocks on the first failure.
func TestAGateThatFailsPastItsBudgetBlocksTheRunNamingTheFailure(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		host, repository any
		fixes            int
	}{
		"the repository's budget over the host's": {map[string]any{"rounds": 3}, map[string]any{"rounds": 1}, 1},
		"a budget of none":                        {map[string]any{"rounds": 0}, nil, 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := panelClaim(t, `@echo "the calibration test fails"; exit 3`)
			cfg := ciConfig(data, nil)
			cfg["gate"] = c.host
			if c.repository != nil {
				cfg["repositories"] = []map[string]any{{"name": "acme/edge-sensors", "gate": c.repository}}
			}
			f := gh.work(t, cfg)
			run := f.ended(t, 1)
			if run.Outcome != outcomeBlocked {
				t.Fatalf("the run ended as %q (%s), want blocked; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
			}
			spent := strconv.Itoa(c.fixes) + " of " + strconv.Itoa(c.fixes)
			for _, want := range []string{"the gate fails after " + spent + " fix sessions (gate.rounds)", "gate_result: fail (exit 2)", "the calibration test fails"} {
				if !strings.Contains(run.Reason, want) {
					t.Errorf("the run was blocked because %q, want a reason with %q", run.Reason, want)
				}
			}
			if workers := gh.workers(t); len(workers) != 1+c.fixes {
				t.Errorf("the factory started %d worker sessions, want the implement session and %d fix sessions", len(workers), c.fixes)
			}
			if len(run.Gates) != 1+c.fixes {
				t.Errorf("the run recorded %d gates, want %d", len(run.Gates), 1+c.fixes)
			}
			if reviewers := gh.reviewerSessions(t); len(reviewers) != 0 {
				t.Errorf("the factory started %d reviewers on a gate that fails", len(reviewers))
			}
			if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 0 {
				t.Errorf("the factory opened %d pull requests on a gate that fails", len(pulls))
			}
		})
	}
}

// A gate that runs past its timeout is ended with its whole process group, and that is a failed round
// of the gate like any other.
func TestAGatePastItsTimeoutIsEndedWithItsProcessGroupAndFails(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "sleep.pid")
	gh, data := panelClaim(t, "@echo the gate hangs; sleep 60 & echo $$! > "+pidFile+"; wait")
	cfg := ciConfig(data, nil)
	cfg["repositories"] = []map[string]any{{"name": "acme/edge-sensors", "gate": map[string]any{"rounds": 0, "timeout": "2s"}}}
	f := gh.work(t, cfg)
	run := f.ended(t, 1)
	if run.Outcome != outcomeBlocked {
		t.Fatalf("the run ended as %q (%s), want blocked; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	for _, want := range []string{"the gate fails after 0 of 0 fix sessions (gate.rounds)", "gate_result: fail (ran past its timeout of 2s)", "the gate hangs"} {
		if !strings.Contains(run.Reason, want) {
			t.Errorf("the run was blocked because %q, want a reason with %q", run.Reason, want)
		}
	}
	if len(run.Gates) != 1 || !run.Gates[0].TimedOut || run.Gates[0].Passed || run.Gates[0].Seconds > 30 {
		t.Errorf("the run recorded the gates %+v, want one that ran past its timeout and was ended in time", run.Gates)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the gate wrote no process id: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("the gate wrote %q, want a process id", raw)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	f.eventually(t, 10*time.Second, "the process the gate started to end with its group", func() bool {
		return syscall.Kill(pid, 0) != nil
	})
}

// When the base has moved on since the branch was cut, the gate stage merges it into the branch with a
// merge commit before the gate runs. A merge that conflicts goes to a fix session with the conflicted
// files, and the gate runs on the commit that session leaves; a session that gives the merge up
// instead of committing it fails the run, and no gate runs on a branch without the base.
func TestTheGateStageMergesTheBaseAndAConflictGoesToAFixSession(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		file     string // the file main takes a change to while the implement session runs
		sessions int
		abort    bool // the fix session gives the merge up and commits something else
	}{
		"a clean merge":       {"other.md", 1, false},
		"a conflicting merge": {"worked.md", 2, false},
		"a merge given up":    {"worked.md", 2, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := panelClaim(t, "@echo the gate ran")
			// The implement session stays a while after it committed, and main moves on meanwhile.
			gh.env = append(gh.env, "CLAUDE_SHIM_SLEEP=5", "CLAUDE_SHIM_THEN_SLEEP=0")
			if c.abort {
				gh.env = append(gh.env, "CLAUDE_SHIM_THEN_ABORT_MERGE=1", "CLAUDE_SHIM_THEN_COMMIT=elsewhere.md")
			}
			f := gh.work(t, ciConfig(data, nil))
			f.saw(t, "worker started")
			other := filepath.Join(t.TempDir(), "other")
			gh.git(t, filepath.Dir(other), "clone", "-q", gh.remotePath("acme/edge-sensors"), other)
			writeFile(t, filepath.Join(other, c.file), "somebody else's work\n")
			gh.git(t, other, "add", c.file)
			gh.git(t, other, "commit", "-q", "-m", "docs: "+c.file)
			gh.git(t, other, "push", "-q", "origin", "HEAD:refs/heads/main")
			moved := gh.head(t, "acme/edge-sensors", "main")

			run := f.ended(t, 1)
			if c.abort {
				if run.Outcome != outcomeFailed || !strings.Contains(run.Reason, "left the branch without origin/main") || len(run.Gates) != 0 {
					t.Errorf("the run ended as %q (%s) with the gates %+v, want it failed without a gate: the merge was never committed", run.Outcome, run.Reason, run.Gates)
				}
				return
			}
			if run.Outcome != outcomeReady {
				t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
			}
			if workers := gh.workers(t); len(workers) != c.sessions {
				t.Errorf("the factory started %d worker sessions, want %d", len(workers), c.sessions)
			}
			head := gh.head(t, "acme/edge-sensors", claimedBranch)
			parents := strings.Fields(gh.git(t, gh.remotePath("acme/edge-sensors"), "rev-list", "--parents", "-n", "1", head))
			if len(parents) != 3 || !slices.Contains(parents[1:], moved) {
				t.Errorf("%s of the remote is at %v, want a merge commit of main's head %s: the base is merged, never rebased", claimedBranch, parents, short(moved))
			}
			if len(run.Gates) != 1 || run.Gates[0].Head != head || !run.Gates[0].Passed {
				t.Errorf("the run recorded the gates %+v, want one pass on the merge %s", run.Gates, short(head))
			}
			conflicts := factoryTitles(run, "the merge of main conflicts")
			if c.sessions == 1 {
				if merged := factoryTitles(run, "merged main into the branch"); len(merged) != 1 || len(conflicts) != 0 {
					t.Errorf("the gate stage said %v and %v, want a clean merge of main", merged, conflicts)
				}
				return
			}
			brief := strings.Join(factoryBodies(run, "briefed a fix session of the merge"), "\n")
			if len(conflicts) != 1 || !strings.Contains(brief, "worked.md") || !strings.Contains(brief, "origin/main") {
				t.Errorf("the gate stage said %v and briefed %q, want a conflicting merge of main handed to a fix session with worked.md", conflicts, brief)
			}
		})
	}
}

// A resumed run whose run before got past the implement session, and whose branch carries commits beyond the
// base and no pass of the gate for them, starts at the gate stage on them and starts no implement session;
// one whose branch carries none starts with the implement session. So does one whose run before ended in the
// implement session after a commit of its own: that session never reported its implementation complete, and
// gating what it left would review and open a pull request of half an implementation.
func TestAResumeWithCommitsBeyondTheBaseStartsAtTheGateStage(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		committed, gating bool
		stages            []string
	}{
		"with commits":    {committed: true, gating: true, stages: []string{"implement", "gate"}},
		"without commits": {committed: false, gating: false, stages: []string{"implement", "gate"}},
		"with commits of an implement session that ended": {committed: true, gating: false, stages: []string{"implement"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh := newGhShim(t)
			gh.remote(t, "acme/edge-sensors")
			gh.loggedInAs(t, "factory-bot")
			gh.workerCommits(t, "worked.md")
			data := filepath.Join(t.TempDir(), "data")
			clone := gh.cloneInto(t, data, "acme/edge-sensors")
			gh.branchAt(t, "acme/edge-sensors", claimedBranch, gh.head(t, "acme/edge-sensors", "main"))
			head := gh.head(t, "acme/edge-sensors", "main")
			if c.committed {
				head = gh.commitOn(t, "acme/edge-sensors", claimedBranch)
			}
			began := time.Now().UTC().Add(-2 * time.Hour)
			interrupted := record(1, claimedIssue, claimedTitle, signalRouted, outcomeInterrupted, true, began, began.Add(30*time.Minute))
			interrupted.Worktree = filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
			interrupted.Stages = c.stages
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
			workers, gating := gh.workers(t), factoryTitles(resumed, "resuming at the gate stage")
			if c.gating {
				if len(workers) != 0 || len(gating) != 1 || !equal(resumed.Stages, []string{"gate", "review", "pr", "ci"}) ||
					len(resumed.Gates) != 1 || resumed.Gates[0].Head != head {
					t.Errorf("the resume started %d implement sessions, said %v, went through %v and ran the gates %+v, want it to start at the gate stage on %s",
						len(workers), gating, resumed.Stages, resumed.Gates, short(head))
				}
				return
			}
			if len(workers) != 1 || len(gating) != 0 || !equal(resumed.Stages, []string{"implement", "gate", "review", "pr", "ci"}) {
				t.Errorf("the resume started %d implement sessions, said %v and went through %v, want it to start with the implement session", len(workers), gating, resumed.Stages)
			}
			if len(resumed.Gates) == 0 || resumed.Gates[0].Head == head {
				t.Errorf("the resume ran the gates %+v, want the first on the commit of its own implement session and not on %s", resumed.Gates, short(head))
			}
		})
	}
}

// A run that ends while the fix session of its merge has resolved every conflict but not committed the
// merge leaves the merge in progress in the worktree. The resumed run keeps those resolutions: it
// commits the merge as it stands and gates it, and neither starts the merge again nor gives it up.
func TestAResumedGateCommitsAMergeWhoseConflictsAreResolved(t *testing.T) {
	t.Parallel()
	gh, data := panelClaim(t, "@true")
	gh.branchAt(t, "acme/edge-sensors", claimedBranch, gh.head(t, "acme/edge-sensors", "main"))
	edit := filepath.Join(t.TempDir(), "edit")
	gh.git(t, filepath.Dir(edit), "clone", "-q", gh.remotePath("acme/edge-sensors"), edit)
	commit := func(branch, text string) string {
		gh.git(t, edit, "checkout", "-q", branch)
		writeFile(t, filepath.Join(edit, "shared.md"), text)
		gh.git(t, edit, "add", "shared.md")
		gh.git(t, edit, "commit", "-q", "-m", "shared.md on "+branch)
		gh.git(t, edit, "push", "-q", "origin", "HEAD:"+branch)
		return gh.git(t, edit, "rev-parse", "HEAD")
	}
	work := commit(claimedBranch, "the branch's line\n")
	base := commit("main", "the base's line\n")

	// The worktree as the run before left it: the merge of the base conflicted, and its fix session
	// resolved the file and staged it before the run ended.
	clone := clonePath(data, "acme/edge-sensors")
	gh.git(t, clone, "fetch", "-q", "origin")
	worktree := worktreePath(clone, claimedBranch)
	gh.git(t, clone, "worktree", "add", "-q", "-b", claimedBranch, worktree, "origin/"+claimedBranch)
	merge := exec.Command("git", "-c", "user.email=t@example.com", "-c", "user.name=t", "merge", "-q", "--no-edit", "origin/main")
	merge.Dir, merge.Env = worktree, gitIsolation()
	if err := merge.Run(); err == nil {
		t.Fatal("the merge of main into the branch was clean, want a conflict in shared.md")
	}
	if conflicted := gh.git(t, worktree, "diff", "--name-only", "--diff-filter=U"); conflicted != "shared.md" {
		t.Fatalf("the merge of main into the branch conflicts in %q, want shared.md", conflicted)
	}
	resolution := "the branch's line\nthe base's line\n"
	writeFile(t, filepath.Join(worktree, "shared.md"), resolution)
	gh.git(t, worktree, "add", "shared.md")

	began := time.Now().UTC().Add(-2 * time.Hour)
	interrupted := record(1, claimedIssue, claimedTitle, signalRouted, outcomeInterrupted, true, began, began.Add(30*time.Minute))
	interrupted.Worktree = worktree
	interrupted.Stages = []string{"implement", "gate"}
	records(t, data, interrupted)
	gh.issues(t, "acme/edge-sensors")
	gh.issue(t, "acme/edge-sensors", assignedTo(openIssue(claimedIssue, claimedTitle, began.Add(-72*time.Hour)), "factory-bot"))
	gh.openPullsListed(t, "acme/edge-sensors", claimedBranch)

	f := gh.work(t, ciConfig(data, nil))
	resumed := f.ended(t, 2)
	if resumed.Outcome != outcomeReady {
		t.Fatalf("run 2 ended as %q (%s), want ready; the factory's log:\n%s", resumed.Outcome, resumed.Reason, f.output(t))
	}
	if len(resumed.Gates) == 0 {
		t.Fatalf("the resume ran no gate; the factory's log:\n%s", f.output(t))
	}
	gated := resumed.Gates[0].Head
	if parents := gh.git(t, worktree, "rev-list", "--parents", "-n", "1", gated); parents != gated+" "+work+" "+base {
		t.Errorf("the resume gated %s with the parents %q, want the merge of %s into %s", short(gated), parents, short(base), short(work))
	}
	if kept := gh.git(t, worktree, "show", gated+":shared.md"); kept+"\n" != resolution {
		t.Errorf("the merge the resume committed has shared.md as %q, want the resolution %q", kept, resolution)
	}
	if workers := gh.workers(t); len(workers) != 0 {
		t.Errorf("the resume started %d implement sessions, want none: the merge was resolved already", len(workers))
	}
}

// A run resumed at the gate stage starts no session before its gate, so the gate is the first process
// that holds the run's lock. A factory killed while that gate runs leaves it running, and the next start
// has to find it through the lock and end it before the issue is worked again in the same worktree.
func TestARestartEndsTheGateOfAResumeThatOutlivedTheFactory(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "gate.pid")
	gh, data := panelClaim(t, "@echo $$$$ > "+pidFile+"; exec sleep 300")
	gh.branchAt(t, "acme/edge-sensors", claimedBranch, gh.head(t, "acme/edge-sensors", "main"))
	gh.commitOn(t, "acme/edge-sensors", claimedBranch)
	clone := clonePath(data, "acme/edge-sensors")
	began := time.Now().UTC().Add(-2 * time.Hour)
	interrupted := record(1, claimedIssue, claimedTitle, signalRouted, outcomeInterrupted, true, began, began.Add(30*time.Minute))
	interrupted.Worktree = worktreePath(clone, claimedBranch)
	interrupted.Stages = []string{"implement", "gate"}
	records(t, data, interrupted)
	gh.issues(t, "acme/edge-sensors")
	gh.issue(t, "acme/edge-sensors", assignedTo(openIssue(claimedIssue, claimedTitle, began.Add(-72*time.Hour)), "factory-bot"))
	gh.openPullsListed(t, "acme/edge-sensors", claimedBranch)

	first := gh.work(t, ciConfig(data, nil))
	var pid int
	first.eventually(t, 30*time.Second, "the gate of the resumed run to start", func() bool {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(raw)))
		return err == nil
	})
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	first.stop(t, syscall.SIGKILL) // a kill of the factory alone, not of the host
	if !survived(pid) {
		t.Fatalf("the gate %d ended with the factory that was killed; this test needs one that outlives it", pid)
	}

	again := gh.work(t, ciConfig(data, nil))
	again.eventually(t, 30*time.Second, "the gate the killed factory left behind to be ended", func() bool {
		return syscall.Kill(pid, 0) != nil
	})
	var run apiRun
	again.get(t, "/api/runs/2", &run)
	if n := len(run.Events); n == 0 || !strings.Contains(run.Events[n-1].Title, "worker ended after the factory") {
		t.Errorf("the log of the resumed run ends on %v, want the start saying it ended the gate that outlived the factory", run.Events)
	}
}
