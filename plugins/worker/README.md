# worker

One session per issue worktree, started by the orchestrator as `claude --agent worker … "/worker:work"`. Works standalone too: check out a branch named `<type>/<issue>-<slug>`, run `claude --agent worker`, type `/worker:work`.

Hook: `SessionStart` runs `scripts/session-start.sh`. On startup it assigns the issue to you and injects title, labels, body and recent comments as untrusted task data. On resume, clear or compact it injects one reminder line. Silent on other branches and in subagents.

| Skill | Purpose |
| --- | --- |
| `/worker:work` | pipeline driver: understand → implement → review → pr → ci → finish |
| `/worker:review` | reviewer panel in parallel (`WF_REVIEWERS`), fix, re-review (`WF_REVIEW_ROUNDS`) |
| `/worker:pr` | forked into `pr-author`: push and open the PR from a fresh context |
| `/worker:ci` | `pr-wait.sh`: checks + bot review wait, returns `green`, `checks-failed`, `review-comments` or `waiting` |
| `/worker:address-reviews` | `pr-threads.sh` + `pr-resolve.sh`: fix or decline each thread, reply, resolve |

Agents: `worker` (main thread), `code-reviewer`, `security-reviewer`, `docs-reviewer` (sonnet, no CLAUDE.md), `test-reviewer`, `senior-reviewer` (all read-only), `pr-author` (read-only). Override any of them per repository in `.claude/agents/<name>.md`.

Yolo mode (`WF_MODE=yolo`): `finish.sh` squash-merges after `green`, notifies through Herdr, and `cleanup-self.sh` removes the worktree, workspace and branch from a detached process.
