---
name: spec
description: Turn the current conversation into one spec issue. Synthesis only, no new questions.
disable-model-invocation: true
allowed-tools: Bash(${CLAUDE_PLUGIN_ROOT}/scripts/facts.sh)
---
Session:
!`${CLAUDE_PLUGIN_ROOT}/scripts/facts.sh`

Write the spec from what the conversation has settled. Do not interview. If something material is still open, list it under Open questions instead of guessing.

1. If you have not explored the code yet, do it now: the modules the change touches, the tests around them, the ADRs in the area.
2. Propose the test seams: where the behaviour is verified end to end. Prefer existing seams; as few and as high as possible. One line to the user; continue unless they object.
3. Write the body with [template.md](template.md) to a temp file and publish it:

       "${CLAUDE_PLUGIN_ROOT}/scripts/issue.sh" create --title "<title>" --body-file <file> --label spec

   When the session started from an issue, put `Refines #N` at the top of the body.
4. Reply with the issue URL and `next: /planner:tickets`. If the whole spec fits one agent session, say so; tickets will then label the spec itself.

No file paths and no code in the spec; they go stale. One exception: a prototype result that states a decision more precisely than prose (a type, a schema, a state table) may be quoted, trimmed to the decision.
