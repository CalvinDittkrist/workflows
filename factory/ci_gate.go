package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The gate on CI: a gate command of "ci", or {"ci": [checks]}, hands the gate of a change class to
// GitHub CI instead of running it in the worktree, so a slow host builds nothing and the change is
// visible on GitHub while the run works on it. GitHub runs a workflow on a pull request and not on a
// pushed branch, so the first gate on CI of a run opens a draft pull request, which the pr stage later
// finishes: the author session's title and body, the verification section, and ready for review. The
// factory pushes only at a gate, so one round of the gate is one run of CI.
//
// A pull request is the run's only by the key: its number is the one the issue's runs recorded, its
// head is the claimed branch, and its author is the login this host's gh is logged in as. The draft
// state is never read: a person who marks the draft ready changes nothing about where the run stands.

// ciGate is one run of a gate on CI: the branch pushed, the run's draft opened when it has no pull
// request yet (draft), and the pull request read until the checks of the pushed head have ended.
// Mergeability is read first, as the ci stage reads it, because GitHub runs no workflow for a branch
// that does not merge: the base is merged (mergedBase) and pushed. The checks read are every check of
// the head, or the named ones; a bot review is none. A named check that has not appeared once the checks
// grace (ci.checks_grace) has passed since the push, or a head without any check by then, ends the run
// blocked naming it, so a typo never reads as a pass. A gate that reads every check passes only on two
// readings a poll apart that show the same checks, all passed, because GitHub registers the checks of a
// head one workflow at a time and a quick one can end before a slow one is there. Checks still running
// are waited for within the run's deadline. The result is a gate run like one in the worktree, with
// the failed logs of the checks that failed as its tail.
func (f *Factory) ciGate(parent, ctx context.Context, r *Run, entry Entry, claim claimed, panel *Panel, classed Classed, stage string) gateRun {
	head, ok := f.gatePushed(parent, ctx, r, claim, *panel)
	if !ok {
		return gateRun{ended: true}
	}
	pull, ok := f.draft(parent, ctx, r, entry, claim)
	if !ok {
		return gateRun{ended: true}
	}
	knobs := f.ciFor(entry.Repository)
	held := Held{Repository: entry.Repository, Number: entry.Number, Branch: claim.branch, PullRequest: pull}
	began, pushedAt := time.Now(), time.Now()
	f.runs.event(r, Event{Kind: "factory", Title: "waiting on CI for the gate on " + pull,
		Body: fmt.Sprintf("the checks %s of %s", whichChecks(classed.Gate), short(head))})
	said, seen := "", ""
	for {
		if ctx.Err() != nil {
			return gateRun{} // the caller ends the run the way its context ended
		}
		read, err := f.source.pullChecks(ctx, held)
		verdict, checks, missing := ciWaiting, []check(nil), []string(nil)
		var foreign notOurs
		switch {
		case errors.As(err, &foreign):
			f.finish(r, outcomeFailed, foreign.Error(), nil)
			return gateRun{ended: true}
		case err != nil:
			if ctx.Err() == nil {
				f.warn(r, "CI not read", "the pull request "+pull+" could not be read while the gate waited on CI: "+err.Error()+"; it is read again on the next poll")
			}
		case read.Mergeable == "CONFLICTING" && read.Head == head:
			f.runs.event(r, Event{Kind: "factory", Title: "the pull request conflicts with " + claim.base,
				Body: "GitHub runs no workflow on a branch that does not merge, so the base is merged before the gate goes on"})
			if !f.mergedBase(parent, ctx, r, entry, claim, panel, stage) {
				return gateRun{ended: true}
			}
			if head, ok = f.gatePushed(parent, ctx, r, claim, *panel); !ok {
				return gateRun{ended: true}
			}
			// The merged head is read after a poll like any push, so a mergeability GitHub has not
			// updated yet costs a poll, not a loop of pushes.
			pushedAt, seen = time.Now(), ""
		case !f.fake && read.Head != head, read.Mergeable != "MERGEABLE":
			// The push has not reached the pull request yet, or GitHub is still trying the merge.
		default:
			verdict, checks, missing = judgeGate(read.Checks, classed.Gate.Checks, time.Since(pushedAt) >= knobs.ChecksGrace)
			if verdict == ciGreen && len(classed.Gate.Checks) == 0 {
				// Every check: a pass stands once a second reading shows the same checks.
				names := read.Head + " " + named(checks)
				if names != seen {
					verdict, seen = ciWaiting, names
				}
			}
		}
		if verdict != said {
			f.runs.event(r, Event{Kind: "factory", Title: "gate on CI: " + verdict, Body: summarise(read)})
			said = verdict
		}
		switch verdict {
		case gateMissing:
			f.runs.update(r, func() { r.Reason = missingReason(pull, head, classed.Gate, missing, knobs.ChecksGrace) })
			f.finish(r, outcomeBlocked, "", nil)
			return gateRun{ended: true}
		case ciGreen, ciFailed:
			return f.ciGateRun(ctx, entry, classed, head, checks, int(time.Since(began).Seconds()))
		}
		select {
		case <-ctx.Done():
		case <-time.After(f.settings.Poll):
		}
	}
}

