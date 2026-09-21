package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The factory reads GitHub through the gh command line, as every script of the workflow does: gh
// carries the host's login, so the factory keeps no token of its own and a repository the host
// cannot reach fails in one place, with the sentence gh gives.

// readyLabel says that an issue is ready for an agent. It is the workflow's label vocabulary
// restated in Go, and together with the routing label it is what makes an issue the factory's; the
// drift test against the orchestrator's board holds the restatement to its original ([ADR 0022]).
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
const readyLabel = "ready-for-agent"

// ghTimeout bounds one read from GitHub. A request that hangs would hold the whole line and the stop
// with it, and the next poll asks again anyway.
const ghTimeout = 60 * time.Second

// cloneTimeout bounds a clone, which is not a request but a whole repository over the host's line:
// minutes for a large one, and a clone that is cut off in the middle is work thrown away. It is a
// bound all the same, so a clone that hangs cannot hold the start for ever.
const cloneTimeout = 60 * time.Minute

// ghIssue is what an issue list carries of an issue: the fields the frontier rule reads and the
// fields the queue shows. GitHub answers the same shape for a pull request, which is why one is
// recognised by its pull_request field rather than counted as an issue.
type ghIssue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the issue was last touched, a label set among it. It is what a remembered
	// routing time is held against, so the timeline of an issue nobody touched is read once.
	UpdatedAt   time.Time                `json:"updated_at"`
	Assignees   []struct{ Login string } `json:"assignees"`
	Labels      []struct{ Name string }  `json:"labels"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
	// Dependencies is GitHub's summary of the issues that block this one; blocked_by counts the open
	// ones, which is what "no open blocker" means.
	Dependencies struct {
		BlockedBy int `json:"blocked_by"`
	} `json:"issue_dependencies_summary"`
}

func (i ghIssue) hasLabel(name string) bool {
	for _, label := range i.Labels {
		if label.Name == name {
			return true
		}
	}
	return false
}

func (i ghIssue) labelNames() []string {
	names := make([]string, 0, len(i.Labels))
	for _, label := range i.Labels {
		names = append(names, label.Name)
	}
	return names
}

// ghEvent is one entry of an issue's event list (the events endpoint of an issue, not the timeline
// endpoint beside it). The queue reads the labeled entries and nothing else: when the routing label
// was set is the order of the line.
type ghEvent struct {
	Event     string                `json:"event"`
	CreatedAt time.Time             `json:"created_at"`
	Label     struct{ Name string } `json:"label"`
}

// routed is the factory's frontier rule: an issue is routed to it when it is open, carries
// ready-for-agent and the routing label, has nobody assigned and nothing open blocking it.
//
// It is the Go restatement of the rule the orchestrator's board applies to free agent-ready issues
// (plugins/orchestrator/scripts/board.sh), which is the same rule with the routing label the other
// way round: what the board leaves to the factory is exactly what the factory takes. A drift test
// runs both programs over one set of issues and fails when they disagree ([ADR 0022]).
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
func routed(issue ghIssue, routingLabel string) bool {
	return issue.PullRequest == nil &&
		issue.State == "open" &&
		issue.hasLabel(readyLabel) &&
		issue.hasLabel(routingLabel) &&
		len(issue.Assignees) == 0 &&
		issue.Dependencies.BlockedBy == 0
}

// gitHub is the live queue: the routed issues of the connected repositories, asked from GitHub on
// every poll and never written down, so an issue is in the line exactly as long as GitHub says it is
// routed ([ADR 0025]).
//
// [ADR 0025]: ../docs/adr/0025-one-queue-one-worker-work-in-progress-first.md
type gitHub struct {
	repositories []string
	label        string

	mu sync.Mutex
	// times is what the factory remembers of the routing times it has read, keyed by the issue.
	// Reading a timeline is a request of its own per issue, so a line of a few dozen issues asked
	// every minute would spend a whole hourly budget on standing still. It is memory and no file: the
	// line itself is still derived from GitHub on every poll ([ADR 0025]).
	times map[string]routing
	// unreadable is the repositories the last poll could not read, with what gh said, and warned the
	// issues whose event list could not be read. Both are reported when they start failing and not
	// once a minute for a week: the factory polls every minute and a host runs it for weeks.
	unreadable map[string]string
	warned     map[string]bool
}

// routing is one remembered routing time with the issue's updated_at it was read at. GitHub touches
// updated_at when a label changes, so a remembered time is good until the issue is touched again.
type routing struct {
	updated time.Time
	at      time.Time
}

// queue is the line across all connected repositories. A repository that cannot be read is reported
// and left out of this poll: one unreachable repository must not empty the line of the others, and
// the next poll asks again.
func (g *gitHub) queue(ctx context.Context) poll {
	result := poll{issues: []Issue{}, unreadable: map[string]string{}}
	read, seen := map[string]bool{}, map[string]bool{}
	for _, repository := range g.repositories {
		issues, err := g.routedIssues(ctx, repository)
		if err != nil {
			if ctx.Err() != nil {
				return result // the factory is stopping; a cancelled request is no failure of GitHub
			}
			result.unreadable[repository] = said(err)
			continue
		}
		read[repository] = true
		for _, issue := range issues {
			seen[issue.key()] = true
		}
		result.issues = append(result.issues, issues...)
	}
	g.settle(result.unreadable, read, seen)
	return result
}

// settle reports what changed with this poll and forgets what the line no longer holds. The report
// is the change and not the state, because the factory polls every minute and runs for weeks: a
// journal that repeats the same line every minute is one nobody reads.
// It forgets an issue of a repository it has read and no longer holds, and keeps what it knows of a
// repository it could not read this time: the likeliest reason for a failed read is a rate limit,
// and answering one by reading every event list again is the opposite of what the memory is for.
func (g *gitHub) settle(unreadable map[string]string, read, seen map[string]bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for repository, said := range unreadable {
		if g.unreadable[repository] != said {
			log.Printf("error: the routed issues of %s could not be read: %s; check `gh auth status` on this host and that its token reaches the repository", repository, said)
		}
	}
	for repository := range g.unreadable {
		if _, again := unreadable[repository]; !again {
			log.Printf("the routed issues of %s can be read again", repository)
		}
	}
	g.unreadable = unreadable
	for key := range g.times {
		if read[repositoryOf(key)] && !seen[key] {
			delete(g.times, key)
		}
	}
	for key := range g.warned {
		if read[repositoryOf(key)] && !seen[key] {
			delete(g.warned, key)
		}
	}
}

// repositoryOf is the repository an issue key names (owner/name#number).
func repositoryOf(key string) string {
	repository, _, _ := strings.Cut(key, "#")
	return repository
}

// warn reports what could not be read of one issue, once, until it can be read again.
func (g *gitHub) warn(key, format string, a ...any) {
	g.mu.Lock()
	if g.warned == nil {
		g.warned = map[string]bool{}
	}
	first := !g.warned[key]
	g.warned[key] = true
	g.mu.Unlock()
	if first {
		log.Printf(format, a...)
	}
}

// readable says that an issue could be read again, so the next failure is reported anew.
func (g *gitHub) readable(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.warned, key)
}

// remembered answers the routing time read earlier for an issue nothing has touched since.
func (g *gitHub) remembered(key string, updated time.Time) (time.Time, bool) {
	if updated.IsZero() { // without an updated_at there is nothing to hold the memory against
		return time.Time{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	known, ok := g.times[key]
	if !ok || !known.updated.Equal(updated) {
		return time.Time{}, false
	}
	return known.at, true
}

func (g *gitHub) remember(key string, updated, at time.Time) {
	if updated.IsZero() {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.times == nil {
		g.times = map[string]routing{}
	}
	g.times[key] = routing{updated: updated, at: at}
}

// routedIssues asks GitHub for the issues of one repository that carry both labels and keeps those
// the frontier rule takes. The rule is applied to the answer and not left to the query, because a
// query can only filter labels: assignees, blockers and pull requests are decided here, in the one
// place the drift test reads.
func (g *gitHub) routedIssues(ctx context.Context, repository string) ([]Issue, error) {
	raw, err := gh(ctx, "api", issuesRequest(repository, g.label))
	if err != nil {
		return nil, err
	}
	var issues []ghIssue
	if err := json.Unmarshal(raw, &issues); err != nil {
		return nil, fmt.Errorf("the issue list is not a list of issues: %w", err)
	}
	out := []Issue{}
	for _, issue := range issues {
		if !routed(issue, g.label) {
			continue
		}
		out = append(out, Issue{
			Repository: repository,
			Number:     issue.Number,
			Title:      issue.Title,
			Labels:     issue.labelNames(),
			RoutedAt:   g.routedAt(ctx, repository, issue),
		})
	}
	return out, nil
}

// issuesRequest is the issue list of one repository: open, carrying ready-for-agent and the routing
// label. GitHub reads several labels as "all of them".
//
// It asks for one page and does not paginate, which is the board's query exactly (board.sh): a
// repository with more than a hundred issues routed at once has months of work waiting, and the
// factory taking a different set than the board leaves it would be the drift the test guards.
func issuesRequest(repository, routingLabel string) string {
	labels := url.QueryEscape(readyLabel) + "," + url.QueryEscape(routingLabel)
	return "repos/" + repository + "/issues?labels=" + labels + "&state=open&per_page=100"
}

// routedAt is when the routing label was last set, read from the issue's event list. That time, and
// not the issue's age, is the order of the line: routing is when the maintainer handed the issue
// over, so an old issue routed today stands behind one routed yesterday.
//
// An issue whose events do not name the label — it fell out of what GitHub keeps, the label came
// with the issue, or the timeline is still catching up with the list that already carries it —
// counts from when it was opened. It keeps its place in the line that way, where an empty time
// would put it at the head of every queue. An event list that could not be read at all counts from
// then too, which is earlier than the routing and moves the issue towards the head.
//
// Only an answer that names the label is remembered. The two views GitHub answers with are not one
// moment: the issue list carries the label seconds before the timeline names the event, and an
// answer without it, written down against the issue's updated_at, would hold that wrong time until
// somebody touched the issue again. So a fallback is read again on the next poll, which is one call
// per poll for as long as the label event is missing, and the issue takes its place as it appears.
func (g *gitHub) routedAt(ctx context.Context, repository string, issue ghIssue) time.Time {
	key := Issue{Repository: repository, Number: issue.Number}.key()
	if at, ok := g.remembered(key, issue.UpdatedAt); ok {
		return at
	}
	at, found := g.readRoutedAt(ctx, key, repository, issue)
	if found {
		g.remember(key, issue.UpdatedAt, at)
	}
	return at
}

// readRoutedAt reads the issue's event list and says whether the routing label was in it.
func (g *gitHub) readRoutedAt(ctx context.Context, key, repository string, issue ghIssue) (time.Time, bool) {
	at := issue.CreatedAt
	found := false
	raw, err := gh(ctx, "api", "--paginate", eventsRequest(repository, issue.Number))
	if err != nil {
		if ctx.Err() == nil {
			g.warn(key, "error: the events of %s could not be read: %v; it stands in the line by the time it was opened", key, err)
		}
		return at, false
	}
	// --paginate answers one JSON array per page, so the pages are read as a stream of arrays.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var page []ghEvent
		if err := decoder.Decode(&page); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			g.warn(key, "error: the events of %s are no event list: %v; it stands in the line by the time it was opened", key, err)
			return at, false
		}
		for _, event := range page {
			// The last time the label was set is the answer: a label that was removed and set again
			// was handed over again. It is the latest of the entries rather than the last one, so the
			// order GitHub sends the timeline in is not part of the rule.
			if event.Event == "labeled" && event.Label.Name == g.label {
				found = true
				if event.CreatedAt.After(at) {
					at = event.CreatedAt
				}
			}
		}
	}
	// The list was read, whatever it held: an issue this poll could read is no issue to warn about.
	g.readable(key)
	return at, found
}

func eventsRequest(repository string, issue int) string {
	return "repos/" + repository + "/issues/" + strconv.Itoa(issue) + "/events?per_page=100"
}

// connect makes sure every connected repository has a clone under the data directory, which is what
// a run makes its worktree from. Connecting a repository is one line in the configuration and a
// restart, so this runs at the start and clones what is missing.
//
// A repository that cannot be cloned is reported and the others still work: the line itself is read
// from GitHub and needs no clone, so one repository the host cannot reach must not keep the factory
// from the rest of its work.
func connect(ctx context.Context, settings Settings) {
	for _, repository := range settings.Repositories {
		dir := clonePath(settings.DataDir, repository)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			log.Printf("error: %s cannot be created for the clone of %s: %v; name a writable data_dir", filepath.Dir(dir), repository, err)
			continue
		}
		log.Printf("cloning %s into %s", repository, dir)
		// The clone lands beside its place and is moved in when gh is done, so a clone that was cut
		// off — the host rebooted, the factory was stopped — leaves no half repository that the next
		// start would take for a finished one and hand to a worker.
		// A staging directory of an earlier start is a clone nobody finished: it is swept, not kept,
		// because a host that was rebooted mid-clone would otherwise collect half repositories.
		if left, err := filepath.Glob(filepath.Join(filepath.Dir(dir), "."+filepath.Base(dir)+".cloning-*")); err == nil {
			for _, path := range left {
				_ = os.RemoveAll(path)
			}
		}
		staging, err := os.MkdirTemp(filepath.Dir(dir), "."+filepath.Base(dir)+".cloning-")
		if err != nil {
			log.Printf("error: the clone of %s cannot be started in %s: %v; name a writable data_dir", repository, filepath.Dir(dir), err)
			continue
		}
		if _, err := ghWithin(ctx, cloneTimeout, "repo", "clone", repository, staging); err != nil {
			_ = os.RemoveAll(staging)
			if ctx.Err() != nil {
				return // the factory is stopping; a clone that was cut off is no failure of GitHub
			}
			log.Printf("error: %s could not be cloned into %s: %v; check `gh auth status` on this host and that its token reaches the repository, then start the factory again", repository, dir, err)
			continue
		}
		if err := os.Rename(staging, dir); err != nil {
			_ = os.RemoveAll(staging)
			log.Printf("error: the clone of %s could not be put in %s: %v; make sure nothing else writes in the data_dir, then start the factory again", repository, dir, err)
			continue
		}
		log.Printf("cloned %s", repository)
	}
}

// clonePath is where a connected repository lives on the host: one clone per repository under the
// data directory. The name is owner/name, which the configuration has already refused unless it is
// exactly that, so the path stays inside the data directory.
//
// It is the name in lower case, because that is the repository GitHub answers for either spelling:
// a configuration rewritten from Acme/Repo to acme/repo names the same repository, and on a host
// whose file system tells the two apart the next start would clone it again beside the first and
// leave the old clone behind.
func clonePath(dataDir, repository string) string {
	return filepath.Join(dataDir, "repos", filepath.FromSlash(strings.ToLower(repository)))
}

// ghError is a gh call that failed: the whole call, which is what the journal needs, and what gh
// itself said, which is what the interface shows beside a repository it has already named.
type ghError struct {
	call string
	said string
}

func (e ghError) Error() string { return "gh " + e.call + ": " + e.said }

// said is the reason out of an error, without the call that carried it.
func said(err error) string {
	var failed ghError
	if errors.As(err, &failed) {
		return failed.said
	}
	return firstLine(err.Error())
}

// gh runs one read from GitHub.
func gh(ctx context.Context, args ...string) ([]byte, error) {
	return ghWithin(ctx, ghTimeout, args...)
}

// ghWithin runs one gh command under a deadline of its own and answers with its output. The error
// carries what gh printed, because that line is what the operator needs: a login that expired, a
// repository the token cannot see.
func ghWithin(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	// gh starts children of its own — git, for a clone — so the deadline and the stop have to reach
	// them: the command gets a process group and the group is what is ended. WaitDelay closes the
	// pipes after that, because a process that outlived the group still holds the output pipe it
	// inherited, and waiting on that pipe would hold the factory past its own deadline.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return endGroup(cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return nil, ghError{call: strings.Join(args, " "), said: firstLine(reason)}
	}
	return out, nil
}
