# Architecture Decision Records

One file per decision, numbered, never edited after acceptance (supersede instead). Format: [MADR](https://adr.github.io/madr/), trimmed. Create one with `/repo-standards:adr <title>`.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-distribute-as-claude-code-plugin-marketplace.md) | Distribute as a Claude Code plugin marketplace | accepted |
| [0002](0002-scripts-do-agents-decide.md) | Scripts do, agents decide | accepted |
| [0003](0003-herdr-worktree-per-issue.md) | One Herdr worktree workspace per issue, branch name as contract | accepted |
| [0004](0004-reviewers-as-fresh-read-only-subagents.md) | Reviewers are fresh-context, read-only subagents | accepted |
| [0005](0005-sandboxing-strategy.md) | Sandboxing strategy: layered, Docker Sandboxes opt-in | accepted |
| [0006](0006-planner-session-writes-issues-not-code.md) | Planning is its own session that writes issues, not code | accepted |
| [0007](0007-agents-md-is-the-instruction-source.md) | AGENTS.md is the instruction source and CLAUDE.md imports it | accepted |
| [0008](0008-make-check-is-the-single-gate.md) | make check is the single gate and check the single required status check | accepted |
| [0009](0009-profile-derived-from-github-with-two-branch-models.md) | The profile is derived from GitHub, with exactly two branch models | accepted |
| [0010](0010-standardisation-audits-read-only-and-backs-up-before-deleting.md) | Standardisation audits read-only, deletes through a pull request and backs up with a protected tag | accepted |
| [0011](0011-github-workspace-configured-by-an-idempotent-script.md) | The GitHub workspace is configured by an idempotent script with rulesets and no bypass | accepted |
| [0012](0012-releases-are-manual-and-close-a-milestone.md) | Releases are manual and close a milestone | accepted |
