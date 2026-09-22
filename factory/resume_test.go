package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Resuming work the factory holds, tested the way the rest of it is: the real binary, against the gh
// shim and a local bare repository that stands in for the remote. The release signal is a gesture on
// GitHub — somebody takes the assignee off — so it is driven through that shim, and what the factory
// answers with is read from the run records, from how the worker was started and from the line it
// serves.

// TestAReleasedIssueIsAssignedAgainAndResumedInTheSameWorktree drives the whole release signal: an
// issue this factory worked and still holds is handed back by removing its assignee, and the factory
// takes it up again where it left off ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func TestAReleasedIssueIsAssignedAgainAndResumedInTheSameWorktree(t *testing.T) {
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", claimedIssue, claimedTitle)
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", claimedIssue, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", claimedIssue)

	data := filepath.Join(t.TempDir(), "data")
	clone := gh.cloneInto(t, data, "acme/edge-sensors")
	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	first := f.ended(t, 1)
	if first.Outcome != "ready" {
		t.Fatalf("the first run ended as %q (%s); the factory's log:\n%s", first.Outcome, first.Reason, f.output(t))
	}
	worktree := filepath.Join(clone, ".claude", "worktrees", "feat-104-retry-the-upload-when-the-broker-drops")
	if first.Worktree != worktree || !first.Holding {
		t.Fatalf("the first run records the worktree %q (holding=%v), want %q and a held issue",
			first.Worktree, first.Holding, worktree)
	}
	// What that run left in the worktree, which is what a resumed run continues on.
	writeFile(t, filepath.Join(worktree, "fix.txt"), "the work of the first session\n")
	gh.git(t, worktree, "add", "fix.txt")
	gh.git(t, worktree, "commit", "-q", "-m", "fix: the first session's commit")
	committed := gh.git(t, worktree, "rev-parse", "HEAD")

	// The release: somebody takes the assignee off the issue, so it matches the routing rule again.
	// The issue is touched by that, which is what makes the factory read its events anew — so the
	// events are put there first: an issue that says it was touched while its timeline is still the
	// old one would be remembered without the release in it.
	released := first.EndedAt.Add(time.Second)
	gh.timeline(t, "acme/edge-sensors", claimedIssue,
		labeled("factory", first.StartedAt.Add(-6*time.Hour)),
		assigned("factory-bot", first.StartedAt), unassigned("factory-bot", released))
	gh.issues(t, "acme/edge-sensors",
		touched(openIssue(claimedIssue, claimedTitle, first.StartedAt.Add(-72*time.Hour)), released))

	second := f.ended(t, 2)
	if second.Signal != "release" || second.Issue != claimedIssue {
		t.Fatalf("run 2 works #%d on the signal %q, want #%d on a release; the factory's log:\n%s",
			second.Issue, second.Signal, claimedIssue, f.output(t))
	}
	if second.Branch != claimedBranch || second.Worktree != first.Worktree || !second.Holding {
		t.Errorf("the resumed run is %s in %s (holding=%v), want the branch and the worktree of the claim (%s in %s)",
			second.Branch, second.Worktree, second.Holding, first.Branch, first.Worktree)
	}
	// The factory assigns itself again before it starts the worker, so nothing else takes the issue
	// while the resumed session runs.
	assigned := fmt.Sprintf("issue edit %d --repo acme/edge-sensors --add-assignee factory-bot", claimedIssue)
	if made := gh.made(t, assigned); made != 2 {
		t.Errorf("the factory made `gh %s` %d times, want twice: once for the claim and once for the release", assigned, made)
	}
	// And it claims nothing: the branch that holds the issue is the one it created the first time.
	if made := gh.asked(t, "api --method POST repos/acme/edge-sensors/git/refs"); made != 1 {
		t.Errorf("the factory created a reference %d times, want once: a resumed run is under the claim that stands", made)
	}

	// The release is answered once and no poll after it queues anything: the issue stays unassigned
	// on GitHub as far as the shim's canned line is concerned, so a factory that read the removal
	// anew would start a third run on it.
	f.never(t, 3*time.Second, "the factory started a third run, so it answered the one release twice",
		func() bool { return !f.missing(t, 3) })
	var line apiLine
	f.get(t, "/api/line", &line)
	if len(line.Queue) != 0 || len(line.Now) != 0 {
		t.Errorf("the factory queues %v and runs %d after the release, want an idle line", keys(line.Queue), len(line.Now))
	}

	// The session itself: the same worktree, and on the commit the work there had reached.
	workers := gh.workers(t)
	if len(workers) != 2 {
		t.Fatalf("the factory started %d workers, want one per run", len(workers))
	}
	resumedWorker := workers[1]
	if resumedWorker.cwd != resolved(t, worktree) {
		t.Errorf("the resumed worker ran in %s, want the worktree of the claim %s", resumedWorker.cwd, resolved(t, worktree))
	}
	if resumedWorker.branch != claimedBranch || resumedWorker.head != committed {
		t.Errorf("the resumed worker ran on %s at %s, want %s at the commit the worktree holds (%s)",
			resumedWorker.branch, resumedWorker.head, claimedBranch, committed)
	}
	if !resumedWorker.started("-p", "/worker:work") {
		t.Errorf("the resumed worker was started as %v, want the same session a claim starts", resumedWorker.args)
	}
}

