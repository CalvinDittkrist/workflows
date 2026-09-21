package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Fake mode: a canned queue and a scripted worker, so the factory can be watched from start to end
// without tokens, git or GitHub. The scripted worker is this binary again (`scripted-worker`), which
// prints the stream a real headless worker printed, recorded on 2026-09-21 with Claude Code 2.1.278.

// daemonLifetime is how long the process a detached scripted worker leaves behind lives: far longer
// than its run may take, and short enough that the ones the tests leave behind go away by themselves.
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

// cannedQueue spreads the canned entries over the connected repositories, so the one line visibly
// mixes them, as a real queue across repositories does.
func cannedQueue(repositories []string, now time.Time) []Issue {
	queue := make([]Issue, 0, len(cannedIssues))
	for _, c := range cannedIssues {
		queue = append(queue, Issue{
			Repository: repositories[c.repo%len(repositories)],
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
// Usage: factory scripted-worker <ready|blocked|failed|silent|detached|hang|child|daemon> <owner/name> <issue>
func scriptedWorker(args []string, stdout, stderr io.Writer) int {
	if len(args) < 3 {
		fmt.Fprintln(stderr, "error: usage: factory scripted-worker <ready|blocked|failed|silent|detached|hang|child|daemon> <owner/name> <issue>")
		return 2
	}
	scenario, repository := args[0], args[1]
	issue, err := strconv.Atoi(args[2])
	if err != nil {
		fmt.Fprintf(stderr, "error: %q is not an issue number; write it as a number\n", args[2])
		return 2
	}
	s := &script{out: stdout, context: contextStart}

	// The child of a hanging worker: it prints which process it is and then waits to be ended with
	// the process group, which is what proves that no worker process survives a deadline.
	if scenario == "child" {
		s.say(fmt.Sprintf("worker child process %d", os.Getpid()))
		time.Sleep(time.Hour)
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
	case "hang":
		s.say(fmt.Sprintf("worker process %d", os.Getpid()))
		child := exec.Command(os.Args[0], "scripted-worker", "child", repository, strconv.Itoa(issue))
		child.Stdout, child.Stderr = stdout, stderr
		if err := child.Start(); err != nil {
			fmt.Fprintf(stderr, "error: the scripted worker could not start its child: %v\n", err)
			return 1
		}
		time.Sleep(time.Hour) // until the deadline ends the process group
		return 0
	case "failed":
		s.say("Reproducing the behaviour end to end first.")
		fmt.Fprintln(stderr, `API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
		s.result("error_during_execution", "", true, "api_error")
		return 1
	case "silent":
		// A session that ends by itself without reporting. The error output of this one is on the
		// worker's own stream — a tool that failed and a line that is not the stream format at all —
		// which the factory logs without it changing how the run ended.
		s.failingTool("Bash", map[string]any{"command": "make check", "description": "Run the gate"}, "make: *** [check] Error 1")
		fmt.Fprintln(stdout, "npm warn: a line of the worker's output that is not the stream format")
		// Its tool call carries a file far beyond what one event keeps: the log has to survive that,
		// here and after a restart.
		s.tool("Write", map[string]any{"file_path": "docs/report.html", "content": strings.Repeat(`<a href="x">&amp;</a>`, 1000)},
			"File created successfully.")
		s.result("success", "I have pushed the branch and stopped here.", false, "completed")
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
		s.result("success", fmt.Sprintf(
			"**blocked: the brief asks the worker to tag the release itself, and ADR 0012 keeps releases manual.**\n\n"+
				"decision needed: drop the tagging step from issue #%d, or supersede ADR 0012.", issue), false, "completed")
		return 0
	}

	s.say("Panel: five PASS. Handing the pull request to a fresh context.")
	s.skill("worker:pr")
	pullRequest := fmt.Sprintf("https://github.com/%s/pull/%d", repository, issue+100)
	s.subagent("worker:pr-author", "Open the pull request", pullRequest)
	s.skill("worker:ci")
	s.tool("Bash", map[string]any{"command": "plugins/worker/scripts/pr-wait.sh", "description": "Wait for checks and bot reviewers"},
		"checks: pass\nreviewers: codex commented\nthreads: 1 unresolved")
	s.skill("worker:address-reviews")
	s.tool("Bash", map[string]any{"command": "plugins/worker/scripts/pr-resolve.sh 1", "description": "Reply to and resolve the review thread"}, "resolved: 1")
	s.result("success", fmt.Sprintf("**ready: %s**\n\nReview: 5/5 PASS after one round. CI green. One Codex thread fixed and resolved.", pullRequest), false, "completed")
	return 0
}

// script writes the stream of a worker session, one JSON object per line.
type script struct {
	out     io.Writer
	tools   int
	context int // what the next message of the worker itself starts from
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
	s.emit(map[string]any{"type": "system", "subtype": "init", "model": "claude-opus-5",
		"permissionMode": "auto", "session_id": "f7ca4f15-d7f2-4168-ac7c-bc91256d5117"})
}

// message is one assistant or user line. parent is the tool-use id of the Agent call a subagent runs
// under, and empty for the worker itself.
func (s *script) message(kind, parent string, content []map[string]any) {
	message := map[string]any{"content": content}
	if kind == "assistant" {
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
		"cache_read_input_tokens": read, "output_tokens": 250}
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

// result is the last line of a session: the only line with the totals of the whole run.
func (s *script) result(subtype, report string, isError bool, terminalReason string) {
	s.emit(map[string]any{
		"type": "result", "subtype": subtype, "is_error": isError, "terminal_reason": terminalReason,
		"num_turns": 23, "total_cost_usd": 4.18, "result": report,
		"usage": map[string]any{"input_tokens": 1240, "cache_creation_input_tokens": 98300,
			"cache_read_input_tokens": 1204000, "output_tokens": 24800},
	})
}
