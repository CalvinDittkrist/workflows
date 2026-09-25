# Factory host runbook

From an empty Linux machine to a running factory, and its upkeep. What the factory does is in the [architecture](architecture.md) (data flow, step 8); this page is what an operator does. It describes factory 0.1.0.

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
   apt-get install -y git jq make curl xz-utils
   useradd --create-home --shell /bin/bash factory
   install -d -o factory -g factory -m 0700 /var/lib/factory
   install -d -m 0755 /etc/factory
   ```

   `gh` comes from GitHub's own apt repository ([install_linux.md](https://github.com/cli/cli/blob/trunk/docs/install_linux.md)), not from the distribution. Debian trixie ships 2.46.0, which still asks for Projects (classic) in `gh pr edit`. GitHub now answers that query with the error `Projects (classic) is being deprecated in favor of the new Projects experience`, so the factory's `gh pr edit --add-reviewer` fails and a run that ends `ready` asks nobody for a review. The keyring is installed only when its checksum matches the one that page lists; apt upgrades the package from that repository along with the rest of the system.

   ```sh
   key=/etc/apt/keyrings/githubcli-archive-keyring.gpg
   cd "$(mktemp -d)"
   curl -fsSLO https://cli.github.com/packages/githubcli-archive-keyring.gpg
   echo "6084d5d7bd8e288441e0e94fc6275570895da18e6751f70f057485dc2d1a811b  githubcli-archive-keyring.gpg" | sha256sum --check &&
     install -D -m 0644 githubcli-archive-keyring.gpg "$key" &&
     echo "deb [arch=$(dpkg --print-architecture) signed-by=$key] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list &&
     apt-get update && apt-get install -y gh
   gh --version                   # gh version 2.<minor>.<patch>, newer than 2.46.0
   ```

2. **The gate's tools.** The factory runs each connected repository's `make check`, so the host needs the tools that gate runs, at the versions the repository's CI pins. Read them from its CI workflow (for this repository `.github/workflows/ci.yml`) and from the error lines of its `Makefile`, not from the distribution: a distribution's version finds other things than CI's, and the gate then fails on the host on files the change never touched, which no worker can fix. For this repository that is shellcheck 0.11.0, Go 1.26, Node 24 and staticcheck 2026.2.1; Python is the distribution's `python3`, which CI does not pin. One host has one version of each tool, so when two connected repositories pin different versions of one, the host runs the newest pin and the repository that is behind moves its CI to it. The gate's tests call two more tools that a minimal Debian image lacks and CI's runner has: a C compiler (`build-essential`), because `go test -race` builds with cgo, and `file`, with which the release test checks that the factory's binaries are static.

   ```sh
   cd "$(mktemp -d)"
   arch=aarch64   # or x86_64: uname -m
   sum=12b331c1d2db6b9eb13cfca64306b1b157a86eb69db83023e261eaa7e7c14588   # x86_64: 8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198
   curl -fsSLO "https://github.com/koalaman/shellcheck/releases/download/v0.11.0/shellcheck-v0.11.0.linux.$arch.tar.xz"
   echo "$sum  shellcheck-v0.11.0.linux.$arch.tar.xz" | sha256sum --check &&
     tar -xJf "shellcheck-v0.11.0.linux.$arch.tar.xz" &&
     install -m 0755 shellcheck-v0.11.0/shellcheck /usr/local/bin/shellcheck
   shellcheck --version           # version: 0.11.0

   goarch=arm64   # or amd64: dpkg --print-architecture
   go=$(curl -fsSL 'https://go.dev/dl/?mode=json' | jq -r '[.[].version | select(startswith("go1.26."))][0]')
   curl -fsSLO "https://go.dev/dl/$go.linux-$goarch.tar.gz"
   curl -fsSL 'https://go.dev/dl/?mode=json' | jq -r --arg f "$go.linux-$goarch.tar.gz" '.[].files[] | select(.filename == $f) | "\(.sha256)  \(.filename)"' | sha256sum --check &&
     rm -rf /usr/local/go && tar -xzf "$go.linux-$goarch.tar.gz" -C /usr/local
   /usr/local/go/bin/go version   # go version go1.26.<patch> linux/<arch>

   key=/etc/apt/keyrings/nodesource.asc
   curl -fsSLO https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key
   echo "b42e0321dabdc24e892115da705cf061167eac12a317f23d329862d0aa0a271d  nodesource-repo.gpg.key" | sha256sum --check &&
     install -D -m 0644 nodesource-repo.gpg.key "$key" &&
     echo "deb [arch=$(dpkg --print-architecture) signed-by=$key] https://deb.nodesource.com/node_24.x nodistro main" > /etc/apt/sources.list.d/nodesource.list &&
     apt-get update && apt-get install -y nodejs python3 build-essential file
   node --version                 # v24.<minor>.<patch>
   ```

   Each archive and the NodeSource key are installed only when `sha256sum --check` answers `OK`, which is why those lines are chained with `&&`; a pasted block runs on past a failed line. Node comes from NodeSource's apt repository for the major version CI pins, `node_24.x`, the way `gh` comes from GitHub's. NodeSource publishes no checksum for its key, so the one above is the sha256 of the key it served on 2026-09-23, whose fingerprint is `6F71 F525 2828 41EE DAF8 51B4 2F59 B5F9 9B1B E0B4`; when NodeSource rotates the key, `gpg --show-keys nodesource-repo.gpg.key` shows the new fingerprint to check against theirs before the checksum is replaced. apt upgrades Node within that major version; moving to another major version is a new source line. `/usr/local/go/bin` is not on a login's `PATH`, so the service puts it there (see [Service](#service)). staticcheck is built with that Go as the user `factory`, into `~/go/bin`, where the `Makefile` looks for it:

   ```sh
   sudo -iu factory /usr/local/go/bin/go install honnef.co/go/tools/cmd/staticcheck@2026.2.1
   ```

   The dashboard's browser test installs its own Chromium on the first `make check`; the system libraries that browser needs are installed once as root, with the Playwright version that `factory/ui/package-lock.json` names, from a directory of root's own:

   ```sh
   cd "$(mktemp -d -p /root)" && npx --yes playwright@1.63.0 install-deps chromium
   ```

   Run it only where no parent directory is writable by anyone but root: npx runs the `node_modules/playwright` of the nearest parent that has one, and the workers write to `/tmp` and to the clones under `/var/lib/factory`, so from there root would run what a worker left. When CI moves a pin, move the host's in the same way before the next run.

   The gate's length is what tells you whether a host is fast enough. The factory runs it itself, once in the gate stage and again on the final head when the review's fixes moved the branch, plus once more for every fix session a failing gate takes ([The gate stage](#the-gate-stage)), and each run may take `gate.timeout` (default `45m`) before it is ended and counts as a failure. On a Raspberry Pi 4 this repository's full gate takes an estimated 12 to 15 minutes: in run 4 of #106 (2026-09-22) the first four targets alone took 559 s, and the next attempt was cut off at 600 s in `go test -race`, the ceiling of the Bash tool call the worker ran the gate in then. A slow host now costs time and blocks a run only past `gate.timeout`. To judge a host, time the gate in a clone of each connected repository as the user `factory`, with the `PATH` the service gives it: `time env PATH="/usr/local/go/bin:$PATH" make check`.
3. **Claude Code**, as the user `factory` (`sudo -iu factory`), with the native installer, which needs no Node and puts `claude` in `~/.local/bin` ([setup](https://code.claude.com/docs/en/setup.md)):

   ```sh
   curl -fsSL https://claude.ai/install.sh | bash
   ```

   Updates are yours, not the factory's ([ADR 0042](adr/0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md)), so switch off the background updater in `~/.claude/settings.json`; `claude update` still works ([setup](https://code.claude.com/docs/en/setup.md)):

   ```json
   { "env": { "DISABLE_AUTOUPDATER": "1" } }
   ```

4. **The headless login**, as the user `factory`: run `claude`, type `/login` and sign in with the subscription the workers run on. Over SSH the browser cannot reach the host, so Claude Code shows a URL to open on another machine and a code to paste back ([authentication](https://code.claude.com/docs/en/authentication.md)). The credential lands in `~/.claude/.credentials.json`, mode 0600, where the worker sessions renew it, and so does the quota check when it finds it expired ([ADR 0037](adr/0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md)). `claude setup-token` with `CLAUDE_CODE_OAUTH_TOKEN` also runs a worker, but it writes no credentials file, so quota-axi has nothing to read and every run carries a warning that the check could not answer.
5. **No plugin.** Every session the factory starts runs on the factory's own prompts, compiled into the binary, and switches the `worker`, `planner` and `orchestrator` plugins of the `workflows` marketplace off, so the host needs Claude Code, `git`, `gh` and the factory binary, beside the tools of the gates above, and no plugin of this repository ([ADR 0042](adr/0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md)). A host that carries the plugin from an earlier factory moves over in [Moving a host off the plugin](#moving-a-host-off-the-plugin).
6. **The factory binary** from a release. The tag `factory/v<version>` carries `factory-linux-amd64`, `factory-linux-arm64` and `checksums.txt`: static binaries with the dashboard inside them, so the host needs no Go, no Node and no checkout for the factory itself.

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
7. **quota-axi** in a pinned version. The factory reads the output of quota-axi 0.1.49 ([ADR 0037](adr/0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md)), which needs Node 22.19 or later (`engines` of the package). Node 24 from NodeSource, installed with the gate's tools, is that. Do not move the pin to 0.1.50: that version reads the `utilization` of Claude's usage endpoint, which is the percentage used, as the percentage remaining, so the check waits while the window is fresh and starts runs when it is nearly used up. As root:

   ```sh
   npm install -g quota-axi@0.1.49
   command -v quota-axi   # /usr/bin/quota-axi, the path for quota_axi below
   ```

   NodeSource's npm has the prefix `/usr` (`npm prefix -g`), so its global packages land in `/usr/lib/node_modules` with their commands in `/usr/bin`, and the path for `quota_axi` is `/usr/bin/quota-axi`. A Node with the prefix `/usr/local`, such as one from the tarball on nodejs.org, puts it in `/usr/local/bin/quota-axi` instead; configure the path `command -v` printed.

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
| `worker_args` | `[]` | Arguments added to the worker's `claude` command line, such as `["--model", "opus"]`. `--settings`, `--agents`, `--agent`, `--permission-mode`, `--output-format`, `-p` and `--print` are refused: the factory sets them. `--plugin-dir` is refused too: the sessions run no plugin. The read-only sessions, the author of the pr stage and the reviewers of the review stage, take only the `--model` of these, and a reviewer whose own model is named takes none. |
| `ci` | see the right column | The knobs of the ci stage ([The ci stage](#the-ci-stage)): `repair_rounds` (default `3`), the repair rounds one pull request may take; `bot_reviewers` (default `["chatgpt-codex-connector"]`), the bot logins whose review is waited for once the checks pass; `review_wait` (default `20m`), how long, as a Go duration; `checks_grace` (default `10m`), how long after a push an empty check list of a repository with GitHub workflows is waited out as checks GitHub has not registered yet. `"bot_reviewers": []` is the one for a host whose machine user no bot reviews the pull requests of: Codex reviews automatically only what a connected account opens, and without it every run waits the whole `review_wait` for a review that never comes. A repository may set any of them for itself (see `repositories`); an unknown knob is refused. |
| `gate` | see the right column | The knobs of the gate stage ([The gate stage](#the-gate-stage)): `rounds` (default `3`), the fix sessions a gate that fails in the gate stage may take, `0` blocking on the first failure; `timeout` (default `45m`), how long one run of the gate may take, in the gate stage and on the final head, as a Go duration, before its process group is ended and the run counts as a failure. A repository may set either for itself (see `repositories`); an unknown knob is refused. |
| `review` | see the right column | The knobs of the review stage ([The review stage](#the-review-stage)): `rounds` (default `3`), the rounds the panel may take; `reviewers` (default `["code", "security", "docs", "tests", "senior"]`), the panel of the first round; `gate_rounds` (default `2`), the fix sessions a gate that fails on the final head may take, `0` blocking on the first failure; `classes` (default none), the change classes ([Change classes](#change-classes)). A repository may set any of them for itself, and its `classes` replace the host's as a whole; an unknown knob or reviewer is refused. |
| `paused` | `true` | A paused factory shows the line and claims, resumes and writes nothing. A file that does not name `paused` is paused, so an unattended line is always something you wrote down. The one field read again on every poll, so it takes no restart ([Pausing](#pausing)). |
| `notify` | `[]` | GitHub logins, without the `@`, that are asked for a review when a run ends `ready` and mentioned in a comment on the issue when it waits for a person. Empty: nobody is notified, and the log says so on start. |
| `repositories` | none, at least one | The connected repositories, each `"owner/name"` or `{"name": "owner/name", "base": "dev"}` when this host branches off something other than the base the repository names (its `WF_BASE_BRANCH`, else its default branch). The object may also carry `"gate"`, `"ci"` and `"review"` with any of their knobs, which then stand for that repository over the host's. |
| `quota_axi` | none: the check is off | The absolute path of the quota-axi installed above. |
| `quota_minimum` | `12` | The percentage of the all-models scope, or of the scope of a model the run spends (the worker's, and the one a reviewer of the repository's panel or of one of its change classes names for itself), below which no run starts; `0` never waits. |

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
  "gate": {"rounds": 3, "timeout": "45m"},
  "review": {"rounds": 3, "reviewers": ["code", "security", "docs", "tests", "senior"], "gate_rounds": 2},
  "ci": {"repair_rounds": 3, "bot_reviewers": [], "review_wait": "20m", "checks_grace": "10m"},
  "paused": false,
  "notify": ["yourname"],
  "quota_axi": "/usr/bin/quota-axi",
  "quota_minimum": 12,
  "repositories": [
    "yourname/service",
    {"name": "yourname/app", "base": "dev", "review": {"rounds": 2}, "ci": {"repair_rounds": 2}},
    {"name": "yourname/handbook", "review": {"classes": [
      {"name": "docs", "paths": ["docs/**", "*.md"], "gate": [], "reviewers": ["docs", "senior"]},
      {"name": "ui", "paths": ["ui/**"], "gate": ["make", "ui"]}
    ]}}
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
# claude lives in ~/.local/bin; node has to be here for quota-axi, go and gofmt for the gate.
Environment=PATH=/home/factory/.local/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin
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

- **A user of its own.** `User=factory` is the user that holds the `gh` login and the Claude Code login; the service runs nothing as root.
- **Restart on failure.** A factory that exits with an error is started again after 30 seconds. A configuration it refuses exits with an error as well, so a unit that keeps restarting is read in the journal, where the first `error:` line names the fix.
- **Time to end the worker on stop.** On SIGTERM the factory sends SIGTERM to its worker's process group and kills what is left of the group once the worker has exited, or after 10 seconds if it has not, reads the worker's last output for up to 3 seconds and shuts its interface down within 5. `KillMode=mixed` sends the first signal to the factory alone, so the worker is ended by the factory, which records it, and not at the same moment by systemd; `TimeoutStopSec=60s` leaves room for all of it before systemd kills what is left, including a process a worker started outside its group ([ADR 0027](adr/0027-the-factorys-isolation-boundary-is-the-host.md)). The run is recorded as interrupted, and the next start resumes it once by itself.
- **One factory per host.** A second one fails on start, on the address when it shares it and on the data directory's lock when it does not.

## Access
The interface binds to `127.0.0.1` and has no login, so it is published to the tailnet and nowhere else. On the host, with Tailscale joined to your tailnet:

```sh
tailscale serve --bg 7341
```

The first time, the command prints a link to enable Serve for the tailnet and waits until that is done: open the link on another machine, signed in as an admin of the tailnet, and the command goes on by itself.

The dashboard is then at `https://<host>.<tailnet>.ts.net/` for the devices of your tailnet. Never use `tailscale funnel` for it, which publishes to the internet, and never set `listen` to an address other networks reach. The interface is read-only — `/api/status`, `/api/repositories`, `/api/line`, `/api/runs/{id}` and the dashboard under `/` — but it shows issue titles, repository names and a worker's tool calls, with the content of private repositories in them ([security](security.md#the-factory)).

