# orchestrator

Coordinator session for one repository. Start it in the main checkout inside a Herdr pane with `claude --agent orchestrator`. It opens with `/orchestrator:board` and runs on Sonnet, because it only calls scripts and relays their output. The agent has only the Bash tool and does not load CLAUDE.md. Scripts require `HERDR_ENV=1`, `gh`, `jq` and `git`; `--sandbox` requires `sbx`.

## Skills
| Skill | Script | Effect |
| --- | --- | --- |
| `/orchestrator:plan <idea words \| #issue> [--base b]` | `plan.sh` | branch `plan/<slug>`, topic or issue in the branch description, worktree, Herdr workspace, start `claude --agent planner … /planner:plan` |
| `/orchestrator:claim <issue> [--sandbox] [--force] [--base b] [--env NAME=VALUE]` | `claim.sh` | validate the issue, branch `<type>/<n>-<slug>`, worktree in `.claude/worktrees/`, Herdr workspace, start `claude --agent worker … /worker:work` |
| `/orchestrator:yolo-claim <issue> [--force] [--env NAME=VALUE]` | `claim.sh --yolo` | same, worker merges itself when green |
| `/orchestrator:hunt-tests [--sandbox] [--base b]` | `hunt.sh` | branch `hunt/tests-<date>` without an issue, worktree and workspace, start `claude --agent worker … /worker:hunt-tests` with the settings of a manual claim ([ADR 0045](../../docs/adr/0045-a-test-hunt-runs-on-a-branch-without-an-issue.md)) |
| `/orchestrator:board` | `board.sh` | table of issue, branch, agent state, PR, checks, review and workspace; then the frontier with milestones, and the specs ready for acceptance |
| `/orchestrator:merge <pr>` | `merge.sh` | refuse unless CLEAN, green, no unresolved threads and no changes requested; remove workspace and worktree, squash-merge, delete branches, fast-forward main; a promotion from `dev` gets a merge commit |
| `/orchestrator:release <vX.Y.Z>` | `release.sh` | refuse while the milestone is missing or open or the tag exists; with `dev` plus `main` wait for the promotion's merge; tag, GitHub release with generated notes, close the milestone |
| `/orchestrator:abandon <issue\|branch> [--force]` | `abandon.sh` | drop the worktree; refuses unpushed or dirty work without `--force` |
| `/orchestrator:herdr` | | loads Herdr's own skill for manual pane control |
| `/orchestrator:gh-axi` | | the `gh-axi` discovery skill, so agents prefer it over raw `gh` |

The frontier is the open `ready-for-agent` issues with no open blocker, no assignee, no worktree and no routing label. A spec is ready for acceptance when it is open, has native sub-issues and none of them is open.

A claim refuses before anything is created, and the error names the labels or branch and the fix. `--force` claims anyway.

- An issue without `ready-for-agent` is refused ([ADR 0014](../../docs/adr/0014-claims-require-ready-for-agent.md)).
- An issue with the routing label `factory` is refused, because the factory host claims it.
- An issue whose `<type>/<issue>-…` branch already exists on origin is refused, since creating that branch is the claim on the remote. `abandon.sh` leaves such a branch on origin.
- A forced claim over a remote branch adopts it and runs code nobody here reviewed. It refuses while a local branch of that name points elsewhere.
- An origin that cannot be read is a warning; that check never stops a claim.

`--env NAME=VALUE`, repeatable, sets one worker knob for the claimed session and no other.

- Only the worker knobs of the [configuration table](../../README.md#configuration) are accepted; the error for any other name lists them.
- jq writes the value into the settings JSON and no shell of the claim reads it. Quote a value with spaces, a quote or a `$`.
- The knob holds across every handover of that pane. A claim of an issue already claimed starts no session and says the values were not applied.

Every started session disables the other workflow plugins through `--settings`, so only its own skills and agents load. A worker session also gets:

- `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`, so its subagents run in the foreground ([ADR 0017](../../docs/adr/0017-worker-subagents-run-in-the-foreground.md)).
- The status line `statusline.sh`, which shows issue, mode and context size and writes the size to `<worktree git dir>/worker/context` ([ADR 0020](../../docs/adr/0020-the-pane-measures-the-context-and-the-worktree-carries-the-value.md)).
- `autoCompactWindow: 312500` and `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=80`, whose product is the compact trigger of 250 000 tokens ([ADR 0031](../../docs/adr/0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md), [ADR 0034](../../docs/adr/0034-the-compact-trigger-is-raised-through-the-window.md)).

Inside `--sandbox` the container has neither the status line script nor Herdr, so the checkpoint reports `unavailable`.

Hook: `SessionStart`, on startup only, prints the gh-axi dashboard of repository, open issues and open PRs through `gh-axi` or `npx -y gh-axi`.

## Configuration
| Variable | Default | Effect |
| --- | --- | --- |
| `WF_BASE_BRANCH` | remote default branch | base of the worktrees; `--base` sets it for one session |
| `WF_CLAUDE_ARGS` | empty | extra flags for every worker and planner start |
| `WF_PLANNER_CLAUDE_ARGS`, `WF_WORKER_CLAUDE_ARGS` | empty | extra flags for one kind of session; a `--settings` here replaces the scripts' object, and they warn |
| `WF_PLANNER_PERMISSION_MODE` | `auto` | the planner's permission mode |
| `WF_WORKER_PERMISSION_MODE` | `auto` | a worker session's permission mode |
| `WF_PLANNER_LANGUAGE` | empty | the planner's conversation language, such as `german`, through claude's `language` setting; issues stay English |
| `WF_MODE`, `WF_ISSUE` | set by the claim | mode (`manual` or `yolo`) and issue of the worker session |

## Develop
`claude --plugin-dir plugins/orchestrator` loads the plugin without installing it; `scripts/dev-orchestrator.sh` also points the sessions it opens at the checkout. `make check` runs the gate.
