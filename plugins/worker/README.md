# worker

The main agent has eight tools (Bash, Read, Write, Edit, Grep, Glob, Agent, Skill); everything else stays out of its context. The reviewers, the PR author and the documentation lookup run as subagents with read-only tools plus Bash.

One session per issue worktree, started by the orchestrator as `claude --agent worker … "/worker:work"`. Works standalone too: check out a branch named `<type>/<issue>-<slug>`, run `claude --agent worker --settings '{"env":{"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS":"1"}}'`, type `/worker:work`. Without that setting the subagents run in the background and the pipeline waits a turn longer for every panel; `facts.sh` prints which of the two it is as `subagents:` ([ADR 0017](../../docs/adr/0017-worker-subagents-run-in-the-foreground.md)).

Hook: `SessionStart` runs `scripts/session-start.sh`. On startup it assigns the issue to you and injects title, labels, body and recent comments as untrusted task data. On resume, clear or compact it injects one reminder line. Silent on other branches and in subagents.

| Skill | Purpose |
| --- | --- |
| `/worker:work` | pipeline driver: understand → implement → review → pr → ci → finish |
| `/worker:review` | one gate run per round (`gate.sh`, the result goes into every brief), reviewer panel in parallel (`WF_REVIEWERS`), fix, re-review (`WF_REVIEW_ROUNDS`); no reviewer runs the gate ([ADR 0019](../../docs/adr/0019-the-gate-runs-once-per-review-round.md)) |
| `/worker:pr` | forked into `pr-author`: push and open the PR from a fresh context, with the recorded panel summary (`panel.sh`) and gate result (`gate.sh`) in the body, and a draft when the panel did not pass |
| `/worker:ci` | `pr-wait.sh`: checks + bot review wait (skipped on a draft, which it reports), returns `green`, `checks-failed`, `review-comments` or `waiting` |
| `/worker:docs` | `claude-docs.sh` in a read-only `docs-lookup` subagent: one Claude Code question answered from the current documentation, pinned to `code.claude.com`, so the pages never enter the worker's context ([ADR 0029](../../docs/adr/0029-agents-verify-claude-code-facts-against-the-live-documentation.md)) |
| `/worker:address-reviews` | `pr-threads.sh` + `pr-resolve.sh`: fix or decline each thread, reply, resolve |

Agents: `worker` (main thread, opus), `code-reviewer`, `security-reviewer`, `docs-reviewer` (sonnet, no CLAUDE.md), `test-reviewer`, `senior-reviewer` (all read-only, inherit the worker model), `pr-author` (read-only, inherit), `docs-lookup` (read-only, sonnet).

Documentation: `scripts/claude-docs.sh` is the worker's pinned way to the Claude Code documentation (not a network boundary: the worker keeps `Bash`, `gh` and `git`). Without an argument it prints the index of the Claude Code documentation, with a page slug (lowercase letters, digits and hyphens, nested with a slash) that page; it builds the URL from a hard-coded origin, speaks https only and prints nothing when the answer came from elsewhere. `WF_DOCS_TIMEOUT` (default 30 s) is the request timeout.

Context: the status line of the pane writes this session's context size into the worktree, and `scripts/checkpoint.sh` reads it, printing `context_tokens`, `threshold` and `handoff: yes | no | unavailable`. `WF_HANDOFF_TOKENS` (default 120000) is the threshold, `WF_CONTEXT_MAX_AGE` (default 900 s) the age past which a recorded size says nothing about this turn and the answer is `yes`. Outside a Herdr pane nothing measures the context and the answer is `unavailable`. Keep the threshold below the 200 000 the claim sets as the session's auto-compact window, or the safety net fires before the handoff does. It reports only; no stage acts on it yet ([ADR 0020](../../docs/adr/0020-the-pane-measures-the-context-and-the-worktree-carries-the-value.md)).

Yolo mode (`WF_MODE=yolo`): `finish.sh` squash-merges after `green`, notifies through Herdr, and `cleanup-self.sh` removes the worktree, workspace and branch from a detached process. It merges only a panel recorded as ready on a pull request that is no draft, so a run whose panel did not pass, or that recorded no summary, ends with the maintainer ([ADR 0018](../../docs/adr/0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md)).
