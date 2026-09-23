# 0040. The factory owns the delivery lifecycle in Go and starts one fresh session per stage

Date: 2026-09-23
Status: accepted
Supersedes: [0022](0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)

## Context
The factory started one worker session per run, and inside it the agent drove the pipeline: it decided when the gate ran and waited for it, launched the reviewers, counted and recorded the rounds, opened the pull request, waited for CI, counted repair rounds and reported the outcome. Every one of those transitions is deterministic, and each cost tokens, varied per run and could be skipped or misread by the agent. [ADR 0002](0002-scripts-do-agents-decide.md) already says deterministic work belongs in a program. Spec #141.

## Decision
The factory owns the delivery pipeline in Go as a fixed sequence of stages: implement, gate, review, pr, ci and address-reviews. It owns the worktree, the base merge, the gate, the review loop, opening the pull request, waiting for CI, the repair budget, the outcome and the notification. Where a stage needs judgement (implementing the issue, fixing findings, answering review comments, reviewing a diff, writing a pull request description), the factory starts one fresh session with a brief of facts and reads one structured result back ([ADR 0039](0039-every-session-reports-through-a-structured-result.md)). Where a stage needs no judgement, the factory runs the command or makes the GitHub call itself.

Sessions are never resumed. The next stage gets its facts (issue, base, branch, diff range, commits, the findings or threads it is there for) and nothing from an earlier context. Reviewers and the pull request author are read-only sessions, made so through the call's tool restriction.

The pipeline is fixed in Go and the configuration holds knobs, never a list of steps. Sessions are started with claude only, through the one session type; there is no executor abstraction. A resumed run derives its stage from git and GitHub, never from the record. The signals, kinds and outcomes of a run stay as they are.

## Consequences
The agent no longer counts, waits or records, so those steps cost no tokens and cannot be skipped. The factory grows by the logic the worker plugin's scripts hold today, rewritten in Go and tested against the shims. The checkpoint, the handoff and the records of the local pipeline stay terms of the local pipeline: a session of the factory is short enough not to need a handoff, and the factory keeps the same facts in its run record.

The migration to this design is stepwise ([ADR 0043](0043-the-migration-runs-from-the-last-stage-to-the-first.md)); until its last step the factory still starts the worker skill for the stages it does not own yet.
