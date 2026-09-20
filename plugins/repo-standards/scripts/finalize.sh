#!/usr/bin/env bash
# The last step of the apply phase, after the cleanup pull request is merged: configure the GitHub workspace,
# keep its snapshot on the catalogue issue, remove the cleanup worktree, and end with the check.
# Usage: finalize.sh
# Refuses while the pull request from chore/standardize is open or was closed without a merge, because the
# rulesets require the job check that the pull request brings. workspace.sh --apply runs only when the
# workspace category was approved; it applies the whole difference, recomputed now, so one line names where
# that differs from the audit. Its snapshot is posted as a comment on the catalogue issue. The check
# (check.sh) runs on the head of the default branch on origin. Exit 1 when the check or the workspace fails.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=lib.sh
. "$here/lib.sh"
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
# workspace_deviation <settings applied now>: one line naming how the difference workspace.sh applied differs
# from the `configure` findings the report recorded, in both directions. workspace.sh recomputes the difference
# when it runs, after the cleanup pull request is merged, so it can be another one than the audit saw
# (ADR 0016). A setting the apply phase of this run has already worked on counts as changed, so a second run
# does not claim the report asked for something that was never needed. Nothing is skipped or aborted
# because of a deviation; the line is there to be read. Silent when the two sides name the same settings.
workspace_deviation() {
  local tab=$'\t'
  { printf '%s\n' "$1" | sed "s/^/now$tab/"
    [ ! -f "$handled" ] || sed "s/^/before$tab/" "$handled"
    awk -F'\t' -v OFS='\t' '$1 == "workspace" && $3 == "configure" { print "audited", $2 }' "$dir/findings"; } \
  | awk -F'\t' '
      $2 == "" { next }
      $1 == "now" { if (!($2 in N)) { N[$2] = 1; nl[++nn] = $2 }; A[$2] = 1; next }
      $1 == "before" { A[$2] = 1; next }
      { if (!($2 in R)) { R[$2] = 1; rl[++nr] = $2 } }
      END {
        for (i = 1; i <= nn; i++) if (!(nl[i] in R)) extra = extra (extra == "" ? "" : ", ") nl[i]
        for (i = 1; i <= nr; i++) if (!(rl[i] in A)) gone = gone (gone == "" ? "" : ", ") rl[i]
        if (extra == "" && gone == "") exit
        line = "workspace: the applied difference is not the audited one"
        if (extra != "") line = line "; changed without a finding: " extra
        if (gone != "") line = line "; in the report but already at the standard: " gone
        print line
      }'
}
# record_handled <settings>: add them to the settings the apply phase of this run has worked on, so a later
# run does not report one of them as a finding the report asked for in vain.
record_handled() {
  { [ ! -f "$handled" ] || cat "$handled"; printf '%s\n' "$1"; } | awk 'NF && !seen[$0]++' > "$handled.tmp" \
    && mv "$handled.tmp" "$handled"
}
for c in gh jq git; do command -v "$c" >/dev/null 2>&1 || die "$c is required but not on PATH"; done
answers=$(decisions) || exit 1
dir=$(state_dir)
# The settings the apply phase of this run has worked on: changed, or on the plan of a run that then failed.
# report.sh clears the file with the approvals, because a new report starts a new run.
handled="$dir/workspace-handled"
err=$(mktemp); tmp=$(mktemp); trap 'rm -f "$err" "$tmp"' EXIT
github_repo || exit 1
catalogue=$(catalogue_issue) || exit 1
[ -n "$catalogue" ] || die "the catalogue issue is missing; run backup.sh and cleanup.sh first"

pr=$(branch_pulls) || exit 1; pr=$(printf '%s' "$pr" | jq -c 'first // empty')
state=$(printf '%s' "$pr" | jq -r '.state // "none"') sha=$(printf '%s' "$pr" | jq -r '.sha // empty')
git fetch -q origin "refs/heads/$default" 2>"$err" || die "cannot fetch $default from origin: $(tail -n1 "$err")"
tip=$(git rev-parse FETCH_HEAD)
# Work in the cleanup worktree that no pull request carries yet: uncommitted changes, or a commit that is neither
# on the default branch nor in the merged pull request (a push or an open that failed). The pull request may have
# moved on after the last open (a review suggestion, "Update branch"), so its head is fetched from GitHub.
wt=$(cleanup_worktree) pending=""
carried() {
  [ "$1" = "$sha" ] || git merge-base --is-ancestor "$1" "$tip" && return 0
  [ "$state" = merged ] && git fetch -q origin "refs/pull/$(printf '%s' "$pr" | jq -r .number)/head" 2>/dev/null \
    && git merge-base --is-ancestor "$1" FETCH_HEAD
}
if [ "$(git -C "$wt" rev-parse --abbrev-ref HEAD 2>/dev/null)" = "$WF_BRANCH" ]; then
  if [ -n "$(git -C "$wt" status --porcelain)" ]; then pending="uncommitted changes"
  elif ! carried "$(git -C "$wt" rev-parse HEAD)"; then pending="a commit"; fi
