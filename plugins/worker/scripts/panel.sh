#!/usr/bin/env bash
# The reviewer panel's summary, handed from the review stage to the pull request stage (ADR 0018).
# Usage: panel.sh record   the summary block on stdin; replaces the previous record
#        panel.sh print    the brief the pull request stage reads
# The record lives in this worktree's git directory, so parallel workers never overwrite each other.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

form='panel: code=PASS security=PASS docs=PASS tests=FIX→PASS senior=PASS'

# wf_panel_verdict: reads a summary block on stdin and prints one line: "draft" when a reviewer's last
# verdict is not PASS, "ready" when every one of them passes, "none" without a panel: line, or
# "bad:<token>" for a verdict that begins with neither PASS nor FIX.
wf_panel_verdict() {
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
    verdict=$(printf '%s\n' "$block" | wf_panel_verdict)
    case "$verdict" in
      none) wf_die "the summary has no panel: line; expected one of the form: $form" ;;
      bad*) wf_die "the panel verdict '${verdict#bad:}' is neither PASS nor FIX; expected a line of the form: $form" ;;
    esac
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
    if ! git rev-parse -q --verify "$commit^{commit}" >/dev/null 2>&1; then
      wf_kv panel_head "the recorded commit is no longer in this branch's history, so the summary may describe other work"
    elif [ "$(git rev-parse HEAD)" = "$commit" ]; then
      wf_kv panel_head "unchanged since the summary was recorded"
    else
      wf_kv panel_head "$(git rev-list --count "$commit..HEAD") commits since the summary was recorded, which it does not describe"
    fi
    wf_kv panel_verdict "$(sed -n 's/^verdict: //p' "$record" | head -1)"
    printf 'panel_summary_block:\n'
    sed '1,/^$/d' "$record"
    ;;
  *) wf_die "usage: panel.sh record < block | panel.sh print" ;;
esac
