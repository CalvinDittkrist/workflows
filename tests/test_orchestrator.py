import json
import unittest

from helpers import ORCH, ShimTest


class ClaimTests(ShimTest):
    def test_claim_creates_branch_worktree_and_starts_worker(self):
        r = self.run_script(ORCH / "claim.sh", "12")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("branch: fix/12-fix-login-timeout", r.stdout)
        self.assertIn("mode: manual", r.stdout)
        branches = self.git("branch", "--list", "fix/12-fix-login-timeout")
        self.assertIn("fix/12-fix-login-timeout", branches)
        start = [c for c in self.calls() if c.startswith("herdr agent start")]
        self.assertEqual(len(start), 1)
        self.assertIn("--agent worker", start[0])
        self.assertIn("/worker:work", start[0])
        settings = json.loads(start[0].split("--settings ")[1].split(" --name")[0])
        self.assertEqual(settings["env"], {"WF_MODE": "manual", "WF_ISSUE": "12"})
        self.assertTrue((self.repo / ".claude/worktrees/fix-12-fix-login-timeout/README.md").exists())
        self.assertIn(".claude/worktrees/", (self.repo / ".git/info/exclude").read_text())
        self.assertEqual(self.git("status", "--porcelain"), "", "worktree dir must not show up as untracked")
        self.assertIn("herdr workspace focus wR", self.calls())
        self.assertIn("agent_status: working", r.stdout)

    def test_yolo_flag_is_passed_to_the_worker_session(self):
        r = self.run_script(ORCH / "claim.sh", "12", "--yolo")
        self.assertEqual(r.returncode, 0, r.stderr)
        start = [c for c in self.calls() if c.startswith("herdr agent start")][0]
        self.assertIn('"WF_MODE":"yolo"', start)

    def test_claude_args_are_word_split_into_the_worker_session_argv(self):
        r = self.run_script(ORCH / "claim.sh", "12", WF_CLAUDE_ARGS=f"--model sonnet --plugin-dir {self.base}")
        self.assertEqual(r.returncode, 0, r.stderr)
        argv = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        # Separate entries, not one "--model sonnet --plugin-dir /x" blob: claim.sh must leave $extra unquoted.
        self.assertEqual(argv[argv.index("--model") + 1], "sonnet")
        self.assertEqual(argv[argv.index("--plugin-dir") + 1], str(self.base))
        self.assertEqual(argv[-1], "/worker:work")

    def test_claim_is_idempotent_for_an_existing_worktree(self):
        self.run_script(ORCH / "claim.sh", "12")
        self.reset_calls()
        r = self.run_script(ORCH / "claim.sh", "12")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("status: already-claimed", r.stdout)
        self.assertFalse([c for c in self.calls() if "worktree create" in c or "agent start" in c])

    def test_closed_or_missing_issue_is_refused(self):
        for issue, text in (("13", "CLOSED"), ("99", "not found")):
            r = self.run_script(ORCH / "claim.sh", issue)
            self.assertNotEqual(r.returncode, 0)
            self.assertIn(text, r.stderr)
        self.assertEqual(self.git("worktree", "list").count("\n"), 1)

    def test_claim_refuses_outside_herdr(self):
        r = self.run_script(ORCH / "claim.sh", "12", HERDR_ENV="")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("HERDR_ENV", r.stderr)


