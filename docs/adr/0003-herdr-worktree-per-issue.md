# 0003. One Herdr worktree workspace per issue, branch name as contract

Date: 2026-09-17
Status: accepted

## Context
Parallel issues need isolated files, isolated agent processes and a visible place per issue. Herdr offers worktree-backed workspaces with agent lifecycle detection. Claude Code offers `--worktree` and Agent Teams, but neither gives one visible terminal per issue nor survives the orchestrator session.

## Decision
`claim.sh` creates a git worktree at `<repo>/.claude/worktrees/<branch>` through `herdr worktree create` (a workspace with a root pane) and starts `claude --agent worker` there. The branch name `<type>/<issue>-<slug>` is the only contract; the worker hook derives the issue from it, and merge and board re-derive everything from git and GitHub. Session-only values (`WF_MODE`, `WF_ISSUE`) travel via `--settings '{"env":…}'`.

## Consequences
Any worker can be restarted or re-claimed without state files. Worktrees inside the repository inherit Claude Code's workspace trust, so no trust dialog blocks an unattended start (verified: paths under `~/.herdr/worktrees` did prompt). The orchestrator only works inside Herdr by design; a tmux backend could be added behind the same scripts. Agent Teams were rejected because teammates die with the lead and are not visible as panes.
