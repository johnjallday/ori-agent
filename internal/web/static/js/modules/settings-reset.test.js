import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./settings-reset.js', import.meta.url), 'utf8');

function element() {
  return {
    value: '',
    checked: false,
    disabled: false,
    hidden: false,
    textContent: '',
    innerHTML: '',
    style: {},
    children: [],
    listeners: {},
    attributes: {},
    appendChild(child) {
      this.children.push(child);
      return child;
    },
    replaceChildren(...children) {
      this.children = children;
      this.textContent = '';
    },
    addEventListener(name, fn) {
      (this.listeners[name] ||= []).push(fn);
    },
    setAttribute(name, value) {
      this.attributes[name] = value;
    },
    removeAttribute(name) {
      delete this.attributes[name];
    },
    focus() {
      this.focused = true;
    },
    async emit(name, event = {}) {
      await Promise.all(
        (this.listeners[name] || []).map(fn => fn.call(this, { preventDefault() {}, ...event }))
      );
    },
    click() {
      return this.disabled ? Promise.resolve() : this.emit('click');
    }
  };
}

const preview = {
  schema_version: 1,
  id: 'preview-1',
  operation_id: 'operation-1',
  intent: 'selected_data',
  selected: ['settings'],
  blockers: [],
  restart: { mode: 'process_relaunch', instructions: 'Fully quit and relaunch Ori.' },
  categories: [
    {
      id: 'settings',
      label: 'Settings & API keys',
      description: 'Reset preferences and exact Ori-saved provider/search keys.',
      facts: [
        { name: 'saved keys', count: 2 },
        { name: 'unavailable domain', count: null, unavailable_reason: 'owner unavailable' }
      ],
      removed: [{ display_path: '/owned/settings.json', reason: 'Reset reviewed fields.' }],
      retained: [{ display_path: '/retained/projects', reason: 'Workspace files are preserved.' }]
    }
  ]
};

function operation(state = 'awaiting_restart', results = []) {
  return {
    operation: {
      schema_version: 1,
      id: 'operation-1',
      intent: 'selected_data',
      state,
      revision: 2,
      results,
      blockers: [],
      restart: { mode: 'process_relaunch', instructions: 'Fully quit and relaunch Ori.' }
    }
  };
}

function deferred() {
  let resolve;
  const promise = new Promise(done => {
    resolve = done;
  });
  return { promise, resolve };
}

