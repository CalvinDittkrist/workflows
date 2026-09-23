# 0039. Every session reports through a structured result and never through prose

Date: 2026-09-23
Status: accepted

## Context
The factory read how a run ended from the worker's final report, which is markdown written for a person: the first line that said `ready:` or `blocked:` decided, and the pull request was searched out of that line ([ADR 0022](0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md)). Every new way a model wrote that line (bold on the word, a heading, a list item, a sentence around the URL) was a parser case, and a report worded differently read as a failed run although the worker had done its work.

Claude Code can hold a print-mode session to a JSON schema: `--json-schema` takes the schema inline, and the result line of the stream carries the validated object as `structured_output`. A session that cannot produce a fitting one ends with the subtype `error_max_structured_output_retries` (https://code.claude.com/docs/en/headless.md, https://code.claude.com/docs/en/cli-reference.md, https://code.claude.com/docs/en/agent-sdk/typescript.md, checked 2026-09-23). A real session run with `--output-format stream-json` and `--json-schema` on Claude Code 2.1.280 printed that field on its result line.

## Decision
Every session the factory starts is started through one session type, which is the only place a print-mode call is built: the agent, the prompt, the settings and permission mode, a timeout fixed in Go for its stage, and the JSON schema of its result. The factory reads the outcome, the pull request and the summary from the structured result on the result line and never from the report text.

The process and the result are read apart:

- a process that fails is a failed run, with what it said;
- a process that ends well without a result line, or with one whose structured output does not fit the schema, is a failed run that says so;
- a result that reports `blocked` is a blocked run, and its summary is the reason;
- a result that reports `complete` with a pull request of the run's repository is a ready run. The URL is rebuilt from the repository and the number, as before, because the text of an issue can steer what a session writes.

The factory checks the result against the schema itself as well, so what it records does not depend only on the check another program made. A session that runs past its stage's timeout is ended with its process group, and the run fails and names the stage; the run's deadline stays the limit that ends a run with the outcome `timeout`.

In the first step the worker skill is unchanged: the call turns its final report into the structured result, and the schema's descriptions tell the session how.

## Consequences
The parser of the final report and its test go away, and so does the class of failed runs a differently worded report caused. The coupling moves from the wording of a report to the shape of the result line, which is documented. The claude shim of the Go tests and fake mode's scripted worker print the structured result, recorded with the Claude Code version it was taken from, as the rest of the stream is.

A schema is a contract with every prompt the factory carries: a new field is a change to the schema, to the reader and to the prompts at once.
