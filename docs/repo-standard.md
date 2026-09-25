# Repository standard

The baseline every repository that runs this workflow is held to. Terms are defined in the [glossary](glossary.md).

- `plugins/repo-standards/scripts/check.sh` (or `/repo-standards:docs-check`) verifies the file rules and the [writing rules](#writing-rules) offline and in CI.
- `/repo-standards:standardize` audits a repository with six read-only auditors, one per area, and records the maintainer's approval per category. On an empty repository every finding is a create action.
- `plugins/repo-standards/scripts/workspace.sh` brings the GitHub workspace and its milestones to the standard. The check reports its differences as warnings when GitHub is reachable.
- `/repo-standards:apply` applies the approved findings: backup, cleanup pull request, issues, then the workspace and the check.

## Profile
A repository's profile is its visibility plus its branch model. Both are derived from GitHub, never configured per repository ([ADR 0009](adr/0009-profile-derived-from-github-with-two-branch-models.md)).

- **Branch model.** `main` alone, or `dev` plus `main` when the default branch is `dev`. No third level exists.
- **Two levels.** With `dev` plus `main`, work merges into `dev` and a promotion pull request from `dev` to `main` carries a release.
- **Public versus private.** Public repositories add a licence, a security policy (`SECURITY.md`), secret scanning, push protection and private vulnerability reporting. Everything else is the same.

## Files that stay
| File | Purpose | Checked |
| --- | --- | --- |
| `README.md` | What the repository is and how to use it | fails if missing or over its word cap; warns with `<fill in>` left |
| `AGENTS.md` | Instruction source for every agent: commands and conventions an agent cannot infer; under 200 lines | fails if missing; warns over 200 lines or with `<fill in>` left |
| `CLAUDE.md` | The line `@AGENTS.md`, optionally a short Claude-only section | fails if missing or without the import; warns over 200 lines |
| `Makefile` | The gate: a `check` target | fails without a `check` target; warns with `<fill in>` left |
| `.github/workflows/*.yml` | A job named `check` that runs `make check` | fails without one |
| `docs/architecture.md` | Map: purpose, components, data flow, boundaries, decisions | fails if missing, under 15 lines or over its word cap |
| `docs/adr/README.md` + `NNNN-title.md` | Decisions, MADR-trimmed, numbered, each with a Status line | fails on a missing index, duplicate numbers, a missing Status or an ADR over its word cap |
| `docs/glossary.md` | Terms the code and issues use, one row each | warns if missing; fails on an entry over its word cap |
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

The check fails on each of these and names it, such as `.claude/skills/<name>` or `.cursor/rules/<file>`:

- every tracked or untracked, not ignored path under `.claude/` other than `settings.json`, `settings.local.json` and `worktrees/`
- `CLAUDE.local.md`, `AGENT.md`, `.rules` and `.worktreeinclude`
- the configuration of other agent tools and skill lock files
- workflow steps that use a known AI reviewer or agent action: `anthropics/claude-code-action`, `openai/codex-action`, `google-github-actions/run-gemini-cli`, `coderabbitai/*`

An `.mcp.json` only warns, because it stays when something uses it. Notes, planning material and dated reports need judgement and are left to the auditors.

## Instruction files
`AGENTS.md` is the source; `CLAUDE.md` imports it with `@AGENTS.md`, because Claude Code reads `CLAUDE.md` and not `AGENTS.md` ([ADR 0007](adr/0007-agents-md-is-the-instruction-source.md)). Both stay under 200 lines.

- A monorepo may keep one such pair per area (`services/api/AGENTS.md` and `services/api/CLAUDE.md`).
- An area's pair loads only when an agent works there, and the check holds each pair to the same rules.
- Claude Code loads every nested `CLAUDE.md` it passes. So a `CLAUDE.md` or `AGENTS.md` kept as data, such as a template or a fixture, is instructions too.
- Such a file must form a valid pair or be renamed.

## Writing rules
The rules for prose in documents, prompts and comments ([ADR 0048](adr/0048-writing-rules-are-part-of-the-standard-and-the-gate-checks-the-mechanical-ones.md)):

- No em dash, in any text file.
- A paragraph has at most 80 words; longer content becomes bullets.
- A bullet has at most 30 words.
- A sentence has at most 25 words.
- No metaphors, no filler, no hedging.
- No session ids, dates or measurements told as a story; a fact is one line or it goes.
- An ADR has at most 250 words, headings included.
- The architecture map has at most 2000 words, the README 1200, a plugin README 800, a glossary entry 40.

The check counts the em dash in every text file and the word caps in Markdown. Sentence length, filler, hedging and metaphors are judged by the docs reviewer. Nothing is measured in lines.

- A word is a whitespace-separated token that is not punctuation alone.
- A bullet is a list item, numbered or not; a glossary entry is a table row of `docs/glossary.md`.
- A plugin README is `plugins/<name>/README.md`, so its cap applies in a repository of plugins.
- Code blocks (fenced or indented), closed front matter, thematic breaks and tables are no paragraphs.
- A document's count skips code blocks and front matter. A README that is not Markdown is counted whole.

Each finding fails the check. A repository not rewritten yet sets `WF_WRITING_LENIENT=1` for the check in its `Makefile`, which turns them into warnings. The check at the end of `/repo-standards:apply` warns on them too, because rewriting the documents is an issue of its own. An accepted ADR may be shortened in wording; its decision is never edited ([ADR 0049](adr/0049-an-accepted-adr-may-be-shortened-in-wording-its-decision-is-never-edited.md)).

The templates in `plugins/repo-standards/templates/` are the fixed form of each document: `README.md.tpl`, `plugin-README.md`, `architecture.md`, `adr-template.md` and `glossary.md`.

## The gate
Every repository has a `Makefile`, and `make check` runs everything CI gates on ([ADR 0008](adr/0008-make-check-is-the-single-gate.md)). Agents run `make check` instead of guessing a per-repository test command. CI runs it in a job named `check`, and `check` is the one required status check. A repository with several CI jobs keeps them parallel, each calling its own make target, and adds an aggregating job named `check`.

## GitHub workspace
Set by an idempotent script that shows the difference first and keeps a snapshot of the previous state ([ADR 0011](adr/0011-github-workspace-configured-by-an-idempotent-script.md)).

- `workspace.sh` prints one `diff:` line per difference and changes nothing.
- `workspace.sh --apply` writes the previous state as JSON to a snapshot file, then makes exactly those changes.
- The snapshot file is `--snapshot <file>`, or a temporary file it names. Applying needs admin rights.
- It refuses to apply while the default branch has no run of a CI job named `check`, because the ruleset requires that status.
- It refuses too while the default branch is neither `main` nor `dev`.
- What the account's plan or the gh token cannot reach is printed as a `manual:` step, and the rest still runs.
- Examples are rulesets on a private repository on GitHub Free, and projects without the `project` scope.

The standard settings:

- Merges: squash only, PR title as commit title, branches deleted on merge; wiki and discussions off.
- With `dev` plus `main`, merge commits are allowed too. Only the promotion pull request uses them, so `dev` stays an ancestor of `main` ([ADR 0013](adr/0013-promotions-merge-with-a-merge-commit-and-releases-tag-it.md)).
- One branch ruleset on `main`, and on `dev` when present. The rulesets are named `standard: main` and `standard: dev`.
- The branch ruleset requires a pull request, zero approvals, resolved conversations and the check `check`, and allows no force push, no deletion and no bypass.
- It requires linear history, except on `main` with `dev` plus `main`.
- The script replaces the standard rulesets when they drift and leaves other rulesets alone.
- Classic branch protection on those branches is removed once the ruleset is in place.
- One tag ruleset, `standard: pre-standard`, that protects `pre-standard` from deletion and moving.
- Labels: the workflow vocabulary plus `skill-candidate`. Missing labels are created; other labels stay.
- Dependabot alerts and security updates; read-only default token for Actions that cannot approve pull requests.
- Grouped version updates are a file, `.github/dependabot.yml`, and arrive with the cleanup pull request.
- Public repositories: secret scanning, push protection, private vulnerability reporting.
- A private repository owned by a personal account cannot get secret scanning or push protection. The script prints that as a manual step.
- GitHub offers them only through Secret Protection for organisations on Team or Enterprise.

## Projects
One GitHub project per product repository, copied from a template project. It carries two single-select fields:

- `Status`, with the options Triage, Ready, In progress, In review, Done
- `Priority`, with P0, P1, P2, P3

Field and option names are compared ignoring case, and the order of the options does not matter. The set must be exactly the standard's.

`workspace.sh` changes what the API can change without losing data:

- When no project is linked and `WF_PROJECT_TEMPLATE=<owner>/<number>` names the template, `--apply` copies it and links the copy. The next run checks the copy's fields.
- A missing `Status` or `Priority` is created with its options.

The rest is printed as `manual:` steps:

- The API cannot create or turn on project workflows, and a copy leaves out the template's auto-add workflow. So auto-add is turned on by hand.
- A field that is there with other options or of another type is changed by hand. Replacing an option list clears that field on every item of the project.
- While more than one open project is linked, all of them are checked but none is written to. Which one is the repository's is the maintainer's call.

`WF_PROJECT_TEMPLATE` lives in `.claude/settings.json` under `env`, with the other `WF_*` variables.

## Milestones and releases
Milestones are named `vX.Y.Z` and their description states the goal. Tickets are attached to one when they are cut. The spec they refine belongs to the same milestone, so a release waits for its acceptance.

A release is manual and closes a milestone ([ADR 0012](adr/0012-releases-are-manual-and-close-a-milestone.md)):

- It refuses while the milestone has open issues.
- In the two-level model it opens the promotion pull request from `dev` to `main`.
- Then it tags, creates the GitHub release with generated notes and closes the milestone.
- The promotion is merged with a merge commit, and the tag goes on that commit ([ADR 0013](adr/0013-promotions-merge-with-a-merge-commit-and-releases-tag-it.md)).

Standardisation creates no milestone and closes only empty or orphaned open milestones. Empty ones have no issues. Orphaned ones have no open issues and a title that is not `vX.Y.Z`, so no release would ever close them.

## Backup
Before standardisation changes anything, `backup.sh` pushes a tag `pre-standard` on the head of the default branch on GitHub. It protects the tag against deletion and moving with the tag ruleset. A tag that already exists is kept, never moved.

It then opens one catalogue issue labelled `skill-candidate`, with one row per removed skill ([ADR 0010](adr/0010-standardisation-audits-read-only-and-backs-up-before-deleting.md)):

- A removed skill is a directory with a `SKILL.md`, or a command file.
- A row holds the name, description, origin, files and size, and the command that restores it from the tag.
- The origin is a skill lock file entry, a licence file in the skill, or else the auditor's reason.
- A second run updates the same issue.

## Applying the findings
`/repo-standards:apply` runs after the audit and refuses while a category with findings is pending. Every step finds what an earlier run created. So a run that stopped anywhere continues when started again, and a run on a conforming repository changes nothing. Rejected categories are left untouched and named at the end.

Approval is per category, never per finding ([ADR 0016](adr/0016-approval-is-per-category-and-scripts-own-what-they-apply.md)):

- Within an approved category the run works through the `delete`, `replace` and `create` findings one by one, exactly as the report lists them.
- `issue` findings become issues.
- A `configure` finding drives nothing of its own. Approving `workspace` applies the whole difference between the GitHub workspace and the standard, recomputed when it runs.
- `scaffold.sh` has templates for `agent-config`, `docs`, `tests-ci` and `workspace`. Approving one of them also creates every missing baseline file of it, listed or not.
- Approving `agent-config` also brings `.claude/settings.json` to the template.

Only a rejected category is left alone. So the report asks about a scaffolded category even when no auditor reported anything for it ([ADR 0035](adr/0035-every-category-the-apply-phase-scaffolds-is-answerable.md)):

- Approving it creates its missing baseline files, and rejecting it keeps the apply phase out.
- Leaving it unanswered scaffolds it as an approval would.
- Approving it answers for those files alone. `workspace.sh --apply` runs only when the audit had a `configure` finding for `workspace` and the category was approved.
- That finding is what the report says applies the whole workspace difference.
- `approve.sh` keeps the two apart. A category with findings that is unanswered is `pending` and stops the apply phase.
- A category that is only scaffolded is `unanswered` and does not stop it.
- A category that has no findings and is never scaffolded has nothing to answer, and `approve.sh` refuses it.
- The report keeps those groups apart, and says per category what approving it triggers beyond its lines.

The run:

1. Backup, as above. Nothing is deleted without the tag on GitHub and the catalogue issue.
   - An empty repository has no branch on GitHub and no commit in the checkout. It first gets an empty commit `chore: start the repository` on the default branch.
   - That gives the cleanup pull request a base; this is the init case of the same run.
   - Local commits that were never pushed are never replaced: the maintainer pushes them first.
2. `cleanup.sh prepare` creates the branch `chore/standardize` in the worktree `.claude/worktrees/chore-standardize`, so the checkout is untouched.
   - It removes the tracked targets of approved delete findings, and runs `scaffold.sh` with `--skip` for each rejected category.
   - That creates the missing baseline files, the `Makefile`, the CI job `check` when no workflow has one, `.github/dependabot.yml`, and `.claude/settings.json`.
   - The settings file registers the marketplace and enables the workflow plugins, through `claude plugin marketplace add` and `claude plugin install --scope project`.
   - Every other plugin enabled at project scope is disabled.
   - A target the tag does not hold as it is, untracked or changed since an earlier tag, is named for the maintainer instead of deleted.
   - The tag could not restore such a target.
   - The agent then does the approved replace and create findings and fills in the placeholders.
   - A later `approve.sh` answer replaces an earlier one, so an answer can change between two `prepare` runs.
   - So each run, fresh or resumed, first restores what a rejected category or finding touched, file by file.
   - It restores every file `scaffold.sh` writes for a rejected category, and every target of a rejected finding.
   - Each comes back as the default branch had it where the branch forked.
   - A file stays when it is, or lies under, the target of an approved finding or a baseline file of a category not rejected.
   - So a rejected directory keeps what another category put inside it. It prints `restored: <path> (rejected)` for each.
   - Rejecting a category after a `prepare` takes its baseline files and its `.claude/settings.json` changes back off the branch.
   - It also takes back its deletions and the agent's edits to its targets.
   - Approving a category after a rejection scaffolds it on the next run.
3. `cleanup.sh open` refuses while a `<fill in>` placeholder is left, commits, pushes without force and opens one pull request.
   - Its description lists what was removed per category, each with its restore command.
4. `issues.sh` opens one `ready-for-agent` issue per approved issue finding, titled `Standard (<category>): <target>`.
5. `finalize.sh` refuses until the pull request is merged, because the rulesets require the job `check` it brings.
   - When the workspace category had a `configure` finding and was approved, it then runs `workspace.sh --apply`.
   - That applies the whole workspace difference, recomputed at that moment, so possibly another one than the report showed.
   - One line names the settings it changed without a finding, and the audited ones that were already at the standard. The run continues either way.
   - It posts the snapshot as a comment on the catalogue issue.
   - It removes the cleanup worktree and branch, runs the check on the default branch and reports pass or the remaining failures.
   - A checkout that started without a commit is left so; `finalize.sh` names the `git pull` that brings the standard into it.
