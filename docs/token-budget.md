# Token budget: what loads when

The pipeline is designed so that every context holds only what its job needs.

| Context | Loaded at start | Loaded on demand | Never |
| --- | --- | --- | --- |
| orchestrator (sonnet) | its agent prompt (≈300 tokens), skill descriptions | script output per command; the Herdr skill only via `/orchestrator:herdr` | CLAUDE.md (`omitClaudeMd`), code, diffs |
| planner (opus) | its agent prompt (≈300 tokens), CLAUDE.md, topic or issue from the hook (same caps as the worker) | one stage skill body per invocation; templates (spec, ticket, brief) only when that stage runs; a research subagent's report | worker and orchestrator skills; other stages' bodies |
| worker (opus) | CLAUDE.md, rules, issue context from the hook (body capped at 6 000 chars, last 8 comments at 1 500 chars) | skill bodies when invoked; diff context from `diff-context.sh` | reviewer transcripts (only their reports return) |
| each reviewer (inherit; docs reviewer sonnet) | its prompt (≈350 tokens), CLAUDE.md (docs reviewer omits it), the brief | files it chooses to read | the worker's conversation |
| pr-author | its prompt, the brief | diff, issue | the worker's conversation |

Practices that keep the budget flat:
- Skills are short and call scripts that print compact `key: value` lines and tables, never raw JSON.
- `!`command`` injection puts facts (mode, issue, range) into the skill at invocation time instead of asking the model to discover them with tool calls. Each injected command is a plugin script pre-approved in the skill's `allowed-tools`; without that, a forked skill's injection is refused by the permission check.
- Long waits (`pr-wait.sh`) happen in one blocking script call, not in polling turns.
- Reviewers run in parallel and only the reviewers that returned FIX are re-run.
- `plan.sh` and `claim.sh` start their session with `--settings` that disables the other workflow plugins (`enabledPlugins`), so a planner never carries worker skill descriptions or the reviewer agent listing, and a worker never carries the planner's.
- Every planner skill is `disable-model-invocation: true`, which keeps even its description out of context (documented behaviour); enabling the plugin costs other sessions nothing.
- Plugin token cost is visible with `claude plugin details <plugin>@workflows`; keep skill descriptions to one sentence.
- Repository CLAUDE.md files stay under 200 lines; path-scoped `.claude/rules/*.md` hold the rest.
