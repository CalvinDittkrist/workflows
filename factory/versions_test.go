package main

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// What a run is made of, tested the way the rest of the factory is: the real binary, against the gh
// shim and a claude shim that answers what the factory asks of the host, watched through its HTTP
// interface, its records and the calls it made. A host needs no plugin, the factory updates nothing
// before a run, and every run says which versions it was made by.

func TestARunRecordsTheClaudeCodeAndTheFactoryItRanWithAndRunsNoPluginCommand(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.claudeIs(t, "2.1.278 (Claude Code)")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	if run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if len(run.Warnings) != 0 {
		t.Errorf("the run carries the warnings %q, want none", run.Warnings)
	}

	// The one question asked of the host is the version of Claude Code: no marketplace and no plugin is
	// updated, listed or installed.
	if calls := gh.hostCalls(t); !equal(calls, []string{"--version"}) {
		t.Errorf("the factory asked claude for %q, want the version of Claude Code alone", calls)
	}

	// The versions of the run: the Claude Code that runs the sessions and this binary, whose prompts they
	// ran on, and no plugin.
	if run.Versions.ClaudeCode != "2.1.278" {
		t.Errorf("the run records Claude Code %q, want 2.1.278, the version `claude --version` printed", run.Versions.ClaudeCode)
	}
	if run.Versions.Factory != versionFileSays(t) {
		t.Errorf("the run records the factory version %q, want %q, the version in factory/VERSION", run.Versions.Factory, versionFileSays(t))
	}
	record := map[string]any{}
	read(t, filepath.Join(data, "run-1.json"), &record)
	versions, _ := record["versions"].(map[string]any)
	if len(versions) != 2 || versions["claudeCode"] == nil || versions["factory"] == nil {
		t.Errorf("the run's record carries the versions %v, want Claude Code and the factory and nothing else", versions)
	}

	// Read before the first session starts, which the run's own log is the record of.
	versionsAt, workerAt := -1, -1
	for i, e := range run.Events {
		switch {
		case e.Title == "versions" && versionsAt < 0:
			versionsAt = i
		case e.Title == "worker started" && workerAt < 0:
			workerAt = i
		}
	}
	if versionsAt < 0 || workerAt < 0 || versionsAt > workerAt {
		t.Errorf("the run logged the versions at %d and the first session at %d, want the versions first: %+v", versionsAt, workerAt, run.Events)
	}
}

func TestAFactoryStoppedWhileItReadsTheVersionInterruptsTheRunRatherThanFailingIt(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.versionHangs(t)

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	f := gh.work(t, config{"poll": "50ms", "deadline": "5m", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	f.eventually(t, 30*time.Second, "the version this run is waiting on", func() bool {
		return len(gh.hostCalls(t)) > 0
	})

	// No session was started, and the record must not name one that failed.
	f.stop(t, syscall.SIGTERM)
	record := map[string]any{}
	read(t, filepath.Join(data, "run-1.json"), &record)
	if record["outcome"] != "interrupted" || record["state"] != "ended" {
		t.Errorf("the run stopped while it read the version is recorded as %v/%v (%v), want ended/interrupted",
			record["state"], record["outcome"], record["reason"])
	}
	if workers := gh.workers(t); len(workers) != 0 {
		t.Errorf("the factory started %d workers, want none: the stop came before the session", len(workers))
	}
	// And it is on the record as work this factory holds, which is what an interruption is resumed
	// from: the claim stands on the remote.
	if record["holding"] != true || record["worktree"] == "" {
		t.Fatalf("the run is recorded as holding=%v in the worktree %q, want held work with the worktree of its claim",
			record["holding"], record["worktree"])
	}

	// The next start resumes it once, in that same worktree, the way any interruption is resumed.
	gh.versionAnswersAgain(t)
	again := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"listen": freeAddress(t), "repositories": []string{"acme/edge-sensors"}})
	resumed := again.ended(t, 2)
	if resumed.Outcome != "ready" || resumed.Signal != "interruption" {
		t.Fatalf("the run after the restart ended as %q on the signal %q, want a resume of the interruption; the factory's log:\n%s",
			resumed.Outcome, resumed.Signal, again.output(t))
	}
	if resumed.Worktree != record["worktree"] {
		t.Errorf("the resumed run ran in %q, want the worktree %q of the claim the stop cut off", resumed.Worktree, record["worktree"])
	}
}

// A claude that prints something other than "<number> (Claude Code)", such as a wrapper's own line, is
// recorded whole: its first word is no version.
func TestAVersionLineOfAnotherShapeIsRecordedWhole(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.claudeIs(t, "Claude Code 2.1.278")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	if run.Versions.ClaudeCode != "Claude Code 2.1.278" {
		t.Errorf("the run records Claude Code %q, want the line claude printed, whole", run.Versions.ClaudeCode)
	}
}

func TestAVersionThatCannotBeReadIsAWarningAndNoVersionOnTheRecord(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	// A Claude Code that printed no version.
	gh.claudeIs(t, "")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	if run.Versions.ClaudeCode != "" {
		t.Errorf("the run records Claude Code %q, want none: the binary printed no version", run.Versions.ClaudeCode)
	}
	// A run nobody can trace to what it ran with says so.
	if len(run.Warnings) != 1 || !strings.Contains(run.Warnings[0], "Claude Code") {
		t.Fatalf("the run carries the warnings %q, want one for Claude Code", run.Warnings)
	}
	// The run is worked all the same: an unreadable version is not a reason to give the issue back.
	if run.Outcome != "ready" {
		t.Errorf("the run ended as %q (%s), want ready: a version nobody could read must not fail a run", run.Outcome, run.Reason)
	}
}
