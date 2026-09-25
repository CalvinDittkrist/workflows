# 0042. The factory carries its own prompts and updates no plugin

Date: 2026-09-23
Status: accepted
Supersedes: [0036](0036-the-factory-updates-the-worker-plugin-and-nothing-else.md)

## Context
- The factory updated the worker plugin before each run ([ADR 0036](0036-the-factory-updates-the-worker-plugin-and-nothing-else.md)), so a change to a hands-on skill changed an unattended host with no deploy.
- Once the factory owns the lifecycle ([ADR 0040](0040-the-factory-owns-the-delivery-lifecycle-in-go.md)), its prompts are part of it. Spec #141.

## Decision
The factory carries one prompt per session kind: implement, fix, the five reviewers, pull request author and address-reviews. Reviewers are inline agent definitions with their tools, model and prompt. The prompts are versioned with the binary. A run records the factory and Claude Code versions, and the factory updates nothing before a run. Claude Code and the factory binary stay the host's to install.

## Consequences
- A host needs claude, git, gh and the factory binary, and no plugin.
- A change to what an unattended session is told is a factory release.
- The worker plugin serves hands-on sessions only ([ADR 0038](0038-the-local-workflow-and-the-factory-are-peers.md)).
- The worker environment setting goes with the last step of the migration; until then the factory still updates the worker plugin ([ADR 0043](0043-the-migration-runs-from-the-last-stage-to-the-first.md)).
- Rejected: keeping the plugin update, which lets a hands-on skill steer an unattended host.
