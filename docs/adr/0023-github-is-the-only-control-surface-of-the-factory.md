# 0023. GitHub is the only control surface of the factory

Date: 2026-09-21
Status: accepted

## Context
- The factory runs on a host of its own and is watched from a phone as often as from a desk.
- A writing endpoint on the host needs authentication, authorisation and an audit trail, and holds state GitHub already holds.
- Two surfaces for one decision drift apart, and the maintainer already decides this work on GitHub.

## Decision
GitHub is the only thing the factory host and a developer's machine share, and the only surface that steers the factory.

## Consequences
- Route: add the routing label to an agent-ready issue.
- Release a blocked, failed or timed-out run: answer on the issue, then remove the assignee.
- Cancel: remove the routing label or close the issue; the worker ends and the issue is given back.
- Ask for changes: a "changes requested" review on the factory's pull request queues a follow-up run.
- Merge: a person, on GitHub. The factory has no yolo mode.
- The pause is the operator's: `"paused"` in the configuration file, read on every poll.
- Notifications go the same way: a review request when a run ends `ready`, a mention otherwise.
- The HTTP interface is read-only: every writing method gets 405. It binds one address, never a wildcard, loopback by default.
- A routed issue stays routed while the host is down.
- Steering lags by a poll, and what has no GitHub gesture has no way in.
- Rejected: steering through the factory's own interface.
