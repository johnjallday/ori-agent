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

function loadSessionManager(
  fetchImpl = async () => ({ ok: true, json: async () => ({}) }),
  windowOverrides = {},
  documentOverrides = {}
) {
  const window = { ...windowOverrides };
  const document = {
    addEventListener() {},
    getElementById() {},
    querySelector() {},
    ...documentOverrides
  };
  vm.runInNewContext(
    source,
    { window, document, fetch: fetchImpl, console, crypto: { randomUUID: () => 'review-key' } },
    { filename: 'sessions.js' }
  );
  return window.sessionManager;
}

class CardElement {
  constructor() {
    this.hidden = false;
    this.className = '';
    this.children = [];
    this.dataset = {};
    this.inputs = [];
    this._innerHTML = '';
    this._text = '';
    this.classList = {
      add: (...names) => {
        this.className = [...new Set(`${this.className} ${names.join(' ')}`.trim().split(/\s+/))]
          .filter(Boolean)
          .join(' ');
      }
    };
  }
  set textContent(value) {
    this._text = String(value ?? '');
  }
  get textContent() {
    return this._text;
  }
  set innerHTML(value) {
    this._innerHTML = String(value ?? '');
    if (!value) this.children = [];
  }
  get innerHTML() {
    return this._innerHTML;
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
  querySelectorAll(selector) {
    return selector === 'input' ? this.inputs : [];
  }
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

async function runCreateWithTeamCompletion(teamCompletion) {
  const toasts = [];
  const requests = [];
  const elements = new Map([
    [
      'folderNameInput',
      { value: 'Completion workspace', focus() {}, classList: { add() {}, remove() {} } }
    ],
    ['folderDescriptionInput', { value: '' }],
    ['folderParentSelect', { value: '' }],
    ['addFolderModal', { dataset: {} }],
    ['createFolderBtn', { textContent: 'Create workspace', disabled: false }],
    ['folderImportToggle', { checked: false }]
  ]);
  const document = {
    addEventListener() {},
    getElementById: id => elements.get(id) || null,
    querySelector: selector =>
      selector === '#addFolderModal .folder-color-btn.active' ? { dataset: { color: '' } } : null
  };
  const window = {
    location: { href: '' },
    ProjectTemplateCard: {
      recheckSelection: async () => ({ state: 'ready' }),
      getPayloadFields: () => ({ template_id: 'assistant-team' }),
      getSelectedTemplate: () => ({ id: 'assistant-team' }),
      shouldOpenAfterCreate: () => false,
      reset() {}
    },
    OriTagInput: { clearTagPoolCache() {} }
  };
  const bootstrap = { Modal: { getInstance: () => ({ hide() {} }) } };
  vm.runInNewContext(
    source,
    {
      window,
      document,
      bootstrap,
      fetch: async (url, options) => {
        requests.push({ url, body: JSON.parse(options.body) });
        return {
          ok: true,
          status: 201,
          json: async () => ({
            folder: { id: 'durable-1', folder_slug: 'completion-workspace' },
            team_completion: teamCompletion
          })
        };
      },
      console,
      crypto: { randomUUID: () => 'completion-request' }
    },
    { filename: 'sessions.js' }
  );
  const manager = window.sessionManager;
  manager.getWorkspaceBootstrapFromModal = () => ({ hasAny: false });
  manager.teamView = () => ({
    canContinueFromTeam: true,
    payload: {
      team_intent: { version: 1, mode: 'staffed', plan_revision: 'reviewed' },
      role_staffing: [{ role_id: 'lead', mode: 'create', name: 'Project Lead' }],
      existing_agent_names: []
    }
  });
  manager.clearWorkspaceCreateError = () => {};
  manager.showToast = (message, kind) => toasts.push({ message, kind });
  manager.resetAddWorkspaceModalForm = () => {};
  await manager.createFolder();
  return { manager, requests, toasts, window };
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

test('a suggestion assigns its explicit role without ambient Assign state', () => {
  const calls = [];
  const draftAPI = {
    FILL_ASSIGN: 'assign',
    findSavedAgent: (_draft, name) => ({ name }),
    setRoleFill: (_draft, roleId, fill) => {
      calls.push({ roleId, fill });
      return true;
    },
    derive: () => ({
      roleRoster: {
        roles: [{ role_id: 'downloads-curator', label: 'Downloads Curator' }]
      },
      roleSummary: '1 saved agent will be attached.'
    })
  };
  const manager = loadSessionManager(undefined, { CreateWorkspaceTeamDraft: draftAPI });
  manager.teamDraft = { roleFills: new Map() };
  manager.workspaceRoleAssigning = '';
  manager.refreshWorkspaceReview = () => {};
  manager.renderExistingAgentRoster = () => {};
  manager.focusWorkspaceRoleRow = () => true;
  manager.announceWorkspaceTeamChange = () => {};

  manager.assignWorkspaceRole('downloads-curator', 'Downloads Curator');

  assert.deepEqual(JSON.parse(JSON.stringify(calls)), [
    {
      roleId: 'downloads-curator',
      fill: { mode: 'assign', name: 'Downloads Curator' }
    }
  ]);
  assert.equal(manager.workspaceRoleAssigning, '');
});

test('complete team observation keeps the normal workspace destination', async () => {
  const result = await runCreateWithTeamCompletion({
    state: 'complete',
    workspace_id: 'durable-1',
    team_staffed: true,
    applied_role_ids: ['lead'],
    missing_required_roles: [],
    requested_unapplied_role_ids: [],
    action: { label: '', href: '' }
  });

  assert.equal(result.requests.length, 1);
  assert.equal(result.window.location.href, '/workspaces/completion-workspace');
  assert.equal(
    result.toasts.some(toast => toast.kind === 'warning' && toast.message.includes('team setup')),
    false
  );
});

test('incomplete team observation routes to the existing workspace without replaying create', async () => {
  const result = await runCreateWithTeamCompletion({
    state: 'incomplete',
    workspace_id: 'durable-1',
    team_staffed: false,
    applied_role_ids: [],
    missing_required_roles: [{ role_id: 'lead', label: 'Lead' }],
    requested_unapplied_role_ids: ['lead'],
    action: { label: 'Complete team setup', href: '/workspaces/completion-workspace' }
  });

  assert.equal(result.requests.length, 1, 'recovery must not replay workspace creation');
  assert.equal(result.window.location.href, '/workspaces/completion-workspace');
  const warnings = result.toasts.filter(toast => toast.kind === 'warning');
  assert.equal(warnings.length, 1);
  assert.match(warnings[0].message, /team setup is incomplete/);
  assert.match(warnings[0].message, /1 required role missing/);
  assert.match(warnings[0].message, /will not create another workspace/);
});

test('an abandoned saved-roster response cannot repopulate a newer draft', async () => {
  const pending = [];
  const applied = [];
  const draftAPI = {
    createDraft: () => ({}),
    resetDraft() {},
    setSavedRosterLoading() {},
    setSavedRosterReady: (_draft, agents) => applied.push(agents.map(agent => agent.name)),
    setSavedRosterError() {},
    clearPlan() {}
  };
  const manager = loadSessionManager(
    () =>
      new Promise(resolve => {
        pending.push(resolve);
      }),
    { CreateWorkspaceTeamDraft: draftAPI }
  );
  manager.teamDraft = {};
  manager.renderExistingAgentRoster = () => {};
  manager.refreshWorkspaceReview = () => {};

  const abandoned = manager.loadExistingAgentRoster();
  manager.discardWorkspaceTeamDraft();
  const current = manager.loadExistingAgentRoster();

  pending[1]({ ok: true, json: async () => [{ name: 'Current Agent' }] });
  await current;
  pending[0]({ ok: true, json: async () => [{ name: 'Stale Agent' }] });
  await abandoned;

  assert.deepEqual(JSON.parse(JSON.stringify(applied)), [['Current Agent']]);
});

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

function loadSessionManagerWithPlacementModal() {
  const listeners = new Map();
  let visible = true;
  const modalElement = {
    dataset: {},
    classList: { contains: name => name === 'show' && visible },
    addEventListener(type, listener, options = {}) {
      const entries = listeners.get(type) || [];
      entries.push({ listener, once: Boolean(options?.once), capture: options === true });
      listeners.set(type, entries);
    },
    emit(type) {
      const event = {
        stopImmediatePropagation() {
          this.stopped = true;
        },
        stopped: false
      };
      const entries = [...(listeners.get(type) || [])].sort(
        (a, b) => Number(b.capture) - Number(a.capture)
      );
      for (const entry of entries) {
        entry.listener(event);
        if (entry.once) {
          const active = listeners.get(type) || [];
          listeners.set(
            type,
            active.filter(candidate => candidate !== entry)
          );
        }
        if (event.stopped) break;
      }
    }
  };
  const reviewTitle = {
    focusCalls: 0,
    focus() {
      this.focusCalls += 1;
    }
  };
  const document = {
    addEventListener() {},
    getElementById: id => {
      if (id === 'addFolderModal') return modalElement;
      if (id === 'wizardStep4Title') return reviewTitle;
      return null;
    },
    querySelector: () => null,
    querySelectorAll: () => []
  };
  const placementCalls = [];
  const window = {
    setTimeout: callback => callback(),
    OriWorkspaceMap: { beginPlacement: placement => placementCalls.push(placement) }
  };
  const bootstrap = {
    Modal: {
      getOrCreateInstance() {
        return {
          hide() {
            visible = false;
            modalElement.emit('hidden.bs.modal');
          },
          show() {
            visible = true;
            modalElement.emit('show.bs.modal');
            modalElement.emit('shown.bs.modal');
          }
        };
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
  return { manager: window.sessionManager, modalElement, placementCalls, reviewTitle };
}

test('Map placement suspends and resumes independently from sibling agent setup', () => {
  const { manager, modalElement, placementCalls, reviewTitle } =
    loadSessionManagerWithPlacementModal();
  let resets = 0;
  let discards = 0;
  manager.resetAddWorkspaceModalForm = () => {
    resets += 1;
  };
  manager.discardWorkspaceTeamDraft = () => {
    discards += 1;
  };
  manager.refreshWizardChrome = () => {};
  manager.bindEvents();

  const placement = manager.beginWorkspaceMapPlacement({
    origin: { entryPoint: 'workspace_map_build', kind: 'canvas' },
    candidate: { x: 76, y: 114 },
    payload: { name: 'Suspended review' },
    review: { signature: 'suspended-review' }
  });
  assert.equal(placement.phase, 'placing', 'the preview starts only after modal hidden');
  assert.equal(placementCalls.length, 1);
  assert.equal(discards, 0, 'hiding for placement does not cancel the wizard draft');
  assert.equal(resets, 0, 'resuming placement never runs the ordinary show reset');
  assert.equal(modalElement.dataset.suspendedForAgentSetup, undefined);
  assert.equal(modalElement.dataset.suspendedForMapPlacement, placement.token);

  assert.equal(manager.returnFromWorkspaceMapPlacement(placement.token, 'escape'), true);
  assert.equal(manager.workspaceMapPlacement.phase, 'review');
  assert.equal(resets, 0, 'the Review form is resumed, not rebuilt');
  assert.equal(reviewTitle.focusCalls, 1, 'Review heading receives restored focus');
  assert.equal(modalElement.dataset.suspendedForMapPlacement, undefined);

  modalElement.emit('hidden.bs.modal');
  assert.equal(
    manager.workspaceMapPlacement,
    null,
    'a later real cancellation clears placement state'
  );
  assert.equal(discards, 1, 'the normal hidden cleanup still owns real cancellation');
});

test('Map placement holds the reviewed draft without creating before a final map confirmation', () => {
  const fetches = [];
  const placementCalls = [];
  const manager = loadSessionManager(
    async (url, options = {}) => {
      fetches.push({ url, options });
      return { ok: true, json: async () => ({}) };
    },
    {
      OriWorkspaceMap: {
        beginPlacement: placement => placementCalls.push(placement)
      }
    }
  );
  const payload = {
    name: 'Canvas studio',
    template_id: 'built-in:canvas',
    existing_agent_names: ['Mina'],
    template_agent_overrides: [{ name: 'Mina', model: 'test-model' }],
    role_staffing: []
  };

  const placement = manager.beginWorkspaceMapPlacement({
    origin: { entryPoint: 'workspace_map_build', kind: 'canvas' },
    candidate: { x: 456, y: 228 },
    payload,
    review: { signature: 'reviewed-canvas' }
  });

  assert.ok(placement?.token, 'one modal-local draft token is minted');
  assert.equal(placement.phase, 'placing');
  assert.equal(fetches.length, 0, 'the placement CTA sends no create request');
  assert.equal(placementCalls.length, 1, 'the Map receives geometry only after review');
  assert.deepEqual(JSON.parse(JSON.stringify(placement.payload)), payload);
  assert.equal(
    'payload' in placementCalls[0],
    false,
    'the Map receives geometry/preview details, never the wizard-owned create payload'
  );
  let confirmed = null;
  manager.confirmWorkspaceMapPlacement = (token, candidate) => {
    confirmed = { token, candidate };
    return true;
  };
  placementCalls[0].onConfirm(placement.token, { x: 456, y: 228 });
  assert.deepEqual(JSON.parse(JSON.stringify(confirmed)), {
    token: placement.token,
    candidate: { x: 456, y: 228 }
  });

  assert.equal(manager.returnFromWorkspaceMapPlacement(placement.token, 'escape'), true);
  assert.equal(manager.workspaceMapPlacement.phase, 'review');
  assert.deepEqual(
    JSON.parse(JSON.stringify(manager.workspaceMapPlacement.payload)),
    payload,
    'Escape returns the exact reviewed payload instead of rebuilding an older draft'
  );
  assert.equal(fetches.length, 0, 'Escape remains non-mutating');
});

test('unavailable Map placement returns to Review with an actionable non-create error', () => {
  let endCalls = 0;
  const manager = loadSessionManager(undefined, {
    OriWorkspaceMap: {
      beginPlacement: () => false,
      endPlacement: () => endCalls++
    }
  });
  let error = '';
  manager.showWorkspaceCreateError = message => {
    error = message;
  };

  const placement = manager.beginWorkspaceMapPlacement({
    origin: { entryPoint: 'workspace_map_build', kind: 'canvas' },
    candidate: { x: 456, y: 228 },
    payload: { name: 'Wait for Map' },
    review: { signature: 'map-loading' }
  });

  assert.equal(placement.phase, 'review');
  assert.equal(endCalls, 1, 'the failed start clears the page-local session only');
  assert.match(error, /Map placement is unavailable/);
  assert.equal(manager.workspaceMapPlacement, placement);
});

test('Map placement keeps an agent-less vacancy snapshot and true cancellation discards it', () => {
  const placementCalls = [];
  const manager = loadSessionManager(undefined, {
    OriWorkspaceMap: { beginPlacement: placement => placementCalls.push(placement) }
  });
  const vacancyPayload = {
    name: 'Quiet canvas',
    blank: true,
    existing_agent_names: [],
    role_staffing: [],
    template_agent_review: { version: 1, expectations: [] }
  };
  const placement = manager.beginWorkspaceMapPlacement({
    origin: { entryPoint: 'workspace_map_build', kind: 'canvas' },
    candidate: { x: 38, y: 76 },
    payload: vacancyPayload,
    review: { signature: 'agentless-vacancy' }
  });

  assert.equal(placementCalls.length, 1, 'the map receives a preview, not the team payload');
  assert.deepEqual(
    JSON.parse(JSON.stringify(placement.payload.role_staffing)),
    [],
    'an intentionally empty role roster stays an explicit wizard-owned vacancy payload'
  );
  assert.deepEqual(
    JSON.parse(JSON.stringify(placement.payload.existing_agent_names)),
    [],
    'an agent-less draft is not silently repopulated while placing'
  );
  assert.equal(manager.abandonWorkspaceMapPlacement(placement.token), true);
  assert.equal(
    manager.workspaceMapPlacement,
    null,
    'real wizard cancellation forgets the in-memory draft'
  );
});

test('rapid map confirmations delegate one ordinary create attempt to the wizard owner', async () => {
  const manager = loadSessionManager(undefined, {
    OriWorkspaceMap: { beginPlacement() {} }
  });
  const placement = manager.beginWorkspaceMapPlacement({
    origin: { entryPoint: 'workspace_map_build', kind: 'canvas' },
    candidate: { x: 76, y: 114 },
    payload: { name: 'One submission', blank: true, existing_agent_names: [], role_staffing: [] },
    review: { signature: 'one-submit' }
  });
  let releaseCreate;
  let createCalls = 0;
  manager.createFolder = async options => {
    createCalls += 1;
    assert.equal(options.placement.token, placement.token);
    assert.deepEqual(JSON.parse(JSON.stringify(options.placement.candidate)), { x: 76, y: 114 });
    await new Promise(resolve => {
      releaseCreate = resolve;
    });
  };

  const first = manager.confirmWorkspaceMapPlacement(placement.token, { x: 76, y: 114 });
  const second = manager.confirmWorkspaceMapPlacement(placement.token, { x: 76, y: 114 });
  assert.equal(createCalls, 1, 'the first confirmation owns the only submit attempt');
  assert.equal(await second, false, 'a repeated click or Enter cannot create again');
  releaseCreate();
  await first;
  assert.equal(createCalls, 1);
});

test('the Review placement CTA prepares the ordinary payload without posting a workspace', async () => {
  const fetches = [];
  const elements = new Map([
    [
      'folderNameInput',
      { value: 'Prepared canvas', focus() {}, classList: { add() {}, remove() {} } }
    ],
    ['folderDescriptionInput', { value: 'Reviewed description' }],
    ['folderParentSelect', { value: '' }],
    ['addFolderModal', { dataset: {} }],
    ['createFolderBtn', { textContent: 'Place on map →', disabled: false }],
    ['folderImportToggle', { checked: false }]
  ]);
  const document = {
    addEventListener() {},
    getElementById: id => elements.get(id) || null,
    querySelector: selector =>
      selector === '#addFolderModal .folder-color-btn.active' ? { dataset: { color: '' } } : null
  };
  const window = {
    ProjectTemplateCard: {
      recheckSelection: async () => ({ state: 'ready' }),
      getPayloadFields: () => ({}),
      getSelectedTemplate: () => ({ blank: true })
    },
    OriWorkspaceMap: { getPendingBuild: () => ({ point: { x: 114, y: 152 }, group: null }) }
  };
  vm.runInNewContext(
    source,
    {
      window,
      document,
      fetch: async (url, options) => {
        fetches.push({ url, options });
        return { ok: true, json: async () => ({}) };
      },
      console,
      crypto: { randomUUID: () => 'placement-review' }
    },
    { filename: 'sessions.js' }
  );
  const manager = window.sessionManager;
  manager.workspaceMapOrigin = true;
  manager.getWorkspaceBootstrapFromModal = () => ({ hasAny: false });
  manager.teamView = () => ({
    canContinueFromTeam: true,
    payload: { existing_agent_names: [], role_staffing: [] }
  });
  const beginnings = [];
  manager.beginWorkspaceMapPlacement = options => {
    beginnings.push(options);
    return { token: 'placement-review-1' };
  };

  await manager.createFolder();

  assert.equal(fetches.length, 0, 'review preparation did not post /api/workspaces');
  assert.equal(beginnings.length, 1, 'the Map handoff replaces the final create');
  assert.deepEqual(JSON.parse(JSON.stringify(beginnings[0].candidate)), { x: 114, y: 152 });
  assert.deepEqual(JSON.parse(JSON.stringify(beginnings[0].payload.existing_agent_names)), []);
  assert.deepEqual(JSON.parse(JSON.stringify(beginnings[0].payload.role_staffing)), []);
  assert.equal(
    beginnings[0].payload.blank,
    true,
    'the existing payload builder remains authoritative'
  );
});

test('a confirmed Map placement latches the returned id and saves its exact coordinate before refresh', async () => {
  const events = [];
  const elements = new Map([
    [
      'folderNameInput',
      { value: 'Placed workspace', focus() {}, classList: { add() {}, remove() {} } }
    ],
    ['folderDescriptionInput', { value: 'Reviewed description' }],
    ['folderParentSelect', { value: '' }],
    ['addFolderModal', { dataset: {} }],
    ['createFolderBtn', { textContent: 'Create workspace', disabled: false }],
    ['folderImportToggle', { checked: false }]
  ]);
  const document = {
    addEventListener() {},
    getElementById: id => elements.get(id) || null,
    querySelector: selector =>
      selector === '#addFolderModal .folder-color-btn.active' ? { dataset: { color: '' } } : null
  };
  const window = {
    ProjectTemplateCard: {
      recheckSelection: async () => ({ state: 'ready' }),
      getPayloadFields: () => ({}),
      getSelectedTemplate: () => ({ blank: true }),
      shouldOpenAfterCreate: () => false,
      reset() {}
    },
    OriWorkspaceMap: {
      commitPlacement: async (id, placement) => {
        events.push({ kind: 'placement', id, placement });
        return { saved: true, point: placement.candidate };
      },
      cancelPlacement() {}
    },
    OriTagInput: { clearTagPoolCache() {} }
  };
  const bootstrap = {
    Modal: { getInstance: () => ({ hide() {} }) }
  };
  vm.runInNewContext(
    source,
    {
      window,
      document,
      bootstrap,
      fetch: async (url, options) => {
        events.push({ kind: 'create', url, body: JSON.parse(options.body) });
        return {
          ok: true,
          json: async () => ({ folder: { id: 'placed-1', folder_slug: 'placed-workspace' } })
        };
      },
      console,
      crypto: { randomUUID: () => 'placement-commit' }
    },
    { filename: 'sessions.js' }
  );
  const manager = window.sessionManager;
  const placement = {
    token: 'placement-commit',
    revision: 1,
    phase: 'committing',
    candidate: { x: 456, y: 228 },
    createdWorkspaceID: ''
  };
  manager.workspaceMapPlacement = placement;
  manager.workspaceMapOrigin = true;
  manager.getWorkspaceBootstrapFromModal = () => ({ hasAny: false });
  manager.teamView = () => ({
    canContinueFromTeam: true,
    payload: { existing_agent_names: [], role_staffing: [] }
  });
  manager.clearWorkspaceCreateError = () => {};
  manager.showToast = () => {};
  manager.resetAddWorkspaceModalForm = () => {};
  manager.loadFolders = async () => events.push({ kind: 'refresh' });

  await manager.createFolder({ placement });

  assert.equal(events.filter(event => event.kind === 'create').length, 1);
  assert.deepEqual(JSON.parse(JSON.stringify(events.map(event => event.kind))), [
    'create',
    'placement',
    'refresh'
  ]);
  assert.deepEqual(JSON.parse(JSON.stringify(events[1])), {
    kind: 'placement',
    id: 'placed-1',
    placement: {
      token: 'placement-commit',
      revision: 1,
      candidate: { x: 456, y: 228 }
    }
  });
  assert.equal(
    manager.workspaceMapPlacement,
    null,
    'successful Map cleanup cannot leave the completed draft able to submit again'
  );
});

test('an unchanged readiness recheck preserves group receipts while a template revision clears them', () => {
  const manager = loadSessionManager();
  manager.syncGroupRequirementParentControl = () => {};
  const template = {
    id: 'plugin:reaper-plugin:reaper-song',
    revision: 'a'.repeat(64),
    group_requirement: { policy: 'required' }
  };
  manager.resetGroupRequirementDraft(template);
  manager.groupRequirementDraft.review = { review_token: 'workspace-receipt' };
  manager.groupRequirementDraft.preparedHome = { home_workspace_id: 'home-1' };

  manager.resetGroupRequirementDraft({ ...template });
  assert.equal(manager.groupRequirementDraft.review.review_token, 'workspace-receipt');
  assert.equal(manager.groupRequirementDraft.preparedHome.home_workspace_id, 'home-1');

  manager.resetGroupRequirementDraft({ ...template, revision: 'b'.repeat(64) });
  assert.equal(manager.groupRequirementDraft.review, null);
  assert.equal(manager.groupRequirementDraft.preparedHome, null);
});

test('Details destination card distinguishes loading, proposed, existing, standalone, and unavailable state', () => {
  const ids = [
    'workspaceGroupDestinationCard',
    'workspaceGroupDestinationTitle',
    'workspaceGroupDestinationBadge',
    'workspaceGroupDestinationRoute',
    'workspaceGroupDestinationSummary',
    'workspaceGroupDestinationProgress',
    'workspaceGroupCompositionChoice',
    'workspaceGroupHomeReview',
    'workspaceGroupHomeReviewTitle',
    'workspaceGroupHomeReviewCopy',
    'workspaceGroupHomeConfirm',
    'workspaceGroupHomeCancel',
    'workspaceGroupDestinationActions',
    'folderNameInput'
  ];
  const elements = new Map(ids.map(id => [id, new CardElement()]));
  elements.get('folderNameInput').value = 'Field Notes';
  elements.get('workspaceGroupCompositionChoice').inputs = [
    { value: 'grouped', checked: false },
    { value: 'standalone', checked: false }
  ];
  const manager = loadSessionManager(
    undefined,
    {},
    {
      getElementById: id => elements.get(id) || null,
      createElement: () => new CardElement()
    }
  );
  manager.groupRequirementDraft = {
    policy: 'required',
    composition: 'grouped',
    status: 'loading',
    projection: null
  };
  manager.renderWorkspaceGroupDestinationCard();
  assert.equal(elements.get('workspaceGroupDestinationBadge').textContent, 'Checking');
  assert.match(elements.get('workspaceGroupDestinationRoute').innerHTML, /Checking exact group/);

  manager.groupRequirementDraft.status = 'ready';
  manager.groupRequirementDraft.projection = {
    state: 'home_creation_review_required',
    summary: 'Create the canonical group first.',
    home: { exists: false, proposed_name: 'Research Program Home' },
    required_home_roles: {
      verification: 'group_absent',
      required: 1,
      filled: 0,
      missing: 1,
      roles: [{ label: 'Portfolio Coordinator', state: 'empty' }]
    },
    actions: ['review_create_home']
  };
  manager.renderWorkspaceGroupDestinationCard();
  assert.match(
    elements.get('workspaceGroupDestinationRoute').innerHTML,
    /Proposed group · not created/
  );
  assert.equal(elements.get('workspaceGroupDestinationBadge').textContent, 'Needs setup');
  assert.equal(
    elements.get('workspaceGroupDestinationActions').children[0].textContent,
    'Review Research Program Home setup'
  );

  manager.groupRequirementDraft.projection = {
    state: 'ready_grouped',
    summary: 'Use the exact group.',
    home: { exists: true, workspace_id: 'home-1', name: 'Renamed Research Home' },
    required_home_roles: {
      verification: 'verified',
      required: 1,
      filled: 1,
      missing: 0,
      roles: [{ label: 'Portfolio Coordinator', state: 'filled' }]
    },
    actions: []
  };
  manager.renderWorkspaceGroupDestinationCard();
  assert.match(elements.get('workspaceGroupDestinationRoute').innerHTML, /Existing verified group/);
  assert.equal(elements.get('workspaceGroupDestinationBadge').textContent, 'Ready');
  assert.equal(elements.get('workspaceGroupDestinationActions').children.length, 0);

  manager.groupRequirementDraft.policy = 'none';
  manager.groupRequirementDraft.composition = 'standalone';
  manager.groupRequirementDraft.projection = {
    state: 'ready_standalone',
    summary: 'Create independently.',
    required_home_roles: { verification: 'not_applicable' },
    actions: []
  };
  manager.renderWorkspaceGroupDestinationCard();
  assert.match(elements.get('workspaceGroupDestinationRoute').innerHTML, /Standalone workspace/);
  assert.equal(
    elements.get('workspaceGroupDestinationProgress').textContent,
    'Group coordinator · Not applicable for standalone placement'
  );

  manager.groupRequirementDraft.policy = 'recommended';
  manager.groupRequirementDraft.composition = '';
  manager.groupRequirementDraft.projection = {
    state: 'choice_required',
    summary: 'Choose grouped or standalone.',
    required_home_roles: { verification: 'unavailable' },
    actions: ['choose_grouped', 'choose_standalone']
  };
  manager.renderWorkspaceGroupDestinationCard();
  assert.equal(elements.get('workspaceGroupDestinationBadge').textContent, 'Choose');
  assert.equal(elements.get('workspaceGroupCompositionChoice').hidden, false);
  assert.equal(
    elements.get('workspaceGroupCompositionChoice').inputs.some(input => input.checked),
    false
  );

  manager.groupRequirementDraft.policy = 'required';
  manager.groupRequirementDraft.composition = 'grouped';
  manager.groupRequirementDraft.projection = {
    state: 'target_ambiguous',
    summary: 'The exact group is ambiguous.',
    required_home_roles: { verification: 'unavailable' },
    actions: ['retry']
  };
  manager.renderWorkspaceGroupDestinationCard();
  assert.equal(
    elements.get('workspaceGroupDestinationProgress').textContent,
    'Required group role status is unavailable'
  );
  assert.doesNotMatch(elements.get('workspaceGroupDestinationProgress').textContent, /0 of/);
});

test('group requirement creation reviews before committing and reuses only the exact receipt', async () => {
  const calls = [];
  const manager = loadSessionManager(async (url, options) => {
    calls.push({ url, body: JSON.parse(options.body) });
    return {
      ok: true,
      json: async () => ({
        group_requirement_review: {
          state: 'ready_standalone',
          selected_composition: 'standalone',
          review_token: 'receipt-1'
        }
      })
    };
  });
  manager.groupRequirementDraft = {
    policy: 'recommended',
    composition: 'standalone',
    review: null,
    preparedHome: null
  };
  manager.refreshWorkspaceReview = () => {};
  manager.refreshWizardChrome = () => {};
  const payload = { name: 'Standalone', template_id: 'variant' };

  assert.equal(await manager.prepareGroupRequirementCommit('/api/workspaces', payload), false);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].body.group_requirement_review, true);
  assert.equal(calls[0].body.group_composition, 'standalone');
  assert.equal(payload.group_review_token, undefined, 'review request must not become a commit');

  assert.equal(await manager.prepareGroupRequirementCommit('/api/workspaces', payload), true);
  assert.equal(calls.length, 1, 'unchanged payload reuses the visible receipt');
  assert.equal(payload.group_review_token, 'receipt-1');
  assert.equal(payload.idempotency_key, 'review-key');

  const changed = { name: 'Changed', template_id: 'variant' };
  assert.equal(await manager.prepareGroupRequirementCommit('/api/workspaces', changed), false);
  assert.equal(calls.length, 2, 'changed request gets a fresh inert review');
});

test('final placement review never creates a missing required Home as a surprise consequence', async () => {
  const calls = [];
  const manager = loadSessionManager(async (url, options) => {
    calls.push({ url, body: JSON.parse(options.body) });
    return {
      ok: true,
      json: async () => ({
        group_requirement_review: {
          state: 'home_creation_review_required',
          home_name: 'Music Production Home'
        }
      })
    };
  });
  manager.groupRequirementDraft = {
    policy: 'required',
    composition: 'grouped',
    review: null,
    preparedHome: null
  };
  manager.refreshWorkspaceReview = () => {};
  manager.refreshWizardChrome = () => {};
  manager.showWorkspaceCreateError = () => {};
  manager.goToWizardStep = step => {
    manager.wizardStep = step;
  };

  assert.equal(
    await manager.prepareGroupRequirementCommit('/api/workspaces', {
      name: 'Song',
      template_id: 'reaper'
    }),
    false
  );
  assert.deepEqual(
    calls.map(call => call.url),
    ['/api/workspaces']
  );
  assert.equal(manager.wizardStep, 2);
  assert.equal(manager.groupRequirementDraft.preparedHome, null);
});

function setUpMissingGroupCard(manager) {
  manager.groupRequirementDraft = {
    templateKey: 'reaper|revision-6',
    policy: 'required',
    composition: 'grouped',
    status: 'ready',
    projection: {
      state: 'home_creation_review_required',
      source_revision: 'source-6',
      home: { exists: false, proposed_name: 'Music Production Home' },
      actions: ['review_create_home']
    },
    review: null,
    preparedHome: null,
    homeOperation: null
  };
  manager.renderWorkspaceGroupDestinationCard = () => {};
  manager.setWorkspaceGroupDestinationMessage = message => {
    manager.groupDestinationMessage = message;
  };
  manager.invalidateGroupRequirementReview = () => {
    manager.groupRequirementDraft.review = null;
  };
  manager.refreshWorkspaceSurfacesAfterGroupPreparation = async () => {};
}

test('Details reviews and commits only the missing canonical Home before any project action', async () => {
  const calls = [];
  const manager = loadSessionManager(
    async (url, options) => {
      const body = JSON.parse(options.body);
      calls.push({ url, body });
      if (url.endsWith('/review')) {
        return {
          ok: true,
          json: async () => ({
            group_requirement_review: {
              state: 'ready_grouped',
              home_name: 'Music Production Home',
              home_will_be_created: true,
              review_token: 'home-receipt'
            }
          })
        };
      }
      return {
        ok: true,
        json: async () => ({
          group_requirement: {
            state: 'home_ready',
            home_name: 'Music Production Home',
            home_workspace_id: 'home-1',
            home_created: true
          }
        })
      };
    },
    {
      ProjectTemplateCard: { getPayloadFields: () => ({ template_id: 'reaper' }) }
    }
  );
  setUpMissingGroupCard(manager);
  manager.refreshTemplateAgentPlan = async () => {
    manager.groupRequirementDraft.projection = {
      state: 'ready_grouped',
      source_revision: 'source-6',
      home: { exists: true, workspace_id: 'home-1', name: 'Music Production Home' },
      actions: []
    };
  };

  assert.equal(await manager.prepareRequiredGroupHomeFromCard(), true);
  assert.equal(manager.groupRequirementDraft.homeOperation.phase, 'awaiting_confirmation');
  assert.deepEqual(
    calls.map(call => call.url),
    ['/api/workspaces/group-requirement/home/review']
  );
  assert.equal(manager.groupRequirementDraft.preparedHome, null);

  assert.equal(await manager.commitRequiredGroupHome(), true);
  assert.deepEqual(
    calls.map(call => call.url),
    [
      '/api/workspaces/group-requirement/home/review',
      '/api/workspaces/group-requirement/home/commit'
    ]
  );
  assert.deepEqual(calls[0].body, { template_id: 'reaper' });
  assert.equal(calls[1].body.group_review_token, 'home-receipt');
  assert.equal(calls[1].body.idempotency_key, 'review-key');
  assert.equal(manager.groupRequirementDraft.preparedHome.home_workspace_id, 'home-1');
});

test('Home setup double clicks issue one review and one commit', async () => {
  const calls = [];
  let releaseReview;
  let releaseCommit;
  const manager = loadSessionManager(
    (url, options) => {
      calls.push({ url, body: JSON.parse(options.body) });
      if (url.endsWith('/review')) {
        return new Promise(resolve => {
          releaseReview = () =>
            resolve({
              ok: true,
              json: async () => ({
                group_requirement_review: {
                  state: 'ready_grouped',
                  home_name: 'Music Production Home',
                  review_token: 'home-receipt'
                }
              })
            });
        });
      }
      return new Promise(resolve => {
        releaseCommit = () =>
          resolve({
            ok: true,
            json: async () => ({
              group_requirement: {
                state: 'home_ready',
                home_name: 'Music Production Home',
                home_workspace_id: 'home-1'
              }
            })
          });
      });
    },
    { ProjectTemplateCard: { getPayloadFields: () => ({ template_id: 'reaper' }) } }
  );
  setUpMissingGroupCard(manager);
  manager.refreshTemplateAgentPlan = async () => {
    manager.groupRequirementDraft.projection = {
      state: 'ready_grouped',
      source_revision: 'source-6',
      home: { exists: true, workspace_id: 'home-1', name: 'Music Production Home' },
      actions: []
    };
  };

  const firstReview = manager.prepareRequiredGroupHomeFromCard();
  const secondReview = manager.prepareRequiredGroupHomeFromCard();
  assert.equal(calls.filter(call => call.url.endsWith('/review')).length, 1);
  releaseReview();
  assert.equal(await firstReview, true);
  assert.equal(await secondReview, true);

  const firstCommit = manager.commitRequiredGroupHome();
  const secondCommit = manager.commitRequiredGroupHome();
  assert.equal(calls.filter(call => call.url.endsWith('/commit')).length, 1);
  assert.equal(await secondCommit, false);
  releaseCommit();
  assert.equal(await firstCommit, true);
});

test('normal grouped creation requests a Map site while explicit Build keeps its coordinate', async () => {
  const placements = [];
  const manager = loadSessionManager(undefined, {
    OriWorkspaceMap: {
      placeCreatedGroupMember: async (workspaceID, groupID) => {
        placements.push({ workspaceID, groupID });
        return { placed: true };
      }
    }
  });
  const result = {
    folder: { id: 'song-1', kind: 'workspace', parent_id: 'music-home' }
  };

  const automatic = await manager.placeCreatedWorkspaceInGroup(result);
  assert.equal(automatic.placed, true);
  assert.deepEqual(placements, [{ workspaceID: 'song-1', groupID: 'music-home' }]);

  const explicit = await manager.placeCreatedWorkspaceInGroup(result, { mapOrigin: true });
  assert.equal(explicit.placed, false);
  assert.equal(explicit.reason, 'not_applicable');
  assert.equal(placements.length, 1, 'an explicit Map Build position is never replaced');

  await manager.placeCreatedWorkspaceInGroup({
    folder: { id: 'loose', kind: 'workspace', parent_id: '' }
  });
  assert.equal(placements.length, 1, 'standalone creation has no group placement effect');
});

test('a failed group-aware Map save never turns workspace creation into a failure', async () => {
  const manager = loadSessionManager(undefined, {
    OriWorkspaceMap: {
      placeCreatedGroupMember: async () => {
        throw new Error('layout unavailable');
      }
    }
  });

  const previousWarn = console.warn;
  console.warn = () => {};
  try {
    const outcome = await manager.placeCreatedWorkspaceInGroup({
      folder: { id: 'song-1', kind: 'workspace', parent_id: 'music-home' }
    });
    assert.equal(outcome.placed, false);
    assert.equal(outcome.reason, 'layout_save_failed');
  } finally {
    console.warn = previousWarn;
  }
});

test('cancelling the reviewed Home-only action leaves every workspace untouched', async () => {
  const calls = [];
  const manager = loadSessionManager(
    async (url, options) => {
      calls.push({ url, body: JSON.parse(options.body) });
      return {
        ok: true,
        json: async () => ({
          group_requirement_review: {
            state: 'ready_grouped',
            home_name: 'Music Production Home',
            home_will_be_created: true,
            review_token: 'home-receipt'
          }
        })
      };
    },
    { ProjectTemplateCard: { getPayloadFields: () => ({ template_id: 'reaper' }) } }
  );
  setUpMissingGroupCard(manager);

  assert.equal(await manager.prepareRequiredGroupHomeFromCard(), true);
  assert.equal(manager.cancelRequiredGroupHomeReview(), true);
  assert.deepEqual(
    calls.map(call => call.url),
    ['/api/workspaces/group-requirement/home/review']
  );
  assert.equal(manager.groupRequirementDraft.homeOperation, null);
  assert.equal(manager.groupRequirementDraft.preparedHome, null);
});

test('an interrupted Home commit retries the same confirmed idempotency key', async () => {
  const calls = [];
  let commitAttempts = 0;
  const manager = loadSessionManager(
    async (url, options) => {
      const body = JSON.parse(options.body);
      calls.push({ url, body });
      if (url.endsWith('/review')) {
        return {
          ok: true,
          json: async () => ({
            group_requirement_review: {
              state: 'ready_grouped',
              home_name: 'Music Production Home',
              review_token: 'home-receipt'
            }
          })
        };
      }
      commitAttempts += 1;
      if (commitAttempts === 1) throw new Error('response lost');
      return {
        ok: true,
        json: async () => ({
          group_requirement: {
            state: 'home_ready',
            home_name: 'Music Production Home',
            home_workspace_id: 'home-1',
            home_created: false
          }
        })
      };
    },
    { ProjectTemplateCard: { getPayloadFields: () => ({ template_id: 'reaper' }) } }
  );
  setUpMissingGroupCard(manager);
  manager.refreshTemplateAgentPlan = async () => {};

  await manager.prepareRequiredGroupHomeFromCard();
  assert.equal(await manager.commitRequiredGroupHome(), false);
  assert.equal(manager.groupRequirementDraft.homeOperation.phase, 'unknown');

  manager.refreshTemplateAgentPlan = async () => {
    manager.groupRequirementDraft.projection = {
      state: 'ready_grouped',
      source_revision: 'source-6',
      home: { exists: true, workspace_id: 'home-1', name: 'Music Production Home' },
      actions: []
    };
  };
  assert.equal(await manager.commitRequiredGroupHome(), true);
  const commits = calls.filter(call => call.url.endsWith('/commit'));
  assert.equal(commits.length, 2);
  assert.equal(commits[0].body.idempotency_key, commits[1].body.idempotency_key);
  assert.equal(commits[0].body.group_review_token, commits[1].body.group_review_token);
});

test('live Home role setup explicitly clears a stale binding before another fill', async () => {
  const button = new CardElement();
  button.disabled = false;
  const error = new CardElement();
  const elements = new Map([
    ['createAgentBtn', button],
    ['agentCreateDraftError', error]
  ]);
  const requests = [];
  const manager = loadSessionManager(
    async (url, options = {}) => {
      requests.push({ url, method: options.method || 'GET', body: options.body || '' });
      return {
        ok: true,
        status: 200,
        json: async () => ({
          roles: {
            workspace_id: 'home-1',
            roles: [
              {
                role_id: 'portfolio-coordinator',
                scope: 'home',
                state: 'empty',
                needs_clear: false
              }
            ]
          }
        })
      };
    },
    {},
    { getElementById: id => elements.get(id) || null }
  );
  manager.groupRequirementDraft = {
    templateKey: 'workspace-group-fixture',
    projection: {
      source_revision: 'source-1',
      home: { workspace_id: 'home-1', name: 'Research Program Home' },
      required_home_roles: {
        roles: [{ role_id: 'portfolio-coordinator', state: 'empty' }]
      }
    }
  };
  const operation = {
    templateKey: 'workspace-group-fixture',
    sourceRevision: 'source-1',
    workspaceID: 'home-1',
    roleID: 'portfolio-coordinator',
    roleLabel: 'Portfolio Coordinator',
    phase: 'editing',
    mode: 'clear',
    completed: ''
  };
  manager.workspaceGroupRoleSetup = operation;
  manager.workspaceGroupRoleSetupForm = {
    extract() {
      throw new Error('the create form must not submit while clearing stale state');
    }
  };
  manager.refreshTemplateAgentPlan = async () => {};
  let closed = false;
  manager.closeWorkspaceGroupRoleSetup = () => {
    closed = true;
  };

  assert.equal(await manager.saveWorkspaceGroupRoleSetup(), true);
  assert.deepEqual(
    requests.map(request => [request.method, request.url]),
    [
      ['DELETE', '/api/workspaces/home-1/roles/portfolio-coordinator'],
      ['GET', '/api/workspaces/home-1/roles']
    ]
  );
  assert.equal(closed, true);
  assert.match(operation.completed, /stale assignment was cleared/);
});

test('live Home-role assignment preserves the unfinished project team draft and rereads canonical state', async () => {
  const calls = [];
  const elements = new Map([
    ['workspaceGroupRoleAgentSelect', { value: 'My Coordinator', focus() {} }],
    ['createAgentBtn', { disabled: false, textContent: '' }],
    ['agentCreateDraftError', { hidden: true, textContent: '', focus() {} }]
  ]);
  const manager = loadSessionManager(
    async (url, options) => {
      calls.push({ url, body: JSON.parse(options.body) });
      return { ok: true, status: 200, json: async () => ({ roles: {} }) };
    },
    {},
    { getElementById: id => elements.get(id) || null }
  );
  const projectDraft = {
    plan: { revision: 'before-home-role' },
    roleFills: { project_lead: { mode: 'create', name: 'Draft Lead' } }
  };
  manager.teamDraft = projectDraft;
  manager.groupRequirementDraft = {
    templateKey: 'fixture-6',
    projection: {
      state: 'ready_grouped',
      source_revision: 'source-6',
      home: { exists: true, workspace_id: 'home-1', name: 'Research Home' },
      required_home_roles: {
        roles: [
          { role_id: 'portfolio_coordinator', label: 'Portfolio Coordinator', state: 'empty' }
        ]
      }
    },
    review: { review_token: 'stale-project-receipt' }
  };
  manager.workspaceGroupRoleSetup = {
    workspaceID: 'home-1',
    roleID: 'portfolio_coordinator',
    roleLabel: 'Portfolio Coordinator',
    templateKey: 'fixture-6',
    sourceRevision: 'source-6',
    projectDraft,
    phase: 'editing',
    mode: 'assign',
    completed: ''
  };
  manager.refreshTemplateAgentPlan = async () => {
    manager.groupRequirementDraft.projection.required_home_roles.roles[0].state = 'filled';
  };
  manager.closeWorkspaceGroupRoleSetup = () => {
    manager.closedGroupRoleSetup = true;
  };

  assert.equal(await manager.saveWorkspaceGroupRoleSetup(), true);
  assert.deepEqual(calls, [
    {
      url: '/api/workspaces/home-1/roles/portfolio_coordinator',
      body: { mode: 'assign', name: 'My Coordinator' }
    }
  ]);
  assert.equal(manager.teamDraft, projectDraft);
  assert.equal(manager.teamDraft.roleFills.project_lead.name, 'Draft Lead');
  assert.equal(manager.groupRequirementDraft.review, null);
  assert.equal(manager.closedGroupRoleSetup, true);
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
