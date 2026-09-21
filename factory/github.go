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

// ghTimeout bounds one call to gh. A request that hangs would hold the whole line and the stop with
// it, and the next poll asks again anyway.
const ghTimeout = 60 * time.Second

// ghIssue is what an issue list carries of an issue: the fields the frontier rule reads and the
// fields the queue shows. GitHub answers the same shape for a pull request, which is why one is
// recognised by its pull_request field rather than counted as an issue.
type ghIssue struct {
	Number      int                      `json:"number"`
	Title       string                   `json:"title"`
	State       string                   `json:"state"`
	CreatedAt   time.Time                `json:"created_at"`
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

// ghEvent is one entry of an issue's timeline. The queue reads the labeled entries of it and nothing
// else: when the routing label was set is the order of the line.
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
}

// queue is the line across all connected repositories. A repository that cannot be read is reported
// and left out of this poll: one unreachable repository must not empty the line of the others, and
// the next poll asks again.
func (g *gitHub) queue(ctx context.Context) []Issue {
	queue := []Issue{}
	for _, repository := range g.repositories {
		issues, err := g.routedIssues(ctx, repository)
		if err != nil {
			if ctx.Err() != nil {
				return queue // the factory is stopping; a cancelled request is no failure of GitHub
			}
			log.Printf("error: the routed issues of %s could not be read: %v; check `gh auth status` on this host and that its token reaches the repository", repository, err)
			continue
		}
		queue = append(queue, issues...)
	}
	return queue
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
func issuesRequest(repository, routingLabel string) string {
	labels := url.QueryEscape(readyLabel) + "," + url.QueryEscape(routingLabel)
	return "repos/" + repository + "/issues?labels=" + labels + "&state=open&per_page=100"
}

// routedAt is when the routing label was last set, read from the issue's timeline. That time, and
// not the issue's age, is the order of the line: routing is when the maintainer handed the issue
// over, so an old issue routed today stands behind one routed yesterday.
//
// An issue whose timeline does not name the label — it fell out of what GitHub keeps, or the label
// came with the issue — counts from when it was opened. It keeps its place in the line that way,
// where an empty time would put it at the head of every queue.
func (g *gitHub) routedAt(ctx context.Context, repository string, issue ghIssue) time.Time {
	at := issue.CreatedAt
	raw, err := gh(ctx, "api", "--paginate", eventsRequest(repository, issue.Number))
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("error: the timeline of %s#%d could not be read: %v; it stands in the line by the time it was opened", repository, issue.Number, err)
		}
		return at
	}
	// --paginate answers one JSON array per page, so the pages are read as a stream of arrays.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var page []ghEvent
		if err := decoder.Decode(&page); err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("error: the timeline of %s#%d is not a timeline: %v; it stands in the line by the time it was opened", repository, issue.Number, err)
			}
			break
		}
		for _, event := range page {
			// The last time the label was set is the answer: a label that was removed and set again
			// was handed over again.
			if event.Event == "labeled" && event.Label.Name == g.label {
				at = event.CreatedAt
			}
		}
	}
	return at
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
		if _, err := gh(ctx, "repo", "clone", repository, dir); err != nil {
			log.Printf("error: %s could not be cloned into %s: %v; check `gh auth status` on this host and that its token reaches the repository, then start the factory again", repository, dir, err)
		}
	}
}

// clonePath is where a connected repository lives on the host: one clone per repository under the
// data directory. The name is owner/name, which the configuration has already refused unless it is
// exactly that, so the path stays inside the data directory.
func clonePath(dataDir, repository string) string {
	return filepath.Join(dataDir, "repos", filepath.FromSlash(repository))
}

// gh runs one gh command and answers with its output. The error carries what gh printed, because
// that line is what the operator needs: a login that expired, a repository the token cannot see.
func gh(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		said := strings.TrimSpace(stderr.String())
		if said == "" {
			said = err.Error()
		}
		return nil, fmt.Errorf("gh %s: %s", strings.Join(args, " "), firstLine(said))
	}
	return out, nil
}
