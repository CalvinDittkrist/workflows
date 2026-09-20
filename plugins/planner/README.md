# planner

Planning session for one topic. The orchestrator starts it with `/orchestrator:plan <idea | #issue>`: worktree `plan/<slug>`, Herdr workspace, `claude --agent planner`, first turn `/planner:plan`. The planner writes GitHub issues, never code, and the plan branch is never pushed or committed to.

| Skill | Script | Effect |
| --- | --- | --- |
| `/planner:plan` | `facts.sh`, `labels.sh` | session facts, label vocabulary, the routes; recommends one and stops |
| `/planner:grill [topic]` | | question rounds along the decision tree until nothing is open; collects glossary terms and ADR candidates |
| `/planner:spec` | `issue.sh create --label spec` | one spec issue from the conversation, no new questions |
| `/planner:tickets [spec]` | `issue.sh milestones`, `issue.sh milestone`, `issue.sh create --parent --milestone`, `issue.sh block` | asks once for a `vX.Y.Z` milestone (existing, new with the goal as description, or none); vertical-slice issues labelled `ready-for-agent`, sub-issues of the spec, native blocking edges |
| `/planner:triage [issue]` | `triage-list.sh`, `issue.sh comment|label|close` | three buckets; per issue verify, grill, agent brief, labels; `wontfix` closes with the reason |
| `/planner:research <question>` | | background subagent, primary sources, answer lands in the issue |
| `/planner:prototype <question>` | `capture-prototype.sh` | throwaway code, moved to `prototype/<plan>-<name>` and linked |
| `/planner:finish [--force]` | `finish.sh`, `cleanup-self.sh` | refuses while uncommitted or unpushed work exists, then removes worktree, workspace and branch |

Every skill has `disable-model-invocation: true`: only the user invokes them, and their descriptions cost no context in any session, including this one. Stage skills that need the interview mechanics link to the grill skill's file instead of invoking it.

Hook: `SessionStart` injects the topic (from the branch description `plan.sh` wrote) or the issue text, marked as data. Silent outside `plan/*` worktrees and in subagents.

Labels the plugin owns and creates on demand: `ready-for-agent`, `needs-triage`, `needs-info`, `ready-for-human`, `wontfix`, `spec`, `bug`, `enhancement`. Sub-issues and blocking edges use GitHub's native APIs and fall back to body text where a repository lacks them.

The agent has eight tools (Bash, Read, Write, Edit, Grep, Glob, Agent, WebFetch) and no Skill tool: you type the stage skills. Requires `gh`, `jq`, `git`. `WF_PLANNER_PERMISSION_MODE` (default `auto`), `WF_PLANNER_LANGUAGE`, `WF_CLAUDE_ARGS` and `WF_PLANNER_CLAUDE_ARGS` apply at start.

`WF_PLANNER_LANGUAGE` (for example `german`) sets the conversation language: the planner talks to you in it from the first turn, because the orchestrator passes it as claude's `language` setting for this session only. What the planner writes for others stays English, whatever the conversation language is: issues, triage comments and briefs, glossary terms, ADR candidates, milestone descriptions and prototype branch names. Any name claude can read works, accents and non-Latin scripts included; a control character or a value longer than a name is refused before the session is created. Unset or empty changes nothing.
