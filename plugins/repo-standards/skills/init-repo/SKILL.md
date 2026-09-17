---
name: init-repo
description: Bring a repository up to the workflow baseline: CLAUDE.md, docs/architecture.md, ADR folder, PR template and .claude/settings.json. Never overwrites existing files.
disable-model-invocation: true
---
1. Run `"${CLAUDE_PLUGIN_ROOT}/scripts/scaffold.sh"` and show its output.
2. For every `created:` file, fill the placeholders from the codebase, briefly:
   - `CLAUDE.md`: the real test, lint and run commands (find them in package manifests, Makefiles, CI). Keep it under 60 lines; only what cannot be inferred from code.
   - `docs/architecture.md`: components table and data flow from what actually exists. One page. No speculation about future work.
   - If `.claude/settings.json` was kept, tell the user which keys from the plugin's `templates/settings.json` to merge (marketplace, enabledPlugins, attribution, env, permissions).
3. Run `"${CLAUDE_PLUGIN_ROOT}/scripts/check.sh"` and report the result. Suggest the first ADR if none exists.
