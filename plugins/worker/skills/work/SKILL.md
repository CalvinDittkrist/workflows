---
name: work
description: Pipeline driver for a claimed issue. Implement, run the reviewer panel, open the PR in a fresh context, wait for CI and bot reviews, address comments, and in yolo mode merge and clean up.
disable-model-invocation: true
allowed-tools: Bash(${CLAUDE_PLUGIN_ROOT}/scripts/facts.sh)
---
Session:
!`${CLAUDE_PLUGIN_ROOT}/scripts/facts.sh`

Run these stages in order. Each stage ends with a one-line status to the user.

When the session facts carry a `resume_stage:`, an earlier context of this worker handed the pipeline over at that stage: start there, not at stage 1. Its note is above, injected by the SessionStart hook — unless the handover it belongs to is older than this session, in which case the stage is named without it and you have the branch alone. Either way read `git log` and the diffstat of the range first: the stages before that one are done, their work is in those commits and never in your context, and a stage re-run at a boundary is cheaper than guessing.

1. **Understand.** The issue is in your context from the SessionStart hook. Read the repository instructions (AGENTS.md, docs/architecture.md, relevant ADRs). If the issue is ambiguous in a way that changes the work materially, ask the user now, once. Otherwise state your plan in three lines and continue.
2. **Implement.** Smallest complete change. For bugs, reproduce end to end first. Update docs and ADRs the change makes stale. Commit in conventional commits, no co-author, then run `"${CLAUDE_PLUGIN_ROOT}/scripts/gate.sh" run`, which runs `make check` and records the result for that commit, which is the one the reviewers read; never run the gate by hand. Fix failures and flakiness you encounter, commit, and run it again until it passes. Then the **first checkpoint**: run `"${CLAUDE_PLUGIN_ROOT}/scripts/checkpoint.sh"`, and on `handoff: yes` hand the review stage over to a fresh context (see below); on `no` or `unavailable` continue here.
3. **Review.** Invoke `/worker:review`. Loop until it reports PASS or the round limit; fix S1 and S2 findings, judge S3. Commit the fixes. The stage ends by recording the panel summary for the next stage; nothing is passed by hand.
4. **Pull request.** Invoke `/worker:pr` without an argument. It opens the PR from a fresh context, reads the recorded panel summary into the body, opens a draft when the panel did not pass, and returns the URL. Then the **second checkpoint**: run `"${CLAUDE_PLUGIN_ROOT}/scripts/checkpoint.sh"` again, and on `handoff: yes` hand the CI stage over the same way.
5. **CI and reviews.** Invoke `/worker:ci`. When it reports `review-comments`, invoke `/worker:address-reviews`, then `/worker:ci` again. When it reports `checks-failed`, read the failed logs, fix, push, and run `/worker:ci` again. Three repair rounds maximum; then stop and report.
6. **Finish.**
   - manual mode: report `ready: <pr-url>` plus the recorded panel summary, read with `"${CLAUDE_PLUGIN_ROOT}/scripts/panel.sh" print`, not from memory. The orchestrator merges with `/orchestrator:merge <pr>`. Do not merge.
   - yolo mode: run `"${CLAUDE_PLUGIN_ROOT}/scripts/finish.sh"`; it merges, notifies and removes this worktree. It refuses unless the recorded panel says ready and the pull request is no draft, which is how the stages before it say the panel did not pass: report that and stop. Never lift a draft yourself.

**Handing over at a checkpoint.** Commit everything first: the note describes the branch, and the script refuses a working tree that carries changes the next context would not see. Then pipe the note into `"${CLAUDE_PLUGIN_ROOT}/scripts/handoff.sh" <stage>` with a quoted heredoc (`<<'NOTE'`, never an unquoted one: the note quotes findings, paths and commands, and the shell would expand `$x` and backticks in it), `<stage>` being the stage you would have run next — `review` at the first checkpoint, `ci` at the second:

```
## decisions
what you decided and why, one line each
## rejected
what you tried or considered and did not do, with the reason
## verified
what you ran and what it said
## open
what is unfinished, unsure or a known limit
```

The script appends the base, the commits and the diffstat itself, so the note repeats none of them; at the second checkpoint it leaves out the pull request and the review summary too, which the next context reads from GitHub and from `panel.sh print`. A note with a section missing is refused with the section named: write it and hand over again.

When the script prints `handoff: started for pane <id>`, say one line to the user and end your turn without another tool call. The detached process waits until you are idle, clears the session and sends `/worker:work` back to the pane, where a fresh context picks the pipeline up at that stage. Never send `/clear` yourself, and never keep working after handing over.

If you get blocked at any stage, say what is blocked and what you need in two lines, then stop.
