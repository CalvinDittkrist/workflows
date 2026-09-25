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
        self.assertEqual(settings["env"]["WF_REVIEW_ROUNDS"], "5", "a value the repository set is kept")
        self.assertEqual(settings["env"]["WF_PROJECT_TEMPLATE"], "", "the project template has a place to be set in")
        self.assertEqual(settings["permissions"]["allow"][:3], ["Bash(make *)", "Bash(git diff *)", "Bash(git status *)"])
        self.assertEqual(settings["permissions"]["allow"].count("Bash(git diff *)"), 1)
        self.reset_calls()
        r = self.scaffold()
        self.assertIn("kept: .claude/settings.json", r.stdout)
        self.assertEqual([c for c in self.calls() if not c.startswith("claude plugin marketplace add")], [])

    def test_the_scaffolded_categories_are_the_ones_the_report_names(self):
        """WF_SCAFFOLD_CATEGORIES (lib.sh) is what report.sh promises; scaffold.sh is what really writes files.
        One run per category, everything else skipped, so a put call added or moved shows up here."""
        def var(name):
            return subprocess.run(["bash", "-c", f'. "{STANDARDS / "lib.sh"}"; printf "%s" "${name}"'],
                                  capture_output=True, text=True, check=True).stdout.split()
        every, scaffolded = var("WF_CATEGORIES"), var("WF_SCAFFOLD_CATEGORIES")
        self.assertTrue(set(scaffolded) <= set(every), scaffolded)
        for c in every:
            root = self.base / f"scaffold-{c}"
            root.mkdir()
            skips = [a for other in every if other != c for a in ("--skip", other)]
            r = self.run_script(STANDARDS / "scaffold.sh", *skips, str(root))
            self.assertEqual(r.returncode, 0, r.stderr)
            written = sorted(str(f.relative_to(root)) for f in root.rglob("*") if f.is_file())
            self.assertEqual(bool(written), c in scaffolded, f"{c} alone wrote {written}")
            # The settings file is the agent-config part of the scaffold, which the report names on its own.
            self.assertEqual(".claude/settings.json" in written, c == "agent-config", f"{c} alone wrote {written}")

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
                     ".github/copilot-instructions.md", "GEMINI.md", "skills-lock.json", "web/.windsurf/rules.md",
                     "CLAUDE.local.md", "api/AGENT.md", ".rules", ".worktreeinclude"):
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
            "web/.windsurf/rules.md", "CLAUDE.local.md", "api/AGENT.md", ".rules", ".worktreeinclude"]))

    def test_check_fails_without_a_readme_or_a_ci_job_named_check(self):
        self.scaffold()
        (self.repo / "README.md").unlink()
        (self.repo / ".github/workflows/check.yml").unlink()
        self.write(".github/workflows/nested/ci.yml", "jobs:\n  check:\n    runs-on: ubuntu-latest\n")
        self.write(".github/workflows/test.yml", "jobs:\n  test:\n    runs-on: ubuntu-latest\n")
        r = self.check()
        self.assertEqual(r.returncode, 1)
        self.assertIn("fail: README.md missing", r.stdout)
        self.assertIn("fail: no CI job named check in .github/workflows", r.stdout)
        self.write("README.rst", "shop\n")
        self.write(".github/workflows/test.yml", "jobs:\n  test:\n    name: check\n    runs-on: ubuntu-latest\n")
        r = self.check()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("ok: README.rst", r.stdout)
        self.assertIn("ok: .github/workflows/test.yml has the CI job check", r.stdout)

    def test_scaffold_creates_a_readme_only_when_none_exists_and_check_wants_it_filled_in(self):
        (self.repo / "README.md").unlink()
        self.write("README.rst", "shop\n")
        r = self.scaffold()
        self.assertIn("kept: README.rst", r.stdout)
        self.assertFalse((self.repo / "README.md").exists())
        (self.repo / "README.rst").unlink()
        r = self.run_script(STANDARDS / "scaffold.sh", "--skip", "docs")
        self.assertNotIn("README", r.stdout)
        r = self.scaffold()
        self.assertIn("created: README.md", r.stdout)
        self.assertTrue((self.repo / "README.md").read_text().startswith(f"# {self.repo.name}\n"))
        r = self.check()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("warn: README.md still has <fill in> placeholders", r.stdout)

    def test_check_warns_without_a_glossary_or_grouped_version_updates(self):
        self.scaffold()
        (self.repo / "docs/glossary.md").unlink()
        (self.repo / ".github/dependabot.yml").unlink()
        r = self.check()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("warn: docs/glossary.md missing", r.stdout)
        self.assertIn("warn: .github/dependabot.yml missing", r.stdout)

    def test_a_public_repository_needs_a_licence_and_a_security_policy(self):
        self.scaffold()
        ws = self.base / "github"
        ws.mkdir()
        (ws / "repo.json").write_text(json.dumps({"visibility": "public", "default_branch": "main"}))
        r = self.run_script(STANDARDS / "check.sh", SHIM_WS=str(ws))
        self.assertEqual(r.returncode, 1)
        self.assertIn("fail: LICENSE missing", r.stdout)
        self.assertIn("fail: SECURITY.md missing", r.stdout)
        self.write("LICENSE", "MIT\n")
        self.write(".github/SECURITY.md", "report privately\n")
        r = self.run_script(STANDARDS / "check.sh", SHIM_WS=str(ws))
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("ok: .github/SECURITY.md", r.stdout)
        (self.repo / "LICENSE").unlink()
        (ws / "repo.json").write_text(json.dumps({"visibility": "private", "default_branch": "main"}))
        r = self.run_script(STANDARDS / "check.sh", SHIM_WS=str(ws))
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertNotIn("LICENSE", r.stdout)
        r = self.check()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("skip: licence and security policy not checked (visibility unknown without GitHub)", r.stdout)

    def test_check_holds_the_settings_to_the_workflow_plugins_without_hooks(self):
        self.scaffold()
        settings = json.loads((self.repo / ".claude/settings.json").read_text())
        settings["enabledPlugins"].update({"planner@workflows": False, "foo@bar": True, "old@bar": False, "a *": True, "worker@workflow": True})
        settings.update({"enabledMcpjsonServers": [], "enableAllProjectMcpServers": False})
        self.write(".claude/settings.json", json.dumps(settings))
        self.assertNotIn("MCP", self.check().stdout)
        settings["enabledMcpjsonServers"] = ["db"]
        self.write(".claude/settings.json", json.dumps(settings))
        r = self.check()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("warn: planner@workflows not enabled in .claude/settings.json", r.stdout)
        self.assertIn("warn: foo@bar enabled at project scope; the standard enables only the workflow plugins", r.stdout)
        self.assertNotIn("old@bar", r.stdout)
        self.assertIn("warn: a * enabled at project scope", r.stdout)
        self.assertIn("warn: worker@workflow enabled at project scope", r.stdout)
        self.assertIn("warn: .claude/settings.json enables MCP servers", r.stdout)
        settings["hooks"] = {"Stop": []}
        self.write(".claude/settings.json", json.dumps(settings))
        r = self.check()
        self.assertEqual(r.returncode, 1)
        self.assertIn("fail: .claude/settings.json has hooks", r.stdout)

    def test_mcp_configuration_warns_and_an_ai_reviewer_action_fails(self):
        self.scaffold()
        self.write(".mcp.json", "{}\n")
        r = self.check()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("warn: .mcp.json: MCP configuration; keep it only if something in the repository uses it", r.stdout)
        self.write(".github/workflows/review.yml", "on: pull_request\njobs:\n  review:\n    runs-on: ubuntu-latest\n"
                   "    steps:\n      - uses: actions/checkout@v5\n      - uses: 'anthropics/claude-code-action@v1'\n"
                   "      - name: codex\n        uses: openai/codex-action@main\n      - uses: anthropics/claude-code-base-action@beta\n"
                   "      - uses: google-github-actions/run-gemini-cli@v0\n      - uses: coderabbitai/ai-pr-reviewer@latest\n")
        # A file name is data: one that reads as a sed script (w writes a file) must not run.
        evil = ".github/workflows/x#;w pwned#.yml"
        self.write(evil, "jobs:\n  r:\n    steps:\n      - uses: coderabbitai/ai-pr-reviewer@latest\n")
        r = self.check()
        self.assertEqual(r.returncode, 1)
        self.assertFalse(any(p.name.startswith("pwned") for p in self.repo.rglob("*")), "check.sh ran a file name as sed")
        self.assertIn(f"fail: {evil}: coderabbitai/ai-pr-reviewer runs an AI reviewer or agent in CI; remove it", r.stdout)
        (self.repo / evil).unlink()
        r = self.check()
        self.assertEqual(r.returncode, 1)
        named = sorted(line for line in r.stdout.splitlines() if "AI reviewer" in line)
        self.assertEqual(named, [f"fail: .github/workflows/review.yml: {a} runs an AI reviewer or agent in CI; remove it" for a in (
            "anthropics/claude-code-action", "anthropics/claude-code-base-action", "coderabbitai/ai-pr-reviewer",
            "google-github-actions/run-gemini-cli", "openai/codex-action")])

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

    # The writing rules: the check counts the em dash and the word caps, fails on each finding, and warns
    # instead with WF_WRITING_LENIENT set.
    EM = "—"
    WRITING_OK = ("ok: no em dash", "ok: paragraphs, bullets and glossary entries within their word caps",
                  "ok: documents within their word caps")

    def strict(self, root=None):
        return self.run_script(STANDARDS / "check.sh", *([str(root)] if root else []), WF_WRITING_LENIENT="")

    def lenient(self, root=None):
        return self.run_script(STANDARDS / "check.sh", *([str(root)] if root else []), WF_WRITING_LENIENT="1")

    @staticmethod
    def words(n, per=50):
        """n words as paragraphs of at most `per` words each."""
        return "\n\n".join(" ".join(["word"] * min(per, n - i)) for i in range(0, n, per)) + "\n"

    def assert_writing(self, findings):
        """The findings fail without the lenient variable and warn with it; everything else is the same."""
        r = self.strict()
        self.assertEqual(r.returncode, 1, r.stdout)
        for f in findings:
            self.assertIn(f"fail: {f}\n", r.stdout)
        self.assertNotIn("warn: " + findings[0], r.stdout)
        r = self.lenient()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("result: pass", r.stdout)
        for f in findings:
            self.assertIn(f"warn: {f}\n", r.stdout)
        self.assertNotIn("fail: ", r.stdout)

    def test_the_templates_pass_the_writing_rules(self):
        (self.repo / "README.md").unlink()
        self.scaffold()
        self.run_script(STANDARDS / "new-adr.sh", "Use", "Postgres")
        tpl = STANDARDS.parent / "templates"
        self.write("plugins/tool/README.md", (tpl / "plugin-README.md").read_text())
        r = self.strict()
        self.assertEqual(r.returncode, 0, r.stdout)
        for line in self.WRITING_OK:
            self.assertIn(line, r.stdout)

        def headings(path):
            return [l for l in (self.repo / path).read_text().splitlines() if l.startswith("## ")]
        self.assertEqual(headings("README.md"), ["## What it ships", "## Install", "## Daily use", "## Configuration",
                                                 "## Design", "## Develop"])
        self.assertEqual(headings("docs/architecture.md"), ["## Purpose", "## Components", "## Data flow", "## Boundaries",
                                                            "## Decisions"])
        self.assertEqual(headings("plugins/tool/README.md"), ["## Skills", "## Configuration", "## Develop"])
        self.assertEqual(headings("docs/adr/0001-use-postgres.md"), ["## Context", "## Decision", "## Consequences"])

    def test_an_em_dash_in_any_text_file_fails_and_names_the_file(self):
        self.scaffold()
        self.write("src/app.py", f"x = 1  # one {self.EM} two {self.EM} three\n")
        self.write("docs/notes.md", f"A note {self.EM} short.\n")
        (self.repo / "logo.png").write_bytes(b"\x89PNG\x00\x00" + self.EM.encode() + b"\x00")
        self.assert_writing(["docs/notes.md has 1 em dash; use a comma, a colon or two sentences",
                             "src/app.py has 2 em dashes; use a comma, a colon or two sentences"])
        self.assertNotIn("logo.png", self.strict().stdout, "a binary file is no text")

    def test_a_long_paragraph_or_bullet_fails_unless_it_is_code_or_a_table(self):
        self.scaffold()
        long, cap = " ".join(["word"] * 81), " ".join(["word"] * 80)
        self.write("docs/notes.md", f"# Notes\n\n{cap}\n\n```\n{long}\n```\n\n| a | b |\n| --- | --- |\n| {long} | x |\n\n"
                                    f"- {' '.join(['word'] * 30)}\n1. {' '.join(['word'] * 30)}\n")
        r = self.strict()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn(self.WRITING_OK[1], r.stdout)
        half = " ".join(["word"] * 40)
        self.write("docs/notes.md", f"# Notes\n\n{half}\n{half} more\n\n- {' '.join(['word'] * 30)}\n  more\n"
                                    f"2. {' '.join(['word'] * 31)}\n")
        self.assert_writing(["docs/notes.md:3: paragraph of 81 words (>80); split it or make it bullets",
                             "docs/notes.md:6: bullet of 31 words (>30); shorten it or split it",
                             "docs/notes.md:8: bullet of 31 words (>30); shorten it or split it"])
        self.assertNotIn(self.WRITING_OK[1], self.strict().stdout)

    def test_each_document_fails_over_its_word_cap_and_is_named_with_its_count(self):
        self.scaffold()
        # Each document one word over its cap: the heading and the Status line count too, a code block does not.
        self.write("docs/adr/0001-big.md", "# 0001. Big\n\nStatus: accepted\n\n" + self.words(247)
                   + "```\n" + " ".join(["code"] * 60) + "\n```\n")
        self.write("docs/architecture.md", "# Architecture\n\n" + self.words(2000))
        self.write("README.md", "# shop\n\n" + self.words(1200))
        self.write("plugins/tool/README.md", "# tool\n\n" + self.words(800))
        self.write("docs/glossary.md", "# Glossary\n\n| Term | Meaning |\n| --- | --- |\n"
                                       f"| short | {' '.join(['word'] * 39)} |\n| long | {' '.join(['word'] * 40)} |\n")
        self.assert_writing(["docs/adr/0001-big.md has 251 words (>250 for an ADR); shorten it",
                             "docs/architecture.md has 2001 words (>2000 for the architecture map); shorten it",
                             "README.md has 1201 words (>1200 for the README); shorten it",
                             "plugins/tool/README.md has 801 words (>800 for a plugin README); shorten it",
                             "docs/glossary.md:6: glossary entry of 41 words (>40); shorten it"])
        r = self.strict()
        self.assertNotIn("docs/glossary.md:5", r.stdout)
        self.assertNotIn(self.WRITING_OK[2], r.stdout)
        self.write("docs/adr/0001-big.md", "# 0001. Big\n\nStatus: accepted\n\n" + self.words(246))
        self.assertNotIn("0001-big.md", self.strict().stdout)

    def test_the_writing_rules_are_counted_outside_git_on_the_files_found(self):
        plain = self.base / "plain"
        r = self.run_script(STANDARDS / "scaffold.sh", str(plain))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.strict(plain).returncode, 0)
        (plain / "notes.md").write_text(f"{' '.join(['word'] * 81)}\n")
        (plain / "run.sh").write_text(f"echo {self.EM}\n")
        r = self.strict(plain)
        self.assertEqual(r.returncode, 1, r.stdout)
        self.assertIn("fail: notes.md:1: paragraph of 81 words (>80); split it or make it bullets\n", r.stdout)
        self.assertIn("fail: run.sh has 1 em dash; use a comma, a colon or two sentences\n", r.stdout)
        r = self.lenient(plain)
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("warn: notes.md:1: paragraph of 81 words (>80)", r.stdout)

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
