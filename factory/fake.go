package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Fake mode: a canned queue and a scripted worker, so the factory can be watched from start to end
// without tokens, git or GitHub. The scripted worker is this binary again (`scripted-worker`), which
// prints the stream a real headless worker printed, recorded on 2026-09-21 with Claude Code 2.1.278,
// and ends it with the structured result on the result line, as a session run with --json-schema
// printed it on 2026-09-23 with Claude Code 2.1.280.

// daemonLifetime is how long the processes of a scripted worker that outlive their factory live: the
// child of a hanging worker, which a factory that was killed leaves behind, and the process a detached
// worker leaves. It is far longer than a run of the tests may take, and short enough that the ones a
// test process that was killed leaves behind go away by themselves: they have no parent that is the
// test process, so its death does not end them.
const daemonLifetime = 2 * time.Minute

// cannedIssue is one entry of the canned queue with the scripted worker that works it: between them
// the entries cover every way a run ends here.
type cannedIssue struct {
	number   int
	title    string
	labels   []string
	routed   time.Duration // how long before the factory started the routing label was set
	repo     int           // which connected repository, counted from the first
	scenario string
}

// Declared out of the order they are worked in, so the queue's ordering rule is visible in what the
// HTTP interface serves: by the time the routing label was set, oldest first.
var cannedIssues = []cannedIssue{
	{number: 118, title: "Document the calibration procedure", labels: []string{"documentation"}, routed: 40 * time.Minute, repo: 1, scenario: "hang"},
	{number: 104, title: "Retry the upload when the broker drops the connection", labels: []string{"bug"}, routed: 6 * time.Hour, repo: 0, scenario: "ready"},
	{number: 112, title: "Replace the hand-written CSV parser", labels: []string{"enhancement"}, routed: 2 * time.Hour, repo: 0, scenario: "failed"},
	{number: 115, title: "Warn when a calibration file is older than the sensor", labels: []string{"enhancement"}, routed: 1 * time.Hour, repo: 0, scenario: "silent"},
	{number: 121, title: "Serve the dashboard preview from the device", labels: []string{"enhancement"}, routed: 50 * time.Minute, repo: 0, scenario: "detached"},
	{number: 109, title: "Überwachung: Füllstand fällt unter den Schwellwert, ohne dass eine Warnung kommt", labels: []string{"bug"}, routed: 4 * time.Hour, repo: 1, scenario: "blocked"},
}

// canned is the queue of fake mode: the same entries on every poll, so a run of the factory can be
// watched from start to end without tokens, git or GitHub.
type canned struct {
	repositories []Connected
	started      time.Time

	mu    sync.Mutex
	reads map[string]int // how often the ci stage has read each issue's pull request
}

// The issues fake mode holds are not asked about: there is no GitHub behind a canned queue, so
// nothing of it is ever let go and no scripted run is ever cancelled.
func (c *canned) queue(context.Context, []Held) poll {
	return poll{issues: cannedQueue(c.repositories, c.started)}
}

// changesRequested answers that nobody asked for changes. Fake mode opens no pull request — its
// scripted workers only say they did — so there is none to read a review of.
func (c *canned) changesRequested(context.Context, string, int) time.Time { return time.Time{} }

// cannedCI is what the ci stage reads of the pull request of a scenario, one reading after the other;
// the last one stands from then on. The ready worker's pull request waits on its checks, fails them
// and is green after the fix session; the detached worker's conflicts with its base first. Every other
// scenario's is green at once.
var cannedCI = map[string][]string{
	"ready":    {ciWaiting, ciFailed, ciGreen},
	"detached": {ciConflicts, ciGreen},
}

// pullState answers the canned reading of the scenario that works the issue, in the shape GitHub's is
// read in, so the verdict the dashboard shows is the one the real stage would make.
func (c *canned) pullState(_ context.Context, held Held, _ []string) (pullReading, error) {
	scenario := ""
	for _, issue := range cannedIssues {
		if issue.number == held.Number {
			scenario = issue.scenario
		}
	}
	c.mu.Lock()
	if c.reads == nil {
		c.reads = map[string]int{}
	}
	n := c.reads[held.key()]
	c.reads[held.key()] = n + 1
	c.mu.Unlock()
	script := cannedCI[scenario]
	state := ciGreen
	if len(script) > 0 {
		state = script[min(n, len(script)-1)]
	}
	gate := check{Name: "gate", URL: "https://github.com/" + held.Repository + "/actions/runs/1/job/1", State: checkPass,
		CompletedAt: c.started}
	read := pullReading{Mergeable: "MERGEABLE", Head: fmt.Sprintf("canned-%d", n), HeadAt: c.started, Bots: 1,
		Objections: []string{}, Threads: []string{}}
	switch state {
	case ciWaiting:
		gate.State, gate.CompletedAt = checkPending, time.Time{}
	case ciFailed:
		gate.State = checkFail
	case ciConflicts:
		read.Mergeable = "CONFLICTING"
	}
	read.Checks = []check{gate}
	return read, nil
}

