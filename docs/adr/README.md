# Architecture Decision Records

One file per decision, numbered, never edited after acceptance (supersede instead). Format: [MADR](https://adr.github.io/madr/), trimmed. Create one with `/repo-standards:adr <title>`.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-distribute-as-claude-code-plugin-marketplace.md) | Distribute as a Claude Code plugin marketplace | accepted |
| [0002](0002-scripts-do-agents-decide.md) | Scripts do, agents decide | accepted |
| [0003](0003-herdr-worktree-per-issue.md) | One Herdr worktree workspace per issue, branch name as contract | accepted |
| [0004](0004-reviewers-as-fresh-read-only-subagents.md) | Reviewers are fresh-context, read-only subagents | accepted |
| [0005](0005-sandboxing-strategy.md) | Sandboxing strategy: layered, Docker Sandboxes opt-in | accepted |
| [0006](0006-planner-session-writes-issues-not-code.md) | Planning is its own session that writes issues, not code | accepted |
| [0007](0007-agents-md-is-the-instruction-source.md) | AGENTS.md is the instruction source and CLAUDE.md imports it | accepted |
| [0008](0008-make-check-is-the-single-gate.md) | make check is the single gate and check the single required status check | accepted; amended by [0041](0041-a-change-class-decides-the-gate-and-the-reviewers-before-the-pull-request.md): the factory's gate before the pull request is its change class's |
| [0009](0009-profile-derived-from-github-with-two-branch-models.md) | The profile is derived from GitHub, with exactly two branch models | accepted |
| [0010](0010-standardisation-audits-read-only-and-backs-up-before-deleting.md) | Standardisation audits read-only, deletes through a pull request and backs up with a protected tag | accepted |
| [0011](0011-github-workspace-configured-by-an-idempotent-script.md) | The GitHub workspace is configured by an idempotent script with rulesets and no bypass | accepted |
| [0012](0012-releases-are-manual-and-close-a-milestone.md) | Releases are manual and close a milestone | accepted |
| [0013](0013-promotions-merge-with-a-merge-commit-and-releases-tag-it.md) | Promotions merge with a merge commit, and the release tags it | accepted |
| [0014](0014-claims-require-ready-for-agent.md) | Claiming requires ready-for-agent, with --force as the only exception | accepted |
| [0015](0015-a-spec-with-tickets-is-closed-by-an-acceptance.md) | A spec with tickets is closed by an acceptance | accepted |
| [0016](0016-approval-is-per-category-and-scripts-own-what-they-apply.md) | Approval is per category, and scripts own what they apply | accepted; partly superseded by [0035](0035-every-category-the-apply-phase-scaffolds-is-answerable.md) |
| [0017](0017-worker-subagents-run-in-the-foreground.md) | Worker sessions run subagents in the foreground, and the agent never waits by polling | accepted |
| [0018](0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md) | A worker stage hands a fact to the next one through the worktree's git directory | accepted |
| [0019](0019-the-gate-runs-once-per-review-round.md) | The gate runs once per review, and its result is a fact in the brief | accepted; amended 2026-09-22: once per review, not per round, and CI gates the repair pushes |
| [0020](0020-the-pane-measures-the-context-and-the-worktree-carries-the-value.md) | The pane's status line measures a worker's context, and the worktree carries the value | accepted |
| [0021](0021-routing-is-decided-in-the-planner-and-never-stands-alone.md) | Routing is decided in the planner, and the routing label never stands alone | accepted |
| [0022](0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md) | The factory is a second driver over the worker pipeline | superseded by [0040](0040-the-factory-owns-the-delivery-lifecycle-in-go.md) |
| [0023](0023-github-is-the-only-control-surface-of-the-factory.md) | GitHub is the only control surface of the factory | accepted |
| [0024](0024-a-claim-is-the-creation-of-the-branch-through-the-api.md) | A claim is the creation of the branch through the GitHub API | accepted |
| [0025](0025-one-queue-one-worker-work-in-progress-first.md) | One queue, one worker, and work in progress before new work | accepted |
| [0026](0026-the-factory-never-deletes-work-on-its-own.md) | The factory never deletes work on its own | accepted |
| [0027](0027-the-factorys-isolation-boundary-is-the-host.md) | The factory's isolation boundary is the host | accepted |
| [0028](0028-the-quota-check-is-a-courtesy-not-a-guard.md) | The quota check is a courtesy, not a guard | accepted |
| [0029](0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md) | A worker resets its context by a handoff at a checkpoint, not by compaction | accepted; the checkpoint's place is superseded by [0032](0032-the-stage-measures-the-context-on-entry-and-a-handoff-grants-one-skip.md) |
| [0030](0030-agents-verify-claude-code-facts-against-the-live-documentation.md) | Agents verify Claude Code facts against the live documentation, and the worker reads it through a pinned script | accepted |
| [0031](0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md) | The workflow pins the size at which a worker session compacts | accepted; the window of 200 000 and the trigger of 160 000 are superseded by [0034](0034-the-compact-trigger-is-raised-through-the-window.md) |
| [0032](0032-the-stage-measures-the-context-on-entry-and-a-handoff-grants-one-skip.md) | The stage measures the context on entry, and the context a handoff started does one unit of work before the next | accepted |
| [0033](0033-the-dashboard-is-built-into-the-factory-binary.md) | The dashboard is built into the factory binary | accepted |
| [0034](0034-the-compact-trigger-is-raised-through-the-window.md) | The compact trigger is 200 000, and the window is what raises it | accepted |
| [0035](0035-every-category-the-apply-phase-scaffolds-is-answerable.md) | Every category the apply phase scaffolds is answerable | accepted |
| [0036](0036-the-factory-updates-the-worker-plugin-and-nothing-else.md) | The factory updates the worker plugin and nothing else | superseded by [0042](0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md) |
| [0037](0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md) | The quota check waits below 12 % of the worker's scope | accepted; amended 2026-09-25: the check renews an expired credential |
| [0038](0038-the-local-workflow-and-the-factory-are-peers.md) | The local workflow and the factory are peers | accepted |
| [0039](0039-every-session-reports-through-a-structured-result.md) | Every session reports through a structured result and never through prose | accepted |
| [0040](0040-the-factory-owns-the-delivery-lifecycle-in-go.md) | The factory owns the delivery lifecycle in Go and starts one fresh session per stage | accepted |
| [0041](0041-a-change-class-decides-the-gate-and-the-reviewers-before-the-pull-request.md) | A change class decides the gate and the reviewers before the pull request, and CI remains the full gate | accepted |
| [0042](0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md) | The factory carries its own prompts and updates no plugin | accepted |
| [0043](0043-the-migration-runs-from-the-last-stage-to-the-first.md) | The migration runs from the last stage to the first, through a temporary stop-after knob | accepted |
| [0044](0044-the-quota-check-reads-the-scope-of-every-model-a-run-spends.md) | The quota check reads the scope of every model a run spends | accepted |
| [0045](0045-a-test-hunt-runs-on-a-branch-without-an-issue.md) | A test hunt runs on a branch without an issue | accepted |
| [0046](0046-a-test-is-removed-at-high-confidence-without-approval-before-the-pull-request.md) | A test is removed at high confidence without a person's approval before the pull request | accepted; amended by [0047](0047-a-test-hunt-reads-its-shares-whole-and-hunts-while-it-finds-something.md): the worker checks a `medium` candidate instead of keeping it unread |
| [0047](0047-a-test-hunt-reads-its-shares-whole-and-hunts-while-it-finds-something.md) | A test hunt reads its shares whole and hunts while it finds something new | accepted |
