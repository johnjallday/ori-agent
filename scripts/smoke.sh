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
# This worktree's feature: Agents in the Workspace Root
# (tasks/prd-agents-in-workspace-root.md): confirm-root, seed-legacy-agents,
# root-unmounted, drop-foreign-workspace, agent-files, agent-chat, and
# agents-root-verify (the success metrics: two starts and a chat leave Agents/
# unchanged under git).
# Before it, Blueprint-aware Create Workspace, Slices A and B
# (tasks/prd-blueprint-aware-create-workspace.md): reaper-blueprint prepares a
# fresh sandbox with the reviewed REAPER blueprint; blueprint-details prints
# what a create stored. Slice B (attach an existing project) adds
# reaper-demo-folders, pick-folder, folder-checksum, and attach-details.
# Earlier features' checks are kept, because the point of one stable name is
# that it accumulates: Reviewed integration floor
# (tasks/prd-reviewed-integration-latest-release.md): integration,
# Starter missions (tasks/prd-starter-missions.md),
# Retire the Agent Type field
# (tasks/prd-retire-agent-type.md), City Economy (tasks/prd-city-economy.md), Agents Page
# UX (tasks/prd-agents-page-ux.md), Workspace
# Planning Workflow (tasks/prd-workspace-planning-policy.md) and the
# domain-specialist onboarding checks all still live below.
#
# Usage:
#   ./scripts/smoke.sh serve 8941 agentsux            # isolated server + sandbox
#   ./scripts/smoke.sh agentseed http://localhost:8941 agentsux
#   ./scripts/smoke.sh agentmap http://localhost:8941
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

# smoke_starter waits for a running isolated server, then runs one stage of the
# starter missions browser demo, saving screenshots under $TMPDIR/starter-demo.
# The wait and the run were a repeated two-step shell during development; here
# they are one stable command. Extra arguments go to the demo script, e.g.
#   ./scripts/smoke.sh starter http://localhost:8947 email --focus=help_with_email
#   ./scripts/smoke.sh starter http://localhost:8947 tidy --sandbox="$TMPDIR/ori-smoke-starter"
smoke_starter() {
  local stage="${3:-}"
  [[ -n "$stage" ]] || fail "usage: $0 starter <base-url> <card|tidy|email|plan|results|states> [demo flags]"
  local ready=""
  for _ in $(seq 1 30); do
    if curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/api/progression" | grep -q 200; then
      ready=1
      break
    fi
    sleep 1
  done
  [[ -n "$ready" ]] || fail "no server answered at $BASE_URL within 30s"
  local root out
  root="$(cd "$(dirname "$0")/.." && pwd -P)"
  out="${TMPDIR:-/tmp}/starter-demo"
  node "$root/scripts/demo-starter-missions.mjs" "$BASE_URL" "$out" "$stage" "${@:4}"
}

# smoke_meet_assistant drives Mission 01 ("Meet your assistant") through the API.
#   onboard       close onboarding without a hire (what the modal's Model step does)
#   status        print the relationship state and the five missions with their locks
#   hire          hire the assistant, model-free, exactly as the Agents page preset does
#   demo <stage>  run a browser stage of scripts/demo-meet-assistant.mjs (preset,
#                 walkthrough, panel-closed, narrow, modal, home); screenshots go to
#                 $TMPDIR/meet-assistant-demo/<stage>. Each stage needs a fresh sandbox.
smoke_meet_assistant() {
  local stage="${3:-status}"
  case "$stage" in
  demo)
    local demo_stage="${4:-home}" root
    root="$(cd "$(dirname "$0")/.." && pwd -P)"
    node "$root/scripts/demo-meet-assistant.mjs" "$BASE_URL" \
      "${TMPDIR:-/tmp}/meet-assistant-demo/$demo_stage" "$demo_stage" "${@:5}"
    ;;
  onboard)
    curl -s -o /dev/null -w "%{http_code} onboarding complete\n" \
      -X POST "$BASE_URL/api/onboarding/complete" -H 'Content-Type: application/json' -d '{}'
    ;;
  status)
    printf 'personal_assistant.state = %s\n' \
      "$(curl -s "$BASE_URL/api/personal-assistant" | json_field 'personal_assistant.state')"
    curl -s "$BASE_URL/api/progression" | python3 -c 'import json, sys
status = json.load(sys.stdin)
print(json.dumps([{key: mission.get(key) for key in
    ("order", "id", "status", "locked", "locked_reason", "action_url", "reward_craft")}
    for mission in status.get("missions", [])], indent=2))'
    ;;
  hire)
    local name="${4:-Atlas}" body
    body=$(python3 -c 'import json, sys, uuid
print(json.dumps({"request_id": "smoke-" + uuid.uuid4().hex, "if_version": 0,
    "display_name": sys.argv[1], "appearance": {"mode": "generated", "generated": {}},
    "mandate": "", "focus_areas": ["plan_my_day", "keep_projects_moving"]}))' "$name")
    curl -s -w "\n%{http_code} hire\n" -X POST "$BASE_URL/api/personal-assistant/hire" \
      -H 'Content-Type: application/json' -d "$body"
    ;;
  *) fail "usage: $0 meetassistant <base-url> <onboard|status|hire [name]|demo <stage>>" ;;
  esac
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

# smoke_agentseed fills an isolated sandbox with a roster that actually exercises
# the /agents page.
#
# A fresh sandbox holds three built-in CLI agents in no workspace, and that hides
# most of what this feature does: the two-pill + overflow rule, the Workspace
# filter, per-workspace sections, multi-workspace membership, and the portrait
# progress ring all need real data. Seeding it by hand is four screens of curl
# that has to be retyped every time the sandbox is thrown away, which is exactly
# the shell this script exists to hold.
#
# Usage: ./scripts/smoke.sh agentseed <base-url> [sandbox-name]
smoke_agentseed() {
  local sandbox_name="${1:-default}"
  local sandbox="${TMPDIR:-/tmp}/ori-smoke-${sandbox_name}"
  echo "--- seeding $BASE_URL (sandbox $sandbox) ---"

  # A representative library: every catalog role represented, some favorites,
  # some tags, and several agents with no description at all - the empty cases
  # are the ones that used to print invented placeholder text.
  echo "== agents =="
  seed_agent "Atlas" orchestrator true '["ops","lead"]' "Runs the release train and dispatches work to the team."
  seed_agent "Beacon" researcher false '["research"]' "Digs through docs and the web for sourced answers."
  seed_agent "Cinder" analyzer false '["data"]' ""
  seed_agent "Delta" synthesizer true '["writing","docs"]' "Turns raw findings into short readable briefs."
  seed_agent "Echo" validator false '[]' ""
  seed_agent "Foxglove" specialist false '["audio"]' "Domain specialist for the studio workflows."
  seed_agent "Grove" researcher false '["research","web"]' ""
  seed_agent "Harbor" orchestrator false '["ops"]' "Second commander, kept for the staging workspace."
  seed_agent "Iris" analyzer true '["data","perf"]' ""
  seed_agent "Juniper" synthesizer false '[]' ""
  seed_agent "Kestrel" validator false '["qa"]' "Checks work before it ships."
  seed_agent "Lantern" specialist false '[]' ""
  seed_agent "Marlow" researcher false '["legal"]' ""
  seed_agent "Nimbus" analyzer false '[]' ""
  seed_agent "Onyx" specialist true '["audio","reaper"]' "Knows the REAPER session inside out."
  seed_agent "Pike" validator false '[]' ""

  # Three workspaces, with deliberate overlap: Delta belongs to all three, which
  # is the only way to see the "+1" overflow pill and the Map's one-tile-per-agent
  # rule diverging from the Gallery's one-card-per-membership rule.
  echo "== workspaces =="
  local studio field release
  studio=$(seed_workspace "Studio" "Atlas")
  field=$(seed_workspace "Field Notes" "Beacon")
  release=$(seed_workspace "Release" "Harbor")
  [[ -n "$studio" && -n "$field" && -n "$release" ]] ||
    fail "workspace creation returned no id (studio='$studio' field='$field' release='$release')"
  echo "ok   studio=$studio field=$field release=$release"

  seed_assign "Delta" "$studio" "$field" "$release"
  seed_assign "Iris" "$studio" "$field"
  seed_assign "Cinder" "$studio"
  seed_assign "Kestrel" "$release"
  seed_assign "Grove" "$field"
  seed_assign "Onyx" "$studio" "$release"

  # Levels and stages. XP normally accrues from activity and there is no endpoint
  # that awards it, so this writes the evolution record straight into the
  # sandbox's own agent files. One agent per stage, plus a 0% and a 99% ring so
  # both extremes are on screen.
  echo "== progression =="
  [[ -d "$sandbox/agents" ]] || fail "no agents directory in $sandbox - is this the sandbox the server is using?"
  python3 -c 'import json, os, sys
sandbox, per = sys.argv[1], 100
plan = {
    "Atlas": (3, 45, "learner"),
    "Beacon": (1, 10, "infant"),
    "Cinder": (0, 0, "spark"),
    "Delta": (7, 80, "expert"),
    "Echo": (2, 99, "infant"),
    "Iris": (5, 60, "learner"),
    "Onyx": (12, 25, "sentient"),
    "Foxglove": (4, 50, "learner"),
    "Harbor": (1, 75, "infant"),
    "Kestrel": (9, 30, "expert"),
}
changed = 0
for name, (level, into, stage) in plan.items():
    path = os.path.join(sandbox, "agents", name, "agent_settings.json")
    if not os.path.exists(path):
        print("skip %s: no settings file" % name)
        continue
    with open(path) as handle:
        data = json.load(handle)
    evolution = data.get("evolution") or {}
    evolution.update({"level": level, "experience": level * per + into, "stage": stage})
    data["evolution"] = evolution
    with open(path, "w") as handle:
        json.dump(data, handle, indent=2)
    changed += 1
    print("ok   %-9s Lv %-2d %2d%% into the level, stage %s" % (name, level, into, stage))
print("%d agents given progression" % changed)' "$sandbox"

  echo
  echo "Restart the server to load the progression records, then open:"
  echo "  $BASE_URL/agents"
  echo "PASS agentseed"
}

