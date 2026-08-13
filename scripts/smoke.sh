#!/usr/bin/env bash
#
# smoke.sh - manual checks against a running isolated demo server.
#
# Every worktree keeps its feature's smoke checks here, under this one stable
# name, so a single allowlist entry - Bash(./scripts/smoke.sh:*) - covers them
# all. Inline multi-step curl pipelines cannot be allowlisted: shell variables
# and $(...) substitution make the command unresolvable to the permission
# analyzer, so it prompts no matter how many rules exist. A script is one
# stable token. Put the shell in here, not in the tool call.
#
# This worktree's feature: Workspace Planning Workflow
# (tasks/prd-workspace-planning-policy.md).
#
# Usage:
#   ./scripts/smoke.sh plans <base-url> <workspace-id>
#
# Start the server it talks to with `wt demo 8931`, or by hand with the
# isolated recipe in CLAUDE.md > Smoke Testing.
#
# Exits non-zero on the first failed expectation.

set -euo pipefail

BASE_URL="${2:-http://localhost:8931}"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

expect_status() {
  local want="$1" method="$2" url="$3" body="${4:-}"
  local got
  if [[ -n "$body" ]]; then
    got=$(curl -s -o /dev/null -w '%{http_code}' -X "$method" "$url" \
      -H 'Content-Type: application/json' -d "$body")
  else
    got=$(curl -s -o /dev/null -w '%{http_code}' -X "$method" "$url")
  fi
  if [[ "$got" != "$want" ]]; then
    fail "$method $url => $got, want $want"
  fi
  echo "ok   $method $url => $got"
}

json_field() {
  python3 -c 'import sys,json
d = json.load(sys.stdin)
for key in sys.argv[1].split("."):
    if d is None:
        break
    d = d[int(key)] if key.isdigit() else d.get(key)
print(d if d is not None else "")' "$1"
}

# workspace_id reads the created-workspace id from whichever envelope the
# workspace API used (the collection endpoint nests it under "folder").
workspace_id() {
  python3 -c 'import sys,json
d = json.load(sys.stdin)
for path in (("id",), ("workspace", "id"), ("folder", "id")):
    value = d
    for key in path:
        value = value.get(key) if isinstance(value, dict) else None
    if value:
        print(value)
        break'
}

smoke_plans() {
  local ws="$1"
  [[ -n "$ws" ]] || fail "usage: smoke.sh plans <base-url> <workspace-id>"

  echo "--- Workspace Plans lifecycle ($ws) ---"

  local plan
  plan=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans" \
    -H 'Content-Type: application/json' \
    -d '{"request":"Smoke plan","actor":"smoke"}' | json_field id)
  [[ -n "$plan" ]] || fail "plan creation returned no id"
  echo "ok   created $plan"

  # The exact initiating request is retained verbatim (FR-21).
  local request
  request=$(curl -s "$BASE_URL/api/workspaces/$ws/plans/$plan" | json_field original_request)
  [[ "$request" == "Smoke plan" ]] || fail "original_request = '$request'"
  echo "ok   original request preserved"

  # Active/History split (FR-146).
  local active_count
  active_count=$(curl -s "$BASE_URL/api/workspaces/$ws/plans?scope=active" |
    python3 -c 'import sys,json;print(len(json.load(sys.stdin)["plans"]))')
  [[ "$active_count" -ge 1 ]] || fail "active plans = $active_count, want >= 1"
  echo "ok   active list contains the plan"

  expect_status 200 POST "$BASE_URL/api/workspaces/$ws/plans/$plan/archive" '{"reason":"smoke"}'

  local history_count
  history_count=$(curl -s "$BASE_URL/api/workspaces/$ws/plans?scope=history" |
    python3 -c 'import sys,json;print(len(json.load(sys.stdin)["plans"]))')
  [[ "$history_count" -ge 1 ]] || fail "history plans = $history_count, want >= 1"
  echo "ok   archived plan moved to history"

  expect_status 200 POST "$BASE_URL/api/workspaces/$ws/plans/$plan/reopen"

  # Ownership: another workspace cannot read this plan (FR-163, FR-167).
  # The name is unique per run so the script stays rerunnable against a
  # sandbox that already has one.
  local other
  other=$(curl -s -X POST "$BASE_URL/api/workspaces" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"Smoke Other $(date +%s)\"}" | workspace_id)
  [[ -n "$other" ]] || fail "could not create the second workspace"
  expect_status 404 GET "$BASE_URL/api/workspaces/$other/plans/$plan"
  echo "ok   cross-workspace read rejected"

  # Wrong verbs get an honest 405 rather than an unrelated 200.
  expect_status 405 GET "$BASE_URL/api/workspaces/$ws/plans/$plan/archive"
  expect_status 405 PUT "$BASE_URL/api/workspaces/$ws/plans"

  # A plan with no effects hard-deletes; the guard is exercised in unit tests.
  expect_status 200 DELETE "$BASE_URL/api/workspaces/$ws/plans/$plan"
  expect_status 404 GET "$BASE_URL/api/workspaces/$ws/plans/$plan"

  echo "--- Workspace Plans smoke passed ---"
}

