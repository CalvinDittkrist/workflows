# 0048. Writing rules are part of the standard, and the gate checks the mechanical ones

Date: 2026-09-25
Status: accepted

## Context
- The documents of this repository grew without a limit: ADRs of over 1600 words, an architecture map of over 8000.
- Nothing checked prose, so every session added more of it, and every agent that loads a document pays for it.
- Refined in #175, built in #176.

## Decision
The repository standard states fixed [writing rules](../repo-standard.md#writing-rules); the standard check fails on the em dash and on every word cap, and the docs reviewer judges the rest.

## Consequences
- The gate of every repository that runs the workflow fails on an em dash or an oversized paragraph, bullet or document.
- A repository not rewritten yet sets `WF_WRITING_LENIENT=1` for its standard target and gets warnings instead.
- Sentence length, filler, hedging and metaphors stay with the docs reviewer, because a script cannot judge them.
- Rejected: a recurring agent run that rewrites the documents. It costs tokens on every run and proves nothing.
