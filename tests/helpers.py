import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SHIMS = ROOT / "tests" / "shims"
ORCH = ROOT / "plugins" / "orchestrator" / "scripts"
WORKER = ROOT / "plugins" / "worker" / "scripts"
STANDARDS = ROOT / "plugins" / "repo-standards" / "scripts"
PLANNER = ROOT / "plugins" / "planner" / "scripts"
# The host's git configuration (a global ignore file, hooks, aliases) must not change what a test sees.
GIT_ISOLATION = {"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1"}


class ShimTest(unittest.TestCase):
    """Base: a temp git repo with one commit on main, shims first on PATH, a call log."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="wf-test-")
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name).resolve()
        self.repo = self.base / "repo"
        self.repo.mkdir()
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.email", "t@example.com")
        self.git("config", "user.name", "t")
        (self.repo / "README.md").write_text("hello\n")
        self.git("add", ".")
        self.git("commit", "-qm", "init")
        self.log = self.base / "calls.log"
        self.argv_log = self.base / "calls.argv.log"
        self.wt_root = self.base / "wt"
        self.wt_root.mkdir()

    def git(self, *args, cwd=None):
        return subprocess.run(["git", *args], cwd=cwd or self.repo, env={**os.environ, **GIT_ISOLATION}, check=True,
                              text=True, capture_output=True).stdout

    def env(self, **extra):
        env = {k: v for k, v in os.environ.items() if not k.startswith(("WF_", "HERDR_", "SHIM_"))}
        env.update({
            "PATH": f"{SHIMS}:{env['PATH']}",
            "SHIM_LOG": str(self.log),
            "SHIM_ARGV_LOG": str(self.argv_log),
            "SHIM_WT_ROOT": str(self.wt_root),
            "SHIM_MAIN": str(self.repo),
            "HERDR_ENV": "1",
            "HERDR_WORKSPACE_ID": "wR",
            **GIT_ISOLATION,
        })
        env.update(extra)
        return env

    def run_script(self, script, *args, cwd=None, stdin="", **extra):
        return subprocess.run(["bash", str(script), *args], cwd=cwd or self.repo, env=self.env(**extra),
                              input=stdin, text=True, capture_output=True)

    def reset_calls(self):
        """Forget every recorded call. Both logs, so calls() and argv_calls() cannot drift apart."""
        for log in (self.log, self.argv_log):
            log.unlink(missing_ok=True)

    def calls(self):
        return self.log.read_text().splitlines() if self.log.exists() else []

    def argv_calls(self):
        """Each logged call as its real argv list, so quoting and word-splitting stay visible."""
        if not self.argv_log.exists():
            return []
        return [line.split("\x1f") for line in self.argv_log.read_text().splitlines()]
