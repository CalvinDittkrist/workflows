# 0043. The migration runs from the last stage to the first, through a temporary stop-after knob

Date: 2026-09-23
Status: accepted

## Context
- The factory takes the lifecycle over from the worker plugin ([ADR 0040](0040-the-factory-owns-the-delivery-lifecycle-in-go.md)). Spec #141.
- One release would replace everything an unattended host does at once.

## Decision
The migration runs in six steps, each a release the host runs, from the last stage to the first:

1. the structured result, read from the unchanged worker session ([ADR 0039](0039-every-session-reports-through-a-structured-result.md));
2. the ci stage, the repair budget and the address-reviews session;
3. the pr stage;
4. the review stage;
5. the gate stage with the base merge;
6. the implement session with the factory's own prompt, ending the plugin update, the worker environment setting and the knob.

Until step 6 the worker stops after the stage before the one the factory owns, through `WF_STOP_AFTER`: `implement`, `gate`, `review` or `pr`.

- The session facts print it as `stop_after:` and refuse any other value.
- The work skill ends with the report `stop.sh` prints: the commits, the gate pass, the panel summary or the pull request.
- The structured result carries them as `commits`, `gateResult`, `panelSummary` and `pullRequest`.
- The local workflow never sets the knob, and a claim refuses it.
- Fake mode follows every step.

## Consequences
- Each step is live before the next, and a bad step is one release to undo.
- For the migration the worker plugin carries a knob only the factory sets.
- Rejected: one release that replaces the whole pipeline.
