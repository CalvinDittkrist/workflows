package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The quota check, tested the way the rest of the factory is: the real binary, claiming a real issue
// against the gh shim, with the scripted quota-axi of testdata as the tool the configuration names.
// What the check decided is read from the interface — whether the factory waits and until when —
// from when the runs started, and from the calls the scripted tool logged.

// quotaShim is the scripted quota-axi: the plan it answers from, one line per call, and the log of
// the calls the factory made of it.
type quotaShim struct {
	path string
	log  string
	env  []string
}

func newQuotaShim(t *testing.T, plan ...string) *quotaShim {
	t.Helper()
	dir := t.TempDir()
	q := &quotaShim{path: abs(t, filepath.Join("testdata", "quota-axi")), log: filepath.Join(dir, "calls.log")}
	writeFile(t, filepath.Join(dir, "plan"), strings.Join(plan, "\n")+"\n")
	q.env = []string{"QUOTA_SHIM_PLAN=" + filepath.Join(dir, "plan"), "QUOTA_SHIM_LOG=" + q.log,
		"QUOTA_SHIM_COUNT=" + filepath.Join(dir, "count")}
	return q
}

// quotaCall is one call the factory made of the scripted tool: when, to the second, and with what.
type quotaCall struct {
	at   time.Time
	args string
}

