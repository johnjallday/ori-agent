import assert from 'node:assert/strict';
import test from 'node:test';
import { previewBlocker, readFinderSelection, resultLabel } from './music-map-finder.js';
const row = { id: 'e', workspace_id: 'w', connection: 'connected' };
test('setup preview needs an openable catalog entry and a writable Home, not just a format', () => {
  const detail = {
    row: {
      connection: 'catalog_only',
      last_observed_availability: 'ambiguous',
      can_open: true,
      format: 'reaper'
    },
    provider_read_only: false
  };
  assert.equal(previewBlocker(detail), '');
  assert.match(previewBlocker({ ...detail, provider_read_only: true }), /Home/);
  for (const [field, value, reason] of [
    ['connection', 'needs_review', /link/],
    ['last_observed_availability', 'revoked_source', /permission/],
    ['can_open', false, /recorded format/]
  ])
    assert.match(previewBlocker({ ...detail, row: { ...detail.row, [field]: value } }), reason);
});
const client = (row, workspace_id) => ({ request: async () => ({ row, workspace_id }) });
test('never follows a reconnected entry, a same-name replacement, or a card for another Home', async () => {
  for (const detail of [
    client(row, 'another'),
    client({ ...row, id: 'other' }, 'w'),
    client({ ...row, connection: 'needs_review' })
  ])
    await assert.rejects(
      readFinderSelection('h', row, detail, undefined, () => assert.fail()),
      /link changed/
    );
  for (const card of [
    { linked: false },
    { linked: true, home_id: 'foreign', entry_id: 'e' },
    { linked: true, home_id: 'h', entry_id: 'other' }
  ])
    await assert.rejects(
      readFinderSelection('h', row, client(row, 'w'), undefined, async () => Response.json(card)),
      /link changed/
    );
});
test('catalog preview reads entry metadata only; unknown stage does not imply progress', async () => {
  const entry = { id: 'demo', connection: 'catalog_only', folder_label: 'Albums / Demo' };
  const selection = await readFinderSelection('h', entry, client(entry), undefined, () =>
    assert.fail()
  );
  assert.equal(selection.view, undefined);
  assert.equal(resultLabel(entry), 'Library only · Albums / Demo · Stage unknown');
});
