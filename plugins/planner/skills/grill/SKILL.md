---
name: grill
description: Interview the user in numbered rounds until the design has no open branch left. Facts come from the repo, decisions from the user.
disable-model-invocation: true
argument-hint: [topic]
---
Interview the user about $ARGUMENTS (or the session topic) until you share one understanding. Do not act on it.

Model the design as a tree: every decision opens the decisions that depend on it. The frontier is every question whose prerequisites are settled. Ask the whole frontier in one round, numbered, each with your recommended answer. Wait for the answers. Then recompute the frontier and ask the next round. A question that depends on another question still open in this round belongs to the next round.

Round format:

    **Q1 <title>**
    <question; options when there are a few>
    Recommended: <answer and why, one line>

    **Q2 <title>**
    ...

Rules:
- Facts are your job.
- When a question needs a fact from the code, the docs or a tool, look it up or spawn a subagent; never ask the user for it.
- Ask the rest of the frontier while it runs.
- Decisions are the user's. Never answer your own question.
- Test relationships with concrete scenarios and edge cases. Check claims about the code against the code and say when they differ.
- Vocabulary: when a term conflicts with `docs/glossary.md` or is fuzzy, ask for the precise term. Keep a list of new or changed terms; the spec carries it.
- Decisions that are hard to reverse and surprising without context go on an ADR list; the spec carries it, the implementing worker writes the ADRs.
- The interview ends when the frontier is empty. Then list the decisions, the glossary terms and the ADR candidates, and ask for confirmation. Next: `/planner:spec`.
