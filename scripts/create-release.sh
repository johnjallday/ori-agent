#!/bin/bash
# Compatibility entry point. A stable version alone no longer bypasses RC testing.
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ "${1:-}" == --help || "${1:-}" == -h ]]; then
  echo 'Usage: ./scripts/create-release.sh vX.Y.Z-rc.N [--yes]'
  echo 'Promotes the exact tested candidate through GitHub Actions.'
  exit 0
fi
exec "$script_dir/release.sh" promote "$@"
