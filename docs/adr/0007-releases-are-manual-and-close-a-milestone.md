# 0007. Releases are manual and close a milestone

Date: 2026-09-18
Status: accepted

## Context
Issues are the unit of work and PRs the unit of delivery, but nothing grouped finished work into something shippable. The repository standard (#3) names milestones `vX.Y.Z` with the goal as description and allows two branch models: `main` alone, or `dev` plus `main`. A release has to fit both models and must never happen because an agent decided on its own that it was time (#8).

## Decision
A milestone is the unit of a release. The planner asks once per ticket batch which milestone the tickets belong to (an open one, a new `vX.Y.Z`, or none) and attaches them through `issue.sh`. The board shows each frontier issue's milestone.

`/orchestrator:release vX.Y.Z` runs `release.sh`. The skill has `disable-model-invocation`, so only the user starts a release. The script refuses when the milestone does not exist, is closed, has open issues, or the tag exists. With `main` alone it tags the head of `main`. With `dev` plus `main` it opens, or finds by its title `chore(release): vX.Y.Z`, the promotion PR from `dev` to `main`, and stops with `status: waiting` until that PR is merged. On a later run it tags the promotion's merge commit. The tag comes from `gh release create --target <sha> --generate-notes`, and then the milestone is closed. `merge.sh` squash-merges a promotion PR like any other but keeps `dev` and `main` and does not touch their checkouts.

## Consequences
A release is repeatable and has no state of its own: the milestone, the promotion PR title and the tag are all it reads, so an interrupted release resumes by running the command again. Tagging the merge commit rather than the head of `main` keeps a later push to `main` out of the release. The script does not block while the promotion PR waits for CI and a merge, so the orchestrator stays usable in that time. The cost is a second command after the merge. Rejected: polling until the PR is merged (the orchestrator session would stay blocked until someone merges, often longer than a tool call may run) and triggering the release when the last milestone issue closes (the release is a decision the user makes).