function harness({
  previewResult = preview,
  executeResult = operation(),
  storedOperation = '',
  onboardingResult = {
    needs_onboarding: true,
    completed: false,
    steps_completed: [],
    user_name: 'Retained User',
    assistant_name: 'Retained Assistant'
  },
  progressionResult = { completed_count: 0, resolved_count: 0, dismissed: false },
  localEntries = {},
  sessionEntries = {}
} = {}) {
  const elements = Object.fromEntries(
    [
      'resetSettings',
      'resetAgents',
      'resetSessions',
      'resetOnboarding',
      'resetInstalledPlugins',
      'resetAppBtn',
      'startFreshBtn',
      'selectAllResetBtn',
      'clearAllResetBtn',
      'resetConfirmInput',
      'confirmResetBtn',
      'resetConfirmError',
      'resetItemsList',
      'resetConfirmModal',
      'resetConfirmTitleText',
      'resetConfirmWarning',
      'resetOperationStatus',
      'resetOperationResults',
      'resetOperationPanel',
      'openStartFreshSetupBtn',
      'resetCancelBtn',
      'resetCloseBtn',
      'replaySetupBtn',
      'replaySetupStatus',
      'openReplaySetupBtn',
      'resetGettingStartedBtn',
      'resetGettingStartedStatus',
      'openGettingStartedBtn',
      'resetAgentsFolderConfirm',
      'resetAgentsFolderPath',
      'resetAgentsFolderNotice',
      'resetAgentsFolderCheck'
    ].map(id => [id, element()])
  );
  elements.resetAgentsFolderConfirm.hidden = true;
  elements.confirmResetBtn.disabled = true;
  elements.openReplaySetupBtn.hidden = true;
  elements.openGettingStartedBtn.hidden = true;
  elements.openStartFreshSetupBtn.hidden = true;
  const calls = [],
    timers = [],
    navigation = [];
  const storage = new Map(Object.entries(localEntries));
  const sessionStorage = new Map(Object.entries(sessionEntries));
  if (storedOperation) storage.set('ori.reset.operation_id', storedOperation);
  const storageAPI = values => ({
    get length() {
      return values.size;
    },
    key: index => [...values.keys()][index] ?? null,
    getItem: key => values.get(key) || null,
    setItem: (key, value) => values.set(key, value),
    removeItem: key => values.delete(key)
  });
  let modalConstructions = 0;
  const modal = {
    show() {
      elements.resetConfirmModal.hidden = false;
    },
    hide() {
      elements.resetConfirmModal.hidden = true;
    }
  };
  const makeResponse = async (value, defaultStatus = 200) => {
    const resolved = await value;
    const status = resolved?.httpStatus || defaultStatus;
    const body = resolved?.body ?? resolved;
    return { ok: status >= 200 && status < 300, status, json: async () => body };
  };
  const context = {
    console: { error() {} },
    URLSearchParams,
    document: {
      readyState: 'complete',
      getElementById: id => elements[id] || null,
      createElement: () => element(),
      addEventListener() {},
      querySelectorAll: selector =>
        selector === '.reset-category-checkbox'
          ? [
              elements.resetSettings,
              elements.resetAgents,
              elements.resetSessions,
              elements.resetOnboarding,
              elements.resetInstalledPlugins
            ]
          : []
    },
    window: {
      location: { href: '/settings', reload: () => navigation.push('reload') },
      addEventListener() {},
      confirm: () => true,
      crypto: { randomUUID: () => 'request-1' },
      localStorage: storageAPI(storage),
      sessionStorage: storageAPI(sessionStorage)
    },
    bootstrap: {
      Modal: class {
        constructor() {
          modalConstructions += 1;
          return modal;
        }
        static getInstance() {
          return modal;
        }
        static getOrCreateInstance() {
          return modal;
        }
      }
    },
    setTimeout: fn => {
      timers.push(fn);
      return timers.length;
    },
    clearTimeout() {},
    fetch: async (url, options = {}) => {
      calls.push({ url, options });
      if (url.startsWith('/api/reset/preview?')) return makeResponse(previewResult);
      if (url === '/api/reset') return makeResponse(executeResult, 202);
      if (url.startsWith('/api/reset/operations/')) return makeResponse(executeResult);
      if (url === '/api/onboarding/reset') return makeResponse(onboardingResult);
      if (url === '/api/progression/reset') return makeResponse(progressionResult);
      throw new Error(`unexpected URL ${url}`);
    }
  };
  context.window.document = context.document;
  vm.runInNewContext(source, context, { filename: 'settings-reset.js' });
  return {
    elements,
    calls,
    timers,
    navigation,
    context,
    storage,
    sessionStorage,
    modalConstructions: () => modalConstructions,
    async review(ids = ['resetSettings']) {
      for (const id of ids) elements[id].checked = true;
      await elements.resetSettings.emit('change');
      await elements.resetAppBtn.click();
      elements.resetConfirmInput.value = 'RESET';
      await elements.resetConfirmInput.emit('input');
    },
    resultText() {
      const text = node => [node.textContent, ...node.children.map(text)].join(' ');
      return (
        text(elements.resetOperationPanel) +
        text(elements.resetOperationStatus) +
        text(elements.resetOperationResults)
      );
    },
    previewText() {
      const text = node => [node.textContent, ...node.children.map(text)].join(' ');
      return text(elements.resetItemsList);
    }
  };
}

test('Replay Setup requires verified narrow server state and keeps a persistent result', async () => {
  const h = harness();
  await h.elements.replaySetupBtn.click();
  const call = h.calls.find(item => item.url === '/api/onboarding/reset');
  assert.equal(call.options.method, 'POST');
  assert.match(h.elements.replaySetupStatus.textContent, /identity and existing work were kept/i);
  assert.equal(h.elements.replaySetupStatus.focused, true);
  assert.equal(h.elements.replaySetupBtn.attributes['aria-busy'], 'false');
  assert.equal(h.elements.openReplaySetupBtn.hidden, false);
  await h.elements.openReplaySetupBtn.click();
  assert.deepEqual(h.navigation, ['reload']);
});

