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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// A pull request that ended is a decision about the run that opened it and about no other. The run
// that takes a routed issue back afterwards stands on a claim of its own and has no pull request
// yet, so the closed one of the run before it must not reach it: a factory that carries that URL
// over cancels the new run on its first poll and hands the issue straight back, over and over.
func TestARoutedIssueIsNotCancelledByThePullRequestOfTheRunBeforeIt(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.unassigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerCommits(t, "worked.md")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	// Both runs take their time, so the second one is still going while the polls of its first
	// seconds read what the factory holds.
	gh.workerWaits(t, 3*time.Second)

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	if run := f.ended(t, 1); run.PullRequest == "" || !run.Holding {
		t.Fatalf("run 1 ended as %q (%s) holding=%v with pull request %q, want a run that holds the issue and opened one; the factory's log:\n%s",
			run.Outcome, run.Reason, run.Holding, run.PullRequest, f.output(t))
	}
	// The maintainer closes the pull request, which is the decision that lets the issue go.
	gh.pull(t, "acme/edge-sensors", claimedIssue, "closed", false)
	gh.issue(t, "acme/edge-sensors", assignedTo(
		openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour)), "factory-bot"))
	var let apiRun
	f.eventually(t, 30*time.Second, "the issue to be let go", func() bool {
		let = apiRun{}
		f.get(t, "/api/runs/1", &let)
		return let.LetGoAt != nil
	})
	// And routes the issue again, which asks for another run of work that starts where it stopped.
	again := let.LetGoAt.Add(time.Second)
	gh.issue(t, "acme/edge-sensors", openIssue(claimedIssue, claimedTitle, again))
	gh.issues(t, "acme/edge-sensors", touched(openIssue(claimedIssue, claimedTitle, again), again))
	gh.timeline(t, "acme/edge-sensors", claimedIssue, labeled("factory", again))

	run := f.ended(t, 2)
	if run.Outcome == "cancelled" {
		t.Fatalf("run 2 ended as cancelled (%s), want a run that was left to work: the pull request of run 1 says nothing about it; the factory's log:\n%s",
			run.Reason, f.output(t))
	}
	if run.Outcome != "ready" {
		t.Errorf("run 2 ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if workers := gh.workers(t); len(workers) != 2 {
		t.Errorf("the factory started %d workers, want one for each run", len(workers))
	}
}

// The one thing that must never let an issue go is a GitHub nobody can reach. A rate limit, a login
// that expired, a repository that answers an error: none of them is a maintainer's decision, so the
// factory keeps what it holds until GitHub answers, says so once however long that takes, and acts
// on the decision the moment it can read it ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func TestAHeldIssueThatCannotBeReadIsKeptAndSaidOnce(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.unassigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerCommits(t, "worked.md")
	gh.workerReportsBlocked(t, "the repository has no test for this")

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	// The one request that says what became of the issue this factory holds is the one GitHub fails.
	gh.fail(t, fmt.Sprintf("api repos/acme/edge-sensors/issues/%d", claimedIssue))

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	if run := f.ended(t, 1); !run.Holding {
		t.Fatalf("run 1 ended as %q (%s) holding=%v, want a run that holds the issue; the factory's log:\n%s",
			run.Outcome, run.Reason, run.Holding, f.output(t))
	}
	worktree := filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
	work := committed(t, f, worktree, "worked.md")
	// The decision stands on GitHub all along; it is the reading of it that fails.
	gh.issue(t, "acme/edge-sensors", assignedTo(
		openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour), readyLabel), "factory-bot"))

	f.never(t, 2*time.Second, "the factory let the issue go while it could not read it", func() bool {
		var held apiRun
		f.get(t, "/api/runs/1", &held)
		return held.LetGoAt != nil
	})
	if _, err := os.Stat(worktree); err != nil {
		t.Errorf("the worktree %s is gone though GitHub said nothing: %v", worktree, err)
	}
	if !localBranch(t, clone, claimedBranch) {
		t.Errorf("the local branch %s is gone from %s though GitHub said nothing", claimedBranch, clone)
	}
	removed := fmt.Sprintf("issue edit %d --repo acme/edge-sensors --remove-assignee factory-bot", claimedIssue)
	if made := gh.made(t, removed); made != 0 {
		t.Errorf("the factory made `gh %s` %d times, want none: an issue it cannot read is one it holds on to", removed, made)
	}
	// Said once over the dozens of polls of that window, not once a poll: a GitHub that is down for a
	// day must not fill the log with one sentence.
	if said := strings.Count(f.output(t), "is held by this factory and could not be read"); said != 1 {
		t.Errorf("the factory said it could not read the issue %d times, want once; its log:\n%s", said, f.output(t))
	}

	// And when GitHub answers again, the decision that stood there all along is acted on.
	gh.fail(t, "")
	f.eventually(t, 30*time.Second, "the issue to be let go once GitHub can be read again", func() bool {
		var let apiRun
		f.get(t, "/api/runs/1", &let)
		return let.LetGoAt != nil
	})
	if _, err := os.Stat(worktree); err == nil {
		t.Errorf("the worktree %s is still on the host after the issue was let go", worktree)
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != work {
		t.Errorf("%s of the remote is at %q, want the commit of the run (%s)", claimedBranch, head, work)
	}
}

