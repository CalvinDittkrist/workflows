# {{REPO}}

<!-- Keep under 200 lines. Only what an agent cannot infer from the code. -->

## Commands
- Test: `{{TEST_CMD}}`
- Lint: `{{LINT_CMD}}`
- Build/run: `{{RUN_CMD}}`

## Conventions
- Branches: `<type>/<issue>-<slug>` (type: feat, fix, docs, chore). Conventional commits. No agent co-authors.
- Docs: `docs/architecture.md` is the map; decisions go to `docs/adr/`. Update both when a change makes them stale.
- Tests prove behaviour through public interfaces; no source-grepping tests.

## Gotchas
- <!-- non-obvious things: env setup, slow tests, flaky areas, forbidden operations -->
