# 0010. Standardisation audits read-only, deletes through a pull request and backs up with a protected tag

Date: 2026-09-18
Status: accepted

## Context
- Bringing a repository to the standard removes files a maintainer may still want, such as a skill written for it.
- Judgement on what goes should be independent per area and change nothing before the maintainer approves.
- Spec: #3.

## Decision
Read-only auditors judge the repository, the maintainer approves per category, a protected tag backs up the head, and deletions arrive in one pull request.

## Consequences
- A main-session agent gathers facts with a script. Six auditors cover files, agent configuration, docs, tests and CI, GitHub workspace, and security, read-only through their tool lists.
- The tag `pre-standard` is pushed on the head and protected against deletion and moving.
- A catalogue issue labelled `skill-candidate` lists each removed skill with its restore command.
- The pull request comes from `chore/standardize`; code findings become issues.
- Nothing is lost, and every removal is reviewed.
- A run is repeatable and doubles as a drift check. It costs one pull request and issue per repository.
- Repositories no longer override plugin agents or skills, which amends ADR 0004; changes go into the plugin or `WF_*` settings.
- Rejected: deleting on the default branch, and an allowlist of local skills per repository.
