#!/usr/bin/env bash
# Print the path of the gofmt that ships with the Go toolchain go.mod pins.
#
# CI formats with the toolchain named in go.mod (setup-go reads go.mod), but a
# bare `gofmt` on PATH belongs to whatever Go is installed locally. Go's
# toolchain switching only applies to the `go` command, so a newer local gofmt
# can pass a file that CI's gofmt rejects (the two align map keys differently).
# Resolving gofmt from the pinned toolchain's GOROOT removes the mismatch.
#
# Usage: scripts/go-toolchain-gofmt.sh [go.mod path]
#
# Falls back to the gofmt on PATH, with a warning on stderr, when the pinned
# toolchain cannot be resolved (for example offline and not yet downloaded).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gomod="${1:-$root/go.mod}"

# A `toolchain` line wins over the `go` line, matching what setup-go installs.
version="$(awk '$1 == "toolchain" { sub(/^go/, "", $2); print $2; exit }' "$gomod")"
if [[ -z "$version" ]]; then
  version="$(awk '$1 == "go" { print $2; exit }' "$gomod")"
fi

fallback() {
  echo "go-toolchain-gofmt: $1; using the gofmt on PATH, which may differ from CI's" >&2
  command -v gofmt
  exit 0
}

[[ -n "$version" ]] || fallback "no Go version found in $gomod"

goroot="$(cd "$root" && GOTOOLCHAIN="go${version}" go env GOROOT 2>/dev/null)" ||
  fallback "could not resolve toolchain go${version}"

if [[ -x "$goroot/bin/gofmt" ]]; then
  echo "$goroot/bin/gofmt"
else
  fallback "no gofmt under $goroot"
fi
