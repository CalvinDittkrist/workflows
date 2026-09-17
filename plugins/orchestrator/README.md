# orchestrator

Coordinator session for one repository. Start it in the main checkout inside a Herdr pane: `claude --agent orchestrator`. It opens with `/orchestrator:board` and runs on Sonnet: it only calls scripts and relays their output.

| Skill | Script | Effect |
| --- | --- | --- |
| `/orchestrator:plan <idea words \| #issue> [--base b]` | `plan.sh` | branch `plan/<slug>`, topic or issue in the branch description, worktree, Herdr workspace, start `claude --agent planner … /planner:plan` |
| `/orchestrator:claim <issue> [--sandbox] [--base b]` | `claim.sh` | validate issue, branch `<type>/<n>-<slug>`, worktree in `.claude/worktrees/`, Herdr workspace, start `claude --agent worker … /worker:work` |
| `/orchestrator:yolo-claim <issue>` | `claim.sh --yolo` | same, worker merges itself when green |
| `/orchestrator:board` | `board.sh` | table: issue (or `plan`), branch, agent state, PR, checks, review, workspace; then the frontier: open `ready-for-agent` issues with no open blocker, no assignee and no worktree |
| `/orchestrator:merge <pr>` | `merge.sh` | refuse unless CLEAN, checks pass, no unresolved threads, no changes requested; remove workspace + worktree, squash-merge, delete branches, ff main |
| `/orchestrator:abandon <issue\|branch> [--force]` | `abandon.sh` | drop worktree; refuses unpushed or dirty work without `--force` |
| `/orchestrator:herdr` | | loads Herdr's own skill (from `herdr --skill`) for manual pane control |

Hook: `SessionStart` (startup only) prints the gh-axi dashboard (repo, open issues, open PRs) in GitHub repositories, via `gh-axi` or `npx -y gh-axi`. Both plugins ship a `gh-axi` discovery skill so agents prefer it over raw `gh`.

The agent has no edit tools and does not load CLAUDE.md. Scripts require `HERDR_ENV=1`, `gh`, `jq`, `git`; `--sandbox` requires `sbx`. `WF_CLAUDE_ARGS` adds flags to every worker and planner start; `WF_PLANNER_PERMISSION_MODE` (default `auto`) sets the planner's permission mode.
