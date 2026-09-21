# Routing to the factory

The factory host works a routed issue unattended: a worker session on a machine of its own, no Herdr, no screen, nobody to ask, ending in a pull request. Routing is decided here, where the acceptance criteria are written, because the criteria say whether that is possible.

**Judge the acceptance criteria, one ticket at a time.** Recommend routed when every criterion can be met and checked by a worker with a checkout, a shell and the gate: code, tests, documentation, a script, a headless command.

Recommend not routed, and name the reason, when a criterion needs:
- Herdr: a workspace, a pane, a session or a notification the worker would have to create or see.
- a screen: a browser, a screenshot, a rendered interface judged by eye, a device.
- a person: a secret only the maintainer holds, an account or environment the host cannot reach, a decision taken during the work, a release or deploy.

A `ready-for-human` issue is never routed, whatever its criteria say; the issue script refuses that combination and the one where the routing label would sit without `ready-for-agent`.

**Ask once**, with the breakdown in front of you: one line per ticket with number or position, title, the recommendation and the reason for every ticket you advise against. The maintainer answers with the tickets to route (all, none or a list). Nothing is routed by default and nothing the maintainer did not name is routed, whatever you recommended.

Route a ticket by adding the label when it is created or afterwards:

    "${CLAUDE_PLUGIN_ROOT}/scripts/issue.sh" create --title "<title>" --body-file <file> --label ready-for-agent --label factory ...
    "${CLAUDE_PLUGIN_ROOT}/scripts/issue.sh" label <n> --add factory

Setting or removing the label by hand on GitHub stays possible, and is how the maintainer routes a ticket later or cancels a run.
