#!/usr/bin/env bash
# The hunt record of a test hunt: its rounds, the tests it removed and the candidates it checked and kept.
# Usage: hunt.sh paths     the shares of the test files, one hunter each, at most 1500 lines a share
#        hunt.sh round     start the next round and print its shares with the candidates kept in them, or
#                          say that the hunt has ended
#        hunt.sh triage N  the reply of share N's hunter on stdin: records every new high and medium candidate,
#                          names each high one to remove and each medium one to check, drops the low ones and
#                          refuses every line that does not fit
#        hunt.sh removed   record the removal the last commit made, its block on stdin
#        hunt.sh print     the hunt block the reviewer and pull request briefs carry instead of an issue
# The records live in this worktree's git directory beside the panel and gate records (ADR 0018), and the
# numbers below are fixed, never knobs (ADR 0046).
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

max_rounds=3
max_candidates=3
share_lines=1500
max_reason=300
max_test=200
categories="source-inspection cannot-fail duplicate mocks-subject incidental"
state="$(wf_state_dir)/hunt"

candidate_form='candidate: <path> | <test> | <category> | <reason> | <confidence>'
removed_form='remove: <path> | <test> | <category> | <reason>
why: <in plain words, why the test proved nothing>
still_proven: <yes, by <which test> | no, <why no test needs to>>'

trim() { printf '%s' "$1" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//'; }

# A line that ends a line for a reader but not for `read`: a carriage return, a NEL or a line or paragraph
# separator. A record is printed back into the briefs, where a line at the left margin is a key, so a field
# may carry none of them.
has_break() { printf '%s' "$1" | LC_ALL=C grep -q -e $'\r' -e $'\xc2\x85' -e $'\xe2\x80\xa8' -e $'\xe2\x80\xa9'; }

# Parse the fields after a line's key, separated by |, into f_path f_test f_cat f_reason and f_conf. $2 is how
# many fields the line carries: five for a hunter's candidate line, four for a removal. Returns 1 with the
# reason in problem when the line does not fit.
parse_fields() {
  local rest="$1" want="$2" n
  problem=""
  if has_break "$rest"; then problem="it carries a carriage return or another line separator"; return 1; fi
  n=$(printf '%s\n' "$rest" | awk -F'|' '{ print NF }')
  if [ "$n" != "$want" ]; then
    problem="it has $n fields separated by |, not $want; the reason may not carry a |"; return 1
  fi
  IFS='|' read -r f_path f_test f_cat f_reason f_conf <<EOF
$rest
EOF
  f_path=$(trim "$f_path"); f_test=$(trim "$f_test"); f_cat=$(trim "$f_cat"); f_reason=$(trim "$f_reason"); f_conf=$(trim "${f_conf:-}")
  if [ -z "$f_path" ] || [ -z "$f_test" ] || [ -z "$f_reason" ]; then problem="a field is empty"; return 1; fi
  [ "${#f_reason}" -le "$max_reason" ] || { problem="the reason is longer than $max_reason characters"; return 1; }
  [ "${#f_test}" -le "$max_test" ] || { problem="the test name is longer than $max_test characters"; return 1; }
  wf_in_list "$f_cat" "$categories" || { problem="'$f_cat' is none of the categories $(printf '%s' "$categories" | sed 's/ /, /g')"; return 1; }
  # Only a test file is ever named for removal: a line that names any other file is refused however it was
  # reasoned, so no reply can steer the worker at the code the tests prove. A candidate names a file this
  # repository tracks, exactly as the rule lists it, so a path through .. or from the root never passes; a
  # removal names the file its commit touched, which the removed command checks against that commit.
  [ -n "$(printf '%s\n' "$f_path" | wf_test_paths)" ] || { problem="$f_path is no test file by the hunt's conventions ($wf_test_file_rule)"; return 1; }
  if [ "$want" = 5 ] && ! printf '%s\n' "$test_files" | grep -Fxq -- "$f_path"; then
    problem="$f_path is no test file this repository tracks; name it exactly as the brief lists it"; return 1
  fi
  if [ "$want" = 5 ]; then
    case "$f_conf" in high|medium|low) ;; *) problem="'$f_conf' is no confidence; it is high, medium or low"; return 1 ;; esac
  fi
  return 0
}

