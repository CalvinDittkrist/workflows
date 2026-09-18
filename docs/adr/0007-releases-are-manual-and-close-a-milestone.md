# 0007. Releases are manual and close a milestone

Date: 2026-09-18
Status: accepted

## Context
Issues are the unit of work and PRs the unit of delivery, but nothing grouped finished work into something shippable. The repository standard (#3) names milestones `vX.Y.Z` with the goal as description and allows two branch models: `main` alone, or `dev` plus `main`. A release has to fit both models and must never happen because an agent decided on its own that it was time (#8).

## Decision
A milestone is the unit of a release. The planner asks once per ticket batch which milestone the tickets belong to (an open one, a new `vX.Y.Z`, or none) and attaches them through `issue.sh`. The board shows each frontier issue's milestone.

`/orchestrator:release vX.Y.Z` runs `release.sh`. The skill has `disable-model-invocation`, so only the user starts a release. The script refuses when the milestone does not exist, is closed, has open issues, or the tag exists. With `main` alone it tags the head of `main`. With `dev` plus `main` it opens, or finds by its title `chore(release): vX.Y.Z`, the promotion PR from this repository's `dev` to `main` (pull requests from forks are ignored), and stops with `status: waiting` until that PR is merged. On a later run it tags the promotion's merge commit. The tag comes from `gh release create --target <sha> --generate-notes`, and then the milestone is closed. `merge.sh` merges a promotion PR with a merge commit, not a squash, and keeps `dev`. That way `dev` stays an ancestor of `main`, the next promotion carries only new commits, and generated notes list the feature PRs merged into `dev`. The ruleset on `main` must therefore allow merge commits and must not require linear history. Feature PRs are still squash-merged.

## Consequences
A release keeps no state of its own. It reads the milestone, the promotion PR title and the tag, so a release waiting for its promotion resumes by running the command again. If publishing succeeds but closing the milestone fails, the milestone must be closed by hand; the script says so. Tagging the merge commit rather than the head of `main` keeps a later push to `main` out of the release. The script does not block while the promotion PR waits for CI and a merge, so the orchestrator stays usable in that time. The cost is a second command after the merge. Rejected: squash-merging the promotion (it cuts `dev` off from `main`, so every later promotion repeats earlier commits and can conflict); polling until the PR is merged (the orchestrator session would stay blocked until someone merges, often longer than a tool call may run) and triggering the release when the last milestone issue closes (the release is a decision the user makes).
