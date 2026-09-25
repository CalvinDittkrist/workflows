# 0019. The gate runs once per review, and its result is a fact in the brief

Date: 2026-09-21
Status: accepted; amended 2026-09-22 (once per review, not per round; CI gates the repair pushes); amended 2026-09-23 (the gate outlasts the tool call)
Amends: [0004](0004-reviewers-as-fresh-read-only-subagents.md) (reviewers stay fresh, read-only contexts; only their most expensive command becomes a briefing fact)

## Context
- Every reviewer and the pull request author ran the gate on its own.
- Parallel gates in one worktree compete for CPU and disturb each other's tests.
- The worker has the result before the review starts, so reviewers re-derived facts it had.

## Decision
The gate runs once per review through `gate.sh run`, which runs `make check` ([ADR 0008](0008-make-check-is-the-single-gate.md)) and records a fact ([ADR 0018](0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md)).

## Consequences
- It runs in the worker's main context: on the work stage's last commit and before the panel summary. No fix, repair or merge commit runs it (#57).
- A review round costs no gate; CI gates repair pushes and is the one independent gate.
- `gate.sh verdict` answers for a clean current head only, and nobody types the gate by hand, so the record cannot drift.
- Reviewers get the block verbatim, verify with single tests and report a missing result as a finding.
- The gate runs detached past a tool call's [ceiling](https://code.claude.com/docs/en/env-vars.md) (#106, #108), in `WF_WAIT_SLICE` slices like `/worker:ci` ([ADR 0017](0017-worker-subagents-run-in-the-foreground.md)).
- It ends with its worker, so the factory leaves none behind.
- Rejected: raising `BASH_MAX_TIMEOUT_MS`, which a [plugin](https://code.claude.com/docs/en/plugins-reference.md) cannot set.
