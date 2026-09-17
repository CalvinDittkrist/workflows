# workflows

Public repository of Claude Code plugins for agent-driven development: an orchestrator that claims GitHub issues into Herdr worktree sessions, a worker pipeline with a fresh-context reviewer panel, and repository standards.

## Commands
- Test and validate everything: `scripts/test.sh` (shellcheck, `claude plugin validate --strict`, python unittest)
- Try a plugin without installing: `claude --plugin-dir plugins/<name>`
- Release a plugin: bump `version` in `plugins/<name>/.claude-plugin/plugin.json`, commit, `scripts/release.sh <name> --push`

## Conventions
- Scripts do, agents decide: anything deterministic lives in `plugins/*/scripts/*.sh` (bash 3.2 compatible, `set -euo pipefail`, `error:` lines on stderr with the fix). Skills are short prompts that call scripts.
- Every user-facing behaviour has a test in `tests/` that runs the real script with the `gh`/`herdr` shims in `tests/shims/`. Tests assert observable behaviour, never grep prompt text.
- Plugins are self-contained (no shared code across plugin directories); duplicated helpers in `lib.sh` are intentional.
- Docs: `docs/architecture.md` is the map, decisions are ADRs in `docs/adr/`. Update them with the change that makes them stale.
- No agent co-authors in commits. Conventional commits.

## Gotchas
- `claude plugin validate <dir>` validates a manifest, or a skills/agents directory; run it on both (see `scripts/test.sh`).
- Skill and agent frontmatter is checked by the runtime; unknown fields fail `--strict`.
- Herdr commands need `HERDR_ENV=1`; the orchestrator scripts refuse outside Herdr by design.
