---
name: tickets
description: Break the spec into agent-ready issues, each a complete vertical slice with its blocking edges, published in dependency order.
disable-model-invocation: true
argument-hint: [spec issue]
---
Source: the spec issue named in the argument ($ARGUMENTS), or the spec this session wrote. Fetch it with `gh issue view <n> --comments` when it is not in context.

1. Explore the code if you have not. Use the project's vocabulary; respect ADRs in the area. Look for a refactor that would make the change easy; if there is one, it is the first ticket.
2. Cut the work into vertical slices. Each ticket is a narrow but complete path through every layer it touches, demoable or verifiable on its own, sized for one fresh agent session. Never one layer per ticket.
   Exception: a wide mechanical change (rename a column, retype a shared symbol) is sequenced as expand, migrate in batches, contract. Each batch is blocked by the expand; the contract is blocked by every batch.
3. Give every ticket its blockers: the tickets that must be closed before it can start. The fewest edges that are true.
4. Show the breakdown as a numbered list: title, blocked by, what it delivers. Ask whether granularity and edges are right. Iterate until the user approves.
5. Ask once which milestone the tickets belong to. Show the open ones with `"${CLAUDE_PLUGIN_ROOT}/scripts/issue.sh" milestones`. Accepted answers: an open milestone, a new `vX.Y.Z` (its description is the spec's goal in one sentence), or none. For a new one run:

       "${CLAUDE_PLUGIN_ROOT}/scripts/issue.sh" milestone <vX.Y.Z> --description "<goal>"

6. Publish in dependency order, blockers first, so edges can name real numbers. For each ticket write the body with [template.md](template.md) and run (`--milestone` and `attach` only when a milestone was chosen):

       "${CLAUDE_PLUGIN_ROOT}/scripts/issue.sh" create --title "<title>" --body-file <file> --label ready-for-agent --parent <spec> --milestone <vX.Y.Z>
       "${CLAUDE_PLUGIN_ROOT}/scripts/issue.sh" block <ticket> --by <n>,<m>

   The first ticket also carries the spec's glossary terms and ADRs under Docs. If the spec fits one session, create no tickets:

       "${CLAUDE_PLUGIN_ROOT}/scripts/issue.sh" label <spec> --add ready-for-agent
       "${CLAUDE_PLUGIN_ROOT}/scripts/issue.sh" attach <spec> --milestone <vX.Y.Z>

7. Reply with the milestone (or none) and one line per ticket (number, title, blocked by) and `next: the orchestrator claims from the frontier (/orchestrator:board); /planner:finish ends this session`.

No em dash character (—) anywhere in the body. Do not close or edit the spec. Bodies carry no file paths and no code; the prototype exception from the spec applies.
