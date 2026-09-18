import json
import subprocess
import unittest

from helpers import STANDARDS, ShimTest

REPORT = STANDARDS / "report.sh"
APPROVE = STANDARDS / "approve.sh"
BACKUP = STANDARDS / "backup.sh"
CLEANUP = STANDARDS / "cleanup.sh"
ISSUES = STANDARDS / "issues.sh"
FINALIZE = STANDARDS / "finalize.sh"
CATALOGUE = "Standardisation: removed skills and how to restore them"
WT = ".claude/worktrees/chore-standardize"

FILES = {
    "CLAUDE.md": "# Rules\nAlways use pnpm.\n",
    ".claude/skills/deploy/SKILL.md": "---\nname: deploy\ndescription: Deploy the app | to production.\n---\nRun run.sh.\n",
    ".claude/skills/deploy/run.sh": "#!/bin/sh\necho deploy\n",
    ".claude/skills/review/SKILL.md": "---\nname: review\ndescription: >\n  Review a diff\n  carefully.\n---\nBody.\n",
    ".claude/skills/review/LICENSE": "MIT\n",
    ".claude/skills/lint/SKILL.md": "Lint everything.\n",
    ".claude/commands/ship.md": "---\ndescription: Ship a release\n---\nShip it.\n",
    ".claude/settings.json": json.dumps({"enabledPlugins": {"foo@bar": True}, "env": {"WF_REVIEW_ROUNDS": "5"}}) + "\n",
    "skills-lock.json": json.dumps({"version": 1, "skills": {"lint": {"source": "acme/skills"}}}) + "\n",
    ".cursor/rules/style.mdc": "be nice\n",
    "NOTES.md": "handover notes\n",
    "src/app.py": "print('hi')\n",
}
REPLIES = """The agent-config auditor:
finding: agent-config | .claude/skills | delete | three skills, deploy written for this repository | high
finding: agent-config | .claude/commands | delete | one command, written for this repository | high
finding: agent-config | .cursor | delete | Cursor rules that repeat CLAUDE.md | high
finding: agent-config | skills-lock.json | delete | lock file of the skills CLI | high
finding: agent-config | CLAUDE.md | replace | its own instructions move to AGENTS.md | high
finding: files | NOTES.md | delete | agent handover notes | medium
finding: tests-ci | Makefile | create | no check target | high
finding: tests-ci | src | issue | src/app.py has no tests | medium
finding: security | src/app.py | issue | prints instead of logging | low
finding: workspace | merge settings | configure | rebase merges are allowed | high
"""


class ApplyCase(ShimTest):
    """The apply phase against a bare origin, the stateful gh shim ($SHIM_WS) and the claude shim."""

    def github(self):
        """An empty bare origin as the remote, and the GitHub side of a private repository on main."""
        self.origin = self.base / "origin.git"
        self.git("init", "-q", "--bare", str(self.origin))
        self.git("remote", "add", "origin", str(self.origin))
        self.ws = self.base / "github"
        self.ws.mkdir()
        self.put("repo.json", {
            "visibility": "private", "default_branch": "main", "permissions": {"admin": True},
            "allow_squash_merge": True, "allow_merge_commit": False, "allow_rebase_merge": True,
            "delete_branch_on_merge": True, "squash_merge_commit_title": "PR_TITLE",
            "squash_merge_commit_message": "COMMIT_MESSAGES", "has_wiki": False, "has_discussions": False,
            "security_and_analysis": {"secret_scanning": {"status": "disabled"}}})
        self.put("labels.json", [{"name": "bug"}])
        self.put("actions-workflow.json", {"default_workflow_permissions": "read", "can_approve_pull_request_reviews": False})
        (self.ws / "vulnerability-alerts").touch()
        self.put("automated-security-fixes.json", {"enabled": True})
        self.put("milestones.json", [])
        self.put("projects.json", [])

    def audit(self, replies, *answers):
        r = self.run_script(REPORT, stdin=replies)
        self.assertEqual(r.returncode, 0, r.stderr)
        r = self.run_script(APPROVE, *answers)
        self.assertEqual(r.returncode, 0, r.stderr)

    def write(self, path, text, root=None):
        p = (root or self.repo) / path
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)

    def put(self, name, data):
        (self.ws / name).write_text(json.dumps(data))

    def get(self, name):
        return json.loads((self.ws / name).read_text())

    def step(self, script, *args, ok=True, **env):
        r = self.run_script(script, *args, SHIM_WS=str(self.ws), **env)
        if ok:
            self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        return r

    def origin_git(self, *args):
        return self.git(f"--git-dir={self.origin}", *args)

    def fill_in(self, todo=""):
        """What the agent does between prepare and open: the CLAUDE.md todo when prepare printed it, then every
        placeholder the branch adds."""
        wt = self.repo / WT
        if "todo: agent-config replace CLAUDE.md" in todo:
            (wt / "CLAUDE.md").write_text("@AGENTS.md\n")
        for f in self.git("diff", "--cached", "--name-only", "origin/main", cwd=wt).split():
            p = wt / f
            if p.is_file() and "<fill in>" in p.read_text():
                p.write_text(p.read_text().replace("<fill in>", "true"))

    def merge(self):
        """GitHub merges the pull request: the branch lands on main and the pull request is marked merged."""
        head = self.origin_git("rev-parse", "refs/heads/chore/standardize").strip()
        self.origin_git("update-ref", "refs/heads/main", head)
        pulls = self.get("pulls.json")
        for p in pulls:
            self.origin_git("update-ref", f"refs/pull/{p['number']}/head", head)
        self.put("pulls.json", [dict(p, state="closed", merged_at="2026-09-18T12:00:00Z", head=dict(p["head"], sha=head))
                                for p in pulls])

    def through_open(self):
        self.step(BACKUP)
        self.fill_in(self.step(CLEANUP, "prepare").stdout)
        return self.step(CLEANUP, "open")


