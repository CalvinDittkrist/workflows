package main

import (
	"bytes"
	"encoding/json"
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
	// is tried again, because connecting it is a line in the configuration and a restart.
	f.stop(t, syscall.SIGTERM)
	gh.start(t, config{"poll": "50ms", "data_dir": data}).queue(t, 0)
	if cloned := gh.made(t, "repo clone acme/edge-sensors "+filepath.Join(data, "repos", "acme", "edge-sensors")); cloned != 1 {
		t.Errorf("the repository was cloned %d times, want once: a clone that is there is kept", cloned)
	}
}

func TestARepositoryThatCannotBeReadDoesNotEmptyTheLineOfTheOthers(t *testing.T) {
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.remote(t, "acme/backtest")
	gh.issues(t, "acme/edge-sensors", openIssue(104, "Retry the upload", now.Add(-72*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 104, labeled("factory", now.Add(-6*time.Hour)))
	gh.fail(t, "api repos/acme/backtest/*") // that repository answers as an unreachable GitHub does

	f := gh.start(t, config{"poll": "50ms"})
	queue := f.queue(t, 1)
	if got := keys(queue); !equal(got, []string{"acme/edge-sensors#104"}) {
		t.Errorf("the line is %v, want the repository that could be read", got)
	}
	f.eventually(t, 10*time.Second, "the error line for the repository that could not be read", func() bool {
		return strings.Contains(f.output(t), "error: the routed issues of acme/backtest could not be read")
	})
}

func TestPausedAgainstGitHubShowsTheLineAndClaimsNothing(t *testing.T) {
	gh := newGhShim(t)
	now := time.Now().UTC()
	gh.remote(t, "acme/edge-sensors")
	gh.issues(t, "acme/edge-sensors", openIssue(104, "Retry the upload", now.Add(-72*time.Hour)))
	gh.timeline(t, "acme/edge-sensors", 104, labeled("factory", now.Add(-6*time.Hour)))

	f := gh.start(t, config{"poll": "50ms", "repositories": []string{"acme/edge-sensors"}})
	f.queue(t, 1)

	var status map[string]any
	f.get(t, "/api/status", &status)
	if status["state"] != "paused" {
		t.Errorf("the factory says it is %q, want paused", status["state"])
	}
	if records, _ := filepath.Glob(filepath.Join(f.data, "run-*.json")); len(records) != 0 {
		t.Errorf("paused, the factory wrote %d run records, want none", len(records))
	}
	// Nothing it did to GitHub is a claim: it reads the line and clones, and writes nothing at all.
	for _, call := range gh.calls(t) {
		if !strings.HasPrefix(call, "api repos/") && !strings.HasPrefix(call, "api --paginate repos/") && !strings.HasPrefix(call, "repo clone ") {
			t.Errorf("the factory called `gh %s`; paused it reads and claims nothing", call)
		}
	}
}

func TestAgainstRealGitHubTheFactoryRefusesToRunUnpaused(t *testing.T) {
	gh := newGhShim(t)
	path := writeConfig(t, config{"listen": freeAddress(t), "data_dir": filepath.Join(t.TempDir(), "data"),
		"repositories": []string{"acme/edge-sensors"}})
	started := exec.Command(binary, "-config", path)
	started.Env = gh.env
	output, err := started.CombinedOutput()
	if err == nil {
		t.Fatalf("the factory started unpaused against real GitHub; it cannot work an issue yet")
	}
	if !strings.HasPrefix(string(output), "error: ") || !strings.Contains(string(output), "-paused") {
		t.Errorf("it said %q, want an error line naming the fix", strings.TrimSpace(string(output)))
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
	answers string
	log     string
	remotes string
	env     []string
}

func newGhShim(t *testing.T) *ghShim {
	t.Helper()
	dir := t.TempDir()
	g := &ghShim{
		answers: filepath.Join(dir, "answers"),
		log:     filepath.Join(dir, "calls.log"),
		remotes: filepath.Join(dir, "remotes"),
	}
	for _, d := range []string{g.answers, g.remotes} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	g.env = append(gitIsolation(),
		"PATH="+abs(t, "testdata")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+dir, "GH_SHIM_DIR="+g.answers, "GH_SHIM_LOG="+g.log, "GH_SHIM_REMOTES="+g.remotes)
	return g
}

// start runs the real binary against this shim: against real GitHub, which so far means paused.
func (g *ghShim) start(t *testing.T, c config) *factory {
	t.Helper()
	return launch(t, c, g.env, "-paused")
}

// answer is what the shim says to one request. The file is named after the request, as the shim
// names it.
func (g *ghShim) answer(t *testing.T, request, body string) {
	t.Helper()
	writeFile(t, filepath.Join(g.answers, regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(request, "-")), body)
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

// fail makes every request that matches the shell pattern fail, as an unreachable GitHub does.
func (g *ghShim) fail(t *testing.T, pattern string) {
	t.Helper()
	g.env = append(g.env, "GH_SHIM_FAIL="+pattern)
}

// remote is a repository on the shim's GitHub: what `gh repo clone owner/name` clones from.
func (g *ghShim) remote(t *testing.T, repository string) {
	t.Helper()
	dir := filepath.Join(g.remotes, strings.ReplaceAll(repository, "/", "-"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "README.md"), "# "+repository+"\n")
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"commit", "-q", "-m", "init"}} {
		git := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
		git.Dir, git.Env = dir, gitIsolation()
		if out, err := git.CombinedOutput(); err != nil {
			t.Fatalf("the remote repository %s could not be made: %v: %s", repository, err, out)
		}
	}
}

func (g *ghShim) calls(t *testing.T) []string {
	t.Helper()
	calls := []string{}
	for _, call := range strings.Split(readFile(t, g.log), "\n") {
		if call != "" {
			calls = append(calls, call)
		}
	}
	return calls
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

// ---- the issues the shim answers with ----

// issueJSON is one issue as GitHub's issue list carries it. The names are GitHub's, because they are
// the contract the frontier rule reads.
type issueJSON map[string]any

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
		"created_at": created.Format(time.RFC3339), "assignees": []any{}, "labels": named,
		"issue_dependencies_summary": map[string]any{"blocked_by": 0, "blocking": 0},
	}
}

func labeled(label string, at time.Time) map[string]any {
	return map[string]any{"event": "labeled", "created_at": at.Format(time.RFC3339), "label": map[string]any{"name": label}}
}

func unlabeled(label string, at time.Time) map[string]any {
	return map[string]any{"event": "unlabeled", "created_at": at.Format(time.RFC3339), "label": map[string]any{"name": label}}
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
