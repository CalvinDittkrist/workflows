# 0031. The workflow pins the size at which a worker session compacts

Date: 2026-09-21
Status: accepted; the window of 200 000 and the trigger of 160 000 are superseded by [ADR 0034](0034-the-compact-trigger-is-raised-through-the-window.md)

## Context
[ADR 0020](0020-the-pane-measures-the-context-and-the-worktree-carries-the-value.md) put `autoCompactWindow: 200000` under a claimed worker session and treated that number as the size the session compacts at. The pane showed `78k/200k (39%)`, the token budget said the same, and the handoff threshold of [ADR 0029](0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md) was chosen against it.

It is not that size. The window is a ceiling the session compacts below: "The auto-compact window is how full the context window can get before Claude Code compacts the conversation" (https://code.claude.com/docs/en/model-config.md, checked 2026-09-21), and how full is a percentage of it, not all of it. Six automatic compactions in worker sessions of this repository — [#37](https://github.com/CalvinDittkrist/workflows/issues/37) three times, [#50](https://github.com/CalvinDittkrist/workflows/issues/50) twice, [#44](https://github.com/CalvinDittkrist/workflows/issues/44) once — fired at 166 512, 167 303, 167 568, 167 711, 168 702 and 171 312 tokens: 83.3 % to 85.7 % of the window, never near it. So every number the workflow stated about compaction was some 30k too high, and the pane showed the size against a limit the session never reaches.

That the percentage exists at all is documented only through the variable that changes it: `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` "Set the percentage (1-100) of the auto-compact window at which auto-compaction triggers. Use lower values like `50` to compact earlier; the variable can't raise the threshold, so values above the default percentage are ignored" (https://code.claude.com/docs/en/env-vars.md, checked 2026-09-21). The default it compares against is named nowhere. A release could move it, every statement in this repository would be wrong again, and nothing in the workflow would notice.

## Decision
This supersedes ADR 0020 for its sentences about the 200 000 safety net. The workflow owns the size at which a worker session compacts, and derives everything it says about it from that one place.

`claim.sh` pins two numbers next to each other: the auto-compact window, 200 000, and the percentage of it at which compaction fires, `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=80` in the session's `env`. Their product is the **compact trigger**, 160 000 tokens, and that is what the claim hands to the status line, which shows the context against the smaller of it and the model's window (`78k/160k`). Both ways the orchestrator starts a worker session — the Herdr pane and the Docker sandbox — send the same settings object, so both carry both numbers.

80 is under the measured default, which is what makes it the percentage that applies: the override can only lower, and a value above the default is dropped in silence. The workflow therefore states a number it set rather than one it guessed, and a release that raises the default changes nothing here. A release that lowers it under 80 would drop the override instead and compact the session earlier, which leaves 160 000 what it is throughout this decision: an upper bound.

Nothing else changes. The status line writes the same context value (`total_input_tokens`, `context_window_size`, the time), so its contract with the worker's `checkpoint.sh` ([ADR 0018](0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md), ADR 0020) is untouched, and a status line started without the argument behaves as before. Planning and orchestrator sessions get neither number: only worker sessions grow long enough for it to matter.

## Consequences
The handoff threshold has a real ceiling to sit under: `WF_HANDOFF_TOKENS` (default 120 000) is 40 000 tokens below the trigger rather than 80 000 below a number nothing enforces. The margin is smaller than it looked, which is the point — it was always this small.

The two numbers can drift from the trigger only if someone writes the trigger by hand, so the claim derives it and never writes it. A test reads all three out of the settings object a real claim starts the session with — 200 000, `"80"` and the status line's 160 000 — so a change to any one of them that does not carry the other two fails the gate.

Pinning the percentage costs a little of the window: a session that would have compacted at 83 % or more now compacts at 80 %, some 10 000 tokens earlier. That is the price of a number the workflow can state.

The trigger is a ceiling, not a promise that a session compacts at exactly 160 000. The check runs on the turn that would cross it, so a boundary lands at or just under the number, and a window at the documented minimum of 100 000 compacts earlier still — measured at 66 471, 66.5 %, where the pinned percentage is not what binds. What the workflow relies on is the upper bound, which is what the pane and the handoff threshold need.

The bound also survives the release this decision cannot see. Were a later Claude Code to lower its default percentage under 80, the override would be ignored and the session would compact under 160 000, not over it: the pane would overstate the headroom, and the handoff at 120 000 would still fire first until a default under 60 %. Nothing here detects that, because Claude Code publishes no effective percentage and the override fails silently by design; it would show up the way the undocumented default showed up here, as a `compact_boundary` below the number in a real session. A threshold moved towards the trigger spends that room, which is why the READMEs say to keep it under the trigger rather than at it.

That bound is measured, not assumed. Real headless sessions grown in 18k steps until Claude Code compacted them, with the window and the override passed exactly as the claim passes them:

| `autoCompactWindow` | `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` | `compact_boundary` (`trigger: auto`) | share of the window |
| --- | --- | --- | --- |
| 200 000 | 80 | 157 713 | 78.9 % |
| 150 000 | 80 | 120 716 | 80.5 % |
| 100 000 | 80 | 66 471 | 66.5 % |
| 100 000 | 50 | 48 156 | 48.2 % |
| 200 000 | unset (the six worker sessions above) | 166 512 – 171 312 | 83.3 – 85.7 % |

The override is therefore read from the session's `env` and does lower the trigger, and at the production window it moves the boundary from about 168k to about 158k. A later Claude Code release that reads it differently would show up the same way, in a session, and not in a document.

One deployment is outside what the measurements cover: the override "applies only in sessions that compact before the model's context limit" (env-vars, same page), and a model that runs with a 200K window — Amazon Bedrock, Google Cloud's Agent Platform, Microsoft Foundry, or `CLAUDE_CODE_DISABLE_1M_CONTEXT=1`, per model-config — has no room below the window the claim sets. There the percentage may not apply and the session compacts nearer 200k than 160k, so the pane understates the ceiling instead of overstating it. Nothing is lost by it: the handoff threshold at 120 000 fires long before either number, which is why this stays a note rather than a second window for that case.
