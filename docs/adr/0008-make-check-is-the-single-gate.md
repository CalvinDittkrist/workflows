# 0008. make check is the single gate and check the single required status check

Date: 2026-09-18
Status: accepted
Amended by: [0019](0019-the-gate-runs-once-per-review-round.md) (the command stays `make check`; reviewers no longer run it and the worker runs it through a script that records the result)

## Context
Each repository ran its tests, linters and builds differently, so an agent had to discover the command every time and sometimes ran less than CI did. The required status checks in rulesets also differed per repository. Spec: #3.

## Decision
Every repository has a `Makefile` whose `check` target runs everything CI gates on. CI runs `make check` in a job named `check`, and `check` is the only required status check. A repository with several CI jobs keeps them parallel, each calling its own make target, and adds an aggregating job named `check`. Worker and reviewer prompts name `make check` instead of a per-repository command. `check.sh` fails without a `check` target.

## Consequences
Agents and humans run the same command CI runs, and one ruleset fits every repository. Make is present on every CI image and developer machine; its syntax is a small cost. `scripts/test.sh` in this repository is replaced by the `Makefile`. A per-repository configured command was rejected because it is exactly the variation this removes.
