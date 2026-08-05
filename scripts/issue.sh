#!/usr/bin/env bash
# This repository's GitHub Issues: list them, read one, capture a new one. A
# plain executable rather than a shell function, so an agent can call it as one
# stable token instead of sourcing a zsh file first.
#
# Issues are the record — your words, and the grooming agent's spec comment.
# The ordered, groomed view of them is a project board, which `backlog.sh`
# reads. Capture here; decide there.
#
# Every subcommand — list, view, add, their flags, and any spelling this shell
# has never heard of — goes to the Go helper, which owns the query, the bounds,
# the sanitization, the JSON contract, and the rejections. One parser decides
# what an invocation means; splitting that between the shell and the helper is
# how a quoted title ends up as two arguments.
set -euo pipefail

devflow_script_name="issue.sh"
# shellcheck source=scripts/lib/devflow-bootstrap.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/devflow-bootstrap.sh"

devflow_exec issue "$@"
