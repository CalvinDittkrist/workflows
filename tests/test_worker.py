import json
import unittest

from helpers import ROOT, WORKER, ShimTest


class SessionStartHookTests(ShimTest):
    def hook(self, branch, source="startup", **extra):
        if branch != "main":
            self.git("checkout", "-qb", branch)
        payload = json.dumps({"hook_event_name": "SessionStart", "source": source, "cwd": str(self.repo), "session_id": "s1"})
        r = self.run_script(WORKER / "session-start.sh", stdin=payload, **extra)
        self.assertEqual(r.returncode, 0, r.stderr)
        return r

    def test_issue_branch_loads_context_and_assigns(self):
        r = self.hook("fix/12-login", WF_MODE="yolo")
        out = json.loads(r.stdout)["hookSpecificOutput"]
        self.assertEqual(out["hookEventName"], "SessionStart")
        ctx = out["additionalContext"]
        self.assertIn("issue #12", ctx)
        self.assertIn("Fix login timeout", ctx)
        self.assertIn("Mode: yolo", ctx)
        self.assertIn("task data", ctx)
        self.assertIn("@alice", ctx)
        self.assertIn("gh issue edit 12 --add-assignee @me", self.calls())

    def test_non_issue_branch_and_subagent_are_silent(self):
        r = self.hook("main")
        self.assertEqual(r.stdout, "")
        payload = json.dumps({"hook_event_name": "SessionStart", "source": "startup", "cwd": str(self.repo), "agent_id": "a1"})
        self.git("checkout", "-qb", "feat/12-x")
        r = self.run_script(WORKER / "session-start.sh", stdin=payload)
        self.assertEqual(r.stdout, "")
        # A plan branch carries a topic, so plan/12-factor-app is not issue #12's worktree.
        self.assertEqual(self.hook("plan/12-factor-app").stdout, "")
        self.assertFalse(self.calls())

    def test_resume_injects_only_a_short_reminder(self):
        r = self.hook("feat/12-x", source="resume")
        ctx = json.loads(r.stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn("#12", ctx)
        self.assertNotIn("Fix login timeout", ctx)
        self.assertFalse([c for c in self.calls() if "issue edit" in c])

    def test_github_unavailable_degrades_gracefully(self):
        r = self.hook("feat/12-x", PATH="/usr/bin:/bin")
        ctx = json.loads(r.stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn("could not be loaded", ctx)


class DiffContextTests(ShimTest):
    def test_reports_range_and_files(self):
        self.git("checkout", "-qb", "feat/12-x")
        (self.repo / "a.txt").write_text("a\n")
        self.git("add", "."); self.git("commit", "-qm", "feat: a")
        r = self.run_script(WORKER / "diff-context.sh", WF_BASE_BRANCH="main")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("issue: #12", r.stdout)
        self.assertIn("commits: 1", r.stdout)
        self.assertIn("a.txt", r.stdout)


class FactsTests(ShimTest):
    def test_reports_mode_issue_base_and_panel_from_env_or_branch(self):
        self.git("checkout", "-qb", "fix/7-y")
        r = self.run_script(WORKER / "facts.sh", WF_BASE_BRANCH="main")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout.splitlines(), [
            "mode: manual", "issue: #7", "base: main",
            "reviewers: code,security,docs,tests,senior", "max_rounds: 3", "subagents: background",
        ])
        r = self.run_script(WORKER / "facts.sh", WF_BASE_BRANCH="main", WF_MODE="yolo", WF_ISSUE="12",
                            WF_REVIEWERS="code,senior", WF_REVIEW_ROUNDS="1")
        self.assertIn("mode: yolo\nissue: #12\n", r.stdout)
        self.assertIn("reviewers: code,senior\nmax_rounds: 1", r.stdout)

    def test_the_waiting_shape_of_the_session_is_a_fact_the_review_stage_can_read(self):
        """A claim disables background tasks, a hand-started session does not; the review stage waits by
        collecting the tool results in the first case and by ending the turn in the second (issue #34)."""
        self.git("checkout", "-qb", "fix/7-y")
        for value, shape in (("1", "foreground"), ("  True ", "foreground"), ("on", "foreground"),
                             ("0", "background"), ("", "background")):
            with self.subTest(value=value):
                r = self.run_script(WORKER / "facts.sh", WF_BASE_BRANCH="main",
                                    CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=value)
                self.assertEqual(r.returncode, 0, r.stderr)
                self.assertIn(f"subagents: {shape}", r.stdout)


SUMMARY = """review_rounds: 2
panel: code=FIX→PASS security=PASS docs=PASS tests=PASS senior=PASS
fixed: 3 (S1 1, S2 1, S3 1)
disputed: none"""


class PanelSummaryTests(ShimTest):
    """The hand-over from the review stage to the pull request stage (issue #41, ADR 0018)."""

    def setUp(self):
        super().setUp()
        self.git("checkout", "-qb", "fix/12-x")

    def record(self, block, cwd=None):
        return self.run_script(WORKER / "panel.sh", "record", stdin=block, cwd=cwd)

    def print_brief(self, cwd=None):
        return self.run_script(WORKER / "panel.sh", "print", cwd=cwd)

    def commit(self, name):
        (self.repo / name).write_text(name)
        self.git("add", "."); self.git("commit", "-qm", f"feat: {name}")

    def verdict(self, panel_line):
        r = self.record(f"review_rounds: 1\n{panel_line}\nfixed: 0\ndisputed: none")
        self.assertEqual(r.returncode, 0, r.stderr)
        return [l for l in self.print_brief().stdout.splitlines() if l.startswith("panel_verdict:")][0]

    def test_a_recorded_summary_reaches_the_pull_request_brief_unchanged(self):
        self.commit("a.txt")
        r = self.record(SUMMARY)
        self.assertEqual(r.returncode, 0, r.stderr)
        brief = self.print_brief().stdout
        self.assertIn("panel_summary_block:\n" + SUMMARY + "\n", brief)
        self.assertIn("panel_verdict: ready", brief)

    def test_without_a_record_the_brief_says_so_in_one_line_and_the_verdict_is_draft(self):
        brief = self.print_brief().stdout.splitlines()
        self.assertEqual(len(brief), 2, brief)
        self.assertTrue(brief[0].startswith("panel_summary: none recorded"), brief)
        self.assertEqual(brief[1], "panel_verdict: draft")

    def test_the_verdict_is_draft_unless_every_reviewer_ends_on_pass(self):
        self.assertEqual(self.verdict("panel: code=PASS security=PASS docs=PASS tests=PASS senior=PASS"),
                         "panel_verdict: ready")
        # The last verdict of each reviewer counts, and a parenthesised suffix is accepted and ignored.
        self.assertEqual(self.verdict("panel: code=FIX→FIX→PASS security=PASS(S3 only) docs=PASS"),
                         "panel_verdict: ready")
        self.assertEqual(self.verdict("panel: code=FIX→FIX→FIX security=PASS docs=PASS"),
                         "panel_verdict: draft")
        self.assertEqual(self.verdict("panel: code=PASS tests=PASS→FIX(S2 open)"), "panel_verdict: draft")

    def test_a_block_without_a_parseable_panel_line_records_nothing(self):
        self.record(SUMMARY)
        for block in ("review_rounds: 1\nfixed: 0", "panel: code=MAYBE", "panel:"):
            r = self.record(block)
            self.assertEqual(r.returncode, 1, block)
            self.assertTrue(r.stderr.startswith("error: "), r.stderr)
            self.assertIn("panel: code=PASS", r.stderr)  # names the expected form
            self.assertIn(SUMMARY, self.print_brief().stdout)  # the earlier record survives

    def test_the_brief_names_the_commit_and_reports_a_moved_head(self):
        self.commit("a.txt")
        recorded = self.git("rev-parse", "--short", "HEAD").strip()
        self.record(SUMMARY)
        self.assertIn(f"panel_summary: recorded at {recorded}", self.print_brief().stdout)
        self.assertIn("panel_head: unchanged", self.print_brief().stdout)
        self.commit("b.txt")
        self.commit("c.txt")
        brief = self.print_brief().stdout
        self.assertIn(f"panel_summary: recorded at {recorded}", brief)
        self.assertIn("panel_head: 2 commits since", brief)

    def test_the_pull_request_stages_brief_carries_the_summary_without_a_skill_argument(self):
        """End to end over the wiring: whatever the pr skill injects has to print the recorded summary,
        because the pipeline driver invokes that skill with no argument."""
        import re
        self.record(SUMMARY)
        body = (ROOT / "plugins/worker/skills/pr/SKILL.md").read_text().split("---")[2]
        briefs = [self.run_script(*cmd.replace("${CLAUDE_PLUGIN_ROOT}/scripts/", str(WORKER) + "/").split())
                  for cmd in re.findall(r"!`([^`]+)`", body)]
        for r in briefs:
            self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(SUMMARY, "".join(r.stdout for r in briefs))

    def test_two_worktrees_of_one_repository_keep_separate_records(self):
        other = self.base / "other-worktree"
        self.git("worktree", "add", "-q", "-b", "fix/13-y", str(other))
        self.record(SUMMARY)
        self.assertTrue(self.print_brief(cwd=other).stdout.startswith("panel_summary: none recorded"))
        self.record(SUMMARY.replace("2", "9"), cwd=other)
        self.assertIn("review_rounds: 9", self.print_brief(cwd=other).stdout)
        self.assertIn("review_rounds: 2", self.print_brief().stdout)


class PrWaitTests(ShimTest):
    def wait(self, **env):
        env.setdefault("WF_POLL_SECONDS", "1")
        return self.run_script(WORKER / "pr-wait.sh", "7", "--max-seconds", "2", **env)

    def test_waits_for_the_bot_review_after_checks_pass(self):
        r = self.wait()
        self.assertEqual(r.returncode, 3, r.stdout + r.stderr)
        self.assertIn("status: waiting", r.stdout)
        self.assertIn("expected from: chatgpt-codex-connector", r.stdout)

    def test_empty_bot_list_means_green_as_soon_as_checks_pass(self):
        r = self.wait(WF_PR_BOT_REVIEWERS="")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("status: green", r.stdout)
        self.assertIn("checks: total=1 pass=1 fail=0 pending=0", r.stdout)

    def test_review_wait_counts_from_when_checks_finished_not_from_each_call(self):
        # Regression: each call restarted the 600 s window, so a worker looping on exit 3 never got green.
        r = self.wait(SHIM_CHECK_DONE="2026-09-17T10:00:00Z")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("status: green", r.stdout)

    def test_empty_check_rollup_right_after_a_push_is_pending_when_ci_is_configured(self):
        # Regression: GitHub reports no checks for a moment after a push; that was reported as green.
        (self.repo / ".github/workflows").mkdir(parents=True)
        (self.repo / ".github/workflows/ci.yml").write_text("on: pull_request\n")
        import datetime
        now = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        r = self.wait(SHIM_CHECKS_EMPTY="1", SHIM_HEAD_AT=now, WF_PR_BOT_REVIEWERS="")
        self.assertEqual(r.returncode, 3, r.stdout + r.stderr)
        self.assertIn("status: waiting", r.stdout)
        # An old push with still no checks means the workflow does not run for this PR: green.
        r = self.wait(SHIM_CHECKS_EMPTY="1", WF_PR_BOT_REVIEWERS="")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("status: green", r.stdout)

    def test_zero_review_wait_does_not_block_on_the_bot(self):
        r = self.wait(WF_PR_REVIEW_WAIT="0")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("status: green", r.stdout)


if __name__ == "__main__":
    unittest.main()