# seed_agent creates one agent and applies the metadata that is not part of
# creation. Both requests report their status so a partial seed is visible.
seed_agent() {
  local name="$1" role="$2" favorite="$3" tags="$4" description="$5"
  local body
  body=$(python3 -c 'import json, sys
print(json.dumps({"name": sys.argv[1], "catalog_role": sys.argv[2]}))' "$name" "$role")
  curl -s -o /dev/null -w "%{http_code} create $name\n" \
    -X POST "$BASE_URL/api/agents" -H 'Content-Type: application/json' -d "$body"
  body=$(python3 -c 'import json, sys
print(json.dumps({
    "description": sys.argv[1],
    "tags": json.loads(sys.argv[2]),
    "favorite": sys.argv[3] == "true",
}))' "$description" "$tags" "$favorite")
  curl -s -o /dev/null -w "%{http_code} patch  $name\n" \
    -X PATCH "$BASE_URL/api/agents/$(seed_urlencode "$name")" \
    -H 'Content-Type: application/json' -d "$body"
}

seed_workspace() {
  local name="$1" entry="$2" body
  body=$(python3 -c 'import json, sys
print(json.dumps({"name": sys.argv[1], "entry_agent_name": sys.argv[2]}))' "$name" "$entry")
  curl -s -X POST "$BASE_URL/api/workspaces" \
    -H 'Content-Type: application/json' -d "$body" | workspace_id
}

# seed_assign replaces an agent's workspace set. PUT, not POST: the
# agent-centric route takes the whole set, which is also how the Inspector's
# Workspaces tab reads it back.
seed_assign() {
  local agent="$1" body
  shift
  body=$(python3 -c 'import json, sys
print(json.dumps({"workspace_ids": sys.argv[1:]}))' "$@")
  curl -s -o /dev/null -w "%{http_code} assign $agent\n" \
    -X PUT "$BASE_URL/api/agents/$(seed_urlencode "$agent")/workspaces" \
    -H 'Content-Type: application/json' -d "$body"
}

# Agent names are display strings and reach the API as a path segment, so a name
# with a space has to be percent-encoded before it becomes a URL.
seed_urlencode() {
  python3 -c 'import sys, urllib.parse
print(urllib.parse.quote(sys.argv[1], safe=""))' "$1"
}

# agentmap_patch builds a positions patch with JSON-escaped agent names, so a
# roster whose first agent is called `O'Brien "Ori"` still produces a valid body.
#
#   agentmap_patch <op> <expected-revision|""> [<name> <x> <y>]...
agentmap_patch() {
  python3 -c 'import sys,json
op, expected, args = sys.argv[1], sys.argv[2], sys.argv[3:]
positions = {args[i]: {"x": float(args[i + 1]), "y": float(args[i + 2])} for i in range(0, len(args), 3)}
patch = {"operations": [{"op": op, "positions": positions}]}
if expected:
    patch["expected_revision"] = int(expected)
print(json.dumps(patch))' "$@"
}

# agentmap_position prints "<x>,<y>" for one agent, or "" when it has no anchor.
# The name is looked up as an exact key rather than through json_field, whose
# dotted path would split a name that contains a dot. Either envelope is
# accepted, so a write response and a read response can be checked the same way.
agentmap_position() {
  python3 -c 'import sys,json
d = json.load(sys.stdin)
layout = d.get("layout") or (d.get("result") or {}).get("layout") or {}
point = (layout.get("positions") or {}).get(sys.argv[1])
print("" if point is None else "%g,%g" % (point["x"], point["y"]))' "$1"
}

# smoke_agentmap drives the three Agent Map layout endpoints end to end.
#
# The Map canvas is a browser surface, but its persistence contract is not: the
# revision protocol, the world bounds, the agent-existence check, strict
# decoding and the identity boundary are all only observable over HTTP. This is
# where they are checked.
#
# It is rerunnable against a sandbox that already has a layout: nothing asserts
# an absolute revision, only that reads never move it and writes always do.
smoke_agentmap() {
  local url="$BASE_URL/api/agent-map/layout"
  echo "--- Agent Map layout API ---"

  # Real agent names. A patch naming an agent the roster does not hold is a 404
  # by design, so the checks use whatever this sandbox actually has.
  local list first second
  list=$(curl -s "$BASE_URL/api/agents/dashboard/list")
  first=$(printf '%s' "$list" | json_field 'agents.0.name')
  second=$(printf '%s' "$list" | json_field 'agents.1.name')
  [[ -n "$first" && -n "$second" ]] ||
    fail "need two agents in the roster (got '$first' '$second')"
  echo "ok   using agents '$first' and '$second'"

  # Reading never writes (FR-51). Two reads of the same layout must report the
  # same revision; a GET that lazily created a record would bump it.
  echo "== read is read-only =="
  local schema rev_a rev_b
  rev_a=$(curl -s "$url" | json_field 'layout.revision')
  rev_b=$(curl -s "$url" | json_field 'layout.revision')
  schema=$(curl -s "$url" | json_field 'layout.schema_version')
  [[ "$rev_a" == "$rev_b" ]] || fail "GET moved the revision: $rev_a -> $rev_b"
  [[ "$schema" == "1" ]] || fail "schema_version = '$schema', want 1"
  echo "ok   two reads both at revision $rev_a, schema 1"

  echo "== anchors, camera and preference round-trip =="
  local anchored rev_anchored
  anchored=$(curl -s -X PATCH "$url" -H 'Content-Type: application/json' \
    -d "$(agentmap_patch set_positions '' "$first" 120 -40 "$second" 300 80)")
  rev_anchored=$(printf '%s' "$anchored" | json_field 'result.layout.revision')
  [[ -n "$rev_anchored" ]] || fail "set_positions returned no revision"
  [[ "$rev_anchored" -gt "$rev_a" ]] || fail "revision did not advance ($rev_a -> $rev_anchored)"
  [[ "$(printf '%s' "$anchored" | agentmap_position "$first")" == "120,-40" ]] ||
    fail "the write response did not echo the committed anchor"
  echo "ok   two anchors committed at revision $rev_anchored"

  # Both operations in one patch, because a camera move and a preference change
  # arrive together when the user zooms with snapping off.
  expect_status 200 PATCH "$url" \
    '{"operations":[{"op":"set_viewport","viewport":{"center_x":210,"center_y":20,"zoom":0.75}},{"op":"set_preferences","snap_to_grid":false}]}'

  local layout zoom snap
  layout=$(curl -s "$url")
  zoom=$(printf '%s' "$layout" | json_field 'layout.viewport.zoom')
  snap=$(printf '%s' "$layout" | json_field 'layout.snap_to_grid')
  [[ "$zoom" == "0.75" ]] || fail "viewport zoom = '$zoom', want 0.75"
  [[ "$snap" == "False" ]] || fail "snap_to_grid = '$snap', want false"
  [[ "$(printf '%s' "$layout" | agentmap_position "$second")" == "300,80" ]] ||
    fail "the second anchor did not survive the read"
  echo "ok   camera 0.75x, snapping off, both anchors persisted"

  # A stale revision is a 409, not a 400: the body was well formed and would
  # have been accepted a moment ago. Nothing may change (FR-53).
  echo "== stale writes are refused, current ones accepted =="
  local rev_now
  rev_now=$(printf '%s' "$layout" | json_field 'layout.revision')
  expect_status 409 PATCH "$url" \
    "$(agentmap_patch set_positions "$((rev_now - 1))" "$first" 999 999)"
  [[ "$(curl -s "$url" | agentmap_position "$first")" == "120,-40" ]] ||
    fail "the refused write changed the layout anyway"
  echo "ok   the 409 left the anchor at 120,-40"

  expect_status 200 PATCH "$url" \
    "$(agentmap_patch set_positions "$rev_now" "$first" 121 -41)"

  # Rejections. Each of these is a distinct guard, and each has a status the
  # client acts on differently.
  echo "== rejections =="
  expect_status 400 PATCH "$url" "$(agentmap_patch set_positions '' "$first" 99999999 0)"
  expect_status 404 PATCH "$url" "$(agentmap_patch set_positions '' 'No Such Agent' 1 1)"
  expect_status 400 PATCH "$url" \
    '{"operations":[{"op":"set_positions","positions":{},"colour":"red"}]}'
  expect_status 400 PATCH "$url" \
    '{"user_id":"someone-else","operations":[{"op":"reset"}]}'
  expect_status 400 PATCH "$url" \
    '{"operations":[{"op":"set_viewport","viewport":{"center_x":0,"center_y":0,"zoom":0}}]}'

  # Reset clears the ARRANGEMENT only. Losing your view as well would make a
  # reset something users learn to fear (FR-72).
  echo "== reset clears anchors and keeps the view =="
  expect_status 200 DELETE "$url"
  layout=$(curl -s "$url")
  [[ -z "$(printf '%s' "$layout" | agentmap_position "$first")" ]] ||
    fail "reset left an anchor behind"
  [[ "$(printf '%s' "$layout" | json_field 'layout.viewport.zoom')" == "0.75" ]] ||
    fail "reset discarded the camera"
  [[ "$(printf '%s' "$layout" | json_field 'layout.snap_to_grid')" == "False" ]] ||
    fail "reset discarded the snap preference"
  echo "ok   anchors gone, camera and snapping intact"

  # Undo. restore_positions is what the reset's undo sends: the exact prior set,
  # in one write, rather than one request per tile.
  echo "== the reset is undoable =="
  expect_status 200 PATCH "$url" \
    "$(agentmap_patch restore_positions '' "$first" 121 -41 "$second" 300 80)"
  layout=$(curl -s "$url")
  [[ "$(printf '%s' "$layout" | agentmap_position "$first")" == "121,-41" ]] ||
    fail "undo did not restore the first anchor"
  [[ "$(printf '%s' "$layout" | agentmap_position "$second")" == "300,80" ]] ||
    fail "undo did not restore the second anchor"
  echo "ok   both anchors restored"

  echo "PASS agentmap"
}

# --------------------------------------------------------------------------
# City Economy (tasks/prd-city-economy.md)
# --------------------------------------------------------------------------

# economy_state prints the whole economy in one line, which is what every check
# below wants to see between steps.
economy_state() {
  printf '  economy: '
  curl -s "$BASE_URL/api/economy"
  printf '\n'
}

# economy_field reads one nested field from a JSON blob on stdin without needing
# jq, which is not guaranteed on a fresh machine.
economy_field() {
  python3 -c 'import json,sys; print(json.loads(sys.stdin.read())["'"$1"'"]["'"$2"'"])'
}

# smoke_economy_seed walks the earning half of the loop against a running
# server: a chat message earns Craft, the same message again earns nothing, a
# hand-completed task earns more, and a recurring task appears as a Farm.
smoke_economy_seed() {
  echo "--- City Economy: earning ---"
  economy_state

  echo "chat message (earns Craft)"
  curl -s -o /dev/null -X POST "$BASE_URL/api/chat" \
    -H 'Content-Type: application/json' \
    -d '{"question":"seed the economy demo","agent_name":""}'
  economy_state

  echo "the same message again (duplicate window: earns nothing)"
  curl -s -o /dev/null -X POST "$BASE_URL/api/chat" \
    -H 'Content-Type: application/json' \
    -d '{"question":"seed the economy demo","agent_name":""}'
  economy_state

  local workspace_id task_id farm_id
  workspace_id="$(curl -s -X POST "$BASE_URL/api/workspaces" \
    -H 'Content-Type: application/json' \
    -d '{"name":"Economy Demo","description":"city-economy checkpoint"}' \
    | economy_field folder id)"
  echo "workspace_id: $workspace_id"

  echo "hand-run task, completed (earns Craft)"
  task_id="$(curl -s -X POST "$BASE_URL/api/orchestration/tasks" \
    -H 'Content-Type: application/json' \
    -d "{\"workspace_id\":\"$workspace_id\",\"description\":\"A hand-run task\",\"priority\":2}" \
    | economy_field task id)"
  curl -s -o /dev/null -X POST "$BASE_URL/api/orchestration/tasks/$task_id/complete" \
    -H 'Content-Type: application/json' -d '{"result":"done by hand"}'
  economy_state

  echo "recurring task (a Farm; costs Craft, so this 409s on an empty city)"
  farm_id="$(curl -s -X POST "$BASE_URL/api/orchestration/tasks" \
    -H 'Content-Type: application/json' \
    -d "{\"workspace_id\":\"$workspace_id\",\"description\":\"Daily inbox triage\",\"to\":\"Claude Code\",\"schedule\":{\"type\":\"daily\",\"time\":\"09:00\"},\"schedule_enabled\":true,\"schedule_name\":\"Inbox triage\"}" \
    | python3 -c 'import json,sys; d=json.loads(sys.stdin.read()); print(d.get("task",{}).get("id","(refused: "+str(d.get("error"))+")"))')"
  echo "farm: $farm_id"
  economy_state

  echo "workspace_id=$workspace_id"
  echo "task_id=$task_id"
  echo "PASS economy seed"
}

# smoke_economy_earn earns Craft the honest way, by sending distinct chat
# messages. The hourly cap still applies, so this cannot mint more than the cap
# allows — which is the point.
smoke_economy_earn() {
  local count="${3:-20}" i
  echo "--- City Economy: earning $count Craft by chatting ---"
  for i in $(seq 1 "$count"); do
    curl -s -o /dev/null -X POST "$BASE_URL/api/chat" \
      -H 'Content-Type: application/json' \
      -d "{\"question\":\"economy demo message number $i\",\"agent_name\":\"\"}"
  done
  economy_state
  echo "PASS economy earn"
}

# smoke_economy_quote prices a cadence change without charging it, which is the
# same call the task editor's price line makes.
smoke_economy_quote() {
  local workspace_id="${3:-}" task_id="${4:-}"
  echo "--- City Economy: quote ---"
  echo "build (a new task becoming a Farm):"
  curl -s -X POST "$BASE_URL/api/economy/quote" \
    -H 'Content-Type: application/json' \
    -d '{"workspace_id":"","task_id":"","schedule":{"type":"daily","time":"09:00"},"schedule_enabled":true}'
  printf '\n'
  if [ -n "$task_id" ]; then
    echo "upgrade (this Farm to hourly):"
    curl -s -X POST "$BASE_URL/api/economy/quote" \
      -H 'Content-Type: application/json' \
      -d "{\"workspace_id\":\"$workspace_id\",\"task_id\":\"$task_id\",\"schedule\":{\"type\":\"interval\",\"interval_minutes\":60},\"schedule_enabled\":true}"
    printf '\n'
  fi
  echo "PASS economy quote"
}

# smoke_economy_pending seeds pending-harvest rows straight into a DEMO sandbox
# database.
#
# A genuine pending Harvest needs a Farm run to finish, which needs a working
# LLM provider. When the demo machine has none (no key, or an exhausted quota)
# the Home surface still has to be driven in a real browser, so this writes the
# rows a finished run would have written. Everything downstream — the pile, the
# popover, banking — is then the real path.
#
# It refuses anything that is not a throwaway sandbox database.
smoke_economy_pending() {
  local db="${3:-}" workspace_id="${4:-}" task_id="${5:-}" count="${6:-3}" batch i
  [ -n "$db" ] && [ -n "$workspace_id" ] && [ -n "$task_id" ] ||
    fail "usage: $0 economypending <base-url> <sandbox-db> <workspace-id> <task-id> [count]"
  case "$db" in
  *ori-demo.* | *smoke* | *economy-smoke*) ;;
  *) fail "refusing: $db is not a demo sandbox database" ;;
  esac

  # Run keys carry the seeding time so a second seeding adds NEW rows rather
  # than silently colliding with rows the previous one already banked.
  batch="$(date -u +%s)"
  for i in $(seq 1 "$count"); do
    sqlite3 "$db" "INSERT OR IGNORE INTO economy_harvest_pending
      (task_id, workspace_id, run_key, produced_at, harvested_at)
      VALUES ('$task_id', '$workspace_id', 'seeded-$batch-$i', '$(date -u +%Y-%m-%dT%H:%M:%SZ)', NULL);"
  done
  echo "seeded $count pending runs for task $task_id"
  economy_state
  echo "PASS economy pending"
}

# assert_no_agent_type reads JSON on stdin and fails if any agent object, or any
# model row under providers[].models[], still carries the retired "type" key.
# A provider's own "type" (cloud/local) is a different field and is allowed.
assert_no_agent_type() {
  python3 -c 'import sys, json
label = sys.argv[1]
doc = json.load(sys.stdin)
hits = []
def check(obj, where):
    if isinstance(obj, dict) and "type" in obj:
        hits.append("%s type=%r" % (where, obj["type"]))
if isinstance(doc, dict) and isinstance(doc.get("providers"), list):
    for p in doc["providers"]:
        for m in p.get("models") or []:
            check(m, "%s model %s" % (p.get("name"), m.get("value")))
elif isinstance(doc, dict) and isinstance(doc.get("agents"), list):
    for a in doc["agents"]:
        check(a, "agent %s" % a.get("name"))
else:
    check(doc, "object")
if hits:
    print("FAIL: %s: %s" % (label, "; ".join(hits)), file=sys.stderr)
    sys.exit(1)
print("ok   %s has no agent type" % label)' "$1"
}

# smoke_agent_type_api checks that an API client still posting the retired
# "type" key succeeds, and that no agent or model response echoes it
# (retire-agent-type, PRD FR11-FR13).
smoke_agent_type_api() {
  local name="Smoke Legacy Type $$" encoded
  encoded=$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1]))' "$name")
  echo "== create with a legacy type key =="
  expect_status 200 POST "$BASE_URL/api/agents" \
    "{\"name\":\"$name\",\"type\":\"tool-calling\",\"model\":\"gpt-5-mini\"}"

  echo "== responses carry no agent type =="
  curl -s "$BASE_URL/api/agents" | assert_no_agent_type "/api/agents"
  curl -s "$BASE_URL/api/agents/dashboard/list" | assert_no_agent_type "/api/agents/dashboard/list"
  curl -s "$BASE_URL/api/agents?name=$encoded" | assert_no_agent_type "agent detail"
  curl -s "$BASE_URL/api/agents/$encoded/detail" | assert_no_agent_type "dashboard agent detail"
  curl -s "$BASE_URL/api/providers" | assert_no_agent_type "/api/providers models"

  expect_status 200 DELETE "$BASE_URL/api/agents?name=$encoded"
  echo "PASS agent-type-api"
}

