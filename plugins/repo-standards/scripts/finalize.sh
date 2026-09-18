#!/usr/bin/env bash
# The last step of the apply phase, after the cleanup pull request is merged: configure the GitHub workspace,
# keep its snapshot on the catalogue issue, remove the cleanup worktree, and end with the check.
# Usage: finalize.sh
# Refuses while the pull request from chore/standardize is open or was closed without a merge, because the
# rulesets require the job check that the pull request brings. workspace.sh --apply runs only when the
# workspace category was approved; its snapshot is posted as a comment on the catalogue issue. The check
# (check.sh) runs on the head of the default branch on origin. Exit 1 when the check or the workspace fails.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=lib.sh
. "$here/lib.sh"
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
for c in gh jq git; do command -v "$c" >/dev/null 2>&1 || die "$c is required but not on PATH"; done
answers=$(decisions) || exit 1
dir=$(state_dir)
err=$(mktemp); tmp=$(mktemp); trap 'rm -f "$err" "$tmp"' EXIT
github_repo || exit 1
catalogue=$(catalogue_issue) || exit 1
[ -n "$catalogue" ] || die "the catalogue issue is missing; run backup.sh and cleanup.sh first"

pr=$(branch_pulls) || exit 1; pr=$(printf '%s' "$pr" | jq -c 'first // empty')
case "$(printf '%s' "$pr" | jq -r '.state // "none"' 2>/dev/null)" in
  open) die "the cleanup pull request $(printf '%s' "$pr" | jq -r .url) is not merged yet; merge it once check passes, then run finalize.sh again" ;;
  closed) die "the cleanup pull request $(printf '%s' "$pr" | jq -r .url) was closed without a merge; reopen and merge it, or run cleanup.sh prepare and open again" ;;
  merged) printf 'pr: %s merged\n' "$(printf '%s' "$pr" | jq -r .url)" ;;
  *) [ -z "$(git -C "$(cleanup_worktree)" status --porcelain 2>/dev/null)" ] \
       || die "the cleanup worktree $(cleanup_worktree) has changes but no pull request; run cleanup.sh open first"
     printf 'pr: none (the default branch needed no cleanup)\n' ;;
esac

# The workspace, only when approved. The snapshot goes to the catalogue issue before anything else can fail.
status=0
case " $(categories "$answers" approve) " in
  *" workspace "*)
    snap=$(mktemp "$dir/workspace-snapshot.XXXXXX")
    rc=0; out=$(bash "$here/workspace.sh" --apply --snapshot "$snap" 2>&1) || rc=$?
    printf '%s\n' "$out" | sed 's/^/workspace: /'
    if printf '%s\n' "$out" | grep -qxF "snapshot: $snap"; then
      { printf 'Snapshot of the GitHub workspace before `workspace.sh --apply` on %s, for undoing a change by hand.\n\nChanged:\n```\n%s\n```\n\n<details><summary>Previous state</summary>\n\n```json\n' \
          "$(date -u +%Y-%m-%d)" "$(printf '%s\n' "$out" | grep '^diff: ' || true)"
        jq . "$snap"; printf '```\n\n</details>\n'; } > "$tmp"
      jq -n --rawfile b "$tmp" '{body: $b}' | gh api --method POST "repos/$nwo/issues/$catalogue/comments" --input - >/dev/null 2>"$err" \
        || die "cannot post the snapshot to #$catalogue: $(tail -n1 "$err"); it is in $snap"
      printf 'snapshot: posted to #%s\n' "$catalogue"
    fi
    [ -s "$snap" ] || rm -f "$snap"
    [ "$rc" = 0 ] || { printf 'workspace: failed; fix the error above and run finalize.sh again\n'; status=1; } ;;
  *) case " $(categories "$answers" reject) " in
       *" workspace "*) printf 'workspace: rejected, left untouched\n' ;;
       *) printf 'workspace: no approved findings, left untouched\n' ;;
     esac ;;
esac

# The cleanup branch is done once its pull request is merged: the worktree, the local branch and the branch on
# origin go, so the next run starts from the default branch.
wt=$(cleanup_worktree)
if [ "$(printf '%s' "$pr" | jq -r '.state // empty')" = merged ]; then
  if [ -e "$wt" ]; then
    if git worktree remove "$wt" 2>"$err"; then
      git branch -q -D "$WF_BRANCH" 2>/dev/null || true
      printf 'worktree: %s removed\n' "$wt"
    else printf 'worktree: %s kept, it has changes the merged pull request does not (%s)\n' "$wt" "$(tail -n1 "$err")"; fi
  fi
  if [ -n "$(git ls-remote --heads origin "refs/heads/$WF_BRANCH" 2>/dev/null)" ]; then
    git push -q origin --delete "$WF_BRANCH" 2>"$err" && printf 'branch: %s deleted on origin\n' "$WF_BRANCH" \
      || printf 'branch: %s kept on origin, deleting it failed (%s)\n' "$WF_BRANCH" "$(tail -n1 "$err")"
  fi
fi

# The check, on what is on GitHub now, in a temporary worktree so the checkout stays as it is.
git fetch -q origin "refs/heads/$default" 2>"$err" || die "cannot fetch $default from origin: $(tail -n1 "$err")"
check=$(mktemp -d); rmdir "$check"
git worktree add -q --detach "$check" FETCH_HEAD 2>"$err" || die "cannot check out $default for the check: $(tail -n1 "$err")"
rc=0; out=$(bash "$here/check.sh" "$check" 2>&1) || rc=$?
git worktree remove --force "$check" >/dev/null 2>&1 || true
printf '%s\n' "$out" | grep -E '^(fail|warn|skip): ' | sed 's/^/check: /' || true
rejected=$(categories "$answers" reject)
[ -z "$rejected" ] || printf 'untouched: %s (rejected in the audit)\n' "$(printf '%s' "$rejected" | sed 's/ /, /g')"
if [ "$rc" = 0 ] && [ "$status" = 0 ]; then printf 'result: pass\n'; else printf 'result: fail\n'; exit 1; fi
