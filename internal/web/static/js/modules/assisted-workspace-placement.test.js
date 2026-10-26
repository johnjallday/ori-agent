import test from 'node:test';
import assert from 'node:assert/strict';
import { placementPanelHTML, mountPlacementPanel } from './assisted-workspace-placement.js';

const draft = { name: 'Music', mission: 'Prepare a review for my incoming files.' };
const team = [{ name: 'File Curator', action: 'create' }];
const folder = { name: 'Music', path: '/synthetic/Music' };
const review = { workspace: draft, team, folder, monitoring: false, terms: {} };

test('the primary surface describes all five comprehension facts without a map', () => {
  const html = placementPanelHTML({ draft, team, folder, review, canConfirm: true });
  for (const text of [
    'Music',
    'File Curator · new agent',
    '/synthetic/Music',
    'Prepare a filing review',
    'Off · manual scans only.',
    'linked, not moved',
    'Filed/',
    'No file contents.',
    'Create workspace & scan',
    'Use suggested setup',
    'Position does not join a district'
  ])
    assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /data-placement-action="confirm" disabled/);
});

test('a visual draft is never sufficient for confirmation', () => {
  for (const view of [
    {},
    { canConfirm: true },
    { review },
    { review, canConfirm: true, busy: true }
  ]) {
    assert.match(
      placementPanelHTML({ draft, ...view }),
      /data-placement-action="confirm" disabled/
    );
  }
});

test('names, paths and draft fields are inert text, including review and messages', () => {
  const hostile = '<img src=x onerror="alert(1)">';
  const html = placementPanelHTML({
    draft: { name: hostile, mission: hostile },
    team: [{ name: hostile }],
    folder: { path: hostile },
    message: hostile
  });
  assert.doesNotMatch(html, /<img/);
  assert.match(html, /&lt;img/);
  assert.match(html, /&quot;/);
});

test('monitoring selection and reviewed schedule are distinct disclosures', () => {
  const html = placementPanelHTML({
    draft,
    monitoring: true,
    review: { ...review, monitoring: true, terms: { daily_scan_local_time: '09:00' } }
  });
  assert.match(html, /data-placement-field="monitoring" checked/);
  assert.match(html, /daily catch-up at 09:00/);
  assert.match(html, /Review the exact schedule/);
});

test('panel emits intentions only, cleans listeners and preserves the focused control', () => {
  const events = new Map();
  const actions = [];
  let focused = '';
  const host = {
    ownerDocument: { activeElement: { dataset: { placementField: 'name' } } },
    contains: () => true,
    addEventListener: (name, handler) => events.set(name, handler),
    removeEventListener: name => events.delete(name),
    querySelector: selector => ({
      focus: () => {
        focused = selector;
      }
    }),
    replaceChildren: () => {
      host.innerHTML = '';
    }
  };
  const panel = mountPlacementPanel(host, action => actions.push(action));
  panel.update({ draft, review, canConfirm: true });
  assert.equal(focused, '[data-placement-field="name"]');
  events.get('change')({ target: { dataset: { placementField: 'monitoring' }, checked: true } });
  events.get('click')({
    target: { closest: () => ({ dataset: { placementAction: 'suggested' } }) }
  });
  events.get('click')({
    target: { closest: () => ({ dataset: { placementAction: 'confirm' }, disabled: true }) }
  });
  assert.deepEqual(actions, [
    { action: 'edit', field: 'monitoring', value: true },
    { action: 'suggested' }
  ]);
  panel.destroy();
  assert.equal(events.size, 0);
  assert.equal(host.innerHTML, '');
});
