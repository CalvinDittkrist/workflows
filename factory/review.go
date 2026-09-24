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
	"strconv"
	"strings"
	"time"
)

// The follow-up run. A run that ended ready leaves a pull request, and the maintainer reads it on
// GitHub: requesting changes there is the gesture that hands the objection back to the factory,
// which queues a run in the worktree of the claim that starts at the factory's address-reviews stage
// ([ADR 0023]). Nothing else is needed of the maintainer — no checkout, no comment on the issue —
// and nothing of it is a state of the factory: the review is read from GitHub on every poll and what
// has been answered is read from the run records, exactly as a release is ([ADR 0025]).
//
// [ADR 0023]: ../docs/adr/0023-github-is-the-only-control-surface-of-the-factory.md
// [ADR 0025]: ../docs/adr/0025-one-queue-one-worker-work-in-progress-first.md

// refreshRequested reads the pull requests this factory holds open, one per issue it holds, and
// remembers the newest review of each that asks for changes. It runs on every poll, beside the
// queue: a follow-up run stands in the same line as the work the factory resumes.
//
// Only an issue whose last run has ended is asked about, which is both what the rule needs — a
// review is answered after the run it arrived during — and what keeps the factory from asking
// GitHub about a pull request while its worker is writing to it.
func (f *Factory) refreshRequested(ctx context.Context) {
	requested, early := map[string]time.Time{}, false
	for key, held := range holdings(f.runs.list()) {
		early = early || !held.idle
		pull, watched := held.pull()
		if !watched {
			continue
		}
		if _, connected := f.connected(held.repository()); !connected {
			continue // a repository this host is not to work is not watched either
		}
		if at := f.source.changesRequested(ctx, held.repository(), pull); !at.IsZero() {
			requested[key] = at
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requested, f.requestedEarly = requested, early
}

// pull is the pull request of this issue that the factory watches, and whether there is one at all.
// It is the newest one the issue's runs reported, and it is only watched while this factory holds
// the issue and no run of it is going: a follow-up run stands under the claim, like every other run
// of held work, and never beside one.
func (h holding) pull() (int, bool) {
	if !h.holds || !h.idle || h.pullRequest == "" {
		return 0, false
	}
	return pullNumber(h.pullRequest)
}

// changesRequested says that a review asks for changes and that no run of this issue has answered
// it: it was submitted after the last run of the issue ended, which is the rule the maintainer's
// gesture is read by — an objection raised while a run was going is the running session's to see —
// and after the review the last follow-up run of this issue already stands for.
//
// That second comparison is what makes one review one run, however many polls read it: it holds
// GitHub's reading of the submission against GitHub's reading of the submission a record carries, so
// the drift between this host's clock and GitHub's cannot answer a review twice. The first
// comparison is the only one in the factory that holds the two clocks against each other, because
// when a run ended is this host's reading and nothing of GitHub's says it: a host whose clock runs
// ahead of GitHub's leaves a review submitted within that drift of the run's end unanswered, and the
// maintainer's next review is read as it should be.
func (h holding) changesRequested(at time.Time) bool {
	if at.IsZero() || !h.holds || !h.idle {
		return false
	}
	return at.After(*h.last.EndedAt) && at.After(h.addressed) // idle says the last run has an end
}

// pullNumber is the number of a pull request out of the URL a run recorded. The record was written
// from the shape the factory itself built (pullRequest in stream.go), so a URL that does not have it
// belongs to no pull request this factory can read.
func pullNumber(link string) (int, bool) {
	found := pullRequestURL.FindStringSubmatch(link)
	if found == nil {
		return 0, false
	}
	number, err := strconv.Atoi(found[2])
	return number, err == nil && number > 0
}

// ghReview is one review of a pull request, as GitHub's review list carries it: who submitted it,
// what it says about the pull request and when it was submitted. A plain comment on the pull request
// is not in this list at all; a review that only commented carries the state COMMENTED, and one that
// was dismissed carries DISMISSED, so neither is read as a review that asks for changes.
type ghReview struct {
	ID   int64 `json:"id"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submitted_at"`
	HTMLURL     string    `json:"html_url"` // where a person reads it, which a blocked ci stage names
	Body        string    `json:"body"`     // what it says, which an address-reviews session is shown
}

// The review states this factory reads. A review that asks for changes is the maintainer's gesture;
// one that only commented and one that was never submitted state nothing about the pull request and
// leave the author's previous review standing, which is GitHub's own rule for the review decision.
const (
	stateChangesRequested = "CHANGES_REQUESTED"
	stateCommented        = "COMMENTED"
	statePending          = "PENDING"
)

// states says whether a review states its author's standing on the pull request at all.
func (r ghReview) states() bool { return r.State != stateCommented && r.State != statePending }

// newer orders two reviews of the same author. GitHub times a submission to the second, so the id
// decides a tie: the answer is then the same whatever order the pages came in, which is what the
// reading below stands on.
func (r ghReview) newer(than ghReview) bool {
	if r.SubmittedAt.Equal(than.SubmittedAt) {
		return r.ID > than.ID
	}
	return r.SubmittedAt.After(than.SubmittedAt)
}

// changesRequested is the newest review that asks for changes on one pull request of this factory,
// submitted by somebody who may write to the repository, or the zero time when there is no such
// review, when the pull request is no longer open, and when GitHub could not be read: a follow-up
// run is queued on what GitHub said and never on the silence of a host that could not reach it.
//
// Both readings are made on every poll rather than remembered against the pull request's updated_at,
// the way the routing times of the line are: the pull requests of a factory are the few it has open
// at once, not every routed issue of every connected repository, and GitHub does not promise that
// submitting a review touches the pull request at all.
func (g *gitHub) changesRequested(ctx context.Context, repository string, pull int) time.Time {
	none, key := time.Time{}, pullKey(repository, pull)
	if g.over(key) {
		return none
	}
	newest, err := g.newestRequest(ctx, repository, pull)
	if err != nil {
		if ctx.Err() == nil { // a request cancelled by a stopping factory is no failure of GitHub
			g.warn(g.pullWarnings, key, "error: %v; a review on %s queues nothing until GitHub can be read", err, key)
		}
		return none
	}
	// The pull request was read, whatever it said: one this poll could read is none to warn about.
	g.readable(g.pullWarnings, key)
	return newest
}

// newestRequest reads the one pull request. A failure to read it or its review list ends the
// reading, because a list that was only half read is not one the latest review of an author can be
// taken from, and a run is queued on what GitHub said rather than on a gap in it. The one failure
// that does not end it is the write access of a single author, which only disqualifies that author's
// review; it is answered where it happens, below.
func (g *gitHub) newestRequest(ctx context.Context, repository string, pull int) (time.Time, error) {
	none, key := time.Time{}, pullKey(repository, pull)
	state, err := gh(ctx, "api", pullRequestRequest(repository, pull), "--jq", ".state")
	if err != nil {
		return none, fmt.Errorf("the pull request %s could not be read: %w", key, err)
	}
	switch read := strings.TrimSpace(string(state)); read {
	case "open":
	case "closed":
		// Merged or closed — GitHub calls both of them closed — so what a review asked of it is not
		// this factory's to answer, and it is not asked about again. That bound is what keeps the
		// watch to the pull requests that are open: the factory holds every issue it has ever claimed
		// until cleanup lets one go ([ADR 0026]), and a call per poll for each of them would spend a
		// rate limit on work that is over. It is memory and no file, like every reading of GitHub, so
		// a pull request somebody reopens is watched again from the next start of the factory.
		log.Printf("%s is closed; no review of it is read again until this factory is started anew", key)
		g.done(key)
		return none, nil
	default:
		// Only the two states GitHub names end the watch. Anything else is a reading this factory does
		// not understand — an empty answer, a gh that changed its output — and reading it as closed
		// would end the watch of an open pull request for the life of the process, silently.
		return none, fmt.Errorf("the state of the pull request %s reads %q, which is neither open nor closed", key, read)
	}
	raw, err := gh(ctx, "api", "--paginate", reviewsRequest(repository, pull))
	if err != nil {
		return none, fmt.Errorf("the reviews of %s could not be read: %w", key, err)
	}
	// What one reviewer says about a pull request is their latest review that states anything, not
	// every review they ever submitted: GitHub leaves an older entry in this list with the state it
	// was submitted with, so a maintainer who asked for changes and later approved without dismissing
	// the first review still has that CHANGES_REQUESTED entry in it. Reading the raw history would
	// answer a retracted objection with a run, so the list is read into the latest review per author
	// and only that one speaks — the same answer whatever order GitHub sends the pages in.
	latest := map[string]ghReview{}
	// --paginate answers one JSON array per page, so the pages are read as a stream of arrays.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var page []ghReview
		if err := decoder.Decode(&page); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return none, fmt.Errorf("the reviews of %s are no review list: %w", key, err)
		}
		for _, review := range page {
			if !review.states() {
				continue
			}
			if seen, ok := latest[review.User.Login]; !ok || review.newer(seen) {
				latest[review.User.Login] = review
			}
		}
	}
	newest := none
	for _, review := range latest {
		// The write access of an author is asked about only for a review that would queue a run.
		if review.State != stateChangesRequested || !review.SubmittedAt.After(newest) {
			continue
		}
		may, err := g.mayWrite(ctx, repository, review)
		if err != nil {
			// An author whose access cannot be read is no writer for this poll, and the other reviews
			// are read all the same: a login GitHub answers nothing for — a reviewing app, a deleted
			// account — would otherwise silence the maintainer's own review on this pull request for
			// good. Nothing of the failure is remembered, so the next poll asks again.
			g.warn(g.pullWarnings, key+" by "+review.User.Login, "error: %v; that review queues nothing", err)
			continue
		}
		// The author was read, so the next failure on them is worth a line again. Without this the key
		// would be set for the life of the process — the pull request's own key above says nothing about
		// it — and a later review of the same author whose access cannot be read would be passed over in
		// silence, which is the one thing a warn-once memo may not become.
		g.readable(g.pullWarnings, key+" by "+review.User.Login)
		if may {
			newest = review.SubmittedAt
		}
	}
	return newest, nil
}

