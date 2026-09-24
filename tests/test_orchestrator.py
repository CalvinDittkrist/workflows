import json
import shlex
import shutil
import subprocess
import unittest
from datetime import date
from pathlib import Path

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
        # The background switch keeps the session's subagents in the foreground, so the worker never waits in a
        # sleep loop for its reviewer panel (issue #34: 328 sleep turns and 120k -> 412k tokens in one session).
        # The session compacts at a percentage of the window, and the claim pins both numbers rather than
        # leaving the percentage to an undocumented default that a release could move (issue #73).
        self.assertEqual(settings["env"], {"WF_MODE": "manual", "WF_ISSUE": "12",
                                           "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS": "1",
                                           "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE": "80"})
        self.assertEqual(settings["autoCompactWindow"], 312500)
        # The pane shows the worker's context size and writes it into the worktree for the checkpoint.
        # claude runs the command through a shell, so it is read back the way that shell reads it: the script,
        # then the compact trigger, which is the window above times the percentage beside it.
        self.assertEqual(shlex.split(settings["statusLine"]["command"]), [str(ORCH / "statusline.sh"), "250000"])
        self.assertEqual(settings["statusLine"]["type"], "command")
        # A long tool call changes no message; without the interval the value would go stale under it.
        self.assertEqual(settings["statusLine"]["refreshInterval"], 60)
        self.assertTrue((self.repo / ".claude/worktrees/fix-12-fix-login-timeout/README.md").exists())
        self.assertIn(".claude/worktrees/", (self.repo / ".git/info/exclude").read_text())
        self.assertEqual(self.git("status", "--porcelain"), "", "worktree dir must not show up as untracked")
        self.assertIn("herdr workspace focus wR", self.calls())
        self.assertIn("agent_status: working", r.stdout)
        self.assertIn("next: board.sh shows progress; merge.sh <pr> when the PR is ready", r.stdout)

    def test_the_sandboxed_worker_session_gets_the_same_session_settings(self):
        r = self.run_script(ORCH / "claim.sh", "12", "--sandbox")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("sandbox: docker", r.stdout)
        run = [c for c in self.argv_calls() if c[1:3] == ["pane", "run"]][0]
        # This start passes the settings inside the pane's command line, so read that line the way the shell in
        # the pane would: a quoting regression in it would hand claude a broken settings object.
        words = shlex.split(run[-1])
        self.assertIn("sbx-worker.sh", words[0])
        settings = json.loads(words[words.index("--settings") + 1])
        self.assertEqual(settings["env"], {"WF_MODE": "manual", "WF_ISSUE": "12",
                                           "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS": "1",
                                           "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE": "80"})
        self.assertEqual(shlex.split(settings["statusLine"]["command"]), [str(ORCH / "statusline.sh"), "250000"])
        self.assertEqual(settings["autoCompactWindow"], 312500)
        self.assertEqual(words[-1], "/worker:work")
        # The pane the command runs in has its own working directory, so the script is named by its full path.
        self.assertEqual(words[0], str(ORCH / "sbx-worker.sh"))

    def test_the_status_line_command_survives_a_plugin_path_with_a_space(self):
        # claude runs the command through a shell. Unquoted, a checkout under "/Users/John Smith" splits into
        # words, nothing renders, and the worker reads the missing value as a handoff for the rest of the run.
        spaced = self.base / "my plugins" / "orchestrator"
        spaced.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(ORCH, spaced / "scripts")
        r = self.run_script(spaced / "scripts" / "claim.sh", "12")
        self.assertEqual(r.returncode, 0, r.stderr)
        start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        command = json.loads(start[start.index("--settings") + 1])["statusLine"]["command"]
        self.assertEqual(shlex.split(command), [str(spaced / "scripts/statusline.sh"), "250000"])
        # And the shell really runs it: the line it prints is what the pane would show. The window is a
        # million tokens, so the 250k in the line is the trigger the command carries and not the model's window.
        payload = json.dumps({"cwd": str(self.repo), "context_window": {"total_input_tokens": 78231, "context_window_size": 1000000}})
        rendered = subprocess.run(["sh", "-c", command], input=payload, text=True, capture_output=True,
                                  env=self.env(WF_ISSUE="12", WF_MODE="manual"))
        self.assertEqual(rendered.returncode, 0, rendered.stderr)
        self.assertEqual(rendered.stdout.strip(), "#12 · manual · 78k/250k (31%)")

    def test_the_sandboxed_start_survives_a_plugin_path_with_a_space(self):
        # The sandbox start hands the whole settings object to the shell of a pane as one word. That object
        # carries quotes of its own since the status line moved into it, so unquoted it loses the session's
        # environment, its plugin isolation and its compact trigger without a word of complaint.
        spaced = self.base / "my plugins" / "orchestrator"
        spaced.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(ORCH, spaced / "scripts")
        r = self.run_script(spaced / "scripts" / "claim.sh", "12", "--sandbox")
        self.assertEqual(r.returncode, 0, r.stderr)
        run = [c for c in self.argv_calls() if c[1:3] == ["pane", "run"]][0]
        words = shlex.split(run[-1])
        self.assertEqual(words[0], str(spaced / "scripts/sbx-worker.sh"))
        settings = json.loads(words[words.index("--settings") + 1])
        self.assertEqual(settings["env"]["WF_ISSUE"], "12")
        self.assertEqual(settings["env"]["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"], "80")
        self.assertEqual(settings["autoCompactWindow"], 312500)
        self.assertEqual(shlex.split(settings["statusLine"]["command"]), [str(spaced / "scripts/statusline.sh"), "250000"])
        self.assertEqual(words[words.index("--name") + 1], "#12")

    def test_yolo_flag_is_passed_to_the_worker_session(self):
        r = self.run_script(ORCH / "claim.sh", "12", "--yolo")
        self.assertEqual(r.returncode, 0, r.stderr)
        start = [c for c in self.calls() if c.startswith("herdr agent start")][0]
        self.assertIn('"WF_MODE":"yolo"', start)
        self.assertIn("this worker merges its own PR", r.stdout)

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

    def test_claim_refuses_an_issue_that_is_not_ready_for_an_agent(self):
        # 18 carries one label whose name contains a comma: a joined label list would match it as a substring.
        for issue, labels in (("14", "needs-triage,enhancement"), ("17", "none"), ("18", "needs,ready-for-agent")):
            r = self.run_script(ORCH / "claim.sh", issue)
            self.assertNotEqual(r.returncode, 0, r.stdout)
            self.assertIn(f"(labels: {labels})", r.stderr)
            self.assertIn(f"/orchestrator:plan #{issue}", r.stderr)
            self.assertIn("--force", r.stderr)
            # Nothing was created: no worktree, no branch, no Herdr call, and no write to GitHub.
            self.assertEqual(self.git("worktree", "list").count("\n"), 1)
            self.assertEqual(self.git("branch", "--list", f"*/{issue}-*"), "")
            for call in self.calls():
                self.assertRegex(call, r"^gh (repo|issue) view ", "the refusal must only read, never create or assign")
            self.reset_calls()

    def test_claim_of_a_spec_points_at_its_tickets(self):
        r = self.run_script(ORCH / "claim.sh", "15")
        self.assertNotEqual(r.returncode, 0, r.stdout)
        self.assertIn("(labels: spec)", r.stderr)
        self.assertIn("Claim its tickets instead", r.stderr)
        self.assertIn("/orchestrator:plan #15", r.stderr)
        self.assertEqual(self.git("worktree", "list").count("\n"), 1)

    def test_a_spec_labelled_ready_for_agent_is_claimed(self):
        r = self.run_script(ORCH / "claim.sh", "16")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("branch: feat/16-small-spec", r.stdout)
        self.assertEqual(len([c for c in self.calls() if c.startswith("herdr agent start")]), 1)

    def test_force_claims_an_issue_that_is_not_ready_and_says_so(self):
        r = self.run_script(ORCH / "claim.sh", "14", "--force", "--yolo")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("--force", r.stderr)
        self.assertIn("(labels: needs-triage,enhancement)", r.stderr)
        self.assertIn("branch: feat/14-dark-mode", r.stdout)
        start = [c for c in self.calls() if c.startswith("herdr agent start")][0]
        self.assertIn('"WF_MODE":"yolo"', start)

    def test_a_claimed_issue_stays_already_claimed_after_its_labels_changed(self):
        self.run_script(ORCH / "claim.sh", "14", "--force")
        r = self.run_script(ORCH / "claim.sh", "14")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("status: already-claimed", r.stdout)
        # Relabelling the issue would derive feat/12-… today, but the worktree on fix/12-… is still its own.
        self.run_script(ORCH / "claim.sh", "12")
        r = self.run_script(ORCH / "claim.sh", "12", SHIM_ISSUE_12_LABELS="enhancement")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("status: already-claimed", r.stdout)
        self.assertIn("branch: fix/12-fix-login-timeout", r.stdout)

    def origin(self):
        """A bare repository as origin, with main on it."""
        remote = self.base / "remote.git"
        self.git("init", "-q", "--bare", str(remote), cwd=self.base)
        self.git("remote", "add", "origin", str(remote))
        self.git("push", "-q", "origin", "main")

    def remote_claim(self, branch, *, also=()):
        """An origin whose branch `branch` carries work this repository does not know: what the factory leaves
        behind when it claims an issue and pushes. The remote-tracking ref the push created is deleted again,
        so the branch is as unknown here as one another machine pushed. Returns the commit. `also` names
        further branches to put on origin at main, for the near misses a lookup has to ignore."""
        self.origin()
        self.git("checkout", "-q", "-b", "wip")
        (self.repo / "factory-work.md").write_text("work the factory pushed\n")
        self.git("add", ".")
        self.git("commit", "-qm", "wip")
        sha = self.git("rev-parse", "HEAD").strip()
        self.git("push", "-q", "origin", f"wip:refs/heads/{branch}")
        for other in also:
            self.git("push", "-q", "origin", f"main:refs/heads/{other}")
        self.git("checkout", "-q", "main")
        self.git("branch", "-qD", "wip")
        self.git("update-ref", "-d", f"refs/remotes/origin/{branch}")
        return sha

    def test_claim_refuses_an_issue_routed_to_the_factory(self):
        r = self.run_script(ORCH / "claim.sh", "19")
        self.assertNotEqual(r.returncode, 0, r.stdout)
        self.assertIn("(labels: ready-for-agent,factory)", r.stderr)
        self.assertIn("Remove the label factory", r.stderr)
        self.assertIn("--force", r.stderr)
        self.assertEqual(self.git("worktree", "list").count("\n"), 1)
        self.assertEqual(self.git("branch", "--list", "*/19-*"), "")
        for call in self.calls():
            self.assertRegex(call, r"^gh (repo|issue) view ", "the refusal must only read, never create or assign")

    def test_force_claims_a_routed_issue_and_warns(self):
        r = self.run_script(ORCH / "claim.sh", "19", "--force")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("routed to the factory", r.stderr)
        self.assertIn("--force", r.stderr)
        self.assertIn("branch: feat/19-routed-to-the-factory", r.stdout)
        self.assertEqual(len([c for c in self.calls() if c.startswith("herdr agent start")]), 1)

    def test_force_claims_a_routed_issue_whose_branch_the_factory_already_pushed(self):
        # The whole factory case in one: routed and claimed on the remote. Both refusals warn, and the worktree
        # continues the factory's branch under its name, not the one this machine's labels derive.
        sha = self.remote_claim("fix/19-an-earlier-slug")
        r = self.run_script(ORCH / "claim.sh", "19", "--force")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("routed to the factory", r.stderr)
        self.assertIn("adopts that branch", r.stderr)
        self.assertIn("branch: fix/19-an-earlier-slug", r.stdout)
        wt = self.repo / ".claude/worktrees/fix-19-an-earlier-slug"
        self.assertEqual(self.git("rev-parse", "HEAD", cwd=wt).strip(), sha)
        self.assertTrue((wt / "factory-work.md").exists())

    def test_claim_refuses_an_issue_already_claimed_on_the_remote(self):
        # The remote branch is a feat/ one while the labels of #12 derive fix/: the claim on the remote is
        # found by issue number, not by the branch type of the moment. The other two branches are near
        # misses of the contract shape that belong to no issue or to another one.
        self.remote_claim("feat/12-fix-login-timeout", also=("feat/120-another-issue", "plan/12-a-topic"))
        r = self.run_script(ORCH / "claim.sh", "12")
        self.assertNotEqual(r.returncode, 0, r.stdout)
        self.assertIn("feat/12-fix-login-timeout", r.stderr)
        self.assertIn("--force", r.stderr)
        self.assertEqual(self.git("worktree", "list").count("\n"), 1)
        self.assertEqual(self.git("branch", "--list", "*/12-*"), "")
        self.assertFalse([c for c in self.calls() if "worktree create" in c or "agent start" in c])

    def test_force_adopts_the_branch_the_remote_claim_left(self):
        sha = self.remote_claim("feat/12-fix-login-timeout")
        r = self.run_script(ORCH / "claim.sh", "12", "--force")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("adopts that branch", r.stderr)
        self.assertIn("branch: feat/12-fix-login-timeout", r.stdout)
        # The worktree continues the remote branch, so the work pushed there is in it and main is not its tip.
        wt = self.repo / ".claude/worktrees/feat-12-fix-login-timeout"
        self.assertTrue((wt / "factory-work.md").exists())
        self.assertEqual(self.git("rev-parse", "HEAD", cwd=wt).strip(), sha)
        self.assertEqual(self.git("branch", "--list", "fix/12-fix-login-timeout"), "")

    def test_an_issue_claimed_locally_stays_already_claimed_when_the_factory_takes_it(self):
        self.run_script(ORCH / "claim.sh", "12")
        self.remote_claim("feat/12-fix-login-timeout")
        self.reset_calls()
        r = self.run_script(ORCH / "claim.sh", "12", SHIM_ISSUE_12_LABELS="bug,ready-for-agent,factory")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("status: already-claimed", r.stdout)
        self.assertIn("branch: fix/12-fix-login-timeout", r.stdout)
        self.assertFalse([c for c in self.calls() if "worktree create" in c or "agent start" in c])

    def test_a_claim_of_an_abandoned_issue_names_the_abandoned_claim_and_force_continues_it(self):
        # The single-machine loop: claim, push, abandon (which keeps the remote branch by design), claim again.
        self.origin()
        self.run_script(ORCH / "claim.sh", "12")
        wt = self.repo / ".claude/worktrees/fix-12-fix-login-timeout"
        (wt / "local-work.md").write_text("work this machine pushed\n")
        self.git("add", ".", cwd=wt)
        self.git("commit", "-qm", "wip", cwd=wt)
        self.git("push", "-q", "origin", "HEAD:refs/heads/fix/12-fix-login-timeout", cwd=wt)
        sha = self.git("rev-parse", "HEAD", cwd=wt).strip()
        r = self.run_script(ORCH / "abandon.sh", "12")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("a claim of your own you abandoned", self.run_script(ORCH / "claim.sh", "12").stderr)

        r = self.run_script(ORCH / "claim.sh", "12", "--force")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.git("rev-parse", "HEAD", cwd=wt).strip(), sha, "the abandoned work is continued")

    def test_a_stale_local_branch_stops_the_adoption_instead_of_starting_from_it(self):
        # A local branch that outlived its worktree would be checked out at its own tip, and the worktree would
        # not carry the work the claim says it continues.
        self.remote_claim("feat/12-fix-login-timeout")
        self.git("branch", "feat/12-fix-login-timeout", "main")
        r = self.run_script(ORCH / "claim.sh", "12", "--force")
        self.assertNotEqual(r.returncode, 0, r.stdout)
        self.assertIn("the local branch feat/12-fix-login-timeout exists at", r.stderr)
        self.assertIn("git branch -D feat/12-fix-login-timeout", r.stderr)
        self.assertEqual(self.git("worktree", "list").count("\n"), 1)
        self.assertFalse([c for c in self.calls() if "worktree create" in c or "agent start" in c])

    def test_an_unreadable_origin_warns_and_claims(self):
        # Offline, or a remote that is gone: the check is a courtesy and must not be able to stop a claim.
        self.git("remote", "add", "origin", str(self.base / "nowhere.git"))
        r = self.run_script(ORCH / "claim.sh", "12")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("could not read the branches of origin", r.stderr)
        self.assertIn("branch: fix/12-fix-login-timeout", r.stdout)

    def test_claim_refuses_outside_herdr(self):
        r = self.run_script(ORCH / "claim.sh", "12", HERDR_ENV="")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("HERDR_ENV", r.stderr)

    def test_base_without_a_branch_name_says_so(self):
        # The trailing shift of the argument loop fails on an empty list, which used to end the claim with
        # exit 1 and nothing said at all; a flag that takes a value asks for it by name instead.
        r = self.run_script(ORCH / "claim.sh", "12", "--base")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: --base needs a branch name", r.stderr)


