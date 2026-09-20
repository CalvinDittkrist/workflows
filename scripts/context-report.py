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
# `<<TAG`, `<<-TAG`, `<<'TAG'`: a here-string (`<<<`) and a shift (`1 << 2`) are neither.
HEREDOC = re.compile(r"<<(?!<)-?\s*([\"']?)([A-Za-z_]\w*)\1")
SLEEP = re.compile(r"^sleep\b")
# `> file` and `&> file` send the output away; `2> file` and `2>&1` only move stderr.
STDOUT_REDIRECT = re.compile(r"(?<![0-9])>")
# What a shell keyword puts in front of the command it runs, and the wrappers that pass it on.
SEGMENT_PREFIX = re.compile(r"^(do|then|else|elif|if|while|until|!|time|nohup|command|exec|eval"
                            r"|timeout\s+[\d.]+[smhd]?)\s+")


class FormatError(Exception):
    """The transcript is not in a format this script understands.

    `unreadable` separates a truncated or half-written file, which says nothing about the
    format, from a transcript whose shape has changed, which asks for a change to this script.
    """

    def __init__(self, detail, version=None, unreadable=False):
        super().__init__(detail)
        self.detail, self.version, self.unreadable = detail, version, unreadable


def strip_heredocs(command):
    """Drop heredoc bodies, so a script passed to python or jq cannot look like a shell read.

    A `<<TAG` whose terminator never comes is not a heredoc (it is a quoted `<<` somewhere in
    the command), and the lines after it are kept: dropping them would hide real shell reads.
    """
    out, lines, i = [], command.split("\n"), 0
    while i < len(lines):
        out.append(lines[i])
        match = HEREDOC.search(lines[i])
        i += 1
        if not match:
            continue
        end = next((j for j in range(i, len(lines)) if lines[j].strip() == match.group(2)), None)
        if end is not None:
            i = end + 1  # skip the body and the delimiter line
    return "\n".join(out)


def split(text):
    """(segment, separator that ended it) for every shell segment, ignoring quoted separators.

    A `;` or a newline inside `git commit -m "... sleep ..."` is text, not a separator.
    """
    parts, current, quote, i = [], [], None, 0
    while i < len(text):
        character, pair = text[i], text[i:i + 2]
        if character == "\\" and i + 1 < len(text) and quote != "'":
            current.append(pair)
            i += 2
            continue
        if quote:
            current.append(character)
            quote = None if character == quote else quote
        elif character in "'\"":
            current.append(character)
            quote = character
        elif pair in ("&&", "||"):
            parts.append(("".join(current), pair))
            current, i = [], i + 1
        elif character in "|;&\n":
            parts.append(("".join(current), character))
            current = []
        else:
            current.append(character)
        i += 1
    parts.append(("".join(current), ""))
    return parts


def commands(command):
    """The commands a shell call runs, without the ones that only receive a pipe.

    `git log | cat` reads no file and `grep sleep x` is no sleep: only what stands at the head
    of a segment counts, after the shell keyword that may precede it (`do sleep 5`).
    """
    piped_into = False
    for part, separator in split(strip_heredocs(command)):
        if not piped_into:
            segment = part.strip().lstrip("({ ")
            while SEGMENT_PREFIX.match(segment):
                segment = SEGMENT_PREFIX.sub("", segment)
            yield segment
        piped_into = separator == "|"


def reads_files(command):
    """True when the command prints file content into the context (cat, sed -n, head, ...).

    A segment that redirects its output to a file writes instead of reading: `cat <<'EOF' >
    new.py` and `cat a b > merged` add nothing to the context. A redirect of stderr does not
    count as one, so `cat missing.md 2>/dev/null` stays a read.
    """
    return any(FILE_READERS.match(segment) and not STDOUT_REDIRECT.search(segment)
               for segment in commands(command))


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


def blocks(message):
    """The content blocks of a message; a message whose content is plain text has none."""
    content = message.get("content")
    return [block for block in content if isinstance(block, dict)] if isinstance(content, list) else []


