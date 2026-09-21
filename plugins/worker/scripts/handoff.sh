#!/usr/bin/env bash
# The handoff: this worker's pipeline continues in a fresh context in the same pane (ADR 0021).
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

# The stages of the driver a checkpoint hands over before. Both are a stage boundary at which everything the
# next context needs is in git, in GitHub or in a record of this worktree.
stages="review ci"
# Every section a note carries, in the order a reader wants them. A fresh context reads this note instead of
# the transcript it will never see, so a missing section is a refusal, not a warning.
sections="decisions rejected verified open"
driver="/worker:work"

stage="${1:-}"
[ -n "$stage" ] || wf_die "usage: handoff.sh <$(printf '%s' "$stages" | tr ' ' '|')> < note"
case " $stages " in *" $stage "*) ;; *) wf_die "'$stage' is not a stage this pipeline hands over before; the two checkpoints resume at $(printf '%s' "$stages" | sed 's/ / or /')" ;; esac

wf_need git; wf_need jq; wf_need herdr
[ "${HERDR_ENV:-}" = 1 ] || wf_die "this session runs outside a Herdr pane (HERDR_ENV is not 1), so nothing can clear it and send the driver command back; carry on in this context instead"
pane="${HERDR_PANE_ID:-}"
[ -n "$pane" ] || wf_die "HERDR_PANE_ID is empty, so this session cannot name the pane to clear; carry on in this context instead"

# The next context reads the branch, not this working tree, and never learns what was left in it.
dirty=$(git status --porcelain)
[ -z "$dirty" ] || wf_die "the working tree has uncommitted changes, which the next context would not see:
$(printf '%s' "$dirty" | sed 's/^/  /')
Commit what belongs to the change, remove what does not, then hand over again."
commit=$(git rev-parse HEAD 2>/dev/null) || wf_die "this branch has no commit, so the next context would find none of the work; commit it first"

# The one signal that the pane really started a fresh session, read before the handover so the detached
# process has something to compare against. Without it nothing could tell the new context from this one.
session=$(herdr agent get "$pane" 2>/dev/null | jq -r '.result.agent.agent_session.value // empty' 2>/dev/null || true)
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

# The same range the reviewers read, resolved the same way as in diff-context.sh: the commits of this
# branch, whether or not they are pushed already, so the second checkpoint lists them as the first one does.
base=$(wf_base_branch)
ref="origin/$base"; git rev-parse -q --verify "$ref" >/dev/null 2>&1 || ref="$base"
mb=$(git merge-base "$ref" HEAD 2>/dev/null || printf '%s' "$ref")

record="$(wf_state_dir)/handoff"
mkdir -p "$(dirname "$record")"
# One file, written in one move: the headers the hook and the facts read, an empty line, then the note
# followed by the state of the branch, which the note's author does not have to copy by hand.
{
  printf 'stage: %s\ncommit: %s\nbase: %s\npane: %s\nsession: %s\nat: %s\n\n' \
    "$stage" "$commit" "$base" "$pane" "$session" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf '%s\n' "$note"
  printf '\n## state at the handoff\n'
  "$(dirname "$0")/diff-context.sh"
  printf 'log:\n'
  git log --oneline --max-count=50 "$mb..HEAD" | sed 's/^/  /'
} > "$record.tmp"
mv "$record.tmp" "$record"

nohup bash "$(dirname "$0")/handoff-resume.sh" "$pane" "$session" "$stage" "$driver" >/dev/null 2>&1 &

wf_kv handoff "started for pane $pane"
wf_kv resume_stage "$stage"
wf_kv note "recorded at $(git rev-parse --short "$commit") in $record, outside the working tree"
wf_kv next "end your turn without another tool call: when this session is idle the detached process clears it and sends $driver to the pane, where the SessionStart hook injects the note"