class ApplyTests(ApplyCase):
    def setUp(self):
        super().setUp()
        self.github()
        for path, text in FILES.items():
            self.write(path, text)
        self.git("add", ".")
        self.git("commit", "-qm", "messy")
        self.git("push", "-q", "origin", "main")
        self.head = self.git("rev-parse", "HEAD").strip()
        self.audit(REPLIES, "agent-config=approve", "tests-ci=approve", "security=approve", "workspace=approve", "files=reject")

    def test_backup_tags_the_head_protects_the_tag_and_catalogues_each_removed_skill(self):
        before = self.git("status", "--porcelain", "--ignored")
        r = self.step(BACKUP)
        short = self.head[:7]
        self.assertIn(f"tag: pre-standard pushed at {short} (the head of main)\n", r.stdout)
        self.assertEqual(self.origin_git("rev-parse", "refs/tags/pre-standard").strip(), self.head)
        self.assertIn("protection: ruleset standard: pre-standard created (no deletion, no moving)\n", r.stdout)
        rulesets = [json.loads(p.read_text()) for p in self.ws.glob("ruleset-*.json")]
        self.assertEqual([(s["name"], s["target"], [x["type"] for x in s["rules"]]) for s in rulesets],
                         [("standard: pre-standard", "tag", ["deletion", "update"])])
        self.assertIn("catalogue: #1 opened, 4 skills\n", r.stdout)
        issue, = self.get("issues.json")
        self.assertEqual((issue["title"], [x["name"] for x in issue["labels"]]), (CATALOGUE, ["skill-candidate"]))
        self.assertIn("skill-candidate", [x["name"] for x in self.get("labels.json")])
        deploy = len(FILES[".claude/skills/deploy/SKILL.md"]) + len(FILES[".claude/skills/deploy/run.sh"])
        review = len(FILES[".claude/skills/review/SKILL.md"]) + len(FILES[".claude/skills/review/LICENSE"])
        rows = [line for line in issue["body"].splitlines() if line.startswith("| ")]
        self.assertEqual(rows, [
            "| Skill | Description | Origin | Files | Restore |",
            "| --- | --- | --- | --- | --- |",
            f"| deploy | Deploy the app to production. | audit: three skills, deploy written for this repository | 2 files, {deploy} B | `git checkout pre-standard -- .claude/skills/deploy` |",
            f"| lint | none | upstream: acme/skills (from skills-lock.json) | 1 file, {len(FILES['.claude/skills/lint/SKILL.md'])} B | `git checkout pre-standard -- .claude/skills/lint` |",
            f"| review | Review a diff carefully. | upstream (carries LICENSE) | 2 files, {review} B | `git checkout pre-standard -- .claude/skills/review` |",
            f"| ship | Ship a release | audit: one command, written for this repository | 1 file, {len(FILES['.claude/commands/ship.md'])} B | `git checkout pre-standard -- .claude/commands/ship.md` |",
        ])
        self.assertIn("git fetch origin tag pre-standard", issue["body"])
        self.assertEqual(self.git("status", "--porcelain", "--ignored"), before)

    def test_a_restore_command_brings_the_skill_back(self):
        self.through_open()
        self.merge()
        self.git("pull", "-q", "origin", "main")
        self.assertFalse((self.repo / ".claude/skills/deploy").exists())
        self.git("checkout", "pre-standard", "--", ".claude/skills/deploy")
        self.assertEqual((self.repo / ".claude/skills/deploy/run.sh").read_text(), FILES[".claude/skills/deploy/run.sh"])

    def test_an_existing_tag_is_kept_and_never_moved(self):
        self.origin_git("tag", "pre-standard", self.head)
        self.write("later.txt", "x\n")
        self.git("add", ".")
        self.git("commit", "-qm", "later")
        self.git("push", "-q", "origin", "main")
        r = self.step(BACKUP)
        self.assertIn(f"tag: pre-standard kept at {self.head[:7]} (pushed before)\n", r.stdout)
        self.assertEqual(self.origin_git("rev-parse", "refs/tags/pre-standard").strip(), self.head)
        self.assertEqual(self.git("rev-parse", "refs/tags/pre-standard").strip(), self.head)

    def test_a_different_local_tag_is_refused_and_the_one_on_origin_kept(self):
        self.origin_git("tag", "pre-standard", self.head)
        self.git("commit", "-q", "--allow-empty", "-m", "local")
        self.git("tag", "pre-standard")
        r = self.step(BACKUP, ok=False)
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: the local tag pre-standard differs from the one on origin", r.stderr)
        self.assertEqual(self.origin_git("rev-parse", "refs/tags/pre-standard").strip(), self.head)

    def test_nothing_is_applied_while_a_category_is_pending(self):
        self.run_script(REPORT, stdin=REPLIES)
        for script, args in ((BACKUP, ()), (CLEANUP, ("prepare",)), (ISSUES, ()), (FINALIZE, ())):
            r = self.step(script, *args, ok=False)
            self.assertEqual(r.returncode, 1, script)
            self.assertIn("error: agent-config is still pending; record it with approve.sh agent-config=approve|reject", r.stderr)
        self.assertEqual(self.origin_git("tag"), "")
        self.assertFalse((self.ws / "issues.json").exists())

    def test_nothing_is_deleted_before_the_backup(self):
        r = self.step(CLEANUP, "prepare", ok=False)
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: the tag pre-standard is not on origin; run backup.sh first", r.stderr)
        self.assertFalse((self.repo / WT).exists())
        self.assertEqual(self.git("branch", "--list", "chore/standardize"), "")

    def test_the_cleanup_branch_carries_approved_deletions_and_baseline_files_in_one_pull_request(self):
        before = self.git("status", "--porcelain")
        self.step(BACKUP)
        r = self.step(CLEANUP, "prepare")
        out = r.stdout
        for t in (".claude/skills", ".claude/commands", ".cursor", "skills-lock.json"):
            self.assertIn(f"deleted: {t}\n", out)
        self.assertNotIn("NOTES.md", out)  # files was rejected
        self.assertIn("todo: agent-config replace CLAUDE.md: its own instructions move to AGENTS.md\n", out)
        self.assertIn("todo: tests-ci create Makefile: no check target\n", out)
        self.assertIn("todo: fill the <fill in> placeholders in Makefile\n", out)
        self.assertIn("untouched: files (rejected)\n", out)
        r = self.step(CLEANUP, "open", ok=False)
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: <fill in> placeholders are left in", r.stderr)
        self.fill_in(out)
        r = self.step(CLEANUP, "open")
        self.assertIn("pr: https://github.com/o/r/pull/2 opened\n", r.stdout)

        tree = set(self.origin_git("ls-tree", "-r", "--name-only", "chore/standardize").split())
        self.assertEqual(tree, {
            "README.md", "CLAUDE.md", "NOTES.md", "src/app.py", ".claude/settings.json", "AGENTS.md", "Makefile",
            "docs/architecture.md", "docs/adr/README.md", "docs/adr/template.md", "docs/glossary.md",
            ".github/PULL_REQUEST_TEMPLATE.md", ".github/dependabot.yml", ".github/workflows/check.yml"})
        self.assertEqual(self.origin_git("rev-parse", "chore/standardize~1").strip(), self.head)
        settings = json.loads(self.origin_git("show", "chore/standardize:.claude/settings.json"))
        self.assertEqual(settings["enabledPlugins"], {"foo@bar": False, "orchestrator@workflows": True, "planner@workflows": True,
                                                      "repo-standards@workflows": True, "worker@workflows": True})
        self.assertEqual(settings["extraKnownMarketplaces"]["workflows"]["source"]["repo"], "CalvinDittkrist/workflows")
        self.assertEqual(settings["env"]["WF_REVIEW_ROUNDS"], "5")
        self.assertEqual(settings["attribution"], {"commit": "", "pr": ""})
        self.assertIn("claude plugin disable foo@bar --scope project", self.calls())
        check = self.origin_git("show", "chore/standardize:.github/workflows/check.yml")
        self.assertIn("  check:\n", check)
        self.assertIn("    branches: [main]\n", check)
        self.assertTrue(self.origin_git("show", "chore/standardize:AGENTS.md").startswith("# r\n"))

        pr, = self.get("pulls.json")
        self.assertEqual((pr["title"], pr["head"]["ref"], pr["base"]["ref"]),
                         ("chore: bring the repository to the standard", "chore/standardize", "main"))
        body = pr["body"]
        self.assertIn("### agent-config\n"
                      "- `.claude/skills`: three skills, deploy written for this repository. Restore: `git checkout pre-standard -- .claude/skills`\n"
                      "- `.claude/commands`: one command, written for this repository. Restore: `git checkout pre-standard -- .claude/commands`\n"
                      "- `.cursor`: Cursor rules that repeat CLAUDE.md. Restore: `git checkout pre-standard -- .cursor`\n"
                      "- `skills-lock.json`: lock file of the skills CLI. Restore: `git checkout pre-standard -- skills-lock.json`\n", body)
        self.assertIn("Removed skills are listed in #1.", body)
        self.assertIn("- added `.github/workflows/check.yml`", body)
        self.assertIn("- changed `.claude/settings.json`", body)
        self.assertIn("Rejected in the audit, untouched: files.", body)
        self.assertNotIn("### files", body)
        # The checkout is untouched: same branch, same head, the worktree ignored.
        self.assertEqual(self.git("rev-parse", "HEAD").strip(), self.head)
        self.assertEqual(self.git("status", "--porcelain"), before)

    def test_issue_findings_become_agent_ready_issues_once(self):
        r = self.step(ISSUES)
        self.assertEqual(r.stdout, "opened: #1 Standard (tests-ci): src\nopened: #2 Standard (security): src/app.py\n"
                                   "issues: 2 opened, 0 kept\n")
        first, second = self.get("issues.json")
        self.assertEqual([x["name"] for x in first["labels"]], ["ready-for-agent"])
        self.assertIn("audit of `src`, confidence medium:\n\n> src/app.py has no tests\n", first["body"])
        self.assertIn("ready-for-agent", [x["name"] for x in self.get("labels.json")])
        r = self.step(ISSUES)
        self.assertEqual(r.stdout, "kept: #1 Standard (tests-ci): src\nkept: #2 Standard (security): src/app.py\n"
                                   "issues: 0 opened, 2 kept\n")
        self.assertEqual(len(self.get("issues.json")), 2)

    def test_rejected_issue_findings_open_nothing(self):
        self.run_script(APPROVE, "tests-ci=reject", "security=reject")
        self.assertEqual(self.step(ISSUES).stdout, "issues: none approved\n")
        self.assertFalse((self.ws / "issues.json").exists())

    def test_the_workspace_waits_for_the_merge(self):
        self.through_open()
        self.reset_calls()
        repo = self.get("repo.json")
        r = self.step(FINALIZE, ok=False)
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: the cleanup pull request https://github.com/o/r/pull/2 is not merged yet", r.stderr)
        self.assertEqual([c for c in self.calls() if "--method" in c], [])
        self.assertEqual(self.get("repo.json"), repo)
        self.assertEqual(list(self.ws.glob("ruleset-*.json")), [self.ws / "ruleset-100.json"])  # the tag's only

    def test_after_the_merge_the_workspace_is_applied_its_snapshot_posted_and_the_check_run(self):
        self.through_open()
        self.merge()
        r = self.step(FINALIZE)
        out = r.stdout
        self.assertIn("pr: https://github.com/o/r/pull/2 merged\n", out)
        self.assertIn("workspace: diff: repo allow_rebase_merge: true -> false\n", out)
        self.assertIn("snapshot: posted to #1\n", out)
        comment, = self.get("issues.json")[0]["comments"]
        self.assertIn("diff: repo allow_rebase_merge: true -> false", comment)
        self.assertIn('"allow_rebase_merge": true', comment)
        self.assertFalse(self.get("repo.json")["allow_rebase_merge"])
        self.assertIn(f"worktree: {self.repo / WT} removed\n", out)
        self.assertEqual(self.git("branch", "--list", "chore/standardize"), "")
        self.assertIn("branch: chore/standardize deleted on origin\n", out)
        self.assertEqual(self.origin_git("branch", "--list", "chore/standardize"), "")
        self.assertIn("untouched: files (rejected in the audit)\n", out)
        self.assertTrue(out.endswith("result: pass\n"), out)

    def test_the_check_reports_what_is_left(self):
        self.run_script(APPROVE, "agent-config=reject")
        self.through_open()
        self.merge()
        r = self.step(FINALIZE, ok=False)
        self.assertEqual(r.returncode, 1)
        self.assertIn("check: fail: .claude/skills/deploy: agent configuration the standard does not define; remove it\n", r.stdout)
        self.assertIn("check: fail: AGENTS.md missing; it is the instruction source, move the project instructions there\n", r.stdout)
        self.assertIn("untouched: agent-config, files (rejected in the audit)\n", r.stdout)
        self.assertTrue(r.stdout.endswith("result: fail\n"), r.stdout)

    def test_a_rejected_workspace_is_left_alone(self):
        self.run_script(APPROVE, "workspace=reject")
        self.through_open()
        self.merge()
        repo = self.get("repo.json")
        r = self.step(FINALIZE)
        self.assertIn("workspace: rejected, left untouched\n", r.stdout)
        self.assertEqual(self.get("repo.json"), repo)
        self.assertEqual(self.get("issues.json")[0]["comments"], [])

    def test_a_second_run_changes_nothing(self):
        self.through_open()
        self.step(ISSUES)
        self.merge()
        self.step(FINALIZE)
        self.git("pull", "-q", "origin", "main")
        issues, pulls = self.get("issues.json"), self.get("pulls.json")
        self.reset_calls()
        r = self.step(BACKUP)
        self.assertIn("tag: pre-standard kept at", r.stdout)
        self.assertIn("protection: ruleset standard: pre-standard kept\n", r.stdout)
        self.assertIn("catalogue: #1 unchanged, 4 skills\n", r.stdout)
        r = self.step(CLEANUP, "prepare")
        self.assertIn("gone: .claude/skills\n", r.stdout)
        self.assertNotIn("created:", r.stdout)
        self.assertIn("pr: none needed, main already has every change\n", self.step(CLEANUP, "open").stdout)
        self.assertIn("issues: 0 opened, 2 kept\n", self.step(ISSUES).stdout)
        r = self.step(FINALIZE)
        self.assertIn("workspace: applied: 0\n", r.stdout)
        self.assertNotIn("snapshot:", r.stdout)
        self.assertEqual([c for c in self.calls() if "--method" in c or c.startswith("claude plugin install")], [])
        self.assertEqual((self.get("issues.json"), self.get("pulls.json")), (issues, pulls))
        self.assertEqual(self.origin_git("rev-parse", "refs/tags/pre-standard").strip(), self.head)

    def test_a_prepare_that_stopped_halfway_resumes_in_the_same_worktree(self):
        self.step(BACKUP)
        self.step(CLEANUP, "prepare")
        (self.repo / WT / "AGENTS.md").write_text("# r\nmine\n")
        r = self.step(CLEANUP, "prepare")
        self.assertIn(f"worktree: {self.repo / WT} (resumed)\n", r.stdout)
        self.assertIn("gone: .cursor\n", r.stdout)
        self.assertIn("kept: AGENTS.md\n", r.stdout)
        self.assertEqual((self.repo / WT / "AGENTS.md").read_text(), "# r\nmine\n")

    def test_the_backup_succeeds_without_rulesets_and_names_the_manual_step(self):
        (self.ws / "plan-free").touch()
        r = self.step(BACKUP)
        self.assertIn("manual: protect the tag pre-standard: rulesets cannot be read", r.stdout)
        self.assertEqual(self.origin_git("rev-parse", "refs/tags/pre-standard").strip(), self.head)
        self.assertIn("catalogue: #1 opened, 4 skills\n", r.stdout)

    def test_a_local_tag_on_the_default_branch_is_pushed_where_it_is(self):
        self.git("tag", "pre-standard", "HEAD")
        self.git("commit", "-q", "--allow-empty", "-m", "later")
        self.git("push", "-q", "origin", "main")
        r = self.step(BACKUP)
        self.assertIn(f"tag: pre-standard pushed at {self.head[:7]} (kept at the local tag)\n", r.stdout)
        self.assertEqual(self.origin_git("rev-parse", "refs/tags/pre-standard").strip(), self.head)

    def test_a_local_tag_off_the_default_branch_is_refused(self):
        self.git("commit", "-q", "--allow-empty", "-m", "unpushed")
        self.git("tag", "pre-standard")
        r = self.step(BACKUP, ok=False)
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: the local tag pre-standard is not on main", r.stderr)
        self.assertEqual(self.origin_git("tag"), "")

    def test_a_catalogue_edited_by_hand_is_brought_back(self):
        self.step(BACKUP)
        self.put("issues.json", [dict(i, body="edited") for i in self.get("issues.json")])
        r = self.step(BACKUP)
        self.assertIn("catalogue: #1 updated, 4 skills\n", r.stdout)
        self.assertIn("| deploy |", self.get("issues.json")[0]["body"])
        self.assertEqual(len(self.get("issues.json")), 1)

    def test_a_target_changed_since_the_tag_or_untracked_is_not_deleted(self):
        self.origin_git("tag", "pre-standard", self.head)
        self.write(".cursor/rules/new.mdc", "newer\n")
        self.write(".claude/skills/deploy/run.sh", "#!/bin/sh\necho deploy v2\n")
        self.git("add", ".")
        self.git("commit", "-qm", "newer rule")
        self.git("push", "-q", "origin", "main")
        self.write("GEMINI.md", "untracked\n")
        per_skill = REPLIES.replace(
            "finding: agent-config | .claude/skills | delete | three skills, deploy written for this repository | high\n",
            "finding: agent-config | .claude/skills/deploy | delete | written for this repository | high\n"
            "finding: agent-config | .claude/skills/review | delete | copied from upstream | high\n")
        self.run_script(REPORT, stdin=per_skill + "finding: agent-config | GEMINI.md | delete | Gemini instructions | high\n")
        self.run_script(APPROVE, "agent-config=approve", "tests-ci=approve", "security=approve", "workspace=approve",
                        "files=reject")
        self.step(BACKUP)
        r = self.step(CLEANUP, "prepare")
        self.assertIn("skipped: .cursor changed since the tag pre-standard was set, so the tag cannot restore it", r.stdout)
        catalogue = self.get("issues.json")[0]["body"]
        self.assertNotIn("| deploy |", catalogue)
        self.assertIn("| review |", catalogue)
        self.assertIn("local: GEMINI.md is not tracked", r.stdout)
        self.assertIn("skipped: .claude/skills/deploy changed since the tag", r.stdout)
        self.assertIn("deleted: .claude/skills/review\n", r.stdout)
        self.assertTrue((self.repo / WT / ".cursor/rules/new.mdc").exists())
        self.assertTrue((self.repo / "GEMINI.md").exists())

    def test_the_description_follows_the_branch_and_ignores_later_commits_on_main(self):
        self.through_open()
        self.write("src/app.py", "print('later')\n")  # someone else's change lands on main meanwhile
        self.git("commit", "-qam", "later")
        self.git("push", "-q", "origin", "main")
        self.write("docs/runbook.md", "# Runbook\n", root=self.repo / WT)
        r = self.step(CLEANUP, "open")
        self.assertIn("pr: https://github.com/o/r/pull/2 updated\n", r.stdout)
        pr, = self.get("pulls.json")
        added = pr["body"].split("## Added and changed\n")[1].split("\n\n")[0].splitlines()
        self.assertEqual(added, ["- changed `.claude/settings.json`", "- added `.github/PULL_REQUEST_TEMPLATE.md`",
                                 "- added `.github/dependabot.yml`", "- added `.github/workflows/check.yml`",
                                 "- added `AGENTS.md`", "- changed `CLAUDE.md`", "- added `Makefile`",
                                 "- added `docs/adr/README.md`", "- added `docs/adr/template.md`",
                                 "- added `docs/architecture.md`", "- added `docs/glossary.md`", "- added `docs/runbook.md`"])
        self.assertIn("- `.cursor`: Cursor rules that repeat CLAUDE.md.", pr["body"])
        self.assertEqual(self.step(CLEANUP, "open").stdout.splitlines()[0], "pr: https://github.com/o/r/pull/2 unchanged")

    def test_a_failing_commit_hook_stops_open_and_a_second_open_continues(self):
        self.step(BACKUP)
        self.fill_in(self.step(CLEANUP, "prepare").stdout)
        hook = self.repo / ".git/hooks/pre-commit"
        hook.write_text("#!/bin/sh\necho 'lint: trailing space in AGENTS.md' >&2\nexit 1\n")
        hook.chmod(0o755)
        r = self.step(CLEANUP, "open", ok=False)
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: cannot commit in", r.stderr)
        self.assertIn("lint: trailing space in AGENTS.md", r.stderr.splitlines()[-1])
        self.assertIn("fix what the repository's commit hooks report there", r.stderr)
        self.assertFalse((self.ws / "pulls.json").exists())
        self.assertEqual(self.origin_git("branch", "--list", "chore/standardize"), "")
        hook.unlink()
        self.assertIn("pr: https://github.com/o/r/pull/2 opened\n", self.step(CLEANUP, "open").stdout)

    def test_a_commit_no_pull_request_carries_is_never_thrown_away(self):
        self.through_open()
        self.merge()
        # A new commit on the old branch whose push failed: no open pull request carries it.
        self.write("docs/runbook.md", "# Runbook\n", root=self.repo / WT)
        self.git("add", ".", cwd=self.repo / WT)
        self.git("commit", "-qm", "more", cwd=self.repo / WT)
        head = self.git("rev-parse", "HEAD", cwd=self.repo / WT).strip()
        r = self.step(FINALIZE, ok=False)
        self.assertEqual(r.returncode, 1)
        self.assertIn("has a commit that no pull request carries; run cleanup.sh open", r.stderr)
        self.assertEqual(self.git("rev-parse", "chore/standardize").strip(), head)
        self.assertEqual(self.origin_git("branch", "--list", "chore/standardize").strip(), "chore/standardize")
        self.assertTrue(self.get("repo.json")["allow_rebase_merge"])  # the workspace waited

    def test_a_worktree_behind_the_merged_pull_request_is_done_not_pending(self):
        self.through_open()
        # Someone commits a review suggestion on GitHub; the worktree never sees it.
        clone = self.base / "clone"
        self.git("clone", "-q", "-b", "chore/standardize", str(self.origin), str(clone))
        self.write("docs/suggested.md", "x\n", root=clone)
        self.git("add", ".", cwd=clone)
        self.git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "suggestion", cwd=clone)
        self.git("push", "-q", "origin", "chore/standardize", cwd=clone)
        self.merge()
        self.origin_git("branch", "-D", "chore/standardize")  # deleted on merge
        r = self.step(FINALIZE)
        self.assertIn(f"worktree: {self.repo / WT} removed\n", r.stdout)

    def test_a_branch_on_origin_ahead_of_the_merged_pull_request_is_kept(self):
        self.through_open()
        self.merge()
        clone = self.base / "clone"
        self.git("clone", "-q", "-b", "chore/standardize", str(self.origin), str(clone))
        self.write("docs/late.md", "x\n", root=clone)
        self.git("add", ".", cwd=clone)
        self.git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "late", cwd=clone)
        self.git("push", "-q", "origin", "chore/standardize", cwd=clone)
        late = self.git("rev-parse", "HEAD", cwd=clone).strip()
        r = self.step(FINALIZE)
        self.assertIn("branch: chore/standardize kept on origin, it has commits the merged pull request does not\n", r.stdout)
        self.assertEqual(self.origin_git("rev-parse", "refs/heads/chore/standardize").strip(), late)

    def test_an_unreachable_origin_is_named_as_such(self):
        self.step(BACKUP)
        self.git("remote", "set-url", "origin", str(self.base / "missing.git"))
        r = self.step(CLEANUP, "prepare", ok=False)
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: cannot reach origin: ", r.stderr)

    def test_a_workspace_that_refuses_leaves_no_snapshot_and_a_rerun_finishes(self):
        self.through_open()
        self.merge()
        (self.ws / "check-runs").write_text("0")
        r = self.step(FINALIZE, ok=False)
        self.assertIn("workspace: failed", r.stdout)
        self.assertNotIn("snapshot:", r.stdout)
        self.assertEqual(self.get("issues.json")[0]["comments"], [])
        self.assertEqual(list((self.repo / ".git/standardize").glob("workspace-snapshot*")), [])
        self.assertTrue(r.stdout.endswith("result: fail\n"), r.stdout)
        (self.ws / "check-runs").write_text("1")
        r = self.step(FINALIZE)
        self.assertIn("snapshot: posted to #1\n", r.stdout)
        self.assertTrue(r.stdout.endswith("result: pass\n"), r.stdout)

    def test_a_pull_request_closed_without_a_merge_is_refused(self):
        self.through_open()
        self.put("pulls.json", [dict(p, state="closed") for p in self.get("pulls.json")])
        r = self.step(FINALIZE, ok=False)
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: the cleanup pull request https://github.com/o/r/pull/2 was closed without a merge", r.stderr)

    def test_prepared_changes_without_a_pull_request_hold_the_workspace_back(self):
        self.step(BACKUP)
        self.step(CLEANUP, "prepare")
        r = self.step(FINALIZE, ok=False)
        self.assertEqual(r.returncode, 1)
        self.assertIn("has uncommitted changes that no pull request carries; run cleanup.sh open", r.stderr)

    def test_a_merged_branch_left_on_origin_is_not_reused(self):
        self.through_open()
        self.merge()
        self.git("worktree", "remove", "--force", str(self.repo / WT))
        r = self.step(CLEANUP, "prepare", ok=False)
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: chore/standardize on origin belongs to a merged pull request; run finalize.sh", r.stderr)

    def test_a_workspace_that_fails_halfway_keeps_its_snapshot_on_the_catalogue(self):
        self.through_open()
        self.merge()
        r = self.step(FINALIZE, ok=False, SHIM_WS_FAIL="api --method POST repos/o/r/labels")
        self.assertEqual(r.returncode, 1)
        self.assertIn("workspace: failed; fix the error above and run finalize.sh again\n", r.stdout)
        self.assertIn("snapshot: posted to #1\n", r.stdout)
        comment, = self.get("issues.json")[0]["comments"]
        self.assertIn('"allow_rebase_merge": true', comment)
        self.assertTrue(r.stdout.endswith("result: fail\n"), r.stdout)



