#!/usr/bin/env bash
# Print a page of the Claude Code documentation, pinned to one origin.
#   claude-docs.sh          the index of every page (llms.txt)
#   claude-docs.sh <slug>   one page, https://code.claude.com/docs/en/<slug>.md
# The origin and the shape of the URL are built here, never passed in: a worker reads issue text written by
# someone else, so the one network call it has must not be steerable into another host (ADR 0029).
set -euo pipefail
. "$(dirname "$0")/lib.sh"

# The one origin this script talks to, and the prefix every requested and every answering URL must carry.
docs_base="https://code.claude.com/docs"
# Seconds for the whole request, and the largest answer that is still a documentation page (the biggest one
# on 2026-09-21 is 115 KB). Both bound what one lookup can cost a session in wall clock and in context.
timeout=${WF_DOCS_TIMEOUT:-30}
max_bytes=5000000
case $timeout in ''|*[!0123456789]*) wf_die "WF_DOCS_TIMEOUT is '$timeout'; set it to a number of seconds, or unset it for 30" ;; esac

case $# in
  0) url="$docs_base/llms.txt" ;;
  1)
    # The slug is a path segment of a URL, so it is matched against a literal set, not against a range like
    # [a-z0-9-]: ranges collate per locale, and this check must mean the same in every environment. A slash,
    # a dot, a colon, a newline and an empty slug all fall outside it, so no argument can leave the path.
    case $1 in
      ''|*[!abcdefghijklmnopqrstuvwxyz0123456789-]*)
        wf_die "not a documentation page: '$1'; pass a slug of lowercase letters, digits and hyphens (sub-agents), not a path or a URL; no argument prints the index" ;;
    esac
    url="$docs_base/en/$1.md" ;;
  *) wf_die "usage: claude-docs.sh [<slug>]; one page slug, or no argument for the index" ;;
esac

wf_need curl
body=$(mktemp) || wf_die "could not create a temporary file for the response"
trap 'rm -f "$body"' EXIT

# --fail: an error page is an error, not a page. --proto/--proto-redir: https only, before and after a
# redirect. url_effective is then checked against the origin, so a redirect that leaves it prints nothing.
effective=$(curl --silent --show-error --fail --location --max-redirs 3 \
  --proto '=https' --proto-redir '=https' --max-time "$timeout" --max-filesize "$max_bytes" \
  --output "$body" --write-out '%{url_effective}' "$url") \
  || wf_die "could not read $url; check the network, or the slug against the index (claude-docs.sh with no argument)"

case $effective in
  "$docs_base/"*) ;;
  *) wf_die "$url answered from $effective, outside $docs_base/; nothing is printed, report it instead of working around it" ;;
esac

wf_kv url "$effective"
cat "$body"
