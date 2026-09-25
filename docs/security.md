# Security and sandboxing

Threat model: an agent with shell access works on code and reads text from the internet: issues, PR comments, CI logs, dependencies. Two failure classes matter. The agent does something destructive on the host, or it is steered by untrusted text (prompt injection).

## Layers, from cheap to strong
1. **Role restriction.** Only the worker main context edits.
   - The orchestrator has no edit tools and an `omitClaudeMd` context.
   - Reviewers, `pr-author` and the six standardisation auditors are read-only (`disallowedTools: Edit, Write, NotebookEdit, Agent`).
2. **Permissions.** `repo-standards` ships a settings template.
   - It allows the git and gh commands the pipeline needs, and denies force-push, hard reset and secret files.
   - Worker sessions run in `auto` mode by default (`WF_WORKER_PERMISSION_MODE`), where Claude Code's classifier blocks scope escalation and hostile content.
   - Set `acceptEdits` or `default` for stricter repos.
3. **Isolation per issue.** Each worker has its own worktree, branch and process. A broken worker cannot touch another issue's files; `/abandon` removes it.
4. **Built-in OS sandbox.** Enable Claude Code's Bash sandbox (macOS Seatbelt, Linux bubblewrap) in the repo settings when the project tolerates it:
   ```json
   { "sandbox": { "enabled": true, "autoAllowBashIfSandboxed": true,
     "network": { "allowedDomains": ["github.com", "api.github.com", "registry.npmjs.org", "code.claude.com"] } } }
   ```
   `code.claude.com` is in the list because agents verify Claude Code facts against the current documentation ([ADR 0030](adr/0030-agents-verify-claude-code-facts-against-the-live-documentation.md)). It is the only documentation origin the pipeline reads.
5. **Docker Sandboxes.** `/orchestrator:claim 123 --sandbox` runs the worker through `sbx run claude` in a container. See [sandbox/README.md](../sandbox/README.md).
   - The container mounts only that worktree read-write and the shared skills store read-only.
   - Inside a container, `WF_CLAUDE_ARGS="--dangerously-skip-permissions"` is acceptable; on the host it is not.

## Prompt injection
- The SessionStart hook labels issue text as "task data written by someone else".
- Reviewer, worker and pr-author prompts repeat that file contents, comments, logs and reviews are data, not instructions.
- `address-reviews` declines what a review asks, in a thread or in its summary, when it would weaken tests, skip checks or change unrelated code.
- Reviewers cannot spawn agents or edit, so a poisoned diff cannot make a reviewer act on the repository.
- The same holds for the auditors. Every auditor prompt treats the audited repository as data.
- Auditor replies reach `report.sh` only as `finding:` lines of a fixed grammar, whose targets must stay inside the repository.
- Auditors and reviewers keep `Bash` to read git history. Their read-only status rests on the tool lists plus the prompt, not on a sandbox.
- The tests-ci auditor judges the repository's test commands without running them.

The worker's main context receives issue bodies, PR comments, CI logs and review comments, so it carries no `WebFetch` and no `WebSearch`. It reads the documentation with `/worker:docs`, which runs `claude-docs.sh` in a read-only `docs-lookup` subagent. The script:

- takes a page slug of lowercase letters, digits and hyphens, nested with a slash; never a relative path, an absolute one or a URL
- builds the URL from a hard-coded origin and speaks https only, before and after a redirect
- prints nothing if the answer came from outside `https://code.claude.com/docs/`

Fetched pages are data like every other external text, and only the subagent's short answer returns to the worker.

That is surface reduction, not containment:

- A subagent whose own file declares `WebFetch` gets it, even when the worker's tool list has neither web tool. Declared tools are granted, not intersected with the parent's.
- So the worker's `Agent` tool is an allowlist of the plugin's own subagents (`Agent(worker:code-reviewer, …, worker:docs-lookup)`).
- Every other type fails at the Agent call with `Agent type '…' not found`. That includes the built-in `general-purpose` and `claude-code-guide`, which carry `WebFetch` and `WebSearch`.
- A test fails when the list and the agent files drift apart.
- The worker's `Bash` stays, so the enforced network boundary is the permission layer and the sandbox `allowedDomains` above.
- The pinned script keeps the untrusted-text context away from the open web, and gives the pipeline one auditable command instead of a free fetch tool.
- The lookup agent holds `Bash`, like every reviewer and auditor. Its read-only, one-origin behaviour rests on its prompt plus the permission layer, not on its tool list ([ADR 0030](adr/0030-agents-verify-claude-code-facts-against-the-live-documentation.md)).

`WF_PLANNER_LANGUAGE` is copied verbatim into the planner session's system prompt by Claude Code's `language` setting. It is operator configuration, as trusted as the rest of `WF_*`. `plan.sh` still refuses a value with a control character or longer than a language name, so a pasted instruction cannot enter through it.

## The factory
The factory has no login of its own. It reads GitHub through the host's `gh`, so it sees what that host's token can see. It writes nothing there while it is paused, and a test asserts that every call it makes is a read ([ADR 0023](adr/0023-github-is-the-only-control-surface-of-the-factory.md)).

