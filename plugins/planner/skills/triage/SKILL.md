---
name: triage
description: Move an issue through the triage states. Verify the claim, grill when needed, post the agent brief, set category and state labels.
disable-model-invocation: true
argument-hint: [issue]
allowed-tools: Bash(${CLAUDE_PLUGIN_ROOT}/scripts/triage-list.sh)
---
Queue:
!`${CLAUDE_PLUGIN_ROOT}/scripts/triage-list.sh`

Labels. Category: `bug` or `enhancement`, exactly one. State: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human` or `wontfix`, exactly one. Routing: `factory`, optional, only next to `ready-for-agent`. If an issue carries two states, say so and ask before doing anything else. Every comment you post starts with `> Written by an agent during triage.`

Without an argument: show the three buckets above, one line each, and let the user pick. With an issue ($ARGUMENTS):

1. Read it fully with `gh issue view <n> --comments`, including earlier triage notes; do not re-ask what they settled. Search the code for an existing implementation of the request by concept, not by wording. Search closed rejections: `gh issue list --state closed --label wontfix --search "<concept>"`.
2. Recommend category and state with reasoning and a three-line summary of the relevant code. Wait for the user.
3. Verify the claim before any interview. Reproduce a bug from the reported steps; for a request, confirm the gap exists. Report confirmed, failed, or not enough detail (a `needs-info` signal).
4. If the request needs shaping, run the interview rules in [../grill/SKILL.md](../grill/SKILL.md) round by round.
5. Apply the outcome with `"${CLAUDE_PLUGIN_ROOT}/scripts/issue.sh"`:
   - `ready-for-agent`: post the brief from [brief.md](brief.md) with `comment`, then ask whether the issue is routed to the factory, following [../tickets/routing.md](../tickets/routing.md): your recommendation with its reason, for this one issue. Then `label <n> --add ready-for-agent --remove needs-triage`, with `--add factory` when the maintainer routed it.
   - `ready-for-human`: the same brief plus one line on why it cannot be delegated. It is never routed; the script refuses that combination.
   - `needs-info`: post the notes template below, then `label <n> --add needs-info --remove needs-triage`.
   - `wontfix`: `close <n> --reason not-planned --comment-file <f>`. For a request that already exists, point to where it lives. For a rejected one, give the reason. The closed issue is the record; write no file.
6. When the user says "move #n to <state>", confirm the change in one line and do it. Skip the interview, but offer a brief when the target is `ready-for-agent`, and ask about routing with it.

Needs-info template:

    ## Triage notes
    **Established so far:** one line each.
    **Still needed from @<reporter>:** specific questions, not "more info".
