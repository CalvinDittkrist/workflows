#!/usr/bin/env bash
# The reviewer panel's summary, handed from the review stage to the pull request stage (ADR 0018).
# Usage: panel.sh record   the summary block on stdin; replaces the previous record
#        panel.sh print    the brief the pull request stage reads
#        panel.sh verdict  draft or ready, the one word the yolo finish stage gates on
# The record lives in this worktree's git directory, so parallel workers never overwrite each other.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

form='panel: code=PASS security=PASS docs=PASS tests=FIX→PASS senior=PASS'

# panel_verdict: reads a summary block on stdin and prints one line: the verdict, then the reviewers the
# panel: line names. The verdict is "draft" when a reviewer's last verdict is not PASS and "ready" when
# every one of them passes; "none" without a panel: line, "empty" for a panel: line that names nobody,
# "twice" for a block with two of them, and "bad:<token>" for a verdict starting with neither PASS nor FIX.
panel_verdict() {
  awk '
    # The whole block is quoted into the pull request, so a second panel: line would show the reader a
    # verdict this one never derived. One line, or none.
    done_ && /^[[:space:]]*panel:/ { result = "twice"; exit }
    done_ { next }
    /^[[:space:]]*panel:/ {
      done_ = 1
      line = $0
      sub(/^[[:space:]]*panel:[[:space:]]*/, "", line)
      # One reviewer per token, but a verdict may carry a parenthesised suffix with spaces in it
      # ("PASS(S3 only)"), so the tokens are cut before each `name=`, not at every space.
      gsub(/[[:space:]]+[A-Za-z][A-Za-z0-9_.-]*=/, "\001&", line)
      n = split(line, tok, "\001")
      for (i = 1; i <= n; i++) {
        sub(/^[[:space:]]+/, "", tok[i]); sub(/[[:space:]]+$/, "", tok[i])
        if (tok[i] == "") continue
        eq = index(tok[i], "=")
        if (eq == 0) { result = "bad:" tok[i]; exit }
        names = names " " substr(tok[i], 1, eq - 1)
        # A reviewer re-reviewed in a later round reads FIX→FIX→PASS; only the last verdict counts.
        v = substr(tok[i], eq + 1)
        gsub(/->/, "\342\206\222", v)
        m = split(v, rounds, "\342\206\222")
        last = rounds[m]
        sub(/[[:space:]]*\(.*$/, "", last)
        if (last ~ /^PASS/) continue
        if (last ~ /^FIX/) { draft = 1; continue }
        result = "bad:" tok[i]; exit
      }
      if (names == "") { result = "empty"; exit }
      result = (draft ? "draft" : "ready") names
      next
    }
    END { print (result != "" ? result : "none") }
  '
}

record="$(wf_state_dir)/panel"

# recorded_verdict: "draft" or "ready" for the record, and "draft" for one that is missing or unreadable,
# so an unknown panel never reads as a passed one.
recorded_verdict() {
  local v=""
  [ ! -f "$record" ] || v=$(sed -n 's/^verdict: //p' "$record" | head -1)
  case "$v" in ready) printf 'ready\n' ;; *) printf 'draft\n' ;; esac
}

case "${1:-}" in
  record)
    block=$(cat)
    parsed=$(printf '%s\n' "$block" | panel_verdict)
    verdict=${parsed%% *}
    case "$verdict" in
      none) wf_die "the summary has no panel: line; expected one of the form: $form" ;;
      empty) wf_die "the panel: line names no reviewer; expected one of the form: $form" ;;
      twice) wf_die "the summary has more than one panel: line, so it states two verdicts; keep the one the panel ended on" ;;
      bad*) wf_die "the panel verdict '${verdict#bad:}' is neither PASS nor FIX; expected a line of the form: $form" ;;
    esac
    # The verdict is only as complete as the line: a reviewer the block leaves out is one nobody hears
    # about, so name it. A warning, not a refusal, because the review stage may run a shorter panel.
    named=" ${parsed#"$verdict"} "
    set -f  # a reviewer name is data, so it may not glob the working directory
    for reviewer in $(wf_reviewers | tr ',' ' '); do
      case "$named" in *" $reviewer "*) ;; *) wf_warn "the panel line does not name $reviewer, so the record says nothing about that reviewer" ;; esac
    done
    set +f
    # The brief prints these keys itself; a block that carries one would say something else about the
    # panel further down the same brief, so the record refuses it instead of quoting it.
    spoof=$(printf '%s\n' "$block" | sed -n -E '/^[[:space:]]*(commit|verdict|panel_summary|panel_verdict|panel_head|panel_summary_block):/p' | head -1)
    [ -z "$spoof" ] || wf_die "the summary carries a line the brief uses for itself ('$spoof'); reword that line and record again"
    commit=$(git rev-parse HEAD 2>/dev/null) || wf_die "this branch has no commit to record the summary at"
    mkdir -p "$(dirname "$record")"
    # One file, written in one move: the headers, an empty line, then the block exactly as given.
    { printf 'commit: %s\nverdict: %s\n\n' "$commit" "$verdict"; printf '%s\n' "$block"; } > "$record.tmp"
    mv "$record.tmp" "$record"
    wf_kv panel_summary "recorded at $(git rev-parse --short "$commit")"
    wf_kv panel_verdict "$verdict"
    ;;
  print)
    if [ ! -f "$record" ]; then
      wf_kv panel_summary "none recorded; the review stage did not hand one over, so the panel result is unknown"
      wf_kv panel_verdict "draft"
      exit 0
    fi
    commit=$(sed -n 's/^commit: //p' "$record" | head -1)
    wf_kv panel_summary "recorded at $(git rev-parse --short "$commit" 2>/dev/null || printf '%s' "$commit")"
    if [ "$(git rev-parse HEAD)" = "$commit" ]; then
      wf_kv panel_head "unchanged since the summary was recorded"
    elif ! git merge-base --is-ancestor "$commit" HEAD 2>/dev/null; then
      wf_kv panel_head "the recorded commit is no longer in this branch's history (amended or rebased), so the summary may describe other work"
    else
      wf_kv panel_head "commits since the summary was recorded, which it does not describe: $(git rev-list --count "$commit..HEAD")"
    fi
    wf_kv panel_verdict "$(recorded_verdict)"
    printf 'panel_summary_block:\n'
    sed '1,/^$/d' "$record"
    ;;
  # The one word the yolo finish stage gates on, so it reads a contract rather than scraping the brief.
  verdict) recorded_verdict ;;
  *) wf_die "usage: panel.sh record < block | panel.sh print | panel.sh verdict" ;;
esac
