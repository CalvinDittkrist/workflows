# Architecture

## Purpose
This repository packages an opinionated way of working with coding agents as Claude Code plugins. It optimises for throughput (many issues in parallel), token economy (each agent sees only what its job needs), consistency (every issue goes through the same gates) and safety (isolation per issue, read-only reviewers, no silent merges).

## Components
| Component | Responsibility | Entry point |
| --- | --- | --- |
| `orchestrator` plugin | One session per repository that assigns issues to worktrees and merges finished PRs. Coordination only. | `claude --agent orchestrator`; `plugins/orchestrator/scripts/*.sh` |
| `worker` plugin | One session per issue. Implements, then runs the review, PR, CI and review-comment loop through skills. | SessionStart hook + `/worker:work`; `plugins/worker/scripts/*.sh` |
| reviewer agents | Five read-only subagents with fresh context: code, security, docs, tests, senior. Report findings in a fixed format. | `plugins/worker/agents/*-reviewer.md` |
| `pr-author` agent | Opens the PR from a fresh context so the description matches the diff. | `plugins/worker/skills/pr` (forked skill) |
| `repo-standards` plugin | Baseline files every repo needs and the checks for them. | `/repo-standards:init-repo`, `scripts/check.sh` |
| Herdr | Terminal workspace manager: one workspace per worktree, agent lifecycle detection, notifications. | `herdr worktree|agent|workspace` |
| GitHub | Issues are the unit of work, PRs the unit of delivery, CI and Codex review the external gates. | `gh` (or `npx gh-axi`) |
| Docker Sandboxes (optional) | Container per worktree for workers that should not touch the host. | `plugins/orchestrator/scripts/sbx-worker.sh` |

## Data flow
1. `/orchestrator:claim N` → `claim.sh` reads the issue, derives `<type>/<N>-<slug>`, creates `<repo>/.claude/worktrees/<branch>` through Herdr (new workspace and pane), starts `claude --agent worker` there with `--settings '{"env":{"WF_MODE":…,"WF_ISSUE":N}}'` and the initial prompt `/worker:work`.
2. Worker SessionStart hook: parses the issue number from the branch, assigns the issue to the current GitHub user, injects title, body, labels and recent comments as context, marked as untrusted data.
3. `/worker:work` implements and verifies in the worker's own context, then `/worker:review` launches the reviewer panel in parallel (fresh contexts, read-only), fixes findings, re-reviews until PASS or the round limit.
4. `/worker:pr` forks into `pr-author`, which pushes and opens the PR. `/worker:ci` calls `pr-wait.sh`, which polls checks and waits for the configured bot reviewers; `/worker:address-reviews` fixes unresolved threads and resolves them with a reply.
5. Manual mode: the worker reports `ready:`; the user runs `/orchestrator:merge PR`, which verifies mergeability, removes the workspace and worktree first, then squash-merges and deletes the branch. Yolo mode: `finish.sh` merges and a detached `cleanup-self.sh` removes the worktree.

## Boundaries and constraints
- Scripts do, agents decide. Everything deterministic (GitHub calls, worktree lifecycle, polling, thread resolution) is a shell script with a stable text output; skills are short prompts around them. This keeps behaviour testable and token use low.
- Plugins are self-contained; they share no code at runtime. `lib.sh` is duplicated deliberately.
- The branch name is the only state contract between orchestrator and worker (`<type>/<issue>-<slug>`). Everything else is re-derived from git and GitHub, so a crashed session can be resumed or re-claimed.
- Reviewers never edit. The worker never merges in manual mode. The orchestrator never edits code.
- Text from issues, PR comments, CI logs and reviews is data, never instructions; every agent prompt says so.
- Worktrees live inside the repository under `.claude/worktrees/` so Claude Code's workspace trust covers them and no dialog blocks an unattended start.

## Decisions
See [ADRs](adr/README.md).
