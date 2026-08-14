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

smoke_review() {
  local ws="$1"
  [[ -n "$ws" ]] || fail "usage: smoke.sh review <base-url> <workspace-id>"

  echo "--- Plan review and approval ($ws) ---"

  local plan
  plan=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans" \
    -H 'Content-Type: application/json' \
    -d '{"request":"Plan the reporting migration"}' | json_field id)
  [[ -n "$plan" ]] || fail "plan creation returned no id"
  local base="$BASE_URL/api/workspaces/$ws/plans/$plan"

  curl -s -X PATCH "$base/draft" -H 'Content-Type: application/json' -d '{
    "objective":"Migrate reporting safely","revision":0,
    "content":{"execution":{"mode":"step_through"},
      "groups":[{"id":"grp-1","title":"Prepare","items":[
        {"id":"itm-1","description":"Snapshot staging"},
        {"id":"itm-2","description":"Verify checksums","depends_on":["itm-1"]}]}]}}' > /dev/null

  # Snapshot an immutable version.
  local version hash
  version=$(curl -s -X POST "$base/versions" -H 'Content-Type: application/json' -d '{"actor":"smoke"}')
  local number
  number=$(echo "$version" | json_field version)
  hash=$(echo "$version" | json_field content_hash)
  [[ "$number" == "1" ]] || fail "first version number = $number"
  [[ -n "$hash" ]] || fail "version has no content hash"
  echo "ok   version 1 snapshotted with a content hash"

  # The review contract states the exact version and every effect.
  local contract label starts
  contract=$(curl -s "$base/versions/1")
  label=$(echo "$contract" | json_field action_label)
  starts=$(echo "$contract" | json_field starts_execution)
  [[ "$label" == "Approve and Create Tasks" ]] || fail "action label = '$label'"
  [[ "$starts" == "False" ]] || fail "step_through plan claims it starts execution: $starts"
  [[ "$(echo "$contract" | json_field content_hash)" == "$hash" ]] ||
    fail "contract hash does not match the version"
  echo "ok   review contract labels the action by its effect"

  # A stale hash cannot approve (FR-69).
  local stale
  stale=$(curl -s -X POST "$base/approvals" -H 'Content-Type: application/json' \
    -d "{\"version\":1,\"content_hash\":\"stale\",\"effect\":\"create_tasks\",\"idempotency_key\":\"k1\"}")
  [[ "$(echo "$stale" | json_field code)" == "approval_mismatch" ]] ||
    fail "a stale hash was accepted: $stale"
  echo "ok   stale approval refused"

  # Asking for more than the version declares is refused (FR-63).
  local overreach
  overreach=$(curl -s -X POST "$base/approvals" -H 'Content-Type: application/json' \
    -d "{\"version\":1,\"content_hash\":\"$hash\",\"effect\":\"create_tasks_and_start\",\"idempotency_key\":\"k2\"}")
  [[ "$(echo "$overreach" | json_field code)" == "approval_mismatch" ]] ||
    fail "an undeclared effect was accepted: $overreach"
  echo "ok   undeclared approval effect refused"

  # The real approval, and a retry that must replay it (FR-73).
  local first second
  first=$(curl -s -X POST "$base/approvals" -H 'Content-Type: application/json' \
    -d "{\"version\":1,\"content_hash\":\"$hash\",\"effect\":\"create_tasks\",\"user_name\":\"smoke\",\"idempotency_key\":\"k3\"}" |
    json_field id)
  [[ -n "$first" ]] || fail "approval returned no id"
  second=$(curl -s -X POST "$base/approvals" -H 'Content-Type: application/json' \
    -d "{\"version\":1,\"content_hash\":\"$hash\",\"effect\":\"create_tasks\",\"user_name\":\"smoke\",\"idempotency_key\":\"k3\"}" |
    json_field id)
  [[ "$first" == "$second" ]] || fail "a retried approval created a second record: $first vs $second"
  echo "ok   approval recorded and idempotent on retry"

  local approvals
  approvals=$(curl -s "$base/approvals" |
    python3 -c 'import sys,json;print(len(json.load(sys.stdin)["approvals"]))')
  [[ "$approvals" == "1" ]] || fail "approval history = $approvals, want 1"
  echo "ok   approval appears once in history"

  echo "--- Plan review smoke passed ---"
}

