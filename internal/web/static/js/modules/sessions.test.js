import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import {
  loadInitialWorkspaceTree,
  loadOnboardingStatus,
  onboardingGateDecision,
  resetOnboardingGateForTests
} from './onboarding-gate.js';

const source = readFileSync(new URL('./sessions.js', import.meta.url), 'utf8');

function loadSessionManager(fetchImpl = async () => ({ ok: true, json: async () => ({}) })) {
  const window = {};
  const document = { addEventListener() {} };
  vm.runInNewContext(
    source,
    { window, document, fetch: fetchImpl, console },
    { filename: 'sessions.js' }
  );
  return window.sessionManager;
}

class PreviewElement {
  constructor(tag = 'div') {
    this.tagName = String(tag).toUpperCase();
    this.hidden = false;
    this.className = '';
    this.children = [];
    this._text = '';
  }
  set textContent(value) {
    this._text = String(value ?? '');
    this.children = [];
  }
  get textContent() {
    return this._text + this.children.map(child => child.textContent).join('');
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
}

function loadSessionManagerWithSetupPreview() {
  const elements = new Map();
  for (const id of [
    'workspaceSetupPreview',
    'workspaceSetupPreviewList',
    'workspaceSetupPreviewNote',
    'workspaceSetupPreviewEyebrow',
    'workspaceSetupPreviewTitle'
  ]) {
    elements.set(id, new PreviewElement(id === 'workspaceSetupPreviewList' ? 'ul' : 'div'));
  }
  const calls = [];
  const document = {
    addEventListener() {},
    getElementById: id => elements.get(id) || null,
    createElement: tag => new PreviewElement(tag)
  };
  const window = {};
  vm.runInNewContext(
    source,
    {
      window,
      document,
      fetch: async (...args) => {
        calls.push(args);
        return { ok: true, json: async () => ({}) };
      },
      console
    },
    { filename: 'sessions.js' }
  );
  return { manager: window.sessionManager, elements, calls };
}

// loadSessionManagerWithModal adds just enough DOM for showAddWorkspaceModal:
// the modal element it reads and a Bootstrap stub that records the show call.
function loadSessionManagerWithModal() {
  const window = {};
  const modalElement = { dataset: {} };
  const shown = [];
  const document = {
    addEventListener() {},
    getElementById: id => (id === 'addFolderModal' ? modalElement : null)
  };
  const bootstrap = {
    Modal: class {
      constructor(element) {
        this.element = element;
      }
      show() {
        shown.push(this.element);
      }
    }
  };
  vm.runInNewContext(
    source,
    {
      window,
      document,
      bootstrap,
      fetch: async () => ({ ok: true, json: async () => ({}) }),
      console
    },
    { filename: 'sessions.js' }
  );
  return { manager: window.sessionManager, modalElement, shown };
}

test('runtime contract review lists modes, immediate behavior, and post-create setup without probes', () => {
  const { manager, elements, calls } = loadSessionManagerWithSetupPreview();
  const template = {
    id: 'fixture-runtime',
    runtime_requirements: {
      schema_version: 1,
      operating_modes: [
        {
          id: 'limited',
          label: 'File-only',
          description: 'Edit the project files.',
          requires: []
        },
        {
          id: 'assisted',
          label: 'Assisted',
          description: 'Work with project files while live control is configured.',
          requires: ['local_control']
        }
      ],
      requirements: [
        {
          key: 'local_control',
          label: '<b>Local control</b>',
          description: 'Configure the external application.',
          disclosure: 'The selected agent receives narrow local access.',
          adapter: 'fixture_runtime'
        }
      ]
    }
  };

  manager.renderSetupPreview(template);

  const panel = elements.get('workspaceSetupPreview');
  const list = elements.get('workspaceSetupPreviewList');
  assert.equal(panel.hidden, false);
  assert.equal(
    elements.get('workspaceSetupPreviewEyebrow').textContent,
    'Supported operating modes'
  );
  assert.equal(
    elements.get('workspaceSetupPreviewTitle').textContent,
    'What works now and what needs setup'
  );
  assert.equal(list.children.length, 2);
  assert.match(list.children[0].textContent, /File-only/);
  assert.match(list.children[0].textContent, /Works immediatelyEdit the project files\./);
  assert.match(list.children[0].textContent, /Setup after creationNo additional runtime setup\./);
  assert.match(list.children[1].textContent, /Assisted/);
  assert.match(
    list.children[1].textContent,
    /Works immediatelyProject-file work remains available/
  );
  assert.match(list.children[1].textContent, /Setup after creation<b>Local control<\/b>/);
  assert.match(list.children[1].textContent, /Configure the external application\./);
  assert.match(list.children[1].textContent, /narrow local access/);
  assert.match(elements.get('workspaceSetupPreviewNote').textContent, /preview only/i);
  assert.equal(calls.length, 0, 'rendering creation disclosure must make no probe request');
});

test('runtime review hides for a no-contract blueprint and fails visibly for an invalid contract', () => {
  const { manager, elements } = loadSessionManagerWithSetupPreview();
  manager.renderSetupPreview({ id: 'plain' });
  assert.equal(elements.get('workspaceSetupPreview').hidden, true);

  const broken = {
    id: 'broken',
    runtime_requirements_error: 'invalid runtime requirements: unknown adapter'
  };
  manager.renderSetupPreview(broken);
  assert.equal(elements.get('workspaceSetupPreview').hidden, false);
  assert.match(elements.get('workspaceSetupPreviewList').textContent, /cannot be read/i);
  assert.match(elements.get('workspaceSetupPreviewList').textContent, /unknown adapter/i);
});

test('a Map-origin create flags the existing modal rather than opening a second form (#292 FR-51)', () => {
  const { manager, modalElement, shown } = loadSessionManagerWithModal();

  manager.showAddWorkspaceModal({ mapOrigin: true, entryPoint: 'workspace_map_build' });
  assert.equal(modalElement.dataset.pendingMapOrigin, 'true');
  assert.equal(modalElement.dataset.pendingEntryPoint, 'workspace_map_build');
  assert.equal(shown.length, 1, 'the one existing Create Workspace modal was shown');

  // An ordinary create must not inherit the flag from a previous map build.
  manager.showAddWorkspaceModal({});
  assert.equal(
    'pendingMapOrigin' in modalElement.dataset,
    false,
    'a normal create is not a map build'
  );
});

test('workspace post-create action keeps the standard workspace destination by default', async () => {
  const manager = loadSessionManager();
  const result = await manager.applyWorkspacePostCreateAction('workspace-uuid', 'marketing site');

  assert.equal(result.applied, false);
  assert.equal(result.destination, '/workspaces/marketing%20site');
});

test('Personal HQ import designates the imported workspace and completes onboarding', async () => {
  const calls = [];
  const manager = loadSessionManager(async (url, options = {}) => {
    calls.push({ url, options });
    return { ok: true, json: async () => ({ success: true }) };
  });
  manager.workspacePostCreateAction = 'designate_personal_hq';

  const result = await manager.applyWorkspacePostCreateAction('hq-workspace');

  assert.equal(result.applied, true);
  assert.equal(result.destination, '/workspaces?view=map&focus=personal-hq');
  assert.equal(calls.length, 2);
  assert.equal(calls[0].url, '/api/personal-hq/replace');
  assert.deepEqual(JSON.parse(calls[0].options.body), { workspace_id: 'hq-workspace' });
  assert.equal(calls[1].url, '/api/personal-hq/onboarding-state');
  assert.deepEqual(JSON.parse(calls[1].options.body), { state: 'completed' });
});

test('pending onboarding prevents the session manager from fetching workspaces', async () => {
  resetOnboardingGateForTests(async () => ({
    ok: true,
    json: async () => ({ needs_onboarding: true })
  }));
  const calls = [];
  const manager = loadSessionManager(async url => {
    calls.push(url);
    return { ok: true, json: async () => ({ folders: [] }) };
  });
  manager.onboardingGate = { loadOnboardingStatus, onboardingGateDecision };
  manager.updateSessionsEmptyState = () => {};

  assert.equal(await manager.canHydrateWorkspaceData(), false);
  await manager.loadFolders();
  assert.deepEqual(calls, []);
  assert.equal(manager.folders.length, 0);
});

test('completed onboarding preserves normal session-manager workspace loading', async () => {
  resetOnboardingGateForTests(async () => ({
    ok: true,
    json: async () => ({ needs_onboarding: false, completed: true })
  }));
  const calls = [];
  const manager = loadSessionManager(async url => {
    calls.push(url);
    return { ok: true, json: async () => ({ folders: [] }) };
  });
  manager.onboardingGate = { loadOnboardingStatus, onboardingGateDecision };
  manager.updateSessionsEmptyState = () => {};
  manager.renderFolderTree = () => {};

  assert.equal(await manager.canHydrateWorkspaceData(), true);
  await manager.loadFolders();
  assert.deepEqual(calls, ['/api/workspaces?tree=true']);
});

// --- Review receipt: blueprint owner/version + session recovery (Group 4) ---

test('the receipt names a built-in blueprint’s owner and shipped version', () => {
  const manager = loadSessionManager();
  assert.equal(
    manager.workspaceReceiptOwnerLine({ builtin: true, builtin_version: 3 }),
    'Owner: Built-in blueprint (v3)'
  );
  assert.equal(manager.workspaceReceiptOwnerLine({ builtin: true }), 'Owner: Built-in blueprint');
});

test('the receipt names a plugin owner and its version', () => {
  const manager = loadSessionManager();
  assert.equal(
    manager.workspaceReceiptOwnerLine({
      plugin_owner: { plugin_id: 'studio-tools', plugin_version: '1.4.0' }
    }),
    'Owner: studio-tools plugin (v1.4.0)'
  );
  assert.equal(
    manager.workspaceReceiptOwnerLine({ plugin_owner: { plugin_id: 'studio-tools' } }),
    'Owner: studio-tools plugin'
  );
});

test('the receipt names a user template, and names nothing for Blank', () => {
  const manager = loadSessionManager();
  assert.equal(manager.workspaceReceiptOwnerLine({ id: 'mine' }), 'Owner: Your template');
  assert.equal(manager.workspaceReceiptOwnerLine({ blank: true }), '');
  assert.equal(manager.workspaceReceiptOwnerLine(null), '');
});

// loadSessionManagerWithProjectTemplateCard runs sessions.js with a stub
// ProjectTemplateCard on its vm-local `window`, so
// workspaceReceiptSessionRecoveryLine reads a controlled session log instead
// of the real picker module.
function loadSessionManagerWithProjectTemplateCard(getSelectedSessionRecovery) {
  const window = { ProjectTemplateCard: { getSelectedSessionRecovery } };
  const document = { addEventListener() {} };
  vm.runInNewContext(
    source,
    { window, document, fetch: async () => ({ ok: true, json: async () => ({}) }), console },
    { filename: 'sessions.js' }
  );
  return window.sessionManager;
}

test('the receipt states a completed session recovery for the selected blueprint', () => {
  const manager = loadSessionManagerWithProjectTemplateCard(() => ({
    pluginName: 'owner-plugin',
    action: 'install_plugin',
    completed: true
  }));
  assert.equal(
    manager.workspaceReceiptSessionRecoveryLine(),
    'Installed and enabled owner-plugin during this session.'
  );
});

test('the receipt states an enable-only session recovery distinctly from install', () => {
  const manager = loadSessionManagerWithProjectTemplateCard(() => ({
    pluginName: 'owner-plugin',
    action: 'enable_plugin',
    completed: true
  }));
  assert.equal(
    manager.workspaceReceiptSessionRecoveryLine(),
    'Enabled owner-plugin during this session.'
  );
});

test('a partial session recovery is stated as unfinished, never as success', () => {
  const manager = loadSessionManagerWithProjectTemplateCard(() => ({
    pluginName: 'owner-plugin',
    action: 'install_plugin',
    completed: false
  }));
  const line = manager.workspaceReceiptSessionRecoveryLine();
  assert.match(line, /not finished yet/);
  assert.doesNotMatch(line, /^Installed and enabled/);
});

test('no session recovery line when nothing was done this session', () => {
  const manager = loadSessionManager();
  assert.equal(manager.workspaceReceiptSessionRecoveryLine(), '');
});

test('session bootstrap consumes the shared initial workspace tree', async () => {
  resetOnboardingGateForTests(async () => ({
    ok: true,
    json: async () => ({ needs_onboarding: false, completed: true })
  }));
  let directCalls = 0;
  const manager = loadSessionManager(async () => {
    directCalls += 1;
    return { ok: true, json: async () => ({ folders: [] }) };
  });
  manager.onboardingGate = {
    loadOnboardingStatus,
    onboardingGateDecision,
    loadInitialWorkspaceTree: () =>
      loadInitialWorkspaceTree({
        fetchImpl: async () => ({
          ok: true,
          json: async () => ({ folders: [{ id: 'shared-bootstrap' }] })
        })
      })
  };
  manager.updateSessionsEmptyState = () => {};
  manager.renderFolderTree = () => {};
  manager.loadAllFolderNotes = async () => {};
  manager.loadAllWorkspaceTasks = async () => {};
  manager.loadAllWorkspaceScheduledTasks = async () => {};

  await manager.loadFolders({ bootstrap: true });

  assert.equal(directCalls, 0);
  assert.equal(manager.folders[0].id, 'shared-bootstrap');
});
