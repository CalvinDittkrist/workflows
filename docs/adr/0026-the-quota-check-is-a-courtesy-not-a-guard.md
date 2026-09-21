# 0026. The quota check is a courtesy, not a guard

Date: 2026-09-21
Status: accepted

## Context
The factory host and the maintainer's machine work on the same Claude subscription. An unattended run that starts at 80 % of the window can leave the maintainer without quota for their own work at lunchtime, and the maintainer is the one who cannot wait.

quota-axi reads the remaining percentage per scope from the provider's own endpoint, for the all-models scope and per model, with the window and the reset time. It is a third-party CLI on npm, it needs Node on a host that would otherwise not need it, and it reads a credential.

That makes it attractive as a gate and dangerous as one. A check that can stop the factory is a check whose failure stops the factory: a changed output format, an expired credential or a missing Node would silently turn the host off, and nobody is watching.

## Decision
Before every run the factory asks quota-axi for the Claude provider and takes the smaller remaining percentage of the all-models scope and of the worker's model scope. Below the configured minimum (default 30) nothing starts; the interface shows that the factory waits for quota and until when, and the check runs again after the reported reset.

The check fails open. When it cannot answer — quota-axi missing, exiting non-zero, printing something unexpected — the run starts and carries a warning, which the interface shows. It protects the maintainer's quota; it is not a safety mechanism and must not be able to stop the factory.

quota-axi is installed on the host in a pinned version and the configuration names its path. Without that entry the check is off. It is never fetched from npm at run time.

## Consequences
The maintainer keeps a margin on their own subscription without the host needing a rule about working hours, and a run that hits the limit anyway is resumed after the reset ([ADR 0024](0024-the-factory-never-deletes-work-on-its-own.md)), so the common case needs no decision from anybody.

A broken check costs quota, not availability: the factory keeps working and says on every run that it did not know. That is the trade this decision makes, and it is the right way round only because the worst case of failing open is a slow lunchtime, while the worst case of failing closed is a host that stopped for days without telling anyone.

Pinning and naming the path keeps a third-party tool out of the run: nothing is downloaded while a run starts, and an update to quota-axi is the operator's act, in the host runbook, not a surprise from a registry.

The default of 30 is an assumption, not a measurement: it rests on host and maintainer sharing one Max subscription and on a run costing far less than 30 % of a window. The percentage a run really uses is measured on the host and corrects this number.
