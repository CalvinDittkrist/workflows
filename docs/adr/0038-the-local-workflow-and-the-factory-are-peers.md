# 0038. The local workflow and the factory are peers

Date: 2026-09-23
Status: accepted
Changes: the priorities of `AGENTS.md`, which put the local workflow first

## Context
- The instructions called the factory a second driver over the worker pipeline ([ADR 0022](0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)).
- A skill written for a person in a Herdr pane decided what an unattended host did.
- Spec #141 moves the delivery lifecycle into the factory ([ADR 0040](0040-the-factory-owns-the-delivery-lifecycle-in-go.md)).

## Decision
The local workflow and the factory are peers. The plugins serve hands-on sessions. The factory serves unattended delivery, drives its own pipeline in Go and starts a session only where a stage needs judgement. Neither is a fork of the other, and neither waits on the other's release. A drift test binds what they share: the branch contract, the base branch rule, the frontier rule, the compact pin and the label vocabulary.

## Consequences
- A change to a worker skill no longer reaches an unattended host, and a change to the factory needs no plugin release.
- Some rules are written twice, and the drift tests keep that visible. A rule outside the shared list may differ on purpose.
- Security, low token use and throughput stay the priorities of both.
- Rejected: keeping the factory a driver over the plugins, which ties unattended delivery to skills written for a person.
