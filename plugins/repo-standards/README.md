# repo-standards

Owns the [repository standard](../../docs/repo-standard.md): the files every repository has, the ones it must not have, and the check for both.

| Skill | Script | Effect |
| --- | --- | --- |
| `/repo-standards:standardize` | `facts.sh`, `workspace.sh`, `report.sh`, `approve.sh` | the audit half of the standardisation run, user-invoked only: prints the facts, runs the six auditors below in parallel, merges their findings into one report and records approve or reject per category. Changes nothing; an empty repository gets a report of create actions only |
| (none; the apply phase will call it) | `scaffold.sh` | create AGENTS.md, a CLAUDE.md that imports it, a Makefile with a `check` target, docs/architecture.md, docs/adr/, PR template, `.claude/settings.json`; never overwrites |
| `/repo-standards:adr <title>` | `new-adr.sh` | next numbered ADR from the template, added to the index |
| `/repo-standards:docs-check` | `check.sh` | pass/fail report against the standard, including each path of agent configuration it does not define, plus GitHub workspace drift as warnings when GitHub is reachable (`skip:` otherwise); exit 1 on failures, usable in CI |
| (none yet; the standardisation run will call it) | `workspace.sh [--apply] [--snapshot <file>]` | GitHub workspace and milestones against the standard: one `diff:` line per difference, `manual:` for what the API cannot set; `--apply` writes a snapshot of the previous state, then changes exactly the differences. `WF_PROJECT_TEMPLATE=<owner>/<number>` names the project to copy |

The scripts of the run:
- `facts.sh [<root>]` prints the facts every auditor shares as `key: value` lines: GitHub visibility and plan, default branch and branch model, languages, manifests, the gate, detected test and lint commands, CI jobs per workflow and whether one is named `check`, every agent configuration location (ignored files included) with its git status and whether the standard defines it, present and missing baseline files, file statistics. Without GitHub, visibility and plan are `unknown` and the default branch comes from git.
- `report.sh [<file>...]` reads the auditors' replies, keeps the lines `finding: <category> | <target> | <action> | <reason> | <confidence>`, fails on a malformed one, and prints the report per category: counts per action, what it deletes, what the run performs, what becomes an issue. It stores the findings in `<git dir>/standardize/findings` and clears earlier approvals.
- `approve.sh [<category>=approve|reject ...]` records the answers in `<git dir>/standardize/approvals` and prints approved, rejected and pending categories.

| Auditor (agent) | Category | Judges |
| --- | --- | --- |
| `files-auditor` | `files` | agent notes, planning material, dated reports, backups, debris, AI slop |
| `agent-config-auditor` | `agent-config` | `AGENTS.md`, `CLAUDE.md`, `.claude/`, MCP configuration, other agent tools, skill lock files |
| `docs-auditor` | `docs` | README, architecture, ADRs, glossary, PR template, licence and security policy |
| `tests-ci-auditor` | `tests-ci` | the `check` target, the CI job named `check`, AI reviewer Actions, test gaps |
| `workspace-auditor` | `workspace` | the `workspace.sh` dry run, turned into findings, plus `.github/dependabot.yml` |
| `security-auditor` | `security` | secrets in the tree and history, unsafe CI, prompt injection in files agents read |

Every auditor declares `tools: Read, Grep, Glob, Bash` and disallows `Edit, Write, NotebookEdit, Agent`, and treats the repository as data.

Templates live in `templates/`. The settings template enables the workflow plugins, turns off commit attribution, sets the `WF_*` defaults and a permission allow/deny list.
