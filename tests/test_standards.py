import json
import subprocess
import unittest

from helpers import STANDARDS, ShimTest


class StandardsTests(ShimTest):
    def scaffold(self):
        r = self.run_script(STANDARDS / "scaffold.sh")
        self.assertEqual(r.returncode, 0, r.stderr)
        return r

    def check(self, root=None):
        return self.run_script(STANDARDS / "check.sh", *([str(root)] if root else []))

    def write(self, path, text=""):
        p = self.repo / path
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)

    def test_scaffold_then_check_passes_and_never_overwrites(self):
        r = self.check()
        self.assertEqual(r.returncode, 1)
        self.assertIn("fail: docs/architecture.md missing", r.stdout)
        r = self.scaffold()
        for f in ("AGENTS.md", "CLAUDE.md", "Makefile", "docs/architecture.md", ".claude/settings.json"):
            self.assertIn(f"created: {f}", r.stdout)
        settings = json.loads((self.repo / ".claude/settings.json").read_text())
        self.assertTrue(settings["enabledPlugins"]["worker@workflows"])
        self.assertEqual(settings["attribution"]["commit"], "")
        (self.repo / "CLAUDE.md").write_text("# mine\n@AGENTS.md\n")
        r = self.scaffold()
        self.assertIn("kept: CLAUDE.md", r.stdout)
        self.assertEqual((self.repo / "CLAUDE.md").read_text(), "# mine\n@AGENTS.md\n")
        r = self.check()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("result: pass", r.stdout)
        self.assertIn("warn: Makefile still has <fill in> placeholders", r.stdout)

    def test_scaffold_enables_the_workflow_plugins_through_the_plugin_commands_and_disables_the_rest(self):
        self.write(".claude/settings.json", json.dumps({"enabledPlugins": {"foo@bar": True, "old@bar": False},
                                                        "env": {"WF_REVIEW_ROUNDS": "5"},
                                                        "permissions": {"allow": ["Bash(make *)", "Bash(git diff *)"]}}))
        r = self.scaffold()
        self.assertIn("updated: .claude/settings.json", r.stdout)
        self.assertIn("claude plugin marketplace add CalvinDittkrist/workflows --scope project", self.calls())
        self.assertIn("claude plugin install worker@workflows --scope project", self.calls())
        self.assertIn("claude plugin disable foo@bar --scope project", self.calls())
        self.assertNotIn("claude plugin disable old@bar --scope project", self.calls())
        settings = json.loads((self.repo / ".claude/settings.json").read_text())
        self.assertEqual(sorted(p for p, on in settings["enabledPlugins"].items() if on),
                         ["orchestrator@workflows", "planner@workflows", "repo-standards@workflows", "worker@workflows"])
        self.assertEqual(settings["env"]["WF_REVIEW_ROUNDS"], "5")
        self.assertEqual(settings["permissions"]["allow"][:3], ["Bash(make *)", "Bash(git diff *)", "Bash(git status *)"])
        self.assertEqual(settings["permissions"]["allow"].count("Bash(git diff *)"), 1)
        self.reset_calls()
        r = self.scaffold()
        self.assertIn("kept: .claude/settings.json", r.stdout)
        self.assertEqual([c for c in self.calls() if not c.startswith("claude plugin marketplace add")], [])

    def test_scaffold_skips_the_files_of_a_category(self):
        r = self.run_script(STANDARDS / "scaffold.sh", "--skip", "agent-config", "--skip", "docs")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual([line for line in r.stdout.splitlines() if not line.startswith("next:")],
                         ["created: Makefile", "created: .github/dependabot.yml", "created: .github/workflows/check.yml"])
        self.assertFalse((self.repo / ".claude").exists())
        self.assertEqual(self.calls(), [])
        r = self.run_script(STANDARDS / "scaffold.sh", "--skip", "nonsense")
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: unknown category nonsense", r.stderr)

    def test_scaffold_names_the_repository_and_runs_check_on_the_branches_of_the_model(self):
        r = self.run_script(STANDARDS / "scaffold.sh", "--name", "shop & co", "--default", "dev")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue((self.repo / "AGENTS.md").read_text().startswith("# shop & co\n"))
        self.assertIn("    branches: [main, dev]\n", (self.repo / ".github/workflows/check.yml").read_text())

    def test_scaffold_adds_the_ci_job_check_only_when_no_workflow_has_one(self):
        self.write(".github/workflows/ci.yml", "on: push\njobs:\n  gate:\n    name: check\n    runs-on: ubuntu-latest\n")
        r = self.scaffold()
        self.assertIn("kept: .github/workflows/ci.yml (has the job check)", r.stdout)
        self.assertFalse((self.repo / ".github/workflows/check.yml").exists())

    def test_scaffolded_gate_fails_until_filled_in(self):
        self.scaffold()
        r = subprocess.run(["make", "check"], cwd=self.repo, text=True, capture_output=True)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: the check target is a placeholder", r.stderr)

    def test_scaffold_keeps_the_makefile_and_pr_template_under_their_other_names(self):
        self.write("makefile", "check:\n\ttrue\n")
        self.write(".github/pull_request_template.md", "mine\n")
        r = self.scaffold()
        self.assertIn("kept: makefile", r.stdout)
        self.assertIn("kept: .github/pull_request_template.md", r.stdout)
        names = {p.name for p in self.repo.iterdir()} | {p.name for p in (self.repo / ".github").iterdir()}
        self.assertNotIn("Makefile", names)
        self.assertNotIn("PULL_REQUEST_TEMPLATE.md", names)
        r = self.check()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("ok: makefile has a check target", r.stdout)
        self.assertIn("ok: .github/pull_request_template.md", r.stdout)

    def test_check_fails_without_agents_md(self):
        self.scaffold()
        (self.repo / "AGENTS.md").unlink()
        r = self.check()
        self.assertEqual(r.returncode, 1)
        self.assertIn("fail: AGENTS.md missing", r.stdout)

    def test_check_fails_when_claude_md_is_missing_or_does_not_import_agents_md(self):
        self.scaffold()
        (self.repo / "CLAUDE.md").write_text("# rules\nsee AGENTS.md\n")
        r = self.check()
        self.assertEqual(r.returncode, 1)
        self.assertIn("fail: CLAUDE.md does not import AGENTS.md", r.stdout)
        (self.repo / "CLAUDE.md").unlink()
        r = self.check()
        self.assertEqual(r.returncode, 1)
        self.assertIn("fail: CLAUDE.md missing", r.stdout)
        (self.repo / "CLAUDE.md").write_text("@./AGENTS.md\n\n## Claude only\n- one line\n")
        self.assertEqual(self.check().returncode, 0)

    def test_check_holds_every_area_pair_in_a_monorepo(self):
        self.scaffold()
        self.write("services/api/CLAUDE.md", "# api\n")
        self.write("docs/.hidden/CLAUDE.md", "not an area\n")
        r = self.check()
        self.assertEqual(r.returncode, 1)
        self.assertIn("fail: services/api/AGENTS.md missing", r.stdout)
        self.assertIn("fail: services/api/CLAUDE.md does not import AGENTS.md", r.stdout)
        self.assertNotIn("docs/.hidden/AGENTS.md", r.stdout)
        self.write("services/api/AGENTS.md", "# api\n")
        self.write("services/api/CLAUDE.md", "@AGENTS.md\n")
        r = self.check()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("ok: services/api/CLAUDE.md imports AGENTS.md", r.stdout)

    def test_check_fails_without_a_check_target(self):
        self.scaffold()
        for text in ("lint:\n\ttrue\n", ".PHONY: check\ncheck-docs:\n\ttrue\ncheck := 1\n"):
            (self.repo / "Makefile").write_text(text)
            r = self.check()
            self.assertEqual(r.returncode, 1, text)
            self.assertIn("fail: no check target in a Makefile", r.stdout)
        (self.repo / "Makefile").write_text("lint check: deps\n\ttrue\n")
        self.assertEqual(self.check().returncode, 0)
        (self.repo / "Makefile").unlink()
        self.assertIn("fail: no check target in a Makefile", self.check().stdout)

    def test_check_warns_on_instruction_files_over_200_lines(self):
        self.scaffold()
        self.write("AGENTS.md", "- rule\n" * 201)
        self.write("CLAUDE.md", "@AGENTS.md\n" + "- claude\n" * 200)
        r = self.check()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("warn: AGENTS.md has 201 lines (>200)", r.stdout)
        self.assertIn("warn: CLAUDE.md has 201 lines (>200)", r.stdout)

    def test_check_names_each_path_of_agent_configuration_the_standard_does_not_define(self):
        self.scaffold()
        for path in (".claude/skills/tdd/SKILL.md", ".claude/skills/tdd/ref.md", ".claude/skills/grill/SKILL.md",
                     ".claude/commands/ship.md", ".claude/agents/reviewer.md", ".claude/rules/api.md",
                     ".cursor/rules/style.mdc", ".cursorrules", ".agents/skills/triage/SKILL.md", ".codex/config.toml",
                     ".github/copilot-instructions.md", "GEMINI.md", "skills-lock.json", "web/.windsurf/rules.md"):
            self.write(path, "x\n")
        self.write(".gitignore", "ignored/\n")
        self.write("ignored/.claude/skills/x/SKILL.md", "x\n")
        r = self.check()
        self.assertEqual(r.returncode, 1)
        named = sorted(line.split(": ")[1] for line in r.stdout.splitlines() if "the standard does not define" in line)
        self.assertEqual(named, sorted([
            ".claude/skills/tdd", ".claude/skills/grill", ".claude/commands/ship.md", ".claude/agents/reviewer.md",
            ".claude/rules/api.md", ".cursor/rules/style.mdc", ".cursorrules", ".agents/skills/triage",
            ".codex/config.toml", ".github/copilot-instructions.md", "GEMINI.md", "skills-lock.json",
            "web/.windsurf/rules.md"]))

    def test_check_counts_tracked_files_and_works_outside_git(self):
        self.scaffold()
        self.write(".claude/commands/ship.md", "x\n")
        self.git("add", "-A")
        self.git("commit", "-qm", "baseline")
        r = self.check()
        self.assertEqual(r.returncode, 1)
        self.assertIn("fail: .claude/commands/ship.md:", r.stdout)
        plain = self.base / "plain"
        r = self.run_script(STANDARDS / "scaffold.sh", str(plain))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.check(plain).returncode, 0)
        (plain / ".claude/skills/x").mkdir(parents=True)
        (plain / ".claude/skills/x/SKILL.md").write_text("x\n")
        self.assertIn("fail: .claude/skills/x:", self.check(plain).stdout)

    def test_new_adr_numbers_sequentially_and_indexes(self):
        self.scaffold()
        r1 = self.run_script(STANDARDS / "new-adr.sh", "Use", "Postgres")
        r2 = self.run_script(STANDARDS / "new-adr.sh", "Drop Redis!")
        self.assertIn("created: docs/adr/0001-use-postgres.md", r1.stdout)
        self.assertIn("created: docs/adr/0002-drop-redis.md", r2.stdout)
        adr = (self.repo / "docs/adr/0001-use-postgres.md").read_text()
        self.assertIn("# 0001. Use Postgres", adr)
        self.assertIn("Status: proposed", adr)
        index = (self.repo / "docs/adr/README.md").read_text()
        self.assertIn("| [0002](0002-drop-redis.md) | Drop Redis! | proposed |", index)
        r = self.check()
        self.assertIn("ok: ADRs: 2", r.stdout)

    def test_new_adr_cuts_a_long_title_without_a_trailing_hyphen(self):
        self.scaffold()
        r = self.run_script(STANDARDS / "new-adr.sh", "make check is the single gate and check the single required status check")
        self.assertIn("created: docs/adr/0001-make-check-is-the-single-gate-and-check-the-single-required.md", r.stdout)


if __name__ == "__main__":
    unittest.main()
