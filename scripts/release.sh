#!/usr/bin/env bash
# Tag a release: a plugin (bump version in its plugin.json first) or the factory (bump factory/VERSION).
# Usage: release.sh <plugin|factory> [--push]
set -euo pipefail
cd "$(dirname "$0")/.."
target="${1:?usage: release.sh <plugin|factory> [--push]}"; shift || true
die() { echo "error: $*" >&2; exit 1; }

if [ "$target" != factory ]; then
  claude plugin validate "plugins/$target" --strict
  claude plugin tag "plugins/$target" -m "$target v%s" "$@"
  exit
fi

# The factory is no plugin: it ships as a static binary, so its release is a tag CI builds from.
push=""
for arg in "$@"; do
  case "$arg" in
    --push) push=yes ;;
    *) die "unknown option $arg; usage: release.sh <plugin|factory> [--push]" ;;
  esac
done
[ -f factory/VERSION ] || die "factory/VERSION is missing; it is the one place the factory's version is written"
# Read as the binary reads it: factory/version.go embeds the file and trims its ends, so whitespace
# inside it is part of the version the binary reports. Stripping it here would tag a version nothing
# else in the release ever says.
version=$(cat factory/VERSION)
if ! printf '%s' "$version" | grep -Eqx '[0-9]+\.[0-9]+\.[0-9]+' \
  || [ "$(printf '%s\n' "$version" | wc -l | tr -d '[:space:]')" != 1 ]; then
  die "factory/VERSION says \"$version\"; write the version as X.Y.Z on one line"
fi
# The namespace: a plugin release is tagged <plugin>--vX.Y.Z and a milestone vX.Y.Z, so a tag with a
# slash in it can be neither. It is also the tag the Go module in factory/ would be published under.
tag="factory/v$version"
if [ -n "$(git status --porcelain)" ]; then
  die "the working tree has uncommitted changes; commit them, so the tag names what is released"
fi
# A binary that runs on a host is built from what was reviewed and merged, so origin decides what is
# taggable, not this checkout: one question asks whether the tag is taken there and what main points
# at (docs/repo-standard.md: releases are tagged on main).
remote=$(git ls-remote origin "refs/tags/$tag" refs/heads/main) \
  || die "cannot read the tags and branches of origin; a release is tagged from a checkout that reaches it"
if printf '%s\n' "$remote" | awk -v t="refs/tags/$tag" '$2 == t { taken = 1 } END { exit !taken }'; then
  die "the tag $tag exists on origin; bump the version in factory/VERSION and commit it"
fi
main=$(printf '%s\n' "$remote" | awk '$2 == "refs/heads/main" { print $1 }')
[ -n "$main" ] || die "origin has no main branch; releases are tagged on main (docs/repo-standard.md)"
if tagged=$(git rev-parse -q --verify "refs/tags/$tag^{commit}"); then
  # Tagged here and never pushed, which is where a run without --push ends: the release is one push
  # away, so this says that rather than to bump a version the tag already carries. That holds only
  # while the tag names what origin/main names: CI asks whether the tagged commit is on main, not
  # whether it is its tip, so a tag left over from an earlier attempt would release an older factory
  # under this version.
  [ "$tagged" = "$main" ] || die "the tag $tag exists here and names $tagged, which is not origin/main \
($main); it would release an older factory, so delete it with: git tag -d $tag"
  die "the tag $tag exists here and not on origin; push it with: git push origin $tag"
fi
head=$(git rev-parse HEAD)
[ "$head" = "$main" ] || die "HEAD is $head and origin/main is $main; releases are tagged on main \
(docs/repo-standard.md), so a released binary is built from a commit that was reviewed and gated"
# The gate is what a release stands on: the binaries CI builds are never gated again.
make check || die "the gate failed; a release is tagged from a green gate, fix it and run this again"
git tag -a "$tag" -m "factory v$version"
echo "tagged $tag"
if [ -n "$push" ]; then
  git push origin "$tag"
  echo "pushed $tag; CI builds the static linux binaries and attaches them to the release"
else
  echo "push it with: git push origin $tag"
fi
