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
    {
      window,
      document,
      bootstrap: windowOverrides.bootstrap,
      fetch: fetchImpl,
      console,
      crypto: { randomUUID: () => 'review-key' }
    },
    { filename: 'sessions.js' }
  );
  return window.sessionManager;
}

test('agent setup retries workspace suspension after an opening transition', () => {
  const listeners = new Map();
  let visible = true;
  let transitioning = true;
  let hideCalls = 0;
  let hiddenCalls = 0;
  const modalElement = {
    classList: { contains: name => name === 'show' && visible },
    addEventListener(type, listener, options = {}) {
      const entries = listeners.get(type) || [];
      entries.push({ listener, once: Boolean(options.once) });
      listeners.set(type, entries);
    },
    emit(type) {
      const entries = [...(listeners.get(type) || [])];
      for (const entry of entries) {
        entry.listener();
        if (entry.once) {
          listeners.set(
            type,
            (listeners.get(type) || []).filter(candidate => candidate !== entry)
          );
        }
      }
    }
  };
  const modal = {
    hide() {
      hideCalls += 1;
      if (transitioning) return;
      visible = false;
      modalElement.emit('hidden.bs.modal');
    }
  };
  const manager = loadSessionManager(undefined, {
    bootstrap: { Modal: { getOrCreateInstance: () => modal } }
  });

  manager.suspendWorkspaceModalForAgentSetup(modalElement, () => {
    hiddenCalls += 1;
  });
  assert.equal(hideCalls, 1);
  assert.equal(hiddenCalls, 0);

  transitioning = false;
  modalElement.emit('shown.bs.modal');
  assert.equal(hideCalls, 2, 'the ignored hide is retried when the opening transition settles');
  assert.equal(hiddenCalls, 1, 'the agent form can continue after the workspace actually hides');
  assert.equal(visible, false);
});

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

async function runGuidedCreate(creatorContext) {
  const events = [];
  const elements = new Map([
    ['folderNameInput', { value: 'Email Ops', focus() {}, classList: { add() {}, remove() {} } }],
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
      getPayloadFields: () => ({ template_id: 'email-ops' }),
      getSelectedTemplate: () => ({ id: 'email-ops' }),
      shouldOpenAfterCreate: () => false,
      reset() {}
    },
    OriTagInput: { clearTagPoolCache() {} }
  };
  const bootstrap = { Modal: { getInstance: () => ({ hide: () => events.push('hide') }) } };
  vm.runInNewContext(
    source,
    {
      window,
      document,
      bootstrap,
      fetch: async url => {
        events.push(`fetch ${url}`);
        return {
          ok: true,
          status: 201,
          json: async () => ({ folder: { id: 'email-ops-1', folder_slug: 'email-ops' } })
        };
      },
      console,
      crypto: { randomUUID: () => 'guided-create' }
    },
    { filename: 'sessions.js' }
  );
  const manager = window.sessionManager;
  manager.getWorkspaceBootstrapFromModal = () => ({ hasAny: false });
  manager.teamView = () => ({
    canContinueFromTeam: true,
    payload: { existing_agent_names: [], role_staffing: [] }
  });
  manager.clearWorkspaceCreateError = () => {};
  manager.showToast = () => {};
  manager.resetAddWorkspaceModalForm = () => events.push('reset');
  manager.loadFolders = async () => events.push('refresh');
  manager.workspaceCreatorContext = { generation: 1, mode: 'ordinary', ...creatorContext };
  await manager.createFolder();
  return { events, window };
}

test('a guided workspace create reports its id and stays on the page for the caller', async () => {
  const created = [];
  const { events, window } = await runGuidedCreate({
    entryPoint: 'host_setup_quest',
    stayAfterCreate: true,
    onCreated: detail => created.push(detail)
  });
  assert.equal(events.filter(event => event.startsWith('fetch /api/workspaces')).length, 1);
  assert.equal(created.length, 1);
  assert.equal(created[0].workspaceId, 'email-ops-1');
  assert.equal(created[0].folder.folder_slug, 'email-ops');
  assert.equal(window.location.href, '', 'a staying create must not navigate away');
  assert.ok(events.indexOf('hide') < events.indexOf('refresh'));
});

test('an ordinary workspace create still navigates and a failing follow-up never retries', async () => {
  const { events, window } = await runGuidedCreate({
    onCreated: () => {
      throw new Error('follow-up failed');
    }
  });
  assert.equal(events.filter(event => event.startsWith('fetch /api/workspaces')).length, 1);
  assert.equal(window.location.href, '/workspaces/email-ops');
});

test('a locked blueprint agent name is read-only with its reason, and unlocking restores it', () => {
  const attributes = new Map();
  const siblings = [];
  const input = {
    readOnly: false,
    parentElement: {
      querySelector: selector =>
        siblings.find(node => `#${node.id}` === selector && !node.removed) || null
    },
    setAttribute: (name, value) => attributes.set(name, value),
    removeAttribute: name => attributes.delete(name),
    after: node => siblings.push(node)
  };
  const manager = loadSessionManager(
    undefined,
    {},
    {
      createElement: () => ({
        id: '',
        className: '',
        textContent: '',
        remove() {
          this.removed = true;
        }
      })
    }
  );
  const form = { get: name => (name === 'name' ? input : null) };
  manager.applyWorkspaceAgentNameLock(form, 'Mail access is granted to the agent named Inbox.');
  assert.equal(input.readOnly, true);
  assert.equal(attributes.get('aria-describedby'), 'agentCreateNameLockReason');
  assert.equal(siblings.at(-1).textContent, 'Mail access is granted to the agent named Inbox.');
  manager.applyWorkspaceAgentNameLock(form, '');
  assert.equal(input.readOnly, false);
  assert.equal(attributes.has('aria-describedby'), false);
  assert.equal(siblings.at(-1).removed, true);
});

function loadGuidedTeamManager(savedAgents) {
  const shared = {};
  for (const file of ['./create-workspace-team-draft.js', './workspace-creator-state.js']) {
    vm.runInNewContext(readFileSync(new URL(file, import.meta.url), 'utf8'), {
      window: shared,
      console
    });
  }
  const manager = loadSessionManager(undefined, shared);
  const api = shared.CreateWorkspaceTeamDraft;
  const events = [];
  Object.assign(manager, {
    showToast: (message, kind) => events.push(`toast:${kind}:${message}`),
    announceWorkspaceTeamChange: message => events.push(`announce:${message}`),
    focusWorkspaceRoleRow: () => true,
    refreshWorkspaceReview() {},
    renderExistingAgentRoster() {},
    invalidateGroupRequirementReview() {}
  });
  manager.workspaceCreatorContext = shared.WorkspaceCreatorState.createCreatorContext({
    blueprint: 'email-ops',
    entryPoint: 'host_setup_quest',
    stageBlueprintRoles: true,
    teamLock: { agentNames: ['Inbox'], reason: 'Mail access is granted to the agent named Inbox.' }
  });
  const draft = manager.ensureWorkspaceTeamDraft();
  api.setPlanReady(draft, 'email-ops', {
    has_agents: true,
    revision: 'email-ops-plan',
    template_id: 'email-ops',
    agents: [
      { name: 'Postmaster', scope: 'reusable', action: 'create', entry_point: true },
      { name: 'Inbox', scope: 'reusable', action: 'create', entry_point: false }
    ],
    warnings: []
  });
  api.setSavedRosterReady(draft, savedAgents);
  return { manager, api, draft, events };
}

