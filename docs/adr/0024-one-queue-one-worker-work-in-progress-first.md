# 0024. One queue, one worker, and work in progress before new work

Date: 2026-09-21
Status: accepted

## Context
The factory host is one machine — a Raspberry Pi 4 or a small VM — and a worker session runs a repository's whole gate next to its own reasoning. Two workers on such a host share a CPU, a disk and one Claude quota, and they would take twice as long each while making the cost of a run unreadable.

The factory also serves several repositories. Either each repository has a line of its own, and the factory decides how to interleave them, or there is one line and the order is a property of the issues.

Left alone, a queue that always takes the oldest new issue starves what is already half done: a pull request that came back with a review comment would wait behind every issue routed since, and go stale while it waits.

## Decision
All connected repositories feed one queue. It is derived from GitHub on every poll and never stored, so an issue is in the queue exactly as long as GitHub says it is routed.

The factory works one issue at a time. There is no concurrency setting. The head of the queue starts when the run before it has ended, without waiting for the next poll.

Work in progress comes first: resumed runs and follow-up runs, ordered by the time of the signal that queued them, then new issues, ordered by the time the routing label was set, oldest first. The routing time, not the issue's creation time, is the order: routing is when the maintainer handed the issue over.

## Consequences
The host is never oversubscribed, a run's cost and duration mean what they say, and the plugin update before a run is safe because nothing else is running.

Throughput is one run at a time, and a long run blocks the queue behind it. The deadline is the only thing that bounds it; there is no way to let a small issue pass a big one.

A pull request that was reviewed is picked up before the next new issue, so the open work of the factory tends to close rather than grow.

The queue is a view, never a state: a cancelled or closed issue simply stops appearing, and a restart of the factory needs nothing but a poll to know what to do next. The order is only as good as GitHub's timeline, which lags a label by a few seconds — the next poll sees it, and the order is unaffected.
