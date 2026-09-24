package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// A session is one print-mode call of Claude Code, and this is the one place such a call is built:
// the agent, the prompt, the settings and the permission mode, the timeout of its stage and the JSON
// schema of its result ([ADR 0039]). A run starts its first session at the stage its signal names;
// the stages that follow it up to the review run inside that session, as the worker plugin drives
// them, and the pr and ci stages are the factory's own: the pr stage starts a read-only session that
// writes the pull request's title and body, the ci stage a fix session per repair round and an
// address-reviews session per round of review comments.
//
// [ADR 0039]: ../docs/adr/0039-every-session-reports-through-a-structured-result.md
type session struct {
	stage   string        // the stage the session is started at, as the run records it
	prompt  string        // the slash command or the brief the session is given
	timeout time.Duration // how long the session may run before its process group is ended
	// stopAfter is the stage of the worker pipeline the session ends after (WF_STOP_AFTER), and empty
	// for a session that runs the pipeline to its end ([ADR 0043]).
	//
	// [ADR 0043]: ../docs/adr/0043-the-migration-runs-from-the-last-stage-to-the-first.md
	stopAfter string
	// scripted is the scripted worker fake mode starts for this session, and empty for the one the
	// issue's canned entry names.
	scripted string
	// readOnly is a session that can read files and do nothing else: it is started without the worker
	// agent, with the workflow plugins off and with the built-in tools cut down to Read, Grep and Glob
	// (--tools), which is what makes it read-only; the tools limit what it can do, not which files it
	// reads, and it is held to schema rather than to resultSchema.
	readOnly bool
	// schema is the JSON schema the session's result is held to, resultSchema when it is empty.
	schema string
	// agent and agents are the inline agent a read-only session runs as (--agents, --agent): a reviewer
	// of the panel, whose prompt, tools and model the definition carries. model is that definition's
	// model; a session whose agent inherits it runs on the model worker_args names.
	agent, agents, model string
	// commits is a session the factory briefs to commit its work on the branch, which the factory then
	// pushes or gates by its commit: one that reports complete with changes it did not commit has not
	// done what it reports (Factory.session).
	commits bool
	// read reads the session's structured output, readResult when it is nil.
	read func(json.RawMessage) (result, error)
	// began is called once the session's process is up, or once it is known that it never will be.
	began func()
}

// The work session is the one a local claim starts and the one a first and a resumed run of an issue
// start with, the resumed one when its branch has no pull request open yet and is not waiting at the
// pr stage: it derives where the work stands from git and GitHub as any worker does, and it stops once
// the gate has recorded its result, where the factory's review stage takes over. A follow-up run
// starts no work session: the maintainer has read the pull request and asked for changes, so it starts
// at the ci stage's address-reviews session.
//
// The timeouts are fixed here and not in the host's configuration. They are a backstop above the
// run's deadline, which stays the limit an operator sets and which ends a run with the outcome
// timeout; a session that outruns its own timeout has failed, and the run says in which stage.
var workSession = session{stage: stages["worker:work"], prompt: "/worker:work", timeout: 4 * time.Hour, stopAfter: stageGate}

// fixTimeout is how long one fix session of the ci stage may run, addressTimeout one address-reviews
// session, and authorTimeout the session that writes the pull request's title and body.
const (
	fixTimeout     = time.Hour
	addressTimeout = 2 * time.Hour
	authorTimeout  = 30 * time.Minute
)

// sessionTimeoutOverride replaces the timeout of every session when it is set, as a Go duration. It is
// set at link time by the tests, which cannot wait out hours, and by nothing a host configures.
var sessionTimeoutOverride string

// fixSession is the session of one repair round of the ci stage, given the brief of that round.
func fixSession(brief string) session {
	return session{stage: stageCI, prompt: brief, timeout: fixTimeout, scripted: "fix", commits: true}.overridden()
}

