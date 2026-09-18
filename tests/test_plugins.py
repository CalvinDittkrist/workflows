import json
import re
import shutil
import subprocess
import unittest
from pathlib import Path

from helpers import ROOT

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
            "planner/agents/planner.md": "opus",
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

    def test_every_auditor_is_read_only_by_its_declared_tools(self):
        agents = ROOT / "plugins/repo-standards/agents"
        names = {"files", "agent-config", "docs", "tests-ci", "workspace", "security"}
        self.assertEqual({p.stem for p in agents.glob("*.md")}, {f"{n}-auditor" for n in names})
        for agent in agents.glob("*.md"):
            fields = dict(line.split(": ", 1) for line in agent.read_text().split("---")[1].strip().splitlines())
            self.assertEqual(fields["tools"].split(", "), ["Read", "Grep", "Glob", "Bash"], agent)
            self.assertTrue({"Edit", "Write", "NotebookEdit", "Agent"} <= set(fields["disallowedTools"].split(", ")), agent)
            self.assertNotIn("mcpServers", fields, agent)

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


if __name__ == "__main__":
    unittest.main()
