#!/usr/bin/env bash
# The handoff: this worker's pipeline continues in a fresh context in the same pane (ADR 0029).
# Usage: handoff.sh <review|ci>   with the note on stdin
# The note is the only thing the next context learns that git and GitHub do not tell it, so this script
# refuses one that leaves a section out, and refuses a working tree whose changes are in neither. It writes
# the note into this worktree's git directory (ADR 0018) — outside the tree, never committed — appends the
# state of the branch, and starts the detached process that clears the session and sends the driver command
# back to the pane. Nothing else in the pipeline reads the note: the SessionStart hook injects it once, and
# no reviewer and no pull request author brief carries it.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

# The stages a checkpoint hands over before: the list lib.sh shares with checkpoint.sh, so the stage it
# accepts and the stage this script accepts cannot drift apart.
stages=$(wf_handoff_stages)
# Every section a note carries, in the order a reader wants them. A fresh context reads this note instead of
# the transcript it will never see, so a missing section is a refusal, not a warning.
sections="decisions rejected verified open"
driver="/worker:work"

stage="${1:-}"
[ -n "$stage" ] || wf_die "usage: handoff.sh <$(printf '%s' "$stages" | tr ' ' '|')> < note"
wf_in_list "$stage" "$stages" ||
  wf_die "'$stage' is not a stage this pipeline hands over before; a checkpoint resumes at $(printf '%s' "$stages" | sed 's/ / or /')"

wf_need git; wf_need jq; wf_need herdr
[ "${HERDR_ENV:-}" = 1 ] || wf_die "this session runs outside a Herdr pane (HERDR_ENV is not 1), so nothing can clear it and send the driver command back; carry on in this context instead"
pane="${HERDR_PANE_ID:-}"
[ -n "$pane" ] || wf_die "HERDR_PANE_ID is empty, so this session cannot name the pane to clear; carry on in this context instead"

# The next context reads the branch, not this working tree, and never learns what was left in it.
dirty=$(wf_dirty_tree)
[ -z "$dirty" ] || wf_die "the working tree has uncommitted changes, which the next context would not see:
$dirty
Commit what belongs to the change, remove what does not, then hand over again."
commit=$(git rev-parse HEAD 2>/dev/null) || wf_die "this branch has no commit, so the next context would find none of the work; commit it first"

# The one signal that the pane really started a fresh session, read before the handover so the detached
# process has something to compare against. Without it nothing could tell the new context from this one.
session=$(wf_agent_session "$pane")
[ -n "$session" ] || wf_die "herdr reports no agent session for pane $pane, so nothing could confirm that a fresh context started; hand over by hand instead: /clear, then $driver"

note=$(cat)
# A section is present when it has a heading and text under it; a heading with nothing under it says as
# little as no heading at all.
section_text() { awk -v h="$1" '
  $0 ~ "^##[[:space:]]+" h "[[:space:]]*$" { inside = 1; next }
  /^##[[:space:]]/ { inside = 0 }
  inside && /[^[:space:]]/ { print }
' ; }
for section in $sections; do
  printf '%s\n' "$note" | section_text "$section" | grep -q . || wf_die "the note has no '## $section' section with text under it; a fresh context reads the note instead of this transcript, so it needs all of them ($(printf '%s' "$sections" | sed 's/ /, ## /g; s/^/## /')); write that section and hand over again"
done

# The same range the reviewers read: the commits of this branch, whether or not they are pushed already, so
# the ci entrance lists them as the review one does.
base=$(wf_base_branch)
mb=$(wf_merge_base)

# The state of the branch, which the note's author does not have to copy by hand. Collected before the
# record is written, so a git that cannot answer ends in an error line here instead of in half a note.
state=$("$(dirname "$0")/diff-context.sh") ||
  wf_die "the range of this branch could not be read, so the next context would get a note without the state of the work; fix what diff-context.sh reports and hand over again"
log=$(git log --oneline --max-count=50 "$mb..HEAD") ||
  wf_die "the commits of $mb..HEAD could not be listed, so the note would name no work; fix that and hand over again"

record="$(wf_state_dir)/handoff"
mkdir -p "$(dirname "$record")"
# One file, written in one move: the headers the hook and the facts read, an empty line, then the note
# followed by the state of the branch. A half-written file is never left behind for the hook to read.
trap 'rm -f "$record.tmp"' EXIT
{
  printf 'stage: %s\ncommit: %s\nbase_branch: %s\npane: %s\nsession: %s\nat: %s\n\n' \
    "$stage" "$commit" "$base" "$pane" "$session" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf '%s\n' "$note"
  printf '\n## state at the handoff\n%s\n' "$state"
  printf 'log:\n%s\n' "$(printf '%s\n' "$log" | sed 's/^/  /')"
} > "$record.tmp"
mv "$record.tmp" "$record"
trap - EXIT

# The record goes with it: the detached process reads it to learn whether a fresh context has taken the note
# already, which is the one case in which a second `/clear` would destroy what it came to deliver.
nohup bash "$(dirname "$0")/handoff-resume.sh" "$pane" "$session" "$stage" "$driver" "$record" >/dev/null 2>&1 &

wf_kv handoff "started for pane $pane"
wf_kv resume_stage "$stage"
wf_kv note "recorded at $(git rev-parse --short "$commit") in $record, outside the working tree"
wf_kv next "end your turn without another tool call: when this session is idle the detached process clears it and sends $driver to the pane, where the SessionStart hook injects the note"
