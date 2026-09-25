# 0041. A change class decides the gate and the reviewers before the pull request, and CI remains the full gate

Date: 2026-09-23
Status: accepted
Amends: [0008](0008-make-check-is-the-single-gate.md): the gate before a factory run's pull request; CI still runs `make check`

## Context
- Every change paid for the full gate and five reviewers before its pull request, a documentation change included.
- CI runs the full gate on every pull request anyway, as the required check ([ADR 0008](0008-make-check-is-the-single-gate.md)). Spec #141.

## Decision
A connected repository may carry an ordered list of change classes. Each has a name, path patterns, a gate command as an argument list (empty for none) and optionally the reviewers.

- A class applies when every changed file matches one of its patterns; the first one wins.
- Otherwise the built-in class `full` applies: the repository's gate and all five reviewers.
- The factory decides the class before each gate and on the final head, and records it on the run and the dashboard.
- CI remains the full gate.

## Consequences
- Cheap changes stay cheap, under a rule the maintainer writes per repository.
- A class that is too wide lets a failure through to the ci stage, where it costs a repair round.
- The local pipeline still gates with `make check`.
- A class without a name, a bad pattern or a gate command that is not a list is refused with the fix.
- Rejected: the full gate and panel for every change, which pays twice for what CI checks anyway.
