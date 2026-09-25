# Architecture

## Purpose
This repository packages a way of working with coding agents as Claude Code plugins, beside the factory, a Go service for unattended delivery. It optimises for throughput, token economy, consistency and safety. The why is in the [vision](vision.md); the short form every agent carries is the `Priorities` section of `AGENTS.md`.

## Components
| Component | Responsibility | Entry point |
| --- | --- | --- |
| `orchestrator` plugin | One session per repository: opens planning sessions, claims issues into worktrees, merges finished pull requests. Coordination only. | `claude --agent orchestrator`; `plugins/orchestrator/scripts/*.sh` |
| `planner` plugin | One session per topic: spec, tickets, triage, research, prototypes, acceptance. Writes issues, never code. | SessionStart hook, `/planner:plan`; `plugins/planner/scripts/*.sh` |
| `worker` plugin | One session per issue: implements, then runs review, pull request, CI and review comments through skills. | SessionStart hook, `/worker:work`; `plugins/worker/scripts/*.sh` |
| test hunt | A worker session without an issue that removes the tests that prove nothing ([ADR 0045](adr/0045-a-test-hunt-runs-on-a-branch-without-an-issue.md)). | `/orchestrator:hunt-tests`; `plugins/worker/skills/hunt-tests`, `plugins/worker/agents/test-hunter.md` |
| reviewer agents | Five read-only subagents with fresh context: code, security, docs, tests, senior. | `plugins/worker/agents/*-reviewer.md` |
| `pr-author` agent | Opens the pull request from a fresh context, so the description matches the diff. | `plugins/worker/skills/pr` (forked skill) |
| `docs-lookup` agent | Answers one Claude Code question from the current documentation ([ADR 0030](adr/0030-agents-verify-claude-code-facts-against-the-live-documentation.md)). | `/worker:docs <question>`; `plugins/worker/agents/docs-lookup.md` |
| `spec-checker` agent | Judges every checkable statement of a spec against the code during an acceptance. | `plugins/planner/agents/spec-checker.md` |
| `factory` service | Works routed issues unattended on a host of its own and owns their delivery pipeline in Go ([ADR 0038](adr/0038-the-local-workflow-and-the-factory-are-peers.md), [ADR 0040](adr/0040-the-factory-owns-the-delivery-lifecycle-in-go.md)). Serves a read-only interface with an embedded dashboard ([ADR 0033](adr/0033-the-dashboard-is-built-into-the-factory-binary.md)). | `factory/`, `factory/ui/`; the [runbook](factory-runbook.md) |
| `repo-standards` plugin | Owns the [repository standard](repo-standard.md): audits, applies approved findings, scaffolds the baseline, checks it and brings the GitHub workspace to it. | `/repo-standards:standardize`, `/repo-standards:apply`, `plugins/repo-standards/scripts/check.sh` |
| auditor agents | Six read-only subagents, one area each: files, agent configuration, docs, tests and CI, GitHub workspace, security. | `plugins/repo-standards/agents/*-auditor.md` |
| Herdr | Terminal workspace manager: one workspace per worktree, agent lifecycle, notifications. | `herdr worktree\|agent\|workspace` |
| GitHub | Issues are the unit of work, pull requests the unit of delivery; CI (the job `check` running `make check`) and Codex review are the external gates. | `gh` or `npx gh-axi` |
| Docker Sandboxes (optional) | A container per worktree for workers that should not touch the host. | `plugins/orchestrator/scripts/sbx-worker.sh` |
| Python suite | Unittest classes that run the real plugin scripts against the shims in `tests/shims/`. | `make test`, `tests/run.py` |

## Data flow