test('a guided Email Ops creator proposes its whole team once and keeps Inbox named and staffed', () => {
  const { manager, api, draft, events } = loadGuidedTeamManager([
    { name: 'Mailroom', source: 'user' }
  ]);
  manager.stageGuidedBlueprintRoles();
  assert.equal(api.getRoleFill(draft, 'postmaster')?.mode, 'create');
  assert.equal(api.getRoleFill(draft, 'postmaster')?.name, 'Postmaster');
  assert.equal(api.getRoleFill(draft, 'inbox')?.mode, 'create');
  assert.equal(api.getRoleFill(draft, 'inbox')?.name, 'Inbox');

  // Postmaster stays the user's to change; staging never runs twice.
  manager.clearWorkspaceRole('postmaster');
  assert.equal(api.getRoleFill(draft, 'postmaster'), null);
  manager.stageGuidedBlueprintRoles();
  assert.equal(api.getRoleFill(draft, 'postmaster'), null);

  // Inbox cannot be cleared or reassigned to a differently named agent.
  manager.clearWorkspaceRole('inbox');
  assert.equal(api.getRoleFill(draft, 'inbox')?.name, 'Inbox');
  assert.ok(events.some(event => event.startsWith('toast:warning:Keep “Inbox” on this team.')));
  manager.assignWorkspaceRole('inbox', 'Mailroom');
  assert.equal(api.getRoleFill(draft, 'inbox')?.mode, 'create');
  assert.equal(manager.workspaceRoleLockReason('postmaster'), '');
});

test('a saved agent already named Inbox is assigned instead of creating a duplicate', () => {
  const { manager, api, draft } = loadGuidedTeamManager([{ name: 'Inbox', source: 'user' }]);
  manager.stageGuidedBlueprintRoles();
  assert.equal(api.getRoleFill(draft, 'inbox')?.mode, 'assign');
  assert.equal(api.getRoleFill(draft, 'inbox')?.name, 'Inbox');
  assert.equal(api.getRoleFill(draft, 'postmaster')?.mode, 'create');
});

test('an ordinary creator never stages roles or locks names', () => {
  const { manager, api, draft } = loadGuidedTeamManager([]);
  manager.workspaceCreatorContext = { generation: 2, mode: 'ordinary' };
  manager.stageGuidedBlueprintRoles();
  assert.equal(api.getRoleFill(draft, 'inbox'), null);
  assert.equal(manager.workspaceRoleLockReason('inbox'), '');
});

test('Workspace create still requires its reviewed strict Team envelope before posting', async () => {
  const requests = [];
  const elements = new Map([
    [
      'folderNameInput',
      { value: 'Strict workspace', focus() {}, classList: { add() {}, remove() {} } }
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
    querySelector: () => null
  };
  const window = {};
  vm.runInNewContext(
    source,
    {
      window,
      document,
      bootstrap: { Modal: { getInstance: () => ({ hide() {} }) } },
      fetch: async (url, options) => {
        requests.push({ url, options });
        return { ok: true, json: async () => ({}) };
      },
      console,
      crypto: { randomUUID: () => 'strict-team' }
    },
    { filename: 'sessions.js' }
  );
  const manager = window.sessionManager;
  let routedTo = 0;
  manager.teamView = () => ({
    canContinueFromTeam: false,
    blockingIssues: [{ id: 'required-roles-missing', anchor: '' }]
  });
  manager.goToWizardStep = step => {
    routedTo = step;
  };
  manager.refreshWorkspaceReview = () => {};

  await manager.createFolder();

  assert.equal(routedTo, 3);
  assert.equal(requests.length, 0, 'Workspace cannot bypass strict Team validation');
});

test('ordinary Group submits its reviewed roster without Workspace blueprint work', async () => {
  const requests = [];
  const elements = new Map([
    [
      'folderNameInput',
      { value: 'Client Homes', focus() {}, classList: { add() {}, remove() {} } }
    ],
    ['folderDescriptionInput', { value: 'Organize client work.' }],
    ['folderParentSelect', { value: 'parent-group' }],
    ['addFolderModal', { dataset: {} }],
    ['createFolderBtn', { textContent: 'Create group', disabled: false }],
    ['folderImportToggle', { checked: false }]
  ]);
  const document = {
    addEventListener() {},
    getElementById: id => elements.get(id) || null,
    querySelector: selector =>
      selector === '#addFolderModal .folder-color-btn.active'
        ? { dataset: { color: '#22c55e' } }
        : null
  };
  const window = {
    ProjectTemplateCard: {
      recheckSelection: async () => {
        throw new Error('Group must not recheck Workspace blueprints');
      }
    }
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
            folder: { id: 'group-1', name: 'Client Homes', folder_slug: 'client-homes' }
          })
        };
      },
      console,
      crypto: { randomUUID: () => 'ordinary-group' }
    },
    { filename: 'sessions.js' }
  );
  const manager = window.sessionManager;
  manager.workspaceCreatorContext = {
    generation: 1,
    mode: 'ordinary',
    kind: 'group',
    drafts: { workspace: {}, group: {} }
  };
  manager.clearWorkspaceCreateError = () => {};
  manager.resetAddWorkspaceModalForm = () => {};
  manager.refreshWorkspaceSurfacesAfterOrdinaryGroupCreate = async () => {};
  manager.showCreatedGroupFollowUp = () => {};
  manager.teamView = () => ({
    canContinueFromTeam: true,
    payload: {
      create_template_agents: true,
      template_agent_review: {
        version: 1,
        plan_revision: 'reviewed-group-roster',
        expectations: [{ index: 0, name: 'Client Homes Manager', action: 'create' }]
      }
    }
  });

  await manager.createFolder();

  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, '/api/workspaces');
  assert.deepEqual(JSON.parse(JSON.stringify(requests[0].body)), {
    name: 'Client Homes',
    description: 'Organize client work.',
    parent_id: 'parent-group',
    color: '#22c55e',
    kind: 'group',
    group_roster: true,
    create_template_agents: true,
    template_agent_review: {
      version: 1,
      plan_revision: 'reviewed-group-roster',
      expectations: [{ index: 0, name: 'Client Homes Manager', action: 'create' }]
    }
  });
});

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

test('created Group follow-up navigates only to its returned team surface', () => {
  let options = null;
  const location = { href: '' };
  const manager = loadSessionManager(undefined, {
    location,
    Toast: {
      success: (_message, received) => {
        options = received;
      }
    }
  });

  manager.showCreatedGroupFollowUp({
    id: 'durable-group',
    name: 'Client Homes',
    folder_slug: 'client-homes'
  });

  assert.equal(options.action.label, 'Open group / Manage team');
  options.action.onClick();
  assert.equal(location.href, '/workspaces/client-homes/assistant');
});

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

