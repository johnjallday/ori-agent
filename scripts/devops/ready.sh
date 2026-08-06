#!/usr/bin/env bash
# The Ready column of this repository's project board, in the order it was
# ranked. A plain executable rather than a shell function, so an agent can call
# it as one stable token instead of sourcing a zsh file first.
#
# Ready means buildable: an Issue a grooming agent has researched and written a
# spec comment on. It does not mean approved — choosing what to build stays with
# the person reading the column. What has not reached this column yet is
# backlog.sh.
#
# Read-only. Ranking is the grooming agent's job and lifecycle is GitHub's.
set -euo pipefail

devflow_script_name="ready.sh"
# shellcheck source=scripts/lib/devflow-bootstrap.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/devflow-bootstrap.sh"

devflow_exec ready "$@"
