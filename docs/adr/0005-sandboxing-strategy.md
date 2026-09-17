# 0005. Sandboxing strategy: layered, Docker Sandboxes opt-in

Date: 2026-09-17
Status: accepted

## Context
Not every repository can run in a container (device access, local services), but unattended yolo workers should not run unconfined on the host. Claude Code offers an OS-level Bash sandbox and permission modes; Docker Sandboxes (`sbx`) offer per-agent containers with a shared read-only skills store and network policy.

## Decision
Safety is layered and chosen per repository: role restriction and permission allowlists always; Claude Code's Bash sandbox via repo settings where possible; `claim --sandbox` runs the worker through `sbx run claude` mounting only that worktree, with plugins installed inside the container. The sandbox mode is a flag on claim, not a separate workflow, so the pipeline is identical inside and outside the container.

## Consequences
The same skills and scripts run in both modes; only the launcher differs. Docker mode currently installs plugins on first start (slower); a prebuilt template (`sbx template save`) can remove that. A dev container was rejected as the default because it forces every repo into Docker and does not integrate with Herdr's agent detection.