// gateMissing is the verdict on a head that lacks a check the gate reads once the checks grace has
// passed: a named check that has not appeared, or any check at all.
const gateMissing = "missing"

// judgeGate makes the verdict of one reading of the checks a gate on CI reads: names are the checks it
// reads, every check when there are none, and a check that runs under one name in several workflows is
// read in each. It answers with the checks read and the names missing, which "any check" stands for
// when the head has none.
func judgeGate(all []check, names []string, graceOver bool) (string, []check, []string) {
	read, missing := all, []string{}
	if len(names) > 0 {
		read = []check{}
		for _, name := range names {
			found := false
			for _, c := range all {
				if c.Name == name {
					read, found = append(read, c), true
				}
			}
			if !found {
				missing = append(missing, name)
			}
		}
	} else if len(all) == 0 {
		missing = []string{"any check"}
	}
	switch {
	case slices.ContainsFunc(read, func(c check) bool { return c.State == checkPending }):
		return ciWaiting, read, missing
	case len(missing) > 0 && graceOver:
		return gateMissing, read, missing
	case len(missing) > 0:
		return ciWaiting, read, missing
	case slices.ContainsFunc(read, func(c check) bool { return c.State == checkFail }):
		return ciFailed, read, missing
	}
	return ciGreen, read, missing
}

// ciGateRun is the gate run the checks of a head make once they have ended: a pass when every check
// read passed, and a failure with the failed logs of the checks that failed.
func (f *Factory) ciGateRun(ctx context.Context, entry Entry, classed Classed, head string, checks []check, seconds int) gateRun {
	states := make([]string, 0, len(checks))
	failing := []check{}
	for _, c := range checks {
		states = append(states, c.Name+"="+c.State)
		if c.State == checkFail {
			failing = append(failing, c)
		}
	}
	ran := gateRun{passed: len(failing) == 0, head: head, seconds: seconds, checks: checks, tail: named(checks)}
	status := fmt.Sprintf("pass (%d checks on CI)", len(checks))
	if !ran.passed {
		ran.exit = 1
		status = fmt.Sprintf("fail (%d of %d checks on CI)", len(failing), len(checks))
		ran.tail = "The checks that failed:\n" + named(failing) + "\n\nTheir failed logs:\n" + f.source.failedLogs(ctx, entry.Repository, failing)
	}
	ran.result = gateResult(status, head, classed, seconds) + "\ngate_checks: " + strings.Join(states, ", ")
	return ran
}

// whichChecks says which checks a gate on CI reads, for the event that says what it waits for.
func whichChecks(gate gateCommand) string {
	if len(gate.Checks) == 0 {
		return "every check"
	}
	return strings.Join(gate.Checks, ", ")
}