test('the shared entry point creates one generation-fenced creator context before opening', () => {
  const modalElement = { dataset: {} };
  const contexts = [];
  const manager = loadSessionManager(
    undefined,
    {
      WorkspaceCreatorState: {
        createCreatorContext: options => {
          contexts.push(options);
          return { ...options, kind: options.kind || 'workspace' };
        }
      },
      bootstrap: {
        Modal: class {
          show() {}
        }
      }
    },
    { getElementById: id => (id === 'addFolderModal' ? modalElement : null) }
  );

  manager.showAddWorkspaceModal({
    kind: 'group',
    entryPoint: 'tree_new_group',
    selection: { ids: ['parent'], names: ['Parent'] }
  });

  assert.equal(contexts.length, 1);
  assert.equal(contexts[0].generation, 1);
  assert.equal(contexts[0].kind, 'group');
  assert.equal(contexts[0].entryPoint, 'tree_new_group');
  assert.deepEqual(JSON.parse(JSON.stringify(contexts[0].selection)), {
    ids: ['parent'],
    names: ['Parent']
  });
  assert.equal(manager.workspaceCreatorContext.kind, 'group');
});

test('Group creator navigation exposes Details, Roster, then Review while Workspace stays four steps', () => {
  const manager = loadSessionManager();
  manager.workspaceCreatorContext = { mode: 'ordinary', kind: 'workspace' };
  manager.wizardStep = 1;
  assert.deepEqual(JSON.parse(JSON.stringify(manager.creatorWizardSteps())), [1, 2, 3, 4]);
  assert.equal(manager.nextWizardStep(), 2);

  manager.workspaceCreatorContext = { mode: 'ordinary', kind: 'group' };
  manager.wizardStep = 2;
  assert.deepEqual(JSON.parse(JSON.stringify(manager.creatorWizardSteps())), [2, 3, 4]);
  assert.equal(manager.nextWizardStep(), 3);
  manager.wizardStep = 3;
  assert.equal(manager.nextWizardStep(), 4);
  assert.equal(manager.previousWizardStep(), 2);
  manager.wizardStep = 4;
  assert.equal(manager.isFinalWizardStep(), true);

  manager.importModeEnabled = true;
  assert.deepEqual(JSON.parse(JSON.stringify(manager.creatorWizardSteps())), [2]);
});

