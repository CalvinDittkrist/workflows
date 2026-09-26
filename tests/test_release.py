import fnmatch
import hashlib
import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from helpers import GIT_ISOLATION, ROOT

WORKFLOW = ROOT / ".github" / "workflows" / "factory-release.yml"


def step_script(workflow, name):
    """The shell of the step called `name`, as the runner would hand it to bash: the block under its
    `run: |`, without the indentation the YAML carries."""
    lines = workflow.read_text().splitlines()
    start = next(i for i, line in enumerate(lines) if line.strip() == f"- name: {name}")
    run = next(i for i in range(start + 1, len(lines)) if lines[i].strip() == "run: |")
    indent = len(lines[run]) - len(lines[run].lstrip()) + 2
    script = []
    for line in lines[run + 1:]:
        if line.strip() and not line.startswith(" " * indent):
            break
        script.append(line[indent:])
    return "\n".join(script)


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
        # A release is tagged on main, so the fixture has the origin the script asks what main points
        # at and which tags are taken. A bare repository next to it answers both without a network.
        self.origin = Path(self.tmp.name) / "origin.git"
        subprocess.run(["git", "init", "-q", "--bare", "-b", "main", str(self.origin)],
                       env=self.env(), check=True, text=True, capture_output=True)
        self.git("remote", "add", "origin", str(self.origin))
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

    def commit(self, push=True):
        """A commit a release can be tagged from, which means one that is on origin/main too.
        `push=False` leaves it where a maintainer's unpushed commit would be."""
        self.git("add", "-A")
        self.git("commit", "-qm", "release fixture")
        if push:
            self.git("push", "-q", "origin", "main")

    def release(self, *args):
        return subprocess.run(["bash", "scripts/release.sh", *args], cwd=self.repo,
                              env=self.env(), text=True, capture_output=True)

    def tags(self):
        return self.git("tag").split()

    def origin_tags(self):
        """The tags origin carries. An annotated tag is listed twice, the second time dereferenced
        to the commit it points at; the name is what matters here."""
        named = [line.split("refs/tags/")[1] for line in self.git("ls-remote", "--tags", "origin").splitlines()]
        return [tag for tag in named if not tag.endswith("^{}")]

    def test_the_factory_is_tagged_from_the_version_file_after_the_gate(self):
        r = self.release("factory")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("the gate ran", r.stdout)
        self.assertEqual(self.tags(), ["factory/v0.1.0"])
        # Annotated, and it says what it is: `git show` of the tag carries the message.
        self.assertIn("factory v0.1.0", self.git("tag", "-l", "-n1", "factory/v0.1.0"))
        # It names the commit that was reviewed, which is the whole of what stands behind a binary.
        self.assertEqual(self.git("rev-parse", "factory/v0.1.0^{}"), self.git("rev-parse", "HEAD"))
        # Nothing pushed without --push, and the command that would is printed.
        self.assertEqual(self.origin_tags(), [])
        self.assertIn("git push origin factory/v0.1.0", r.stdout)

    def test_push_puts_the_tag_on_origin(self):
        """`release.sh factory --push` is the command the agent instructions name, and the tag on
        origin is the whole trigger: nothing else makes CI build the binaries."""
        r = self.release("factory", "--push")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.origin_tags(), ["factory/v0.1.0"])

    def test_the_tag_is_neither_a_plugin_tag_nor_a_milestone_tag(self):
        """A version that would read as a milestone on its own: the namespace is what keeps the three
        kinds of tag apart, so it is read from a release of 1.2.3, not only of the fixture's 0.1.0."""
        self.version("1.2.3")
        self.commit()
        self.assertEqual(self.release("factory").returncode, 0)
        self.assertEqual(self.tags(), ["factory/v1.2.3"])  # not v1.2.3, and not <plugin>--v1.2.3

    def test_a_tag_that_is_here_and_not_on_origin_is_a_release_one_push_away(self):
        """Where a run without --push ends. Running it again must not say to bump the version: the
        tag already carries this one, and the release is a push away."""
        self.assertEqual(self.release("factory").returncode, 0)
        r = self.release("factory")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: the tag factory/v0.1.0 exists here and not on origin; "
                      "push it with: git push origin factory/v0.1.0", r.stderr)
        self.assertEqual(self.tags(), ["factory/v0.1.0"])

    def test_a_tag_that_exists_on_origin_is_refused_before_the_gate_runs(self):
        """A checkout that has not fetched for a while knows nothing of a tag another release made:
        without asking origin it would run the whole gate and only then fail on the push."""
        self.git("tag", "factory/v0.1.0")
        self.git("push", "-q", "origin", "factory/v0.1.0")
        self.git("tag", "-d", "factory/v0.1.0")
        r = self.release("factory")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: the tag factory/v0.1.0 exists on origin", r.stderr)
        self.assertNotIn("the gate ran", r.stdout)
        self.assertEqual(self.tags(), [])

    def test_a_commit_that_is_not_what_origin_main_points_at_is_refused(self):
        """The binaries are built from the tag and never gated again, so what stands behind them is
        that their commit went through a pull request onto main (docs/repo-standard.md)."""
        self.version("0.2.0")
        self.commit(push=False)
        r = self.release("factory")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn(f"error: HEAD is {self.git('rev-parse', 'HEAD').strip()} and origin/main is",
                      r.stderr)
        self.assertIn("releases are tagged on main", r.stderr)
        self.assertNotIn("the gate ran", r.stdout)
        self.assertEqual(self.tags(), [])

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

    def test_a_version_with_whitespace_inside_it_is_refused(self):
        """factory/version.go trims the ends of VERSION and nothing else, so `0. 1.0` is the version
        the binary reports. A release that quietly tagged v0.1.0 for it would name a version no
        binary and no run record ever says."""
        for said in ("0. 1.0", "0.1\n.0", "0.1.0 "):
            with self.subTest(said=said):
                (self.repo / "factory" / "VERSION").write_text(f"{said}\n")
                self.commit()
                r = self.release("factory")
                self.assertNotEqual(r.returncode, 0)
                self.assertIn("write the version as X.Y.Z on one line", r.stderr)
                self.assertEqual(self.tags(), [])

    def test_a_local_tag_on_an_older_commit_is_not_a_release_to_push(self):
        """CI asks whether the tagged commit is on main, not whether it is its tip, so a tag left
        over from an earlier attempt would release an older factory under this version. It is
        refused here, where the tag can still be deleted."""
        old = self.git("rev-parse", "HEAD").strip()
        self.git("tag", "-a", "factory/v0.1.0", "-m", "factory v0.1.0")
        (self.repo / "factory" / "run.go").write_text("// a commit the tag does not carry\n")
        self.commit()
        r = self.release("factory")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn(f"error: the tag factory/v0.1.0 exists here and names {old}", r.stderr)
        self.assertIn("git tag -d factory/v0.1.0", r.stderr)
        self.assertNotIn("git push origin factory/v0.1.0", r.stderr)
        self.assertNotIn("the gate ran", r.stdout)
        self.assertEqual(self.origin_tags(), [])

    def test_an_unknown_option_is_refused(self):
        r = self.release("factory", "--force")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: unknown option --force", r.stderr)
        self.assertEqual(self.tags(), [])


