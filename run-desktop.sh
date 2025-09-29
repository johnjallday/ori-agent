#!/bin/bash

# Convenience script to run Dolphin Desktop
# This script simply calls the main run-desktop.sh script in the scripts directory

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "$SCRIPT_DIR/scripts/run-desktop.sh" "$@"