# 0049. An accepted ADR may be shortened in wording; its decision is never edited

Date: 2026-09-25
Status: accepted

## Context
- Most accepted ADRs exceed the word cap of the [writing rules](../repo-standard.md#writing-rules) ([ADR 0048](0048-writing-rules-are-part-of-the-standard-and-the-gate-checks-the-mechanical-ones.md)).
- The index said an ADR is never edited after acceptance, so the only way to shorten one was a superseding ADR per decision.

## Decision
An accepted ADR may be rewritten in place to shorter wording if it states the same decision. A change of decision still needs a superseding ADR.

## Consequences
- The ADRs can be brought under the cap without a second ADR per decision.
- A rewrite pull request lists what must survive: the number, the title, the decision and every link.
- Rejected: superseding every long ADR. It doubles the ADRs and leaves the long ones in the index.
