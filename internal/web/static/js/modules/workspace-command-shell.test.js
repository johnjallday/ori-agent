import test from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';
import { AssistantProgramEntry } from './assistant-program-entry.js';
import { workspaceNavigationHTML } from './workspace-command-chrome.js';

// These contracts deliberately exercise the existing owners, not prototype state.
test('declared destinations use their own host, never a nested Plans insertion anchor', () => {
  const previous = globalThis.document;
  const inserted = [];
  const plans = { parentNode: {} };
  const host = {
    querySelector: () => null,
    appendChild: node => inserted.push(node)
  };
  const switcher = {
    querySelector: selector => (selector === 'a[href$="/plans"]' ? plans : null),
    insertBefore(_node, reference) {
      assert.equal(reference, null, 'Plans is no longer a direct child of the switcher');
    }
  };
  globalThis.document = {
    querySelector: selector => (selector === '[data-cmd-program-destinations]' ? host : switcher),
    createElement: () => ({ dataset: {}, setAttribute() {} })
  };
  try {
    const entry = new AssistantProgramEntry({ workspaceId: 'w', workspaceSlug: 'research' });
    entry.visible = true;
    entry.program = {
      available: true,
      is_station: true,
      home_profile_title: 'Research profile',
      declaration: { station_name: '<Research & planning>' }
    };
    entry.render();
    assert.equal(inserted.length, 1, 'one declaration-owned Home, not a duplicate profile link');
    assert.equal(inserted[0].textContent, '<Research & planning>');
    assert.equal(inserted[0].href, '/workspaces/research/assistant');
  } finally {
    globalThis.document = previous;
  }
});

test('repainting an open config owner does not reinitialize its editable fields', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  let loads = 0;
  const config = { classList: { add() {} } };
  Object.assign(view, {
    statModalSection: 'intent',
    statModalEl: { hidden: false, querySelector: () => ({}) },
    mountSharedSurface: () => config,
    showConfigTab() {},
    expandMountedConfig() {},
    refreshConfigData() {
      loads++;
    }
  });
  view.syncConfigModalSurface();
  view.syncConfigModalSurface();
  assert.equal(loads, 1, 'background repaint must not reset an intent draft');
});

test('a detached shared-header opener resolves to its live replacement', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  const opener = {
    isConnected: false,
    hasAttribute: name => name === 'data-cmd-settings',
    getAttribute: () => ''
  };
  const replacement = { focus() {} };
  view.container = {
    querySelector: selector => (selector === '[data-cmd-settings]' ? replacement : null),
    querySelectorAll: () => []
  };
  assert.equal(view.resolveStatModalTrigger(opener), replacement);
  // A connected item is still hidden after its Settings disclosure closes.
  opener.isConnected = true;
  assert.equal(view.resolveStatModalTrigger(opener), replacement);
});

test('Overview keeps the details key; primary modes and secondary native links stay distinct', () => {
  const html = workspaceNavigationHTML({
    mode: 'details',
    dashboard: true,
    plans: '<a href="/workspaces/w/plans">Plans</a>',
    canvas: '/workspaces/w/canvas'
  });
  assert.match(html, /data-cmd-view-mode="details" aria-pressed="true">Overview/);
  for (const key of ['map', 'tickets', 'dashboard'])
    assert.match(html, new RegExp(`data-cmd-view-mode="${key}"`));
  assert.match(html, /data-cmd-program-destinations/);
  assert.match(
    html,
    /<details[^>]*data-cmd-disclosure="views"[\s\S]*href="\/workspaces\/w\/plans"[\s\S]*href="\/workspaces\/w\/canvas"/
  );
  assert.doesNotMatch(html, /data-cmd-view-mode="(?:plans|canvas)"/);
  assert.doesNotMatch(workspaceNavigationHTML({ mode: 'map' }), /data-cmd-view-mode="dashboard"/);
});

test('every shared header has one real Give work door, Settings and literal identity', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  view.page = {
    workspaceId: 'w',
    workspaceSlug: 'w',
    workspace: { name: '<img src=x>', tags: [] }
  };
  for (const mode of ['details', 'map', 'tickets', 'dashboard']) {
    view.viewMode = mode;
    const html = view.commandBarHTML(view.page.workspace, view.page.workspace.name, '', {});
    assert.equal((html.match(/data-cmd-give-work/g) || []).length, 1);
    assert.equal((html.match(/data-cmd-settings aria-haspopup/g) || []).length, 1);
    assert.match(html, /&lt;img src=x&gt;/);
    assert.doesNotMatch(html, /No description|No tags|ws-cmd-readout/);
    assert.match(html, /data-cmd-resources aria-haspopup="dialog"/);
  }
});

test('Give work passes the actual clicked opener to the capture owner without creating work', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  const handlers = {};
  const root = {
    addEventListener: (name, fn) => {
      handlers[name] = fn;
    }
  };
  const calls = [];
  view.page = { showAddTaskModal: options => calls.push(options) };
  view.bindIdentityControls(root);
  const opener = {};
  handlers.click({
    target: { closest: selector => (selector === '[data-cmd-give-work]' ? opener : null) }
  });
  assert.deepEqual(calls, [{ trigger: opener }]);
});