# smoke_agent_type_strip boots a fresh sandbox seeded with pre-upgrade agent
# files and checks the one-time strip: "type" is gone from disk, the model is
# untouched, and a "workspace-manager" typed agent was still recognized as
# stale (it became a tagged agent, which the boot cleanup removes).
smoke_agent_type_strip() {
  local port="${2:-8932}"
  local dir="${TMPDIR:-/tmp}/ori-smoke-agent-type-strip-$$"
  local binary
  binary="$(cd "$(dirname "$0")/.." && pwd -P)/bin/ori-agent"
  [[ -x "$binary" ]] || fail "build first: go build -o bin/ori-agent ./cmd/server"
  [[ ! -e "$dir" ]] || fail "sandbox $dir already exists; remove it and re-run"
  mkdir -p "$dir/agents/legacy" "$dir/agents/manager" || fail "could not create $dir"
  [[ -d "$dir/agents/legacy" ]] || fail "sandbox $dir does not exist"

  printf '%s\n' '{"type":"research","role":"researcher","Settings":{"model":"claude-sonnet-5","provider":"claude","temperature":1}}' \
    >"$dir/agents/legacy/agent_settings.json"
  printf '%s\n' '{"type":"workspace-manager","Settings":{"model":"gpt-5-mini","temperature":1}}' \
    >"$dir/agents/manager/agent_settings.json"

  local base="http://localhost:$port" pid
  (cd "$dir" && HOME="$dir" ORI_DATA_DIR="$dir" PORT="$port" exec "$binary" >"$dir/server.log" 2>&1) &
  pid=$!
  trap 'kill "$pid" 2>/dev/null || true' EXIT
  local i
  for i in $(seq 1 60); do
    [[ "$(curl -s -o /dev/null -w '%{http_code}' "$base/api/agents" || true)" == "200" ]] && break
    kill -0 "$pid" 2>/dev/null || fail "server exited early; see $dir/server.log"
    sleep 1
  done
  echo "ok   server $pid up on $base (sandbox $dir)"

  if grep -l '"type"' "$dir"/agents/*/agent_settings.json 2>/dev/null; then
    fail "agent files above still carry a type key"
  fi
  echo "ok   no agent_settings.json carries a type key"

  local model
  model=$(json_field Settings.model <"$dir/agents/legacy/agent_settings.json")
  [[ "$model" == "claude-sonnet-5" ]] || fail "legacy agent model changed to '$model'"
  echo "ok   legacy agent kept model $model"

  local names
  names=$(curl -s "$base/api/agents" | python3 -c 'import sys, json; print(" ".join(a["name"] for a in json.load(sys.stdin)["agents"]))')
  [[ " $names " == *" legacy "* ]] || fail "legacy agent missing from /api/agents: $names"
  [[ " $names " != *" manager "* ]] || fail "workspace-manager typed agent survived boot cleanup: $names"
  echo "ok   workspace-manager typed agent was recognized as stale"

  kill "$pid"
  wait "$pid" 2>/dev/null || true
  trap - EXIT
  echo "PASS agent-type-strip (sandbox left at $dir)"
}

# canonical_dir resolves a directory to its real, symlink-free absolute path
# (macOS temp dirs live under /var, itself a symlink to /private/var), so a
# path this script writes and a path the server echoes back after resolving
# the same folder compare equal.
canonical_dir() {
  (cd "$1" 2>/dev/null && pwd -P)
}

# smoke_janitor_upgrade_seed creates a Downloads Janitor workspace against the
# OLD (pre-rename) binary, confirms setup against a throwaway folder inside
# the sandbox holding a few "finished" dummy files, and runs one scan. The
# workspace id is written to <sandbox>/janitor-workspace-id so a later run of
# this script against the NEW binary (see smoke_janitor_upgrade_verify) can
# find it. See tests/downloads-janitor.spec.ts for the request shapes this
# mirrors.
smoke_janitor_upgrade_seed() {
  local sandbox="$1"
  [[ -n "$sandbox" ]] || fail "usage: $0 janitor-upgrade-seed <base-url> <sandbox>"
  mkdir -p "$sandbox"

  local folder="$sandbox/janitor-inbox"
  mkdir -p "$folder"
  printf 'seed\n' >"$folder/report.pdf"
  printf 'seed\n' >"$folder/photo.jpg"
  printf 'seed\n' >"$folder/archive.zip"
  # A file must look finished for the scanner to propose it: backdated well
  # past the settling interval, same as the Playwright fixture's OLD stamp.
  local old_stamp
  old_stamp=$(date -v-6H +%Y%m%d%H%M 2>/dev/null || date -d '-6 hours' +%Y%m%d%H%M)
  touch -t "$old_stamp" "$folder"/report.pdf "$folder"/photo.jpg "$folder"/archive.zip

  echo "--- Downloads Janitor upgrade seed ($BASE_URL) ---"

  local ws
  ws=$(curl -s -X POST "$BASE_URL/api/workspaces" \
    -H 'Content-Type: application/json' \
    -d '{"name":"Janitor Upgrade Seed","description":"","template_id":"downloads-janitor","create_template_agents":true}' |
    workspace_id)
  [[ -n "$ws" ]] || fail "could not create a downloads-janitor workspace"
  echo "ok   created workspace $ws"

  local resolved_folder
  resolved_folder=$(canonical_dir "$folder")
  [[ -n "$resolved_folder" ]] || fail "could not resolve $folder"

  local setup_status
  setup_status=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    "$BASE_URL/api/workspaces/$ws/downloads-janitor/setup" \
    -H 'Content-Type: application/json' \
    -d "$(FOLDER="$resolved_folder" python3 -c 'import json,os;print(json.dumps({"path":os.environ["FOLDER"],"paused":True}))')")
  [[ "$setup_status" == "200" ]] || fail "setup confirmation => $setup_status"
  echo "ok   setup confirmed against $resolved_folder"

  local scan_status
  scan_status=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/api/workspaces/$ws/downloads-janitor/scan")
  [[ "$scan_status" == "200" ]] || fail "scan => $scan_status"
  echo "ok   ran one scan"

  printf '%s' "$ws" >"$sandbox/janitor-workspace-id"
  echo "ok   wrote workspace id to $sandbox/janitor-workspace-id"
  echo "PASS janitor upgrade seed"
}

# smoke_janitor_upgrade_verify re-reads the workspace smoke_janitor_upgrade_seed
# created, against a (presumably upgraded) binary serving the same sandbox, and
# checks the upgrade preserved everything: the confirmed folder, the pending
# batch, the file-janitor capability install, and that the retired blueprint no
# longer appears in the picker's source list.
smoke_janitor_upgrade_verify() {
  local sandbox="$1"
  [[ -n "$sandbox" ]] || fail "usage: $0 janitor-upgrade-verify <base-url> <sandbox>"
  local id_file="$sandbox/janitor-workspace-id"
  [[ -f "$id_file" ]] || fail "$id_file not found; run janitor-upgrade-seed first"
  local ws
  ws=$(cat "$id_file")
  [[ -n "$ws" ]] || fail "$id_file was empty"

  echo "--- Downloads Janitor upgrade verify ($BASE_URL, workspace $ws) ---"

  local expected_folder
  expected_folder=$(canonical_dir "$sandbox/janitor-inbox")
  [[ -n "$expected_folder" ]] || fail "could not resolve $sandbox/janitor-inbox"

  local root_path
  root_path=$(curl -s "$BASE_URL/api/workspaces/$ws/file-janitor" | json_field status.settings.root_path)
  [[ "$root_path" == "$expected_folder" ]] || fail "root_path = '$root_path', want '$expected_folder'"
  echo "ok   /file-janitor reports the confirmed folder"

  local batch_total history_total
  batch_total=$(curl -s "$BASE_URL/api/workspaces/$ws/file-janitor/batches/latest" | json_field total)
  history_total=$(curl -s "$BASE_URL/api/workspaces/$ws/file-janitor/history" | json_field total)
  [[ "${batch_total:-0}" -gt 0 || "${history_total:-0}" -gt 0 ]] ||
    fail "neither the latest batch ($batch_total) nor history ($history_total) has any entries"
  echo "ok   history or the latest batch is non-empty (batch=$batch_total, history=$history_total)"

  local installed
  installed=$(curl -s "$BASE_URL/api/workspaces/$ws/capabilities" | python3 -c '
import sys, json
d = json.load(sys.stdin)
for item in d.get("capabilities", []):
    if item.get("definition", {}).get("id") == "file-janitor":
        print(item.get("installed"))
        break
else:
    print(False)')
  [[ "$installed" == "True" ]] || fail "file-janitor capability is not installed on $ws"
  echo "ok   file-janitor capability is installed"

  local still_listed
  still_listed=$(curl -s "$BASE_URL/api/project-templates" | python3 -c '
import sys, json
d = json.load(sys.stdin)
print(any(t.get("id") == "downloads-janitor" for t in d.get("templates", [])))')
  [[ "$still_listed" == "False" ]] || fail "GET /api/project-templates still lists downloads-janitor"
  echo "ok   /api/project-templates omits downloads-janitor"
  echo "PASS janitor upgrade verify"
}

# ---------------------------------------------------------------------------
# Task-run show (tasks/prd-task-run-show.md). Start the server with
#   ORI_DEV_SCRIPTED_TASK_RUNS=1 ./scripts/demo-server.sh 8931
# so every run plays the scripted event sequence instead of calling a model.
# ---------------------------------------------------------------------------

# smoke_show_seed completes onboarding FIRST (workspaces seeded before it vanish
# on restart), then creates three workspaces that each have a Commander.
smoke_show_seed() {
  curl -s -o /dev/null -w "%{http_code} onboarding complete\n" \
    -X POST "$BASE_URL/api/onboarding/complete" -H 'Content-Type: application/json' -d '{}'
  seed_agent "Theo" "researcher" false '[]' "Looks things up"
  seed_agent "Ada" "synthesizer" false '[]' "Writes things down"
  seed_agent "Mira" "orchestrator" false '[]' "Keeps launches moving"
  local research launch notes
  research="$(seed_workspace "Research Lab" "Theo")"
  launch="$(seed_workspace "Product Launch" "Mira")"
  notes="$(seed_workspace "Field Notes" "Ada")"
  echo "research=$research"
  echo "launch=$launch"
  echo "notes=$notes"
}

# smoke_show_run creates a task (no assignee: the entry agent is the default)
# and starts it, the same two calls the New Quest composer makes. Put [fail]
# in the description to play the failure script.
smoke_show_run() {
  local workspace_id="${3:-}" description="${4:-Look into the launch checklist}" body task_id
  [[ -n "$workspace_id" ]] || fail "usage: showrun <base-url> <workspace-id> [description]"
  body=$(python3 -c 'import json, sys
print(json.dumps({"workspace_id": sys.argv[1], "description": sys.argv[2], "priority": 2}))' \
    "$workspace_id" "$description")
  task_id="$(curl -s -X POST "$BASE_URL/api/orchestration/tasks" \
    -H 'Content-Type: application/json' -d "$body" \
    | python3 -c 'import json,sys; d=json.loads(sys.stdin.read()); print(d.get("task",{}).get("id",""))')"
  [[ -n "$task_id" ]] || fail "task was not created"
  curl -s -o /dev/null -w "%{http_code} start $task_id\n" \
    -X POST "$BASE_URL/api/orchestration/tasks/execute" \
    -H 'Content-Type: application/json' -d "{\"task_id\":\"$task_id\"}"
  echo "task_id=$task_id"
}

# smoke_show_markfailed answers a blocked task with "mark failed", the way a user
# does on the task's decision page. A manual run that errors is BLOCKED, not
# failed, so this is how a "Needs a look" parcel appears for one.
smoke_show_markfailed() {
  local task_id="${3:-}"
  [[ -n "$task_id" ]] || fail "usage: showmarkfailed <base-url> <task-id>"
  curl -s -o /dev/null -w "%{http_code} mark failed $task_id\n" \
    -X POST "$BASE_URL/api/orchestration/tasks/$task_id/assist" \
    -H 'Content-Type: application/json' -d '{"action":"mark_failed"}'
}

# smoke_show_stream prints the activity stream for a few seconds, so a run's
# events and the absence of arguments/results can be read directly.
smoke_show_stream() {
  local seconds="${3:-15}"
  curl -s -N --max-time "$seconds" "$BASE_URL/api/workspace-map/activity/stream" || true
}

# smoke_show_wait blocks until the demo server answers, so a rebuild-and-restart
# can be followed by one command instead of a hand-written polling loop.
smoke_show_wait() {
  local waited=0
  until curl -sf -o /dev/null "$BASE_URL/"; do
    ((waited++ < 300)) || fail "server at $BASE_URL did not answer within 300s"
    sleep 1
  done
  echo "ok   $BASE_URL is answering"
}

# smoke_show_janitor installs File Janitor on a workspace and grants it a
# folder (created if missing), so its watcher scans become map activities.
smoke_show_janitor() {
  local workspace_id="${3:-}" folder="${4:-}"
  [[ -n "$workspace_id" && -n "$folder" ]] || fail "usage: showjanitor <base-url> <workspace-id> <folder>"
  mkdir -p "$folder"
  folder="$(canonical_dir "$folder")"
  expect_status 200 POST "$BASE_URL/api/workspaces/$workspace_id/capabilities/file-janitor/install" '{"source":"in-place"}'
  expect_status 200 POST "$BASE_URL/api/workspaces/$workspace_id/file-janitor/setup" \
    "$(FOLDER="$folder" python3 -c 'import json,os; print(json.dumps({"path": os.environ["FOLDER"]}))')"
  echo "ok   File Janitor watches $folder"
}

# smoke_show_drop moves settled files into a watched folder. The scanner only
# proposes files that look finished, and the watcher wakes on rename, so each
# file is written elsewhere, backdated six hours, then moved in. The scan runs
# after the watcher's five-minute settle window.
smoke_show_drop() {
  local folder="${3:-}"
  shift 3 || true
  [[ -n "$folder" && $# -gt 0 ]] || fail "usage: showdrop <base-url> <folder> <file-name>..."
  local staging stamp name
  staging="$(dirname "$folder")/.showdrop-staging"
  mkdir -p "$staging"
  stamp=$(date -v-6H +%Y%m%d%H%M 2>/dev/null || date -d '-6 hours' +%Y%m%d%H%M)
  for name in "$@"; do
    printf 'demo file %s\n' "$name" >"$staging/$name"
    touch -t "$stamp" "$staging/$name"
    mv "$staging/$name" "$folder/$name"
    echo "ok   dropped $name"
  done
}

# smoke_integration covers the reviewed integration floor
# (tasks/prd-reviewed-integration-latest-release.md): wait for the server, skip
# onboarding, optionally install one plugin source, then print the install
# quest's integration projection and the cached plugin update snapshot. It
# asserts nothing about versions, because the latest release moves; read the
# printed fields against the manual test guide.
smoke_integration() {
  local source="${3:-}"
  local waited=0
  until curl -sf -o /dev/null "$BASE_URL/"; do
    ((waited++ < 180)) || fail "server at $BASE_URL did not answer"
    sleep 1
  done
  curl -sf -X POST "$BASE_URL/api/onboarding/skip" >/dev/null || fail "could not skip onboarding"
  if [[ -n "$source" ]]; then
    curl -sf -X POST "$BASE_URL/api/plugins/install" -H 'Content-Type: application/json' \
      -d "$(python3 -c 'import json,sys; print(json.dumps({"source": sys.argv[1], "confirm": True}))' "$source")" |
      python3 -c 'import json,sys; p=json.load(sys.stdin).get("plugin",{}); print("installed", p.get("name"), p.get("version"), p.get("source"))' ||
      fail "install of $source failed"
  fi
  curl -sf "$BASE_URL/api/host-setup-quests/install_ori_reaper" | python3 -c '
import json, sys
journey = json.load(sys.stdin)["setup_journey"]
step = journey["steps"][0]
integration = step.get("integration") or {}
fields = ["expected_version", "minimum_version", "release_checked", "installed_version", "verified", "replacement_required", "enabled"]
print("install step", step.get("status"), step.get("reason_code") or "-", {k: integration.get(k) for k in fields})
print("actions", [a["id"] for a in step.get("actions", [])])' || fail "could not read the install quest"
  echo "updates $(curl -sf "$BASE_URL/api/plugins/updates")"
}

# smoke_reaper_blueprint prepares a fresh demo sandbox for the blueprint-aware
# Create Workspace checks (tasks/prd-blueprint-aware-create-workspace.md):
# completes onboarding FIRST (a plugin installed before it vanishes on
# restart), installs the reviewed reaper-plugin fallback commit that
# internal/reviewedintegration/entries.go records, enables it, and asserts the
# Reaper Song blueprint is listed with its required group.
smoke_reaper_blueprint() {
  local root commit ready=""
  root="$(cd "$(dirname "$0")/.." && pwd -P)"
  commit=$(grep -o 'FallbackCommit: *"[0-9a-f]*"' "$root/internal/reviewedintegration/entries.go" |
    head -1 | grep -o '[0-9a-f]\{40\}')
  [[ -n "$commit" ]] || fail "no reviewed reaper-plugin FallbackCommit in entries.go"
  for _ in $(seq 1 60); do
    if curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/api/onboarding/status" | grep -q 200; then
      ready=1
      break
    fi
    sleep 2
  done
  [[ -n "$ready" ]] || fail "no server answered at $BASE_URL within 120s"
  expect_status 200 POST "$BASE_URL/api/onboarding/complete" '{}'
  local installed
  installed=$(curl -s -X POST "$BASE_URL/api/plugins/install" \
    -H 'Content-Type: application/json' -H "Origin: $BASE_URL" \
    -d "{\"source\":\"https://github.com/johnjallday/reaper-plugin#sha=$commit\",\"confirm\":true}" |
    json_field installed)
  [[ "$installed" == "True" ]] || fail "reaper-plugin@$commit did not install"
  echo "ok   installed reaper-plugin@${commit:0:12}"
  local enabled
  enabled=$(curl -s -X POST "$BASE_URL/api/plugins/reaper-plugin/enable" \
    -H 'Content-Type: application/json' -H "Origin: $BASE_URL" -d '{}' | json_field enabled)
  [[ "$enabled" == "True" ]] || fail "reaper-plugin did not enable"
  echo "ok   enabled reaper-plugin"
  local policy
  policy=$(curl -s "$BASE_URL/api/project-templates" | python3 -c '
import sys, json
for t in json.load(sys.stdin).get("templates", []):
    if t.get("id") == "plugin:reaper-plugin:reaper-song":
        print((t.get("group_requirement") or {}).get("policy", ""))
        break')
  [[ "$policy" == "required" ]] || fail "Reaper Song is not listed with a required group (got '$policy')"
  echo "ok   Reaper Song blueprint listed (group_requirement.policy=required)"
  echo "PASS reaper blueprint ready at $BASE_URL"
}

# smoke_blueprint_details prints what a Create Workspace request stored for one
# workspace: its parent group, description, and workspace_bootstrap.
smoke_blueprint_details() {
  local ws="${3:-}"
  [[ -n "$ws" ]] || fail "usage: $0 blueprint-details <base-url> <workspace-id>"
  curl -s "$BASE_URL/api/workspaces/$ws" | python3 -c '
import sys, json
d = json.load(sys.stdin)
w = d.get("workspace", d)
print(json.dumps({
    "name": w.get("name"),
    "parent_id": w.get("parent_id"),
    "description": w.get("description"),
    "workspace_bootstrap": (w.get("shared_data") or {}).get("workspace_bootstrap"),
}, indent=2))'
}

# smoke_reaper_demo_folders writes the existing-project folders the Slice B
# demos attach (create-workspace-attach-existing-project), under a demo sandbox
# only. Each project file is a copy of the installed Reaper Song scaffold, or a
# minimal stand-in when the plugin is not installed yet.
#   music/Night Drive/Night Drive.rpp          one project file
#   music/Two Takes/Take One.rpp, Take Two.rpp several project files
#   music/No Project/notes.txt                 no project file
#   music/Bridge Sketch/Bridge Sketch.rpp      name prefill
#   music/Imported Song/Imported Song.rpp      adopt with Import Folder first
smoke_reaper_demo_folders() {
  local sandbox="${2:-}"
  [[ -n "$sandbox" && -d "$sandbox" ]] || fail "usage: $0 reaper-demo-folders <demo-sandbox-dir>"
  case "$sandbox" in
  *ori-demo.* | *smoke-*) ;;
  *) fail "refusing to write demo folders outside a demo sandbox (ori-demo.* or smoke-*): $sandbox" ;;
  esac
  local scaffold
  scaffold=$(find "$sandbox/plugins" -path '*blueprints/reaper-song/project/*.rpp' 2>/dev/null | head -1 || true)
  local music="$sandbox/music"
  mkdir -p "$music/Night Drive" "$music/Two Takes" "$music/No Project" "$music/Bridge Sketch" "$music/Imported Song"
  local target
  for target in "Night Drive/Night Drive.rpp" "Two Takes/Take One.rpp" "Two Takes/Take Two.rpp" "Bridge Sketch/Bridge Sketch.rpp" "Imported Song/Imported Song.rpp"; do
    [[ -e "$music/$target" ]] && continue
    if [[ -n "$scaffold" ]]; then
      cp "$scaffold" "$music/$target"
    else
      printf '<REAPER_PROJECT 0.1 "7.0"\n  TEMPO 120 4 4\n>\n' >"$music/$target"
    fi
  done
  [[ -e "$music/No Project/notes.txt" ]] || printf 'mix notes\n' >"$music/No Project/notes.txt"
  echo "ok   demo project folders under $music"
  find "$music" -type f | sort
}

# smoke_pick_folder answers an open native folder dialog (macOS) with <path>:
# it waits for the ori-folder-picker sheet, opens Go to Folder (Cmd-Shift-G),
# types the path, and confirms twice. Browser automation cannot click a native
# dialog, and the server must never gain a picker bypass, so demos drive the
# real dialog. Click "Choose project folder" first, then run this.
smoke_pick_folder() {
  local target="${2:-}"
  [[ -n "$target" && -d "$target" ]] || fail "usage: $0 pick-folder <existing-folder>"
  local result
  result=$(osascript - "$target" <<'APPLESCRIPT'
on run argv
  set target to item 1 of argv
  tell application "System Events"
    set found to false
    repeat 60 times
      try
        if (count of sheets of window 1 of process "ori-folder-picker") > 0 then
          set found to true
          exit repeat
        end if
      end try
      delay 0.5
    end repeat
    if not found then return "no-dialog"
    set frontmost of process "ori-folder-picker" to true
    delay 0.8
    keystroke "g" using {command down, shift down}
    delay 1.2
    -- Go to Folder remembers the last path; replace it rather than append.
    keystroke "a" using {command down}
    delay 0.3
    keystroke target
    delay 0.8
    key code 36
    delay 1.5
    key code 36
    delay 1.5
    try
      if (count of sheets of window 1 of process "ori-folder-picker") > 0 then return "still-open"
    end try
    return "chosen"
  end tell
end run
APPLESCRIPT
  )
  [[ "$result" == "chosen" ]] || fail "folder dialog was not answered ($result)"
  echo "ok   chose $target in the native folder dialog"
}

# smoke_import_folder adopts <path> as a workspace through Import Folder, the
# way the modal's Import mode does, and prints the new workspace id.
smoke_import_folder() {
  local target="${3:-}"
  [[ -n "$target" && -d "$target" ]] || fail "usage: $0 import-folder <base-url> <folder>"
  local body
  body=$(python3 -c 'import json, sys; print(json.dumps({"path": sys.argv[1], "entry_point": "smoke"}))' "$target")
  curl -s -X POST "$BASE_URL/api/workspaces/import" -H 'Content-Type: application/json' \
    -H "Origin: $BASE_URL" -d "$body" | python3 -c '
import sys, json
d = json.load(sys.stdin)
f = d.get("folder") or {}
if not f.get("id"):
    sys.exit("FAIL: import refused: " + json.dumps(d))
print("ok   imported", f.get("name"), f.get("id"))'
}

# smoke_folder_checksum prints one sha256 per file (sorted), so a before/after
# diff shows any created, removed, renamed, or modified file.
smoke_folder_checksum() {
  local dir="${2:-}"
  [[ -n "$dir" && -d "$dir" ]] || fail "usage: $0 folder-checksum <dir>"
  (cd "$dir" && find . -type f -print0 | sort -z | xargs -0 shasum -a 256)
}

# smoke_attach_details prints what an existing-project create stored for one
# workspace: its parent, typed project-entry locator, the directory reference
# that locator names, and its starter tasks.
smoke_attach_details() {
  local ws="${3:-}"
  [[ -n "$ws" ]] || fail "usage: $0 attach-details <base-url> <workspace-id>"
  curl -s "$BASE_URL/api/workspaces/$ws" | python3 -c '
import sys, json
d = json.load(sys.stdin)
w = d.get("workspace", d)
shared = w.get("shared_data") or {}
locator = shared.get("project_entry") or {}
refs = w.get("directory_references") or []
named = [r for r in refs if r.get("id") == locator.get("directory_reference_id")]
tasks = w.get("tasks") or []
print(json.dumps({
    "name": w.get("name"),
    "parent_id": w.get("parent_id"),
    "project_path": w.get("project_path"),
    "project_entry": locator,
    "attached_folder": named[0].get("path") if named else None,
    "starter_tasks": [t.get("description") for t in tasks],
}, indent=2))'
}

# smoke_blueprint_intake imports the repository's eligible fixture through the
# public API, creates a workspace from it, and verifies both the immutable
# declaration snapshot and the compiled setup-wizard step. It intentionally
# stops before uploading: this check is provider-independent and safe to run in
# a fresh isolated demo sandbox.
smoke_blueprint_intake() {
  local root fixture imported template_id created ws status name
  root="$(cd "$(dirname "$0")/.." && pwd -P)"
  fixture="$root/internal/projecttemplates/testdata/intake-eligible"
  [[ -f "$fixture/template.json" ]] || fail "blueprint intake fixture is missing: $fixture"
  name="Blueprint Intake Smoke $(date +%s)-$$"

  echo "--- Blueprint Intake declaration smoke ($BASE_URL) ---"
  imported=$(curl -sf -X POST "$BASE_URL/api/project-templates/import" \
    -H 'Content-Type: application/json' -H "Origin: $BASE_URL" \
    -d "$(FIXTURE="$fixture" NAME="$name" python3 -c 'import json,os; print(json.dumps({"path":os.environ["FIXTURE"],"name":os.environ["NAME"]}))')") ||
    fail "could not import the intake-eligible fixture"
  template_id=$(echo "$imported" | json_field template.id)
  [[ -n "$template_id" ]] || fail "template import returned no id: $imported"
  echo "ok   imported fixture as $template_id"

  created=$(curl -sf -X POST "$BASE_URL/api/workspaces" \
    -H 'Content-Type: application/json' -H "Origin: $BASE_URL" \
    -d "$(TEMPLATE_ID="$template_id" NAME="$name" python3 -c 'import json,os; print(json.dumps({"name":os.environ["NAME"],"template_id":os.environ["TEMPLATE_ID"],"create_template_agents":True}))')") ||
    fail "could not create a workspace from $template_id"
  ws=$(echo "$created" | workspace_id)
  [[ -n "$ws" ]] || fail "workspace creation returned no id: $created"
  echo "ok   created workspace $ws"

  status=$(curl -sf "$BASE_URL/api/workspaces/$ws/setup-wizard") || fail "could not read setup wizard for $ws"
  STATUS="$status" python3 - "$fixture/template.json" <<'PY'
import json, os, sys
with open(sys.argv[1]) as f:
    expected = json.load(f)["intake_requirements"][0]
status = json.loads(os.environ["STATUS"])
status = status.get("setup", status)
steps = [step for step in status.get("steps", []) if step.get("id") == "materials"]
if len(steps) != 1:
    raise SystemExit("FAIL: expected exactly one materials wizard step")
step = steps[0]
checks = {
    "kind": "intake",
    "intake_key": expected["key"],
    "intake_label": expected["label"],
    "intake_files": expected["sources"]["files"],
    "intake_accepted_extensions": expected["accepted_extensions"],
}
for key, want in checks.items():
    if step.get(key) != want:
        raise SystemExit(f"FAIL: wizard step {key}={step.get(key)!r}, want {want!r}")
print("ok   setup wizard compiled the intake step with the workspace's copied declaration")
PY
  echo "PASS blueprint intake declaration smoke"
}

# smoke_agent_files prints, for one agent in a demo sandbox, the modification
# time and a short sha256 of its definition file and of its runtime state file,
# so a before/after pair shows exactly which of the two a step rewrote. The
# definition is looked for in the data dir, the confirmed workspace root, and
# the staging root.
smoke_agent_files() {
  local sandbox="${2:-}" name="${3:-}" path found=0
  [[ -n "$sandbox" && -d "$sandbox" && -n "$name" ]] || fail "usage: $0 agent-files <sandbox> <agent-name>"
  for path in "$sandbox/agents/$name/agent_settings.json" \
    "$sandbox/Ori Workspaces/Agents/$name/agent_settings.json" \
    "$sandbox/workspace-staging/Agents/$name/agent_settings.json" \
    "$sandbox"/agent_state/*/"$name.json"; do
    [[ -f "$path" ]] || continue
    found=1
    printf '%s  %s  %s\n' "$(stat -f %m "$path")" "$(shasum -a 256 "$path" | cut -c1-16)" "${path#"$sandbox"/}"
  done
  [[ "$found" == 1 ]] || fail "no files for agent $name under $sandbox"
}