class PlanTests(ShimTest):
    def test_plan_from_idea_opens_a_plan_worktree_and_starts_the_planner(self):
        r = self.run_script(ORCH / "plan.sh", "Offline", "mode", "for", "the", "app")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("branch: plan/offline-mode-for-the-app", r.stdout)
        self.assertIn("topic: Offline mode for the app", r.stdout)
        self.assertTrue((self.repo / ".claude/worktrees/plan-offline-mode-for-the-app/README.md").exists())
        self.assertEqual(self.git("config", "branch.plan/offline-mode-for-the-app.description").strip(), "topic: Offline mode for the app")
        start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        self.assertIn("--agent", start); self.assertEqual(start[start.index("--agent") + 1], "planner")
        self.assertEqual(start[-1], "/planner:plan")
        settings = json.loads(start[start.index("--settings") + 1])
        self.assertEqual(settings["env"], {"WF_PLAN": "offline-mode-for-the-app"})
        self.assertIn("agent_status: working", r.stdout)
        self.assertFalse([c for c in self.calls() if "issue view" in c])

    def test_plan_from_issue_uses_its_title_and_records_the_issue(self):
        r = self.run_script(ORCH / "plan.sh", "#12")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("branch: plan/fix-login-timeout", r.stdout)
        self.assertIn("issue: #12 Fix login timeout", r.stdout)
        self.assertEqual(self.git("config", "branch.plan/fix-login-timeout.description").strip(), "issue: #12")
        start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        settings = json.loads(start[start.index("--settings") + 1])
        self.assertEqual(settings["env"], {"WF_PLAN": "fix-login-timeout", "WF_PLAN_ISSUE": "12"})

    def test_plan_slug_transliterates_umlauts_and_drops_urls(self):
        r = self.run_script(ORCH / "plan.sh", "Füge", "einen", "Map-Skill", "hinzu,", "wie", "https://github.com/mattpocock/skills")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("branch: plan/fuege-einen-map-skill-hinzu-wie", r.stdout)

    def test_failed_session_start_rolls_back_worktree_and_branch(self):
        r = self.run_script(ORCH / "plan.sh", "Offline mode", SHIM_AGENT_START_FAILS="1", WF_AGENT_WAIT="0")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("--agent 'planner' not found", r.stderr)
        self.assertIn("WF_CLAUDE_ARGS", r.stderr)
        self.assertIn("removed", r.stderr)
        self.assertFalse((self.repo / ".claude/worktrees/plan-offline-mode").exists())
        self.assertEqual(self.git("branch", "--list", "plan/offline-mode"), "")
        self.assertIn("herdr worktree remove --workspace w9 --force", self.calls())
        r = self.run_script(ORCH / "claim.sh", "12", SHIM_AGENT_START_FAILS="1", WF_AGENT_WAIT="0")
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(self.git("branch", "--list", "fix/12-fix-login-timeout"), "")

    def test_broken_claude_args_are_refused_before_anything_is_created(self):
        for args in ("--plugin-dir", "--plugin-dir --model sonnet", "--plugin-dir /nonexistent/dir"):
            r = self.run_script(ORCH / "plan.sh", "Offline mode", WF_CLAUDE_ARGS=args)
            self.assertNotEqual(r.returncode, 0, args)
            self.assertIn("WF_CLAUDE_ARGS", r.stderr)
            self.assertFalse([c for c in self.calls() if "worktree create" in c], args)
        r = self.run_script(ORCH / "claim.sh", "12", WF_CLAUDE_ARGS="--plugin-dir")
        self.assertNotEqual(r.returncode, 0)

    def test_session_back_at_the_shell_prompt_rolls_back_too(self):
        r = self.run_script(ORCH / "plan.sh", "Offline mode", SHIM_AGENT_START_FAILS="2", WF_AGENT_WAIT="0")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("exited right after start", r.stderr)
        self.assertIn("argument missing", r.stderr)
        self.assertEqual(self.git("branch", "--list", "plan/offline-mode"), "")

    def test_plan_is_idempotent_and_refuses_closed_issues_and_non_herdr(self):
        self.run_script(ORCH / "plan.sh", "Offline mode")
        self.reset_calls()
        r = self.run_script(ORCH / "plan.sh", "Offline mode")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("status: already-open", r.stdout)
        self.assertFalse([c for c in self.calls() if "worktree create" in c or "agent start" in c])
        r = self.run_script(ORCH / "plan.sh", "13")
        self.assertNotEqual(r.returncode, 0); self.assertIn("CLOSED", r.stderr)
        r = self.run_script(ORCH / "plan.sh", "Offline mode", HERDR_ENV="")
        self.assertNotEqual(r.returncode, 0); self.assertIn("HERDR_ENV", r.stderr)


