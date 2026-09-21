#!/usr/bin/env bash
# The reviewer panel's state: one record per review round, and the summary derived from them and handed from
# the review stage to the pull request stage (ADR 0018).
# Usage: panel.sh rounds   the review stage's brief: the round to run next and the reviewers still on FIX
#        panel.sh round    record one round, its block on stdin, and print the round's context checkpoint
#        panel.sh record   the summary, derived from the round records; only the disputed: lines on stdin
#        panel.sh print    the brief the pull request stage reads
#        panel.sh verdict  draft or ready, the one word the yolo finish stage gates on
# The records live in this worktree's git directory, so parallel workers never overwrite each other.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

here=$(cd "$(dirname "$0")" && pwd)
state=$(wf_state_dir)
record="$state/panel"

panel_form='panel: code=FIX security=PASS docs=PASS tests=FIX senior=PASS'
fixed_form='fixed: 3 (S1 1, S2 2, S3 0)'
round_form="$panel_form
$fixed_form
disputed: none"

# panel_pairs: the reviewers of a block's one panel: line as "<name> <verdict>" lines, in the order the line
# names them, or a single line "!<problem>" for a block that states no panel, names no reviewer, names one
# twice, carries two panel: lines or a verdict that is neither PASS nor FIX. One parser: a round is written
# through it and read back through it, so the line this script writes is the line it can read.
panel_pairs() {
  awk '
    function fail(p) { if (problem == "") problem = p }
    /^[[:space:]]*panel:/ {
      if (seen) { fail("twice"); next }
      seen = 1
      line = $0
      sub(/^[[:space:]]*panel:[[:space:]]*/, "", line)
      # One reviewer per token, but a verdict may carry a parenthesised suffix with spaces in it
      # ("PASS (S3 only)"), so the tokens are cut before each `name=`, not at every space.
      gsub(/[[:space:]]+[A-Za-z][A-Za-z0-9_.-]*=/, "\001&", line)
      n = split(line, tok, "\001")
      for (i = 1; i <= n; i++) {
        sub(/^[[:space:]]+/, "", tok[i]); sub(/[[:space:]]+$/, "", tok[i])
        if (tok[i] == "") continue
        eq = index(tok[i], "=")
        # No "=" is no verdict, and an "=" in first place is a verdict with no reviewer on it: the writer
        # below would read the verdict as the name, and what it wrote back would parse as nothing at all.
        if (eq <= 1) { fail("bad:" tok[i]); break }
        v = substr(tok[i], eq + 1)
        # A round states one verdict per reviewer; the chain across the rounds is derived from the records,
        # never given, so a token that already carries one is a round that would be counted twice.
        if (v !~ /^(PASS|FIX)/ || index(v, "\342\206\222") || index(v, "->")) { fail("bad:" tok[i]); break }
        name = substr(tok[i], 1, eq - 1)
        # One round is one verdict per reviewer. A second one would be appended to the chain of that
        # reviewer in the summary, so the chain would state more rounds than review_rounds: counts.
        if (name in named) { fail("dup:" name); break }
        named[name] = 1
        out = out name " " v "\n"
        count++
      }
      if (problem == "" && count == 0) fail("empty")
      next
    }
    END {
      if (problem != "") print "!" problem
      else if (!seen) print "!none"
      else printf "%s", out
    }
  '
}

round_file() { printf '%s/round.%s\n' "$state" "$1"; }
# A counter from a record's headers, and 0 for a record that carries none, so arithmetic over a file that
# was edited by hand fails as a wrong sum rather than as a syntax error. It says so, because a summary that
# understates what a round fixed is worth a line and an unreadable panel: line of the same record is refused.
field_num() {
  local v; v=$(wf_record_field "$1" "$2")
  case "$v" in
    ''|*[!0-9]*) wf_warn "the round record $1 states no $2 this script can read ('$v'), so the summary counts 0 for it"; printf '0\n' ;;
    *) printf '%s\n' "$((10#$v))" ;;  # base ten, so a zero-padded count is no octal number to the sum below
  esac
}
# The disputed: lines of a block on stdin, normalised to one key each. Both records take them from their
# caller and refuse the same two shapes, so they are read in one place and the two cannot drift apart.
# $1 names what refused them, so each error still says which call the caller has to make again.
disputed_lines() {
  local block=$1 what=$2 disputed blank
  disputed=$(printf '%s\n' "$block" | sed -n -E 's/^[[:space:]]*disputed:[[:space:]]*/disputed: /p')
  [ -n "$disputed" ] || wf_die "the $what block has no disputed: line; state 'disputed: none' when nothing stands, one line per finding otherwise"
  blank=$(printf '%s\n' "$disputed" | grep -vE '^disputed: [^[:space:]]' | head -1 || true)
  [ -z "$blank" ] || wf_die "a disputed: line of the $what block carries no text; write 'disputed: none' when nothing stands, one line per finding otherwise"
  printf '%s\n' "$disputed"
}

