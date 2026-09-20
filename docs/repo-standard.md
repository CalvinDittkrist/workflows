# Repository standard

The baseline every repository that runs this workflow is held to. `plugins/repo-standards/scripts/check.sh` (or `/repo-standards:docs-check`) verifies the file rules offline and in CI; `/repo-standards:standardize` audits a repository with six read-only auditors, one per area, and records the maintainer's approval per category of the findings; on an empty repository every finding is a create action. `plugins/repo-standards/scripts/workspace.sh` brings the GitHub workspace and its milestones to the standard, and the check reports its differences as warnings when GitHub is reachable. `/repo-standards:apply` applies the approved findings: backup, cleanup pull request, issues, then the workspace and the check. Terms are defined in the [glossary](glossary.md).

## Profile
A repository's profile is its visibility plus its branch model. Both are derived from GitHub, never configured per repository ([ADR 0009](adr/0009-profile-derived-from-github-with-two-branch-models.md)).

- **Branch model.** `main` alone, or `dev` plus `main` when the default branch is `dev`. With two levels, work merges into `dev` and a promotion pull request from `dev` to `main` carries a release. No third level exists.
- **Public versus private.** Public repositories add a licence, a security policy (`SECURITY.md`), secret scanning, push protection and private vulnerability reporting. Everything else is the same.

## Files that stay
| File | Purpose | Checked |
| --- | --- | --- |
| `README.md` | What the repository is and how to use it | fails if missing; warns with `<fill in>` left |
| `AGENTS.md` | Instruction source for every agent: commands and conventions an agent cannot infer; under 200 lines | fails if missing; warns over 200 lines or with `<fill in>` left |
| `CLAUDE.md` | The line `@AGENTS.md`, optionally a short Claude-only section | fails if missing or without the import; warns over 200 lines |
| `Makefile` | The gate: a `check` target | fails without a `check` target; warns with `<fill in>` left |
| `.github/workflows/*.yml` | A job named `check` that runs `make check` | fails without one |
| `docs/architecture.md` | One-page map: components, data flow, boundaries | fails if missing or under 15 lines |
| `docs/adr/README.md` + `NNNN-title.md` | Decisions, MADR-trimmed, numbered, each with a Status line | fails on a missing index, duplicate numbers or a missing Status |
| `docs/glossary.md` | Terms the code and issues use | warns if missing |
| `.github/PULL_REQUEST_TEMPLATE.md` | Closes, what and why, verification, limits | warns if missing |
| `.github/dependabot.yml` | Grouped version updates, one entry per package manager | warns if missing |
| `.claude/settings.json` | Marketplace, enabled plugins, `WF_*` env, permission allowlist, attribution off | warns on a missing workflow plugin, any other plugin enabled, MCP servers enabled or attribution on; fails on hooks |
| Operational docs | Pages that describe the present state (runbooks, local setup) | no |
| `LICENSE`, `SECURITY.md` | Public repositories only | fails if missing on a public repository; skipped when GitHub is unreachable |

## Files that go
Everything below is removed unless the standard defines it; there is no allowlist per repository.

- Repository-local skills, commands, subagents, rules, hooks and MCP configuration. An MCP configuration stays only when something in the repository uses it.
- Configuration of other agent tools: `.cursor/`, `.cursorrules`, `.windsurf/`, `.clinerules`, `.roo/`, `.codex/`, `.gemini/`, `GEMINI.md`, `.agents/`, `.aider*`, `.github/copilot-instructions.md` and similar.
- Skill lock files (`skills-lock.json`, `.skill-lock.json`).
- Context, resume and review notes; dated audit reports; planning material such as feature specs, user stories, personas and research notes. Plans live in issues.
- GitHub Actions that run an AI reviewer or agent.

The check fails on every tracked or untracked, not ignored path under `.claude/` other than `settings.json`, `settings.local.json` and `worktrees/`, on `CLAUDE.local.md`, `AGENT.md`, `.rules` and `.worktreeinclude`, on the configuration of other agent tools, on skill lock files and on workflow steps that use a known AI reviewer or agent action (`anthropics/claude-code-action`, `openai/codex-action`, `google-github-actions/run-gemini-cli`, `coderabbitai/*`), and names each one (`.claude/skills/<name>`, `.cursor/rules/<file>`). An `.mcp.json` only warns, because it stays when something uses it. Notes, planning material and dated reports need judgement and are left to the auditors.

