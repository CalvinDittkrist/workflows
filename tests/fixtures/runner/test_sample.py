"""A fixture suite for tests/run.py, run by RunnerTests in test_runner.py and never by the suite itself:
this directory is no package, so discovery of tests/ does not descend into it. Every test that runs
appends its id and the number of its process to the file RUNNER_FIXTURE_LOG names."""
import os
import unittest


def ran(test):
    with open(os.environ["RUNNER_FIXTURE_LOG"], "a") as log:
        log.write(f"{test.id()} {os.getpid()}\n")


class Passes(unittest.TestCase):
    def test_one(self):
        ran(self)

    def test_two(self):
        ran(self)


class Fails(unittest.TestCase):
    def test_fails(self):
        ran(self)
        print("printed by the failing test")
        self.assertEqual(1, 2, "the fixture fails here")


class Errors(unittest.TestCase):
    def test_errors(self):
        ran(self)
        raise RuntimeError("the fixture errors here")


class LeftOut(unittest.TestCase):
    def test_left_out(self):
        ran(self)
