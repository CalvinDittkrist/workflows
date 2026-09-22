package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Cancelling a run and letting an issue go, tested the way the claim is: the real binary, the gh
// shim, a scripted worker and a local bare repository that stands in for the remote. What the
// factory did is read from that repository, from the clone on disk and from the run records, which
// are the three places a person would look ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md

// The worktree of the claimed issue, under the name the branch gives it in the clone.
const claimedWorktree = "feat-104-retry-the-upload-when-the-broker-drops"

// Taking the routing label off an issue whose run is going is the one gesture that ends work in
// progress: the worker's process group goes, the run is recorded cancelled, and the issue is given
// back — pushed first, and with the branch on the remote kept exactly as long as it carries a commit.
func TestARunIsCancelledWhenTheRoutingLabelIsTakenOffItsIssue(t *testing.T) {
	for _, one := range []struct {
		name        string
		commit      string
		branchStays bool
	}{
		{name: "what the worker committed is pushed and the branch stays", commit: "worked.md", branchStays: true},
		{name: "a run that committed nothing leaves no branch behind", branchStays: false},
	} {
		t.Run(one.name, func(t *testing.T) {
			gh := newGhShim(t)
			gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
			gh.loggedInAs(t, "factory-bot")
			gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
			gh.unassigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
			gh.workerWaits(t, 10*time.Minute)
			if one.commit != "" {
				gh.workerCommits(t, one.commit)
			}

			data := filepath.Join(t.TempDir(), "data")
			clone := gh.cloneInto(t, data, "acme/edge-sensors")
			base := gh.head(t, "acme/edge-sensors", "main")

			f := gh.work(t, config{"poll": "50ms", "deadline": "10m", "data_dir": data,
				"repositories": []string{"acme/edge-sensors"}})
			worker := workerOf(t, f, gh, claimedBranch)
			worktree := filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
			work := base
			if one.commit != "" {
				work = committed(t, f, worktree, one.commit)
			}

			// The gesture: the routing label comes off the issue the factory is working.
			gh.issue(t, "acme/edge-sensors", assignedTo(
				openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour), readyLabel), "factory-bot"))

			run := f.ended(t, 1)
			if run.Outcome != "cancelled" {
				t.Fatalf("the run ended as %q (%s), want cancelled; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
			}
			if !strings.Contains(run.Reason, "routing label") {
				t.Errorf("the cancelled run gives the reason %q, want the gesture that ended it in it", run.Reason)
			}
			// The whole process group of the worker is ended, not only the session that led it.
			if groupSurvived(worker.pgid) {
				t.Errorf("the process group %d of the worker is still there after the cancel", worker.pgid)
			}

			f.eventually(t, 30*time.Second, "the issue to be let go", func() bool {
				var let apiRun
				f.get(t, "/api/runs/1", &let)
				return let.LetGoAt != nil
			})

			// Nothing of the work is lost: what the worker committed is on the remote branch.
			if head := gh.head(t, "acme/edge-sensors", claimedBranch); one.branchStays && head != work {
				t.Errorf("%s of the remote is at %q, want the commit the cancelled worker made (%s): a worktree is pushed before it goes", claimedBranch, head, work)
			} else if !one.branchStays && head != "" {
				t.Errorf("%s is still on the remote at %s, want it gone: the claim produced no commit beyond main", claimedBranch, head)
			}
			removal := "api --method DELETE repos/acme/edge-sensors/git/refs/heads/" + claimedBranch
			removals := 1
			if one.branchStays {
				removals = 0
			}
			if made := gh.made(t, removal); made != removals {
				t.Errorf("the factory made `gh %s` %d times, want %d: a branch is removed from the remote only when it holds nothing beyond its base", removal, made, removals)
			}

			// The host is left clean: no worktree, no local branch.
			if _, err := os.Stat(worktree); err == nil {
				t.Errorf("the worktree %s is still on the host after the issue was let go", worktree)
			}
			if localBranch(t, clone, claimedBranch) {
				t.Errorf("the local branch %s is still in %s after the issue was let go", claimedBranch, clone)
			}
			// And the issue is free again: this host is off it, because no pull request came of the run.
			removed := fmt.Sprintf("issue edit %d --repo acme/edge-sensors --remove-assignee factory-bot", claimedIssue)
			if made := gh.made(t, removed); made != 1 {
				t.Errorf("the factory made `gh %s` %d times, want once: an issue let go without a pull request loses its assignee", removed, made)
			}
			// The record and the log of the run stay, whole: they are what the operator reads.
			if _, err := os.Stat(filepath.Join(data, "run-1.json")); err != nil {
				t.Errorf("the record of the cancelled run is gone: %v", err)
			}
			if lines := countLines(t, filepath.Join(data, "run-1.events.jsonl")); lines == 0 {
				t.Errorf("the log of the cancelled run is empty; the factory deletes no log")
			}
			// The issue is not worked again by itself: it was routed before it was let go, and only a
			// routing after that is a new decision.
			f.never(t, 2*time.Second, "the factory started a second run of an issue it was told to let go",
				func() bool { return !f.missing(t, 2) })
		})
	}
}

