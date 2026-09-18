---
name: init-repo
description: Bring a repository up to the workflow baseline: AGENTS.md, a CLAUDE.md that imports it, a Makefile with the check gate, docs/architecture.md, ADR folder, PR template and .claude/settings.json. Never overwrites existing files.
disable-model-invocation: true
---
1. Run `"${CLAUDE_PLUGIN_ROOT}/scripts/scaffold.sh"` and show its output.
2. For every `created:` file, fill the placeholders from the codebase, briefly:
   - `AGENTS.md`: the run command and the conventions and gotchas an agent cannot infer from the code. Keep it under 60 lines.
   - `Makefile`: replace the placeholder `check` recipe with the lint, test and build commands CI gates on (find them in package manifests, existing scripts and CI). If CI exists, make it run `make check` in a job named `check`.
   - `docs/architecture.md`: components table and data flow from what actually exists. One page. No speculation about future work.
   - If `CLAUDE.md` was kept, move its project instructions to `AGENTS.md` and leave the line `@AGENTS.md` plus at most a short Claude-only section.
   - If `.claude/settings.json` was kept, tell the user which keys from the plugin's `templates/settings.json` to merge (marketplace, enabledPlugins, attribution, env, permissions).
3. Run `make check` and `"${CLAUDE_PLUGIN_ROOT}/scripts/check.sh"` and report both results. Suggest the first ADR if none exists.
