package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"
)

// Letting an issue go: the factory gives back what it holds once GitHub says the maintainer is done
// with the issue — the routing label taken off it, the issue closed, the pull request of one of its
// runs merged or closed. It is the only path in the factory that removes anything, and it removes
// nothing that is work ([ADR 0026]): the commits of the worktree go to the remote before the
// worktree does, a push that fails keeps the worktree where it is, the branch on the remote is
// deleted only when it holds nothing beyond the base it was cut from, and no record and no log of
// any run is ever touched.
//
// What is not a commit is not kept: a worker that was cancelled in the middle of an edit leaves
// changes in the worktree that were never committed, and those go with it. The pipeline commits
// every stage it finishes, so what a run has done is on the branch, and a host whose clone kept a
// worktree of every issue it has ever worked would fill up.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md

// letGo gives one issue back, in the order that keeps the work: push, then the worktree and the
// local branch, then the branch on the remote if it holds nothing, then the assignee if no pull
// request came of the issue. The record is marked last and only when the worktree is gone, because
// the mark is what says the issue is out of this factory's hands — a run marked while its worktree
// is still there would leave that directory on the host for good.
func (f *Factory) letGo(ctx context.Context, h holding, decision string) {
	held := h.run
	record, ok := f.runs.find(held.ID)
	if !ok {
		return
	}
	connected, ok := f.connected(held.Repository)
	if !ok {
		return // a repository the configuration no longer names is one nothing of is touched
	}
	clone := clonePath(f.settings.DataDir, connected.Name)
	log.Printf("letting %s#%d go: %s", connected.Name, held.Issue, decision)
	f.runs.event(record, Event{Kind: "factory", Title: "letting " + held.Branch + " go", Body: decision})

	if !f.pushWorktree(ctx, record, clone, held) {
		return
	}
	if !f.removeWorktree(ctx, record, clone, held) {
		return
	}
	f.removeRemoteBranch(ctx, record, clone, connected, held)
	f.removeAssignee(ctx, record, connected, held, h.pullRequest)
	at := time.Now()
	f.runs.update(record, func() { record.LetGoAt = &at })
	f.runs.event(record, Event{Kind: "factory", Title: "let " + held.Branch + " go",
		Body: "the worktree and the local branch are gone and this factory holds the issue no more; its records and its logs stay"})
}

// pushWorktree puts what this host holds of the branch on the remote, which is the act everything
// below waits for. What that is depends on what is left: the worktree's HEAD while the worktree is
// there — whatever the worker checked out on the way is what it did — and the local branch in the
// clone when the directory is gone and the name is not, which is what a worktree somebody removed by
// hand leaves behind. A host that holds neither has nothing to push.
//
// A push that is refused — the branch moved on the remote, the host cannot reach it — leaves the
// worktree, the branch and the assignee exactly as they are, says so on the run, and the next poll
// tries again. The alternative is a directory of commits nobody else has.
func (f *Factory) pushWorktree(ctx context.Context, record *Run, clone string, held Run) bool {
	if held.Branch == "" {
		return true
	}
	from, ref := held.Worktree, "HEAD"
	if _, err := os.Stat(held.Worktree); held.Worktree == "" || err != nil {
		from, ref = clone, "refs/heads/"+held.Branch
		if _, err := git(ctx, clone, "rev-parse", "--verify", ref); err != nil {
			return true // no worktree and no branch of it on this host: nothing of it to push
		}
	}
	if _, err := gitWithin(ctx, from, fetchTimeout, "push", "--quiet", "origin", ref+":refs/heads/"+held.Branch); err != nil {
		return f.heldUp(ctx, record, fmt.Sprintf("the commits of %s in %s could not be pushed: %v; nothing of this issue is removed from this host until they are on the remote",
			held.Branch, from, err))
	}
	return true
}

