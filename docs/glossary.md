# Glossary

Terms the code, the issues and the docs use, one row each.

| Term | Meaning |
| --- | --- |
| standard | The written baseline a repository is checked against: [repo-standard.md](repo-standard.md). |
| writing rules | The fixed rule set for prose in documents, prompts and comments ([repo-standard.md](repo-standard.md#writing-rules)). The standard check counts the mechanical ones; the docs reviewer judges the rest. |
| profile | Visibility plus branch model (`main` alone, or `dev` plus `main`), derived from GitHub, never configured. |
| gate | The command that must pass before a pull request: the gate command of the change class that applies ([ADR 0041](adr/0041-a-change-class-decides-the-gate-and-the-reviewers-before-the-pull-request.md)), `make check` for `full`. It runs in the worktree or on CI. |
| auditor | A read-only subagent that judges one area of a repository during standardisation and returns findings. |
| facts | The compact `key: value` block `facts.sh` prints about a repository; every auditor gets it instead of exploring. |
| finding | One proposed action of an auditor, one line: `finding: <category> \| <target> \| <action> \| <reason> \| <confidence>`. Actions are delete, replace, create, issue and configure ([ADR 0016](adr/0016-approval-is-per-category-and-scripts-own-what-they-apply.md)). |
| cleanup pull request | The one pull request from `chore/standardize` that carries a standardisation run's deletions and new baseline files; its description lists how to restore each removed path. |
| catalogue issue | The issue, labelled `skill-candidate`, that lists the skills standardisation removed and how to restore each from the `pre-standard` tag. |
| drift | A difference between a repository's GitHub workspace and the standard; `workspace.sh` prints one `diff:` line per difference. |
| snapshot | The JSON file `workspace.sh --apply` writes before its first change: the previous value of everything it changes. |
| conversation language | The language the planner talks to the user in, set with `WF_PLANNER_LANGUAGE`; issues, comments and glossary terms stay English. |
| promotion | The pull request from `dev` to `main` that carries a release in the two-level branch model. |
| acceptance | The check of a whole spec against the code on the base branch after its tickets are closed; it ends with gap tickets or the spec closed ([ADR 0015](adr/0015-a-spec-with-tickets-is-closed-by-an-acceptance.md)). |
| foreground subagent | A subagent whose report is the result of the Agent call, because background tasks are disabled; worker sessions run this way ([ADR 0017](adr/0017-worker-subagents-run-in-the-foreground.md)). |
| context report | `scripts/context-report.py`, the maintainer's diagnostic over finished worker sessions: context at stage boundaries, peak, tool mix, sleep calls. Never an input to the pipeline. |
| spec checker | The read-only subagent that judges each checkable statement of a spec during an acceptance. |
| item | One checkable statement of a spec with its verdict: `item: <section> \| <statement> \| <verdict> \| <evidence> \| <confidence>`. |
| accepted deviation | A difference between spec and code the maintainer keeps. A writer records it on the spec in a comment that opens with `> Accepted deviation (spec acceptance).` An acceptance does not report it again. |
| gap ticket | A `ready-for-agent` sub-issue an acceptance creates for an item that is not met. |
| routing label | The label `factory`, which hands an issue to the factory host. A local claim refuses it without `--force`; the planner sets it per ticket ([ADR 0021](adr/0021-routing-is-decided-in-the-planner-and-never-stands-alone.md)). |
| label vocabulary | The fixed set of GitHub labels the workflow uses. Each plugin that creates labels defines it; a test in `tests/test_plugins.py` keeps the copies identical. |
| round record | What one review round leaves: each reviewer's verdict, the fixes by severity, the `disputed:` lines and the commit, recorded by `panel.sh round` ([ADR 0018](adr/0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md)). |
| panel summary | The block the pull request stage reads (`review_rounds`, `panel`, `fixed`, `disputed`), derived by `panel.sh record` from the round records. It is a draft when a reviewer's last verdict is not PASS. |
| compact trigger | The context size a worker session compacts at, 250 000 tokens: the pinned window times the pinned percentage ([ADR 0031](adr/0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md), [ADR 0034](adr/0034-the-compact-trigger-is-raised-through-the-window.md)). |
| context value | A worker session's context size, written by its pane's status line to `<worktree git dir>/worker/context` and read by `checkpoint.sh` ([ADR 0020](adr/0020-the-pane-measures-the-context-and-the-worktree-carries-the-value.md)). |
| checkpoint | A point where the pipeline measures the context and may hand over: entering review, entering `/worker:ci`, and the end of each review round ([ADR 0032](adr/0032-the-stage-measures-the-context-on-entry-and-a-handoff-grants-one-skip.md)). |
| handoff | Continuing a worker's issue in a fresh context in the same pane once the context passes `WF_HANDOFF_TOKENS`; `/worker:work` resumes there ([ADR 0029](adr/0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md)). |
| handoff note | The page a handing-over context writes for the next: `## decisions`, `## rejected`, `## verified`, `## open`, plus base, commits and diffstat. Never committed; injected into exactly one fresh session. |
| resume stage | The stage a handed-over pipeline continues at, `review` or `ci`, named by the note and printed by `facts.sh` as `resume_stage:`. |
| implement session | The first session of a factory run, the inline agent `worker` on the factory's own prompt ([ADR 0042](adr/0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md)). It commits the change, pushes nothing and reports its commits or `blocked`. |
| author session | The read-only session of the factory's pr stage. From the diff, the commits and the issue it reports the pull request's title and body. |
| review round | One round of the factory's review stage: the due reviewers run in parallel and report verdicts and findings; on any `fix`, one fix session gets every finding by its id. |
| fix session | A factory session that repairs one thing, such as a merge conflict, failed checks or review findings, then commits and pushes. |
| ci knobs | The factory's ci settings `repair_rounds`, `bot_reviewers`, `review_wait` and `checks_grace`, per host and per repository. They replace `WF_CI_REPAIR_ROUNDS`, `WF_PR_BOT_REVIEWERS` and `WF_PR_REVIEW_WAIT`. |
| gate record | The result of one gate run, written by the worker's `gate.sh`: commit, dirty flag, exit status, time and output tail. It answers for its own commit only ([ADR 0019](adr/0019-the-gate-runs-once-per-review-round.md)). |
| repair record | The count of CI repair rounds, such as a `/worker:address-reviews` round, of one pull request, kept by `repair.sh`, refused past `WF_CI_REPAIR_ROUNDS`. `WF_REVIEW_MANDATE` restarts it once per review ([ADR 0018](adr/0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md), [ADR 0032](adr/0032-the-stage-measures-the-context-on-entry-and-a-handoff-grants-one-skip.md)). |
| factory | The Go service in `factory/` that works routed issues unattended on a host of its own ([ADR 0038](adr/0038-the-local-workflow-and-the-factory-are-peers.md), [ADR 0040](adr/0040-the-factory-owns-the-delivery-lifecycle-in-go.md)). Not a plugin. |
| factory host | The dedicated machine the factory runs on. It shares nothing with a developer's machine but GitHub and is the isolation boundary ([ADR 0027](adr/0027-the-factorys-isolation-boundary-is-the-host.md)). |
| routed issue | An open issue with `ready-for-agent` and the routing label, no assignee and no open blocker. |
| queue | The routed issues of all connected repositories in one line, derived from GitHub on every poll, never stored; held work first ([ADR 0025](adr/0025-one-queue-one-worker-work-in-progress-first.md)). |
| factory run | The factory's answer to one signal, with its record and event log, across its stages. Kinds: first run, resumed run, follow-up run. |
| stage | One step of the factory's pipeline in Go: implement, gate, review, pr, ci, address-reviews ([ADR 0040](adr/0040-the-factory-owns-the-delivery-lifecycle-in-go.md)). Not a stage of the local worker. |
| session | One print-mode `claude` call inside a stage, with a fresh context, the factory's prompt, a stage timeout and a structured result ([ADR 0039](adr/0039-every-session-reports-through-a-structured-result.md)). |
| change class | An ordered rule of a connected repository: name, path patterns, gate command, optional reviewers. `full` is built in ([ADR 0041](adr/0041-a-change-class-decides-the-gate-and-the-reviewers-before-the-pull-request.md)). |
| review finding | One structured finding of a reviewer session: severity, path, line, claim, why, fix. `finding` stays the auditor's line. |
| outcome | How a factory run ended: `ready`, `blocked`, `failed`, `timeout`, `lost`, `interrupted`, `cancelled`, `quota`. |
| remote claim | Creating the issue's branch through the GitHub API, which exactly one claimer wins ([ADR 0024](adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md)). |
| local claim | The orchestrator's claim of an issue into a Herdr worktree on a developer's machine (`/orchestrator:claim`). It refuses an issue the factory owns. |
| release signal | Removing the assignee from an issue the factory holds, which queues a resumed run. |
| changes-requested signal | A new review asking for changes on the pull request of a held issue, by a writer, after the last run ended. It queues a follow-up run; only the reviewer's latest review counts. |
| follow-up run | The factory run that answers such a review: in the worktree of the claim, starting at address-reviews, with a fresh count of repair rounds. |
| address-reviews session | The factory session that fixes or declines each point writers or configured bots still raise, pushes, and reports replies. The factory posts them and resolves the threads. |
| quota check | The factory's call of the host's quota-axi before every run and after a session error ([ADR 0028](adr/0028-the-quota-check-is-a-courtesy-not-a-guard.md), [ADR 0037](adr/0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md), [ADR 0044](adr/0044-the-quota-check-reads-the-scope-of-every-model-a-run-spends.md)). |
| connected repository | A repository named in the factory's configuration, as `owner/name`. |
| drain | The factory's answer to `SIGHUP`: it claims and resumes nothing new, lets the run in `.now` end with its own outcome, delivers what that run owes and exits with the drain code, 75 ([ADR 0050](adr/0050-the-host-installs-every-factory-release-and-the-factory-drains-on-signal.md)). |
| update tick | One run of the factory binary's update mode by the host's timer, as root; it reads the release, the running state and the file, does one action and exits. |
| auto-update | The configuration field `auto_update` that lets the host install factory releases; false by default, read by the update tick and reported by the factory. |
| block list | The updater's root-owned list of versions that failed after an install and are never installed again; a line is lifted by deleting it. |
| dashboard | The page the factory serves at `/`, built into the binary from `factory/ui`. It reads the four endpoints and writes nothing ([ADR 0033](adr/0033-the-dashboard-is-built-into-the-factory-binary.md)). |
| test hunt | One run of `/orchestrator:hunt-tests`: a worker on a branch of its own that removes tests that prove nothing ([ADR 0045](adr/0045-a-test-hunt-runs-on-a-branch-without-an-issue.md)). |
| hunter | The read-only subagent `test-hunter` of a test hunt, with no shell, that reads one share of at most 1500 lines and replies with candidates. |
| candidate | One hunter line: `candidate: <path> \| <test> \| <category> \| <reason> \| <confidence>` ([ADR 0046](adr/0046-a-test-is-removed-at-high-confidence-without-approval-before-the-pull-request.md), [ADR 0047](adr/0047-a-test-hunt-reads-its-shares-whole-and-hunts-while-it-finds-something.md)). |
| hunt record | The rounds, removals and kept candidates of a test hunt, kept by `hunt.sh` in the worktree's git directory; it stands in for the issue. |

`checkpoint`, `handoff`, `handoff note`, `resume stage`, `round record`, `panel summary`, `gate record` and `repair record` are terms of the local pipeline only. The factory keeps the same facts in its run record.
