# 0047. A test hunt reads its shares whole and hunts while it finds something new

Date: 2026-09-25
Status: accepted

## Context
The first hunt of this repository ([ADR 0045](0045-a-test-hunt-runs-on-a-branch-without-an-issue.md)) removed one test out of about 760 and ended after two of its three rounds. Its record showed three causes:
- A share was at most ten files, whatever their size. One hunter got about 330 tests in over 5000 lines, and another got `tests/test_worker.py`, 2823 lines on its own. With three candidates per share and round, a hunter sampled its share instead of reading it.
- The hunt ended after a round that removed nothing. Round 2 still found two new `medium` candidates, but it ended the hunt.
- A `medium` candidate was recorded as kept, and nobody read it again ([ADR 0046](0046-a-test-is-removed-at-high-confidence-without-approval-before-the-pull-request.md)). Six of the eight candidates the hunters were unsure of stayed in place unread, although the worker reads a test anyway before it removes one.

## Decision
- A share is sized by lines, not by files. The files of each directory are packed in order into shares of at most 1500 lines, and a longer file is split into parts of 1500 lines, each a share of its own. A hunter's share is the test cases that begin in its lines. 1500 lines is what a hunter can read whole together with the code the tests call. A smaller share would spend a hunter's fixed start-up cost (its instructions and `AGENTS.md`) on less code.
- The first round hunts every test file. A later round hunts only the files the round before found a new candidate in, because a share read whole that yielded nothing new has been read.
- The hunt ends when a round finds no new candidate, after three rounds, or when no file with a new candidate is left.
- Triage records every new `high` and `medium` candidate, so no later round proposes it again. It names a `high` one to remove and a `medium` one to check. The worker removes a `high` one unless it finds that the test proves something, and a `medium` one only when it is sure the test proves nothing. A candidate left in place stays kept as checked, and a hunter's repeat of it is neither removed nor checked again.
- Duplicates are searched across the whole repository: a hunter looks for tests in other files that drive the same interface. Grouping tests by the code they call was rejected, because no script can tell that reliably across languages.

The other fixed numbers of ADR 0046 stay: three candidates per share and round, three rounds, five hunters per message and the five categories.

## Consequences
A first round spends more hunters than before, 17 instead of 5 in this repository. Each one reads its whole share, and later rounds hunt only the files where something was found. Nothing is removed without the worker reading it, and a `medium` candidate now goes through the same safeguards as a `high` one: one commit, the reviewer panel and the manual merge.
