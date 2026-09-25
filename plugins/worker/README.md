# worker

One session per issue worktree, started by the orchestrator as `claude --agent worker … "/worker:work"`. It implements the issue, passes a reviewer panel, opens the pull request from a fresh context and drives CI and review comments to green. The main agent has eight tools (Bash, Read, Write, Edit, Grep, Glob, Agent, Skill). Its `Agent` tool allows only the plugin's subagents, listed in `agents/worker.md` (tested).

## Skills
| Skill | Script | Effect |
| --- | --- | --- |
| `/worker:work` | `facts.sh` | pipeline driver: understand, implement, review, pr, ci, finish; starts at `resume_stage:` after a handoff |
| `/worker:review` | `checkpoint.sh review`, `panel.sh`, `gate.sh` | reviewer panel in parallel with the recorded gate result in every brief, fix, `panel.sh round`, re-review with the reviewers still on FIX; no reviewer runs the gate ([ADR 0019](../../docs/adr/0019-the-gate-runs-once-per-review-round.md)) |
| `/worker:pr` | `panel.sh`, `gate.sh` | forked into `pr-author`: push and open the PR from a fresh context with the panel summary and gate result; never a draft, which no bot reviews |
| `/worker:ci` | `checkpoint.sh ci`, `repair.sh print`, `pr-wait.sh` | checks and the bot's one review; returns `conflicts`, `checks-failed`, `review-comments`, `green` or `waiting`; each repair round is counted with `repair.sh round` first |
| `/worker:docs` | `claude-docs.sh` | a read-only `docs-lookup` subagent answers one Claude Code question from the current documentation ([ADR 0030](../../docs/adr/0030-agents-verify-claude-code-facts-against-the-live-documentation.md)) |
| `/worker:hunt-tests` | `hunt.sh` | test hunt on a `hunt/` branch started by `/orchestrator:hunt-tests`; see below |
| `/worker:address-reviews` | `pr-threads.sh`, `pr-resolve.sh` | fix or decline each thread, reply, resolve |
| `/worker:gh-axi` | | the `gh-axi` discovery skill, so the worker prefers it over raw `gh` |

Agents: `worker` (main thread, opus) and read-only subagents. `code-reviewer`, `test-reviewer` and `pr-author` run on sonnet; `docs-reviewer` and `docs-lookup` on sonnet without CLAUDE.md. `test-hunter` runs on opus with `Read`, `Grep` and `Glob` only. `security-reviewer` and `senior-reviewer` inherit the worker model.

Standalone: on a branch `<type>/<issue>-<slug>`, run `claude --agent worker --settings '{"env":{"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS":"1"}}'` and type `/worker:work`. Otherwise subagents run in the background, as `facts.sh` prints under `subagents:` ([ADR 0017](../../docs/adr/0017-worker-subagents-run-in-the-foreground.md)).

Hook: `SessionStart` runs `scripts/session-start.sh`. On startup it assigns the issue to you and injects it as untrusted data, every line indented, so no outside text imitates the hook's headings. Later it injects a reminder line, or a waiting handoff note once.

Context: the pane's status line writes the context size into the worktree ([ADR 0020](../../docs/adr/0020-the-pane-measures-the-context-and-the-worktree-carries-the-value.md)). `scripts/checkpoint.sh` reads it and prints `context_tokens`, `threshold` and `handoff: yes | no | unavailable`.

- `/worker:review` and `/worker:ci` inject it on entry; a context a handoff started skips it once ([ADR 0032](../../docs/adr/0032-the-stage-measures-the-context-on-entry-and-a-handoff-grants-one-skip.md)).
- `panel.sh round` runs it too, so a review can be handed over after any round ([ADR 0018](../../docs/adr/0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md)).
- Keep the threshold plus one review round under the compact trigger of 250 000 ([ADR 0031](../../docs/adr/0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md), [ADR 0034](../../docs/adr/0034-the-compact-trigger-is-raised-through-the-window.md)).

Handoff: on `handoff: yes` the stage does none of its work and the pipeline continues in a fresh context in the same pane ([ADR 0029](../../docs/adr/0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md)).

- `scripts/handoff.sh <review|ci>` writes a note with four sections to `<worktree git dir>/worker/handoff`, never committed and never in a brief.
- `scripts/handoff-resume.sh` runs detached, clears only the session that asked and never types into a busy pane. On failure it notifies through Herdr.
- The hook never injects a note into the session that wrote it. The record stays, so a later `/worker:work` resumes at that stage.

Repair rounds: the count is a record, because a fresh context would count from zero.

- `scripts/repair.sh round` counts a check fix, a base merge or an `/worker:address-reviews` round into `<worktree git dir>/worker/repair`, per pull request.
- Past `WF_CI_REPAIR_ROUNDS` it refuses, and the pipeline stops with the maintainer. A `waiting` answer counts nothing.

Test hunt ([ADR 0045](../../docs/adr/0045-a-test-hunt-runs-on-a-branch-without-an-issue.md), [ADR 0046](../../docs/adr/0046-a-test-is-removed-at-high-confidence-without-approval-before-the-pull-request.md), [ADR 0047](../../docs/adr/0047-a-test-hunt-reads-its-shares-whole-and-hunts-while-it-finds-something.md)):

- `hunt.sh round` splits the tests into shares of at most 1500 lines, one `test-hunter` each.
- `high` candidates are removed and `medium` ones checked by the worker, one commit per test. Up to three rounds run while a round finds something new.
- The hunt record, `hunt.sh print`, stands in for the issue. A hunt that removed nothing opens no pull request.

Documentation: `scripts/claude-docs.sh` prints the documentation index, or one page per slug, from a hard-coded https origin. It is no network boundary.

Yolo mode (`WF_MODE=yolo`): `finish.sh` squash-merges after `green`, notifies through Herdr, and `cleanup-self.sh` removes worktree, workspace and branch. It merges only a panel recorded as ready at the current head; anything else ends with the maintainer.

## Configuration
| Variable | Default | Effect |
| --- | --- | --- |
| `WF_REVIEWERS` | `code,security,docs,tests,senior` | members of the reviewer panel |
| `WF_REVIEW_ROUNDS` | `3` | max review rounds, counted over the recorded rounds |
| `WF_CI_REPAIR_ROUNDS` | `3` | max repair rounds per pull request |
| `WF_PR_BOT_REVIEWERS` | `chatgpt-codex-connector` | bot logins whose review the worker waits for; `""` for none |
| `WF_PR_REVIEW_WAIT` | `1200` | seconds to wait for the bot's one review after checks pass |
| `WF_REVIEW_MANDATE` | unset | names the review a driver starts a worker to answer; `repair.sh reset` restarts the repair count once per review |
| `WF_HANDOFF_TOKENS` | `100000` | context size at which the checkpoint says hand over |
| `WF_CONTEXT_MAX_AGE` | `900` | seconds after which a recorded size is too old and the answer is `yes` |
| `WF_HANDOFF_SESSION_MS` | `60000` | how long a fresh session may take to appear; no number means the default |
| `WF_HANDOFF_POLL_SECONDS` | `1` | how often the pane is asked; no number means the default |
| `WF_DOCS_TIMEOUT` | `30` | seconds one documentation request may take |
| `WF_MODE`, `WF_ISSUE` | set by the claim | mode (`manual` or `yolo`) and issue of the worker session |

## Develop
`claude --plugin-dir plugins/worker` loads the plugin without installing it. `make check` runs the gate.
