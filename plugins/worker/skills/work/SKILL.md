---
name: work
description: Pipeline driver for a claimed issue. Implement, run the reviewer panel, open the PR in a fresh context, wait for CI and bot reviews, address comments, and in yolo mode merge and clean up.
disable-model-invocation: true
allowed-tools: Bash(${CLAUDE_PLUGIN_ROOT}/scripts/facts.sh)
---
Session:
!`${CLAUDE_PLUGIN_ROOT}/scripts/facts.sh`

Run these stages in order. Each stage ends with a one-line status to the user.

1. **Understand.** The issue is in your context from the SessionStart hook. Read the repository instructions (AGENTS.md, docs/architecture.md, relevant ADRs). If the issue is ambiguous in a way that changes the work materially, ask the user now, once. Otherwise state your plan in three lines and continue.
2. **Implement.** Smallest complete change. For bugs, reproduce end to end first. Run the gate until it passes with `"${CLAUDE_PLUGIN_ROOT}/scripts/gate.sh" run`, which runs `make check` and records the result for the reviewers; never run the gate by hand. Fix failures and flakiness you encounter. Update docs and ADRs the change makes stale. Commit in conventional commits, no co-author.
3. **Review.** Invoke `/worker:review`. Loop until it reports PASS or the round limit; fix S1 and S2 findings, judge S3. Commit the fixes. The stage ends by recording the panel summary for the next stage; nothing is passed by hand.
4. **Pull request.** Invoke `/worker:pr` without an argument. It opens the PR from a fresh context, reads the recorded panel summary into the body, opens a draft when the panel did not pass, and returns the URL.
5. **CI and reviews.** Invoke `/worker:ci`. When it reports `review-comments`, invoke `/worker:address-reviews`, then `/worker:ci` again. When it reports `checks-failed`, read the failed logs, fix, push, and run `/worker:ci` again. Three repair rounds maximum; then stop and report.
6. **Finish.**
   - manual mode: report `ready: <pr-url>` plus the recorded panel summary, read with `"${CLAUDE_PLUGIN_ROOT}/scripts/panel.sh" print`, not from memory. The orchestrator merges with `/orchestrator:merge <pr>`. Do not merge.
   - yolo mode: run `"${CLAUDE_PLUGIN_ROOT}/scripts/finish.sh"`; it merges, notifies and removes this worktree. It refuses unless the recorded panel says ready and the pull request is no draft, which is how the stages before it say the panel did not pass: report that and stop. Never lift a draft yourself.

If you get blocked at any stage, say what is blocked and what you need in two lines, then stop.
