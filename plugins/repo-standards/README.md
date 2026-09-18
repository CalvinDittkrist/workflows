# repo-standards

Owns the [repository standard](../../docs/repo-standard.md): the files every repository has, the ones it must not have, and the check for both.

| Skill | Script | Effect |
| --- | --- | --- |
| `/repo-standards:init-repo` | `scaffold.sh` | create AGENTS.md, a CLAUDE.md that imports it, a Makefile with a `check` target, docs/architecture.md, docs/adr/, PR template, `.claude/settings.json` (never overwrites), then fill placeholders from the codebase and run the check |
| `/repo-standards:adr <title>` | `new-adr.sh` | next numbered ADR from the template, added to the index |
| `/repo-standards:docs-check` | `check.sh` | pass/fail report against the standard, including each path of agent configuration it does not define, plus GitHub workspace drift as warnings when GitHub is reachable (`skip:` otherwise); exit 1 on failures, usable in CI |
| (none yet; the standardisation run will call it) | `workspace.sh [--apply] [--snapshot <file>]` | GitHub workspace and milestones against the standard: one `diff:` line per difference, `manual:` for what the API cannot set; `--apply` writes a snapshot of the previous state, then changes exactly the differences. `WF_PROJECT_TEMPLATE=<owner>/<number>` names the project to copy |

Templates live in `templates/`. The settings template enables the workflow plugins, turns off commit attribution, sets the `WF_*` defaults and a permission allow/deny list.
