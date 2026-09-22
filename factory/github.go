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
// endpoint beside it). The queue reads three kinds of entry: labeled, because when the routing label
// was set is the order of the line, and assigned and unassigned, because taking the assignee off an
// issue the factory holds is the release signal and the assignment it undid is what that removal is
// held against ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
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

// ghPull is what the factory reads of a pull request: whether it was merged, and whether it is still
// open. Both are one decision — the maintainer is done with the issue behind it — and the merge is
// asked for separately because GitHub calls a merged pull request closed as well.
type ghPull struct {
	State  string `json:"state"`
	Merged bool   `json:"merged"`
}

// gitHub is the live queue: the routed issues of the connected repositories, asked from GitHub on
// every poll and never written down, so an issue is in the line exactly as long as GitHub says it is
// routed ([ADR 0025]).
//
// [ADR 0025]: ../docs/adr/0025-one-queue-one-worker-work-in-progress-first.md
type gitHub struct {
	repositories []Connected
	label        string

	mu sync.Mutex
	// times is what the factory remembers of the event times it has read, keyed by the issue.
	// Reading a timeline is a request of its own per issue, so a line of a few dozen issues asked
	// every minute would spend a whole hourly budget on standing still. It is memory and no file: the
	// line itself is still derived from GitHub on every poll ([ADR 0025]).
	times map[string]reading
	// unreadable is the repositories the last poll could not read, with what gh said, and warned the
	// issues whose event list could not be read. Both are reported when they start failing and not
	// once a minute for a week: the factory polls every minute and a host runs it for weeks.
	unreadable map[string]string
	warned     map[string]bool
}

// reading is one remembered reading of an issue's event list with the issue's updated_at it was made
// at. GitHub touches updated_at when a label or an assignee changes, so a remembered reading is good
// until the issue is touched again.
type reading struct {
	updated time.Time
	read    signals
}

// signals is what one issue's event list says the factory acts on: when the routing label was last
// set, which is where a new issue stands in the line, and when an assignee was last put on and last
// taken off. A removal that is newer than the assignment it undid is the release signal for an issue
// this factory holds, and where its resumed run stands. Both assignment times are GitHub's own, so
// the release is decided without this host's clock in it.
type signals struct {
	routedAt     time.Time
	assignedAt   time.Time
	unassignedAt time.Time
}

// queue is the line across all connected repositories. A repository that cannot be read is reported
// and left out of this poll: one unreachable repository must not empty the line of the others, and
// the next poll asks again.
func (g *gitHub) queue(ctx context.Context, held []Held) poll {
	result := poll{issues: []Issue{}, unreadable: map[string]string{}, letGo: map[string]string{}}
	read, seen := map[string]bool{}, map[string]bool{}
	for _, connected := range g.repositories {
		repository := connected.Name
		issues, err := g.routedIssues(ctx, repository)
		if err != nil {
			if ctx.Err() != nil {
				return result // the factory is stopping; a cancelled request is no failure of GitHub
			}
			result.unreadable[repository] = said(err)
			continue
		}
		read[repositoryKey(repository)] = true
		for _, issue := range issues {
			seen[issue.key()] = true
		}
		result.issues = append(result.issues, issues...)
	}
	g.readHeld(ctx, held, result.letGo, seen)
	g.settle(result.unreadable, read, seen)
	return result
}

