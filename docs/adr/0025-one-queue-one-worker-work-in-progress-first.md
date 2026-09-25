# 0025. One queue, one worker, and work in progress before new work

Date: 2026-09-21
Status: accepted

## Context
- The factory host is one small machine, and a worker session runs a repository's whole gate.
- Two workers would share one CPU, disk and Claude quota, and make a run's cost unreadable.
- The factory serves several repositories, in a line each or in one line.
- Taking the oldest new issue first starves half-done work, such as a pull request back from review.

## Decision
All connected repositories feed one queue, derived from GitHub on every poll, and the factory works one issue at a time, work in progress first.

## Consequences
- The queue is never stored: an issue is in it while GitHub says it is routed, and a restart needs only a poll.
- There is no concurrency setting. The head starts when the run before it ends.
- Resumed runs and follow-up runs come first, by the time of their signal. New issues follow, by the time the routing label was set.
- The routing time orders new issues, not the creation time, because routing is when the maintainer handed the issue over.
- The host is never oversubscribed, and a run's cost and duration mean what they say.
- A long run blocks the queue. Only the deadline bounds it, and a small issue cannot pass a big one.
- Open work tends to close rather than grow.
- Rejected: a line per repository, which makes the factory decide how to interleave them.
