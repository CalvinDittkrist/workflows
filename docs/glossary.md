# Glossary

| Term | Meaning |
| --- | --- |
| standard | The written baseline a repository is checked against: [repo-standard.md](repo-standard.md). |
| profile | Visibility plus branch model (`main` alone, or `dev` plus `main`), derived from GitHub, never configured. |
| gate | `make check`, the one command that must pass before a merge; CI runs it in the job named `check`. |
| auditor | A read-only subagent that judges one area of a repository during standardisation and returns findings. |
| facts | The compact `key: value` block `facts.sh` prints about a repository (profile, languages, commands, CI jobs, agent configuration, baseline files, file statistics); every auditor gets it instead of exploring. |
| finding | One proposed action in a fixed format, one line: `finding: <category> \| <target> \| <action> \| <reason> \| <confidence>`. Category is the auditor's area (`files`, `agent-config`, `docs`, `tests-ci`, `workspace`, `security`); action is delete, replace or create (the run works through them one by one, as listed), issue (it becomes an agent-ready issue) or configure (it describes a GitHub setting; approving the category applies the whole workspace difference, [ADR 0016](adr/0016-approval-is-per-category-and-scripts-own-what-they-apply.md)); confidence is high, medium or low. |
| cleanup pull request | The one pull request from `chore/standardize` that carries a standardisation run's deletions and new baseline files; its description lists how to restore each removed path. |
| catalogue issue | The issue, labelled `skill-candidate`, that lists the skills standardisation removed and how to restore each from the `pre-standard` tag. |
| drift | A difference between a repository's GitHub workspace and the standard; `workspace.sh` prints one `diff:` line per difference. |
| snapshot | The JSON file `workspace.sh --apply` writes before its first change: the previous value of everything it changes, for posting to an issue and undoing by hand. |
| conversation language | The language the planner talks to the user in, set with `WF_PLANNER_LANGUAGE`; issues, comments and glossary terms stay English. |
| promotion | The pull request from `dev` to `main` that carries a release in the two-level branch model. |
| acceptance | The check of a whole spec against the code on the base branch after its tickets are closed; it ends with gap tickets or with the spec closed ([ADR 0015](adr/0015-a-spec-with-tickets-is-closed-by-an-acceptance.md)). |
| foreground subagent | A subagent whose report comes back as the result of the Agent call, because the session has background tasks disabled. Worker sessions run this way so the agent never waits in a loop; `facts.sh` prints which shape a session has as `subagents:` ([ADR 0017](adr/0017-worker-subagents-run-in-the-foreground.md)). |
| context report | The maintainer's diagnostic over finished worker sessions, `scripts/context-report.py`: one line per session with the context at the stage boundaries, the peak, the tool mix and the sleep calls. Reads Claude Code's internal transcript format; never an input to the pipeline. |
| spec checker | The read-only subagent that judges each checkable statement of a spec during an acceptance. |
| item | One checkable statement of a spec with its verdict, in the fixed format `item: <section> \| <statement> \| <verdict> \| <evidence> \| <confidence>`. |
| accepted deviation | A difference between spec and code the maintainer keeps, recorded as a comment on the spec, by someone with write access, that opens with `> Accepted deviation (spec acceptance).`; where write access cannot be read the author association decides, with a warning; an acceptance does not report it again. |
| gap ticket | A `ready-for-agent` sub-issue an acceptance creates for an item that is not met. |
| routing label | The label `factory` of the label vocabulary. It hands an issue to the factory host, which claims it on GitHub; a local claim refuses an issue that carries it, and `--force` is the exception. |
| label vocabulary | The fixed set of GitHub labels the workflow uses, each with a name, a colour and a description. Every plugin that creates labels defines it itself; a test in `tests/test_plugins.py` keeps the copies identical, `skill-candidate` being the one label only `repo-standards` creates. |
| panel summary | The block the review stage records at the end of a round loop (`review_rounds`, `panel`, `fixed`, `disputed`) and the pull request stage reads, with the commit it was recorded at and the draft verdict derived from its `panel:` line: draft as soon as a reviewer's last verdict is not PASS ([ADR 0018](adr/0018-worker-stages-hand-facts-over-through-the-worktree-git-dir.md)). |
| gate record | The recorded result of one gate run, written by the worker's `gate.sh` and read by the review and pull request stages: the commit, whether the working tree was dirty, the exit status, the start time, the duration and the tail of the output, with the full output in a file beside it. It answers for the commit it was taken at and for no other, so a stale or dirty record reads as none ([ADR 0019](adr/0019-the-gate-runs-once-per-review-round.md)). |