// mayWrite says whether the author of a review may write to the repository, which is what makes the
// review the maintainer's gesture rather than a passer-by's: anybody may review a pull request of a
// public repository, and a run of the factory is not something a stranger starts.
//
// It is read once per review and kept: a review is submitted once, and the access its author had
// when they submitted it is what the follow-up run stands on. The answer is the push permission of
// that user and not the association GitHub puts on the review, which says that somebody is a member
// of the organisation or was invited to the repository — neither of which is write access to it.
func (g *gitHub) mayWrite(ctx context.Context, repository string, review ghReview) (bool, error) {
	return g.mayPush(ctx, repository, review.User.Login, "review "+strconv.FormatInt(review.ID, 10))
}

// mayPush says whether login may push to the repository, asked once per key: the review or the
// review thread the login wrote, which is written once.
func (g *gitHub) mayPush(ctx context.Context, repository, login, key string) (bool, error) {
	g.mu.Lock()
	known, seen := g.writers[key]
	g.mu.Unlock()
	if seen {
		return known, nil
	}
	raw, err := gh(ctx, "api", permissionRequest(repository, login), "--jq", ".user.permissions.push")
	if err != nil {
		return false, fmt.Errorf("whether %s may write to %s could not be read: %w", login, repository, err)
	}
	may := strings.TrimSpace(string(raw)) == "true"
	g.mu.Lock()
	g.writers[key] = may
	g.mu.Unlock()
	return may, nil
}

// over says that one pull request was found merged or closed, and done writes that down.
func (g *gitHub) over(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.finished[key]
}

func (g *gitHub) done(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.finished[key] = true
}

// pullKey names one pull request in a log line, the way an issue is named in one.
func pullKey(repository string, pull int) string {
	return fmt.Sprintf("%s#%d", repositoryKey(repository), pull)
}

func pullRequestRequest(repository string, pull int) string {
	return "repos/" + repository + "/pulls/" + strconv.Itoa(pull)
}

func reviewsRequest(repository string, pull int) string {
	return "repos/" + repository + "/pulls/" + strconv.Itoa(pull) + "/reviews?per_page=100"
}

// permissionRequest asks what one user may do with a repository. The login is GitHub's own answer
// and is put in the path escaped all the same: nothing this factory builds a request from goes into
// one unescaped.
func permissionRequest(repository, login string) string {
	return "repos/" + repository + "/collaborators/" + url.PathEscape(login) + "/permission"
}
