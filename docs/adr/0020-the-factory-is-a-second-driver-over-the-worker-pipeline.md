# 0020. The factory is a second driver over the worker pipeline

Date: 2026-09-21
Status: accepted

## Context
An issue that is fully specified and needs no screen still waits for the maintainer to sit down and claim it into a Herdr session. A host that works such issues unattended has to start the same worker session a local claim starts, watch it, and record what it did.

A prototype (branch `prototype/factory-headless-ui`) showed that the worker pipeline runs headlessly without a change: `claude -p '/worker:work' --agent worker --output-format stream-json --verbose` reached a real pull request, and `--permission-mode auto` let every write through. It also showed the price: the driver is not a Claude Code plugin, so it cannot source `lib.sh` and `board.sh`, and it needs a language that builds a static binary for a Raspberry Pi with no toolchain on it.

The alternative was another plugin, driven by an agent session on the host. That would put the decisions of an unattended machine — what to claim, when to stop, what to report — into a context window, where they cost tokens and vary per run. [ADR 0002](0002-scripts-do-agents-decide.md) already says that deterministic work is a program.

## Decision
The factory is a Go service in the top-level directory `factory`, not a plugin. It drives the unchanged worker pipeline as a second driver beside the orchestrator's local claim: the same session, the same skills, the same gates, only started by a program instead of by a person, in print mode with stream output and in a process group of its own.

The worker reports nothing to the factory. Everything the factory knows about a run it reads from the stream: the stages from the `Skill` calls, a subagent's events from the `parent_tool_use_id` they carry, the outcome from the final report, which is markdown written for a person, and cost, turns and token totals from the result line, which is the only line that carries them. Nothing in the worker plugin changes to make a run observable, so a worker behaves the same whether a person or the factory started it.

Because it is outside the plugins, the factory restates what the shell owns: the branch contract `<type>/<issue>-<slug>` ([ADR 0003](0003-herdr-worktree-per-issue.md)), the base branch rule and the frontier rule that decides which issue is ready. Every such restatement is bound to its shell original by a drift test, the way the label vocabulary is kept identical across plugins, and no restatement is written without one. The skeleton claims nothing and restates none of the three yet; each arrives with the ticket that needs it, and with its drift test.

## Consequences
The pipeline has one definition and two drivers, so a fix to a worker skill reaches the factory as soon as the host updates the plugin, and a change to the pipeline never has to be made twice.

The parser is the coupling: the factory depends on the shape of Claude Code's stream and on a worker ending with a report that says `ready:` or `blocked:`. A change to either makes runs read as `failed` without the worker being wrong, which is why the stream shape is recorded with the version it was taken from and the scripted worker of fake mode replays exactly that shape.

Three rules will live in two languages. A drift test is what keeps a difference between the two from being silent, but every change to the branch contract, the base branch rule or the frontier rule is a change in two places by design.

Go adds a second language and a second toolchain to a repository of shell and Python, and with it Go's part of the gate: `make check` runs vet, staticcheck and the Go tests ([ADR 0008](0008-make-check-is-the-single-gate.md)).
