# 0004. Reviewers are fresh-context, read-only subagents

Date: 2026-09-17
Status: accepted

## Context
The author of a change is the worst judge of it. Reviews must be independent of the implementation context, cheap to run in parallel, unable to damage the tree, and consistent across repositories. Separate reviewer sessions per worktree would multiply panes and tokens.

## Decision
Five reviewer agents (code, security, docs, tests, senior) are Claude Code subagents with `disallowedTools: Edit, Write, NotebookEdit, Agent`, launched in parallel by `/worker:review` with the same brief. Each returns a fixed-format report (S1/S2/S3, PASS/FIX). The worker fixes and re-runs only the reviewers that returned FIX, bounded by `WF_REVIEW_ROUNDS`. The PR is opened by another fresh-context agent (`pr-author`) so its description reflects the diff. An external reviewer (Codex on the PR) remains the model-independent gate.

## Consequences
Reviews cost one context each, never see the author's reasoning, and cannot edit. Disagreements surface in the review summary instead of being dropped. The docs reviewer uses a smaller model and omits CLAUDE.md because slop detection does not need repository context; repositories can change models by overriding the agent file.