// An issue the factory holds whose runs are over is let go on the same three decisions, and this is
// where they are read: the pull request of its run merged or closed, the issue closed, the routing
// label taken off it. An issue that reached a pull request keeps its assignee — the work is with a
// person from then on — and a branch that carries commits stays on the remote whatever the decision
// was.
func TestAnIdleHeldIssueIsLetGoOnTheDecisionGitHubCarries(t *testing.T) {
	for _, one := range []struct {
		name            string
		blocked         bool // the run reports blocked, so the issue never reached a pull request
		decide          func(t *testing.T, gh *ghShim, held issueJSON)
		assigneeRemoved bool
	}{
		{
			name: "the pull request was merged",
			decide: func(t *testing.T, gh *ghShim, held issueJSON) {
				gh.pull(t, "acme/edge-sensors", claimedIssue, "closed", true)
				gh.issue(t, "acme/edge-sensors", held)
			},
		},
		{
			name: "the pull request was closed",
			decide: func(t *testing.T, gh *ghShim, held issueJSON) {
				gh.pull(t, "acme/edge-sensors", claimedIssue, "closed", false)
				gh.issue(t, "acme/edge-sensors", held)
			},
		},
		{
			name: "the issue was closed",
			decide: func(t *testing.T, gh *ghShim, held issueJSON) {
				gh.issue(t, "acme/edge-sensors", closedIssue(held))
			},
		},
		{
			name:    "the routing label was taken off an issue without a pull request",
			blocked: true,
			decide: func(t *testing.T, gh *ghShim, held issueJSON) {
				gh.issue(t, "acme/edge-sensors", assignedTo(
					openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour), readyLabel), "factory-bot"))
			},
			assigneeRemoved: true,
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			gh := newGhShim(t)
			gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
			gh.loggedInAs(t, "factory-bot")
			gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
			gh.unassigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
			gh.workerCommits(t, "worked.md")
			if one.blocked {
				gh.workerReportsBlocked(t, "the repository has no test for this")
			} else {
				gh.workerReports(t, "acme/edge-sensors", claimedIssue)
			}

			data := filepath.Join(t.TempDir(), "data")
			clone := gh.cloneInto(t, data, "acme/edge-sensors")

			f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
				"repositories": []string{"acme/edge-sensors"}})
			run := f.ended(t, 1)
			if run.Outcome == "failed" || !run.Holding {
				t.Fatalf("run 1 ended as %q (%s) holding=%v, want a run that holds the issue; the factory's log:\n%s",
					run.Outcome, run.Reason, run.Holding, f.output(t))
			}
			worktree := filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
			// What the worker committed, which is in the worktree alone until the issue is let go: a
			// run of the pipeline pushes its own work, and the scripted worker of these tests does not.
			work := committed(t, f, worktree, "worked.md")

			one.decide(t, gh, assignedTo(
				openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour)), "factory-bot"))

			f.eventually(t, 30*time.Second, "the issue to be let go", func() bool {
				var let apiRun
				f.get(t, "/api/runs/1", &let)
				return let.LetGoAt != nil
			})
			// The host keeps nothing of it, and the remote keeps everything: the branch carries the
			// commit of the run, which is what the pull request and every later run of it stand on.
			if _, err := os.Stat(worktree); err == nil {
				t.Errorf("the worktree %s is still on the host after the issue was let go", worktree)
			}
			if localBranch(t, clone, claimedBranch) {
				t.Errorf("the local branch %s is still in %s after the issue was let go", claimedBranch, clone)
			}
			if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != work {
				t.Errorf("%s of the remote is at %q, want the commit the run made (%s): the worktree is pushed before it goes, and a branch that carries a commit stays",
					claimedBranch, head, work)
			}
			removed := fmt.Sprintf("issue edit %d --repo acme/edge-sensors --remove-assignee factory-bot", claimedIssue)
			want := 0
			if one.assigneeRemoved {
				want = 1
			}
			if made := gh.made(t, removed); made != want {
				t.Errorf("the factory made `gh %s` %d times, want %d: only an issue let go without a pull request loses its assignee", removed, made, want)
			}
			if _, err := os.Stat(filepath.Join(data, "run-1.json")); err != nil {
				t.Errorf("the record of the run that was let go is gone: %v", err)
			}
		})
	}
}

