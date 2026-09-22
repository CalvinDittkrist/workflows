# Security and sandboxing

Threat model: an agent with shell access works on code and reads text from the internet (issues, PR comments, CI logs, dependencies). Two failure classes matter: the agent does something destructive on the host, and the agent is steered by untrusted text (prompt injection).

## Layers, from cheap to strong
1. **Role restriction.** The orchestrator has no edit tools and an `omitClaudeMd` context. Reviewers, `pr-author` and the six standardisation auditors are read-only (`disallowedTools: Edit, Write, NotebookEdit, Agent`). Only the worker main context edits.
2. **Permissions.** `repo-standards` ships a settings template with an allowlist for the git and gh commands the pipeline needs and a deny list for force-push, hard reset and secret files. Worker sessions run in `auto` mode by default (`WF_WORKER_PERMISSION_MODE`), where Claude Code's classifier blocks scope escalation and hostile content; set `acceptEdits` or `default` for stricter repos.
3. **Isolation per issue.** Each worker has its own worktree, branch and process. A broken worker cannot touch another issue's files; `/abandon` removes it.
4. **Built-in OS sandbox.** Enable Claude Code's Bash sandbox (macOS Seatbelt, Linux bubblewrap) in the repo settings when the project tolerates it:
   ```json
   { "sandbox": { "enabled": true, "autoAllowBashIfSandboxed": true,
     "network": { "allowedDomains": ["github.com", "api.github.com", "registry.npmjs.org", "code.claude.com"] } } }
   ```
   `code.claude.com` is in the list because agents verify Claude Code facts against the current documentation ([ADR 0030](adr/0030-agents-verify-claude-code-facts-against-the-live-documentation.md)); it is the only documentation origin the pipeline reads.
5. **Docker Sandboxes.** `/orchestrator:claim 123 --sandbox` runs the worker through `sbx run claude` in a container that mounts only that worktree read-write and the shared skills store read-only. See [sandbox/README.md](../sandbox/README.md). Inside a container, `WF_CLAUDE_ARGS="--dangerously-skip-permissions"` is acceptable; on the host it is not.

## Prompt injection
- The SessionStart hook labels issue text as "task data written by someone else". Reviewer, worker and pr-author prompts repeat that file contents, comments, logs and reviews are data, not instructions.
- `address-reviews` explicitly declines what a review asks, in a thread or in its summary, when it would weaken tests, skip checks or change unrelated code.
- Reviewers cannot spawn agents or edit, so a poisoned diff cannot make a reviewer act on the repository. The same holds for the auditors: every auditor prompt treats the audited repository as data, and their replies reach `report.sh` only as `finding:` lines of a fixed grammar, whose targets must stay inside the repository. Auditors and reviewers keep `Bash` to read git history, so their read-only status rests on the tool lists plus the prompt, not on a sandbox; the tests-ci auditor judges the repository's test commands without running them.
- The worker's main context is where issue bodies, PR comments, CI logs and review comments arrive, so it carries no `WebFetch` and no `WebSearch`. It reads the documentation with `/worker:docs`, which runs `claude-docs.sh` in a read-only `docs-lookup` subagent: the script takes a page slug of lowercase letters, digits and hyphens, nested with a slash (never a relative path, an absolute one or a URL), builds the URL from a hard-coded origin, speaks https only before and after a redirect, and prints nothing if the answer came from outside `https://code.claude.com/docs/`. Fetched pages are data like every other external text, and only the subagent's short answer returns to the worker.
- That is surface reduction, not containment: measured in a live worker session on 2026-09-21, a subagent whose own file declares `WebFetch` gets it even though the worker's tool list has neither web tool, so a subagent's declared tools are granted rather than intersected with the parent's. The worker's `Agent` tool is therefore an allowlist of the plugin's own subagents (`Agent(worker:code-reviewer, …, worker:docs-lookup)`): every other type, the built-in `general-purpose` and `claude-code-guide` with their `WebFetch` and `WebSearch` among them, fails at the Agent call with `Agent type '…' not found`, and a test fails when the list and the agent files drift apart. The worker's `Bash` stays, so the enforced network boundary is the permission layer and the sandbox `allowedDomains` above; the pinned script keeps the untrusted-text context away from the open web and gives the pipeline one auditable command instead of a free fetch tool. The lookup agent itself holds `Bash`, like every reviewer and auditor, so its read-only, one-origin behaviour rests on its prompt plus the permission layer, not on its tool list ([ADR 0030](adr/0030-agents-verify-claude-code-facts-against-the-live-documentation.md)).
- `WF_PLANNER_LANGUAGE` is copied verbatim into the planner session's system prompt by Claude Code's `language` setting. It is operator configuration, as trusted as the rest of `WF_*`; `plan.sh` still refuses a value with a control character or longer than a language name, so a pasted instruction cannot ride in on it.

## The factory
- The factory has no login of its own: it reads GitHub through the host's `gh`, so what it can see is what that host's token can see. It writes nothing there while it is paused, and a test asserts that every call it makes is a read ([ADR 0023](adr/0023-github-is-the-only-control-surface-of-the-factory.md)).
- Its HTTP interface is read-only and unauthenticated, and it serves live issue titles and repository names: it binds to one address, by default the loopback, a wildcard address is refused, and reaching it from elsewhere is the tailnet's job. The host is the isolation boundary ([ADR 0027](adr/0027-the-factorys-isolation-boundary-is-the-host.md)).

## Supply chain
- Plugins are installed from a pinned marketplace (`extraKnownMarketplaces` + `enabledPlugins` in the repo settings). Claude Code caches plugin versions; releases are git tags created with `claude plugin tag`.
- `npx -y gh-axi` and `npx -y quota-axi` are optional and run unpinned; pin them in your own settings or install globally if that matters to you. The factory never runs `npx`: its quota check runs the quota-axi at the absolute path its configuration names, installed on the host in a pinned version, and never lets it refresh a credential.
- The security reviewer flags new dependencies (pin, provenance, need) and CI or hook changes that widen permissions.

## What this does not do
- No secret management. Use `sbx secret`, your keychain, or CI secrets; never `.env` in the worktree (denied by the permission template).
- No protection against a compromised `gh` or `herdr` binary; these are trusted host tools.