## Instruction files
`AGENTS.md` is the source; `CLAUDE.md` imports it with `@AGENTS.md`, because Claude Code reads `CLAUDE.md` and not `AGENTS.md` ([ADR 0007](adr/0007-agents-md-is-the-instruction-source.md)). Both stay under 200 lines. A monorepo may keep one such pair per area (`services/api/AGENTS.md` and `services/api/CLAUDE.md`); an area's pair loads only when an agent works there, and the check holds each pair to the same rules. Claude Code loads every nested `CLAUDE.md` it passes, so a `CLAUDE.md` or `AGENTS.md` kept as data (a template, a fixture) is instructions too and must form a valid pair or be renamed.

## The gate
Every repository has a `Makefile`, and `make check` runs everything CI gates on ([ADR 0008](adr/0008-make-check-is-the-single-gate.md)). Agents run `make check` instead of guessing a per-repository test command. CI runs it in a job named `check`, and `check` is the one required status check. A repository with several CI jobs keeps them parallel, each calling its own make target, and adds an aggregating job named `check`.

## GitHub workspace
Set by an idempotent script that shows the difference first and keeps a snapshot of the previous state ([ADR 0011](adr/0011-github-workspace-configured-by-an-idempotent-script.md)). `workspace.sh` prints one `diff:` line per difference and changes nothing; `workspace.sh --apply` writes the previous state as JSON to a snapshot file (`--snapshot <file>`, or a temporary file it names), then makes exactly those changes. It needs admin rights. It refuses to apply while the default branch has no run of a CI job named `check`, because the ruleset requires that status, and while the default branch is neither `main` nor `dev`. What the account's plan or the gh token cannot reach (rulesets on a private repository on GitHub Free, projects without the `project` scope) is printed as a `manual:` step and the rest still runs.

- Merges: squash only, PR title as commit title, branches deleted on merge; wiki and discussions off. With `dev` plus `main`, merge commits are allowed too, and only the promotion pull request uses them, so `dev` stays an ancestor of `main` ([ADR 0013](adr/0013-promotions-merge-with-a-merge-commit-and-releases-tag-it.md)).
- One branch ruleset on `main`, and on `dev` when present: pull request required, zero approvals, conversations resolved, required check `check`, linear history (except on `main` with `dev` plus `main`), no force push, no deletion, no bypass. The rulesets are named `standard: main` and `standard: dev`; the script replaces them when they drift and leaves other rulesets alone. Classic branch protection on those branches is removed once the ruleset is in place.
- One tag ruleset, `standard: pre-standard`, that protects `pre-standard` from deletion and moving.
- Labels: the workflow vocabulary plus `skill-candidate`. Missing labels are created; other labels stay.
- Dependabot alerts and security updates; read-only default token for Actions that cannot approve pull requests. Grouped version updates are a file, `.github/dependabot.yml`, and arrive with the cleanup pull request.
- Public repositories: secret scanning, push protection, private vulnerability reporting. A private repository owned by a personal account cannot get secret scanning or push protection; GitHub offers them only through Secret Protection for organisations on Team or Enterprise. The script prints that as a manual step.

## Projects
One GitHub project per product repository, copied from a template project. It carries a single-select field `Status` with the options Triage, Ready, In progress, In review, Done and a single-select field `Priority` with P0, P1, P2, P3. Field and option names are compared ignoring case, the order of the options does not matter, and the set must be exactly the standard's.

`workspace.sh` changes what the API can change without losing data: when no project is linked and `WF_PROJECT_TEMPLATE=<owner>/<number>` names the template, `--apply` copies it and links the copy (whose fields the next run checks), and a missing `Status` or `Priority` is created with its options. The rest is printed as `manual:` steps: the API cannot create or turn on project workflows, and a copy leaves out the template's auto-add workflow, so auto-add is turned on by hand; a field that is there with other options or of another type is changed by hand, because replacing an option list clears that field on every item of the project; and while more than one open project is linked, all of them are checked but none is written to, because which one is the repository's is the maintainer's call. `WF_PROJECT_TEMPLATE` lives in `.claude/settings.json` under `env`, with the other `WF_*` variables.