// The push comes before everything, so a push that cannot land leaves the whole handover undone: the
// worktree stays where it is, the issue keeps its assignee, and the run carries the warning that
// says what a person has to look at. The alternative is a directory of commits nobody else has.
func TestAWorktreeWhoseCommitsCannotBePushedStaysAndTheRunWarns(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.unassigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerCommits(t, "worked.md")
	gh.workerReportsBlocked(t, "the repository has no test for this")

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	if run := f.ended(t, 1); run.Outcome != "blocked" {
		t.Fatalf("run 1 ended as %q (%s), want blocked; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	worktree := filepath.Join(clone, ".claude", "worktrees", claimedWorktree)

	// Somebody else pushed to the branch, so the commits of the worktree no longer fast-forward it
	// and nothing of them can be put on the remote without deciding whose work wins.
	moved := gh.commitOn(t, "acme/edge-sensors", claimedBranch)
	gh.issue(t, "acme/edge-sensors", assignedTo(
		openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour), readyLabel), "factory-bot"))

	var held apiRun
	f.eventually(t, 30*time.Second, "the run to warn that the commits could not be pushed", func() bool {
		held = apiRun{}
		f.get(t, "/api/runs/1", &held)
		for _, warning := range held.Warnings {
			if strings.Contains(warning, worktree) && strings.Contains(warning, "could not be pushed") {
				return true
			}
		}
		return false
	})
	if held.LetGoAt != nil {
		t.Errorf("the run says the issue was let go at %s, want it held: its commits are on no remote", held.LetGoAt)
	}
	if _, err := os.Stat(filepath.Join(worktree, "worked.md")); err != nil {
		t.Errorf("the worktree of a run whose commits could not be pushed is gone: %v", err)
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != moved {
		t.Errorf("%s of the remote is at %q, want %s: nothing of the other work may be overwritten", claimedBranch, head, moved)
	}
	removed := fmt.Sprintf("issue edit %d --repo acme/edge-sensors --remove-assignee factory-bot", claimedIssue)
	if made := gh.made(t, removed); made != 0 {
		t.Errorf("the factory made `gh %s` %d times, want none: an issue whose worktree stays is not let go", removed, made)
	}
	// The same trouble every poll is one line on the run and not a thousand.
	if len(held.Warnings) != 1 {
		t.Errorf("the run carries %d warnings, want the one that is true: %v", len(held.Warnings), held.Warnings)
	}
}

// An issue the factory let go and that is routed again: its branch is where its work is, so a run
// that finds that branch on the remote takes it back and makes the worktree from it, and a run that
// does not is a first run and claims the issue anew.
func TestAnIssueRoutedAgainAfterItWasLetGoIsTakenBackOnItsBranch(t *testing.T) {
	for _, one := range []struct {
		name      string
		commit    string
		creations int // how often the branch was created on the remote after the second routing
	}{
		{name: "the branch is on the remote, so nothing is claimed again", commit: "worked.md", creations: 1},
		{name: "the branch is gone, so the issue is claimed anew", creations: 2},
	} {
		t.Run(one.name, func(t *testing.T) {
			gh := newGhShim(t)
			gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
			gh.loggedInAs(t, "factory-bot")
			gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
			gh.unassigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
			gh.workerReportsBlocked(t, "the repository has no test for this")
			if one.commit != "" {
				gh.workerCommits(t, one.commit)
			}

			data := filepath.Join(t.TempDir(), "data")
			clone := gh.cloneInto(t, data, "acme/edge-sensors")

			f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
				"repositories": []string{"acme/edge-sensors"}})
			if run := f.ended(t, 1); run.Outcome != "blocked" {
				t.Fatalf("run 1 ended as %q (%s), want blocked; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
			}
			// The maintainer takes the routing label off, and the factory gives the issue back.
			gh.issue(t, "acme/edge-sensors", assignedTo(
				openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour), readyLabel), "factory-bot"))
			var let apiRun
			f.eventually(t, 30*time.Second, "the issue to be let go", func() bool {
				let = apiRun{}
				f.get(t, "/api/runs/1", &let)
				return let.LetGoAt != nil
			})
			work := gh.head(t, "acme/edge-sensors", claimedBranch)
			// And routes it again afterwards, which is the gesture that asks for another run.
			again := let.LetGoAt.Add(time.Second)
			gh.issue(t, "acme/edge-sensors", openIssue(claimedIssue, claimedTitle, again))
			gh.issues(t, "acme/edge-sensors", touched(openIssue(claimedIssue, claimedTitle, again), again))
			gh.timeline(t, "acme/edge-sensors", claimedIssue, labeled("factory", again))

			run := f.ended(t, 2)
			if run.Issue != claimedIssue || run.Branch != claimedBranch || !run.Holding {
				t.Fatalf("run 2 works #%d on %q holding=%v, want #%d on %s held; the factory's log:\n%s",
					run.Issue, run.Branch, run.Holding, claimedIssue, claimedBranch, f.output(t))
			}
			creations := gh.asked(t, "api --method POST repos/acme/edge-sensors/git/refs")
			if creations != one.creations {
				t.Errorf("the branch was created on the remote %d times, want %d", creations, one.creations)
			}
			workers := gh.workers(t)
			if len(workers) != 2 {
				t.Fatalf("the factory started %d workers, want one for each run", len(workers))
			}
			worktree := filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
			if workers[1].cwd != resolved(t, worktree) {
				t.Errorf("the second worker ran in %s, want the worktree %s, made again from the branch", workers[1].cwd, resolved(t, worktree))
			}
			if workers[1].branch != claimedBranch {
				t.Errorf("the second worker ran on %q, want %s", workers[1].branch, claimedBranch)
			}
			if one.commit != "" {
				if workers[1].head != work {
					t.Errorf("the second worker ran at %s, want %s: the work of the first run is what the branch carries", workers[1].head, work)
				}
				if _, err := os.Stat(filepath.Join(worktree, one.commit)); err != nil {
					t.Errorf("the worktree made again from %s does not carry %s: %v", claimedBranch, one.commit, err)
				}
			}
			// The issue is assigned to this host again, because a run of it holds it once more.
			assigned := fmt.Sprintf("issue edit %d --repo acme/edge-sensors --add-assignee factory-bot", claimedIssue)
			if made := gh.made(t, assigned); made != 2 {
				t.Errorf("the factory made `gh %s` %d times, want one per run of the issue", assigned, made)
			}
		})
	}
}

