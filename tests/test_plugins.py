import json
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from helpers import ORCH, PLANNER, ROOT, STANDARDS, WORKER, ShimTest

PLUGINS = sorted(p for p in (ROOT / "plugins").iterdir() if (p / ".claude-plugin/plugin.json").exists())


class ManifestTests(unittest.TestCase):
    def test_marketplace_lists_every_plugin_with_matching_names(self):
        market = json.loads((ROOT / ".claude-plugin/marketplace.json").read_text())
        listed = {p["name"]: p["source"] for p in market["plugins"]}
        for plugin in PLUGINS:
            manifest = json.loads((plugin / ".claude-plugin/plugin.json").read_text())
            self.assertEqual(manifest["name"], plugin.name)
            self.assertEqual(listed[plugin.name], f"./plugins/{plugin.name}")
            self.assertRegex(manifest["version"], r"^\d+\.\d+\.\d+$")

    def test_every_skill_and_agent_has_frontmatter_name_matching_its_file(self):
        for plugin in PLUGINS:
            for skill in plugin.glob("skills/*/SKILL.md"):
                fm = skill.read_text().split("---")[1]
                self.assertIn(f"name: {skill.parent.name}\n", fm, skill)
                self.assertIn("description:", fm, skill)
            for agent in plugin.glob("agents/*.md"):
                fm = agent.read_text().split("---")[1]
                self.assertIn(f"name: {agent.stem}\n", fm, agent)

    def test_agent_models_match_their_role(self):
        """The model of a session agent is a decision; every other agent inherits the session it serves."""
        expected = {
            "orchestrator/agents/orchestrator.md": "sonnet",
            "worker/agents/worker.md": "opus",
            "planner/agents/planner.md": "fable",
            "worker/agents/docs-reviewer.md": "sonnet",
            "worker/agents/docs-lookup.md": "sonnet",
        }
        agents = sorted(ROOT.glob("plugins/*/agents/*.md"))
        self.assertLessEqual(set(expected), {a.relative_to(ROOT / "plugins").as_posix() for a in agents})
        for agent in agents:
            rel = agent.relative_to(ROOT / "plugins").as_posix()
            found = re.search(r"^model: (.+)$", agent.read_text().split("---")[1], re.M)
            self.assertIsNotNone(found, rel)
            # claude plugin validate accepts any string here, so a typo like "fabel" is only caught by this.
            self.assertIn(found.group(1), {"fable", "opus", "sonnet", "haiku", "inherit"}, rel)
            self.assertEqual(found.group(1), expected.get(rel, "inherit"), rel)

    def assert_read_only(self, agent):
        """An agent that only judges: no edit tool and no agent tool, neither granted nor reachable."""
        fields = dict(line.split(": ", 1) for line in agent.read_text().split("---")[1].strip().splitlines())
        self.assertEqual(fields["tools"].split(", "), ["Read", "Grep", "Glob", "Bash"], agent)
        self.assertTrue({"Edit", "Write", "NotebookEdit", "Agent"} <= set(fields["disallowedTools"].split(", ")), agent)
        self.assertNotIn("mcpServers", fields, agent)

    def test_every_auditor_is_read_only_by_its_declared_tools(self):
        agents = ROOT / "plugins/repo-standards/agents"
        names = {"files", "agent-config", "docs", "tests-ci", "workspace", "security"}
        self.assertEqual({p.stem for p in agents.glob("*.md")}, {f"{n}-auditor" for n in names})
        for agent in agents.glob("*.md"):
            self.assert_read_only(agent)

    def test_the_spec_checker_is_read_only_by_its_declared_tools(self):
        self.assert_read_only(ROOT / "plugins/planner/agents/spec-checker.md")

    def test_the_documentation_lookup_is_read_only_by_its_declared_tools(self):
        self.assert_read_only(ROOT / "plugins/worker/agents/docs-lookup.md")

    def test_the_worker_reaches_the_documentation_through_its_script_and_not_through_the_web_tools(self):
        """The worker's main context holds issue text written by someone else, so its own tool list carries
        no free web access; the documentation arrives through the pinned script and a lookup subagent
        (issue #44, ADR 0030). A subagent with its own tool list does get WebFetch, so this is surface
        reduction in the context that reads untrusted text, not a network boundary."""
        worker = ROOT / "plugins/worker/agents/worker.md"
        tools = self.declared_tools(worker)
        for tool in ("WebFetch", "WebSearch"):
            self.assertNotIn(tool, tools,
                             f"{worker.name} lists {tool}; the documentation is read with /worker:docs, which "
                             f"runs claude-docs.sh in a lookup subagent")

    def declared_tools(self, agent):
        """The tools of an agent file, each name mapped to its specifier list: `Agent(a, b)` is
        {"Agent": ["a", "b"]}, a bare `Bash` is {"Bash": None}."""
        declared = re.search(r"^tools: (.+)$", agent.read_text().split("---")[1], re.M)
        self.assertIsNotNone(declared, f"{agent.name} declares no tools")
        # Split on the comma alone, outside parentheses: a tool written without the space after it is
        # still a granted tool, and the commas of a specifier list belong to their tool.
        tools = {}
        for entry in re.split(r",(?![^(]*\))", declared.group(1)):
            name, _, inner = entry.strip().partition("(")
            tools[name] = [item.strip() for item in inner.rstrip(")").split(",")] if inner else None
        return tools

    def test_the_worker_spawns_its_own_subagents_and_no_other_type(self):
        """A subagent's declared tools are granted, not intersected with the worker's, so a bare `Agent`
        hands the worker every built-in type, the ones with `WebFetch` and `WebSearch` among them
        (`general-purpose`, `claude-code-guide`). The allowlist names the plugin's own subagents and
        nothing else (ADR 0030). A type missing from it fails at the Agent call, so a new agent file
        has to be listed here to be reachable at all."""
        agents = ROOT / "plugins/worker/agents"
        own = {f"worker:{a.stem}" for a in agents.glob("*.md")} - {"worker:worker"}
        allowed = self.declared_tools(agents / "worker.md").get("Agent")
        self.assertIsNotNone(allowed, "worker.md has no Agent(...) allowlist; a bare Agent spawns every built-in type")
        self.assertEqual(sorted(allowed), sorted(own))

    def test_every_planner_skill_is_user_invoked_only(self):
        skills = sorted(p.parent.name for p in (ROOT / "plugins/planner/skills").glob("*/SKILL.md"))
        self.assertIn("accept", skills)
        for skill in skills:
            fm = (ROOT / f"plugins/planner/skills/{skill}/SKILL.md").read_text().split("---")[1]
            self.assertIn("disable-model-invocation: true\n", fm, skill)

    def test_the_standardisation_run_is_user_invoked_only(self):
        for skill in ("standardize", "apply"):
            fm = (ROOT / f"plugins/repo-standards/skills/{skill}/SKILL.md").read_text().split("---")[1]
            self.assertIn("disable-model-invocation: true\n", fm, skill)

    def test_every_inline_command_in_a_skill_is_pre_approved(self):
        # A forked skill's !`command` fails silently without a matching allowed-tools rule (verified on 2.1.274).
        for plugin in PLUGINS:
            for skill in plugin.glob("skills/*/SKILL.md"):
                fm, body = skill.read_text().split("---")[1:3]
                commands = re.findall(r"!`([^`]+)`", body)
                if not commands:
                    continue
                rules = re.findall(r"Bash\(([^)]+)\)", fm)
                for cmd in commands:
                    script = cmd.split()[0]
                    self.assertTrue(script.startswith("${CLAUDE_PLUGIN_ROOT}/scripts/"), f"{skill}: {cmd} must be a plugin script")
                    self.assertTrue(any(script == r.rstrip("*") for r in rules), f"{skill}: no allowed-tools rule for {cmd}")

    def test_scripts_referenced_by_skills_and_hooks_exist_and_are_executable(self):
        for plugin in PLUGINS:
            texts = [p.read_text() for p in plugin.glob("skills/*/SKILL.md")]
            hooks = plugin / "hooks/hooks.json"
            if hooks.exists():
                texts.append(hooks.read_text())
            for text in texts:
                for name in re.findall(r"\$\{CLAUDE_PLUGIN_ROOT\}/scripts/([\w.-]+)", text):
                    script = plugin / "scripts" / name
                    self.assertTrue(script.exists(), script)
                    self.assertTrue(script.stat().st_mode & 0o111, f"{script} not executable")

    @unittest.skipUnless(shutil.which("claude"), "claude CLI not installed")
    def test_claude_plugin_validate_strict(self):
        for path in [ROOT, *PLUGINS]:
            r = subprocess.run(["claude", "plugin", "validate", str(path), "--strict"], text=True, capture_output=True)
            self.assertEqual(r.returncode, 0, f"{path}\n{r.stdout}{r.stderr}")


