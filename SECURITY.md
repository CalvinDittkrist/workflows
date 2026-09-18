# Security policy

## Reporting a vulnerability
Report vulnerabilities privately through GitHub: open the repository's **Security** tab and choose **Report a vulnerability**. Do not open a public issue for a vulnerability.

Include the plugin and version (`plugins/<name>/.claude-plugin/plugin.json`), the steps to reproduce, and what an attacker gains.

## Supported versions
Only the latest release of each plugin gets fixes.

## Scope
The plugins run agents with shell access on your machine. The threat model and the layers that contain an agent are in [docs/security.md](docs/security.md). Reports about prompt injection that leads an agent past those layers are in scope.
