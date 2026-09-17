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
        r = self.run_script(ORCH / "claim.sh", "12", WF_CLAUDE_ARGS="--model sonnet --plugin-dir /x")
        self.assertEqual(r.returncode, 0, r.stderr)
        argv = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        # Separate entries, not one "--model sonnet --plugin-dir /x" blob: claim.sh must leave $extra unquoted.
        self.assertEqual(argv[argv.index("--model") + 1], "sonnet")
        self.assertEqual(argv[argv.index("--plugin-dir") + 1], "/x")
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
            ({"reviewDecision": "CHANGES_REQUESTED"}, "changes requested"),
            ({"isDraft": True}, "draft"),
        ]
        for over, text in cases:
            r = self.run_script(ORCH / "merge.sh", "7", SHIM_PR_FIXTURE=self.pr_fixture(**over))
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
