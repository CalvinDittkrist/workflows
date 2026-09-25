# 0021. Routing is decided in the planner, and the routing label never stands alone

Date: 2026-09-21
Status: accepted

## Context
- The factory host works every open issue with `ready-for-agent` and the routing label `factory` unattended ([ADR 0014](0014-claims-require-ready-for-agent.md) made the first the gate for a claim).
- A criterion that needs a workspace, a browser or the maintainer's credential ends the run blocked, which was knowable when the criterion was written.
- Nothing said where that judgement happens.

## Decision
The planner decides routing where the acceptance criteria are written, and `issue.sh` refuses the routing label beside `ready-for-human` or without `ready-for-agent`.

## Consequences
- `issue.sh` is the only script that sets the label.
- `/planner:tickets` and `/planner:triage` recommend per ticket against `plugins/planner/skills/tickets/routing.md`, naming the reason, and route only tickets the maintainer names.
- `issue.sh label` checks the labels the issue carries after the call, and refuses when GitHub cannot be read.
- `WF_ROUTING_LABEL` is the planner's copy of the name; a drift test binds it to the orchestrator's and to the label vocabulary.
- A routed issue is one a worker can finish; the session that cut it records why a ticket is not routed.
- The factory's queue rule stays one label check.
- Routing by hand on GitHub passes no guard, by design.
- Rejected: a flag per ticket on the worker side, and enforcing the rule in the factory, one poll too late.
