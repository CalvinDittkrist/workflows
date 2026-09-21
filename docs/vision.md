# Vision

This repository is my workflow for working with AI agents, with its best practices, and it is maintained and developed continuously. It optimises for high throughput, a workflow that is uniform but adapts to the repository, low token use, and security.

## Two kinds of workflow
I develop projects locally and through a factory (for example https://github.com/owainlewis/machinist). That needs more than one workflow. The ideal local workflow comes first; the factory workflow follows. Local and factory are separate units.

The local workflow works through GitHub issues and has a planning mode that creates them.

## Every repository on one standard
The workflow has to apply to every repository of mine. They differ: some carry a lot of AI slop, each runs different tests, some are private and some public, some need other branch settings (for example `staging` and `main`). So an independent workflow puts an existing repository on a conventional standard:

- it removes unnecessary files and AI slop without mercy and reduces the repository to a minimum
- it installs the plugins the workflow needs and removes every other skill
- it puts the GitHub side on the standard as well: milestones, project, branch rules
- each check runs in its own independent subagent

This is the `repo-standards` plugin; the standard itself is [docs/repo-standard.md](repo-standard.md).
