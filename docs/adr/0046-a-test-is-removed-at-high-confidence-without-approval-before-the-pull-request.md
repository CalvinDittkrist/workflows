# 0046. A test is removed at high confidence without a person's approval before the pull request

Date: 2026-09-24
Status: accepted, amended
Amended by: [0047](0047-a-test-hunt-reads-its-shares-whole-and-hunts-while-it-finds-something.md)

## Context
- A test hunt ([ADR 0045](0045-a-test-hunt-runs-on-a-branch-without-an-issue.md)) sends read-only hunters through the tests. Spec #170.
- Removing a test that proved something loses evidence silently.

## Decision
The worker removes every candidate a hunter names with `high` confidence, after reading the test, and asks no one before the pull request. A `medium` candidate is kept as checked, and a `low` one dropped.

- Only five categories justify a removal: `source-inspection`, `cannot-fail`, `duplicate`, `mocks-subject` and `incidental`.
- They are fixed, as are three rounds, three candidates per share and round, five hunters per message and the threshold.
- The hunt removes and never replaces. A hunter has no shell.
- The script refuses a candidate outside the test-file conventions.

The safeguards come after the removal:
- one commit per removed test, with a plain reason;
- the reviewer panel with the test reviewer;
- a pull request listing every removal, whether another test still proves the behaviour, and the kept candidates;
- the maintainer's manual merge, since a hunt runs in manual mode only.

## Consequences
- A hunt runs to its pull request without a person.
- A wrong removal costs a revert of one commit, visible before the merge.
- Rejected: asking the maintainer per candidate, which stops an unattended session once per test with less context than the pull request.
