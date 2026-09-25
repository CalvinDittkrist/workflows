# 0015. A spec with tickets is closed by an acceptance

Date: 2026-09-20
Status: accepted

## Context
- A spec cut into tickets stayed open after its last ticket closed, and nothing compared the whole spec with the code.
- With #3 a deviation survived five merged tickets, and a worker then claimed the spec itself (#17).
- Spec: #19.

## Decision
A spec with tickets is closed by an acceptance in the planner, never by a worker.

## Consequences
- The board lists an open `spec` with native sub-issues, none of them open, as ready for acceptance.
- `accept-facts.sh` prints the facts: the spec, its tickets and their merged pull requests, the changed files, and each accepted deviation.
- A read-only checker with a fresh context judges each checkable statement against the base branch.
- Per gap the maintainer files a `ready-for-agent` sub-issue, records an accepted deviation as a comment, or reports no finding.
- The stage closes the spec only when nothing is open, with a comment on what was checked.
- Gaps go through the pipeline as tickets. An accepted deviation is not reported again.
- An acceptance costs one session per spec and needs native sub-issues; the facts script takes ticket numbers otherwise.
- Rejected: a worker closing the spec, and closing it when the last ticket merges. Both drop the comparison with the whole spec.
