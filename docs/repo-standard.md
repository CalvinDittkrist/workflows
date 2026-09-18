# Repository standard

The baseline every repository that runs this workflow is held to. `plugins/repo-standards/scripts/check.sh` (or `/repo-standards:docs-check`) verifies the file rules offline and in CI; `/repo-standards:init-repo` creates the baseline in a new repository and never overwrites. The sections from GitHub workspace on are the target state; the scripts that apply them are not built yet (#5, #7, #8). Terms are defined in the [glossary](glossary.md).

## Profile
A repository's profile is its visibility plus its branch model. Both are derived from GitHub, never configured per repository ([ADR 0009](adr/0009-profile-derived-from-github-with-two-branch-models.md)).

- **Branch model.** `main` alone, or `dev` plus `main` when the default branch is `dev`. With two levels, work merges into `dev` and a promotion pull request from `dev` to `main` carries a release. No third level exists.
- **Public versus private.** Public repositories add a licence, a security policy (`SECURITY.md`), secret scanning, push protection and private vulnerability reporting. Everything else is the same.

## Files that stay
| File | Purpose | Checked |
| --- | --- | --- |
| `README.md` | What the repository is and how to use it | no |
| `AGENTS.md` | Instruction source for every agent: commands and conventions an agent cannot infer; under 200 lines | fails if missing; warns over 200 lines or with `<fill in>` left |
| `CLAUDE.md` | The line `@AGENTS.md`, optionally a short Claude-only section | fails if missing or without the import; warns over 200 lines |
| `Makefile` | The gate: a `check` target | fails without a `check` target; warns with `<fill in>` left |
| `docs/architecture.md` | One-page map: components, data flow, boundaries | fails if missing or under 15 lines |
| `docs/adr/README.md` + `NNNN-title.md` | Decisions, MADR-trimmed, numbered, each with a Status line | fails on a missing index, duplicate numbers or a missing Status |
| `docs/glossary.md` | Terms the code and issues use | no |
| `.github/PULL_REQUEST_TEMPLATE.md` | Closes, what and why, verification, limits | warns if missing |
| `.claude/settings.json` | Marketplace, enabled plugins, `WF_*` env, permission allowlist, attribution off | warns on missing plugins or attribution |
| Operational docs | Pages that describe the present state (runbooks, local setup) | no |
| `LICENSE`, `SECURITY.md` | Public repositories only | no |

## Files that go
Everything below is removed unless the standard defines it; there is no allowlist per repository.

- Repository-local skills, commands, subagents, rules, hooks and MCP configuration. An MCP configuration stays only when something in the repository uses it.
- Configuration of other agent tools: `.cursor/`, `.cursorrules`, `.windsurf/`, `.clinerules`, `.roo/`, `.codex/`, `.gemini/`, `GEMINI.md`, `.agents/`, `.aider*`, `.github/copilot-instructions.md` and similar.
- Skill lock files (`skills-lock.json`, `.skill-lock.json`).
- Context, resume and review notes; dated audit reports; planning material such as feature specs, user stories, personas and research notes. Plans live in issues.
- GitHub Actions that run an AI reviewer or agent.

The check fails on every tracked or untracked, not ignored path under `.claude/` other than `settings.json`, `settings.local.json` and `worktrees/`, on the configuration of other agent tools and on skill lock files, and names each one (`.claude/skills/<name>`, `.cursor/rules/<file>`). The other categories need judgement and are not checked by the script.

## Instruction files
`AGENTS.md` is the source; `CLAUDE.md` imports it with `@AGENTS.md`, because Claude Code reads `CLAUDE.md` and not `AGENTS.md` ([ADR 0007](adr/0007-agents-md-is-the-instruction-source.md)). Both stay under 200 lines. A monorepo may keep one such pair per area (`services/api/AGENTS.md` and `services/api/CLAUDE.md`); an area's pair loads only when an agent works there, and the check holds each pair to the same rules. Claude Code loads every nested `CLAUDE.md` it passes, so a `CLAUDE.md` or `AGENTS.md` kept as data (a template, a fixture) is instructions too and must form a valid pair or be renamed.

## The gate
Every repository has a `Makefile`, and `make check` runs everything CI gates on ([ADR 0008](adr/0008-make-check-is-the-single-gate.md)). Agents run `make check` instead of guessing a per-repository test command. CI runs it in a job named `check`, and `check` is the one required status check. A repository with several CI jobs keeps them parallel, each calling its own make target, and adds an aggregating job named `check`.

## GitHub workspace
Set by an idempotent script that shows the difference first and keeps a snapshot of the previous state ([ADR 0011](adr/0011-github-workspace-configured-by-an-idempotent-script.md)). It runs only after the gate exists, because the ruleset requires the `check` status.

- Merges: squash only, PR title as commit title, branches deleted on merge; wiki and discussions off.
- One branch ruleset on `main`, and on `dev` when present: pull request required, zero approvals, conversations resolved, required check `check`, linear history, no force push, no deletion, no bypass.
- One tag ruleset that protects `pre-standard` from deletion and moving.
- Labels: the workflow vocabulary plus `skill-candidate`.
- Dependabot alerts, security updates and grouped version updates; read-only default token for Actions.
- Public repositories: secret scanning, push protection, private vulnerability reporting.

## Projects
One GitHub project per product repository, copied from a template project, with status Triage, Ready, In progress, In review, Done and priority P0 to P3. Settings the API cannot set are printed as manual steps.

## Milestones and releases
Milestones are named `vX.Y.Z` and their description states the goal. Tickets are attached to one when they are cut. A release is manual and closes a milestone: it refuses while the milestone has open issues, opens the promotion pull request from `dev` to `main` in the two-level model, then tags, creates the GitHub release with generated notes and closes the milestone ([ADR 0012](adr/0012-releases-are-manual-and-close-a-milestone.md)). Standardisation closes only empty or orphaned milestones.

## Backup
Before standardisation changes anything, it pushes a tag `pre-standard` on the current head, protected against deletion and moving, and opens one catalogue issue labelled `skill-candidate` with one row per removed skill: name, description, origin, files and size, and the command that restores it from the tag. Deletions and new baseline files arrive in one pull request from `chore/standardize` ([ADR 0010](adr/0010-standardisation-audits-read-only-and-backs-up-before-deleting.md)).