## Milestones and releases
Milestones are named `vX.Y.Z` and their description states the goal. Tickets are attached to one when they are cut, and the spec they refine belongs to the same milestone, so a release waits for its acceptance. A release is manual and closes a milestone: it refuses while the milestone has open issues, opens the promotion pull request from `dev` to `main` in the two-level model, then tags, creates the GitHub release with generated notes and closes the milestone ([ADR 0012](adr/0012-releases-are-manual-and-close-a-milestone.md)). The promotion is merged with a merge commit, and the tag goes on that commit ([ADR 0013](adr/0013-promotions-merge-with-a-merge-commit-and-releases-tag-it.md)). Standardisation creates none and closes only empty or orphaned open milestones: empty ones have no issues; orphaned ones have no open issues and a title that is not `vX.Y.Z`, so no release would ever close them.

## Backup
Before standardisation changes anything, `backup.sh` pushes a tag `pre-standard` on the head of the default branch on GitHub and protects it against deletion and moving with the tag ruleset. A tag that already exists is kept, never moved. It then opens one catalogue issue labelled `skill-candidate` with one row per removed skill (a directory with a `SKILL.md`, or a command file): name, description, origin (a skill lock file entry, a licence file in the skill, or else the auditor's reason), files and size, and the command that restores it from the tag. A second run updates the same issue ([ADR 0010](adr/0010-standardisation-audits-read-only-and-backs-up-before-deleting.md)).

## Applying the findings
`/repo-standards:apply` runs after the audit and refuses while a category is pending. Every step finds what an earlier run created, so a run that stopped anywhere continues when started again, and a run on a repository that already conforms changes nothing. Rejected categories are left untouched and named at the end.

Approval is per category, never per finding ([ADR 0016](adr/0016-approval-is-per-category-and-scripts-own-what-they-apply.md)). Within an approved category the run works through the `delete`, `replace` and `create` findings one by one, exactly as the report lists them, and `issue` findings become issues. A `configure` finding drives nothing of its own: approving `workspace` applies the whole difference between the GitHub workspace and the standard, recomputed when it runs. Approving a category `scaffold.sh` has templates for (`agent-config`, `docs`, `tests-ci`, `workspace`) also creates every baseline file of it that is missing, listed or not. Approving `agent-config` also brings `.claude/settings.json` to the template. Only a rejected category is left alone, so such a category without findings — which cannot be answered — is scaffolded too; the report names it. The report keeps those groups apart and says per category what approving it triggers beyond its lines.

1. Backup, as above. Nothing is deleted without the tag on GitHub and the catalogue issue. An empty repository (no branch on GitHub, no commit in the checkout) first gets an empty commit `chore: start the repository` on the default branch, so the cleanup pull request has a base; this is the init case of the same run. Local commits that were never pushed are never replaced: the maintainer pushes them first.
2. `cleanup.sh prepare` creates the branch `chore/standardize` in the worktree `.claude/worktrees/chore-standardize`, so the checkout is untouched. It removes the tracked targets of approved delete findings and runs `scaffold.sh` with `--skip` for each rejected category. That creates the missing baseline files, the `Makefile`, the CI job `check` when no workflow has one, `.github/dependabot.yml`, and `.claude/settings.json`. The settings file registers the marketplace and enables the workflow plugins through `claude plugin marketplace add` and `claude plugin install --scope project`. Every other plugin enabled at project scope is disabled. A target the tag does not hold as it is (untracked, or changed since a tag from an earlier run was set) is named for the maintainer instead of deleted, because the tag could not restore it. The agent then does the approved replace and create findings and fills in the placeholders.
3. `cleanup.sh open` refuses while a `<fill in>` placeholder is left, commits, pushes without force and opens one pull request. Its description lists what was removed per category, each with its restore command.
4. `issues.sh` opens one `ready-for-agent` issue per approved issue finding, titled `Standard (<category>): <target>`.
5. `finalize.sh` refuses until the pull request is merged, because the rulesets require the job `check` it brings. When the workspace category was approved it then runs `workspace.sh --apply`, which applies the whole workspace difference, recomputed at that moment and therefore possibly another one than the report showed; one line names the settings it changed without a finding and the audited ones that were already at the standard, and the run continues either way. It posts the snapshot as a comment on the catalogue issue. It removes the cleanup worktree and branch, runs the check on the default branch and reports pass or the remaining failures. A checkout that started without a commit is left so; `finalize.sh` names the `git pull` that brings the standard into it.