class FactoryBinariesTests(unittest.TestCase):
    """`scripts/factory-binaries.sh`: what a factory host downloads. The flags that build a released
    binary are written there and nowhere else, so the test runs it and reads what came out."""

    def test_it_builds_one_static_linux_binary_per_host_architecture(self):
        out = tempfile.TemporaryDirectory(prefix="wf-binaries-")
        self.addCleanup(out.cleanup)
        r = subprocess.run(["bash", "scripts/factory-binaries.sh", out.name],
                           cwd=ROOT, env=os.environ, text=True, capture_output=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        for arch, machine in (("amd64", "x86-64"), ("arm64", "aarch64")):
            with self.subTest(arch=arch):
                built = Path(out.name) / f"factory-linux-{arch}"
                described = subprocess.run(["file", "-b", str(built)], text=True,
                                           capture_output=True, check=True).stdout
                self.assertIn("ELF 64-bit", described)
                self.assertIn(machine, described)
                # No cgo, so the host it lands on needs no libc of the right version.
                self.assertIn("statically linked", described)

    def test_the_checksums_are_the_checksums_of_those_binaries(self):
        """What a host verifies its download against. A checksum file that names something else, or
        that is written before the build, is worth nothing."""
        out = tempfile.TemporaryDirectory(prefix="wf-binaries-")
        self.addCleanup(out.cleanup)
        subprocess.run(["bash", "scripts/factory-binaries.sh", out.name],
                       cwd=ROOT, env=os.environ, check=True, capture_output=True)
        said = {}
        for line in (Path(out.name) / "checksums.txt").read_text().splitlines():
            digest, name = line.split()
            said[name.lstrip("*")] = digest
        self.assertEqual(sorted(said), ["factory-linux-amd64", "factory-linux-arm64"])
        for name, digest in said.items():
            with self.subTest(name=name):
                self.assertEqual(hashlib.sha256((Path(out.name) / name).read_bytes()).hexdigest(), digest)

    def test_it_refuses_to_build_binaries_that_have_no_dashboard_in_them(self):
        """The embed pattern matches the committed placeholder, so a build without the dashboard in
        front of it comes out as a binary that answers 404 under /. The script says what to run
        instead of writing that file. The fixture is the script alone in an empty tree, which is what
        a checkout whose dashboard was never built looks like to it."""
        tmp = tempfile.TemporaryDirectory(prefix="wf-binaries-")
        self.addCleanup(tmp.cleanup)
        tree = Path(tmp.name)
        (tree / "scripts").mkdir()
        shutil.copy(ROOT / "scripts" / "factory-binaries.sh", tree / "scripts" / "factory-binaries.sh")
        out = tree / "dist"
        r = subprocess.run(["bash", "scripts/factory-binaries.sh", str(out)], cwd=tree,
                           env=os.environ, text=True, capture_output=True)
        self.assertNotEqual(r.returncode, 0, "it built binaries without a dashboard in them")
        self.assertIn("error:", r.stderr)
        self.assertIn("make binaries", r.stderr)
        self.assertFalse(out.exists(), "it wrote an output directory before it refused")



class PublishStepTests(unittest.TestCase):
    """The step that attaches the binaries to the release, run as GitHub runs it: `bash -e` with a
    `gh` on PATH. It is shell in a workflow file, so the test reads that shell out of the file and
    runs it; a step rewritten some other way stops being found and fails here."""

    ASSETS = ["dist/factory-linux-amd64", "dist/factory-linux-arm64", "dist/checksums.txt"]
    # A gh that says what the release already carries and writes down what it was asked to do. It
    # answers `release view` the way gh does: an unknown release is an error, not an empty answer.
    GH = """#!/usr/bin/env bash
echo "$*" >> "$GH_LOG"
case "$1 $2" in
  'release view') [ -n "${GH_ASSETS:-}" ] || { echo "release not found" >&2; exit 1; }; echo "$GH_ASSETS" ;;
  'release create') [ -z "${GH_ASSETS:-}" ] || { echo "a release with that tag already exists" >&2; exit 1; } ;;
esac
"""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="wf-publish-")
        self.addCleanup(self.tmp.cleanup)
        self.bin = Path(self.tmp.name) / "bin"
        self.bin.mkdir()
        gh = self.bin / "gh"
        gh.write_text(self.GH)
        gh.chmod(0o755)
        self.log = Path(self.tmp.name) / "gh.log"
        self.script = step_script(WORKFLOW, "Attach them to the release")

    def run_step(self, assets=None):
        env = {"PATH": f"{self.bin}:{os.environ['PATH']}", "GH_LOG": str(self.log),
               "GITHUB_REF_NAME": "factory/v0.1.0", "GH_TOKEN": "x", "GH_REPO": "o/r"}
        if assets is not None:
            env["GH_ASSETS"] = str(assets)
        return subprocess.run(["bash", "-e", "-c", self.script], cwd=self.tmp.name,
                              env=env, text=True, capture_output=True)

    def asked(self):
        return self.log.read_text() if self.log.exists() else ""

    def test_a_release_that_is_not_there_yet_is_created_with_the_binaries_on_it(self):
        r = self.run_step()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("release create factory/v0.1.0", self.asked())
        for asset in self.ASSETS:
            self.assertIn(asset, self.asked())

    def test_a_half_written_release_is_finished(self):
        """Why --clobber is there: a run whose upload died halfway is re-run, and the assets it did
        write are written again over themselves."""
        r = self.run_step(assets=1)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("release upload --clobber factory/v0.1.0", self.asked())

    def test_a_release_that_already_carries_its_binaries_is_never_overwritten(self):
        """What a host downloaded under a version stays what it downloaded. A re-run of a finished
        release (a moved tag, a re-run months later), would replace those files with others, so the
        job fails instead and a person decides."""
        r = self.run_step(assets=len(self.ASSETS))
        self.assertNotEqual(r.returncode, 0, "it overwrote a published release")
        self.assertIn("error:", r.stderr)
        self.assertNotIn("release create", self.asked())
        self.assertNotIn("release upload", self.asked())


