#!/usr/bin/env bash
# Bring this branch up to its base before the work stage runs its first gate (issue #128).
# Usage: base-sync.sh
# Exit: 0 the branch carries the base (already, or through the merge this call made), 2 the merge
# conflicts and waits for a person, 1 error.
#
# A branch taken up days after it was made carries the base as it was then, and its first gate fails on
# whatever the base has fixed since: a worker then asks the maintainer about a problem the base has
# already solved. The base is merged, never rebased: the branch may be pushed, and every gate and panel
# record names a commit, which a merge keeps in history and a rebase would take away. The merge is a merge
# commit even where a fast-forward would do, so it is one commit a reader of the log and of the report can
# point at. A conflict is left in the worktree as git leaves it, for a person: which side of it is right is
# a decision about the change, not something this script or a worker guesses.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

base=$(wf_base_branch)
branch=$(wf_branch)

conflicted() { git diff --name-only --diff-filter=U | sed 's/^/  - /'; }

blocked() {
  if [ -z "$(git diff --name-only --diff-filter=U)" ]; then
    wf_kv base_sync "the merge of $1 into $branch is resolved but not committed"
    printf 'blocked: the merge of %s into %s is resolved but not committed; review it, commit it and run /worker:work again.\n' "$1" "$branch"
    exit 2
  fi
  wf_kv base_sync "conflicts merging $1 into $branch"
  printf 'conflicted_files:\n'; conflicted
  printf 'blocked: the merge of %s into %s conflicts; resolve the files above, commit the merge and run /worker:work again. The worktree is left in the merge state; git merge --abort undoes it.\n' "$1" "$branch"
  exit 2
}

# A merge a person has not finished yet is the same stop as a new one: never a second merge over it.
if git rev-parse -q --verify MERGE_HEAD >/dev/null 2>&1; then
  blocked "$(git rev-parse --short MERGE_HEAD)"
fi
dirty=$(wf_dirty_tree)
[ -z "$dirty" ] || wf_die "the working tree has uncommitted changes, and a merge over them mixes them into the merge commit:
$dirty
commit them or remove them, then run base-sync.sh again"

# The remote base is the one the pull request is merged into, so it is fetched when there is a remote; a
# repository without one merges its local base branch, which is what wf_base_ref names then.
if git remote get-url origin >/dev/null 2>&1; then
  git fetch -q origin "$base" || wf_die "cannot fetch $base from origin; check the network and 'git remote -v', then run base-sync.sh again"
fi
ref=$(wf_base_ref)
git rev-parse -q --verify "$ref^{commit}" >/dev/null || wf_die "the base $ref names no commit; set WF_BASE_BRANCH to the branch this one is merged into"

behind=$(git rev-list --count "HEAD..$ref")
if [ "$behind" -eq 0 ]; then
  wf_kv base_sync "current: $branch carries $ref, nothing merged"
  exit 0
fi

if ! out=$(git merge --no-ff -m "Merge $ref into $branch" "$ref" 2>&1); then
  git rev-parse -q --verify MERGE_HEAD >/dev/null 2>&1 && blocked "$ref"
  # No merge in progress: git refused before it began (an untracked file it would overwrite, a hook).
  wf_die "git refused to merge $ref: $(printf '%s' "$out" | tail -n 5); fix that and run base-sync.sh again"
fi
wf_kv base_sync "merged $ref into $branch at $(git rev-parse --short HEAD), $behind commit(s) of the base the branch lacked; name this merge in the run's report"
