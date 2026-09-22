# workflows

Claude Code plugins for high-throughput, low-token, security-conscious development with AI agents. One orchestrator session opens planning sessions that turn ideas into agent-ready issues, and claims those issues into isolated worktree sessions; each worker implements, passes an independent reviewer panel, opens a PR from a fresh context, and drives CI and review comments to green.

```mermaid
flowchart LR
  O[orchestrator<br/>/plan · /claim · /merge · /board] -->|worktree + Herdr pane| PL[planner<br/>/grill · /spec · /tickets · /triage · /accept]
  PL -->|ready-for-agent issues| O
  O -->|worktree + Herdr pane| W[worker<br/>/work]
  W --> I[implement + verify]
  I --> R{reviewer panel<br/>code · security · docs · tests · senior}
  R -->|FIX| I
  R -->|PASS| P[pr-author<br/>fresh context]
  P --> C[CI + Codex review]
  C -->|comments / failures| I
  C -->|green| M[/merge or yolo self-merge/]
```

## Plugins

| Plugin | What it gives you | Runs where |
| --- | --- | --- |
| [orchestrator](plugins/orchestrator/README.md) | `/plan`, `/claim`, `/yolo-claim`, `/merge`, `/board` (with frontier and the specs ready for acceptance), `/abandon`, `/herdr` | main checkout, inside [Herdr](https://herdr.dev) |
| [planner](plugins/planner/README.md) | `/grill`, `/spec`, `/tickets`, `/triage`, `/accept`, `/research`, `/prototype`, `/finish`; writes agent-ready issues and accepts a finished spec, never code | each planning worktree |
| [worker](plugins/worker/README.md) | `/work` pipeline, five read-only reviewer agents, fresh-context PR author, CI and review-thread loop, SessionStart hook that loads and assigns the issue | each issue worktree |
| [repo-standards](plugins/repo-standards/README.md) | `/standardize` (six read-only auditors, one findings report, approval per category), `/apply` (backup tag, skill catalogue, cleanup pull request, issues, GitHub workspace), `/adr`, `/docs-check`; templates for README.md, AGENTS.md, CLAUDE.md, Makefile, the CI job `check`, architecture.md, ADRs, glossary, PR template, Dependabot, settings | any repository |

## Install

Requirements: Claude Code ≥ 2.1.270, `gh` (authenticated), `jq`, git. Herdr for the orchestrator. Optional: `sbx` (Docker Sandboxes) for sandboxed workers, `npx gh-axi` for token-efficient GitHub output, Codex as PR reviewer.

```sh
claude plugin marketplace add CalvinDittkrist/workflows
claude plugin install worker@workflows
claude plugin install planner@workflows
claude plugin install repo-standards@workflows
claude plugin install orchestrator@workflows
```

Per repository, once: run `/repo-standards:standardize` in the repo. It prints the repository's facts, lets six read-only auditors judge it against the [standard](docs/repo-standard.md), shows one findings report and records your approval per category; an empty repository gets a report of create actions only. The audit changes nothing. Then run `/repo-standards:apply`: in an empty repository it first pushes an empty first commit to the default branch; it pushes a protected `pre-standard` tag, lists removed skills in a catalogue issue, opens one cleanup pull request from `chore/standardize` (deletions, baseline files, `.claude/settings.json` with the workflow plugins enabled, the `Makefile` and the CI job `check`) and turns code findings into issues. Merge the pull request, then run `/repo-standards:apply` again: it configures the GitHub workspace and ends with the check. Teammates then only run the install commands above.

Skills also work outside Claude Code: `npx skills add CalvinDittkrist/workflows --skill <name>` (Agent Skills format) or `sbx skills add CalvinDittkrist/workflows` for Docker Sandboxes.

## Daily use

```sh
cd my-repo && claude --agent orchestrator       # inside a Herdr pane
```

```text
/orchestrator:plan add offline mode   # worktree + pane + planner: grill, spec, tickets
/orchestrator:plan 123                # triage or shape an existing issue
/orchestrator:claim 123          # worktree + pane + worker for issue 123
/orchestrator:board              # who is doing what, PR and CI state
/orchestrator:merge 45           # squash-merge, remove worktree, workspace and branch
/orchestrator:yolo-claim 124     # worker merges itself when green
/orchestrator:release v1.2.0     # milestone done: promote dev, tag, release notes, close it
```

The worker in each pane reports at decision points only. Talk to it directly in its pane when it asks something.

## Configuration

All knobs are environment variables, set per repository in `.claude/settings.json` → `env` (the template is in `plugins/repo-standards/templates/settings.json`):

| Variable | Default | Meaning |
| --- | --- | --- |
| `WF_BASE_BRANCH` | remote default branch | base for worktrees and PRs |
| `WF_REVIEWERS` | `code,security,docs,tests,senior` | reviewer panel members |
| `WF_REVIEW_ROUNDS` | `3` | max fix-and-re-review rounds, counted over the rounds the review recorded, so the limit holds across a hand-over |
| `WF_CI_REPAIR_ROUNDS` | `3` | max repair rounds per pull request in the CI stage (a fix for failed checks, a merge of the base after a conflict, a round of `/worker:address-reviews`); counted in the worktree, so the limit holds across a handover |
| `WF_PR_BOT_REVIEWERS` | `chatgpt-codex-connector` | bot logins whose PR review the worker waits for; set to `""` in repositories without a bot reviewer |
| `WF_PR_REVIEW_WAIT` | `1200` | seconds to wait for the bot's review after checks pass; a bot reviews a pull request once, so a review it left on an earlier commit ends the wait and a repair push does not wait again |
| `WF_HANDOFF_TOKENS` | `100000` | context size at which a worker hands the stage it is entering to a fresh context; leave room for one review round under the compact trigger of 250 000 a claim pins, an upper bound rather than an exact size ([ADR 0031](docs/adr/0031-the-workflow-pins-the-size-at-which-a-worker-session-compacts.md), [ADR 0034](docs/adr/0034-the-compact-trigger-is-raised-through-the-window.md)) |
| `WF_CONTEXT_MAX_AGE` | `900` | seconds after which a recorded context size is too old to answer for this turn, and the checkpoint says hand over |
| `WF_HANDOFF_SESSION_MS` | `60000` | how long the handover waits for the pane to report a fresh session |
| `WF_HANDOFF_POLL_SECONDS` | `1` | how often it asks the pane while it waits |
| `WF_WORKER_PERMISSION_MODE` | `auto` | permission mode for worker sessions |
| `WF_DOCS_TIMEOUT` | `30` | seconds one documentation request of `claude-docs.sh` may take (`/worker:docs`) |
| `WF_PLANNER_PERMISSION_MODE` | `auto` | permission mode for planner sessions |
| `WF_PLANNER_LANGUAGE` | empty | conversation language of planner sessions (claude's `language` setting, e.g. `german`); what the planner writes stays English |
| `WF_CLAUDE_ARGS` | empty | extra flags for every worker and planner (`--model sonnet`, `--plugin-dir …`) |
| `WF_PLANNER_CLAUDE_ARGS`, `WF_WORKER_CLAUDE_ARGS` | empty | extra flags for planner or worker sessions only |
| `WF_MODE`, `WF_ISSUE` | set by `/claim` | per-session mode (`manual`/`yolo`) and issue |
| `WF_REVIEW_MANDATE` | set by the factory's follow-up run | names the review the session was started to answer (the time it was submitted), which is what `repair.sh reset` starts the pull request's repair count again on, once per review; unset on every other session |
| `WF_PLAN`, `WF_PLAN_ISSUE` | set by `/plan` | per-session plan slug and, when planning an issue, its number |
| `WF_PROJECT_TEMPLATE` | empty | `<owner>/<number>` of the project the standardisation run copies into a repository without one (`workspace.sh --apply`, not the orchestrator) |

A worker knob can also be set for one claimed session, on the claim itself: `/orchestrator:claim 38 --env WF_HANDOFF_TOKENS=5000`. The argument may be repeated, and it works for a manual, a yolo and a sandboxed claim alike. Accepted names: `WF_REVIEWERS`, `WF_REVIEW_ROUNDS`, `WF_CI_REPAIR_ROUNDS`, `WF_PR_BOT_REVIEWERS`, `WF_PR_REVIEW_WAIT`, `WF_HANDOFF_TOKENS`, `WF_CONTEXT_MAX_AGE`, `WF_HANDOFF_SESSION_MS`, `WF_HANDOFF_POLL_SECONDS`, `WF_DOCS_TIMEOUT` — the knobs of the table above that a worker session reads itself. Any other name, an argument without `NAME=VALUE` and a name given twice are refused before anything is created; an empty value is a setting of its own (`--env WF_PR_BOT_REVIEWERS=`). The value rides in the `env` block of the `--settings` object `claim.sh` builds, beside `WF_MODE` and `WF_ISSUE`, so the session keeps the status line, the compact trigger and the plugin isolation every claim gives it, and it reaches no other session. Claude Code merges an `env` block per variable across the settings levels and takes the command line's value for a key it sets ([settings](https://code.claude.com/docs/en/settings.md)), so the knob wins over the same variable in the repository's `.claude/settings.json` for that session and leaves every other variable of it alone. It holds for the whole run of that pane, across every handover, because a handover clears the session and does not restart the process.

`WF_PLANNER_LANGUAGE` reaches the planner through the `--settings` JSON `plan.sh` builds, so it applies to that one session and writes no settings file. Claude Code takes the **last** `--settings` on the command line and does not merge: a `--settings` of your own in `WF_CLAUDE_ARGS` or `WF_PLANNER_CLAUDE_ARGS` comes after and therefore replaces the whole object, language, plugin switches, the worker's foreground-subagent switch and any knob given with `--env` included. `plan.sh` and `claim.sh` print a `warning:` when they see one, so put those keys into your own JSON. Every other flag in those variables composes normally.

A worker or planner session takes its model from the first of these that is set:

1. `--model` in `WF_CLAUDE_ARGS` or, per session kind, `WF_WORKER_CLAUDE_ARGS` / `WF_PLANNER_CLAUDE_ARGS`
2. the `model` field of the session's agent file (`fable` for `planner`, `opus` for `worker`)
3. `model` in your Claude Code settings

None of those variables reaches the orchestrator: you start it by hand, so it runs on the `sonnet` of its agent file unless your own command line says otherwise.

The planner runs on Fable because planning has the highest leverage in the pipeline: a wrong spec multiplies into every ticket, and a planning session is interactive and small in token volume.

Subagents resolve separately: an agent file that names a model keeps it — the panel's `docs-reviewer` stays on `sonnet` — and only `model: inherit` follows the session. So `WF_CLAUDE_ARGS="--model sonnet"` pins the worker session per repository, not every reviewer. The planner's subagents inherit instead: `spec-checker` is `model: inherit`, and the research subagent is a plain Agent-tool spawn with no agent file, which follows the session unless your settings name a default subagent model. `WF_PLANNER_CLAUDE_ARGS="--model opus"` therefore moves the planner session and the subagents that inherit from it together.

Repositories do not override agents or skills locally: the [repository standard](docs/repo-standard.md) keeps `.claude/` to the settings file, and its check fails on local skills, agents, commands and rules. Tune a repository with the `WF_*` variables and its `AGENTS.md`.

## Design

- [Vision](docs/vision.md): why the repository exists and what it optimises for
- [Architecture](docs/architecture.md) and [ADRs](docs/adr/README.md)
- [The local workflow, step by step](docs/local-workflow.md)
- [Security and sandboxing](docs/security.md)
- [Factory host runbook](docs/factory-runbook.md): from an empty Linux machine to a running factory, and its upkeep
- [Token budget: what loads when](docs/token-budget.md)
- [Repository standard](docs/repo-standard.md)

## Develop

```sh
make check                                        # the gate: shellcheck, plugin validate --strict, standard check, unit tests, Go vet/staticcheck/tests, the dashboard's lint, build and browser test
claude --plugin-dir plugins/worker                # try a plugin in a session without installing it
scripts/dev-orchestrator.sh                       # orchestrator from the checkout, inside a Herdr pane
scripts/context-report.py                         # diagnostic: context and tool mix of finished worker sessions
make ui                                           # build the dashboard the factory binary embeds (a fresh clone has only a placeholder)
go -C factory run . -fake -config factory.json    # the factory on a canned queue: no tokens, no git, no GitHub
npm --prefix factory/ui run dev                   # the dashboard with hot reload, against a factory started beside it
```

`factory/factory.example.json` is a host's configuration: it runs paused, keeps its clones in `/var/lib/factory` and starts as it is. A host that should work its line sets `"paused": false`, and a configuration that does not name `paused` at all is paused, so an unattended line is always something an operator wrote down; `-paused` on the command line pauses a factory whose configuration says otherwise and never unpauses one, so the operator's brake is always the stronger of the two. A run on a developer's machine wants a `factory.json` of its own — a data directory that machine can write, and no `"quota_axi"`, since the check would read that machine's own Claude quota — and `-fake` to work a canned queue without tokens, git or GitHub. A connected repository is `"owner/name"`, or `{"name": "owner/name", "base": "dev"}` when this host branches its runs off something other than the base the repository names for itself (its `WF_BASE_BRANCH`, and its default branch when it names none). `"notify"` is the GitHub logins the factory tells how a run ended, written without the `@`: a run that ends `ready` asks them for a review of its pull request, and one that waits for a person comments on the issue and mentions them. A configuration that names none notifies nobody and says so when it starts. `"quota_axi"` is the absolute path of the [quota-axi](https://github.com/kunchenguid/quota-axi) installed on the host in a pinned version (`npm install -g quota-axi@0.1.50`), and with it the factory checks the Claude quota it shares with the maintainer before every run. Below `"quota_minimum"` (default 12 %) of the all-models scope or the worker's model scope, nothing starts, and the dashboard says the factory waits for quota and until when. After the reset the check runs again. A check that cannot answer starts the run with a warning, and a session that ends in an error while that quota is used up ends as `quota` and is resumed after the reset, once in a row ([ADR 0037](docs/adr/0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md)). Without `"quota_axi"` the check is off, and the factory never fetches the tool from npm.

`factory/` is the factory: a Go service, not a plugin, that works the issues routed to it unattended on a host of its own, as a second driver over the same worker pipeline ([ADR 0022](docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)). It is steered on GitHub and shows what it did over a read-only HTTP interface, with a dashboard built into the binary that reads those endpoints and writes nothing ([ADR 0033](docs/adr/0033-the-dashboard-is-built-into-the-factory-binary.md)). It takes the head of its line by creating that issue's branch on GitHub — the one act with a single winner ([ADR 0024](docs/adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md)) — assigns the issue to the user it is logged in as, makes a worktree in its own clone and runs the worker pipeline there in a headless session. Before each run it updates the `workflows` marketplace and the `worker` plugin with Claude Code's own plugin commands and writes down the versions of the worker plugin, Claude Code and the factory the run then starts with, so a merged fix reaches the host with its next run and every run can be read back to what made it; it updates neither Claude Code nor itself — those are the operator's — and an update that fails is a warning on the run, which goes on with what is installed ([ADR 0036](docs/adr/0036-the-factory-updates-the-worker-plugin-and-nothing-else.md)). That session is the host's own `claude`, so the host needs the `worker` plugin installed and a `gh` logged in as the machine user; a host that works from a checkout instead points `"worker_args"` at it (`["--plugin-dir", "/path/to/workflows/plugins/worker"]`), which is how every extra argument reaches the worker's command line — such a host loads its worker from that directory, which takes precedence over an installed plugin of the same name, so no run of it records a worker version and every one carries a warning naming the directory. It adds to that line and never replaces what the factory decided: a `worker_args` carrying `--settings`, `--agent`, `--permission-mode`, `--output-format` or `-p` is refused when the factory starts, because those are the run itself — its mode, its issue, its base branch and the stream the factory reads it from. Developing the dashboard needs Node: `make check` builds it, installs its dependencies with `npm ci` and the Chromium its browser test drives, and names the fix when npm itself is missing.

`dev-orchestrator.sh` points `WF_PLANNER_CLAUDE_ARGS` and `WF_WORKER_CLAUDE_ARGS` at the checkout's plugins, so the sessions the orchestrator opens use them too. Without that (or the plugins installed), a started session exits with `--agent 'planner' not found`; `plan.sh` and `claim.sh` detect that, remove the worktree again and print the fix. The same rollback runs when Herdr refuses the start itself (its error is printed as is) or when the pane is back at a shell prompt. Herdr agent names are derived from the branch and cut to its 32-character limit.

Tests run the real scripts against `gh` and `herdr` shims (`tests/shims/`). Plugins are self-contained; bump `version` in a plugin's manifest and run `scripts/release.sh <plugin> --push` to tag a release. The factory is released by the same script: bump `factory/VERSION`, and from an up-to-date `main` run `scripts/release.sh factory --push`, and the `factory/v<version>` tag makes CI attach static linux binaries for amd64 and arm64 — the dashboard inside, no cgo — with their checksums to a GitHub release, so a factory host needs neither Go nor Node nor a checkout.

MIT licensed.
