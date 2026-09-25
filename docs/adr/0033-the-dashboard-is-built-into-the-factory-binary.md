# 0033. The dashboard is built into the factory binary

Date: 2026-09-21
Status: accepted
Extends: [0023](0023-github-is-the-only-control-surface-of-the-factory.md) (the reading surface the control surface leaves open)

## Context
- The factory answers four read-only endpoints. JSON serves debugging, but the maintainer wants to watch runs, waits and costs from a phone.
- A page needs a build step, the first thing here that is neither shell nor Go.

## Decision
The dashboard is a Vite single-page app in `factory/ui`, embedded into the binary with `//go:embed` and served at `/`, with the endpoints under `/api`.

## Consequences
- It reads and never writes. The interface refuses everything but `GET` and `HEAD`, so no button makes it a control surface ([ADR 0023](0023-github-is-the-only-control-surface-of-the-factory.md)).
- The binary serves the fonts too. Deploying is copying one binary, with no web root, reverse proxy or Node on the host.
- The gate runs the dashboard's lint, build and a browser test against the real binary in fake mode ([ADR 0008](0008-make-check-is-the-single-gate.md)).
- The Go tests need the build first. `factory/ui/dist` keeps a placeholder, which answers `/` with a line naming `make ui`.
- Node and Chromium join the development toolchain, not the host's.
- The browser test compares an approved screenshot per operating system, because glyph rasterisation differs.
- Rejected: Go templates, which poll whole pages and hand-write markup the prototype solved in React.
- Rejected: a separate static server, a second process on a host that runs one thing ([ADR 0027](0027-the-factorys-isolation-boundary-is-the-host.md)).
