# 0036. The factory updates the worker plugin and nothing else

Date: 2026-09-22
Status: accepted
Extends: [0027](0027-the-factorys-isolation-boundary-is-the-host.md) (what an unattended host may change about itself)

## Context
The factory works its line on a host nobody watches ([ADR 0027](0027-the-factorys-isolation-boundary-is-the-host.md)), and every run of it is a session of the worker plugin ([ADR 0022](0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)). A fix merged into the pipeline and released to the marketplace would otherwise reach that host only when somebody logged in, so the factory would keep working a line with a pipeline the repository had already moved past — and a run that went wrong could not be read back to the version that made it, because nothing on the record said which one that was.

Something has to bring the host forward. How far it may go is the question: the same argument would have the factory upgrade Claude Code, and then itself.

## Decision
Before every run the factory updates the marketplace and the worker plugin, with Claude Code's own plugin commands, and records the versions of the worker plugin, Claude Code and the factory as they stand after that update and before the worker starts.

It updates those two things and nothing else. Claude Code is the host's, and so is the factory binary:

- Claude Code is what runs the session the factory is about to start. A service that upgraded the runtime it drives would be changing the tool under its own hand, on a schedule nobody chose, and a release that breaks the stream the factory reads a run from would stop the line without anybody having deployed anything.
- The factory is the process doing the updating. A binary that replaced itself would be deciding its own version, and the operator who deployed it would no longer know what is running on the host. The factory is released as a tagged binary, and installing one is the host's act.

A failed update is a warning on the run and never the end of it: the run goes on with the state the host has installed, which is an older worker and still a worker. A version that cannot be read is a warning for the same reason — a run nobody can trace to what it ran with is what this record exists to prevent.

The update runs between the claim and the worker, where the factory has nothing else going: one worker at a time ([ADR 0025](0025-one-queue-one-worker-work-in-progress-first.md)), so no session is reading the plugin while it is being written.

Fake mode does none of it. Its worker is the factory binary itself, so there is no plugin in the run to update and no version of one to record, and trying the factory out on a machine must not change what that machine has installed.

## Consequences
A fix to the pipeline reaches the factory with its next run, and nobody logs in to deploy it. The step between two runs is one marketplace fetch and one plugin update, seconds on a line that holds; a host whose line is down keeps working with what it has.

Every run says which worker plugin, which Claude Code and which factory made it, on the record the HTTP interface serves and on the dashboard. A run that went wrong after a release can be told from one that went wrong before it.

The marketplace becomes a trust boundary of every host, and this decision accepts it: whoever can publish to the `workflows` marketplace decides what the next unattended run executes, because the run installs that plugin and works an issue with it unwatched. Nothing is pinned here beyond what the plugin commands themselves check, so the marketplace's release path is guarded as the hosts are.

Claude Code and the factory move when the operator moves them. That is deliberate friction: the two pieces whose failure takes the whole line down are the two the factory is not allowed to change about itself.

A host that runs the worker from a checkout instead — `worker_args` with `--plugin-dir` — records no worker version and gets a warning per run naming that directory. A plugin loaded that way takes precedence over an installed one of the same name and is listed by no plugin command the factory can ask, so whatever the install says is not what the session loaded. That host is a developer's, and the warning is true: the run cannot be traced to a released worker.
