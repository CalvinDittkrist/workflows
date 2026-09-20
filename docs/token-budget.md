# Token budget: what loads when

The pipeline is designed so that every context holds only what its job needs.

| Context | Loaded at start | Loaded on demand | Never |
| --- | --- | --- | --- |
| orchestrator (sonnet) | its agent prompt (≈300 tokens), the Bash tool only (≈7k with system prompt, versus ≈17k for the full tool set) | script output per command; the Herdr skill only via `/orchestrator:herdr` | CLAUDE.md (`omitClaudeMd`), code, diffs |
| planner (opus) | its agent prompt (≈300 tokens), eight tools (Bash, Read, Write, Edit, Grep, Glob, Agent, WebFetch; ≈10k), CLAUDE.md with the imported AGENTS.md, topic or issue from the hook (same caps as the worker) | one stage skill body per invocation; templates (spec, ticket, brief) only when that stage runs; a research subagent's report | worker and orchestrator skills; other stages' bodies |
| worker (opus) | eight tools (the planner's set with Skill instead of WebFetch; ≈13k), CLAUDE.md with the imported AGENTS.md, issue context from the hook (body capped at 6 000 chars, last 8 comments at 1 500 chars) | skill bodies when invoked; diff context from `diff-context.sh` | reviewer transcripts (only their reports return) |
| each reviewer (inherit; docs reviewer sonnet) | its prompt (≈350 tokens), CLAUDE.md (docs reviewer omits it), the brief | files it chooses to read | the worker's conversation |
| pr-author | its prompt, the brief | diff, issue | the worker's conversation |
| each auditor (inherit) | its prompt (≈600 tokens), CLAUDE.md, the brief with the facts block (≈1k on a mid-size repository) | files it chooses to read | the other auditors' replies, the main session's conversation |

Practices that keep the budget flat:
- Skills are short and call scripts that print compact `key: value` lines and tables, never raw JSON.
- `!`command`` injection puts facts (mode, issue, range) into the skill at invocation time instead of asking the model to discover them with tool calls. Each injected command is a plugin script pre-approved in the skill's `allowed-tools`; without that, a forked skill's injection is refused by the permission check.
- Long waits (`pr-wait.sh`) happen in one blocking script call, not in polling turns.
- Worker sessions start with `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`, so their subagents block and hand the report back as the tool result. A background launch returns at once and leaves the agent waiting: the measured cost of that was 328 sleep turns in one session, each one a full pass over the context, 120k to 412k tokens ([ADR 0017](adr/0017-worker-subagents-run-in-the-foreground.md)). The reports themselves are about 3k tokens per session.
- A main-thread agent's prompt replaces Claude Code's default system prompt entirely, so the default guidance to prefer the file tools is gone and the prompt has to carry it itself. Without it a worker reads and writes through the shell: over the 19 worker sessions before this change, 1 `Read` call against 2 307 shell calls, and 55 % of a session's tool output came from files printed with `cat` or `sed -n` (median, maximum 85 %). The worker prompt therefore states how to work with files (read with a range, search with the search tools, change with edits, write only new files, no whole-file shell reads or heredoc rewrites, independent reads in parallel). `scripts/context-report.py` measures whether that holds. The planner prompt does not carry the guidance yet; its sessions have not been measured.
- Reviewers run in parallel and only the reviewers that returned FIX are re-run.
- Each main-session agent lists its `tools:`; a tool that is not listed is not sent to the model at all (measured on 2.1.274: Skill alone costs ≈3k because it carries the listing of every skill in the account). Planner skills are typed by the user, so the planner has no Skill tool; the worker keeps it because `/worker:work` invokes the stages itself.
- `plan.sh` and `claim.sh` start their session with `--strict-mcp-config`, so account-level MCP connectors (their instructions and tool names, ≈1.7k) stay out; and with `--settings` that disables the other workflow plugins (`enabledPlugins`), so a planner never carries worker skill descriptions or the reviewer agent listing, and a worker never carries the planner's.
- `/repo-standards:standardize` and `/repo-standards:apply` are `disable-model-invocation: true` too. Plugin agents cannot be hidden, so the six auditor descriptions (one line each) are listed in every session that enables `repo-standards`: worker sessions keep it for `/repo-standards:adr` and carry those six lines, planner sessions start with it disabled.
- Every planner skill is `disable-model-invocation: true`, which keeps even its description out of context (documented behaviour); enabling the plugin costs other sessions nothing.
- Plugin token cost is visible with `claude plugin details <plugin>@workflows`; keep skill descriptions to one sentence.
- Repository instruction files (`AGENTS.md`, `CLAUDE.md`) stay under 200 lines; a monorepo keeps one pair per area, which loads only when an agent works there.

## Measuring it
`scripts/context-report.py` prints one line per finished worker session: Claude Code version, turns, the context at the start of the review and of the pull request stage, the peak, the share of tool output that came from reading files through the shell, the number of read, edit, write and shell calls, and the number of sleep calls. With no argument it reads the worktree sessions under `~/.claude/projects` (or `$CLAUDE_CONFIG_DIR`); a path argument reads one transcript or one directory.

It reads Claude Code's session transcripts, a format that is internal and changes without notice, so it fails with an `error:` line naming the version when it meets a format it does not understand. It is a diagnostic for the maintainer and never an input to the pipeline, which is why it lives in `scripts/` and not in a plugin.
