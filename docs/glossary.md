# Glossary

| Term | Meaning |
| --- | --- |
| standard | The written baseline a repository is checked against: [repo-standard.md](repo-standard.md). |
| profile | Visibility plus branch model (`main` alone, or `dev` plus `main`), derived from GitHub, never configured. |
| gate | `make check`, the one command that must pass before a merge; CI runs it in the job named `check`. |
| auditor | A read-only subagent that judges one area of a repository during standardisation and returns findings. |
| finding | One proposed action in a fixed format: category, target, action (delete, replace, create, configure, issue), reason, confidence. |
| catalogue issue | The issue, labelled `skill-candidate`, that lists the skills standardisation removed and how to restore each from the `pre-standard` tag. |
| promotion | The pull request from `dev` to `main` that carries a release in the two-level branch model. |
