#!/usr/bin/env bash
# The Backlog column of this repository's project board: captured work that has
# not been groomed yet. A plain executable rather than a shell function, so an
# agent can call it as one stable token instead of sourcing a zsh file first.
#
# This is the column GitHub's auto-add workflow drops every new Issue into, so
# it answers "what is waiting" — not "what can I start", which is ready.sh. The
# name matches the column on the board deliberately: a command called backlog
# that showed a different column than the one labelled Backlog is a trap that
# gets rediscovered every few weeks.
#
# Read-only. Ranking is the grooming agent's job and lifecycle is GitHub's.
set -euo pipefail

devflow_script_name="backlog.sh"
# shellcheck source=scripts/lib/devflow-bootstrap.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/devflow-bootstrap.sh"

devflow_exec backlog "$@"
