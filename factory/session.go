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
// schema of its result ([ADR 0039]). A run starts one session today, at the stage its signal names;
// the stages that follow it in the pipeline run inside that session, as the worker plugin drives them.
//
// [ADR 0039]: ../docs/adr/0039-every-session-reports-through-a-structured-result.md
type session struct {
	stage   string        // the stage the session is started at, as the run records it
	prompt  string        // the slash command the session is given
	timeout time.Duration // how long the session may run before its process group is ended
}

// The sessions a run can start. The work session is the one a local claim starts and the one every
// run of an issue starts with — a first run as well as a resumed one, which derives where the work
// stands from git and GitHub as any worker does. The reviews session is the follow-up run's: the
// maintainer has read the pull request and asked for changes, so the session starts at the stage that
// reads the review threads, and that stage ends by running the CI stage again.
//
// The timeouts are fixed here and not in the host's configuration. They are a backstop above the
// run's deadline, which stays the limit an operator sets and which ends a run with the outcome
// timeout; a session that outruns its own timeout has failed, and the run says in which stage.
var (
	workSession    = session{stage: stages["worker:work"], prompt: "/worker:work", timeout: 4 * time.Hour}
	reviewsSession = session{stage: stages["worker:address-reviews"], prompt: "/worker:address-reviews", timeout: 3 * time.Hour}
)

// sessionTimeoutOverride replaces the timeout of every session when it is set, as a Go duration. It is
// set at link time by the tests, which cannot wait out hours, and by nothing a host configures.
var sessionTimeoutOverride string

// sessionFor is the session a run of that signal starts.
func sessionFor(signal string) session {
	s := workSession
	if signal == signalChangesRequested {
		s = reviewsSession
	}
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
		args := []string{"scripted-worker", issue.scenario, issue.Repository, strconv.Itoa(issue.Number)}
		return exec.CommandContext(ctx, f.self, append(args, f.settings.WorkerArgs...)...), nil
	}
	variables := workerVariables(entry, claim, f.settings.WorkerEnv)
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
const resultSchema = `{"type":"object","additionalProperties":false,"required":["outcome","summary"],"properties":{` +
	`"outcome":{"type":"string","enum":["complete","blocked"],"description":"complete when the final report opens with ready:, blocked when it opens with blocked:"},` +
	`"pullRequest":{"type":"string","description":"the URL of the pull request the session opened or worked on; empty when there is none"},` +
	`"summary":{"type":"string","description":"the final report after its first word: for blocked, what the session needs from a person and why, as written"}}}`

// result is a session's structured result as the factory reads it.
type result struct {
	Outcome     string `json:"outcome"`
	PullRequest string `json:"pullRequest"`
	Summary     string `json:"summary"`
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
