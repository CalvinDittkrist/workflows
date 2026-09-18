# 0011. The GitHub workspace is configured by an idempotent script with rulesets and no bypass

Date: 2026-09-18
Status: accepted

## Context
Merge settings, branch protection, labels and security features were set by hand per repository and differ in ways nobody chose. Classic branch protection allows admin bypass and cannot protect tags. Spec: #3.

## Decision
A script derives the profile, reads the current settings and prints the difference to the standard; only with an apply flag does it change them, after storing the previous state as a snapshot. It sets squash-only merges, branch deletion on merge, one branch ruleset on `main` (and `dev`) with required pull request, zero approvals, resolved conversations, required check `check`, linear history, no force push, no deletion and no bypass, a tag ruleset for `pre-standard`, the label vocabulary, Dependabot and a read-only Actions token, plus secret scanning, push protection and private vulnerability reporting on public repositories. It runs only once the `check` job exists.

## Consequences
A second run reports no difference, so the script is also a drift check, and every change is visible before it happens and reversible from the snapshot. No bypass means the maintainer merges through pull requests too. Configuring by hand and using classic branch protection were rejected.