// ---- reading the host ----

// workerOf waits until the scripted worker of a branch has started and answers with what the shim
// recorded of it.
func workerOf(t *testing.T, f *factory, gh *ghShim, branch string) workerStart {
	t.Helper()
	var started workerStart
	f.eventually(t, 60*time.Second, "the worker of "+branch+" to start", func() bool {
		for _, worker := range gh.workers(t) {
			if worker.branch == branch && worker.pid != 0 && worker.pgid != 0 {
				started = worker
				return true
			}
		}
		return false
	})
	return started
}

// committed waits until the scripted worker has committed its work in the worktree and answers with
// that commit. A test that acts before it is past this point would not be testing what happens to
// the work.
func committed(t *testing.T, f *factory, worktree, file string) string {
	t.Helper()
	commit := ""
	f.eventually(t, 60*time.Second, "the worker to commit "+file+" in "+worktree, func() bool {
		git := exec.Command("git", "-C", worktree, "log", "-1", "--format=%H %s")
		git.Env = gitIsolation()
		out, err := git.Output()
		if err != nil {
			return false
		}
		id, subject, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
		if subject != "feat: "+file {
			return false
		}
		commit = id
		return true
	})
	return commit
}

// localBranch says whether a clone of this host still has a branch of that name.
func localBranch(t *testing.T, clone, branch string) bool {
	t.Helper()
	git := exec.Command("git", "-C", clone, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	git.Env = gitIsolation()
	return git.Run() == nil
}

// groupSurvived answers whether anything of a process group is still there after the time it needs
// to take a signal. Signal 0 to the group asks the kernel about it without sending anything.
func groupSurvived(pgid int) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if err := syscall.Kill(-pgid, 0); err != nil {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}
