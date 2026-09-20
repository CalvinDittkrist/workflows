import json
import unittest
from pathlib import Path

from helpers import STANDARDS, ShimTest

WORKSPACE = STANDARDS / "workspace.sh"
AUTOADD = "turn on Workflows > Auto-add to project with the filter is:issue,pr is:open for o/r (the API cannot create or turn on project workflows)"


def milestone(number, title, open_issues, closed_issues, state="open"):
    return {"number": number, "title": title, "state": state, "open_issues": open_issues, "closed_issues": closed_issues}


STATUS = ["Triage", "Ready", "In progress", "In review", "Done"]
PRIORITY = ["P0", "P1", "P2", "P3"]


def select(name, options):
    return {"name": name, "dataType": "SINGLE_SELECT", "options": [{"name": o} for o in options]}


def project(number, fields=None, autoadd=False, closed=False):
    """One projectsV2 node as the GraphQL query returns it; by default its fields match the standard."""
    if fields is None:
        fields = [select("Status", STATUS), select("Priority", PRIORITY)]
    return {"id": f"PVT_{number}", "number": number, "title": "r",
            "url": f"https://github.com/users/o/projects/{number}", "closed": closed,
            "workflows": {"nodes": [{"name": "Auto-add to project", "enabled": autoadd}]},
            "fields": {"nodes": [{"name": "Title", "dataType": "TITLE"}, *fields]}}


