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
version=$(tr -d '[:space:]' < factory/VERSION)
printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' \
  || die "factory/VERSION says \"$version\"; write the version as X.Y.Z"
# The namespace: a plugin release is tagged <plugin>--vX.Y.Z and a milestone vX.Y.Z, so a tag with a
# slash in it can be neither. It is also the tag the Go module in factory/ would be published under.
tag="factory/v$version"
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  die "the tag $tag exists; bump the version in factory/VERSION and commit it"
fi
if [ -n "$(git status --porcelain)" ]; then
  die "the working tree has uncommitted changes; commit them, so the tag names what is released"
fi
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
