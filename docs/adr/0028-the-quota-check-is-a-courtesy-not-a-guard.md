# 0028. The quota check is a courtesy, not a guard

Date: 2026-09-21
Status: accepted, amended
Amended by: [0037](0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md) (the default minimum is 12, the worker's scope is read from its model name, and a run that ran out of quota is told apart by a check after its error)

## Context
- The factory host and the maintainer share one Claude subscription, and the maintainer cannot wait for quota.
- quota-axi reads the remaining percentage per scope with its reset time. It is a third-party npm CLI.
- A check that can stop the factory stops it when the check breaks, unwatched.

## Decision
Before every run the quota check waits while quota is below a minimum, and it fails open so it can never stop the factory.

## Consequences
- It takes the smaller of the all-models scope and the worker's model scope. Below the minimum (default 30) nothing starts.
- The interface shows the wait and its end; the check runs again after the reset.
- When quota-axi is missing, fails or prints something unexpected, the run starts with a warning.
- quota-axi is pinned at a path the configuration names, never fetched at run time. Without that entry the check is off.
- A run that hits the limit anyway resumes after the reset ([ADR 0026](0026-the-factory-never-deletes-work-on-its-own.md)).
- The default of 30 assumes one shared Max subscription; measurements on the host correct it.
- Rejected: a gate that fails closed. A changed output or an expired credential would stop the host for days, silently.