class FactoryGateTests(unittest.TestCase):
    """`factory-go`, the Go part of `make factory`: a tool it needs and cannot find is named with its fix."""

    def gate(self, *tools):
        # The recipe runs with nothing on PATH but the named tools, each a stub that succeeds and prints nothing.
        # It is the Go part alone: `make factory` builds the dashboard first, which needs an npm this PATH has not.
        with tempfile.TemporaryDirectory() as path:
            for tool in tools:
                stub = Path(path) / tool
                stub.write_text("#!/bin/sh\nexit 0\n")
                stub.chmod(0o755)
            return subprocess.run([shutil.which("make"), "factory-go"], cwd=ROOT, env={"PATH": path, "HOME": path},
                                  text=True, capture_output=True)

    def test_a_missing_go_is_named_with_the_fix(self):
        r = self.gate()
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: go not installed; brew install go", r.stderr)

    def test_a_gofmt_that_cannot_run_fails_the_gate_instead_of_passing_it(self):
        r = self.gate("go")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: gofmt could not run", r.stderr)

    def test_a_missing_staticcheck_is_named_with_the_pinned_install(self):
        r = self.gate("go", "gofmt")
        self.assertNotEqual(r.returncode, 0)
        named = re.search(r"error: staticcheck not installed; go install honnef\.co/go/tools/cmd/staticcheck@([0-9.]+)", r.stderr)
        self.assertIsNotNone(named, r.stderr)
        # Local and CI findings match only while both run the same version.
        ci = (ROOT / ".github" / "workflows" / "ci.yml").read_text()
        self.assertIn(f"go install honnef.co/go/tools/cmd/staticcheck@{named.group(1)}\n", ci)


