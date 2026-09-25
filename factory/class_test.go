package main

import (
	"slices"
	"strings"
	"testing"
)

// Change classes ([ADR 0041]): the factory determines the class of a change from the files it changed
// before the review, whose reviewers the class decides, and again on the final head, whose gate it
// decides. These tests run the real binary on a repository whose make check says it ran, and whose work
// session commits a file under docs/.
//
// [ADR 0041]: ../docs/adr/0041-a-change-class-decides-the-gate-and-the-reviewers-before-the-pull-request.md

// docsClass is a class of the files under docs/ with the given gate, reviewed by docs and senior.
func docsClass(gate []string) map[string]any {
	return map[string]any{"name": "docs", "paths": []string{"docs/**", "*.md"}, "gate": gate, "reviewers": []string{"docs", "senior"}}
}

// classedConfig is ciConfig with these change classes on the repository.
func classedConfig(data string, classes ...map[string]any) config {
	c := ciConfig(data, nil)
	c["repositories"] = []map[string]any{{"name": "acme/edge-sensors", "review": map[string]any{"classes": classes}}}
	return c
}

// agents is the agents the reviewer sessions were started as, in the order they were started, by the
// name the panel knows them by.
func agents(t *testing.T, gh *ghShim) []string {
	t.Helper()
	out := []string{}
	for _, r := range gh.reviewerSessions(t) {
		agent, _ := agentOf(t, r)
		out = append(out, strings.TrimSuffix(agent, "-reviewer"))
	}
	return out
}

// classesOf is the determinations a run recorded, as class/for pairs.
func classesOf(run apiRun) []string {
	out := []string{}
	if run.Panel != nil {
		for _, c := range run.Panel.Classes {
			out = append(out, c.Class+"/"+c.For)
		}
	}
	return out
}

// A change whose files all fall under a class runs that class's reviewers, and the gate on the final
// head is the class's command, run in the worktree without a shell of its own. When a fix adds a file
// outside the class, the class of the final head is determined again, and it is full: make check, and
// the record and the pull request carry both determinations.
func TestAChangeClassDecidesTheReviewersAndTheGateOnTheFinalHead(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		fix       string // the file the fix session commits
		classes   []string
		gate      string // what the gate on the final head printed
		gateClass string
	}{
		"a fix within the class":  {"docs/worked.md", []string{"docs/gate", "docs/review", "docs/gate"}, "the docs gate ran", "docs"},
		"a fix outside the class": {"upload/retry.go", []string{"docs/gate", "docs/review", "full/gate"}, "make check ran", "full"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, data := panelClaim(t, "@echo make check ran")
			gh.workerCommits(t, "docs/worked.md")
			gh.env = append(gh.env, "CLAUDE_SHIM_THEN_COMMIT="+c.fix)
			gh.verdict(t, "docs", 1, findings(t, Finding{Severity: "S2", Path: "docs/worked.md", Line: 1, Claim: "The heading is wrong.", Why: "It names the old tool.", Fix: "Rename it."}))
			gh.repairs(t, map[string]any{"outcome": "complete", "fixed": []string{"F1"}, "disputed": []map[string]any{}, "skipped": []map[string]any{}, "summary": "Fixed."})
			f := gh.work(t, classedConfig(data, docsClass([]string{"sh", "-c", "echo the docs gate ran"})))
			run := f.ended(t, 1)
			if run.Outcome != outcomeReady {
				t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
			}
			// The class's reviewers in the first round, and the one that asked for a fix in the second.
			if got := agents(t, gh); !equal(slices.Sorted(slices.Values(got[:min(2, len(got))])), []string{"docs", "senior"}) || len(got) != 3 || got[2] != "docs" {
				t.Errorf("the factory started the reviewers %v, want docs and senior, then docs", got)
			}
			if got := classesOf(run); !equal(got, c.classes) {
				t.Errorf("the run recorded the classes %v, want %v", got, c.classes)
			}
			head := gh.head(t, "acme/edge-sensors", claimedBranch)
			ran := strings.Join(factoryBodies(run, "gate_result: pass (exit 0) at "+short(head)), "\n")
			if !strings.Contains(ran, c.gate) {
				t.Errorf("the gate on the final head %s printed %q, want %q", short(head), ran, c.gate)
			}
			pulls := gh.opened(t, "acme/edge-sensors")
			if len(pulls) != 1 {
				t.Fatalf("the factory opened %d pull requests, want one", len(pulls))
			}
			for _, want := range []string{"gate_class: " + c.gateClass, "change_class: docs for the gate at ", ", docs for the review at ", ", " + c.gateClass + " for the gate at " + short(head),
				"panel: docs=FIX→PASS senior=PASS\n"} {
				if !strings.Contains(pulls[0].Body, want) {
					t.Errorf("the body does not carry %q:\n%s", want, pulls[0].Body)
				}
			}
			if c.gateClass == classFull {
				why := strings.Join(factoryBodies(run, "change class full for the gate"), "\n")
				if !strings.Contains(why, "upload/retry.go is outside every class") {
					t.Errorf("the factory said the class full applied because %q, want the file outside every class named", why)
				}
				// The class full asks the repository's whole panel, not the reviewers of the review's class.
				if classed, ok := classedFor(*run.Panel, classForGate); !ok || !equal(classed.Reviewers, defaultReview.Reviewers) {
					t.Errorf("the run recorded the class of the gate %+v, want the reviewers %v", classed, defaultReview.Reviewers)
				}
			}
		})
	}
}