// readHeld asks GitHub about every issue this factory holds and answers with those it is done with.
// An issue the factory holds is assigned to this host, so it is not in the line above and there is
// no other reading of it: this is where a maintainer's decision reaches work in progress ([ADR
// 0023]). It is one request per issue it is given and one more for a pull request that stands, and
// which issues those are is the caller's cadence (heldIssues): the run that is going on every poll,
// so a cancel is heard within one, and what is only waiting to be cleaned up rarely, so a host with
// a dozen pull requests in review does not spend its hour of requests on them.
//
// An issue whose reading fails says nothing at all. A rate limit, a login that expired or a
// repository nobody can reach must never take a worktree apart ([ADR 0026]), so the factory holds on
// to what it holds until GitHub answers, and says once that it could not ask.
//
// [ADR 0023]: ../docs/adr/0023-github-is-the-only-control-surface-of-the-factory.md
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func (g *gitHub) readHeld(ctx context.Context, held []Held, letGo map[string]string, seen map[string]bool) {
	for _, issue := range held {
		if ctx.Err() != nil {
			return // the factory is stopping; a cancelled request says nothing about the issue
		}
		key := issue.key()
		// An issue this factory holds is one this poll knows about, answered or not, so what is
		// remembered of it stays: an unreadable issue would otherwise be warned about anew on every
		// poll, and a GitHub that is down for a day would fill the log with one sentence.
		seen[key] = true
		decision, err := g.decided(ctx, issue)
		if err != nil {
			if ctx.Err() == nil {
				g.warn(key, "error: %s is held by this factory and could not be read: %v; it stays as it is until GitHub answers", key, err)
			}
			continue
		}
		g.readable(key) // it answered, so the next failure is reported anew
		if decision != "" {
			letGo[key] = decision
		}
	}
}

// decided is the decision GitHub carries about one held issue, or nothing. The three gestures are
// the maintainer's: the routing label taken off the issue, the issue closed, and the pull request a
// run of it opened merged or closed. They are read in that order, because the first two are the
// answer to the issue itself while the third is the answer to one run's work, and every one of them
// ends whatever of the issue is still going: a factory that worked the issue on while its pull
// request was decided would be working against the decision.
//
// The routing label alone is read and not the rest of the frontier rule: routing is what hands an
// issue to this factory and taking that label off is what takes it back ([ADR 0023]), while
// ready-for-agent says the issue is ready to be worked at all — an issue already in work is past
// that question, and a maintainer who wants this run to end says so with the label that named the
// host.
//
// An answer that is not this issue decides nothing: a reading that goes wrong must not be read as a
// gesture, and neither must a state that is anything other than the closed GitHub spells.
func (g *gitHub) decided(ctx context.Context, held Held) (string, error) {
	raw, err := gh(ctx, "api", "repos/"+held.Repository+"/issues/"+strconv.Itoa(held.Number))
	if err != nil {
		return "", err
	}
	var issue ghIssue
	if err := json.Unmarshal(raw, &issue); err != nil {
		return "", fmt.Errorf("the answer is no issue: %w", err)
	}
	if issue.Number != held.Number {
		return "", fmt.Errorf("the answer is issue #%d and not #%d", issue.Number, held.Number)
	}
	switch {
	case issue.State == "closed":
		return "the issue was closed", nil
	case !issue.hasLabel(g.label):
		return "the routing label " + g.label + " was taken off the issue", nil
	case held.PullRequest == "":
		return "", nil
	}
	return g.decidedOnPull(ctx, held)
}

