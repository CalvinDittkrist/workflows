# workflows

Public repository of Claude Code plugins for agent-driven development: an orchestrator that claims GitHub issues into Herdr worktree sessions, a worker pipeline with a fresh-context reviewer panel, and repository standards. Beside the plugins, `factory/` is the factory: a Go service that works routed issues unattended on a host of its own, as a second driver over the same worker pipeline ([ADR 0022](docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)).

## Commands
- Gate: `make check` runs everything CI runs (shellcheck, `claude plugin validate --strict`, the standard check, python unittest, and the factory's gofmt, vet, staticcheck and Go tests); `make lint`, `make validate`, `make standard`, `make test`, `make factory` run one part
- Factory without tokens, git or GitHub: `go -C factory run . -fake -config <file>` works a canned queue with scripted workers; read it at `http://<listen>/api/line`
- Try a plugin without installing: `claude --plugin-dir plugins/<name>`
- Release a plugin: bump `version` in `plugins/<name>/.claude-plugin/plugin.json`, commit, `scripts/release.sh <name> --push`

## Conventions
- Scripts do, agents decide: anything deterministic lives in `plugins/*/scripts/*.sh` (bash 3.2 compatible, `set -euo pipefail`, `error:` lines on stderr with the fix). Skills are short prompts that call scripts.
- Every user-facing behaviour has a test in `tests/` that runs the real script with the `gh`/`herdr` shims in `tests/shims/`, and the factory's has a Go test in `factory/` that starts the real binary. Tests assert observable behaviour, never grep prompt text.
- Plugins are self-contained (no shared code across plugin directories); duplicated helpers in `lib.sh` are intentional. The label vocabulary is duplicated the same way, and a test in `tests/test_plugins.py` fails when the two copies drift apart.
- Docs: `docs/architecture.md` is the map, decisions are ADRs in `docs/adr/`, terms are in `docs/glossary.md`, the standard every repository follows is `docs/repo-standard.md`. Update them with the change that makes them stale.
- `AGENTS.md` is the instruction source for every agent; `CLAUDE.md` only imports it. No repository-local skills, agents, commands or rules (the standard check fails on them).
- No agent co-authors in commits. Conventional commits.

## Gotchas
- `claude plugin validate <dir>` validates a manifest, or a skills/agents directory; run it on both (see the `validate` target in the `Makefile`).
- Skill and agent frontmatter is checked by the runtime; unknown fields fail `--strict`.
- Herdr commands need `HERDR_ENV=1`; the orchestrator scripts refuse outside Herdr by design.
- The factory is the one part that is not shell: a Go module in `factory/` with no dependencies, tests that start the real binary and watch it over HTTP and its data directory, and `staticcheck` pinned in the `factory` target's error line.
- A skill's `` !`command` `` runs through the permission system. Forked skills (`context: fork`) fail silently without a matching `allowed-tools: Bash(${CLAUDE_PLUGIN_ROOT}/scripts/x.sh)` rule, so every injection calls a plugin script and lists it there (tested).
- This repository develops the plugins, so `.claude/settings.json` enables only `repo-standards@workflows` and `make standard` warns that `orchestrator`, `planner` and `worker` are off; sessions load the other plugins from the checkout with `--plugin-dir` (see `scripts/dev-orchestrator.sh`).