## Operation on GitHub
The factory is steered on GitHub alone; its interface never writes ([ADR 0023](adr/0023-github-is-the-only-control-surface-of-the-factory.md)).

| You want to | On GitHub |
| --- | --- |
| **Route** an issue | Give it the routing label beside `ready-for-agent`, with no assignee and no open blocker; the planner does this when you route a ticket ([ADR 0021](adr/0021-routing-is-decided-in-the-planner-and-never-stands-alone.md)). The line is ordered by the time the label was set, oldest first, and work the factory already holds comes first ([ADR 0025](adr/0025-one-queue-one-worker-work-in-progress-first.md)). |
| **Release** an issue that waits for you | Remove the machine user as its assignee. An issue whose run ended `blocked`, `failed`, on the deadline or interrupted twice is held — branch, worktree and assignee stay — and is out of the line until you do; the factory then assigns itself again and resumes it in the same worktree. When `notify` names somebody, the comment the factory leaves on the issue says so. |
| **Cancel** a run or let an issue go | Remove the routing label, or close the issue. A run that is going is ended within one poll. The worktree's commits are pushed before the worktree is removed, the branch is deleted only when it carries nothing beyond its base or its pull request was merged at the commit the branch is at, the run's log says which of the two it was, and no record is touched ([ADR 0026](adr/0026-the-factory-never-deletes-work-on-its-own.md)). |
| **Request changes** | Submit a review that requests changes on the run's pull request, as somebody with write access. The factory answers it in the same worktree with an address-reviews session and posts the replies (see [The ci stage](#the-ci-stage)); one review is one run. |
| **Merge** | Merge the pull request yourself: the factory never merges. Merged or closed, it lets the issue go the same way as a cancel. |