// missingReason is why a gate on CI ended the run blocked: what it did not find, on which head, and
// where the fix goes.
func missingReason(pull, head string, gate gateCommand, missing []string, grace time.Duration) string {
	if len(gate.Checks) == 0 {
		return fmt.Sprintf("the gate on CI read no check on %s of %s within the checks grace of %s (ci.checks_grace) after the push, so there is nothing that passed: "+
			"a gate on CI needs a workflow that runs on pull requests, or a gate command that runs in the worktree", short(head), pull, grace)
	}
	return fmt.Sprintf("the gate on CI names the check(s) %s, which %s did not show on %s within the checks grace of %s (ci.checks_grace) after the push: "+
		"name the checks in the gate command as the pull request shows them", strings.Join(missing, ", "), pull, short(head), grace)
}

// gatePushed pushes the branch for a gate on CI, and answers with the commit its checks are read on,
// which in fake mode is the one fakeHead counts.
func (f *Factory) gatePushed(parent, ctx context.Context, r *Run, claim claimed, panel Panel) (string, bool) {
	head, ok := f.pushed(parent, ctx, r, claim, "the branch")
	if f.fake {
		head = fakeHead(panel)
	}
	return head, ok
}

// draft is the pull request a gate on CI reads, which is the run's own: the one it holds already
// (settlePull), or a draft it opens now against the base the branch was cut from, with the body
// Closes #N and nothing else. It answers false once it has ended the run.
func (f *Factory) draft(parent, ctx context.Context, r *Run, entry Entry, claim claimed) (string, bool) {
	if _, ok := f.settlePull(parent, ctx, r, entry, claim); !ok {
		return "", false
	}
	if r.PullRequest != "" {
		return r.PullRequest, true
	}
	title := strings.TrimSpace(firstLine(entry.Title))
	if title == "" {
		title = "Issue #" + strconv.Itoa(entry.Number)
	}
	pull, err := f.source.createPull(ctx, entry.Repository, newPull{Title: cut(title, maxTitle), Head: claim.branch, Base: claim.base,
		Body: fmt.Sprintf("Closes #%d", entry.Number), Draft: true, issue: entry.Number})
	if err != nil {
		if !f.halted(parent, ctx, r, "opened the draft pull request") {
			f.finish(r, outcomeFailed, "the draft pull request the gate on CI runs on could not be opened: "+err.Error()+"; the branch "+claim.branch+" is pushed", nil)
		}
		return "", false
	}
	f.runs.update(r, func() { r.PullRequest, r.Draft = pull, true })
	f.runs.event(r, Event{Kind: "factory", Title: "opened the draft " + pull,
		Body: "the gate runs on CI, which runs on pull requests; the pr stage writes the title and the body and marks it ready for review"})
	return pull, true
}

// settlePull settles once per run which pull request open on the claimed branch is the run's, by the
// key: the number the issue's runs recorded (Entry.pull), the claimed branch as its head, and this
// host's login as its author. The run takes the one that is its own on, with whether it is still the
// gate's draft as the record says. A pull request on the branch that is not the run's ends the run
// blocked (foreignPull). It answers whether the pull requests could be read, and false for ok once it
// has ended the run.
func (f *Factory) settlePull(parent, ctx context.Context, r *Run, entry Entry, claim claimed) (known, ok bool) {
	if r.PullRequest != "" || claim.branch == "" {
		return true, true
	}
	pulls, err := f.source.openPulls(ctx, entry.Repository, claim.branch)
	login := ""
	if err == nil && len(pulls) > 0 {
		login, err = f.login(ctx)
	}
	if err != nil {
		if f.halted(parent, ctx, r, "read the pull requests of its branch") {
			return false, false
		}
		f.warn(r, "pull requests not read", "which pull requests "+claim.branch+" has open could not be read: "+err.Error())
		return false, true
	}
	ours := ""
	foreign := []string{}
	for _, p := range pulls {
		if entry.pull != "" && pullOf(p.URL) == pullOf(entry.pull) && strings.EqualFold(p.Author, login) {
			ours = p.URL
			continue
		}
		foreign = append(foreign, fmt.Sprintf("%s, opened by %s", p.URL, p.Author))
	}
	if len(foreign) > 0 {
		f.foreignPull(ctx, r, entry, claim, foreign)
		return true, false
	}
	switch {
	case ours != "":
		f.runs.update(r, func() { r.PullRequest, r.Draft = ours, entry.drafted })
		if entry.drafted {
			f.runs.event(r, Event{Kind: "factory", Title: "going on with the draft " + ours,
				Body: "the gate on CI of a run before opened it, and the pr stage has not finished it yet; the stage the run goes on at is read from the record and the branch"})
		}
	case entry.pull != "":
		f.runs.event(r, Event{Kind: "factory", Title: "the recorded pull request is not open",
			Body: "the runs of this issue recorded " + entry.pull + ", which is not open on " + claim.branch + " any more"})
	}
	return true, true
}

