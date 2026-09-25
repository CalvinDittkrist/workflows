package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"
)

// Letting an issue go: the factory gives back what it holds once GitHub says the maintainer is done
// with the issue: the routing label taken off it, the issue closed, the pull request of one of its
// runs merged or closed. It is the only path in the factory that removes anything, and it removes
// nothing that is work ([ADR 0026]): the commits of the worktree go to the remote before the
// worktree does, a push that fails keeps the worktree where it is, the branch on the remote is
// deleted only when it holds nothing beyond the base it was cut from or its pull request was merged,
// and no record and no log of any run is ever touched.
//
// What is not a commit is not kept: a worker that was cancelled in the middle of an edit leaves
// changes in the worktree that were never committed, and those go with it. The pipeline commits
// every stage it finishes, so what a run has done is on the branch, and a host whose clone kept a
// worktree of every issue it has ever worked would fill up.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md

// handoverTimeout bounds every handover of one poll: their pushes, their fetches and the requests
// that give the issues back. Handovers run in the working loop, so whatever they wait for the line
// waits for too: a poll that does not happen, an interface whose last poll goes stale under a
// reader. What one of them transfers is the commits of one branch and not a repository, which is
// why they are given the room of a few polls and not the hour a clone has; each transfer may take
// that room, and this deadline over the whole pass is what keeps a poll to it however many
// decisions arrive at once.
//
// A handover that is cut loses nothing. Nothing of it is removed before the work is on the remote,
// every step reads the host and the remote again rather than trusting what the last one left, and a
// later poll starts it over.
const handoverTimeout = 2 * time.Minute

// errHandoverCut is the cause that deadline carries, which is what tells a handover that ran out of
// its own time from one a stopping factory cut short: the first is trouble on this host and is said
// on the run, the second is the operator's own act and says nothing.
var errHandoverCut = errors.New("letting the issue go took longer than " + handoverTimeout.String())

// letGo gives one issue back, in the order that keeps the work: push, then the worktree and the
// local branch, then the branch on the remote if it holds nothing or was merged, then the assignee
// if no pull request came of the issue. The record is marked last and only when the worktree is gone and this
// host is off the issue, because the mark is what says the issue is out of this factory's hands: a
// run marked while its worktree is still there would leave that directory on the host for good, and
// one marked while GitHub still names this host as the assignee would leave an issue nobody (no
// other claimer, no person reading the frontier) ever sees as free again.
//
// Every step of it may be made again: each reads the host and the remote as they are rather than
// what the step before it left, so a handover that stops halfway is taken up whole by a later poll.
//
// The context is the one handoverTimeout bounds the poll's handovers by, so a step of this one that
// hangs takes time from the handovers behind it and from nothing else.
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
	f.removeRemoteBranch(ctx, record, clone, connected, held, h.pullRequest)
	if !f.removeAssignee(ctx, record, connected, held, h.pullRequest) {
		return
	}
	at := time.Now()
	f.runs.update(record, func() { record.LetGoAt = &at })
	f.runs.event(record, Event{Kind: "factory", Title: "let " + held.Branch + " go",
		Body: "the worktree and the local branch are gone and this factory holds the issue no more; its records and its logs stay"})
}

// pushWorktree puts what this host holds of the branch on the remote, which is the act everything
// below waits for. What that is depends on what is left: the worktree's HEAD while the worktree is
// there (whatever the worker checked out on the way is what it did), and the local branch in the
// clone when the directory is gone and the name is not, which is what a worktree somebody removed by
// hand leaves behind. A host that holds neither has nothing to push.
//
// A push that is refused because the branch moved on the remote is no loss when the remote branch
// holds what is pushed: a maintainer who merged the base in or fixed a review on the pull request
// pushed on top of the host's commits. The branch is fetched and, when it contains them, the
// handover carries on as after a push that landed.
//
// Any other refusal (a remote branch that does not hold the commits, a host that cannot reach it),
// leaves the worktree, the branch and the assignee exactly as they are, says so on the run with all
// git said, and the next poll tries again. The alternative is a directory of commits nobody else has.
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
	if _, err := gitWithin(ctx, from, handoverTimeout, "push", "--quiet", "origin", ref+":refs/heads/"+held.Branch); err != nil {
		if onRemote(ctx, clone, from, ref, held.Branch) {
			f.runs.event(record, Event{Kind: "factory", Title: held.Branch + " is on the remote already",
				Body: "the push was refused because the branch moved on the remote, and origin/" + held.Branch + " holds the commits of " + from})
			return true
		}
		return f.heldUp(ctx, record, fmt.Sprintf("the commits of %s in %s could not be pushed: %v; nothing of this issue is removed from this host until they are on the remote",
			held.Branch, from, err))
	}
	return true
}

// onRemote says whether the branch on the remote holds the commit ref points at in from, read after
// fetching the branch into the clone, whose remote-tracking branches its worktrees share. A branch
// that cannot be fetched or read holds nothing as far as this is concerned.
func onRemote(ctx context.Context, clone, from, ref, branch string) bool {
	tracking := "refs/remotes/origin/" + branch
	if _, err := gitWithin(ctx, clone, handoverTimeout, "fetch", "--quiet", "origin", "+refs/heads/"+branch+":"+tracking); err != nil {
		return false
	}
	_, err := git(ctx, from, "merge-base", "--is-ancestor", ref, tracking)
	return err == nil
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
// Such a name stops the handover too, rather than leaving it to run on without it. The push above
// puts the worktree's HEAD on the remote, and a worker that left that HEAD behind its own branch
// (detached, or moved on by hand) is the one way the two differ; carrying on would then read a
// remote branch that holds nothing beyond its base and delete it, and the commits the name holds
// would be on this host alone. The next poll finds the worktree gone, pushes the name itself and
// takes the issue the rest of the way.
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
		return f.heldUp(ctx, record, fmt.Sprintf("the local branch %s in %s holds commits origin/%s does not have, or could not be read against it (%v); it stays with them, and nothing more of this issue is given back until they are on the remote",
			held.Branch, clone, held.Branch, err))
	}
	if _, err := git(ctx, clone, "branch", "-D", held.Branch); err != nil {
		f.heldUp(ctx, record, fmt.Sprintf("the local branch %s could not be removed from %s: %v; its commits are on the remote, so this is a name left behind and no work", held.Branch, clone, err))
	}
	return true
}

