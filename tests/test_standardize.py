import json
import subprocess
import unittest

from helpers import STANDARDS, ShimTest

FACTS = STANDARDS / "facts.sh"
REPORT = STANDARDS / "report.sh"
APPROVE = STANDARDS / "approve.sh"
BASELINE = ("README.md, AGENTS.md, CLAUDE.md, Makefile, docs/architecture.md, docs/adr/README.md, docs/glossary.md, "
            ".github/PULL_REQUEST_TEMPLATE.md, .claude/settings.json")


def lines(out, *keys):
    """The lines of out that start with one of keys, in order; a key ending in ':' matches that line exactly
    plus the indented block below it."""
    result, block = [], False
    for line in out.splitlines():
        if line.startswith("  ") and block:
            result.append(line)
            continue
        block = False
        for k in keys:
            if line == k:
                result.append(line)
                block = True
            elif line.startswith(k + " "):
                result.append(line)
    return result


class FactsTests(ShimTest):
    """facts.sh on fixture repositories: an empty one, a messy public one, a private dev plus main one, offline."""

    def setUp(self):
        super().setUp()
        self.ws = self.base / "github"
        self.ws.mkdir()

    def write(self, path, text="x\n", root=None):
        p = (root or self.repo) / path
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)

    def put(self, name, data):
        (self.ws / name).write_text(json.dumps(data))

    def facts(self, cwd=None, github=True):
        r = self.run_script(FACTS, cwd=cwd, **({"SHIM_WS": str(self.ws)} if github else {}))
        self.assertEqual(r.returncode, 0, r.stderr)
        return r.stdout

    def messy(self):
        """A public repository clicked together by hand, with agent configuration of three tools."""
        self.write("package.json", json.dumps({"scripts": {"test": "vitest", "lint": "eslint .", "dev": "vite",
                                                           "test:e2e": "playwright test"}}))
        self.write("pnpm-lock.yaml", "")
        self.write("src/app.ts")
        self.write("src/util.ts")
        self.write("src/view.tsx")
        self.write("api/pyproject.toml", "[project]\nname = 'api'\n\n[tool.pytest.ini_options]\n\n[tool.ruff]\n")
        self.write("api/main.py")
        self.write("Makefile", "VAR := 1\ntest:\n\tpnpm test\nlint:\n\tpnpm lint\nbuild: lint\n\ttrue\n")
        self.write("docs/big.bin", "0" * 3000)
        self.write(".github/workflows/ci.yml", "name: CI\non: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n"
                   "    steps:\n      - run: make build\n  review:\n    name: AI review\n    runs-on: ubuntu-latest\n"
                   "    steps:\n      - uses: anthropics/claude-code-action@v1\n")
        self.write("CLAUDE.md", "# Rules\nAlways use pnpm.\n")
        self.write(".claude/skills/deploy/SKILL.md")
        self.write(".claude/skills/deploy/run.sh")
        self.write(".claude/commands/ship.md")
        self.write(".claude/settings.json", "{}\n")
        self.write(".cursor/rules/style.mdc")
        self.write(".mcp.json", "{}\n")
        self.write(".gitignore", ".claude/settings.local.json\nCLAUDE.local.md\n")
        self.git("add", ".")
        self.git("commit", "-qm", "messy")
        self.write(".claude/settings.local.json", "{}\n")  # ignored
        self.write("CLAUDE.local.md")  # ignored
        self.write("GEMINI.md")  # untracked
        self.put("repo.json", {"visibility": "public", "default_branch": "main", "owner": {"login": "o", "type": "User"}})
        self.put("user.json", {"login": "o", "plan": {"name": "pro"}})

    def test_an_empty_repository_has_only_missing_baseline_files(self):
        empty = self.base / "empty"
        empty.mkdir()
        self.git("init", "-q", "-b", "main", cwd=empty)
        out = self.facts(cwd=empty, github=False)
        self.assertEqual(lines(out, "github:", "head:", "branch-model:", "languages:", "manifests:", "gate:", "test:",
                               "lint:", "ci:", "ci-check-job:", "agent-config:", "baseline-present:",
                               "baseline-missing:", "files:", "largest:"), [
            "github: unreachable (gh shim: unhandled: api repos/o/r)",
            "branch-model: main",
            "head: none (no commits yet)",
            "languages: none",
            "manifests: none",
            "gate: none (no check target in a Makefile)",
            "test: none",
            "lint: none",
            "ci: none",
            "ci-check-job: no",
            "agent-config: none",
            "baseline-present: none",
            f"baseline-missing: {BASELINE}",
            "files: 0 tracked, 0 untracked, 0 B",
            "largest: none"])

    def test_a_messy_public_repository(self):
        self.messy()
        before = self.git("status", "--porcelain", "--ignored")
        out = self.facts()
        self.assertEqual(lines(out, "github:", "visibility:", "plan:", "default-branch:", "branch-model:", "languages:",
                               "manifests:", "gate:", "test:", "lint:", "ci:", "ci-check-job:", "agent-config:",
                               "baseline-missing:", "files:", "top-dirs:"), [
            "github: o/r, owner o",
            "visibility: public",
            "plan: pro",
            "default-branch: main",
            "branch-model: main",
            "languages: Markdown 5, TypeScript 3, Python 1, Shell 1",
            "manifests: Makefile, package.json, api/pyproject.toml",
            "gate: none (no check target in a Makefile)",
            "test: make test (Makefile), pnpm test (package.json), pnpm run test:e2e (package.json), "
            "pytest (api/pyproject.toml)",
            "lint: make lint (Makefile), pnpm run lint (package.json), ruff check (api/pyproject.toml)",
            "ci:",
            '  .github/workflows/ci.yml: build, review ("AI review")',
            "ci-check-job: no",
            "agent-config:",
            "  .claude/commands/ship.md: 1 file, tracked, outside the standard",
            "  .claude/settings.json: 1 file, tracked, standard",
            "  .claude/settings.local.json: 1 file, ignored, standard",
            "  .claude/skills/deploy: 2 files, tracked, outside the standard",
            "  .cursor/rules/style.mdc: 1 file, tracked, outside the standard",
            "  .mcp.json: 1 file, tracked, outside the standard",
            "  CLAUDE.local.md: 1 file, ignored, outside the standard",
            "  CLAUDE.md: 1 file, tracked, standard",
            "  GEMINI.md: 1 file, untracked, outside the standard",
            "baseline-missing: AGENTS.md, docs/architecture.md, docs/adr/README.md, docs/glossary.md, "
            ".github/PULL_REQUEST_TEMPLATE.md, LICENSE, SECURITY.md",
            "files: 19 tracked, 1 untracked, 3 KB",
            "top-dirs: .claude/ 4, src/ 3, api/ 2, .cursor/ 1, .github/ 1, docs/ 1"])
        self.assertIn("largest: docs/big.bin (3 KB), ", out)
        self.assertEqual(self.git("status", "--porcelain", "--ignored"), before, "facts.sh changed the working tree")

    def test_a_private_repository_on_dev_plus_main_owned_by_an_organisation(self):
        self.write("Makefile", "check: lint test\nlint:\n\ttrue\ntest:\n\ttrue\n")
        self.write("go.mod", "module x\n")
        self.write(".github/workflows/ci.yml", "on: push\njobs:\n    gate:\n        name: check\n        runs-on: x\n"
                   "        steps:\n            - run: make check\n")
        self.write(".gitlab-ci.yml")
        self.git("add", ".")
        self.git("commit", "-qm", "ci")
        self.put("repo.json", {"visibility": "private", "default_branch": "dev", "owner": {"login": "o", "type": "Organization"}})
        out = self.facts()
        self.assertEqual(lines(out, "visibility:", "plan:", "branch-model:", "gate:", "test:", "lint:", "ci:",
                               "ci-check-job:"), [
            "visibility: private",
            "plan: unknown (visible to organisation owners only)",
            "branch-model: dev+main",
            "gate: make check (Makefile)",
            "test: make test (Makefile), go test ./... (go.mod)",
            "lint: make lint (Makefile), go vet ./... (go.mod)",
            "ci:",
            '  .github/workflows/ci.yml: gate ("check")',
            "  .gitlab-ci.yml: not GitHub Actions",
            "ci-check-job: yes"])
        self.assertNotIn("LICENSE", out, "a private repository needs no licence")

    def test_symlinked_agent_configuration_is_listed_and_deleted_files_are_skipped(self):
        self.write("AGENTS.md", "# rules\n")
        (self.repo / "CLAUDE.md").symlink_to("AGENTS.md")
        self.write("shared/skills/x/SKILL.md")
        (self.repo / ".claude/skills").mkdir(parents=True)
        (self.repo / ".claude/skills/x").symlink_to(self.repo / "shared/skills/x")
        self.write("Makefile", "test:\n\ttrue\n")
        self.write(".github/workflows/ci.yml", "jobs:\n  check:\n    runs-on: x\n")
        self.git("add", ".")
        self.git("commit", "-qm", "links")
        (self.repo / "Makefile").unlink()
        (self.repo / ".github/workflows/ci.yml").unlink()
        r = self.run_script(FACTS)
        self.assertEqual((r.returncode, r.stderr), (0, ""))
        self.assertEqual(lines(r.stdout, "agent-config:", "test:", "ci:"), [
            "test: none",
            "ci: none",
            "agent-config:",
            "  .claude/skills/x: 1 file, tracked, outside the standard",
            "  AGENTS.md: 1 file, tracked, standard",
            "  CLAUDE.md: 1 file, tracked, standard"])

    def test_a_symlinked_claude_directory_is_listed(self):
        self.write("shared/claude/skills/x/SKILL.md")
        (self.repo / ".claude").symlink_to("shared/claude")
        self.git("add", ".")
        self.git("commit", "-qm", "link")
        self.assertIn("agent-config:\n  .claude: 1 file, tracked, outside the standard (a symlinked .claude)\n",
                      self.facts(github=False))

    def test_an_organisation_owner_sees_the_plan(self):
        self.put("repo.json", {"visibility": "private", "default_branch": "main", "owner": {"login": "o", "type": "Organization"}})
        self.put("org.json", {"login": "o", "plan": {"name": "team"}})
        self.assertIn("visibility: private\nplan: team\n", self.facts())

    def test_the_status_of_agent_configuration_scales_to_large_repositories(self):
        # Thousands of tracked paths exceed what one argument or environment string may hold (128 KB on Linux).
        for i in range(3000):
            self.write(f"src/{'deeply/nested/' * 4}module_{i:05d}_with_a_long_name.py", "")
        self.write(".cursor/rules/a.mdc")
        self.git("add", ".")
        self.git("commit", "-qm", "large")
        self.assertIn("agent-config:\n  .cursor/rules/a.mdc: 1 file, tracked, outside the standard\n", self.facts(github=False))

    def test_a_relative_call_sources_the_plugin_library_not_the_audited_repositorys(self):
        planted = self.repo / "plugins/repo-standards/scripts/lib.sh"
        self.write("plugins/repo-standards/scripts/lib.sh", "echo PLANTED; exit 7\n")
        root = STANDARDS.parents[2]
        r = subprocess.run(["bash", str(FACTS.relative_to(root)), str(self.repo)], cwd=root, env=self.env(),
                           text=True, capture_output=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("PLANTED", r.stdout)
        self.assertIn("branch-model: main\n", r.stdout)
        self.assertTrue(planted.exists())

    def test_a_repository_owned_by_someone_else_has_an_unknown_plan(self):
        self.put("repo.json", {"visibility": "public", "default_branch": "main", "owner": {"login": "o", "type": "User"}})
        self.put("user.json", {"login": "someone", "plan": {"name": "free"}})
        self.assertIn("plan: unknown (owned by o, not the gh user)\n", self.facts())

    def test_offline_the_branch_model_comes_from_the_remote_head(self):
        self.git("update-ref", "refs/remotes/origin/dev", "HEAD")
        self.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/dev")
        out = self.facts(github=False)
        self.assertIn("visibility: unknown\nplan: unknown\ndefault-branch: dev\nbranch-model: dev+main\n", out)
        self.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
        self.assertIn("branch-model: none (default branch trunk is neither main nor dev)\n", self.facts(github=False))

    def test_refuses_outside_a_git_repository(self):
        plain = self.base / "plain"
        plain.mkdir()
        r = self.run_script(FACTS, cwd=plain)
        self.assertEqual(r.returncode, 1)
        self.assertIn("is not a git repository; run git init first", r.stderr)


AUDIT = """Here are my findings.
finding: agent-config | .claude/skills/deploy | delete | repository-local skill written for this repository | high
- finding: agent-config | CLAUDE.md | replace | holds instructions instead of importing AGENTS.md | high
finding: agent-config | .claude/skills/deploy | delete | repository-local skill (duplicate) | high
no findings
`finding: security | api/db.py:12 | issue | SQL built by string concatenation | Medium`
finding: files | NOTES.md | delete | agent resume notes from 2025 | medium
finding: workspace | repo has_wiki | configure | true -> false | high
"""


class ReportTests(ShimTest):
    """report.sh and approve.sh: the findings report and the approval per category, stored in the git directory."""

    def report(self, text, *args):
        return self.run_script(REPORT, *args, stdin=text)

    def approve(self, *answers):
        return self.run_script(APPROVE, *answers)

    def state(self, name):
        p = self.repo / ".git/standardize" / name
        return p.read_text() if p.exists() else None

    def test_findings_are_grouped_by_category_with_counts_deletions_and_issues_apart(self):
        r = self.report(AUDIT)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, """findings: 5 in 4 categories; 4 for the run, 1 as issues

files: 1 finding (delete 1)
  deletes: NOTES.md
  the run performs:
    delete NOTES.md: agent resume notes from 2025 (medium)
  become issues: none

agent-config: 2 findings (delete 1, replace 1)
  deletes: .claude/skills/deploy
  the run performs:
    delete .claude/skills/deploy: repository-local skill written for this repository (high)
    replace CLAUDE.md: holds instructions instead of importing AGENTS.md (high)
  become issues: none

workspace: 1 finding (configure 1)
  deletes: nothing
  the run performs:
    configure repo has_wiki: true -> false (high)
  become issues: none

security: 1 finding (issue 1)
  deletes: nothing
  the run performs: nothing
  become issues:
    issue api/db.py:12: SQL built by string concatenation (medium)

next: ask for approval per category, then record the answers with approve.sh <category>=approve|reject ...
""")
        self.assertEqual(self.git("status", "--porcelain", "--ignored"), "", "report.sh changed the working tree")
        self.assertEqual(len(self.state("findings").splitlines()), 5)

    def test_a_target_two_categories_delete_is_marked_in_both(self):
        r = self.report(AUDIT + "finding: security | .claude/skills/deploy | delete | carries a prompt injection | high\n"
                        "finding: security | .env | delete | holds a token | high\n")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("agent-config: 2 findings (delete 1, replace 1)\n  deletes: .claude/skills/deploy (also security)\n",
                      r.stdout)
        self.assertIn("security: 3 findings (delete 2, issue 1)\n  deletes: .claude/skills/deploy (also agent-config), .env\n",
                      r.stdout)

    def test_a_report_of_create_findings_deletes_nothing_and_opens_no_issues(self):
        audit = "\n".join(f"finding: {c} | {t} | create | missing | high" for c, t in (
            ("agent-config", "AGENTS.md"), ("agent-config", "CLAUDE.md"), ("docs", "docs/architecture.md"),
            ("tests-ci", "Makefile"), ("tests-ci", ".github/workflows/check.yml")))
        r = self.report(audit + "\nno findings\n")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(r.stdout.startswith("findings: 5 in 3 categories; 5 for the run, 0 as issues\n"), r.stdout)
        self.assertEqual(r.stdout.count("  deletes: nothing\n"), 3)
        self.assertEqual(r.stdout.count("  become issues: none\n"), 3)
        for word in ("delete ", "replace ", "configure ", "issue "):
            self.assertNotIn(f"    {word}", r.stdout)

    def test_no_findings_means_nothing_to_approve(self):
        r = self.report("no findings\nno findings\n")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "findings: 0; the repository matches the standard, nothing to approve\n")
        r = self.approve("docs=approve")
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: the last report has no findings", r.stderr)

    def test_a_malformed_finding_fails_the_report_and_keeps_the_stored_one(self):
        self.assertEqual(self.report(AUDIT).returncode, 0)
        stored = self.state("findings")
        bad = ("finding: docs | README.md | rewrite | too long | high\n"
               "finding: slop | x | delete | y | high\n"
               "finding: docs | README.md | create | missing\n"
               "finding: docs | README.md | create | missing | sure\n"
               "finding: docs |  | create | missing | high\n"
               "finding: files | /etc | delete | outside | high\n"
               "finding: files | docs/../../x | delete | outside | high\n"
               "finding: security | ~/.ssh/id_rsa | issue | outside | high\n"
               "finding: files | /etc | configure | outside | high\n"
               "finding: files | -rf | delete | option | high\n")
        r = self.report(bad)
        self.assertEqual(r.returncode, 1)
        self.assertEqual(r.stdout, "")
        self.assertEqual(r.stderr.splitlines(), [
            "error: unknown action rewrite; use delete, replace, create, configure or issue: "
            "finding: docs | README.md | rewrite | too long | high",
            "error: unknown category slop; use one of files agent-config docs tests-ci workspace security: "
            "finding: slop | x | delete | y | high",
            "error: has 4 fields, needs 5: category | target | action | reason | confidence: "
            "finding: docs | README.md | create | missing",
            "error: unknown confidence sure; use high, medium or low: finding: docs | README.md | create | missing | sure",
            "error: empty target: finding: docs |  | create | missing | high",
            "error: target /etc leaves the repository; use a path relative to its root: finding: files | /etc | delete | outside | high",
            "error: target docs/../../x leaves the repository; use a path relative to its root: "
            "finding: files | docs/../../x | delete | outside | high",
            "error: target ~/.ssh/id_rsa leaves the repository; use a path relative to its root: "
            "finding: security | ~/.ssh/id_rsa | issue | outside | high",
            "error: configure is for GitHub settings, which only the workspace category proposes: "
            "finding: files | /etc | configure | outside | high",
            "error: target -rf starts with -; name the path without a leading dash: finding: files | -rf | delete | option | high",
            "error: malformed findings, nothing stored; correct those lines and run report.sh again"])
        self.assertEqual(self.state("findings"), stored)

    def test_report_reads_the_saved_replies_from_files(self):
        saved = self.base / "replies.txt"
        saved.write_text(AUDIT)
        r = self.run_script(REPORT, str(saved))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(r.stdout.startswith("findings: 5 in 4 categories"))
        r = self.run_script(REPORT, str(self.base / "missing.txt"))
        self.assertEqual(r.returncode, 1)
        self.assertIn("missing.txt not found", r.stderr)

    def test_approval_is_recorded_per_category_and_the_last_answer_wins(self):
        self.assertEqual(self.report(AUDIT).returncode, 0)
        r = self.approve()
        self.assertEqual(r.stdout, "approved: none\nrejected: none\npending: files, agent-config, workspace, security\n")
        r = self.approve("agent-config=approve", "security=reject")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "approved: agent-config\nrejected: security\npending: files, workspace\n")
        r = self.approve("security=approve", "files=reject", "workspace=approve")
        self.assertEqual(r.stdout, "approved: agent-config, workspace, security\nrejected: files\npending: none\n")
        self.assertEqual(sorted(self.state("approvals").splitlines()),
                         ["agent-config\tapprove", "files\treject", "security\tapprove", "workspace\tapprove"])
        self.assertEqual(self.git("status", "--porcelain", "--ignored"), "", "approve.sh changed the working tree")

    def test_a_bad_answer_records_nothing(self):
        self.assertEqual(self.report(AUDIT).returncode, 0)
        for args, message in ((("files=approve", "docs=approve"), "docs has no findings in the last report"),
                              (("files=approve", "security=maybe"), "security=maybe: the answer is approve or reject"),
                              (("files",), "files: use <category>=approve or <category>=reject")):
            r = self.approve(*args)
            self.assertEqual(r.returncode, 1, args)
            self.assertIn(f"error: {message}", r.stderr)
            self.assertIsNone(self.state("approvals"), args)

    def test_a_new_report_clears_the_answers_to_the_old_one(self):
        self.assertEqual(self.report(AUDIT).returncode, 0)
        self.assertEqual(self.approve("files=approve").returncode, 0)
        self.assertEqual(self.report(AUDIT).returncode, 0)
        self.assertIn("pending: files, agent-config, workspace, security\n", self.approve().stdout)

    def test_approve_needs_a_report_first(self):
        r = self.approve("files=approve")
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: no findings recorded; run the audit and report.sh first", r.stderr)

    def test_a_worktree_shares_the_state_of_its_repository(self):
        wt = self.base / "wt-standardize"
        self.git("worktree", "add", "-q", "-b", "chore/standardize", str(wt))
        self.assertEqual(self.report(AUDIT).returncode, 0)
        self.assertEqual(self.approve("files=approve").returncode, 0)
        r = self.run_script(APPROVE, cwd=wt)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(r.stdout.startswith("approved: files\n"), r.stdout)


if __name__ == "__main__":
    unittest.main()
