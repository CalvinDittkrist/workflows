# 0031. The stage measures the context on entry, and the context a handoff started does one unit of work before the next

Date: 2026-09-21
Status: accepted

## Context
[ADR 0029](0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md) puts the checkpoint in two prose steps of the driver skill: at the end of the implementation stage and at the end of the pull request stage. A context that walks the stages in order meets both. A context that does not meets neither.

That is not a corner. A worker started in the worktree of #37 on 2026-09-21 (session `b171e467`, `WF_HANDOFF_TOKENS=1000`) found the branch carrying the work and an open pull request, judged stages 1 to 4 done and invoked `/worker:review` directly. It never ran `checkpoint.sh`, although every checkpoint would have answered `handoff: yes`. The review stage then started at 45.5k, ran three rounds of fixes in that one context and auto-compacted at 167 568 tokens in round 3 — the two stages that decide what gets merged ran in exactly the context ADR 0029 exists to prevent. A session restarted by hand, a `/worker:work` typed after a crash and a forced claim that adopts a remote branch all enter the pipeline the same way, and `/worker:review` can be invoked by the model besides.

The stage the driver enters is also where the context grows: across 28 measured worker sessions, auto-compaction fired six times between 166.5k and 171.3k, and the first review round alone added 38k in the median, about 68k at the 90th percentile and 78k at most. In #50 the checkpoint answered `handoff: no` at 99.3k and the review stage compacted twice. A threshold of 120 000 plus one round does not fit under the trigger.

Moving the measurement to the entrance of a stage costs one guarantee the driver's position gave for free. The context value is per worktree (ADR 0020), so in the first turns of a fresh context it still describes the context that is gone, which is over the threshold by definition. ADR 0029 could note that nothing reads the value in that window because a resumed context started *after* its checkpoint; an entry checkpoint reads it at once, at every threshold, and would hand over again before doing any work. `resume_stage:` cannot be the signal that suppresses it: `facts.sh` prints it for the rest of the worktree's life, and the record names the session that handed over, not the one that received the note.

## Decision
This supersedes ADR 0029 for "two checkpoints" and for its last paragraph. The handover itself — the note, the record, the detached `/clear`, the injection by the hook — stands as it is.

**The stage measures.** `/worker:review` and `/worker:ci` each inject `checkpoint.sh <stage>` the way they inject the other facts of their brief, so the reading is in the context of whoever enters the stage, from the driver or directly, and no model can leave it out. The driver's two prose checkpoints go away: one place measures, not two. On `handoff: yes` the stage does no work at all and hands over with its own stage as the resume stage; on `no` or `unavailable` it runs as before. A context that entered a stage skill directly never read the driver's text, so the answer carries the procedure that answer asks for: `checkpoint.sh <stage>` prints the four sections, the quoted heredoc and the rule to end the turn, the way every script in this repository prints the fix beside the problem. Without a stage argument the script only reports, which is how a maintainer reads the size by hand.

**One skip, bound to one session.** When the SessionStart hook injects a note it records the session it injected it into (`injected_session:`), in the same move that marks the note as spent. `checkpoint.sh <stage>` asks Herdr for the pane's current agent session, as `handoff.sh` does. When that session is the one the note was injected into, the record's stage is this stage, and the skip is not used up yet, it answers `handoff: no` with a reason that says so and marks the skip as used; every later checkpoint of that session measures. The rule is that a context started by a handoff does at least one unit of work before it may hand over again, so every chain of handovers makes progress. A session that merely finds the record — restarted by hand, typed after a crash, a forced claim that adopted the branch — inherits nothing: whatever `resume_stage:` says, its first entry into a stage is measured. Outside a Herdr pane the answer stays `unavailable` and nothing else changes.

A mark the script cannot write is a warning and not a refusal, which is the opposite of the hook's rule for the same record. The invariants differ: the hook fails closed because injecting without the mark would give one note to two contexts, while a checkpoint that failed closed would measure the stale value of the context that is gone and start the very loop the skip exists to prevent. Failing open costs at most a stage that measures late in one session, and it says so on stderr.

**One threshold.** `WF_HANDOFF_TOKENS` keeps its meaning and its single value for both stages, with the default lowered to 100 000: the threshold plus one review round has to stay under the compact trigger, and 100k covers rounds up to about 60k under a trigger of 160k. A stage-specific threshold would need a second number to explain and would not change which stage grows.

The same decision covers two units built under their own tickets: a checkpoint at the end of every review round, with the round state (round number, verdict per reviewer, open findings) in a record a resumed review stage reads (#74), and the repair record the CI stage needs to hand a repair round over (#75). A `waiting` repeat of the CI stage re-runs `pr-wait.sh` and not the skill, so it is no entrance and no checkpoint.

## Consequences
Every entrance into the two stages that decide what gets merged is measured, including the entrances nobody planned: the review stage of the session above would have handed over before its first round instead of compacting in its third.

`checkpoint.sh` now talks to Herdr, which it did not before. It asks only when a record could grant a skip — a note for this stage, injected, unspent — so the ordinary reading stays one file read in the worktree, and a Herdr that does not answer means no skip and a measurement, never an error.

The skip is one per handover and per stage, and it is spent at the first entrance whatever the measurement says. A fresh context that is genuinely over the threshold at its first entrance therefore runs that stage in a large context; the cost is bounded, because the handover that started it has just reset the context, and the next checkpoint of that session measures normally.

The driver no longer holds a copy of the checkpoint, so `/worker:work` cannot drift away from what the stages do. What it keeps is the shape of the pipeline and the rule that a handed-over turn ends without another tool call.

Two records now carry a mark: `injected:` with `injected_session:`, written by the hook, and `skip_used:`, written by the checkpoint. Both are headers prepended in one move, both are read through `wf_record_field`, and the record stays one file the worktree takes with it.
