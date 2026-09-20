#!/usr/bin/env bash
# The facts a spec acceptance starts from: the spec, its tickets with the merged pull requests that closed
# them, the files those pull requests changed, and the deviations accepted in earlier runs.
# Usage: accept-facts.sh <spec> [<ticket>...]   (tickets only where native sub-issues are unavailable)
set -euo pipefail
. "$(dirname "$0")/lib.sh"
wf_need gh; wf_need jq
usage() { sed -n '2,4p' "$0"; exit "${1:-0}"; }
case "${1:-}" in -h|--help) usage 0 ;; "") usage 1 ;; esac

spec=$(wf_issue_num "$1"); shift
tickets=""
for t in "$@"; do tickets="$tickets $(wf_issue_num "$t")"; done

nwo=$(wf_repo_nwo) || wf_die "cannot read the repository; is gh authenticated here?"
issue_json() { gh api "repos/$nwo/issues/$1" 2>/dev/null || wf_die "issue #$1 does not exist in $nwo"; }

# The spec itself: it must be a spec issue, and an open one.
sj=$(issue_json "$spec")
labels=$(printf '%s' "$sj" | jq -r '[.labels[]?.name] | join(",")')
case ",$labels," in
  *,spec,*) ;;
  *) wf_die "#$spec is not labelled spec (labels: ${labels:--}); an acceptance judges a spec against the code. Open a planning session on the issue to triage it." ;;
esac
[ "$(printf '%s' "$sj" | jq -r .state)" = open ] || wf_die "#$spec is closed; it was accepted already. Reopen it to accept it again."

# The tickets: the arguments, or the native sub-issues.
if [ -z "$tickets" ]; then
  subs=$(gh api "repos/$nwo/issues/$spec/sub_issues?per_page=100" 2>/dev/null || echo '[]')
  tickets=$(printf '%s' "$subs" | jq -r '[.[]?.number] | join(" ")')
  [ -n "$tickets" ] || wf_die "#$spec has no native sub-issues; pass the ticket numbers as further arguments: accept-facts.sh $spec <ticket>..."
fi

# One line per ticket first, so an open one is refused before any pull request is read.
rows=""; open_tickets=""
for t in $tickets; do
  tj=$(issue_json "$t")
  state=$(printf '%s' "$tj" | jq -r .state)
  if [ "$state" = open ]; then open_tickets="$open_tickets #$t"; fi
  rows="$rows$t	$state	$(printf '%s' "$tj" | jq -r '.title | gsub("[\\n\\t]"; " ")')
"
done
[ -z "$open_tickets" ] || wf_die "#$spec still has open tickets:$open_tickets; accept the spec when all of them are closed"

# The merged pull requests that closed each ticket, and the files they touched.
pr_page=20
query='query($o:String!,$r:String!,$n:Int!){repository(owner:$o,name:$r){issue(number:$n){closedByPullRequestsReferences(first:'"$pr_page"',includeClosedPrs:false){nodes{number merged files(first:100){totalCount nodes{path}}}}}}}'
files=$(mktemp); trap 'rm -f "$files" "$files.u"' EXIT
ticket_rows=""
while IFS='	' read -r t state title; do
  [ -n "$t" ] || continue
  prs="-"
  if pj=$(gh api graphql -f query="$query" -F o="${nwo%%/*}" -F r="${nwo#*/}" -F n="$t" 2>/dev/null); then
    nodes=$(printf '%s' "$pj" | jq -c '[.data.repository.issue.closedByPullRequestsReferences.nodes[]?]')
    merged=$(printf '%s' "$nodes" | jq -c '[.[] | select(.merged)]')
    prs=$(printf '%s' "$merged" | jq -r '[.[] | "#\(.number)"] | join(" ")')
    [ -n "$prs" ] || prs="-"
    printf '%s' "$merged" | jq -r '.[].files.nodes[]?.path' >> "$files"
    [ "$(printf '%s' "$nodes" | jq length)" -lt "$pr_page" ] || wf_warn "#$t names $pr_page or more pull requests; only the first $pr_page are read"
    truncated=$(printf '%s' "$merged" | jq -r '[.[] | select(.files.totalCount > (.files.nodes | length)) | "#\(.number)"] | join(" ")')
    [ -z "$truncated" ] || wf_warn "pull request(s) $truncated changed more than 100 files; the file list is incomplete"
  else
    wf_warn "could not read the pull requests that closed #$t; the pull requests and files below are incomplete"
  fi
  ticket_rows="$ticket_rows  $t,$state,$prs,$title
"
done <<EOF
$rows
EOF

# Deviations accepted in earlier runs: comments on the spec that open with the fixed marker line.
comments=$(gh api "repos/$nwo/issues/$spec/comments?per_page=100" 2>/dev/null || echo '[]')
marked=$(printf '%s' "$comments" | jq -c --arg marker '> Accepted deviation (spec acceptance).' '
  [.[] | select((((.body // "") | split("\n") | .[0] // "") | sub("\r$"; "")) == $marker)]')
# Anyone can comment the marker on a public issue, so only a maintainer's comment counts, and it is attributed.
maintainer='def maintainer: .author_association as $a | ["OWNER", "MEMBER", "COLLABORATOR"] | index($a) != null;'
deviations=$(printf '%s' "$marked" | jq -r "$maintainer"' .[] | select(maintainer)
  | "@\(.user.login // "unknown"): " + (((.body // "") | split("\n")[1:] | join(" ") | gsub("\\s+"; " ") | sub("^ "; "") | sub(" $"; "")))')
outsiders=$(printf '%s' "$marked" | jq -r "$maintainer"' [.[] | select(maintainer | not)] | length')
[ "$outsiders" = 0 ] || wf_warn "ignored $outsiders comment(s) with the deviation marker from outside the repository; only a maintainer accepts a deviation"

wf_kv repo "$nwo"
wf_kv spec "#$spec $(printf '%s' "$sj" | jq -r .title)"
wf_kv milestone "$(printf '%s' "$sj" | jq -r '.milestone.title // "-"')"
wf_kv base "$(wf_base_branch)"
printf 'tickets[%s]{issue,state,prs,title}:\n' "$(printf '%s' "$tickets" | wc -w | tr -d ' ')"
printf '%s' "$ticket_rows"
LC_ALL=C sort -u "$files" > "$files.u" && mv "$files.u" "$files"   # stable order on every machine
printf 'files[%s]:\n' "$(wc -l < "$files" | tr -d ' ')"
sed 's/^/  /' "$files"
printf 'deviations[%s]:\n' "$(printf '%s' "$deviations" | grep -c . || true)"
printf '%s\n' "$deviations" | sed '/^$/d; s/^/  /'
