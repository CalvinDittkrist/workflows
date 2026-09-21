# 0019. The gate runs once per review round, and its result is a fact in the brief

Date: 2026-09-21
Status: accepted
Amends: [0004](0004-reviewers-as-fresh-read-only-subagents.md) (reviewers stay fresh, read-only contexts; only their most expensive command becomes a briefing fact)

## Context
Every reviewer prompt allowed "the gate `make check` or single tests and linters", and the reviewers used that permission. Measured on the session behind pull request #40, 13 subagent runs made 16 gate invocations: in round 2 all four re-run reviewers ran the gate, in round 3 all three did, several of them twice, and the pull request author ran it twice as well, although its job is to describe the change, not to verify it.

| round | reviewers | gate invocations | wall clock |
| --- | --- | --- | --- |
| 1 | 5 | 3 | 9:22 |
| 2 | 4 | 6 | 8:09 |
| 3 | 3 | 5 | 9:51 |
| pull request | 1 | 2 | 4:21 |

The gate of this repository takes about 190 s, so a round could never be shorter than one gate plus the reading. Five of them run in parallel in one worktree compete for the same CPU, and they share the working tree: tests that touch a common path or a common GitHub shim log can disturb each other, which surfaces later as flakiness nobody can reproduce. The gate result is also the same fact for all of them — the worker has to have a passing gate before it invokes the review at all, so the panel was paying five times for an answer it already had.

## Decision
The gate runs once per review round, in the worker's main context, through `gate.sh run`, which runs `make check` ([ADR 0008](0008-make-check-is-the-single-gate.md)), records the result in this worktree's git directory ([ADR 0018](0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md)): the commit, whether the working tree was dirty, the exit status, the start time, the duration and the tail of the output, with the full output in a file beside it. The worker never types the gate command by hand, so the record cannot drift from the run.

The output of a passing run stays in that file: it is 36 KB of "ok" lines in this repository, and the run happens in the worker's own context, whose budget is the reason for the change. A failing run prints the whole output in the same call, so it is read rather than run again.

`gate.sh print` is that record as a brief. It answers for the current head or not at all: a record from an older commit, and one taken on a dirty working tree, both read as "none for this head" rather than as an older value. The review stage runs the gate after committing the round's fixes, launches no reviewer when it failed, and copies the block verbatim into every reviewer's brief; the pull request stage's brief carries it beside the panel summary.

No reviewer runs the full gate any more. All five prompts allow a single test or a single linter to verify one claim of their own, and a reviewer whose brief carries no gate result reports that as a finding instead of running the gate itself. `pr-author` quotes the recorded result and runs no verification command at all.

## Consequences
A review round costs the reading and the reviewers' own single commands, plus one gate for the whole round instead of one per reviewer, and nothing in a round writes to the worktree while five contexts read it. The reviewers stay what [ADR 0004](0004-reviewers-as-fresh-read-only-subagents.md) made them — fresh, independent, read-only — and lose no judgement: they still verify a specific claim with a single test, they just no longer each re-establish the same fact.

The price is that a reviewer's gate result is now hearsay: it believes a block the worker pasted. The record's own defences are that it names the commit and is refused for any other one, and that the worker never produces it by hand; a worker that pasted a false block would be a worker that could equally misreport its own run. The output tail is repository output quoted into a brief, so it is indented by two spaces and a line of it cannot be read as a key of the block.

With neither the reviewers nor the pull request author running it, CI is the one independent run of the gate left before a merge, which is what the required `check` job is for ([ADR 0008](0008-make-check-is-the-single-gate.md)); a repository without that required check has none, and in yolo mode nothing but the recorded result stands between a broken branch and the merge.

A repository whose gate is slower than the Bash tool's ten-minute maximum cannot be run through `gate.sh` at all; that limit already applied to the gate the worker ran by hand ([ADR 0017](0017-worker-subagents-run-in-the-foreground.md)) and is the same problem, not a new one.