class MergeTests(ShimTest):
    def pr_fixture(self, **over):
        pr = {"number": 7, "title": "fix: login timeout", "url": "https://github.com/o/r/pull/7", "state": "OPEN",
              "isDraft": False, "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN", "headRefName": "fix/12-fix-login-timeout",
              "baseRefName": "main", "reviewDecision": "APPROVED",
              "statusCheckRollup": [{"name": "ci", "status": "COMPLETED", "conclusion": "SUCCESS"}]}
        pr.update(over)
        f = self.base / "pr.json"
        f.write_text(json.dumps(pr))
        return str(f)

    def claimed(self):
        self.run_script(ORCH / "claim.sh", "12")
        path = self.repo / ".claude/worktrees/fix-12-fix-login-timeout"
        self.assertTrue(path.exists())
        self.reset_calls()
        return path

    def test_merge_fast_forwards_main_even_with_untracked_files_present(self):
        origin = self.base / "origin.git"
        self.git("init", "-q", "--bare", str(origin))
        self.git("remote", "add", "origin", str(origin))
        self.git("push", "-q", "origin", "main")
        path = self.claimed()
        (self.repo / "CHANGELOG.md").write_text("squash\n")
        self.git("add", "."); self.git("commit", "-qm", "docs: squash (#7)")
        self.git("push", "-q", "origin", "main")
        self.git("reset", "-q", "--hard", "HEAD~1")  # local main is now behind, as after gh pr merge
        (self.repo / "prompt.md").write_text("private notes\n")  # untracked, must not block the ff
        r = self.run_script(ORCH / "merge.sh", "7", SHIM_PR_FIXTURE=self.pr_fixture())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("could not fast-forward", r.stderr)
        self.assertEqual(self.git("rev-parse", "HEAD"), self.git("rev-parse", "origin/main"))
        self.assertTrue((self.repo / "prompt.md").exists())
        self.assertTrue(path.exists() is False)

    def test_merge_removes_workspace_then_merges_and_deletes_branch(self):
        path = self.claimed()
        r = self.run_script(ORCH / "merge.sh", "7", SHIM_PR_FIXTURE=self.pr_fixture())
        self.assertEqual(r.returncode, 0, r.stderr)
        calls = self.calls()
        remove = next(i for i, c in enumerate(calls) if c.startswith("herdr worktree remove --workspace w9"))
        merge = next(i for i, c in enumerate(calls) if c == "gh pr merge 7 --squash --delete-branch")
        self.assertLess(remove, merge, "worktree must be removed before gh deletes the local branch")
        self.assertFalse(path.exists())
        self.assertNotIn("fix/12", self.git("branch", "--list"))
        self.assertIn("merged: squash into main", r.stdout)

    def test_merge_refuses_failed_pending_unstable_and_unresolved(self):
        self.claimed()
        cases = [
            ({"statusCheckRollup": [{"name": "ci", "status": "COMPLETED", "conclusion": "FAILURE"}]}, "failed checks"),
            ({"statusCheckRollup": [{"name": "ci", "status": "IN_PROGRESS"}]}, "pending checks"),
            ({"mergeStateStatus": "UNSTABLE"}, "UNSTABLE"),
            ({"mergeable": "CONFLICTING"}, "CONFLICTING"),
            ({"mergeable": "UNKNOWN"}, "still computing mergeability"),
            ({"reviewDecision": "CHANGES_REQUESTED"}, "changes requested"),
            ({"isDraft": True}, "draft"),
        ]
        for over, text in cases:
            r = self.run_script(ORCH / "merge.sh", "7", SHIM_PR_FIXTURE=self.pr_fixture(**over), WF_MERGEABLE_WAIT="5")
            self.assertNotEqual(r.returncode, 0, over)
            self.assertIn(text, r.stderr, over)
        r = self.run_script(ORCH / "merge.sh", "7", SHIM_PR_FIXTURE=self.pr_fixture(), SHIM_UNRESOLVED="2")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("unresolved review threads", r.stderr)
        self.assertFalse([c for c in self.calls() if "pr merge" in c])

    def test_allow_unstable_and_ignore_threads_flags(self):
        self.claimed()
        r = self.run_script(ORCH / "merge.sh", "7", "--allow-unstable", "--ignore-threads",
                            SHIM_PR_FIXTURE=self.pr_fixture(mergeStateStatus="UNSTABLE"), SHIM_UNRESOLVED="3")
        self.assertEqual(r.returncode, 0, r.stderr)


