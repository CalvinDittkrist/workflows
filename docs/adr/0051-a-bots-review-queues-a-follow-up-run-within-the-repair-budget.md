# 0051. A bot's review queues a follow-up run within the repair budget

Date: 2026-09-26
Status: accepted
Amends: [0023](0023-github-is-the-only-control-surface-of-the-factory.md) (a bot's review is a second gesture under "Ask for changes")

## Context
- The ci stage answered a bot's thread only when `bot_reviewers` listed the bot, so Codex threads went unanswered on a host listing none.
- A review after the run ended queued nothing unless a writer asked for changes. Spec #217.

## Decision
- A thread by any account of type Bot counts like a writer's. Installing an app is the maintainer's decision.
- GitHub's account type tells a bot from a user of the same login.
- A bot's review on a held issue's pull request queues a follow-up run whose repair count carries over.
- Only a writer's review is a mandate, which starts the count again.
- The repair budget bounds the loop of a bot that reviews every push.
- `bot_reviewers` only names the bots whose review the ci stage waits for after green checks.

## Consequences
- Bot threads get their reply and resolution without a knob.
- The follow-up run itself is the next ticket; this ADR records the decision whole.
- Rejected: the factory asking the bot for a review; a machine user without a Codex connection triggers nothing.
- Rejected: pull requests under the maintainer's token; the machine user is the isolation boundary.
- Rejected: a list of trusted bots for the signal; the knob would have to be set for the feature to work.
