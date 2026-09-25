# Token budget: what loads when

Every context of the pipeline holds only what its job needs.

| Context | Loaded at start | Loaded on demand | Never |
| --- | --- | --- | --- |
| orchestrator (sonnet) | its agent prompt (≈300 tokens), the Bash tool only (≈7k with system prompt, versus ≈17k for the full tool set) | script output per command; the Herdr skill only via `/orchestrator:herdr` | CLAUDE.md (`omitClaudeMd`), code, diffs |
| planner (fable; its `spec-checker` and research subagents inherit) | its agent prompt (≈300 tokens), eight tools (Bash, Read, Write, Edit, Grep, Glob, Agent, WebFetch; ≈10k). CLAUDE.md with the imported AGENTS.md, topic or issue from the hook (same caps as the worker) | one stage skill body per invocation; templates (spec, ticket, brief) only when that stage runs; a research subagent's report | worker and orchestrator skills; other stages' bodies |
| worker (opus) | eight tools (the planner's set with Skill instead of WebFetch; ≈13k), CLAUDE.md with the imported AGENTS.md. Issue context from the hook: body capped at 6 000 chars, last 8 comments at 1 500 chars | skill bodies when invoked; diff context from `diff-context.sh` | reviewer transcripts (only their reports return) |
| worker after a handoff | the same start, plus the handoff note the hook injects once. The note holds four sections written by the previous context, and the base, commits and diffstat the script appended | the same, plus the commit log and the diffstat it reads before resuming. At the CI stage it adds `panel.sh print`. A review resumed mid-panel adds the recorded rounds instead, one short block each, in place of the reviewer reports | the transcript it replaces: every reviewer report, gate output, file read and dead end of the stages before |
| `docs-lookup` (sonnet) | its prompt (≈400 tokens), the question and the script path | the documentation pages it reads through `claude-docs.sh` | CLAUDE.md (`omitClaudeMd`), the worker's conversation, the open web |
| each reviewer (code, docs and test reviewer sonnet; security and senior reviewer inherit) | its prompt (≈350 tokens), CLAUDE.md (docs reviewer omits it), the brief with the round's gate record | files it chooses to read | the worker's conversation |
| pr-author (sonnet) | its prompt, the brief with the panel summary and the gate record | diff, issue | the worker's conversation |
| each auditor (inherit) | its prompt (≈600 tokens), CLAUDE.md, the brief with the facts block (≈1k on a mid-size repository) | files it chooses to read | the other auditors' replies, the main session's conversation |

Practices that keep the budget flat:

- Skills are short and call scripts that print compact `key: value` lines and tables, never raw JSON.
- `!`command`` injection puts facts (mode, issue, range) into the skill at invocation time, instead of the model discovering them with tool calls.
- Each injected command is a plugin script pre-approved in the skill's `allowed-tools`. Without that, the permission check refuses a forked skill's injection.
- Long waits (`pr-wait.sh`, `gate.sh wait`) happen in one blocking script call, not in polling turns.
- Worker sessions start with `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`, so their subagents block and hand the report back as the tool result.
- A background launch returns at once and leaves the agent waiting in sleep turns, each one a full pass over the context ([ADR 0017](adr/0017-worker-subagents-run-in-the-foreground.md)).

File access:

- A main-thread agent's prompt replaces Claude Code's default system prompt entirely. The default guidance to prefer the file tools is gone, so the prompt carries it.
- Without it a worker reads and writes through the shell, and file contents read with `cat`, `sed -n` or `head` fill the context.
- The worker prompt states how to work with files: read with a range, search with the search tools, change with edits, write only new files.
- It also rules out whole-file shell reads and heredoc rewrites, and asks for independent reads in parallel.
- `scripts/context-report.py` measures whether that holds. The planner and reviewer prompts do not carry the guidance yet, and the report does not measure subagent turns.

The gate:

- Reviewers run in parallel, and only the reviewers that returned FIX are re-run.
- None of them runs the gate. It runs once per review in the worker's context, in the work stage and again before the summary ([ADR 0019](adr/0019-the-gate-runs-once-per-review-round.md)).
- Its recorded result is a line of the reviewers' brief.
- `gate.sh run` writes the gate's output to a file and prints it only when the gate failed. The brief quotes the last ten lines.

The context size of a worker session is visible while it runs:

- The status line of its pane prints issue, mode and size against the compact trigger (`78k/250k (31%)`, not the model's window).
- The same render writes that size into the worktree. The worker's `checkpoint.sh` reads it ([ADR 0020](adr/0020-the-pane-measures-the-context-and-the-worktree-carries-the-value.md)).
- `checkpoint.sh` answers `context_tokens`, `threshold` (`WF_HANDOFF_TOKENS`, default 100 000) and `handoff`.
- A missing or stale value answers `handoff: yes`; outside a Herdr pane it answers `unavailable`.
- The review and the CI stage take that reading as they load, so every entrance is measured.
- The default of 100 000 leaves a median review round room under the compact trigger. A large round can still reach it.
- So the review stage measures again with every round it records. It hands over between rounds, not at the end of the panel ([ADR 0032](adr/0032-the-stage-measures-the-context-on-entry-and-a-handoff-grants-one-skip.md), [ADR 0018](adr/0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md)).
- A claimed worker session sets `autoCompactWindow: 312500` and pins `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=80`. A session nobody hands over compacts by 250 000 tokens ([ADR 0031](adr/0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md)).
- The workflow states that bound, not an undocumented percentage of the window. It stops the session growing until the model refuses ([ADR 0034](adr/0034-the-compact-trigger-is-raised-through-the-window.md)).

The stage being entered acts on that verdict, not the driver ([ADR 0029](adr/0029-a-worker-resets-its-context-by-a-handoff-not-by-compaction.md), [ADR 0032](adr/0032-the-stage-measures-the-context-on-entry-and-a-handoff-grants-one-skip.md)):

- On `handoff: yes` it does no work and writes a four-section note instead.
- A detached process clears the session and sends `/worker:work` back to the pane. The pipeline continues at that stage in a fresh context.
- So the two stages that decide what gets merged see the issue, a page of notes and the diff, not the whole run.
- Compaction stays the safety net under the threshold, never the plan. It summarises the dead ends along with the decisions, at a moment nobody chooses.

Tools, plugins and instructions:

- Each main-session agent lists its `tools:`; a tool that is not listed is not sent to the model at all.
- Skill alone costs ≈3k, because it carries the listing of every skill in the account.
- Planner skills are typed by the user, so the planner has no Skill tool. The worker keeps it, because `/worker:work` invokes the stages itself.
- `plan.sh` and `claim.sh` start their session with `--strict-mcp-config`, so account-level MCP connectors (their instructions and tool names, ≈1.7k) stay out.
- They also pass `--settings` that disables the other workflow plugins (`enabledPlugins`).
- So a planner never carries worker skill descriptions or the reviewer agent listing, and a worker never carries the planner's.
- `/repo-standards:standardize` and `/repo-standards:apply` are `disable-model-invocation: true` too.
- Plugin agents cannot be hidden, so the six auditor descriptions (one line each) are listed in every session that enables `repo-standards`.
- Worker sessions keep `repo-standards` for `/repo-standards:adr` and carry those six lines; planner sessions start with it disabled.
- Every planner skill is `disable-model-invocation: true`, which keeps even its description out of context. Enabling the plugin costs other sessions nothing.
- Plugin token cost is visible with `claude plugin details <plugin>@workflows`; keep skill descriptions to one sentence.
- Repository instruction files (`AGENTS.md`, `CLAUDE.md`) stay under 200 lines. A monorepo keeps one pair per area, which loads only when an agent works there.
- What goes in is what every session needs, because it loads in every session and every subagent.
- So the vision is a linked file ([vision.md](vision.md)) and not an `@` import. An import would load at launch and cost the whole text everywhere.
- A documentation page is read where it is needed and nowhere else. `/worker:docs` puts the question to a `docs-lookup` subagent that runs `claude-docs.sh`.
- The pages stay in that context. A page can run to tens of thousands of tokens, so the agent narrows it with `grep` and runs on sonnet.
- The worker's context gets the answer and the page URLs, under 300 words ([ADR 0030](adr/0030-agents-verify-claude-code-facts-against-the-live-documentation.md)).

## Measuring it
`scripts/context-report.py` prints one line per finished worker session:

- Claude Code version and turns
- the context at the start of the review and of the pull request stage, and the peak
- the share of tool output that came from reading files through the shell
- the number of read, edit, write and shell calls, and the number of sleep calls

With no argument it reads the worktree sessions under `~/.claude/projects` (or `$CLAUDE_CONFIG_DIR`). A path argument reads one transcript or one directory.

It reads Claude Code's session transcripts, a format that is internal and changes without notice. So it fails with an `error:` line naming the version when it meets a format it does not understand. It is a diagnostic for the maintainer and never an input to the pipeline, so it lives in `scripts/` and not in a plugin. It looks at finished sessions; the checkpoint above is the live reading of the one that is running.