The logins in `notify` are asked for a review when a run ends `ready`, and mentioned on the issue when a run waits for a person.

### The implement stage
A run starts with the version of Claude Code it is made with, written on the run, and then one implement session in the run's worktree ([ADR 0042](adr/0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md)). The session runs as the factory's own agent (`--agents`, `--agent worker`), whose prompt is compiled into the binary: the tools `Bash`, `Read`, `Write`, `Edit`, `Grep`, `Glob` and `Agent(Explore)`, which can start the built-in Explore and no other subagent, the model `opus` unless `worker_args` names another, the auto permission mode, no MCP server and none of the workflow plugins; the worktree's own settings apply. Its brief names the issue, the repository, the branch and the base it was cut from. The session reads the issue with `gh`, its body cut at 6,000 characters and its last eight comments at 1,500 each, and the repository's `AGENTS.md` or `CLAUDE.md`, makes the smallest complete change, updates the docs it makes stale, checks it with the single tests of the files it touched and commits in conventional commits without a co-author. It never pushes and runs no full gate, reviewer or pull request: those are the stages after it.

It reports `complete` with its commits, and the run goes on to the gate stage; a session that reports `complete` on a branch with no commit beyond its base fails the run, since there is nothing to gate. It reports `blocked` when the issue cannot be done without you, and the run ends `blocked` with the session's summary, what it needs and why, as its reason and in the comment on the issue.

