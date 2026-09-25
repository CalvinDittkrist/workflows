# 0009. The profile is derived from GitHub, with exactly two branch models

Date: 2026-09-18
Status: accepted

## Context
- Public and private repositories need different settings: licence, security policy, secret scanning.
- Some repositories stage releases on `dev`.
- A config file per repository would be one more thing to keep in sync and to audit.
- Spec: #3.

## Decision
The profile is visibility plus branch model, read from GitHub on every run; the branch model is `main` alone, or `dev` plus `main`.

## Consequences
- The model is `dev` plus `main` when the default branch is `dev`.
- No third level exists, and no config file overrides the profile.
- The same command works in every repository and cannot disagree with GitHub.
- Repositories with other layouts, such as release branches or `staging`, must move to one of the two models.
- Rejected: a config file per repository.
- Rejected: arbitrary branch chains, which multiply rulesets and promotion logic.