# The records of this hunt, counted up from 1 while their files are there.
count_files() {
  local n=0
  while [ -f "$state/$1.$((n + 1))" ]; do n=$((n + 1)); done
  printf '%s\n' "$n"
}
rounds() { count_files round; }
field() { wf_record_field "$1" "$2"; }

# Whether the file $1 at HEAD still names the test $2 as a word: a removed test is gone from its file.
in_head() { git cat-file -e "HEAD:$1" 2>/dev/null && git show "HEAD:$1" | grep -Fqw -- "$2"; }

# Records are named by their number, never by their path: the git directory may lie under a path with a
# space in it, and the lists below are split into words.
removal() { printf '%s/removal.%s\n' "$state" "$1"; }
kept_file() { printf '%s/kept.%s\n' "$state" "$1"; }

# The numbers of the removals that describe this branch: a record counts while its commit is in the history
# of HEAD and its test is not back in its file, so neither a removal a reset took away nor one a later commit
# restored is listed as one the pull request makes.
# The list is read once per call of this script, into removal_list below: nothing in one call changes it
# before the call ends.
removals() {
  local k=1 n
  n=$(count_files removal)
  while [ "$k" -le "$n" ]; do
    if git merge-base --is-ancestor "$(field "$(removal "$k")" commit)" HEAD 2>/dev/null &&
      ! in_head "$(field "$(removal "$k")" path)" "$(field "$(removal "$k")" test)"; then printf '%s\n' "$k"; fi
    k=$((k + 1))
  done
}
is_removed() { local k; for k in $removal_list; do [ "$(field "$(removal "$k")" path)" = "$1" ] && [ "$(field "$(removal "$k")" test)" = "$2" ] && return 0; done; return 1; }
is_kept() {
  local k=1 n; n=$(count_files kept)
  while [ "$k" -le "$n" ]; do
    [ "$(field "$(kept_file "$k")" path)" = "$1" ] && [ "$(field "$(kept_file "$k")" test)" = "$2" ] && return 0
    k=$((k + 1))
  done
  return 1
}

# Every new high and medium candidate is recorded as it is triaged; the numbers of those that still stand are
# the kept candidates: a test removed later is no longer kept.
kept_records() {
  local k=1 n
  n=$(count_files kept)
  while [ "$k" -le "$n" ]; do
    is_removed "$(field "$(kept_file "$k")" path)" "$(field "$(kept_file "$k")" test)" || printf '%s\n' "$k"
    k=$((k + 1))
  done
}
kept_line() { local f; f=$(kept_file "$1"); printf '%s | %s | %s | %s\n' "$(field "$f" path)" "$(field "$f" test)" "$(field "$f" category)" "$(field "$f" reason)"; }

# A round runs until a call of `round` after every share's reply is triaged closes it; that call decides
# whether the hunt goes on, and an end it decides is recorded, so every later call reads the same end.
# Nothing while the hunt goes on.
ended() { [ ! -f "$state/end" ] || field "$state/end" reason; }
# Why closing the running round ends the hunt, or nothing when another round runs.
end_reason() {
  local n; n=$(rounds)
  if [ "$n" -ge 1 ] && [ -z "$(found_in "$n")" ]; then printf 'round %s found no new candidate\n' "$n"
  elif [ "$n" -ge "$max_rounds" ]; then printf '%s rounds ran, the most a hunt runs\n' "$max_rounds"
  elif [ "$n" -ge 1 ] && [ -z "$(round_files $((n + 1)))" ]; then printf 'no file round %s found a candidate in is a test file any more\n' "$n"; fi
}

