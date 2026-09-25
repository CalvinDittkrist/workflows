# 0032. The stage measures the context on entry, and the context a handoff started does one unit of work before the next

Date: 2026-09-21
Status: accepted

## Context
- [ADR 0029](0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md) checks in two driver steps, which a worker entering `/worker:review` directly skips.
- The context value is per worktree, so a fresh context first reads its predecessor's size.

## Decision
`/worker:review` and `/worker:ci` measure the context on entry, and a context a handoff started does one unit of work before handing over again.

## Consequences
- This supersedes ADR 0029 for its two checkpoints and its last paragraph; the handover stands.
- Each stage injects `checkpoint.sh <stage>` among its facts, and on `handoff: yes` hands over with itself as resume stage.
- `/worker:work` keeps no copy of the checkpoint, so it cannot drift from the stages.
- The hook records `injected_session:`, and the first entry of that session into the note's stage skips once, marked `skip_used:`.
- The skip is denied only to a provably different session, and an unwritable mark is a warning.
- `WF_HANDOFF_TOKENS` defaults to 100 000, so one review round fits under the compact trigger ([ADR 0031](0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md)).
- The number is kept in the README, [the token budget](../token-budget.md) and here; ADR 0029's `Status:` names this, as [ADR 0011](0011-github-workspace-configured-by-an-idempotent-script.md) names [ADR 0013](0013-promotions-merge-with-a-merge-commit-and-releases-tag-it.md).
- A checkpoint after every review round, with its open findings, is #74; the repair record of the CI stage is #75.
- Rejected: a threshold per stage, a second number to explain.
