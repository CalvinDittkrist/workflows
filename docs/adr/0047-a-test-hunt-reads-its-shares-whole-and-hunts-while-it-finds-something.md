# 0047. A test hunt reads its shares whole and hunts while it finds something new

Date: 2026-09-25
Status: accepted
Amends: [0046](0046-a-test-is-removed-at-high-confidence-without-approval-before-the-pull-request.md): the worker checks a `medium` candidate instead of keeping it unread

## Context
The first hunt ([ADR 0045](0045-a-test-hunt-runs-on-a-branch-without-an-issue.md)) removed one test:
- A share was ten files of any size, so hunters sampled instead of reading.
- A round that removed nothing ended the hunt, new candidates or not.
- A `medium` candidate stayed unread ([ADR 0046](0046-a-test-is-removed-at-high-confidence-without-approval-before-the-pull-request.md)).

## Decision
- The files of each directory are packed into shares of at most 1500 lines, and a longer file is split.
- A hunter's share is the test cases that begin in its lines.
- The first round hunts every test file, a later round only files with a new candidate.
- The hunt ends after a round with no new candidate, after three rounds, or when no such file is left.
- Triage records every new `high` and `medium` candidate, so no round proposes it again.
- The worker removes a `high` one unless the test proves something, and a `medium` one only when sure it proves nothing.
- A candidate left in place is kept as checked.
- A hunter searches the whole repository for duplicates.
- The other fixed numbers of 0046 stand.

## Consequences
- A first round spends more hunters.
- A `medium` removal goes through the safeguards of a `high` one.
- A smaller share would spend a hunter's fixed cost, its instructions and `AGENTS.md`, on less code.
- Rejected: grouping tests by the code they call, which no script tells across languages.