// authorSession is the session of the pr stage, given its brief: read-only, held to authorSchema and
// read against the issue whose closing reference its body has to carry.
func authorSession(brief string, issue int) session {
	return session{stage: stagePR, prompt: brief, timeout: authorTimeout, scripted: "author",
		readOnly: true, schema: authorSchema, read: func(raw json.RawMessage) (result, error) { return readAuthored(raw, issue) }}.overridden()
}

// readTools is the built-in tools a read-only session has (https://code.claude.com/docs/en/cli-reference.md,
// checked on 2026-09-23: --tools restricts the built-in tools, and a tool it does not list is not there).
// No MCP server is configured for it either (--strict-mcp-config), so it can read the worktree and nothing
// else: no shell, no edit, no skill and no subagent.
const readTools = "Read,Grep,Glob"

// addressSession is the session that answers what the reviewers ask for, given the brief of its round.
func addressSession(brief string) session {
	return session{stage: stageAddressReviews, prompt: brief, timeout: addressTimeout, scripted: "address", commits: true}.overridden()
}

func (s session) overridden() session {
	if d, err := time.ParseDuration(sessionTimeoutOverride); err == nil && d > 0 {
		s.timeout = d
	}
	return s
}

// overran is the cause the context of a session carries when the session ran past its timeout, which
// tells that ending apart from the run's deadline and from a stop.
type overran struct{ s session }

func (o overran) Error() string {
	return fmt.Sprintf("the session of the stage %s ran past its timeout of %s", o.s.stage, o.s.timeout)
}

// command is the process of the session: `claude` in print mode with the stream of events on its
// output, run in the worktree the claim made. In fake mode it is this binary again, printing the
// stream of a scripted worker: no tokens, no git, no GitHub. The configured worker arguments go to
// the worker command whichever it is; the scripted worker ignores them.
//
// The mode is manual: the factory never merges what it built. A finished run waits for the maintainer
// on GitHub, which is the only surface the factory is steered from ([ADR 0023]). Foreground subagents
// are the same setting a local claim makes ([ADR 0017]), and the auto permission mode is what being
// unattended costs: no prompt has anybody to ask ([ADR 0027]).
//
// [ADR 0017]: ../docs/adr/0017-worker-subagents-run-in-the-foreground.md
// [ADR 0023]: ../docs/adr/0023-github-is-the-only-control-surface-of-the-factory.md
// [ADR 0027]: ../docs/adr/0027-the-factorys-isolation-boundary-is-the-host.md
func (f *Factory) command(ctx context.Context, s session, entry Entry, claim claimed) (*exec.Cmd, error) {
	issue := entry.Issue
	if f.fake {
		scenario := issue.scenario
		if s.scripted != "" {
			scenario = s.scripted
		}
		args := []string{"scripted-worker", scenario, issue.Repository, strconv.Itoa(issue.Number)}
		return exec.CommandContext(ctx, f.self, append(args, f.settings.WorkerArgs...)...), nil
	}
	if s.readOnly {
		return f.readOnlyCommand(ctx, s, claim)
	}
	variables := workerVariables(entry, claim, f.settings.WorkerEnv, s)
	settings, err := workerSettings(variables)
	if err != nil {
		return nil, err
	}
	schema := s.schema
	if schema == "" {
		schema = resultSchema
	}
	args := []string{
		"--agent", "worker",
		"--output-format", "stream-json", "--verbose",
		"--permission-mode", "auto",
		"--strict-mcp-config",
		"--settings", settings,
		"--json-schema", schema,
	}
	args = append(args, f.settings.WorkerArgs...)
	args = append(args, "-p", s.prompt)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = claim.worktree
	cmd.Env = workerEnv(os.Environ(), variables)
	return cmd, nil
}