test('a duplicate Replay Setup click cannot overlap an in-flight request', async () => {
  const response = deferred();
  const h = harness({ onboardingResult: response.promise });
  const first = h.elements.replaySetupBtn.click();
  const second = h.elements.replaySetupBtn.emit('click');
  response.resolve({
    needs_onboarding: true,
    completed: false,
    steps_completed: [],
    user_name: 'Retained User'
  });
  await Promise.all([first, second]);
  assert.equal(h.calls.filter(call => call.url === '/api/onboarding/reset').length, 1);
});

test('Getting Started refuses an unverified response without a success toast or navigation', async () => {
  const h = harness({
    progressionResult: { completed_count: 1, resolved_count: 1, dismissed: false }
  });
  await h.elements.resetGettingStartedBtn.click();
  assert.match(h.elements.resetGettingStartedStatus.textContent, /did not verify|try again/i);
  assert.equal(h.elements.openGettingStartedBtn.hidden, true);
  assert.equal(h.navigation.length, 0);
});

test('review loads and renders authoritative counts, unknowns, removed and retained scope before submission', async () => {
  const h = harness();
  await h.review();
  assert.equal(h.calls.length, 1);
  assert.match(h.calls[0].url, /^\/api\/reset\/preview\?/);
  assert.match(h.calls[0].url, /intent=selected_data/);
  assert.match(h.calls[0].url, /category=settings/);
  assert.match(h.previewText(), /saved keys: 2/);
  assert.match(h.previewText(), /unavailable.*owner unavailable/i);
  assert.match(h.previewText(), /Remove: \/owned\/settings\.json/);
  assert.match(h.previewText(), /Keep: \/retained\/projects/);
  assert.equal(h.elements.confirmResetBtn.disabled, false);
});

test('preview blockers stay literal and prevent destructive submission', async () => {
  const blocked = {
    ...preview,
    blockers: [
      {
        code: 'lifecycle_unavailable',
        message: '<b>Host unavailable</b>',
        recovery: 'Fully relaunch.'
      }
    ]
  };
  const h = harness({ previewResult: blocked });
  await h.review();
  assert.equal(h.elements.confirmResetBtn.disabled, true);
  await h.elements.confirmResetBtn.emit('click');
  assert.equal(h.calls.filter(call => call.url === '/api/reset').length, 0);
  assert.match(h.previewText(), /<b>Host unavailable<\/b>/);
  assert.equal(
    h.elements.resetItemsList.children.some(node => node.innerHTML),
    false
  );
});

test('restart-required remains pending, visible, and never substitutes a timed reload', async () => {
  const h = harness();
  await h.review();
  await h.elements.confirmResetBtn.click();
  assert.equal(h.timers.length, 0);
  assert.match(h.resultText(), /restart|required|relaunch/i);
  assert.equal(h.storage.get('ori.reset.operation_id'), 'operation-1');
});

test('a real mixed operation keeps completed and failed categories visible', async () => {
  const h = harness({
    executeResult: operation('partial_failure', [
      { id: 'settings', outcome: 'completed', checks: [] },
      { id: 'agents', outcome: 'failed', message: '<img src=x onerror=alert(1)>', checks: [] }
    ])
  });
  await h.review(['resetSettings', 'resetAgents']);
  await h.elements.confirmResetBtn.click();
  assert.match(h.resultText(), /Settings & API keys: completed/);
  assert.match(h.resultText(), /Agents: failed/);
  const failed = h.elements.resetOperationResults.children.find(node =>
    node.textContent.includes('<img')
  );
  assert.ok(failed);
  assert.equal(failed.innerHTML, '');
});

test('editing confirmation cannot re-enable an in-flight destructive button', async () => {
  const response = deferred();
  const h = harness({ executeResult: response.promise });
  await h.review();
  const submitted = h.elements.confirmResetBtn.click();
  h.elements.resetConfirmInput.value = 'RESET';
  await h.elements.resetConfirmInput.emit('input');
  assert.equal(h.elements.confirmResetBtn.disabled, true);
  response.resolve(operation());
  await submitted;
});