// The worktree is where the commits are written, not where they live: they are in the clone's branch
// too, which is what is left when somebody removes the directory by hand or a data directory comes
// back without it. That branch is pushed before the name goes, or `branch -D` would be the one thing
// in this factory that deletes work ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func TestAWorktreeRemovedByHandStillHasItsBranchPushedBeforeThatBranchGoes(t *testing.T) {
	t.Parallel()
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
	if run := f.ended(t, 1); !run.Holding {
		t.Fatalf("run 1 ended as %q (%s) holding=%v, want a run that holds the issue; the factory's log:\n%s",
			run.Outcome, run.Reason, run.Holding, f.output(t))
	}
	worktree := filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
	work := committed(t, f, worktree, "worked.md")
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}

	gh.issue(t, "acme/edge-sensors", assignedTo(
		openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour), readyLabel), "factory-bot"))
	f.eventually(t, 30*time.Second, "the issue to be let go", func() bool {
		var let apiRun
		f.get(t, "/api/runs/1", &let)
		return let.LetGoAt != nil
	})
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != work {
		t.Errorf("%s of the remote is at %q, want the commit that was only in the clone (%s): what is left of the work is pushed from there",
			claimedBranch, head, work)
	}
	if localBranch(t, clone, claimedBranch) {
		t.Errorf("the local branch %s is still in %s after the issue was let go", claimedBranch, clone)
	}
}

