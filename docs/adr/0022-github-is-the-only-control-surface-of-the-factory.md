# 0022. GitHub is the only control surface of the factory

Date: 2026-09-21
Status: accepted

## Context
The factory runs on a host of its own and is watched from a phone as often as from a desk. Steering it needs a surface: routing an issue to it, answering a blocked run, stopping a run that misunderstood its issue, taking a pull request.

Its own interface is the obvious place, and the wrong one. A writing endpoint on the host needs authentication, authorisation and an audit trail of its own, and it would hold state that GitHub already holds — who owns an issue, what was decided, what is waiting. Two surfaces for the same decision drift apart, and the one on the host is the one nobody can reach from a phone.

The maintainer is already on GitHub all day: issues, pull requests, reviews and notifications are where this work is decided.

## Decision
GitHub is the only thing the factory host and a developer's machine share, and the only surface that steers the factory:

- **Route**: add the routing label to an agent-ready issue.
- **Release** a blocked, failed or timed-out run: remove the assignee, after answering on the issue.
- **Cancel** a running run: remove the routing label.
- **Ask for changes**: a "changes requested" review on the factory's pull request queues a follow-up run.
- **Merge**: on GitHub, by a person. The factory has no yolo mode.

Its notifications go the same way: a review request on the pull request when a run ends `ready`, a comment that mentions the maintainer when it ends `blocked`, `failed`, `timeout` or is interrupted a second time. There is no other channel.

The factory's own HTTP interface is read-only. It has no endpoint that writes anything, and a request with a writing method is refused with 405, whatever the path, so an endpoint that writes cannot appear by accident. It binds to one address and refuses a wildcard one, so an unauthenticated interface cannot end up on every network the host is on; by default that address is the loopback, and reaching it from elsewhere is the tailnet's job, so the factory carries no login of its own.

## Consequences
Everything the maintainer does to the factory is done in a place that already has accounts, permissions, an audit trail and a phone client, and it survives the factory: a routed issue that the host never sees is still a routed issue.

The interface is a window, not a console. Watching a run and steering it are separate acts in separate places, and a reader of the UI who wants to stop a run has to go to the issue. That is the point: the factory holds no decision the rest of the workflow cannot see.

The price is the lag of a poll and of GitHub's own consistency — a label takes a few seconds to appear in the issue list, so the poll after the next one sees it — and a hard floor under what the factory can ever do: anything that has no GitHub gesture has no way in.

The read-only rule also decides the UI: it shows and never asks. A feature that needs a button on the host is a feature this decision refuses.
