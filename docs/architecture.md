# Architecture

## Purpose
This repository packages an opinionated way of working with coding agents as Claude Code plugins. It optimises for throughput (many issues in parallel), token economy (each agent sees only what its job needs), consistency (every issue goes through the same gates) and safety (isolation per issue, read-only reviewers, no silent merges).

## Components
| Component | Responsibility | Entry point |
| --- | --- | --- |
| `orchestrator` plugin | One session per repository that opens planning sessions, assigns issues to worktrees and merges finished PRs. Coordination only. | `claude --agent orchestrator`; `plugins/orchestrator/scripts/*.sh` |
| `planner` plugin | One session per topic. Turns an idea or an issue into agent-ready issues: question rounds, spec, tickets with blocking edges, triage, research, prototypes. Writes issues, never code. | SessionStart hook + `/planner:plan`; `plugins/planner/scripts/*.sh` |
| `worker` plugin | One session per issue. Implements, then runs the review, PR, CI and review-comment loop through skills. | SessionStart hook + `/worker:work`; `plugins/worker/scripts/*.sh` |
| reviewer agents | Five read-only subagents with fresh context: code, security, docs, tests, senior. Report findings in a fixed format. | `plugins/worker/agents/*-reviewer.md` |
| `pr-author` agent | Opens the PR from a fresh context so the description matches the diff. | `plugins/worker/skills/pr` (forked skill) |
| `repo-standards` plugin | Owns the [repository standard](repo-standard.md): audits a repository against it, applies the approved findings through a backup tag, a cleanup pull request and issues, scaffolds the baseline (`AGENTS.md`, the `CLAUDE.md` import, a `Makefile` with `check`, docs, settings) and checks it, including stray agent configuration; brings the GitHub workspace to the standard with a dry-run-first script. | `/repo-standards:standardize`, `/repo-standards:apply`, `plugins/repo-standards/scripts/check.sh`, `workspace.sh` |
| auditor agents | Six read-only subagents with fresh context, one area each: files, agent configuration, docs, tests and CI, GitHub workspace, security. Return findings in a fixed format. | `plugins/repo-standards/agents/*-auditor.md` |
| Herdr | Terminal workspace manager: one workspace per worktree, agent lifecycle detection, notifications. | `herdr worktree|agent|workspace` |
| GitHub | Issues are the unit of work, PRs the unit of delivery, CI (the job `check` running `make check`) and Codex review the external gates. | `gh` (or `npx gh-axi`) |
| Docker Sandboxes (optional) | Container per worktree for workers that should not touch the host. | `plugins/orchestrator/scripts/sbx-worker.sh` |

