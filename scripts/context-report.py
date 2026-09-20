#!/usr/bin/env python3
"""Context report: a maintainer diagnostic over finished worker sessions.

It reads Claude Code's session transcripts (JSONL) and prints one line per worker session:
Claude Code version, turns, the context at the start of the review and of the pull request
stage, the peak context, the share of tool output that came from reading files through the
shell, the number of read, edit, write and shell calls, and the number of sleep calls.

The transcript format is internal to Claude Code and undocumented; this script is written
against the version in KNOWN_VERSION and fails with an `error:` line when it meets a format
it does not understand. It is a diagnostic only and never an input to the pipeline, so it
lives here and not in a plugin. Python, not bash, because the input is JSONL.

Usage:
  scripts/context-report.py [transcript.jsonl | directory ...]

With no argument it scans the worktree projects under ~/.claude/projects (or
$CLAUDE_CONFIG_DIR/projects) and keeps the sessions that ran a `worker:` skill.
"""

import json
import os
import re
import sys
from pathlib import Path

KNOWN_VERSION = "2.1.278"

# Tool names, grouped the way the report counts them.
READ_TOOLS = {"Read", "NotebookRead"}
EDIT_TOOLS = {"Edit", "MultiEdit", "NotebookEdit"}
WRITE_TOOLS = {"Write"}
SHELL_TOOLS = {"Bash", "BashOutput"}

# A shell segment that prints file content instead of using the read tool.
FILE_READERS = re.compile(r"^(cat|bat|head|tail|less|more|sed\s+-n)\b")
HEREDOC = re.compile(r"<<-?\s*[\"']?(\w+)[\"']?")
SLEEP = re.compile(r"^sleep\b")


class FormatError(Exception):
    """The transcript is not in a format this script understands."""


def strip_heredocs(command):
    """Drop heredoc bodies, so a script passed to python or jq cannot look like a shell read."""
    out, lines, i = [], command.split("\n"), 0
    while i < len(lines):
        line = lines[i]
        out.append(line)
        match = HEREDOC.search(line)
        i += 1
        if match:
            while i < len(lines) and lines[i].strip() != match.group(1):
                i += 1
            i += 1  # the delimiter line itself
    return "\n".join(out)


def commands(command):
    """The commands a shell call runs, without the ones that only receive a pipe.

    `git log | cat` reads no file and `grep sleep x` is no sleep: only what stands at the head
    of a segment counts.
    """
    piped_into = False
    for part in re.split(r"(\|\||&&|[|;&\n])", strip_heredocs(command)):
        if part in ("||", "&&", "|", ";", "&", "\n"):
            piped_into = part == "|"
            continue
        if not piped_into:
            yield part.strip().lstrip("({ ")


def reads_files(command):
    """True when the command prints file content through the shell (cat, sed -n, head, ...)."""
    return any(FILE_READERS.match(segment) for segment in commands(command))


def sleeps(command):
    """True when the command waits with sleep."""
    return any(SLEEP.match(segment) for segment in commands(command))


def context_tokens(usage):
    return (usage.get("input_tokens", 0) + usage.get("cache_read_input_tokens", 0)
            + usage.get("cache_creation_input_tokens", 0))


def result_text(content):
    """Characters a tool result added to the context."""
    if isinstance(content, str):
        return len(content)
    if isinstance(content, list):
        return sum(len(b.get("text", "")) if isinstance(b, dict) else len(str(b)) for b in content)
    return 0


def invocations(record):
    """The worker stages this record starts: `review` or `pr`.

    A stage starts when the skill is invoked: by the agent through the Skill tool, or by the
    maintainer as a slash command. Prose that merely names the skill does not start it.
    """
    found = set()
    message = record.get("message") or {}
    for block in message.get("content") or []:
        if not isinstance(block, dict):
            continue
        if block.get("type") == "tool_use" and block.get("name") in ("Skill", "SlashCommand"):
            text = json.dumps(block.get("input") or {})
        elif block.get("type") == "text" and record.get("type") == "user":
            text = " ".join(re.findall(r"<command-name>(.*?)</command-name>", block.get("text", "")))
        else:
            continue
        if re.search(r"\bworker:review\b", text):
            found.add("review")
        if re.search(r"\bworker:pr\b(?!-)", text):  # not worker:pr-author
            found.add("pr")
    return found


