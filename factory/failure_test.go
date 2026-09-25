package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The failure matrix of the stages: for every kind of session, how the run ends when it reports
// blocked, reports a result that does not fit its schema, runs past its stage timeout, or is cancelled
// under it. Each case starts the real binary against the claude and gh shims and reads the run as the
// interface serves it, its log, and what the factory wrote to GitHub.

// loggedEnding says whether the log of a run carries its ending: an event titled with the outcome whose
// body is the reason the run gives.
func loggedEnding(run apiRun) bool {
	for _, e := range run.Events {
		if e.Title == run.Outcome && e.Body == run.Reason {
			return true
		}
	}
	return false
}

// reportedBlocked says whether the log of a run carries a result line of a session with that summary.
func reportedBlocked(run apiRun, summary string) bool {
	for _, e := range run.Events {
		if e.Kind == "result" && strings.Contains(e.Body, summary) {
			return true
		}
	}
	return false
}

// A session that reports blocked blocks the run on its own words, whether it is the implement session
// or the fix session of a review round, and the maintainers hear of it on the issue.
func TestASessionThatReportsBlockedBlocksTheRunOnItsWords(t *testing.T) {
	t.Parallel()
	const blocker = "the broker's retry contract is undocumented; a maintainer has to say what it is"
	for name, c := range map[string]struct {
		prepare func(*ghShim, *testing.T)
		stage   string
		workers int
	}{
		"the implement session": {func(gh *ghShim, t *testing.T) {
			gh.workerReportsBlocked(t, blocker)
			gh.workerCommits(t, "")
		}, stageImplement, 1},
		"the fix session of a review round": {func(gh *ghShim, t *testing.T) {
			gh.verdict(t, "code", 1, findings(t, Finding{Severity: "S2", Path: "a.go", Line: 3, Claim: "Off by one.", Why: "It skips the last.", Fix: "Use <=."}))
			gh.repairs(t, map[string]any{"outcome": "blocked", "fixed": []string{}, "disputed": []map[string]any{}, "skipped": []map[string]any{}, "summary": blocker})
		}, stageReview, 2},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := panelClaim(t, "@true")
			c.prepare(gh, t)
			gh.comments(t, "acme/edge-sensors", claimedIssue)
			cfg := ciConfig(data, nil)
			cfg["notify"] = maintainers
			f := gh.work(t, cfg)
			run := f.ended(t, 1)
			if run.Outcome != outcomeBlocked || run.Reason != blocker || run.Stage != c.stage {
				t.Fatalf("the run ended as %q in the stage %q (%s), want blocked in %s on the session's words; the factory's log:\n%s",
					run.Outcome, run.Stage, run.Reason, c.stage, f.output(t))
			}
			if !reportedBlocked(run, blocker) {
				t.Errorf("the log of the run carries no result line with the blocker")
			}
			f.notified(t, 1)
			if said := gh.commented(t, "acme/edge-sensors", claimedIssue); !strings.Contains(said, "`blocked`") || !strings.Contains(said, blocker) {
				t.Errorf("the comment on the issue is %q, want the blocked run and its blocker in it", said)
			}
			if workers := gh.workers(t); len(workers) != c.workers {
				t.Errorf("the factory started %d worker sessions, want %d", len(workers), c.workers)
			}
			if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 0 {
				t.Errorf("the factory opened %d pull requests for a blocked run", len(pulls))
			}
		})
	}
}

// A fix session of the gate, a fix session of the ci stage and an address-reviews session that report
// a result that does not fit their schema fail the run, which names the stage of the session.
func TestAnUnfittingResultOfAFixSessionFailsTheRunNamingItsStage(t *testing.T) {
	t.Parallel()
	const unfitting = `{"outcome":"ready","summary":"pushed the fix"}`
	for name, c := range map[string]struct {
		recipe  string
		prepare func(*ghShim, *testing.T)
		stage   string
	}{
		"a fix session of the gate": {"@echo the gate fails; exit 2", func(*ghShim, *testing.T) {}, stageGate},
		"a fix session of the ci stage": {"@true", func(gh *ghShim, t *testing.T) {
			gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{checks: []map[string]any{failed("test", 4242)}})
			gh.answer(t, "run view 4242 --repo acme/edge-sensors --log-failed", "FAIL: TestUploadRetries\n")
		}, stageCI},
		"an address-reviews session": {"@true", func(gh *ghShim, t *testing.T) {
			gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{
				reviews: []map[string]any{objectionOf(t, gh, 9001, "maintainer", "Back off between the retries.")}})
		}, stageAddressReviews},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := panelClaim(t, c.recipe)
			c.prepare(gh, t)
			gh.env = append(gh.env, "CLAUDE_SHIM_THEN_RESULT="+unfitting)
			f := gh.work(t, ciConfig(data, nil))
			run := f.ended(t, 1)
			want := "the result of the session of the stage " + c.stage + " does not fit the schema"
			if run.Outcome != outcomeFailed || !strings.Contains(run.Reason, want) || run.Stage != c.stage {
				t.Fatalf("the run ended as %q in the stage %q (%s), want failed with %q; the factory's log:\n%s",
					run.Outcome, run.Stage, run.Reason, want, f.output(t))
			}
			if !loggedEnding(run) {
				t.Errorf("the log of the run does not carry its ending %q", run.Reason)
			}
			if workers := gh.workers(t); len(workers) != 2 {
				t.Errorf("the factory started %d worker sessions, want the implement session and the one that did not fit", len(workers))
			}
		})
	}
}

