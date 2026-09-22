# 0035. Every category the apply phase scaffolds is answerable

Date: 2026-09-22
Status: accepted

## Context
Approval is per category, and only a rejected category is left alone ([ADR 0016](0016-approval-is-per-category-and-scripts-own-what-they-apply.md)): `cleanup.sh prepare` passes `--skip` to `scaffold.sh` for the rejected categories and scaffolds every other one. `approve.sh` accepted an answer only for a category that had a finding in the last report, so a category `scaffold.sh` has templates for (`agent-config`, `docs`, `tests-ci`, `workspace`) that no auditor reported was scaffolded without the maintainer ever being asked, and there was no way to say no — for `agent-config` that includes bringing `.claude/settings.json` to the template. ADR 0016 made the report name those categories in one `also:` line, which said the behaviour out loud without giving the maintainer a way to change it (#38, a follow-up of #15).

## Decision
The report asks about a scaffolded category without findings like about any other category, and `approve.sh` accepts `approve` and `reject` for it; the `also:` line is gone. The answerable categories are the ones with findings plus `WF_SCAFFOLD_CATEGORIES`, computed in one place in `lib.sh`, so the report and the answer cannot drift apart. A category with no findings that `scaffold.sh` has no templates for is still refused, with an error that names the categories the report asks about. An audit that found nothing at all is answerable too, because the apply phase runs on it and scaffolds.

Only a rejection changes what the apply phase does. A scaffolded category without findings that was approved, or that was never answered, is scaffolded as before; `decisions()` therefore carries an answer for it when there is one and leaves it out while there is none, and it keeps refusing to hand anything over while a category *with* findings is pending. Approving such a category answers for the baseline files it scaffolds, not for what its findings would have driven: `finalize.sh` runs `workspace.sh --apply` only when `workspace` was approved *and* had findings, so an approval given for `.github/dependabot.yml` never configures GitHub on its own.

## Consequences
The maintainer can keep the apply phase out of any category it scaffolds, `agent-config` and its `.claude/settings.json` included, and the report says per category what approving and what rejecting it means. The report is longer: a repository that conforms is still asked four questions. The default is unchanged, so an unanswered category behaves exactly as it did, and no existing run changes its outcome; `approve.sh` lists those questions as `pending` until they are answered, which is how the audit skill knows to ask.

Rejected: scaffolding only categories that were approved (the other way #38 named). It would silently change the outcome for every repository that conforms except for missing baseline files no auditor reported — the common case for an empty or a small repository — and it would make "not answered" mean two different things in the same run, since a category with findings that is not answered stops the apply phase instead.
