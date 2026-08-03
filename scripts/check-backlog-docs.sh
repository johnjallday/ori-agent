#!/bin/zsh
# Fail when current documentation or executable code still instructs anyone to
# maintain the deleted BACKLOG.md file.
#
# The repository's backlog is GitHub Issues. The old file-backed workflow left
# instructions in several docs, and instructions are the part that actually
# misleads somebody: a stale sentence telling a reader to run `wt backlog sync`
# costs them a confused minute, and a stale one telling an agent to promote an
# entry costs a commit nobody wanted.
#
# History is not the target. Reports, release notes, and anything describing what
# used to happen are allowed to say the old name, so this check works on an
# explicit allowlist of paths rather than trying to guess intent from wording.
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$repo_root"

# Paths that may mention the old file because they are a record of the past, or
# because their whole job is asserting the file is gone.
typeset -a historical
historical=(
  "docs/feature-discovery/reports/"   # dated discovery reports, written at the time
  "scripts/check-backlog-docs.sh"     # this file
  "scripts/wt-backlog.test.sh"        # asserts the file and its commands are gone
  "scripts/wt-herd.test.sh"           # asserts no backlog commit is ever created
  "tools/herdr-devflow/internal/overview/types.go"        # schema-version note
  "tools/herdr-devflow/internal/overview/sanitize_test.go" # writes one, proves it is ignored
  "tools/herdr-devflow/internal/app/dispatch_test.go"      # same, through the CLI
  "tools/herdr-devflow/internal/overview/service_test.go"  # same, through the collector
)

function is_historical {
  # rg reports paths as ./a/b when it is given `.`; the allowlist is written
  # without that prefix, so normalize before comparing.
  local path="${1#./}" allowed
  for allowed in "${historical[@]}"; do
    [[ "$path" == "$allowed"* ]] && return 0
  done
  return 1
}

# The file itself must not come back.
if [[ -e "$repo_root/BACKLOG.md" ]]; then
  print -r -- "BACKLOG.md exists again; the backlog is GitHub Issues" >&2
  exit 1
fi

failed=0

# Anything naming the deleted file, outside the historical allowlist.
while IFS=: read -r path line text; do
  is_historical "$path" && continue
  print -r -- "$path:$line names the deleted backlog file: $text" >&2
  failed=1
done < <(rg -n --no-heading "BACKLOG\.md" \
  --glob '!node_modules' --glob '!.git' --glob '!tasks/' \
  --glob '!internal/**' --glob '!tests/**' . 2>/dev/null || true)

# Instructions to run a command that no longer exists.
while IFS=: read -r path line text; do
  is_historical "$path" && continue
  print -r -- "$path:$line documents a removed backlog command: $text" >&2
  failed=1
done < <(rg -n --no-heading "wt backlog (sync|prune|promote|ship|drop)" \
  --glob '!node_modules' --glob '!.git' --glob '!tasks/' . 2>/dev/null || true)

# The retention setting went with the file.
while IFS=: read -r path line text; do
  is_historical "$path" && continue
  print -r -- "$path:$line references the removed retention setting: $text" >&2
  failed=1
done < <(rg -n --no-heading "WT_BACKLOG_RETENTION_DAYS" \
  --glob '!node_modules' --glob '!.git' --glob '!tasks/' . 2>/dev/null || true)

# The lifecycle commits the migration exists to eliminate.
while IFS=: read -r path line text; do
  is_historical "$path" && continue
  print -r -- "$path:$line still creates or documents a docs(backlog) commit: $text" >&2
  failed=1
done < <(rg -n --no-heading 'docs\(backlog\)' \
  --glob '!node_modules' --glob '!.git' --glob '!tasks/' . 2>/dev/null || true)

if (( failed )); then
  print -r -- "" >&2
  print -r -- "Current docs and code must describe GitHub Issues as the backlog." >&2
  print -r -- "If a match is a genuine historical record, add its path to the" >&2
  print -r -- "allowlist at the top of scripts/check-backlog-docs.sh." >&2
  exit 1
fi

print -r -- "check-backlog-docs.sh: ok"
