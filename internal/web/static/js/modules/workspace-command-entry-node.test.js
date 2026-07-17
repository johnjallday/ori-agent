import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';

globalThis.document = { getElementById: () => null };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };

function agent(over = {}) {
  const name = over.name || 'Agent';
  return {
    key: name.toLowerCase(),
    name,
    encodedName: name,
    entry: false,
    tone: 'idle',
    destination: 'hub',
    role: { label: 'Agent' },
    status: { label: 'Idle' },
    ...over
  };
}

function makeView() {
  const view = Object.create(WorkspaceCommandView.prototype);
  view.selectedAgentKey = '';
  return view;
}

test('entry agent renders first, top-centered, ahead of the specialist grid (FR66)', () => {
  const view = makeView();
  const entry = agent({ name: 'Manager', key: 'manager', entry: true });
  const spec1 = agent({ name: 'Writer', key: 'writer' });
  const html = view.renderMapAgentUnits([spec1, entry]); // input order reversed
  const rowIdx = html.indexOf('ws-cmd-map-command-row');
  const fieldIdx = html.indexOf('ws-cmd-map-agent-field');
  assert.ok(rowIdx !== -1 && rowIdx < fieldIdx, 'command row renders before the specialist field');
  assert.ok(html.indexOf('Manager') < html.indexOf('Writer'), 'entry agent appears first in markup');
});

test('command node carries a role frame class distinct from a specialist card (FR70)', () => {
  const view = makeView();
  const entry = agent({ name: 'Manager', key: 'manager', entry: true });
  const spec = agent({ name: 'Writer', key: 'writer' });
  const html = view.renderMapAgentUnits([entry, spec]);
  assert.match(html, /is-command-node/);
  // The specialist card must NOT carry the role class.
  const specialistSection = html.slice(html.indexOf('ws-cmd-map-agent-field'));
  assert.ok(!specialistSection.includes('is-command-node'));
});

test('role frame (is-command-node) and runtime tone are independent classes (FR40)', () => {
  const view = makeView();
  const entry = agent({ name: 'Manager', key: 'manager', entry: true, tone: 'needs-input' });
  const html = view.renderMapAgentUnits([entry]);
  assert.match(html, /is-command-node/);
  assert.match(html, /needs-input/);
});

test('orchestration copy reports the specialist count, excluding the entry agent (FR68-69)', () => {
  const view = makeView();
  const entry = agent({ name: 'Manager', key: 'manager', entry: true });
  const specs = [agent({ name: 'Writer', key: 'writer' }), agent({ name: 'Editor', key: 'editor' })];
  const html = view.renderMapAgentUnits([entry, ...specs]);
  assert.match(html, /Routes work to 2 specialist agents/);
});

test('singular specialist copy reads naturally', () => {
  const view = makeView();
  const entry = agent({ name: 'Manager', key: 'manager', entry: true });
  const html = view.renderMapAgentUnits([entry, agent({ name: 'Writer', key: 'writer' })]);
  assert.match(html, /Routes work to 1 specialist agent(?!s)/);
});

test('zero specialists gets a neutral copy, not "0 specialist agents"', () => {
  const view = makeView();
  const entry = agent({ name: 'Manager', key: 'manager', entry: true });
  const html = view.renderMapAgentUnits([entry]);
  assert.match(html, /No specialist agents yet/);
});

test('accessible name on the command node includes role + orchestration copy (FR72)', () => {
  const view = makeView();
  const entry = agent({ name: 'Manager', key: 'manager', entry: true });
  const html = view.renderMapAgentUnits([entry, agent({ name: 'Writer', key: 'writer' })]);
  assert.match(html, /aria-label="Select Manager, Acting Commander\. Routes work to 1 specialist agent\. Idle"/);
});

test('missing entry agent shows a repair state, not a promoted specialist (FR77)', () => {
  const view = makeView();
  const spec = agent({ name: 'Writer', key: 'writer' });
  const html = view.renderMapAgentUnits([spec]);
  assert.match(html, /ws-cmd-map-command-repair/);
  assert.match(html, /No Commander/);
  assert.match(html, /data-cmd-add-agent/);
  assert.ok(!html.includes('is-command-node'), 'no specialist is visually promoted to the command position');
  assert.match(html, /Writer/, 'the specialist still renders in the normal grid');
});

test('entry agent is individually selectable via the same select-agent contract as specialists (FR73)', () => {
  const view = makeView();
  view.selectedAgentKey = 'manager';
  const entry = agent({ name: 'Manager', key: 'manager', entry: true });
  const html = view.renderMapAgentUnits([entry]);
  assert.match(html, /data-cmd-map-select-agent="Manager"/);
  assert.match(html, /aria-pressed="true"/);
});

test('specialists remain individually selectable in a stable grid (FR75)', () => {
  const view = makeView();
  const entry = agent({ name: 'Manager', key: 'manager', entry: true });
  const specs = [agent({ name: 'Writer', key: 'writer' }), agent({ name: 'Editor', key: 'editor' })];
  const html = view.renderMapAgentUnits([entry, ...specs]);
  assert.match(html, /data-cmd-map-select-agent="Writer"/);
  assert.match(html, /data-cmd-map-select-agent="Editor"/);
});

test('empty roster (no agents at all) keeps the existing empty state', () => {
  const view = makeView();
  const html = view.renderMapAgentUnits([]);
  assert.match(html, /No agents in this workspace/);
  assert.match(html, /data-cmd-add-agent/);
});