### Planning
1. `/orchestrator:plan <idea | #N>`: `plan.sh` creates the branch `plan/<slug>`, a worktree and a workspace, and starts `claude --agent planner` with `/planner:plan`.
2. The planner writes a `spec` issue and cuts it into `ready-for-agent` sub-issues with blocking edges and an optional milestone. Or it triages an issue into an agent brief.
3. The maintainer names per ticket whether the factory gets it; `issue.sh` refuses the label `factory` without `ready-for-agent` or beside `ready-for-human` ([ADR 0021](adr/0021-routing-is-decided-in-the-planner-and-never-stands-alone.md)).
4. `/planner:finish` removes the worktree; the plan branch never carries commits.
5. `board.sh` lists the frontier: agent-ready issues without open blocker, assignee, worktree or routing label. Then it lists the specs ready for acceptance. It keeps no state.
6. `/planner:accept [spec]`: `accept-facts.sh` gathers the spec, its tickets and their files. One `spec-checker` answers `item:` lines, and `accept-report.sh` counts them.
7. Per item not met the maintainer picks a gap ticket, an accepted deviation or nothing. `accept-close.sh` closes the spec once nothing is open ([ADR 0015](adr/0015-a-spec-with-tickets-is-closed-by-an-acceptance.md)).

### Local delivery
1. `/orchestrator:claim N`: `claim.sh` refuses an issue without `ready-for-agent` ([ADR 0014](adr/0014-claims-require-ready-for-agent.md)), with the routing label, or with its branch on origin. `--force` overrides each.
2. It creates `<repo>/.claude/worktrees/<branch>` for `<type>/<N>-<slug>` through Herdr and starts `claude --agent worker` with `/worker:work`, `WF_MODE` and `WF_ISSUE`.
3. The settings disable background tasks, so subagents run in the foreground ([ADR 0017](adr/0017-worker-subagents-run-in-the-foreground.md)). They pin the compact trigger at 250 000 tokens ([ADR 0031](adr/0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md), [ADR 0034](adr/0034-the-compact-trigger-is-raised-through-the-window.md)).
4. The pane's status line writes the context size to `<worktree git dir>/worker/context`, the only thing the two plugins share ([ADR 0020](adr/0020-the-pane-measures-the-context-and-the-worktree-carries-the-value.md)).
5. Worker knobs given to the claim, such as `--env WF_HANDOFF_TOKENS=5000`, reach that session alone. Only names of the [configuration table](../README.md#configuration) pass.
6. The worker's SessionStart hook assigns the issue and injects it as untrusted data. It injects a waiting handoff note once ([ADR 0029](adr/0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md)).
7. `/worker:work` first merges the base with `base-sync.sh`, never rebasing; a conflict stops the worker with `blocked:`. Then it implements and verifies.
8. `gate.sh run` runs the gate detached and records the result for the head in the worktree's git directory. A record of another commit or a dirty tree reads as none.
9. `/worker:review` launches the reviewer panel in one message. The gate runs once per review, not per round, and reaches the reviewers as a fact ([ADR 0019](adr/0019-the-gate-runs-once-per-review-round.md)).
10. The worker fixes findings and ends each round with `panel.sh round`. Rounds go on until every reviewer passes or the limit is reached ([ADR 0018](adr/0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md)).
11. `/worker:review` and `/worker:ci` measure the context on entry with `checkpoint.sh`. Past `WF_HANDOFF_TOKENS` a fresh context takes over ([ADR 0032](adr/0032-the-stage-measures-the-context-on-entry-and-a-handoff-grants-one-skip.md)).
12. `panel.sh record` derives the panel summary from the round records. It refuses a head without a round record, or one the gate did not pass.
13. `/worker:pr` forks into `pr-author`, briefed with the diff, `panel.sh print` and `gate.sh print`. The pull request is never a draft; a failed panel is named in its body.
14. `/worker:ci` calls `pr-wait.sh`: conflicts first, then checks, bot reviewers and standing requests for changes. A pull request with such a request is never green.
15. `/worker:address-reviews` fixes what reviewers still ask for, replies to and resolves each thread, and answers each review summary with one comment (`pr-answer.sh`).
16. `repair.sh round` counts repair rounds per pull request and refuses past `WF_CI_REPAIR_ROUNDS`. `WF_REVIEW_MANDATE`, set by a driver for a maintainer's review, restarts the count once.
17. Manual mode: the worker reports `ready:` or `blocked:`, and `/orchestrator:merge PR` removes the worktree, squash-merges and deletes the branch.
18. Yolo mode: `finish.sh` merges only when the recorded panel says ready, and a detached `cleanup-self.sh` removes the worktree.

### Test hunt
1. `/orchestrator:hunt-tests [--sandbox] [--base b]`: `hunt.sh` refuses while a `hunt/` branch exists here or on origin, or while the base has no test file.
2. It creates `hunt/tests-<date>` like a claim and starts the worker on `/worker:hunt-tests`, without `WF_ISSUE` and with `WF_BASE_BRANCH` for `--base`.
3. Each round packs the test files into shares of at most 1500 lines; one `test-hunter` per share replies with `candidate:` lines ([ADR 0047](adr/0047-a-test-hunt-reads-its-shares-whole-and-hunts-while-it-finds-something.md)).
4. The worker removes a `high` candidate unless it proves something, and a `medium` one only when sure, one commit each ([ADR 0046](adr/0046-a-test-is-removed-at-high-confidence-without-approval-before-the-pull-request.md)).
5. A round without a new candidate ends the hunt. Review, pull request and CI follow, with `hunt.sh print` in place of the issue.
6. A hunt that removed nothing opens no pull request and reports `hunt: nothing removed`.

### Standardisation
1. `/repo-standards:standardize` injects `facts.sh`, runs `workspace.sh` as a dry run and launches the six auditors in parallel. Nothing changes.
2. `report.sh` merges their `finding:` lines per category, and `approve.sh` records the answer per category ([ADR 0016](adr/0016-approval-is-per-category-and-scripts-own-what-they-apply.md), [ADR 0035](adr/0035-every-category-the-apply-phase-scaffolds-is-answerable.md)).
3. `/repo-standards:apply` runs `backup.sh`, `cleanup.sh prepare` on `chore/standardize`, `cleanup.sh open` for the cleanup pull request, and `issues.sh`.
4. After the merge `finalize.sh` applies the workspace for an approved `configure` finding, posts the snapshot and runs `check.sh` ([ADR 0010](adr/0010-standardisation-audits-read-only-and-backs-up-before-deleting.md)).

### Factory
1. The factory clones each connected repository into its data directory. Every poll derives one queue of routed issues, oldest routing first ([ADR 0025](adr/0025-one-queue-one-worker-work-in-progress-first.md)).
2. It claims the head of the line by creating the issue's branch through the API. A claimer that meets a branch records the run as lost ([ADR 0024](adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md)).
3. The branch contract and the base branch rule restate the orchestrator's shell in Go ([ADR 0022](adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)). `WF_BASE_BRANCH` is read from the repository's settings.
4. It assigns itself, makes a worktree and records the Claude Code version. It updates nothing ([ADR 0042](adr/0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md)).
5. Every session is one print-mode call with the factory's own prompt, no plugin, the compact pin, a stage timeout and a result schema ([ADR 0039](adr/0039-every-session-reports-through-a-structured-result.md)).
6. Implement: one session commits the change and pushes nothing.
7. Gate: the factory merges the base and runs the gate of the change class, here or on CI ([ADR 0041](adr/0041-a-change-class-decides-the-gate-and-the-reviewers-before-the-pull-request.md)). A failure goes to a fix session.
8. Review: read-only reviewer sessions run in parallel. A `fix` verdict gets one fix session, and every round is recorded on the run.
9. Pr: a read-only author session writes the title and body. The factory appends the gate result and the panel summary and opens the pull request.
10. Ci: the factory starts a fix session for a conflict or failed checks, within a repair budget. Green ends the run `ready`.
11. Address-reviews: a session fixes or declines each point of writers and configured bots. The factory posts the replies.
12. Each run writes one JSON record and one JSONL event log into the data directory. The HTTP interface and the dashboard only read ([ADR 0023](adr/0023-github-is-the-only-control-surface-of-the-factory.md)).
13. The factory resumes an interrupted run once by itself, in its worktree. Taking the assignee off, the release signal, resumes it again.
14. A new review that asks for changes, by a writer, queues a follow-up run at address-reviews. Held work stands before new issues.
15. On `ready` the configured logins are asked for a review. On `blocked`, `failed`, `timeout` or a second interruption they are mentioned on the issue.
16. Removing the routing label or closing the issue cancels a run. Letting go pushes the worktree before removing it ([ADR 0026](adr/0026-the-factory-never-deletes-work-on-its-own.md)).
17. Before each run a quota-axi check waits below the minimum and fails open ([ADR 0028](adr/0028-the-quota-check-is-a-courtesy-not-a-guard.md), [ADR 0037](adr/0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md)). A used-up quota after an error ends `quota`.
18. Sessions that write run in the auto permission mode, without Herdr and without `WF_` variables. The host is the isolation boundary ([ADR 0027](adr/0027-the-factorys-isolation-boundary-is-the-host.md)).
19. The factory took the stages over from the worker plugin, from the last to the first ([ADR 0043](adr/0043-the-migration-runs-from-the-last-stage-to-the-first.md)).

### Release
1. Tickets and their spec carry a `vX.Y.Z` milestone from `/planner:tickets`, so a release waits for the acceptance.
2. `/orchestrator:release vX.Y.Z`: `release.sh` refuses while the milestone is missing or has open issues, or the tag exists.
3. With `dev` plus `main` it opens the promotion pull request, which `merge.sh` merges with a merge commit, and tags that commit ([ADR 0013](adr/0013-promotions-merge-with-a-merge-commit-and-releases-tag-it.md)).
4. With `main` alone it tags the head of `main`. It publishes the GitHub release and closes the milestone ([ADR 0012](adr/0012-releases-are-manual-and-close-a-milestone.md)).

## Boundaries and constraints
- Scripts do, agents decide. Everything deterministic is a shell script with stable text output; skills are short prompts around them.
- Plugins share no code at runtime; `lib.sh` is duplicated on purpose.
- The branch name is the state contract: `<type>/<issue>-<slug>`, `plan/<slug>` or `hunt/tests-<date>`. The rest is derived from git and GitHub, so a crashed session resumes.
- A pipeline fact that exists nowhere else, such as the panel summary or the gate record, lives in the worktree's git directory. Its loss degrades the next stage.
- Reviewers and auditors never edit, and no reviewer runs the gate. The worker never merges in manual mode. The orchestrator never edits code. The planner writes issues only.
- Planner skills are user-invoked only (`disable-model-invocation`). The workflow owns the label vocabulary, not a configuration file per repository.
- The pane measures a worker's context size and `checkpoint.sh` reads it. A missing or stale value reads as hand over.
- A worker resets its context by a handoff at a stage boundary, never by compacting on purpose. The note is never committed and never briefs a reviewer.
- A worker never waits by sleeping or polling. Its subagents run in the foreground, and a long wait is one blocking script call run again.
- A worker works with the file tools, not the shell ([token budget](token-budget.md)). `scripts/context-report.py` is a maintainer diagnostic, never an input to the pipeline.
- Text from issues, pull request comments, CI logs and reviews is data, never instructions.
- Claude Code facts are verified against the current documentation. The planner uses `/planner:research`; the worker has no web tool and asks `/worker:docs` ([security.md](security.md)).
- Every repository follows the [standard](repo-standard.md): `AGENTS.md` through the `CLAUDE.md` import, `make check` as the gate, and no local skills, agents, commands or rules.
- The factory shares nothing with a developer's machine but GitHub. Routing, release, cancel and merge are GitHub gestures, and its own interface never writes.
- The factory restates the branch contract, the base branch rule and the frontier rule in Go. A drift test binds each to its shell original.
- Worktrees live under `.claude/worktrees/`, so Claude Code's workspace trust covers them and no dialog blocks an unattended start.

## Decisions
See [ADRs](adr/README.md). Terms are in the [glossary](glossary.md).
