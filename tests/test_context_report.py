import json
import os
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
        return subprocess.run(["python3", str(REPORT), *args], cwd=ROOT, text=True, capture_output=True,
                              env={**os.environ, **env})

    def row(self, stdout):
        """The header row and the one session row, each split into fields."""
        lines = [line for line in stdout.splitlines() if not line.startswith("#")]
        self.assertEqual(len(lines), 2, stdout)
        return dict(zip(lines[0].split(), lines[1].split()))

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
        self.assertEqual(result.stdout, "")

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

    def test_a_missing_transcript_is_an_error_not_a_traceback(self):
        result = self.report(str(FIXTURES / "does-not-exist.jsonl"))
        self.assertEqual(result.returncode, 1)
        self.assertTrue(result.stderr.startswith("error:"), result.stderr)
        self.assertNotIn("Traceback", result.stderr)


if __name__ == "__main__":
    unittest.main()