class WorkflowTriggerTests(unittest.TestCase):
    """Which push starts which workflow. The binaries are built by a factory version tag and by
    nothing else, and a milestone tag of the orchestrator still starts nothing at all."""

    # One push per kind of ref this repository sees, and the workflows it has to start.
    STARTS = {
        "refs/tags/factory/v0.1.0": {"factory-release"},  # what scripts/release.sh factory creates
        "refs/tags/v1.2.3": set(),                        # a milestone the orchestrator releases
        "refs/tags/worker--v1.2.3": set(),                # a plugin release of `claude plugin tag`
        "refs/heads/main": {"ci"},
        "refs/heads/feat/61-something": set(),
    }

    def setUp(self):
        self.workflows = sorted((ROOT / ".github" / "workflows").glob("*.yml"))
        self.assertTrue(self.workflows, "no workflows to read")

    def test_a_push_starts_the_workflows_it_should_and_no_others(self):
        for ref, expected in self.STARTS.items():
            with self.subTest(ref=ref):
                started = {w.stem for w in self.workflows if starts(w, ref)}
                self.assertEqual(started, expected)

    def test_every_push_trigger_is_one_this_test_can_read(self):
        """The triggers are read as written, in brackets. A workflow that names its patterns some
        other way would pass the test above by matching nothing, so it fails here instead."""
        for workflow in self.workflows:
            with self.subTest(workflow=workflow.name):
                if "  push:" not in on_block(workflow):
                    continue
                self.assertTrue(patterns(workflow, "branches") or patterns(workflow, "tags"),
                                "the push trigger names no branch or tag pattern in brackets")

    def test_the_binaries_are_built_by_a_push_and_by_no_other_event(self):
        """A pull_request, schedule or workflow_dispatch trigger would build release binaries from
        something that is not a release."""
        events = [line.strip().rstrip(":") for line in on_block(WORKFLOW) if re.fullmatch(r"  \w+:", line)]
        self.assertEqual(events, ["push"])


