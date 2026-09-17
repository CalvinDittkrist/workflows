# orchestrator

Coordinator session for one repository. Start it in the main checkout inside a Herdr pane: `claude --agent orchestrator`. It opens with `/orchestrator:board`.

| Skill | Script | Effect |
| --- | --- | --- |
| `/orchestrator:claim <issue> [--sandbox] [--base b]` | `claim.sh` | validate issue, branch `<type>/<n>-<slug>`, worktree in `.claude/worktrees/`, Herdr workspace, start `claude --agent worker … /worker:work` |
| `/orchestrator:yolo-claim <issue>` | `claim.sh --yolo` | same, worker merges itself when green |
| `/orchestrator:board` | `board.sh` | table: issue, branch, agent state, PR, checks, review, workspace |
| `/orchestrator:merge <pr>` | `merge.sh` | refuse unless CLEAN, checks pass, no unresolved threads, no changes requested; remove workspace + worktree, squash-merge, delete branches, ff main |
| `/orchestrator:abandon <issue\|branch> [--force]` | `abandon.sh` | drop worktree; refuses unpushed or dirty work without `--force` |
| `/orchestrator:herdr` | | loads Herdr's own skill (from `herdr --skill`) for manual pane control |

The agent has no edit tools and does not load CLAUDE.md. Scripts require `HERDR_ENV=1`, `gh`, `jq`, `git`; `--sandbox` requires `sbx`. `WF_CLAUDE_ARGS` adds flags to every worker start.
