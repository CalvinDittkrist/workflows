# 0008. make check is the single gate and check the single required status check

Date: 2026-09-18
Status: accepted, amended
Amended by: [0019](0019-the-gate-runs-once-per-review-round.md) (the command stays `make check`; reviewers no longer run it and the worker runs it through a script that records the result), [0041](0041-a-change-class-decides-the-gate-and-the-reviewers-before-the-pull-request.md) (the gate before a factory run's pull request is the gate command of its change class; CI still runs `make check`)

## Context
- Each repository ran tests, linters and builds differently, so an agent discovered the command every time and sometimes ran less than CI.
- The required status checks in rulesets differed per repository.
- Spec: #3.

## Decision
A `Makefile` target `check` runs everything CI gates on, CI runs it in a job named `check`, and that is the only required status check.

## Consequences
- A repository with several CI jobs keeps them parallel, each on its own make target, and adds an aggregating job named `check`.
- Worker and reviewer prompts name `make check`; `check.sh` fails without a `check` target.
- Agents and humans run what CI runs, and one ruleset fits every repository.
- Make is on every CI image and developer machine; its syntax is a small cost.
- The `Makefile` replaces `scripts/test.sh` in this repository.
- Rejected: a configured command per repository, the variation this removes.
