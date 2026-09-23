# 0041. A change class decides the gate and the reviewers before the pull request, and CI remains the full gate

Date: 2026-09-23
Status: accepted
Amends: [0008](0008-make-check-is-the-single-gate.md) (the gate before the pull request of a factory run; `make check` stays the gate CI runs)

## Context
Every change paid for the full gate and five reviewers before its pull request, whatever it touched. A documentation change ran the Go tests and the browser test and was read by a correctness reviewer. CI runs the full gate on every pull request anyway, and it is the required check ([ADR 0008](0008-make-check-is-the-single-gate.md)). Spec #141.

## Decision
A connected repository may carry an ordered list of change classes. Each class has a name, path patterns, a gate command as an argument list (an empty list means no gate) and, optionally, the reviewers. A class applies when every file changed between the merge base and the head matches one of its patterns; the first class that applies wins. When none applies, the built-in class `full` applies: the repository's single gate command and all five reviewers.

The factory determines the class before each gate, again on the final head, records it on the run and shows it on the dashboard. The gate before the pull request is the class's gate command. CI remains the full gate on every pull request, so what a class skips is caught in the ci stage and repaired within the repair budget.

## Consequences
Cheap changes stay cheap, under a rule the maintainer writes per repository. A class that is too wide lets a failure through to CI, where it costs a repair round rather than a merge. `make check` stays the one command CI runs and the command of the class `full`. The local pipeline is unchanged and still gates with `make check`.

A configuration that names a class without a name, a pattern that is not a pattern, or a gate command that is not a list is refused with the fix.