EMPTY_REPLIES = """finding: docs | README.md | create | the repository has no README | high
finding: agent-config | AGENTS.md | create | no instruction source | high
finding: tests-ci | Makefile | create | no gate | high
finding: workspace | merge settings | configure | rebase merges are allowed | high
"""


class EmptyRepositoryApplyTests(ApplyCase):
    """A repository without a commit, here or on GitHub: the init case of the same run."""

    def setUp(self):
        super().setUp()
        self.git("update-ref", "-d", "HEAD")  # the base repository's first commit is undone: nothing is committed
        self.git("rm", "-q", "--cached", "README.md")
        (self.repo / "README.md").unlink()
        self.write("src/main.py", "print('draft')\n")  # work the maintainer has not committed yet
        self.github()
        self.audit(EMPTY_REPLIES, "docs=approve", "agent-config=approve", "tests-ci=approve", "workspace=approve")

    def test_the_run_starts_the_default_branch_and_brings_the_standard_through_one_pull_request(self):
        r = self.step(BACKUP)
        root = self.origin_git("rev-parse", "refs/heads/main").strip()
        self.assertIn(f"root: {root[:7]} pushed as the first commit of main (the repository was empty)\n", r.stdout)
        self.assertEqual(self.origin_git("ls-tree", "-r", "--name-only", root), "")
        self.assertEqual(self.origin_git("rev-parse", "refs/tags/pre-standard^{commit}").strip(), root)
        self.assertIn("No skills are removed.", self.get("issues.json")[0]["body"])
        self.fill_in(self.step(CLEANUP, "prepare").stdout)
        r = self.step(CLEANUP, "open")
        self.assertIn("pr: https://github.com/o/r/pull/2 opened\n", r.stdout)
        added = self.origin_git("diff", "--name-only", root, "refs/heads/chore/standardize").split()
        for f in ("README.md", "AGENTS.md", "CLAUDE.md", "Makefile", ".github/workflows/check.yml", ".claude/settings.json"):
            self.assertIn(f, added)
        self.merge()
        r = self.step(FINALIZE)
        self.assertIn("workspace: diff: repo allow_rebase_merge: true -> false\n", r.stdout)
        self.assertIn("next: the checkout has no commit yet; git pull origin main brings the standard into it\n", r.stdout)
        self.assertTrue(r.stdout.endswith("result: pass\n"), r.stdout)
        # The checkout is untouched: still without a commit, the maintainer's draft still there.
        self.assertEqual(subprocess.run(["git", "rev-parse", "-q", "--verify", "HEAD"], cwd=self.repo,
                                        capture_output=True).returncode, 1)
        self.assertEqual((self.repo / "src/main.py").read_text(), "print('draft')\n")
        # A second run finds the first commit and the tag and starts nothing new.
        r = self.step(BACKUP)
        self.assertNotIn("root:", r.stdout)
        self.assertIn(f"tag: pre-standard kept at {root[:7]} (pushed before)\n", r.stdout)

    def test_local_commits_that_were_never_pushed_are_not_replaced(self):
        self.write("README.md", "mine\n")
        self.git("add", "README.md")
        self.git("commit", "-qm", "first")
        r = self.step(BACKUP, ok=False)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: origin has no branch main", r.stderr)
        self.assertIn("git push -u origin HEAD", r.stderr)
        self.assertEqual(self.origin_git("for-each-ref"), "")


if __name__ == "__main__":
    unittest.main()
