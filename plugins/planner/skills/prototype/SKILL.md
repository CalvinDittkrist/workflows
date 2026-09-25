---
name: prototype
description: Build throwaway code that answers one design question, then move it to its own branch and link it from the issue.
disable-model-invocation: true
argument-hint: <question>
---
A prototype is throwaway code that answers one question: $ARGUMENTS

Pick the shape from the question:
- "Does this logic or state model hold?" One runnable file: a script, or a single HTML page a non-developer can click through.
  - It drives the model through the cases that are hard to reason about on paper and prints the full state after every step.
- "What should this look like?" Several clearly different variants of one screen, switchable in place, in the project's own routing convention.

Rules: name it so a reader sees it is a prototype; one command to run; state in memory only; no tests, no error handling beyond what makes it run, no abstractions. Tell the user how to run it and what to look at.

When the question is answered: state the verdict in one paragraph, then run

    "${CLAUDE_PLUGIN_ROOT}/scripts/capture-prototype.sh" <name>

It moves the code to branch `prototype/<plan>-<name>`, pushes it and prints the URL. Put the verdict and the URL into the spec or ticket. The plan branch stays clean.