// failedLogs is the log a failed gate prints.
func (c *canned) failedLogs(context.Context, string, []check) string {
	return "gate\tRun make check\t--- FAIL: TestCalibrationFileAge (0.02s)\n    calibration_test.go:41: want a warning, got none\nFAIL"
}

// openPull answers that no branch has a pull request open: fake mode opens none.
func (c *canned) openPull(context.Context, string, string) (string, error) { return "", nil }

// issueText answers the canned issue's title and a body of its own.
func (c *canned) issueText(_ context.Context, _ string, number int) (string, string, error) {
	for _, issue := range cannedIssues {
		if issue.number == number {
			return issue.title, "The canned issue #" + strconv.Itoa(number) + " of fake mode: " + issue.title + ".", nil
		}
	}
	return "", "", fmt.Errorf("fake mode has no issue #%d", number)
}

// createPull answers with the pull request a scripted worker used to name, and opens nothing.
func (c *canned) createPull(_ context.Context, repository string, p newPull) (string, error) {
	return fmt.Sprintf("https://github.com/%s/pull/%d", repository, p.issue+100), nil
}

// cannedChange is the change fake mode briefs its author with: it has no worktree to read one from.
func cannedChange(entry Entry) facts {
	return facts{span: "c0ffee0..f00d5ed", commits: fmt.Sprintf("f00d5ed feat: %s", strings.ToLower(entry.Title)),
		stat: " docs/change.md | 12 ++++++++++++\n 1 file changed, 12 insertions(+)", diff: "diff --git a/docs/change.md b/docs/change.md"}
}

// cannedMerge is the files a merge of the base conflicts in, for the scenario whose pull request
// conflicts with its base.
func cannedMerge(scenario string) []string {
	if scenario == "detached" {
		return []string{"docs/preview.md"}
	}
	return nil
}

// cannedQueue spreads the canned entries over the connected repositories, so the one line visibly
// mixes them, as a real queue across repositories does.
func cannedQueue(repositories []Connected, now time.Time) []Issue {
	queue := make([]Issue, 0, len(cannedIssues))
	for _, c := range cannedIssues {
		queue = append(queue, Issue{
			Repository: repositories[c.repo%len(repositories)].Name,
			Number:     c.number,
			Title:      c.title,
			Labels:     c.labels,
			RoutedAt:   now.Add(-c.routed),
			scenario:   c.scenario,
		})
	}
	return queue
}

