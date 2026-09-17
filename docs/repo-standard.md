# Repository standard

Every repository that runs this workflow has:

| File | Purpose | Checked by |
| --- | --- | --- |
| `CLAUDE.md` | Commands and conventions an agent cannot infer; under 200 lines | `check.sh` (exists, length warning, placeholders) |
| `docs/architecture.md` | One-page map: components, data flow, boundaries | `check.sh` (exists, not a stub) |
| `docs/adr/README.md` + `NNNN-title.md` | Decisions, MADR-trimmed, sequential, with a Status line | `check.sh` (index, numbering, status) |
| `.github/PULL_REQUEST_TEMPLATE.md` | Closes, what and why, verification, limits | `check.sh` (warning) |
| `.claude/settings.json` | Marketplace, enabled plugins, `WF_*` env, permission allowlist, attribution off | `check.sh` (warnings) |

Create everything with `/repo-standards:init-repo`; it never overwrites. Verify with `/repo-standards:docs-check` or `plugins/repo-standards/scripts/check.sh` in CI. The docs reviewer in the worker panel flags changes that should have touched these files and did not.
