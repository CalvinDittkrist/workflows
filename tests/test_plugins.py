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

    def test_subagents_declare_a_model_so_the_readme_precedence_note_stays_true(self):
        # README documents that docs-reviewer keeps its own model while the rest follow the session.
        models = {a.stem: next((l.split(": ", 1)[1] for l in a.read_text().splitlines() if l.startswith("model: ")), None)
                  for plugin in PLUGINS for a in plugin.glob("agents/*.md")}
        self.assertEqual(models["docs-reviewer"], "sonnet")
        self.assertIsNone(models["worker"], "README says the worker agent sets no model")
        self.assertIsNone(models["orchestrator"], "README says the orchestrator agent sets no model")
        for name in ("code-reviewer", "security-reviewer", "senior-reviewer", "test-reviewer", "pr-author"):
            self.assertEqual(models[name], "inherit", name)

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
