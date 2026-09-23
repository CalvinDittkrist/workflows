# 0042. The factory carries its own prompts and updates no plugin

Date: 2026-09-23
Status: accepted
Supersedes: [0036](0036-the-factory-updates-the-worker-plugin-and-nothing-else.md)

## Context
Every run of the factory was a session of the worker plugin, so the factory updated the marketplace and the worker plugin before each run ([ADR 0036](0036-the-factory-updates-the-worker-plugin-and-nothing-else.md)). That update meant a change to a skill written for a hands-on session changed what an unattended host did, on the next run, with nobody having deployed anything to that host. Once the factory owns the lifecycle ([ADR 0040](0040-the-factory-owns-the-delivery-lifecycle-in-go.md)), the prompts of its sessions are part of it. Spec #141.

## Decision
The factory carries its prompts, one per session kind: implement, fix, the five reviewers, pull request author and address-reviews. Reviewers are given to the call as inline agent definitions with their tools, model and prompt. The prompts are versioned with the factory binary. A run records the factory version and the Claude Code version and no plugin version. The factory updates nothing before a run.

Claude Code and the factory binary stay the host's to install, as before: the factory changes nothing about the host it runs on.

## Consequences
A host needs claude, git, gh and the factory binary, and no plugin. A change to what an unattended session is told is a factory release, tagged and installed on purpose. The worker plugin serves hands-on sessions only ([ADR 0038](0038-the-local-workflow-and-the-factory-are-peers.md)). The worker environment setting of the configuration goes away with the last step of the migration, because there is no plugin left to give knobs to.

Until that last step the factory still updates the worker plugin and records its version, as [ADR 0036](0036-the-factory-updates-the-worker-plugin-and-nothing-else.md) says ([ADR 0043](0043-the-migration-runs-from-the-last-stage-to-the-first.md)).