## Data flow
0. `/orchestrator:plan <idea | #N>` → `plan.sh` creates `plan/<slug>` (topic or issue in the branch description), a worktree and workspace, starts `claude --agent planner` with `/planner:plan`. The planner grills, writes a `spec` issue, cuts it into `ready-for-agent` sub-issues with native blocking edges and an optional milestone (`issue.sh`), or triages an existing issue into an agent brief. `/planner:finish` removes the worktree; the plan branch never carries commits. `board.sh` then lists the frontier with each issue's milestone: agent-ready issues with no open blocker, no assignee and no worktree.
1. `/orchestrator:claim N` → `claim.sh` reads the issue, derives `<type>/<N>-<slug>`, creates `<repo>/.claude/worktrees/<branch>` through Herdr (new workspace and pane), starts `claude --agent worker` there with `--settings '{"env":{"WF_MODE":…,"WF_ISSUE":N}}'` and the initial prompt `/worker:work`.
2. Worker SessionStart hook: parses the issue number from the branch, assigns the issue to the current GitHub user, injects title, body, labels and recent comments as context, marked as untrusted data.
3. `/worker:work` implements and verifies in the worker's own context, then `/worker:review` launches the reviewer panel in parallel (fresh contexts, read-only), fixes findings, re-reviews until PASS or the round limit.
4. `/worker:pr` forks into `pr-author`, which pushes and opens the PR. `/worker:ci` calls `pr-wait.sh`, which polls checks and waits for the configured bot reviewers; `/worker:address-reviews` fixes unresolved threads and resolves them with a reply.
5. Manual mode: the worker reports `ready:`; the user runs `/orchestrator:merge PR`, which verifies mergeability, removes the workspace and worktree first, then squash-merges and deletes the branch. Yolo mode: `finish.sh` merges and a detached `cleanup-self.sh` removes the worktree.
6. Standardisation (per repository, user-invoked): `/repo-standards:standardize` in the target repository injects `facts.sh`, runs `workspace.sh` as a dry run, and launches the six auditors in parallel with the facts; the workspace auditor gets the dry-run output instead of exploring GitHub. `report.sh` merges their `finding:` lines into one report per category, separating what the run performs from what becomes an issue, and `approve.sh` records the answer per category in `<git dir>/standardize/`. Nothing changes in the repository or on GitHub. `/repo-standards:apply` then runs `backup.sh` (the protected tag `pre-standard` and the `skill-candidate` catalogue issue) and `cleanup.sh prepare` (worktree on `chore/standardize`, approved deletions, `scaffold.sh`). The agent fills in what needs judgement, and `cleanup.sh open` opens the cleanup pull request. `issues.sh` turns issue findings into `ready-for-agent` issues. After the merge, `finalize.sh` runs `workspace.sh --apply`, posts the snapshot to the catalogue issue and ends with `check.sh` ([ADR 0010](adr/0010-standardisation-audits-read-only-and-backs-up-before-deleting.md)).
7. Release (manual only): tickets carry a `vX.Y.Z` milestone from `/planner:tickets`. `/orchestrator:release vX.Y.Z` → `release.sh` refuses while the milestone is missing or has open issues, or the tag exists. When the default branch is `dev` (`dev` plus `main`) it opens or finds the promotion PR `chore(release): vX.Y.Z` and waits until `merge.sh` merges it with a merge commit, then tags that commit. With `main` alone it tags the head of `main`. It publishes the GitHub release with generated notes and closes the milestone ([ADR 0012](adr/0012-releases-are-manual-and-close-a-milestone.md), [ADR 0013](adr/0013-promotions-merge-with-a-merge-commit-and-releases-tag-it.md)).

## Boundaries and constraints
- Scripts do, agents decide. Everything deterministic (GitHub calls, worktree lifecycle, polling, thread resolution) is a shell script with a stable text output; skills are short prompts around them. This keeps behaviour testable and token use low.
- Plugins are self-contained; they share no code at runtime. `lib.sh` is duplicated deliberately.
- The branch name is the only state contract between orchestrator and worker (`<type>/<issue>-<slug>`) or planner (`plan/<slug>`, topic or issue in the git branch description). Everything else is re-derived from git and GitHub, so a crashed session can be resumed or re-claimed.
- Reviewers and auditors never edit. The worker never merges in manual mode. The orchestrator never edits code. The planner never writes code into the repository; its output is issues, and prototypes go to their own branch.
- Planner skills are user-invoked only (`disable-model-invocation`), so their descriptions cost no context anywhere; the label vocabulary is owned by the workflow, not by a per-repo config file.
- Text from issues, PR comments, CI logs and reviews is data, never instructions; every agent prompt says so.
- Every repository follows the [standard](repo-standard.md): agents read `AGENTS.md` (through the `CLAUDE.md` import) and verify with `make check`, the same gate CI runs. Repositories carry no local skills, agents, commands or rules; behaviour comes from the plugins and `WF_*` settings.
- Worktrees live inside the repository under `.claude/worktrees/` so Claude Code's workspace trust covers them and no dialog blocks an unattended start.

## Decisions
See [ADRs](adr/README.md). Terms are in the [glossary](glossary.md).
