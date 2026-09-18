# {{REPO}}

<!-- The instruction source for every agent; CLAUDE.md imports it. Keep under 200 lines. Only what an agent cannot infer from the code. -->

## Commands
- Gate: `make check` runs everything CI gates on. Run it before you push.
- Build/run: `{{RUN_CMD}}`

## Conventions
- Branches: `<type>/<issue>-<slug>` (type: feat, fix, docs, chore). Conventional commits. No agent co-authors.
- Docs: `docs/architecture.md` is the map; decisions go to `docs/adr/`. Update both when a change makes them stale.
- Tests prove behaviour through public interfaces; no source-grepping tests.

## Gotchas
- <!-- non-obvious things: env setup, slow tests, flaky areas, forbidden operations -->