class BoardAndAbandonTests(ShimTest):
    def test_board_lists_claimed_worktrees(self):
        r = self.run_script(ORCH / "board.sh")
        self.assertIn("worktrees[0]", r.stdout)
        self.run_script(ORCH / "claim.sh", "12")
        r = self.run_script(ORCH / "board.sh")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("worktrees[1]", r.stdout)
        self.assertIn("12,fix/12-fix-login-timeout,", r.stdout)
        self.run_script(ORCH / "plan.sh", "Offline mode")
        r = self.run_script(ORCH / "board.sh")
        self.assertIn("worktrees[2]", r.stdout)
        self.assertIn("plan,plan/offline-mode,", r.stdout)

    def test_board_shows_the_frontier_of_unblocked_unclaimed_ready_issues(self):
        fixture = self.base / "ready.json"
        fixture.write_text(json.dumps([
            {"number": 40, "title": "Expand schema", "assignees": [], "issue_dependencies_summary": {"blocked_by": 0}},
            {"number": 41, "title": "Migrate callers", "assignees": [], "issue_dependencies_summary": {"blocked_by": 1}},
            {"number": 43, "title": "Taken", "assignees": [{"login": "bob"}], "issue_dependencies_summary": {"blocked_by": 0}},
            {"number": 12, "title": "Fix login timeout", "assignees": [], "issue_dependencies_summary": {"blocked_by": 0}},
            {"number": 50, "title": "A PR", "assignees": [], "pull_request": {"url": "x"}},
        ]))
        self.run_script(ORCH / "claim.sh", "12")
        r = self.run_script(ORCH / "board.sh", SHIM_FRONTIER_FIXTURE=str(fixture))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("frontier[1]{issue,title}:\n  40,Expand schema\n", r.stdout)
        self.assertNotIn("41,", r.stdout); self.assertNotIn("43,", r.stdout); self.assertNotIn("12,Fix", r.stdout)
        self.assertIn("waiting: 3 ready-for-agent issue(s)", r.stdout)

    def test_abandon_refuses_dirty_or_unpushed_without_force(self):
        self.run_script(ORCH / "claim.sh", "12")
        path = self.repo / ".claude/worktrees/fix-12-fix-login-timeout"
        (path / "x.txt").write_text("dirty")
        r = self.run_script(ORCH / "abandon.sh", "12")
        self.assertNotEqual(r.returncode, 0)
        self.assertTrue(path.exists())
        r = self.run_script(ORCH / "abandon.sh", "12", "--force")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertFalse(path.exists())
        self.assertNotIn("fix/12", self.git("branch", "--list"))


if __name__ == "__main__":
    unittest.main()


class GhAxiContextHookTests(ShimTest):
    def hook(self, source="startup", agent_id=None, remote="https://github.com/o/r.git", **extra):
        if remote:
            self.git("remote", "add", "origin", remote)
        payload = {"hook_event_name": "SessionStart", "source": source, "cwd": str(self.repo)}
        if agent_id:
            payload["agent_id"] = agent_id
        r = self.run_script(ORCH / "gh-axi-context.sh", stdin=json.dumps(payload), **extra)
        self.assertEqual(r.returncode, 0, r.stderr)
        return r.stdout

    def test_dashboard_is_printed_on_startup_in_github_repos(self):
        out = self.hook()
        self.assertIn("issues: 2 open", out)
        self.assertIn("gh-axi", out)
        self.assertNotIn("bin:", out, "the local binary path is noise, not context")

    def test_silent_on_resume_subagent_and_non_github_repo(self):
        self.assertEqual(self.hook(remote=None), "", "no remote")
        self.assertEqual(self.hook(remote="https://gitlab.com/o/r.git"), "", "non-GitHub remote")
        self.git("remote", "set-url", "origin", "https://github.com/o/r.git")
        self.assertEqual(self.hook(source="resume", remote=None), "", "resume")
        self.assertEqual(self.hook(agent_id="a1", remote=None), "", "subagent")
        self.assertFalse([c for c in self.calls() if c.startswith("gh-axi")])