// removeWorktree takes the worktree and the local branch out of this host's clone. Both are a copy
// of what the remote now has, and nothing reaches here that was not pushed. The removal is forced
// because a cancelled worker leaves changes that were never committed, and a worktree that refuses
// to go would stay on the host for ever.
//
// A worktree that will not go stops the whole handover: the issue keeps its assignee and its branch,
// so a person finds it where the factory left it. The local branch is a name and no work as long as
// every commit under it is on the remote, which is what is read before it goes and not assumed of
// the push above: a name that holds anything the remote does not stays, and so does one that cannot
// be read ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func (f *Factory) removeWorktree(ctx context.Context, record *Run, clone string, held Run) bool {
	if held.Worktree != "" {
		if _, err := git(ctx, clone, "worktree", "remove", "--force", held.Worktree); err != nil {
			if _, there := os.Stat(held.Worktree); there == nil {
				return f.heldUp(ctx, record, fmt.Sprintf("the worktree %s could not be removed from %s: %v; the issue stays as it is until that directory is gone", held.Worktree, clone, err))
			}
		}
	}
	// A worktree whose directory somebody removed by hand is still registered in the clone, and the
	// local branch is checked out by that registration until it is pruned.
	_, _ = git(ctx, clone, "worktree", "prune")
	if held.Branch == "" {
		return true
	}
	if _, err := git(ctx, clone, "rev-parse", "--verify", "refs/heads/"+held.Branch); err != nil {
		return true // no local branch of that name: nothing to remove
	}
	unpushed, err := git(ctx, clone, "rev-list", "--count", "refs/remotes/origin/"+held.Branch+"..refs/heads/"+held.Branch)
	if err != nil || unpushed != "0" {
		f.heldUp(ctx, record, fmt.Sprintf("the local branch %s in %s holds commits %s does not have, or could not be read against it (%v); the name stays on this host with them",
			held.Branch, clone, held.Branch, err))
		return true
	}
	if _, err := git(ctx, clone, "branch", "-D", held.Branch); err != nil {
		f.heldUp(ctx, record, fmt.Sprintf("the local branch %s could not be removed from %s: %v; its commits are on the remote, so this is a name left behind and no work", held.Branch, clone, err))
	}
	return true
}

// removeRemoteBranch deletes the branch on the remote, and only when it holds nothing beyond the
// base it was cut from: a branch with a commit on it is work, and work is never deleted
// ([ADR 0026]). So a claim that produced nothing leaves nothing behind, and everything else stays
// for the person the issue is with now — the pull request is under that branch, and a run of the
// issue after this one continues on it.
//
// The remote is fetched first, because what the branch holds is decided against what the remote has
// now and not against what this clone last heard. A branch that cannot be read is left alone.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func (f *Factory) removeRemoteBranch(ctx context.Context, record *Run, clone string, connected Connected, held Run) {
	if held.Branch == "" || held.Base == "" {
		return
	}
	if _, err := gitWithin(ctx, clone, fetchTimeout, "fetch", "--quiet", "--prune", "origin"); err != nil {
		f.heldUp(ctx, record, fmt.Sprintf("%s could not be fetched into %s: %v; the branch %s stays on the remote", connected.Name, clone, err, held.Branch))
		return
	}
	beyond, err := git(ctx, clone, "rev-list", "--count", "refs/remotes/origin/"+held.Base+"..refs/remotes/origin/"+held.Branch)
	if err != nil || beyond != "0" {
		return // it carries commits, it is gone from the remote already, or it cannot be read: it stays
	}
	if _, err := gh(ctx, "api", "--method", "DELETE", "repos/"+connected.Name+"/git/refs/heads/"+held.Branch); err != nil {
		f.heldUp(ctx, record, fmt.Sprintf("the branch %s of %s holds nothing beyond %s and could not be removed from the remote: %v; remove it by hand or leave it", held.Branch, connected.Name, held.Base, err))
		return
	}
	f.runs.event(record, Event{Kind: "factory", Title: "removed " + held.Branch + " from the remote",
		Body: "it held no commit beyond " + held.Base + ", so nothing of it was work"})
}

// removeAssignee takes this host off the issue, which is what says on GitHub that the factory holds
// it: an issue it has given back must not keep it, or no other claimer and no person would ever see
// it as free. An issue whose run opened a pull request keeps the assignee — the work is with a
// person from then on, and who worked it is part of what they read ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func (f *Factory) removeAssignee(ctx context.Context, record *Run, connected Connected, held Run, pull string) {
	if pull != "" {
		return
	}
	login, err := f.login(ctx)
	if err != nil {
		f.heldUp(ctx, record, fmt.Sprintf("the assignee of %s#%d could not be removed: %v; take this host off the issue by hand", connected.Name, held.Issue, err))
		return
	}
	if _, err := gh(ctx, "issue", "edit", strconv.Itoa(held.Issue), "--repo", connected.Name, "--remove-assignee", login); err != nil {
		f.heldUp(ctx, record, fmt.Sprintf("%s could not be taken off %s#%d: %v; take this host off the issue by hand", login, connected.Name, held.Issue, err))
	}
}

// heldUp says on the run what stopped the handover, and answers false so a step can hand its own
// answer on. A factory that is stopping says nothing: the git or gh call it cancelled itself failed
// because of that and not because of this host, and the next start takes the issue up again.
func (f *Factory) heldUp(ctx context.Context, r *Run, warning string) bool {
	if ctx.Err() == nil {
		f.warn(r, "letting the issue go is held up", warning)
	}
	return false
}
