# 0039. Every session reports through a structured result and never through prose

Date: 2026-09-23
Status: accepted

## Context
- The factory read a run's end from the first `ready:` or `blocked:` line of the worker's markdown report ([ADR 0022](0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)).
- Every new form of that line was a parser case.
- `--json-schema` holds a print-mode session to a schema, and the result line carries `structured_output`. A session that cannot fit it ends with `error_max_structured_output_retries` ([headless](https://code.claude.com/docs/en/headless.md), [CLI reference](https://code.claude.com/docs/en/cli-reference.md), [Agent SDK](https://code.claude.com/docs/en/agent-sdk/typescript.md)).

## Decision
Every session starts through one session type, the only place a print-mode call is built, with its stage's timeout and result schema. The factory reads the outcome, the pull request and the summary from the structured result, never from the report text.

- A failed process, a missing result line or a result off the schema is a failed run.
- `blocked` is a blocked run with its summary as the reason.
- `complete` with a pull request of the run's repository is a ready run; the URL is rebuilt from repository and number, because issue text can steer what a session writes.
- The factory checks the result against the schema itself.
- A session past its stage's timeout is ended with its process group, and the run fails naming the stage; only the run's deadline gives the outcome `timeout`.

## Consequences
- The report parser goes; the coupling moves to the documented result line.
- A new field changes the schema, the reader and the prompts at once.
- Rejected: hardening the report parser, which grows with every new wording.