def on_block(workflow):
    """The lines of a workflow's `on:` block: everything indented under it."""
    said = workflow.read_text().split("\non:\n", 1)
    if len(said) != 2:
        raise AssertionError(f"{workflow.name} writes its triggers in a way this test cannot read")
    lines = []
    for line in said[1].splitlines():
        if line and not line.startswith(" "):
            break
        lines.append(line)
    return lines


def patterns(workflow, kind):
    """The branch or tag patterns a push has to match to start the workflow."""
    found, under = [], None
    for line in on_block(workflow):
        if re.fullmatch(r"  \w+:", line):
            under = line.strip()
        elif under == "push:" and line.strip().startswith(f"{kind}: ["):
            found += re.findall(r"[^\s,'\"\[\]]+", line.split(":", 1)[1])
    return found


def starts(workflow, ref):
    """Whether pushing `ref` starts `workflow`. fnmatch stands in for GitHub's ref filter, which
    differs in one place: there `*` stops at a slash and `**` crosses it. No pattern here relies on
    that, and one that did would have to be matched properly."""
    kind, name = ("branches", ref[len("refs/heads/"):]) if ref.startswith("refs/heads/") \
        else ("tags", ref[len("refs/tags/"):])
    return any(fnmatch.fnmatch(name, said) for said in patterns(workflow, kind))


if __name__ == "__main__":
    unittest.main()
