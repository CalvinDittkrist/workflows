#!/usr/bin/env bash
# Count the writing rules (docs/repo-standard.md, writing rules) over the files of a repository: no em dash in
# any text file, and the word caps. A word is a whitespace-separated token that is not punctuation alone.
# Usage: writing.sh <repo-root> < files
# The files are read from stdin, one path relative to the root per line. Prints one line per finding, its
# group (`dash`, `words` or `docs`) and the message, tab separated; nothing when the files follow the rules.
# check.sh fails or warns on the findings, facts.sh hands them to the docs auditor.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"
root=${1:?usage: writing.sh <repo-root> < files}
[ -d "$root" ] || { printf 'error: %s is no directory; pass the repository root\n' "$root" >&2; exit 1; }
all=$(cat)
# shellcheck disable=SC2086 # a list of names
readme=$(first_of "$root" $WF_README_NAMES || true)
existing_nul() { # the regular files of the list on stdin, no symlinks, NUL separated and prefixed ./, so no name reads as an option or an awk assignment
  while IFS= read -r f; do if [ -n "$f" ] && [ -f "$root/$f" ] && [ ! -L "$root/$f" ]; then printf './%s\0' "$f"; fi; done
}

# The em dash, counted per file. grep finds nothing in most repositories, which is no error.
emdash=$(printf '\342\200\224')
printf '%s\n' "$all" | existing_nul | { (cd "$root" && LC_ALL=C xargs -0 grep -oIHF -- "$emdash" /dev/null 2>/dev/null) || true; } \
  | LC_ALL=C awk '{ sub(/:[^:]*$/, ""); sub(/^\.\//, ""); n[$0]++ } END { for (f in n) printf "%d\t%s\n", n[f], f }' \
  | LC_ALL=C sort -t "$(printf '\t')" -k2 \
  | while IFS="$(printf '\t')" read -r n f; do # the count first, so a tab in a file name stays in the name
      if [ "$n" = 1 ]; then printf 'dash\t%s has 1 em dash; use a comma, a colon or two sentences\n' "$f"
      else printf 'dash\t%s has %d em dashes; use a comma, a colon or two sentences\n' "$f" "$n"; fi
    done

# Markdown (`.md`, `.markdown`), one pass: a paragraph at most 80 words, a bullet (a list item, numbered or not)
# at most 30, a row of docs/glossary.md at most 40. Code blocks (fenced or indented), front matter, thematic
# breaks and tables (with or without the leading pipe) are no paragraphs. Front matter needs its closing line;
# without it the opening `---` is a thematic break. Documents are counted whole but for code blocks and front
# matter: the README 1200, docs/architecture.md 2000, an ADR 250 and a plugin README 800. A README in another
# format has no Markdown structure, so only its whole count is checked.
# shellcheck disable=SC2016 # an awk program
count='
function words(s,   t, i, n, c) { n = split(s, t, /[ \t\r]+/); for (i = 1; i <= n; i++) if (t[i] != "" && t[i] !~ /^[[:punct:]]+$/) c++; return c + 0 }
function cols(s,   i, c, n) { for (i = 1; i <= length(s); i++) { c = substr(s, i, 1); n += (c == "\t") ? 4 - n % 4 : 1 }; return n + 0 }
function indent(s) { match(s, /^[ \t]*/); return cols(substr(s, 1, RLENGTH)) }
function blank(s) { return s ~ /^[ \t\r]*$/ }
function flush() {
  if (kind == "p" && cnt > 80) printf "words\t%s:%d: paragraph of %d words (>80); split it or make it bullets\n", f, start, cnt
  if (kind == "b" && cnt > 30) printf "words\t%s:%d: bullet of %d words (>30); shorten it or split it\n", f, start, cnt
  kind = ""; cnt = 0
}
function row(   n) {
  n = words($0); total += n
  if (f == "docs/glossary.md" && table && $0 !~ /^[ \t|:\r-]+$/ && n > 40) printf "words\t%s:%d: glossary entry of %d words (>40); shorten it\n", f, FNR, n
  table = 1
}
function finish(   cap, what) {
  flush()
  if (f == readme) { cap = 1200; what = "the README" }
  else if (f == "docs/architecture.md") { cap = 2000; what = "the architecture map" }
  else if (f ~ /^docs\/adr\/[0-9][0-9][0-9][0-9]-[^\/]*\.md$/) { cap = 250; what = "an ADR" }
  else if (f ~ /^plugins\/[^\/]+\/README\.md$/) { cap = 800; what = "a plugin README" }
  if (cap && total > cap) printf "docs\t%s has %d words (>%d for %s); shorten it\n", f, total, cap, what
}
FNR == 1 {
  if (f != "") finish()
  f = FILENAME; sub(/^\.\//, "", f); total = 0; fence = ""; table = 0; code = 0; list = -1; front = 0; pipe = 0
  md = (tolower(f) ~ /\.(md|markdown)$/)
  if (md && $0 ~ /^---[ \t\r]*$/) {
    seen = 0; while ((getline l < FILENAME) > 0) if (++seen > 1 && l ~ /^---[ \t\r]*$/) { front = 1; break }
    close(FILENAME); if (front) next
  }
}
!md { total += words($0); next }
front { if ($0 ~ /^---[ \t\r]*$/) front = 0; next }
fence != "" { t = $0; gsub(/[ \t\r]/, "", t); if (substr(t, 1, 3) == fence && t ~ /^(```+|~~~+)$/) fence = ""; next }
code { if (blank($0) || indent($0) >= code) next; code = 0 }
table { if (!blank($0)) { row(); next }; table = 0 }
kind == "" && !blank($0) && indent($0) >= (list < 0 ? 0 : list) + 4 { code = (list < 0 ? 0 : list) + 4; next }
/^[ \t]*(```|~~~)/ { flush(); fence = ($0 ~ /^[ \t]*~/) ? "~~~" : "```"; next }
/^[ \t]*\|/ { flush(); row(); next }
kind == "p" && pipe && $0 ~ /\|/ && $0 ~ /-/ && $0 ~ /^[ \t|:\r-]+$/ { cnt -= last; flush(); table = 1; next }
blank($0) { flush(); next }
{ t = $0; gsub(/[ \t\r]/, "", t) }
t ~ /^(---+|\*\*\*+|___+)$/ { flush(); next }
/^[ \t]*#+([ \t]|$)/ { flush(); list = -1; total += words($0); next }
/^[ \t]*([-*+]|[0-9]+[.)])[ \t]/ {
  flush(); kind = "b"; start = FNR; line = $0
  match(line, /^[ \t]*([-*+]|[0-9]+[.)])[ \t]+/); list = cols(substr(line, 1, RLENGTH))
  line = substr(line, RLENGTH + 1); cnt = words(line); total += cnt; next
}
{
  if (kind == "") { kind = "p"; start = FNR; if (indent($0) < list) list = -1 }
  n = words($0); cnt += n; total += n; last = n; pipe = ($0 ~ /\|/)
}
END { if (f != "") finish() }'
{ printf '%s\n' "$all" | grep -Ei '\.(md|markdown)$' || true; [ -z "$readme" ] || printf '%s\n' "$readme"; } \
  | LC_ALL=C sort -u | existing_nul | (cd "$root" && LC_ALL=C xargs -0 awk -v readme="$readme" "$count" /dev/null)
