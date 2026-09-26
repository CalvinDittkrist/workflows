# 0050. The host installs every factory release and the factory drains on signal

Date: 2026-09-25
Status: accepted
Amends: [0036](0036-the-factory-updates-the-worker-plugin-and-nothing-else.md), [0042](0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md) (the factory binary is no longer installed by hand)

## Context
- A factory release reached a host only when a person installed it ([ADR 0042](0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md)), and a restart interrupted the run that was going. Spec #206.

## Decision
- A root timer runs the binary's update mode, one update tick at a time, when `auto_update` is true. The factory never replaces itself.
- The timer never runs as the user `factory`: that user runs the worker sessions, so a worker could otherwise install a binary of its own.
- On `SIGHUP` the factory drains: it starts nothing new, lets the run in `.now` end and exits with 75. A drain spends no resume.
- The release path is the host's trust boundary. The attestation narrows it to built by our CI from main.
- `gh attestation verify` checks the attestation as a child process, so the module keeps no dependencies.
- Version N writes what N minus 1 reads. The configuration parser still refuses unknown fields.
- The test suite verifies the drain: it starts the real binary and signals it (`factory/drain_test.go`).
- The host verifies the updater, since systemd and root belong to the host.

## Consequences
- A release reaches every host that opts in, between runs.
- `SIGTERM` still interrupts a run, drain or not.
- Rejected: self-replacement by the factory user, which gives a worker the binary.
