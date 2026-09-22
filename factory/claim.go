package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The remote claim: the factory takes an issue by creating its branch on GitHub, and only the
// claimer GitHub answers 201 to owns it ([ADR 0024]). Everything here is the workflow's shell
// restated in Go — the branch contract, the base branch rule — and every restatement is bound to
// its original by a drift test ([ADR 0022]).
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
// [ADR 0024]: ../docs/adr/0024-a-claim-is-the-creation-of-the-branch-through-the-api.md

// gitTimeout bounds one git command on a clone. A fetch is a request over the host's line, so it is
// given the same room a read from GitHub has.
const gitTimeout = 60 * time.Second

// errLost is the answer of a claim another claimer won: GitHub refuses the second creation of a
// reference, whatever commit it names, which is what makes the claim decide a race.
var errLost = errors.New("the branch exists on the remote already")

// claimed is what a won claim leaves behind: the branch it created on the remote, the base it was
// cut from, and the worktree the worker runs in.
type claimed struct {
	branch   string
	base     string
	worktree string
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
	base := baseBranch(ctx, connected, clone)

	// The base is fetched before its head is read, so the branch is cut from what the remote has now
	// and the worktree starts from the same commit the remote branch names.
	if _, err := git(ctx, clone, "fetch", "--quiet", "origin", "+refs/heads/"+base+":refs/remotes/origin/"+base); err != nil {
		return claimed{}, fmt.Errorf("the base branch %s of %s could not be fetched: %w; does the branch exist and can this host reach the repository?", base, connected.Name, err)
	}
	head, err := git(ctx, clone, "rev-parse", "refs/remotes/origin/"+base)
	if err != nil {
		return claimed{}, fmt.Errorf("the head of %s in %s could not be read: %w", base, clone, err)
	}

	branch := branchName(issue)
	f.runs.event(r, Event{Kind: "factory", Title: "claiming " + branch, Body: fmt.Sprintf("creating refs/heads/%s of %s at %s (%s)", branch, connected.Name, head, base)})
	if err := createRef(ctx, connected.Name, branch, head); err != nil {
		return claimed{branch: branch, base: base}, err
	}

	login, err := f.login(ctx)
	if err != nil {
		return claimed{branch: branch, base: base}, err
	}
	if _, err := gh(ctx, "issue", "edit", strconv.Itoa(issue.Number), "--repo", connected.Name, "--add-assignee", login); err != nil {
		return claimed{branch: branch, base: base}, fmt.Errorf("issue #%d of %s could not be assigned to %s: %w", issue.Number, connected.Name, login, err)
	}

	// The worktree lives inside the clone, under the path the local workflow uses: Claude Code's
	// trust of a workspace covers what lies within it, and an unattended start must meet no dialog.
	worktree := filepath.Join(clone, ".claude", "worktrees", filepath.FromSlash(branch))
	if _, err := git(ctx, clone, "worktree", "add", "--quiet", "-b", branch, worktree, head); err != nil {
		return claimed{branch: branch, base: base}, fmt.Errorf("the worktree for %s could not be created in %s: %w", branch, clone, err)
	}
	f.runs.event(r, Event{Kind: "factory", Title: "claimed " + branch, Body: "worktree " + worktree})
	return claimed{branch: branch, base: base, worktree: worktree}, nil
}

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
// in Go (wf_base_branch in plugins/orchestrator/scripts/lib.sh): the explicit setting first, then
// the head the remote points at, then the repository's default branch on GitHub, and main when
// nothing answers at all. A drift test binds the two ([ADR 0022]).
//
// The explicit setting is the repository's in the configuration, where the shell reads
// WF_BASE_BRANCH from the repository's settings; the remote's head is read from the clone, where
// the shell reads it from the checkout.
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
func baseBranch(ctx context.Context, connected Connected, clone string) string {
	if connected.Base != "" {
		return connected.Base
	}
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

// git runs one command on a clone and answers with its output, the trailing newline removed. Like
// gh it gets a process group and a deadline of its own, because a fetch that hangs must not hold
// the run it belongs to for ever.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return endGroup(cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), firstLine(reason))
	}
	return strings.TrimSpace(string(out)), nil
}
