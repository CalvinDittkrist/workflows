# 0009. The profile is derived from GitHub, with exactly two branch models

Date: 2026-09-18
Status: accepted

## Context
Public and private repositories need different settings (licence, security policy, secret scanning), and some repositories stage releases on `dev`. A per-repository config file would be one more thing to keep in sync and to audit. Spec: #3.

## Decision
The profile is visibility plus branch model, and both are read from GitHub on every run. The branch model is `main` alone, or `dev` plus `main` when the default branch is `dev`. No third level exists and no config file overrides the profile.

## Consequences
The same command works in every repository and cannot disagree with GitHub. Repositories with other branch layouts (release branches, `staging`) must move to one of the two models. A config file per repository was rejected; so was supporting arbitrary branch chains, which would multiply rulesets and promotion logic.