# smoke_seed_legacy_agents prepares a demo sandbox (used as both HOME and
# ORI_DATA_DIR, the wt demo layout) the way an existing user's install looks
# before agents moved into the workspace root: three agents in the data dir's
# agents/ folder, a built-in assistant, and a settings.json whose workspace
# root (the HOME-derived "Ori Workspaces") is already confirmed. Run it before
# the first start of that sandbox.
smoke_seed_legacy_agents() {
  local sandbox="${2:-}"
  [[ -n "$sandbox" ]] || fail "usage: $0 seed-legacy-agents <sandbox>"
  [[ ! -e "$sandbox/agents" ]] || fail "$sandbox already has agents; seed a fresh sandbox"
  mkdir -p "$sandbox/Ori Workspaces"
  python3 - "$sandbox" <<'PY'
import json, os, sys
sandbox = sys.argv[1]

def write(rel, doc):
    path = os.path.join(sandbox, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.write(doc if isinstance(doc, str) else json.dumps(doc, indent=2))

write("settings.json", {"workspace_root_confirmed": True})
write("agents.json", {})
write("agents/Ask Ori/agent_settings.json", {
    "role": "orchestrator", "Settings": {"model": "gpt-5-nano"},
    "metadata": {"tags": ["system", "ori:system-assistant"]}})
write("agents/Scout/agent_settings.json", {
    "role": "researcher", "Settings": {"model": "gpt-4o-mini", "provider": "openai", "system_prompt": "You scout ahead."},
    "status": "active", "statistics": {"message_count": 12, "token_usage": 3400}})
write("agents/Scout/mcp_servers.json", {"enabled_servers": ["filesystem"]})
write("agents/Scout/skills_state.json", {"skills": {"*": {"enabled": False, "trusted": False}}})
write("agents/Sleeper/agent_settings.json", {
    "role": "general", "Settings": {"model": "gpt-4o-mini", "system_prompt": "Paused for now."},
    "status": "disabled"})
write("agents/Quill/agent_settings.json", {
    "role": "general", "Settings": {"model": "gpt-4o-mini", "system_prompt": "You write."}})
PY
  echo "ok   seeded Scout, Sleeper (paused), Quill and the assistant into $sandbox/agents"
}

# smoke_confirm_root prepares a fresh demo sandbox whose Workspace Directory
# (the HOME-derived "Ori Workspaces") is already confirmed, so the demo can
# create workspaces without walking onboarding. Run before the first start.
smoke_confirm_root() {
  local sandbox="${2:-}"
  [[ -n "$sandbox" ]] || fail "usage: $0 confirm-root <sandbox>"
  [[ ! -e "$sandbox/settings.json" ]] || fail "$sandbox already has settings; use a fresh sandbox"
  mkdir -p "$sandbox/Ori Workspaces"
  printf '{\n  "workspace_root_confirmed": true\n}\n' >"$sandbox/settings.json"
  echo "ok   $sandbox/Ori Workspaces is the confirmed Workspace Directory"
}

# smoke_drop_foreign_workspace copies a workspace this install has never seen
# into a Workspace Directory, the way a zip or a sync tool would: a
# workspace.json referencing "Cartographer" and that workspace's own copy of
# the agent. Press "Rescan from disk" afterwards.
smoke_drop_foreign_workspace() {
  local root="${2:-}"
  [[ -n "$root" && -d "$root" ]] || fail "usage: $0 drop-foreign-workspace <workspace-root>"
  local folder="$root/field-notes"
  [[ ! -e "$folder" ]] || fail "$folder already exists"
  mkdir -p "$folder/agents/cartographer" "$folder/files" "$folder/notes"
  python3 - "$folder" <<'PY'
import json, os, sys
folder = sys.argv[1]
workspace = {
    "id": "ws-foreign-field-notes", "name": "Field Notes", "folder_slug": "field-notes",
    "status": "active", "shared_data": {"entry_agent_name": "Cartographer"},
    "agent_instances": [{"id": "cartographer-1", "name": "Cartographer", "instance_number": 1,
                         "node_id": "cartographer-node-1", "entry_point": True}],
    "messages": [], "tasks": [],
}
agent = {"role": "researcher", "Settings": {"model": "gpt-4o-mini",
         "system_prompt": "You map places and keep field notes."}}
with open(os.path.join(folder, "workspace.json"), "w") as f:
    json.dump(workspace, f, indent=2)
with open(os.path.join(folder, "agents", "cartographer", "config.json"), "w") as f:
    json.dump(agent, f, indent=2)
PY
  echo "ok   dropped Field Notes (agent Cartographer) into $root"
}

# smoke_root_unmounted points a stopped demo sandbox's workspace root at a
# folder that cannot be created, the way an unplugged drive behaves: the root's
# parent is read-only, so neither Ori nor the workspace store can make it.
smoke_root_unmounted() {
  local sandbox="${2:-}"
  [[ -n "$sandbox" && -f "$sandbox/settings.json" ]] || fail "usage: $0 root-unmounted <sandbox>"
  local volume="$sandbox/unmounted-volume"
  mkdir -p "$volume"
  chmod 555 "$volume"
  python3 - "$sandbox/settings.json" "$volume/Ori Workspaces" <<'PY'
import json, sys
path, root = sys.argv[1], sys.argv[2]
with open(path) as f:
    settings = json.load(f)
settings["workspace_root"] = root
settings["workspace_root_confirmed"] = True
with open(path, "w") as f:
    json.dump(settings, f, indent=2)
PY
  echo "ok   workspace root is now $volume/Ori Workspaces (parent is read-only)"
}

# smoke_agent_chat sends one chat message to a named agent and prints the start
# of the reply, so a demo can drive a real chat turn without the browser.
smoke_agent_chat() {
  local name="${3:-}" question="${4:-Reply with exactly five words.}" body
  [[ -n "$name" ]] || fail "usage: $0 agent-chat <base-url> <agent-name> [question]"
  body=$(python3 -c 'import json, sys; print(json.dumps({"question": sys.argv[2], "agent_name": sys.argv[1]}))' "$name" "$question")
  curl -s -X POST "$BASE_URL/api/chat" -H 'Content-Type: application/json' -d "$body" | head -c 400
  echo
}

# smoke_agents_root_verify checks the agents-in-the-root success metrics on a
# STOPPED demo sandbox whose Workspace Directory already holds the agent (run
# seed-legacy-agents and start the sandbox once, or create the agent in the
# UI). It puts the root under git, then starts and stops the demo server twice
# with one chat in between. Ordinary use must leave Agents/ byte-identical,
# move runtime state instead, and never log the retired snapshot wipe/restore.
smoke_agents_root_verify() {
  local sandbox="${2:-}" port="${3:-8931}" agent="${4:-Scout}"
  local root="$sandbox/Ori Workspaces" log="$sandbox/agents-root-verify.log" run server starts
  [[ -n "$sandbox" && -f "$root/Agents/$agent/agent_settings.json" ]] ||
    fail "usage: $0 agents-root-verify <sandbox> [port] [agent]   (needs <sandbox>/Ori Workspaces/Agents/<agent>/)"
  ! lsof -ti ":$port" >/dev/null 2>&1 || fail "port $port is in use; stop that server first"

  # A repeat run reuses the repository, starting from a clean Agents/.
  if [[ ! -e "$root/.git" ]]; then
    git -C "$root" init -q
  fi
  git -C "$root" add Agents
  git -C "$root" -c user.name=smoke -c user.email=smoke@example.invalid commit -qm "Agents before two starts and a chat" --allow-empty
  local state_before state_after
  state_before=$(agent_state_digest "$sandbox")
  : >"$log"
  for run in 1 2; do
    ./scripts/demo-server.sh "$port" "$sandbox" >>"$log" 2>&1 &
    server=$!
    for _ in $(seq 1 180); do
      [[ $(grep -c "Server initialized successfully" "$log" || true) -ge "$run" ]] && break
      kill -0 "$server" 2>/dev/null || fail "the demo server exited during start $run; see $log"
      sleep 1
    done
    [[ $(grep -c "Server initialized successfully" "$log" || true) -ge "$run" ]] || fail "start $run did not finish; see $log"
    if [[ "$run" == 1 ]]; then
      echo "chat $agent:"
      # A plain question: a "reply to ..." prompt is routed to messaging
      # capabilities and never reaches the model.
      BASE_URL="http://localhost:$port" smoke_agent_chat - - "$agent" "What is two plus two? Answer in one word."
    fi
    ./scripts/stop-demo-server.sh "$port"
    wait "$server" 2>/dev/null || true
  done
  state_after=$(agent_state_digest "$sandbox")
  starts=$(grep -c "Server initialized successfully" "$log" || true)

  local changes
  changes=$(git -C "$root" status --porcelain Agents/)
  [[ -z "$changes" ]] || fail "Agents/ changed after two starts and a chat:"$'\n'"$changes"
  echo "ok   Agents/ is unchanged after $starts starts and a chat (git status --porcelain is empty)"
  [[ "$state_before" != "$state_after" ]] || fail "the runtime state in $sandbox/agent_state did not change; did the chat reach a model?"
  echo "ok   runtime state changed in $sandbox/agent_state instead"
  ! grep -Eq "snapshots (restored|wiped)" "$log" || fail "the log still reports a snapshot wipe or restore; see $log"
  echo "ok   no snapshot wipe or restore in the log ($log)"
}

# agent_state_digest fingerprints every runtime state file under a sandbox.
agent_state_digest() {
  local dir="$1/agent_state"
  [[ -d "$dir" ]] || {
    echo none
    return
  }
  (cd "$dir" && find . -type f -name '*.json' -print0 | sort -z | xargs -0 shasum -a 256 | shasum -a 256)
}

case "${1:-}" in
serve) serve_isolated "${2:-8931}" "${3:-default}" ;;
agent-files) smoke_agent_files "$@" ;;
agent-chat) smoke_agent_chat "$@" ;;
seed-legacy-agents) smoke_seed_legacy_agents "$@" ;;
confirm-root) smoke_confirm_root "$@" ;;
drop-foreign-workspace) smoke_drop_foreign_workspace "$@" ;;
root-unmounted) smoke_root_unmounted "$@" ;;
agents-root-verify) smoke_agents_root_verify "$@" ;;
showseed) smoke_show_seed ;;
showrun) smoke_show_run "$@" ;;
showmarkfailed) smoke_show_markfailed "$@" ;;
showstream) smoke_show_stream "$@" ;;
showwait) smoke_show_wait ;;
showjanitor) smoke_show_janitor "$@" ;;
showdrop) smoke_show_drop "$@" ;;
attach-details) smoke_attach_details "$@" ;;
integration) smoke_integration "$@" ;;
reaper-blueprint) smoke_reaper_blueprint ;;
reaper-demo-folders) smoke_reaper_demo_folders "$@" ;;
folder-checksum) smoke_folder_checksum "$@" ;;
pick-folder) smoke_pick_folder "$@" ;;
import-folder) smoke_import_folder "$@" ;;
blueprint-details) smoke_blueprint_details "$@" ;;
blueprintintake | blueprint-intake) smoke_blueprint_intake ;;
starter) smoke_starter "$@" ;;
meetassistant) smoke_meet_assistant "$@" ;;
agent-type-api) smoke_agent_type_api ;;
agent-type-strip) smoke_agent_type_strip "$@" ;;
economyseed) smoke_economy_seed ;;
economyearn) smoke_economy_earn "$@" ;;
economyquote) smoke_economy_quote "$@" ;;
economypending) smoke_economy_pending "$@" ;;
agentseed) smoke_agentseed "${3:-default}" ;;
agentmap) smoke_agentmap ;;
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
janitor-upgrade-seed) smoke_janitor_upgrade_seed "${3:-}" ;;
janitor-upgrade-verify) smoke_janitor_upgrade_verify "${3:-}" ;;
*)
  echo "usage:" >&2
  echo "  $0 serve [port] [sandbox-name]           # run an ISOLATED demo server (Ctrl-C to stop)" >&2
  echo "  $0 agent-files <sandbox> <agent>         # agents in the root: mtime + sha of definition and state files" >&2
  echo "  $0 agent-chat <base-url> <agent> [text]  # agents in the root: send one chat turn to an agent" >&2
  echo "  $0 seed-legacy-agents <sandbox>          # agents in the root: pre-upgrade install (agents in the data dir)" >&2
  echo "  $0 root-unmounted <sandbox>              # agents in the root: point a stopped sandbox at an unreachable root" >&2
  echo "  $0 agents-root-verify <sandbox> [port] [agent] # agents in the root: 2 starts + a chat leave Agents/ unchanged" >&2
  echo "  $0 confirm-root <sandbox>                # agents in the root: fresh sandbox with a confirmed Workspace Directory" >&2
  echo "  $0 drop-foreign-workspace <root>         # agents in the root: copy in a never-seen workspace with its own agent" >&2
  echo "  $0 showseed <base-url>                   # task-run show: onboarding + 3 workspaces with Commanders" >&2
  echo "  $0 showrun <base-url> <ws> [description] # task-run show: create and start a task ([fail] fails it)" >&2
  echo "  $0 showmarkfailed <base-url> <task-id>    # task-run show: answer a blocked task with mark failed" >&2
  echo "  $0 showstream <base-url> [seconds]       # task-run show: print the activity stream" >&2
  echo "  $0 showwait <base-url>                   # task-run show: wait until the demo server answers" >&2
  echo "  $0 showjanitor <base-url> <ws> <folder>  # task-run show: install File Janitor and grant a folder" >&2
  echo "  $0 showdrop <base-url> <folder> <name>... # task-run show: move settled files in (scan runs ~5 min later)" >&2
  echo "  $0 integration <base-url> [source]       # reviewed integration floor: install a source, print the install step and updates" >&2
  echo "  $0 starter <base-url> <stage> [flags]    # starter missions: wait for the server, run a demo stage" >&2
  echo "  $0 meetassistant <base-url> <stage>      # Mission 01: onboard | status | hire [name] | demo <stage>" >&2
  echo "  $0 reaper-blueprint <base-url>           # onboard + install/enable the reviewed REAPER blueprint" >&2
  echo "  $0 blueprint-details <base-url> <ws-id>  # parent, description, workspace_bootstrap of a workspace" >&2
  echo "  $0 blueprintintake <base-url>            # import intake fixture; verify workspace snapshot + wizard step" >&2
  echo "  $0 agent-type-api <base-url>             # retired agent type: API accepts and never echoes it" >&2
  echo "  $0 agent-type-strip [port]               # retired agent type: boot strips it from a seeded sandbox" >&2
  echo "  $0 agentseed <base-url> [sandbox-name]   # fill a sandbox with a demo agent roster" >&2
  echo "  $0 agentmap <base-url>                   # Agent Map layout API checks" >&2
  echo "  $0 specialist <base-url>                 # domain-specialist onboarding API checks" >&2
  echo "  $0 economyseed <base-url>                # City Economy: walk the earning half of the loop" >&2
  echo "  $0 economyearn <base-url> [count]        # City Economy: earn Craft by chatting" >&2
  echo "  $0 economyquote <base-url> [ws] [task]   # City Economy: price a cadence change" >&2
  echo "  $0 economypending <base-url> <db> <ws> <task> [n]  # City Economy: seed pending Harvest (demo sandboxes only)" >&2
  echo "  $0 seed <base-url>                       # seed plans and print URLs to review" >&2
  echo "  $0 {plans|drafting|review|materialize|execution|slot|reconcile|policy|boundary|hardening|packaged} <base-url> <workspace-id>" >&2
  echo "  $0 janitor-upgrade-seed <base-url> <sandbox>    # seed a downloads-janitor workspace on the OLD binary" >&2
  echo "  $0 janitor-upgrade-verify <base-url> <sandbox>  # verify it survived the rename on the NEW binary" >&2
  exit 2
  ;;
esac
