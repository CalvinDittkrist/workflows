# 0004. Reviewers are fresh-context, read-only subagents

Date: 2026-09-17
Status: accepted, amended
Amended by: [0010](0010-standardisation-audits-read-only-and-backs-up-before-deleting.md) (repositories no longer override agent files), [0019](0019-the-gate-runs-once-per-review-round.md) (reviewers no longer run the gate; its result reaches them as a fact in the brief)

## Context
- The author of a change is the worst judge of it.
- Reviews must be independent of the implementation context, cheap in parallel, unable to damage the tree and consistent across repositories.
- Separate reviewer sessions per worktree would multiply panes and tokens.

## Decision
Five reviewer agents (code, security, docs, tests, senior) are read-only Claude Code subagents, launched in parallel by `/worker:review` with the same brief.

## Consequences
- Each has `disallowedTools: Edit, Write, NotebookEdit, Agent`.
- Each returns a fixed-format report (S1/S2/S3, PASS/FIX).
- The worker fixes and re-runs only the reviewers that returned FIX, bounded by `WF_REVIEW_ROUNDS`.
- Another fresh-context agent, `pr-author`, opens the PR, so its description reflects the diff.
- An external reviewer, Codex on the PR, remains the model-independent gate.
- Reviews cost one context each, never see the author's reasoning and cannot edit.
- Disagreements surface in the review summary.
- The docs reviewer uses a smaller model and omits CLAUDE.md, because slop detection needs no repository context.
- Rejected: separate reviewer sessions per worktree, for their panes and tokens.