class ShimCallLogTests(ShimTest):
    """The harness itself: what the shims log has to be what a test reads back."""

    def test_an_argument_with_a_newline_stays_one_logged_call(self):
        # release.sh passes a multi-line --body, so without escaping the log would read back as two calls.
        subprocess.run(["gh", "pr", "create", "--title", "t", "--body", "one\ntwo"], env=self.env(), capture_output=True)
        self.assertEqual(self.argv_calls(), [["gh", "pr", "create", "--title", "t", "--body", "one\ntwo"]])


class LabelVocabularyTests(ShimTest):
    """repo-standards and planner each define the label vocabulary; drift between the copies is a bug."""

    # The two files that define the vocabulary, and the one label repo-standards has that the planner has not.
    STANDARDS_FILE = str((STANDARDS / "lib.sh").relative_to(ROOT))
    PLANNER_FILE = str((PLANNER / "labels.sh").relative_to(ROOT))
    PRIVATE = "skill-candidate"
    # The whole point of the test is the failure message, so it prints the differing label, not an elision.
    maxDiff = None

    def standards_vocabulary(self):
        """WF_LABELS as workspace.sh feeds it into its label loop. Sourced outside a git repository, because
        reading the vocabulary must not need one. Split like every shell reader of the value: a pipe in the
        description belongs to the description."""
        r = subprocess.run(["bash", "-c", r'. "$1/lib.sh"; printf "%s\n" "$WF_LABELS"', "_", str(STANDARDS)],
                           cwd=self.base, text=True, capture_output=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        vocabulary = []
        for line in r.stdout.splitlines():
            if not line:
                continue
            entry = tuple(line.split("|", 2))
            self.assertEqual(len(entry), 3, f"{self.STANDARDS_FILE} has a label that is not name|color|description: {line!r}")
            vocabulary.append(entry)
        return vocabulary

    def planner_vocabulary(self):
        """The labels labels.sh creates in a repository that has none, with the colour and description it gives them."""
        r = self.run_script(PLANNER / "labels.sh", SHIM_NO_LABELS="1")
        self.assertEqual(r.returncode, 0, r.stderr)
        vocabulary = []
        for call in self.argv_calls():
            if call[1:3] != ["label", "create"]:
                continue
            name, options = call[3], call[4:]
            self.assertEqual(options[0::2], ["--color", "--description"],
                             f"{self.PLANNER_FILE} creates {name} with other options than this test reads: {options}")
            vocabulary.append((name, options[1], options[3]))
        return vocabulary

    def test_the_two_definitions_of_the_label_vocabulary_are_identical(self):
        standards, planner = self.standards_vocabulary(), self.planner_vocabulary()
        self.assertTrue(standards, f"no labels read from {self.STANDARDS_FILE}")
        self.assertTrue(planner, f"no labels created by {self.PLANNER_FILE}")
        names = [name for name, _, _ in standards]
        self.assertIn(self.PRIVATE, names, f"{self.STANDARDS_FILE} no longer defines {self.PRIVATE}, the one label "
                      f"{self.PLANNER_FILE} is allowed to omit; decide what this test should exempt instead")
        self.assertNotIn(self.PRIVATE, [name for name, _, _ in planner],
                         f"{self.PLANNER_FILE} creates {self.PRIVATE}, which belongs to {self.STANDARDS_FILE} alone")
        self.assertEqual([entry for entry in standards if entry[0] != self.PRIVATE], planner,
                         f"the label vocabulary of {self.STANDARDS_FILE} (WF_LABELS, minus {self.PRIVATE}) and of "
                         f"{self.PLANNER_FILE} differ in name, colour, description or order. One of the two copies "
                         f"was changed and the other has to follow; do not adjust this test.")

    def routing_label(self, scripts):
        """WF_ROUTING_LABEL as the scripts of one plugin read it, sourced outside a git repository."""
        file = str((scripts / "lib.sh").relative_to(ROOT))
        r = subprocess.run(["bash", "-c", r'. "$1/lib.sh"; printf "%s\n" "$WF_ROUTING_LABEL"', "_", str(scripts)],
                           cwd=self.base, text=True, capture_output=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(r.stdout.strip(), f"{file} defines no WF_ROUTING_LABEL")
        return r.stdout.strip()

    def test_the_routing_label_the_local_claim_refuses_is_in_the_vocabulary(self):
        """claim.sh refuses a routed issue by the name in WF_ROUTING_LABEL, and the planner sets the label by
        that name; a rename in the vocabulary that leaves either behind would let a local claim take an issue
        the factory owns, or route an issue by a name the factory never reads."""
        routing = self.routing_label(ORCH)
        self.assertEqual(routing, self.routing_label(PLANNER),
                         "the orchestrator refuses and the planner sets two different routing labels")
        for file, vocabulary in ((self.STANDARDS_FILE, self.standards_vocabulary()),
                                 (self.PLANNER_FILE, self.planner_vocabulary())):
            self.assertIn(routing, [name for name, _, _ in vocabulary],
                          f"claim.sh refuses the label {routing}, which {file} does not define")


class ContextValueContractTests(ShimTest):
    """The context value file is the only thing the orchestrator and the worker share (ADR 0020): the status
    line of the pane writes it, the worker's checkpoint reads it, and no code crosses between the plugins."""

    def test_the_status_line_writes_what_the_checkpoint_reads(self):
        payload = json.dumps({"cwd": str(self.repo), "context_window": {"total_input_tokens": 130000, "context_window_size": 200000}})
        written = self.run_script(ORCH / "statusline.sh", stdin=payload, WF_ISSUE="12", WF_MODE="manual")
        self.assertEqual(written.returncode, 0, written.stderr)
        self.assertIn("130k/200k", written.stdout)
        read = self.run_script(WORKER / "checkpoint.sh", WF_ISSUE="12")
        self.assertEqual(read.returncode, 0, read.stderr)
        self.assertIn("context_tokens: 130000", read.stdout)
        self.assertIn("handoff: yes", read.stdout)


if __name__ == "__main__":
    unittest.main()
