---
name: review
description: Run the independent reviewer panel (code, security, docs, tests, senior) on the branch diff in fresh contexts and fix the findings until the panel passes.
argument-hint: [reviewers=code,security,docs,tests,senior]
allowed-tools: Bash(${CLAUDE_PLUGIN_ROOT}/scripts/diff-context.sh), Bash(${CLAUDE_PLUGIN_ROOT}/scripts/facts.sh), Bash(${CLAUDE_PLUGIN_ROOT}/scripts/gate.sh*)
---
Diff context:
!`${CLAUDE_PLUGIN_ROOT}/scripts/diff-context.sh`
!`${CLAUDE_PLUGIN_ROOT}/scripts/facts.sh`
!`${CLAUDE_PLUGIN_ROOT}/scripts/gate.sh print`

Use `reviewers` and `max_rounds` from above unless the argument overrides the reviewer list.

Round procedure:
1. If there are uncommitted changes, commit them first; reviewers read committed history.
2. Run the gate once for this round: `"${CLAUDE_PLUGIN_ROOT}/scripts/gate.sh" run`. No reviewer runs it, so this is the only run, and it has to be after the commit. It fails → fix, commit and run it again; launch no reviewer on a failed gate. It passes → read the block with `"${CLAUDE_PLUGIN_ROOT}/scripts/gate.sh" print` (the injection above is only current for the first round).
3. Launch every listed reviewer **in parallel, in one message** with the Agent tool. Map names to agents: code → `worker:code-reviewer`, security → `worker:security-reviewer`, docs → `worker:docs-reviewer`, tests → `worker:test-reviewer`, senior → `worker:senior-reviewer`. Give each the same brief: the diff range, the base ref, the issue number and title, the `gate_` block verbatim from `gate.sh print`, and this instruction: "Review range <range>. Read-only. Use the report format from your instructions. The gate result below is a fact of this round; do not run the gate yourself."
4. Collect the reports. With `subagents: foreground` they arrive as the results of the Agent calls, in the same turn as the launch; with `subagents: background` end your turn and continue when they arrive. Never wait with a `sleep` or a polling loop. Fix every S1 and S2 in the main context. For an S3, fix it if it is cheap, otherwise leave it. If you disagree with an S1 or S2, do not drop it silently: keep it in the final summary as `disputed:` with your reason so the user and the PR reviewer see it.
5. Commit the fixes. Run the gate again (step 2), then re-run only the reviewers that returned FIX, on the new range and with the new gate block. Stop when all return PASS or the round limit is reached.
6. Write the summary, in exactly this form:

```
review_rounds: N
panel: code=PASS security=PASS docs=PASS tests=FIX→PASS senior=PASS
fixed: <count> (S1 <n>, S2 <n>, S3 <n>)
disputed: <none | one line each>
```

   A reviewer that was re-reviewed carries one verdict per round, oldest first (`FIX→FIX→PASS`); a reviewer that ended on FIX at the round limit stays `FIX`.

7. Hand the summary over to the pull request stage, which has a fresh context and cannot see yours: pipe the block into `"${CLAUDE_PLUGIN_ROOT}/scripts/panel.sh" record` with a quoted heredoc (`<<'PANEL'`, never an unquoted one: the block quotes reviewer text, and the shell would expand `$x` and backticks in it). The script stores it in this worktree, derives from the `panel:` line whether the pull request opens as a draft, and refuses a block it cannot parse with an `error:` line naming the expected form — correct the block and record again. Then print the same summary to the user.

Rules: reviewers never edit and never run the gate; you never skip a listed reviewer; never lower a reviewer's severity in the summary; you record the summary as written, including a panel that ended on FIX and every `disputed:` line.