test('duplicate click and Enter cannot start another request while one is in flight', async () => {
  const response = deferred();
  const h = harness({ executeResult: response.promise });
  await h.review();
  const first = h.elements.confirmResetBtn.click();
  const second = h.elements.confirmResetBtn.emit('click');
  await h.elements.resetConfirmInput.emit('keydown', { key: 'Enter' });
  response.resolve(operation());
  await Promise.all([first, second]);
  assert.equal(h.calls.filter(call => call.url === '/api/reset').length, 1);
});

test('scope changes after review cannot broaden the server-held preview', async () => {
  const h = harness();
  await h.review();
  h.elements.resetAgents.checked = true;
  await h.elements.resetAgents.emit('change');
  await h.elements.confirmResetBtn.emit('click');
  assert.equal(h.calls.filter(call => call.url === '/api/reset').length, 0);
  assert.equal(h.calls[0].url.includes('category=agents'), false);
});

test('submission contains only preview, request identity, and exact confirmation', async () => {
  const template = readFileSync(
    new URL('../../../templates/pages/settings.tmpl', import.meta.url),
    'utf8'
  );
  const page = readFileSync(new URL('./settings-page.js', import.meta.url), 'utf8');
  assert.equal(template.match(/src="\/js\/modules\/settings-reset\.js"/g)?.length, 1);
  assert.equal(page.includes("fetch('/api/reset'"), false);
  const h = harness();
  await h.review();
  await h.elements.confirmResetBtn.click();
  const post = h.calls.find(call => call.url === '/api/reset');
  assert.equal(post.options.headers['X-Requested-With'], 'XMLHttpRequest');
  assert.equal(post.options.headers['Content-Type'], 'application/json');
  assert.deepEqual(JSON.parse(post.options.body), {
    preview_id: 'preview-1',
    request_id: 'request-1',
    confirmation: 'RESET'
  });
});

const agentsPreview = {
  ...preview,
  selected: ['agents'],
  categories: [
    {
      id: 'agents',
      label: 'Agents',
      description: 'Remove agents.',
      facts: [{ name: 'agent folders in your Workspace Directory', count: 2 }],
      removed: [{ display_path: '/Users/me/Ori Workspaces/Agents', reason: 'Remove each agent.' }],
      retained: []
    }
  ],
  agents_folder: {
    path: '/Users/me/Ori Workspaces/Agents',
    notice: 'This folder is in your Workspace Directory and may be synced to your other machines.',
    confirmation_required: true
  }
};

test('an agents reset shows its folder and needs the second confirmation before it can be sent', async () => {
  const h = harness({ previewResult: agentsPreview });
  await h.review(['resetAgents']);
  const { resetAgentsFolderConfirm, resetAgentsFolderPath, resetAgentsFolderNotice } = h.elements;
  assert.equal(resetAgentsFolderConfirm.hidden, false);
  assert.equal(resetAgentsFolderPath.textContent, '/Users/me/Ori Workspaces/Agents');
  assert.match(resetAgentsFolderNotice.textContent, /synced to your other machines/);

  // Typing RESET alone is not enough.
  assert.equal(h.elements.confirmResetBtn.disabled, true);
  await h.elements.confirmResetBtn.emit('click');
  assert.equal(h.calls.filter(call => call.url === '/api/reset').length, 0);

  h.elements.resetAgentsFolderCheck.checked = true;
  await h.elements.resetAgentsFolderCheck.emit('change');
  assert.equal(h.elements.confirmResetBtn.disabled, false);
  await h.elements.confirmResetBtn.click();
  const post = h.calls.find(call => call.url === '/api/reset');
  assert.deepEqual(JSON.parse(post.options.body), {
    preview_id: 'preview-1',
    request_id: 'request-1',
    confirmation: 'RESET',
    confirm_agents_folder: '/Users/me/Ori Workspaces/Agents'
  });
});

