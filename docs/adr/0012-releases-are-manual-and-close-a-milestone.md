# 0012. Releases are manual and close a milestone

Date: 2026-09-18
Status: accepted

## Context
Work is cut into tickets, but nothing groups them into something shippable, and releases were tagged ad hoc. Automatic releases on every merge do not fit repositories that stage work on `dev`. Spec: #3.

## Decision
Milestones are named `vX.Y.Z` and their description states the goal; the planner attaches tickets to one. A manual orchestrator command releases a milestone: it refuses while the milestone has open issues, opens the promotion pull request from `dev` to `main` in the two-level model, then tags, creates the GitHub release with generated notes and closes the milestone.

## Consequences
A milestone maps to one release and its notes. A release never happens by accident, and one with open work is refused. The maintainer must trigger each release. Release on every merge and release-please style automation were rejected for now.
