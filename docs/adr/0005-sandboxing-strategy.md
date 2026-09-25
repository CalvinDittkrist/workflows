# 0005. Sandboxing strategy: layered, Docker Sandboxes opt-in

Date: 2026-09-17
Status: accepted

## Context
- Not every repository can run in a container, because of device access or local services.
- Unattended yolo workers should not run unconfined on the host.
- Claude Code offers an OS-level Bash sandbox and permission modes.
- Docker Sandboxes (`sbx`) offer per-agent containers with a shared read-only skills store and network policy.

## Decision
Safety is layered and chosen per repository, and the Docker sandbox is a flag on claim, not a separate workflow.

## Consequences
- Role restriction and permission allowlists apply always; Claude Code's Bash sandbox applies via repository settings where possible.
- `claim --sandbox` runs the worker through `sbx run claude`, mounting only that worktree, with plugins installed inside the container.
- The same skills and scripts run in both modes; only the launcher differs.
- Docker mode installs plugins on first start, which is slower; a prebuilt template (`sbx template save`) can remove that.
- Rejected: a dev container as the default. It forces every repository into Docker and does not integrate with Herdr's agent detection.