# A block is refused when it carries a character that ends a line for a reader but not for the checks above:
# a carriage return, a NEL (U+0085) or a line/paragraph separator (U+2028, U+2029). The text of a block is
# stored verbatim in the record and printed back into the brief of the next stage, where a line at the left
# margin is a key — `panel_verdict:` among them, which the pull request stage takes its draft decision from.
# The stray check reads such a line as one line, and `sed 's/^/    /'` indents only as far as the break, so
# the tail of it would land at column 0. session-start.sh normalises the same characters out of the handoff
# note; here they are refused instead, because the worker writes these lines itself and can write one line.
refuse_line_breaks() {
  local block=$1 what=$2 call=$3
  printf '%s\n' "$block" | LC_ALL=C grep -q -e $'\r' -e $'\xc2\x85' -e $'\xe2\x80\xa8' -e $'\xe2\x80\xa9' || return 0
  wf_die "a line of the $what block carries a carriage return or another line separator, which the record cannot hold: it would read as a line of its own in the brief of the next stage, where a line is a key. Write each line as one line of plain text and record the $what again with $call"
}

# Both records describe one commit: the one the fixes are in and the gate has passed on. One written over a
# dirty tree or an ungated commit would state a verdict for work no reviewer read — and for the summary that
# is the word `panel.sh verdict` answers, which the yolo finish stage merges on. $1 names the record and $2
# the call that writes it, so each refusal still names the call its caller has to make again.
gated_head() {
  local what=$1 call=$2 dirty gate
  dirty=$(wf_dirty_tree)
  [ -z "$dirty" ] || wf_die "the working tree has uncommitted changes, so this $what would be recorded at a commit that does not carry them:
$dirty
Commit what belongs to the change, run the worker's gate.sh run on that commit, then record the $what again with $call."
  gate=$("$here/gate.sh" verdict)
  [ "$gate" = pass ] || wf_die "the gate answers '$gate' for this head, not 'pass'; a $what is recorded for a commit the gate has passed on, so run the worker's gate.sh run, fix what it reports, commit, and record the $what again with $call"
}

# The rounds recorded for this review, counted up from 1 while their files are there.
recorded_rounds() {
  local n=0
  while [ -f "$(round_file $((n + 1)))" ]; do n=$((n + 1)); done
  printf '%s\n' "$n"
}

# The rounds that describe this branch. A record counts while the commit of the last one is still in the
# history of HEAD, so a worker that stopped in the middle of a round after a few fix commits continues at
# that round rather than at the panel. Once that commit is gone (amended, rebased) the records describe
# other work: they read as none and the panel starts at round 1 again.
counted_rounds() {
  local n commit
  n=$(recorded_rounds)
  if [ "$n" -gt 0 ]; then
    commit=$(wf_record_field "$(round_file "$n")" commit)
    if [ -z "$commit" ] || ! git merge-base --is-ancestor "$commit" HEAD 2>/dev/null; then n=0; fi
  fi
  printf '%s\n' "$n"
}

# Every verdict of the first $1 rounds as "<name> <verdict>" lines, oldest round first. A record this script
# wrote parses; one that was edited by hand may not, and its "!" line is refused by the caller rather than
# read as a reviewer named "!bad".
all_pairs() {
  local r=1
  while [ "$r" -le "$1" ]; do
    wf_record_body "$(round_file "$r")" | panel_pairs
    r=$((r + 1))
  done
}

