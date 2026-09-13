#!/bin/bash
# Read-only readiness entry point (fetches refs; never pushes or publishes).
# RELEASE_MIN_PRS defaults to 10; FORCE_RELEASE=true bypasses cadence only.
# AUTO_RELEASE_HOLD stops lifecycle writes. Active candidates hold new batches.
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir/.."
exec python3 "$script_dir/release-candidate.py" evaluate "$@"
