---
name: work
description: Pipeline driver for a claimed issue. Implement, run the reviewer panel, open the PR in a fresh context, wait for CI and bot reviews, address comments, and in yolo mode merge and clean up.
disable-model-invocation: true
---
Mode: !`echo "${WF_MODE:-manual}"` · Issue: !`echo "#${WF_ISSUE:-$(git rev-parse --abbrev-ref HEAD | sed -nE 's#^[a-z]+/([0-9]+)-.*#\1#p')}"` · Base: !`echo "${WF_BASE_BRANCH:-$(git symbolic-ref -q --short refs/remotes/origin/HEAD 2>/dev/null | sed 's#^origin/##')}"`

Run these stages in order. Each stage ends with a one-line status to the user.

1. **Understand.** The issue is in your context from the SessionStart hook. Read the repository instructions (CLAUDE.md, docs/architecture.md, relevant ADRs). If the issue is ambiguous in a way that changes the work materially, ask the user now, once. Otherwise state your plan in three lines and continue.
2. **Implement.** Smallest complete change. For bugs, reproduce end to end first. Run the project's checks (tests, lint, type-check, build). Fix failures and flakiness you encounter. Update docs and ADRs the change makes stale. Commit in conventional commits, no co-author.
3. **Review.** Invoke `/worker:review`. Loop until it reports PASS or the round limit; fix S1 and S2 findings, judge S3. Commit the fixes.
4. **Pull request.** Invoke `/worker:pr`. It opens the PR from a fresh context and returns the URL.
5. **CI and reviews.** Invoke `/worker:ci`. When it reports `review-comments`, invoke `/worker:address-reviews`, then `/worker:ci` again. When it reports `checks-failed`, read the failed logs, fix, push, and run `/worker:ci` again. Three repair rounds maximum; then stop and report.
6. **Finish.**
   - manual mode: report `ready: <pr-url>` plus the review summary. The orchestrator merges with `/orchestrator:merge <pr>`. Do not merge.
   - yolo mode: run `"${CLAUDE_PLUGIN_ROOT}/scripts/finish.sh"`; it merges, notifies and removes this worktree.

If you get blocked at any stage, say what is blocked and what you need in two lines, then stop.
