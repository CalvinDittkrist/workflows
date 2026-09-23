# 0043. The migration runs from the last stage to the first, through a temporary stop-after knob

Date: 2026-09-23
Status: accepted

## Context
The factory takes the delivery lifecycle over from the worker plugin ([ADR 0040](0040-the-factory-owns-the-delivery-lifecycle-in-go.md)). Done in one release, that would replace everything an unattended host does at once, with no step between the pipeline that works today and the one that should. Spec #141.

## Decision
The migration runs in six steps, each a release the host runs, from the last stage to the first:

1. the structured result contract, read from the unchanged worker session ([ADR 0039](0039-every-session-reports-through-a-structured-result.md));
2. the ci stage with the repair budget and the address-reviews session, with the worker stopping after the pull request;
3. the pr stage, with the worker stopping after the review;
4. the review stage, with the worker stopping after the gate;
5. the gate stage with the base merge, with the worker stopping after implementing;
6. the implement session with the factory's own prompt, and the end of the plugin update, of the worker environment setting and of the stop-after knob.

The worker skill gets a temporary stop-after knob, read by the session facts and honoured by the work skill, so a factory session ends with a structured report at the named stage and the factory takes over the stages after it. The knob is `WF_STOP_AFTER`, one of `implement`, `gate`, `review` and `pr`; the facts print it as `stop_after:` and refuse any other value, and the work skill ends right after that stage with the report the worker's `stop.sh` prints from git, GitHub and the worktree's records: the commits, the recorded gate pass, the panel summary with the gate result, or the pull request. The session's structured result carries the same facts as the fields `commits`, `gateResult`, `panelSummary` and `pullRequest`, so the stage that follows on the factory's side reads them from the result. The local workflow never sets the knob, and a claim does not accept it. The knob goes away with the last step. Fake mode follows every step, so a run can still be watched from start to end without tokens.

## Consequences
Every step is live on the host before the next one starts, and a step that goes wrong is one release to undo. For the length of the migration the worker plugin carries a knob that only the factory sets, which is the one change to the local pipeline the spec allows beyond its removal.