### The gate stage
Once the implement session has reported its commits, the factory runs the gate itself. When the base has commits the branch lacks, it first merges `origin/<base>` into the branch with a merge commit, never a rebase. A merge that conflicts is left in progress and goes to a fix session given the conflicted files, which resolves them and commits the merge; a session that gives the merge up rather than committing it fails the run.

The factory then determines the change class ([Change classes](#change-classes)) and runs its gate, `make check` for the class `full`, in the worktree in a process group of its own, for at most `gate.timeout`; a gate past it is ended with its process group and counts as a failure. A failure goes to a fix session given the end of the gate's output, and the gate runs again on the commit the session leaves, up to `gate.rounds` fix sessions; past that the run is `blocked` and its reason names the failure with the end of the output. Every run of the gate is recorded on the run under `gates` with its stage, commit, class, command, exit status, whether it timed out, its duration in seconds and the end of its output, and the pass is the gate result the reviewers are briefed with and the pull request carries. A class without a gate runs none and says so. A resumed run with no review to go on from starts at this stage, not at the implement stage, with a fresh budget, when the run before it got past its implement session and its branch carries commits beyond the base. A run before it that ended in the implement session, interrupted or out of quota after a commit of its own, never reported its implementation complete, so the resumed run starts with an implement session again, which reads the commits already on the branch and goes on from them. A resumed run that finds a merge of the base in progress in the worktree takes it up rather than starting it again: files still in conflict go to a fix session, and a merge whose conflicts are all resolved is committed as it stands.

### The review stage
Once the gate stage has recorded a pass, the factory reviews the branch itself ([ADR 0043](adr/0043-the-migration-runs-from-the-last-stage-to-the-first.md)). It starts the reviewers of the panel beside each other in the run's worktree, each a read-only session run as an inline agent of the factory's own prompt (`--agents`, `--agent`): the tools `Read`, `Grep` and `Glob` and nothing else, no MCP server, none of the workflow plugins and none of the worktree's settings. The code, docs and tests reviewers run on `sonnet`; security and senior run on the model `worker_args` names. Every reviewer of a round gets the same brief: the diff range, the base, the commits, the diff, the issue and the gate result. Each reports a verdict, `pass` or `fix`, and its findings, each with a severity (`S1` must fix, `S2` should fix, `S3` a nit), a path, a line, the claim, why it is wrong and the fix. A result that does not fit, such as a `pass` with an `S1` standing, fails the run and names the reviewer.

When any reviewer says `fix`, one fix session in the worktree is given every finding of the round, numbered `F1`, `F2` and so on. It fixes or disputes, with a reason, every `S1` and `S2`, may skip an `S3` with a reason, commits and reports what it fixed, disputed and skipped; a report that leaves an `S1` or `S2` out fails the run, and one that says `blocked` blocks it. The next round runs only the reviewers whose last verdict was `fix`, and a round in which all of them pass ends the review. After `rounds` rounds a `fix` that still stands ends the review all the same: the panel did not pass, and the pull request says so.

When the fixes moved the branch off the commit the gate passed at, the factory runs the gate of the change class, `make check` unless a class says otherwise ([Change classes](#change-classes)), in the worktree on the final head, in a process group of its own and for at most `gate.timeout`. A failure goes to a fix session with the end of the gate's output, and the gate runs again, up to `gate_rounds` fix sessions; past that the run is `blocked` with the output in its reason. Every round, fix and gate is recorded on the run as it ends, so a run resumed during the review goes on from the rounds it recorded, provided the branch still carries the commit they were recorded at, and runs no recorded round again. The dashboard shows the round the review is in.

#### Change classes
A repository may carry an ordered list of change classes under `review.classes` ([ADR 0041](adr/0041-a-change-class-decides-the-gate-and-the-reviewers-before-the-pull-request.md)). Each has a `name`; `paths`, patterns relative to the repository's root whose parts between slashes are `path.Match` patterns or `**` for any number of directories (`docs/**` is everything under `docs`, `*.md` a Markdown file at the root); a `gate`, the command as a list of arguments that runs in the worktree without a shell, `[]` for no gate; and optionally `reviewers`, some of the five, which otherwise are the `reviewers` knob. The factory reads the files changed between the merge base and the head, renames as both their paths: the first class whose patterns cover every one of them applies, and when none does, or no file changed, the built-in class `full` applies, `make check` and all five reviewers, whatever the `reviewers` knob says; a repository without a class keeps its `reviewers` knob.

The class is determined when the review starts, and the reviewers of the first round are its reviewers, kept for a resumed review. It is determined again on the final head before every gate there, since a fix can change a file the first class does not cover, and that gate runs the class's command; a class without a gate runs none, and the gate result says `gate_result: none` and `gate_command: none` and names the class. The gate stage determines it the same way before each of its gates. Every determination is logged with what decided it, the class that covers every file or the first file outside every class, recorded on the run's panel, listed on the `change_class:` line of the panel summary in the pull request and shown on the dashboard beside the review stage. CI still runs the whole gate on every pull request, so what a class leaves out fails there and costs a repair round. A class without a name, with no paths, with a pattern that is no pattern, with a gate that is left out or is not a list, or with a reviewer that is not one of the five is refused on start with the fix, and so is a class named `full` or a name used twice.

### The pr stage
Once the review stage has ended, the factory opens the pull request itself. It pushes the branch, then starts a read-only author session in the run's worktree: the tools `Read`, `Grep` and `Glob` and nothing else, no MCP server, none of the workflow plugins, none of the worktree's own settings and of `worker_args` only the model, briefed with the diff range, the commits, the diff and the issue. The session reports a title in conventional-commit style and a body that closes the issue and carries no verification section. A branch that already has a pull request open when the stage comes to open one goes on with it into the ci stage. The factory uses that body as it is and appends a verification section with the gate result and the panel summary it derived from the recorded rounds: every reviewer's verdicts over the rounds, the fixes by severity and every dispute word for word with the finding it disputes. When the panel did not pass, the section says so and names the reviewers that did not pass. The pull request goes against the base the branch was cut from and is never a draft, since no bot reviews one: you decide on it. An author session that fails, or reports a result that does not fit its schema, ends the run `failed` with the branch pushed. The ended review is recorded on the run, so a run resumed after it, while the branch is still at the commit the review ended at and no pull request is open, starts at the pr stage and reviews nothing again; a branch that moved since starts at the gate stage.

### The ci stage
Once the pr stage has opened the pull request, the factory waits on it, reading it every `poll`. It reads mergeability first, then the checks, then the review of a configured bot, then the reviews and unresolved threads of the pull request, and acts on the first row that decides:

| The pull request | The factory |
| --- | --- |
| conflicts with the base | merges the base into the branch in the run's worktree (a merge commit, never a rebase). A clean merge is pushed as it is; a conflicting one is left in progress and a fix session is started in the worktree with the conflicted files, which resolves, commits and pushes. |
| has checks pending | waits; the run's stage reads `ci`. |
| has failed checks | starts a fix session with the failed checks and the tail of their failed logs from GitHub Actions, which fixes, commits and pushes. |
| has no review of a listed bot yet, within `review_wait` of its checks passing | waits. |
| has a writer's review that asks for changes, or an unresolved thread a writer or a listed bot opened | starts an address-reviews session in the worktree with what the reviewers still ask for: the review summaries, then the unresolved threads with their ids and their replies, of those replies only the ones a writer or a listed bot wrote. The session fixes each point or declines it with a reason, commits and pushes, and reports a reply per thread and one answer to the summaries. The factory posts them: a reply to each thread the brief listed and a resolution of it, and one comment on the pull request for the summaries. A reply to a thread the brief did not list is posted nowhere and warned about. A thread whose reply went through but whose resolution failed is resolved on the next reading without another session, so it gets its reply once. Nobody dismisses a review. A review stands on GitHub until its author approves, so once answered the run does not read it as asking again, nor does a later run on the same pull request, whichever spelling of the repository its record keeps. |
| is green | ends the run `ready` and asks `notify` for a review. |

Every merge, every fix session and every address-reviews session is one repair round, counted against `repair_rounds`; a run whose budget is spent while the pull request still conflicts, fails or has reviewers asking for changes is `blocked`, and its reason names what stands. An address-reviews session that reports `blocked`, naming the point it cannot settle without you, blocks the run on its words and is notified like any blocked run.

A follow-up run, queued by a review that requests changes (see [Operation on GitHub](#operation-on-github)), starts at the address-reviews stage: it answers that review first and then waits in the ci stage. The review is a new mandate, so the follow-up run's count of repair rounds starts at none and the round that answers it is not counted; the rounds after it count, and one review queues one follow-up run.

After a repair the factory judges the pull request again once its head has moved off the commit the round was spent on. A fix session that reports `blocked` blocks the run on its words. The ci stage runs inside the run's `deadline`, a cancel ends a fix or address-reviews session like any session, and a run resumed while its pull request is open starts at the ci stage with the rounds of the run before still counted; nothing before it is done again. The host's git needs an identity (`user.name`, `user.email`) for the merge commit, as its worker sessions do for theirs.

## Upkeep

### Updating
The factory updates nothing: Claude Code, the factory binary and quota-axi are yours, and the prompts every session runs on come with the factory binary ([ADR 0042](adr/0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md)), so a change to them reaches a host with the next factory release. Update them between runs: stopping the factory interrupts the run that is going, which is resumed once by itself, and a second interruption of the same issue waits for you. `curl -s http://127.0.0.1:7341/api/line | jq '.now | length'` prints `0` when nothing runs; pause the factory first (below) to keep it that way.

- **Claude Code**, as the user `factory`: `claude update`, then `claude --version`. Every run records the version it was made with.
- **The factory binary**: download and check it as in [Installation](#installation), then `systemctl stop factory`, `install -m 0755 factory-linux-$arch /usr/local/bin/factory`, `systemctl start factory`, and look for the new version in the journal's first line.
- **quota-axi**: the factory reads the output of the pinned version, so move the pin only after reading the new version's changelog and comparing its Claude percentages with Claude Code's `/usage` (0.1.50 reports the used percentage as remaining): `npm install -g quota-axi@<version>`, restart nothing. If the factory cannot read its answer, every run carries a warning that the check could not answer and starts regardless ([ADR 0028](adr/0028-the-quota-check-is-a-courtesy-not-a-guard.md)); install the pinned version again.

### Moving a host off the plugin
A host set up for an earlier factory carries the `worker` plugin, and its configuration may carry `worker_env`. The factory now refuses to start on `worker_env`, with an error that names the factory's own knobs to write instead: `WF_CI_REPAIR_ROUNDS`, `WF_PR_BOT_REVIEWERS` and `WF_PR_REVIEW_WAIT` are `ci.repair_rounds`, `ci.bot_reviewers` and `ci.review_wait`, `WF_REVIEWERS` and `WF_REVIEW_ROUNDS` are `review.reviewers` and `review.rounds`, and a variable with no knob of its own set something no session of the factory reads any more. Pause the factory, write those knobs and remove `worker_env` from `/etc/factory/factory.json`, install the new binary as in [Updating](#updating) and unpause it. It refuses a `--plugin-dir` in `worker_args` the same way, because that flag would load the plugin into the implement, fix and address-reviews sessions, so take it out too.

The plugin itself does no harm: every session switches it off, and the factory runs no plugin command. Remove it all the same, so the host carries only what its runs use, as the user `factory`:

```sh
claude plugin uninstall worker@workflows
claude plugin marketplace remove workflows
```

A run record written before the move still carries the worker version on the disk; the factory reads it as before and shows the versions of Claude Code and the factory alone.

### Pausing
Set `"paused": true` in the configuration and save it; the factory reads it at its next poll, with no restart. A run that is going finishes with its own outcome, notified like any ending. After it the paused factory shows the line, polls GitHub and writes nothing there: it claims, resumes, follows up, notifies and lets go of nothing, and the dashboard says it is paused. Set `"paused": false` to unpause it the same way; the next poll claims again, and the endings a paused start owed are notified then.

`paused` is the one field read again while the factory runs. Every other field takes effect only with `systemctl restart factory`, and the restart interrupts a run that is going, so pause first and restart once `.now` is empty (see [Updating](#updating)). A file that does not read when the factory reads it again changes nothing: the factory keeps the settings it runs with and names the error once in the journal.

`-paused` is the brake no configuration undoes: add it to `ExecStart` with `systemctl edit factory` and restart, and the factory stays paused whatever the file says until you take it out and restart again.

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

The factory itself never deletes a record or a log, so the directory grows by one record and one log per run. Most of what a host writes comes from the gate, not the factory: an event log is a few KB per run, while the gate's caches, in the home of the user `factory` and in the worktrees, write about 1 GB on the first run and 100 to 150 MB per run afterwards, so the data directory needs no location of its own, not even on an SD card. Back it up if you want the history kept. A host that loses it knows nothing of the work it held, which is still on GitHub on the branches every removed worktree was pushed to, and it loses the history, the automatic resume an issue had left and the notifications it still owed. The issues it held stay assigned to the machine user and out of the line. Release one by taking the assignee off: the factory takes the branch up again by itself when it carries commits beyond the base, the machine user pushed it last and no pull request of it is open. It assigns itself, makes the worktree from the branch and continues on those commits ([ADR 0024](adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md)). A branch somebody else pushed last is a foreign claim, and so is one with an open pull request or with nothing beyond the base: the run ends `lost` and touches nothing. Delete such a branch, or finish the issue by hand, and set the routing label again; a lost run stands in the way of no routing newer than it.