class ClaimEnvTests(ShimTest):
    """`--env NAME=VALUE` sets a worker knob for the one session a claim starts. It rides in the env block of
    the --settings object the claim builds, so the session keeps the status line, the compact trigger and the
    plugin isolation every claim gives it, and the knob reaches no other session."""

    # What the env block of a claim without --env holds; a knob is expected next to these, never instead of one.
    SESSION_ENV = {"WF_MODE": "manual", "WF_ISSUE": "12", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS": "1",
                   "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE": "80"}

    def claim(self, *args, sandbox=False):
        """Claim issue #12 and return its output with the settings the worker session was really started with.
        The sandboxed start passes them inside a command line for the shell of a pane, so that line is run by
        a real shell here: what that shell makes of it is what the sandboxed session is really started with."""
        r = self.run_script(ORCH / "claim.sh", "12", *args)
        self.assertEqual(r.returncode, 0, r.stderr)
        if sandbox:
            words = self.shell_words([c for c in self.argv_calls() if c[1:3] == ["pane", "run"]][0][-1])
        else:
            words = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        return r, json.loads(words[words.index("--settings") + 1])

    def assert_session_shape(self, settings):
        """What every claim gives a worker session, knob or no knob: the status line the checkpoint reads the
        context from, the compact trigger under it, and the plugin isolation that leaves only worker skills."""
        self.assertEqual(settings["autoCompactWindow"], 312500)
        self.assertEqual(shlex.split(settings["statusLine"]["command"]), [str(ORCH / "statusline.sh"), "250000"])
        self.assertEqual(settings["enabledPlugins"],
                         {"planner@workflows": False, "orchestrator@workflows": False})

    def test_a_knob_given_on_the_claim_reaches_that_session_and_changes_nothing_else(self):
        r, settings = self.claim("--env", "WF_HANDOFF_TOKENS=5000")
        self.assertEqual(settings["env"], {**self.SESSION_ENV, "WF_HANDOFF_TOKENS": "5000"})
        self.assert_session_shape(settings)
        # The transcript of the orchestrator shows the effect beside the command that asked for it.
        self.assertIn("env: WF_HANDOFF_TOKENS", r.stdout)

    def test_a_sandboxed_claim_carries_the_knob_the_same_way(self):
        r, settings = self.claim("--env", "WF_HANDOFF_TOKENS=5000", "--sandbox", sandbox=True)
        self.assertEqual(settings["env"], {**self.SESSION_ENV, "WF_HANDOFF_TOKENS": "5000"})
        self.assert_session_shape(settings)
        self.assertIn("env: WF_HANDOFF_TOKENS", r.stdout)

    def test_a_yolo_claim_carries_the_knob_beside_its_mode(self):
        r, settings = self.claim("--env", "WF_HANDOFF_TOKENS=5000", "--yolo")
        self.assertEqual(settings["env"], {**self.SESSION_ENV, "WF_MODE": "yolo", "WF_HANDOFF_TOKENS": "5000"})
        self.assert_session_shape(settings)
        self.assertIn("env: WF_HANDOFF_TOKENS", r.stdout)

    # A value no shell would leave alone: spaces, both quotes, and a $ that a shell would expand away. It
    # travels through jq into JSON, and in the sandboxed start through one more shell on the way to the pane.
    HOSTILE = """a b 'c' "d" $HOME"""

    def test_two_knobs_both_arrive_and_no_shell_reads_their_values(self):
        r, settings = self.claim("--env", "WF_REVIEWERS=code,tests", "--env", f"WF_PR_BOT_REVIEWERS={self.HOSTILE}")
        self.assertEqual(settings["env"], {**self.SESSION_ENV, "WF_REVIEWERS": "code,tests",
                                           "WF_PR_BOT_REVIEWERS": self.HOSTILE})
        self.assertIn("env: WF_REVIEWERS WF_PR_BOT_REVIEWERS", r.stdout)

    def test_the_sandboxed_start_passes_the_same_values_byte_for_byte(self):
        _, settings = self.claim("--env", "WF_REVIEWERS=code,tests", "--env", f"WF_PR_BOT_REVIEWERS={self.HOSTILE}",
                                 "--sandbox", sandbox=True)
        self.assertEqual(settings["env"], {**self.SESSION_ENV, "WF_REVIEWERS": "code,tests",
                                           "WF_PR_BOT_REVIEWERS": self.HOSTILE})

    def test_an_empty_value_is_a_setting_of_its_own(self):
        # WF_PR_BOT_REVIEWERS="" is how a repository without a bot reviewer is configured, so it is not malformed.
        _, settings = self.claim("--env", "WF_PR_BOT_REVIEWERS=")
        self.assertEqual(settings["env"]["WF_PR_BOT_REVIEWERS"], "")

    def assert_refused(self, *args, expect):
        """The claim refuses before anything exists: no worktree, no branch, no workspace, no session."""
        r = self.run_script(ORCH / "claim.sh", "12", *args)
        self.assertNotEqual(r.returncode, 0, r.stdout)
        self.assertIn("error:", r.stderr)
        self.assertIn(expect, r.stderr)
        self.assertEqual(self.git("branch", "--list", "fix/12-fix-login-timeout"), "")
        self.assertFalse((self.repo / ".claude/worktrees").exists())
        self.assertFalse([c for c in self.calls() if "worktree create" in c or "agent start" in c])
        return r

    def test_a_name_that_is_not_a_worker_knob_is_refused_with_the_names_that_are(self):
        # WF_MODE and CLAUDE_AUTOCOMPACT_PCT_OVERRIDE are the session's own keys, PATH is everything else.
        for name in ("WF_MODE", "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "PATH"):
            with self.subTest(name=name):
                r = self.assert_refused("--env", f"{name}=x", expect="Accepted names:")
                # The error is the whole list, so the fix is in it and nobody has to look the names up.
                for knob in ("WF_HANDOFF_TOKENS", "WF_REVIEWERS", "WF_DOCS_TIMEOUT"):
                    self.assertIn(knob, r.stderr)

    def test_a_malformed_argument_is_refused_with_the_shape_it_needs(self):
        for args in (["--env", "WF_HANDOFF_TOKENS"], ["--env", "=5000"], ["--env"]):
            with self.subTest(args=args):
                self.assert_refused(*args, expect="--env takes NAME=VALUE")

    def test_a_name_that_is_not_one_word_is_refused_and_sets_nothing(self):
        # Two accepted names in one argument, and a name with a glob in it: neither is a variable name, and
        # the lookup of an accepted name must not be talked into either by the spaces around its list.
        for name in ("WF_REVIEWERS WF_REVIEW_ROUNDS", "WF_REVIEWERS*", "wf reviewers"):
            with self.subTest(name=name):
                self.assert_refused("--env", f"{name}=x", expect="a name is A-Z, 0-9 and _")

    def test_the_same_name_twice_is_refused_rather_than_resolved(self):
        self.assert_refused("--env", "WF_HANDOFF_TOKENS=5000", "--env", "WF_HANDOFF_TOKENS=9000",
                            expect="was given twice")

    def test_a_claim_of_a_claimed_issue_says_the_values_were_not_applied(self):
        self.run_script(ORCH / "claim.sh", "12")
        self.reset_calls()
        r = self.run_script(ORCH / "claim.sh", "12", "--env", "WF_HANDOFF_TOKENS=5000")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("status: already-claimed", r.stdout)
        self.assertIn("env: not applied (WF_HANDOFF_TOKENS)", r.stdout)
        self.assertFalse([c for c in self.calls() if "worktree create" in c or "agent start" in c])

    def test_the_dry_run_names_the_variables_it_would_pass(self):
        r = self.run_script(ORCH / "claim.sh", "12", "--env", "WF_HANDOFF_TOKENS=5000", WF_DRY_RUN="1")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("status: dry-run", r.stdout)
        self.assertIn("env: WF_HANDOFF_TOKENS", r.stdout)


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
        self.assertIn("--strict-mcp-config", start)
        settings = json.loads(start[start.index("--settings") + 1])
        self.assertEqual(settings["env"], {"WF_PLAN": "offline-mode-for-the-app"})
        self.assertEqual(settings["enabledPlugins"], {"worker@workflows": False, "orchestrator@workflows": False, "repo-standards@workflows": False})
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

    def test_planning_sessions_keep_their_background_subagents(self):
        # The planner's research stage works while a subagent runs; only workers wait for their subagents.
        r = self.run_script(ORCH / "plan.sh", "Offline mode")
        self.assertEqual(r.returncode, 0, r.stderr)
        start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        settings = json.loads(start[start.index("--settings") + 1])
        self.assertNotIn("CLAUDE_CODE_DISABLE_BACKGROUND_TASKS", settings["env"])

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
        for args in ("--plugin-dir", "--plugin-dir --model sonnet", "--plugin-dir /nonexistent/dir",
                     "--plugin-dir=", "--plugin-dir=--model", "--plugin-dir=/nonexistent/dir"):
            r = self.run_script(ORCH / "plan.sh", "Offline mode", WF_CLAUDE_ARGS=args)
            self.assertNotEqual(r.returncode, 0, args)
            self.assertIn("WF_CLAUDE_ARGS", r.stderr)
            self.assertIn("--plugin-dir", r.stderr, args)
            self.assertFalse([c for c in self.calls() if "worktree create" in c], args)
        r = self.run_script(ORCH / "claim.sh", "12", WF_CLAUDE_ARGS="--plugin-dir")
        self.assertNotEqual(r.returncode, 0)

    def test_per_session_claude_args_reach_only_their_session(self):
        env = dict(WF_CLAUDE_ARGS="--verbose", WF_PLANNER_CLAUDE_ARGS="--model opus", WF_WORKER_CLAUDE_ARGS="--model sonnet")
        r = self.run_script(ORCH / "plan.sh", "Offline mode", **env)
        self.assertEqual(r.returncode, 0, r.stderr)
        start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        self.assertEqual(start[start.index("--model") + 1], "opus"); self.assertIn("--verbose", start)
        self.reset_calls()
        r = self.run_script(ORCH / "claim.sh", "12", **env)
        self.assertEqual(r.returncode, 0, r.stderr)
        start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        self.assertEqual(start[start.index("--model") + 1], "sonnet"); self.assertIn("--verbose", start)
        settings = json.loads(start[start.index("--settings") + 1])
        self.assertEqual(settings["enabledPlugins"], {"planner@workflows": False, "orchestrator@workflows": False})
        self.assertIn("--strict-mcp-config", start)
        r = self.run_script(ORCH / "plan.sh", "Other topic", WF_PLANNER_CLAUDE_ARGS="--plugin-dir")
        self.assertNotEqual(r.returncode, 0); self.assertIn("WF_PLANNER_CLAUDE_ARGS", r.stderr)

    def test_planner_language_is_the_sessions_language_and_reaches_nothing_else(self):
        home = self.base / "home"; home.mkdir()
        r = self.run_script(ORCH / "plan.sh", "Offline mode", WF_PLANNER_LANGUAGE="german", HOME=str(home))
        self.assertEqual(r.returncode, 0, r.stderr)
        start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        settings = json.loads(start[start.index("--settings") + 1])
        self.assertEqual(settings["language"], "german")
        self.assertEqual(settings["env"], {"WF_PLAN": "offline-mode"})
        # Session-scoped: no settings file anywhere, and the worker start carries no language.
        self.assertEqual(list(home.rglob("settings*.json")), [])
        wt = self.repo / ".claude/worktrees/plan-offline-mode"
        self.assertEqual(sorted(p.name for p in (self.repo / ".claude").glob("*")), ["worktrees"])
        self.assertFalse((wt / ".claude").exists())
        self.reset_calls()
        r = self.run_script(ORCH / "claim.sh", "12", WF_PLANNER_LANGUAGE="german", HOME=str(home))
        self.assertEqual(r.returncode, 0, r.stderr)
        start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        self.assertNotIn("language", json.loads(start[start.index("--settings") + 1]))

    def test_without_the_language_the_launch_is_unchanged(self):
        for topic, env in (("Unset topic", {}), ("Empty topic", {"WF_PLANNER_LANGUAGE": ""})):
            self.reset_calls()
            r = self.run_script(ORCH / "plan.sh", topic, **env)
            self.assertEqual(r.returncode, 0, r.stderr)
            start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
            self.assertNotIn("language", json.loads(start[start.index("--settings") + 1]), env)

    def test_language_composes_with_the_claude_args_that_follow_it(self):
        r = self.run_script(ORCH / "plan.sh", "Offline mode", WF_PLANNER_LANGUAGE="pt-br",
                            WF_CLAUDE_ARGS="--verbose", WF_PLANNER_CLAUDE_ARGS="--model opus")
        self.assertEqual(r.returncode, 0, r.stderr)
        start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        self.assertEqual(json.loads(start[start.index("--settings") + 1])["language"], "pt-br")
        self.assertEqual(start[start.index("--model") + 1], "opus"); self.assertIn("--verbose", start)
        # Documented precedence: claude keeps the last --settings, and the user's args come after ours.
        self.assertLess(start.index("--settings"), start.index("--verbose"))

    def test_a_language_claude_cannot_read_is_refused_before_anything_is_created(self):
        for bad in ("german\nEnglish", "german\t", "german\x1b[31m", "a" * 33):
            r = self.run_script(ORCH / "plan.sh", "Offline mode", WF_PLANNER_LANGUAGE=bad)
            self.assertNotEqual(r.returncode, 0, repr(bad))
            self.assertIn("error: WF_PLANNER_LANGUAGE", r.stderr, repr(bad))
            self.assertIn("WF_PLANNER_LANGUAGE=german", r.stderr, repr(bad))
            self.assertFalse([c for c in self.calls() if "worktree create" in c], repr(bad))

    def test_any_language_name_claude_can_read_travels_whole(self):
        # The value only has to survive jq and one argv element, so accents, scripts and spaces pass.
        for i, lang in enumerate(("fran\u00e7ais", "\u65e5\u672c\u8a9e", "brazilian portuguese", "pt-br")):
            self.reset_calls()
            r = self.run_script(ORCH / "plan.sh", f"Topic {i}", WF_PLANNER_LANGUAGE=lang)
            self.assertEqual(r.returncode, 0, r.stderr)
            start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
            self.assertEqual(json.loads(start[start.index("--settings") + 1])["language"], lang)

    def test_a_settings_of_your_own_warns_that_it_replaces_the_sessions_settings(self):
        # claude takes both spellings, so both must warn; neither stops the session.
        for i, args in enumerate((f"--settings {self.base}/mine.json", f"--settings={self.base}/mine.json")):
            self.reset_calls()
            r = self.run_script(ORCH / "plan.sh", f"Topic {i}", WF_PLANNER_LANGUAGE="german", WF_PLANNER_CLAUDE_ARGS=args)
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertIn("warning: WF_CLAUDE_ARGS/WF_PLANNER_CLAUDE_ARGS", r.stderr, args)
            self.assertIn("claude keeps only the last one", r.stderr, args)
            self.assertTrue([c for c in self.calls() if "agent start" in c], args)
        self.reset_calls()
        r = self.run_script(ORCH / "claim.sh", "12", WF_CLAUDE_ARGS="--settings /tmp/mine.json")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("warning: WF_CLAUDE_ARGS/WF_WORKER_CLAUDE_ARGS", r.stderr)
        # The shared warning is about the session's own settings, not about a planner-only knob.
        self.assertNotIn("WF_PLANNER_LANGUAGE", r.stderr)

    def test_long_topics_get_a_valid_herdr_agent_name(self):
        words = "Füge einen Map-Skill zum Planner hinzu, wie wayfinder von mattpocock".split()
        r = self.run_script(ORCH / "plan.sh", *words)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("branch: plan/fuege-einen-map-skill-zum-planner-hinzu", r.stdout)
        start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        self.assertRegex(start[3], r"^[a-z][a-z0-9_-]{0,31}$")
        self.assertIn("agent: plan-fuege-einen-map-skill-zum-p\n", r.stdout)
        self.assertIn("agent_status: working", r.stdout)

    def test_herdr_refusing_the_start_is_reported_at_once_and_rolled_back(self):
        r = self.run_script(ORCH / "plan.sh", "Offline mode", SHIM_AGENT_START_ERROR="pane is not at a shell prompt", WF_AGENT_WAIT="60")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("herdr agent start failed (pane_busy): pane is not at a shell prompt", r.stderr)
        self.assertNotIn("exited right after start", r.stderr)
        self.assertFalse([c for c in self.calls() if c.startswith("herdr agent list")])
        self.assertEqual(self.git("branch", "--list", "plan/offline-mode"), "")
        self.assertIn("herdr worktree remove --workspace w9 --force", self.calls())

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

    # The test hunt opens a worktree of a branch without an issue, as a planning session does, and starts the
    # session a manual claim starts.
    def with_tests(self):
        (self.repo / "tests").mkdir()
        (self.repo / "tests/test_login.py").write_text("def test_login():\n    assert True\n")
        self.git("add", "."); self.git("commit", "-qm", "test: login")

    def hunt(self, *args, **env):
        days = {date.today().isoformat()}
        r = self.run_script(ORCH / "hunt.sh", *args, **env)
        days.add(date.today().isoformat())
        return r, {f"hunt/tests-{d}" for d in days}

    def test_hunt_opens_a_hunt_worktree_and_starts_the_worker_on_the_hunt_skill(self):
        self.with_tests()
        r, branches = self.hunt()
        self.assertEqual(r.returncode, 0, r.stderr)
        branch = self.git("branch", "--list", "hunt/*", "--format=%(refname:short)").strip()
        self.assertIn(branch, branches)
        self.assertIn(f"branch: {branch}", r.stdout)
        self.assertIn("test_files: 1 in 1 directories", r.stdout)
        self.assertTrue((self.repo / ".claude/worktrees" / branch.replace("/", "-") / "tests/test_login.py").exists())
        start = [c for c in self.argv_calls() if c[1:3] == ["agent", "start"]][0]
        self.assertEqual(start[start.index("--agent") + 1], "worker")
        self.assertEqual(start[-1], "/worker:hunt-tests")
        settings = json.loads(start[start.index("--settings") + 1])
        # A manual claim's session, with no issue in it: the branch names none, and the status line says so.
        self.assertEqual(settings["env"], {"WF_MODE": "manual", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS": "1",
                                           "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE": "80"})
        self.assertEqual(settings["enabledPlugins"], {"planner@workflows": False, "orchestrator@workflows": False})
        self.assertEqual(shlex.split(settings["statusLine"]["command"]), [str(ORCH / "statusline.sh"), "250000"])
        self.assertEqual(settings["autoCompactWindow"], 312500)
        self.assertIn("agent_status: working", r.stdout)
        # No issue is read and none is created.
        self.assertFalse([c for c in self.calls() if c.startswith("gh issue")])

    def test_a_sandboxed_hunt_runs_the_hunt_skill_in_the_sandbox(self):
        self.with_tests()
        r, _ = self.hunt("--sandbox", "--base", "main")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("sandbox: docker", r.stdout)
        words = shlex.split([c for c in self.argv_calls() if c[1:3] == ["pane", "run"]][0][-1])
        self.assertEqual(words[0], str(ORCH / "sbx-worker.sh"))
        self.assertEqual(words[-1], "/worker:hunt-tests")

    def test_a_second_hunt_is_already_open_and_creates_nothing(self):
        self.with_tests()
        self.hunt()
        self.reset_calls()
        r, _ = self.hunt()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("status: already-open", r.stdout)
        self.assertIn("abandon.sh hunt/tests-", r.stdout)
        self.assertFalse([c for c in self.calls() if "worktree create" in c or "agent start" in c])

    def test_a_repository_without_test_files_is_refused_with_the_patterns_before_anything_exists(self):
        (self.repo / "tests/fixtures").mkdir(parents=True)
        (self.repo / "tests/fixtures/test_data.py").write_text("x = 1\n")
        self.git("add", "."); self.git("commit", "-qm", "fixture")
        r, _ = self.hunt()
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: no test file", r.stderr)
        self.assertIn("test_*.py", r.stderr); self.assertIn("*_test.go", r.stderr); self.assertIn("*.spec.*", r.stderr)
        self.assertEqual(self.git("branch", "--list", "hunt/*"), "")
        self.assertFalse([c for c in self.calls() if "worktree create" in c])

    def test_hunt_refuses_outside_herdr_and_an_issue_argument(self):
        self.with_tests()
        r, _ = self.hunt(HERDR_ENV="")
        self.assertNotEqual(r.returncode, 0); self.assertIn("HERDR_ENV", r.stderr)
        r, _ = self.hunt("12")
        self.assertNotEqual(r.returncode, 0); self.assertIn("takes no issue", r.stderr)
        self.assertEqual(self.git("branch", "--list", "hunt/*"), "")


class MergeTests(ShimTest):
    def pr_fixture(self, **over):
        pr = {"number": 7, "title": "fix: login timeout", "url": "https://github.com/o/r/pull/7", "state": "OPEN",
              "isDraft": False, "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN", "headRefName": "fix/12-fix-login-timeout",
              "baseRefName": "main", "isCrossRepository": False, "reviewDecision": "APPROVED",
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

    def test_merging_a_promotion_pr_uses_a_merge_commit_and_keeps_the_branch(self):
        self.git("branch", "dev")
        for head in ("dev", "main"):
            self.reset_calls()
            r = self.run_script(ORCH / "merge.sh", "7", SHIM_PR_FIXTURE=self.pr_fixture(headRefName=head, title="chore(release): v1.2.0"))
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertEqual([c for c in self.calls() if c.startswith("gh pr merge")], ["gh pr merge 7 --merge"])
            self.assertFalse([c for c in self.calls() if c.startswith("herdr worktree remove")])
            self.assertIn(head, self.git("branch", "--list", head))
            self.assertIn("merged: merge into main", r.stdout)
            self.assertIn(f"branch: {head} (long-lived) kept", r.stdout)

    def test_a_fork_pr_never_touches_a_local_branch_of_the_same_name(self):
        path = self.claimed()
        r = self.run_script(ORCH / "merge.sh", "7", SHIM_PR_FIXTURE=self.pr_fixture(isCrossRepository=True))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual([c for c in self.calls() if c.startswith("gh pr merge")], ["gh pr merge 7 --squash"])
        self.assertTrue(path.exists())
        self.assertIn("fix/12-fix-login-timeout", self.git("branch", "--list"))
        self.assertIn("branch: fix/12-fix-login-timeout (fork) kept", r.stdout)

    def test_allow_unstable_and_ignore_threads_flags(self):
        self.claimed()
        r = self.run_script(ORCH / "merge.sh", "7", "--allow-unstable", "--ignore-threads",
                            SHIM_PR_FIXTURE=self.pr_fixture(mergeStateStatus="UNSTABLE"), SHIM_UNRESOLVED="3")
        self.assertEqual(r.returncode, 0, r.stderr)


class ReleaseTests(ShimTest):
    def milestones(self, **over):
        m = {"number": 3, "title": "v1.2.0", "state": "open", "open_issues": 0, "closed_issues": 4, "description": "Offline mode"}
        m.update(over)
        f = self.base / f"milestones-{len(list(self.base.glob('milestones-*')))}.json"
        f.write_text(json.dumps([{"number": 2, "title": "v1.1.0", "state": "closed", "open_issues": 0, "closed_issues": 2}, m]))
        return str(f)

    def promotions(self, *prs):
        f = self.base / "promotions.json"
        f.write_text(json.dumps([{"isCrossRepository": False, **pr} for pr in prs]))
        return str(f)

    def mutations(self):
        return [c for c in self.calls() if c.startswith(("gh release create", "gh pr create", "gh api --method"))]

    def test_main_alone_tags_the_head_of_main_and_closes_the_milestone(self):
        r = self.run_script(ORCH / "release.sh", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("model: main\n", r.stdout)
        self.assertEqual(self.mutations(), [
            "gh release create v1.2.0 --target sha-main --title v1.2.0 --generate-notes",
            "gh api --method PATCH repos/o/r/milestones/3 -f state=closed",
        ])
        self.assertIn("release: https://github.com/o/r/releases/tag/v1.2.0", r.stdout)
        self.assertIn("status: released", r.stdout)

    def test_a_dev_branch_beside_the_default_main_is_not_a_promotion_model(self):
        r = self.run_script(ORCH / "release.sh", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones(), SHIM_BRANCHES="main dev")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("model: main\n", r.stdout)
        self.assertEqual(self.mutations(), [
            "gh release create v1.2.0 --target sha-main --title v1.2.0 --generate-notes",
            "gh api --method PATCH repos/o/r/milestones/3 -f state=closed",
        ])

    def test_a_default_branch_outside_the_two_models_or_unreadable_stops_before_anything_changes(self):
        for env, text in [(dict(SHIM_DEFAULT_BRANCH="trunk"), "default branch trunk is neither main nor dev"),
                          (dict(SHIM_DEFAULT_BRANCH="dev", SHIM_BRANCHES="dev"), "o/r has no main branch"),
                          (dict(SHIM_DEFAULT_ERROR="1"), "cannot read the default branch of o/r"),
                          (dict(SHIM_BRANCH_ERROR="main"), "cannot read branch main of o/r")]:
            r = self.run_script(ORCH / "release.sh", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones(), **env)
            self.assertNotEqual(r.returncode, 0, env)
            self.assertIn(f"error: {text}", r.stderr)
        self.assertEqual(self.mutations(), [])

    def test_dev_and_main_open_the_promotion_pr_and_wait_without_tagging(self):
        env = dict(SHIM_MILESTONES_FIXTURE=self.milestones(), SHIM_BRANCHES="main dev", SHIM_DEFAULT_BRANCH="dev")
        r = self.run_script(ORCH / "release.sh", "v1.2.0", **env)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("model: dev+main", r.stdout)
        self.assertIn("promotion: https://github.com/o/r/pull/77 (opened)", r.stdout)
        self.assertIn("status: waiting", r.stdout)
        self.assertIn("/orchestrator:merge 77", r.stdout)
        [create] = self.mutations()
        self.assertTrue(create.startswith("gh pr create --base main --head dev --title chore(release): v1.2.0 --body "), create)
        # A second run finds the open PR instead of opening another one, and still does not tag.
        self.reset_calls()
        open_pr = {"number": 77, "title": "chore(release): v1.2.0", "state": "OPEN", "url": "https://github.com/o/r/pull/77", "mergeCommit": None}
        r = self.run_script(ORCH / "release.sh", "v1.2.0", SHIM_PROMOTION_FIXTURE=self.promotions(open_pr), **env)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("promotion: https://github.com/o/r/pull/77 (open)", r.stdout)
        self.assertIn("status: waiting", r.stdout)
        self.assertEqual(self.mutations(), [])

    def test_promotion_ignores_fork_prs_and_refuses_while_another_promotion_is_open(self):
        env = dict(SHIM_MILESTONES_FIXTURE=self.milestones(), SHIM_BRANCHES="main dev", SHIM_DEFAULT_BRANCH="dev")
        fork = {"number": 66, "title": "chore(release): v1.2.0", "state": "MERGED", "url": "https://github.com/o/r/pull/66",
                "mergeCommit": {"oid": "evil"}, "isCrossRepository": True}
        r = self.run_script(ORCH / "release.sh", "v1.2.0", SHIM_PROMOTION_FIXTURE=self.promotions(fork), **env)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("promotion: https://github.com/o/r/pull/77 (opened)", r.stdout)
        self.assertFalse([c for c in self.calls() if "evil" in c])
        self.reset_calls()
        stale = {"number": 71, "title": "chore(release): v1.1.5", "state": "OPEN", "url": "https://github.com/o/r/pull/71", "mergeCommit": None}
        r = self.run_script(ORCH / "release.sh", "v1.2.0", SHIM_PROMOTION_FIXTURE=self.promotions(stale), **env)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("another promotion pull request is open (https://github.com/o/r/pull/71)", r.stderr)
        self.assertEqual(self.mutations(), [])

    def test_a_failed_tag_lookup_stops_before_publishing(self):
        r = self.run_script(ORCH / "release.sh", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones(), SHIM_TAG_ERROR="1")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: cannot check whether tag v1.2.0 exists", r.stderr)
        self.assertEqual(self.mutations(), [])

    def test_a_failed_milestone_close_says_the_release_is_already_published(self):
        r = self.run_script(ORCH / "release.sh", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones(), SHIM_MILESTONE_CLOSE_FAILS="1")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: release v1.2.0 is published but closing milestone v1.2.0 failed", r.stderr)

    def test_dev_and_main_tag_the_merge_commit_of_the_merged_promotion(self):
        older = {"number": 60, "title": "chore(release): v1.1.0", "state": "MERGED", "url": "https://github.com/o/r/pull/60", "mergeCommit": {"oid": "old"}}
        closed = {"number": 70, "title": "chore(release): v1.2.0", "state": "CLOSED", "url": "https://github.com/o/r/pull/70", "mergeCommit": None}
        merged = {"number": 77, "title": "chore(release): v1.2.0", "state": "MERGED", "url": "https://github.com/o/r/pull/77", "mergeCommit": {"oid": "abc123"}}
        r = self.run_script(ORCH / "release.sh", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones(), SHIM_BRANCHES="main dev", SHIM_DEFAULT_BRANCH="dev",
                            SHIM_PROMOTION_FIXTURE=self.promotions(older, closed, merged))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("promotion: https://github.com/o/r/pull/77 (merged)", r.stdout)
        self.assertEqual(self.mutations(), [
            "gh release create v1.2.0 --target abc123 --title v1.2.0 --generate-notes",
            "gh api --method PATCH repos/o/r/milestones/3 -f state=closed",
        ])
        self.assertIn("status: released", r.stdout)

    def test_an_open_issue_such_as_the_spec_holds_the_release_back(self):
        # release.sh counts open issues and knows nothing about specs, which is why the planner puts the spec on
        # the milestone: with every ticket closed, that one open issue is what holds the release back.
        r = self.run_script(ORCH / "release.sh", "v1.2.0", SHIM_MILESTONES_FIXTURE=self.milestones(open_issues=1, closed_issues=4))
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("error: milestone v1.2.0 has 1 open issue(s)", r.stderr)
        self.assertEqual(self.mutations(), [])

    def test_refuses_a_missing_milestone_and_an_existing_tag(self):
        cases = [
            (dict(), "milestone v1.2.0 does not exist"),
            (dict(SHIM_MILESTONES_FIXTURE=self.milestones(), SHIM_TAGS="v1.2.0"), "tag v1.2.0 already exists"),
        ]
        for env, text in cases:
            r = self.run_script(ORCH / "release.sh", "v1.2.0", SHIM_BRANCHES="main dev", SHIM_DEFAULT_BRANCH="dev", **env)
            self.assertNotEqual(r.returncode, 0, env)
            self.assertIn(f"error: {text}", r.stderr)
        r = self.run_script(ORCH / "release.sh", "1.2", SHIM_MILESTONES_FIXTURE=self.milestones())
        self.assertNotEqual(r.returncode, 0); self.assertIn("v1.2.3", r.stderr)
        r = self.run_script(ORCH / "release.sh", "v1.1.0", SHIM_MILESTONES_FIXTURE=self.milestones())
        self.assertNotEqual(r.returncode, 0); self.assertIn("already closed", r.stderr)
        self.assertEqual(self.mutations(), [])


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
        (self.repo / "test_app.py").write_text("def test_app():\n    assert 1\n")
        self.git("add", "."); self.git("commit", "-qm", "test")
        self.run_script(ORCH / "hunt.sh")
        r = self.run_script(ORCH / "board.sh")
        self.assertIn("worktrees[3]", r.stdout)
        self.assertRegex(r.stdout, r"\n  hunt,hunt/tests-\d{4}-\d{2}-\d{2},")

    def test_board_shows_the_frontier_of_unblocked_unclaimed_ready_issues(self):
        fixture = self.base / "ready.json"
        fixture.write_text(json.dumps([
            {"number": 40, "title": "Expand schema", "assignees": [], "issue_dependencies_summary": {"blocked_by": 0},
             "milestone": {"title": "v1.2.0"}},
            {"number": 44, "title": "Loose end", "assignees": [], "issue_dependencies_summary": {"blocked_by": 0}, "milestone": None},
            {"number": 41, "title": "Migrate callers", "assignees": [], "issue_dependencies_summary": {"blocked_by": 1}},
            {"number": 43, "title": "Taken", "assignees": [{"login": "bob"}], "issue_dependencies_summary": {"blocked_by": 0}},
            {"number": 12, "title": "Fix login timeout", "assignees": [], "issue_dependencies_summary": {"blocked_by": 0}},
            {"number": 50, "title": "A PR", "assignees": [], "pull_request": {"url": "x"}},
        ]))
        self.run_script(ORCH / "claim.sh", "12")
        r = self.run_script(ORCH / "board.sh", SHIM_FRONTIER_FIXTURE=str(fixture))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("frontier[2]{issue,milestone,title}:\n  40,v1.2.0,Expand schema\n  44,-,Loose end\n", r.stdout)
        self.assertNotIn("41,", r.stdout); self.assertNotIn("43,", r.stdout); self.assertNotIn("12,Fix", r.stdout)
        self.assertIn("waiting: 3 ready-for-agent issue(s)", r.stdout)

    def test_the_frontier_leaves_out_what_the_claim_refuses(self):
        fixture = self.base / "ready.json"
        fixture.write_text(json.dumps([
            {"number": 40, "title": "Expand schema", "assignees": [], "issue_dependencies_summary": {"blocked_by": 0},
             "milestone": None, "labels": [{"name": "ready-for-agent"}]},
            {"number": 45, "title": "Routed to the factory", "assignees": [], "milestone": None,
             "issue_dependencies_summary": {"blocked_by": 0}, "labels": [{"name": "ready-for-agent"}, {"name": "factory"}]},
        ]))
        r = self.run_script(ORCH / "board.sh", SHIM_FRONTIER_FIXTURE=str(fixture))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("frontier[1]{issue,milestone,title}:\n  40,-,Expand schema\n", r.stdout)
        self.assertNotIn("45,", r.stdout, "the factory works a routed issue; a local claim of it is refused")
        self.assertIn("waiting: 1 ready-for-agent issue(s) blocked, assigned, routed to the factory or claimed", r.stdout)

    def specs(self):
        """Four open specs on the shim: one accepted-ready, one with an open ticket, one without sub-issues, one closed."""
        fixture = self.base / "specs.json"
        fixture.write_text(json.dumps([
            {"number": 19, "title": "Accept a spec against the code", "state": "open", "labels": [{"name": "spec"}],
             "milestone": {"title": "v1.2.0"}, "sub_issues": [20, 21]},
            {"number": 20, "title": "Refuse to claim a raw issue", "state": "closed", "labels": []},
            {"number": 21, "title": "List the specs", "state": "closed", "labels": []},
            {"number": 30, "title": "Spec with an open ticket", "state": "open", "labels": [{"name": "spec"}],
             "milestone": None, "sub_issues": [31]},
            {"number": 31, "title": "Still open", "state": "open", "labels": []},
            {"number": 32, "title": "Spec nobody cut up", "state": "open", "labels": [{"name": "spec"}]},
            {"number": 33, "title": "Accepted spec", "state": "closed", "labels": [{"name": "spec"}], "sub_issues": [20]},
            {"number": 40, "title": r"Escape \n and \t in a title", "state": "open", "labels": [{"name": "spec"}],
             "milestone": {"title": "v1.3.0"}, "sub_issues": [21]},
        ]))
        return str(fixture)

    def test_board_lists_the_specs_whose_tickets_are_all_closed(self):
        r = self.run_script(ORCH / "board.sh", SHIM_SPEC_FIXTURE=self.specs())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("acceptance[2]{issue,milestone,title}:\n"
                      "  19,v1.2.0,Accept a spec against the code\n"
                      r"  40,v1.3.0,Escape \n and \t in a title" "\n", r.stdout)
        self.assertIn("help: every ticket is closed; accept the spec in a planning session, e.g. /orchestrator:plan #19.", r.stdout)
        for absent in ("30,", "32,", "33,"):
            self.assertNotIn(absent, r.stdout, "only an open spec with sub-issues and none of them open is due")
        self.assertLess(r.stdout.index("frontier["), r.stdout.index("acceptance["), "the section follows the frontier")

    def test_board_says_so_when_it_cannot_read_the_sub_issues(self):
        r = self.run_script(ORCH / "board.sh", SHIM_SPEC_FIXTURE=self.specs(), SHIM_NO_SUBISSUES="1")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("acceptance[0]{issue,milestone,title}:\n", r.stdout)
        self.assertIn("note: could not read the sub-issues of 4 spec(s); they are not listed.", r.stdout,
                      "an unreadable spec is not the same as a spec with nothing to accept")

    def test_board_without_a_spec_ready_and_with_github_unreachable(self):
        r = self.run_script(ORCH / "board.sh")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("acceptance[0]{issue,milestone,title}:\n", r.stdout)
        self.assertNotIn("help: every ticket is closed", r.stdout)
        r = self.run_script(ORCH / "board.sh", SHIM_GH_DOWN="1")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("worktrees[0]", r.stdout)
        self.assertNotIn("frontier[", r.stdout)
        self.assertNotIn("acceptance[", r.stdout)

    def test_board_says_so_when_it_cannot_read_the_open_specs(self):
        r = self.run_script(ORCH / "board.sh", SHIM_SPEC_FIXTURE=self.specs(), SHIM_SPECS_FAIL="1")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("acceptance[0]{issue,milestone,title}:\n", r.stdout)
        self.assertIn("note: could not read the open specs; the section is empty, not idle.", r.stdout)

    def test_a_plan_worktree_whose_slug_starts_with_a_number_is_not_a_claim(self):
        self.run_script(ORCH / "plan.sh", "12", "factor", "app")
        self.assertIn("plan/12-factor-app", self.git("branch", "--list"))
        # Neither the claim nor the abandon of issue #12 may take that planner worktree for its own.
        r = self.run_script(ORCH / "abandon.sh", "12")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("no worktree branch for issue #12", r.stderr)
        self.assertTrue((self.repo / ".claude/worktrees/plan-12-factor-app").exists())
        fixture = self.base / "frontier.json"
        fixture.write_text(json.dumps([
            {"number": 12, "title": "Fix login timeout", "assignees": [], "issue_dependencies_summary": {"blocked_by": 0}},
        ]))
        r = self.run_script(ORCH / "board.sh", SHIM_FRONTIER_FIXTURE=str(fixture))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("  12,-,Fix login timeout\n", r.stdout, "the plan worktree must not count as a claim")
        r = self.run_script(ORCH / "claim.sh", "12")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("branch: fix/12-fix-login-timeout", r.stdout)

    def test_abandon_names_an_issue_without_a_worktree(self):
        r = self.run_script(ORCH / "abandon.sh", "12")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("no worktree branch for issue #12", r.stderr)

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


class StatusLineTests(ShimTest):
    """The pane line and the context value file, from the JSON Claude Code pipes into the status line."""

    LINE = {"cwd": None, "model": {"id": "claude-opus-5", "display_name": "Opus"},
            "context_window": {"total_input_tokens": 78231, "context_window_size": 200000}}

    def statusline(self, payload, cwd=None, **env):
        payload = {**payload, "cwd": str(cwd or self.repo)}
        r = self.run_script(ORCH / "statusline.sh", stdin=json.dumps(payload), **env)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stderr, "", "a status line renders on every turn and must stay quiet")
        return r.stdout.strip()

    def value_file(self, path=None):
        return Path(self.git("rev-parse", "--path-format=absolute", "--git-dir", cwd=path or self.repo).strip()) / "worker/context"

    def test_shows_issue_mode_and_context_and_records_the_size(self):
        line = self.statusline(self.LINE, WF_ISSUE="12", WF_MODE="yolo")
        self.assertEqual(line, "#12 · yolo · 78k/200k (39%)")
        recorded = dict(l.split(": ", 1) for l in self.value_file().read_text().splitlines())
        self.assertEqual(recorded["total_input_tokens"], "78231")
        self.assertEqual(recorded["context_window_size"], "200000")
        self.assertRegex(recorded["at"], r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")

    def test_the_size_is_shown_against_the_compact_trigger(self):
        # A worker on a million-token model compacts at 250k, so 78k is almost a third of what it has; the
        # model's 7 % would read as room the session never gets. Without the argument nothing changes.
        big = {**self.LINE, "context_window": {"total_input_tokens": 78231, "context_window_size": 1000000}}
        self.assertEqual(self.statusline(big, WF_ISSUE="12", WF_MODE="manual"), "#12 · manual · 78k/1000k (7%)")
        r = self.run_script(ORCH / "statusline.sh", "250000", stdin=json.dumps({**big, "cwd": str(self.repo)}),
                            WF_ISSUE="12", WF_MODE="manual")
        self.assertEqual(r.stdout.strip(), "#12 · manual · 78k/250k (31%)")
        # The model's window wins when it is the smaller one, and the recorded value stays the model's.
        r = self.run_script(ORCH / "statusline.sh", "500000", stdin=json.dumps({**self.LINE, "cwd": str(self.repo)}),
                            WF_ISSUE="12", WF_MODE="manual")
        self.assertEqual(r.stdout.strip(), "#12 · manual · 78k/200k (39%)")
        self.assertIn("context_window_size: 200000", self.value_file().read_text())

    def test_input_without_context_numbers_prints_a_line_and_leaves_no_stale_value(self):
        # Before the first response of a session the input carries no context numbers at all.
        line = self.statusline({"model": {"id": "claude-opus-5"}}, WF_ISSUE="12", WF_MODE="manual")
        self.assertEqual(line, "#12 · manual · context n/a")
        self.assertFalse(self.value_file().exists(), "an invented zero would read as a nearly empty context")
        # And it does not overwrite a value an earlier render wrote either.
        self.statusline(self.LINE, WF_ISSUE="12", WF_MODE="manual")
        before = self.value_file().read_text()
        self.assertEqual(self.statusline({"context_window": {}}, WF_ISSUE="12", WF_MODE="manual"), "#12 · manual · context n/a")
        self.assertEqual(self.value_file().read_text(), before)

    def test_the_issue_comes_from_the_branch_when_the_session_carries_none(self):
        self.git("checkout", "-qb", "feat/12-x")
        self.assertEqual(self.statusline(self.LINE, WF_MODE="manual"), "#12 · manual · 78k/200k (39%)")
        self.git("checkout", "-q", "main")
        self.assertEqual(self.statusline(self.LINE, WF_MODE="manual"), "no issue · manual · 78k/200k (39%)")

    def test_each_worktree_records_into_its_own_git_directory(self):
        # Two workers run in parallel; the checkpoint of one must never read the size of the other.
        other = self.base / "wt-13"
        self.git("worktree", "add", "-q", "-b", "feat/13-y", str(other))
        self.statusline(self.LINE, WF_ISSUE="12", WF_MODE="manual")
        self.statusline({**self.LINE, "context_window": {"total_input_tokens": 5000, "context_window_size": 200000}},
                        cwd=other, WF_ISSUE="13", WF_MODE="manual")
        self.assertIn("total_input_tokens: 78231", self.value_file().read_text())
        self.assertIn("total_input_tokens: 5000", self.value_file(other).read_text())
        self.assertNotEqual(self.value_file(), self.value_file(other))


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


if __name__ == "__main__":
    unittest.main()