// A reviewer, a fix session of the review, the author of the pull request and an address-reviews
// session that run past the timeout of their stage are ended with their whole process group, and the
// run fails naming the stage.
func TestASessionOfALaterStagePastItsTimeoutIsEndedAndFailsNamingTheStage(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		prepare  func(*ghShim, *testing.T)
		sessions func(*ghShim, *testing.T) []workerStart
		stage    string
		reason   string
	}{
		"a reviewer": {func(gh *ghShim, t *testing.T) {
			gh.verdict(t, "code", 1, "SLEEP 600\n"+findings(t))
		}, (*ghShim).reviewerSessions, stageReview, "the code reviewer: "},
		"a fix session of the review": {func(gh *ghShim, t *testing.T) {
			gh.verdict(t, "code", 1, findings(t, Finding{Severity: "S2", Path: "a.go", Line: 3, Claim: "Off by one.", Why: "It skips the last.", Fix: "Use <=."}))
			gh.env = append(gh.env, "CLAUDE_SHIM_THEN_SLEEP=600")
		}, (*ghShim).workers, stageReview, ""},
		"the author of the pull request": {func(gh *ghShim, t *testing.T) {
			gh.env = append(gh.env, "CLAUDE_SHIM_AUTHOR_SLEEP=600")
		}, (*ghShim).authorSessions, stagePR, ""},
		"an address-reviews session": {func(gh *ghShim, t *testing.T) {
			gh.ciReads(t, "acme/edge-sensors", claimedIssue, ciPull{
				reviews: []map[string]any{objectionOf(t, gh, 9001, "maintainer", "Back off between the retries.")}})
			gh.env = append(gh.env, "CLAUDE_SHIM_THEN_SLEEP=600")
		}, (*ghShim).workers, stageAddressReviews, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := panelClaim(t, "@true")
			c.prepare(gh, t)
			f := launchBinary(t, hurried, ciConfig(data, nil), gh.env)
			run := f.ended(t, 1)
			want := c.reason + "the session of the stage " + c.stage + " ran past its timeout of " + hurriedTimeout
			if run.Outcome != outcomeFailed || !strings.Contains(run.Reason, want) || run.Stage != c.stage {
				t.Fatalf("the run ended as %q in the stage %q (%s), want failed with %q; the factory's log:\n%s",
					run.Outcome, run.Stage, run.Reason, want, f.output(t))
			}
			if !loggedEnding(run) {
				t.Errorf("the log of the run does not carry its ending %q", run.Reason)
			}
			sessions := c.sessions(gh, t)
			if len(sessions) == 0 {
				t.Fatalf("the shim recorded no session of the stage %s", c.stage)
			}
			// The session that timed out is the last one of its kind the factory started.
			last := sessions[len(sessions)-1]
			if last.pgid == 0 || groupSurvived(last.pgid) {
				t.Errorf("the process group %d of the session that ran past its timeout is still there", last.pgid)
			}
			if pulls := gh.opened(t, "acme/edge-sensors"); c.stage != stageAddressReviews && len(pulls) != 0 {
				t.Errorf("the factory opened %d pull requests after a session that ran past its timeout", len(pulls))
			}
		})
	}
}

