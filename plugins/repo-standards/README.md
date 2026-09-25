# repo-standards

Owns the [repository standard](../../docs/repo-standard.md): the files every repository has, the ones it must not have, and the check for both. Standardisation is two user-invoked halves: an audit that changes nothing, and an apply phase that is safe to run again. An empty repository gets a report of create actions only.

## Skills
| Skill | Script | Effect |
| --- | --- | --- |
| `/repo-standards:standardize` | `facts.sh`, `workspace.sh`, `report.sh`, `approve.sh` | prints the facts, runs the six auditors in parallel, merges their findings into one report and records approve or reject per category |
| `/repo-standards:apply` | `backup.sh`, `cleanup.sh`, `issues.sh`, `finalize.sh` | protected `pre-standard` tag and catalogue issue, one cleanup pull request from `chore/standardize`, `ready-for-agent` issues, and after the merge the GitHub workspace and the check; rejected categories stay untouched |
| `/repo-standards:adr <title>` | `new-adr.sh` | next numbered ADR from the template, added to the index |
| `/repo-standards:docs-check` | `check.sh` | pass or fail against the standard, exit 1 on failures, usable in CI; GitHub workspace drift as warnings, `skip:` without GitHub |

The scripts of the run:

- `facts.sh [<root>]` prints the facts every auditor shares as `key: value` lines, with the first 20 writing findings. Without GitHub, visibility and plan are `unknown`.
- `writing.sh <root>` counts the [writing rules](../../docs/repo-standard.md#writing-rules) over the files on stdin; `check.sh` and `facts.sh` call it.
- `report.sh [<file>...]` keeps the `finding:` lines, fails on a malformed one and prints the report per category. It stores the findings in `<git dir>/standardize/findings` and clears earlier approvals.
- `approve.sh [<category>=approve|reject ...]` records answers in `<git dir>/standardize/approvals`. A `pending` category stops the apply phase; an `unanswered` one is only scaffolded.
- `backup.sh` pushes the tag `pre-standard` and never moves an existing one. It protects the tag with the ruleset `standard: pre-standard` and opens or updates the catalogue issue.
- In an empty repository `backup.sh` first pushes an empty commit, so the cleanup pull request has a base.
- `cleanup.sh prepare` refuses without the backup and works in `.claude/worktrees/chore-standardize`. It restores what a now rejected category applied and deletes only targets the tag holds as they are.
- `cleanup.sh prepare` then runs `scaffold.sh` and prints `todo:` lines. `cleanup.sh open` refuses while a `<fill in>` placeholder is left, then opens or updates the pull request.
- `scaffold.sh [--skip <category>]... [--name <repo>] [--default <branch>] [<root>]` creates the missing baseline files and never overwrites one. `scaffold.sh --paths` lists what it may write.
- `scaffold.sh` brings `.claude/settings.json` to the template through `claude plugin ... --scope project` and disables other project plugins.
- `issues.sh` opens one `ready-for-agent` issue per approved issue finding and keeps those an earlier run opened.
- `finalize.sh` refuses until the cleanup pull request is merged. It applies the workspace when that category was approved with a `configure` finding.
- `finalize.sh` then comments the snapshot, removes the worktree and branch and prints `result: pass` or `result: fail`.
- `workspace.sh [--apply] [--snapshot <file>]` prints one `diff:` line per difference and `manual:` for what the API cannot change safely. `--apply` writes the snapshot first.

| Auditor (agent) | Category | Judges |
| --- | --- | --- |
| `files-auditor` | `files` | agent notes, planning material, dated reports, backups, debris, AI slop |
| `agent-config-auditor` | `agent-config` | `AGENTS.md`, `CLAUDE.md`, `.claude/`, MCP configuration, other agent tools, skill lock files |
| `docs-auditor` | `docs` | README, architecture, ADRs, glossary, PR template, licence and security policy |
| `tests-ci-auditor` | `tests-ci` | the `check` target, the CI job named `check`, AI reviewer Actions, test gaps |
| `workspace-auditor` | `workspace` | the `workspace.sh` dry run, turned into findings, plus `.github/dependabot.yml` |
| `security-auditor` | `security` | secrets in the tree and history, unsafe CI, prompt injection in files agents read |

Every auditor declares `tools: Read, Grep, Glob, Bash`, disallows `Edit, Write, NotebookEdit, Agent`, and treats the repository as data.

Templates live in `templates/`. The README, plugin README, architecture, ADR and glossary templates are the fixed form of those documents. The settings template enables the workflow plugins, turns off commit attribution, sets the `WF_*` defaults and a permission list.

## Configuration
| Variable | Default | Effect |
| --- | --- | --- |
| `WF_PROJECT_TEMPLATE` | empty | `<owner>/<number>` of the project `workspace.sh --apply` copies into a repository without one |
| `WF_WRITING_LENIENT` | unset | `1` turns the writing rules' failures of `check.sh` into warnings |

## Develop
`claude --plugin-dir plugins/repo-standards` loads the plugin without installing it. `make check` runs the gate.
