package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

// The worker reports nothing to the factory. Everything the factory knows about a run it reads from
// the stream `claude -p --output-format stream-json --verbose` prints, one JSON object per line
// (recorded from a real headless worker on 2026-09-21 with Claude Code 2.1.278; the result line's
// structured_output from a real session run with --json-schema on 2026-09-23 with Claude Code 2.1.280).
//
// Message is an object on an assistant or user line and a string on a permission_denied line, so it
// is read once the type of the line is known.
type streamLine struct {
	Type               string          `json:"type"`
	Subtype            string          `json:"subtype"`
	HookName           string          `json:"hook_name"`
	Outcome            string          `json:"outcome"`
	Model              string          `json:"model"`
	SessionID          string          `json:"session_id"`
	PermissionMode     string          `json:"permissionMode"`
	Parent             *string         `json:"parent_tool_use_id"`
	NumTurns           int             `json:"num_turns"`
	TotalCostUSD       float64         `json:"total_cost_usd"`
	IsError            bool            `json:"is_error"`
	TerminalReason     string          `json:"terminal_reason"`
	Result             any             `json:"result"`
	StructuredOutput   json.RawMessage `json:"structured_output"`
	Usage              usage           `json:"usage"`
	ToolName           string          `json:"tool_name"`
	DecisionReason     string          `json:"decision_reason"`
	AgentID            string          `json:"agent_id"`
	DecisionReasonType string          `json:"decision_reason_type"`
	Message            json.RawMessage `json:"message"`
}

// Who denied a tool call, by the decision_reason_type of its permission_denied line.
var deniers = map[string]string{
	"classifier": "the classifier",
	"rule":       "a permission rule",
	"mode":       "the permission mode",
	"asyncAgent": "the background agent's permissions",
}

// message is what an assistant or user line carries in its message.
type message struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   usage           `json:"usage"`
}

// The system subtypes the stream-json reference documents (checked on 2026-09-23 against
// https://code.claude.com/docs/en/agent-sdk/typescript.md) that the factory does not record. A system
// line of any other subtype is logged under its subtype, so a line a later Claude Code adds is seen
// rather than lost or mistaken for a line out of the format.
var quietSubtypes = map[string]bool{
	"compact_boundary": true, "informational": true, "worker_shutting_down": true, "plugin_install": true,
	"status": true, "task_notification": true, "hook_started": true, "hook_progress": true,
	"task_started": true, "task_progress": true, "task_updated": true, "background_tasks_changed": true,
	"thinking_tokens": true, "files_persisted": true, "local_command_output": true, "commands_changed": true,
	"api_retry": true,
}

