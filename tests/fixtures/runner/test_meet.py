"""Two fixture classes for tests/run.py that pass only when they run at the same time: each leaves a
file in the directory RUNNER_FIXTURE_MEET names and waits for the other's. In one process after the
other the first waits out its limit and fails, so a pass proves two processes ran them side by side."""
import os
import time
import unittest

LIMIT = 30  # seconds; a meeting ends as soon as both are there, so the limit is spent only on a failure


def meet(me, other):
    directory = os.environ["RUNNER_FIXTURE_MEET"]
    open(os.path.join(directory, me), "w").close()
    deadline = time.monotonic() + float(os.environ.get("RUNNER_FIXTURE_MEET_LIMIT", LIMIT))
    while not os.path.exists(os.path.join(directory, other)):
        if time.monotonic() > deadline:
            raise AssertionError(f"{other} never came: the classes did not run at the same time")
        time.sleep(0.01)


class First(unittest.TestCase):
    def test_meets_second(self):
        meet("first", "second")


class Second(unittest.TestCase):
    def test_meets_first(self):
        meet("second", "first")