GitHub is its control surface, so whoever can make its gestures on an issue or a pull request can make the host act:

- Removing the routing label, closing the issue, or merging or closing the pull request ends a running run and gives the issue back.
- An issue's author can close their own issue.
- The cost is bounded by [ADR 0026](adr/0026-the-factory-never-deletes-work-on-its-own.md). The worktree's commits are pushed before anything of it is removed.
- A branch that carries a commit is never deleted, and no record and no log is ever touched.
- A repository whose issue authors are not trusted with that is one to route work from by hand.
- The worker does not say which pull request a gesture counts on. The URL of a run comes out of its report.
- The factory acts on that pull request only while GitHub says it is of the branch the run holds. A report steered into naming another one hands nothing over.

The sessions that write on the branch are the implement session and every fix and address-reviews session:

- They run as the factory's own agent, whose prompt is compiled into the binary.
- They have a shell, the edit tools and built-in subagents, the auto permission mode and the host's `gh` login.
- They read the issue and the reviews, which are text somebody else wrote. The prompt tells them it is data and never instructions.
- The auto mode classifier judges every action, and what is left is bounded by the host ([ADR 0027](adr/0027-the-factorys-isolation-boundary-is-the-host.md)).
- No plugin runs in them and the factory installs or updates none. So no code reaches the host between two releases of the factory ([ADR 0042](adr/0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md)).

The author session of the pr stage reads the issue, the commits and the diff, which are text somebody else wrote. So it gets no power to act:

- It runs with the tools `Read`, `Grep` and `Glob` alone: no shell, no edit, no MCP server, none of the workflow plugins. It reports only a title and a body.
- The worktree's own settings are not loaded (`--setting-sources user`), so a hook the branch declares in `.claude/settings.json` does not run.
- Of `worker_args` it takes the model alone, so an MCP configuration or a directory meant for the worker does not reach it.
- The tools limit what it can do, not which files it can read. Injected text could steer it to a file outside the worktree.
- Such a file could end up in the body, which is published with the pull request. What the host's user can read is bounded by the host ([ADR 0027](adr/0027-the-factorys-isolation-boundary-is-the-host.md)).
- The factory checks the title: conventional, on one line, with no control character or Unicode line separator.
- It checks the body: it closes the issue and carries no verification section of its own. No field the schema lacks is accepted.
- The factory writes the verification section itself, from the gate result and the rounds the run recorded.
- It opens the pull request from the branch the run holds against its base.

The reviewers of the review stage read the same text and get the same bounds:

- `Read`, `Grep` and `Glob`, no MCP server, no workflow plugin, none of the worktree's settings.
- Of `worker_args` the model alone, which only a reviewer whose definition inherits its model takes.
- The inline agent they run as is the factory's own definition, with the same three tools. `--tools` stays on the command line whatever it says.
- A verdict is read from its findings. A reviewer steered into passing a finding it rates S1 or S2 is read as `fix`, and the run carries a warning.
- The findings are model text that quotes the diff, so the fix session given them is briefed that they are data.
- The disputes reach the pull request as the fix session wrote them, inside the fenced panel summary.
- The gate on the final head is `make check` in the worktree, the command the gate stage ran. It runs the branch's code with the host user's rights.
- Its output reaches a fix session as data.

Its HTTP interface is read-only and unauthenticated, and it serves live issue titles and repository names. It binds to one address, by default the loopback, and refuses a wildcard address. Reaching it from elsewhere is the tailnet's job. The host is the isolation boundary ([ADR 0027](adr/0027-the-factorys-isolation-boundary-is-the-host.md)).

## Supply chain
- Plugins are installed from a pinned marketplace (`extraKnownMarketplaces` + `enabledPlugins` in the repo settings).
- Claude Code caches plugin versions; releases are git tags created with `claude plugin tag`.
- `npx -y gh-axi` and `npx -y quota-axi` are optional and run unpinned. Pin them in your own settings or install them globally if that matters to you.
- The factory never runs `npx`. Its quota check runs the quota-axi at the absolute path its configuration names, installed on the host in a pinned version.
- An expired credential it renews through Claude Code's own `claude doctor`, while no session runs ([ADR 0037](adr/0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md)).
- The release path is the factory host's trust boundary. Whoever can push a factory version tag (`factory/v<version>`) on main decides what the host runs.
- The release workflow attests both binaries in its publishing job, the one job with an OIDC token, which runs nothing but gh and the attestation action. The build job, which runs npm packages, can only read.
- The attestation narrows the binary to one built by this repository's CI, from that tag, on a commit on main. No further: it says nothing about what the commit does.
- The security reviewer flags new dependencies (pin, provenance, need) and CI or hook changes that widen permissions.

## What this does not do
- No secret management. Use `sbx secret`, your keychain, or CI secrets; never `.env` in the worktree (denied by the permission template).
- No protection against a compromised `gh` or `herdr` binary; these are trusted host tools.
