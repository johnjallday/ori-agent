import test from 'node:test';
import assert from 'node:assert/strict';
import {
  HomeResourceWorkspacesPanel,
  catalogLines,
  resourceWorkspaceView,
  setUpURL,
  templatesEvidence
} from './home-resource-workspaces.js';
import { catalogTemplatesNote } from './home-profile.js';

const CATALOG = {
  revision: 3,
  provider: { available: true },
  categories: [
    {
      id: 'templates',
      label: 'Templates',
      present: 12,
      state: 'inspected',
      inspected_at: '2026-10-21T09:00:00Z',
      profile_row: 'templates'
    },
    { id: 'scripts', label: 'Scripts', present: 0, state: 'access_withdrawn' },
    { id: 'audio_plugins', label: 'Audio Plugins', present: 0, state: 'not_inspected' }
  ]
};

test('the Home card counts what was found and how fresh it is', () => {
  const lines = catalogLines(CATALOG, { locale: 'en-US', timeZone: 'UTC' });
  assert.match(lines[0], /^Templates: 12 found · Inspected Oct 21, 2026/);
  assert.equal(lines[1], 'Scripts: 0 found · Access withdrawn');
  assert.equal(lines[2], 'Audio Plugins: Not inspected');
  const offline = catalogLines({ ...CATALOG, provider: { available: false } });
  assert.match(offline.at(-1), /Unavailable now/);
});

test('the profile templates row defers to the Studio only after a current read', () => {
  const studio = {
    attachment: { label: 'REAPER Studio' },
    workspace: { id: 's1', url: '/workspaces/reaper-studio' },
    catalog: CATALOG
  };
  const evidence = templatesEvidence([studio]);
  assert.deepEqual(evidence, {
    label: 'REAPER Studio',
    url: '/workspaces/reaper-studio/catalog',
    present: 12,
    state: 'inspected',
    inspectedAt: '2026-10-21T09:00:00Z'
  });
  assert.match(
    catalogTemplatesNote(evidence, { locale: 'en-US', timeZone: 'UTC' }),
    /^12 templates in REAPER Studio, inspected Oct 21, 2026\.$/
  );
  const notYet = {
    ...studio,
    catalog: { ...CATALOG, categories: [{ ...CATALOG.categories[0], state: 'not_inspected' }] }
  };
  assert.equal(templatesEvidence([notYet]), null);
  assert.equal(templatesEvidence([{ ...studio, workspace: null }]), null);
});

const ATTACHMENT = {
  provider_plugin_id: 'reaper-plugin',
  blueprint_id: 'reaper-studio',
  label: 'REAPER Studio',
  description: 'Your studio catalog.'
};

test('set up opens Create Workspace with the blueprint and this Home locked', () => {
  const url = new URL(setUpURL('plugin:reaper-plugin:reaper-studio', 'home-1'), 'http://ori.test');
  assert.equal(url.pathname, '/');
  assert.equal(url.searchParams.get('create'), '1');
  assert.equal(url.searchParams.get('blueprint'), 'plugin:reaper-plugin:reaper-studio');
  assert.equal(url.searchParams.get('parent'), 'home-1');
});

test('a Home without a Studio offers Set up in the package words, and says nothing is read', () => {
  const view = resourceWorkspaceView(
    {
      attachment: ATTACHMENT,
      state: 'not_created',
      template_id: 'plugin:reaper-plugin:reaper-studio'
    },
    'home-1'
  );
  assert.equal(view.label, 'REAPER Studio');
  assert.equal(view.description, 'Your studio catalog.');
  assert.match(view.note, /Nothing is read until you inspect it/);
  assert.equal(view.actions.length, 1);
  assert.equal(view.actions[0].text, 'Set up REAPER Studio');
  assert.match(view.actions[0].href, /parent=home-1/);
});

test('an existing Studio is opened, never set up again', () => {
  const view = resourceWorkspaceView(
    {
      attachment: ATTACHMENT,
      state: 'available',
      workspace: { id: 's1', name: 'REAPER Studio', url: '/workspaces/reaper-studio' }
    },
    'home-1'
  );
  assert.deepEqual(
    view.actions.map(action => [action.text, action.href]),
    [['Open REAPER Studio', '/workspaces/reaper-studio/catalog']]
  );
});

test('provider states name what is missing and keep an existing Studio openable', () => {
  const studio = { id: 's1', name: 'REAPER Studio', url: '/workspaces/reaper-studio' };
  for (const [state, note] of [
    ['provider_missing', /is not installed/],
    ['provider_disabled', /switched off/],
    ['provider_incompatible', /Update the plugin/]
  ]) {
    const without = resourceWorkspaceView({ attachment: ATTACHMENT, state }, 'home-1');
    assert.match(without.note, note, state);
    assert.deepEqual(
      without.actions.map(action => action.href),
      ['/plugins'],
      state
    );
    assert.ok(!without.actions.some(action => /Set up/.test(action.text)), state);
    const withStudio = resourceWorkspaceView(
      { attachment: ATTACHMENT, state, workspace: studio },
      'home-1'
    );
    assert.equal(withStudio.actions[0].href, '/workspaces/reaper-studio/catalog', state);
  }
});

test('not_created without a template offers no action', () => {
  const view = resourceWorkspaceView({ attachment: ATTACHMENT, state: 'not_created' }, 'home-1');
  assert.deepEqual(view.actions, []);
});

function fakeDocument() {
  const make = tag => ({
    tag,
    children: [],
    className: '',
    textContent: '',
    href: '',
    hidden: false,
    append(...nodes) {
      this.children.push(...nodes);
    },
    replaceChildren(...nodes) {
      this.children = nodes;
    }
  });
  return { createElement: make, addEventListener() {}, getElementById: () => null, make };
}

function texts(node) {
  return [node.textContent, ...node.children.flatMap(texts)].filter(Boolean);
}

test('the panel renders one card from the summary and hides itself when there is none', async () => {
  const doc = fakeDocument();
  const root = doc.make('div');
  const requests = [];
  const respond = rows => async url => {
    requests.push(url);
    return { ok: true, json: async () => ({ data: { resource_workspaces: rows } }) };
  };
  const panel = new HomeResourceWorkspacesPanel({
    workspaceId: 'home-1',
    root,
    documentImpl: doc,
    fetchImpl: respond([
      {
        attachment: ATTACHMENT,
        state: 'not_created',
        template_id: 'plugin:reaper-plugin:reaper-studio'
      }
    ])
  });
  await panel.init();
  assert.equal(requests[0], '/api/workspaces/home-1/assistant-program/resource-workspaces');
  assert.equal(root.hidden, false);
  assert.equal(root.children.length, 1);
  assert.ok(texts(root).includes('Set up REAPER Studio'));

  panel.fetchImpl = respond([]);
  await panel.load();
  assert.equal(root.hidden, true);
  assert.equal(root.children.length, 0);
});

test('an unavailable summary renders nothing', async () => {
  const doc = fakeDocument();
  const root = doc.make('div');
  const panel = new HomeResourceWorkspacesPanel({
    workspaceId: 'home-1',
    root,
    documentImpl: doc,
    fetchImpl: async () => ({ ok: false, json: async () => ({}) })
  });
  await panel.init();
  assert.equal(root.hidden, true);
});