# The paths round $1 found a new candidate in, whether it was removed since or kept.
found_in() {
  local k=1 n; n=$(count_files kept)
  while [ "$k" -le "$n" ]; do
    [ "$(field "$(kept_file "$k")" round)" != "$1" ] || field "$(kept_file "$k")" path
    k=$((k + 1))
  done | sort -u
}

# The test files round $1 hunts: every one in the first round, and in a later one only the files the round
# before found a new candidate in. A file whose hunter found nothing new was read to its end, since a share
# is small enough to be read whole, so hunting it again spends a hunter on what was read already.
round_files() {
  local all found f
  all=$(wf_test_files)
  if [ "$1" = 1 ]; then printf '%s\n' "$all"; return 0; fi
  found=$(found_in $(($1 - 1)))
  [ -n "$found" ] || return 0
  printf '%s\n' "$all" | while IFS= read -r f; do
    if printf '%s\n' "$found" | grep -Fxq -- "$f"; then printf '%s\n' "$f"; fi
  done
}

# The shares of the test files $1, one per line: the files of each directory, packed in order into shares of
# at most $share_lines lines, and a file longer than that split into parts of that many lines, so a hunter
# can read its share to the end instead of sampling it. With $2 = kept, each share lists the candidates kept
# in its files, which its hunter is told not to propose again.
shares() {
  local files="$1" kept_list="" k f
  [ -n "$files" ] || wf_die "no test file in this repository matches the conventions of a test hunt: $wf_test_file_rule"
  [ "${2:-}" != kept ] || kept_list=$(kept_records)
  {
    for k in $kept_list; do printf 'K\t%s\t%s\n' "$(field "$(kept_file "$k")" path)" "$(kept_line "$k")"; done
    printf '%s\n' "$files" | while IFS= read -r f; do
      printf 'F\t%s\t%s\n' "$( (wc -l < "$f" || echo 0) 2>/dev/null | tr -d ' ')" "$f"
    done
  } | awk -F'\t' -v max="$share_lines" '
    function emit() {
      if (!cnt) return
      out = out sprintf("share %d: %s, files: %d, lines: %d\n%s%s", ++c, d, cnt, sum, body, kb)
      cnt = 0; sum = 0; body = ""; kb = ""
    }
    $1 == "K" { line = $0; sub(/^K\t[^\t]*\t/, "", line); kept[$2] = kept[$2] "  kept: " line "\n"; next }
    {
      p = $0; sub(/^F\t[^\t]*\t/, "", p); dir = p; if (!sub(/\/[^\/]*$/, "", dir)) dir = "."
      if (!(dir in nf)) dirs[++nd] = dir
      nf[dir]++; fp[dir, nf[dir]] = p; fl[dir, nf[dir]] = $2 + 0
    }
    END {
      for (i = 1; i <= nd; i++) {
        d = dirs[i]
        for (j = 1; j <= nf[d]; j++) {
          p = fp[d, j]; n = fl[d, j]
          if (n > max) {
            emit()
            parts = int((n + max - 1) / max)
            for (q = 1; q <= parts; q++) {
              a = (q - 1) * max + 1; b = q * max; if (b > n) b = n
              out = out sprintf("share %d: %s, part %d of %d of %s, lines %d-%d of %d\n  file: %s\n%s", ++c, d, q, parts, p, a, b, n, p, kept[p])
            }
            continue
          }
          if (cnt && sum + n > max) emit()
          cnt++; sum += n; body = body "  file: " p "\n"; kb = kb kept[p]
        }
        emit()
      }
      printf "hunt_shares: %d, at most %d lines each; one hunter each, at most five hunters per message\n%s", c, max, out
    }'
}

