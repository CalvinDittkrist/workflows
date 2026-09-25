# 0012. Releases are manual and close a milestone

Date: 2026-09-18
Status: accepted

## Context
- Work is cut into tickets, but nothing groups them into something shippable, and releases were tagged ad hoc.
- Automatic releases on every merge do not fit repositories that stage work on `dev`.
- Spec: #3.

## Decision
A manual orchestrator command releases a milestone named `vX.Y.Z`, whose description states the goal and to which the planner attaches tickets.

## Consequences
- The command refuses while the milestone has open issues.
- In the two-level model it opens the promotion pull request from `dev` to `main`.
- Then it tags, creates the GitHub release with generated notes and closes the milestone.
- A milestone maps to one release and its notes.
- A release never happens by accident, and the maintainer must trigger each one.
- Rejected for now: release on every merge, and release-please style automation.
