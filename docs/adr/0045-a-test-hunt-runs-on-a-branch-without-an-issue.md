# 0045. A test hunt runs on a branch without an issue

Date: 2026-09-24
Status: accepted

## Context
A repository collects tests that prove nothing: tests that grep the source, tests that cannot fail, duplicates, tests whose mock replaces the thing under test, and tests that assert incidental output. The worker's test reviewer judges only the tests a branch changes, and nothing looks at a whole test suite. What a hunt through the suite removes is known only at its end, so no issue can describe it beforehand. Every worker session so far has been bound to an issue: its branch `<type>/<n>-<slug>` is the contract the worker hook, the board and the factory read. Spec #170.

## Decision
`/orchestrator:hunt-tests` opens a worktree of the branch `hunt/tests-<date>` and starts a worker session with the settings of a manual claim and `/worker:hunt-tests` as its first prompt. No issue is read or created. The branch is the unit of work, and the pull request at its end is its trace.

The branch carries no issue number. The worker hook stays silent on it, except to hand a waiting handoff note to a fresh context. The status line says `no issue`. The factory's branch contract does not match the branch, so the factory neither works nor resumes it. The board lists the worktree with `hunt` in the issue column, the way it lists a planning session. It is merged by its pull request number and abandoned by its branch name. One hunt is open at a time, on this clone and on origin.

The hunt record in the worktree's git directory stands where the issue stands for a ticket ([ADR 0018](0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md)). `hunt.sh print` prints it into the diff context that the reviewers and the pull request author read, and the scripts that print the issue print `none`. The pull request carries no closing keyword.

The hunt is local only: it never runs in the factory and never on a path given as an argument.

## Consequences
From the review stage on, a hunt runs through the same pipeline as a ticket: review, pull request, CI and handoff. The review, pull request and CI stages need no issue. A hunt that removes nothing opens no pull request, and its worktree stays until the maintainer abandons it. The drift between the two plugins' copies of the test-file rule is bound by a test.
