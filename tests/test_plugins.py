import json
import re
import shutil
import subprocess
import unittest
from pathlib import Path

from helpers import PLANNER, ROOT, STANDARDS, ShimTest

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
        expected = {
            "orchestrator/agents/orchestrator.md": "sonnet",
            "worker/agents/worker.md": "opus",
            "planner/agents/planner.md": "fable",
            "worker/agents/docs-reviewer.md": "sonnet",
        }
        for rel, model in expected.items():
            fm = (ROOT / "plugins" / rel).read_text().split("---")[1]
            self.assertIn(f"model: {model}\n", fm, rel)
        for agent in (ROOT / "plugins/worker/agents").glob("*.md"):
            if f"worker/agents/{agent.name}" in expected:
                continue
            fm = agent.read_text().split("---")[1]
            self.assertIn("model: inherit\n", fm, agent)

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


if __name__ == "__main__":
    unittest.main()