// foreignPull ends the run blocked on the pull requests of its branch that are not its own, because an
// issue gets one pull request, and says so on the issue, which the ending's notification does when
// the host has logins to notify.
func (f *Factory) foreignPull(ctx context.Context, r *Run, entry Entry, claim claimed, foreign []string) {
	reason := fmt.Sprintf("the branch %s has a pull request open that is not this run's:\n%s\n\n"+
		"A pull request is the run's only when the factory recorded its number, it is of the claimed branch and the factory's login opened it. "+
		"An issue gets one pull request, so the run stops here for a person to decide which one stands.", claim.branch, bullets(foreign))
	f.runs.update(r, func() { r.Reason = reason })
	if !f.notifying() {
		body := fmt.Sprintf("The factory stopped run %d of this issue: %s", r.ID, reason)
		if err := f.source.commentOnIssue(ctx, entry.Repository, entry.Number, body); err != nil && ctx.Err() == nil {
			f.warn(r, "issue not commented on", "the pull request that is not this run's could not be named on the issue: "+err.Error())
		}
	}
	f.finish(r, outcomeBlocked, "", nil)
}

// branchPull is one open pull request of a branch: where it is and who opened it.
type branchPull struct {
	URL    string
	Author string
}

// openPulls is the open pull requests of the branch a run holds the issue by, from the repository
// itself: a pull request from a fork is of another branch.
func (g *gitHub) openPulls(ctx context.Context, repository, branch string) ([]branchPull, error) {
	owner, _, _ := strings.Cut(repository, "/")
	raw, err := gh(ctx, "api", "repos/"+repository+"/pulls?state=open&head="+url.QueryEscape(owner+":"+branch)+"&per_page=10")
	if err != nil {
		return nil, err
	}
	var pulls []struct {
		Number int    `json:"number"`
		Head   ghHead `json:"head"`
		User   struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal(raw, &pulls); err != nil {
		return nil, fmt.Errorf("the answer is no list of pull requests: %w", err)
	}
	out := []branchPull{}
	for _, p := range pulls {
		if (ghPull{Head: p.Head}).of(repository, branch) && p.Number > 0 {
			out = append(out, branchPull{URL: "https://github.com/" + repository + "/pull/" + strconv.Itoa(p.Number), Author: p.User.Login})
		}
	}
	return out, nil
}

// finishPull makes the gate's draft the pull request the pr stage opens: the title and the body
// replaced, and marked ready for review, which gh answers alike for a pull request a person marked
// ready already.
func (g *gitHub) finishPull(ctx context.Context, repository string, pull int, title, body string) error {
	edit, err := json.Marshal(map[string]string{"title": title, "body": body})
	if err != nil {
		return err
	}
	if _, err := ghInput(ctx, ghTimeout, string(edit)+"\n", "api", "--method", "PATCH", pullRequestRequest(repository, pull), "--input", "-"); err != nil {
		return fmt.Errorf("the title and the body could not be written: %w", err)
	}
	if _, err := gh(ctx, "pr", "ready", strconv.Itoa(pull), "--repo", repository); err != nil {
		return fmt.Errorf("the pull request could not be marked ready for review: %w", err)
	}
	return nil
}

// commentOnIssue writes one comment on an issue, its body on standard input.
func (g *gitHub) commentOnIssue(ctx context.Context, repository string, issue int, body string) error {
	_, err := ghInput(ctx, ghTimeout, body, "issue", "comment", strconv.Itoa(issue), "--repo", repository, "--body-file", "-")
	return err
}
