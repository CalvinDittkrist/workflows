# The local workflow, step by step

Actors: **you** (in the orchestrator pane), the **orchestrator** agent, one **worker** agent per issue, the worker's **reviewer panel**, **pr-author**, GitHub **CI**, and **Codex** as external PR reviewer.

| Step | Who | What happens | Done when |
| --- | --- | --- | --- |
| Claim | you → orchestrator | `/orchestrator:claim 123`. Script validates the issue is open, creates branch `feat/123-slug` and worktree `.claude/worktrees/feat-123-slug` in a new Herdr workspace, starts the worker. | claim prints workspace, pane, branch |
| Context | worker hook | On startup: assign issue to you on GitHub, inject issue text and comments (marked as untrusted data). On resume/compact: one reminder line. | worker knows issue, mode, branch |
| Implement | worker | Reads CLAUDE.md, architecture.md, ADRs. Asks once if materially ambiguous. Smallest complete change; bugs reproduced end to end first; project checks run; docs and ADRs updated; conventional commits. | checks pass locally |
| Review | worker + panel | `/worker:review`: five reviewers in parallel, fresh contexts, read-only. Worker fixes S1/S2, judges S3, re-runs only reviewers that said FIX. Max `WF_REVIEW_ROUNDS`. Disputed findings are kept in the summary, not dropped. | all reviewers PASS |
| PR | pr-author (fresh context) | `/worker:pr`: pushes, writes title and body from the diff and issue, uses the PR template, `Closes #123`. | `pr: <url>` |
| CI and external review | worker | `/worker:ci` → `pr-wait.sh` polls checks and waits up to `WF_PR_REVIEW_WAIT` s for a review by `WF_PR_BOT_REVIEWERS` (Codex). `checks-failed` → read failed logs, fix, push. `review-comments` → `/worker:address-reviews` fixes, pushes, replies and resolves each thread. Three repair rounds. | `status: green` |
| Merge (manual) | you → orchestrator | Worker reports `ready:`. `/orchestrator:merge 45` refuses unless checks pass, no threads open, no changes requested, mergeable and CLEAN. Then: remove workspace and worktree, squash-merge, delete remote and local branch, fast-forward main. | merge prints `merged:` |
| Merge (yolo) | worker | `/orchestrator:yolo-claim` sets `WF_MODE=yolo`; after `green` the worker runs `finish.sh`: squash-merge, Herdr notification, detached cleanup of its own worktree, workspace and branch. | notification "Merged #45 (yolo)" |

Parallelism: claim as many issues as you like; `/orchestrator:board` lists each worktree with agent state (idle, working, blocked), PR, checks and review decision. Blocked means the worker is asking something: switch to its pane and answer.

Recovery: everything is derived from the branch name and GitHub. A dead session is restarted with `claude --agent worker` in the worktree (`--continue` keeps its history). A stuck worktree is dropped with `/orchestrator:abandon 123` (refuses to delete unpushed work without `--force`).

Per-repository variation: enable a subset of plugins, set `WF_REVIEWERS`, override any agent or skill by name in `.claude/agents/` or `.claude/skills/`, or add path-scoped rules in `.claude/rules/`.
