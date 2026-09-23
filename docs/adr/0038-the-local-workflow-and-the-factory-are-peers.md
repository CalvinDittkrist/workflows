# 0038. The local workflow and the factory are peers

Date: 2026-09-23
Status: accepted
Changes: the priorities of `AGENTS.md`, which put the local workflow first and called the factory a second driver over it

## Context
The repository instructions said the local workflow comes first and the factory is a second driver over the same worker pipeline ([ADR 0022](0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)). That made every change to the pipeline a change to the factory, and a skill written for a person in a Herdr pane decided what an unattended host did. Spec #141 moves the delivery lifecycle into the factory ([ADR 0040](0040-the-factory-owns-the-delivery-lifecycle-in-go.md)), and then the old priority is no longer true.

## Decision
The local workflow and the factory are peers. The plugins serve hands-on sessions: a person claims an issue into a pane, watches the pipeline and steps in. The factory serves unattended delivery: it drives its own pipeline in Go and starts a session only where a stage needs judgement. Neither is built as a fork of the other, and neither waits on the other's release.

What the two still share is written down and bound by a drift test: the branch contract, the base branch rule, the frontier rule, the compact pin and the label vocabulary. Everything else belongs to one of the two.

## Consequences
A change to a worker skill no longer reaches an unattended host, and a change to the factory needs no plugin release. Some rules are now written twice. The drift tests are what keep that duplication visible, and a rule outside that list may differ between the two on purpose.

The priorities of security, low token use and throughput stay as they are and apply to both.