func (q *quotaShim) calls(t *testing.T) []quotaCall {
	t.Helper()
	raw, err := os.ReadFile(q.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	out := []quotaCall{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		epoch, args, _ := strings.Cut(line, " ")
		seconds, err := strconv.ParseInt(epoch, 10, 64)
		if err != nil {
			t.Fatalf("the quota shim logged %q, which does not start with the time of the call", line)
		}
		out = append(out, quotaCall{at: time.Unix(seconds, 0), args: args})
	}
	return out
}

// claimsWithQuota is the fixture of every test below: one issue routed to the factory, which claims
// it and runs the claude shim's worker on it, with the quota check on and answered from the plan.
func claimsWithQuota(t *testing.T, q *quotaShim, c config, env ...string) (*factory, *ghShim) {
	t.Helper()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	c["poll"], c["deadline"], c["data_dir"], c["repositories"] = "50ms", "90s", data, []string{"acme/edge-sensors"}
	if _, named := c["quota_axi"]; !named {
		c["quota_axi"] = q.path
	}
	return launch(t, c, append(append(gh.env, q.env...), env...)), gh
}

type apiStatus struct {
	State      string     `json:"state"`
	QuotaUntil *time.Time `json:"quotaUntil"`
}

// waitsForQuota waits until the factory says it waits for quota, and answers until when.
func (f *factory) waitsForQuota(t *testing.T) time.Time {
	t.Helper()
	var status apiStatus
	f.eventually(t, 30*time.Second, "the factory to wait for quota", func() bool {
		status = apiStatus{}
		f.get(t, "/api/status", &status)
		return status.State == "waiting-for-quota"
	})
	if status.QuotaUntil == nil {
		t.Fatalf("the factory waits for quota and does not say until when; the factory's log:\n%s", f.output(t))
	}
	return *status.QuotaUntil
}

// The scope the check reads is the one of the model the worker runs on, and without a --model in
// worker_args that is the worker agent's own. The factory restates that model, so this holds it to
// the agent's frontmatter: a worker moved to another model would otherwise be checked against the
// quota of one it no longer spends.
func TestTheWorkerModelAgreesWithTheWorkerAgent(t *testing.T) {
	t.Parallel()
	agent := readFile(t, abs(t, filepath.Join("..", "plugins", "worker", "agents", "worker.md")))
	frontmatter, _, _ := strings.Cut(strings.TrimPrefix(agent, "---\n"), "\n---")
	found := regexp.MustCompile(`(?m)^model:\s*(\S+)\s*$`).FindStringSubmatch(frontmatter)
	if found == nil {
		t.Fatal("the worker agent names no model in its frontmatter; the factory's quota check reads the scope of that model")
	}
	if found[1] != workerModel {
		t.Errorf("the factory checks the quota of %s for a worker whose agent runs on %s", workerModel, found[1])
	}
}

// Enough quota left: the run starts, as if there were no check at all. What counts is the smaller of
// the all-models scope and the scope of the worker's model; a scope of another model that is nearly
// used up is none of this run's business.
func TestARunStartsWhenEnoughQuotaIsLeft(t *testing.T) {
	t.Parallel()
	q := newQuotaShim(t, "all=80 opus=40 sonnet=3 reset=+3600")
	f, _ := claimsWithQuota(t, q, config{})
	run := f.ended(t, 1)
	if run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if len(run.Warnings) != 0 {
		t.Errorf("the run carries the warnings %q, want none: the check answered", run.Warnings)
	}
	calls := q.calls(t)
	if len(calls) != 1 {
		t.Fatalf("the factory asked quota-axi %d times, want once: before the one run", len(calls))
	}
	// The report of the Claude provider, as JSON, and nothing that renews a credential on the way.
	if want := "--provider claude --json --no-credential-refresh"; calls[0].args != want {
		t.Errorf("the factory ran quota-axi %s, want %s", calls[0].args, want)
	}
	var status apiStatus
	f.get(t, "/api/status", &status)
	if status.State != "running" || status.QuotaUntil != nil {
		t.Errorf("the factory says %q until %v, want running and no quota wait", status.State, status.QuotaUntil)
	}
}

// Too little left: nothing starts, the interface says the factory waits for quota and until when,
// and after that reset the check runs again and the run starts. The worker runs on the model
// worker_args names, so that model's scope is the one read — here the one that is short.
func TestTooLittleQuotaWaitsForTheResetAndStartsAfterIt(t *testing.T) {
	t.Parallel()
	q := newQuotaShim(t, "all=80 opus=90 sonnet=11 reset=+3", "all=80 opus=90 sonnet=60 reset=+3600")
	f, gh := claimsWithQuota(t, q, config{"worker_args": []string{"--model", "claude-sonnet-4-5"}})
	until := f.waitsForQuota(t)
	if !f.missing(t, 1) {
		t.Fatalf("a run started while the factory waits for quota; the factory's log:\n%s", f.output(t))
	}
	// The line keeps the issue while the factory waits: nothing of it is claimed or recorded.
	if made := gh.asked(t, "api --method POST repos/acme/edge-sensors/git/refs"); made != 0 {
		t.Errorf("the factory created a reference %d times while it waited, want never", made)
	}
	var line apiLine
	f.get(t, "/api/line", &line)
	if len(line.Queue) != 1 || line.Queue[0].Number != claimedIssue {
		t.Errorf("the factory queues %v while it waits, want #%d still in the line", keys(line.Queue), claimedIssue)
	}

	run := f.ended(t, 1)
	if run.Outcome != "ready" {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if run.StartedAt.Before(until) {
		t.Errorf("the run started at %s, before the reset at %s the factory said it waits for", run.StartedAt, until)
	}
	// The second check is what let the run start, and it came after the reset rather than before it.
	calls := q.calls(t)
	if len(calls) != 2 {
		t.Fatalf("the factory asked quota-axi %d times, want twice: once before the reset and once after it", len(calls))
	}
	if calls[1].at.Before(until.Truncate(time.Second)) {
		t.Errorf("the factory checked again at %s, before the reset at %s", calls[1].at, until)
	}
	// And it was the scope of the model the worker runs on that held it back, not opus's.
	if !strings.Contains(f.output(t), "11 % of model:sonnet is left") {
		t.Errorf("the factory's log does not say which scope it waited for; want model:sonnet with 11 %%:\n%s", f.output(t))
	}
	var status apiStatus
	f.get(t, "/api/status", &status)
	if status.State != "running" || status.QuotaUntil != nil {
		t.Errorf("after the reset the factory says %q until %v, want running and no quota wait", status.State, status.QuotaUntil)
	}
}

// A wait is over at its reset even when nothing checks again: an issue that left the line while the
// factory waited leaves nothing to check for, and the interface still says the factory runs again
// rather than showing a wait for a moment that has passed.
func TestAQuotaWaitEndsAtTheResetWhenTheLineIsEmpty(t *testing.T) {
	t.Parallel()
	q := newQuotaShim(t, "all=80 opus=5 reset=+3")
	f, gh := claimsWithQuota(t, q, config{})
	until := f.waitsForQuota(t)
	gh.issues(t, "acme/edge-sensors") // the routing label came off while the factory waited
	var status apiStatus
	f.eventually(t, 30*time.Second, "the factory to stop waiting for quota after the reset", func() bool {
		status = apiStatus{}
		f.get(t, "/api/status", &status)
		return status.State == "running" && status.QuotaUntil == nil
	})
	if time.Now().Before(until) {
		t.Errorf("the factory stopped waiting before the reset at %s", until)
	}
	if !f.missing(t, 1) {
		t.Errorf("a run started for an issue that left the line; the factory's log:\n%s", f.output(t))
	}
	if calls := q.calls(t); len(calls) != 1 {
		t.Errorf("the factory asked quota-axi %d times, want once: an empty line has nothing to check for", len(calls))
	}
}

// A check that cannot answer is no reason to stop: the run starts, and it says it started without
// knowing how much was left ([ADR 0028]).
//
// [ADR 0028]: ../docs/adr/0028-the-quota-check-is-a-courtesy-not-a-guard.md
func TestARunStartsWithAWarningWhenTheQuotaCheckFails(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, plan, path, said string
	}{
		{"the tool exits with an error", "fail", "", "no Claude credentials found"},
		{"the tool prints something that is not its report", "garbage", "", "not its JSON report"},
		{"the tool is not installed", "all=80 opus=80 reset=+3600", "/nonexistent/quota-axi", "/nonexistent/quota-axi"},
		{"the scope that is short names no reset", "all=80 opus=5 reset=none", "", "names no reset time"},
		// A stale reading is no reading: a short scope in quota-axi's old cache holds nothing back.
		{"the reading is stale", "all=80 opus=5 reset=+3600 stale", "", "stale"},
	} {
		t.Run(c.name, func(t *testing.T) {
			q := newQuotaShim(t, c.plan)
			settings := config{}
			if c.path != "" {
				settings["quota_axi"] = c.path
			}
			f, _ := claimsWithQuota(t, q, settings)
			run := f.ended(t, 1)
			if run.Outcome != "ready" {
				t.Fatalf("the run ended as %q (%s), want ready: a failed check starts the run; the factory's log:\n%s",
					run.Outcome, run.Reason, f.output(t))
			}
			if len(run.Warnings) != 1 || !strings.Contains(run.Warnings[0], "quota check failed") || !strings.Contains(run.Warnings[0], c.said) {
				t.Errorf("the run carries the warnings %q, want one that says the quota check failed and why (%q)", run.Warnings, c.said)
			}
		})
	}
}

// A run that ends in an error while the worker's quota is used up has not failed: it ran out. The
// branch, the worktree and the assignee stay, and after the reset the factory resumes the issue in
// that worktree by itself — the resume is the quota's, so the one after an interruption is still
// there (TestTheAutomaticResumeIsOnePerIssueAndOnlyAReleaseGivesItBack).
func TestARunThatRanOutOfQuotaIsResumedAfterTheReset(t *testing.T) {
	t.Parallel()
	q := newQuotaShim(t, "all=80 opus=60 reset=+3600", "all=0 opus=0 reset=+3", "all=100 opus=100 reset=+3600")
	f, gh := claimsWithQuota(t, q, config{}, "CLAUDE_SHIM_FAIL=API Error: Claude usage limit reached")
	first := f.ended(t, 1)
	if first.Outcome != "quota" {
		t.Fatalf("the run ended as %q (%s), want quota; the factory's log:\n%s", first.Outcome, first.Reason, f.output(t))
	}
	if !first.Holding || first.Branch != claimedBranch || first.Worktree == "" {
		t.Errorf("the run holds %s in %q (holding=%v), want the claim's branch and worktree held", first.Branch, first.Worktree, first.Holding)
	}
	if !strings.Contains(first.Reason, "usage limit reached") || !strings.Contains(first.Reason, "exhausted") {
		t.Errorf("the run says %q, want the worker's error and that the quota is exhausted", first.Reason)
	}
	if _, err := os.Stat(first.Worktree); err != nil {
		t.Errorf("the worktree %s is gone after the run ran out of quota: %v", first.Worktree, err)
	}
	if head := gh.head(t, "acme/edge-sensors", claimedBranch); head == "" {
		t.Errorf("%s is not on the remote; a run that ran out of quota leaves its branch", claimedBranch)
	}
	until := f.waitsForQuota(t)

	second := f.ended(t, 2)
	if second.Signal != "quota" || second.Issue != claimedIssue {
		t.Fatalf("run 2 works #%d on the signal %q, want #%d resumed after the quota reset; the factory's log:\n%s",
			second.Issue, second.Signal, claimedIssue, f.output(t))
	}
	if second.StartedAt.Before(until) {
		t.Errorf("the resumed run started at %s, before the reset at %s", second.StartedAt, until)
	}
	if second.Branch != first.Branch || second.Worktree != first.Worktree || !second.Holding {
		t.Errorf("the resumed run is %s in %s (holding=%v), want the claim's %s in %s",
			second.Branch, second.Worktree, second.Holding, first.Branch, first.Worktree)
	}
	// The resumed run is under the claim that stands: no second branch and no second assignment.
	if made := gh.asked(t, "api --method POST repos/acme/edge-sensors/git/refs"); made != 1 {
		t.Errorf("the factory created a reference %d times, want once", made)
	}
	assigned := fmt.Sprintf("issue edit %d --repo acme/edge-sensors --add-assignee factory-bot", claimedIssue)
	if made := gh.made(t, assigned); made != 1 {
		t.Errorf("the factory made `gh %s` %d times, want once: the assignee stayed", assigned, made)
	}
	// Nobody is told anything: the factory comments on no issue for a run that ran out of quota.
	for _, call := range gh.calls(t) {
		if regexp.MustCompile(`\bcomment\b|/comments`).MatchString(call) {
			t.Errorf("the factory made `%s`; a run that ran out of quota notifies nobody", call)
		}
	}
	// The resumed session ends in the same error, and this time the quota is there: it has failed.
	if second.Outcome != "failed" {
		t.Errorf("the resumed run ended as %q (%s), want failed: its error came with quota left", second.Outcome, second.Reason)
	}
}
