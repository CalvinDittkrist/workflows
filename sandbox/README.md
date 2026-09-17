# Docker Sandboxes for workers

`/orchestrator:claim 123 --sandbox` starts the worker through `plugins/orchestrator/scripts/sbx-worker.sh`, which:

1. creates a sandbox `wf-<repo>-<branch>` with `sbx create claude <worktree>`: only that worktree is mounted read-write, the shared skills store read-only (`--skills readonly`), and `WF_MODE`, `WF_ISSUE`, `WF_BASE_BRANCH` are passed through;
2. installs the marketplace and the `worker` and `repo-standards` plugins inside the container (`WF_MARKETPLACE`, default `CalvinDittkrist/workflows`);
3. attaches with `sbx run --name … -- <claude args>` in the Herdr pane, so Herdr still detects the agent and the orchestrator prompts it the same way.

Recommended one-time setup on the host:

```sh
sbx skills add CalvinDittkrist/workflows          # skills visible in every sandbox
sbx secret set github                             # gh auth without exposing the token
sbx policy deny network --resource '*' && sbx policy allow network --resource github.com --resource api.github.com --resource registry.npmjs.org
```

Inside the container you may run workers with `WF_CLAUDE_ARGS="--dangerously-skip-permissions"`; the container, not the permission prompt, is the boundary. Never use that flag on the host.

To avoid installing plugins on every new sandbox, start one, let the install run, then `sbx template save wf-worker` and set `WF_CLAUDE_ARGS`/`sbx run -t wf-worker` accordingly. Status: launcher script and flag are implemented; the container path has not been exercised end to end in this repository yet.