// scriptedWorker stands in for `claude -p --output-format stream-json --verbose`. It is a subcommand
// of the factory's own binary, so fake mode needs nothing installed on the host.
// Usage: factory scripted-worker <ready|blocked|failed|silent|detached|fix|author|hang|child|daemon> <owner/name> <issue>
func scriptedWorker(args []string, stdout, stderr io.Writer) int {
	if len(args) < 3 {
		fmt.Fprintln(stderr, "error: usage: factory scripted-worker <ready|blocked|failed|silent|detached|fix|author|hang|child|daemon> <owner/name> <issue>")
		return 2
	}
	scenario, repository := args[0], args[1]
	issue, err := strconv.Atoi(args[2])
	if err != nil {
		fmt.Fprintf(stderr, "error: %q is not an issue number; write it as a number\n", args[2])
		return 2
	}
	s := &script{out: stdout, context: contextStart}
	if scenario == "author" {
		return scriptedAuthor(s, issue)
	}

	// The child of a hanging worker: it prints which process it is and then waits to be ended with
	// the process group, which is what proves that no worker process survives a deadline.
	if scenario == "child" {
		s.messages = 1 << 20 // its message is one of its own, not the worker's first one again
		s.say(fmt.Sprintf("worker child process %d", os.Getpid()))
		time.Sleep(daemonLifetime)
		return 0
	}

	// What a detached worker leaves behind: it says nothing and holds the worker's output open, the
	// way a server started with nohup does, for longer than any run here takes.
	if scenario == "daemon" {
		time.Sleep(daemonLifetime)
		return 0
	}

	s.hook("SessionStart:startup", "success")
	s.init()
	s.say(fmt.Sprintf("The hook loaded issue #%d of %s. I'll read the code the brief names before changing anything.", issue, repository))
	s.tool("Read", map[string]any{"file_path": "plugins/worker/skills/work/SKILL.md"}, "1  ---\n2  name: work")

	switch scenario {
	case "fix":
		// The fix session of a repair round: it reads what its brief names, fixes it and pushes.
		s.say("The brief names what failed. Fixing that and nothing else.")
		s.tool("Edit", map[string]any{"file_path": "calibration/age.go"}, "The file has been updated.")
		s.tool("Bash", map[string]any{"command": "git commit -am 'fix: warn on an old calibration file' && git push", "description": "Commit and push the fix"},
			"To github.com:acme/edge-sensors.git")
		s.result("success", "", false, "completed", map[string]any{"outcome": resultComplete, "summary": "Fixed and pushed."})
		return 0
	case "hang":
		s.thinkAndSay("The calibration procedure is spread over three files.", fmt.Sprintf("worker process %d", os.Getpid()))
		// A subagent on a model the factory has no price for: its tokens count, its cost cannot.
		s.emit(map[string]any{"type": "assistant", "parent_tool_use_id": "toolu_scout", "message": map[string]any{
			"id": "msg_scout", "model": unpricedModel, "usage": s.usage(true),
			"content": []map[string]any{{"type": "text", "text": "Three files name the procedure."}}}})
		child := exec.Command(os.Args[0], "scripted-worker", "child", repository, strconv.Itoa(issue))
		child.Stdout, child.Stderr = stdout, stderr
		if err := child.Start(); err != nil {
			fmt.Fprintf(stderr, "error: the scripted worker could not start its child: %v\n", err)
			return 1
		}
		// The stream keeps coming while the run hangs, until the deadline ends the process group.
		// A reader that follows a running log is read against this one, so no two lines are alike.
		for beat := 1; ; beat++ {
			time.Sleep(time.Second)
			s.say(fmt.Sprintf("Still working on it, minute %d.", beat))
		}
	case "failed":
		s.say("Reproducing the behaviour end to end first.")
		fmt.Fprintln(stderr, `API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
		s.result("error_during_execution", "", true, "api_error", nil)
		return 1
	case "silent":
		// A session that ends by itself with a result that carries no structured output. The error
		// output of this one is on the worker's own stream — a tool that failed and a line that is not
		// the stream format at all — which the factory logs without it changing how the run ended.
		s.failingTool("Bash", map[string]any{"command": "make check", "description": "Run the gate"}, "make: *** [check] Error 1")
		fmt.Fprintln(stdout, "npm warn: a line of the worker's output that is not the stream format")
		// Lines that are the stream format all the same: a tool call the auto mode classifier denied,
		// a system line the factory has no use for, and one of a subtype it does not know.
		s.emit(map[string]any{"type": "system", "subtype": "permission_denied", "tool_name": "Bash", "tool_use_id": "toolu_denied",
			"decision_reason_type": "classifier", "decision_reason": "[Untrusted Code Integration]",
			"message": "Permission for this action was denied by the Claude Code auto mode classifier."})
		s.emit(map[string]any{"type": "system", "subtype": "hook_started", "hook_name": "PreToolUse:Bash"})
		s.emit(map[string]any{"type": "system", "subtype": "sensor_calibrated", "detail": "a subtype of a later Claude Code"})
		// Its tool call carries a file far beyond what one event keeps: the log has to survive that,
		// here and after a restart.
		s.tool("Write", map[string]any{"file_path": "docs/report.html", "content": strings.Repeat(`<a href="x">&amp;</a>`, 1000)},
			"File created successfully.")
		s.result("success", "I have pushed the branch and stopped here.", false, "completed", nil)
		return 0
	}

	// A worker that reports ready and leaves a process behind that the process group does not reach:
	// it has a session of its own and still holds the worker's output. The run has to end all the same.
	if scenario == "detached" {
		daemon := exec.Command(os.Args[0], "scripted-worker", "daemon", repository, strconv.Itoa(issue))
		daemon.Stdout, daemon.Stderr = stdout, stderr
		daemon.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := daemon.Start(); err != nil {
			fmt.Fprintf(stderr, "error: the scripted worker could not start its daemon: %v\n", err)
			return 1
		}
		s.say(fmt.Sprintf("worker detached process %d", daemon.Process.Pid))
	}

	s.tool("Edit", map[string]any{"file_path": "plugins/worker/skills/work/SKILL.md"}, "The file has been updated.")
	s.tool("Bash", map[string]any{"command": "plugins/worker/scripts/gate.sh run", "description": "Run the gate and record it"}, "gate: pass")
	s.say("The gate is green. Starting the reviewer panel.")
	s.skill("worker:review")
	for _, reviewer := range []string{"code", "security", "docs", "tests", "senior"} {
		s.subagent("worker:"+reviewer+"-reviewer", "Review the branch diff", "verdict: PASS\nfindings: 0")
	}

	if scenario == "blocked" {
		s.say("The senior reviewer is right: the brief contradicts ADR 0012.")
		summary := fmt.Sprintf("the brief asks the worker to tag the release itself, and ADR 0012 keeps releases manual.\n\n"+
			"decision needed: drop the tagging step from issue #%d, or supersede ADR 0012.", issue)
		s.result("success", "", false, "completed", map[string]any{"outcome": resultBlocked, "summary": summary})
		return 0
	}

	// The review's checkpoint hands the rest of the stage to a fresh context, so what the worker carries
	// drops back to a loaded session: the peak of this run stands before the handover, not at its end.
	s.compact()
	s.say("The panel summary is recorded. Stopping after the review.")
	// The session stops after the review (WF_STOP_AFTER=review), and the factory opens the pull request.
	// The detached worker's panel ends with the tests reviewer on FIX, which the pull request says.
	panel := "review_rounds: 1\npanel: code=PASS security=PASS docs=PASS tests=PASS senior=PASS\nfixed: 0 (S1 0, S2 0, S3 0)\ndisputed: none"
	if scenario == "detached" {
		panel = "review_rounds: 3\npanel: code=PASS security=PASS docs=PASS tests=FIX→FIX→FIX senior=PASS\nfixed: 4 (S1 0, S2 3, S3 1)\n" +
			"disputed: tests S2 docs/preview.md:12 a browser test of the preview; the device has no browser to run one in"
	}
	gate := "gate_result: pass (exit 0) at f00d5ed"
	s.tool("Bash", map[string]any{"command": "plugins/worker/scripts/stop.sh", "description": "Report the stage the session stops after"},
		"ready: stopped after review\npanel_summary_block:\n"+panel+"\n"+gate)
	s.result("success", "", false, "completed", map[string]any{"outcome": resultComplete, "panelSummary": panel, "gateResult": gate,
		"summary": "stopped after review"})
	return 0
}

// scriptedAuthor is the author session of the pr stage: it reads the change and reports the title and
// the body of the pull request, and nothing else, because it can do nothing else.
func scriptedAuthor(s *script, issue int) int {
	s.init()
	s.say("The brief carries the diff and the issue. Reading the changed file before writing the description.")
	s.tool("Read", map[string]any{"file_path": "docs/change.md"}, "1  # The change")
	s.result("success", "", false, "completed", map[string]any{
		"title": "feat: the change the canned issue asks for",
		"body":  fmt.Sprintf("Closes #%d\n\n## What changed\n\nThe canned change of fake mode, in one file.\n\n## Known limits\n\nNone known.", issue)})
	return 0
}

// script writes the stream of a worker session, one JSON object per line.
type script struct {
	out      io.Writer
	tools    int
	messages int
	context  int // what the next message of the worker itself starts from
}

// scriptedModel is the model the messages of the scripted worker name; unpricedModel is one the
// factory has no price for, which a subagent of the hanging worker runs on.
const (
	scriptedModel = "claude-opus-5"
	unpricedModel = "claude-unreleased-9"
)

// thinkAndSay is one message of the worker written as two lines, the way Claude Code prints a
// message of two content blocks: both carry its id, and the first its usage from before the text was
// written. A factory that counted lines rather than messages would count two turns and too much.
func (s *script) thinkAndSay(thought, text string) {
	s.messages++
	id, u := fmt.Sprintf("msg_%d", s.messages), s.usage(false)
	start := map[string]any{}
	for k, v := range u {
		start[k] = v
	}
	start["output_tokens"] = 1
	for _, line := range []struct {
		usage map[string]any
		block map[string]any
	}{
		{start, map[string]any{"type": "thinking", "thinking": thought}},
		{u, map[string]any{"type": "text", "text": text}},
	} {
		s.emit(map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{
			"id": id, "model": scriptedModel, "content": []map[string]any{line.block}, "usage": line.usage}})
	}
}

// The context of the scripted worker: it starts at a loaded session and grows with every message the
// worker writes, the way a real one does. A subagent's is far above every peak a scripted run
// reaches, so a peak taken from a subagent's line instead of the worker's could not be missed.
const (
	contextStart    = 22_000
	contextStep     = 5_800
	subagentContext = 900_000
)

func (s *script) emit(line map[string]any) {
	raw, err := json.Marshal(line)
	if err != nil {
		return
	}
	fmt.Fprintln(s.out, string(raw))
}

func (s *script) hook(name, outcome string) {
	s.emit(map[string]any{"type": "system", "subtype": "hook_response", "hook_name": name, "outcome": outcome})
}

func (s *script) init() {
	s.emit(map[string]any{"type": "system", "subtype": "init", "model": scriptedModel,
		"permissionMode": "auto", "session_id": "f7ca4f15-d7f2-4168-ac7c-bc91256d5117"})
}

// message is one assistant or user line. parent is the tool-use id of the Agent call a subagent runs
// under, and empty for the worker itself.
func (s *script) message(kind, parent string, content []map[string]any) {
	message := map[string]any{"content": content}
	if kind == "assistant" {
		s.messages++
		message["id"], message["model"] = fmt.Sprintf("msg_%d", s.messages), scriptedModel
		message["usage"] = s.usage(parent != "")
	}
	line := map[string]any{"type": kind, "parent_tool_use_id": nil, "message": message}
	if parent != "" {
		line["parent_tool_use_id"] = parent
	}
	s.emit(line)
}

// usage is the context one assistant message started from, as the stream carries it.
func (s *script) usage(sub bool) map[string]any {
	read := subagentContext
	if !sub {
		s.context += contextStep
		read = s.context
	}
	return map[string]any{"input_tokens": 400, "cache_creation_input_tokens": 1200,
		"cache_read_input_tokens": read, "output_tokens": 250,
		"cache_creation": map[string]any{"ephemeral_5m_input_tokens": 400, "ephemeral_1h_input_tokens": 800}}
}

// compact is what a handoff does to the worker's context: it starts from a loaded session again,
// and every message after it carries less than the run already reached.
func (s *script) compact() {
	s.context = contextStart
}

func (s *script) say(text string) {
	s.message("assistant", "", []map[string]any{{"type": "text", "text": text}})
}

// call is one tool call with its result, as the worker's own or as a subagent's.
func (s *script) call(parent, name string, input map[string]any, result string) {
	s.tools++
	id := fmt.Sprintf("toolu_%d", s.tools)
	s.message("assistant", parent, []map[string]any{{"type": "tool_use", "id": id, "name": name, "input": input}})
	s.message("user", parent, []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": result, "is_error": false}})
}

func (s *script) tool(name string, input map[string]any, result string) {
	s.call("", name, input, result)
}

// failingTool is a tool call whose result is an error, which the factory logs as one.
func (s *script) failingTool(name string, input map[string]any, result string) {
	s.tools++
	id := fmt.Sprintf("toolu_%d", s.tools)
	s.message("assistant", "", []map[string]any{{"type": "tool_use", "id": id, "name": name, "input": input}})
	s.message("user", "", []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": result, "is_error": true}})
}

// skill is how a worker enters a stage: the factory reads the stage from this call.
func (s *script) skill(name string) {
	s.call("", "Skill", map[string]any{"skill": name}, "Launching skill: "+name)
}

// subagent is an Agent call and the work it does: its events carry the tool-use id of the call, which
// is how the factory recognises them as a subagent's.
func (s *script) subagent(kind, description, report string) {
	s.tools++
	id := fmt.Sprintf("toolu_%d", s.tools)
	s.message("assistant", "", []map[string]any{{"type": "tool_use", "id": id, "name": "Agent",
		"input": map[string]any{"subagent_type": kind, "description": description}}})
	s.call(id, "Bash", map[string]any{"command": "git diff origin/main...HEAD", "description": "Read the diff under review"},
		"diff --git a/plugins/worker/skills/work/SKILL.md")
	s.message("user", "", []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": report, "is_error": false}})
}

// result is the last line of a session: the only line with the totals of the whole run, and the one
// that carries the structured result. A session run with a schema prints that result as its result
// text as well, so the text is the output's JSON when the report is empty; nil leaves the structured
// output out, as a session without a schema, or one that ended in an error, leaves it out.
func (s *script) result(subtype, report string, isError bool, terminalReason string, output map[string]any) {
	line := map[string]any{
		"type": "result", "subtype": subtype, "is_error": isError, "terminal_reason": terminalReason,
		"num_turns": 23, "total_cost_usd": 4.18, "result": report,
		"usage": map[string]any{"input_tokens": 1240, "cache_creation_input_tokens": 98300,
			"cache_read_input_tokens": 1204000, "output_tokens": 24800},
	}
	if output != nil {
		line["structured_output"] = output
		if report == "" {
			raw, _ := json.Marshal(output)
			line["result"] = string(raw)
		}
	}
	s.emit(line)
}
