# 0018. A worker stage hands a fact to the next one through the worktree's git directory

Date: 2026-09-21
Status: accepted

## Context
- The panel's verdict lives in the worker's context; the pull request stage is a forked skill with a fresh one.
- The skill argument was the only channel. Pull request #40 hid a panel that never passed.
- Passing it by hand contradicts [ADR 0002](0002-scripts-do-agents-decide.md), and a verdict about a diff exists nowhere else.

## Decision
A worker stage leaves a fact for a later stage in `<this worktree's git directory>/worker/`, written and read by a script, never an agent.

## Consequences
- `wf_state_dir` uses `git rev-parse --git-dir`, so parallel workers never share it.
- `panel.sh round` stores one round record per round, at a commit the gate passed ([ADR 0019](0019-the-gate-runs-once-per-review-round.md)), with a context checkpoint.
- `panel.sh record` derives the panel summary; a reviewer not on `PASS`, or a head past the last round, makes it a draft.
- The records resume a review ([ADR 0032](0032-the-stage-measures-the-context-on-entry-and-a-handoff-grants-one-skip.md)) while their commit is in HEAD.
- `panel.sh print` briefs the pull request stage. No argument can undo a draft, and the summary cannot drift from the verdicts.
- `finish.sh` merges only a panel recorded as ready.
- A lost directory is harmless: the verdict reads `draft`, also when `/worker:work` skips the review ([ADR 0029](0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md)).
- A fact there is small plain text owned by one script.
- These facts never cache what git or GitHub answers, and a resumed session needs none of them.
- Rejected: the skill argument, and `--git-common-dir`, shared by every worktree.
