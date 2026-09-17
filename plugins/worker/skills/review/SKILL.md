---
name: review
description: Run the independent reviewer panel (code, security, docs, tests, senior) on the branch diff in fresh contexts and fix the findings until the panel passes.
argument-hint: [reviewers=code,security,docs,tests,senior]
---
Diff context:
!`"${CLAUDE_PLUGIN_ROOT}/scripts/diff-context.sh"`

Reviewers: !`echo "${WF_REVIEWERS:-code,security,docs,tests,senior}"` · Max rounds: !`echo "${WF_REVIEW_ROUNDS:-3}"`

Round procedure:
1. If there are uncommitted changes, commit them first; reviewers read committed history.
2. Launch every listed reviewer **in parallel, in one message** with the Agent tool. Map names to agents: code → `worker:code-reviewer`, security → `worker:security-reviewer`, docs → `worker:docs-reviewer`, tests → `worker:test-reviewer`, senior → `worker:senior-reviewer`. Give each the same brief: the diff range, the base ref, the issue number and title, and this instruction: "Review range <range>. Read-only. Use the report format from your instructions."
3. Collect the reports. Fix every S1 and S2 in the main context. For an S3, fix it if it is cheap, otherwise leave it. If you disagree with an S1 or S2, do not drop it silently: keep it in the final summary as `disputed:` with your reason so the user and the PR reviewer see it.
4. Commit the fixes. Re-run only the reviewers that returned FIX, on the new range. Stop when all return PASS or the round limit is reached.
5. Append a summary to your context for the PR:

```
review_rounds: N
panel: code=PASS security=PASS docs=PASS tests=FIX→PASS senior=PASS
fixed: <count> (S1 <n>, S2 <n>, S3 <n>)
disputed: <none | one line each>
```

Rules: reviewers never edit; you never skip a listed reviewer; never lower a reviewer's severity in the summary.
