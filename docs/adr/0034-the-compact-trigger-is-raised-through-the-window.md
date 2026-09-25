# 0034. The compact trigger is 200 000, and the window is what raises it

Date: 2026-09-21
Status: accepted

## Context
- [ADR 0031](0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md) pinned a window of 200 000 at 80 %, a compact trigger of 160 000.
- The 200 000 came from [ADR 0020](0020-the-pane-measures-the-context-and-the-worktree-carries-the-value.md), which took the window for the compaction size.
- A worker's model has a window of a million tokens, so nothing asks for a trigger that low.

## Decision
`claim.sh` sets `autoCompactWindow: 250000` and keeps 80 %, so the compact trigger is 200 000; this supersedes the two numbers of ADR 0031.

## Consequences
- 250 000 is inside the documented range of 100K to 1M ([model-config](https://code.claude.com/docs/en/model-config.md)).
- The rest of ADR 0031 stands: one place pins both numbers, derives the trigger as an upper bound, and a test reads them.
- `WF_HANDOFF_TOKENS` keeps 100 000 ([ADR 0032](0032-the-stage-measures-the-context-on-entry-and-a-handoff-grants-one-skip.md)), because the handoff, not the trigger, resets the context ([ADR 0029](0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md)).
- A review round at the 90th percentile now ends under the trigger. The round checkpoint of #74 covers several rounds.
- A session the handoff missed pays up to 40 000 more input tokens per turn above 160 000.
- The boundary at 250 000 is unmeasured; a real `compact_boundary` will confirm it.
- A model with a 200K window caps the window at its own, and the handoff keeps the session from that limit.
- Rejected: raising the percentage, which the override cannot do ([env-vars](https://code.claude.com/docs/en/env-vars.md)).
- Rejected: `CLAUDE_CODE_DISABLE_1M_CONTEXT=1`, which makes the trigger the model's limit and risks the context-limit error.
