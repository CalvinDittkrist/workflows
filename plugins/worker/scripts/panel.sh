#!/usr/bin/env bash
# The reviewer panel's summary, handed from the review stage to the pull request stage (ADR 0018).
# Usage: panel.sh record   the summary block on stdin; replaces the previous record
#        panel.sh print    the brief the pull request stage reads
# The record lives in this worktree's git directory, so parallel workers never overwrite each other.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

form='panel: code=PASS security=PASS docs=PASS tests=FIX→PASS senior=PASS'

# panel_verdict: reads a summary block on stdin and prints one line: "draft" when a reviewer's last
# verdict is not PASS, "ready" when every one of them passes, "none" without a panel: line, or
# "bad:<token>" for a verdict that begins with neither PASS nor FIX.
panel_verdict() {
  awk '
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
        pairs++
        eq = index(tok[i], "=")
        if (eq == 0) { print "bad:" tok[i]; exit }
        # A reviewer re-reviewed in a later round reads FIX→FIX→PASS; only the last verdict counts.
        v = substr(tok[i], eq + 1)
        gsub(/->/, "\342\206\222", v)
        m = split(v, round, "\342\206\222")
        last = round[m]
        sub(/[[:space:]]*\(.*$/, "", last)
        if (last ~ /^PASS/) continue
        if (last ~ /^FIX/) { draft = 1; continue }
        print "bad:" tok[i]; exit
      }
      if (!pairs) { print "bad:"; exit }
      print (draft ? "draft" : "ready")
      exit
    }
    END { if (!done_) print "none" }
  '
}

record="$(wf_state_dir)/panel"

case "${1:-}" in
  record)
    block=$(cat)
    verdict=$(printf '%s\n' "$block" | panel_verdict)
    case "$verdict" in
      none) wf_die "the summary has no panel: line; expected one of the form: $form" ;;
      bad*) wf_die "the panel verdict '${verdict#bad:}' is neither PASS nor FIX; expected a line of the form: $form" ;;
    esac
    # The verdict is only as complete as the line: a reviewer the block leaves out is one nobody hears
    # about, so name it. A warning, not a refusal, because the review stage may run a shorter panel.
    panel_line=$(printf '%s\n' "$block" | sed -n '/^[[:space:]]*panel:/p' | head -1 | tr '\t' ' ')
    set -f  # a reviewer name is data, so it may not glob the working directory
    for reviewer in $(wf_reviewers | tr ',' ' '); do
      case " $panel_line" in *"panel: $reviewer="*|*" $reviewer="*) ;; *) wf_warn "the panel line does not name $reviewer, so the record says nothing about that reviewer" ;; esac
    done
    set +f
    # The brief prints these keys itself; a block that carries one would say something else about the
    # panel further down the same brief, so the record refuses it instead of quoting it.
    spoof=$(printf '%s\n' "$block" | sed -n -E '/^(commit|verdict|panel_summary|panel_verdict|panel_head|panel_summary_block):/p' | head -1)
    [ -z "$spoof" ] || wf_die "the summary carries a line the brief uses for itself ('$spoof'); indent or reword that line and record again"
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
    verdict=$(sed -n 's/^verdict: //p' "$record" | head -1)
    case "$verdict" in draft|ready) ;; *) verdict=draft ;; esac  # an unreadable record is an unknown panel
    wf_kv panel_verdict "$verdict"
    printf 'panel_summary_block:\n'
    sed '1,/^$/d' "$record"
    ;;
  *) wf_die "usage: panel.sh record < block | panel.sh print" ;;
esac
