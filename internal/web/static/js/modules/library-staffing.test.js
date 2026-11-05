import test from 'node:test';
import assert from 'node:assert/strict';
import {
  addSharedAssistant,
  loadProjectStaffing,
  projectStaffingOffer,
  sharedAssistantOfferHTML
} from './library-staffing.js';

const DIGEST = 'a'.repeat(64);
const base = {
  eligible: true,
  home_id: 'home-1',
  home_name: 'Music Production Home',
  entry_id: 'e1',
  workspace_id: 'ws-1',
  connected: true,
  filled: false,
  role_label: 'REAPER Assistant',
  agent_name: 'REAPER Assistant',
  team_digest: DIGEST
};

function respond(status, body) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

test('loadProjectStaffing reads only, and is null for anything not eligible', async () => {
  const seen = [];
  const view = await loadProjectStaffing('ws 1', async (url, init) => {
    seen.push([url, init.method || 'GET']);
    return respond(200, { ...base, plan: 'join' });
  });
  assert.equal(view.plan, 'join');
  assert.deepEqual(seen, [['/api/workspaces/ws%201/library-staffing', 'GET']]);
  assert.equal(
    await loadProjectStaffing('ws-2', async () => respond(200, { eligible: false })),
    null
  );
  assert.equal(await loadProjectStaffing('ws-3', async () => respond(404, {})), null);
  assert.equal(
    await loadProjectStaffing('ws-4', async () => {
      throw new Error('offline');
    }),
    null
  );
  assert.equal(await loadProjectStaffing('', async () => respond(200, base)), null);
});

test('projectStaffingOffer names the effect, keeps blockers as reasons, and offers nothing when filled', () => {
  const join = projectStaffingOffer({ ...base, plan: 'join' });
  assert.equal(join.canAdd, true);
  assert.equal(join.label, 'Add the shared REAPER Assistant');
  assert.match(join.line, /No new agent is made, no ticket runs and no model is called/);
  const create = projectStaffingOffer({ ...base, plan: 'create' });
  assert.equal(create.canAdd, true);
  assert.match(create.line, /^Creates Music Production Home's shared REAPER Assistant/);
  const off = projectStaffingOffer({
    ...base,
    plan: 'off',
    message: 'Adding the shared assistant is switched off on this Home.'
  });
  assert.equal(off.canAdd, false);
  assert.match(off.blocked, /switched off/);
  assert.equal(projectStaffingOffer({ ...base, filled: true, plan: '' }), null);
  assert.equal(projectStaffingOffer(null), null);
  assert.equal(projectStaffingOffer({ eligible: false }), null);
});

test('sharedAssistantOfferHTML escapes names and marks the one action', () => {
  const html = sharedAssistantOfferHTML(
    projectStaffingOffer({ ...base, plan: 'join', agent_name: '<b>Mix</b>' })
  );
  assert.match(html, /data-shared-assistant-add/);
  assert.match(html, /&lt;b&gt;Mix&lt;\/b&gt;/);
  assert.doesNotMatch(html, /<b>Mix<\/b>/);
  assert.match(html, /role="status"/);
  const blocked = sharedAssistantOfferHTML(
    projectStaffingOffer({ ...base, plan: 'consent_stale', message: 'Review the update.' })
  );
  assert.match(blocked, /data-shared-assistant-blocked/);
  assert.doesNotMatch(blocked, /data-shared-assistant-add/);
  assert.equal(sharedAssistantOfferHTML(null), '');
});

test('addSharedAssistant posts only the request id and the shown approval', async () => {
  const seen = [];
  const ok = await addSharedAssistant(
    'ws-1',
    { ...base, plan: 'join' },
    {
      requestId: 'r-1',
      fetchImpl: async (url, init) => {
        seen.push([url, init.method, JSON.parse(init.body)]);
        return respond(200, { staffing: { state: 'joined', agent_name: 'REAPER Assistant' } });
      }
    }
  );
  assert.deepEqual(ok, { ok: true, staffing: { state: 'joined', agent_name: 'REAPER Assistant' } });
  assert.deepEqual(seen, [
    ['/api/workspaces/ws-1/library-staffing', 'POST', { request_id: 'r-1', team_digest: DIGEST }]
  ]);
  const refused = await addSharedAssistant('ws-1', base, {
    requestId: 'r-2',
    fetchImpl: async () => respond(409, { reason: 'needs_model', error: 'Set up a model first.' })
  });
  assert.deepEqual(refused, { ok: false, reason: 'needs_model', message: 'Set up a model first.' });
  const lost = await addSharedAssistant('ws-1', base, {
    requestId: 'r-3',
    fetchImpl: async () => {
      throw new Error('network');
    }
  });
  assert.equal(lost.ok, false);
  assert.equal(lost.reason, 'unknown');
});
