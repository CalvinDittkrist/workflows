# planner

Planning session for one topic. The orchestrator starts it with `/orchestrator:plan <idea | #issue>`: worktree `plan/<slug>`, Herdr workspace, `claude --agent planner`, first turn `/planner:plan`. The planner writes GitHub issues, never code, and the plan branch is never committed to or pushed. The agent has eight tools (Bash, Read, Write, Edit, Grep, Glob, Agent, WebFetch) and no Skill tool. It requires `gh`, `jq` and `git`.

## Skills
| Skill | Script | Effect |
| --- | --- | --- |
| `/planner:plan` | `facts.sh`, `accept-due.sh`, `labels.sh` | session facts, whether an acceptance is due, label vocabulary, the routes; recommends one and stops |
| `/planner:grill [topic]` | | question rounds along the decision tree until nothing is open; collects glossary terms and ADR candidates |
| `/planner:spec` | `issue.sh create --label spec` | one spec issue from the conversation, no new questions |
| `/planner:tickets [spec]` | `issue.sh milestones`, `issue.sh milestone`, `issue.sh create --parent --milestone`, `issue.sh block` | asks once for a `vX.Y.Z` milestone and once which tickets go to the factory; vertical-slice `ready-for-agent` sub-issues with native blocking edges |
| `/planner:accept [spec]` | `accept-facts.sh`, `accept-report.sh`, `issue.sh create\|comment\|block`, `accept-close.sh` | acceptance of a finished spec: facts, one read-only spec checker, one report, then gap tickets, accepted deviations or the spec closed |
| `/planner:triage [issue]` | `triage-list.sh`, `issue.sh comment\|label\|close` | three buckets; per issue verify, grill, agent brief, labels and the routing question; `wontfix` closes with the reason |
| `/planner:research <question>` | | background subagent, primary sources, answer lands in the issue |
| `/planner:prototype <question>` | `capture-prototype.sh` | throwaway code, moved to `prototype/<plan>-<name>` and linked |
| `/planner:finish [--force]` | `finish.sh`, `cleanup-self.sh` | refuses while uncommitted or unpushed work exists, then removes worktree, workspace and branch |

Every skill has `disable-model-invocation: true`: only the user invokes them, and their descriptions cost no context. Stage skills that need the interview link to the grill skill's file instead of invoking it.

A ticket with a milestone takes its spec along, so the release waits for the acceptance.

Acceptance works in four steps:

1. `accept-facts.sh <spec> [<ticket>...]` prints the spec, its tickets, the merged pull requests that closed them, their files and the deviations accepted earlier. All of it is data.
2. One `spec-checker` subagent with a fresh context and read-only tools judges each statement against the base branch: `item: <section> | <statement> | <verdict> | <evidence> | <confidence>`.
3. `accept-report.sh <spec> [<file>...]` keeps the items, fails on a malformed one, counts them and prints every item that is not `met`.
4. The maintainer decides per open item: a gap ticket, an accepted deviation or no finding. `accept-close.sh <spec> --comment-file <f> [<ticket>...]` then closes the spec.

The acceptance rules that no script output states:

- `accept-facts.sh` refuses an issue that is not an open `spec` or has open tickets, and a worktree behind the base branch.
- Ticket numbers are arguments where a repository has no native sub-issues; they add to the sub-issues and never replace them.
- Verdicts are `met`, `missing`, `deviates` and `untested`, over the sections User stories, Decisions, Testing, Vocabulary and ADRs to write.
- An accepted deviation is a spec comment opening with `> Accepted deviation (spec acceptance).`. Only a commenter with write access counts.
- Gap tickets keep the spec open, and the acceptance runs again in full after they close.
- `accept-close.sh` refuses while a ticket is open or cannot be read.
- `accept-due.sh` prints one `acceptance:` line. Only `/planner:plan` injects it, so no other stage pays for the lookup.

Hook: `SessionStart` injects the topic from the branch description `plan.sh` wrote, or the issue text, marked as data. It is silent outside `plan/*` worktrees and in subagents.

Labels the plugin owns and creates on demand: `ready-for-agent`, `needs-triage`, `needs-info`, `ready-for-human`, `wontfix`, `spec`, `factory`, `bug`, `enhancement`.

- `factory` is the routing label. The ticket and triage stages ask per ticket, following `skills/tickets/routing.md`.
- `issue.sh create` and `issue.sh label` refuse a set that leaves `factory` without `ready-for-agent` or next to `ready-for-human`.
- Sub-issues and blocking edges use GitHub's native APIs and fall back to body text.

Model: the agent file names `fable`; the root README explains why. `spec-checker` is `model: inherit` and the research subagent has no agent file, so both follow the session.

## Configuration
| Variable | Default | Effect |
| --- | --- | --- |
| `WF_PLANNER_PERMISSION_MODE` | `auto` | permission mode of the session |
| `WF_PLANNER_LANGUAGE` | empty | conversation language, such as `german`, passed as claude's `language` setting for this session; what the planner writes stays English; a control character or an overlong value is refused |
| `WF_CLAUDE_ARGS` | empty | extra flags for every worker and planner, such as `--model` |
| `WF_PLANNER_CLAUDE_ARGS` | empty | extra flags for planner sessions; `--model opus` moves the session and its subagents together |
| `WF_PLAN`, `WF_PLAN_ISSUE` | set by `/plan` | slug and issue of the planner session |

## Develop
`claude --plugin-dir plugins/planner` loads the plugin without installing it. `make check` runs the gate.