fi
case "$state" in
  open) die "the cleanup pull request $(printf '%s' "$pr" | jq -r .url) is not merged yet; merge it once check passes, then run finalize.sh again" ;;
  closed) die "the cleanup pull request $(printf '%s' "$pr" | jq -r .url) was closed without a merge; reopen and merge it, or run cleanup.sh prepare and open again" ;;
esac
[ -z "$pending" ] || die "the cleanup worktree $wt has $pending that no pull request carries; run cleanup.sh open, merge the pull request, then run finalize.sh again"
if [ "$state" = merged ]; then printf 'pr: %s merged\n' "$(printf '%s' "$pr" | jq -r .url)"
else printf 'pr: none (the default branch needed no cleanup)\n'; fi

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
    # After the snapshot is on the catalogue issue, because this only reads: the settings workspace.sh worked
    # on, how they deviate from the audit, and the note for the next run. It never fails the run.
    settings=$(printf '%s\n' "$out" | workspace_settings) || settings=""
    [ "$rc" != 0 ] || workspace_deviation "$settings" || true
    record_handled "$settings" || true
    [ "$rc" = 0 ] || { printf 'workspace: failed; fix the error above and run finalize.sh again\n'; status=1; } ;;
  *) case " $(categories "$answers" reject) " in
       *" workspace "*) printf 'workspace: rejected, left untouched\n' ;;
       *) printf 'workspace: no approved findings, left untouched\n' ;;
     esac ;;
esac

# The cleanup branch is done once its pull request is merged: the worktree, the local branch and the branch on
# origin go, so the next run starts from the default branch. Only what the merged pull request carried goes.
if [ "$state" = merged ]; then
  if [ -e "$wt" ]; then
    if git worktree remove "$wt" 2>"$err"; then
      git branch -q -D "$WF_BRANCH" 2>/dev/null || true
      printf 'worktree: %s removed\n' "$wt"
    else printf 'worktree: %s kept, it has changes the merged pull request does not (%s)\n' "$wt" "$(tail -n1 "$err")"; fi
  fi
  pushed=$(remote_ref "refs/heads/$WF_BRANCH") || exit 1
  if [ -n "$pushed" ] && [ "$pushed" != "$sha" ]; then
    printf 'branch: %s kept on origin, it has commits the merged pull request does not\n' "$WF_BRANCH"
  elif [ -n "$pushed" ]; then
    git push -q origin --delete "$WF_BRANCH" 2>"$err" && printf 'branch: %s deleted on origin\n' "$WF_BRANCH" \
      || printf 'branch: %s kept on origin, deleting it failed (%s)\n' "$WF_BRANCH" "$(tail -n1 "$err")"
  fi
fi

# The check, on what is on GitHub now, in a temporary worktree so the checkout stays as it is.
check=$(mktemp -d); rmdir "$check"
git worktree add -q --detach "$check" "$tip" 2>"$err" || die "cannot check out $default for the check: $(tail -n1 "$err")"
rc=0; out=$(bash "$here/check.sh" "$check" 2>&1) || rc=$?
git worktree remove --force "$check" >/dev/null 2>&1 || true
printf '%s\n' "$out" | grep -E '^(fail|warn|skip): ' | sed 's/^/check: /' || true
rejected=$(categories "$answers" reject)
[ -z "$rejected" ] || printf 'untouched: %s (rejected in the audit)\n' "$(printf '%s' "$rejected" | sed 's/ /, /g')"
# A repository that started empty: the checkout still has no commit, and pulling is the maintainer's step.
git rev-parse -q --verify HEAD >/dev/null || printf 'next: the checkout has no commit yet; git pull origin %s brings the standard into it\n' "$default"
if [ "$rc" = 0 ] && [ "$status" = 0 ]; then printf 'result: pass\n'; else printf 'result: fail\n'; exit 1; fi
