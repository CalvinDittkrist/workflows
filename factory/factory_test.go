package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"
)

// The tests start the real binary and watch it from outside: through its HTTP interface, through the
// files in its data directory and through the processes it leaves or does not leave on the machine.
// They read the interface as a reader of it does, by its field names, not through the Go types.
// TestTheReportIsReadFromMarkdown is the one exception: the markdown a worker's final report can be
// written in has more shapes than a scripted worker can end in, and they are cheapest to pin here.

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "factory-binary-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: no temporary directory for the build:", err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "factory")
	args := []string{"build"}
	if raceEnabled {
		args = append(args, "-race")
	}
	build := exec.Command("go", append(args, "-o", binary, ".")...)
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error: the factory could not be built:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// What the HTTP interface promises, read by the names it serves.
type apiRun struct {
	ID          int        `json:"id"`
	Repository  string     `json:"repository"`
	Issue       int        `json:"issue"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	Stage       string     `json:"stage"`
	Stages      []string   `json:"stages"`
	Outcome     string     `json:"outcome"`
	PullRequest string     `json:"pullRequest"`
	Reason      string     `json:"reason"`
	StartedAt   time.Time  `json:"startedAt"`
	EndedAt     *time.Time `json:"endedAt"`
	Turns       int        `json:"turns"`
	CostUSD     float64    `json:"costUsd"`
	ContextPeak int        `json:"contextPeak"`
	Tokens      struct {
		Input         int `json:"input"`
		Output        int `json:"output"`
		CacheCreation int `json:"cacheCreation"`
		CacheRead     int `json:"cacheRead"`
	} `json:"tokens"`
	ExitCode   *int     `json:"exitCode"`
	EventCount int      `json:"eventCount"`
	Warnings   []string `json:"warnings"`
	Versions   struct {
		Worker     string `json:"worker"`
		ClaudeCode string `json:"claudeCode"`
		Factory    string `json:"factory"`
	} `json:"versions"`
	Events []struct {
		Seq   int    `json:"seq"`
		Kind  string `json:"kind"`
		Title string `json:"title"`
		Body  string `json:"body"`
		Sub   bool   `json:"sub"`
	} `json:"events"`
}

type apiIssue struct {
	Repository string    `json:"repository"`
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	RoutedAt   time.Time `json:"routedAt"`
}

type apiLine struct {
	Now   []apiRun   `json:"now"`
	Queue []apiIssue `json:"queue"`
	Done  []apiRun   `json:"done"`
}

func TestFakeModeWorksTheCannedQueueOneRunAtATime(t *testing.T) {
	// The deadline has to be far above what a scripted run costs — a binary built with the race
	// detector pays about a second on every exit — or a quick run would be read as a timeout.
	f := start(t, config{"deadline": "15s", "poll": "100ms"})

	var line apiLine
	f.eventually(t, 60*time.Second, "the whole canned queue to be done", func() bool {
		line = apiLine{}
		f.get(t, "/api/line", &line)
		return len(line.Queue) == 0 && len(line.Now) == 0 && len(line.Done) == len(cannedIssues)
	})

	// The order of the queue is the order the routing label was set in, oldest first, and the entries
	// of both connected repositories stand in one line.
	worked := []string{}
	for _, run := range line.Done {
		worked = append(worked, fmt.Sprintf("%s#%d", run.Repository, run.Issue))
	}
	want := []string{"acme/edge-sensors#104", "acme/backtest#109", "acme/edge-sensors#112",
		"acme/edge-sensors#115", "acme/edge-sensors#121", "acme/backtest#118"}
	if strings.Join(worked, " ") != strings.Join(want, " ") {
		t.Errorf("the queue was worked as %v, want %v", worked, want)
	}
	for i, run := range line.Done {
		if run.ID != i+1 {
			t.Errorf("run %d of the line has the id %d, want %d", i, run.ID, i+1)
		}
		if i > 0 && run.StartedAt.Before(*line.Done[i-1].EndedAt) {
			t.Errorf("run %d started at %s, before run %d ended at %s: the factory ran two workers at once",
				run.ID, run.StartedAt, line.Done[i-1].ID, line.Done[i-1].EndedAt)
		}
	}

	ready, blocked, failed := line.Done[0], line.Done[1], line.Done[2]
	silent, detached, timeout := line.Done[3], line.Done[4], line.Done[5]

	// ready: the pull request comes from a final report written as markdown, the stages from the
	// skill calls, and the totals from the result line.
	if ready.Outcome != "ready" || ready.PullRequest != "https://github.com/acme/edge-sensors/pull/204" {
		t.Errorf("run 1 ended %q with the pull request %q, want ready with the pull request of the report", ready.Outcome, ready.PullRequest)
	}
	if got := strings.Join(ready.Stages, " "); got != "implement review pr ci reviews" {
		t.Errorf("run 1 went through the stages %q, want %q", got, "implement review pr ci reviews")
	}
	// The context peak is the fullest one message of the worker itself came. The scripted session
	// hands the pull request to a fresh context halfway through, so the peak stands at the message
	// before that handover: neither the last message, which carries less, nor the far larger context
	// its subagents report, which says nothing about the worker's.
	const readyPeak = 87_400 // the eleventh message of the worker, the one before the handover
	if ready.ContextPeak != readyPeak {
		t.Errorf("run 1 peaked at %d tokens of context, want %d: the fullest message of the worker itself, taken before the handover dropped it and never from the %d a subagent reported",
			ready.ContextPeak, readyPeak, subagentContext)
	}
	if ready.Turns != 23 || ready.CostUSD != 4.18 || ready.Tokens.Output != 24800 || ready.Tokens.CacheRead != 1204000 {
		t.Errorf("run 1 has turns %d, cost %v and tokens %+v, want the totals of the result line", ready.Turns, ready.CostUSD, ready.Tokens)
	}
	// Shape, not behaviour: nothing fills warnings or versions yet, and the record carries the fields
	// from the start so the tickets that fill them change no reader.
	if ready.Warnings == nil || len(ready.Warnings) != 0 || ready.Versions != (struct {
		Worker     string `json:"worker"`
		ClaudeCode string `json:"claudeCode"`
		Factory    string `json:"factory"`
	}{}) {
		t.Errorf("run 1 has warnings %v and versions %+v, want both empty until they are filled", ready.Warnings, ready.Versions)
	}

	// blocked: the reason is the report, to its end.
	if blocked.Outcome != "blocked" || !strings.Contains(blocked.Reason, "ADR 0012") || !strings.Contains(blocked.Reason, "decision needed:") {
		t.Errorf("run 2 ended %q because %q, want blocked with the whole reason of the report", blocked.Outcome, blocked.Reason)
	}
	if blocked.PullRequest != "" {
		t.Errorf("run 2 names the pull request %q, want none", blocked.PullRequest)
	}

	// failed: the session ended in an error, and the record says what the error was.
	if failed.Outcome != "failed" || failed.ExitCode == nil || *failed.ExitCode != 1 {
		t.Errorf("run 3 ended %q with the exit code %v, want failed with exit 1", failed.Outcome, failed.ExitCode)
	}
	if !strings.Contains(failed.Reason, "529") {
		t.Errorf("run 3 failed because %q, want the error the session printed", failed.Reason)
	}

	// silent: a session that ended by itself without reporting is failed, and says so.
	if silent.Outcome != "failed" || !strings.Contains(silent.Reason, "without a report") {
		t.Errorf("run 4 ended %q because %q, want failed because it reported nothing", silent.Outcome, silent.Reason)
	}
	if silent.ExitCode == nil || *silent.ExitCode != 0 {
		t.Errorf("run 4 has the exit code %v, want 0: the session itself ended well", silent.ExitCode)
	}
	// Its tool call carries a file far longer than an event keeps: the log holds the head and says so.
	var withBody apiRun
	f.get(t, "/api/runs/4", &withBody)
	truncated := 0
	for _, e := range withBody.Events {
		if strings.HasSuffix(e.Body, "[truncated]") {
			truncated++
			if len(e.Body) > maxEventBody+len("\n[truncated]") {
				t.Errorf("a truncated event body is %d bytes long, want at most %d", len(e.Body), maxEventBody)
			}
		}
	}
	if truncated != 1 {
		t.Errorf("run 4 logged %d truncated events, want the one over-long tool call", truncated)
	}
	logged := map[string]bool{}
	for _, e := range withBody.Events {
		logged[e.Title] = true
	}
	for _, title := range []string{"tool error", "the worker printed a line that is not the stream format"} {
		if !logged[title] {
			t.Errorf("run 4 did not log %q, want what went wrong in the stream to be in the log", title)
		}
	}

	// detached: the worker reported and left a process behind that has a session of its own, so the
	// process group does not reach it, and that still holds the worker's output. The run ends by its
	// report all the same, long before that process does, and says what it left behind.
	var full apiRun
	f.get(t, "/api/runs/5", &full)
	for _, e := range full.Events {
		if match := detachedPidInEvent.FindStringSubmatch(e.Title); match != nil {
			pid, _ := strconv.Atoi(match[1])
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
		}
	}
	if detached.Outcome != "ready" || detached.PullRequest != "https://github.com/acme/edge-sensors/pull/221" {
		t.Errorf("run 5 ended %q with the pull request %q, want ready by its report", detached.Outcome, detached.PullRequest)
	}
	if took := detached.EndedAt.Sub(detached.StartedAt); took > daemonLifetime/4 {
		t.Errorf("run 5 took %s, want it to end without waiting for the process its worker left behind", took)
	}
	if len(detached.Warnings) != 1 || !strings.Contains(detached.Warnings[0], "left a process behind") {
		t.Errorf("run 5 has the warnings %q, want the one that says its worker left a process behind", detached.Warnings)
	}

	// timeout: the deadline passed and the whole process group was ended.
	if timeout.Outcome != "timeout" || !strings.Contains(timeout.Reason, "deadline of 15s") {
		t.Errorf("run 6 ended %q because %q, want timeout on the deadline", timeout.Outcome, timeout.Reason)
	}
	full = apiRun{} // a field the interface omits would keep the value of the run read before
	f.get(t, "/api/runs/6", &full)
	pids := workerPids(t, full)
	if len(pids) != 2 {
		t.Fatalf("the scripted worker of run 6 logged %d processes, want the worker and its child", len(pids))
	}
	for _, pid := range pids {
		if survived(pid) {
			t.Errorf("process %d of the worker survived the deadline", pid)
		}
	}

	// The run is one JSON record and one append-only JSONL event log in the data directory.
	for _, run := range line.Done {
		record := map[string]any{}
		read(t, filepath.Join(f.data, fmt.Sprintf("run-%d.json", run.ID)), &record)
		if record["outcome"] != run.Outcome || record["state"] != "ended" {
			t.Errorf("the record of run %d says %v/%v, the interface says %s/%s", run.ID, record["state"], record["outcome"], run.State, run.Outcome)
		}
		if lines := countLines(t, filepath.Join(f.data, fmt.Sprintf("run-%d.events.jsonl", run.ID))); lines != run.EventCount {
			t.Errorf("run %d has %d events in its log and %d in its record", run.ID, lines, run.EventCount)
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(f.data, "*")); len(entries) != 2*len(cannedIssues) {
		t.Errorf("the data directory holds %d files, want one record and one event log per run", len(entries))
	}

	// A subagent's events are told apart from the worker's own by the tool-use id they carry.
	full = apiRun{} // a field the interface omits would keep the value of the run read before
	f.get(t, "/api/runs/1", &full)
	subs, agents := 0, 0
	for _, e := range full.Events {
		if e.Sub {
			subs++
		}
		if strings.HasPrefix(e.Title, "Agent worker:") {
			agents++
		}
	}
	if agents != 6 || subs != agents {
		t.Errorf("run 1 logged %d Agent calls and %d subagent events, want six reviewers and authors and one event each", agents, subs)
	}
	if len(full.Events) != full.EventCount {
		t.Errorf("run 1 served %d events for an event count of %d", len(full.Events), full.EventCount)
	}
	var tail apiRun
	f.get(t, "/api/runs/1?after=20", &tail)
	if len(tail.Events) != full.EventCount-20 {
		t.Fatalf("after=20 served %d events, want the %d after the twentieth", len(tail.Events), full.EventCount-20)
	}
	if tail.Events[0].Seq != 21 {
		t.Errorf("after=20 starts at event %d, want 21", tail.Events[0].Seq)
	}
}

func TestTheInterfaceIsReadOnly(t *testing.T) {
	f := start(t, config{"paused": true})
	for _, path := range []string{"/", "/api/status", "/api/line", "/api/runs/1"} {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			response := f.do(t, method, path)
			if response.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s answered %d, want 405: the interface has no writing endpoint", method, path, response.StatusCode)
			}
			if allow := response.Header.Get("Allow"); allow != "GET, HEAD" {
				t.Errorf("%s %s allows %q, want %q", method, path, allow, "GET, HEAD")
			}
			// A refused answer is an answer a browser reads, so it is hardened like every other one.
			if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("%s %s answers X-Content-Type-Options: %q, want %q", method, path, got, "nosniff")
			}
			response.Body.Close()
		}
	}
	// /api and /api/ are the same index; nothing else under /api is.
	for path, want := range map[string]int{"/api": http.StatusOK, "/api/": http.StatusOK,
		"/api/nothing": http.StatusNotFound, "/api/runs/1": http.StatusNotFound, "/nothing": http.StatusNotFound} {
		response := f.do(t, "GET", path)
		if response.StatusCode != want {
			t.Errorf("GET %s answered %d, want %d", path, response.StatusCode, want)
		}
		response.Body.Close()
	}
}

// The host runs one process. The dashboard is built into the binary, so there is no web server and
// no Node process beside it, and the browser that opens the factory gets everything from it.
func TestTheBinaryServesTheDashboardFromItself(t *testing.T) {
	f := start(t, config{"paused": true})

	page, contentType := f.page(t, "/")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("GET / is served as %q, want HTML", contentType)
	}
	if !strings.Contains(page, `<div id="root">`) {
		t.Fatalf("GET / answered %.120q, want the dashboard; it is built into the binary with make ui", page)
	}

	// Everything the page asks for afterwards comes out of the binary too, the self-hosted font last.
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(page, -1)
	if len(assets) < 2 {
		t.Fatalf("the dashboard names %d built assets, want at least its script and its stylesheet", len(assets))
	}
	fonts := 0
	for _, asset := range assets {
		body, _ := f.page(t, asset[1])
		for _, font := range regexp.MustCompile(`url\((/assets/[^)]+\.woff2)\)`).FindAllStringSubmatch(body, -1) {
			f.page(t, font[1])
			fonts++
		}
	}
	if fonts == 0 {
		t.Error("the dashboard's assets name no font file, want JetBrains Mono served by the factory itself")
	}

	// The terminal reader keeps its own page: the endpoints are listed under /api.
	endpoints, _ := f.page(t, "/api")
	if !strings.Contains(endpoints, "GET /api/line") {
		t.Errorf("GET /api answered %.120q, want the list of endpoints", endpoints)
	}

	// The page loads from this binary and from nowhere else, and no foreign page may frame it or
	// read what it shows.
	response := f.do(t, "GET", "/")
	response.Body.Close()
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := response.Header.Get(header); got != want {
			t.Errorf("GET / answers %s: %q, want %q", header, got, want)
		}
	}
	policy := response.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("the content security policy is %q, want %q in it", policy, want)
		}
	}
}

// A clone that has not run make ui builds a factory whose interface works and whose dashboard is
// not there. It says so, in the one place a reader would look.
func TestABinaryWithoutTheDashboardSaysHowToBuildIt(t *testing.T) {
	server := httptest.NewServer(serveDashboard(fstest.MapFS{"robots.txt": &fstest.MapFile{}}))
	defer server.Close()

	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("a binary without the dashboard answers / with %d, want 404", response.StatusCode)
	}
	if !strings.Contains(string(body), "make ui") {
		t.Errorf("it answers %q, want the command that builds the dashboard", body)
	}
}

func TestPausedShowsTheQueueAndStartsNothing(t *testing.T) {
	f := start(t, config{"paused": true, "poll": "50ms"})
	var status map[string]any
	f.eventually(t, 10*time.Second, "the first poll", func() bool {
		status = map[string]any{}
		f.get(t, "/api/status", &status)
		return status["polledAt"] != "0001-01-01T00:00:00Z"
	})
	if status["state"] != "paused" {
		t.Errorf("the factory says it is %q, want paused", status["state"])
	}
	if quota, served := status["quotaUntil"]; !served || quota != nil {
		t.Errorf("the factory serves quotaUntil as %v (served: %v), want it served and empty", quota, served)
	}
	var line apiLine
	f.get(t, "/api/line", &line)
	if len(line.Queue) != len(cannedIssues) || len(line.Now) != 0 || len(line.Done) != 0 {
		t.Errorf("paused, the line is now=%d queue=%d done=%d, want the whole queue and nothing running",
			len(line.Now), len(line.Queue), len(line.Done))
	}
	for i := 1; i < len(line.Queue); i++ {
		if line.Queue[i].RoutedAt.Before(line.Queue[i-1].RoutedAt) {
			t.Errorf("the queue is not in the order the routing label was set: %s before %s",
				line.Queue[i].RoutedAt, line.Queue[i-1].RoutedAt)
		}
	}
	var repositories []map[string]any
	f.get(t, "/api/repositories", &repositories)
	if len(repositories) != 2 || repositories[0]["repository"] != "acme/edge-sensors" || repositories[0]["queued"] != 4.0 {
		t.Errorf("the connected repositories are %v, want both with what waits in them", repositories)
	}
	if records, _ := filepath.Glob(filepath.Join(f.data, "run-*.json")); len(records) != 0 {
		t.Errorf("paused, the factory wrote %d run records, want none", len(records))
	}
}

func TestASecondFactoryOnTheSameAddressStartsNothing(t *testing.T) {
	first := start(t, config{"paused": true})
	data := filepath.Join(t.TempDir(), "data")
	path := writeConfig(t, config{"listen": first.address, "data_dir": data, "paused": true,
		"repositories": []string{"acme/edge-sensors"}})

	second := exec.Command(binary, "-config", path, "-fake")
	output, err := second.CombinedOutput()
	if err == nil {
		t.Fatalf("the second factory started; it must fail on the address the first one holds")
	}
	if !strings.HasPrefix(string(output), "error: ") || !strings.Contains(string(output), first.address) {
		t.Errorf("the second factory said %q, want an error line naming the address", strings.TrimSpace(string(output)))
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Errorf("the second factory made its data directory before it failed on the address")
	}
}

func TestAnInvalidConfigurationIsRefusedWithTheFix(t *testing.T) {
	for _, c := range []struct {
		name   string
		config string
		want   string
	}{
		{"no repositories", `{"data_dir":"data"}`, `repositories is empty; name at least one`},
		{"repository without owner", `{"data_dir":"data","repositories":["workflows"]}`, `is not owner/name`},
		{"repository twice", `{"data_dir":"data","repositories":["a/b","a/b"]}`, `named twice; remove the duplicate`},
		{"repository of dots", `{"data_dir":"data","repositories":["../.."]}`, `is not owner/name`},
		{"repository whose name is dots", `{"data_dir":"data","repositories":["acme/.."]}`, `is not owner/name`}, // it would name a directory outside the data directory
		{"no data directory", `{"repositories":["a/b"]}`, `data_dir is missing; name the directory`},
		{"unknown field", `{"data_dir":"data","repositories":["a/b"],"listn":"x"}`, `unknown field "listn"; the fields are listen, label`},
		{"not JSON", `listen = 7341`, `see factory/factory.example.json`},
		{"deadline in words", `{"data_dir":"data","repositories":["a/b"],"deadline":"90 minutes"}`, `is not a positive duration; write it as "90m"`},
		{"poll of zero", `{"data_dir":"data","repositories":["a/b"],"poll":"0s"}`, `is not a positive duration`},
		{"address without host", `{"data_dir":"data","repositories":["a/b"],"listen":":7341"}`, `answers on every interface`},
		{"address without port", `{"data_dir":"data","repositories":["a/b"],"listen":"127.0.0.1"}`, `is not an address; write it as host:port`},
		{"wildcard address", `{"data_dir":"data","repositories":["a/b"],"listen":"0.0.0.0:7341"}`, `answers on every interface; bind it to one address`},
		{"wildcard address, IPv6", `{"data_dir":"data","repositories":["a/b"],"listen":"[::]:7341"}`, `answers on every interface`},
		{"wildcard address, IPv6 written out", `{"data_dir":"data","repositories":["a/b"],"listen":"[0:0:0:0:0:0:0:0]:7341"}`, `answers on every interface`},
		// A host that is not an IP literal is refused after binding, or by the resolver that could not
		// make an address of it; which of the two answers is the platform's business, so only the
		// refusal is asserted here.
		{"address that is not a literal host", `{"data_dir":"data","repositories":["a/b"],"listen":"0:7341"}`, `error: `},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "factory.json")
			if err := os.WriteFile(path, []byte(c.config), 0o600); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(binary, "-config", path, "-fake").CombinedOutput()
			if err == nil {
				t.Fatalf("the factory started on %s, want a refusal", c.config)
			}
			if !strings.HasPrefix(string(output), "error: ") || !strings.Contains(string(output), c.want) {
				t.Errorf("the factory said %q, want an error line with %q", strings.TrimSpace(string(output)), c.want)
			}
		})
	}
	t.Run("data directory that is a file", func(t *testing.T) {
		dir := t.TempDir()
		blocked := filepath.Join(dir, "runs")
		if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "factory.json")
		config := fmt.Sprintf(`{"listen":%q,"data_dir":%q,"repositories":["a/b"]}`, freeAddress(t), blocked)
		if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command(binary, "-config", path, "-fake").CombinedOutput()
		if err == nil {
			t.Fatalf("the factory started with a data directory it cannot write, want a refusal")
		}
		if !strings.Contains(string(output), "name a writable data_dir") {
			t.Errorf("the factory said %q, want the fix for a data directory it cannot use", strings.TrimSpace(string(output)))
		}
	})
	// Without fake mode the factory reads real GitHub, which so far it may only do paused:
	// TestAgainstRealGitHubTheFactoryRefusesToRunUnpaused.
	t.Run("missing file", func(t *testing.T) {
		output, _ := exec.Command(binary, "-config", filepath.Join(t.TempDir(), "gone.json"), "-fake").CombinedOutput()
		if !strings.Contains(string(output), "copy factory/factory.example.json") {
			t.Errorf("the factory said %q, want the fix for a missing configuration", strings.TrimSpace(string(output)))
		}
	})
}

func TestStoppingEndsTheWorkerAndTheRunIsInterrupted(t *testing.T) {
	f := start(t, config{"deadline": "5m", "poll": "50ms"})
	pids := f.waitForTheHangingWorker(t)

	f.stop(t, syscall.SIGTERM)
	for _, pid := range pids {
		if survived(pid) {
			t.Errorf("process %d of the worker survived the stop", pid)
		}
	}
	record := map[string]any{}
	read(t, filepath.Join(f.data, fmt.Sprintf("run-%d.json", len(cannedIssues))), &record)
	if record["outcome"] != "interrupted" || record["state"] != "ended" {
		t.Errorf("the run that was active is recorded as %v/%v, want ended/interrupted", record["state"], record["outcome"])
	}
}

func TestARestartKeepsTheRunsAndInterruptsWhatWasActive(t *testing.T) {
	f := start(t, config{"deadline": "5m", "poll": "50ms"})
	pids := f.waitForTheHangingWorker(t)
	// A power cut, not a stop: the factory is gone without ending anything.
	f.stop(t, syscall.SIGKILL)
	t.Cleanup(func() {
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	record := map[string]any{}
	read(t, filepath.Join(f.data, fmt.Sprintf("run-%d.json", len(cannedIssues))), &record)
	if record["state"] != "running" {
		t.Fatalf("the record of the active run says %v, want running before the restart", record["state"])
	}

	again := start(t, config{"deadline": "5m", "poll": "50ms", "data_dir": f.data, "listen": freeAddress(t)})
	var line apiLine
	again.eventually(t, 10*time.Second, "the runs of the first factory", func() bool {
		line = apiLine{}
		again.get(t, "/api/line", &line)
		return len(line.Done) == len(cannedIssues)
	})
	if len(line.Now) != 0 {
		t.Errorf("the restarted factory shows %d runs as running, want none: their processes are gone", len(line.Now))
	}
	if last := line.Done[len(line.Done)-1]; last.ID != len(cannedIssues) || last.Outcome != "interrupted" {
		t.Errorf("the run that was active is run %d with the outcome %q, want run %d interrupted", last.ID, last.Outcome, len(cannedIssues))
	}
	// The log of the run that was active reads to its end: its last event says how the run ended.
	var interrupted apiRun
	again.get(t, fmt.Sprintf("/api/runs/%d", len(cannedIssues)), &interrupted)
	if n := len(interrupted.Events); n == 0 || interrupted.Events[n-1].Title != "interrupted" || interrupted.EventCount != n {
		t.Errorf("the log of the interrupted run has %d events for an event count of %d and does not end in the interruption", n, interrupted.EventCount)
	}
	if line.Done[0].Outcome != "ready" {
		t.Errorf("the restarted factory reads run 1 as %q, want the outcome it was recorded with", line.Done[0].Outcome)
	}
	if len(line.Queue) != 0 {
		t.Errorf("the restarted factory queues %d issues again, want none: every canned entry has a run", len(line.Queue))
	}
}

func TestOnlyThePullRequestOfTheRunIsTakenFromAReport(t *testing.T) {
	for _, c := range []struct {
		name, detail, url string
	}{
		{"the pull request of the run", "https://github.com/acme/edge-sensors/pull/204", "https://github.com/acme/edge-sensors/pull/204"},
		{"a sentence that ends after it", "https://github.com/acme/edge-sensors/pull/204.", "https://github.com/acme/edge-sensors/pull/204"},
		{"a markdown link", "[#204](https://github.com/acme/edge-sensors/pull/204)", "https://github.com/acme/edge-sensors/pull/204"},
		{"an autolink after a word", "pull request <https://github.com/acme/edge-sensors/pull/204>", "https://github.com/acme/edge-sensors/pull/204"},
		{"the repository as GitHub spells it, not as the configuration does", "https://github.com/Acme/Edge-Sensors/pull/204", "https://github.com/acme/edge-sensors/pull/204"},
		{"a longer path under the pull request", "https://github.com/acme/edge-sensors/pull/204/files", "https://github.com/acme/edge-sensors/pull/204"},
		{"a repository whose name only starts the same", "https://github.com/acme/edge-sensors-fork/pull/204", ""},
		{"another repository", "https://github.com/acme/backtest/pull/204", ""},
		{"another host", "https://attacker.example/acme/edge-sensors/pull/204", ""},
		{"another scheme", "javascript:alert(1)", ""},
		{"an issue, not a pull request", "https://github.com/acme/edge-sensors/issues/204", ""},
		{"no number", "https://github.com/acme/edge-sensors/pull/", ""},
		{"nothing at all", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			url, reason := pullRequest(c.detail, "acme/edge-sensors")
			if url != c.url {
				t.Errorf("the report %q gives the pull request %q, want %q", c.detail, url, c.url)
			}
			if (reason == "") != (c.url != "") {
				t.Errorf("the report %q gives the reason %q, want one exactly when there is no pull request", c.detail, reason)
			}
		})
	}
}

// The stage of a run is read from the worker's skill calls, so the names the factory knows have to
// be the skills the worker plugin has. This is the drift test that binds the two.
func TestTheStagesAreTheSkillsOfTheWorkerPlugin(t *testing.T) {
	for skill := range stages {
		name, found := strings.CutPrefix(skill, "worker:")
		if !found {
			t.Errorf("the stage %q is not a skill of the worker plugin", skill)
			continue
		}
		path := filepath.Join("..", "plugins", "worker", "skills", name, "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the stage %q reads the skill %s, which is not there: %v", skill, path, err)
		}
	}
	// The other direction is not a rule: the worker plugin has skills that are no stage of a run, such
	// as the one that talks to GitHub.
	if len(stages) != 5 {
		t.Errorf("the factory knows %d stages, want the five the worker pipeline has", len(stages))
	}
}

func TestTheReportIsReadFromMarkdown(t *testing.T) {
	for _, c := range []struct {
		name            string
		report          string
		outcome, detail string
	}{
		{"bold", "**ready: https://github.com/a/b/pull/7**\n\nReview: 5/5 PASS.", "ready", "https://github.com/a/b/pull/7"},
		{"plain", "ready: https://github.com/a/b/pull/7", "ready", "https://github.com/a/b/pull/7"},
		{"a sentence around the pull request", "ready: the pull request is https://github.com/a/b/pull/7.", "ready", "the pull request is https://github.com/a/b/pull/7."},
		{"a heading over a summary", "## ready: https://github.com/a/b/pull/7\n\nCI green.", "ready", "https://github.com/a/b/pull/7"},
		{"a list item after a preamble", "Here is where I got to.\n\n- `blocked: the issue needs Herdr`", "blocked", "the issue needs Herdr"},
		{"a reason over several lines", "blocked: the brief contradicts ADR 0012.\n\ndecision needed: drop the step.", "blocked", "the brief contradicts ADR 0012.\n\ndecision needed: drop the step."},
		{"no report at all", "I have pushed the branch.", "", ""},
		{"the word in a sentence", "The run is ready: nothing is left to do.", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			outcome, detail := report(c.report)
			if outcome != c.outcome || detail != c.detail {
				t.Errorf("the report %q reads as %q/%q, want %q/%q", c.report, outcome, detail, c.outcome, c.detail)
			}
		})
	}
}

// ---- starting and watching the real binary ----

type config map[string]any

type factory struct {
	cmd     *exec.Cmd
	address string
	data    string
	log     string
}

// start writes a configuration, starts the binary in fake mode and waits until it answers.
func start(t *testing.T, c config) *factory {
	t.Helper()
	return launch(t, c, nil, "-fake")
}

// launch starts the binary with the given arguments and environment and waits until it answers. The
// environment is the test process's, so a shim on PATH is added by the caller.
func launch(t *testing.T, c config, env []string, args ...string) *factory {
	t.Helper()
	if _, ok := c["listen"]; !ok {
		c["listen"] = freeAddress(t)
	}
	if _, ok := c["data_dir"]; !ok {
		c["data_dir"] = filepath.Join(t.TempDir(), "data")
	}
	if _, ok := c["repositories"]; !ok {
		c["repositories"] = []string{"acme/edge-sensors", "acme/backtest"}
	}
	path := writeConfig(t, c)
	f := &factory{
		address: c["listen"].(string),
		data:    c["data_dir"].(string),
		log:     filepath.Join(filepath.Dir(path), "factory.log"),
	}
	output, err := os.Create(f.log)
	if err != nil {
		t.Fatal(err)
	}
	f.cmd = exec.Command(binary, append([]string{"-config", path}, args...)...)
	f.cmd.Stdout, f.cmd.Stderr = output, output
	if env != nil {
		f.cmd.Env = env
	}
	if err := f.cmd.Start(); err != nil {
		t.Fatalf("the factory could not be started: %v", err)
	}
	// A worker leads a process group of its own and only the factory ends it, so a test that fails
	// while a worker hangs has to let the factory stop rather than kill it, or the worker is orphaned.
	t.Cleanup(func() {
		defer output.Close()
		if f.cmd.ProcessState != nil {
			return
		}
		_ = f.cmd.Process.Signal(syscall.SIGTERM)
		ended := make(chan struct{})
		go func() { _ = f.cmd.Wait(); close(ended) }()
		select {
		case <-ended:
		case <-time.After(30 * time.Second):
			_ = f.cmd.Process.Signal(syscall.SIGKILL)
			<-ended
		}
	})
	f.eventually(t, 20*time.Second, "the factory to answer", func() bool {
		response, err := http.Get("http://" + f.address + "/api/status")
		if err != nil {
			return false
		}
		response.Body.Close()
		return response.StatusCode == http.StatusOK
	})
	return f
}

// stop signals the factory and waits until it is gone.
func (f *factory) stop(t *testing.T, signal syscall.Signal) {
	t.Helper()
	if err := f.cmd.Process.Signal(signal); err != nil {
		t.Fatalf("the factory could not be signalled: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- f.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("the factory did not exit on %v; its log:\n%s", signal, f.output(t))
	}
}

// waitForTheHangingWorker waits until the last canned entry, whose scripted worker hangs, is the
// running one, and answers with the processes it started.
func (f *factory) waitForTheHangingWorker(t *testing.T) []int {
	t.Helper()
	var run apiRun
	f.eventually(t, 60*time.Second, "the hanging worker of the last canned entry and its child", func() bool {
		run = apiRun{}
		response, err := http.Get(fmt.Sprintf("http://%s/api/runs/%d", f.address, len(cannedIssues)))
		if err != nil || response.StatusCode != http.StatusOK {
			if response != nil {
				response.Body.Close()
			}
			return false
		}
		defer response.Body.Close()
		if json.NewDecoder(response.Body).Decode(&run) != nil {
			return false
		}
		return run.State == "running" && len(workerPids(t, run)) == 2
	})
	return workerPids(t, run)
}

// page reads what a browser reads: the body a GET answers, and how it is typed.
func (f *factory) page(t *testing.T, path string) (string, string) {
	t.Helper()
	response := f.do(t, "GET", path)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s answered %d, want it served from the binary", path, response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("GET %s could not be read: %v", path, err)
	}
	return string(body), response.Header.Get("Content-Type")
}

func (f *factory) get(t *testing.T, path string, into any) {
	t.Helper()
	response := f.do(t, "GET", path)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s answered %d", path, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(into); err != nil {
		t.Fatalf("GET %s did not answer JSON: %v", path, err)
	}
}

func (f *factory) do(t *testing.T, method, path string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, "http://"+f.address+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s failed: %v; the factory's log:\n%s", method, path, err, f.output(t))
	}
	return response
}

func (f *factory) eventually(t *testing.T, within time.Duration, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waited %s for %s; the factory's log:\n%s", within, what, f.output(t))
}

func (f *factory) output(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(f.log)
	if err != nil {
		return "(no log)"
	}
	return string(raw)
}

func writeConfig(t *testing.T, c config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "factory.json")
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

var (
	pidInEvent         = regexp.MustCompile(`worker (?:child )?process (\d+)`)
	detachedPidInEvent = regexp.MustCompile(`worker detached process (\d+)`)
)

// workerPids are the processes the scripted worker said it is, read from the run's log.
func workerPids(t *testing.T, run apiRun) []int {
	t.Helper()
	pids := []int{}
	for _, e := range run.Events {
		if match := pidInEvent.FindStringSubmatch(e.Title); match != nil {
			pid, err := strconv.Atoi(match[1])
			if err != nil {
				t.Fatal(err)
			}
			pids = append(pids, pid)
		}
	}
	return pids
}

// survived answers whether a process is still there after the time it needs to take a signal. Signal
// 0 asks the kernel about a process without sending anything.
func survived(pid int) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if err := syscall.Kill(pid, 0); err != nil {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

func read(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s cannot be read: %v", path, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s is not JSON: %v", path, err)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("%s cannot be read: %v", path, err)
	}
	defer file.Close()
	lines := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), maxEventLine) // the limit the factory itself reads the log with
	for scanner.Scan() {
		lines++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("%s cannot be read to its end: %v", path, err)
	}
	return lines
}
