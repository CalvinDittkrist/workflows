# 0035. Every category the apply phase scaffolds is answerable

Date: 2026-09-22
Status: accepted

## Context
- Only a rejected category is left alone ([ADR 0016](0016-approval-is-per-category-and-scripts-own-what-they-apply.md)), but `approve.sh` took answers only for categories with findings.
- A category `scaffold.sh` has templates for that no auditor reported was scaffolded unasked, `.claude/settings.json` included (#38).

## Decision
The report asks about every category the apply phase scaffolds, with findings or without, and `approve.sh` accepts `approve` and `reject` for each.

## Consequences
- The `also:` line is gone. `answerable()` in `lib.sh` adds `WF_SCAFFOLD_CATEGORIES` to the categories with findings for both callers, so they cannot drift.
- A category with neither findings nor templates is refused, naming the answerable ones. An audit that found nothing is answerable too.
- Only a rejection changes the apply phase; an approved or unanswered category without findings is scaffolded as before.
- An unanswered category with findings is `pending` and stops the apply phase; one that is only scaffolded is `unanswered` and does not.
- `finalize.sh` runs `workspace.sh --apply` only for an approved `workspace` with a `configure` finding. That outcome changes: approving baseline files never configures GitHub.
- A conforming repository is asked four questions.
- Rejected: scaffolding only approved categories, which silently changes the outcome for most small repositories.
