package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// What a run is made of, tested the way the rest of the factory is: the real binary, against the gh
// shim and a claude shim that answers the plugin commands, watched through its HTTP interface and
// through the calls it made. A merged fix to the worker has to reach the next run, and every run has
// to say which versions it was made by.

func TestARunUpdatesTheWorkerPluginBeforeItStartsAndRecordsWhatItRanWith(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	gh.installs(t, "0.9.3", "2.1.278 (Claude Code)")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	if run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if len(run.Warnings) != 0 {
		t.Errorf("the run carries the warnings %q, want none: every plugin command answered", run.Warnings)
	}

	// Exactly these calls, in this order: the marketplace, then the plugin in it, then what the two
	// left installed. Nothing updates or installs Claude Code, and nothing updates the factory — the
	// only things this run may change about the host are the marketplace and the worker plugin.
	want := []string{
		"plugin marketplace update " + marketplace,
		"plugin update " + workerPlugin,
		"plugin list --json",
		"--version",
	}
	if calls := gh.pluginCalls(t); !equal(calls, want) {
		t.Errorf("the factory asked claude for\n%q\nwant\n%q", calls, want)
	}

	// The versions of the run: the plugin as the host's own scope holds it after the update — not the
	// one a checkout on the host carries — the Claude Code that runs the session, and this binary.
	if run.Versions.Worker != "0.9.3" {
		t.Errorf("the run records the worker plugin %q, want 0.9.3, the version installed for the user the factory runs as", run.Versions.Worker)
	}
	if run.Versions.ClaudeCode != "2.1.278" {
		t.Errorf("the run records Claude Code %q, want 2.1.278, the version `claude --version` printed", run.Versions.ClaudeCode)
	}
	if run.Versions.Factory != versionFileSays(t) {
		t.Errorf("the run records the factory version %q, want %q, the version in factory/VERSION", run.Versions.Factory, versionFileSays(t))
	}

	// Read after the update and before the worker starts, which the run's own log is the record of.
	marketplaceAt, pluginAt, versionsAt, workerAt := -1, -1, -1, -1
	for i, e := range run.Events {
		switch {
		case e.Title == "updated the marketplace "+marketplace:
			marketplaceAt = i
		case e.Title == "updated the plugin "+workerPlugin:
			pluginAt = i
		case e.Title == "versions":
			versionsAt = i
		case e.Title == "worker started":
			workerAt = i
		}
	}
	if marketplaceAt < 0 || pluginAt < 0 || versionsAt < 0 || workerAt < 0 {
		t.Fatalf("the run's log misses one of the updates, the versions or the start of the worker: %+v", run.Events)
	}
	if !(marketplaceAt < pluginAt && pluginAt < versionsAt && versionsAt < workerAt) {
		t.Errorf("the run logged the marketplace at %d, the plugin at %d, the versions at %d and the worker at %d; want the updates, then the versions, then the worker",
			marketplaceAt, pluginAt, versionsAt, workerAt)
	}
}

func TestAFactoryStoppedWhileItUpdatesTheWorkerInterruptsTheRunRatherThanFailingIt(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	gh.installs(t, "0.9.3", "2.1.278 (Claude Code)")
	gh.updatesHang(t)

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	f := gh.work(t, config{"poll": "50ms", "deadline": "5m", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	f.eventually(t, 30*time.Second, "the update this run is waiting on", func() bool {
		return len(gh.pluginCalls(t)) > 0
	})

	// The update reaches over the host's line, so a stop lands in it. No worker was started, and the
	// record must not name one that failed.
	f.stop(t, syscall.SIGTERM)
	record := map[string]any{}
	read(t, filepath.Join(data, "run-1.json"), &record)
	if record["outcome"] != "interrupted" || record["state"] != "ended" {
		t.Errorf("the run stopped while it updated is recorded as %v/%v (%v), want ended/interrupted",
			record["state"], record["outcome"], record["reason"])
	}
	if workers := gh.workers(t); len(workers) != 0 {
		t.Errorf("the factory started %d workers, want none: the stop came before the session", len(workers))
	}
}

func TestAWorkerPluginThatIsSwitchedOffIsNoVersionTheRunRanWith(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	gh.installs(t, "0.9.3", "2.1.278 (Claude Code)")
	gh.switchedOff(t)

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	// A plugin that is installed but switched off is not the worker the session ran: naming its
	// version would put a worker on the record that this run never had.
	if run.Versions.Worker != "" {
		t.Errorf("the run records the worker plugin %q, want none: the install it was read from is switched off", run.Versions.Worker)
	}
	if run.Versions.ClaudeCode != "2.1.278" {
		t.Errorf("the run records Claude Code %q, want 2.1.278: one version that cannot be read is not the other", run.Versions.ClaudeCode)
	}
	if len(run.Warnings) != 1 || !strings.Contains(run.Warnings[0], workerPlugin) {
		t.Fatalf("the run carries the warnings %q, want one naming %s: a run nobody can trace to a worker says so", run.Warnings, workerPlugin)
	}
}

func TestAFailedUpdateIsAWarningAndTheRunGoesOnWithWhatIsInstalled(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)
	gh.installs(t, "0.9.3", "2.1.278 (Claude Code)")
	gh.updatesFail(t, "claude: the marketplace could not be fetched")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	run := f.ended(t, 1)

	// The update is not the run: a worker that cannot be brought up to date is an older worker, and
	// the issue is worked by it rather than given back.
	if run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready: a failed update must not fail the run; the factory's log:\n%s",
			run.Outcome, run.Reason, f.output(t))
	}
	if workers := gh.workers(t); len(workers) != 1 {
		t.Fatalf("the factory started %d workers, want the one the failed update did not stop", len(workers))
	}

	// One warning per command that did not work, each naming what it was and what claude said.
	if len(run.Warnings) != 2 {
		t.Fatalf("the run carries the warnings %q, want one for the marketplace and one for the plugin", run.Warnings)
	}
	for i, what := range []string{marketplace, workerPlugin} {
		if !strings.Contains(run.Warnings[i], what) || !strings.Contains(run.Warnings[i], "the marketplace could not be fetched") {
			t.Errorf("the warning %q says nothing of %s or of what claude said", run.Warnings[i], what)
		}
	}
	// And the run still says what it ran with: the state the host had installed all along.
	if run.Versions.Worker != "0.9.3" || run.Versions.ClaudeCode != "2.1.278" {
		t.Errorf("the run records the versions %+v, want the installed state it started with", run.Versions)
	}
	if calls, want := gh.pluginCalls(t), fmt.Sprintf("plugin update %s", workerPlugin); len(calls) < 2 || calls[1] != want {
		t.Errorf("the factory asked claude for %q, want the plugin update attempted after the marketplace failed", calls)
	}
}