// removeRemoteBranch deletes the branch on the remote when it holds no work the base does not have
// ([ADR 0026]), which is one of two readings. The first is a branch with no commit beyond the base
// it was cut from, so a claim that produced nothing leaves nothing behind. The second is a branch
// whose pull request GitHub reports as merged: its work is in the base whatever the merge method, and
// a squash or a rebase leaves commits on the branch that the base does not have by their names.
// Everything else stays for the person the issue is with now: a closed pull request that was not
// merged is still under that branch, and a run of the issue after this one continues on it.
//
// The remote is fetched first, because what the branch holds is decided against what the remote has
// now and not against what this clone last heard. A branch that cannot be read is left alone.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func (f *Factory) removeRemoteBranch(ctx context.Context, record *Run, clone string, connected Connected, held Run, pull string) {
	if held.Branch == "" || held.Base == "" {
		return
	}
	if _, err := gitWithin(ctx, clone, handoverTimeout, "fetch", "--quiet", "--prune", "origin"); err != nil {
		f.heldUp(ctx, record, fmt.Sprintf("%s could not be fetched into %s: %v; the branch %s stays on the remote", connected.Name, clone, err, held.Branch))
		return
	}
	carries, err := carriesWork(ctx, clone, held.Branch, held.Base)
	if err != nil {
		return // it is gone from the remote already, or it cannot be read: it stays
	}
	reason := "it held no commit beyond " + held.Base + ", so nothing of it was work"
	if carries {
		if !mergedAtTip(ctx, clone, connected.Name, held.Branch, pull) {
			return // it carries commits no merged pull request stands for: it stays
		}
		reason = "its pull request " + pull + " was merged, so its work is in " + held.Base
	}
	if _, err := gh(ctx, "api", "--method", "DELETE", "repos/"+connected.Name+"/git/refs/heads/"+held.Branch); err != nil {
		f.heldUp(ctx, record, fmt.Sprintf("the branch %s of %s holds no work beyond %s and could not be removed from the remote: %v; remove it by hand or leave it", held.Branch, connected.Name, held.Base, err))
		return
	}
	f.runs.event(record, Event{Kind: "factory", Title: "removed " + held.Branch + " from the remote", Body: reason})
}

// mergedAtTip says whether the pull request the claim opened is merged, is of this branch of the
// repository itself, and was merged at the commit the branch is at on the remote now. The last one is
// what keeps a commit somebody pushed to the branch after the merge (or a cancelled worker's that
// the push above put there) from going with it. Only the commits the merge took are in the base. A
// pull request that cannot be read is no answer, so the branch stays.
func mergedAtTip(ctx context.Context, clone, repository, branch, link string) bool {
	if link == "" {
		return false
	}
	pull, named, err := readPull(ctx, repository, link)
	if err != nil || !named || !pull.Merged || !pull.of(repository, branch) || pull.Head.SHA == "" {
		return false
	}
	tip, err := git(ctx, clone, "rev-parse", "--verify", "refs/remotes/origin/"+branch)
	return err == nil && tip == pull.Head.SHA
}

// removeAssignee takes this host off the issue, which is what says on GitHub that the factory holds
// it: an issue it has given back must not keep it, or no other claimer and no person would ever see
// it as free. An issue whose run opened a pull request keeps the assignee: the work is with a
// person from then on, and who worked it is part of what they read ([ADR 0026]).
//
// It is the last step of the handover and the handover stands or falls with it: a removal GitHub
// refused (a token that expired, a rate limit, a factory that was stopped in the middle of it)
// leaves the issue marked as this host's, and only a run that is not yet marked as let go is tried
// again. So the handover stops here, says so on the run, and a later poll asks GitHub once more.
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md
func (f *Factory) removeAssignee(ctx context.Context, record *Run, connected Connected, held Run, pull string) bool {
	if pull != "" {
		return true
	}
	login, err := f.login(ctx)
	if err != nil {
		return f.heldUp(ctx, record, fmt.Sprintf("the assignee of %s#%d could not be removed: %v; the issue stays as this host's until it can be taken off", connected.Name, held.Issue, err))
	}
	if _, err := gh(ctx, "issue", "edit", strconv.Itoa(held.Issue), "--repo", connected.Name, "--remove-assignee", login); err != nil {
		return f.heldUp(ctx, record, fmt.Sprintf("%s could not be taken off %s#%d: %v; the issue stays as this host's until that removal is made", login, connected.Name, held.Issue, err))
	}
	return true
}

// heldUp says on the run what stopped the handover, and answers false so a step can hand its own
// answer on. A factory that is stopping says nothing: the git or gh call it cancelled itself failed
// because of that and not because of this host, and the next start takes the issue up again. A
// handover that ran out of its own time (errHandoverCut) is this host's trouble all the same (a
// line that cannot carry a branch in two minutes), so that one is said.
func (f *Factory) heldUp(ctx context.Context, r *Run, warning string) bool {
	if ctx.Err() == nil || errors.Is(context.Cause(ctx), errHandoverCut) {
		f.warn(r, "letting the issue go is held up", warning)
	}
	return false
}
