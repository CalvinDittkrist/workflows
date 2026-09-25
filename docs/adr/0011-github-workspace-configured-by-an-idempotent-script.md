# 0011. The GitHub workspace is configured by an idempotent script with rulesets and no bypass

Date: 2026-09-18
Status: accepted; the promotion merge method is superseded by [ADR 0013](0013-promotions-merge-with-a-merge-commit-and-releases-tag-it.md)

## Context
- Merge settings, branch protection, labels and security features were set by hand per repository and differ in ways nobody chose.
- Classic branch protection allows admin bypass and cannot protect tags.
- Spec: #3.

## Decision
A script prints the difference to the standard, and only an apply flag makes it change the settings, after it stores a snapshot.

## Consequences
- It derives the profile first, and the snapshot is the previous state.
- It sets squash-only merges, branch deletion on merge, the label vocabulary, Dependabot and a read-only Actions token.
- One branch ruleset on `main` (and `dev`) requires a pull request, zero approvals, resolved conversations, the check `check` and linear history.
- It forbids force push and deletion and allows no bypass.
- A tag ruleset protects `pre-standard`.
- Public repositories get secret scanning, push protection and private vulnerability reporting.
- It runs only once the `check` job exists.
- A second run reports no difference, so the script is also a drift check.
- Every change is visible first and reversible from the snapshot. The maintainer merges through pull requests too.
- Rejected: configuring by hand, and classic branch protection.