# A round's shares are recorded as it starts, so the round a context left before every hunter reported is
# resumed with the same shares, numbered as they were, whatever the removals since changed in the files.
shares_file() { printf '%s/shares.%s\n' "$state" "$1"; }
share_count() { [ -f "$(shares_file "$1")" ] && grep -c '^share ' "$(shares_file "$1")" || printf '0\n'; }
share_block() { awk -v s="share $2:" 'index($0, s) == 1 { on = 1; print; next } /^[^ ]/ { on = 0 } on' "$(shares_file "$1")"; }
triaged_file() { printf '%s/triaged.%s.%s\n' "$state" "$1" "$2"; }
# The shares of round $1 whose hunter's reply has not been triaged.
pending_shares() {
  local s=1 c; c=$(share_count "$1")
  while [ "$s" -le "$c" ]; do [ -f "$(triaged_file "$1" "$s")" ] || printf '%s\n' "$s"; s=$((s + 1)); done
}

clean_tree() {
  local dirty; dirty=$(wf_dirty_tree)
  [ -z "$dirty" ] || wf_die "the working tree has uncommitted changes:
$dirty
$1"
}

removal_list=$(removals)
test_files=""

case "${1:-}" in
  paths) shares "$(wf_test_files)" ;;
  round)
    clean_tree "Commit each removal on its own, record it with hunt.sh removed, then start the next round."
    why=$(ended)
    # A round closes once every share's reply is triaged; until then this call prints the shares still out.
    n=$(rounds)
    if [ -z "$why" ] && [ "$n" -ge 1 ] && [ -n "$(pending_shares "$n")" ]; then
      pending=$(pending_shares "$n")
      wf_kv hunt_round "$n of at most $max_rounds, resumed: $(printf '%s' "$pending" | grep -c .) of $(share_count "$n") share(s) not triaged yet; hunt these, then call round again"
      for s in $pending; do share_block "$n" "$s"; done
      exit 0
    fi
    if [ -z "$why" ]; then
      why=$(end_reason)
      if [ -n "$why" ]; then
        mkdir -p "$state"
        printf 'reason: %s\nat: %s\n\n' "$why" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$state/end.tmp"
        mv "$state/end.tmp" "$state/end"
      fi
    fi
    if [ -n "$why" ]; then
      n=$(rounds); removed=$(printf '%s' "$removal_list" | grep -c . || true)
      wf_kv hunt_round "none; the hunt has ended: $why"
      if [ "$removed" -gt 0 ]; then
        wf_kv next "$removed test(s) removed in $n round(s): run the worker's gate.sh run, then review, pull request and CI as the hunt skill says"
      else
        wf_kv next "nothing removed in $n round(s): open no pull request and run no review; report 'hunt: nothing removed' with the kept candidates of hunt.sh print and /orchestrator:abandon $(wf_branch) for the maintainer"
      fi
      exit 0
    fi
    next=$(( $(rounds) + 1 ))
    mkdir -p "$state"
    # The shares are written before the round, so a round is never recorded without them.
    shares "$(round_files "$next")" kept > "$(shares_file "$next").tmp"
    mv "$(shares_file "$next").tmp" "$(shares_file "$next")"
    printf 'round: %s\ncommit: %s\nat: %s\n\n' "$next" "$(git rev-parse HEAD)" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$state/round.$next.tmp"
    mv "$state/round.$next.tmp" "$state/round.$next"
    wf_kv hunt_round "$next of at most $max_rounds"
    cat "$(shares_file "$next")"
    ;;
  triage)
    n=$(rounds)
    [ "$n" -ge 1 ] || wf_die "no round of this hunt has started; start one with the worker's hunt.sh round"
    [ -z "$(ended)" ] || wf_die "the hunt has ended ($(ended)); triage no more replies"
    share="${2:-}"
    case "$share" in ''|*[!0-9]*) wf_die "name the share whose reply this is: hunt.sh triage <share number> < reply" ;; esac
    [ "$share" -ge 1 ] && [ "$share" -le "$(share_count "$n")" ] || wf_die "round $n has no share $share; its shares are 1 to $(share_count "$n")"
    [ ! -f "$(triaged_file "$n" "$share")" ] || wf_die "the reply of share $share is triaged already in round $n; each hunter's reply is triaged once"
    remove=0 check=0 kept=0 dropped=0 refused=0 seen=0
    test_files=$(wf_test_files)  # the list a candidate's path is checked against in parse_fields
    while IFS= read -r line || [ -n "$line" ]; do
      line=$(trim "$line")
      [ -n "$line" ] || continue
      [ "$line" != "no candidates" ] || continue
      case "$line" in
        candidate:*) ;;
        *) refused=$((refused + 1)); wf_kv refused "'$line': not a candidate line; a hunter replies with lines of the form $candidate_form, or no candidates"; continue ;;
      esac
      seen=$((seen + 1))
      if [ "$seen" -gt "$max_candidates" ]; then
        refused=$((refused + 1)); wf_kv refused "'$line': a hunter names at most $max_candidates candidates a round"; continue
      fi
      if ! parse_fields "${line#candidate:}" 5; then
        refused=$((refused + 1)); wf_kv refused "'$line': $problem; the form is $candidate_form"; continue
      fi
      summary="$f_path | $f_test | $f_cat | $f_reason"
      # A high or a medium candidate is new unless it was removed or checked before: a new one is recorded, so
      # no later round proposes it again, and handed to the worker, who removes a high one unless it proves
      # something and a medium one only when it is sure it proves nothing (ADR 0047).
      if [ "$f_conf" = low ]; then dropped=$((dropped + 1)); wf_kv dropped "$summary"
      elif is_removed "$f_path" "$f_test"; then dropped=$((dropped + 1)); wf_kv dropped "$summary (removed already)"
      elif is_kept "$f_path" "$f_test"; then kept=$((kept + 1)); wf_kv kept "$summary (checked already)"
      else
        f=$(kept_file $(( $(count_files kept) + 1 )))
        mkdir -p "$state"
        printf 'round: %s\npath: %s\ntest: %s\ncategory: %s\nreason: %s\nconfidence: %s\n\n' "$n" "$f_path" "$f_test" "$f_cat" "$f_reason" "$f_conf" > "$f.tmp"
        mv "$f.tmp" "$f"
        if [ "$f_conf" = high ]; then remove=$((remove + 1)); wf_kv remove "$summary"
        else check=$((check + 1)); wf_kv check "$summary"; fi
      fi
    done
    printf 'share: %s\nremove: %s\ncheck: %s\nkept: %s\ndropped: %s\nrefused: %s\n\n' "$share" "$remove" "$check" "$kept" "$dropped" "$refused" > "$(triaged_file "$n" "$share").tmp"
    mv "$(triaged_file "$n" "$share").tmp" "$(triaged_file "$n" "$share")"
    wf_kv hunt_triage "$remove to remove, $check to check, $kept kept, $dropped dropped, $refused refused"
    [ "$refused" = 0 ] || printf 'help: a refused line is dropped; ask its hunter nothing\n'
    ;;
  removed)
    block=$(cat)
    n=$(rounds)
    [ "$n" -ge 1 ] || wf_die "no round of this hunt has started; start one with the worker's hunt.sh round"
    [ -z "$(ended)" ] || wf_die "the hunt has ended ($(ended)); a removal is made in a round, so record none after it"
    clean_tree "A removal is recorded at the commit that makes it: commit it on its own, then record it again."
    head=$(git rev-parse HEAD)
    [ "$head" != "$(field "$state/round.$n" commit)" ] || wf_die "the last commit is the one round $n started at, so it removes nothing; commit the removal on its own, then record it"
    for k in $removal_list; do
      [ "$(field "$(removal "$k")" commit)" != "$head" ] || wf_die "the last commit is recorded already as the removal of $(field "$(removal "$k")" test); one commit removes one test, so commit the next removal on its own first"
    done
    line_of() { printf '%s\n' "$block" | sed -n -E "s/^[[:space:]]*$1:[[:space:]]*//p"; }
    for key in remove why still_proven; do
      [ "$(line_of "$key" | grep -c . || true)" = 1 ] || wf_die "the removal block states no single $key: line; the form is:
