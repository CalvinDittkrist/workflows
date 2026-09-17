# repo-standards

| Skill | Script | Effect |
| --- | --- | --- |
| `/repo-standards:init-repo` | `scaffold.sh` | create CLAUDE.md, docs/architecture.md, docs/adr/, PR template, `.claude/settings.json` (never overwrites), then fill placeholders from the codebase and run the check |
| `/repo-standards:adr <title>` | `new-adr.sh` | next numbered ADR from the template, added to the index |
| `/repo-standards:docs-check` | `check.sh` | pass/fail report against the standard; exit 1 on failures, usable in CI |

Templates live in `templates/`. The settings template enables the three plugins, turns off commit attribution, sets the `WF_*` defaults and a permission allow/deny list.
