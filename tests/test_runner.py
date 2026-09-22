import os
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from helpers import ROOT

RUNNER = ROOT / "tests" / "run.py"
FIXTURE = ROOT / "tests" / "fixtures" / "runner"
CHOSEN = ("test_sample.Passes", "test_sample.Fails", "test_sample.Errors")


class RunnerTests(unittest.TestCase):
    """tests/run.py, the runner `make test` calls, run as a subprocess on the fixture suite in
    tests/fixtures/runner: a class that passes, one that fails, one that errors, one a filter leaves out,
    and two that pass only when they run at the same time."""

    def setUp(self):
        tmp = tempfile.TemporaryDirectory(prefix="wf-runner-")
        self.addCleanup(tmp.cleanup)
        self.base = Path(tmp.name)
        self.log = self.base / "ran.log"
        (self.base / "meet").mkdir()

    def run_fixture(self, *args, **env):
        return subprocess.run([sys.executable, str(RUNNER), "-s", str(FIXTURE), *args], capture_output=True,
                              text=True, timeout=120,
                              env={**os.environ, "RUNNER_FIXTURE_LOG": str(self.log),
                                   "RUNNER_FIXTURE_MEET": str(self.base / "meet"), **env})

    def ran(self):
        """The fixture tests that ran, each with the process it ran in."""
        lines = self.log.read_text().splitlines() if self.log.exists() else []
        return dict(line.split() for line in lines)

    def test_a_filter_runs_the_named_classes_and_nothing_else(self):
        r = self.run_fixture("-j", "2", *CHOSEN)
        self.assertEqual(sorted(self.ran()), ["test_sample.Errors.test_errors", "test_sample.Fails.test_fails",
                                              "test_sample.Passes.test_one", "test_sample.Passes.test_two"])
        self.assertNotIn("test_left_out", r.stdout)
        self.assertIn("Ran 4 tests in ", r.stdout)

    def test_a_pool_of_one_runs_a_named_class_alone_in_one_process(self):
        r = self.run_fixture("-j", "1", "test_sample.Passes", "test_sample.LeftOut")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        ran = self.ran()
        self.assertEqual(sorted(ran), ["test_sample.LeftOut.test_left_out", "test_sample.Passes.test_one",
                                       "test_sample.Passes.test_two"])
        self.assertEqual(len(set(ran.values())), 1, f"a pool of one is one process: {ran}")

    def test_classes_run_on_several_processes_at_once(self):
        # The two classes of test_meet each wait for the other, so they pass only side by side.
        r = self.run_fixture("-j", "2", "test_meet")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("Ran 2 tests in ", r.stdout)
        self.assertTrue(r.stdout.rstrip().endswith("\nOK"), r.stdout)

    def test_a_pool_of_one_runs_the_classes_one_after_the_other(self):
        # The same two classes fail in one process, which is what makes the test above a proof.
        r = self.run_fixture("-j", "1", "test_meet", RUNNER_FIXTURE_MEET_LIMIT="0.5")
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("never came: the classes did not run at the same time", r.stdout)

    def test_the_output_is_unittest_verbose_per_class_with_the_tracebacks_at_the_end(self):
        r = self.run_fixture("-j", "2", *CHOSEN)
        self.assertEqual(r.stderr, "")
        out = r.stdout
        # One verbose line per test, and the lines of one class together whatever finished in between.
        results = re.findall(r"^(test_\w+) \(test_sample\.(\w+)[.\w]*\) \.\.\. (\w+)$", out, re.M)
        self.assertEqual(sorted(results), [("test_errors", "Errors", "ERROR"), ("test_fails", "Fails", "FAIL"),
                                           ("test_one", "Passes", "ok"), ("test_two", "Passes", "ok")])
        classes = [cls for _, cls, _ in results]
        first = classes.index("Passes")
        self.assertEqual(classes[first:first + 2], ["Passes", "Passes"], f"the lines of Passes are apart:\n{out}")
        # The tracebacks come after every test line, errors before failures, as TextTestRunner prints them.
        last_line = max(out.index(f"{name} (") for name, _, _ in results)
        error = out.index("ERROR: test_errors (test_sample.Errors")
        failure = out.index("FAIL: test_fails (test_sample.Fails")
        self.assertLess(last_line, error)
        self.assertLess(error, failure)
        self.assertIn('raise RuntimeError("the fixture errors here")\nRuntimeError: the fixture errors here',
                      out[error:failure])
        self.assertIn("AssertionError: 1 != 2 : the fixture fails here", out[failure:])
        # What the failing test printed is shown with its failure, not in between the lines of other classes.
        self.assertIn("Stdout:\nprinted by the failing test", out[failure:])
        self.assertRegex(out, r"\n-{70}\nRan 4 tests in \d+\.\d{3}s\n\nFAILED \(failures=1, errors=1\)\n$")

    def test_the_exit_status_is_one_on_a_failure_or_an_error_and_zero_otherwise(self):
        for names, status in ((("test_sample.Passes",), 0), (("test_sample.Passes", "test_sample.Fails"), 1),
                              (("test_sample.Passes", "test_sample.Errors"), 1)):
            with self.subTest(names=names):
                r = self.run_fixture("-j", "2", *names)
                self.assertEqual(r.returncode, status, r.stdout + r.stderr)
                verdict = r.stdout.rstrip().splitlines()[-1]
                self.assertEqual(verdict if status == 0 else verdict.split(" ")[0], "OK" if status == 0 else "FAILED")

    def test_a_name_that_does_not_load_fails_the_run(self):
        # A typo in a filter must not pass green on no tests at all.
        r = self.run_fixture("-j", "1", "test_sample.Pases")
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("has no attribute 'Pases'", r.stdout)
        self.assertEqual(self.ran(), {})
