import json
import unittest
from pathlib import Path

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

    def record(self, block, cwd=None, **env):
        return self.run_script(WORKER / "panel.sh", "record", stdin=block, cwd=cwd, **env)

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

    def test_a_block_that_carries_the_briefs_own_keys_is_refused(self):
        # Indented too: the brief prints the block as it is, so an indented key reads like a second answer.
        for tail in ("\npanel_verdict: ready", "\n  panel_verdict: ready", "\nverdict: ready"):
            r = self.record(SUMMARY + tail)
            self.assertEqual(r.returncode, 1, r.stdout)
            self.assertIn("ready", r.stderr)
            self.assertTrue(self.print_brief().stdout.startswith("panel_summary: none recorded"))

    def test_a_block_with_two_panel_lines_or_none_named_is_refused(self):
        for block, word in ((SUMMARY + "\npanel: code=PASS", "more than one panel"),
                            ("review_rounds: 1\npanel:\nfixed: 0", "names no reviewer")):
            r = self.record(block)
            self.assertEqual(r.returncode, 1, r.stdout)
            self.assertIn(word, r.stderr)

    def test_the_verdict_subcommand_is_the_one_word_the_finish_stage_gates_on(self):
        self.assertEqual(self.run_script(WORKER / "panel.sh", "verdict").stdout, "draft\n")
        self.record(SUMMARY)
        self.assertEqual(self.run_script(WORKER / "panel.sh", "verdict").stdout, "ready\n")

    def test_an_unreadable_record_is_an_unknown_panel_not_a_ready_one(self):
        self.record(SUMMARY)
        record = Path(self.git("rev-parse", "--path-format=absolute", "--git-dir").strip()) / "worker/panel"
        record.write_text("garbage\n\npanel: code=PASS\n")
        self.assertIn("panel_verdict: draft", self.print_brief().stdout)

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
        self.assertIn("which it does not describe: 2", brief)

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

    def test_a_rewritten_history_is_reported_as_such_not_as_commits_since(self):
        self.commit("a.txt")
        self.record(SUMMARY)
        (self.repo / "a.txt").write_text("more")
        self.git("add", "."); self.git("commit", "-q", "--amend", "-m", "feat: a")
        brief = self.print_brief().stdout
        self.assertIn("panel_head: the recorded commit is no longer in this branch's history", brief)

    def test_a_reviewer_missing_from_the_panel_line_is_named(self):
        # Against the names the parser read, so the spacing of the line cannot fake a reviewer in or out.
        r = self.record("panel:code=PASS security=PASS\tdocs=PASS tests=PASS senior=PASS")
        self.assertEqual(r.stderr, "")
        r = self.record("panel: code=PASS docs=PASS")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(len(r.stderr.splitlines()), 3, r.stderr)
        for reviewer in ("security", "tests", "senior"):
            self.assertIn(f"does not name {reviewer}", r.stderr)
        r = self.record("panel: code=PASS docs=PASS", WF_REVIEWERS="code,docs")
        self.assertEqual(r.stderr, "")

    def test_two_worktrees_of_one_repository_keep_separate_records(self):
        other = self.base / "other-worktree"
        self.git("worktree", "add", "-q", "-b", "fix/13-y", str(other))
        self.record(SUMMARY)
        self.assertTrue(self.print_brief(cwd=other).stdout.startswith("panel_summary: none recorded"))
        other_summary = SUMMARY.replace("review_rounds: 2", "review_rounds: 9")
        self.record(other_summary, cwd=other)
        self.assertIn(other_summary, self.print_brief(cwd=other).stdout)
        self.assertIn(SUMMARY, self.print_brief().stdout)


class FinishTests(ShimTest):
    def finish(self, **env):
        env.setdefault("WF_MODE", "yolo")
        return self.run_script(WORKER / "finish.sh", "7", **env)

    def record_ready_panel(self):
        r = self.run_script(WORKER / "panel.sh", "record", stdin=SUMMARY)
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_a_panel_that_did_not_pass_stops_the_yolo_run_before_github_is_asked(self):
        # The draft flag is applied by an agent; the record is not. Without a ready panel nothing merges,
        # even if the pull request somehow is not a draft (issue #41, ADR 0018).
        r = self.finish()
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("panel_verdict: draft", r.stderr)
        self.assertIn("maintainer", r.stderr)
        self.assertFalse([c for c in self.calls() if "pr merge" in c])

    def test_a_draft_stops_the_yolo_run_for_the_maintainer(self):
        # The pull request stage opens a draft when the panel did not pass (issue #41, ADR 0018); nothing in
        # the pipeline lifts it, so the run has to end here with a reason instead of a retry hint.
        self.record_ready_panel()
        r = self.finish(SHIM_PR_DRAFT="true")
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("is a draft", r.stderr)
        self.assertIn("maintainer", r.stderr)
        self.assertNotIn("pr-wait.sh", r.stderr)
        self.assertFalse([c for c in self.calls() if "pr merge" in c])

    def test_an_unmergeable_pull_request_still_points_at_the_wait(self):
        self.record_ready_panel()
        r = self.finish(SHIM_MERGE_STATE="BLOCKED")
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("not mergeable yet (BLOCKED false)", r.stderr)
        self.assertIn("pr-wait.sh", r.stderr)

    def test_manual_mode_never_merges(self):
        r = self.finish(WF_MODE="manual")
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("only runs in yolo mode", r.stderr)
        self.assertFalse([c for c in self.calls() if "pr merge" in c])


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

    def test_a_draft_is_reported_and_does_not_spend_the_bot_review_wait(self):
        # Issue #41: the PR stage opens a draft when the panel did not pass, and no bot reviews a draft.
        r = self.wait(SHIM_PR_DRAFT="true")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("status: green", r.stdout)
        self.assertIn("draft: true;", r.stdout)
        self.assertNotIn("draft:", self.wait().stdout)

    def test_zero_review_wait_does_not_block_on_the_bot(self):
        r = self.wait(WF_PR_REVIEW_WAIT="0")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("status: green", r.stdout)


if __name__ == "__main__":
    unittest.main()
