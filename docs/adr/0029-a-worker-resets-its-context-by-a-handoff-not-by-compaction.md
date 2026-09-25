# 0029. A worker resets its context by a handoff at a checkpoint, not by compaction

Date: 2026-09-21
Status: accepted; the two checkpoints of the driver, the default threshold and the note on the context value after a handover are superseded by [ADR 0032](0032-the-stage-measures-the-context-on-entry-and-a-handoff-grants-one-skip.md)

## Context
- A worker's context only grows: sessions of [ADR 0017](0017-worker-subagents-run-in-the-foreground.md) reached 412k tokens.
- The pipeline's state lives in the branch, GitHub and the worktree's git directory ([ADR 0018](0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md)), not the transcript.
- `/clear` in the pane empties the context under a new session id and keeps the process.

## Decision
A worker hands its pipeline over to a fresh context in the same pane at a stage boundary, and never compacts on purpose.

## Consequences
- The driver runs `checkpoint.sh` after the implementation and the pull request stages.
- `handoff.sh` refuses a dirty tree and a handoff note missing one of its four sections.
- A detached `handoff-resume.sh` sends `/clear` only into the asking session after its turn, reading the pane before every keystroke.
- It drives the pane only after a new session id and an injected note, else notifies the maintainer, who can send `/worker:work`.
- The hook injects the note once, as data.
- `facts.sh` prints `resume_stage:`. The note reaches no reviewer, whose brief is the diff, the gate record and the panel record ([ADR 0004](0004-reviewers-as-fresh-read-only-subagents.md)).
- `WF_HANDOFF_TOKENS` (default 120 000) sits under the compact trigger ([ADR 0031](0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md)).
- Briefly after a handover the context value describes the old context, unread.
- Rejected: auto-compaction, which summarises dead ends too, at a moment nobody chooses.
