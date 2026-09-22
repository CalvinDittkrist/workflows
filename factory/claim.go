package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The remote claim: the factory takes an issue by creating its branch on GitHub, and only the
// claimer GitHub answers 201 to owns it ([ADR 0024]). Everything here is the workflow's shell
// restated in Go — the branch contract, the base branch rule — and every restatement is bound to
// its original by a drift test ([ADR 0022]).
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
// [ADR 0024]: ../docs/adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md

// gitTimeout bounds one git command on a clone: a question asked of the repository on disk, which
// answers at once or is hanging. fetchTimeout bounds the one command that is a transfer instead —
// the whole remote, over the host's line — and it is given the room a clone of the same repository
// has, because failing a claim for a slow line costs the issue its place in the line.
const (
	gitTimeout   = 60 * time.Second
	fetchTimeout = cloneTimeout
)

// errLost is the answer of a claim another claimer won: GitHub refuses the second creation of a
// reference, whatever commit it names, which is what makes the claim decide a race.
var errLost = errors.New("the branch exists on the remote already")

// claimed is what a won claim leaves behind: the branch it created on the remote, the base it was
// cut from, and the worktree the worker runs in. created says the branch is on the remote, which is
// what an operator reading a failed run needs: the claim after that point leaves it behind.
type claimed struct {
	branch   string
	base     string
	worktree string
	created  bool
}

// claim takes one issue on the remote and prepares the worktree its worker runs in. The order is the
// one [ADR 0024] fixes: the branch first, because that is the act with exactly one winner, and the
// assignee and the worktree only after GitHub said this factory owns the issue.
//
// [ADR 0024]: ../docs/adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md
func (f *Factory) claim(ctx context.Context, r *Run, issue Issue) (claimed, error) {
	connected, ok := f.connected(issue.Repository)
	if !ok { // a run of a repository nobody connected cannot happen; said rather than assumed
		return claimed{}, fmt.Errorf("%s is not a connected repository", issue.Repository)
	}
	clone := clonePath(f.settings.DataDir, connected.Name)

	// The remote is fetched before anything is read of it, so the rule below reads what the
	// repository says now and the branch is cut from what the base holds now: a clone is written once
	// and never checked out again, and its working tree is the day this host cloned it.
	if _, err := gitWithin(ctx, clone, fetchTimeout, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return claimed{}, fmt.Errorf("%s could not be fetched into %s: %w; can this host reach the repository?", connected.Name, clone, err)
	}
	// A fetch does not touch refs/remotes/origin/HEAD. That reference is written once, when this host
	// cloned the repository, so "the head the remote points at" would be the head it pointed at then
	// — and a repository that moves its default branch afterwards would be branched off the old one
	// for as long as this clone lives, or off a name the remote no longer has at all. Asking the
	// remote for it again with every claim is what keeps the rule's second step true.
	if _, err := git(ctx, clone, "remote", "set-head", "origin", "--auto"); err != nil {
		log.Printf("error: the head %s points at could not be read into %s: %v; this claim uses what that clone last knew", connected.Name, clone, err)
	}
	base := baseBranch(ctx, connected, clone)
	head, err := git(ctx, clone, "rev-parse", "refs/remotes/origin/"+base)
	if err != nil {
		return claimed{}, fmt.Errorf("the head of the base branch %s of %s could not be read: %w; is that branch on the remote?", base, connected.Name, err)
	}

	// The user this host is logged in as is read before the branch is created: it is a host fact that
	// says nothing about the race, and asking for it first keeps a host that cannot answer it from
	// leaving a branch behind for nothing.
	login, err := f.login(ctx)
	if err != nil {
		return claimed{}, err
	}

	branch := branchName(issue)
	f.runs.event(r, Event{Kind: "factory", Title: "claiming " + branch, Body: fmt.Sprintf("creating refs/heads/%s of %s at %s (%s)", branch, connected.Name, head, base)})
	if err := createRef(ctx, connected.Name, branch, head); err != nil {
		return claimed{branch: branch, base: base}, err
	}
	// From here on the branch is on the remote whatever else fails, and every claimer after this one
	// loses the issue to it. Nothing rolls it back — work is never deleted ([ADR 0026]) — so a failure
	// below says the branch is left behind and the operator decides.
	//
	// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
	won := claimed{branch: branch, base: base, created: true}

	if _, err := gh(ctx, "issue", "edit", strconv.Itoa(issue.Number), "--repo", connected.Name, "--add-assignee", login); err != nil {
		return won, fmt.Errorf("issue #%d of %s could not be assigned to %s: %w", issue.Number, connected.Name, login, err)
	}

	// The worktree lies where the local workflow puts its own (wf_create_worktree in the
	// orchestrator's lib.sh): under .claude/worktrees of the checkout, named after the branch with
	// every slash turned into a hyphen, and the directory kept out of the clone's status. One
	// convention for both drivers, so a maintainer who opens this host's clone finds what a Herdr
	// session would have left. Trust needs no dialog here either way: Claude Code keys it on the
	// checkout a worktree belongs to, and a print-mode session is never asked
	// (https://code.claude.com/docs/en/permissions.md, checked 2026-09-22).
	worktree := filepath.Join(clone, ".claude", "worktrees", strings.ReplaceAll(branch, "/", "-"))
	excludeWorktrees(clone)
	if _, err := git(ctx, clone, "worktree", "add", "--quiet", "-b", branch, worktree, head); err != nil {
		return won, fmt.Errorf("the worktree for %s could not be created in %s: %w; a run before this one may have left a branch or a worktree of that name in this clone, which nothing here removes", branch, clone, err)
	}
	f.runs.event(r, Event{Kind: "factory", Title: "claimed " + branch, Body: "worktree " + worktree})
	won.worktree = worktree
	return won, nil
}