test('an ordinary Group starts on its Blueprint step; a managed blueprint skips the roster', () => {
  let blueprintStep = true;
  let managed = false;
  const manager = loadSessionManager(undefined, {
    GroupTemplateCreator: {
      hasBlueprintStep: () => blueprintStep,
      managedActive: () => managed
    }
  });
  manager.workspaceCreatorContext = { mode: 'ordinary', kind: 'group' };
  manager.wizardStep = 1;
  assert.deepEqual(JSON.parse(JSON.stringify(manager.creatorWizardSteps())), [1, 2, 3, 4]);
  assert.equal(manager.nextWizardStep(), 2);

  managed = true;
  assert.deepEqual(JSON.parse(JSON.stringify(manager.creatorWizardSteps())), [1, 2, 4]);
  manager.wizardStep = 2;
  assert.equal(manager.nextWizardStep(), 4);
  assert.equal(manager.previousWizardStep(), 1);

  // Selected-member grouping and guided setup have no blueprint choice.
  managed = false;
  blueprintStep = false;
  manager.workspaceCreatorContext = { mode: 'selected-members', kind: 'group' };
  assert.deepEqual(JSON.parse(JSON.stringify(manager.creatorWizardSteps())), [2, 3, 4]);
  manager.workspaceCreatorContext = { mode: 'guided', kind: 'group' };
  assert.deepEqual(JSON.parse(JSON.stringify(manager.creatorWizardSteps())), [2, 4]);
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
    addEventListener() {},
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
  manager.syncWorkspaceDestinationControl = () => {};
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

test('Team missing-role recovery builds an absent group or opens exact Home staffing', () => {
  const detailsButton = { disabled: false };
  const manager = loadSessionManager(
    undefined,
    {},
    {
      querySelector: selector =>
        selector === '[data-group-destination-action="review_create_home"]' ? detailsButton : null,
      getElementById: () => null
    }
  );
  const view = {
    blockingIssues: [{ id: 'required-roles-missing', roleId: 'portfolio-coordinator' }],
    roleRoster: {
      roles: [
        {
          role_id: 'portfolio-coordinator',
          label: 'Portfolio Coordinator',
          scope: 'home',
          required: true,
          state: 'empty'
        }
      ]
    },
    assistantProgram: { stationName: 'Research Program Home' }
  };
  manager.teamView = () => view;
  manager.groupRequirementDraft = {
    projection: {
      home: { exists: false, proposed_name: 'Research Program Home' },
      actions: ['review_create_home']
    }
  };

  assert.equal(manager.teamRecoveryLabel('fill-required-role'), 'Build Research Program Home…');
  let returnedStep = 0;
  let destinationAction = '';
  manager.goToWizardStep = step => {
    returnedStep = step;
  };
  manager.runWorkspaceGroupDestinationAction = action => {
    destinationAction = action;
  };
  manager.runTeamRecovery('fill-required-role', { id: 'team-recovery' });
  assert.equal(returnedStep, 2);
  assert.equal(destinationAction, 'review_create_home');

  manager.groupRequirementDraft.projection = {
    home: { exists: true, workspace_id: 'home-1', name: 'Research Program Home' },
    actions: ['open_group_roles']
  };
  assert.equal(manager.teamRecoveryLabel('fill-required-role'), 'Set up Portfolio Coordinator');
  let opened = null;
  const opener = { id: 'setup-group-role' };
  manager.openWorkspaceGroupRoleSetup = (receivedOpener, roleID) => {
    opened = { opener: receivedOpener, roleID };
  };
  manager.runTeamRecovery('fill-required-role', opener);
  assert.deepEqual(opened, { opener, roleID: 'portfolio-coordinator' });
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

test('Details destination describes its Group Template only for the draft that asked', async () => {
  const ids = [
    'workspaceGroupDestinationCard',
    'workspaceGroupDestinationRoute',
    'workspaceGroupHomeReview',
    'workspaceGroupHomeReviewCopy',
    'workspaceGroupDestinationActions',
    'folderNameInput'
  ];
  const elements = new Map(ids.map(id => [id, new CardElement()]));
  const lookups = [];
  const entry = {
    id: 'group-template:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    name: 'Research Program Home'
  };
  const creator = {
    catalogEntry: id => {
      let resolve;
      const promise = new Promise(done => (resolve = done));
      lookups.push({ id, resolve });
      return promise;
    },
    templateMeta: value => `Template: ${value.name} · Plugin: fixture 1.0.0`,
    roleSummary: () => ['Set up after: Portfolio Coordinator (required)']
  };
  const manager = loadSessionManager(
    undefined,
    { GroupTemplateCreator: creator },
    {
      getElementById: id => elements.get(id) || null,
      createElement: () => new CardElement()
    }
  );
  const projection = {
    state: 'home_creation_review_required',
    group_template_id: entry.id,
    home: { exists: false, proposed_name: 'Research Program Home' },
    required_home_roles: { verification: 'group_absent', required: 1, filled: 0, missing: 1 },
    actions: ['review_create_home']
  };
  const first = { policy: 'required', composition: 'grouped', status: 'ready', projection };
  manager.groupRequirementDraft = first;
  manager.renderWorkspaceGroupDestinationCard();
  manager.renderWorkspaceGroupDestinationCard();
  assert.equal(lookups.length, 1, 'one lookup per template for a draft');
  assert.doesNotMatch(elements.get('workspaceGroupDestinationRoute').innerHTML, /Template:/);

  // A newer draft replaces the first before its lookup resolves.
  const second = {
    policy: 'required',
    composition: 'grouped',
    status: 'ready',
    projection: { ...projection }
  };
  manager.groupRequirementDraft = second;
  lookups[0].resolve(entry);
  await new Promise(done => setTimeout(done, 0));
  assert.equal(first.groupTemplate.entry, null, 'a stale draft is never described');
  manager.renderWorkspaceGroupDestinationCard();
  lookups[1].resolve(entry);
  await new Promise(done => setTimeout(done, 0));
  assert.match(
    elements.get('workspaceGroupDestinationRoute').innerHTML,
    /Template: Research Program Home · Plugin: fixture 1\.0\.0/
  );

  second.homeOperation = { phase: 'awaiting_confirmation', review: {} };
  manager.renderWorkspaceGroupDestinationCard();
  assert.match(
    elements.get('workspaceGroupHomeReviewCopy').textContent,
    /Set up after: Portfolio Coordinator \(required\)\./
  );

  // Standalone placement never names a group template.
  manager.groupRequirementDraft = {
    policy: 'recommended',
    composition: 'standalone',
    status: 'ready',
    projection: { ...projection, state: 'ready_standalone', home: null, actions: [] }
  };
  manager.renderWorkspaceGroupDestinationCard();
  assert.equal(lookups.length, 2);
  assert.doesNotMatch(elements.get('workspaceGroupDestinationRoute').innerHTML, /Template:/);
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

// ---------------------------------------------------------------------------
// Locked parent: a group page's Build owns the destination (group-map-build
// FR-14 – FR-16). The wizard preselects and freezes the group, skips the
// placement question, and puts that group in the POST whatever else is set.
// ---------------------------------------------------------------------------

function lockedParentManager({ groups = [{ id: 'g-1', name: 'Studio Group', depth: 0 }] } = {}) {
  const options = [{ value: '', textContent: 'No group' }];
  groups.forEach(group => options.push({ value: group.id, textContent: group.name }));
  const parentSelect = {
    value: '',
    disabled: false,
    innerHTML: '',
    options,
    querySelector: selector => {
      const match = /option\[value="([^"]*)"\]/.exec(selector);
      const id = match ? match[1] : '';
      return options.find(option => option.value === id) || null;
    },
    appendChild: option => options.push(option)
  };
  const parentHelp = { textContent: '' };
  const elements = {
    folderParentSelect: parentSelect,
    folderParentHelp: parentHelp,
    workspaceCreatorDestinationCard: { hidden: false }
  };
  const manager = loadSessionManager(
    async () => ({ ok: true, json: async () => ({}) }),
    {
      WorkspaceGroupOptions: {
        collectWorkspaceGroupOptions: () => groups,
        renderWorkspaceParentOptions: () => ''
      },
      CSS: { escape: value => value }
    },
    {
      getElementById: id => elements[id] || null,
      createElement: () => ({ value: '', textContent: '' })
    }
  );
  manager.folders = groups.map(group => ({ ...group, kind: 'group' }));
  return { manager, parentSelect, parentHelp, elements };
}

test('a locked parent preselects, disables, and captions the destination control', () => {
  const { manager, parentSelect, parentHelp, elements } = lockedParentManager();
  manager.beginWorkspaceCreatorContext({
    parentId: 'g-1',
    parentName: 'Studio Group',
    parentLocked: true,
    entryPoint: 'group_detail_build'
  });

  assert.equal(manager.lockedParentId(), 'g-1');
  manager.syncWorkspaceDestinationControl();
  assert.equal(parentSelect.value, 'g-1');
  assert.equal(parentSelect.disabled, true);
  assert.equal(parentHelp.textContent, 'Building into Studio Group');
  assert.equal(elements.workspaceCreatorDestinationCard.hidden, false);
});

test('an unlocked creator keeps the destination control a free choice', () => {
  const { manager, parentSelect, parentHelp } = lockedParentManager();
  manager.beginWorkspaceCreatorContext({ entryPoint: 'home_cockpit_create' });
  assert.equal(manager.lockedParentId(), '');
  manager.syncWorkspaceDestinationControl();
  assert.equal(parentSelect.value, '');
  assert.equal(parentSelect.disabled, false);
  assert.doesNotMatch(parentHelp.textContent, /Building into/);
});

test('parentLocked without a parent id is not a lock', () => {
  const { manager } = lockedParentManager();
  manager.beginWorkspaceCreatorContext({ parentLocked: true });
  assert.equal(manager.lockedParentId(), '');
});

test('a locked parent answers the grouped-or-standalone question', () => {
  const { manager, parentSelect, elements } = lockedParentManager();
  manager.groupRequirementDraft = { policy: 'recommended', composition: '' };
  assert.equal(manager.groupRequirementBlocked(), true, 'unlocked still has to ask');

  manager.beginWorkspaceCreatorContext({ parentId: 'g-1', parentLocked: true });
  assert.equal(manager.groupRequirementBlocked(), false);

  // And the control keeps showing the group rather than being cleared and
  // hidden the way an unanswered placement question does.
  manager.syncWorkspaceDestinationControl();
  assert.equal(parentSelect.value, 'g-1');
  assert.equal(parentSelect.disabled, true);
  assert.equal(elements.workspaceCreatorDestinationCard.hidden, false);
});

test('the locked parent wins over the select and over a pending map build', () => {
  const { manager, parentSelect } = lockedParentManager();
  manager.beginWorkspaceCreatorContext({ parentId: 'g-1', parentLocked: true });

  // Three ways the destination could otherwise be decided.
  const resolve = (selectValue, pendingGroupId) => {
    parentSelect.value = selectValue;
    const payload = { parent_id: manager.lockedParentId() || parentSelect.value };
    if (!payload.parent_id && pendingGroupId) payload.parent_id = pendingGroupId;
    const locked = manager.lockedParentId();
    if (locked) payload.parent_id = locked;
    return payload.parent_id;
  };
  assert.equal(resolve('', ''), 'g-1', 'blank form');
  assert.equal(resolve('g-other', ''), 'g-1', 'a stale select value');
  assert.equal(resolve('', 'g-district'), 'g-1', 'a pending district build');
});

// ---------------------------------------------------------------------------
// Details destination: the generic "Create in" control has one owner, so a
// blueprint's fixed placement is never contradicted by a second "No group"
// statement on any navigation path (blueprint-aware Details FR 1–8).
// ---------------------------------------------------------------------------

class DestinationElement {
  constructor(props = {}) {
    this.hidden = false;
    this.disabled = false;
    this.value = '';
    this.textContent = '';
    this.innerHTML = '';
    this.placeholder = '';
    this.dataset = {};
    this.attributes = {};
    Object.assign(this, props);
  }
  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }
  removeAttribute(name) {
    delete this.attributes[name];
  }
  querySelectorAll() {
    return [];
  }
  querySelector() {
    return null;
  }
  focus() {}
}

// The real page-local helpers the owner reads, loaded the way the page loads
// them (window globals), so these tests cannot drift from their contracts.
function creatorWindowHelpers() {
  const window = {};
  for (const file of ['./workspace-creator-state.js', './workspace-group-options.js']) {
    vm.runInNewContext(readFileSync(new URL(file, import.meta.url), 'utf8'), {
      window,
      console,
      escapeHtml: value => String(value ?? '')
    });
  }
  return {
    WorkspaceCreatorState: window.WorkspaceCreatorState,
    WorkspaceGroupOptions: window.WorkspaceGroupOptions
  };
}

function detailsDestinationManager({ groups = [], windowOverrides = {} } = {}) {
  const options = [{ value: '', textContent: 'No group' }];
  groups.forEach(group => options.push({ value: group.id, textContent: group.name }));
  const elements = {
    workspaceCreatorDestinationCard: new DestinationElement(),
    workspaceCreatorColorCard: new DestinationElement(),
    folderParentSelect: new DestinationElement({
      options,
      selectedIndex: 0,
      appendChild: option => options.push(option)
    }),
    folderParentHelp: new DestinationElement(),
    folderNameInput: new DestinationElement({ value: 'Night Drive' })
  };
  const manager = loadSessionManager(
    async () => ({ ok: true, json: async () => ({}) }),
    { ...creatorWindowHelpers(), ...windowOverrides },
    {
      getElementById: id => elements[id] || null,
      querySelectorAll: () => [],
      createElement: () => ({ value: '', textContent: '' })
    }
  );
  manager.folders = groups.map(group => ({ ...group, kind: 'group' }));
  // Plan loading is network work; placement visibility never waits on it.
  manager.refreshTemplateAgentPlan = async () => {};
  manager.beginWorkspaceCreatorContext({ entryPoint: 'workspace_hub_create' });
  return { manager, elements };
}

// What the workspace-template-selected listener does with a blueprint.
function selectBlueprint(manager, template) {
  manager.workspaceTemplate = template;
  manager.resetGroupRequirementDraft(template);
}

const requiredGroupTemplate = {
  id: 'plugin:owner-plugin:grouped-blueprint',
  revision: 'r1',
  group_requirement: { policy: 'required' }
};

const recommendedGroupTemplate = {
  id: 'plugin:owner-plugin:flexible-blueprint',
  revision: 'r1',
  group_requirement: { policy: 'recommended' }
};

test('a required group keeps the generic destination control hidden on Details (FR 17)', () => {
  const { manager, elements } = detailsDestinationManager();
  manager.resetGroupRequirementDraft(requiredGroupTemplate);
  manager.wizardStep = 2;
  manager.refreshWizardChrome();
  assert.equal(elements.workspaceCreatorDestinationCard.hidden, true);
});

test('a fixed placement hides the generic destination control (FR 3)', () => {
  const groups = [{ id: 'g-1', name: 'Studio Group', depth: 0 }];
  const details = template => {
    const context = detailsDestinationManager({ groups });
    context.elements.folderParentSelect.value = 'g-1';
    selectBlueprint(context.manager, template);
    context.manager.wizardStep = 2;
    context.manager.refreshWizardChrome();
    return context;
  };

  const required = details(requiredGroupTemplate);
  assert.equal(required.elements.workspaceCreatorDestinationCard.hidden, true, 'required');
  assert.equal(required.elements.folderParentSelect.value, '', 'no stale organizational parent');

  const unanswered = details(recommendedGroupTemplate);
  assert.equal(
    unanswered.elements.workspaceCreatorDestinationCard.hidden,
    true,
    'recommended, not answered yet'
  );

  const grouped = details(recommendedGroupTemplate);
  grouped.manager.setGroupRequirementComposition('grouped');
  assert.equal(
    grouped.elements.workspaceCreatorDestinationCard.hidden,
    true,
    'recommended + grouped'
  );
  assert.equal(grouped.elements.folderParentSelect.disabled, true);
});

test('standalone placement and blueprints without a requirement keep the control (FR 4)', () => {
  const groups = [{ id: 'g-1', name: 'Studio Group', depth: 0 }];

  const standalone = detailsDestinationManager({ groups });
  selectBlueprint(standalone.manager, recommendedGroupTemplate);
  standalone.manager.wizardStep = 2;
  standalone.manager.setGroupRequirementComposition('standalone');
  assert.equal(standalone.elements.workspaceCreatorDestinationCard.hidden, false, 'standalone');
  assert.equal(standalone.elements.folderParentSelect.disabled, false);

  const none = detailsDestinationManager({ groups });
  selectBlueprint(none.manager, {
    ...requiredGroupTemplate,
    group_requirement: { policy: 'none' }
  });
  none.manager.wizardStep = 2;
  none.manager.refreshWizardChrome();
  assert.equal(none.elements.workspaceCreatorDestinationCard.hidden, false, 'policy none');

  const blank = detailsDestinationManager();
  selectBlueprint(blank.manager, null);
  blank.manager.wizardStep = 2;
  blank.manager.refreshWizardChrome();
  assert.equal(blank.elements.workspaceCreatorDestinationCard.hidden, false, 'Blank');
  assert.equal(blank.elements.folderParentSelect.value, '');
  assert.equal(blank.elements.folderParentSelect.disabled, true, 'nothing to choose yet');
  assert.match(blank.elements.folderParentHelp.textContent, /^No groups yet\./);
});

test('a locked parent stays visible, disabled, and captioned even for a required group (FR 5)', () => {
  const groups = [{ id: 'g-1', name: 'Studio Group', depth: 0 }];
  const { manager, elements } = detailsDestinationManager({ groups });
  manager.beginWorkspaceCreatorContext({
    parentId: 'g-1',
    parentName: 'Studio Group',
    parentLocked: true,
    entryPoint: 'group_detail_build'
  });
  selectBlueprint(manager, requiredGroupTemplate);
  manager.wizardStep = 2;
  manager.refreshWizardChrome();
  assert.equal(elements.workspaceCreatorDestinationCard.hidden, false);
  assert.equal(elements.folderParentSelect.value, 'g-1');
  assert.equal(elements.folderParentSelect.disabled, true);
  assert.equal(elements.folderParentHelp.textContent, 'Building into Studio Group');
});

test('Color stays available when the destination control is hidden (FR 6)', () => {
  const { manager, elements } = detailsDestinationManager();
  selectBlueprint(manager, requiredGroupTemplate);
  manager.wizardStep = 2;
  manager.refreshWizardChrome();
  assert.equal(elements.workspaceCreatorDestinationCard.hidden, true);
  assert.equal(elements.workspaceCreatorColorCard.hidden, false);

  // Import still has no Color, exactly as before the move.
  manager.importModeEnabled = true;
  manager.refreshWizardChrome();
  assert.equal(elements.workspaceCreatorColorCard.hidden, true);
});

test('every navigation path gives Details the same destination answer (FR 2)', () => {
  const groups = [{ id: 'g-1', name: 'Studio Group', depth: 0 }];
  const { manager, elements } = detailsDestinationManager({ groups });
  const card = elements.workspaceCreatorDestinationCard;
  // Rendering Team and Review is not what this test is about.
  manager.teamView = () => null;
  manager.refreshWorkspaceReview = function () {
    this.syncWorkspaceDestinationControl();
  };

  selectBlueprint(manager, requiredGroupTemplate);
  manager.goToWizardStep(2);
  assert.equal(manager.wizardStep, 2);
  assert.equal(card.hidden, true, 'first Continue');

  manager.goToWizardStep(3);
  assert.equal(manager.wizardStep, 3);
  assert.equal(card.hidden, true, 'Team');

  manager.goToWizardStep(2);
  assert.equal(card.hidden, true, 'Back to Details');

  assert.equal(manager.switchWorkspaceCreatorKind('group'), true);
  assert.equal(card.hidden, false, 'an ordinary Group chooses its own parent');
  assert.match(elements.folderParentHelp.textContent, /Nest this group/);

  assert.equal(manager.switchWorkspaceCreatorKind('workspace'), true);
  manager.goToWizardStep(2);
  assert.equal(manager.wizardStep, 2);
  assert.equal(card.hidden, true, 'Workspace → Group → Workspace');

  // A readiness recheck emits the same blueprint again.
  selectBlueprint(manager, { ...requiredGroupTemplate });
  manager.refreshWizardChrome();
  assert.equal(card.hidden, true, 'readiness recheck');
  assert.equal(elements.folderParentSelect.value, '');
});

test('destination help names program membership only for an assistant program (FR 8)', () => {
  const groups = [{ id: 'g-1', name: 'Studio Group', depth: 0 }];
  const plain = detailsDestinationManager({ groups });
  selectBlueprint(plain.manager, { id: 'research-project' });
  plain.manager.wizardStep = 2;
  plain.manager.refreshWizardChrome();
  assert.equal(
    plain.elements.folderParentHelp.textContent,
    'Optional. Put this workspace inside a group to keep related work together.'
  );

  const program = detailsDestinationManager({ groups });
  selectBlueprint(program.manager, {
    id: 'plugin:owner-plugin:program-blueprint',
    assistant_program: { id: 'owner-program' }
  });
  program.manager.wizardStep = 2;
  program.manager.refreshWizardChrome();
  assert.equal(
    program.elements.folderParentHelp.textContent,
    'Optional. Choose an organizational group for this workspace. This does not grant program membership.'
  );
});

test('Review states a fixed destination once, with the same group state Details shows (FR 7)', () => {
  const groups = [{ id: 'g-1', name: 'Studio Group', depth: 0 }];
  const { manager, elements } = detailsDestinationManager({ groups });
  const template = {
    ...requiredGroupTemplate,
    group_requirement: { policy: 'required', default_home_name: 'Studio Home' }
  };
  selectBlueprint(manager, template);
  manager.syncWorkspaceDestinationControl();

  manager.groupRequirementDraft.projection = {
    home: { exists: false, proposed_name: 'Studio Home' }
  };
  const proposed = manager.renderWorkspaceGroupRequirementReceipt(template);
  assert.match(proposed, /Proposed group · not created yet/);
  assert.doesNotMatch(proposed, /Existing verified group/);

  manager.groupRequirementDraft.projection = {
    home: { exists: true, workspace_id: 'home-1', name: 'Studio Home' }
  };
  assert.match(
    manager.renderWorkspaceGroupRequirementReceipt(template),
    /Existing verified group · reused/
  );

  // The Details choices never add a second "Group:" line for a hidden control.
  elements.folderParentSelect.value = 'g-1';
  elements.folderParentSelect.selectedIndex = 1;
  assert.equal(
    manager.workspaceReviewDetailChoices().some(choice => choice.startsWith('Group:')),
    false
  );

  const free = detailsDestinationManager({ groups });
  selectBlueprint(free.manager, null);
  free.manager.syncWorkspaceDestinationControl();
  free.elements.folderParentSelect.value = 'g-1';
  free.elements.folderParentSelect.selectedIndex = 1;
  assert.deepEqual(JSON.parse(JSON.stringify(free.manager.workspaceReviewDetailChoices())), [
    'Group: Studio Group'
  ]);
});

test('guided setup keeps its reviewed group while the generic control stays hidden', () => {
  const groups = [{ id: 'home-1', name: 'Studio Home', depth: 0 }];
  const { manager, elements } = detailsDestinationManager({
    groups,
    windowOverrides: { SetupWorkspaceCreator: { isActive: () => true } }
  });
  manager.teamView = () => null;
  manager.refreshWorkspaceReview = function () {
    this.syncWorkspaceDestinationControl();
  };
  // setup-workspace-creator.js writes the reviewed Home into the select and
  // mounts its own placement control; its submit refuses any other parent_id.
  // Before this owner, moving to Team cleared the value and the create failed.
  elements.folderParentSelect.value = 'home-1';
  elements.folderParentSelect.disabled = true;
  selectBlueprint(manager, requiredGroupTemplate);
  manager.goToWizardStep(2);
  manager.goToWizardStep(3);
  assert.equal(manager.wizardStep, 3);
  assert.equal(elements.workspaceCreatorDestinationCard.hidden, true);
  assert.equal(elements.folderParentSelect.value, 'home-1');
  assert.equal(elements.folderParentSelect.disabled, true, 'setup owns the disabled state too');

  // A standalone setup placement is stated by setup's own control as well.
  selectBlueprint(manager, recommendedGroupTemplate);
  manager.setGroupRequirementComposition('standalone');
  elements.folderParentSelect.value = '';
  manager.goToWizardStep(2);
  assert.equal(elements.workspaceCreatorDestinationCard.hidden, true);
  assert.equal(elements.folderParentSelect.value, '');
  assert.deepEqual(JSON.parse(JSON.stringify(manager.workspaceReviewDetailChoices())), []);
});

// ---------------------------------------------------------------------------
// Fields the blueprint already answers (blueprint-aware Details FR 9–11a).
// ---------------------------------------------------------------------------

const selfConfiguringTemplate = {
  id: 'plugin:owner-plugin:song',
  name: 'Song',
  description: 'A blueprint that sets up its own project.',
  plugin_owner: { plugin_id: 'owner-plugin', blueprint_id: 'song' },
  assistant_program: { id: 'owner-program' },
  project_entry: { relative_path: '{{name}}.proj' }
};

test('blueprintDetailsProfile derives plain flags from what a blueprint declares (FR 9)', () => {
  const manager = loadSessionManager();
  const profile = template => JSON.parse(JSON.stringify(manager.blueprintDetailsProfile(template)));
  const blank = {
    blank: true,
    bringsOwnSetup: false,
    hasAssistantProgram: false,
    entryNamedAfterWorkspace: false,
    prefillsDescription: false,
    projectEntryPath: ''
  };
  assert.deepEqual(profile(null), blank);
  assert.deepEqual(profile({ blank: true, builtin: true, name: 'Blank' }), blank);

  assert.deepEqual(profile(selfConfiguringTemplate), {
    blank: false,
    bringsOwnSetup: true,
    hasAssistantProgram: true,
    entryNamedAfterWorkspace: true,
    prefillsDescription: false,
    projectEntryPath: '{{name}}.proj'
  });
  assert.equal(profile({ id: 'wizard', setup_wizard: { id: 'w' } }).bringsOwnSetup, true);
  assert.equal(profile({ id: 'quest', setup_quest: { id: 'q' } }).bringsOwnSetup, true);
  assert.deepEqual(
    profile({ id: 'writing', builtin: true, project_entry: { relative_path: 'outline.md' } }),
    {
      blank: false,
      bringsOwnSetup: false,
      hasAssistantProgram: false,
      entryNamedAfterWorkspace: false,
      prefillsDescription: true,
      projectEntryPath: 'outline.md'
    }
  );
  assert.equal(
    profile({ id: 'dated', project_entry: { relative_path: 'notes-{{date}}.md' } })
      .entryNamedAfterWorkspace,
    false
  );
  // Only stock blueprints offer their description; a stock blueprint whose
  // project file is named after the workspace does not.
  assert.equal(profile({ id: 'user-made' }).prefillsDescription, false);
  assert.equal(
    profile({ id: 'stock-song', builtin: true, project_entry: { relative_path: '{{name}}.proj' } })
      .prefillsDescription,
    false
  );
});

async function runDetailsCreate({
  name = 'Night Drive',
  description = '',
  systems = '',
  context = ''
}) {
  const requests = [];
  const elements = new Map([
    ['folderNameInput', { value: name, focus() {}, classList: { add() {}, remove() {} } }],
    ['folderDescriptionInput', { value: description }],
    ['folderSystemsInput', { value: systems }],
    ['folderContextInput', { value: context }],
    ['folderParentSelect', { value: '' }],
    ['addFolderModal', { dataset: {} }],
    ['createFolderBtn', { textContent: 'Create workspace', disabled: false }],
    ['folderImportToggle', { checked: false }]
  ]);
  const window = {
    location: { href: '' },
    ProjectTemplateCard: {
      recheckSelection: async () => ({ state: 'ready' }),
      getPayloadFields: () => ({ template_id: 'research-project' }),
      getSelectedTemplate: () => ({ id: 'research-project' }),
      shouldOpenAfterCreate: () => false,
      reset() {}
    },
    OriTagInput: { clearTagPoolCache() {} }
  };
  vm.runInNewContext(
    source,
    {
      window,
      document: {
        addEventListener() {},
        getElementById: id => elements.get(id) || null,
        querySelector: selector =>
          selector === '#addFolderModal .folder-color-btn.active'
            ? { dataset: { color: '' } }
            : null
      },
      bootstrap: { Modal: { getInstance: () => ({ hide() {} }) } },
      fetch: async (url, options) => {
        requests.push({ url, body: JSON.parse(options.body) });
        return {
          ok: true,
          status: 201,
          json: async () => ({ folder: { id: 'ws-1', folder_slug: 'night-drive' } })
        };
      },
      console,
      crypto: { randomUUID: () => 'details-request' }
    },
    { filename: 'sessions.js' }
  );
  const manager = window.sessionManager;
  manager.teamView = () => ({
    canContinueFromTeam: true,
    payload: { team_intent: { version: 1, mode: 'staffed' }, role_staffing: [] }
  });
  manager.clearWorkspaceCreateError = () => {};
  manager.showToast = () => {};
  manager.resetAddWorkspaceModalForm = () => {};
  await manager.createFolder();
  const create = requests.find(request => request.url === '/api/workspaces');
  return create?.body || null;
}

test('context typed into Advanced still reaches workspace_bootstrap (FR 10)', async () => {
  const body = await runDetailsCreate({
    systems: 'Mixer, Sample library',
    context: '~/Music/References'
  });
  assert.deepEqual(JSON.parse(JSON.stringify(body.workspace_bootstrap)), {
    goal: '',
    systems: 'Mixer, Sample library',
    context: '~/Music/References'
  });
});

test('the collapsed Advanced summary says when agent context was added (FR 11)', () => {
  const elements = {
    folderBehaviorHint: { textContent: '' },
    folderPresetSelect: { value: 'general' },
    folderSystemsInput: { value: '' },
    folderContextInput: { value: '' }
  };
  const manager = loadSessionManager(undefined, {}, { getElementById: id => elements[id] || null });
  manager.updateBehaviorHint();
  assert.equal(elements.folderBehaviorHint.textContent, 'Agent behavior: General');

  // Also how a seeded open (context filled while Advanced is collapsed) reads.
  elements.folderContextInput.value = 'Brief in ~/Docs';
  manager.updateBehaviorHint();
  assert.equal(elements.folderBehaviorHint.textContent, 'Agent behavior: General · Context added');

  elements.folderContextInput.value = '   ';
  elements.folderSystemsInput.value = 'Mixer';
  manager.updateBehaviorHint();
  assert.match(elements.folderBehaviorHint.textContent, /Context added$/);
});

test('a self-configuring blueprint hides and clears the folder override, once announced (FR 11a)', () => {
  const elements = {
    projectTemplatePathField: { hidden: false },
    projectTemplatePathInput: { value: '' },
    workspaceDetailsLiveRegion: { textContent: '' }
  };
  const manager = loadSessionManager(undefined, {}, { getElementById: id => elements[id] || null });
  let planRefreshes = 0;
  manager.scheduleTemplateAgentPlanRefresh = () => {
    planRefreshes += 1;
  };

  // Blank with a typed path: the override stays, exactly as before.
  elements.projectTemplatePathInput.value = '/Users/me/template-folder';
  manager.templateFolderOverridePath = elements.projectTemplatePathInput.value;
  manager.syncTemplateFolderOverride(null);
  assert.equal(elements.projectTemplatePathField.hidden, false);
  assert.equal(elements.projectTemplatePathInput.value, '/Users/me/template-folder');
  assert.equal(elements.workspaceDetailsLiveRegion.textContent, '');

  // Choosing a blueprint that sets up its own project removes it and says so.
  manager.syncTemplateFolderOverride(selfConfiguringTemplate);
  assert.equal(elements.projectTemplatePathField.hidden, true);
  assert.equal(elements.projectTemplatePathInput.value, '');
  assert.equal(planRefreshes, 1, 'the plan no longer describes the removed folder');
  assert.equal(
    elements.workspaceDetailsLiveRegion.textContent,
    'Removed the template folder override. This blueprint sets up its own project.'
  );

  // A readiness recheck re-emitting the same blueprint announces nothing new.
  elements.workspaceDetailsLiveRegion.textContent = '';
  manager.syncTemplateFolderOverride({ ...selfConfiguringTemplate });
  assert.equal(elements.workspaceDetailsLiveRegion.textContent, '');

  // The picker empties the path itself before a library blueprint is emitted;
  // the remembered path is what makes the removal announceable.
  manager.syncTemplateFolderOverride(null);
  manager.templateFolderOverridePath = '/Users/me/other-folder';
  elements.projectTemplatePathInput.value = '';
  manager.syncTemplateFolderOverride(selfConfiguringTemplate);
  assert.match(elements.workspaceDetailsLiveRegion.textContent, /Removed the template folder/);

  // Back to Blank or an ordinary blueprint: the control returns, empty.
  manager.syncTemplateFolderOverride({ id: 'research-project' });
  assert.equal(elements.projectTemplatePathField.hidden, false);
  assert.equal(elements.projectTemplatePathInput.value, '');
});

// ---------------------------------------------------------------------------
// Name, description, and copy (blueprint-aware Details FR 13–16, §6.2).
// ---------------------------------------------------------------------------

function nameAndDescriptionManager() {
  const input = value => ({ value, dataset: {}, classList: { contains: () => false } });
  const elements = {
    folderNameInput: input(''),
    folderDescriptionInput: input(''),
    folderDescriptionHelp: { textContent: '' },
    workspaceNameHint: { textContent: '', hidden: true, classList: { contains: () => false } }
  };
  const manager = loadSessionManager(undefined, creatorWindowHelpers(), {
    getElementById: id => elements[id] || null,
    querySelectorAll: () => []
  });
  manager.closeWorkspaceAgentSetup = () => {};
  manager.applyTemplateBehavior = () => {};
  manager.updateWizardRecap = () => {};
  manager.refreshTemplateAgentPlan = async () => {};
  manager.refreshWorkspaceReview = () => {};
  manager.beginWorkspaceCreatorContext({ entryPoint: 'workspace_hub_create' });
  return { manager, elements };
}

const catalogTemplate = {
  id: 'research-project',
  name: 'Research Project',
  builtin: true,
  description: 'Synthesis docs, sources, weekly reading.'
};

test('only a stock blueprint prefills its description into the workspace (FR 13)', () => {
  const { manager, elements } = nameAndDescriptionManager();
  const description = elements.folderDescriptionInput;

  manager.handleWorkspaceTemplateSelected(catalogTemplate);
  assert.equal(description.value, catalogTemplate.description, 'stock blueprint');

  // Switching to a plugin blueprint clears that autofill rather than keeping
  // another blueprint's text.
  manager.handleWorkspaceTemplateSelected(selfConfiguringTemplate);
  assert.equal(description.value, '', 'plugin blueprint');

  manager.handleWorkspaceTemplateSelected({
    id: 'my-template',
    name: 'My Template',
    description: 'A template I made.'
  });
  assert.equal(description.value, '', 'user blueprint');

  manager.handleWorkspaceTemplateSelected(catalogTemplate);
  manager.handleWorkspaceTemplateSelected(null);
  assert.equal(description.value, '', 'Blank clears the autofill');

  // Typed text survives any number of blueprint switches.
  description.value = 'Album two, side B';
  manager.handleWorkspaceTemplateSelected(catalogTemplate);
  manager.handleWorkspaceTemplateSelected(selfConfiguringTemplate);
  manager.handleWorkspaceTemplateSelected(null);
  assert.equal(description.value, 'Album two, side B');
});

test('description help follows every blueprint selection (FR 14)', () => {
  const { manager, elements } = nameAndDescriptionManager();
  const help = elements.folderDescriptionHelp;
  manager.handleWorkspaceTemplateSelected(catalogTemplate);
  assert.equal(help.textContent, 'Optional. What this workspace is for.');
  manager.handleWorkspaceTemplateSelected({ blank: true });
  assert.match(help.textContent, /^Describe what this workspace is for so Ori can review/);
  manager.handleWorkspaceTemplateSelected(selfConfiguringTemplate);
  assert.equal(help.textContent, 'Optional. What this workspace is for.');
});

test('a project file named after the workspace is not named after the blueprint (FR 15)', () => {
  const { manager, elements } = nameAndDescriptionManager();
  const name = elements.folderNameInput;
  const hint = elements.workspaceNameHint;

  // Other blueprints keep today's prefill and folder-only hint.
  manager.handleWorkspaceTemplateSelected(catalogTemplate);
  assert.equal(name.value, 'Research Project');
  assert.equal(hint.textContent, 'Folder: research-project');

  // A {{name}} entry clears that autofill instead of prefilling "Song".
  manager.handleWorkspaceTemplateSelected(selfConfiguringTemplate);
  assert.equal(name.value, '');
  assert.equal(hint.hidden, true);

  name.value = 'Night Drive';
  manager.updateWorkspaceNameHint();
  assert.equal(hint.textContent, 'Folder: night-drive · Project file: night-drive.proj');

  // Typed names are never replaced by a later blueprint's name.
  manager.handleWorkspaceTemplateSelected(catalogTemplate);
  assert.equal(name.value, 'Night Drive');
  assert.equal(hint.textContent, 'Folder: night-drive');

  // A {{date}} entry depends on the server's clock, so only the folder shows.
  manager.handleWorkspaceTemplateSelected({
    id: 'journal',
    name: 'Journal',
    project_entry: { relative_path: '{{name}}-{{date}}.md' }
  });
  assert.equal(hint.textContent, 'Folder: night-drive');
});

test('the name hint slugs exactly like the server (shared vectors with workspace.Slugify)', () => {
  const vectors = JSON.parse(
    readFileSync(
      new URL('../../../../workspace/testdata/slugify_vectors.json', import.meta.url),
      'utf8'
    )
  ).vectors;
  assert.ok(vectors.length > 0);
  const manager = loadSessionManager();
  for (const vector of vectors) {
    assert.equal(
      manager.slugifyWorkspaceName(vector.input),
      vector.slug,
      `slug for ${JSON.stringify(vector.input)}`
    );
  }
});

test('an empty description is never sent as a goal (FR 16)', async () => {
  const nothing = await runDetailsCreate({ description: '', systems: '', context: '' });
  assert.equal(nothing.description, '');
  assert.equal('workspace_bootstrap' in nothing, false);

  const contextOnly = await runDetailsCreate({
    description: '  ',
    context: 'Stems are in ~/Audio'
  });
  assert.equal(contextOnly.description, '');
  assert.equal(contextOnly.workspace_bootstrap.goal, '');
  assert.equal(contextOnly.workspace_bootstrap.context, 'Stems are in ~/Audio');

  const described = await runDetailsCreate({ description: 'A second album' });
  assert.equal(described.workspace_bootstrap.goal, 'A second album');
});

test('the Grouped choice names the group the workspace would join (§6.2)', () => {
  const copy = { textContent: '' };
  const manager = loadSessionManager(
    undefined,
    {},
    {
      getElementById: id =>
        id === 'workspaceGroupDestinationCard'
          ? { hidden: true }
          : id === 'workspaceGroupCompositionGroupedCopy'
            ? copy
            : null
    }
  );
  manager.workspaceGroupDestinationTemplate = () => null;
  const render = home => {
    manager.groupRequirementDraft = {
      policy: 'recommended',
      composition: '',
      projection: { state: 'choice_required', home }
    };
    manager.renderWorkspaceGroupDestinationCard();
    return copy.textContent;
  };
  assert.equal(
    render({ exists: true, name: 'Studio Home' }),
    'Create inside Studio Home, alongside its other projects.'
  );
  assert.equal(
    render({ exists: false, proposed_name: 'Music Production Home' }),
    'Create inside Music Production Home, alongside its other projects.'
  );
  assert.equal(render(null), 'Create inside the blueprint’s group, alongside its other projects.');
});

test('a successful create announces the hierarchy change for pages that stay put', () => {
  const events = [];
  const { manager } = lockedParentManager();
  manager.notifyWorkspacesChanged({ workspaceId: 'ws-new' });
  // The stub window has no dispatchEvent, so nothing is thrown and nothing is
  // sent; with one, the event carries the created id.
  const withWindow = loadSessionManager(async () => ({ ok: true, json: async () => ({}) }), {
    dispatchEvent: event => events.push(event),
    CustomEvent: class {
      constructor(type, init) {
        this.type = type;
        this.detail = init && init.detail;
      }
    }
  });
  withWindow.notifyWorkspacesChanged({ workspaceId: 'ws-new' });
  assert.equal(events.length, 1);
  assert.equal(events[0].type, 'ori:workspaces-changed');
  assert.equal(events[0].detail.workspaceId, 'ws-new');
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