# The verdicts of the first $1 rounds, and a refusal for a record this script cannot read back. Every caller
# that reads the records stops here: a brief that took a "!bad" line for a reviewer name would send the next
# round to a reviewer nobody named, and a summary would state a panel nobody ran.
readable_pairs() {
  local pairs bad
  pairs=$(all_pairs "$1")
  bad=$(printf '%s\n' "$pairs" | grep '^!' | head -1 || true)
  [ -z "$bad" ] || wf_die "a round record of this review states no panel this script can read ($bad); the records are in $state, so remove them and run the panel again from round 1"
  printf '%s\n' "$pairs"
}

# The last verdict of every reviewer of the first $1 rounds, in the order the rounds first named them.
last_verdicts() {
  all_pairs "$1" | awk '
    { name = $1; $1 = ""; sub(/^ /, "")
      if (!(name in last)) order[++k] = name
      last[name] = $0 }
    END { for (i = 1; i <= k; i++) print order[i] " " last[order[i]] }'
}

# The reviewers of the first $1 rounds whose last verdict is FIX, comma separated: the ones the next round
# runs. A PASS is not re-litigated: the round that follows exists for the findings still open. That a reviewer
# which passed never reads the fixes made after it did is the trade ADR 0004 makes for the contexts it saves.
reviewers_on_fix() {
  last_verdicts "$1" | awk '$2 ~ /^FIX/ { printf "%s%s", (n++ ? "," : ""), $1 } END { if (n) print "" }'
}

# The two keys that say what the review stage does next, from the rounds that count. They stand in the brief
# of the stage and in the answer of every round record, so a worker reads the same two lines wherever it
# entered the review.
next_keys() {
  local n next limit reviewers
  n=$1
  next=$((n + 1))
  limit=$(wf_review_rounds)
  if [ "$next" -gt "$limit" ]; then
    wf_kv review_round "none; $n round(s) are recorded and max_rounds is $limit, so run no further round"
    wf_kv review_reviewers "none; the round limit ends the panel, so record the summary with panel.sh record"
    return
  fi
  wf_kv review_round "$next of at most $limit"
  if [ "$n" = 0 ]; then
    wf_kv review_reviewers "$(wf_reviewers) (no round of this review is recorded, so the panel runs the whole list)"
    return
  fi
  reviewers=$(reviewers_on_fix "$n")
  if [ -z "$reviewers" ]; then
    wf_kv review_reviewers "none; every reviewer's last verdict is PASS, so run no further round and record the summary with panel.sh record"
  else
    wf_kv review_reviewers "$reviewers"
  fi
}

# recorded_verdict: "draft" or "ready" for the record, and "draft" for one that is missing or unreadable,
# so an unknown panel never reads as a passed one. A summary describes the commit it was recorded at and
# nothing else: once a commit has landed since, no reviewer has read what would be merged, so the one word
# the finish stage gates on is "draft" however the panel itself ended. It matters because the review stage is
# skippable now — a `/worker:work` resuming at the ci stage (ADR 0029) reaches the merge without it — and
# because `verdict` is read by scripts, which see no `panel_head:` line to tell them the distance.
recorded_verdict() {
  local v=""
  [ ! -f "$record" ] || v=$(wf_record_field "$record" verdict)
  [ "$v" != ready ] || [ "$(git rev-parse HEAD 2>/dev/null)" = "$(wf_record_field "$record" commit)" ] || v=stale
  case "$v" in ready) printf 'ready\n' ;; *) printf 'draft\n' ;; esac
}

