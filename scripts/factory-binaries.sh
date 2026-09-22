#!/usr/bin/env bash
# Build the binaries a factory host downloads, with their checksums. `make binaries` runs this after
# building the dashboard the binary embeds, and the release workflow runs that, so the flags a
# released binary was built with are written here and nowhere else.
# Usage: factory-binaries.sh [output directory, dist by default]
set -euo pipefail
cd "$(dirname "$0")/.."
out="${1:-dist}"

# The dashboard the binary embeds is built by `make binaries` before this runs, because the Makefile
# owns what is built. Without it the embed still matches the committed placeholder and the binary
# answers 404 under /, which is not what a host downloads: refuse instead of shipping that.
if [ ! -f factory/ui/dist/app/index.html ]; then
  echo "error: the dashboard is not built, so these binaries would have no interface; run make binaries, which builds it first" >&2
  exit 1
fi

mkdir -p "$out"
out="$(cd "$out" && pwd)"

# Linux, because that is what a factory host runs, and both architectures it comes as: a Raspberry Pi
# is arm64, a small server amd64. Without cgo, so the binary needs nothing of the host it lands on,
# and with -trimpath, so no path of the machine that built it is in it and two builds of one
# commit with one toolchain are the same file.
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go -C factory build -trimpath -o "$out/factory-linux-$arch" .
done

# sha256sum is the GNU tool on the CI runner, shasum the one macOS brings; they write the same format.
sums() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}
(cd "$out" && sums factory-linux-* > checksums.txt)
echo "built factory-linux-amd64, factory-linux-arm64 and checksums.txt in $out"
