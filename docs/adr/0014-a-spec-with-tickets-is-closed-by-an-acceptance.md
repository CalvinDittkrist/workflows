# 0014. A spec with tickets is closed by an acceptance

Date: 2026-09-20
Status: accepted

## Context
A spec that was cut into tickets stayed open after its last ticket closed, and nothing compared the whole spec with the code: every pull request was reviewed against one ticket only. With #3 a real deviation survived five merged tickets, and the spec itself was then claimed by a worker that invented its own acceptance criteria and shipped 400 lines across two plugins in one pull request (#17). Spec: #19.

## Decision
A spec with tickets is closed by an acceptance in the planner, never by a worker. The board lists an open `spec` issue with native sub-issues and none of them open as ready for acceptance, derived from GitHub on every run. The maintainer opens a planning session on the spec; `accept-facts.sh` prints the facts (the spec, its tickets with the merged pull requests that closed them, the files those changed, the deviations accepted earlier), a read-only checker with a fresh context judges each checkable statement of the spec against the code on the base branch, and the maintainer decides per gap: a `ready-for-agent` sub-issue, an accepted deviation recorded as a comment on the spec, or no finding. Only when nothing is left open does the stage close the spec with a comment that records what was checked.

## Consequences
A spec stays a truthful document, and its gaps go through the normal pipeline as tickets instead of one bulk pull request. The verdict comes from a fresh context that never wrote the code, and an accepted deviation is not reported again because the facts carry it. The maintainer must run the acceptance, which costs one session per spec, and the board and the acceptance both need native sub-issues (the facts script takes ticket numbers where a repository lacks them). Letting a worker close the spec, and closing it automatically when the last ticket merges, were rejected: both drop the comparison with the whole spec.