test('closing an agents review clears the second confirmation', async () => {
  const h = harness({ previewResult: agentsPreview });
  await h.review(['resetAgents']);
  h.elements.resetAgentsFolderCheck.checked = true;
  await h.elements.resetAgentsFolderCheck.emit('change');
  await h.elements.resetConfirmModal.emit('hidden.bs.modal');
  assert.equal(h.elements.resetAgentsFolderCheck.checked, false);
  assert.equal(h.elements.resetAgentsFolderConfirm.hidden, true);
});

test('a reset without agents never shows the agents folder', async () => {
  const h = harness();
  await h.review();
  assert.equal(h.elements.resetAgentsFolderConfirm.hidden, true);
  assert.equal(h.elements.confirmResetBtn.disabled, false);
});

test('a refused agents confirmation returns to review instead of locking the page', async () => {
  const h = harness({
    previewResult: agentsPreview,
    executeResult: {
      httpStatus: 409,
      body: {
        code: 'agents_folder_confirmation_required',
        message: 'Confirm that folder too, then reset again.'
      }
    }
  });
  await h.review(['resetAgents']);
  h.elements.resetAgentsFolderCheck.checked = true;
  await h.elements.resetAgentsFolderCheck.emit('change');
  await h.elements.confirmResetBtn.click();
  assert.match(h.resultText(), /Confirm that folder too/);
  assert.equal(h.elements.resetAppBtn.disabled, false);
});

test('canceling review does not submit and restores focus and selection controls', async () => {
  const h = harness();
  await h.review();
  await h.elements.resetConfirmModal.emit('hidden.bs.modal');
  assert.equal(h.calls.filter(call => call.url === '/api/reset').length, 0);
  assert.equal(h.elements.resetSettings.disabled, false);
  assert.equal(h.elements.resetAppBtn.focused, true);
  assert.equal(h.elements.confirmResetBtn.disabled, true);
});

test('Start Fresh is a distinct reviewed intent and clears only enumerated browser state after verified completion', async () => {
  const freshPreview = {
    ...preview,
    id: 'fresh-preview',
    operation_id: 'fresh-operation',
    intent: 'start_fresh',
    selected: ['settings', 'agents', 'app_records', 'identity_progress'],
    categories: preview.categories
  };
  const completed = operation('completed');
  completed.operation.id = 'fresh-operation';
  completed.operation.intent = 'start_fresh';
  const h = harness({
    previewResult: freshPreview,
    executeResult: completed,
    localEntries: {
      'ori-theme': 'dark',
      ori_chat_session_old: 'old',
      oriTabId: 'keep-tab',
      'unrelated-app-key': 'keep'
    },
    sessionEntries: {
      'oriSetupWizardResume:old': 'old',
      oriTabId: 'keep-tab',
      'unrelated-session-key': 'keep'
    }
  });

  await h.elements.startFreshBtn.click();
  assert.equal(h.calls[0].url, '/api/reset/preview?intent=start_fresh');
  assert.match(h.elements.confirmResetBtn.textContent, /Start Fresh/i);
  assert.match(h.elements.resetConfirmTitleText.textContent, /Start Fresh/i);
  assert.match(h.elements.resetConfirmWarning.textContent, /detached/i);
  assert.match(h.previewText(), /Start Fresh impact: 1 category/i);
  h.elements.resetConfirmInput.value = 'RESET';
  await h.elements.resetConfirmInput.emit('input');
  await h.elements.confirmResetBtn.click();

  assert.equal(h.storage.has('ori-theme'), false);
  assert.equal(h.storage.has('ori_chat_session_old'), false);
  assert.equal(h.sessionStorage.has('oriSetupWizardResume:old'), false);
  assert.equal(h.storage.get('ori.reset.operation_id'), 'fresh-operation');
  assert.equal(h.storage.get('ori.reset.generation'), 'fresh-operation');
  assert.equal(h.elements.openStartFreshSetupBtn.hidden, false);
  await h.elements.openStartFreshSetupBtn.click();
  assert.equal(h.context.window.location.href, '/');
  assert.equal(h.storage.get('oriTabId'), 'keep-tab');
  assert.equal(h.sessionStorage.get('oriTabId'), 'keep-tab');
  assert.equal(h.storage.get('unrelated-app-key'), 'keep');
  assert.equal(h.sessionStorage.get('unrelated-session-key'), 'keep');
});

