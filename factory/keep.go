package main

import (
	"context"
	"fmt"
	"os"
)

// Keeping the work of a held issue on the remote. A run that ends without a pull request holds its
// issue (branch, worktree and assignee stay), and every commit its sessions made that no stage
// pushed is in the worktree on this host and nowhere else. So the ending of such a run pushes the
// worktree's HEAD to the issue's branch, the same push the let-go makes: what the host holds is then
// a copy of what origin holds, and a host that loses its disk or its data directory loses no commit
// ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md

// pushEnding pushes the worktree of a run that ends holding its issue. A run that ends ready pushed
// its branch for its pull request, a cancelled one gives the issue back, and a run that holds
// nothing (a lost claim, a resume that did not take the issue back) has no branch of this factory's
// to push. A worktree that is gone has nothing to push, and a push with nothing new says nothing.
//
// It runs from the factory after the worker's process group has ended, with a context of its own:
// a run that ends because the factory is stopping pushes all the same, and handoverTimeout keeps a
// hung remote from holding the poll or the stop. A push that fails is a warning of the run and is
// named in its notification; the outcome, the worktree, the branch and the assignee stay as they are.
func (f *Factory) pushEnding(r *Run, outcome string) {
	switch outcome {
	case outcomeBlocked, outcomeFailed, outcomeTimeout, outcomeInterrupted, outcomeQuota:
	default:
		return
	}
	held, ok := f.runs.get(r.ID)
	if !ok || f.fake || !held.Holding || held.LetGoAt != nil || held.Branch == "" || held.Worktree == "" {
		return
	}
	if _, err := os.Stat(held.Worktree); err != nil {
		return
	}
	pushed, _, err := pushBranch(context.Background(), clonePath(f.settings.DataDir, held.Repository), held.Worktree, "HEAD", held.Branch)
	if err != nil {
		warning := fmt.Sprintf("the commits of %s in %s could not be pushed when this run ended: %v; they are on this host alone until a push lands, and letting the issue go pushes them again",
			held.Branch, held.Worktree, err)
		f.warn(r, "the worktree is not on the remote", warning)
		f.runs.update(r, func() { r.Unpushed = warning })
		return
	}
	if pushed {
		f.runs.event(r, Event{Kind: "factory", Title: "copied " + held.Branch + " to the remote",
			Body: "the commits of " + held.Worktree + " are on the remote, so this host holds a copy of what origin holds"})
	}
}

// PushCutOff pushes the worktrees of the runs this start found active and recorded as interrupted:
// the store wrote their ending and has no git, so the push of that ending is made here, before the
// first poll resumes any of them and before their notification is made. A paused factory writes
// nothing anywhere, so it pushes them when it works again (followPause), once per process.
func (f *Factory) PushCutOff() {
	if f.Paused() || f.cutOffPushed {
		return
	}
	f.cutOffPushed = true
	for _, id := range f.runs.cutOff {
		if run, ok := f.runs.find(id); ok {
			f.pushEnding(run, run.Outcome)
		}
	}
}