# plan_task_count reports how many of a workspace's tasks were created by ONE
# plan. It reads the workspace detail rather than a tasks endpoint, because the
# tasks ARE the workspace's tasks — there is no separate plan task store to
# query (FR-11).
#
# Counting by plan id rather than by "has plan provenance" keeps the script
# rerunnable against a sandbox that already holds tasks from an earlier plan.
plan_task_count() {
  curl -s "$BASE_URL/api/workspaces/$1" | python3 -c '
import sys, json
plan_id = sys.argv[1]
d = json.load(sys.stdin)
ws = d.get("workspace", d)
tasks = ws.get("tasks") or []
print(sum(1 for t in tasks
          if ((t.get("context") or {}).get("workspace_plan") or {}).get("plan_id") == plan_id))' "$2"
}

smoke_materialize() {
  local ws="$1"
  [[ -n "$ws" ]] || fail "usage: smoke.sh materialize <base-url> <workspace-id>"

  echo "--- Plan materialization ($ws) ---"

  local plan
  plan=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans" \
    -H 'Content-Type: application/json' \
    -d '{"request":"Plan the reporting migration"}' | json_field id)
  [[ -n "$plan" ]] || fail "plan creation returned no id"
  local base="$BASE_URL/api/workspaces/$ws/plans/$plan"

  curl -s -X PATCH "$base/draft" -H 'Content-Type: application/json' -d '{
    "objective":"Migrate reporting safely","revision":0,
    "content":{"execution":{"mode":"step_through"},
      "artifacts":[{"id":"art-1","kind":"prd","path":"tasks/prd-smoke.md","enabled":true}],
      "groups":[
        {"id":"grp-1","title":"Prepare","items":[
          {"id":"itm-1","description":"Snapshot staging"},
          {"id":"itm-2","description":"Verify checksums","depends_on":["itm-1"]}]},
        {"id":"grp-2","title":"Cut over","depends_on":["grp-1"],"items":[
          {"id":"itm-3","description":"Switch traffic"}]}]}}' > /dev/null

  local version hash
  version=$(curl -s -X POST "$base/versions" -H 'Content-Type: application/json' -d '{"actor":"smoke"}')
  hash=$(echo "$version" | json_field content_hash)
  [[ -n "$hash" ]] || fail "version has no content hash"

  local approval
  approval=$(curl -s -X POST "$base/approvals" -H 'Content-Type: application/json' \
    -d "{\"version\":1,\"content_hash\":\"$hash\",\"effect\":\"create_tasks\",\"user_name\":\"smoke\",\"idempotency_key\":\"m1\"}" |
    json_field id)
  [[ -n "$approval" ]] || fail "approval returned no id"
  echo "ok   approved version 1"

  # Materialize: three groups/items become a real task tree.
  local first
  first=$(curl -s -X POST "$base/materialize" -H 'Content-Type: application/json' \
    -d "{\"approval_id\":\"$approval\"}")
  local created
  created=$(echo "$first" | python3 -c 'import sys,json;print(len(json.load(sys.stdin).get("task_ids",[])))')
  [[ "$created" == "5" ]] || fail "materialized $created tasks, want 5 (2 groups + 3 items): $first"
  echo "ok   materialized 5 tasks"

  # The tasks are real workspace tasks, not a plan-private copy.
  local tasks
  tasks=$(plan_task_count "$ws" "$plan")
  [[ "$tasks" == "5" ]] || fail "workspace shows $tasks plan-created tasks, want 5"
  echo "ok   tasks are real workspace tasks carrying plan provenance"

  # The plan reached approved and links back to its tasks, both directions.
  local status links
  status=$(curl -s "$base" | json_field status)
  [[ "$status" == "approved" ]] || fail "plan status = $status, want approved"
  links=$(curl -s "$base" | python3 -c 'import sys,json;print(len(json.load(sys.stdin).get("task_links",[])))')
  [[ "$links" == "5" ]] || fail "plan has $links task links, want 5"
  echo "ok   plan approved with bidirectional task provenance"

  # Retrying the same approval replays rather than duplicating (FR-73, SM-2).
  local retry replayed
  retry=$(curl -s -X POST "$base/materialize" -H 'Content-Type: application/json' \
    -d "{\"approval_id\":\"$approval\"}")
  replayed=$(echo "$retry" | json_field replayed)
  [[ "$replayed" == "True" ]] || fail "the retry did not replay: $retry"
  tasks=$(plan_task_count "$ws" "$plan")
  [[ "$tasks" == "5" ]] || fail "the retry duplicated work: $tasks tasks"
  echo "ok   retried materialization replayed without duplicating"

  echo "--- Plan materialization smoke passed ---"
}

case "${1:-}" in
plans) smoke_plans "${3:-}" ;;
drafting) smoke_drafting "${3:-}" ;;
review) smoke_review "${3:-}" ;;
materialize) smoke_materialize "${3:-}" ;;
*)
  echo "usage: $0 {plans|drafting|review|materialize} <base-url> <workspace-id>" >&2
  exit 2
  ;;
esac