class WorkspaceTests(ShimTest):
    """workspace.sh against a stateful gh shim: $SHIM_WS holds the GitHub state and every write changes it."""

    def setUp(self):
        super().setUp()
        self.ws = self.base / "github"
        self.ws.mkdir()

    def put(self, name, data):
        (self.ws / name).write_text(json.dumps(data))

    def public_main(self):
        """A public repository on main alone, clicked together by hand."""
        self.put("repo.json", {
            "visibility": "public", "default_branch": "main", "permissions": {"admin": True},
            "allow_squash_merge": True, "allow_merge_commit": True, "allow_rebase_merge": True,
            "delete_branch_on_merge": False, "squash_merge_commit_title": "COMMIT_OR_PR_TITLE",
            "squash_merge_commit_message": "COMMIT_MESSAGES", "has_wiki": True, "has_discussions": False,
            "security_and_analysis": {"secret_scanning": {"status": "disabled"},
                                      "secret_scanning_push_protection": {"status": "disabled"}}})
        # GitHub matches label names ignoring case, so Wontfix counts as wontfix.
        self.put("labels.json", [{"name": n} for n in ("bug", "enhancement", "ready-for-agent", "question", "Wontfix")])
        # automated-security-fixes.json is absent: GitHub may answer 404 while Dependabot alerts are off.
        self.put("actions-workflow.json", {"default_workflow_permissions": "write", "can_approve_pull_request_reviews": True})
        self.put("private-vulnerability-reporting.json", {"enabled": False})
        self.put("protection-main.json", {"required_pull_request_reviews": {"required_approving_review_count": 1}})
        self.put("milestones.json", [
            milestone(1, "v0.1.0", 0, 3), milestone(2, "Backlog", 0, 2), milestone(3, "v0.2.0", 0, 0),
            milestone(4, "v0.3.0", 2, 0), milestone(5, "Someday", 1, 0), milestone(6, "old", 0, 0, state="closed")])
        self.put("projects.json", [])

    def private_dev_main(self):
        """A private repository on dev plus main that is close to the standard: dev conforms, main has a bypass."""
        self.public_main()
        repo = json.loads((self.ws / "repo.json").read_text())
        repo.update({"visibility": "private", "default_branch": "dev", "allow_merge_commit": False, "allow_rebase_merge": False,
                     "delete_branch_on_merge": True, "squash_merge_commit_title": "PR_TITLE", "has_wiki": False,
                     "security_and_analysis": None})
        self.put("repo.json", repo)
        self.put("labels.json", [{"name": n} for n in ("ready-for-agent", "needs-triage", "needs-info", "ready-for-human",
                                                        "wontfix", "spec", "bug", "enhancement", "skill-candidate")])
        (self.ws / "vulnerability-alerts").touch()
        self.put("automated-security-fixes.json", {"enabled": True, "paused": False})
        self.put("actions-workflow.json", {"default_workflow_permissions": "read"})
        (self.ws / "private-vulnerability-reporting.json").unlink()
        (self.ws / "protection-main.json").unlink()
        self.put("milestones.json", [milestone(1, "v1.0.0", 1, 4)])
        self.put("projects.json", [project(3)])
        # As GitHub returns them: ids, defaults the standard leaves open, rules in another order.
        dev = {"id": 7, "name": "standard: dev", "target": "branch", "enforcement": "active", "source_type": "Repository",
               "bypass_actors": [], "current_user_can_bypass": "never",
               "conditions": {"ref_name": {"exclude": [], "include": ["refs/heads/dev"]}},
               "rules": [{"type": "required_linear_history"}, {"type": "deletion"}, {"type": "non_fast_forward"},
                         {"type": "required_status_checks", "parameters": {
                             "strict_required_status_checks_policy": False, "do_not_enforce_on_create": False,
                             "required_status_checks": [{"context": "check", "integration_id": 15368}]}},
                         {"type": "pull_request", "parameters": {
                             "required_approving_review_count": 0, "dismiss_stale_reviews_on_push": False,
                             "require_code_owner_review": False, "require_last_push_approval": False,
                             "required_review_thread_resolution": True, "allowed_merge_methods": ["squash"],
                             "automatic_copilot_code_review_enabled": False}}]}
        main = json.loads(json.dumps(dev))
        main.update({"id": 8, "name": "standard: main", "bypass_actors": [{"actor_id": 5, "actor_type": "RepositoryRole",
                                                                           "bypass_mode": "always"}],
                     "conditions": {"ref_name": {"exclude": [], "include": ["refs/heads/main"]}}})
        main["rules"] = [r for r in main["rules"] if r["type"] != "required_linear_history"]
        main["rules"][-1]["parameters"]["allowed_merge_methods"] = ["squash", "merge"]
        copilot = {"id": 9, "name": "Copilot review for default branch", "target": "branch", "enforcement": "active",
                   "bypass_actors": [], "conditions": {"ref_name": {"exclude": [], "include": ["~DEFAULT_BRANCH"]}},
                   "rules": [{"type": "copilot_code_review", "parameters": {"review_on_push": False}}]}
        for r in (dev, main, copilot):
            self.put(f"ruleset-{r['id']}.json", r)

    def ws_run(self, *args, **env):
        return self.run_script(WORKSPACE, *args, SHIM_WS=str(self.ws), TMPDIR=str(self.base), **env)

    def writes(self):
        return [c for c in self.calls()
                if " --method " in c or c.startswith(("gh project copy", "gh project link", "gh api graphql --input -"))]

    def body(self, prefix):
        """Payload of the one logged write that starts with prefix."""
        hits = [c for c in self.writes() if c.startswith(prefix)]
        self.assertEqual(len(hits), 1, hits)
        return json.loads(hits[0].split(" --input - ", 1)[1])

    def assert_clean_second_run(self):
        self.reset_calls()
        r = self.ws_run()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("diff:", r.stdout)
        self.assertIn("differences: 0", r.stdout)
        r = self.ws_run("--apply")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("applied: 0", r.stdout)
        self.assertNotIn("snapshot:", r.stdout)
        self.assertEqual(self.writes(), [])
        return r

    def test_public_main_dry_run_prints_every_difference_and_changes_nothing(self):
        self.public_main()
        r = self.ws_run(WF_PROJECT_TEMPLATE="tpl-owner/1")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "\n".join([
            "repository: o/r",
            "profile: public, main",
            "diff: repo allow_merge_commit: true -> false",
            "diff: repo allow_rebase_merge: true -> false",
            "diff: repo delete_branch_on_merge: false -> true",
            "diff: repo squash_merge_commit_title: COMMIT_OR_PR_TITLE -> PR_TITLE",
            "diff: repo has_wiki: true -> false",
            "diff: branch-protection main: classic -> removed (the ruleset replaces it)",
            "diff: ruleset standard: main: missing -> create",
            "diff: ruleset standard: pre-standard: missing -> create",
            "diff: label needs-triage: missing -> create",
            "diff: label needs-info: missing -> create",
            "diff: label ready-for-human: missing -> create",
            "diff: label spec: missing -> create",
            "diff: label skill-candidate: missing -> create",
            "diff: dependabot alerts: off -> on",
            "diff: dependabot security-updates: off -> on",
            "diff: actions default-token: write -> read",
            "diff: actions token-approves-pull-requests: true -> false",
            "diff: secret-scanning: disabled -> enabled",
            "diff: secret-scanning-push-protection: disabled -> enabled",
            "diff: private-vulnerability-reporting: off -> on",
            "diff: milestone Backlog: open, orphaned -> closed",
            "diff: milestone v0.2.0: open, empty -> closed",
            "diff: project: none linked -> copy of tpl-owner/1",
            f"manual: project (the copy): {AUTOADD}",
            "differences: 23",
            "next: run workspace.sh --apply to make these changes",
            ""]))
        self.assertEqual(self.writes(), [])
        self.assertEqual(self.ws_run(WF_PROJECT_TEMPLATE="tpl-owner/1").stdout, r.stdout, "the output is stable")

    def test_public_main_apply_snapshots_first_changes_exactly_the_differences_and_is_idempotent(self):
        self.public_main()
        before = {p.name: p.read_text() for p in self.ws.iterdir()}
        snap = self.base / "snapshot.json"
        r = self.ws_run("--apply", "--snapshot", str(snap), WF_PROJECT_TEMPLATE="tpl-owner/1")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(f"snapshot: {snap}", r.stdout)
        self.assertIn("project: https://github.com/users/o/projects/12", r.stdout)
        self.assertTrue(r.stdout.endswith("applied: 23\n"), r.stdout)

        s = json.loads(snap.read_text())
        self.assertEqual(s["repository"], "o/r")
        old = json.loads(before["repo.json"])
        self.assertEqual(s["repo"]["allow_rebase_merge"], True)
        self.assertEqual(s["repo"]["security_and_analysis"], old["security_and_analysis"])
        self.assertEqual(s["branch_protection"], {"main": json.loads(before["protection-main.json"])})
        self.assertEqual(s["labels"], ["bug", "enhancement", "ready-for-agent", "question", "Wontfix"])
        self.assertEqual(s["dependabot"], {"alerts": "off", "security_updates": "off"})
        self.assertEqual(s["actions_default_token"], "write")
        self.assertEqual(s["actions_token_approves_pull_requests"], True)
        self.assertEqual(s["private_vulnerability_reporting"], "off")
        self.assertEqual([m["title"] for m in s["milestones_closed"]], ["Backlog", "v0.2.0"])

        writes = [c.split(" --input - ")[0] for c in self.writes()]
        self.assertEqual(writes, [
            "gh api --method PATCH repos/o/r",
            "gh api --method POST repos/o/r/rulesets",
            "gh api --method POST repos/o/r/rulesets",
            "gh api --method DELETE repos/o/r/branches/main/protection",
            *["gh api --method POST repos/o/r/labels"] * 5,
            "gh api --method PUT repos/o/r/vulnerability-alerts",
            "gh api --method PUT repos/o/r/automated-security-fixes",
            "gh api --method PUT repos/o/r/actions/permissions/workflow",
            "gh api --method PATCH repos/o/r",
            "gh api --method PUT repos/o/r/private-vulnerability-reporting",
            "gh api --method PATCH repos/o/r/milestones/2",
            "gh api --method PATCH repos/o/r/milestones/3",
            "gh project copy 1 --source-owner tpl-owner --target-owner o --title r --format json",
            "gh project link 12 --owner o --repo o/r",
        ])
        self.assertEqual(self.body("gh api --method PATCH repos/o/r --input - {\"allow"), {
            "allow_merge_commit": False, "allow_rebase_merge": False, "delete_branch_on_merge": True,
            "squash_merge_commit_title": "PR_TITLE", "squash_merge_commit_message": "COMMIT_MESSAGES", "has_wiki": False})
        main, tag = [json.loads(c.split(" --input - ")[1]) for c in self.writes() if "rulesets" in c]
        self.assertEqual(main["name"], "standard: main")
        self.assertEqual(main["bypass_actors"], [])
        self.assertEqual(main["conditions"]["ref_name"]["include"], ["refs/heads/main"])
        rules = {r["type"]: r.get("parameters") for r in main["rules"]}
        self.assertEqual(set(rules), {"deletion", "non_fast_forward", "pull_request", "required_status_checks",
                                      "required_linear_history"})
        self.assertEqual(rules["pull_request"]["required_approving_review_count"], 0)
        self.assertTrue(rules["pull_request"]["required_review_thread_resolution"])
        self.assertEqual(rules["pull_request"]["allowed_merge_methods"], ["squash"])
        self.assertEqual(rules["required_status_checks"]["required_status_checks"], [{"context": "check"}])
        self.assertEqual(tag["target"], "tag")
        self.assertEqual(tag["conditions"]["ref_name"]["include"], ["refs/tags/pre-standard"])
        self.assertEqual({r["type"] for r in tag["rules"]}, {"deletion", "update"})
        labels = [json.loads(c.split(" --input - ")[1]) for c in self.writes() if "/labels" in c]
        self.assertEqual(labels[-1], {"name": "skill-candidate", "color": "C5DEF5",
                                      "description": "A removed skill that could move into the marketplace"})
        self.assertEqual(self.body("gh api --method PUT repos/o/r/actions/permissions/workflow"),
                         {"default_workflow_permissions": "read", "can_approve_pull_request_reviews": False})
        self.assertEqual(self.body("gh api --method PATCH repos/o/r --input - {\"security"), {"security_and_analysis": {
            "secret_scanning": {"status": "enabled"}, "secret_scanning_push_protection": {"status": "enabled"}}})
        self.assertEqual(self.body("gh api --method PATCH repos/o/r/milestones/2"), {"state": "closed"})
        self.assertFalse(any("milestones" in c and "POST" in c for c in self.calls()), "no milestone is created")

        r = self.assert_clean_second_run()
        # The copied project has auto-add off, which only a person can turn on.
        self.assertIn(f"manual: project https://github.com/users/o/projects/12: {AUTOADD}", r.stdout)

    def test_private_dev_main_replaces_only_the_drifted_ruleset_and_keeps_others(self):
        self.private_dev_main()
        r = self.ws_run()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "\n".join([
            "repository: o/r",
            "profile: private, dev+main",
            "diff: repo allow_merge_commit: false -> true",
            "diff: ruleset standard: main: differs -> replace",
            "diff: ruleset standard: pre-standard: missing -> create",
            "manual: secret scanning and push protection: not offered for this private repository (GitHub Secret "
            "Protection needs an organisation on GitHub Team or Enterprise); make the repository public or move it to "
            "such an organisation to get them",
            f"manual: project https://github.com/users/o/projects/3: {AUTOADD}",
            "differences: 3",
            "next: run workspace.sh --apply to make these changes",
            ""]))
        self.assertFalse(any("private-vulnerability-reporting" in c for c in self.calls()))

        r = self.ws_run("--apply")
        self.assertEqual(r.returncode, 0, r.stderr)
        snap = r.stdout.split("snapshot: ", 1)[1].splitlines()[0]
        self.assertTrue(snap.startswith(str(self.base / "workspace-snapshot.")), snap)
        self.assertEqual([x["name"] for x in json.loads(Path(snap).read_text())["rulesets"]],
                         ["standard: main", "standard: dev"])
        self.assertEqual([c.split(" --input - ")[0] for c in self.writes()], [
            "gh api --method PATCH repos/o/r",
            "gh api --method PUT repos/o/r/rulesets/8",
            "gh api --method POST repos/o/r/rulesets"])
        self.assertEqual(self.body("gh api --method PATCH repos/o/r"), {"allow_merge_commit": True})
        main = self.body("gh api --method PUT repos/o/r/rulesets/8")
        self.assertEqual(main["bypass_actors"], [])
        rules = {r["type"]: r.get("parameters") for r in main["rules"]}
        self.assertNotIn("required_linear_history", rules, "the promotion lands on main as a merge commit")
        self.assertEqual(rules["pull_request"]["allowed_merge_methods"], ["merge", "squash"])
        self.assertIn("Copilot review", (self.ws / "ruleset-9.json").read_text())
        self.assert_clean_second_run()

    def test_apply_refuses_the_branch_ruleset_until_the_default_branch_has_a_check_job(self):
        self.public_main()
        (self.ws / "check-runs").write_text("0")
        r = self.ws_run()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("blocked: the default branch main has no CI job named check; add a CI job named check that runs "
                      "make check on every push to main, merge it so it runs on the head of main, then run workspace.sh "
                      "--apply again", r.stdout)
        snap = self.base / "snapshot.json"
        r = self.ws_run("--apply", "--snapshot", str(snap))
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: refusing to apply: the default branch main has no CI job named check", r.stderr)
        self.assertEqual(self.writes(), [])
        self.assertFalse(snap.exists())

    def test_without_a_template_a_missing_project_is_a_manual_step_not_a_difference(self):
        self.public_main()
        r = self.ws_run()
        self.assertIn("manual: project: none linked; set WF_PROJECT_TEMPLATE=<owner>/<number> and run again to copy "
                      "the template project, or create one by hand", r.stdout)
        self.assertIn("differences: 22", r.stdout)
        self.assertNotIn("diff: project", r.stdout)
        r = self.ws_run(WF_PROJECT_TEMPLATE="not a project")
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: WF_PROJECT_TEMPLATE must look like <owner>/<number>", r.stderr)

    def test_a_failed_write_stops_the_run_and_leaves_the_snapshot_of_the_state_before(self):
        self.public_main()
        snap = self.base / "snapshot.json"
        r = self.ws_run("--apply", "--snapshot", str(snap), SHIM_WS_FAIL="api --method POST repos/o/r/labels")
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: POST repos/o/r/labels failed: gh: Validation Failed (HTTP 422); the snapshot has the state "
                      "before this run", r.stderr)
        self.assertEqual(json.loads(snap.read_text())["repo"]["allow_rebase_merge"], True)
        self.assertFalse(any("vulnerability-alerts" in c for c in self.writes()), "nothing after the failure ran")
        r = self.ws_run("--apply", "--snapshot", str(snap))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("diff: repo", r.stdout, "a second run continues where the first stopped")
        self.assert_clean_second_run()

    def test_a_private_repository_without_rulesets_on_its_plan_gets_a_manual_step(self):
        self.private_dev_main()
        (self.ws / "plan-free").touch()
        r = self.ws_run()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("manual: rulesets: not offered for this private repository on its account's plan; upgrade the "
                      "account to GitHub Pro or make the repository public, then run workspace.sh again", r.stdout)
        self.assertNotIn("diff: ruleset", r.stdout)
        self.assertIn("differences: 1", r.stdout)
        self.assertEqual(self.ws_run("--apply").returncode, 0)
        self.assertFalse(any("rulesets" in c or "protection" in c for c in self.writes()))

    def test_a_token_without_the_project_scope_skips_only_the_project(self):
        self.public_main()
        (self.ws / "no-project-scope").touch()
        r = self.ws_run(WF_PROJECT_TEMPLATE="tpl-owner/1")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("manual: project: not checked, the gh token cannot read projects; run gh auth refresh -s project, "
                      "then run workspace.sh again", r.stdout)
        self.assertNotIn("diff: project", r.stdout)
        self.assertNotIn(" field ", r.stdout, "no field is checked without the scope")
        self.assertIn("differences: 22", r.stdout)

    def test_a_default_branch_outside_the_two_models_blocks_apply(self):
        self.public_main()
        repo = json.loads((self.ws / "repo.json").read_text())
        repo["default_branch"] = "master"
        self.put("repo.json", repo)
        r = self.ws_run()
        self.assertIn("blocked: the default branch is master, but the standard knows main alone or dev plus main; "
                      "rename it to main", r.stdout)
        self.assertNotIn("next:", r.stdout)
        r = self.ws_run("--apply")
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: refusing to apply: the default branch is master", r.stderr)
        self.assertEqual(self.writes(), [])

    def test_snapshot_without_apply_is_refused(self):
        r = self.ws_run("--snapshot", str(self.base / "s.json"))
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: --snapshot is written by --apply only", r.stderr)

    def test_refuses_without_admin_rights(self):
        self.public_main()
        repo = json.loads((self.ws / "repo.json").read_text())
        repo["permissions"] = {"admin": False, "push": True}
        self.put("repo.json", repo)
        r = self.ws_run()
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: admin rights on o/r are needed", r.stderr)

    def test_a_project_field_that_is_missing_is_created_and_one_that_differs_is_left_to_a_person(self):
        self.public_main()
        # What GitHub gives a new project: Status with its own options, no Priority.
        self.put("projects.json", [project(3, fields=[select("Status", ["Todo", "In progress", "Done"])], autoadd=True)])
        url = "https://github.com/users/o/projects/3"
        r = self.ws_run()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(f"diff: project {url} field Priority: missing -> create single-select with P0, P1, P2, P3", r.stdout)
        status_line = (f"manual: project {url} field Status: options Todo, In progress, Done, but the standard wants "
                       "Triage, Ready, In progress, In review, Done; change them by hand (replacing an option list "
                       "clears the field on every item)")
        self.assertIn(status_line, r.stdout)
        self.assertIn("differences: 23", r.stdout)
        self.assertEqual(self.writes(), [])

        snap = self.base / "snapshot.json"
        r = self.ws_run("--apply", "--snapshot", str(snap))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(json.loads(snap.read_text())["projects"], [{
            "number": 3, "title": "r", "url": url,
            "fields": [{"name": "Title", "dataType": "TITLE", "options": []},
                       {"name": "Status", "dataType": "SINGLE_SELECT", "options": ["Todo", "In progress", "Done"]}]}],
            "the snapshot holds the fields as they were before the run")
        created = [c for c in self.writes() if c.startswith("gh api graphql --input -")]
        self.assertEqual(len(created), 1, self.writes())
        body = json.loads(created[0].split(" --input - ", 1)[1])
        self.assertIn("createProjectV2Field", body["query"])
        self.assertEqual(body["variables"]["p"], "PVT_3")
        self.assertEqual(body["variables"]["n"], "Priority")
        self.assertEqual([o["name"] for o in body["variables"]["o"]], ["P0", "P1", "P2", "P3"])
        self.assertTrue(all(o["color"] and o["description"] for o in body["variables"]["o"]), body["variables"]["o"])

        self.reset_calls()
        r = self.ws_run()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("diff:", r.stdout)
        self.assertIn(status_line, r.stdout)
        self.assertNotIn("field Priority", r.stdout, "the created field matches the standard")
        self.assertIn("differences: 0", r.stdout)

    def test_project_field_options_match_in_any_order_and_casing(self):
        self.private_dev_main()
        self.put("projects.json", [project(3, fields=[select("status", ["done", "IN REVIEW", "In progress", "ready", "TRIAGE"]),
                                                      select("Priority", ["P3", "P2", "P1", "P0"])])])
        r = self.ws_run()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn(" field ", r.stdout)
        self.assertIn("differences: 3", r.stdout)

    def test_an_extra_option_or_a_field_of_another_type_is_a_manual_step_and_nothing_is_written(self):
        self.private_dev_main()
        url = "https://github.com/users/o/projects/3"
        self.put("projects.json", [project(3, fields=[select("Status", [*STATUS, "Blocked"]),
                                                      {"name": "Priority", "dataType": "TEXT"}])])
        r = self.ws_run()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(f"manual: project {url} field Status: options Triage, Ready, In progress, In review, Done, Blocked, "
                      "but the standard wants Triage, Ready, In progress, In review, Done; change them by hand "
                      "(replacing an option list clears the field on every item)", r.stdout)
        self.assertIn(f"manual: project {url} field Priority: a text field, but the standard wants a single-select with "
                      "P0, P1, P2, P3; change it by hand", r.stdout)
        self.assertNotIn("diff: project", r.stdout)
        self.assertIn("differences: 3", r.stdout)
        self.assertEqual(self.ws_run("--apply").returncode, 0)
        self.assertFalse([c for c in self.writes() if "graphql" in c], "an existing field is never rewritten")

    def test_more_than_one_open_project_is_a_manual_step_and_every_one_is_checked_but_none_is_written(self):
        self.private_dev_main()
        self.put("projects.json", [project(3), project(4, fields=[select("Status", STATUS)], autoadd=True),
                                   project(5, closed=True, fields=[])])
        r = self.ws_run()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("manual: projects: 2 open ones are linked (https://github.com/users/o/projects/3, "
                      "https://github.com/users/o/projects/4); the standard wants one; unlink or close the others by hand",
                      r.stdout)
        self.assertNotIn("projects/5", r.stdout, "a closed project is not checked")
        # The missing field is reported, but creating it would write into a project the run just asked to unlink.
        self.assertIn("manual: project https://github.com/users/o/projects/4 field Priority: missing; the run creates "
                      "it only while one project is linked, so unlink the others and run again, or create the "
                      "single-select with P0, P1, P2, P3 by hand", r.stdout)
        self.assertNotIn("projects/3 field", r.stdout)
        self.assertNotIn("diff: project", r.stdout)
        self.assertIn("differences: 3", r.stdout)
        self.assertEqual(self.ws_run("--apply").returncode, 0)
        self.assertFalse([c for c in self.writes() if "graphql" in c], "no field is created")

    def test_a_field_type_the_query_cannot_read_does_not_hide_the_other_fields(self):
        self.private_dev_main()
        # A field type that implements none of the query's fragments comes back as an empty node.
        self.put("projects.json", [project(3, fields=[{}, select("Status", STATUS)])])
        r = self.ws_run()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("diff: project https://github.com/users/o/projects/3 field Priority: missing -> create "
                      "single-select with P0, P1, P2, P3", r.stdout)
        self.assertIn("differences: 4", r.stdout)

    def test_a_failed_field_creation_stops_the_run_and_names_the_project(self):
        self.private_dev_main()
        self.put("projects.json", [project(3, fields=[select("Status", STATUS)])])
        snap = self.base / "snapshot.json"
        r = self.ws_run("--apply", "--snapshot", str(snap), SHIM_WS_FAIL="api graphql --input -")
        self.assertEqual(r.returncode, 1)
        self.assertIn("error: creating the field Priority on https://github.com/users/o/projects/3 failed: gh: "
                      "Validation Failed (HTTP 422); the snapshot has the state before this run", r.stderr)
        self.assertEqual(json.loads(snap.read_text())["projects"][0]["fields"][-1]["name"], "Status")

    def test_check_reports_workspace_drift_and_skips_it_when_github_is_unreachable(self):
        self.assertEqual(self.run_script(STANDARDS / "scaffold.sh").returncode, 0)
        r = self.run_script(STANDARDS / "check.sh")
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("skip: GitHub workspace not checked (cannot read repos/o/r:", r.stdout)
        self.private_dev_main()
        r = self.run_script(STANDARDS / "check.sh", SHIM_WS=str(self.ws))
        self.assertEqual(r.returncode, 0, r.stdout)
        drift = [line for line in r.stdout.splitlines() if "GitHub workspace" in line]
        self.assertEqual(drift, [
            "warn: GitHub workspace: repo allow_merge_commit: false -> true",
            "warn: GitHub workspace: ruleset standard: main: differs -> replace",
            "warn: GitHub workspace: ruleset standard: pre-standard: missing -> create",
            "warn: GitHub workspace differs from the standard; plugins/repo-standards/scripts/workspace.sh shows why, "
            "--apply fixes it"])
        snap = self.base / "snapshot.json"
        self.assertEqual(self.ws_run("--apply", "--snapshot", str(snap)).returncode, 0)
        r = self.run_script(STANDARDS / "check.sh", SHIM_WS=str(self.ws))
        self.assertIn("ok: GitHub workspace matches the standard", r.stdout)
        self.assertNotIn("warn: GitHub workspace", r.stdout)


if __name__ == "__main__":
    unittest.main()