// What the line is made of after a factory has worked a while: the runs it holds and resumes stand
// before the issues nobody has worked yet, and the outcomes that wait for a person are in neither.
// The records are written into the data directory the factory then starts on, which is the restart
// path itself: a factory that was stopped knows what it holds from those files alone.
func TestTheLineResumesWhatTheFactoryHoldsBeforeItClaimsAnythingNew(t *testing.T) {
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	opened := time.Now().UTC().Add(-72 * time.Hour)
	began := time.Now().UTC().Add(-2 * time.Hour)
	interruptedAt := began.Add(30 * time.Minute)
	releasedAt := began.Add(50 * time.Minute)
	foreignAt := began.Add(70 * time.Minute) // the newest signal of all, and the one that is ignored

	// 104 was interrupted and has its one automatic resume; 109 is blocked and waits for a person;
	// 112 was worked, is held, and has just been released; 115 is another claimer's, so nothing about
	// it is this factory's to resume; 121 is new.
	records(t, data,
		record(1, 104, "Retry the upload", signalRouted, outcomeInterrupted, true, began, interruptedAt),
		record(2, 109, "Warn on an old calibration", signalRouted, outcomeBlocked, true, began, began.Add(10*time.Minute)),
		record(3, 112, "Replace the CSV parser", signalRouted, outcomeReady, true, began, began.Add(20*time.Minute)),
		record(4, 115, "Serve the preview", signalRouted, outcomeLost, false, began, began.Add(25*time.Minute)),
		// And 130 is held in a repository this host is no longer connected to. A repository the
		// configuration does not name is not worked, whatever the records of it say it holds.
		in("acme/backtest", record(5, 130, "Cache the fills", signalRouted, outcomeInterrupted, true, began, began.Add(40*time.Minute))),
	)
	// What GitHub says: every issue the factory does not hold is routed and unassigned. 109 is still
	// assigned to the factory, so it is not in the list at all.
	gh.issues(t, "acme/edge-sensors",
		touched(openIssue(112, "Replace the CSV parser", opened), releasedAt),
		touched(openIssue(115, "Serve the preview", opened), foreignAt),
		openIssue(121, "Document the calibration procedure", opened))
	gh.timeline(t, "acme/edge-sensors", 112, labeled("factory", opened),
		assigned("factory-bot", began), unassigned("factory-bot", releasedAt))
	gh.timeline(t, "acme/edge-sensors", 115, labeled("factory", opened), unassigned("somebody", foreignAt))
	gh.timeline(t, "acme/edge-sensors", 121, labeled("factory", opened.Add(time.Hour)))

	f := gh.start(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	queue := f.queue(t, 3)
	want := []string{"acme/edge-sensors#104", "acme/edge-sensors#112", "acme/edge-sensors#121"}
	if !equal(keys(queue), want) {
		t.Fatalf("the line is %v, want %v: the interruption, then the release, then the new issue", keys(queue), want)
	}
	for position, signal := range []string{"interruption", "release", "routed"} {
		if queue[position].Signal != signal {
			t.Errorf("#%d stands in the line on the signal %q, want %q", queue[position].Number, queue[position].Signal, signal)
		}
	}
	// Among the resumed the order is the time of the signal, and both stand before the new issue
	// however long ago it was routed.
	if !queue[0].SignalAt.Equal(interruptedAt) {
		t.Errorf("the resumed #104 stands at %s, want the time of its interruption %s", queue[0].SignalAt, interruptedAt)
	}
	if !queue[1].SignalAt.Equal(releasedAt.Truncate(time.Second)) {
		t.Errorf("the released #112 stands at %s, want the time the assignee was removed %s", queue[1].SignalAt, releasedAt.Truncate(time.Second))
	}
}

// Answering a release is taking the issue back, and the factory can be stopped in between: the run
// that stands for the release is recorded before the assignee is put on again. Such a run holds
// nothing, and the next start reads the release as the unanswered gesture it still is — rather than
// as an interruption to resume, which would start a worker on an issue GitHub says is nobody's and
// leave it lying in every other claimer's line ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func TestAReleaseThatWasCutOffBeforeTheTakeBackIsStillAnswered(t *testing.T) {
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")

	began := time.Now().UTC().Add(-2 * time.Hour)
	releasedAt := began.Add(50 * time.Minute)
	// The claim that holds the issue, and the release run a stop cut off before it had assigned the
	// issue back to this host: it carries the release it was queued on and holds nothing.
	cutOff := record(2, claimedIssue, claimedTitle, signalRelease, outcomeInterrupted, false,
		releasedAt.Add(time.Second), releasedAt.Add(2*time.Second))
	cutOff.SignalAt = releasedAt
	records(t, data,
		record(1, claimedIssue, claimedTitle, signalRouted, outcomeReady, true, began, began.Add(30*time.Minute)),
		cutOff)
	// GitHub still shows the issue as the person left it: routed, with nobody on it.
	gh.issues(t, "acme/edge-sensors",
		touched(openIssue(claimedIssue, claimedTitle, began.Add(-72*time.Hour)), releasedAt))
	gh.timeline(t, "acme/edge-sensors", claimedIssue, labeled("factory", began.Add(-6*time.Hour)),
		assigned("factory-bot", began), unassigned("factory-bot", releasedAt))

	f := gh.start(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	head := f.queue(t, 1)[0]
	if head.Number != claimedIssue || head.Signal != "release" {
		t.Fatalf("the line opens with #%d on the signal %q, want #%d on a release: the take-back never landed",
			head.Number, head.Signal, claimedIssue)
	}
	if !head.SignalAt.Equal(releasedAt.Truncate(time.Second)) {
		t.Errorf("the released #%d stands at %s, want the time the assignee was removed %s",
			claimedIssue, head.SignalAt, releasedAt.Truncate(time.Second))
	}
}

// GitHub answers for either spelling of a repository, so the factory reads one name whatever case it
// is written in. A configuration rewritten from acme/edge-sensors to Acme/Edge-Sensors names the
// repository whose work this host already holds: the records keep the spelling of the day they were
// written, and a factory that told the two apart would drop that work out of its line and claim its
// issues anew against the branches it owns itself.
func TestHeldWorkStaysInTheLineWhenTheConfigurationRespellsTheRepository(t *testing.T) {
	const respelled, next = "Acme/Edge-Sensors", 121
	gh := newGhShim(t)
	gh.remote(t, respelled)
	gh.loggedInAs(t, "factory-bot")
	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, respelled)

	began := time.Now().UTC().Add(-2 * time.Hour)
	interruptedAt := began.Add(30 * time.Minute)
	// The interrupted run of #104 was written when the configuration spelled the repository the other
	// way; it holds the issue and has its one automatic resume.
	records(t, data,
		record(1, claimedIssue, claimedTitle, signalRouted, outcomeInterrupted, true, began, interruptedAt))
	// #104 is assigned to this host, so GitHub's line carries only the issue nobody has worked yet.
	gh.issues(t, respelled, openIssue(next, "Document the calibration procedure", began.Add(-72*time.Hour)))
	gh.timeline(t, respelled, next, labeled("factory", began.Add(-6*time.Hour)))

	f := gh.start(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{respelled}})
	queue := f.queue(t, 2)
	want := []string{fmt.Sprintf("%s#%d", respelled, claimedIssue), fmt.Sprintf("%s#%d", respelled, next)}
	if !equal(keys(queue), want) {
		t.Fatalf("the line is %v, want %v: the work the records hold, then the new issue", keys(queue), want)
	}
	if queue[0].Signal != "interruption" {
		t.Errorf("#%d stands in the line on the signal %q, want an interruption to resume", claimedIssue, queue[0].Signal)
	}
}