def load(path):
    """Parse one transcript into a row, or return None when it is not a worker session."""
    records = []
    # split("\n"), not splitlines(): a record may carry \x0b, \x1e or   inside a string,
    # and splitlines() would break it into two halves that are both invalid JSON.
    for number, line in enumerate(path.read_text(errors="replace").split("\n"), 1):
        if not line.strip():
            continue
        try:
            records.append(json.loads(line))
        except json.JSONDecodeError:
            raise FormatError(f"line {number} is not JSON") from None
    if not records:
        raise FormatError("the file holds no records")

    version = next((r["version"] for r in records if r.get("version")), None)
    if not version:
        raise FormatError("no record carries a `version` field")

    assistants = [r for r in records if r.get("type") == "assistant" and not r.get("isSidechain")]
    if not assistants:
        return None  # a session that never got an answer: nothing to measure, not a format change
    if not any(isinstance((r.get("message") or {}).get("content"), list) for r in assistants):
        raise FormatError("no assistant record with a message content list", version)
    if not any("input_tokens" in ((r.get("message") or {}).get("usage") or {}) for r in assistants):
        raise FormatError("no assistant record carries `message.usage.input_tokens`", version)

    row = {
        "session": path.stem[:8],
        "version": version,
        "start": next((r["timestamp"] for r in records if r.get("timestamp")), ""),
        "label": next((r["agentName"] for r in records if r.get("type") == "agent-name" and r.get("agentName")),
                      next((r["gitBranch"] for r in records if r.get("gitBranch")), "-")),
    }
    tools, pending, stages = {"read": 0, "edit": 0, "write": 0, "shell": 0, "sleep": 0}, {}, {}
    peak, turns, awaiting = 0, set(), set()
    output_chars, shell_read_chars = 0, 0
    is_worker = any(r.get("attributionPlugin") == "worker" for r in records)

    for record in records:
        if record.get("isSidechain"):
            continue
        message = record.get("message") or {}
        if record.get("type") == "assistant":
            usage = message.get("usage") or {}
            if "input_tokens" in usage:
                tokens = context_tokens(usage)
                peak = max(peak, tokens)
                for stage in awaiting:
                    stages.setdefault(stage, tokens)
                awaiting.clear()
                turns.add(record.get("requestId") or record.get("uuid"))
            for block in message.get("content") or []:
                if isinstance(block, dict) and block.get("type") == "tool_use":
                    name = block.get("name")
                    arguments = block.get("input") or {}
                    if not isinstance(arguments, dict):
                        arguments = {}
                    pending[block.get("id")] = (name, arguments.get("command", ""))
                    if name in READ_TOOLS:
                        tools["read"] += 1
                    elif name in EDIT_TOOLS:
                        tools["edit"] += 1
                    elif name in WRITE_TOOLS:
                        tools["write"] += 1
                    elif name in SHELL_TOOLS:
                        tools["shell"] += 1
                        if sleeps(arguments.get("command", "") or ""):
                            tools["sleep"] += 1
        for block in message.get("content") or []:
            if isinstance(block, dict) and block.get("type") == "tool_result":
                size = result_text(block.get("content"))
                output_chars += size
                name, command = pending.get(block.get("tool_use_id"), (None, ""))
                if name in SHELL_TOOLS and reads_files(command or ""):
                    shell_read_chars += size
        stage = invocations(record)
        if stage:
            is_worker = True
            awaiting |= stage

    if not is_worker:
        return None
    row.update(tools)
    row["turns"] = len(turns)
    row["peak"] = peak
    row["review"] = stages.get("review")
    row["pr"] = stages.get("pr")
    row["shellread"] = (shell_read_chars / output_chars) if output_chars else 0.0
    return row


def transcripts(arguments):
    if arguments:
        paths = []
        for argument in arguments:
            path = Path(argument).expanduser()
            if path.is_dir():
                paths.extend(sorted(path.glob("*.jsonl")))
            else:
                paths.append(path)
        return paths
    config = Path(os.environ.get("CLAUDE_CONFIG_DIR", Path.home() / ".claude")).expanduser()
    projects = config / "projects"
    return sorted(p for d in sorted(projects.glob("*worktrees*")) for p in d.glob("*.jsonl"))


def tokens(value):
    return "-" if value is None else f"{value / 1000:.1f}k"


def render(rows):
    columns = [("session", "session"), ("version", "version"), ("turns", "turns"), ("review", "review"),
               ("pr", "pr"), ("peak", "peak"), ("shellread", "shellread"), ("read", "read"), ("edit", "edit"),
               ("write", "write"), ("shell", "shell"), ("sleep", "sleep"), ("label", "label")]
    table = []
    for row in rows:
        table.append([row["session"], row["version"], str(row["turns"]), tokens(row["review"]), tokens(row["pr"]),
                      tokens(row["peak"]), f"{row['shellread'] * 100:.0f}%", str(row["read"]), str(row["edit"]),
                      str(row["write"]), str(row["shell"]), str(row["sleep"]), row["label"]])
    heads = [head for _, head in columns]
    widths = [max(len(head), *(len(line[i]) for line in table)) if table else len(head)
              for i, head in enumerate(heads)]
    lines = ["  ".join(head.ljust(width) for head, width in zip(heads, widths)).rstrip()]
    for line in table:
        lines.append("  ".join(cell.ljust(width) for cell, width in zip(line, widths)).rstrip())
    return lines


def main(argv):
    paths = transcripts(argv)
    rows = []
    for path in paths:
        try:
            row = load(path)
        except FormatError as error:
            version = error.args[1] if len(error.args) > 1 else "unknown"
            print(f"error: {path}: transcript format not understood ({error.args[0]}); "
                  f"the Claude Code session transcript format is internal and has changed "
                  f"(transcript written by Claude Code {version}, this report is written against "
                  f"{KNOWN_VERSION}); update scripts/context-report.py", file=sys.stderr)
            return 1
        except OSError as error:
            print(f"error: {path}: cannot be read ({error.strerror}); pass a transcript file or a directory "
                  f"of transcripts", file=sys.stderr)
            return 1
        if row:
            rows.append(row)
    print(f"# context report: a diagnostic over Claude Code's internal session transcript format "
          f"(written against {KNOWN_VERSION}), never an input to the pipeline.")
    print("# review/pr/peak: context tokens of the first turn of that stage and the session's maximum. "
          "shellread: share of tool output read from files through the shell.")
    if not rows:
        print("# no worker session found")
        return 0
    for line in render(sorted(rows, key=lambda row: (row["start"], row["session"]))):
        print(line)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