// A class with an empty gate command runs no gate, in the gate stage or on the final head, and the run
// and the pull request say so; a change with a file outside every class is the class full, reviewed by the whole panel.
func TestAClassWithoutAGateRunsNoneAndAChangeOutsideEveryClassIsFull(t *testing.T) {
	t.Parallel()
	t.Run("no gate", func(t *testing.T) {
		t.Parallel()
		gh, data := panelClaim(t, "@echo make check ran, which it should not have; exit 1")
		gh.workerCommits(t, "docs/worked.md")
		gh.verdict(t, "senior", 1, findings(t, Finding{Severity: "S2", Path: "docs/worked.md", Line: 1, Claim: "Unclear.", Why: "It hedges.", Fix: "Say it."}))
		gh.repairs(t, map[string]any{"outcome": "complete", "fixed": []string{"F1"}, "disputed": []map[string]any{}, "skipped": []map[string]any{}, "summary": "Fixed."})
		f := gh.work(t, classedConfig(data, docsClass([]string{})))
		run := f.ended(t, 1)
		if run.Outcome != outcomeReady {
			t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
		}
		head, worked := gh.head(t, "acme/edge-sensors", claimedBranch), run.Panel.Rounds[0].Head
		none := "gate_result: none (the change class docs has no gate) at " + short(head)
		if titles := factoryTitles(run, "gate_result: "); !equal(titles, []string{"gate_result: none (the change class docs has no gate) at " + short(worked), none}) {
			t.Errorf("the factory logged the gates %v, want none run on %s or on %s", titles, short(worked), short(head))
		}
		if run.Panel == nil || !strings.HasPrefix(run.Panel.Gate, none) || !equal(classesOf(run), []string{"docs/gate", "docs/review", "docs/gate"}) {
			t.Errorf("the run recorded the panel %+v, want no gate on the final head of the class docs", run.Panel)
		}
		if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 1 || !strings.Contains(pulls[0].Body, none+"\ngate_command: none\ngate_class: docs") {
			t.Errorf("the factory opened %+v, want one pull request that says no gate ran", pulls)
		}
	})
	// The class full is the change no class vouches for: all five reviewers read it, however narrow the
	// repository's own panel is.
	t.Run("a narrowed panel", func(t *testing.T) {
		t.Parallel()
		gh, data := panelClaim(t, "@echo make check ran")
		gh.workerCommits(t, "upload/retry.go")
		c := classedConfig(data, docsClass([]string{}))
		c["repositories"].([]map[string]any)[0]["review"].(map[string]any)["reviewers"] = []string{"docs"}
		f := gh.work(t, c)
		run := f.ended(t, 1)
		if run.Outcome != outcomeReady {
			t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
		}
		if got := agents(t, gh); !equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(defaultReview.Reviewers))) {
			t.Errorf("the factory started the reviewers %v for the class full, want all five", got)
		}
	})
	// The patterns are read part by part: *.md is a Markdown file at the root, docs/** anything under
	// docs, however deep.
	for file, want := range map[string]string{"upload/retry.go": classFull, "notes/todo.md": classFull, "README.md": "docs", "docs/api/v2/index.md": "docs"} {
		t.Run("a change of "+file, func(t *testing.T) {
			t.Parallel()
			gh, data := panelClaim(t, "@echo make check ran")
			gh.workerCommits(t, file)
			f := gh.work(t, classedConfig(data, docsClass([]string{})))
			run := f.ended(t, 1)
			if run.Outcome != outcomeReady {
				t.Fatalf("the run ended as %q (%s), want ready; the factory's log:\n%s", run.Outcome, run.Reason, f.output(t))
			}
			panel := []string{"docs", "senior"}
			if want == classFull {
				panel = defaultReview.Reviewers
			}
			if got := agents(t, gh); !equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(panel))) {
				t.Errorf("the factory started the reviewers %v, want %v", got, panel)
			}
			// The branch did not move, so no gate ran again and the class was determined once for the gate
			// stage and once for the review.
			if got := classesOf(run); !equal(got, []string{want + "/gate", want + "/review"}) {
				t.Errorf("the run recorded the classes %v, want %s for the gate and the review alone", got, want)
			}
			if want == classFull {
				classed := run.Panel.Classes[1]
				if !equal(classed.Gate, fullGate) || !strings.Contains(classed.Why, file+" is outside every class") {
					t.Errorf("the run recorded the class %+v, want make check and the file outside every class", classed)
				}
			}
			if pulls := gh.opened(t, "acme/edge-sensors"); len(pulls) != 1 || !strings.Contains(pulls[0].Body, "change_class: "+want+" for the gate at ") || !strings.Contains(pulls[0].Body, ", "+want+" for the review at ") {
				t.Errorf("the factory opened %+v, want one pull request with the class %s", pulls, want)
			}
		})
	}
}
