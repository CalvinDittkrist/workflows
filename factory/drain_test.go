package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// processLine is what /api/line says of the running process beside the line itself.
type processLine struct {
	Version    string `json:"version"`
	Draining   bool   `json:"draining"`
	AutoUpdate bool   `json:"auto_update"`
}

// exited waits until the factory is gone without signalling it, and answers with its exit code.
func (f *factory) exited(t *testing.T, within time.Duration) int {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f.cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatalf("the factory could not be waited for: %v", err)
		}
		return f.cmd.ProcessState.ExitCode()
	case <-time.After(within):
		t.Fatalf("the factory did not exit within %v; its log:\n%s", within, f.output(t))
		return 0
	}
}

// A SIGHUP lets the run that is going end with its own outcome, starts nothing after it while issues
// stand in the line, and then exits with the drain code. A second SIGHUP changes nothing and is said
// once.
func TestADrainLetsTheRunFinishStartsNothingAndExitsWithTheDrainCode(t *testing.T) {
	t.Parallel()
	// The hanging issue was interrupted once before, so it is resumed first and the rest of the canned
	// queue stands behind it. Its deadline is what ends it here: that is its own ending.
	data := filepath.Join(t.TempDir(), "data")
	began := time.Now().UTC().Add(-time.Hour)
	records(t, data, in("acme/backtest", record(1, hangingIssue(t), "Document the calibration procedure",
		signalRouted, outcomeInterrupted, true, began, began.Add(time.Minute))))
	f := start(t, config{"deadline": "10s", "poll": "50ms", "data_dir": data})
	const going = 2
	f.waitForTheHangingWorker(t, going)

	for range 3 {
		if err := f.cmd.Process.Signal(syscall.SIGHUP); err != nil {
			t.Fatalf("the factory could not be signalled: %v", err)
		}
	}
	f.eventually(t, 5*time.Second, "the line to say the factory drains", func() bool {
		var process processLine
		f.get(t, "/api/line", &process)
		return process.Draining
	})
	var run apiRun
	f.get(t, fmt.Sprintf("/api/runs/%d", going), &run)
	if run.State != "running" {
		t.Fatalf("run %d is %q once the drain began, want still running: the deadline ends it, not the drain", going, run.State)
	}

	if code := f.exited(t, 60*time.Second); code != drainExit {
		t.Errorf("the drained factory exited with %d, want %d; its log:\n%s", code, drainExit, f.output(t))
	}
	var ended Run
	read(t, filepath.Join(data, fmt.Sprintf("run-%d.json", going)), &ended)
	if ended.Outcome != outcomeTimeout {
		t.Errorf("the run that was going ended as %q, want %q: a drain interrupts nothing", ended.Outcome, outcomeTimeout)
	}
	if _, err := os.Stat(filepath.Join(data, fmt.Sprintf("run-%d.json", going+1))); err == nil {
		t.Errorf("run %d was started while the factory drained", going+1)
	}
	if said := strings.Count(f.output(t), "SIGHUP again"); said != 1 {
		t.Errorf("the log names the repeated SIGHUP %d times, want once; the log:\n%s", said, f.output(t))
	}
}

// A factory with no run going drains at once. The line carries the version of the running process
// and the auto_update its configuration says now, read again without a restart.
func TestAnIdleFactoryDrainsAtOnceAndTheLineCarriesVersionAndAutoUpdate(t *testing.T) {
	t.Parallel()
	f := start(t, config{"paused": true, "poll": "50ms"})
	var process processLine
	f.get(t, "/api/line", &process)
	if process.Version != version || process.Draining || process.AutoUpdate {
		t.Errorf("the line says %+v, want version %q, not draining and auto_update off", process, version)
	}

	f.configure(t, config{"auto_update": true})
	f.eventually(t, 5*time.Second, "auto_update to be read from the configuration", func() bool {
		process = processLine{}
		f.get(t, "/api/line", &process)
		return process.AutoUpdate
	})

	if err := f.cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("the factory could not be signalled: %v", err)
	}
	if code := f.exited(t, 10*time.Second); code != drainExit {
		t.Errorf("the idle factory exited with %d on SIGHUP, want %d; its log:\n%s", code, drainExit, f.output(t))
	}
}