// readOnlyCommand is the process of a read-only session: no worker agent and no plugin, so no skill,
// hook or workflow variable of the worker reaches it, the tools cut down to readTools, and its own
// schema. The settings of the worktree it runs in are not loaded (--setting-sources user): the branch
// is the work under review, and a hook its .claude/settings.json declares would run a command at the
// session's start, which no tool list holds back. Of the configured worker arguments it takes the
// model alone (readOnlyArgs). It keeps what every session keeps: the compact pin, no background tasks,
// the auto permission mode and the worktree as its directory, which it reads.
func (f *Factory) readOnlyCommand(ctx context.Context, s session, claim claimed) (*exec.Cmd, error) {
	variables := map[string]string{
		"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS": "1",
		"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE":      compactPercentage,
	}
	settings, err := json.Marshal(map[string]any{
		"env":               variables,
		"enabledPlugins":    map[string]bool{workerPlugin: false, "planner@" + marketplace: false, "orchestrator@" + marketplace: false},
		"autoCompactWindow": compactWindow,
	})
	if err != nil {
		return nil, fmt.Errorf("the settings of a read-only session could not be written: %w", err)
	}
	args := []string{
		"--output-format", "stream-json", "--verbose",
		"--permission-mode", "auto",
		"--strict-mcp-config",
		"--setting-sources", "user",
		"--tools", readTools,
		"--settings", string(settings),
		"--json-schema", s.schema,
	}
	if s.agent != "" {
		// The session runs as the inline agent its definition names; --tools stays, so the session is
		// read-only whatever the definition says (https://code.claude.com/docs/en/cli-reference.md and
		// https://code.claude.com/docs/en/sub-agents.md, checked on 2026-09-24).
		args = append(args, "--agents", s.agents, "--agent", s.agent)
	}
	if s.agent == "" || s.model == "inherit" {
		args = append(args, readOnlyArgs(f.settings.WorkerArgs)...)
	}
	args = append(args, "-p", s.prompt)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = claim.worktree
	cmd.Env = workerEnv(os.Environ(), variables)
	return cmd, nil
}

// readOnlyArgs is what a read-only session takes of worker_args: the model it names, and nothing else.
// worker_args is written for the worker, and an argument that gives it a capability — an MCP server
// (--mcp-config, which --strict-mcp-config still loads), a plugin directory, an added directory or
// tools — would give it to a session that reads text somebody else wrote as well
// (https://code.claude.com/docs/en/cli-reference.md, checked on 2026-09-24).
func readOnlyArgs(workerArgs []string) []string {
	for i := len(workerArgs) - 1; i >= 0; i-- {
		if model, ok := strings.CutPrefix(workerArgs[i], "--model="); ok {
			return []string{"--model", model}
		}
		if workerArgs[i] == "--model" && i+1 < len(workerArgs) {
			return []string{"--model", workerArgs[i+1]}
		}
	}
	return nil
}

// The outcomes a session reports. complete is a session that finished its stages; with a pull request
// of the run's repository it is a ready run. blocked is a session that stops for a person, and its
// summary is the reason the run records and the issue comment quotes.
const (
	resultComplete = "complete"
	resultBlocked  = "blocked"
)