// excludeWorktrees keeps the worktrees of a clone out of its own status, the entry wf_create_worktree
// writes for the local workflow. It is cosmetic: a clone that refuses the write is still a clone a
// worker can run in, so a claim is never failed for it.
func excludeWorktrees(clone string) {
	path := filepath.Join(clone, ".git", "info", "exclude")
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return
	}
	for _, line := range strings.Split(string(current), "\n") {
		if strings.TrimSpace(line) == worktreesEntry {
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	entry := worktreesEntry + "\n"
	if len(current) > 0 && !strings.HasSuffix(string(current), "\n") {
		entry = "\n" + entry // a file whose last line was never ended keeps it
	}
	_, _ = file.WriteString(entry)
}

// worktreesEntry is the line the local workflow appends to .git/info/exclude.
const worktreesEntry = ".claude/worktrees/"

// connected is the configured repository of that name.
func (f *Factory) connected(name string) (Connected, bool) {
	for _, c := range f.settings.Repositories {
		if c.Name == name {
			return c, true
		}
	}
	return Connected{}, false
}

// login is the user this host's gh is logged in as, read once: it is the machine user the factory
// runs as ([ADR 0027]) and it does not change while the process lives.
//
// [ADR 0027]: ../docs/adr/0027-the-factorys-isolation-boundary-is-the-host.md
func (f *Factory) login(ctx context.Context) (string, error) {
	f.mu.Lock()
	known := f.user
	f.mu.Unlock()
	if known != "" {
		return known, nil
	}
	raw, err := gh(ctx, "api", "user", "--jq", ".login")
	if err != nil {
		return "", fmt.Errorf("the user this host is logged in as could not be read: %w; check `gh auth status` on this host", err)
	}
	user := strings.TrimSpace(string(raw))
	if user == "" {
		return "", errors.New("the user this host is logged in as is empty; check `gh auth status` on this host")
	}
	f.mu.Lock()
	f.user = user
	f.mu.Unlock()
	return user, nil
}

// refObject is what GitHub answers when a reference is created: the reference and the commit it
// names. Only the failure is read for its meaning, so the body is decoded to confirm the shape and
// nothing more.
type refObject struct {
	Ref string `json:"ref"`
}

// createRef creates the issue's branch on the remote. This is the claim: GitHub refuses the second
// creation of a reference with 422 "Reference already exists", even for the same commit, and that
// refusal is the only act in the workflow with exactly one winner ([ADR 0024]). A claimer that meets
// it has lost and touches nothing else.
//
// [ADR 0024]: ../docs/adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md
func createRef(ctx context.Context, repository, branch, sha string) error {
	raw, err := gh(ctx, "api", "--method", "POST", "repos/"+repository+"/git/refs",
		"-f", "ref=refs/heads/"+branch, "-f", "sha="+sha)
	if err != nil {
		if exists.MatchString(said(err)) {
			return errLost
		}
		return fmt.Errorf("the branch %s of %s could not be created: %w", branch, repository, err)
	}
	var created refObject
	if err := json.Unmarshal(raw, &created); err != nil || created.Ref == "" {
		return fmt.Errorf("the branch %s of %s was created and GitHub answered with no reference: %s", branch, repository, firstLine(string(raw)))
	}
	return nil
}

// exists recognises the one refusal that means another claimer was first. GitHub says "Reference
// already exists" with status 422, and gh puts that sentence in what it prints; a claim that fails
// for any other reason is an error and not a lost race.
var exists = regexp.MustCompile(`(?i)reference already exists`)

// baseBranch is the branch a run of this repository is cut from. It is the workflow's rule restated
// in Go (wf_base_branch in plugins/orchestrator/scripts/lib.sh): the explicit setting first, then the
// head the remote points at, then the repository's default branch on GitHub, and main when nothing
// answers at all. A drift test binds the two ([ADR 0022]).
//
// The explicit setting is WF_BASE_BRANCH, which a local session is given by the repository's own
// settings file; the factory reads that same file out of the repository's default branch, and the
// configuration of this host says a base of its own above it. The remote's head is read from the
// clone, where the shell reads it from the checkout.
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
func baseBranch(ctx context.Context, connected Connected, clone string) string {
	if connected.Base != "" {
		return connected.Base
	}
	// The branch the repository is worked from when it declares nothing is also the branch its
	// declaration is read from: it is the checkout a local session of this repository would have.
	def := defaultBranch(ctx, connected, clone)
	if declared := declaredBase(ctx, clone, def); declared != "" {
		return declared
	}
	return def
}

// defaultBranch is the branch the remote points at, as the clone knows it after a fetch, then the
// default branch GitHub names, then main: the steps of the rule below the explicit setting.
func defaultBranch(ctx context.Context, connected Connected, clone string) string {
	if head, err := git(ctx, clone, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil && head != "" {
		return strings.TrimPrefix(head, "origin/")
	}
	if raw, err := gh(ctx, "repo", "view", connected.Name, "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name"); err == nil {
		if name := strings.TrimSpace(string(raw)); name != "" {
			return name
		}
	}
	return "main"
}

// declaredBase is the base branch a repository declares for itself: WF_BASE_BRANCH in the env block
// of the .claude/settings.json its checkout carries (README, Configuration). That file is where a
// local session gets the variable wf_base_branch reads, so this is the same explicit setting and not
// a second one — and reading it here is what keeps the branch the factory cuts and the base the
// worker reviews and opens its pull request against the same branch.
//
// It is read out of the fetched reference and not out of the clone's working tree: that tree is
// written once, when this host cloned the repository, and a repository that moves its line of work
// afterwards would otherwise be branched off the base it named years ago.
//
// The file belongs to the repository, so its value is held to the rule a configured base is held to
// before it reaches a ref or a command line; anything else is read as if the repository said nothing.
func declaredBase(ctx context.Context, clone, branch string) string {
	raw, err := git(ctx, clone, "show", "refs/remotes/origin/"+branch+":.claude/settings.json")
	if err != nil {
		return ""
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		log.Printf("error: the .claude/settings.json of %s on %s is not JSON: %v; this run is branched as if the repository declared no base", clone, branch, err)
		return ""
	}
	declared := settings.Env["WF_BASE_BRANCH"]
	if declared == "" { // the repository declares nothing, which is the usual case and says nothing
		return ""
	}
	if !validBase(declared) {
		log.Printf("error: %s declares WF_BASE_BRANCH=%q on %s, which is no branch name; this run is branched as if it declared none", clone, declared, branch)
		return ""
	}
	return declared
}

// branchName is the branch contract of the workflow, restated in Go: <type>/<issue>-<slug>
// ([ADR 0003]). It is what the worker's session start reads the issue number from, so the shape is
// the pipeline's and not the factory's, and a drift test binds it to the shell ([ADR 0022]).
//
// [ADR 0003]: ../docs/adr/0003-herdr-worktree-per-issue.md
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
func branchName(issue Issue) string {
	return branchType(issue.Labels) + "/" + strconv.Itoa(issue.Number) + "-" + slug(issue.Title)
}

// branchType maps an issue's labels to the type of its branch (wf_branch_type). The order is the
// shell's: the first case that matches wins, whatever order the labels arrive in.
func branchType(labels []string) string {
	has := func(names ...string) bool {
		for _, label := range labels {
			for _, name := range names {
				if label == name {
					return true
				}
			}
		}
		return false
	}
	switch {
	case has("bug", "fix"):
		return "fix"
	case has("docs", "documentation"):
		return "docs"
	case has("chore", "maintenance"):
		return "chore"
	default:
		return "feat"
	}
}

var (
	// A URL in a title is dropped whole rather than spelled out in the branch name (wf_slug).
	link = regexp.MustCompile(`https?://[^ ]*`)
	// Everything a branch name does not carry becomes one hyphen, however long the run of it.
	notSlug = regexp.MustCompile(`[^a-z0-9]+`)
	// The umlauts the shell transliterates, so a German title reads as a word and not as hyphens.
	transliterated = strings.NewReplacer(
		"ä", "ae", "ö", "oe", "ü", "ue", "Ä", "ae", "Ö", "oe", "Ü", "ue", "ß", "ss")
)

// slugLength is how much of the title the branch name carries (wf_slug's cut -c1-40).
const slugLength = 40

// slug is the branch-safe form of an issue title (wf_slug in plugins/orchestrator/scripts/lib.sh):
// URLs dropped, German umlauts transliterated, ASCII lower case, every other run of characters one
// hyphen, no hyphen at either end, at most 40 characters. A drift test binds it to that original.
func slug(title string) string {
	s := link.ReplaceAllString(title, "")
	s = transliterated.Replace(s)
	// Lower case is the shell's `tr '[:upper:]' '[:lower:]'`, which is ASCII: every other character
	// is on its way to a hyphen anyway.
	s = strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
	s = strings.Trim(notSlug.ReplaceAllString(s, "-"), "-")
	if len(s) > slugLength { // the string is ASCII here, so a character is a byte
		s = s[:slugLength]
	}
	return strings.TrimRight(s, "-")
}

// git runs one command on a clone and answers with its output, the trailing newline removed. Like gh
// it gets a process group and a deadline of its own, because a fetch that hangs must not hold the run
// it belongs to for ever.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	return gitWithin(ctx, dir, gitTimeout, args...)
}

// gitWithin is git with a deadline of the caller's choosing, for the commands the usual one is too
// short for.
func gitWithin(ctx context.Context, dir string, within time.Duration, args ...string) (string, error) {
	out, reason, err := command(ctx, within, "git", append([]string{"-C", dir}, args...)...)
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), reason)
	}
	return strings.TrimSpace(string(out)), nil
}