// Routing an issue this factory let go again asks for one more run, and one is what it gets. The run
// it starts answers that routing whatever becomes of it — a claim another host won in between makes
// it a lost run, and lost or not the gesture is spent — or the factory would take the issue back on
// every poll for as long as the label stays on it.
func TestARoutingAnsweredByARunIsNotTakenUpAgain(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	now := time.Now().UTC()
	first, again := now.Add(-6*time.Hour), now.Add(-time.Hour)
	gh.issues(t, "acme/edge-sensors", openIssue(claimedIssue, claimedTitle, now.Add(-72*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", claimedIssue, labeled("factory", again))

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	// The first run worked the issue and was let go; the second was the take-back that routing it
	// again asked for, and another claimer had the branch by then.
	let := record(1, claimedIssue, claimedTitle, signalRouted, outcomeReady, true, first, first.Add(time.Minute))
	let.SignalAt = first
	letGo := first.Add(2 * time.Minute)
	let.LetGoAt = &letGo
	lost := record(2, claimedIssue, claimedTitle, signalRouted, outcomeLost, false, again, again.Add(time.Minute))
	lost.SignalAt, lost.Worktree = again, ""
	records(t, data, let, lost)

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	f.never(t, 3*time.Second, "the factory started a run for a routing that run 2 already answered",
		func() bool { return !f.missing(t, 3) })
	var line apiLine
	f.get(t, "/api/line", &line)
	if len(line.Queue) != 0 || len(line.Now) != 0 {
		t.Errorf("the factory queues %v and runs %d, want an idle line: the routing was answered and the issue waits for the next one",
			keys(line.Queue), len(line.Now))
	}
}

// The issues this factory holds are asked about one by one, and one whose run is over is only
// waiting to be cleaned up: it is asked about once every heldPolls poll intervals, not on each poll,
// or a host with a dozen pull requests standing in review would spend its whole hour of requests on
// issues nobody has touched. What that costs is the few minutes such an issue may wait, and the decision
// made on it is still heard and acted on.
func TestAnIdleHeldIssueIsAskedAboutFarMoreRarelyThanThePoll(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.unassigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerCommits(t, "worked.md")
	gh.workerReportsBlocked(t, "the repository has no test for this")

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	const poll = 50 * time.Millisecond
	f := gh.work(t, config{"poll": poll.String(), "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	if run := f.ended(t, 1); !run.Holding {
		t.Fatalf("run 1 ended as %q (%s) holding=%v, want a run that holds the issue; the factory's log:\n%s",
			run.Outcome, run.Reason, run.Holding, f.output(t))
	}
	committed(t, f, filepath.Join(clone, ".claude", "worktrees", claimedWorktree), "worked.md")

	// The line is read once a poll, so what the shim logged of it is how often this factory polled.
	// The pause between two questions about the issue is heldPolls poll intervals of wall time, and a
	// poll takes its own work on top of its interval, so the bound is the time the window took: on a
	// loaded machine the window of polls is longer and fits more questions.
	line := "api " + issuesRequest("acme/edge-sensors", "factory")
	held := fmt.Sprintf("api repos/acme/edge-sensors/issues/%d", claimedIssue)
	const window = 30
	polled, asked, from := gh.made(t, line), gh.made(t, held), time.Now()
	f.eventually(t, 60*time.Second, fmt.Sprintf("%d more polls of the line", window), func() bool {
		return gh.made(t, line)-polled >= window
	})
	polls, reads, took := gh.made(t, line)-polled, gh.made(t, held)-asked, time.Since(from)
	if want := int(took/(heldPolls*poll)) + 2; reads > want {
		t.Errorf("the factory asked GitHub about the issue it holds %d times over %d polls in %s, want at most %d: an issue that only waits to be cleaned up is asked about once every %d poll intervals",
			reads, polls, took, want, heldPolls)
	}

	// And rarely is often enough: the decision made on it is read and acted on all the same.
	gh.issue(t, "acme/edge-sensors", assignedTo(
		openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour), readyLabel), "factory-bot"))
	f.eventually(t, 30*time.Second, "the issue to be let go", func() bool {
		var let apiRun
		f.get(t, "/api/runs/1", &let)
		return let.LetGoAt != nil
	})
}

// Pushing a worktree pushes what its HEAD points at, and a worker that left that HEAD behind its own
// branch — detached, or moved by hand — is the one case where that is not the work. The branch in
// the clone still holds those commits, so it holds the whole handover up until the poll after it has
// pushed them from there: a handover that carried on would read a remote branch with nothing beyond
// its base and delete it, and the work would be on this host alone ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func TestAWorktreeWhoseHeadIsBehindItsBranchStillHasEveryCommitPushed(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.unassigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerCommits(t, "worked.md")
	gh.workerReportsBlocked(t, "the repository has no test for this")

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	base := gh.head(t, "acme/edge-sensors", "main")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	if run := f.ended(t, 1); !run.Holding {
		t.Fatalf("run 1 ended as %q (%s) holding=%v, want a run that holds the issue; the factory's log:\n%s",
			run.Outcome, run.Reason, run.Holding, f.output(t))
	}
	worktree := filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
	work := committed(t, f, worktree, "worked.md")
	// The HEAD of the worktree goes back to the base, while the branch keeps the commit of the run.
	checkout := exec.Command("git", "-C", worktree, "checkout", "--quiet", "--detach", base)
	checkout.Env = gitIsolation()
	if out, err := checkout.CombinedOutput(); err != nil {
		t.Fatalf("the HEAD of %s could not be detached: %v: %s", worktree, err, out)
	}

	gh.issue(t, "acme/edge-sensors", assignedTo(
		openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour), readyLabel), "factory-bot"))
	var let apiRun
	f.eventually(t, 30*time.Second, "the issue to be let go", func() bool {
		let = apiRun{}
		f.get(t, "/api/runs/1", &let)
		return let.LetGoAt != nil
	})
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != work {
		t.Errorf("%s of the remote is at %q, want the commit of the run (%s): the commits of the branch are pushed before anything of it goes",
			claimedBranch, head, work)
	}
	removal := "api --method DELETE repos/acme/edge-sensors/git/refs/heads/" + claimedBranch
	if made := gh.made(t, removal); made != 0 {
		t.Errorf("the factory made `gh %s` %d times, want none: the branch carries the commit of the run", removal, made)
	}
	if localBranch(t, clone, claimedBranch) {
		t.Errorf("the local branch %s is still in %s after the issue was let go", claimedBranch, clone)
	}
	if _, err := os.Stat(worktree); err == nil {
		t.Errorf("the worktree %s is still on the host after the issue was let go", worktree)
	}
	// The poll that could not finish says what it waited for, once.
	held := 0
	for _, warning := range let.Warnings {
		if strings.Contains(warning, "holds commits origin/"+claimedBranch+" does not have") {
			held++
		}
	}
	if held != 1 {
		t.Errorf("the run carries %d warnings about the commits only the clone had, want one: %v", held, let.Warnings)
	}
}

