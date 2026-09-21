package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

// The worker reports nothing to the factory. Everything the factory knows about a run it reads from
// the stream `claude -p --output-format stream-json --verbose` prints, one JSON object per line
// (recorded from a real headless worker on 2026-09-21 with Claude Code 2.1.278).
type streamLine struct {
	Type           string  `json:"type"`
	Subtype        string  `json:"subtype"`
	HookName       string  `json:"hook_name"`
	Outcome        string  `json:"outcome"`
	Model          string  `json:"model"`
	SessionID      string  `json:"session_id"`
	PermissionMode string  `json:"permissionMode"`
	Parent         *string `json:"parent_tool_use_id"`
	NumTurns       int     `json:"num_turns"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
	IsError        bool    `json:"is_error"`
	TerminalReason string  `json:"terminal_reason"`
	Result         any     `json:"result"`
	Usage          usage   `json:"usage"`
	Message        struct {
		Content json.RawMessage `json:"content"`
		Usage   usage           `json:"usage"`
	} `json:"message"`
}

type usage struct {
	Input         int `json:"input_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
	Output        int `json:"output_tokens"`
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

// ingest reads one line of the worker's stream into the run: its events, its stage, and on the
// result line the totals of the session.
func (f *Factory) ingest(r *Run, line []byte) {
	var m streamLine
	if json.Unmarshal(line, &m) != nil {
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
	case m.Type == "assistant":
		// What a message started from is its input plus everything read from the cache: the context it
		// was answered with. A subagent has a context of its own, which says nothing about how full the
		// worker's is, so only the worker's own messages count.
		if !sub {
			u := m.Message.Usage
			f.runs.raiseContextPeak(r, u.Input+u.CacheCreation+u.CacheRead)
		}
		for _, b := range blocks(m.Message.Content) {
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
		for _, b := range blocks(m.Message.Content) {
			if b.Type == "tool_result" && b.IsError {
				f.error(r, Event{Kind: "error", Title: "tool error", Body: blockText(b.Content), Sub: sub})
			}
		}
	case m.Type == "result":
		final := text(m.Result)
		outcome, detail := report(final)
		f.runs.update(r, func() {
			r.Turns, r.CostUSD = m.NumTurns, m.TotalCostUSD
			r.Tokens = Tokens{Input: m.Usage.Input, Output: m.Usage.Output, CacheCreation: m.Usage.CacheCreation, CacheRead: m.Usage.CacheRead}
			r.reportOutcome, r.reportDetail = outcome, detail
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
		}
		f.runs.event(r, event)
	}
}

// report reads the worker's final report. It is markdown written for a person, so the line that
// carries the outcome may be bold, quoted, a heading or a list item: `**ready: <url>**`. The first
// line that says ready or blocked decides. For ready the detail is the rest of that line, which
// names the pull request; for blocked it is the reason, which runs to the end of the report because
// a blocker takes more than one line.
func report(final string) (outcome, detail string) {
	lines := strings.Split(final, "\n")
	for i, line := range lines {
		bare := undecorate(line)
		for _, want := range []string{outcomeReady, outcomeBlocked} {
			if !strings.HasPrefix(strings.ToLower(bare), want+":") {
				continue
			}
			rest := strings.TrimSpace(bare[len(want)+1:])
			if want == outcomeReady {
				return outcomeReady, rest
			}
			reason := []string{rest}
			for _, more := range lines[i+1:] {
				reason = append(reason, undecorate(more))
			}
			return outcomeBlocked, strings.TrimSpace(strings.Join(reason, "\n"))
		}
	}
	return "", ""
}

// A pull request as a report names it. GitHub spells a repository as its owner did and takes any
// case, so the repository is compared without it.
var pullRequestURL = regexp.MustCompile(`(?i)https://github\.com/([a-z0-9._-]+/[a-z0-9._-]+)/pull/([0-9]+)`)

// pullRequest is the pull request a ready report names, as the record may carry it. The report is
// written by a model and read by a browser later, and the text of an issue can steer what a worker
// writes, so nothing of the report itself reaches the record: the line is searched for a pull
// request of the repository this run is for — a model ends a sentence after it, or writes it as a
// link — and the URL is built from that repository and the number found. A line without one is
// given back as the reason nothing was taken.
func pullRequest(detail, repository string) (url, reason string) {
	for _, match := range pullRequestURL.FindAllStringSubmatch(detail, -1) {
		if strings.EqualFold(match[1], repository) {
			return "https://github.com/" + repository + "/pull/" + match[2], ""
		}
	}
	return "", "the report says ready but names no pull request of " + repository + ": " + firstLine(detail)
}

// undecorate strips the markdown around a line, so the text of the line can be read as text.
func undecorate(line string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "*_`#>- \t"))
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
