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
  progressionResult = { completed_count: 0, resolved_count: 0, dismissed: false }
} = {}) {
  const elements = Object.fromEntries(
    [
      'resetSettings',
      'resetAgents',
      'resetSessions',
      'resetOnboarding',
      'resetAppBtn',
      'selectAllResetBtn',
      'clearAllResetBtn',
      'resetConfirmInput',
      'confirmResetBtn',
      'resetConfirmError',
      'resetItemsList',
      'resetConfirmModal',
      'resetOperationStatus',
      'resetOperationResults',
      'resetOperationPanel',
      'resetCancelBtn',
      'resetCloseBtn',
      'replaySetupBtn',
      'replaySetupStatus',
      'openReplaySetupBtn',
      'resetGettingStartedBtn',
      'resetGettingStartedStatus',
      'openGettingStartedBtn'
    ].map(id => [id, element()])
  );
  elements.confirmResetBtn.disabled = true;
  elements.openReplaySetupBtn.hidden = true;
  elements.openGettingStartedBtn.hidden = true;
  const calls = [],
    timers = [],
    navigation = [];
  const storage = new Map();
  if (storedOperation) storage.set('ori.reset.operation_id', storedOperation);
  const modal = {
    show() {},
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
      querySelectorAll: () => []
    },
    window: {
      location: { href: '/settings', reload: () => navigation.push('reload') },
      addEventListener() {},
      confirm: () => true,
      crypto: { randomUUID: () => 'request-1' },
      localStorage: {
        getItem: key => storage.get(key) || null,
        setItem: (key, value) => storage.set(key, value),
        removeItem: key => storage.delete(key)
      }
    },
    bootstrap: {
      Modal: class {
        constructor() {
          return modal;
        }
        static getInstance() {
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

test('canceling review does not submit and restores focus and selection controls', async () => {
  const h = harness();
  await h.review();
  await h.elements.resetConfirmModal.emit('hidden.bs.modal');
  assert.equal(h.calls.filter(call => call.url === '/api/reset').length, 0);
  assert.equal(h.elements.resetSettings.disabled, false);
  assert.equal(h.elements.resetAppBtn.focused, true);
  assert.equal(h.elements.confirmResetBtn.disabled, true);
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