// The last step of letting an issue go is taking this host off it, and the handover is not done
// before GitHub has made that removal. A run marked as let go is never taken up again, so an issue
// marked while its assignee is still there would be one no other claimer and no person ever sees as
// free. Every step before it can be made again, so the handover stops where it is and a later poll
// takes it the rest of the way.
func TestAnIssueIsNotLetGoWhileThisHostIsStillItsAssignee(t *testing.T) {
	t.Parallel()
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
	if run := f.ended(t, 1); !run.Holding {
		t.Fatalf("run 1 ended as %q (%s) holding=%v, want a run that holds the issue; the factory's log:\n%s",
			run.Outcome, run.Reason, run.Holding, f.output(t))
	}
	worktree := filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
	work := committed(t, f, worktree, "worked.md")

	// GitHub refuses to take the assignee off, and the maintainer takes the routing label off.
	gh.fail(t, "issue edit * --remove-assignee *")
	gh.issue(t, "acme/edge-sensors", assignedTo(
		openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour), readyLabel), "factory-bot"))

	// The work reaches the remote and the worktree goes with it — those steps are done — but the
	// issue itself is not given back while GitHub still names this host as its assignee.
	f.eventually(t, 30*time.Second, "the worktree to be removed", func() bool {
		_, err := os.Stat(worktree)
		return err != nil
	})
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != work {
		t.Errorf("%s of the remote is at %q, want the commit of the run (%s): the worktree is pushed before it goes", claimedBranch, head, work)
	}
	var stuck apiRun
	f.never(t, 2*time.Second, "the issue was let go while this host is still its assignee", func() bool {
		stuck = apiRun{}
		f.get(t, "/api/runs/1", &stuck)
		return stuck.LetGoAt != nil
	})
	f.get(t, "/api/runs/1", &stuck)
	said := 0
	for _, warning := range stuck.Warnings {
		if strings.Contains(warning, "could not be taken off") {
			said++
		}
	}
	if said != 1 {
		t.Errorf("the run carries %d warnings about the assignee that stayed, want one: %v", said, stuck.Warnings)
	}

	// GitHub answers again, and a later poll finishes what the one before it could not.
	gh.fail(t, "")
	f.eventually(t, 30*time.Second, "the issue to be let go", func() bool {
		var let apiRun
		f.get(t, "/api/runs/1", &let)
		return let.LetGoAt != nil
	})
	removal := fmt.Sprintf("issue edit %d --repo acme/edge-sensors --remove-assignee factory-bot", claimedIssue)
	if made := gh.made(t, removal); made < 2 {
		t.Errorf("the factory made `gh %s` %d times, want the refused one and the one GitHub took", removal, made)
	}
}

