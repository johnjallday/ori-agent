import test from 'node:test';
import assert from 'node:assert/strict';
import {
  HomeResourceWorkspacesPanel,
  resourceWorkspaceView,
  setUpURL
} from './home-resource-workspaces.js';

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