case "${1:-}" in
  rounds)
    n=$(counted_rounds)
    recorded=$(recorded_rounds)
    if [ "$n" = 0 ] && [ "$recorded" = 0 ]; then
      wf_kv review_rounds_recorded "none for this review, so the panel starts at round 1"
    elif [ "$n" = 0 ]; then
      wf_kv review_rounds_recorded "none for this head; the $recorded recorded round(s) name a commit that is no longer in this branch's history (amended or rebased), so they describe other work and the panel starts at round 1"
    else
      wf_kv review_rounds_recorded "$n, the last at $(wf_short "$(wf_record_field "$(round_file "$n")" commit)")"
    fi
    [ "$n" = 0 ] || readable_pairs "$n" >/dev/null
    next_keys "$n"
    # The rounds themselves, for a context that did not run them: their verdicts are what the next round
    # continues from and their disputed: lines are what the summary still has to carry. Indented, so no line
    # of a round can be read as a key of this brief.
    if [ "$n" -gt 0 ]; then
      printf 'review_rounds_block:\n'
      r=1
      while [ "$r" -le "$n" ]; do
        printf '  round %s at %s\n' "$r" "$(wf_short "$(wf_record_field "$(round_file "$r")" commit)")"
        wf_record_body "$(round_file "$r")" | sed 's/^/    /'
        r=$((r + 1))
      done
    fi
    ;;
  round)
    block=$(cat)
    # A round is recorded for the commit its fixes are in and the gate ran on: that commit is what the next
    # context continues from, and what tells a record of this review from one of an older one.
    gated_head round "panel.sh round"

    refuse_line_breaks "$block" round "panel.sh round"
    stray=$(printf '%s\n' "$block" | grep -vE '^[[:space:]]*(panel|fixed|disputed):' | grep -v '^[[:space:]]*$' | head -1 || true)
    [ -z "$stray" ] || wf_die "the round block carries a line that is none of its three ('$stray'); a round states the panel:, the fixed: and the disputed: lines and nothing else, in the form:
$round_form"

    pairs=$(printf '%s\n' "$block" | panel_pairs)
    case "$pairs" in
      '!none') wf_die "the round block has no panel: line; expected one of the form: $panel_form" ;;
      '!empty') wf_die "the panel: line names no reviewer; expected one of the form: $panel_form" ;;
      '!twice') wf_die "the round block has more than one panel: line, so it states two panels for one round; keep the one the round ended on" ;;
      '!bad:'*) wf_die "'${pairs#!bad:}' states no verdict a round can carry: one PASS or FIX per reviewer, optionally with a parenthesised note, and never a chain (the chain across rounds is derived from the round records); expected a line of the form: $panel_form" ;;
      '!dup:'*) wf_die "the panel: line names ${pairs#!dup:} twice, so this round would state two verdicts for one reviewer; a round carries the one verdict that reviewer ended it on, and the chain across rounds is derived from the round records" ;;
    esac

    fixed=$(printf '%s\n' "$block" | sed -n -E 's/^[[:space:]]*fixed:[[:space:]]*//p')
    [ "$(printf '%s\n' "$fixed" | grep -c . || true)" = 1 ] || wf_die "a round states one fixed: line, the fixes made in it; expected: $fixed_form"
    counts=$(printf '%s\n' "$fixed" | sed -n -E 's/^([0-9]+)[[:space:]]*\(S1[[:space:]]+([0-9]+),[[:space:]]*S2[[:space:]]+([0-9]+),[[:space:]]*S3[[:space:]]+([0-9]+)\)[[:space:]]*$/\1 \2 \3 \4/p')
    [ -n "$counts" ] || wf_die "the fixed: line 'fixed: $fixed' is not of the form: $fixed_form"
    read -r total s1 s2 s3 <<COUNTS