// A branch this factory takes back is recognised by the work it carries and not by its name alone.
// Every claimer spells an issue's branch the same way, so a branch another claimer cut in the
// seconds between this factory's reading of the line and its fetch carries the name of the run that
// once held the issue — and two claimers working one branch is what the claim exists to make
// impossible. A branch is left on the remote only when it carries work the base does not have, so a
// branch of that name with nothing on it is somebody else's claim and this issue is claimed anew:
// GitHub refuses the second creation of the reference, and this run is recorded as lost.
func TestABranchOfTheIssuesNameThatCarriesNoWorkIsNotTakenBack(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReportsBlocked(t, "the repository has no test for this")

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	base := gh.head(t, "acme/edge-sensors", "main")

	// This factory worked the issue once and let it go; its branch carried nothing, so it went from
	// the remote with everything else.
	letGoAt := time.Now().UTC().Add(-time.Hour)
	let := record(1, claimedIssue, claimedTitle, signalRouted, outcomeBlocked, false, letGoAt.Add(-time.Hour), letGoAt)
	let.LetGoAt = &letGoAt
	records(t, data, let)
	// Another claimer has taken the issue since: the branch of it is on the remote again, cut from
	// the base and carrying nothing of the run before.
	gh.branchAt(t, "acme/edge-sensors", claimedBranch, base)
	// And the issue is routed again, which asks this factory for another run.
	again := letGoAt.Add(time.Minute)
	gh.issues(t, "acme/edge-sensors", touched(openIssue(claimedIssue, claimedTitle, again), again))
	gh.timeline(t, "acme/edge-sensors", claimedIssue, labeled("factory", again))
	gh.issue(t, "acme/edge-sensors", openIssue(claimedIssue, claimedTitle, again))

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 2)
	if run.Outcome != outcomeLost {
		t.Fatalf("run 2 ended as %q (%s), want lost: the branch of that name carries no work of this factory; the factory's log:\n%s",
			run.Outcome, run.Reason, f.output(t))
	}
	// Nothing of the other claimer's branch was touched, and this host took nothing of the issue.
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != base {
		t.Errorf("%s of the remote is at %q, want the commit the other claimer cut it from (%s)", claimedBranch, head, base)
	}
	assigned := fmt.Sprintf("issue edit %d --repo acme/edge-sensors --add-assignee factory-bot", claimedIssue)
	if made := gh.made(t, assigned); made != 0 {
		t.Errorf("the factory made `gh %s` %d times, want none: an issue another claimer holds is not assigned to this host", assigned, made)
	}
	if workers := gh.workers(t); len(workers) != 0 {
		t.Errorf("the factory started %d workers, want none: nothing of this issue is this host's to work", len(workers))
	}
	if _, err := os.Stat(filepath.Join(clone, ".claude", "worktrees", claimedWorktree)); err == nil {
		t.Errorf("a worktree of %s was made in %s for a branch another claimer holds", claimedBranch, clone)
	}
}

