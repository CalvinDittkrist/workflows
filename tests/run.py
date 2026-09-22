"""Run the Python suite with its test classes spread over a pool of processes.

Usage: python3 tests/run.py [-j N] [-s DIR] [NAME ...]

The suite spends its time waiting on the scripts it runs, so its classes run side by side, one class per
process at a time. Without a NAME every test*.py under DIR (default: this directory) is discovered, as
`python3 -m unittest discover` finds them; a NAME is a module (test_worker), a class
(test_worker.PrWaitTests) or a test, and only those run. -j sets the pool size, the CPU count by default;
`-j 1 test_worker.PrWaitTests` repeats one class alone and serially.

The output is what `unittest -v` prints: the lines of one class together, printed when that class ends,
then the tracebacks of every failure and error, the `Ran N tests` line and OK or FAILED. What a test
prints is held back and shown with its failure (unittest's --buffer), and what a class prints stays with
its lines, so classes that run at the same time do not interleave. The exit status is 1 when a test failed or errored, 0 otherwise.

Standard library only: the tests stay ordinary unittest classes, and plain discovery still runs them.
"""
import argparse
import concurrent.futures
import io
import multiprocessing
import os
import sys
import time
import traceback
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SEPARATOR1 = "=" * 70
SEPARATOR2 = "-" * 70

_tests = {}  # the loaded tests of a pool process by id, filled once by init_worker


def load(start_dir, names):
    """The tests in discovery order: all of them, or the named ones the way `python -m unittest NAME` loads
    them. A name that cannot be loaded becomes a test that errors with the reason, so a typo fails the run."""
    loader = unittest.TestLoader()
    if names:
        if start_dir not in sys.path:
            sys.path.insert(0, start_dir)
        suite = loader.loadTestsFromNames(names)
    else:
        suite = loader.discover(start_dir, top_level_dir=start_dir)
    return list(flatten(suite))


def flatten(suite):
    for item in suite:
        if isinstance(item, unittest.TestSuite):
            yield from flatten(item)
        else:
            yield item


def class_of(test):
    kind = type(test)
    return f"{kind.__module__}.{kind.__qualname__}"


def by_class(tests):
    """The test ids grouped per class, classes in the order their first test was loaded."""
    groups = {}
    for test in tests:
        groups.setdefault(class_of(test), []).append(test.id())
    return groups


def init_worker(start_dir, names):
    # Every pool process loads the same tests the parent grouped, and runs them by id.
    for test in load(start_dir, names):
        _tests[test.id()] = test


def run_class(ids):
    """One class, in this process, with unittest's own verbose result. Returns what the parent prints."""
    stream = io.StringIO()
    suite = unittest.TestSuite(_tests[i] for i in ids)
    # What the class prints outside a test, and what unittest's buffer repeats of a failed test, stays
    # with the class's lines instead of reaching the terminal in between another class's. The result is
    # made after the switch, because it takes the stdout it gives back after a test when it is made.
    saved = sys.stdout, sys.stderr
    sys.stdout = sys.stderr = stream
    try:
        result = unittest.TextTestResult(unittest.runner._WritelnDecorator(stream), True, 2)
        result.buffer = True
        result.startTestRun()
        suite(result)
        result.stopTestRun()
    finally:
        sys.stdout, sys.stderr = saved
    problems = [("ERROR", result.getDescription(t), err) for t, err in result.errors]
    problems += [("FAIL", result.getDescription(t), err) for t, err in result.failures]
    return {
        "lines": stream.getvalue(),
        "run": result.testsRun,
        "problems": problems,
        "skipped": len(result.skipped),
        "expected_failures": len(result.expectedFailures),
        "unexpected_successes": len(result.unexpectedSuccesses),
    }


def crashed(name, ids, exc):
    """What a class whose process died reports: every test of it as not run, and one error that says why."""
    err = "".join(traceback.format_exception(type(exc), exc, exc.__traceback__))
    return {"lines": f"{name} ... ERROR (its process ended before the class did)\n", "run": 0,
            "problems": [("ERROR", name, err)], "skipped": 0, "expected_failures": 0, "unexpected_successes": 0}


def main(argv=None):
    parser = argparse.ArgumentParser(prog="tests/run.py", description=__doc__.split("\n\n")[0])
    parser.add_argument("-j", "--jobs", type=int, default=os.cpu_count() or 1,
                        help="processes that run classes at the same time (default: the CPU count)")
    parser.add_argument("-s", "--start-dir", default=HERE, help="directory to discover test*.py in (default: tests/)")
    parser.add_argument("names", nargs="*", help="module, class or test to run, e.g. test_worker.PrWaitTests")
    args = parser.parse_args(argv)
    if args.jobs < 1:
        parser.error(f"--jobs is {args.jobs}; give a pool of at least one process, e.g. -j 1")
    start_dir = os.path.abspath(args.start_dir)

    started = time.perf_counter()
    groups = by_class(load(start_dir, args.names))
    out = sys.stdout
    totals = {"run": 0, "skipped": 0, "expected_failures": 0, "unexpected_successes": 0}
    problems = []
    # spawn on every platform: a pool process starts from a clean interpreter on macOS and Linux alike.
    context = multiprocessing.get_context("spawn")
    with concurrent.futures.ProcessPoolExecutor(max_workers=min(args.jobs, max(len(groups), 1)), mp_context=context,
                                                initializer=init_worker, initargs=(start_dir, args.names)) as pool:
        pending = {pool.submit(run_class, ids): (name, ids) for name, ids in groups.items()}
        for future in concurrent.futures.as_completed(pending):
            name, ids = pending[future]
            try:
                report = future.result()
            except Exception as exc:  # the process died or the result could not be sent back
                report = crashed(name, ids, exc)
            out.write(report["lines"])
            out.flush()
            for key in totals:
                totals[key] += report[key]
            problems += report["problems"]
    elapsed = time.perf_counter() - started

    # The tail unittest's TextTestRunner prints: errors before failures, then the summary.
    out.write("\n")
    for flavour in ("ERROR", "FAIL"):
        for kind, description, err in problems:
            if kind == flavour:
                out.write(f"{SEPARATOR1}\n{flavour}: {description}\n{SEPARATOR2}\n{err}\n")
    run = totals["run"]
    out.write(f"{SEPARATOR2}\nRan {run} test{'' if run == 1 else 's'} in {elapsed:.3f}s\n\n")
    errors = sum(1 for kind, _, _ in problems if kind == "ERROR")
    failures = len(problems) - errors
    infos = []
    if failures:
        infos.append(f"failures={failures}")
    if errors:
        infos.append(f"errors={errors}")
    if totals["skipped"]:
        infos.append(f"skipped={totals['skipped']}")
    if totals["expected_failures"]:
        infos.append(f"expected failures={totals['expected_failures']}")
    if totals["unexpected_successes"]:
        infos.append(f"unexpected successes={totals['unexpected_successes']}")
    ok = not problems and not totals["unexpected_successes"]
    out.write(("OK" if ok else "FAILED") + (f" ({', '.join(infos)})" if infos else "") + "\n")
    out.flush()
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
