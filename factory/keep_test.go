package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A run that ends without a pull request holds its issue. What its sessions committed is pushed as
// the last step of that ending. The host then holds a copy of what the remote holds, so a lost disk
// or data directory loses no commit ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func TestARunThatEndsWithoutAPullRequestLeavesItsCommitsOnTheRemote(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		deadline string
		worker   func(*testing.T, *ghShim)
		outcome  string
	}{
		{"on the deadline, over a worker that hangs after committing", "3s",
			func(t *testing.T, gh *ghShim) { gh.workerWaits(t, 60*time.Second) }, outcomeTimeout},
		{"blocked, by a worker that committed first", "90s",
			func(t *testing.T, gh *ghShim) { gh.workerReportsBlocked(t, "the repository has no test for this") }, outcomeBlocked},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			gh := newGhShim(t)
			gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
			gh.loggedInAs(t, "factory-bot")
			gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
			gh.workerCommits(t, "worked.md")
			c.worker(t, gh)

			data := filepath.Join(t.TempDir(), "data")
			clone := gh.cloneInto(t, data, "acme/edge-sensors")
			f := gh.work(t, config{"poll": "50ms", "deadline": c.deadline, "data_dir": data,
				"repositories": []string{"acme/edge-sensors"}})
			run := f.ended(t, 1)
			if run.Outcome != c.outcome || !run.Holding {
				t.Fatalf("run 1 ended as %q (%s) holding=%v, want %s holding the issue; the factory's log:\n%s",
					run.Outcome, run.Reason, run.Holding, c.outcome, f.output(t))
			}
			worktree := filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
			work := committed(t, f, worktree, "worked.md")
			if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != work {
				t.Errorf("%s of the remote is at %q, want the worktree's HEAD %s: the ending pushes what the run committed", claimedBranch, head, work)
			}
			if len(run.Warnings) != 0 {
				t.Errorf("the run carries the warnings %v, want none: the push landed", run.Warnings)
			}
			if _, err := os.Stat(worktree); err != nil {
				t.Errorf("the worktree of a run that holds its issue is gone: %v", err)
			}
		})
	}
}