// A resumed run needs the worktree its claim made, and #58 is where a missing one is put back. Until
// then such a resume fails, and this is what that failure must cost: the issue's one automatic
// resume, and nothing else. The repository keeps its place — the next issue in the line is claimed
// and worked — and the issue itself waits for a person, with its branch, its worktree and its
// assignee untouched ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func TestAResumeWhoseWorktreeIsGoneFailsAndSpendsTheResumeOnThatIssueAlone(t *testing.T) {
	const next, nextTitle = 121, "Document the calibration procedure"
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors")
	gh.loggedInAs(t, "factory-bot")
	gh.assigns(t, "acme/edge-sensors", next, "factory-bot")
	gh.workerReports(t, "acme/edge-sensors", next)

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	began := time.Now().UTC().Add(-2 * time.Hour)
	// The interrupted run of #104 holds the issue, and the worktree it names is gone from this host —
	// somebody removed it while the factory was down.
	interrupted := record(1, claimedIssue, claimedTitle, signalRouted, outcomeInterrupted, true, began, began.Add(30*time.Minute))
	interrupted.Worktree = filepath.Join(t.TempDir(), "worktrees", "feat-104")
	records(t, data, interrupted)
	// #104 is assigned to this host, so GitHub's line carries only the issue nobody has worked yet.
	gh.issues(t, "acme/edge-sensors", openIssue(next, nextTitle, began.Add(-72*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", next, labeled("factory", began.Add(-6*time.Hour)))

	f := gh.work(t, config{"poll": "50ms", "deadline": "90s", "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}})
	failed := f.ended(t, 2)
	if failed.Issue != claimedIssue || failed.Signal != "interruption" || failed.Outcome != "failed" {
		t.Fatalf("run 2 works #%d on the signal %q and ends as %q, want the resume of #%d ending failed; the factory's log:\n%s",
			failed.Issue, failed.Signal, failed.Outcome, claimedIssue, f.output(t))
	}
	// The reason names the worktree that is missing and the branch that still holds the issue, which
	// is what a person needs to put it back or let the issue go.
	for _, want := range []string{interrupted.Worktree, claimedBranch} {
		if !strings.Contains(failed.Reason, want) {
			t.Errorf("the failed resume gives the reason %q, want %q in it", failed.Reason, want)
		}
	}
	// The issue is left exactly as it was: no worker ran on it and nothing was said to GitHub about it.
	if made := gh.asked(t, fmt.Sprintf("issue edit %d --repo acme/edge-sensors", claimedIssue)); made != 0 {
		t.Errorf("the factory edited #%d %d times, want none: a resume that cannot start touches nothing", claimedIssue, made)
	}

	// The repository is not held for it: the failure was the issue's, so the next issue in the line is
	// claimed and worked as if nothing had happened.
	worked := f.ended(t, 3)
	if worked.Issue != next || worked.Outcome != "ready" {
		t.Fatalf("run 3 works #%d and ends as %q (%s), want #%d ready: the repository keeps working; the factory's log:\n%s",
			worked.Issue, worked.Outcome, worked.Reason, next, f.output(t))
	}
	// And #104 is not resumed a second time: the one automatic resume was spent on the run that failed.
	f.never(t, 3*time.Second, fmt.Sprintf("the factory started a run after #%d was worked, so it resumed #%d again", next, claimedIssue),
		func() bool { return !f.missing(t, 4) })
	var line apiLine
	f.get(t, "/api/line", &line)
	if len(line.Queue) != 0 || len(line.Now) != 0 {
		t.Errorf("the factory queues %v and runs %d, want an idle line: the failed resume waits for a person",
			keys(line.Queue), len(line.Now))
	}
	if workers := gh.workers(t); len(workers) != 1 {
		t.Errorf("the factory started %d workers, want one: only the claim of #%d had a worktree to run in", len(workers), next)
	}
}

// The one automatic resume per issue, in every shape an issue can reach it in. A run of the factory
// reaches one of these at a time and a stop is minutes of test for each, so the rule itself is read
// here, from the records a restart reads it from, and the two signals are driven end to end above
// and in TestAnInterruptedIssueIsResumedOnceByItselfAndASecondInterruptionWaitsForAPerson.
func TestTheAutomaticResumeIsOnePerIssueAndOnlyAReleaseGivesItBack(t *testing.T) {
	ended := time.Now().UTC()
	run := func(id int, signal, outcome string, holding bool) Run {
		return record(id, 104, "Retry the upload", signal, outcome, holding, ended.Add(-time.Hour), ended)
	}
	for _, c := range []struct {
		name    string
		runs    []Run
		resumes bool
	}{
		{"the first interruption of an issue", []Run{
			run(1, signalRouted, outcomeInterrupted, true)}, true},
		{"the second interruption of an issue", []Run{
			run(1, signalRouted, outcomeInterrupted, true),
			run(2, signalInterruption, outcomeInterrupted, true)}, false},
		// The resume is spent by the run it queued, whatever became of that run: a resume that could
		// not start — a worktree that is not on the host — is not tried again by itself either.
		{"an automatic resume that ended in something else", []Run{
			run(1, signalRouted, outcomeInterrupted, true),
			run(2, signalInterruption, outcomeFailed, true)}, false},
		{"an interruption after the automatic resume ended otherwise", []Run{
			run(1, signalRouted, outcomeInterrupted, true),
			run(2, signalInterruption, outcomeFailed, true),
			run(3, signalRelease, outcomeInterrupted, true)}, true},
		{"the first interruption after a release", []Run{
			run(1, signalRouted, outcomeInterrupted, true),
			run(2, signalInterruption, outcomeInterrupted, true),
			run(3, signalRelease, outcomeInterrupted, true)}, true},
		{"the second interruption after a release", []Run{
			run(1, signalRouted, outcomeInterrupted, true),
			run(2, signalInterruption, outcomeInterrupted, true),
			run(3, signalRelease, outcomeInterrupted, true),
			run(4, signalInterruption, outcomeInterrupted, true)}, false},
		{"a run that ended in anything else", []Run{
			run(1, signalRouted, outcomeFailed, true)}, false},
		{"a claim another claimer won", []Run{
			run(1, signalRouted, outcomeLost, false)}, false},
		{"a claim that was interrupted before it held anything", []Run{
			run(1, signalRouted, outcomeInterrupted, false)}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			held := holdings(c.runs)["acme/edge-sensors#104"]
			if held.resumes != c.resumes {
				t.Errorf("the factory resumes this issue by itself: %v, want %v", held.resumes, c.resumes)
			}
		})
	}
	// A run that is still going is nothing to queue beside, whatever went before it.
	active := []Run{run(1, signalRouted, outcomeInterrupted, true), run(2, signalInterruption, "", true)}
	active[1].EndedAt, active[1].State = nil, "running"
	if held := holdings(active)["acme/edge-sensors#104"]; held.resumes || held.idle {
		t.Errorf("an issue whose run is still going is read as idle=%v resumes=%v, want neither", held.idle, held.resumes)
	}
}

// The release signal, in the shapes a poll can meet it in. What it is read from is one issue of the
// line GitHub answers with, beside the records of that issue: the gesture itself is driven end to
// end in TestAReleasedIssueIsAssignedAgainAndResumedInTheSameWorktree, and what is read here is the
// rule that decides whether a given poll is a release at all — above all that a release is answered
// once, because the poll that queued it repeats for as long as the re-assignment takes to land.
func TestAReleaseIsTheRemovedAssigneeOfAHeldIssueAndIsAnsweredOnce(t *testing.T) {
	began := time.Now().UTC().Add(-time.Hour)
	ended := began.Add(30 * time.Minute)
	removed := began.Add(45 * time.Minute) // after the run that held the issue, so it is a release
	run := func(id int, signal, outcome string, holding bool) Run {
		return record(id, 104, "Retry the upload", signal, outcome, holding, began, ended)
	}
	answered := func(id int, at time.Time) Run {
		r := run(id, signalRelease, outcomeReady, true)
		r.SignalAt, r.StartedAt = at, at.Add(time.Second)
		return r
	}
	// The same answering run on a host whose clock is five minutes behind GitHub's, so the run
	// started, as this host tells the time, before the removal that queued it.
	behind := func(id int, at time.Time) Run {
		r := answered(id, at)
		r.StartedAt = at.Add(-5 * time.Minute)
		return r
	}
	// The claim of the issue on a host whose clock runs half an hour ahead of GitHub's: it holds the
	// issue and is long over by the time GitHub says the assignee came off.
	ahead := func(id int) Run {
		return record(id, 104, "Retry the upload", signalRouted, outcomeReady, true,
			removed.Add(30*time.Minute), removed.Add(40*time.Minute))
	}
	// claimed is when this factory put itself on the issue, as GitHub timed it: every claim makes
	// that assignment, so every poll of a held issue reads one. never is an event list that names no
	// assignment at all.
	claimed, never := began, time.Time{}
	for _, c := range []struct {
		name string
		runs []Run
		// assigned is when an assignee was last put on the issue and at when one was last taken off,
		// both as GitHub's event list gives them.
		assigned time.Time
		at       time.Time // when the assignee came off, as this poll reads it
		routed   bool      // the issue is in the line GitHub answers with: open, routed, unassigned
		released bool
	}{
		{"the assignee of a held issue was removed", []Run{
			run(1, signalRouted, outcomeReady, true)}, claimed, removed, true, true},
		{"the same removal while the run it queued has not been recorded", []Run{
			run(1, signalRouted, outcomeReady, true), answered(2, removed)}, claimed, removed, true, false},
		// And the same removal answered by a run of a host whose clock lags GitHub's. What decides is
		// the release the run was queued on, which is GitHub's own reading of the removal, so the
		// drift between the two clocks cannot queue the release a second time.
		{"the same removal answered by a run that started earlier on this host's clock", []Run{
			run(1, signalRouted, outcomeReady, true), behind(2, removed)}, claimed, removed, true, false},
		// The other direction of the same drift: a host running ahead of GitHub, whose run of the
		// issue is over before GitHub's clock reaches the removal. The removal is held against the
		// assignment it undid, which GitHub timed as well, so the release is read for what it is.
		{"a removal this host's clock puts before the run that holds the issue", []Run{
			ahead(1)}, claimed, removed, true, true},
		{"another removal after the release that was answered", []Run{
			run(1, signalRouted, outcomeReady, true), answered(2, removed)},
			claimed, removed.Add(20 * time.Minute), true, true},
		{"an assignee somebody removed before the factory claimed the issue", []Run{
			run(1, signalRouted, outcomeReady, true)}, claimed, began.Add(-time.Hour), true, false},
		// The same gesture on an event list that has lost the assignment, or has not caught up with
		// it: there is no reading of GitHub's to hold the removal against, and the run that holds the
		// issue is what says the removal is older than the claim.
		{"the same removal while GitHub names no assignment at all", []Run{
			run(1, signalRouted, outcomeReady, true)}, never, began.Add(-time.Hour), true, false},
		{"an issue that is not in the routed line, so nobody unassigned it", []Run{
			run(1, signalRouted, outcomeReady, true)}, claimed, removed, false, false},
		{"a routed issue whose branch another claimer created", []Run{
			run(1, signalRouted, outcomeLost, false)}, claimed, removed, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			held := holdings(c.runs)["acme/edge-sensors#104"]
			issue := Issue{Repository: "acme/edge-sensors", Number: 104,
				assignedAt: c.assigned, unassignedAt: c.at}
			if released := held.released(issue, c.routed); released != c.released {
				t.Errorf("the factory reads this as a release: %v, want %v", released, c.released)
			}
		})
	}
	// And nothing is queued beside a run that is still going, however long ago the assignee came off.
	active := []Run{run(1, signalRouted, "", true)}
	active[0].EndedAt, active[0].State = nil, "running"
	issue := Issue{Repository: "acme/edge-sensors", Number: 104, unassignedAt: removed}
	if holdings(active)["acme/edge-sensors#104"].released(issue, true) {
		t.Error("an issue whose run is still going is read as released; the run would be queued beside itself")
	}
}

// record is one run as the data directory carries it, with the fields the resume rules read.
func record(id, issue int, title, signal, outcome string, holding bool, started, ended time.Time) Run {
	return Run{
		ID: id, Repository: "acme/edge-sensors", Issue: issue, Title: title,
		Branch: fmt.Sprintf("feat/%d-%s", issue, strings.ToLower(strings.ReplaceAll(title, " ", "-"))),
		Base:   "main", Worktree: filepath.Join("worktrees", fmt.Sprint(issue)), Holding: holding,
		Signal: signal, State: "ended", Outcome: outcome, Stages: []string{}, Warnings: []string{},
		StartedAt: started, EndedAt: &ended,
	}
}

// in puts a record in another repository than the one the tests work in.
func in(repository string, run Run) Run {
	run.Repository = repository
	return run
}

// records writes run records into a data directory before the factory starts on it, which is what a
// factory that was stopped, rebooted or cut off from power finds there.
func records(t *testing.T, data string, runs ...Run) {
	t.Helper()
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		writeFile(t, filepath.Join(data, fmt.Sprintf("run-%d.json", run.ID)), marshal(t, run))
	}
}
