# 0003. One Herdr worktree workspace per issue, branch name as contract

Date: 2026-09-17
Status: accepted

## Context
- Parallel issues need isolated files, isolated agent processes and a visible place per issue.
- Herdr offers worktree-backed workspaces with agent lifecycle detection.
- Claude Code's `--worktree` and Agent Teams give no visible terminal per issue and do not survive the orchestrator session.

## Decision
`claim.sh` runs each issue's worker in a git worktree at `<repo>/.claude/worktrees/<branch>`, and the branch name `<type>/<issue>-<slug>` is the only contract.

## Consequences
- The worktree comes from `herdr worktree create`, and `claude --agent worker` starts in it.
- The worker hook derives the issue from the branch; merge and board re-derive everything from git and GitHub.
- Session-only values (`WF_MODE`, `WF_ISSUE`) travel via `--settings '{"env":…}'`.
- Any worker can be restarted or re-claimed without state files.
- Worktrees inside the repository inherit Claude Code's workspace trust, so no trust dialog blocks an unattended start. Paths under `~/.herdr/worktrees` did prompt.
- The orchestrator works only inside Herdr by design; a tmux backend could sit behind the same scripts.
- Rejected: Agent Teams. Teammates die with the lead and are not visible as panes.
