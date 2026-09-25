# 0040. The factory owns the delivery lifecycle in Go and starts one fresh session per stage

Date: 2026-09-23
Status: accepted
Supersedes: [0022](0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)

## Context
- Inside one worker session the agent drove the pipeline: the gate, the reviewers, the rounds, the pull request, CI, the repairs and the outcome.
- Each transition is deterministic, cost tokens and could be skipped or misread. Deterministic work belongs in a program ([ADR 0002](0002-scripts-do-agents-decide.md)). Spec #141.

## Decision
The factory owns the delivery pipeline in Go as fixed stages: implement, gate, review, pr, ci and address-reviews.

- A stage that needs judgement starts one fresh session with a brief of facts and reads one structured result ([ADR 0039](0039-every-session-reports-through-a-structured-result.md)). Other stages run the command or the GitHub call.
- Sessions are never resumed. Reviewers and the pull request author are read-only through the call's tool restriction.
- The configuration holds knobs, never steps. Sessions start with claude only, through the one session type.
- A resumed run reads its stage from git, GitHub and its record, which names its pull request and whether it is the gate's draft.
- A person's change to that draft decides nothing.

## Consequences
- The agent no longer counts, waits or records.
- The checkpoint and the handoff stay terms of the local pipeline.
- The migration runs in steps ([ADR 0043](0043-the-migration-runs-from-the-last-stage-to-the-first.md)).
- Amended by #166: the resume rule first excluded the record. With a gate on CI, GitHub alone cannot tell the gate's draft from a finished pull request.
- Rejected: an executor abstraction or a configurable list of steps.
