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
2. **Implement.** Smallest complete change. For bugs, reproduce end to end first. Update docs and ADRs the change makes stale. While you work, verify with the single test or linter for the files you touched, never with `make check` or another make target: the full run is the gate's, and it runs once here. Commit in conventional commits, no co-author, then run `"${CLAUDE_PLUGIN_ROOT}/scripts/gate.sh" run` with the Bash tool timeout set to 600000 ms. It runs `make check` detached from the call and records the result for that commit, which is the one the reviewers read. It returns within about nine minutes: `gate_recorded:` is the result, and `gate_running:` (exit 3) means the gate is still running, so run `"${CLAUDE_PLUGIN_ROOT}/scripts/gate.sh" wait` the same way until it prints `gate_recorded:`. Never start the gate a second time to see how it is doing. Fix failures and flakiness you encounter, commit, and run it again until it passes; that is the one place in the pipeline where a fix commit is followed by a gate.
3. **Review.** Invoke `/worker:review`. It measures this context as it loads and may hand the stage over before any reviewer runs, and again after every round it records (see below). Otherwise loop until it reports PASS or the round limit; fix S1 and S2 findings, judge S3. Commit the fixes. Every round is recorded as it ends, and the stage ends by recording the panel summary derived from those rounds; nothing is passed by hand.
4. **Pull request.** Invoke `/worker:pr` without an argument. It opens the PR from a fresh context, reads the recorded panel summary into the body, and returns the URL.
5. **CI and reviews.** Invoke `/worker:ci`, which measures this context on entry the same way. When it reports `review-comments`, invoke `/worker:address-reviews`, then `/worker:ci` again. When it reports `checks-failed`, read the failed logs, fix, push, and run `/worker:ci` again. When it reports `conflicts`, merge the base into the branch as its `help:` line says, resolve, commit, push, and run `/worker:ci` again; CI runs the gate. Before every repair round `/worker:ci` has you count it in the repair record, whose script holds the limit across a handover; when it refuses a round, stop and report.
6. **Finish.**
   - manual mode: report `ready: <pr-url>` plus the recorded panel summary, read with `"${CLAUDE_PLUGIN_ROOT}/scripts/panel.sh" print`, not from memory. The orchestrator merges with `/orchestrator:merge <pr>`. Do not merge.
   - yolo mode: run `"${CLAUDE_PLUGIN_ROOT}/scripts/finish.sh"`; it merges, notifies and removes this worktree. It refuses unless the recorded panel says ready: report that and stop.

**Handing over at a checkpoint.** The review stage and the CI stage measure this context as they load, the review stage again with every round it records, and on `handoff: yes` the checkpoint prints the procedure with its answer: commit first, write the note's four sections, pipe them into `handoff.sh` with a quoted heredoc, and end your turn. Follow that text, not a memory of it. The stage the checkpoint names is the stage the fresh context resumes at, and it does the work this context did not.

When the script prints `handoff: started for pane <id>`, say one line to the user and end your turn without another tool call. The detached process waits until you are idle, clears the session and sends `/worker:work` back to the pane, where a fresh context picks the pipeline up at that stage. Never send `/clear` yourself, and never keep working after handing over.

If you get blocked at any stage, say what is blocked and what you need in two lines, then stop.