// Taking the routing label off the issue while the reviewers run, or while the author of the pull
// request does, ends the running sessions with their process groups; the run is cancelled and the
// issue is given back with the work pushed.
func TestACancelDuringTheReviewOrThePRStageEndsItsSessions(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		prepare  func(*ghShim, *testing.T)
		sessions func(*ghShim, *testing.T) []workerStart
		running  int
		stage    string
	}{
		"the review": {func(gh *ghShim, t *testing.T) {
			for _, reviewer := range []string{"code", "security", "docs", "tests", "senior"} {
				gh.verdict(t, reviewer, 1, "SLEEP 600\n"+findings(t))
			}
		}, (*ghShim).reviewerSessions, 5, stageReview},
		"the pr stage": {func(gh *ghShim, t *testing.T) {
			gh.env = append(gh.env, "CLAUDE_SHIM_AUTHOR_SLEEP=600")
		}, (*ghShim).authorSessions, 1, stagePR},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := panelClaim(t, "@true")
			gh.unassigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
			c.prepare(gh, t)
			f := gh.work(t, config{"poll": "50ms", "deadline": "10m", "data_dir": data, "repositories": []string{"acme/edge-sensors"}})
			var running []workerStart
			f.eventually(t, 60*time.Second, "the sessions of the stage "+c.stage+" to start", func() bool {
				running = c.sessions(gh, t)
				if len(running) < c.running {
					return false
				}
				for _, s := range running {
					if s.pgid == 0 {
						return false
					}
				}
				return true
			})
			var going apiRun
			f.get(t, "/api/runs/1", &going)
			work := committed(t, f, going.Worktree, "worked.md")

			gh.issue(t, "acme/edge-sensors", assignedTo(
				openIssue(claimedIssue, claimedTitle, time.Now().UTC().Add(-72*time.Hour), readyLabel), "factory-bot"))

			run := f.ended(t, 1)
			if run.Outcome != outcomeCancelled || !strings.Contains(run.Reason, "routing label") || run.Stage != c.stage {
				t.Fatalf("the run ended as %q in the stage %q (%s), want cancelled by the label in %s; the factory's log:\n%s",
					run.Outcome, run.Stage, run.Reason, c.stage, f.output(t))
			}
			if !loggedEnding(run) {
				t.Errorf("the log of the run does not carry its ending %q", run.Reason)
			}
			for _, s := range running {
				if groupSurvived(s.pgid) {
					t.Errorf("the process group %d of a session of the stage %s is still there after the cancel", s.pgid, c.stage)
				}
			}
			f.eventually(t, 30*time.Second, "the issue to be let go", func() bool {
				var let apiRun
				f.get(t, "/api/runs/1", &let)
				return let.LetGoAt != nil
			})
			if head := gh.head(t, "acme/edge-sensors", claimedBranch); head != work {
				t.Errorf("%s of the remote is at %q, want the commit of the cancelled run (%s): a worktree is pushed before it goes", claimedBranch, head, work)
			}
			if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 0 {
				t.Errorf("the factory opened %d pull requests for a cancelled run", len(pulls))
			}
		})
	}
}

// Two classes that both cover every changed file: the first in the configuration's list is the class
// of the change, recorded on the run and served with it.
func TestTheFirstOfTwoClassesThatCoverTheChangeIsItsClass(t *testing.T) {
	t.Parallel()
	gh, data := panelClaim(t, "@echo make check ran")
	gh.workerCommits(t, "docs/worked.md")
	notes := map[string]any{"name": "notes", "paths": []string{"docs/**"}, "gate": []string{"sh", "-c", "echo the notes gate ran"}, "reviewers": []string{"senior"}}
	f := gh.work(t, classedConfig(data, notes, docsClass([]string{"sh", "-c", "echo the docs gate ran"})))
	run := f.ended(t, 1)
	if run.Outcome != outcomeReady {
		t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
	}
	if got := classesOf(run); !equal(got, []string{"notes/gate", "notes/review"}) {
		t.Errorf("the run served the classes %v, want notes for the gate and the review", got)
	}
	if got := agents(t, gh); !equal(got, []string{"senior"}) {
		t.Errorf("the factory started the reviewers %v, want the senior reviewer of the class notes", got)
	}
	var stored Run
	read(t, filepath.Join(data, "run-1.json"), &stored)
	if stored.Panel == nil || len(stored.Panel.Classes) == 0 || stored.Panel.Classes[0].Class != "notes" {
		t.Errorf("the record of the run carries the classes %+v, want notes first", stored.Panel)
	}
	if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 1 || !strings.Contains(pulls[0].Body, "change_class: notes for the gate at ") {
		t.Errorf("the factory opened %+v, want one pull request that names the class notes", pulls)
	}
}
