# Factory host runbook

From an empty Linux machine to a running factory, and its upkeep. What the factory does is in the [architecture](architecture.md) (data flow, step 7); this page is what an operator does. It describes factory 0.1.0.

The host is a machine of its own, a Raspberry Pi or a small server, amd64 or arm64. The commands assume a Debian-like system with systemd; the names used below are the user `factory`, the data directory `/var/lib/factory` and the configuration `/etc/factory/factory.json`.

## Identity
The host is the factory's isolation boundary ([ADR 0027](adr/0027-the-factorys-isolation-boundary-is-the-host.md)). Its workers run unattended in the auto permission mode, so anything on the host is theirs to read and run: keep nothing on it that is not already on GitHub, and give it no credential of yours beyond the Claude subscription the workers run on. No SSH key of yours, no Claude Code login of yours and no `gh` login of yours go on the host.

1. **A GitHub machine user.** Create a GitHub account for the host, such as `yourname-factory`. Its claims, commits, pull requests and comments are its own, which is what lets you review a pull request you did not write. Invite it as a collaborator with the Write role to each repository the factory is to work, and to nothing else: it creates branches, assigns issues to itself, pushes and opens pull requests.
2. **A token limited to the connected repositories.** Sign in as the machine user and create a personal access token. For repositories an organisation owns, a fine-grained token with that organisation as the resource owner and *Only select repositories* limits it to them; give it Contents, Issues and Pull requests read and write, and Metadata read. A fine-grained token cannot reach the repositories of a personal account the machine user is only a collaborator on; there a classic token with the scopes `repo`, `read:org` and `workflow` is the one that works, and the repositories it reaches are the ones the machine user was invited to — which is why step 1 invites it to nothing else. `workflow` lets a worker push a change to a repository's `.github/workflows`, which a change to CI needs.
3. **The `gh` login on the host**, as the user `factory` (see [Installation](#installation)):

   ```sh
   gh auth login --with-token < token.txt   # the token from step 2, then delete the file
   gh auth setup-git                        # git pushes with the same token
   gh auth status                           # names the machine user
   git config --global user.name  "yourname-factory"
   git config --global user.email "<id>+yourname-factory@users.noreply.github.com"
   ```

   The factory reads and writes GitHub through this `gh` alone and assigns every issue it claims to the user it is logged in as. It has no login of its own ([security](security.md#the-factory)).

## Installation
Run the following as root unless it says otherwise.

1. **The user and its directories.**

   ```sh
   apt-get install -y git gh jq make curl
   useradd --create-home --shell /bin/bash factory
   install -d -o factory -g factory -m 0700 /var/lib/factory
   install -d -m 0755 /etc/factory
   ```

   A worker runs each connected repository's `make check`, so install the toolchain those gates need as well (for this repository: shellcheck, Go, Node, Python).
2. **Claude Code**, as the user `factory` (`sudo -iu factory`), with the native installer, which needs no Node and puts `claude` in `~/.local/bin` ([setup](https://code.claude.com/docs/en/setup.md)):

   ```sh
   curl -fsSL https://claude.ai/install.sh | bash
   ```

   Updates are yours, not the factory's ([ADR 0036](adr/0036-the-factory-updates-the-worker-plugin-and-nothing-else.md)), so switch off the background updater in `~/.claude/settings.json`; `claude update` still works ([setup](https://code.claude.com/docs/en/setup.md)):

   ```json
   { "env": { "DISABLE_AUTOUPDATER": "1" } }
   ```

3. **The headless login**, as the user `factory`: run `claude`, type `/login` and sign in with the subscription the workers run on. Over SSH the browser cannot reach the host, so Claude Code shows a URL to open on another machine and a code to paste back ([authentication](https://code.claude.com/docs/en/authentication.md)). The credential lands in `~/.claude/.credentials.json`, mode 0600, where the worker sessions refresh it and the quota check reads it. `claude setup-token` with `CLAUDE_CODE_OAUTH_TOKEN` also runs a worker, but it writes no credentials file, so quota-axi has nothing to read and every run carries a warning that the check could not answer.
4. **The marketplace and the worker plugin**, as the user `factory`, in the user scope ([discover plugins](https://code.claude.com/docs/en/discover-plugins.md)):

   ```sh
   claude plugin marketplace add CalvinDittkrist/workflows
   claude plugin install worker@workflows
   ```

   The factory updates both before every run and records the version a run was made with; install nothing else — the planner and the orchestrator are switched off in every worker session.
5. **The factory binary** from a release. The tag `factory/v<version>` carries `factory-linux-amd64`, `factory-linux-arm64` and `checksums.txt`: static binaries with the dashboard inside them, so the host needs no Go, no Node and no checkout for the factory itself.

   ```sh
   version=0.1.0
   arch=arm64   # or amd64: dpkg --print-architecture
   cd "$(mktemp -d)"
   gh release download "factory/v$version" -R CalvinDittkrist/workflows -p "factory-linux-$arch" -p checksums.txt
   sha256sum --check --ignore-missing checksums.txt   # must print: factory-linux-<arch>: OK
   install -m 0755 "factory-linux-$arch" /usr/local/bin/factory
   factory -version                                   # factory 0.1.0
   ```

   Install nothing that `sha256sum` did not answer `OK` for.
6. **quota-axi** in a pinned version. The factory reads the output of quota-axi 0.1.50 ([ADR 0037](adr/0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md)), which needs Node 22.19 or later (`engines` of the package). Install Node 22 from your distribution or NodeSource, check `node --version`, then:

   ```sh
   npm install -g quota-axi@0.1.50
   command -v quota-axi   # /usr/local/bin/quota-axi, the path for quota_axi below
   ```

   The factory runs it by that absolute path and never through `npx`, so nothing is fetched from npm at run time. The script starts with `#!/usr/bin/env node`, so `node` has to be on the service's `PATH` (see [Service](#service)).

## Configuration
The factory is configured by one JSON file and nothing else: no environment variable, and nothing is written back. It refuses to start on an unknown field, a value it cannot use, or a second JSON value in the file, and the error names the fix.

| Field | Default | Meaning |
| --- | --- | --- |
| `listen` | `127.0.0.1:7341` | The address of the HTTP interface, as `host:port`. A wildcard address (`0.0.0.0`, `::`, an empty host) is refused, and so is a name that resolves to one. |
| `label` | `factory` | The routing label that puts an issue in the line. |
| `deadline` | `120m` | How long one run may take, as a Go duration (`90m`, `2h30m`); a run past it is ended with its process group. |
| `poll` | `60s` | How often GitHub is asked for the line, as a Go duration. |
| `data_dir` | none, required | Where the clones, the run records and the locks live ([The data directory](#the-data-directory)). |
| `worker_args` | `[]` | Arguments added to the worker's `claude` command line, such as `["--model", "opus"]`. `--settings`, `--agent`, `--permission-mode`, `--output-format`, `-p` and `--print` are refused: the factory sets them. A `--plugin-dir` here loads the worker from that directory instead of the installed plugin; no run then records a worker version and every one carries a warning. |
| `paused` | `true` | A paused factory shows the line and claims, resumes and writes nothing. A file that does not name `paused` is paused, so an unattended line is always something you wrote down. |
| `notify` | `[]` | GitHub logins, without the `@`, that are asked for a review when a run ends `ready` and mentioned in a comment on the issue when it waits for a person. Empty: nobody is notified, and the log says so on start. |
| `repositories` | none, at least one | The connected repositories, each `"owner/name"` or `{"name": "owner/name", "base": "dev"}` when this host branches off something other than the base the repository names (its `WF_BASE_BRANCH`, else its default branch). |
| `quota_axi` | none: the check is off | The absolute path of the quota-axi installed above. |
| `quota_minimum` | `12` | The percentage of the all-models scope or the worker's model scope below which no run starts; `0` never waits. |

The command line has three flags for the service: `-config <file>` (default `factory.json` in the working directory), `-paused`, which pauses a factory whose file says otherwise and never unpauses one, and `-version`. `-fake` works a canned queue with scripted workers and is for development only.

A complete configuration, written to `/etc/factory/factory.json` (root owns it, mode 0644; it holds no secret):

```json
{
  "listen": "127.0.0.1:7341",
  "label": "factory",
  "deadline": "120m",
  "poll": "60s",
  "data_dir": "/var/lib/factory",
  "worker_args": [],
  "paused": false,
  "notify": ["yourname"],
  "quota_axi": "/usr/local/bin/quota-axi",
  "quota_minimum": 12,
  "repositories": [
    "yourname/service",
    {"name": "yourname/app", "base": "dev"}
  ]
}
```

Start with `"paused": true` and switch it off once the dashboard shows the line you expect.

## Service
A systemd unit, `/etc/systemd/system/factory.service`:

```ini
[Unit]
Description=factory: works the issues routed to it
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=factory
Group=factory
WorkingDirectory=/var/lib/factory
# claude lives in ~/.local/bin; node has to be here for quota-axi.
Environment=PATH=/home/factory/.local/bin:/usr/local/bin:/usr/bin:/bin
ExecStart=/usr/local/bin/factory -config /etc/factory/factory.json
Restart=on-failure
RestartSec=30s
# SIGTERM goes to the factory alone, which ends its worker's process group and records the run as
# interrupted; whatever is left of the service after it exits is killed.
KillMode=mixed
TimeoutStopSec=60s

[Install]
WantedBy=multi-user.target
```

```sh
systemctl daemon-reload
systemctl enable --now factory
journalctl -u factory -f     # factory 0.1.0 on http://127.0.0.1:7341 (fake=false paused=false ...)
```

What the settings rest on:

- **A user of its own.** `User=factory` is the user that holds the `gh` login, the Claude Code login and the plugin; the service runs nothing as root.
- **Restart on failure.** A factory that exits with an error is started again after 30 seconds. A configuration it refuses exits with an error as well, so a unit that keeps restarting is read in the journal, where the first `error:` line names the fix.
- **Time to end the worker on stop.** On SIGTERM the factory sends SIGTERM to its worker's process group and kills what is left of the group once the worker has exited, or after 10 seconds if it has not, reads the worker's last output for up to 3 seconds and shuts its interface down within 5. `KillMode=mixed` sends the first signal to the factory alone, so the worker is ended by the factory, which records it, and not at the same moment by systemd; `TimeoutStopSec=60s` leaves room for all of it before systemd kills what is left, including a process a worker started outside its group ([ADR 0027](adr/0027-the-factorys-isolation-boundary-is-the-host.md)). The run is recorded as interrupted, and the next start resumes it once by itself.
- **One factory per host.** A second one fails on start, on the address when it shares it and on the data directory's lock when it does not.

## Access
The interface binds to `127.0.0.1` and has no login, so it is published to the tailnet and nowhere else. On the host, with Tailscale joined to your tailnet:

```sh
tailscale serve --bg 7341
```

The dashboard is then at `https://<host>.<tailnet>.ts.net/` for the devices of your tailnet. Never use `tailscale funnel` for it, which publishes to the internet, and never set `listen` to an address other networks reach. The interface is read-only — `/api/status`, `/api/repositories`, `/api/line`, `/api/runs/{id}` and the dashboard under `/` — but it shows issue titles, repository names and a worker's tool calls, with the content of private repositories in them ([security](security.md#the-factory)).

## Operation on GitHub
The factory is steered on GitHub alone; its interface never writes ([ADR 0023](adr/0023-github-is-the-only-control-surface-of-the-factory.md)).

| You want to | On GitHub |
| --- | --- |
| **Route** an issue | Give it the routing label beside `ready-for-agent`, with no assignee and no open blocker; the planner does this when you route a ticket ([ADR 0021](adr/0021-routing-is-decided-in-the-planner-and-never-stands-alone.md)). The line is ordered by the time the label was set, oldest first, and work the factory already holds comes first ([ADR 0025](adr/0025-one-queue-one-worker-work-in-progress-first.md)). |
| **Release** an issue that waits for you | Remove the machine user as its assignee. An issue whose run ended `blocked`, `failed`, on the deadline or interrupted twice is held — branch, worktree and assignee stay — and is out of the line until you do; the factory then assigns itself again and resumes it in the same worktree. When `notify` names somebody, the comment the factory leaves on the issue says so. |
| **Cancel** a run or let an issue go | Remove the routing label, or close the issue. A run that is going is ended within one poll. The worktree's commits are pushed before the worktree is removed, the branch is deleted only when it carries nothing beyond its base, and no record is touched ([ADR 0026](adr/0026-the-factory-never-deletes-work-on-its-own.md)). |
| **Request changes** | Submit a review that requests changes on the run's pull request, as somebody with write access. The factory runs the worker again in the same worktree to answer it; one review is one run. |
| **Merge** | Merge the pull request yourself: the factory never merges. Merged or closed, it lets the issue go the same way as a cancel. |

The logins in `notify` are asked for a review when a run ends `ready`, and mentioned on the issue when a run waits for a person.

## Upkeep

### Updating
The factory updates the `workflows` marketplace and the `worker` plugin before every run itself. Claude Code, the factory binary and quota-axi are yours ([ADR 0036](adr/0036-the-factory-updates-the-worker-plugin-and-nothing-else.md)). Update them between runs: stopping the factory interrupts the run that is going, which is resumed once by itself, and a second interruption of the same issue waits for you. `curl -s http://127.0.0.1:7341/api/line | jq '.now | length'` prints `0` when nothing runs; pause the factory first (below) to keep it that way.

- **Claude Code**, as the user `factory`: `claude update`, then `claude --version`. Every run records the version it was made with.
- **The factory binary**: download and check it as in [Installation](#installation), then `systemctl stop factory`, `install -m 0755 factory-linux-$arch /usr/local/bin/factory`, `systemctl start factory`, and look for the new version in the journal's first line.
- **quota-axi**: the factory reads the output of the pinned version, so move the pin only after reading the new version's changelog: `npm install -g quota-axi@<version>`, restart nothing. If the factory cannot read its answer, every run carries a warning that the check could not answer and starts regardless ([ADR 0028](adr/0028-the-quota-check-is-a-courtesy-not-a-guard.md)); install the pinned version again.

### Pausing
Set `"paused": true` in the configuration and `systemctl restart factory`, or leave the file alone and add `-paused` to `ExecStart` for a while (`systemctl edit factory`). A paused factory shows the line, polls GitHub and writes nothing there: it claims, resumes, notifies and lets go of nothing, and the dashboard says it is paused. The restart interrupts a run that is going, so pause between runs. Unpause the same way.

### The data directory
Everything the factory knows about itself is in `data_dir`:

| Path | What it is | Delete by hand? |
| --- | --- | --- |
| `factory.lock` | The lock that keeps a second factory off this directory; the kernel releases it when the process is gone. | Harmless, and pointless: it is taken again on start. |
| `run-<n>.json` | The record of run `n`: issue, branch, worktree, outcome, versions, warnings, what it owes a notification. | No. The records are how a factory knows after a restart what it holds, which resume it has spent and what it still owes the maintainer. Deleting one makes it forget an issue it holds. |
| `run-<n>.events.jsonl` | The append-only event log of run `n`, which the dashboard shows. | Only with its record, and only for an issue the factory no longer holds. |
| `run-<n>.lock` | The lock the worker of run `n` held; it tells a start whether that worker is still alive. | With the factory stopped, for a run whose record is no longer running. |
| `repos/<owner>/<name>/` | The clone of a connected repository, in lower case, with the worktrees of the issues the factory holds under `.claude/worktrees/`. | Not while a worktree in it holds commits that are not pushed. The clone of a repository you disconnected may go once its worktrees are pushed; a clone that is missing is made again on the next start. |
| `repos/<owner>/.<name>.cloning-*` | A clone that was cut off. | Yes; the next start sweeps it too. |

The factory itself never deletes a record or a log, so the directory grows by one record and one log per run. Back it up if you want the history kept; a host that loses it knows nothing of the work it held, which is still on GitHub on the branches every removed worktree was pushed to.