// resultSchema is the JSON schema every session's result is held to, given to Claude Code with
// --json-schema, which turns the session's final report into an object of this shape and prints it as
// structured_output on the result line of the stream (https://code.claude.com/docs/en/headless.md,
// checked on 2026-09-23). The descriptions are what the session reads to fill it in: the worker skill
// still ends in a report that opens with ready: or blocked:, and this is that report as data.
//
// The gate result and the commits are what a session reports when it was told to stop after an
// earlier stage than the last ([ADR 0043]): the worker's stop.sh prints them in its final report, and
// the stage that follows on the factory's side reads them here and not from the worktree's records. A
// session that ran the whole pipeline leaves them out. The panel summary is not among them: the
// factory derives it from the rounds it recorded, and no session writes it.
//
// The replies, the answer and the lists of what was fixed and declined are what an address-reviews
// session reports: the factory posts the replies and the answer itself (postAnswers), so a session
// writes nothing to GitHub. Every other session leaves them out.
//
// [ADR 0043]: ../docs/adr/0043-the-migration-runs-from-the-last-stage-to-the-first.md
const resultSchema = `{"type":"object","additionalProperties":false,"required":["outcome","summary"],"properties":{` +
	`"outcome":{"type":"string","enum":["complete","blocked"],"description":"complete when the final report opens with ready:, blocked when it opens with blocked:"},` +
	`"pullRequest":{"type":"string","description":"the URL of the pull request the session opened or worked on; empty when there is none"},` +
	`"gateResult":{"type":"string","description":"the gate_result line of the final report, as written; empty when the report has none"},` +
	`"commits":{"type":"array","items":{"type":"string"},"description":"the lines under commits: in the final report, each a short hash and a subject, as written; empty when the report lists none"},` +
	`"replies":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["thread","body"],"properties":{` +
	`"thread":{"type":"string","description":"the id of a thread the brief lists"},` +
	`"body":{"type":"string","description":"one or two sentences: what changed, or why not"}}},` +
	`"description":"an address-reviews session's reply to each thread its brief lists, which the factory posts before it resolves the thread; empty for every other session"},` +
	`"answer":{"type":"string","description":"an address-reviews session's answer to the review summaries its brief lists, point by point, which the factory posts as one comment; empty when there are none and for every other session"},` +
	`"fixed":{"type":"array","items":{"type":"string"},"description":"an address-reviews session's points it fixed, one line each; empty for every other session"},` +
	`"declined":{"type":"array","items":{"type":"string"},"description":"an address-reviews session's points it declined, one line each with the reason; empty for every other session"},` +
	`"summary":{"type":"string","description":"the final report after its first word: for blocked, what the session needs from a person and why, as written"}}}`

// result is a session's structured result as the factory reads it.
type result struct {
	Outcome     string   `json:"outcome"`
	PullRequest string   `json:"pullRequest"`
	GateResult  string   `json:"gateResult"`
	Commits     []string `json:"commits"`
	Replies     []reply  `json:"replies"`
	Answer      string   `json:"answer"`
	Fixed       []string `json:"fixed"`
	Declined    []string `json:"declined"`
	Summary     string   `json:"summary"`
	// Title and Body are the pull request an author session wrote, read by readAuthored; the work
	// session's schema has neither, so its reader refuses them as fields it does not know.
	Title string `json:"-"`
	Body  string `json:"-"`
	// Verdict is a reviewer's report, read by readVerdict, and Repair a review fix session's, read by
	// readRepair.
	Verdict *Verdict `json:"-"`
	Repair  *Repair  `json:"-"`
}

// reply is an address-reviews session's reply to one review thread, named by its id.
type reply struct {
	Thread string `json:"thread"`
	Body   string `json:"body"`
}

// readResult reads the structured output of a result line against the schema. Claude Code holds the
// session to the schema already; the factory reads it again, because what it records must not rest on
// a check another program made, and a result that does not fit is a run that failed and says why.
func readResult(raw json.RawMessage) (result, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return result{}, fmt.Errorf("the result line carries no structured output")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var read struct {
		result
		Summary *string `json:"summary"` // a pointer, so a summary that is missing is told from an empty one
	}
	if err := decoder.Decode(&read); err != nil {
		return result{}, fmt.Errorf("the structured output is not an object of the schema: %v", err)
	}
	if read.Summary == nil {
		return result{}, fmt.Errorf("the structured output has no summary")
	}
	got := read.result
	got.Summary = *read.Summary
	if got.Outcome != resultComplete && got.Outcome != resultBlocked {
		return result{}, fmt.Errorf("the structured output reports the outcome %q, which is neither %s nor %s",
			got.Outcome, resultComplete, resultBlocked)
	}
	got.Summary = strings.TrimSpace(got.Summary)
	if got.Outcome == resultBlocked && got.Summary == "" {
		return result{}, fmt.Errorf("the structured output reports blocked with an empty summary, which leaves the person it waits for no reason")
	}
	return got, nil
}

