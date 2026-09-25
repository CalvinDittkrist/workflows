# 0040. The factory owns the delivery lifecycle in Go and starts one fresh session per stage

Date: 2026-09-23
Status: accepted, amended 2026-09-25 (#166: the resume rule)
Supersedes: [0022](0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)

## Context
The factory started one worker session per run, and inside it the agent drove the pipeline: it decided when the gate ran and waited for it, launched the reviewers, counted and recorded the rounds, opened the pull request, waited for CI, counted repair rounds and reported the outcome. Every one of those transitions is deterministic, and each cost tokens, varied per run and could be skipped or misread by the agent. [ADR 0002](0002-scripts-do-agents-decide.md) already says deterministic work belongs in a program. Spec #141.

## Decision
The factory owns the delivery pipeline in Go as a fixed sequence of stages: implement, gate, review, pr, ci and address-reviews. It owns the worktree, the base merge, the gate, the review loop, opening the pull request, waiting for CI, the repair budget, the outcome and the notification. Where a stage needs judgement (implementing the issue, fixing findings, answering review comments, reviewing a diff, writing a pull request description), the factory starts one fresh session with a brief of facts and reads one structured result back ([ADR 0039](0039-every-session-reports-through-a-structured-result.md)). Where a stage needs no judgement, the factory runs the command or makes the GitHub call itself.

Sessions are never resumed. The next stage gets its facts (issue, base, branch, diff range, commits, the findings or threads it is there for) and nothing from an earlier context. Reviewers and the pull request author are read-only sessions, made so through the call's tool restriction.

The pipeline is fixed in Go and the configuration holds knobs, never a list of steps. Sessions are started with claude only, through the one session type; there is no executor abstraction. A resumed run derives its stage from git, GitHub and the run's record together: git says which commits the branch carries, GitHub whether the run's pull request is still open and whether a pull request that is not the run's stands on the branch, and the record which pull request is the run's, whether it is still the draft of a gate on CI, and which gate and review rounds were recorded at which commit. What a person does to that pull request's draft state on GitHub decides no stage and spends no budget. The signals, kinds and outcomes of a run stay as they are.

## Consequences
The resume rule was first written as "from git and GitHub, never from the record". A gate on CI (#166) opens its draft pull request before the review, so GitHub alone can no longer tell a run that stopped in the gate or the review from one that stopped in the ci stage: a draft someone marked ready by hand would look like a finished pull request. The record is what the factory wrote itself, and it now decides that, while git and GitHub still decide whether the commits and the pull request it names are still there.

The agent no longer counts, waits or records, so those steps cost no tokens and cannot be skipped. The factory grows by the logic the worker plugin's scripts hold today, rewritten in Go and tested against the shims. The checkpoint, the handoff and the records of the local pipeline stay terms of the local pipeline: a session of the factory is short enough not to need a handoff, and the factory keeps the same facts in its run record.

The migration to this design is stepwise ([ADR 0043](0043-the-migration-runs-from-the-last-stage-to-the-first.md)); until its last step the factory still starts the worker skill for the stages it does not own yet.
