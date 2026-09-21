# 0034. The compact trigger is 200 000, and the window is what raises it

Date: 2026-09-21
Status: accepted

## Context
[ADR 0031](0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md) made the workflow own the size at which a worker session compacts: `claim.sh` pins the auto-compact window and the percentage of it at which compaction fires, and derives the **compact trigger** from the two. The numbers it chose were 200 000 and 80, so the trigger was 160 000 tokens.

That ceiling is lower than the one the workflow set out with. The window of 200 000 was inherited from [ADR 0020](0020-the-pane-measures-the-context-and-the-worktree-carries-the-value.md), which took it for the size the session compacts at; once ADR 0031 showed the session compacts at a percentage of it, the 200 000 the workflow had meant all along turned into 160 000. A worker session runs on a model with a window of a million tokens, so nothing about the model asks for a trigger that low.

Two ways to a trigger of 200 000 were on the table.

**Raise the percentage.** It cannot be done: `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` "can't raise the threshold, so values above the default percentage are ignored" (https://code.claude.com/docs/en/env-vars.md, checked 2026-09-21). The default measured in ADR 0031 is 83 % to 86 %, so a window of 200 000 tops out near 168 000 whatever the variable says.

**Hold the model to a 200K window** with `CLAUDE_CODE_DISABLE_1M_CONTEXT=1`, and drop both pinned numbers: "With auto-compaction on, sessions compact at the 200K boundary" (https://code.claude.com/docs/en/model-config.md, checked 2026-09-21). That gives the number up again that ADR 0031 took ownership of. Where under the boundary the session compacts is not documented, the override that would pin it "applies only in sessions that compact before the model's context limit" (env-vars, same page), and the pane would show the context against a window the session does not reach, which is the mistake ADR 0031 corrected. It also turns the trigger into the model's limit: today a turn that crosses the trigger with one large tool result has the rest of the million-token window above it and compacts afterwards, while a session held to 200K has nothing above it and can end in the context-limit error instead.

## Decision
The window is the lever. `claim.sh` sets `autoCompactWindow: 250000` and keeps `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=80`, so the compact trigger it derives and hands to the status line is 200 000 tokens. 250 000 is inside the documented range of the setting, 100K to 1M (model-config, same page).

This supersedes ADR 0031 for its two numbers, the window of 200 000 and the trigger of 160 000. Everything else in it stands: the claim pins both numbers in one place and derives the trigger, 80 stays under the default so it is the percentage that applies, the trigger is an upper bound rather than an exact size, and a test reads all three values out of the settings object of a real claim.

`WF_HANDOFF_TOKENS` keeps its default of 100 000 ([ADR 0032](0032-the-stage-measures-the-context-on-entry-and-a-handoff-grants-one-skip.md)). The handoff is how a worker resets its context, and the trigger is only the safety net under it ([ADR 0029](0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md)); raising the net is no reason to let sessions grow longer before they hand over.

## Consequences
The room between the handoff threshold and the safety net grows from 60 000 to 100 000 tokens. A review round at the 90th percentile (68k, ADR 0032) that starts just under the threshold now ends under the trigger instead of reaching it; several rounds inside one entrance can still reach it, which the round checkpoint of #74 covers.

A session nobody hands over costs more before it compacts: every turn between 160 000 and 200 000 tokens carries up to 40 000 tokens more input than it did. That is the price of the higher ceiling, and it is paid only by sessions the handoff missed.

The measurements of ADR 0031 were taken at windows of 100 000, 150 000 and 200 000. At 150 000 and 200 000 the boundary landed at 80.5 % and 78.9 % of the window, so 250 000 is expected to compact by 200 000 and in practice a little under it; that row is not measured yet, and a `compact_boundary` in a real worker session is what will confirm it.

The deployment ADR 0031 notes as outside its measurements moves further out: a model that runs with a 200K window (Amazon Bedrock, Google Cloud's Agent Platform, Microsoft Foundry, or `CLAUDE_CODE_DISABLE_1M_CONTEXT=1`) has the window capped at its own, "because Claude Code caps that window at the model's context window" (model-config, same page). There the trigger of 200 000 is the model's limit, the percentage may not apply, and the handoff at 100 000 is what keeps the session away from it. The pane stays right, because the status line shows the context against the smaller of the trigger and the model's window.
