package main

import (
	"context"
	"fmt"
	"os"
)

// Keeping the work of a held issue on the remote. A run that ends without a pull request holds its
// issue: branch, worktree and assignee stay. Every commit its sessions made that no stage pushed is
// on this host alone. So the ending of such a run pushes the worktree's HEAD to the issue's branch,
// the same push the let-go makes. The host then holds a copy of what origin holds, and a host that
// loses its disk or its data directory loses no commit ([ADR 0026]).
//
// [ADR 0026]: ../docs/adr/0026-the-factory-never-deletes-work-on-its-own.md

// pushEnding pushes the worktree of a run that ends holding its issue. A run that ends ready pushed
// its branch for its pull request, and a cancelled one gives the issue back. A run that holds
// nothing, such as a lost claim, has no branch of this factory's to push. A worktree removed by hand
// leaves the local branch in the clone, which is pushed instead, as the let-go does. A host with
// neither has nothing to push, and a push with nothing new says nothing.
//
// It runs from the factory after the worker's process group has ended, with a context of its own.
// A run that ends because the factory is stopping pushes all the same. handoverTimeout keeps a hung
// remote from holding the poll or the stop. A push that fails is a warning of the run and is named
// in its notification. The outcome, the worktree, the branch and the assignee stay as they are.
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
	clone := clonePath(f.settings.DataDir, held.Repository)
	from, ref := held.Worktree, "HEAD"
	if _, err := os.Stat(held.Worktree); err != nil {
		from, ref = clone, "refs/heads/"+held.Branch
		if _, err := git(context.Background(), clone, "rev-parse", "--verify", ref); err != nil {
			return
		}
	}
	pushed, _, err := pushBranch(context.Background(), clone, from, ref, held.Branch)
	if err != nil {
		warning := fmt.Sprintf("the commits of %s in %s could not be pushed when this run ended: %v; they are on this host alone until a push lands, and letting the issue go pushes them again",
			held.Branch, from, err)
		f.warn(r, "the worktree is not on the remote", warning)
		f.runs.update(r, func() { r.Unpushed = warning })
		return
	}
	if pushed {
		f.runs.event(r, Event{Kind: "factory", Title: "copied " + held.Branch + " to the remote",
			Body: "the commits of " + held.Worktree + " are on the remote, so this host holds a copy of what origin holds"})
	}
}

// PushCutOff pushes the worktrees of the runs this start found active and recorded as interrupted.
// The store wrote their ending and has no git, so the push of that ending is made here. It comes
// before the first poll resumes any of them and before their notification is made. A paused factory
// writes nothing anywhere, so it pushes them when it works again (followPause), once per process.
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
