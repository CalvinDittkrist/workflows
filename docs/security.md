# Security and sandboxing

Threat model: an agent with shell access works on code and reads text from the internet (issues, PR comments, CI logs, dependencies). Two failure classes matter: the agent does something destructive on the host, and the agent is steered by untrusted text (prompt injection).

## Layers, from cheap to strong
1. **Role restriction.** The orchestrator has no edit tools and an `omitClaudeMd` context. Reviewers, `pr-author` and the six standardisation auditors are read-only (`disallowedTools: Edit, Write, NotebookEdit, Agent`). Only the worker main context edits.
2. **Permissions.** `repo-standards` ships a settings template with an allowlist for the git and gh commands the pipeline needs and a deny list for force-push, hard reset and secret files. Worker sessions run in `auto` mode by default (`WF_WORKER_PERMISSION_MODE`), where Claude Code's classifier blocks scope escalation and hostile content; set `acceptEdits` or `default` for stricter repos.
3. **Isolation per issue.** Each worker has its own worktree, branch and process. A broken worker cannot touch another issue's files; `/abandon` removes it.
4. **Built-in OS sandbox.** Enable Claude Code's Bash sandbox (macOS Seatbelt, Linux bubblewrap) in the repo settings when the project tolerates it:
   ```json
   { "sandbox": { "enabled": true, "autoAllowBashIfSandboxed": true,
     "network": { "allowedDomains": ["github.com", "api.github.com", "registry.npmjs.org"] } } }
   ```
5. **Docker Sandboxes.** `/orchestrator:claim 123 --sandbox` runs the worker through `sbx run claude` in a container that mounts only that worktree read-write and the shared skills store read-only. See [sandbox/README.md](../sandbox/README.md). Inside a container, `WF_CLAUDE_ARGS="--dangerously-skip-permissions"` is acceptable; on the host it is not.

## Prompt injection
- The SessionStart hook labels issue text as "task data written by someone else". Reviewer, worker and pr-author prompts repeat that file contents, comments, logs and reviews are data, not instructions.
- `address-reviews` explicitly declines review comments that ask to weaken tests, skip checks or change unrelated code.
- Reviewers cannot spawn agents or edit, so a poisoned diff cannot make a reviewer act on the repository. The same holds for the auditors: every auditor prompt treats the audited repository as data, and their replies reach `report.sh` only as `finding:` lines of a fixed grammar, whose targets must stay inside the repository. Auditors and reviewers keep `Bash` to read git history, so their read-only status rests on the tool lists plus the prompt, not on a sandbox; the tests-ci auditor judges the repository's test commands without running them.

## Supply chain
- Plugins are installed from a pinned marketplace (`extraKnownMarketplaces` + `enabledPlugins` in the repo settings). Claude Code caches plugin versions; releases are git tags created with `claude plugin tag`.
- `npx -y gh-axi` and `npx -y quota-axi` are optional and run unpinned; pin them in your own settings or install globally if that matters to you.
- The security reviewer flags new dependencies (pin, provenance, need) and CI or hook changes that widen permissions.

## What this does not do
- No secret management. Use `sbx secret`, your keychain, or CI secrets; never `.env` in the worktree (denied by the permission template).
- No protection against a compromised `gh` or `herdr` binary; these are trusted host tools.