type usage struct {
	Input         int `json:"input_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
	Output        int `json:"output_tokens"`
	// CacheCreationDetail splits the cache writes by how long the cache keeps them, which is what a
	// write costs: the hour's twice the input price, the five minutes' a quarter above it.
	CacheCreationDetail cacheCreation `json:"cache_creation"`
}

type cacheCreation struct {
	Hour int `json:"ephemeral_1h_input_tokens"`
}

type block struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	Input    map[string]any  `json:"input"`
	Content  json.RawMessage `json:"content"`
	IsError  bool            `json:"is_error"`
}

// The worker never reports a stage; the skill it invokes is the stage.
var stages = map[string]string{
	"worker:work":            "implement",
	"worker:review":          "review",
	"worker:pr":              "pr",
	"worker:ci":              "ci",
	"worker:address-reviews": "reviews",
}

// ingest reads one line of the worker's stream into the run: its events, its stage, the totals the
// factory counts from the assistant lines, and on the result line the worker's own totals, which
// replace them.
func (f *Factory) ingest(r *Run, line []byte) {
	var m streamLine
	if json.Unmarshal(line, &m) != nil || m.Type == "" {
		f.runs.event(r, Event{Kind: "error", Title: "the worker printed a line that is not the stream format", Body: string(line)})
		return
	}
	// A subagent's events carry the tool-use id of the Agent call that started them; the worker's own
	// carry none. That is the only way the two are told apart.
	sub := m.Parent != nil
	switch {
	case m.Type == "system" && m.Subtype == "hook_response":
		f.runs.event(r, Event{Kind: "hook", Title: m.HookName + " " + m.Outcome, Sub: sub})
	case m.Type == "system" && m.Subtype == "init":
		f.runs.update(r, func() { r.Model, r.SessionID = m.Model, m.SessionID })
		f.runs.event(r, Event{Kind: "init", Title: "session " + m.Model + ", permission mode " + m.PermissionMode, Body: m.SessionID})
	case m.Type == "system" && m.Subtype == "permission_denied":
		// The session goes on after a denial, which the model is told of in the tool result, so the
		// denial is logged as an error event and not taken as the reason the run ended. A denial in a
		// subagent names the agent rather than the Agent call it runs under.
		denier, ok := deniers[m.DecisionReasonType]
		if !ok {
			denier = "a permission check"
		}
		f.runs.event(r, Event{Kind: "error", Title: denier + " denied " + m.ToolName,
			Body: strings.TrimSpace(m.DecisionReason + "\n" + blockText(m.Message)), Sub: sub || m.AgentID != ""})
	case m.Type == "system" && !quietSubtypes[m.Subtype]:
		f.runs.event(r, Event{Kind: "system", Title: "system: " + m.Subtype, Body: string(line), Sub: sub})
	case m.Type == "assistant":
		msg := body(m.Message)
		f.runs.count(r, msg, sub)
		for _, b := range blocks(msg.Content) {
			switch b.Type {
			case "text":
				f.runs.event(r, Event{Kind: "text", Title: firstLine(b.Text), Body: b.Text, Sub: sub})
			case "thinking":
				f.runs.event(r, Event{Kind: "thinking", Title: "thinking", Body: b.Thinking, Sub: sub})
			case "tool_use":
				if stage, ok := stages[text(b.Input["skill"])]; ok && b.Name == "Skill" && !sub {
					f.runs.update(r, func() { r.stage(stage) })
				}
				input, _ := json.MarshalIndent(b.Input, "", "  ")
				f.runs.event(r, Event{Kind: "tool", Title: strings.TrimSpace(b.Name + " " + toolLabel(b)), Body: string(input), Sub: sub})
			}
		}
	case m.Type == "user":
		for _, b := range blocks(body(m.Message).Content) {
			if b.Type == "tool_result" && b.IsError {
				f.error(r, Event{Kind: "error", Title: "tool error", Body: blockText(b.Content), Sub: sub})
			}
		}
	case m.Type == "result":
		final := text(m.Result)
		f.runs.update(r, func() {
			r.Turns, r.CostUSD, r.Totals = m.NumTurns, m.TotalCostUSD, totalsWorker
			r.Tokens = Tokens{Input: m.Usage.Input, Output: m.Usage.Output, CacheCreation: m.Usage.CacheCreation, CacheRead: m.Usage.CacheRead}
		})
		event := Event{Kind: "result", Title: "result: " + m.Subtype, Body: final}
		if m.TerminalReason != "" {
			event.Title += " (" + m.TerminalReason + ")"
		}
		if m.IsError {
			// The result line says that a session ended in an error, never why: the cause is in the
			// lines before it. So it is the reason of last resort and never overwrites one of them.
			event.Kind = "error"
			f.runs.update(r, func() { r.resultSummary = event.Title })
			f.runs.event(r, event)
			return
		}
		// The outcome, the pull request and the summary are the structured result's and never the
		// report's words ([ADR 0039]).
		//
		// [ADR 0039]: ../docs/adr/0039-every-session-reports-through-a-structured-result.md
		got, err := readResult(m.StructuredOutput)
		f.runs.update(r, func() {
			if err != nil {
				r.result, r.misfit = nil, err.Error()
				return
			}
			r.result, r.misfit = &got, ""
		})
		if err != nil {
			event.Kind = "error"
			event.Body = strings.TrimSpace(err.Error() + "\n" + final)
		}
		f.runs.event(r, event)
	}
}

// A pull request as a result names it. GitHub spells a repository as its owner did and takes any
// case, so the repository is compared without it.
var pullRequestURL = regexp.MustCompile(`(?i)https://github\.com/([a-z0-9._-]+/[a-z0-9._-]+)/pull/([0-9]+)`)

// pullRequest is the pull request a complete result names, as the record may carry it. The result is
// written by a model and read by a browser later, and the text of an issue can steer what a worker
// writes, so nothing of the field itself reaches the record: it is searched for a pull request of the
// repository this run is for, and the URL is built from that repository and the number found. A field
// without one is given back as the reason nothing was taken.
func pullRequest(detail, repository string) (url, reason string) {
	for _, match := range pullRequestURL.FindAllStringSubmatch(detail, -1) {
		if strings.EqualFold(match[1], repository) {
			return "https://github.com/" + repository + "/pull/" + match[2], ""
		}
	}
	return "", "the result says complete but names no pull request of " + repository + ": " + firstLine(detail)
}

func body(raw json.RawMessage) message {
	var parsed message
	_ = json.Unmarshal(raw, &parsed) // a line without a message object has no content and no usage
	return parsed
}

func blocks(raw json.RawMessage) []block {
	var parsed []block
	_ = json.Unmarshal(raw, &parsed) // a plain string prompt is not a block list and yields none
	return parsed
}

// blockText is the text of a tool result, which is a string or a list of blocks.
func blockText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []block
	_ = json.Unmarshal(raw, &parts)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.Text)
	}
	return strings.Join(out, "\n")
}

// toolLabel is what a tool call did, in one line: the most telling of its arguments.
func toolLabel(b block) string {
	for _, key := range []string{"skill", "description", "file_path", "command", "pattern"} {
		if v := text(b.Input[key]); v != "" {
			if key == "description" && text(b.Input["subagent_type"]) != "" {
				return text(b.Input["subagent_type"]) + ": " + firstLine(v)
			}
			return firstLine(v)
		}
	}
	return ""
}

func text(v any) string {
	s, _ := v.(string)
	return s
}