// decidedOnPull is what became of the pull request a run of the issue opened. A pull request that is
// merged or closed is the maintainer's answer to the work, and the issue is done with this factory
// either way: the worktree it was written in is of no use to anybody after that.
func (g *gitHub) decidedOnPull(ctx context.Context, held Held) (string, error) {
	found := pullRequestURL.FindStringSubmatch(held.PullRequest)
	if found == nil {
		return "", nil // a record without a pull request URL is nothing to ask about
	}
	raw, err := gh(ctx, "api", "repos/"+held.Repository+"/pulls/"+found[2])
	if err != nil {
		return "", err
	}
	var pull ghPull
	if err := json.Unmarshal(raw, &pull); err != nil {
		return "", fmt.Errorf("the answer is no pull request: %w", err)
	}
	switch {
	case pull.Merged:
		return "the pull request " + held.PullRequest + " was merged", nil
	case pull.State == "closed":
		return "the pull request " + held.PullRequest + " was closed", nil
	}
	return "", nil
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

// remembered answers the reading made earlier for an issue nothing has touched since.
func (g *gitHub) remembered(key string, updated time.Time) (signals, bool) {
	if updated.IsZero() { // without an updated_at there is nothing to hold the memory against
		return signals{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	known, ok := g.times[key]
	if !ok || !known.updated.Equal(updated) {
		return signals{}, false
	}
	return known.read, true
}

func (g *gitHub) remember(key string, updated time.Time, read signals) {
	if updated.IsZero() {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.times == nil {
		g.times = map[string]reading{}
	}
	g.times[key] = reading{updated: updated, read: read}
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
		read := g.signalsOf(ctx, repository, issue)
		out = append(out, Issue{
			Repository:   repository,
			Number:       issue.Number,
			Title:        issue.Title,
			Labels:       issue.labelNames(),
			RoutedAt:     read.routedAt,
			assignedAt:   read.assignedAt,
			unassignedAt: read.unassignedAt,
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

// signalsOf is when the routing label was last set and when the assignee was last removed, read from
// the issue's event list. The routing time, and not the issue's age, is the order of the line:
// routing is when the maintainer handed the issue over, so an old issue routed today stands behind
// one routed yesterday.
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
//
// The removal of an assignee is remembered on the same condition and not on one of its own: an issue
// that never had an assignee has no such event, and demanding one would read the event list of every
// routed issue on every poll. A release whose event list is that much behind the issue list is read
// on the next touch of the issue, and until then the issue stands where it stood.
func (g *gitHub) signalsOf(ctx context.Context, repository string, issue ghIssue) signals {
	key := Issue{Repository: repository, Number: issue.Number}.key()
	if read, ok := g.remembered(key, issue.UpdatedAt); ok {
		return read
	}
	read, found := g.readSignals(ctx, key, repository, issue)
	if found {
		g.remember(key, issue.UpdatedAt, read)
	}
	return read
}

// readSignals reads the issue's event list and says whether the routing label was in it. The
// assignments are read in the same pass: an issue that is released has been touched, so its event
// list is read again anyway, and a call of its own for them would double what a poll costs.
func (g *gitHub) readSignals(ctx context.Context, key, repository string, issue ghIssue) (signals, bool) {
	read := signals{routedAt: issue.CreatedAt}
	found := false
	raw, err := gh(ctx, "api", "--paginate", eventsRequest(repository, issue.Number))
	if err != nil {
		if ctx.Err() == nil {
			g.warn(key, "error: the events of %s could not be read: %v; it stands in the line by the time it was opened", key, err)
		}
		return read, false
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
			return read, false
		}
		for _, event := range page {
			// The last time each of the two happened is the answer: a label that was removed and set
			// again was handed over again, and the last release is the one this factory answers. It is
			// the latest of the entries rather than the last one, so the order GitHub sends the
			// timeline in is not part of the rule.
			switch {
			case event.Event == "labeled" && event.Label.Name == g.label:
				found = true
				if event.CreatedAt.After(read.routedAt) {
					read.routedAt = event.CreatedAt
				}
			case event.Event == "assigned":
				if event.CreatedAt.After(read.assignedAt) {
					read.assignedAt = event.CreatedAt
				}
			case event.Event == "unassigned":
				if event.CreatedAt.After(read.unassignedAt) {
					read.unassignedAt = event.CreatedAt
				}
			}
		}
	}
	// The list was read, whatever it held: an issue this poll could read is no issue to warn about.
	g.readable(key)
	return read, found
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
	for _, connected := range settings.Repositories {
		repository := connected.Name
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
	return filepath.Join(dataDir, "repos", filepath.FromSlash(repositoryKey(repository)))
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
	out, reason, err := command(ctx, timeout, "gh", args...)
	if err != nil {
		return nil, ghError{call: strings.Join(args, " "), said: reason}
	}
	return out, nil
}

// command is how the factory runs another program: under a deadline of its own and in a process group of
// its own, answering with its output and, when it failed, with the first line it said. gh and git
// both start children — git for a clone, git for a fetch over the line — so the deadline and the stop
// have to reach those too: the group is what is ended. WaitDelay closes the pipes after that, because
// a process that outlived the group still holds the output pipe it inherited, and waiting on that
// pipe would hold the factory past its own deadline. The reason falls back to the error itself, for a
// program that fails without a word.
func command(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
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
		return nil, firstLine(reason), err
	}
	return out, "", nil
}