def prompt_text(record):
    """The text of a user record, whether its content is a string or text blocks."""
    content = (record.get("message") or {}).get("content")
    if isinstance(content, str):
        return content
    return " ".join(block.get("text", "") for block in blocks(record.get("message") or {})
                    if block.get("type") == "text")


def invocations(record):
    """The worker stages this record starts: `review` or `pr`.

    A stage starts when the skill is invoked: by the agent through the Skill tool, or by the
    maintainer as a slash command, which the transcript marks with `<command-name>`. Prose that
    merely names the skill does not start it.
    """
    texts = [json.dumps(block.get("input") or {}) for block in blocks(record.get("message") or {})
             if block.get("type") == "tool_use" and block.get("name") in ("Skill", "SlashCommand")]
    if record.get("type") == "user":
        texts += re.findall(r"<command-name>(.*?)</command-name>", prompt_text(record))
    found = set()
    for text in texts:
        if re.search(r"\bworker:review\b", text):
            found.add("review")
        if re.search(r"\bworker:pr\b(?!-)", text):  # not worker:pr-author
            found.add("pr")
    return found


def read_records(path):
    """Every JSON record of a transcript, in order."""
    records = []
    # split("\n"), not splitlines(): a record may carry \x0b, \x1e or U+2028 inside a string,
    # and splitlines() would break it into two halves that are both invalid JSON.
    for number, line in enumerate(path.read_text(encoding="utf-8", errors="replace").split("\n"), 1):
        if not line.strip():
            continue
        try:
            record = json.loads(line)
        except json.JSONDecodeError:
            raise FormatError(f"line {number} is not JSON", unreadable=True) from None
        if not isinstance(record, dict):
            raise FormatError(f"line {number} is not a JSON object")
        records.append(record)
    return records


def check_transcript(records, version):
    """Refuse a transcript whose records no longer carry what every measurement reads."""
    assistants = [r for r in records if r.get("type") == "assistant" and not r.get("isSidechain")]
    if not assistants:
        return False  # a session that never got an answer: nothing to measure, not a format change
    if not any(blocks(r.get("message") or {}) for r in assistants):
        raise FormatError("no assistant record with a message content list", version)
    if not any("input_tokens" in ((r.get("message") or {}).get("usage") or {}) for r in assistants):
        raise FormatError("no assistant record carries `message.usage.input_tokens`", version)
    return True


def check_tool_calls(records, calls, answered, version):
    """Refuse a session whose tool calls stopped parsing, but not one that made none.

    A worker that was aborted after its first question ran no tool at all; that is a short
    session, not a changed format. The evidence that a call is there but unread is the
    `toolUseResult` field beside the message: every answered call carries it.
    """
    if not any(r.get("toolUseResult") is not None for r in records if not r.get("isSidechain")):
        return
    if not calls:
        raise FormatError("records carry `toolUseResult` but no `tool_use` block was found", version)
    if not answered:
        raise FormatError("records carry `toolUseResult` but no `tool_result` block matches a "
                          "`tool_use` id", version)


def load(path):
    """Parse one transcript into a row, or return None when it is not a worker session."""
    records = read_records(path)
    if not records:
        return None  # an empty file: a session that wrote nothing

    version = next((r["version"] for r in records if r.get("version")), None)
    if not version:
        raise FormatError("no record carries a `version` field")
    try:
        if not check_transcript(records, version):
            return None
        row = measure(records, path, version)
    except (AttributeError, TypeError, ValueError) as error:
        raise FormatError(f"a record has a shape this report cannot read ({error})", version) from None
    if row is None:
        return None
    row["version"] = version
    return row


