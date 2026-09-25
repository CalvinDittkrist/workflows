# 0031. The workflow pins the size at which a worker session compacts

Date: 2026-09-21
Status: accepted; the window of 200 000 and the trigger of 160 000 are superseded by [ADR 0034](0034-the-compact-trigger-is-raised-through-the-window.md)

## Context
- [ADR 0020](0020-the-pane-measures-the-context-and-the-worktree-carries-the-value.md) took `autoCompactWindow: 200000` for the size a session compacts at, and [ADR 0029](0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md) chose its handoff threshold against it.
- Sessions compact at a percentage of the window ([model-config](https://code.claude.com/docs/en/model-config.md)): in [#37](https://github.com/CalvinDittkrist/workflows/issues/37), [#50](https://github.com/CalvinDittkrist/workflows/issues/50) and [#44](https://github.com/CalvinDittkrist/workflows/issues/44) at 83 % to 86 %.
- `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` can only lower it ([env-vars](https://code.claude.com/docs/en/env-vars.md)), and its default is documented nowhere.

## Decision
`claim.sh` pins a window of 200 000 and 80 % of it as the compact trigger, 160 000, superseding ADR 0020's 200 000 safety net.

## Consequences
- The status line shows the context against the smaller of the trigger and the model's window, in the pane and in Docker.
- `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=80` is under the default, so it applies, and 160 000 stays an upper bound.
- The context value and the contract with `checkpoint.sh` ([ADR 0018](0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md)) are unchanged. Only worker sessions get the numbers.
- `WF_HANDOFF_TOKENS` (default 120 000) sits 40 000 under the trigger.
- A test reads all three numbers from a real claim, so a drift fails the gate.
- With a 200K model window the percentage may not apply; the handoff fires first.
- Rejected: relying on the undocumented default, which a release could move unnoticed.
