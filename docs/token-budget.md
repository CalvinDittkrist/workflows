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
- Reviewers run in parallel and only the reviewers that returned FIX are re-run.
- Each main-session agent lists its `tools:`; a tool that is not listed is not sent to the model at all (measured on 2.1.274: Skill alone costs ≈3k because it carries the listing of every skill in the account). Planner skills are typed by the user, so the planner has no Skill tool; the worker keeps it because `/worker:work` invokes the stages itself.
- `plan.sh` and `claim.sh` start their session with `--strict-mcp-config`, so account-level MCP connectors (their instructions and tool names, ≈1.7k) stay out; and with `--settings` that disables the other workflow plugins (`enabledPlugins`), so a planner never carries worker skill descriptions or the reviewer agent listing, and a worker never carries the planner's.
- `/repo-standards:standardize` and `/repo-standards:apply` are `disable-model-invocation: true` too. Plugin agents cannot be hidden, so the six auditor descriptions (one line each) are listed in every session that enables `repo-standards`: worker sessions keep it for `/repo-standards:adr` and carry those six lines, planner sessions start with it disabled.
- Every planner skill is `disable-model-invocation: true`, which keeps even its description out of context (documented behaviour); enabling the plugin costs other sessions nothing.
- Plugin token cost is visible with `claude plugin details <plugin>@workflows`; keep skill descriptions to one sentence.
- Repository instruction files (`AGENTS.md`, `CLAUDE.md`) stay under 200 lines; a monorepo keeps one pair per area, which loads only when an agent works there.
