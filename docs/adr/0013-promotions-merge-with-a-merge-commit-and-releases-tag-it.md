# 0013. Promotions merge with a merge commit, and the release tags it

Date: 2026-09-18
Status: accepted

## Context
- [ADR 0012](0012-releases-are-manual-and-close-a-milestone.md) makes releases manual but leaves open how the command finds the promotion, what it tags and how the promotion merges.
- [ADR 0011](0011-github-workspace-configured-by-an-idempotent-script.md) sets squash-only merges and linear history on `main` and `dev`.
- A squashed promotion gives `main` a commit `dev` never gets, so later promotions repeat earlier commits and can conflict.
- Implemented in #8.

## Decision
`merge.sh` merges a promotion PR with a merge commit and keeps `dev`, and `/orchestrator:release vX.Y.Z` tags that merge commit; this refines ADR 0012 and supersedes ADR 0011 for promotions only.

## Consequences
- The planner asks once per ticket batch for a milestone; the board shows each frontier issue's milestone.
- `release.sh` refuses a missing or closed milestone, open issues or an existing tag. With `main` alone it tags the head of `main`.
- With `dev` plus `main` it opens or finds the PR `chore(release): vX.Y.Z` and stops with `status: waiting` until it merges.
- `gh release create --target <sha> --generate-notes` tags; then the milestone closes, or the script says to close it by hand.
- The two-level model allows merge commits on `main`; `dev` keeps linear history and feature PRs are squashed.
- A release keeps no state; running the command again after the merge resumes it, and the orchestrator session stays usable meanwhile.
- Rejected: squashing the promotion, polling until the merge, and releasing when the last milestone issue closes.