$removed_form"
    done
    parse_fields "$(line_of remove)" 4 || wf_die "the remove: line does not fit: $problem; the form is:
$removed_form"
    why=$(trim "$(line_of why)"); still=$(trim "$(line_of still_proven)")
    if has_break "$why" || has_break "$still"; then wf_die "a line of the removal block carries a carriage return or another line separator; write each line as one line of plain text"; fi
    if [ "${#why}" -gt "$max_reason" ] || [ "${#still}" -gt "$max_reason" ]; then wf_die "the why: and still_proven: lines hold at most $max_reason characters each; say it in one plain sentence"; fi
    case "$still" in yes*|no*) ;; *) wf_die "still_proven: starts with yes or no: 'yes, by <which test>' or 'no, <why no test needs to>'" ;; esac
    ! is_removed "$f_path" "$f_test" || wf_die "$f_test in $f_path is recorded as removed already"
    git diff-tree --no-commit-id --name-only -r "$head" | grep -Fxq -- "$f_path" ||
      wf_die "the last commit does not touch $f_path, so it is not the removal of $f_test; commit the removal on its own, then record it"
    ! in_head "$f_path" "$f_test" || wf_die "$f_path still names $f_test at the last commit, so the commit did not remove it; remove the test, commit, then record it"
    f=$(removal $(( $(count_files removal) + 1 )))
    mkdir -p "$state"
    printf 'round: %s\ncommit: %s\npath: %s\ntest: %s\ncategory: %s\nreason: %s\nwhy: %s\nstill_proven: %s\n\n' \
      "$n" "$head" "$f_path" "$f_test" "$f_cat" "$f_reason" "$why" "$still" > "$f.tmp"
    mv "$f.tmp" "$f"
    wf_kv hunt_removed "$f_test in $f_path at $(git rev-parse --short "$head"), round $n"
    ;;
  print)
    n=$(rounds)
    list=$removal_list; removed=$(printf '%s' "$list" | grep -c . || true)
    kept_list=$(kept_records); kept=$(printf '%s' "$kept_list" | grep -c . || true)
    wf_kv hunt "a test hunt on $(wf_branch): it removes tests that prove nothing and closes no issue"
    why=$(ended)
    if [ "$n" = 0 ]; then wf_kv hunt_rounds "none has started"
    elif [ -n "$why" ]; then wf_kv hunt_rounds "$n of at most $max_rounds; the hunt has ended: $why"
    else wf_kv hunt_rounds "$n of at most $max_rounds; round $n is running"; fi
    wf_kv hunt_removed "$removed"
    wf_kv hunt_kept "$kept"
    stale=$(( $(count_files removal) - removed ))
    [ "$stale" = 0 ] || wf_kv hunt_note "$stale recorded removal(s) no longer stand, because their commit left this branch's history or their test is back in its file, so they are not listed"
    # The fields are text a hunter and the worker wrote, so every line of the block is indented and none
    # can be read as a key of the brief.
    printf 'hunt_block:\n'
    i=0
    for k in $list; do
      i=$((i + 1)); f=$(removal "$k")
      printf '  removed %s at %s, round %s: %s | %s | %s\n' "$i" "$(wf_short "$(field "$f" commit)")" "$(field "$f" round)" \
        "$(field "$f" path)" "$(field "$f" test)" "$(field "$f" category)"
      printf '    why: %s\n' "$(field "$f" why)"
      printf '    still proven: %s\n' "$(field "$f" still_proven)"
    done
    for k in $kept_list; do printf '  kept, round %s: %s\n' "$(field "$(kept_file "$k")" round)" "$(kept_line "$k")"; done
    [ "$removed$kept" != 00 ] || printf '  nothing removed and nothing kept\n'
    ;;
  *) wf_die "usage: hunt.sh paths | hunt.sh round | hunt.sh triage <share> < reply | hunt.sh removed < block | hunt.sh print" ;;
esac
