# 0046. A test is removed at high confidence without a person's approval before the pull request

Date: 2026-09-24
Status: accepted

## Context
A test hunt ([ADR 0045](0045-a-test-hunt-runs-on-a-branch-without-an-issue.md)) sends read-only hunters through a repository's tests. Asking the maintainer about each candidate would stop an unattended session once per test, and the maintainer would judge each removal with less context than the pull request gives. Removing a test that did prove something loses evidence silently. Spec #170.

## Decision
The worker removes every candidate a hunter names with `high` confidence, after reading the test itself, and asks no one before the pull request. A `medium` candidate is recorded as checked and kept, and a `low` one is dropped. Only five categories justify a removal: `source-inspection`, `cannot-fail`, `duplicate`, `mocks-subject` and `incidental`. They are fixed in the workflow, as are the three rounds, the three candidates per share and round, the five hunters per message and the threshold `high`; no repository can configure them. The hunt removes and never replaces. A hunter has no shell. A candidate that names a file outside the test-file conventions is refused by the script, whatever its reasoning.

The safeguards come after the removal:
- One commit per removed test, recorded with a plain-language reason, so a single removal can be reverted.
- The reviewer panel with the test reviewer, which reads the removals like any diff.
- A pull request that lists every removal with its reason and whether another test still proves the behaviour, and the kept candidates in a section of their own.
- The maintainer's manual merge: a hunt runs in manual mode only.

## Consequences
A hunt runs to its pull request without a person. A wrong removal costs a revert of one commit, and it is visible in the body before the merge. The kept candidates tell the maintainer what the hunt was unsure about, at no cost in code.
