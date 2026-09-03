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

workspace_slug() {
  python3 -c 'import sys,json
d = json.load(sys.stdin)
for path in (("folder_slug",), ("workspace", "folder_slug"), ("folder", "folder_slug")):
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

smoke_execution() {
  local ws="$1"
  [[ -n "$ws" ]] || fail "usage: smoke.sh execution <base-url> <workspace-id>"

  echo "--- Plan execution ($ws) ---"

  local plan
  plan=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans" \
    -H 'Content-Type: application/json' \
    -d '{"request":"Plan the execution demo"}' | json_field id)
  [[ -n "$plan" ]] || fail "plan creation returned no id"
  local base="$BASE_URL/api/workspaces/$ws/plans/$plan"

  curl -s -X PATCH "$base/draft" -H 'Content-Type: application/json' -d '{
    "objective":"Run the demo safely","revision":0,
    "content":{"execution":{"mode":"step_through"},
      "groups":[{"id":"grp-1","title":"Prepare","items":[
        {"id":"itm-1","description":"First step"},
        {"id":"itm-2","description":"Second step","depends_on":["itm-1"]}]}]}}' > /dev/null

  local hash approval
  hash=$(curl -s -X POST "$base/versions" -H 'Content-Type: application/json' -d '{}' | json_field content_hash)
  approval=$(curl -s -X POST "$base/approvals" -H 'Content-Type: application/json' \
    -d "{\"version\":1,\"content_hash\":\"$hash\",\"effect\":\"create_tasks\",\"user_name\":\"smoke\",\"idempotency_key\":\"e1\"}" |
    json_field id)
  curl -s -X POST "$base/materialize" -H 'Content-Type: application/json' \
    -d "{\"approval_id\":\"$approval\"}" > /dev/null

  # Approval created tasks and started nothing (FR-102).
  local status progress
  status=$(curl -s "$base" | json_field status)
  [[ "$status" == "approved" ]] || fail "status after materialize = $status, want approved"
  progress=$(curl -s "$base" | json_field progress.running)
  [[ "$progress" == "0" ]] || fail "step_through started $progress task(s) on approval"
  echo "ok   approval created tasks and started nothing"

  # Progress is derived from real tasks: one ready, one blocked behind it.
  local ready blocked
  ready=$(curl -s "$base" | json_field progress.ready)
  blocked=$(curl -s "$base" | json_field progress.blocked)
  [[ "$ready" == "1" && "$blocked" == "1" ]] ||
    fail "derived progress = ready:$ready blocked:$blocked, want 1/1"
  echo "ok   progress derived from real task state (1 ready, 1 blocked)"

  # Pause and resume.
  expect_status 200 POST "$base/execution" '{"action":"pause","reason":"checking something"}'
  status=$(curl -s "$base" | json_field status)
  [[ "$status" == "paused" ]] || fail "status after pause = $status"
  expect_status 200 POST "$base/execution" '{"action":"resume"}'
  echo "ok   paused and resumed"

  # A cancel preview names the affected work before it happens (FR-154).
  local queued
  queued=$(curl -s "$base/cancel-preview" |
    python3 -c 'import sys,json;print(len(json.load(sys.stdin).get("queued",[])))')
  [[ "$queued" == "2" ]] || fail "cancel preview lists $queued queued task(s), want 2"
  echo "ok   cancel preview names affected work"

  # Skipping approved work requires a reason (FR-115).
  local taskid
  taskid=$(curl -s "$base" | python3 -c '
import sys, json
d = json.load(sys.stdin)
for link in d.get("task_links", []):
    if link.get("role") == "item":
        print(link["task_id"])
        break')
  [[ -n "$taskid" ]] || fail "no item task link found"
  local noreason
  noreason=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$base/execution" \
    -H 'Content-Type: application/json' -d "{\"action\":\"skip\",\"task_id\":\"$taskid\"}")
  [[ "$noreason" == "422" ]] || fail "skipping without a reason returned $noreason, want 422"
  echo "ok   skipping approved work requires a reason"

  # The reverse lookup answers "which plan produced this task?" (FR-10).
  local related
  related=$(curl -s "$BASE_URL/api/workspaces/$ws/plan-for-task/$taskid")
  [[ "$(echo "$related" | json_field plan_id)" == "$plan" ]] ||
    fail "reverse lookup did not resolve the plan: $related"
  [[ -z "$(echo "$related" | json_field url)" ]] ||
    fail "UUID-scoped reverse lookup returned a browser URL: $related"
  echo "ok   task resolves back to its plan without manufacturing a UUID page route"

  # Cancelling stops the plan and leaves history alone (FR-112).
  expect_status 200 POST "$base/execution" '{"action":"cancel","reason":"demo over"}'
  status=$(curl -s "$base" | json_field status)
  [[ "$status" == "cancelled" ]] || fail "status after cancel = $status"
  echo "ok   cancelled"

  echo "--- Plan execution smoke passed ---"
}

# seed_demo creates one workspace holding a plan in each state worth LOOKING at,
# then prints the URLs. It exists for human browser verification: the automated
# checks above prove behaviour, but nobody has seen these pages render.
seed_demo() {
  echo "--- Seeding plans for browser review ---"

  # Confirm the workspace root so created workspaces reach the database rather
  # than sitting in staging (a fresh sandbox starts unconfirmed).
  curl -s -X POST "$BASE_URL/api/settings/workspace-root" \
    -H 'Content-Type: application/json' \
    -d "{\"workspace_root\":\"$HOME/Ori Workspaces\"}" > /dev/null

  local created ws slug
  created=$(curl -s -X POST "$BASE_URL/api/workspaces" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"Plan Review $(date +%H%M%S)\"}")
  ws=$(echo "$created" | workspace_id)
  slug=$(echo "$created" | workspace_slug)
  [[ -n "$ws" && -n "$slug" ]] || fail "could not create a workspace with route identity"

  # Structured planning on, so the create panel shows.
  curl -s -X PATCH "$BASE_URL/api/workspaces/$ws/settings" \
    -H 'Content-Type: application/json' -d '{"planning":{"enabled":true}}' > /dev/null

  # 1. A draft with a real task tree and a dependency — the editor surface.
  local draft
  draft=$(seed_plan "$ws" "Migrate the reporting database" "step_through")
  echo "  draft plan:      $draft"

  # 2. A plan in review — the approval contract, step-through labelling.
  local review
  review=$(seed_plan "$ws" "Add audit logging to the billing service" "step_through")
  curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans/$review/versions" \
    -H 'Content-Type: application/json' -d '{"actor":"demo"}' > /dev/null
  echo "  in review:       $review"

  # 3. An AUTO plan in review — this one's button must read "Approve and Start"
  #    and warn that work begins.
  local auto
  auto=$(seed_plan "$ws" "Nightly index rebuild" "auto")
  curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans/$auto/versions" \
    -H 'Content-Type: application/json' -d '{"actor":"demo"}' > /dev/null
  echo "  in review (auto): $auto"

  # 4. An approved + materialized plan — created work, task links, provenance.
  local approved hash approval
  approved=$(seed_plan "$ws" "Publish the Q3 status report" "step_through")
  hash=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans/$approved/versions" \
    -H 'Content-Type: application/json' -d '{"actor":"demo"}' | json_field content_hash)
  approval=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans/$approved/approvals" \
    -H 'Content-Type: application/json' \
    -d "{\"version\":1,\"content_hash\":\"$hash\",\"effect\":\"create_tasks\",\"user_name\":\"demo\",\"idempotency_key\":\"seed\"}" |
    json_field id)
  curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans/$approved/materialize" \
    -H 'Content-Type: application/json' -d "{\"approval_id\":\"$approval\"}" > /dev/null
  echo "  approved:        $approved"

  # 5. An archived plan — the History section.
  local archived
  archived=$(seed_plan "$ws" "Retire the legacy export job" "step_through")
  curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans/$archived/archive" \
    -H 'Content-Type: application/json' -d '{"reason":"superseded"}' > /dev/null
  echo "  archived:        $archived"

  echo
  echo "Open these:"
  echo "  Plans list      $BASE_URL/workspaces/$slug/plans"
  echo "  Draft (editor)  $BASE_URL/workspaces/$slug/plans/$draft"
  echo "  In review       $BASE_URL/workspaces/$slug/plans/$review"
  echo "  In review (auto)$BASE_URL/workspaces/$slug/plans/$auto"
  echo "  Approved        $BASE_URL/workspaces/$slug/plans/$approved"
  echo
}

# seed_plan creates one plan with a two-task group and a dependency between the
# tasks, so the editor has something with structure to render.
#
# The fourth argument assigns every item to one agent. Leave it empty to seed
# unassigned work: that is a legitimate plan, but it cannot be started, because
# an unassigned step is a capability gate rather than something to dispatch.
seed_plan() {
  local ws="$1" request="$2" mode="$3" assignee="${4:-}"
  local plan
  plan=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans" \
    -H 'Content-Type: application/json' \
    -d "{\"request\":\"$request\"}" | json_field id)
  curl -s -X PATCH "$BASE_URL/api/workspaces/$ws/plans/$plan/draft" \
    -H 'Content-Type: application/json' -d "{
      \"objective\":\"$request\",\"revision\":0,
      \"content\":{\"execution\":{\"mode\":\"$mode\"},
        \"in_scope\":[\"the reporting tables\"],
        \"non_goals\":[\"anything touching billing\"],
        \"groups\":[
          {\"id\":\"grp-1\",\"title\":\"Prepare\",\"outcome\":\"A verified copy exists\",\"items\":[
            {\"id\":\"itm-1\",\"description\":\"Snapshot the current state\",\"assignee\":\"$assignee\",\"expected_result\":\"Checksums match\"},
            {\"id\":\"itm-2\",\"description\":\"Verify the snapshot\",\"assignee\":\"$assignee\",\"depends_on\":[\"itm-1\"]}]},
          {\"id\":\"grp-2\",\"title\":\"Cut over\",\"depends_on\":[\"grp-1\"],\"items\":[
            {\"id\":\"itm-3\",\"description\":\"Switch traffic to the new path\",\"assignee\":\"$assignee\"}]}]}}" > /dev/null
  echo "$plan"
}

# ensure_agent puts one agent in the workspace, so plan items have somebody to
# be assigned to. Both calls are idempotent enough to rerun.
ensure_agent() {
  local ws="$1" name="$2"
  curl -s -X POST "$BASE_URL/api/agents" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$name\"}" > /dev/null
  curl -s -X POST "$BASE_URL/api/workspaces/$ws/agents" -H 'Content-Type: application/json' \
    -d "{\"agent_name\":\"$name\"}" > /dev/null
}

# version_hash reads the content hash of one version BY NUMBER.
#
# Indexing the version list by position is a trap: it is ordered oldest-first,
# so v[0] is version 1 forever. Approving with the wrong hash is refused, and
# the refusal is easy to miss if the caller does not check.
version_hash() {
  local base="$1" number="$2"
  curl -s "$base/versions" | python3 -c 'import sys,json
want = int(sys.argv[1])
for version in json.load(sys.stdin)["versions"]:
    if version["version"] == want:
        print(version["content_hash"])
        break' "$number"
}

# approve_version approves one exact version and fails loudly if the approval
# was refused, so a later step never runs against work that was never approved.
approve_version() {
  local base="$1" number="$2" key="$3"
  local hash response approval
  hash=$(version_hash "$base" "$number")
  [[ -n "$hash" ]] || fail "no content hash for version $number"
  response=$(curl -s -X POST "$base/approvals" -H 'Content-Type: application/json' \
    -d "{\"version\":$number,\"content_hash\":\"$hash\",\"effect\":\"create_tasks\",\"user_name\":\"smoke\",\"idempotency_key\":\"$key\"}")
  approval=$(echo "$response" | json_field id)
  [[ -n "$approval" ]] || fail "approving version $number was refused: $response"
  echo "$approval"
}

# approve_and_materialize drives a plan from draft to created tasks, returning
# nothing. Used by the slot smoke, which needs two ready-to-run plans.
approve_and_materialize() {
  local ws="$1" plan="$2" key="$3"
  local base="$BASE_URL/api/workspaces/$ws/plans/$plan"
  local hash approval
  hash=$(curl -s -X POST "$base/versions" -H 'Content-Type: application/json' -d '{}' | json_field content_hash)
  approval=$(curl -s -X POST "$base/approvals" -H 'Content-Type: application/json' \
    -d "{\"version\":1,\"content_hash\":\"$hash\",\"effect\":\"create_tasks\",\"user_name\":\"smoke\",\"idempotency_key\":\"$key\"}" |
    json_field id)
  curl -s -X POST "$base/materialize" -H 'Content-Type: application/json' \
    -d "{\"approval_id\":\"$approval\"}" > /dev/null
}

smoke_slot() {
  local ws="$1"
  [[ -n "$ws" ]] || fail "usage: smoke.sh slot <base-url> <workspace-id>"

  echo "--- Workspace execution slot ($ws) ---"

  # Two approved plans in ONE workspace, both with runnable work.
  ensure_agent "$ws" "builder"
  local first second
  first=$(seed_plan "$ws" "Slot demo: first plan" "step_through" "builder")
  second=$(seed_plan "$ws" "Slot demo: second plan" "step_through" "builder")
  approve_and_materialize "$ws" "$first" "slot-1"
  approve_and_materialize "$ws" "$second" "slot-2"
  echo "ok   two approved plans in one workspace"

  local firstBase="$BASE_URL/api/workspaces/$ws/plans/$first"
  local secondBase="$BASE_URL/api/workspaces/$ws/plans/$second"

  # The first start takes the slot. Dispatch may fail (the demo sandbox has no
  # agent), but the slot claim is what this checks.
  curl -s -X POST "$firstBase/execution" -H 'Content-Type: application/json' \
    -d '{"action":"start","actor":"smoke"}' > /dev/null

  local holder
  holder=$(curl -s "$BASE_URL/api/workspaces/$ws/plan-execution-slot" | json_field executing_plan)
  [[ "$holder" == "$first" ]] || fail "slot holder = '$holder', want the first plan"
  echo "ok   the first plan holds the workspace slot"

  # The second start does NOT run; it queues, and says what it waits behind.
  local queued reason
  queued=$(curl -s -X POST "$secondBase/execution" -H 'Content-Type: application/json' \
    -d '{"action":"start","actor":"smoke"}')
  [[ "$(echo "$queued" | json_field started)" == "False" ]] ||
    fail "a second plan started in the same workspace: $queued"
  reason=$(echo "$queued" | json_field reason)
  case "$reason" in
  *"another plan is executing"*) ;;
  *) fail "waiting reason = '$reason'" ;;
  esac
  echo "ok   the second plan waits visibly ($reason)"

  # The queue is inspectable.
  local depth
  depth=$(curl -s "$BASE_URL/api/workspaces/$ws/plan-execution-slot" | json_field queue_length)
  [[ "$depth" == "1" ]] || fail "queue length = $depth, want 1"
  echo "ok   queue depth reported"

  # Pausing with nothing in flight releases the slot to the waiting plan.
  expect_status 200 POST "$firstBase/execution" '{"action":"pause","reason":"handing over"}'
  curl -s -X POST "$secondBase/execution" -H 'Content-Type: application/json' \
    -d '{"action":"start","actor":"smoke"}' > /dev/null
  holder=$(curl -s "$BASE_URL/api/workspaces/$ws/plan-execution-slot" | json_field executing_plan)
  [[ "$holder" == "$second" ]] || fail "after handover the holder = '$holder', want the second plan"
  echo "ok   pausing handed the slot to the waiting plan"

  # Resuming rejoins the queue rather than displacing the new holder.
  expect_status 200 POST "$firstBase/execution" '{"action":"resume"}'
  holder=$(curl -s "$BASE_URL/api/workspaces/$ws/plan-execution-slot" | json_field executing_plan)
  [[ "$holder" == "$second" ]] || fail "resuming displaced the holder: '$holder'"
  echo "ok   resuming rejoined the queue without displacing the holder"

  # A standalone Task — one no Plan materialized — is untouched by all of this.
  # The Plan slot sits ABOVE the Task executor, so an unrelated task neither
  # takes the slot nor joins the queue behind it (FR-100).
  local beforeQueue standalone afterHolder afterQueue
  beforeQueue=$(curl -s "$BASE_URL/api/workspaces/$ws/plan-execution-slot" | json_field queue_length)
  standalone=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/tasks" \
    -H 'Content-Type: application/json' \
    -d '{"description":"Unrelated standalone task","to":"builder","from":"smoke"}' |
    json_field task.id)
  [[ -n "$standalone" ]] || fail "could not create a standalone task"

  afterHolder=$(curl -s "$BASE_URL/api/workspaces/$ws/plan-execution-slot" | json_field executing_plan)
  afterQueue=$(curl -s "$BASE_URL/api/workspaces/$ws/plan-execution-slot" | json_field queue_length)
  [[ "$afterHolder" == "$second" ]] || fail "a standalone task disturbed the slot holder: '$afterHolder'"
  [[ "$afterQueue" == "$beforeQueue" ]] ||
    fail "a standalone task joined the plan queue ($beforeQueue -> $afterQueue)"
  echo "ok   a standalone task runs outside plan arbitration"

  # Cancelling the holder frees the slot.
  expect_status 200 POST "$secondBase/execution" '{"action":"cancel","reason":"demo over"}'
  local free
  free=$(curl -s "$BASE_URL/api/workspaces/$ws/plan-execution-slot" | json_field slot_available)
  [[ "$free" == "True" ]] || fail "the slot was not freed by cancelling the holder"
  echo "ok   cancelling the holder freed the slot"

  echo "--- Execution slot smoke passed ---"
}

smoke_reconcile() {
  local ws="$1"
  [[ -n "$ws" ]] || fail "usage: smoke.sh reconcile <base-url> <workspace-id>"

  echo "--- Revision reconciliation ($ws) ---"
  ensure_agent "$ws" "builder"

  local plan base
  plan=$(seed_plan "$ws" "Reconcile demo" "step_through" "builder")
  base="$BASE_URL/api/workspaces/$ws/plans/$plan"
  approve_and_materialize "$ws" "$plan" "reconcile-v1"

  local firstTasks
  firstTasks=$(curl -s "$base" | python3 -c 'import sys,json;print(len(json.load(sys.stdin).get("task_links") or []))')
  [[ "$firstTasks" -gt 0 ]] || fail "version 1 created no task links"
  echo "ok   version 1 materialized ($firstTasks links)"

  # --- Additive revision: adds one step, disturbs nothing -------------------
  curl -s -X POST "$base/revise-approved" -H 'Content-Type: application/json' \
    -d '{"intent":"additive","actor":"smoke"}' > /dev/null
  local revision
  revision=$(curl -s "$base" | json_field draft_revision)
  curl -s -X PATCH "$base/draft" -H 'Content-Type: application/json' -d "{
    \"objective\":\"Reconcile demo\",\"revision\":$revision,
    \"content\":{\"execution\":{\"mode\":\"step_through\"},
      \"groups\":[
        {\"id\":\"grp-1\",\"title\":\"Prepare\",\"outcome\":\"A verified copy exists\",\"items\":[
          {\"id\":\"itm-1\",\"description\":\"Snapshot the current state\",\"assignee\":\"builder\"},
          {\"id\":\"itm-2\",\"description\":\"Verify the snapshot\",\"assignee\":\"builder\",\"depends_on\":[\"itm-1\"]},
          {\"id\":\"itm-4\",\"description\":\"Publish the checksum report\",\"assignee\":\"builder\"}]},
        {\"id\":\"grp-2\",\"title\":\"Cut over\",\"depends_on\":[\"grp-1\"],\"items\":[
          {\"id\":\"itm-3\",\"description\":\"Switch traffic to the new path\",\"assignee\":\"builder\"}]}]}}" > /dev/null
  curl -s -X POST "$base/versions" -H 'Content-Type: application/json' -d '{"intent":"additive"}' > /dev/null

  local preview needsConfirm created cancelled
  preview=$(curl -s "$base/reconcile")
  needsConfirm=$(echo "$preview" | json_field requires_confirmation)
  [[ "$needsConfirm" == "False" ]] || fail "an additive revision demanded a confirmation"
  created=$(echo "$preview" | json_field summary.created)
  cancelled=$(echo "$preview" | json_field summary.cancel)
  [[ "$created" == "1" ]] || fail "additive preview created = $created, want 1"
  [[ -z "$cancelled" || "$cancelled" == "0" ]] || fail "additive preview would cancel $cancelled task(s)"
  echo "ok   additive preview: adds 1, cancels nothing, needs no confirmation"

  # --- Corrective revision: drops an unstarted step -------------------------
  # Approve the additive version first so the corrective one revises it.
  local approval
  approval=$(approve_version "$base" 2 "reconcile-v2")
  expect_status 200 POST "$base/materialize" "{\"approval_id\":\"$approval\"}"

  local approved
  approved=$(curl -s "$base" | json_field approved_version)
  [[ "$approved" == "2" ]] || fail "approved version = $approved after materializing v2"
  echo "ok   additive revision materialized and is now the approved version"

  curl -s -X POST "$base/revise-approved" -H 'Content-Type: application/json' \
    -d '{"intent":"corrective","actor":"smoke"}' > /dev/null
  revision=$(curl -s "$base" | json_field draft_revision)
  curl -s -X PATCH "$base/draft" -H 'Content-Type: application/json' -d "{
    \"objective\":\"Reconcile demo\",\"revision\":$revision,
    \"content\":{\"execution\":{\"mode\":\"step_through\"},
      \"groups\":[
        {\"id\":\"grp-1\",\"title\":\"Prepare\",\"outcome\":\"A verified copy exists\",\"items\":[
          {\"id\":\"itm-1\",\"description\":\"Snapshot the current state\",\"assignee\":\"builder\"},
          {\"id\":\"itm-2\",\"description\":\"Verify the snapshot\",\"assignee\":\"builder\",\"depends_on\":[\"itm-1\"]}]},
        {\"id\":\"grp-2\",\"title\":\"Cut over\",\"depends_on\":[\"grp-1\"],\"items\":[
          {\"id\":\"itm-3\",\"description\":\"Switch traffic to the new path\",\"assignee\":\"builder\"}]}]}}" > /dev/null
  curl -s -X POST "$base/versions" -H 'Content-Type: application/json' -d '{"intent":"corrective"}' > /dev/null

  preview=$(curl -s "$base/reconcile")
  needsConfirm=$(echo "$preview" | json_field requires_confirmation)
  [[ "$needsConfirm" == "True" ]] || fail "a corrective revision did not require a confirmation: $preview"
  cancelled=$(echo "$preview" | json_field summary.cancel)
  [[ "$cancelled" == "1" ]] || fail "corrective preview cancel = $cancelled, want the dropped step"
  echo "ok   corrective preview: 1 unstarted step to cancel, confirmation required"

  # A confirmation must name the exact preview it accepts.
  local bad
  bad=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$base/reconcile" \
    -H 'Content-Type: application/json' -d '{"token":"invented"}')
  [[ "$bad" == "409" ]] || fail "an invented token was accepted (status $bad)"
  echo "ok   an invented confirmation token is refused"

  local token
  token=$(echo "$preview" | json_field token)
  expect_status 200 POST "$base/reconcile" "{\"token\":\"$token\",\"actor\":\"smoke\"}"

  approval=$(approve_version "$base" 3 "reconcile-v3")
  expect_status 200 POST "$base/materialize" "{\"approval_id\":\"$approval\"}"

  # The dropped work is cancelled, not deleted, and its link is retired.
  local retired
  retired=$(curl -s "$base" | python3 -c 'import sys,json
links = json.load(sys.stdin).get("task_links") or []
print(sum(1 for l in links if l.get("retired_at")))')
  [[ "$retired" -ge 1 ]] || fail "no task link was retired by the corrective revision"
  echo "ok   the superseded task link is retired, not deleted"

  echo "--- Reconciliation smoke passed ---"
}

smoke_policy() {
  local ws="$1"
  [[ -n "$ws" ]] || fail "usage: smoke.sh policy <base-url> <workspace-id>"

  echo "--- Planning policy ($ws) ---"
  local base="$BASE_URL/api/workspaces/$ws/planning-policy"

  # The two halves are separate groups on the wire.
  local payload
  payload=$(curl -s "$base")
  [[ -n "$(echo "$payload" | json_field policy.guidance.style)" ]] ||
    fail "policy has no guidance style: $payload"
  [[ -n "$(echo "$payload" | json_field policy.enforced.0.key)" ]] ||
    fail "policy has no enforced controls: $payload"
  echo "ok   guidance and enforcement are separate groups"

  # Approval is enforced and no preset turns it off.
  local approval
  approval=$(curl -s "$base" | python3 -c 'import sys,json
for c in json.load(sys.stdin)["policy"]["enforced"]:
    if c["key"] == "plan_approval":
        print("enabled" if (c["enabled"] and c["available"]) else "off")')
  [[ "$approval" == "enabled" ]] || fail "plan approval is not enforced by default"
  echo "ok   plan approval is enforced"

  # Outside a repository, branch enforcement reports itself unavailable WITH a
  # machine-readable reason rather than silently claiming to work.
  local branch
  branch=$(curl -s "$base?preset=planner" | python3 -c 'import sys,json
for c in json.load(sys.stdin)["policy"]["enforced"]:
    if c["key"] == "safe_branch":
        print("%s|%s|%s" % (c["available"], c.get("reason",""), bool(c.get("detail"))))')
  case "$branch" in
  "False|not_a_repository|True" | "False|no_workspace_folder|True") ;;
  *) fail "branch enforcement outside a repo reported: $branch" ;;
  esac
  echo "ok   branch enforcement is unavailable outside a repository, with a reason"

  # Autonomous selects automatic execution and KEEPS approval.
  local auto
  auto=$(curl -s "$base?preset=autonomous" | python3 -c 'import sys,json
p = json.load(sys.stdin)["policy"]
controls = {c["key"]: c for c in p["enforced"]}
approval = controls["plan_approval"]
mode = controls["execution_mode"]
print("%s|%s" % (approval["enabled"] and approval["available"], "starts automatically" in mode["description"]))')
  [[ "$auto" == "True|True" ]] ||
    fail "Autonomous preview reported approval/automatic = $auto"
  echo "ok   Autonomous starts automatically and still requires approval"

  # Previewing a preset does not save it.
  local savedPreset
  savedPreset=$(curl -s "$BASE_URL/api/workspaces/$ws/settings" | json_field settings.preset)
  [[ "$savedPreset" != "autonomous" ]] ||
    fail "previewing a preset saved it"
  echo "ok   previewing a preset changed nothing"

  echo "--- Planning policy smoke passed ---"
}

smoke_boundary() {
  local ws="$1"
  [[ -n "$ws" ]] || fail "usage: smoke.sh boundary <base-url> <workspace-id>"

  echo "--- Planning entry-point boundary ($ws) ---"

  # A durable Plan created from a request is reviewable at the canonical route,
  # and it is a DRAFT: nothing was approved by proposing it.
  local plan status
  plan=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans" \
    -H 'Content-Type: application/json' \
    -d '{"request":"Migrate reporting across three services","source":"chat"}' | json_field id)
  [[ -n "$plan" ]] || fail "no plan was created"
  status=$(curl -s "$BASE_URL/api/workspaces/$ws/plans/$plan" | json_field status)
  [[ "$status" == "draft" ]] || fail "a proposed plan arrived as '$status', want draft"
  echo "ok   a proposed plan lands as a draft, approving nothing"

  # Its origin records that chat asked for it, so the audit trail says where
  # the work came from.
  local origin
  origin=$(curl -s "$BASE_URL/api/workspaces/$ws/plans/$plan" | json_field origin.kind)
  [[ "$origin" == "chat" ]] || fail "origin kind = '$origin', want chat"
  echo "ok   the plan records that chat opened it"

  # The canonical route serves it. One surface, one link.
  expect_status 200 GET "$BASE_URL/api/workspaces/$ws/plans/$plan"

  # Approval is the only path to tasks: an unapproved plan materializes nothing.
  local materialized
  materialized=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    "$BASE_URL/api/workspaces/$ws/plans/$plan/materialize" \
    -H 'Content-Type: application/json' -d '{"approval_id":"made-up"}')
  [[ "$materialized" != "200" ]] || fail "an invented approval created work"
  echo "ok   work cannot be created without a real approval (status $materialized)"

  # Dynamic-agent approval no longer resumes execution.
  local resume
  resume=$(curl -s -X POST "$BASE_URL/api/orchestration/dynamic-agents/approve" \
    -H 'Content-Type: application/json' \
    -d "{\"workspace_id\":\"$ws\",\"request_id\":\"nope\",\"approve\":true}")
  case "$resume" in
  *'"resume_result":null'* | *"not found"* | *"error"*) ;;
  *) fail "dynamic-agent approval returned a resume result: $resume" ;;
  esac
  echo "ok   approving a dynamic agent does not resume execution"

  echo "--- Planning boundary smoke passed ---"
}

smoke_hardening() {
  local ws="$1"
  [[ -n "$ws" ]] || fail "usage: smoke.sh hardening <base-url> <workspace-id>"

  echo "--- Lifecycle hardening ($ws) ---"

  # Diagnostics: a read-only capability report with no plan content in it.
  local diag
  diag=$(curl -s "$BASE_URL/api/workspaces/$ws/plan-diagnostics")
  [[ "$(echo "$diag" | json_field components.materializer)" == "True" ]] ||
    fail "diagnostics report materialization as unwired: $diag"
  [[ "$(echo "$diag" | json_field components.execution_slot)" == "True" ]] ||
    fail "diagnostics report the execution slot as unwired"
  [[ -n "$(echo "$diag" | json_field limits.max_versions)" ]] ||
    fail "diagnostics carry no limits"
  echo "ok   diagnostics report every wired component and the limits"

  # Reading and editing work without a model. Only generation needs one.
  local offline
  offline=$(echo "$diag" | python3 -c 'import sys,json;print(",".join(json.load(sys.stdin)["offline_capable"]))')
  case "$offline" in
  *approve*) ;;
  *) fail "approval is not listed as usable offline: $offline" ;;
  esac
  echo "ok   approval and execution are usable without a model"

  # A credential in plan content is REFUSED, not stored and redacted later.
  local plan rejected
  plan=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans" \
    -H 'Content-Type: application/json' -d '{"request":"Hardening demo"}' | json_field id)
  rejected=$(curl -s -o /dev/null -w '%{http_code}' -X PATCH \
    "$BASE_URL/api/workspaces/$ws/plans/$plan/draft" \
    -H 'Content-Type: application/json' -d '{
      "objective":"Deploy the service","revision":0,
      "content":{"execution":{"mode":"step_through"},
        "groups":[{"id":"grp-1","title":"Deploy","items":[
          {"id":"itm-1","description":"Use sk-abcdefghijklmnopqrstuvwxyz012345 for the API"}]}]}}')
  [[ "$rejected" == "422" ]] || fail "content carrying an API key was accepted (status $rejected)"
  echo "ok   content carrying a credential is refused"

  # The same text WITHOUT a credential shape is fine — the check matches
  # shapes, not words.
  expect_status 200 PATCH "$BASE_URL/api/workspaces/$ws/plans/$plan/draft" '{
    "objective":"Deploy the service","revision":0,
    "content":{"execution":{"mode":"step_through"},
      "groups":[{"id":"grp-1","title":"Deploy","items":[
        {"id":"itm-1","description":"Rotate the API key and the database password"}]}]}}'
  echo "ok   ordinary text mentioning credentials is accepted"

  # Cancelling moves a plan to History immediately, without losing it.
  local hash approval cancelled archived
  hash=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans/$plan/versions" \
    -H 'Content-Type: application/json' -d '{}' | json_field content_hash)
  approval=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans/$plan/approvals" \
    -H 'Content-Type: application/json' \
    -d "{\"version\":1,\"content_hash\":\"$hash\",\"effect\":\"create_tasks\",\"user_name\":\"smoke\",\"idempotency_key\":\"harden-1\"}" |
    json_field id)
  curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans/$plan/materialize" \
    -H 'Content-Type: application/json' -d "{\"approval_id\":\"$approval\"}" > /dev/null
  cancelled=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans/$plan/execution" \
    -H 'Content-Type: application/json' -d '{"action":"cancel","reason":"demo"}')
  [[ "$(echo "$cancelled" | json_field status)" == "cancelled" ]] ||
    fail "cancel did not cancel: $cancelled"

  archived=$(curl -s "$BASE_URL/api/workspaces/$ws/plans/$plan" | json_field archived_at)
  [[ -n "$archived" ]] || fail "a cancelled plan stayed in the active list"
  echo "ok   cancelling moves the plan to history immediately"

  # And it is still readable, with its versions and approvals intact.
  local versions
  versions=$(curl -s "$BASE_URL/api/workspaces/$ws/plans/$plan/versions" |
    python3 -c 'import sys,json;print(len(json.load(sys.stdin)["versions"]))')
  [[ "$versions" -ge 1 ]] || fail "archiving lost the plan's versions"
  echo "ok   history keeps every version and approval"

  echo "--- Hardening smoke passed ---"
}

smoke_packaged() {
  local ws="$1"
  [[ -n "$ws" ]] || fail "usage: smoke.sh packaged <base-url> <workspace-id>"

  echo "--- Packaged-app independence ($ws) ---"
  echo "     (run this against a server started OUTSIDE the repository,"
  echo "      with an isolated HOME and no .agents directory)"

  # The whole planning lifecycle must work with no repository skill in reach.
  local plan base
  plan=$(curl -s -X POST "$BASE_URL/api/workspaces/$ws/plans" \
    -H 'Content-Type: application/json' \
    -d '{"request":"Packaged independence check"}' | json_field id)
  [[ -n "$plan" ]] || fail "could not create a plan"
  base="$BASE_URL/api/workspaces/$ws/plans/$plan"
  echo "ok   created a plan"

  # Manual drafting: no model, no skill.
  expect_status 200 PATCH "$base/draft" '{
    "objective":"Prove planning is self-contained","revision":0,
    "content":{"execution":{"mode":"step_through"},
      "groups":[{"id":"grp-1","title":"Check","items":[
        {"id":"itm-1","description":"Confirm the plan lifecycle needs no repository skill"}]}]}}'

  local hash approval
  hash=$(curl -s -X POST "$base/versions" -H 'Content-Type: application/json' -d '{}' | json_field content_hash)
  [[ -n "$hash" ]] || fail "review snapshot produced no content hash"
  echo "ok   review version snapshotted"

  approval=$(curl -s -X POST "$base/approvals" -H 'Content-Type: application/json' \
    -d "{\"version\":1,\"content_hash\":\"$hash\",\"effect\":\"create_tasks\",\"user_name\":\"smoke\",\"idempotency_key\":\"packaged-1\"}" |
    json_field id)
  [[ -n "$approval" ]] || fail "approval was refused"
  expect_status 200 POST "$base/materialize" "{\"approval_id\":\"$approval\"}"
  echo "ok   approved and materialized with no repository skill in reach"

  # Diagnostics confirm the lifecycle is wired and that only generation needs
  # a model.
  local diag
  diag=$(curl -s "$BASE_URL/api/workspaces/$ws/plan-diagnostics")
  [[ "$(echo "$diag" | json_field components.materializer)" == "True" ]] ||
    fail "materialization is unwired in the packaged build: $diag"
  echo "ok   diagnostics report a wired planning subsystem"

  # No planning skill is installed or resolvable.
  local skills
  skills=$(curl -s "$BASE_URL/api/skills" | python3 -c 'import sys,json
try:
    d = json.load(sys.stdin)
except Exception:
    print(""); raise SystemExit
items = d.get("skills") if isinstance(d, dict) else d
print(",".join(str(s.get("name","")) for s in (items or [])))')
  case "$skills" in
  *workspace-planning*) fail "the legacy planning skill is installed: $skills" ;;
  esac
  echo "ok   no workspace-planning skill is installed"

  echo "--- Packaged independence smoke passed ---"
}

# ---------------------------------------------------------------------------
# Issue #353 — live Workspace Directory refresh
#
#   ./scripts/smoke.sh rootswitch <base-url> <fixture-dir>
#
# Drives Manual Test Guide steps 1-8 against a running isolated demo server.
# <fixture-dir> must be a disposable directory inside the demo sandbox; two
# roots are created under it and are the only paths this check ever writes to.
# ---------------------------------------------------------------------------

# seed_workspace_folder writes a workspace folder straight onto disk, the way a
# workspace directory carried from another machine already looks before Ori has
# ever been pointed at it. Nothing here goes through the app.
seed_workspace_folder() {
  local parent="$1" id="$2" name="$3" slug="$4" kind="${5:-}"
  mkdir -p "$parent/$slug/files" "$parent/$slug/notes"
  chmod 0750 "$parent/$slug"
  ID="$id" NAME="$name" SLUG="$slug" KIND="$kind" python3 -c '
import json, os, datetime
now = datetime.datetime.now(datetime.timezone.utc).isoformat()
ws = {
    "id": os.environ["ID"],
    "name": os.environ["NAME"],
    "folder_slug": os.environ["SLUG"],
    "status": "active",
    "created_at": now,
    "updated_at": now,
    "shared_data": {},
    "messages": [],
    "tasks": [],
}
if os.environ.get("KIND"):
    ws["kind"] = os.environ["KIND"]
print(json.dumps(ws, indent=2))' >"$parent/$slug/workspace.json"
  chmod 0600 "$parent/$slug/workspace.json"
}

# set_workspace_root POSTs a directory and echoes the whole response.
set_workspace_root() {
  curl -s -X POST "$BASE_URL/api/settings/workspace-root" \
    -H 'Content-Type: application/json' \
    -d "$(ROOT="$1" python3 -c 'import json,os;print(json.dumps({"workspace_root":os.environ["ROOT"]}))')"
}

# workspace_names lists the names the app currently shows, one entry per listed
# workspace. The endpoint repeats the same set under a legacy "folders" key, so
# only one array is read — otherwise every workspace would look duplicated.
workspace_names() {
  curl -s "$BASE_URL/api/workspaces" | python3 -c 'import sys,json
d = json.load(sys.stdin)
items = d.get("workspaces")
if items is None:
    items = d.get("folders") or []
print(",".join(sorted(str(w.get("name","")) for w in items)))'
}

# expect_listed_once guards against a root switch duplicating a workspace row.
expect_listed_once() {
  local names count
  names=$(workspace_names)
  count=$(NAMES="$names" WANT="$1" python3 -c 'import os
print(os.environ["NAMES"].split(",").count(os.environ["WANT"]))')
  [[ "$count" == "1" ]] || fail "expected \"$1\" listed exactly once, got $count (listing: $names)"
  echo "ok   \"$1\" is listed exactly once"
}

expect_visible() {
  local names
  names=$(workspace_names)
  case ",$names," in
  *",$1,"*) echo "ok   \"$1\" is visible" ;;
  *) fail "expected \"$1\" to be visible, listing was: $names" ;;
  esac
}

expect_hidden() {
  local names
  names=$(workspace_names)
  case ",$names," in
  *",$1,"*) fail "expected \"$1\" to be hidden, listing was: $names" ;;
  *) echo "ok   \"$1\" is hidden" ;;
  esac
}

smoke_rootswitch() {
  local fixtures="$1"
  [[ -n "$fixtures" ]] || fail "usage: smoke.sh rootswitch <base-url> <fixture-dir>"

  # Every identity carries a run tag so the check is rerunnable against a
  # sandbox that already holds an earlier run's workspaces.
  local run="${SMOKE_RUN:-$(date +%H%M%S)-$$}"
  local root_a="$fixtures/root-a-$run" root_b="$fixtures/root-b-$run"
  mkdir -p "$root_a" "$root_b"
  chmod 0750 "$root_a" "$root_b"

  local a_name="A Only $run" b_name="B Only $run" g_name="B Group $run" c_name="B Child $run"

  echo "--- Step 1: establish Root A ---"
  local resp
  resp=$(set_workspace_root "$root_a")
  [[ "$(echo "$resp" | json_field success)" == "True" ]] || fail "saving Root A failed: $resp"
  echo "ok   Root A saved (refresh: $(echo "$resp" | json_field refresh.imported) imported)"

  local a_only
  a_only=$(curl -s -X POST "$BASE_URL/api/workspaces" \
    -H 'Content-Type: application/json' \
    -d "$(NAME="$a_name" python3 -c 'import json,os;print(json.dumps({"name":os.environ["NAME"]}))')" | workspace_id)
  [[ -n "$a_only" ]] || fail "could not create \"$a_name\" under Root A"
  expect_visible "$a_name"

  echo "--- Step 2: Root B is pre-populated on disk, then made live ---"
  seed_workspace_folder "$root_b" "smoke-353-b-only-$run" "$b_name" "b-only"
  seed_workspace_folder "$root_b" "smoke-353-b-group-$run" "$g_name" "b-group" "group"
  seed_workspace_folder "$root_b/b-group/sub-workspaces" "smoke-353-b-child-$run" "$c_name" "b-child"

  # Snapshot both trees: a root switch must never move, copy, rewrite, or
  # delete a workspace folder under either root.
  local digest_a_before digest_b_before
  digest_a_before=$(tree_digest "$root_a")
  digest_b_before=$(tree_digest "$root_b")

  resp=$(set_workspace_root "$root_b")
  [[ "$(echo "$resp" | json_field success)" == "True" ]] || fail "saving Root B failed: $resp"
  local imported
  imported=$(echo "$resp" | json_field refresh.imported)
  [[ "$imported" == "3" ]] || fail "refresh.imported = $imported, want 3 (response: $resp)"
  echo "ok   Root B applied live: imported=$imported hidden=$(echo "$resp" | json_field refresh.orphaned)"

  # No restart, no manual Rescan: the pre-existing folders are simply there.
  expect_listed_once "$b_name"
  expect_listed_once "$g_name"
  expect_listed_once "$c_name"
  expect_hidden "$a_name"

  echo "--- Step 3: switch back to Root A ---"
  resp=$(set_workspace_root "$root_a")
  local restored hidden
  restored=$(echo "$resp" | json_field refresh.restored)
  hidden=$(echo "$resp" | json_field refresh.orphaned)
  [[ "$restored" == "1" ]] || fail "refresh.restored = $restored, want 1 (response: $resp)"
  [[ "$hidden" == "3" ]] || fail "refresh.orphaned = $hidden, want 3 (response: $resp)"
  expect_listed_once "$a_name"
  expect_hidden "$b_name"
  echo "ok   switching back restored=$restored hidden=$hidden"

  echo "--- Step 4: switch to Root B again ---"
  resp=$(set_workspace_root "$root_b")
  restored=$(echo "$resp" | json_field refresh.restored)
  [[ "$restored" == "3" ]] || fail "refresh.restored = $restored, want 3 (response: $resp)"
  expect_listed_once "$b_name"
  expect_listed_once "$c_name"
  expect_hidden "$a_name"

  echo "--- Step 5: same-root save discovers an out-of-band folder ---"
  seed_workspace_folder "$root_b" "smoke-353-b-extra-$run" "B Extra $run" "b-extra"
  resp=$(set_workspace_root "$root_b")
  imported=$(echo "$resp" | json_field refresh.imported)
  [[ "$imported" == "1" ]] || fail "same-root save imported = $imported, want 1 (response: $resp)"
  expect_listed_once "B Extra $run"
  # ...and exactly once: saving the same root again adds nothing.
  resp=$(set_workspace_root "$root_b")
  imported=$(echo "$resp" | json_field refresh.imported)
  [[ "$imported" == "0" ]] || fail "repeat save imported = $imported, want 0 (response: $resp)"
  echo "ok   out-of-band folder discovered exactly once"

  # The ordinary Rescan button still works and still honors its own cooldown.
  local rescan
  rescan=$(curl -s -X POST "$BASE_URL/api/workspaces/rescan")
  [[ "$(echo "$rescan" | json_field success)" == "True" ]] || fail "explicit rescan failed: $rescan"
  rescan=$(curl -s -X POST "$BASE_URL/api/workspaces/rescan?background=1")
  [[ "$(echo "$rescan" | json_field skipped)" == "True" ]] ||
    fail "background rescan should be skipped inside the cooldown: $rescan"
  echo "ok   explicit Rescan unchanged, background cooldown intact"

  echo "--- Step 6: an explicit folder import outside both roots is unaffected ---"
  # A plain project directory (no workspace.json) is linked where it lives and
  # carries shared_data.folder_import. Its visibility belongs to the import
  # flow, not to whichever directory happens to be active.
  #
  # Importing an *exported workspace* folder — one that does contain a
  # workspace.json — is a different, unchanged flow: it copies the folder into
  # the active root, so it legitimately becomes a workspace of that root.
  local ext="$fixtures/external-$run/linked-project"
  mkdir -p "$ext"
  chmod 0750 "$ext"
  : >"$ext/README.md"
  local import_resp
  import_resp=$(curl -s -X POST "$BASE_URL/api/workspaces/import" \
    -H 'Content-Type: application/json' \
    -d "$(P="$ext" N="External $run" python3 -c 'import json,os;print(json.dumps({"path":os.environ["P"],"name":os.environ["N"]}))')")
  [[ "$(echo "$import_resp" | json_field success)" == "True" ]] || fail "folder import failed: $import_resp"
  expect_listed_once "External $run"
  set_workspace_root "$root_a" >/dev/null
  expect_listed_once "External $run"
  set_workspace_root "$root_b" >/dev/null
  expect_listed_once "External $run"
  [[ -f "$ext/README.md" ]] || fail "the linked folder was moved off its original path"
  [[ ! -d "$root_b/linked-project" ]] || fail "the linked folder was copied into the active root"
  echo "ok   linked folder import survived A->B->A in place"

  echo "--- Step 7: clearing the custom directory applies the default root ---"
  resp=$(set_workspace_root "")
  [[ "$(echo "$resp" | json_field success)" == "True" ]] || fail "clearing the directory failed: $resp"
  local effective
  effective=$(echo "$resp" | json_field effective_workspace_root)
  [[ -n "$effective" ]] || fail "cleared save reported no effective root: $resp"
  [[ "$effective" != "$root_b" ]] || fail "clearing left the custom root active: $resp"
  expect_hidden "$b_name"
  echo "ok   cleared to the effective default root: $effective"

  echo "--- Step 8: an invalid directory is refused and the live root survives ---"
  set_workspace_root "$root_b" >/dev/null
  local blocker="$fixtures/not-a-directory-$run"
  : >"$blocker"
  local status
  status=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/api/settings/workspace-root" \
    -H 'Content-Type: application/json' \
    -d "$(ROOT="$blocker" python3 -c 'import json,os;print(json.dumps({"workspace_root":os.environ["ROOT"]}))')")
  [[ "$status" != "200" ]] || fail "a file was accepted as a workspace directory"
  expect_listed_once "$b_name"
  echo "ok   invalid directory refused (HTTP $status), Root B still live"

  echo "--- Step 9: disk integrity ---"
  # Root A must be byte-identical: nothing under it was read, rewritten, or
  # removed by any of the switches. Root B legitimately gained the out-of-band
  # folder seeded in step 5, so it is compared against that expectation.
  [[ "$(tree_digest "$root_a")" == "$digest_a_before" ]] ||
    fail "Root A changed on disk across the switches"
  echo "ok   Root A is byte-identical after every switch"
  local added
  added=$(comm -13 <(echo "$digest_b_before" | awk '{print $2}' | sort) \
    <(tree_digest "$root_b" | awk '{print $2}' | sort) | tr '\n' ' ')
  case "$added" in
  *b-extra*) echo "ok   Root B gained only the folder seeded in step 5: $added" ;;
  "") echo "ok   Root B is unchanged on disk" ;;
  *) fail "Root B gained unexpected files: $added" ;;
  esac

  echo "--- Root switch smoke passed (run tag: $run) ---"
}

# tree_digest prints a stable checksum per file under a root, so before/after
# comparison proves a root switch moved, copied, rewrote, or deleted nothing.
# The store-owned index cache is excluded: its bytes change on every open.
tree_digest() {
  (cd "$1" && find . -type f ! -name 'index.db*' -print0 | sort -z |
    xargs -0 shasum 2>/dev/null | sed "s|  \./|  |")
}

# --- Domain-specialist onboarding (tasks/prd-domain-specialist-onboarding.md)
#
# serve is the isolated-demo launch from CLAUDE.md > Smoke Testing, as one
# stable token. `wt demo` is the normal way in, but it needs an interactive
# shell and uses `mktemp -d`, which an agent sandbox denies silently — an empty
# SMOKE_DIR then resolves HOME and ORI_DATA_DIR to the working directory and
# the server writes its state into the worktree. This uses $TMPDIR, always set
# and always writable, and starts the server from inside the sandbox so the
# plugin store is isolated too.
serve_isolated() {
  local port="${1:-8931}" name="${2:-default}"
  local dir="${TMPDIR:-/tmp}/ori-smoke-${name}"
  mkdir -p "$dir" || fail "could not create $dir"
  [[ -d "$dir" ]] || fail "sandbox $dir does not exist"
  local binary
  binary="$(cd "$(dirname "$0")/.." && pwd -P)/bin/ori-agent"
  [[ -x "$binary" ]] || fail "build first: ./scripts/build-server.sh"
  echo "sandbox: $dir"
  echo "url:     http://localhost:${port}"
  cd "$dir" || fail "could not enter $dir"
  HOME="$dir" ORI_DATA_DIR="$dir" PORT="$port" exec "$binary"
}

# smoke_specialist checks the server side of the detected-app offer. The
# browser paths are covered by tests/domain-specialist-onboarding.spec.ts.
smoke_specialist() {
  local catalog detect
  echo "== specialist mapping =="
  expect_status 200 GET "$BASE_URL/api/onboarding/specialists"
  expect_status 405 POST "$BASE_URL/api/onboarding/specialists"

  catalog=$(curl -s "$BASE_URL/api/onboarding/specialists")
  local slug template manual
  slug=$(printf '%s' "$catalog" | json_field 'specialists.0.slug')
  template=$(printf '%s' "$catalog" | json_field 'specialists.0.suggested_template_id')
  manual=$(printf '%s' "$catalog" | json_field 'specialists.0.offer_copy.manual_label')
  [[ -n "$slug" ]] || fail "catalog returned no specialist slug"
  [[ -n "$template" ]] || fail "specialist $slug has no suggested template"
  [[ -n "$manual" ]] || fail "specialist $slug has no manual path label"
  echo "ok   catalog: $slug -> $template (manual: $manual)"

  # Detection is allowed 30s and may legitimately match nothing on this host.
  # Either answer is correct; a malformed one is not.
  echo "== detection =="
  detect=$(curl -s -m 40 -X POST "$BASE_URL/api/onboarding/detect")
  local detected
  detected=$(printf '%s' "$detect" | json_field 'specialist.slug')
  if [[ -n "$detected" ]]; then
    local headline question
    headline=$(printf '%s' "$detect" | json_field 'specialist.offer_copy.headline')
    question=$(printf '%s' "$detect" | json_field 'specialist.offer_copy.question')
    [[ -n "$headline" && -n "$question" ]] || fail "matched $detected with incomplete offer copy"
    case "$question" in
    "Do you use"* | "Are you a"*) fail "offer asks what is already known: $question" ;;
    esac
    echo "ok   detected $detected: $headline $question"
  else
    echo "ok   nothing matched on this host — generic path"
  fi

  # The offer's own write. A bad answer is rejected before anything is
  # persisted, so these are validation probes: none of them records an answer.
  echo "== bad answers are refused =="
  expect_status 400 POST "$BASE_URL/api/personal-assistant/specialist" \
    '{"decision":"accepted","slug":"not_a_domain"}'
  expect_status 400 POST "$BASE_URL/api/personal-assistant/specialist" \
    '{"decision":"accepted"}'
  expect_status 400 POST "$BASE_URL/api/personal-assistant/specialist" \
    '{"decision":"maybe"}'
  expect_status 400 POST "$BASE_URL/api/personal-assistant/specialist" '{}'
  echo "PASS specialist"
}

case "${1:-}" in
serve) serve_isolated "${2:-8931}" "${3:-default}" ;;
specialist) smoke_specialist ;;
seed) seed_demo ;;
rootswitch) smoke_rootswitch "${3:-}" ;;
slot) smoke_slot "${3:-}" ;;
reconcile) smoke_reconcile "${3:-}" ;;
policy) smoke_policy "${3:-}" ;;
boundary) smoke_boundary "${3:-}" ;;
hardening) smoke_hardening "${3:-}" ;;
packaged) smoke_packaged "${3:-}" ;;
plans) smoke_plans "${3:-}" ;;
drafting) smoke_drafting "${3:-}" ;;
review) smoke_review "${3:-}" ;;
materialize) smoke_materialize "${3:-}" ;;
execution) smoke_execution "${3:-}" ;;
*)
  echo "usage:" >&2
  echo "  $0 serve [port] [sandbox-name]           # run an ISOLATED demo server (Ctrl-C to stop)" >&2
  echo "  $0 specialist <base-url>                 # domain-specialist onboarding API checks" >&2
  echo "  $0 seed <base-url>                       # seed plans and print URLs to review" >&2
  echo "  $0 {plans|drafting|review|materialize|execution|slot|reconcile|policy|boundary|hardening|packaged} <base-url> <workspace-id>" >&2
  exit 2
  ;;
esac
