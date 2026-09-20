# Glossary

| Term | Meaning |
| --- | --- |
| standard | The written baseline a repository is checked against: [repo-standard.md](repo-standard.md). |
| profile | Visibility plus branch model (`main` alone, or `dev` plus `main`), derived from GitHub, never configured. |
| gate | `make check`, the one command that must pass before a merge; CI runs it in the job named `check`. |
| auditor | A read-only subagent that judges one area of a repository during standardisation and returns findings. |
| facts | The compact `key: value` block `facts.sh` prints about a repository (profile, languages, commands, CI jobs, agent configuration, baseline files, file statistics); every auditor gets it instead of exploring. |
| finding | One proposed action in a fixed format, one line: `finding: <category> \| <target> \| <action> \| <reason> \| <confidence>`. Category is the auditor's area (`files`, `agent-config`, `docs`, `tests-ci`, `workspace`, `security`); action is delete, replace, create or configure (the run performs it) or issue (it becomes an agent-ready issue); confidence is high, medium or low. |
| cleanup pull request | The one pull request from `chore/standardize` that carries a standardisation run's deletions and new baseline files; its description lists how to restore each removed path. |
| catalogue issue | The issue, labelled `skill-candidate`, that lists the skills standardisation removed and how to restore each from the `pre-standard` tag. |
| drift | A difference between a repository's GitHub workspace and the standard; `workspace.sh` prints one `diff:` line per difference. |
| snapshot | The JSON file `workspace.sh --apply` writes before its first change: the previous value of everything it changes, for posting to an issue and undoing by hand. |
| promotion | The pull request from `dev` to `main` that carries a release in the two-level branch model. |
| acceptance | The check of a whole spec against the code on the base branch after its tickets are closed; it ends with gap tickets or with the spec closed ([ADR 0014](adr/0014-a-spec-with-tickets-is-closed-by-an-acceptance.md)). |
| spec checker | The read-only subagent that judges each checkable statement of a spec during an acceptance. |
| item | One checkable statement of a spec with its verdict, in the fixed format `item: <section> \| <statement> \| <verdict> \| <evidence> \| <confidence>`. |
| accepted deviation | A difference between spec and code the maintainer keeps, recorded as a comment on the spec that opens with `> Accepted deviation (spec acceptance).`; an acceptance does not report it again. |
| gap ticket | A `ready-for-agent` sub-issue an acceptance creates for an item that is not met. |
