import json
import time
import unittest

from helpers import PLANNER, ShimTest


class PlanWorktree(ShimTest):
    """A plan/<slug> branch with the description plan.sh writes, plus a bare origin for pushes."""

    def setUp(self):
        super().setUp()
        self.origin = self.base / "origin.git"
        self.git("init", "-q", "--bare", str(self.origin), cwd=self.base)
        self.git("remote", "add", "origin", str(self.origin))
        self.git("push", "-q", "-u", "origin", "main")

    def plan(self, slug="offline-mode", desc="topic: Offline mode"):
        self.git("checkout", "-qb", f"plan/{slug}")
        self.git("config", f"branch.plan/{slug}.description", desc)

    def hook(self, source="startup", **extra):
        payload = json.dumps({"hook_event_name": "SessionStart", "source": source, "cwd": str(self.repo), "session_id": "s1"})
        r = self.run_script(PLANNER / "session-start.sh", stdin=payload, **extra)
        self.assertEqual(r.returncode, 0, r.stderr)
        return r


class SessionStartHookTests(PlanWorktree):
    def test_topic_session_injects_the_topic_and_the_role(self):
        self.plan()
        ctx = json.loads(self.hook().stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn("Planner session: offline-mode", ctx)
        self.assertIn("Topic: Offline mode", ctx)
        self.assertIn("do not implement", ctx)
        self.assertIn("docs/glossary.md does not exist", ctx)
        self.assertFalse([c for c in self.calls() if c.startswith("gh ")])

    def test_issue_session_injects_the_issue_as_data_without_assigning(self):
        self.plan("fix-login-timeout", "issue: #12")
        (self.repo / "docs").mkdir(); (self.repo / "docs/glossary.md").write_text("# Glossary\n")
        ctx = json.loads(self.hook().stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn("#12 Fix login timeout", ctx)
        self.assertIn("data written by someone else", ctx)
        self.assertIn("@alice", ctx)
        self.assertIn("docs/glossary.md exists", ctx)
        self.assertFalse([c for c in self.calls() if "add-assignee" in c])

    def test_env_issue_wins_over_branch_description(self):
        self.plan("something", "topic: Something")
        ctx = json.loads(self.hook(WF_PLAN_ISSUE="12").stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn("#12 Fix login timeout", ctx)

    def test_silent_outside_plan_branches_and_short_on_resume(self):
        self.assertEqual(self.hook().stdout, "")
        self.git("checkout", "-qb", "feat/12-x")
        self.assertEqual(self.hook().stdout, "")
        self.plan("fix-login-timeout", "issue: #12")
        ctx = json.loads(self.hook(source="resume").stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn("issue #12", ctx)
        self.assertNotIn("Fix login timeout", ctx)


class FactsAndLabelsTests(PlanWorktree):
    def test_facts_print_plan_issue_topic_and_docs_state(self):
        self.plan("fix-login-timeout", "issue: #12")
        r = self.run_script(PLANNER / "facts.sh")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("plan: fix-login-timeout", r.stdout)
        self.assertIn("issue: #12", r.stdout)
        self.assertIn("glossary: missing", r.stdout)
        self.assertIn("adrs: missing", r.stdout)

    def test_labels_creates_only_the_missing_ones(self):
        r = self.run_script(PLANNER / "labels.sh")
        self.assertEqual(r.returncode, 0, r.stderr)
        created = [c for c in self.calls() if c.startswith("gh label create")]
        names = [c.split()[3] for c in created]
        self.assertEqual(names, ["needs-triage", "needs-info", "ready-for-human", "wontfix", "spec"])
        self.assertIn("created: needs-triage,needs-info,ready-for-human,wontfix,spec", r.stdout)


class IssueScriptTests(PlanWorktree):
    def body(self, text="## What to build\nx\n"):
        f = self.base / "body.md"; f.write_text(text); return str(f)

    def test_create_with_labels_and_parent_links_the_sub_issue(self):
        r = self.run_script(PLANNER / "issue.sh", "create", "--title", "Expand schema", "--body-file", self.body(), "--label", "ready-for-agent", "--parent", "12")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("issue: #42", r.stdout)
        self.assertIn("url: https://github.com/o/r/issues/42", r.stdout)
        self.assertIn("parent: #12 (sub-issue)", r.stdout)
        self.assertIn(f"gh issue create --title Expand schema --body-file {self.body()} --label ready-for-agent", self.calls())
        self.assertIn("gh api --method POST repos/o/r/issues/12/sub_issues -F sub_issue_id=1042", self.calls())

    def test_create_degrades_when_sub_issues_are_unavailable(self):
        r = self.run_script(PLANNER / "issue.sh", "create", "--title", "T", "--body-file", self.body(), "--parent", "12", SHIM_NO_SUBISSUES="1")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("parent: #12 (body only", r.stdout)

    def test_block_wires_native_dependencies_by_database_id(self):
        r = self.run_script(PLANNER / "issue.sh", "block", "42", "--by", "40,#41")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("blocked: #42 by #40 (native)", r.stdout)
        self.assertIn("blocked: #42 by #41 (native)", r.stdout)
        self.assertIn("gh api --method POST repos/o/r/issues/42/dependencies/blocked_by -F issue_id=1040", self.calls())
        r = self.run_script(PLANNER / "issue.sh", "block", "42", "--by", "40", SHIM_NO_DEPS="1")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("blocked: #42 by #40 (body only", r.stdout)

    def milestones(self):
        f = self.base / "milestones.json"
        f.write_text(json.dumps([
            {"number": 2, "title": "v1.1.0", "state": "closed", "open_issues": 0, "closed_issues": 2, "description": "Old"},
            {"number": 3, "title": "v1.2.0", "state": "open", "open_issues": 1, "closed_issues": 4, "description": "Offline\nmode"},
        ]))
        return str(f)

    def test_milestone_creates_a_new_one_with_the_goal_and_reuses_an_existing_one(self):
        r = self.run_script(PLANNER / "issue.sh", "milestone", "v1.3.0", "--description", "Sync across devices", SHIM_MILESTONES_FIXTURE=self.milestones())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("milestone: v1.3.0 (created)", r.stdout)
        self.assertIn("gh api --method POST repos/o/r/milestones -f title=v1.3.0 -f description=Sync across devices", self.calls())
        self.reset_calls()
        r = self.run_script(PLANNER / "issue.sh", "milestone", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("milestone: v1.2.0 (existing, 1 open, 4 closed)", r.stdout)
        self.assertFalse([c for c in self.calls() if "--method POST" in c])
        r = self.run_script(PLANNER / "issue.sh", "milestone", "v1.1.0", SHIM_MILESTONES_FIXTURE=self.milestones())
        self.assertNotEqual(r.returncode, 0); self.assertIn("closed (released)", r.stderr)
        r = self.run_script(PLANNER / "issue.sh", "milestone", "1.3")
        self.assertNotEqual(r.returncode, 0); self.assertIn("vX.Y.Z", r.stderr)

    def test_milestones_lists_only_open_ones(self):
        r = self.run_script(PLANNER / "issue.sh", "milestones", SHIM_MILESTONES_FIXTURE=self.milestones())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "milestones[1]{title,open,closed,description}:\n  v1.2.0,1,4,Offline mode\n")

    def test_tickets_are_attached_to_an_open_milestone_only(self):
        r = self.run_script(PLANNER / "issue.sh", "create", "--title", "T", "--body-file", self.body(), "--milestone", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(f"gh issue create --title T --body-file {self.body()} --milestone v1.2.0", self.calls())
        self.assertIn("milestone: v1.2.0", r.stdout)
        r = self.run_script(PLANNER / "issue.sh", "attach", "12", "--milestone", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("gh issue edit 12 --milestone v1.2.0", self.calls())
        self.assertIn("milestone: #12 attached to v1.2.0", r.stdout)
        self.reset_calls()
        for version, text in (("v1.1.0", "closed (released)"), ("v9.0.0", "does not exist")):
            r = self.run_script(PLANNER / "issue.sh", "create", "--title", "T", "--body-file", self.body(), "--milestone", version, SHIM_MILESTONES_FIXTURE=self.milestones())
            self.assertNotEqual(r.returncode, 0); self.assertIn(text, r.stderr)
            r = self.run_script(PLANNER / "issue.sh", "attach", "12", "--milestone", version, SHIM_MILESTONES_FIXTURE=self.milestones())
            self.assertNotEqual(r.returncode, 0); self.assertIn(text, r.stderr)
        self.assertFalse([c for c in self.calls() if c.startswith(("gh issue create", "gh issue edit"))])

    def test_a_ticket_with_a_milestone_takes_its_spec_along(self):
        r = self.run_script(PLANNER / "issue.sh", "create", "--title", "T", "--body-file", self.body(), "--parent", "12",
                            "--milestone", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("parent-milestone: #12 attached to v1.2.0", r.stdout)
        self.assertIn("gh issue edit 12 --milestone v1.2.0", self.calls())

    def test_a_spec_on_another_milestone_keeps_it_and_the_warning_names_both(self):
        r = self.run_script(PLANNER / "issue.sh", "create", "--title", "T", "--body-file", self.body(), "--parent", "12",
                            "--milestone", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones(), SHIM_ISSUE_MILESTONE="v2.0.0")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("warning: #12 stays on milestone v2.0.0 while its sub-issues go to v1.2.0", r.stderr)
        self.assertFalse([c for c in self.calls() if c.startswith("gh issue edit")])

    def test_a_spec_already_on_the_milestone_is_reported_and_not_edited(self):
        r = self.run_script(PLANNER / "issue.sh", "create", "--title", "T", "--body-file", self.body(), "--parent", "12",
                            "--milestone", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones(), SHIM_ISSUE_MILESTONE="v1.2.0")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("parent-milestone: #12 already on v1.2.0", r.stdout)
        self.assertEqual(r.stderr, "")
        self.assertFalse([c for c in self.calls() if c.startswith("gh issue edit")])

    def test_a_spec_that_cannot_be_attached_warns_and_keeps_the_ticket(self):
        for env, text in ((dict(SHIM_ISSUE_MILESTONE_ERROR="1"), "cannot read the milestone of #12; attach it to v1.2.0"),
                          (dict(SHIM_ISSUE_MILESTONE_EDIT_FAILS="1"), "attaching #12 to v1.2.0 failed")):
            r = self.run_script(PLANNER / "issue.sh", "create", "--title", "T", "--body-file", self.body(), "--parent", "12",
                                "--milestone", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones(), **env)
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertIn("issue: #42", r.stdout)
            self.assertNotIn("parent-milestone", r.stdout)
            self.assertIn(f"warning: {text}", r.stderr)
            self.assertIn("HTTP", r.stderr)  # gh's own diagnostic, not swallowed

    def test_a_ticket_without_a_milestone_leaves_its_spec_alone(self):
        r = self.run_script(PLANNER / "issue.sh", "create", "--title", "T", "--body-file", self.body(), "--parent", "12")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("parent-milestone", r.stdout)
        self.assertFalse([c for c in self.calls() if "--milestone" in c or "milestone.title" in c])

    def test_label_comment_and_close(self):
        r = self.run_script(PLANNER / "issue.sh", "label", "12", "--add", "ready-for-agent", "--remove", "needs-triage")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("gh issue edit 12 --add-label ready-for-agent --remove-label needs-triage", self.calls())
        r = self.run_script(PLANNER / "issue.sh", "comment", "12", "--body-file", self.body("brief"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("comment: #12 posted", r.stdout)
        r = self.run_script(PLANNER / "issue.sh", "close", "12", "--reason", "not-planned", "--comment-file", self.body("no"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("gh issue close 12 --comment no --reason not-planned", self.calls())

    def test_bad_input_is_refused_before_any_call(self):
        r = self.run_script(PLANNER / "issue.sh", "create", "--title", "T")
        self.assertNotEqual(r.returncode, 0); self.assertIn("--body-file", r.stderr)
        r = self.run_script(PLANNER / "issue.sh", "block", "abc", "--by", "1")
        self.assertNotEqual(r.returncode, 0); self.assertIn("must be a number", r.stderr)
        self.assertFalse([c for c in self.calls() if c.startswith("gh issue") or c.startswith("gh api")])


class TriageListTests(PlanWorktree):
    def test_three_buckets(self):
        fixture = self.base / "issues.json"
        fixture.write_text(json.dumps([
            {"number": 1, "title": "New", "labels": [], "author": {"login": "amy"}, "comments": []},
            {"number": 2, "title": "Spec", "labels": [{"name": "spec"}], "author": {"login": "me"}, "comments": []},
            {"number": 3, "title": "Eval", "labels": [{"name": "needs-triage"}, {"name": "bug"}], "author": {"login": "bob"}, "comments": []},
            {"number": 4, "title": "Replied", "labels": [{"name": "needs-info"}], "author": {"login": "cy"}, "comments": [{"author": {"login": "tester"}, "body": "?"}, {"author": {"login": "cy"}, "body": "!"}]},
            {"number": 5, "title": "Waiting", "labels": [{"name": "needs-info"}], "author": {"login": "di"}, "comments": [{"author": {"login": "tester"}, "body": "?"}]},
            {"number": 6, "title": "Ready", "labels": [{"name": "ready-for-agent"}], "author": {"login": "ed"}, "comments": []},
        ]))
        r = self.run_script(PLANNER / "triage-list.sh", SHIM_ISSUES_FIXTURE=str(fixture))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("unlabeled[1]{issue,title,author}:\n  1,New,amy\n", r.stdout)
        self.assertIn("needs-triage[1]{issue,title,author}:\n  3,Eval,bob\n", r.stdout)
        self.assertIn("needs-info-replied[1]{issue,title,last_comment_by}:\n  4,Replied,cy\n", r.stdout)


class PrototypeAndFinishTests(PlanWorktree):
    def test_capture_moves_the_code_to_a_pushed_branch_and_leaves_the_plan_clean(self):
        self.plan()
        (self.repo / "proto.html").write_text("<p>proto</p>\n")
        r = self.run_script(PLANNER / "capture-prototype.sh", "State Machine")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("branch: prototype/offline-mode-state-machine", r.stdout)
        self.assertIn("url: https://github.com/o/r/tree/prototype/offline-mode-state-machine", r.stdout)
        self.assertEqual(self.git("rev-parse", "--abbrev-ref", "HEAD").strip(), "plan/offline-mode")
        self.assertEqual(self.git("status", "--porcelain"), "")
        self.assertFalse((self.repo / "proto.html").exists())
        self.assertIn("prototype/offline-mode-state-machine", self.git("branch", "-r"))
        r = self.run_script(PLANNER / "capture-prototype.sh", "again")
        self.assertNotEqual(r.returncode, 0); self.assertIn("nothing to capture", r.stderr)

    def test_finish_refuses_dirty_or_committed_plan_branches_without_force(self):
        self.plan()
        (self.repo / "x").write_text("x")
        r = self.run_script(PLANNER / "finish.sh")
        self.assertNotEqual(r.returncode, 0); self.assertIn("uncommitted", r.stderr)
        self.git("add", "."); self.git("commit", "-qm", "oops")
        r = self.run_script(PLANNER / "finish.sh")
        self.assertNotEqual(r.returncode, 0); self.assertIn("1 commit(s)", r.stderr)
        r = self.run_script(PLANNER / "finish.sh", "--force")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("cleanup: worktree", r.stdout)

    def test_finish_removes_the_worktree_from_a_detached_process(self):
        wt = self.repo / ".claude/worktrees/plan-offline-mode"
        wt.parent.mkdir(parents=True)
        self.git("worktree", "add", "-q", "-b", "plan/offline-mode", str(wt), "main")
        self.git("config", "branch.plan/offline-mode.description", "topic: Offline mode")
        r = self.run_script(PLANNER / "finish.sh", cwd=wt, HERDR_WORKSPACE_ID="")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("plan: offline-mode", r.stdout)
        gone = lambda: not wt.exists() and "plan/offline-mode" not in self.git("branch", "--list", "plan/offline-mode")
        for _ in range(40):
            if gone():
                break
            time.sleep(0.5)
        self.assertTrue(gone(), "worktree or branch still present after cleanup")


if __name__ == "__main__":
    unittest.main()