test('pending Start Fresh preserves browser data and its recovery receipt', async () => {
  const freshPreview = { ...preview, intent: 'start_fresh' };
  const pending = operation('awaiting_restart');
  pending.operation.intent = 'start_fresh';
  const h = harness({
    previewResult: freshPreview,
    executeResult: pending,
    localEntries: { 'ori-theme': 'dark', ori_chat_session_old: 'old' },
    sessionEntries: { 'oriSetupWizardResume:old': 'old' }
  });
  await h.elements.startFreshBtn.click();
  h.elements.resetConfirmInput.value = 'RESET';
  await h.elements.resetConfirmInput.emit('input');
  await h.elements.confirmResetBtn.click();
  assert.equal(h.storage.get('ori-theme'), 'dark');
  assert.equal(h.storage.get('ori_chat_session_old'), 'old');
  assert.equal(h.sessionStorage.get('oriSetupWizardResume:old'), 'old');
  assert.equal(h.storage.get('ori.reset.operation_id'), 'operation-1');
  assert.equal(h.storage.has('ori.reset.generation'), false);
  assert.equal(h.elements.openStartFreshSetupBtn.hidden, true);
});

test('saved operation identity recovers durable status after a page load', async () => {
  const h = harness({
    storedOperation: 'operation-1',
    executeResult: operation('partial_failure', [
      { id: 'settings', outcome: 'failed', message: 'retry later', checks: [] }
    ])
  });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(h.calls[0].url, '/api/reset/operations/operation-1');
  assert.match(h.resultText(), /partial|retry later/i);
});

// --- Installed plugins ------------------------------------------------------

const pluginPreview = {
  ...preview,
  id: 'plugin-preview',
  operation_id: 'plugin-operation',
  selected: ['installed_plugins'],
  categories: [
    {
      id: 'installed_plugins',
      label: 'Installed plugins',
      description: 'Uninstall every plugin this Ori installation has installed.',
      facts: [
        { name: 'installed plugins', count: 2 },
        { name: 'plugin-copied personal skills removed', count: 2 },
        { name: 'linked plugin sources kept', count: 1 }
      ],
      items: [
        {
          name: 'demo-managed',
          summary: 'version 1.4.0 · enabled · managed clone (removed)',
          details: ['MCP registrations: demo-managed/tools', 'Personal skills: demo-managed-skill']
        },
        {
          name: 'demo-linked',
          summary: 'version 0.2.0 · disabled · linked source (kept)',
          details: ['Personal skills: demo-linked-skill']
        }
      ],
      removed: [
        { display_path: '/home/.agents/skills/demo-managed-skill', reason: 'Exact copied skill.' }
      ],
      retained: [
        { display_path: '/data/plugins/marketplaces.json', reason: 'Marketplaces are preserved.' }
      ]
    }
  ]
};

test('Installed plugins is a selectable fifth category with its own preview query', async () => {
  const h = harness({ previewResult: pluginPreview });
  await h.review(['resetInstalledPlugins']);
  assert.equal(h.calls.length, 1);
  assert.match(h.calls[0].url, /intent=selected_data/);
  assert.match(h.calls[0].url, /category=installed_plugins/);
  assert.equal(h.calls[0].url.includes('category=settings'), false);
  assert.equal(h.elements.confirmResetBtn.disabled, false);
  // Start Fresh stays a distinct control and intent.
  assert.match(h.elements.resetConfirmTitleText.textContent, /reviewed data reset/i);
  assert.equal(h.elements.startFreshBtn.disabled, true);
});

