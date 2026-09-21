# 0031. The workflow pins the size at which a worker session compacts

Date: 2026-09-21
Status: accepted

## Context
[ADR 0020](0020-the-pane-measures-the-context-and-the-worktree-carries-the-value.md) put `autoCompactWindow: 200000` under a claimed worker session and treated that number as the size the session compacts at. The pane showed `78k/200k (39%)`, the token budget said the same, and the handoff threshold of [ADR 0029](0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md) was chosen against it.

It is not that size. The window is the base of a percentage: "Compaction occurs when the conversation reaches the percentage of the context window specified by `autoCompactWindow`" (https://code.claude.com/docs/en/model-config.md, checked 2026-09-21). Six automatic compactions in worker sessions of this repository — [#37](https://github.com/CalvinDittkrist/workflows/issues/37) three times, [#50](https://github.com/CalvinDittkrist/workflows/issues/50) twice, [#44](https://github.com/CalvinDittkrist/workflows/issues/44) once — fired between 166.5k and 171.3k tokens, about 83 % of the window. So every number the workflow stated about compaction was 40k too high, and the pane showed the size against a limit the session never reaches.

The percentage itself is Claude Code's, and it is not documented: `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` "Set the percentage (1-100) of the auto-compact window at which auto-compaction triggers … the variable can't raise the threshold, so values above the default percentage are ignored" (https://code.claude.com/docs/en/env-vars.md, checked 2026-09-21), and the default it compares against is named nowhere. A release could move it, every statement in this repository would be wrong again, and nothing in the workflow would notice.

## Decision
This supersedes ADR 0020 for its sentences about the 200 000 safety net. The workflow owns the size at which a worker session compacts, and derives everything it says about it from that one place.

`claim.sh` pins two numbers next to each other: the auto-compact window, 200 000, and the percentage of it at which compaction fires, `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=80` in the session's `env`. Their product is the **compact trigger**, 160 000 tokens, and that is what the claim hands to the status line, which shows the context against the smaller of it and the model's window (`78k/160k`). Both ways the orchestrator starts a worker session — the Herdr pane and the Docker sandbox — send the same settings object, so both carry both numbers.

80 is under the measured default, which is what makes it the percentage that applies: the override can only lower, and a value above the default is dropped in silence. The workflow therefore states a number it set rather than one it guessed, and a release that moves the default moves nothing here.

Nothing else changes. The status line writes the same context value (`total_input_tokens`, `context_window_size`, the time), so its contract with the worker's `checkpoint.sh` ([ADR 0018](0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md), ADR 0020) is untouched, and a status line started without the argument behaves as before. Planning and orchestrator sessions get neither number: only worker sessions grow long enough for it to matter.

## Consequences
The handoff threshold has a real ceiling to sit under: `WF_HANDOFF_TOKENS` (default 120 000) is 40 000 tokens below the trigger rather than 80 000 below a number nothing enforces. The margin is smaller than it looked, which is the point — it was always this small.

The two numbers can drift from the trigger only if someone writes the trigger by hand; a test asserts that the argument the claim passes to the status line is the window times the percentage of the same settings object, so a literal that stops matching fails the gate.

Pinning the percentage costs a little of the window: a session that would have compacted at 83 % now compacts at 80 %, about 6 000 tokens earlier. That is the price of a number the workflow can state.

The measurement stays honest only as long as someone takes it. The evidence for this decision is a real session whose `compact_boundary` fired at the pinned percentage of a deliberately lowered window; a later Claude Code release that reads the override differently would show up the same way, in a session, and not in a document.
