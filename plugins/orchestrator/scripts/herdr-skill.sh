#!/usr/bin/env bash
# Print Herdr's own skill body (frontmatter stripped) so the herdr skill can inject it.
set -uo pipefail
herdr --skill 2>/dev/null | awk 'BEGIN{n=0} /^---$/ && n<2 {n++; next} n>=2 {print}'