test('Select every category selects five boxes and stays a selected-data reset', async () => {
  const h = harness({ previewResult: pluginPreview });
  await h.elements.selectAllResetBtn.click();
  const boxes = [
    'resetSettings',
    'resetAgents',
    'resetSessions',
    'resetOnboarding',
    'resetInstalledPlugins'
  ];
  for (const id of boxes) assert.equal(h.elements[id].checked, true, `${id} was not selected`);
  await h.elements.clearAllResetBtn.click();
  for (const id of boxes) assert.equal(h.elements[id].checked, false, `${id} was not cleared`);

  await h.elements.selectAllResetBtn.click();
  await h.elements.resetAppBtn.click();
  assert.match(h.calls[0].url, /intent=selected_data/);
  assert.equal(h.calls[0].url.includes('intent=start_fresh'), false);
  for (const category of [
    'settings',
    'agents',
    'app_records',
    'setup_steps',
    'installed_plugins'
  ]) {
    assert.ok(h.calls[0].url.includes(`category=${category}`), `${category} was not requested`);
  }
  // Choices lock once review begins, so neither quick-select control can widen
  // a held preview.
  await h.elements.clearAllResetBtn.click();
  for (const id of boxes)
    assert.equal(h.elements[id].checked, true, `${id} was cleared mid-review`);
});

test('plugin review names each plugin and its components as literal text', async () => {
  const h = harness({ previewResult: pluginPreview });
  await h.review(['resetInstalledPlugins']);
  assert.match(h.previewText(), /installed plugins: 2/);
  assert.match(h.previewText(), /Item: demo-managed/);
  assert.match(h.previewText(), /managed clone \(removed\)/);
  assert.match(h.previewText(), /Item: demo-linked/);
  assert.match(h.previewText(), /linked source \(kept\)/);
  assert.match(h.previewText(), /MCP registrations: demo-managed\/tools/);
  assert.match(h.previewText(), /Remove: \/home\/\.agents\/skills\/demo-managed-skill/);
  assert.match(h.previewText(), /Keep: \/data\/plugins\/marketplaces\.json/);
  assert.equal(
    h.elements.resetItemsList.children.some(node => node.innerHTML),
    false
  );
});

test('untrusted plugin text renders literally and never as markup', async () => {
  const hostile = {
    ...pluginPreview,
    categories: [
      {
        ...pluginPreview.categories[0],
        items: [
          {
            name: '<img src=x onerror=alert(1)>',
            summary: '<script>alert(2)</script>',
            details: ['<b>not bold</b>']
          }
        ]
      }
    ]
  };
  const h = harness({ previewResult: hostile });
  await h.review(['resetInstalledPlugins']);
  assert.match(h.previewText(), /<img src=x onerror=alert\(1\)>/);
  assert.match(h.previewText(), /<script>alert\(2\)<\/script>/);
  assert.match(h.previewText(), /<b>not bold<\/b>/);
  assert.equal(
    h.elements.resetItemsList.children.some(node => node.innerHTML),
    false
  );
});

test('zero installed plugins reviews as a safe no-op rather than a blocker', async () => {
  const empty = {
    ...pluginPreview,
    categories: [
      {
        ...pluginPreview.categories[0],
        facts: [{ name: 'installed plugins', count: 0 }],
        items: [],
        removed: []
      }
    ]
  };
  const h = harness({ previewResult: empty });
  await h.review(['resetInstalledPlugins']);
  assert.match(h.previewText(), /installed plugins: 0/);
  assert.equal(h.previewText().includes('Item:'), false);
  assert.equal(h.elements.confirmResetBtn.disabled, false);
});

test('unsafe plugin ownership blocks confirmation with the server message', async () => {
  const blocked = {
    ...pluginPreview,
    blockers: [
      {
        code: 'plugin_skill_ownership_ambiguous',
        category: 'installed_plugins',
        message: 'Two installed plugins claim the same personal skill directory.',
        recovery: 'Uninstall one of them manually, then review reset again.'
      }
    ]
  };
  const h = harness({ previewResult: blocked });
  await h.review(['resetInstalledPlugins']);
  assert.equal(h.elements.confirmResetBtn.disabled, true);
  await h.elements.confirmResetBtn.emit('click');
  assert.equal(h.calls.filter(call => call.url === '/api/reset').length, 0);
  assert.match(h.previewText(), /claim the same personal skill directory/);
  assert.match(h.previewText(), /Uninstall one of them manually/);
});