// What became of a pull request is a decision about the issue it was opened for, and about no other.
// The URL in the record came out of a worker's report, which is written by a model out of a session
// that reads what a stranger wrote in an issue: a report that names another pull request of the
// repository must not hand this issue's work to whoever closes that one.
func TestAPullRequestOfAnotherBranchIsNoDecisionAboutTheIssue(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.unassigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerCommits(t, "worked.md")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	if run := f.ended(t, 1); run.PullRequest == "" || !run.Holding {
		t.Fatalf("run 1 ended as %q (%s) holding=%v with pull request %q, want a run that holds the issue and named one; the factory's log:\n%s",
			run.Outcome, run.Reason, run.Holding, run.PullRequest, f.output(t))
	}
	worktree := filepath.Join(clone, ".claude", "worktrees", claimedWorktree)

	// The pull request that URL names is of another branch of the same repository, and somebody
	// closes it. The issue itself carries no decision: it is open, routed and held.
	gh.pullOf(t, "acme/edge-sensors", claimedIssue, "closed", false, "acme/edge-sensors", "feat/900-another-issue-entirely")
	gh.issue(t, "acme/edge-sensors", assignedTo(
		openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour)), "factory-bot"))
	f.never(t, 3*time.Second, "the issue was let go by a pull request of another branch", func() bool {
		var let apiRun
		f.get(t, "/api/runs/1", &let)
		return let.LetGoAt != nil
	})
	if _, err := os.Stat(worktree); err != nil {
		t.Errorf("the worktree %s is gone: %v; a pull request of another branch decides nothing about this issue", worktree, err)
	}
	if said := f.output(t); !strings.Contains(said, "not of "+claimedBranch) {
		t.Errorf("the factory says nothing about the pull request that is not of the branch it holds; its log:\n%s", said)
	}
	// The gestures that are about this issue still reach it: the routing label comes off.
	gh.issue(t, "acme/edge-sensors", assignedTo(
		openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour), readyLabel), "factory-bot"))
	f.eventually(t, 30*time.Second, "the issue to be let go", func() bool {
		var let apiRun
		f.get(t, "/api/runs/1", &let)
		return let.LetGoAt != nil
	})
}

// The issues this factory holds are read in one bounded pass, and the run that is going is read
// first. What that reading carries for it is a cancel, which has to reach a worker while it is still
// working; an issue that only waits to be cleaned up can be read after it, or by the next poll.
func TestTheIssueOfARunningWorkerIsReadBeforeTheIssuesThatOnlyWait(t *testing.T) {
	t.Parallel()
	const (
		waitingIssue = 101 // held, idle, and sorted before the issue being worked
		waitingTitle = "Widen the retry window"
	)
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerWaits(t, 10*time.Minute)

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	// An issue of an earlier run that this factory still holds and nobody has decided about.
	ended := time.Now().UTC().Add(-time.Hour)
	held := record(1, waitingIssue, waitingTitle, signalRouted, outcomeBlocked, true, ended.Add(-time.Hour), ended)
	records(t, data, held)
	gh.issue(t, "acme/edge-sensors", assignedTo(openIssue(waitingIssue, waitingTitle, ended), "factory-bot"))
	gh.issue(t, "acme/edge-sensors", assignedTo(
		openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour)), "factory-bot"))

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	workerOf(t, f, gh, claimedBranch)

	line := "api " + issuesRequest("acme/edge-sensors", "factory")
	running := fmt.Sprintf("api repos/acme/edge-sensors/issues/%d", claimedIssue)
	waiting := fmt.Sprintf("api repos/acme/edge-sensors/issues/%d", waitingIssue)
	const polls = 3
	f.eventually(t, 60*time.Second, fmt.Sprintf("%d polls that read both held issues", polls), func() bool {
		return len(pollsReading(gh.calls(t), line, running, waiting)) >= polls
	})
	for i, poll := range pollsReading(gh.calls(t), line, running, waiting) {
		if positionIn(poll, waiting) < positionIn(poll, running) {
			t.Fatalf("poll %d asked GitHub about the issue that waits (#%d) before the one being worked (#%d): %v",
				i, waitingIssue, claimedIssue, poll)
		}
	}
}

// pollsReading splits what the shim logged into one slice per poll of the line and answers with
// those polls that asked about both of these issues.
func pollsReading(calls []string, line string, requests ...string) [][]string {
	polls, poll := [][]string{}, []string{}
	ends := func() {
		for _, request := range requests {
			if positionIn(poll, request) < 0 {
				return
			}
		}
		polls = append(polls, poll)
	}
	for _, call := range calls {
		if call != line {
			poll = append(poll, call)
			continue
		}
		ends()
		poll = []string{}
	}
	ends()
	return polls
}

// positionIn is where a request stands among the calls of one poll, or -1 when it is not among them.
func positionIn(calls []string, request string) int {
	for i, call := range calls {
		if call == request {
			return i
		}
	}
	return -1
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
