/*
 * Seeds the workspaces the Plans-retirement checks use, through the public API
 * only, so the same records exist whether or not the Plans UI can create them.
 *
 *   node scripts/seed-plan-fixtures.mjs [baseUrl]        (default http://localhost:8931)
 *
 * Point it at a `wt demo` server (isolated HOME and data); never at a real one.
 * Names carry a short stamp so re-seeding does not collide with an earlier run.
 * The last line printed is a JSON object of workspace ids and slugs.
 *
 *   empty-on        no Plans, planning ON  (what an upgraded workspace looks like)
 *   empty-off       no Plans, planning OFF (fresh workspace)
 *   active          one draft + one in-review Plan, nothing materialized
 *   approved        an approved Plan with its tickets materialized, not started
 *   revised         a Plan with two retained versions (v1 sent back, v2 in review)
 *   history         one archived (history-only) Plan, planning OFF
 *   api-created     a Plan created by a bare API call after "upgrade", no ticket
 *
 * Old-policy behavior recorded for the checklist (1.3): creating a Plan over the
 * API succeeds whether planning is ON or OFF; the setting only hid the creation
 * form on the Plans page and changed artifact paths. Recovery from any state:
 * delete the workspace (`DELETE /api/workspaces/{id}`) or discard the sandbox.
 */
const base = process.argv[2] || 'http://localhost:8931';
const stamp = Date.now().toString(36).slice(-4);

async function api(method, path, body, { allow = [] } = {}) {
  const res = await fetch(`${base}${path}`, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined
  });
  const text = await res.text();
  let payload = text;
  try {
    payload = JSON.parse(text);
  } catch {
    /* keep the text */
  }
  if (!res.ok && !allow.includes(res.status)) {
    throw new Error(`${method} ${path} -> ${res.status}: ${text.slice(0, 300)}`);
  }
  return payload;
}

async function workspace(name, planningEnabled) {
  const created = await api('POST', '/api/workspaces', { name: `${name} ${stamp}` });
  const id = (created.folder && created.folder.id) || created.id;
  const slug = (created.folder && created.folder.folder_slug) || '';
  // A preset, not a lone flag: patching only planning.enabled stores a preset
  // label ("guided") that does not describe its own values, which the settings
  // form then rewrites to "custom" on the first save. Planner is the preset that
  // enables planning; Guided is the one that leaves it off.
  await api('PATCH', `/api/workspaces/${id}/settings`, {
    preset: planningEnabled ? 'planner' : 'guided'
  });
  return { id, slug, name: `${name} ${stamp}` };
}

async function plan(ws, title, request) {
  const created = await api('POST', `/api/workspaces/${ws.id}/plans`, {
    title,
    request,
    objective: 'A small reviewable outline. Fixture only; nothing here runs.'
  });
  await api('PATCH', `/api/workspaces/${ws.id}/plans/${created.id}/draft`, {
    revision: created.draft_revision,
    objective: created.objective,
    content: {
      rationale: 'Fixture content authored locally; no model was used.',
      groups: [
        {
          id: 'g1',
          title: 'Check the brief',
          items: [
            {
              id: 'i1',
              description: 'Read the brief and note anything unclear.',
              expected_result: 'A short list of questions.',
              author: 'user'
            }
          ]
        }
      ],
      execution: { mode: 'step_through' }
    }
  });
  return created;
}

const out = {};

out.emptyOn = await workspace('Plans Fixture Empty On', true);
out.emptyOff = await workspace('Plans Fixture Empty Off', false);

out.active = await workspace('Plans Fixture Active', true);
out.active.draftPlan = (await plan(out.active, 'Draft outline', 'Outline the launch checklist')).id;
const inReview = await plan(out.active, 'Reviewed outline', 'Outline the support handoff');
await api('POST', `/api/workspaces/${out.active.id}/plans/${inReview.id}/versions`, {});
out.active.reviewPlan = inReview.id;

out.approved = await workspace('Plans Fixture Approved', true);
const approvedPlan = await plan(out.approved, 'Approved outline', 'Outline the migration');
await api('POST', `/api/workspaces/${out.approved.id}/plans/${approvedPlan.id}/versions`, {});
const contract = await api(
  'GET',
  `/api/workspaces/${out.approved.id}/plans/${approvedPlan.id}/versions/1`
);
const approval = await api(
  'POST',
  `/api/workspaces/${out.approved.id}/plans/${approvedPlan.id}/approvals`,
  {
    version: contract.version,
    content_hash: contract.content_hash,
    effect: contract.effect,
    user_id: 'fixture',
    user_name: 'Fixture',
    idempotency_key: `${approvedPlan.id}:${contract.version}:${contract.content_hash}`
  }
);
const materialized = await api(
  'POST',
  `/api/workspaces/${out.approved.id}/plans/${approvedPlan.id}/materialize`,
  { approval_id: approval.id }
);
out.approved.plan = approvedPlan.id;
out.approved.taskIds = materialized.task_ids || [];

// Two retained versions: version one was sent back for changes, the draft was
// extended, and version two is now in review. Version one stays readable.
out.revised = await workspace('Plans Fixture Revised', true);
const revisedPlan = await plan(out.revised, 'Revised outline', 'Outline the rollout');
const revisedBase = `/api/workspaces/${out.revised.id}/plans/${revisedPlan.id}`;
await api('POST', `${revisedBase}/versions`, {});
await api('POST', `${revisedBase}/decision`, {
  decision: 'request_changes',
  version: 1,
  actor: 'fixture',
  reason: 'Fixture: keep version one and extend the outline.'
});
const reopened = await api('GET', revisedBase);
const extended = structuredClone(reopened.draft);
extended.groups[0].items.push({
  id: 'i2',
  description: 'Add a rollback note to the outline.',
  expected_result: 'A short rollback paragraph.',
  author: 'user'
});
await api('PATCH', `${revisedBase}/draft`, {
  revision: reopened.draft_revision,
  objective: reopened.objective,
  content: extended
});
await api('POST', `${revisedBase}/versions`, {});
out.revised.plan = revisedPlan.id;

out.history = await workspace('Plans Fixture History', false);
const archivedPlan = await plan(out.history, 'Old outline', 'Outline the retired project');
await api('POST', `/api/workspaces/${out.history.id}/plans/${archivedPlan.id}/archive`, {
  reason: 'Fixture: history-only workspace.',
  actor: 'fixture'
});
out.history.plan = archivedPlan.id;

out.apiCreated = await workspace('Plans Fixture Api Created', false);
const bare = await api('POST', `/api/workspaces/${out.apiCreated.id}/plans`, {
  request: 'Created by an external client; no ticket yet.',
  source: 'api'
});
out.apiCreated.plan = bare.id;

console.error('plan fixtures seeded: ' + Object.keys(out).join(', '));
console.log(JSON.stringify(out));
