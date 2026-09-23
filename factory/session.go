package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// A session is one print-mode call of Claude Code, and this is the one place such a call is built:
// the agent, the prompt, the settings and the permission mode, the timeout of its stage and the JSON
// schema of its result ([ADR 0039]). A run starts its first session at the stage its signal names;
// the stages that follow it up to the pull request run inside that session, as the worker plugin
// drives them, and the ci stage is the factory's own, which starts a fix session per repair round and
// an address-reviews session per round of review comments.
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
}

// The work session is the one a local claim starts and the one a first and a resumed run of an issue
// start with, the resumed one when its branch has no pull request open yet: it derives where the work
// stands from git and GitHub as any worker does, and it stops once the pull request is open, where
// the factory's ci stage takes over. A follow-up run starts no work session: the maintainer has read
// the pull request and asked for changes, so it starts at the ci stage's address-reviews session.
//
// The timeouts are fixed here and not in the host's configuration. They are a backstop above the
// run's deadline, which stays the limit an operator sets and which ends a run with the outcome
// timeout; a session that outruns its own timeout has failed, and the run says in which stage.
var workSession = session{stage: stages["worker:work"], prompt: "/worker:work", timeout: 4 * time.Hour, stopAfter: "pr"}

// fixTimeout is how long one fix session of the ci stage may run, and addressTimeout one
// address-reviews session.
const (
	fixTimeout     = time.Hour
	addressTimeout = 2 * time.Hour
)

// sessionTimeoutOverride replaces the timeout of every session when it is set, as a Go duration. It is
// set at link time by the tests, which cannot wait out hours, and by nothing a host configures.
var sessionTimeoutOverride string

// fixSession is the session of one repair round of the ci stage, given the brief of that round.
func fixSession(brief string) session {
	return session{stage: stageCI, prompt: brief, timeout: fixTimeout, scripted: "fix"}.overridden()
}

// addressSession is the session that answers what the reviewers ask for, given the brief of its round.
func addressSession(brief string) session {
	return session{stage: stageAddressReviews, prompt: brief, timeout: addressTimeout, scripted: "address"}.overridden()
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
	variables := workerVariables(entry, claim, f.settings.WorkerEnv, s)
	settings, err := workerSettings(variables)
	if err != nil {
		return nil, err
	}
	args := []string{
		"--agent", "worker",
		"--output-format", "stream-json", "--verbose",
		"--permission-mode", "auto",
		"--strict-mcp-config",
		"--settings", settings,
		"--json-schema", resultSchema,
	}
	args = append(args, f.settings.WorkerArgs...)
	args = append(args, "-p", s.prompt)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = claim.worktree
	cmd.Env = workerEnv(os.Environ(), variables)
	return cmd, nil
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
// The panel summary, the gate result and the commits are what a session reports when it was told to
// stop after an earlier stage than the last ([ADR 0043]): the worker's stop.sh prints them in its final
// report, and the stage that follows on the factory's side reads them here and not from the worktree's
// records. A session that ran the whole pipeline leaves them out.
//
// The replies, the answer and the lists of what was fixed and declined are what an address-reviews
// session reports: the factory posts the replies and the answer itself (postAnswers), so a session
// writes nothing to GitHub. Every other session leaves them out.
//
// [ADR 0043]: ../docs/adr/0043-the-migration-runs-from-the-last-stage-to-the-first.md
const resultSchema = `{"type":"object","additionalProperties":false,"required":["outcome","summary"],"properties":{` +
	`"outcome":{"type":"string","enum":["complete","blocked"],"description":"complete when the final report opens with ready:, blocked when it opens with blocked:"},` +
	`"pullRequest":{"type":"string","description":"the URL of the pull request the session opened or worked on; empty when there is none"},` +
	`"panelSummary":{"type":"string","description":"the panel_summary_block of the final report, its lines as written; empty when the report has none"},` +
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
	Outcome      string   `json:"outcome"`
	PullRequest  string   `json:"pullRequest"`
	PanelSummary string   `json:"panelSummary"`
	GateResult   string   `json:"gateResult"`
	Commits      []string `json:"commits"`
	Replies      []reply  `json:"replies"`
	Answer       string   `json:"answer"`
	Fixed        []string `json:"fixed"`
	Declined     []string `json:"declined"`
	Summary      string   `json:"summary"`
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
