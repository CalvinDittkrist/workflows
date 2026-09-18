# 0010. Standardisation audits read-only, deletes through a pull request and backs up with a protected tag

Date: 2026-09-18
Status: accepted

## Context
Bringing an existing repository to the standard removes files a maintainer may still want, such as a skill written for that repository. Judgement about what goes should be independent per area and must not change anything before the maintainer approves. Spec: #3.

## Decision
A main-session agent gathers facts with a script and fans out to six read-only auditors (files, agent configuration, docs, tests and CI, GitHub workspace, security); read-only is expressed through their tool lists. Findings are approved per category. Before anything changes, a tag `pre-standard` is pushed on the current head and protected against deletion and moving, and a catalogue issue labelled `skill-candidate` lists each removed skill with the command that restores it. All deletions and baseline files arrive in one pull request from `chore/standardize`. Code findings become issues for the worker pipeline.

## Consequences
Nothing is lost, every removal is reviewed like other changes, and a removed skill can later move into the marketplace. A run is repeatable and doubles as a drift check. The cost is one extra pull request and issue per repository. Deleting directly on the default branch and keeping a per-repository allowlist of local skills were rejected.