test('a partial plugin failure names the unresolved plugin and keeps the recovery identity', async () => {
  const partial = operation('partial_failure', [
    {
      id: 'installed_plugins',
      outcome: 'failed',
      message: 'One or more installed plugins could not be removed and verified.',
      retryable: true,
      checks: [{ name: 'plugin_components_absent', outcome: 'failed' }],
      items: [
        { name: 'demo-managed', outcome: 'completed' },
        {
          name: 'demo-linked',
          outcome: 'failed',
          message: "One or more of this plugin's components remain."
        }
      ]
    }
  ]);
  partial.operation.id = 'plugin-operation';
  const h = harness({ previewResult: pluginPreview, executeResult: partial });
  await h.review(['resetInstalledPlugins']);
  await h.elements.confirmResetBtn.click();
  assert.match(h.resultText(), /Installed plugins: failed/);
  assert.match(h.resultText(), /Installed plugins \/ demo-managed: completed/);
  assert.match(h.resultText(), /Installed plugins \/ demo-linked: failed/);
  assert.match(h.resultText(), /partially completed|retry only unresolved/i);
  assert.equal(/rolled back|nothing was deleted/i.test(h.resultText()), false);
  assert.equal(h.storage.get('ori.reset.operation_id'), 'plugin-operation');
});

test('a recovered plugin reset reports verified completion per plugin', async () => {
  const completed = operation('completed', [
    {
      id: 'installed_plugins',
      outcome: 'completed',
      checks: [{ name: 'plugin_components_absent', outcome: 'completed' }],
      items: [
        { name: 'demo-managed', outcome: 'completed' },
        { name: 'demo-linked', outcome: 'completed' }
      ]
    }
  ]);
  completed.operation.id = 'plugin-operation';
  const h = harness({ storedOperation: 'plugin-operation', executeResult: completed });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(h.calls[0].url, '/api/reset/operations/plugin-operation');
  assert.match(h.resultText(), /completed and its named postconditions were verified/i);
  assert.match(h.resultText(), /Installed plugins \/ demo-linked: completed/);
  assert.equal(h.elements.openStartFreshSetupBtn.hidden, true);
});

test('repeated reviews reuse one dialog instance so Cancel always closes it', async () => {
  const h = harness({ previewResult: pluginPreview });
  await h.review(['resetInstalledPlugins']);
  await h.elements.resetConfirmModal.emit('hidden.bs.modal');
  // A second review of a different scope must not construct a competing dialog
  // instance: the stale one keeps its listeners and can leave the reviewed
  // dialog stuck open with the destructive controls behind it.
  h.elements.resetInstalledPlugins.checked = false;
  await h.elements.startFreshBtn.click();
  assert.equal(h.modalConstructions(), 0, 'a fresh Modal instance was constructed');
  assert.equal(h.elements.resetConfirmModal.hidden, false);
  await h.elements.resetConfirmModal.emit('hidden.bs.modal');
  assert.equal(h.elements.resetAppBtn.disabled, true, 'no selection should re-lock review');
  assert.equal(h.elements.startFreshBtn.disabled, false);
});

test('the Installed plugins checkbox is described and kept out of Start Fresh', () => {
  const template = readFileSync(
    new URL('../../../templates/pages/settings.tmpl', import.meta.url),
    'utf8'
  );
  assert.match(template, /id="resetInstalledPlugins"/);
  assert.match(template, /aria-describedby="resetInstalledPluginsHelp"/);
  assert.match(template, /id="resetInstalledPluginsHelp"/);
  assert.match(template, /for="resetInstalledPlugins"[^>]*>\s*<strong>Installed plugins<\/strong>/);
  // Copy states both sides of the contract.
  const help = template.slice(template.indexOf('resetInstalledPluginsHelp'));
  assert.match(help, /marketplaces/i);
  assert.match(help, /linked source/i);
  // Start Fresh remains its own separately described control.
  assert.match(template, /id="startFreshBtn"/);
  assert.equal(
    /id="startFreshBtn"[^>]*>[\s\S]{0,200}Installed plugins/.test(template),
    false,
    'Start Fresh must not present itself as the installed-plugins control'
  );
});
