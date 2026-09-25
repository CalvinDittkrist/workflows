# 0036. The factory updates the worker plugin and nothing else

Date: 2026-09-22
Status: superseded by [0042](0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md)
Extends: [0027](0027-the-factorys-isolation-boundary-is-the-host.md)
Amended by: [0050](0050-the-host-installs-every-factory-release-and-the-factory-drains-on-signal.md)

## Context
- The factory works on a host nobody watches ([ADR 0027](0027-the-factorys-isolation-boundary-is-the-host.md)), and every run is a session of the worker plugin ([ADR 0022](0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)).
- A released pipeline fix reached that host only when somebody logged in.
- The same argument would have the factory upgrade Claude Code, and then itself.

## Decision
Before every run the factory updates the marketplace and the worker plugin with Claude Code's plugin commands. It records the versions of the worker plugin, Claude Code and the factory. It updates nothing else: Claude Code and the factory binary are the host's to install.

- The update runs between the claim and the worker, while no session reads the plugin ([ADR 0025](0025-one-queue-one-worker-work-in-progress-first.md)).
- A failed update or an unreadable version is a warning, and the run goes on.
- Fake mode updates and records nothing.
- A host that loads the worker with `--plugin-dir` in `worker_args` records no worker version and warns per run.

## Consequences
- Every run names its worker plugin, Claude Code and factory, on the record and the dashboard.
- The `workflows` marketplace becomes a trust boundary of every host: whoever publishes there decides what the next unattended run executes, pinned by nothing but the plugin commands.
- The last step of the migration ends the update ([ADR 0043](0043-the-migration-runs-from-the-last-stage-to-the-first.md)).
- Rejected: updating Claude Code and the factory too. A service that upgrades its own runtime or binary changes it on a schedule nobody chose.
