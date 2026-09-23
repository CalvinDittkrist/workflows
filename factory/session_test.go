package main

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A worker session reports through its structured result and through nothing else ([ADR 0039]): a
// result line that carries none, or one that does not fit the schema, and a stream that ends without
// a result line at all, are failed runs that say which of these it was, whatever the session wrote.
//
// [ADR 0039]: ../docs/adr/0039-every-session-reports-through-a-structured-result.md
func TestASessionWithoutAResultThatFitsTheSchemaFailsTheRunAndSaysSo(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		output any // nil: the stream carries no result line
		reason string
	}{
		{"no result line", nil, "without a result line"},
		{"an outcome the schema does not have", map[string]any{"outcome": "ready", "summary": "done"}, `the outcome "ready"`},
		{"no summary", map[string]any{"outcome": "complete", "pullRequest": "https://github.com/acme/edge-sensors/pull/104"}, "no summary"},
		{"a field the schema does not have", map[string]any{"outcome": "complete", "summary": "done", "merged": true}, "not an object of the schema"},
		{"not an object", "ready: https://github.com/acme/edge-sensors/pull/104", "not an object of the schema"},
		{"blocked with nothing to say", map[string]any{"outcome": "blocked", "summary": " "}, "empty summary"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			gh := newGhShim(t)
			gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
			gh.loggedInAs(t, "factory-bot")
			gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
			if c.output == nil {
				gh.workerPrintsNoResult(t)
			} else {
				gh.workerResults(t, c.output)
			}
			data := filepath.Join(t.TempDir(), "data")
			gh.cloneInto(t, data, "acme/edge-sensors")
			f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data, "repositories": []string{"acme/edge-sensors"}})
			run := f.ended(t, 1)
			if run.Outcome != "failed" || !strings.Contains(run.Reason, c.reason) {
				t.Fatalf("the run ended as %q because %q, want failed with %q in the reason; the factory's log:\n%s",
					run.Outcome, run.Reason, c.reason, f.output(t))
			}
			if c.output != nil && !strings.Contains(run.Reason, "does not fit the schema") {
				t.Errorf("the run says %q, want it to say the result does not fit the schema", run.Reason)
			}
			if run.PullRequest != "" {
				t.Errorf("the run carries the pull request %q, want none from a result that does not fit", run.PullRequest)
			}
		})
	}
}

// The process and the result are read apart: a session whose process fails is a failed run with what
// it said, even when its result line reported a pull request.
func TestASessionThatExitsInAnErrorFailsTheRunWhateverItsResultSaid(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := launch(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data, "repositories": []string{"acme/edge-sensors"}},
		append(gh.env, "CLAUDE_SHIM_EXIT=3"))
	run := f.ended(t, 1)
	if run.Outcome != "failed" || !strings.Contains(run.Reason, "exit 3") {
		t.Fatalf("the run ended as %q because %q, want failed on exit 3; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if run.PullRequest != "" {
		t.Errorf("the run carries the pull request %q, want none: a session that failed is not ready", run.PullRequest)
	}
}

// A session that runs past the timeout of its stage is ended with its whole process group, and the
// run fails naming the stage. That is not the run's deadline, which is further off here and would
// have ended it with the outcome timeout.
func TestASessionThatRunsPastItsStageTimeoutIsEndedAndFailsNamingTheStage(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerWaits(t, 10*time.Minute) // far past the hurried binary's session timeout
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := launchBinary(t, hurried, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}}, gh.env)
	run := f.ended(t, 1)
	if run.Outcome != "failed" || !strings.Contains(run.Reason, "stage implement") || !strings.Contains(run.Reason, hurriedTimeout) {
		t.Fatalf("the run ended as %q because %q, want failed naming the stage implement and its timeout of %s; the factory's log:\n%s",
			run.Outcome, run.Reason, hurriedTimeout, f.output(t))
	}
	workers := gh.workers(t)
	if len(workers) != 1 {
		t.Fatalf("the factory started %d workers, want one", len(workers))
	}
	if survived(workers[0].pid) {
		t.Errorf("the session (process %d) survived its timeout", workers[0].pid)
	}
	if err := syscall.Kill(-workers[0].pgid, 0); err == nil {
		t.Errorf("the process group %d of the session survived its timeout", workers[0].pgid)
	}
}