def measure(records, path, version):
    """The row for a worker session, or None when the session ran no worker stage."""
    row = {
        "session": path.stem[:8],
        "start": next((r["timestamp"] for r in records if r.get("timestamp")), ""),
        "label": next((r["agentName"] for r in records if r.get("type") == "agent-name" and r.get("agentName")),
                      next((r["gitBranch"] for r in records if r.get("gitBranch")), "-")),
    }
    tools, pending, stages = {"read": 0, "edit": 0, "write": 0, "shell": 0, "sleep": 0}, {}, {}
    peak, turns, awaiting = 0, set(), set()
    output_chars, shell_read_chars, calls, answered = 0, 0, 0, 0
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
            for block in blocks(message):
                if block.get("type") == "tool_use":
                    calls += 1
                    name = block.get("name")
                    arguments = block.get("input")
                    arguments = arguments if isinstance(arguments, dict) else {}
                    command = arguments.get("command") or ""
                    pending[block.get("id")] = (name, command)
                    if name in READ_TOOLS:
                        tools["read"] += 1
                    elif name in EDIT_TOOLS:
                        tools["edit"] += 1
                    elif name in WRITE_TOOLS:
                        tools["write"] += 1
                    elif name in SHELL_TOOLS:
                        tools["shell"] += 1
                        if sleeps(command):
                            tools["sleep"] += 1
        for block in blocks(message):
            if block.get("type") == "tool_result":
                name, command = pending.get(block.get("tool_use_id"), (None, None))
                if name is None:
                    continue  # a result whose call is not in this transcript says nothing about the mix
                answered += 1
                if name == "BashOutput":
                    continue  # polls a background call: its output cannot be attributed to a command
                size = result_text(block.get("content"))
                output_chars += size
                if name in SHELL_TOOLS and reads_files(command):
                    shell_read_chars += size
        stage = invocations(record)
        if stage:
            is_worker = True
            awaiting |= stage

    if not is_worker:
        return None
    check_tool_calls(records, calls, answered, version)
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


def thousands(value):
    """A context size as the maintainer reads it: 96.2k."""
    return "-" if value is None else f"{value / 1000:.1f}k"


COLUMNS = [
    ("session", lambda row: row["session"]),
    ("version", lambda row: row["version"]),
    ("turns", lambda row: str(row["turns"])),
    ("review", lambda row: thousands(row["review"])),
    ("pr", lambda row: thousands(row["pr"])),
    ("peak", lambda row: thousands(row["peak"])),
    ("shellread", lambda row: f"{row['shellread'] * 100:.0f}%"),
    ("read", lambda row: str(row["read"])),
    ("edit", lambda row: str(row["edit"])),
    ("write", lambda row: str(row["write"])),
    ("shell", lambda row: str(row["shell"])),
    ("sleep", lambda row: str(row["sleep"])),
    ("label", lambda row: row["label"]),
]


def printable(cell):
    """No control character from a transcript reaches the maintainer's terminal."""
    return re.sub(r"[^\x20-\x7e]", "?", cell)


def render(rows):
    heads = [head for head, _ in COLUMNS]
    table = [[printable(cell(row)) for _, cell in COLUMNS] for row in rows]
    widths = [max([len(head)] + [len(line[i]) for line in table]) for i, head in enumerate(heads)]
    return ["  ".join(cell.ljust(width) for cell, width in zip(line, widths)).rstrip()
            for line in [heads, *table]]


def main(argv):
    rows, failed = [], False
    for path in transcripts(argv):
        try:
            row = load(path)
        except FormatError as error:
            if error.unreadable:
                print(f"error: {path}: unreadable transcript ({error.detail}); a truncated or half-written "
                      f"session file, skipped", file=sys.stderr)
            else:
                print(f"error: {path}: transcript format not understood ({error.detail}); the Claude Code "
                      f"session transcript format is internal and has changed (transcript written by Claude "
                      f"Code {error.version or 'unknown'}, this report is written against {KNOWN_VERSION}); "
                      f"update scripts/context-report.py", file=sys.stderr)
            failed = True
            continue
        except OSError as error:
            print(f"error: {path}: cannot be read ({error.strerror}); pass a transcript file or a directory "
                  f"of transcripts", file=sys.stderr)
            failed = True
            continue
        if row:
            rows.append(row)
    print(f"# context report: a diagnostic over Claude Code's internal session transcript format "
          f"(written against {KNOWN_VERSION}), never an input to the pipeline.")
    print("# review/pr/peak: context tokens of the first turn of that stage and the session's maximum. "
          "shellread: share of tool output read from files through the shell.")
    if not rows:
        print("# no worker session found")
        return 1 if failed else 0
    for line in render(sorted(rows, key=lambda row: (row["start"], row["session"]))):
        print(line)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