$counts
COUNTS
    # Base ten, whatever the caller wrote: the regex above accepts a zero-padded count, and the shell reads
    # `08` as octal and aborts the arithmetic below with a message of its own, not an error: line with a fix.
    total=$((10#$total)); s1=$((10#$s1)); s2=$((10#$s2)); s3=$((10#$s3))
    [ "$total" = "$((s1 + s2 + s3))" ] || wf_die "the fixed: line counts $total fixes but names $((s1 + s2 + s3)) (S1 $s1, S2 $s2, S3 $s3); the summary sums the rounds, so correct the line and record the round again"

    disputed=$(disputed_lines "$block" round)

    # The rounds this one continues, read through the reader that refuses a record it cannot parse: the
    # answer below names the next round's reviewers from them, and a record read as a reviewer called "!bad"
    # would drop one still on FIX. Refused here, before anything of this round is measured or written.
    n=$(counted_rounds)
    readable_pairs "$n" >/dev/null

    # Measured before the record is written, so a checkpoint that cannot answer leaves no record behind and
    # the call is simply made again; a round recorded twice would count twice against the round limit.
    checkpoint=$("$here/checkpoint.sh" review)

    next=$((n + 1))
    limit=$(wf_review_rounds)
    [ "$next" -le "$limit" ] || wf_warn "this is round $next and max_rounds is $limit; the panel ends at the limit, so record the summary instead of running another round"
    mkdir -p "$state"
    # Every record from this round on goes: the ones a rewritten history left behind, and any the numbering
    # skipped, so what is read back is this review's rounds and nothing else.
    for old in "$state"/round.*; do
      [ -f "$old" ] || continue
      i=${old##*/round.}
      case "$i" in ''|*[!0-9]*) rm -f "$old"; continue ;; esac
      [ "$i" -lt "$next" ] || rm -f "$old"
    done
    file=$(round_file "$next")
    # One file, written in one move: the headers a later round and the summary read, an empty line, then the
    # round as this script parsed it. Nothing of the block is copied verbatim except the text of a disputed:
    # line, which begins with its own key and can therefore imitate no other.
    { printf 'round: %s\ncommit: %s\nat: %s\nfixed_s1: %s\nfixed_s2: %s\nfixed_s3: %s\n\n' \
        "$next" "$(git rev-parse HEAD)" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$s1" "$s2" "$s3"
      printf '%s\n' "$pairs" | awk '{ name = $1; $1 = ""; sub(/^ /, ""); line = line " " name "=" $0 } END { print "panel:" line }'
      printf 'fixed: %s (S1 %s, S2 %s, S3 %s)\n' "$total" "$s1" "$s2" "$s3"
      printf '%s\n' "$disputed"; } > "$file.tmp"
    mv "$file.tmp" "$file"

    wf_kv review_round_recorded "round $next at $(git rev-parse --short HEAD)"
    next_keys "$next"
    # The checkpoint of this round, with its own keys: between rounds the skill is already loaded, so no
    # injection can carry the measurement, and a worker that records a round cannot miss it (ADR 0032).
    printf '%s\n' "$checkpoint"
    ;;
  record)
    block=$(cat)
    n=$(counted_rounds)
    recorded=$(recorded_rounds)
    if [ "$n" = 0 ]; then
      [ "$recorded" = 0 ] || wf_die "the $recorded recorded round(s) name a commit that is no longer in this branch's history (amended or rebased), so they describe other work and the summary would state a panel nobody ran on this branch; run the panel from round 1 and record each round with panel.sh round"
      wf_die "no round of this review is recorded, so the summary would have no verdict to state; record every round with panel.sh round as it ends, then record the summary"
    fi
    # The rounds may describe an earlier commit of this branch, but the summary describes the head it is
    # recorded at: a commit made after the last round is one no reviewer read, so it is gated before the
    # summary calls the panel ready for it.
    gated_head summary "panel.sh record"
    refuse_line_breaks "$block" summary "panel.sh record"
    # The only thing the caller still writes: the findings it disputes, one line each. Everything else is
    # derived, so a line that states one of those keys would contradict the record it is written into.
    stray=$(printf '%s\n' "$block" | grep -v '^[[:space:]]*disputed:' | grep -v '^[[:space:]]*$' | head -1 || true)
    [ -z "$stray" ] || wf_die "the summary block carries a line that is not a disputed: one ('$stray'); the rounds, the panel: line and the fixed: counts are derived from the round records, so record only the disputed: lines that still stand"
    disputed=$(disputed_lines "$block" summary)

    pairs=$(readable_pairs "$n")
    # One verdict per round a reviewer ran in, oldest first, and the draft decision from the last of them.
    derived=$(printf '%s\n' "$pairs" | awk '
      { name = $1; $1 = ""; sub(/^ /, "")
        if (!(name in chain)) { order[++k] = name; chain[name] = $0 } else chain[name] = chain[name] "\342\206\222" $0
        last[name] = $0 }
      END {
        for (i = 1; i <= k; i++) { name = order[i]; line = line " " name "=" chain[name]; if (last[name] !~ /^PASS/) draft = 1 }
        print "panel:" line
        print (draft ? "draft" : "ready")
      }')
    panel_line=$(printf '%s\n' "$derived" | head -1)
    verdict=$(printf '%s\n' "$derived" | tail -1)
    s1=0; s2=0; s3=0; r=1
    while [ "$r" -le "$n" ]; do
      s1=$((s1 + $(field_num "$(round_file "$r")" fixed_s1)))
      s2=$((s2 + $(field_num "$(round_file "$r")" fixed_s2)))
      s3=$((s3 + $(field_num "$(round_file "$r")" fixed_s3)))
      r=$((r + 1))
    done
    # The verdict is only as complete as the rounds are: a reviewer no round ever ran is one nobody hears
    # about, so name it. A warning, not a refusal, because the review stage may run a shorter panel.
    named=" $(last_verdicts "$n" | awk '{ printf "%s ", $1 }')"
    set -f  # a reviewer name is data, so it may not glob the working directory
    for reviewer in $(wf_reviewers | tr ',' ' '); do
      case "$named" in *" $reviewer "*) ;; *) wf_warn "no round of this review names $reviewer, so the record says nothing about that reviewer" ;; esac
    done
    set +f
    commit=$(git rev-parse HEAD 2>/dev/null) || wf_die "this branch has no commit to record the summary at"
    # The rounds read the commit they were recorded at; the summary describes the head. A commit in between
    # is gated (above) but read by no reviewer, so the summary that covers it is a draft however the panel
    # ended: `verdict` is the word the yolo finish stage merges on, and nothing merges code nobody read. Not
    # a refusal, because the way on is a round at this head, which the round limit may no longer allow; the
    # line below says it in the block itself, which is the text the pull request body carries.
    unread=""
    last_round=$(wf_record_field "$(round_file "$n")" commit)
    if [ "$last_round" != "$commit" ]; then
      unread="unreviewed: $(git rev-list --count "$last_round..$commit") commit(s) since round $n at $(wf_short "$last_round"), which no reviewer read, so this summary is a draft"
      [ "$verdict" = draft ] || wf_warn "the panel passed, but $unread"
      verdict=draft
    fi
    mkdir -p "$state"
    # One file, written in one move: the headers, an empty line, then the summary the round records derive.
    { printf 'commit: %s\nverdict: %s\n\n' "$commit" "$verdict"
      printf 'review_rounds: %s\n%s\nfixed: %s (S1 %s, S2 %s, S3 %s)\n%s\n' \
        "$n" "$panel_line" "$((s1 + s2 + s3))" "$s1" "$s2" "$s3" "$disputed"
      [ -z "$unread" ] || printf '%s\n' "$unread"; } > "$record.tmp"
    mv "$record.tmp" "$record"
    # The summary closes the rounds it was derived from: a later review of this branch is a new panel, and
    # it starts at round 1 rather than continuing a review that has already been handed on.
    rm -f "$state"/round.*
    wf_kv panel_summary "recorded at $(wf_short "$commit")"
    wf_kv panel_verdict "$verdict"
    # The summary the rounds derived, in the same shape the pull request stage reads it in: this call is
    # where the worker learns what it recorded, and it reports that instead of its memory of the rounds.
    printf 'panel_summary_block:\n'
    wf_record_body "$record"
    ;;
  print)
    if [ ! -f "$record" ]; then
      wf_kv panel_summary "none recorded; the review stage did not hand one over, so the panel result is unknown"
      wf_kv panel_verdict "draft"
      exit 0
    fi
    commit=$(wf_record_field "$record" commit)
    wf_kv panel_summary "recorded at $(wf_short "$commit")"
    if [ "$(git rev-parse HEAD)" = "$commit" ]; then
      wf_kv panel_head "unchanged since the summary was recorded"
    elif ! git merge-base --is-ancestor "$commit" HEAD 2>/dev/null; then
      wf_kv panel_head "the recorded commit is no longer in this branch's history (amended or rebased), so the summary may describe other work"
    else
      wf_kv panel_head "commits since the summary was recorded, which it does not describe: $(git rev-list --count "$commit..HEAD")"
    fi
    wf_kv panel_verdict "$(recorded_verdict)"
    printf 'panel_summary_block:\n'
    wf_record_body "$record"
    ;;
  # The one word the yolo finish stage gates on, so it reads a contract rather than scraping the brief.
  verdict) recorded_verdict ;;
  *) wf_die "usage: panel.sh rounds | panel.sh round < block | panel.sh record < disputed | panel.sh print | panel.sh verdict" ;;
esac
