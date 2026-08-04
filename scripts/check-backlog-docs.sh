#!/bin/zsh
# Fail when current documentation or executable code still instructs anyone to
# maintain the deleted BACKLOG.md file, or to reach the backlog through a
# command that no longer exists.
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
#
# Note the loop variable below is `match_path`, not `path`. In zsh `path` is the
# array tied to $PATH: reading a match into it replaces the command search path
# with a filename, and every later check silently finds nothing because `rg` can
# no longer be found. A guard that cannot fail is worse than no guard.
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$repo_root"

# Paths that may mention the old file because they are a record of the past, or
# because their whole job is asserting the file is gone.
typeset -a historical
historical=(
  "docs/feature-discovery/reports/"   # dated discovery reports, written at the time
  "scripts/check-backlog-docs.sh"     # this file
  "scripts/wt-backlog.test.sh"        # asserts the file, its commands, and `wt backlog` are gone
  "scripts/wt-herd.test.sh"           # asserts no backlog commit is ever created
  "tools/herdr-devflow/internal/overview/types.go"        # schema-version note
  "tools/herdr-devflow/internal/overview/sanitize_test.go" # writes one, proves it is ignored
  "tools/herdr-devflow/internal/app/dispatch_test.go"      # same, through the CLI
  "tools/herdr-devflow/internal/overview/service_test.go"  # same, through the collector
)

function is_historical {
  # rg reports paths as ./a/b when it is given `.`; the allowlist is written
  # without that prefix, so normalize before comparing.
  local candidate="${1#./}" allowed
  for allowed in "${historical[@]}"; do
    [[ "$candidate" == "$allowed"* ]] && return 0
  done
  return 1
}

# Every check below is a search. Without the searcher this script would report a
# clean repository it never actually read, so refuse to run rather than lie.
if ! command -v rg > /dev/null 2>&1; then
  print -r -- "rg is not on PATH; this check cannot verify anything" >&2
  exit 1
fi

# The file itself must not come back.
if [[ -e "$repo_root/BACKLOG.md" ]]; then
  print -r -- "BACKLOG.md exists again; the backlog is GitHub Issues" >&2
  exit 1
fi

failed=0

# Anything naming the deleted file, outside the historical allowlist.
while IFS=: read -r match_path line text; do
  is_historical "$match_path" && continue
  print -r -- "$match_path:$line names the deleted backlog file: $text" >&2
  failed=1
done < <(rg -n --no-heading "BACKLOG\.md" \
  --glob '!node_modules' --glob '!.git' --glob '!tasks/' \
  --glob '!internal/**' --glob '!tests/**' . 2>/dev/null || true)

# Instructions to run a command that no longer exists.
while IFS=: read -r match_path line text; do
  is_historical "$match_path" && continue
  print -r -- "$match_path:$line documents a removed backlog command: $text" >&2
  failed=1
done < <(rg -n --no-heading "(wt backlog|backlog\.sh) (sync|prune|promote|ship|drop)" \
  --glob '!node_modules' --glob '!.git' --glob '!tasks/' . 2>/dev/null || true)

# The retention setting went with the file.
while IFS=: read -r match_path line text; do
  is_historical "$match_path" && continue
  print -r -- "$match_path:$line references the removed retention setting: $text" >&2
  failed=1
done < <(rg -n --no-heading "WT_BACKLOG_RETENTION_DAYS" \
  --glob '!node_modules' --glob '!.git' --glob '!tasks/' . 2>/dev/null || true)

# Instructions to reach the backlog through `wt`. It is ./scripts/backlog.sh now:
# its own executable, so an agent — which cannot source a zsh function — can run
# it. A doc still saying `wt backlog` sends a reader to a signpost.
#
# The signposts themselves are the exception. They name the command they replace
# because that is their entire job, so a line that also names the replacement is
# a correction rather than an instruction.
while IFS=: read -r match_path line text; do
  is_historical "$match_path" && continue
  [[ "$text" == *"moved to ./scripts/backlog.sh"* ]] && continue
  print -r -- "$match_path:$line tells a reader to run wt backlog: $text" >&2
  failed=1
done < <(rg -n --no-heading "wt backlog" \
  --glob '!node_modules' --glob '!.git' --glob '!tasks/' . 2>/dev/null || true)

# Same for `wt merge`, which was removed because it merged straight into the dev
# worktree, bypassing PR review and the checks dev requires.
while IFS=: read -r match_path line text; do
  is_historical "$match_path" && continue
  [[ "$text" == *"wt merge was removed"* ]] && continue
  print -r -- "$match_path:$line tells a reader to run the removed wt merge: $text" >&2
  failed=1
done < <(rg -n --no-heading "wt merge" \
  --glob '!node_modules' --glob '!.git' --glob '!tasks/' . 2>/dev/null || true)

# The lifecycle commits the migration exists to eliminate.
while IFS=: read -r match_path line text; do
  is_historical "$match_path" && continue
  print -r -- "$match_path:$line still creates or documents a docs(backlog) commit: $text" >&2
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
