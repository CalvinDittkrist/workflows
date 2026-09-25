# 0048. The quota check renews an expired credential

Date: 2026-09-25
Status: accepted
Amends: [0037](0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md) (the check no longer passes `--no-credential-refresh`)

## Context
[ADR 0037](0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md) ran the check with `--no-credential-refresh`, on the ground that the checks run beside a worker's Claude Code, which renews the same credential, and a second writer of `~/.claude/.credentials.json` was nobody's wish. That ground does not hold: the factory works one run at a time, the check before a run happens while no session of the factory is running, and the check after a session's error happens after that session has ended. No session of the factory reads the credential while the check runs.

What the flag did do was make the check fail open whenever the host had been idle for longer than the credential's life. Claude Code's OAuth access token lasts about eight hours from its last renewal, and a worker session is what renews it. On the Pi host every run that started more than eight hours after the one before it started with the warning `the quota check failed, so this run started without knowing how much of the Claude quota is left: quota-axi's reading of claude is stale`, which is the check being absent exactly when the line had been quiet and the maintainer's share of the window was most likely in use.

quota-axi renews an expired credential by running `claude doctor`, the smallest non-interactive Claude Code command: it spends no quota, opens no browser and rewrites the credential file the way Claude Code itself does. On the Pi host it takes two to three seconds with the factory's `PATH`, well inside the check's 30 seconds.

## Decision
The factory runs `quota-axi --provider claude --json`, without `--no-credential-refresh`. An expired credential is renewed by Claude Code's own command through quota-axi, and the check reads a fresh answer.

Everything else of [ADR 0037](0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md) stands: the check fails open on a reading quota-axi still marks stale, on another schema version, on an error exit and on a check that takes longer than 30 seconds, and the factory itself never writes a credential.

## Consequences
The check answers after a quiet night, which is when it matters. A renewal that fails, because the refresh token is gone and the host needs the headless login again, leaves the reading stale and the check fails open as before, with the same warning on the run, so a host that lost its login is still not a host that stopped.

The credential file is now written by two programs, Claude Code and quota-axi's call of `claude doctor`, but never at the same time, because the factory runs the check only while it runs no session. Should the factory ever run a check beside a session, this decision has to be revisited, and the one-run-at-a-time rule is where to look first.

Rejected: keeping the flag and renewing the credential some other way before the check, such as the factory running `claude doctor` itself. That is a second copy of what quota-axi already does, with a version of Claude Code's command line to keep in step.
