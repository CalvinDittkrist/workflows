import fnmatch
import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from helpers import GIT_ISOLATION, ROOT

WORKFLOW = ROOT / ".github" / "workflows" / "factory-release.yml"


class FactoryReleaseTests(unittest.TestCase):
    """`scripts/release.sh factory`: it tags the version in factory/VERSION, and refuses everything
    that would tag something else. The real script runs in a repository of its own, because a
    release tags the checkout it stands in."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="wf-release-")
        self.addCleanup(self.tmp.cleanup)
        self.repo = Path(self.tmp.name) / "repo"
        (self.repo / "scripts").mkdir(parents=True)
        (self.repo / "factory").mkdir()
        shutil.copy(ROOT / "scripts" / "release.sh", self.repo / "scripts" / "release.sh")
        self.version("0.1.0")
        self.gate(green=True)
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.email", "t@example.com")
        self.git("config", "user.name", "t")
        self.commit()

    def version(self, said):
        (self.repo / "factory" / "VERSION").write_text(f"{said}\n")

    def gate(self, green):
        """A `make check` that stands in for the gate: it says it ran, and passes or fails as asked."""
        ran = 'echo "the gate ran"' if green else '(echo "the gate ran"; echo "make: FAIL" >&2; exit 2)'
        (self.repo / "Makefile").write_text(f"check:\n\t@{ran}\n")

    def env(self):
        """The suite itself runs under `make test`, and a make that finds MAKELEVEL or MAKEFLAGS in its
        environment treats itself as a sub-make: it would print "Entering directory" lines into what the
        script under test says. A release is run outside make, so the test gives it that environment."""
        return {**{k: v for k, v in os.environ.items() if not k.startswith(("MAKE", "MFLAGS"))}, **GIT_ISOLATION}

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.repo, env=self.env(),
                              check=True, text=True, capture_output=True).stdout

    def commit(self):
        self.git("add", "-A")
        self.git("commit", "-qm", "release fixture")

    def release(self, *args):
        return subprocess.run(["bash", "scripts/release.sh", *args], cwd=self.repo,
                              env=self.env(), text=True, capture_output=True)

    def tags(self):
        return self.git("tag").split()

    def test_the_factory_is_tagged_from_the_version_file_after_the_gate(self):
        r = self.release("factory")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("the gate ran", r.stdout)
        self.assertEqual(self.tags(), ["factory/v0.1.0"])
        # Annotated, and it says what it is: `git show` of the tag carries the message.
        self.assertIn("factory v0.1.0", self.git("tag", "-l", "-n1", "factory/v0.1.0"))
        # Nothing pushed without --push, and the command that would is printed.
        self.assertIn("git push origin factory/v0.1.0", r.stdout)

    def test_the_tag_is_neither_a_plugin_tag_nor_a_milestone_tag(self):
        self.version("1.2.3")
        self.commit()
        self.assertEqual(self.release("factory").returncode, 0)
        tag = self.tags()[0]
        self.assertEqual(tag, "factory/v1.2.3")
        self.assertNotRegex(tag, r"^v\d+\.\d+\.\d+$")  # a milestone release of the orchestrator
        self.assertNotIn("--v", tag)  # a plugin release of `claude plugin tag`

    def test_a_tag_that_exists_is_refused_before_the_gate_runs(self):
        self.git("tag", "factory/v0.1.0")
        r = self.release("factory")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: the tag factory/v0.1.0 exists; bump the version", r.stderr)
        self.assertNotIn("the gate ran", r.stdout)

    def test_a_failing_gate_tags_nothing(self):
        self.gate(green=False)
        self.commit()
        r = self.release("factory")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: the gate failed", r.stderr)
        self.assertEqual(self.tags(), [])

    def test_uncommitted_changes_are_refused(self):
        (self.repo / "factory" / "run.go").write_text("// not committed\n")
        r = self.release("factory")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: the working tree has uncommitted changes", r.stderr)
        self.assertEqual(self.tags(), [])

    def test_a_version_that_is_not_x_y_z_is_refused(self):
        for said in ("0.1", "v0.1.0", "0.1.0-rc1", ""):
            with self.subTest(said=said):
                self.version(said)
                self.commit()
                r = self.release("factory")
                self.assertNotEqual(r.returncode, 0)
                self.assertIn("write the version as X.Y.Z", r.stderr)
                self.assertEqual(self.tags(), [])

    def test_an_unknown_option_is_refused(self):
        r = self.release("factory", "--force")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: unknown option --force", r.stderr)
        self.assertEqual(self.tags(), [])

    def test_the_tag_it_creates_is_the_one_ci_builds_binaries_for(self):
        """The script and the workflow are two files; a tag only one of them knows is a release that
        never builds."""
        self.assertEqual(self.release("factory").returncode, 0)
        trigger = re.search(r"tags: \['([^']+)'\]", WORKFLOW.read_text())
        self.assertIsNotNone(trigger, "the workflow names no tag pattern")
        self.assertTrue(fnmatch.fnmatch(self.tags()[0], trigger.group(1)),
                        f"{self.tags()[0]} does not match {trigger.group(1)}")


class FactoryReleaseWorkflowTests(unittest.TestCase):
    """The workflow that builds the binaries: what starts it, and what it builds."""

    def setUp(self):
        self.workflow = WORKFLOW.read_text()
        self.trigger = self.workflow.split("jobs:")[0]

    def test_nothing_but_a_factory_version_tag_starts_it(self):
        self.assertIn("tags: ['factory/v*']", self.trigger)
        for never in ("branches:", "pull_request:", "workflow_dispatch:", "schedule:"):
            self.assertNotIn(never, self.trigger, f"{never} would build binaries outside a release")

    def test_the_gate_workflow_is_not_started_by_a_tag(self):
        ci = (ROOT / ".github" / "workflows" / "ci.yml").read_text()
        self.assertNotIn("tags:", ci.split("jobs:")[0])
        # The milestone release of the orchestrator tags vX.Y.Z and expects no workflow of its own.
        self.assertNotIn("factory/v", ci)

    def test_it_builds_a_static_binary_for_both_factory_host_architectures(self):
        self.assertIn("CGO_ENABLED=0 GOOS=linux", self.workflow)
        for arch in ("amd64", "arm64"):
            self.assertIn(f"dist/factory-linux-{arch}", self.workflow)
        self.assertIn("sha256sum factory-linux-* > checksums.txt", self.workflow)
        self.assertIn("dist/checksums.txt", self.workflow)
        # With the dashboard inside: the host runs one file and no Node (ADR 0033).
        self.assertIn("make ui", self.workflow)

    def test_the_agent_instructions_name_the_release_command(self):
        agents = (ROOT / "AGENTS.md").read_text()
        self.assertIn("scripts/release.sh factory --push", agents)
        self.assertIn("factory/VERSION", agents)