// authorSchema is the result of the session that writes the pull request: its title and its body, and
// nothing else. The descriptions are its instructions on the form; readAuthored holds it to them.
var authorSchema = `{"type":"object","additionalProperties":false,"required":["title","body"],"properties":{` +
	`"title":{"type":"string","description":"the pull request's title in conventional-commit style, type(scope): subject, one line of at most ` + strconv.Itoa(maxTitle) + ` characters"},` +
	`"body":{"type":"string","description":"the pull request's body in Markdown: the closing reference Closes #<issue> on a line of its own, what changed and why, and the known limits; no verification section, which the factory appends"}}}`

// conventionalTitle is a conventional-commit subject line: a type, an optional scope, an optional !
// and a subject after the colon.
var conventionalTitle = regexp.MustCompile(`^[a-z]+(\([^()\s]+\))?!?: \S`)

// maxTitle is the longest title the factory takes: GitHub takes 256 characters, a reader far fewer.
const maxTitle = 100

// readAuthored reads the result of an author session: a title in conventional-commit style and a body
// that closes the issue. A result that is not of that shape does not fit, and the run fails on it: the
// body is used as it is written, so the factory does not mend one that misses its closing reference.
func readAuthored(raw json.RawMessage, issue int) (result, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return result{}, fmt.Errorf("the result line carries no structured output")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var read struct {
		Title *string `json:"title"`
		Body  *string `json:"body"`
	}
	if err := decoder.Decode(&read); err != nil {
		return result{}, fmt.Errorf("the structured output is not an object of the author's schema: %v", err)
	}
	if read.Title == nil || read.Body == nil {
		return result{}, fmt.Errorf("the structured output lacks the title or the body")
	}
	title, body := strings.TrimSpace(*read.Title), strings.TrimSpace(*read.Body)
	switch {
	case strings.IndexFunc(title, lineBreaking) >= 0:
		return result{}, fmt.Errorf("the title %q is more than one line or carries a control character", firstLine(title))
	case !conventionalTitle.MatchString(title):
		return result{}, fmt.Errorf("the title %q is not in conventional-commit style, type(scope): subject", title)
	case utf8.RuneCountInString(title) > maxTitle:
		return result{}, fmt.Errorf("the title is %d characters long, more than %d", utf8.RuneCountInString(title), maxTitle)
	case !closes(body, issue):
		return result{}, fmt.Errorf("the body carries no closing reference to #%d, such as Closes #%d", issue, issue)
	case verificationHeading.MatchString(body):
		return result{}, fmt.Errorf("the body carries a verification section of its own, which only the factory writes from the run's facts")
	}
	return result{Outcome: resultComplete, Title: title, Body: body}, nil
}

// lineBreaking is a character that breaks a line or is no text at all: the control characters, among
// them CR, LF and NEL (U+0085), and the Unicode line and paragraph separators.
func lineBreaking(r rune) bool {
	return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
}

// verificationHeading is a Markdown heading of a verification section. The factory appends the one
// section of that name from the run's gate result and panel summary; an author that read an issue or a
// diff telling it to write one of its own would put a verification nobody ran above the real one.
// Both kinds of heading count: an ATX heading (## Verification) and a setext one, underlined with = or -.
var verificationHeading = regexp.MustCompile(`(?im)^[ \t]{0,3}(#{1,6}[ \t]*verification\b|verification[^\n]*\n[ \t]{0,3}(=+|-+)[ \t]*$)`)

// closes says whether a body carries a keyword GitHub closes an issue by, followed by that issue.
func closes(body string, issue int) bool {
	return regexp.MustCompile(`(?i)\b(close[sd]?|fix(e[sd])?|resolve[sd]?):? #` + strconv.Itoa(issue) + `\b`).MatchString(body)
}
