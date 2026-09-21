# 0018. A worker stage hands a fact to the next one through the worktree's git directory

Date: 2026-09-21
Status: accepted

## Context
The reviewer panel's verdict is produced in the worker's own context and needed in the pull request stage, which is a forked skill with a fresh context. The only channel was the skill argument, and the pipeline driver invoked the skill without one, so the author described the commits instead. On the run behind pull request #40 the panel ended `code=FIX security=PASS docs=PASS tests=FIX senior=FIX` at the round limit and the pull request said "No written panel summary was handed to this context"; the one page a maintainer reads before merging hid that the panel never passed. Relying on the agent to pass it by hand contradicts [ADR 0002](0002-scripts-do-agents-decide.md), and the architecture had no place for a fact that cannot be re-derived from git or GitHub: a verdict about a diff exists nowhere else.

## Decision
A worker stage may leave a fact for a later stage in `<this worktree's git directory>/worker/`, written and read by a worker script, never by an agent. `wf_state_dir` resolves it with `git rev-parse --git-dir`, not `--git-common-dir` as the standardisation run does: the common directory is shared by every worktree of a repository, and a worker's facts belong to one issue's worktree, so two workers in parallel would otherwise overwrite each other.

The first fact is the panel summary: `panel.sh record` takes the block on stdin, refuses one without a parseable `panel:` line, stores it with the commit it describes and with the draft verdict derived from it (draft as soon as a reviewer's last verdict is not `PASS`), and replaces any earlier record. `panel.sh print` is the pull request stage's brief and is injected by its skill, next to the diff context. The skill argument stays as an override.

What may live there: a fact one stage of this pipeline run produces and a later one needs, small, plain text, owned by one script. Not a cache of anything git or GitHub already answers, and not state that a resumed session must have.

## Consequences
The pull request body carries the panel result verbatim, including a panel that ended on FIX and every `disputed:` line, and a panel that did not pass opens the pull request as a draft that only the maintainer lifts. The pipeline driver reports the same recorded text instead of quoting its memory.

The branch name is no longer the only state contract, so the loss of the directory has to be harmless: it is not versioned, not pushed and gone with the worktree. A stage that finds nothing reads `panel_summary: none recorded`, the verdict falls back to draft, and the pull request says the panel result is unknown rather than inventing one — a degraded run, not a broken one. A re-claim of the same issue starts a new worktree and therefore an empty directory, which is correct: the previous run's verdict said nothing about the new diff. Because the record names the commit it was taken at, a brief after further commits says how many they are, and the reader can see the summary does not describe them.
