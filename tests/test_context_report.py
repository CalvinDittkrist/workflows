import json
import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from helpers import ROOT

REPORT = ROOT / "scripts" / "context-report.py"
FIXTURES = ROOT / "tests" / "fixtures" / "context-report"
WORKER_SESSION = FIXTURES / "f1a7e3aa-0000-4000-8000-000000000001.jsonl"
UNKNOWN_FORMAT = FIXTURES / "deadbeef-0000-4000-8000-000000000002.jsonl"


class ContextReportTests(unittest.TestCase):
    """The maintainer's diagnostic over worker transcripts. Fixture in, one line per session out."""

    def report(self, *args, **env):
        """Run it the way the README documents it: the script itself, not `python3 <path>`."""
        return subprocess.run([str(REPORT), *args], cwd=ROOT, text=True, capture_output=True,
                              env={**os.environ, **env})

    def rows(self, stdout):
        """Every session line, split into fields by column.

        On the two-space separator, not on whitespace: a label is free text and may hold a space.
        """
        lines = [line for line in stdout.splitlines() if not line.startswith("#")]
        heads = re.split(r" {2,}", lines[0]) if lines else []
        return [dict(zip(heads, re.split(r" {2,}", line))) for line in lines[1:]]

    def row(self, stdout):
        """The one session line, split into fields."""
        rows = self.rows(stdout)
        self.assertEqual(len(rows), 1, stdout)
        return rows[0]

    def write(self, directory, name, records):
        path = Path(directory) / name
        path.write_text("".join(json.dumps(record) + "\n" for record in records))
        return path

    def shell_session(self, directory, commands, extra=(), timestamp="2026-09-20T12:00:00.000Z",
                      name="0badc0de-0000-4000-8000-000000000005.jsonl"):
        """A transcript of one worker turn per command, each answering with 100 characters."""
        base = {"version": "2.1.278", "isSidechain": False, "gitBranch": "feat/42-file-tools",
                "timestamp": timestamp}
        records = [{**base, **record} for record in extra]
        for number, command in enumerate(commands, 1):
            call = {"type": "tool_use", "id": f"t{number}", "name": "Bash", "input": {"command": command}}
            records.append({**base, "type": "assistant", "uuid": f"a{number}", "requestId": f"r{number}",
                            "message": {"role": "assistant", "content": [call],
                                        "usage": {"input_tokens": 1000, "cache_read_input_tokens": 9000}}})
            records.append({**base, "type": "user", "uuid": f"u{number}",
                            "toolUseResult": {"stdout": "x" * 100, "stderr": ""},
                            "message": {"role": "user", "content": [
                                {"type": "tool_result", "tool_use_id": f"t{number}", "content": "x" * 100}]}})
        records.append({**base, "type": "assistant", "uuid": "az", "requestId": "rz",
                        "message": {"role": "assistant",
                                    "content": [{"type": "tool_use", "id": "tz", "name": "Skill",
                                                 "input": {"skill": "worker:review"}}],
                                    "usage": {"input_tokens": 0, "cache_read_input_tokens": 10000}}})
        return self.write(directory, name, records)

    def test_reports_one_line_per_session_with_stage_contexts_and_tool_mix(self):
        result = self.report(str(WORKER_SESSION))
        self.assertEqual(result.returncode, 0, result.stderr)
        row = self.row(result.stdout)
        self.assertEqual(row, {
            "session": "f1a7e3aa", "version": "2.1.278", "turns": "9",
            # The stage contexts are the first turn after the skill was invoked, the peak is the session maximum;
            # the subagent turn in the fixture carries 900k and must not count as the worker's context.
            "review": "50.0k", "pr": "80.0k", "peak": "90.0k",
            # 90 of 180 result characters came from `cat` and `sed -n`; `git log | cat` reads no file.
            "shellread": "50%",
            # Five shell calls: cat, the piped git log, grep, sed and sleep. `grep -rn "sleep"` is not a sleep.
            "read": "1", "edit": "1", "write": "1", "shell": "5", "sleep": "1", "label": "#42",
        })

    def test_header_says_it_is_a_diagnostic_over_an_internal_format(self):
        # The header is what tells a reader not to build on the numbers; the issue asks for it.
        stdout = self.report(str(WORKER_SESSION)).stdout
        header = "\n".join(line for line in stdout.splitlines() if line.startswith("#"))
        self.assertIn("internal", header)
        self.assertIn("never an input to the pipeline", header)

    def test_unknown_transcript_format_fails_naming_the_version(self):
        result = self.report(str(UNKNOWN_FORMAT))
        self.assertEqual(result.returncode, 1)
        self.assertTrue(result.stderr.startswith("error:"), result.stderr)
        self.assertIn("9.9.9", result.stderr)  # the Claude Code version that wrote the transcript
        self.assertIn("internal", result.stderr)
        self.assertIn("changed", result.stderr)
        self.assertEqual(self.rows(result.stdout), [])

    def test_one_unreadable_transcript_does_not_hide_the_other_sessions(self):
        # A directory argument reads every transcript in it; a half-written one is named and skipped,
        # the run still exits non-zero, and the sessions that parsed are still reported.
        with tempfile.TemporaryDirectory(prefix="wf-report-") as tmp:
            shutil.copy(WORKER_SESSION, tmp)
            (Path(tmp) / "beef0000-0000-4000-8000-000000000006.jsonl").write_text('{"type": "assis')
            result = self.report(tmp)
        self.assertEqual(result.returncode, 1)
        self.assertIn("unreadable transcript", result.stderr)
        self.assertNotIn("has changed", result.stderr)  # a truncated file is no format change
        self.assertEqual(self.row(result.stdout)["session"], "f1a7e3aa")

    def test_counts_a_sleep_in_a_loop_and_ignores_a_heredoc_body(self):
        with tempfile.TemporaryDirectory(prefix="wf-report-") as tmp:
            path = self.shell_session(tmp, [
                "while true; do sleep 5; done",             # the polling loop ADR 0017 forbids
                "python3 - <<'PY'\ncat /etc/hosts\nPY",     # a heredoc body is not a shell read
                "grep -c '<<EOF' setup.sh\ncat AGENTS.md",  # a quoted `<<` must not swallow the cat
                'grep x <<< "foo"; cat README.md',          # a here-string is not a heredoc either
                'git commit -m "one\nsleep calls; cat is mentioned"',  # a quoted separator is text
                "cat notes.md 2>/dev/null",                 # a stderr redirect still reads the file
                "cat a.txt > b.txt",                        # a stdout redirect writes, it reads nothing in
                "timeout 5 head -5 CHANGELOG.md",           # a wrapper does not hide the reader
            ])
            result = self.report(str(path))
        self.assertEqual(result.returncode, 0, result.stderr)
        row = self.row(result.stdout)
        self.assertEqual(row["sleep"], "1")
        self.assertEqual(row["shell"], "8")
        self.assertEqual(row["shellread"], "50%")  # four reading calls, 400 of 800 characters
        self.assertEqual(row["label"], "feat/42-file-tools")  # no agent name: the branch names the session

    def test_output_that_cannot_be_attributed_to_a_command_is_left_out_of_the_share(self):
        poll = {"type": "assistant", "uuid": "ap", "requestId": "rp", "message": {
            "role": "assistant", "usage": {"input_tokens": 0, "cache_read_input_tokens": 10000},
            "content": [{"type": "tool_use", "id": "tp", "name": "BashOutput", "input": {"bash_id": "b1"}}]}}
        polled = {"type": "user", "uuid": "up", "toolUseResult": {"stdout": "y" * 500}, "message": {
            "role": "user", "content": [{"type": "tool_result", "tool_use_id": "tp", "content": "y" * 500}]}}
        orphan = {"type": "user", "uuid": "uo", "message": {
            "role": "user", "content": [{"type": "tool_result", "tool_use_id": "gone", "content": "z" * 500}]}}
        with tempfile.TemporaryDirectory(prefix="wf-report-") as tmp:
            path = self.shell_session(tmp, ["cat AGENTS.md"], extra=[poll, polled, orphan])
            result = self.report(str(path))
        self.assertEqual(result.returncode, 0, result.stderr)
        row = self.row(result.stdout)
        # The polled output belongs to a background call whose command is elsewhere, and the orphan
        # result has no call in this transcript: neither says anything about how the worker reads.
        self.assertEqual(row["shellread"], "100%")
        self.assertEqual(row["shell"], "2")

    def test_a_worker_session_that_ran_no_tool_is_reported_not_called_a_format_change(self):
        # A worker aborted after its first answer is a short session, not a changed format.
        start = {"type": "user", "uuid": "u0", "message": {
            "role": "user", "content": "<command-name>/worker:work</command-name>"}}
        answer = {"type": "assistant", "uuid": "a0", "requestId": "r0", "attributionPlugin": "worker",
                  "message": {"role": "assistant", "content": [{"type": "text", "text": "Which repository?"}],
                              "usage": {"input_tokens": 500, "cache_read_input_tokens": 9500}}}
        with tempfile.TemporaryDirectory(prefix="wf-report-") as tmp:
            path = self.write(tmp, "0badc0de-0000-4000-8000-00000000000c.jsonl",
                              [{"version": "2.1.278", "isSidechain": False, "gitBranch": "feat/42-x",
                                "timestamp": "2026-09-20T12:00:00.000Z", **record}
                               for record in (start, answer)])
            result = self.report(str(path))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.row(result.stdout)["shell"], "0")

    def test_a_control_character_in_a_label_cannot_repaint_the_terminal(self):
        name = {"type": "agent-name", "agentName": "#42\x1b[2K\x1b[31mFAKE"}
        with tempfile.TemporaryDirectory(prefix="wf-report-") as tmp:
            path = self.shell_session(tmp, ["ls"], extra=[name])
            result = self.report(str(path))
        self.assertNotIn("\x1b", result.stdout)
        self.assertEqual(self.row(result.stdout)["label"], "#42?[2K?[31mFAKE")

    def test_scans_the_worktree_projects_and_skips_sessions_that_are_not_workers(self):
        with tempfile.TemporaryDirectory(prefix="wf-report-") as tmp:
            projects = Path(tmp) / "projects"
            worktrees = projects / "-repo--claude-worktrees-feat-42-x"
            worktrees.mkdir(parents=True)
            shutil.copy(WORKER_SESSION, worktrees)
            # A session in the same directory that never ran a worker skill: no line of its own.
            record = {"type": "assistant", "version": "2.1.278", "uuid": "a1", "requestId": "r1",
                      "isSidechain": False, "timestamp": "2026-09-20T11:00:00.000Z",
                      "message": {"role": "assistant", "content": [{"type": "text", "text": "hi"}],
                                  "usage": {"input_tokens": 10, "cache_read_input_tokens": 90}}}
            (worktrees / "cafe0000-0000-4000-8000-000000000003.jsonl").write_text(json.dumps(record) + "\n")
            # A session outside a worktree project is not a worker session either.
            plain = projects / "-repo"
            plain.mkdir()
            shutil.copy(WORKER_SESSION, plain / "beef0000-0000-4000-8000-000000000004.jsonl")

            result = self.report(CLAUDE_CONFIG_DIR=tmp)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.row(result.stdout)["session"], "f1a7e3aa")

    def test_no_worker_session_reports_it_instead_of_an_empty_table(self):
        with tempfile.TemporaryDirectory(prefix="wf-report-") as tmp:
            (Path(tmp) / "projects").mkdir()
            result = self.report(CLAUDE_CONFIG_DIR=tmp)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("no worker session found", result.stdout)

    def test_a_stage_the_maintainer_types_as_a_slash_command_starts_it(self):
        # Claude Code writes a typed command as plain string content, with the command in a marker.
        typed = {"type": "user", "uuid": "ut", "message": {
            "role": "user", "content": "<command-message>worker:pr</command-message>"
                                       "<command-name>/worker:pr</command-name>"}}
        # No skill is named worker:pr-author today, but the agent is; naming it starts no stage.
        author = {"type": "assistant", "uuid": "aa", "requestId": "ra", "message": {
            "role": "assistant", "usage": {"input_tokens": 0, "cache_read_input_tokens": 99000},
            "content": [{"type": "tool_use", "id": "ta", "name": "Skill",
                         "input": {"skill": "worker:pr-author"}}]}}
        with tempfile.TemporaryDirectory(prefix="wf-report-") as tmp:
            path = self.shell_session(tmp, ["ls"], extra=[author, typed])
            result = self.report(str(path))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.row(result.stdout)["pr"], "10.0k")

    def test_a_session_attributed_to_the_worker_plugin_counts_without_a_skill_call(self):
        # A worker whose stages were all invoked before a handover still belongs in the report.
        with tempfile.TemporaryDirectory(prefix="wf-report-") as tmp:
            records = [json.loads(line) for line in
                       self.shell_session(tmp, ["ls"]).read_text().splitlines()]
            records = [record for record in records
                       if "Skill" not in json.dumps(record.get("message", {}))]
            records[0]["attributionPlugin"] = "worker"
            path = self.write(tmp, "0badc0de-0000-4000-8000-000000000007.jsonl", records)
            result = self.report(str(path))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.row(result.stdout)["review"], "-")  # no review stage, but a row

    def test_sessions_are_reported_oldest_first(self):
        with tempfile.TemporaryDirectory(prefix="wf-report-") as tmp:
            self.shell_session(tmp, ["ls"], timestamp="2026-09-20T09:00:00.000Z",
                               name="0000aaaa-0000-4000-8000-000000000008.jsonl")
            self.shell_session(tmp, ["ls"], timestamp="2026-09-19T09:00:00.000Z",
                               name="1111bbbb-0000-4000-8000-000000000009.jsonl")
            result = self.report(tmp)
        self.assertEqual([row["session"] for row in self.rows(result.stdout)], ["1111bbbb", "0000aaaa"])

    def test_a_record_shape_the_report_cannot_read_is_an_error_not_a_traceback(self):
        with tempfile.TemporaryDirectory(prefix="wf-report-") as tmp:
            records = [json.loads(line) for line in
                       self.shell_session(tmp, ["cat AGENTS.md"]).read_text().splitlines()]
            records[0]["message"] = "a message that is no longer an object"
            path = self.write(tmp, "0badc0de-0000-4000-8000-00000000000a.jsonl", records)
            result = self.report(str(path))
        self.assertEqual(result.returncode, 1)
        self.assertTrue(result.stderr.startswith("error:"), result.stderr)
        self.assertIn("2.1.278", result.stderr)
        self.assertNotIn("Traceback", result.stderr)

    def test_a_renamed_tool_block_is_reported_instead_of_silently_zero(self):
        # The numbers come from tool_use and tool_result; if either is renamed, the report must say so
        # rather than print a row of zeroes.
        with tempfile.TemporaryDirectory(prefix="wf-report-") as tmp:
            text = self.shell_session(tmp, ["cat AGENTS.md"]).read_text()
            path = self.write(tmp, "0badc0de-0000-4000-8000-00000000000b.jsonl", [])
            path.write_text(text.replace('"tool_result"', '"toolResult"'))
            result = self.report(str(path))
        self.assertEqual(result.returncode, 1)
        self.assertIn("tool_result", result.stderr)
        self.assertIn("has changed", result.stderr)
        self.assertEqual(self.rows(result.stdout), [])

    def test_a_missing_transcript_is_an_error_not_a_traceback(self):
        result = self.report(str(FIXTURES / "does-not-exist.jsonl"))
        self.assertEqual(result.returncode, 1)
        self.assertTrue(result.stderr.startswith("error:"), result.stderr)
        self.assertNotIn("Traceback", result.stderr)


if __name__ == "__main__":
    unittest.main()