smoke_drafting() {
  local ws="$1"
  [[ -n "$ws" ]] || fail "usage: smoke.sh drafting <base-url> <workspace-id>"

  echo "--- Plan drafting, editing, and recovery ($ws) ---"

  local plan
  plan=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans" \
    -H 'Content-Type: application/json' \
    -d '{"request":"Plan the reporting migration"}' | json_field id)
  [[ -n "$plan" ]] || fail "plan creation returned no id"
  local base="$BASE_URL/api/workspaces/$ws/plans/$plan"

  # A manual edit needs no model at all (FR-58).
  local saved
  saved=$(curl -s -X PATCH "$base/draft" -H 'Content-Type: application/json' -d '{
    "objective":"Migrate reporting safely",
    "revision":0,
    "autosave":true,
    "content":{"execution":{"mode":"step_through"},
      "groups":[{"id":"grp-1","title":"Prepare","items":[
        {"id":"itm-1","description":"Snapshot staging"},
        {"id":"itm-2","description":"Verify checksums","depends_on":["itm-1"]}]}]}
  }')
  [[ "$(echo "$saved" | json_field objective)" == "Migrate reporting safely" ]] ||
    fail "manual draft edit did not persist"
  [[ "$(echo "$saved" | json_field draft_revision)" == "1" ]] ||
    fail "draft revision did not advance"
  echo "ok   manual draft edit saved without a model"

  # A stale write is refused and carries recovery context (FR-30, FR-151).
  local conflict
  conflict=$(curl -s -X PATCH "$base/draft" -H 'Content-Type: application/json' \
    -d '{"objective":"Stale write","revision":0}')
  [[ "$(echo "$conflict" | json_field code)" == "stale_draft" ]] ||
    fail "a stale write was accepted: $conflict"
  [[ "$(echo "$conflict" | json_field details.current_revision)" == "1" ]] ||
    fail "conflict did not carry the winning revision"
  echo "ok   stale write refused with recovery context"

  # Recovery points exist and are restorable.
  local snapshot
  snapshot=$(curl -s "$base/snapshots" | json_field snapshots.0.id)
  [[ -n "$snapshot" ]] || fail "no recovery snapshot was recorded"
  expect_status 200 POST "$base/snapshots/$snapshot/recover" '{"actor":"smoke"}'
  echo "ok   autosave snapshot recovered"

  # A dangling dependency is refused even in a work-in-progress draft.
  local broken
  broken=$(curl -s -o /dev/null -w '%{http_code}' -X PATCH "$base/draft" \
    -H 'Content-Type: application/json' -d '{
      "objective":"Broken","revision":3,
      "content":{"execution":{"mode":"step_through"},
        "groups":[{"id":"grp-1","title":"Prepare","items":[
          {"id":"itm-1","description":"x","depends_on":["itm-missing"]}]}]}}')
  [[ "$broken" == "422" ]] || fail "a dangling dependency was accepted (status $broken)"
  echo "ok   dangling dependency refused"

  # Revision disclosure reports what would be replaced, without changing it.
  local disclosure
  disclosure=$(curl -s "$base/revision?section=grp-1")
  [[ -n "$(echo "$disclosure" | json_field disclosure)" ]] ||
    fail "revision disclosure returned nothing: $disclosure"
  echo "ok   revision disclosure available"

  # Generation is unavailable in this sandbox (no model configured), and that
  # must read as its own condition rather than as a failure (FR-58).
  local generate
  generate=$(curl -s -X POST "$base/draft" -H 'Content-Type: application/json' -d '{}')
  local code
  code=$(echo "$generate" | json_field code)
  if [[ "$code" == "model_unavailable" ]]; then
    echo "ok   generation reports model_unavailable distinctly"
  elif [[ -n "$(echo "$generate" | json_field id)" ]]; then
    echo "ok   generation produced a draft (a model is configured)"
  else
    fail "unexpected generate response: $generate"
  fi

  expect_status 405 GET "$base/draft"
  expect_status 200 DELETE "$base"

  echo "--- Plan drafting smoke passed ---"
}

case "${1:-}" in
plans) smoke_plans "${3:-}" ;;
drafting) smoke_drafting "${3:-}" ;;
*)
  echo "usage: $0 {plans|drafting} <base-url> <workspace-id>" >&2
  exit 2
  ;;
esac
