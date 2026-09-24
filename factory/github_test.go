package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The live queue, tested the way the rest of the factory is: the real binary, started against the gh
// shim in testdata, watched through its HTTP interface, its data directory and the calls it made.

func TestTheQueueIsTheRoutedIssuesOfEveryConnectedRepositoryOldestRoutingFirst(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	now := time.Now().UTC()
	// The three are routed out of the order they were opened, and #118 is the oldest issue of all: it
	// stands last because it was handed over last.
	gh.remote(t, "acme/edge-sensors")
	gh.remote(t, "acme/backtest")
	gh.issues(t, "acme/edge-sensors",
		openIssue(104, "Retry the upload when the broker drops the connection", now.Add(-72*time.Hour)),
		openIssue(118, "Document the calibration procedure", now.Add(-200*time.Hour)))
	gh.issues(t, "acme/backtest",
		openIssue(9, "Überwachung: Füllstand fällt unter den Schwellwert", now.Add(-48*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 104, labeled("factory", now.Add(-6*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 118, labeled("bug", now.Add(-190*time.Hour)), labeled("factory", now.Add(-time.Hour)))
	gh.timeline(t, "acme/backtest", 9,
		labeled("factory", now.Add(-30*time.Hour)), unlabeled("factory", now.Add(-20*time.Hour)), labeled("factory", now.Add(-4*time.Hour)))

	f := gh.start(t, config{"poll": "50ms"})
	queue := f.queue(t, 3)
	if got := keys(queue); !equal(got, []string{"acme/edge-sensors#104", "acme/backtest#9", "acme/edge-sensors#118"}) {
		t.Errorf("the line is %v, want the routed issues of both repositories by the time the label was set, oldest first", got)
	}
	// The label of #9 was set, removed and set again four hours ago: the last time it was handed over
	// is the one that counts, and #118, an issue from a week ago, stands behind both.
	if !queue[2].RoutedAt.After(queue[1].RoutedAt) || !queue[1].RoutedAt.After(queue[0].RoutedAt) {
		t.Errorf("the routing times are not in order: %v", queue)
	}
	if queue[0].Title != "Retry the upload when the broker drops the connection" {
		t.Errorf("the head of the line is %q, want the issue's title as GitHub gives it", queue[0].Title)
	}
}

// An issue whose timeline does not name the routing label keeps its place by the time it was opened,
// rather than jumping to the head of the line with an empty time.
func TestAnIssueWithoutALabelEventStandsInTheLineByWhenItWasOpened(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.issues(t, "acme/edge-sensors",
		openIssue(7, "Opened with the label already on it", now.Add(-90*time.Hour)),
		openIssue(8, "Routed an hour ago", now.Add(-100*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 7)
	gh.timeline(t, "acme/edge-sensors", 8, labeled("factory", now.Add(-time.Hour)))

	f := gh.start(t, config{"poll": "50ms", "repositories": []string{"acme/edge-sensors"}})
	queue := f.queue(t, 2)
	if got := keys(queue); !equal(got, []string{"acme/edge-sensors#7", "acme/edge-sensors#8"}) {
		t.Errorf("the line is %v, want the issue without a label event in the place its age gives it", got)
	}
	if queue[0].RoutedAt.IsZero() {
		t.Error("the issue without a label event carries an empty routing time; it would stand in front of every issue")
	}
}

func TestTheQueueIsAskedOnEveryPollAndNeverWrittenDown(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.issues(t, "acme/edge-sensors", openIssue(104, "Retry the upload", now.Add(-72*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 104, labeled("factory", now.Add(-6*time.Hour)))

	f := gh.start(t, config{"poll": "50ms", "repositories": []string{"acme/edge-sensors"}})
	f.queue(t, 1)
	// The maintainer takes the routing label off: at the next poll the issue is out of the line.
	gh.issues(t, "acme/edge-sensors")
	f.queue(t, 0)

	if asked := gh.made(t, "api "+issuesRequest("acme/edge-sensors", "factory")); asked < 2 {
		t.Errorf("the factory asked GitHub for the line %d times, want one per poll", asked)
	}
	for _, file := range files(t, f.data) {
		if strings.Contains(readFile(t, file), "Retry the upload") {
			t.Errorf("%s holds the queue; it is a view of GitHub and is never written down", file)
		}
	}
}

func TestAConnectedRepositoryIsClonedOnStartAndAFailedCloneIsReported(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.remote(t, "acme/edge-sensors") // the other repository is on no GitHub the host can reach
	gh.issues(t, "acme/edge-sensors")
	gh.issues(t, "acme/backtest")

	data := filepath.Join(t.TempDir(), "data")
	f := gh.start(t, config{"poll": "50ms", "data_dir": data})
	f.queue(t, 0)
	if _, err := os.Stat(filepath.Join(data, "repos", "acme", "edge-sensors", ".git")); err != nil {
		t.Errorf("the connected repository was not cloned into the data directory: %v", err)
	}
	if output := f.output(t); !strings.Contains(output, "error: acme/backtest could not be cloned") {
		t.Errorf("the factory said %q, want an error line for the repository it could not clone", output)
	}
	if _, err := os.Stat(filepath.Join(data, "repos", "acme", "backtest", ".git")); !os.IsNotExist(err) {
		t.Errorf("the failed clone left something behind: %v", err)
	}

	// The repository that is there is not cloned again on the next start, and the one that is missing
	// is tried again, because connecting it is a line in the configuration and a restart. What a host
	// that was rebooted mid-clone left behind is swept rather than collected.
	f.stop(t, syscall.SIGTERM)
	leftover := filepath.Join(data, "repos", "acme", ".backtest.cloning-42")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(leftover, "half"), "a clone nobody finished\n")
	gh.start(t, config{"poll": "50ms", "data_dir": data}).queue(t, 0)
	if cloned := gh.cloned(t, "acme/edge-sensors"); cloned != 1 {
		t.Errorf("the repository was cloned %d times, want once: a clone that is there is kept", cloned)
	}
	if tried := gh.cloned(t, "acme/backtest"); tried != 2 {
		t.Errorf("the repository that is missing was cloned %d times, want one try per start", tried)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("the staging directory of an earlier start is still there: %v", err)
	}
}

// GitHub answers for either spelling of a repository, so the host keeps one clone of it whatever
// the configuration spells: an operator who rewrites acme/edge-sensors as Acme/Edge-Sensors has
// renamed nothing, and a second clone of the same repository beside the first is a data directory
// growing by a spelling.
func TestARepositoryIsClonedOnceHoweverTheConfigurationSpellsIt(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	for _, spelling := range []string{"acme/edge-sensors", "Acme/Edge-Sensors"} {
		gh.remote(t, spelling)
		gh.issues(t, spelling)
	}

	data := filepath.Join(t.TempDir(), "data")
	f := gh.start(t, config{"poll": "50ms", "data_dir": data, "repositories": []string{"acme/edge-sensors"}})
	f.queue(t, 0)
	f.stop(t, syscall.SIGTERM)

	gh.start(t, config{"poll": "50ms", "data_dir": data, "repositories": []string{"Acme/Edge-Sensors"}}).queue(t, 0)
	if cloned := gh.cloned(t, "acme/edge-sensors") + gh.cloned(t, "Acme/Edge-Sensors"); cloned != 1 {
		t.Errorf("the repository was cloned %d times, want once: the clone of the other spelling is the same clone", cloned)
	}
	if _, err := os.Stat(filepath.Join(data, "repos", "acme", "edge-sensors", ".git")); err != nil {
		t.Errorf("the clone is not where the first start put it: %v", err)
	}
	// The path itself, because the host that runs the factory tells the two spellings apart and the
	// one a developer runs this on may not: on a case-insensitive file system the start above would
	// have found the clone either way.
	if got, want := clonePath(data, "Acme/Edge-Sensors"), filepath.Join(data, "repos", "acme", "edge-sensors"); got != want {
		t.Errorf("the clone of Acme/Edge-Sensors belongs in %s, want %s: one repository, one directory", got, want)
	}
}

func TestARepositoryThatCannotBeReadIsSaidSoAndDoesNotEmptyTheLineOfTheOthers(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.remote(t, "acme/backtest")
	gh.issues(t, "acme/edge-sensors", openIssue(104, "Retry the upload", now.Add(-72*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 104, labeled("factory", now.Add(-6*time.Hour)))
	gh.issues(t, "acme/backtest", openIssue(9, "Warn when a backtest is short", now.Add(-48*time.Hour)))
	gh.timeline(t, "acme/backtest", 9, labeled("factory", now.Add(-time.Hour)))

	f := gh.start(t, config{"poll": "50ms"})
	f.queue(t, 2)
	events := "api --paginate " + eventsRequest("acme/backtest", 9)
	knew := gh.made(t, events)
	if knew != 1 {
		t.Fatalf("the events of the issue were read %d times before the outage, want once", knew)
	}

	// One of the two repositories answers as an unreachable GitHub does from here on.
	gh.fail(t, "api repos/acme/backtest/*")
	queue := f.queue(t, 1)
	if got := keys(queue); !equal(got, []string{"acme/edge-sensors#104"}) {
		t.Errorf("the line is %v, want the repository that could be read", got)
	}
	f.eventually(t, 10*time.Second, "the error line for the repository that could not be read", func() bool {
		return strings.Contains(f.output(t), "error: the routed issues of acme/backtest could not be read")
	})
	// The interface says it too: a repository nobody can read holds no queue either, and an empty
	// count would be the same sight as a repository with nothing routed.
	if said := f.repositorySaid(t, "acme/backtest"); !strings.Contains(said, "error connecting to api.github.com") {
		t.Errorf("the interface says %q of the repository it could not read, want what gh said", said)
	}
	if said := f.repositorySaid(t, "acme/edge-sensors"); said != "" {
		t.Errorf("the repository that could be read carries the error %q", said)
	}
	// It is reported when it starts failing, not once per poll: this factory polls twenty times a
	// second and a host runs it for weeks.
	asked := "api " + issuesRequest("acme/backtest", "factory")
	f.eventually(t, 10*time.Second, "several polls", func() bool { return gh.made(t, asked) >= 5 })
	if said := strings.Count(f.output(t), "error: the routed issues of acme/backtest could not be read"); said != 1 {
		t.Errorf("the factory reported the unreadable repository %d times over %d polls, want once", said, gh.made(t, asked))
	}

	// GitHub answers again: the repository comes back into the line and the interface stops warning.
	gh.fail(t, "")
	f.queue(t, 2)
	if said := f.repositorySaid(t, "acme/backtest"); said != "" {
		t.Errorf("the repository that can be read again carries the error %q", said)
	}
	if !strings.Contains(f.output(t), "the routed issues of acme/backtest can be read again") {
		t.Errorf("the factory said %q, want a line for the repository that can be read again", f.output(t))
	}
	// What the factory knew of that repository's issues survived the outage: the likeliest reason for
	// a failed read is a rate limit, and answering one by reading every event list again is the worst
	// thing it could do.
	if again := gh.made(t, events); again != knew {
		t.Errorf("the events of the issue were read %d more times after the repository came back; what was known of it was thrown away", again-knew)
	}
}

// An issue whose event list cannot be read keeps its place by the time it was opened, and is
// reported once: the read is tried again on every poll, the line about it is not written again.
func TestAnIssueWhoseEventsCannotBeReadStandsInTheLineAllTheSame(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.issues(t, "acme/edge-sensors", openIssue(104, "Retry the upload", now.Add(-72*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 104, labeled("factory", now.Add(-6*time.Hour)))
	gh.fail(t, "api --paginate repos/acme/edge-sensors/issues/104/*")

	f := gh.start(t, config{"poll": "50ms", "repositories": []string{"acme/edge-sensors"}})
	queue := f.queue(t, 1)
	if at := queue[0].RoutedAt; at.After(now.Add(-71 * time.Hour)) {
		t.Errorf("the issue stands in the line by %v, want the time it was opened", at)
	}
	asked := "api " + issuesRequest("acme/edge-sensors", "factory")
	f.eventually(t, 10*time.Second, "several polls", func() bool { return gh.made(t, asked) >= 5 })
	if said := strings.Count(f.output(t), "error: the events of acme/edge-sensors#104 could not be read"); said != 1 {
		t.Errorf("the factory reported the unreadable event list %d times over %d polls, want once", said, gh.made(t, asked))
	}

	// It is read again on every poll all the same, so the issue takes its place as soon as GitHub
	// answers: nothing about the failure is remembered but the line that reported it.
	gh.fail(t, "")
	f.eventually(t, 10*time.Second, "the routing time after the events could be read", func() bool {
		return f.queue(t, 1)[0].RoutedAt.After(now.Add(-7 * time.Hour))
	})
}

// The routing time of an issue costs a request of its own, so it is remembered until GitHub says the
// issue was touched: a line that stands still is one request per repository and per poll, not one
// per issue, on a token the whole workflow shares.
func TestARoutingTimeIsReadAgainOnlyWhenTheIssueWasTouched(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.issues(t, "acme/edge-sensors", openIssue(104, "Retry the upload", now.Add(-72*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 104, labeled("factory", now.Add(-6*time.Hour)))

	f := gh.start(t, config{"poll": "50ms", "repositories": []string{"acme/edge-sensors"}})
	routed := f.queue(t, 1)[0].RoutedAt
	asked := "api " + issuesRequest("acme/edge-sensors", "factory")
	timeline := "api --paginate " + eventsRequest("acme/edge-sensors", 104)
	f.eventually(t, 10*time.Second, "several polls", func() bool { return gh.made(t, asked) >= 5 })
	if read := gh.made(t, timeline); read != 1 {
		t.Errorf("the timeline was read %d times over %d polls, want once: nothing touched the issue", read, gh.made(t, asked))
	}

	// The maintainer takes the label off and puts it back on. GitHub touches the issue with it, and
	// the factory reads the timeline again and moves the issue to the back of the line.
	touched := openIssue(104, "Retry the upload", now.Add(-72*time.Hour))
	touched["updated_at"] = now.Add(-time.Minute).Format(time.RFC3339)
	// The events are written before the issue that says it was touched, as GitHub has them before it
	// answers the list: the other order would let a poll in between remember the old time for good.
	gh.timeline(t, "acme/edge-sensors", 104, labeled("factory", now.Add(-6*time.Hour)),
		unlabeled("factory", now.Add(-2*time.Hour)), labeled("factory", now.Add(-time.Minute)))
	gh.issues(t, "acme/edge-sensors", touched)
	f.eventually(t, 10*time.Second, "the routing time of the issue after it was touched", func() bool {
		return f.queue(t, 1)[0].RoutedAt.After(routed)
	})
}

// The two views GitHub answers with are not one moment: the issue list carries the routing label
// while the timeline has yet to name the event that set it. The issue stands in the line by its age
// until then, and because that answer is nothing GitHub confirmed, it is not written down: the next
// poll looks again and the issue takes the place its routing gives it, without anybody touching it.
func TestARoutingTimeTheTimelineDoesNotCarryYetIsReadAgain(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.issues(t, "acme/edge-sensors",
		openIssue(11, "Routed while the timeline was catching up", now.Add(-90*time.Hour)),
		openIssue(12, "Routed yesterday", now.Add(-48*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 11) // the label is on the issue, the event is not there yet
	gh.timeline(t, "acme/edge-sensors", 12, labeled("factory", now.Add(-24*time.Hour)))

	f := gh.start(t, config{"poll": "50ms", "repositories": []string{"acme/edge-sensors"}})
	if got := keys(f.queue(t, 2)); !equal(got, []string{"acme/edge-sensors#11", "acme/edge-sensors#12"}) {
		t.Fatalf("the line is %v, want the issue the timeline says nothing about in the place its age gives it", got)
	}
	// Seconds later GitHub names the event. Nothing touched the issue, so its updated_at is the one
	// the factory already saw: only an answer that was never remembered is read again.
	gh.timeline(t, "acme/edge-sensors", 11, labeled("factory", now.Add(-time.Minute)))
	f.eventually(t, 10*time.Second, "the issue behind the one routed a day before it", func() bool {
		var line apiLine
		f.get(t, "/api/line", &line)
		return equal(keys(line.Queue), []string{"acme/edge-sensors#12", "acme/edge-sensors#11"})
	})
}

// gh --paginate answers one JSON array per page, and the routing label may have been set on any of
// them; the pages are read as the stream of arrays they are.
func TestTheRoutingTimeIsReadFromEveryPageOfTheTimeline(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.issues(t, "acme/edge-sensors", openIssue(104, "Retry the upload", now.Add(-72*time.Hour)))
	gh.answer(t, "api --paginate "+eventsRequest("acme/edge-sensors", 104),
		marshal(t, []map[string]any{labeled("bug", now.Add(-60*time.Hour))})+"\n"+
			marshal(t, []map[string]any{labeled("factory", now.Add(-3*time.Hour))}))

	f := gh.start(t, config{"poll": "50ms", "repositories": []string{"acme/edge-sensors"}})
	if at := f.queue(t, 1)[0].RoutedAt; at.Before(now.Add(-4 * time.Hour)) {
		t.Errorf("the issue stands in the line by %v, want the label event on the second page", at)
	}
}

// A clone is the one call that takes minutes, and gh does it in a git of its own. The stop has to
// reach that child, and a clone that was cut off must leave nothing the next start would take for a
// finished clone and hand to a worker.
func TestACloneThatIsCutOffEndsWithTheFactoryAndLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.issues(t, "acme/edge-sensors")
	child := gh.hang(t, 5*time.Minute)

	data := filepath.Join(t.TempDir(), "data")
	f := gh.start(t, config{"poll": "50ms", "data_dir": data, "repositories": []string{"acme/edge-sensors"}})
	f.eventually(t, 20*time.Second, "the clone and the child it runs in", func() bool {
		_, err := os.Stat(child)
		return err == nil
	})
	// While the clones are made there is no line yet, and the interface says which of the two it is.
	var status map[string]any
	f.get(t, "/api/status", &status)
	if status["state"] != "connecting" {
		t.Errorf("the factory says it is %q while it clones, want connecting", status["state"])
	}
	stopped := time.Now()
	f.stop(t, syscall.SIGTERM)
	if held := time.Since(stopped); held > 20*time.Second {
		t.Errorf("the factory took %v to stop while a clone ran; the clone has to end with it", held)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(readFile(t, child)))
	if err != nil {
		t.Fatalf("the clone wrote no pid to %s: %v", child, err)
	}
	if survived(pid) {
		t.Errorf("the child of the clone (%d) outlived the factory", pid)
	}
	clone := filepath.Join(data, "repos", "acme", "edge-sensors")
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Errorf("the clone that was cut off left %s behind; the next start would take it for a whole one", clone)
	}
	if staging, _ := filepath.Glob(filepath.Join(data, "repos", "acme", ".edge-sensors.cloning-*")); len(staging) != 0 {
		t.Errorf("the clone that was cut off left %v behind", staging)
	}
}

// A pause is the brake on everything this host does by itself: it claims nothing, and it answers
// nothing about the issue it holds either, whatever GitHub says has become of it.
func TestPausedAgainstGitHubShowsTheLineAndClaimsNothing(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.issues(t, "acme/edge-sensors", openIssue(104, "Retry the upload", now.Add(-72*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 104, labeled("factory", now.Add(-6*time.Hour)))
	// An issue this host holds whose routing label a maintainer has taken off, which is the decision
	// a working factory would cancel and let go on.
	gh.issue(t, "acme/edge-sensors", assignedTo(openIssue(121, "Document the calibration procedure", now.Add(-72*time.Hour), readyLabel), "factory-bot"))

	data := filepath.Join(t.TempDir(), "data")
	held := record(1, 121, "Document the calibration procedure", signalRouted, outcomeReady, true, now.Add(-2*time.Hour), now.Add(-time.Hour))
	held.Worktree = filepath.Join(t.TempDir(), "worktrees", "feat-121")
	if err := os.MkdirAll(held.Worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	// And what a factory before this one owes the maintainer: an ending it recorded and did not get
	// to notify, and a run it left active that this start records as a second interruption, which
	// waits for a person. A paused start is how a recovered data directory is looked at, so neither
	// may reach GitHub from here.
	owed := record(2, 130, "Calibrate the sensors", signalRouted, outcomeFailed, true, now.Add(-4*time.Hour), now.Add(-3*time.Hour))
	owed.Notified = notifyPending
	first := record(3, 131, "Log the sensor drift", signalRouted, outcomeInterrupted, true, now.Add(-4*time.Hour), now.Add(-3*time.Hour))
	cutOff := record(4, 131, "Log the sensor drift", signalInterruption, "", true, now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	cutOff.State, cutOff.EndedAt = "running", nil
	records(t, data, held, owed, first, cutOff)

	f := gh.start(t, config{"poll": "50ms", "data_dir": data, "repositories": []string{"acme/edge-sensors"}, "notify": []string{"ada"}})
	f.queue(t, 1)
	// Owed, and kept owed for the first start that works.
	for _, id := range []int{2, 4} {
		var run apiRun
		f.get(t, fmt.Sprintf("/api/runs/%d", id), &run)
		if run.Notified != notifyPending {
			t.Errorf("run %d says its notification is %q, want it pending until the factory works again", id, run.Notified)
		}
	}

	// It does not even ask what became of what it holds: there is nothing it would do about it.
	if asked := gh.asked(t, "api repos/acme/edge-sensors/issues/121"); asked != 0 {
		t.Errorf("the factory asked about the issue it holds %d times, want none while it is paused", asked)
	}
	if _, err := os.Stat(held.Worktree); err != nil {
		t.Errorf("the worktree %s of the held issue is gone: %v; a paused factory takes nothing apart", held.Worktree, err)
	}
	var stillHeld apiRun
	f.get(t, "/api/runs/1", &stillHeld)
	if stillHeld.LetGoAt != nil {
		t.Errorf("the factory let the held issue go at %v while it was paused", stillHeld.LetGoAt)
	}

	var status map[string]any
	f.get(t, "/api/status", &status)
	if status["state"] != "paused" {
		t.Errorf("the factory says it is %q, want paused", status["state"])
	}
	if written, _ := filepath.Glob(filepath.Join(f.data, "run-*.json")); len(written) != 4 {
		t.Errorf("paused, the factory left %d run records, want the four it started with", len(written))
	}
	// Nothing it did to GitHub is a claim: it reads the line and clones, and writes nothing at all.
	for _, call := range gh.calls(t) {
		if !strings.HasPrefix(call, "api repos/") && !strings.HasPrefix(call, "api --paginate repos/") && !strings.HasPrefix(call, "repo clone ") {
			t.Errorf("the factory called `gh %s`; paused it reads and claims nothing", call)
		}
		// A reading path with a writing flag on it is a write: gh api sends a POST as soon as one of
		// these is there.
		for _, writes := range []string{"-X", "--method", "-f", "--field", "-F", "--raw-field", "--input"} {
			if strings.Contains(" "+call+" ", " "+writes+" ") || strings.Contains(" "+call, " "+writes+"=") {
				t.Errorf("the factory called `gh %s`; paused it reads and claims nothing", call)
			}
		}
	}
}

// -paused is the operator's brake: it pauses a factory whose configuration says otherwise. There is
// no flag the other way round, so a paused configuration stays paused whatever the command line says.
func TestTheCommandLinePausesAFactoryWhoseConfigurationSaysOtherwise(t *testing.T) {
	t.Parallel()
	gh := newGhShim(t)
	gh.routed(t, "acme/edge-sensors", 104, "Retry the upload")

	data := filepath.Join(t.TempDir(), "data")
	gh.cloneInto(t, data, "acme/edge-sensors")
	f := launch(t, config{"poll": "50ms", "paused": false, "data_dir": data,
		"repositories": []string{"acme/edge-sensors"}}, gh.env, "-paused")
	// The issue is in the line and everything a claim needs is there; only the brake is in the way.
	f.queue(t, 1)
	var status map[string]any
	f.get(t, "/api/status", &status)
	if status["state"] != "paused" {
		t.Errorf("the factory says it is %q with -paused on a configuration that is not, want paused", status["state"])
	}
	if head := gh.head(t, "acme/edge-sensors", "feat/104-retry-the-upload"); head != "" {
		t.Errorf("the brake says paused and the factory claimed the head of its line anyway: %s is on the remote", "feat/104-retry-the-upload")
	}
	if records, _ := filepath.Glob(filepath.Join(f.data, "run-*.json")); len(records) != 0 {
		t.Errorf("paused on the command line, the factory made %d runs of a line it should not touch", len(records))
	}
}

// The factory's frontier rule and the orchestrator board's rule for free agent-ready issues are the
// same rule in two languages, split by the routing label: what the board leaves out is what the
// factory takes. This is the drift test that binds them (ADR 0022). It runs both programs over one
// set of issues, once with the routing label on every issue and once with it on none.
//
// Both read GitHub through a query that already says `state=open` and `labels=ready-for-agent`, so
// those two conditions are the query's and cannot be varied here; what the fixture varies is what
// each rule decides for itself: assignees, open blockers and pull requests.
func TestTheFrontierRuleAgreesWithTheOrchestratorBoard(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	free := []int{40, 44}
	fixture := func(routed bool) []issueJSON {
		labels := []string{readyLabel}
		if routed {
			labels = append(labels, "factory")
		}
		with := func(i issueJSON, change func(issueJSON)) issueJSON { change(i); return i }
		return []issueJSON{
			openIssue(40, "Expand schema", now, labels...),
			openIssue(44, "Loose end", now, labels...),
			with(openIssue(41, "Migrate callers", now, labels...), func(i issueJSON) {
				i["issue_dependencies_summary"] = map[string]any{"blocked_by": 1}
			}),
			with(openIssue(43, "Taken", now, labels...), func(i issueJSON) {
				i["assignees"] = []any{map[string]any{"login": "bob"}}
			}),
			with(openIssue(50, "A pull request", now, labels...), func(i issueJSON) {
				i["pull_request"] = map[string]any{"url": "https://api.github.com/repos/o/r/pulls/50"}
			}),
		}
	}

	routedIssues, factoryRequest := factoryQueueOf(t, fixture(true), len(free))
	if !equal(routedIssues, free) {
		t.Errorf("the factory takes %v of the routed issues, want the free ones %v", routedIssues, free)
	}
	if unrouted, _ := factoryQueueOf(t, fixture(false), 0); len(unrouted) != 0 {
		t.Errorf("the factory takes %v without the routing label, want nothing: an unrouted issue is the maintainer's", unrouted)
	}

	boardOfRouted, _ := boardFrontierOf(t, fixture(true))
	if len(boardOfRouted) != 0 {
		t.Errorf("the board offers %v of the routed issues, want none: they are the factory's", boardOfRouted)
	}
	boardOfUnrouted, boardRequest := boardFrontierOf(t, fixture(false))
	if !equal(boardOfUnrouted, free) {
		t.Errorf("the board offers %v, want the free ones %v; the two rules disagree on the same issues", boardOfUnrouted, free)
	}

	// Both ask GitHub the same question, the factory's with the routing label added to it.
	want := strings.Replace(boardRequest, "labels="+readyLabel, "labels="+readyLabel+",factory", 1)
	if factoryRequest != want {
		t.Errorf("the factory asks GitHub for %q and the board for %q; want %q", factoryRequest, boardRequest, want)
	}
}

// factoryQueueOf starts the real binary against the gh shim and answers with the issues it takes into
// its line, and with the request it asked GitHub for them.
func factoryQueueOf(t *testing.T, issues []issueJSON, want int) ([]int, string) {
	t.Helper()
	gh := newGhShim(t)
	gh.remote(t, "o/r")
	gh.issues(t, "o/r", issues...)
	for _, issue := range issues {
		gh.timeline(t, "o/r", issue["number"].(int))
	}
	f := gh.start(t, config{"poll": "50ms", "repositories": []string{"o/r"}})
	numbers := []int{}
	for _, issue := range f.queue(t, want) {
		numbers = append(numbers, issue.Number)
	}
	request := ""
	for _, call := range gh.calls(t) {
		if strings.HasPrefix(call, "api repos/o/r/issues?labels=") {
			request = call
		}
	}
	if request == "" {
		t.Fatalf("the factory asked GitHub for no issue list; its calls were %v", gh.calls(t))
	}
	return numbers, request
}

// boardFrontierOf runs the real board.sh of the orchestrator plugin against the plugins' gh shim and
// answers with the issues it offers, and with the request it asked GitHub for them.
func boardFrontierOf(t *testing.T, issues []issueJSON) ([]int, string) {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		git := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
		git.Dir, git.Env = repo, gitIsolation()
		if out, err := git.CombinedOutput(); err != nil {
			t.Fatalf("the test repository could not be made: %v: %s", err, out)
		}
	}
	fixture := filepath.Join(dir, "ready.json")
	writeFile(t, fixture, marshal(t, issues))
	calls := filepath.Join(dir, "calls.log")

	board := exec.Command("bash", abs(t, filepath.Join("..", "plugins", "orchestrator", "scripts", "board.sh")))
	board.Dir = repo
	board.Env = append(gitIsolation(),
		"PATH="+abs(t, filepath.Join("..", "tests", "shims"))+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SHIM_FRONTIER_FIXTURE="+fixture, "SHIM_LOG="+calls, "HOME="+dir)
	var said bytes.Buffer
	board.Stderr = &said
	out, err := board.Output()
	if err != nil {
		t.Fatalf("board.sh failed: %v; it is the original this rule is bound to: %s", err, said.String())
	}

	numbers := []int{}
	inFrontier := false
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "frontier[") {
			inFrontier = true
			continue
		}
		if !inFrontier {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			break
		}
		number, err := strconv.Atoi(strings.SplitN(strings.TrimSpace(line), ",", 2)[0])
		if err != nil {
			t.Fatalf("the board offered %q, which does not start with an issue number", line)
		}
		numbers = append(numbers, number)
	}
	request := ""
	for _, call := range strings.Split(readFile(t, calls), "\n") {
		if strings.HasPrefix(call, "gh api repos/o/r/issues?labels=") {
			request = strings.TrimPrefix(call, "gh ")
		}
	}
	if request == "" {
		t.Fatalf("board.sh asked GitHub for no issue list; its calls were:\n%s", readFile(t, calls))
	}
	return numbers, request
}

// ---- the gh shim ----

// ghShim is the gh the factory finds on PATH: canned answers, a log of every call and the local
// repositories a clone comes from (factory/testdata/gh).
type ghShim struct {
	answers  string
	log      string
	remotes  string
	bodies   string // the directory the body of every call that reads one from standard input lands in
	failing  string // the file holding the pattern of requests that fail
	stalling string // the file holding the pattern of requests that are never answered
	hanging  string // the file holding how long a clone sleeps instead of cloning
	gate     string // the file that holds a ref creation until the test lets it through
	worker   string // the log of the claude shim: how every worker was started
	authors  string // the log of the claude shim: how every author session of the pr stage was started
	plugins  string // the log of the claude shim: every plugin and version call before a session
	env      []string
}

func newGhShim(t *testing.T) *ghShim {
	t.Helper()
	dir := t.TempDir()
	g := &ghShim{
		answers:  filepath.Join(dir, "answers"),
		log:      filepath.Join(dir, "calls.log"),
		remotes:  filepath.Join(dir, "remotes"),
		bodies:   filepath.Join(dir, "bodies"),
		failing:  filepath.Join(dir, "failing"),
		stalling: filepath.Join(dir, "stalling"),
		hanging:  filepath.Join(dir, "hanging"),
		gate:     filepath.Join(dir, "gate"),
		worker:   filepath.Join(dir, "workers.log"),
		authors:  filepath.Join(dir, "authors.log"),
		plugins:  filepath.Join(dir, "plugins.log"),
	}
	for _, d := range []string{g.answers, g.remotes} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	g.env = append(gitIsolation(),
		"PATH="+abs(t, "testdata")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+dir, "GH_SHIM_DIR="+g.answers, "GH_SHIM_LOG="+g.log, "GH_SHIM_REMOTES="+g.remotes,
		"GH_SHIM_BODIES="+g.bodies,
		"GH_SHIM_FAIL="+g.failing, "GH_SHIM_STALL="+g.stalling, "GH_SHIM_HANG="+g.hanging,
		"CLAUDE_SHIM_LOG="+g.worker, "CLAUDE_SHIM_AUTHOR_LOG="+g.authors, "CLAUDE_SHIM_PLUGIN_LOG="+g.plugins)
	return g
}

// ciPull is what a pull request is to the ci stage: how it merges, its checks and its reviews. Its
// zero value is a mergeable pull request of the claimed branch whose one check passed long ago, with
// no review and no thread.
type ciPull struct {
	mergeable string           // MERGEABLE when empty
	branch    string           // claimedBranch when empty
	head      string           // the head commit, "c0ffee" when empty
	checks    []map[string]any // one passed check when nil
	reviews   []map[string]any
	threads   []map[string]any
}

// ciReads is the answer to the three reads the ci stage makes of a pull request each time it looks:
// the pull request with its rollup, its reviews and its review threads. A later call replaces them,
// which is how a test moves a pull request on while the factory waits on it.
func (g *ghShim) ciReads(t *testing.T, repository string, number int, p ciPull) {
	t.Helper()
	if p.mergeable == "" {
		p.mergeable = "MERGEABLE"
	}
	if p.branch == "" {
		p.branch = claimedBranch
	}
	if p.head == "" {
		p.head = "c0ffee"
	}
	if p.checks == nil {
		p.checks = []map[string]any{passed("gate")}
	}
	if p.reviews == nil {
		p.reviews = []map[string]any{}
	}
	if p.threads == nil {
		p.threads = []map[string]any{}
	}
	g.answer(t, pullViewCall(repository, number), marshal(t, map[string]any{
		"mergeable": p.mergeable, "headRefName": p.branch, "headRefOid": p.head, "isCrossRepository": false,
		"commits": []map[string]any{{"committedDate": "2026-01-01T00:00:00Z"}}, "statusCheckRollup": p.checks}))
	g.answer(t, "api --paginate "+reviewsRequest(repository, number), marshal(t, p.reviews))
	g.answer(t, "api graphql --input -", marshal(t, map[string]any{"data": map[string]any{"repository": map[string]any{
		"pullRequest": map[string]any{"reviewThreads": map[string]any{"nodes": p.threads}}}}}))
}

// opensPull is the answer to the pull request the pr stage opens: that number of that repository.
// Unanswered, the shim numbers it after the issue of the branch it is opened from.
func (g *ghShim) opensPull(t *testing.T, repository string, number int) {
	t.Helper()
	g.answer(t, createPullCall(repository), marshal(t, map[string]any{"number": number, "draft": false,
		"html_url": fmt.Sprintf("https://github.com/%s/pull/%d", repository, number)}))
}

// createPullCall is the call that opens a pull request, whose body the factory writes to standard input.
func createPullCall(repository string) string {
	return "api --method POST repos/" + repository + "/pulls --input -"
}

// opened is every pull request the pr stage opened on that repository, as the factory sent it.
func (g *ghShim) opened(t *testing.T, repository string) []newPullJSON {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(g.bodies, requestName(createPullCall(repository))))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	out := []newPullJSON{}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	for decoder.More() {
		var p newPullJSON
		if err := decoder.Decode(&p); err != nil {
			t.Fatalf("the factory opened a pull request with a body that is not JSON: %v\n%s", err, raw)
		}
		out = append(out, p)
	}
	return out
}

// newPullJSON is a pull request as the factory asks GitHub to open it.
type newPullJSON struct {
	Title string `json:"title"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Body  string `json:"body"`
	Draft *bool  `json:"draft"`
}

// pullViewCall is the read of a pull request the ci stage judges.
func pullViewCall(repository string, number int) string {
	return fmt.Sprintf("pr view %d --repo %s --json mergeable,headRefName,headRefOid,isCrossRepository,commits,statusCheckRollup", number, repository)
}

// passed, failed and pending are checks of the rollup as GitHub Actions reports them.
func passed(name string) map[string]any {
	return map[string]any{"name": name, "status": "COMPLETED", "conclusion": "SUCCESS", "completedAt": "2026-01-01T00:00:00Z",
		"detailsUrl": "https://github.com/o/r/actions/runs/7/job/1"}
}

func failed(name string, run int) map[string]any {
	return map[string]any{"name": name, "status": "COMPLETED", "conclusion": "FAILURE", "completedAt": "2026-01-01T00:00:00Z",
		"detailsUrl": fmt.Sprintf("https://github.com/o/r/actions/runs/%d/job/1", run)}
}

func pending(name string) map[string]any {
	return map[string]any{"name": name, "status": "IN_PROGRESS", "conclusion": "",
		"detailsUrl": "https://github.com/o/r/actions/runs/9/job/1"}
}

// openPullsListed is the answer to the one read a resumed run makes before it decides where to start: the
// open pull requests of the branch it holds, which are the ones given.
func (g *ghShim) openPullsListed(t *testing.T, repository, branch string, numbers ...int) {
	t.Helper()
	pulls := []map[string]any{}
	for _, n := range numbers {
		pulls = append(pulls, map[string]any{"number": n, "head": map[string]any{"ref": branch, "repo": map[string]any{"full_name": repository}}})
	}
	owner, _, _ := strings.Cut(repository, "/")
	g.answer(t, "api repos/"+repository+"/pulls?state=open&head="+url.QueryEscape(owner+":"+branch)+"&per_page=10", marshal(t, pulls))
}

// start runs the real binary against this shim, paused: it reads the line and claims nothing.
func (g *ghShim) start(t *testing.T, c config) *factory {
	t.Helper()
	return launch(t, c, g.env, "-paused")
}

// work runs the real binary against this shim as a host runs it: unpaused, claiming the head of its
// line and starting the worker of the claude shim on it.
func (g *ghShim) work(t *testing.T, c config) *factory {
	t.Helper()
	return launch(t, c, g.env)
}

// loggedInAs is the user this host's gh answers as, which a claim assigns its issue to.
func (g *ghShim) loggedInAs(t *testing.T, login string) {
	t.Helper()
	g.answer(t, "api user --jq .login", login+"\n")
}

// assigns is the answer to the assignment one claim makes, so an assignment the factory does not
// make exactly that way is a call the shim has no answer for.
func (g *ghShim) assigns(t *testing.T, repository string, issue int, login string) {
	t.Helper()
	g.answer(t, fmt.Sprintf("issue edit %d --repo %s --add-assignee %s", issue, repository, login),
		fmt.Sprintf("https://github.com/%s/issues/%d\n", repository, issue))
}

// comments is the answer to a comment on one issue, so a comment the factory does not make exactly
// that way is a call the shim has no answer for. The body is read from standard input, which is
// where commented reads it back from.
func (g *ghShim) comments(t *testing.T, repository string, issue int) {
	t.Helper()
	g.answer(t, commentCall(repository, issue),
		fmt.Sprintf("https://github.com/%s/issues/%d#issuecomment-1\n", repository, issue))
}

// commented is what the factory wrote in its comments on one issue, all of them in the order it
// made them, and an empty string when it commented nothing.
func (g *ghShim) commented(t *testing.T, repository string, issue int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(g.bodies, requestName(commentCall(repository, issue))))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// reviewRequests is the answer to the review requests the factory makes on a pull request of a run
// that ended ready: one per login, as the factory asks them.
func (g *ghShim) reviewRequests(t *testing.T, url string, logins ...string) {
	t.Helper()
	for _, login := range logins {
		g.answer(t, reviewCall(url, login), url+"\n")
	}
}

// commentCall and reviewCall are the two calls a notification is: a comment on the issue whose body
// the factory writes to standard input, and a review request on the pull request, which names one
// login, because GitHub refuses a whole request that carries one login it will not take.
func commentCall(repository string, issue int) string {
	return fmt.Sprintf("issue comment %d --repo %s --body-file -", issue, repository)
}

func reviewCall(url, login string) string {
	return "pr edit " + url + " --add-reviewer " + login
}

// requestName is the file a request's answer and its body lie under, as the shim names them.
func requestName(request string) string {
	return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(request, "-")
}

// issue is the answer to the reading of one issue, which is the only reading there is of an issue
// this factory holds: it is assigned to this host, so no line GitHub answers with carries it.
func (g *ghShim) issue(t *testing.T, repository string, issue issueJSON) {
	t.Helper()
	g.answer(t, fmt.Sprintf("api repos/%s/issues/%v", repository, issue["number"]), marshal(t, issue))
}

// pull is what became of a pull request of the branch a claim holds its issue by. Merged is a field
// of its own because GitHub calls a merged pull request closed as well.
func (g *ghShim) pull(t *testing.T, repository string, number int, state string, merged bool) {
	t.Helper()
	g.pullOf(t, repository, number, state, merged, repository, claimedBranch)
}

// pullOf is a pull request of another branch or another repository than the one the factory holds
// the issue by: what a report that named the wrong one, or one somebody else's, binds to the run.
func (g *ghShim) pullOf(t *testing.T, repository string, number int, state string, merged bool, head, branch string) {
	t.Helper()
	g.answer(t, fmt.Sprintf("api repos/%s/pulls/%d", repository, number),
		marshal(t, map[string]any{"number": number, "state": state, "merged": merged,
			"head": map[string]any{"ref": branch, "repo": map[string]any{"full_name": head}}}))
}

// pullMergedAt is a pull request of the claimed branch that GitHub reports as merged, with the
// commit the branch was at when it was.
func (g *ghShim) pullMergedAt(t *testing.T, repository string, number int, sha string) {
	t.Helper()
	g.answer(t, fmt.Sprintf("api repos/%s/pulls/%d", repository, number),
		marshal(t, map[string]any{"number": number, "state": "closed", "merged": true,
			"head": map[string]any{"ref": claimedBranch, "sha": sha, "repo": map[string]any{"full_name": repository}}}))
}

// unassigns is the answer to the one edit that takes this host off an issue it lets go, so a removal
// the factory does not make exactly that way is a call the shim has no answer for.
func (g *ghShim) unassigns(t *testing.T, repository string, issue int, login string) {
	t.Helper()
	g.answer(t, fmt.Sprintf("issue edit %d --repo %s --remove-assignee %s", issue, repository, login),
		fmt.Sprintf("https://github.com/%s/issues/%d\n", repository, issue))
}

// workerCommits makes the scripted worker write and commit a file in its worktree, which is the work
// a run leaves behind and the branch carries.
func (g *ghShim) workerCommits(t *testing.T, file string) {
	t.Helper()
	g.env = append(g.env, "CLAUDE_SHIM_COMMIT="+file)
}

// workerWaits makes the scripted worker sit in its worktree instead of reporting, so a test can act
// on a run that is still going.
func (g *ghShim) workerWaits(t *testing.T, how time.Duration) {
	t.Helper()
	g.env = append(g.env, fmt.Sprintf("CLAUDE_SHIM_SLEEP=%d", int(how.Seconds())))
}

// workerReportsBlocked ends the scripted worker's session blocked, which is a run that opened no
// pull request: the issue is the factory's until somebody decides, and it has nothing to show. The
// blocker's text is what the factory's notification carries.
func (g *ghShim) workerReportsBlocked(t *testing.T, reason string) {
	t.Helper()
	g.workerResults(t, map[string]any{"outcome": "blocked", "summary": reason})
}

// workerResults is the structured result the scripted worker ends its session with, as a session
// prints it on its result line, whether or not it fits the schema.
func (g *ghShim) workerResults(t *testing.T, output any) {
	t.Helper()
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	g.env = append(g.env, "CLAUDE_SHIM_RESULT="+string(raw))
}

// workerPrintsNoResult ends the scripted worker's session well and without a result line.
func (g *ghShim) workerPrintsNoResult(t *testing.T) {
	t.Helper()
	g.env = append(g.env, "CLAUDE_SHIM_NO_RESULT=1")
}

// installs is what the claude shim answers about this host: the version of the worker plugin its
// user scope holds, and what `claude --version` prints — an empty one being a binary that printed
// no version at all.
func (g *ghShim) installs(t *testing.T, worker, claudeCode string) {
	t.Helper()
	g.env = append(g.env, "CLAUDE_SHIM_WORKER_VERSION="+worker, "CLAUDE_SHIM_VERSION="+claudeCode)
}

// switchedOff makes the worker plugin of the claude shim's user scope an install that is there but
// disabled, which is a host no session of it runs the worker from.
func (g *ghShim) switchedOff(t *testing.T) {
	t.Helper()
	g.env = append(g.env, "CLAUDE_SHIM_WORKER_ENABLED=false")
}

// updatesHang makes every plugin call of the claude shim wait to be ended instead of answering, as a
// host whose line hangs. The call is logged before it waits, so pluginCalls says when it began.
func (g *ghShim) updatesHang(t *testing.T) {
	t.Helper()
	g.env = append(g.env, "CLAUDE_SHIM_PLUGIN_HANG=300")
}

// updatesAnswerAgain takes that hang off, so the next factory started from this shim is answered.
func (g *ghShim) updatesAnswerAgain(t *testing.T) {
	t.Helper()
	answering := g.env[:0]
	for _, entry := range g.env {
		if !strings.HasPrefix(entry, "CLAUDE_SHIM_PLUGIN_HANG=") {
			answering = append(answering, entry)
		}
	}
	g.env = answering
}

// updatesFail makes every plugin update of the claude shim fail with that sentence, as a host whose
// line is down meets it. What is installed can still be read, which is the state such a run uses.
func (g *ghShim) updatesFail(t *testing.T, said string) {
	t.Helper()
	g.env = append(g.env, "CLAUDE_SHIM_PLUGIN_FAIL="+said)
}

// pluginCalls is every plugin and version call the factory made of claude, in the order it made
// them, one line per call.
func (g *ghShim) pluginCalls(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(g.plugins)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			calls = append(calls, line)
		}
	}
	return calls
}

// holdClaims makes every ref creation wait inside the shim until openClaims lets it through, so two
// claimers can be brought into the one act that decides which of them owns the issue.
func (g *ghShim) holdClaims(t *testing.T) {
	t.Helper()
	g.env = append(g.env, "GH_SHIM_GATE="+g.gate)
}

// claimersWaiting is how many claimers are inside the ref creation the gate holds. Each of them
// leaves a file named after its process, which the file that opens the gate is not.
func (g *ghShim) claimersWaiting(t *testing.T) int {
	t.Helper()
	waiting, err := filepath.Glob(g.gate + ".[0-9]*")
	if err != nil {
		t.Fatal(err)
	}
	return len(waiting)
}

// openClaims lets the held ref creations through, all of them at once.
func (g *ghShim) openClaims(t *testing.T) {
	t.Helper()
	writeFile(t, g.gate+".open", "")
}

// answer is what the shim says to one request. The file is named after the request, as the shim
// names it.
func (g *ghShim) answer(t *testing.T, request, body string) {
	t.Helper()
	writeFile(t, filepath.Join(g.answers, requestName(request)), body)
}

// issues is the issue list of one repository, as GitHub's list endpoint answers it.
func (g *ghShim) issues(t *testing.T, repository string, issues ...issueJSON) {
	t.Helper()
	if issues == nil {
		issues = []issueJSON{}
	}
	g.answer(t, "api "+issuesRequest(repository, "factory"), marshal(t, issues))
}

// timeline is the event list of one issue, which is where the time the routing label was set is read.
func (g *ghShim) timeline(t *testing.T, repository string, issue int, events ...map[string]any) {
	t.Helper()
	if events == nil {
		events = []map[string]any{}
	}
	g.answer(t, "api --paginate "+eventsRequest(repository, issue), marshal(t, events))
}

// fail makes every request that matches the shell pattern fail, as an unreachable GitHub does, and
// an empty pattern makes GitHub reachable again. It takes effect on the next call, so a test can
// break GitHub under a running factory.
func (g *ghShim) fail(t *testing.T, pattern string) {
	t.Helper()
	writeFile(t, g.failing, pattern)
}

// stall makes every request that matches the shell pattern wait instead of answering, as a GitHub
// that takes a call and says nothing does. It leaves the factory standing inside one act of a claim,
// which is where a test reads what that claim had written down by then.
func (g *ghShim) stall(t *testing.T, pattern string) {
	t.Helper()
	writeFile(t, g.stalling, pattern)
}

// hang makes `gh repo clone` sleep in a child of its own instead of cloning, as a clone of a large
// repository does, and answers with the file that child's pid is written to.
func (g *ghShim) hang(t *testing.T, how time.Duration) string {
	t.Helper()
	writeFile(t, g.hanging, strconv.Itoa(int(how.Seconds())))
	return g.hanging + ".pid"
}

// remote is a repository on the shim's GitHub: what `gh repo clone owner/name` clones from and what
// a claim creates its branch in. It is bare, as the remote of the workflow is: a claim writes a
// reference into it and a worker pushes to it, and neither may meet a checked-out branch.
func (g *ghShim) remote(t *testing.T, repository string) {
	t.Helper()
	dir := filepath.Join(g.remotes, strings.ReplaceAll(repository, "/", "-"))
	// A host whose file system does not tell two spellings of one name apart has the repository
	// already, which is what GitHub answers for either spelling too.
	if _, err := os.Stat(dir); err == nil {
		return
	}
	work := dir + ".work"
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "README.md"), "# "+repository+"\n")
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"commit", "-q", "-m", "init"}} {
		g.git(t, work, args...)
	}
	g.git(t, work, "clone", "-q", "--bare", work, dir)
	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
}

// git runs one git command for the test's own bookkeeping, isolated from the host's configuration.
func (g *ghShim) git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	git := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
	git.Dir, git.Env = dir, gitIsolation()
	var said bytes.Buffer
	git.Stderr = &said
	out, err := git.Output()
	if err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, said.String())
	}
	return strings.TrimSpace(string(out))
}

// remotePath is where the shim's GitHub keeps one repository.
func (g *ghShim) remotePath(repository string) string {
	return filepath.Join(g.remotes, strings.ReplaceAll(repository, "/", "-"))
}

// head is the commit a branch of the shim's GitHub points at, and an empty string when there is no
// such branch — which is what a claim that nobody won looks like from outside.
func (g *ghShim) head(t *testing.T, repository, branch string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", g.remotePath(repository), "rev-parse", "--verify", "--quiet", "refs/heads/"+branch).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// branchAt makes a branch of the shim's GitHub point at another one, as a second line of work on the
// remote does.
func (g *ghShim) branchAt(t *testing.T, repository, branch, at string) {
	t.Helper()
	g.git(t, g.remotePath(repository), "update-ref", "refs/heads/"+branch, at)
}

// defaultBranchIs points the head of a repository on the shim's GitHub at one of its branches, as a
// repository that moves its line of work does.
func (g *ghShim) defaultBranchIs(t *testing.T, repository, branch string) {
	t.Helper()
	g.git(t, g.remotePath(repository), "symbolic-ref", "HEAD", "refs/heads/"+branch)
}

// dropBranch removes a branch from the shim's GitHub, which is what a clone made before it still
// remembers until it prunes.
func (g *ghShim) dropBranch(t *testing.T, repository, branch string) {
	t.Helper()
	g.git(t, g.remotePath(repository), "update-ref", "-d", "refs/heads/"+branch)
}

// commitOn puts one more commit on a branch of the shim's GitHub, which is what a clone made before
// it has yet to see.
func (g *ghShim) commitOn(t *testing.T, repository, branch string) string {
	t.Helper()
	dir := g.remotePath(repository)
	tree := g.git(t, dir, "rev-parse", "refs/heads/"+branch+"^{tree}")
	parent := g.git(t, dir, "rev-parse", "refs/heads/"+branch)
	commit := g.git(t, dir, "commit-tree", tree, "-p", parent, "-m", "work on "+branch)
	g.git(t, dir, "update-ref", "refs/heads/"+branch, commit)
	return commit
}

// cloneInto makes the clone of a repository in a data directory before the factory starts, so a test
// can let the remote move on afterwards: a factory that finds its clone does not clone again, and
// what it works from then depends on its own fetch.
func (g *ghShim) cloneInto(t *testing.T, dataDir, repository string) string {
	t.Helper()
	dir := clonePath(dataDir, repository)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	g.git(t, filepath.Dir(dir), "clone", "-q", g.remotePath(repository), dir)
	return dir
}

func (g *ghShim) calls(t *testing.T) []string {
	t.Helper()
	calls := []string{}
	raw, err := os.ReadFile(g.log)
	if os.IsNotExist(err) {
		return calls // the factory has not called gh yet, which is a reading and not a failure
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range strings.Split(string(raw), "\n") {
		if call != "" {
			calls = append(calls, call)
		}
	}
	return calls
}

// cloned counts how often the factory cloned one repository. Where it cloned it to is not part of
// the count: a clone lands beside its place and is moved in when it is whole.
func (g *ghShim) cloned(t *testing.T, repository string) int {
	t.Helper()
	count := 0
	for _, call := range g.calls(t) {
		if strings.HasPrefix(call, "repo clone "+repository+" ") {
			count++
		}
	}
	return count
}

// made counts how often the factory made one request.
func (g *ghShim) made(t *testing.T, request string) int {
	t.Helper()
	count := 0
	for _, call := range g.calls(t) {
		if call == request {
			count++
		}
	}
	return count
}

// asked counts the requests that begin this way, for the calls whose tail is a commit and so differs
// from run to run.
func (g *ghShim) asked(t *testing.T, prefix string) int {
	t.Helper()
	count := 0
	for _, call := range g.calls(t) {
		if strings.HasPrefix(call, prefix) {
			count++
		}
	}
	return count
}

// ---- the issues the shim answers with ----

// issueJSON is one issue as GitHub's issue list carries it. The names are GitHub's, because they are
// the contract the frontier rule reads.
type issueJSON map[string]any

// openIssue is an issue nothing has touched since it was opened. Its updated_at is the issue's own:
// GitHub touches it whenever a label changes, and the factory holds what it remembers against it.
func openIssue(number int, title string, created time.Time, labels ...string) issueJSON {
	if labels == nil {
		labels = []string{readyLabel, "factory"}
	}
	named := []any{}
	for _, label := range labels {
		named = append(named, map[string]any{"name": label})
	}
	return issueJSON{
		"number": number, "title": title, "state": "open",
		"created_at": created.Format(time.RFC3339), "updated_at": created.Format(time.RFC3339),
		"assignees": []any{}, "labels": named,
		"issue_dependencies_summary": map[string]any{"blocked_by": 0, "blocking": 0},
	}
}

func labeled(label string, at time.Time) map[string]any {
	return map[string]any{"event": "labeled", "created_at": at.Format(time.RFC3339), "label": map[string]any{"name": label}}
}

func unlabeled(label string, at time.Time) map[string]any {
	return map[string]any{"event": "unlabeled", "created_at": at.Format(time.RFC3339), "label": map[string]any{"name": label}}
}

// assigned is the entry GitHub writes when somebody puts an assignee on an issue. Every claim makes
// one, and it is what the removal below is held against.
func assigned(login string, at time.Time) map[string]any {
	return map[string]any{"event": "assigned", "created_at": at.Format(time.RFC3339),
		"assignee": map[string]any{"login": login}}
}

// unassigned is the entry GitHub writes when somebody takes an assignee off an issue, which for an
// issue the factory holds is the release signal.
func unassigned(login string, at time.Time) map[string]any {
	return map[string]any{"event": "unassigned", "created_at": at.Format(time.RFC3339),
		"assignee": map[string]any{"login": login}}
}

// assignedTo is an issue this factory holds: the claim put its login on it, which is what takes the
// issue out of the line GitHub answers with and leaves this reading as the only one of it.
func assignedTo(issue issueJSON, login string) issueJSON {
	issue["assignees"] = []any{map[string]any{"login": login}}
	return issue
}

// closedIssue is an issue somebody has closed, which is one decision that ends the factory's part.
func closedIssue(issue issueJSON) issueJSON {
	issue["state"] = "closed"
	return issue
}

// touched is an issue somebody has changed since it was opened. The factory holds what it remembers
// of an issue's events against its updated_at, so an issue whose timeline changed has to say so.
func touched(issue issueJSON, at time.Time) issueJSON {
	issue["updated_at"] = at.Format(time.RFC3339)
	return issue
}

// ---- reading what the factory serves ----

// queue waits for a poll and then until the line holds as many entries as the test expects, and
// answers with it. The poll is waited for first, so that an empty line is one GitHub was asked for.
func (f *factory) queue(t *testing.T, want int) []apiIssue {
	t.Helper()
	f.eventually(t, 20*time.Second, "the first poll", func() bool {
		var status map[string]any
		f.get(t, "/api/status", &status)
		return status["polledAt"] != "0001-01-01T00:00:00Z"
	})
	var line apiLine
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		line = apiLine{}
		f.get(t, "/api/line", &line)
		if len(line.Queue) == want {
			return line.Queue
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the line holds %v, want %d routed issue(s)", keys(line.Queue), want)
	return nil
}

// repositorySaid is what /api/repositories carries as the error of one repository, empty when it
// carries none.
func (f *factory) repositorySaid(t *testing.T, repository string) string {
	t.Helper()
	var rows []struct {
		Repository string `json:"repository"`
		Queued     int    `json:"queued"`
		Error      string `json:"error"`
	}
	f.get(t, "/api/repositories", &rows)
	for _, row := range rows {
		if row.Repository == repository {
			return row.Error
		}
	}
	t.Fatalf("/api/repositories does not carry %s at all", repository)
	return ""
}

func keys(issues []apiIssue) []string {
	out := []string{}
	for _, issue := range issues {
		out = append(out, issue.Repository+"#"+strconv.Itoa(issue.Number))
	}
	return out
}

func equal[T comparable](got, want []T) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// ---- small helpers ----

// gitIsolation keeps the host's git configuration — a global ignore file, hooks, a signing key — out
// of what these tests do.
func gitIsolation() []string {
	env := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch {
		case strings.HasPrefix(name, "GH_SHIM_"), strings.HasPrefix(name, "SHIM_"),
			strings.HasPrefix(name, "HERDR_"), strings.HasPrefix(name, "WF_"),
			strings.HasPrefix(name, "GIT_CONFIG"):
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
}

func abs(t *testing.T, path string) string {
	t.Helper()
	out, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	out := []string{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