// A push the remote refuses at the ending loses nothing and changes nothing of the ending: the run
// keeps its outcome, the issue its worktree, branch and assignee, and the refusal is a warning the
// notification names, since the commits are on the host alone.
func TestAPushTheRemoteRefusesAtTheEndingIsAWarningTheNotificationNames(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.comments(t, "acme/edge-sensors", claimedIssue)
	gh.workerCommits(t, "worked.md")
	gh.workerWaits(t, 60*time.Second)

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "3s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}, "notify": maintainers})
	worktree := filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
	committed(t, f, worktree, "worked.md")
	// The claim made the branch; from now on the remote refuses every push.
	refusesPushes(t, gh, "acme/edge-sensors", "the remote takes no push today")

	run := f.ended(t, 1)
	if run.Outcome != outcomeTimeout {
		t.Fatalf("run 1 ended as %q (%s), want timeout; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	var warning string
	for _, w := range run.Warnings {
		if strings.Contains(w, claimedBranch) && strings.Contains(w, "the remote takes no push today") {
			warning = w
		}
	}
	if warning == "" {
		t.Fatalf("the run carries the warnings %v, want one that names %s and what git said", run.Warnings, claimedBranch)
	}
	f.notified(t, 1)
	said := gh.commented(t, "acme/edge-sensors", claimedIssue)
	if !strings.Contains(said, "`timeout`") || !strings.Contains(said, "the remote takes no push today") {
		t.Errorf("the comment on the issue is %q, want the timeout and the push that failed", said)
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head == "" {
		t.Errorf("%s is gone from the remote, want it kept", claimedBranch)
	}
	if _, err := os.Stat(filepath.Join(worktree, "worked.md")); err != nil {
		t.Errorf("the worktree of a run whose push failed is gone: %v", err)
	}
	if made := gh.made(t, fmt.Sprintf("issue edit %d --repo acme/edge-sensors --remove-assignee factory-bot", claimedIssue)); made != 0 {
		t.Errorf("the factory took itself off the issue %d times, want never: the issue stays held", made)
	}
}

// A run the next start finds active was ended by a factory that is gone. Its commits may be in the
// worktree alone. The start pushes them before it works, so they are on the remote whatever becomes
// of the host after it. A start that is paused pushes nothing, and pushes them once when it is
// unpaused.
func TestAnInterruptedRunTheStartFindsHasItsCommitsPushed(t *testing.T) {
	t.Parallel()
	for _, paused := range []bool{false, true} {
		t.Run(fmt.Sprintf("paused=%v", paused), func(t *testing.T) {
			t.Parallel()
			interruptedRunIsPushed(t, paused)
		})
	}
}

func interruptedRunIsPushed(t *testing.T, paused bool) {
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	gh.issues(t, "acme/edge-sensors")
	gh.comments(t, "acme/edge-sensors", claimedIssue)
	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	main := gh.head(t, "acme/edge-sensors", "main")
	gh.branchAt(t, "acme/edge-sensors", claimedBranch, main)

	// The worktree of the run carries a commit the remote never saw.
	worktree := filepath.Join(clone, ".claude", "worktrees", claimedWorktree)
	gh.git(t, clone, "fetch", "-q", "origin")
	gh.git(t, clone, "worktree", "add", "-q", "-b", claimedBranch, worktree, "origin/"+claimedBranch)
	writeFile(t, filepath.Join(worktree, "worked.md"), "the work of the interrupted run\n")
	gh.git(t, worktree, "add", "worked.md")
	gh.git(t, worktree, "-c", "user.email=worker@example.com", "-c", "user.name=worker", "commit", "-q", "-m", "feat: worked.md")
	work := gh.git(t, worktree, "rev-parse", "HEAD")

	// The second interruption of the issue, which waits for a person rather than being resumed.
	began := time.Now().UTC().Add(-2 * time.Hour)
	first := record(1, claimedIssue, claimedTitle, signalRouted, outcomeInterrupted, true, began, began.Add(time.Minute))
	first.Worktree = worktree
	active := record(2, claimedIssue, claimedTitle, signalInterruption, "", true, began, began)
	active.State, active.Outcome, active.EndedAt, active.Worktree = "running", "", nil, worktree
	records(t, data, first, active)

	f := gh.work(t, config{"poll": "50ms", "data_dir": data, "paused": paused,
		"repositories": []string{"acme/edge-sensors"}, "notify": maintainers})
	if ended := f.ended(t, 2); ended.Outcome != outcomeInterrupted {
		t.Fatalf("run 2 ended as %q, want interrupted: the start found it active", ended.Outcome)
	}
	if paused {
		f.never(t, time.Second, "a push while the configuration pauses the factory",
			func() bool { return gh.head(t, "acme/edge-sensors", claimedBranch) != main })
		f.configure(t, config{"paused": false})
		// A second pause and unpause makes no second push.
		f.eventually(t, 10*time.Second, "the push once the factory works again",
			func() bool { return gh.head(t, "acme/edge-sensors", claimedBranch) == work })
		f.configure(t, config{"paused": true})
		f.eventually(t, 5*time.Second, "the second pause", func() bool { return f.state(t) == "paused" })
		f.configure(t, config{"paused": false})
		f.eventually(t, 5*time.Second, "the second unpause", func() bool { return f.state(t) != "paused" })
	}
	f.notified(t, 2)
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != work {
		t.Errorf("%s of the remote is at %q, want the commit of the interrupted run %s; the factory's log:\n%s",
			claimedBranch, head, work, f.output(t))
	}
	run := f.ended(t, 2)
	if len(factoryTitles(run, "copied "+claimedBranch)) != 1 {
		t.Errorf("run 2 logged %v, want the one push that copied %s to the remote", factoryTitles(run, "copied "), claimedBranch)
	}
}

// A run that ends ready pushed its branch for its pull request already, so its ending adds no push.
func TestARunThatEndsReadyMakesNoPushOfItsEnding(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady {
		t.Fatalf("run 1 ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if copied := factoryTitles(run, "copied "); len(copied) != 0 {
		t.Errorf("the ready run logged %v, want no push of its ending", copied)
	}
	if len(run.Warnings) != 0 {
		t.Errorf("the ready run carries the warnings %v, want none", run.Warnings)
	}
}

// refusesPushes makes the shim's GitHub refuse every push to a repository with that message, as a
// remote that is down or protects its branches does.
func refusesPushes(t *testing.T, gh *ghShim, repository, message string) {
	t.Helper()
	hook := filepath.Join(gh.remotePath(repository), "hooks", "pre-receive")
	writeFile(t, hook, "#!/bin/sh\necho '"+message+"' >&2\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
}
