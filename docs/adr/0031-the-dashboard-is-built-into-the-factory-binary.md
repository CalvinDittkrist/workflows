# 0031. The dashboard is built into the factory binary

Date: 2026-09-21
Status: accepted
Extends: [0023](0023-github-is-the-only-control-surface-of-the-factory.md) (the reading surface the control surface leaves open)

## Context
The factory answers four read-only endpoints. JSON is enough to debug it and not enough to watch it: the maintainer wants to see, from a phone on the tailnet, what runs, what waits, what a run cost and where it stopped.

A page needs a build step, and a build step is the first thing in this repository that is neither shell nor Go. The alternatives were a server-rendered page from Go templates, which keeps the toolchain but polls the whole page and hand-writes the markup the prototype already solved in React, and a separate static server beside the factory, which puts a second process, a second port and a second unit on a host whose whole isolation argument is that it runs one thing ([ADR 0027](0027-the-factorys-isolation-boundary-is-the-host.md)).

## Decision
The dashboard is a single-page app in `factory/ui`, built by Vite into `factory/ui/dist/app` and embedded into the binary with `//go:embed`. The binary serves it at `/`, the endpoints move under `/api`, and the fonts it renders in are served from the binary too.

It reads and never writes. It calls the same four endpoints any other reader calls, and the interface keeps refusing everything that is not `GET` or `HEAD`, so the dashboard cannot become a control surface by growing a button ([ADR 0023](0023-github-is-the-only-control-surface-of-the-factory.md)).

The gate gains the dashboard's lint, its build and one browser test that drives the real binary in fake mode ([ADR 0008](0008-make-check-is-the-single-gate.md)).

## Consequences
The host runs one process from one binary. Deploying the factory is copying that binary; there is no web root, no reverse proxy and no Node on the host.

A fresh clone has to build the dashboard before the factory's Go tests pass, because they read it out of the binary. `make check` does that itself, and `factory/ui/dist` is in git with a placeholder so a build without the dashboard still compiles — it then answers `/` with a short line that names `make ui`.

Node joins the toolchain for developing this repository, and Chromium joins it for the browser test. The gate installs the npm dependencies and that Chromium itself, and refuses with the fix when npm is not there; the CI job brings Node and Chromium with it. Neither is needed to run a factory.

The browser test is the only test here that judges pixels. It masks what counts up, asserts the three panes numerically, and compares the rest to an approved screenshot. Layout is the same everywhere, the rasterisation of glyphs is not, so the baseline is per operating system and the one for a platform is approved on it: a platform without one fails the first time and is approved from what that run saw.
