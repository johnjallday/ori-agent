import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./reaper-readiness-panel.js', import.meta.url), 'utf8');

class FakeElement {
  constructor(tag = 'div') {
    this.tagName = String(tag).toUpperCase();
    this.className = '';
    this.children = [];
    this._text = '';
    this.listeners = {};
    this.attributes = {};
    this.disabled = false;
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
  setAttribute(name, value) {
    this.attributes[name] = value;
  }
  addEventListener(type, handler) {
    (this.listeners[type] ||= []).push(handler);
  }
  removeEventListener(type, handler) {
    this.listeners[type] = (this.listeners[type] || []).filter(candidate => candidate !== handler);
  }
  querySelector(selector) {
    if (!selector.startsWith('.')) return null;
    const className = selector.slice(1);
    if (this.className.split(/\s+/).includes(className)) return this;
    for (const child of this.children) {
      const found = child.querySelector?.(selector);
      if (found) return found;
    }
    return null;
  }
  focus() {
    this.focused = true;
  }
}

function findButtons(root) {
  const out = [];
  const visit = node => {
    const isActionLink = node.tagName === 'A' && node.className.split(/\s+/).includes('modern-btn');
    if (node.tagName === 'BUTTON' || isActionLink) out.push(node);
    node.children.forEach(visit);
  };
  visit(root);
  return out;
}

async function click(node) {
  for (const handler of node.listeners.click || []) await handler();
}

function runtime(overrides = {}) {
  return {
    workspace_id: 'ws-1',
    applicable: true,
    selected_mode_id: 'ori_assisted',
    durable_state: 'in_progress',
    live_state: 'not_checked',
    requirements: [
      {
        key: 'reaper_live_control',
        label: 'Local REAPER control',
        durable_state: 'in_progress',
        live_state: 'not_checked',
        reason_code: 'web_remote_unconfigured',
        summary: 'REAPER Web Remote is not configured.',
        disclosure: 'The selected agent may use loopback and the dedicated runner directory.'
      }
    ],
    first_blocker: {
      requirement_key: 'reaper_live_control',
      reason_code: 'web_remote_unconfigured',
      summary: 'REAPER Web Remote is not configured.',
      action: { token: 'enable_web_remote', code: 'enable_web_remote', label: 'Enable Web Remote' }
    },
    ...overrides
  };
}

function load({ fetchImpl } = {}) {
  const renderers = new Map();
  const modelModal = new FakeElement('div');
  const document = {
    readyState: 'complete',
    createElement: tag => new FakeElement(tag),
    getElementById: id => (id === 'workspace-detail-agent-model-modal' ? modelModal : null),
    addEventListener() {},
    querySelector() {
      return null;
    }
  };
  const window = {
    SetupWizard: {
      registerStepRenderer: (kind, renderer) => renderers.set(kind, renderer)
    },
    location: { href: '' },
    openCalls: [],
    open(...args) {
      this.openCalls.push(args);
    }
  };
  vm.runInNewContext(
    source,
    {
      window,
      document,
      fetch:
        fetchImpl ||
        (async () => ({ ok: true, json: async () => ({ workspace: { agent_instances: [] } }) })),
      console,
      Intl,
      Date,
      encodeURIComponent
    },
    { filename: 'reaper-readiness-panel.js' }
  );
  return { window, document, modelModal, renderer: renderers.get('runtime_readiness') };
}

function context(document, status, options = {}) {
  const calls = {
    busy: [],
    errors: [],
    announcements: [],
    requests: [],
    rechecks: 0,
    statuses: []
  };
  const ctx = {
    step: {
      id: 'live-control',
      kind: 'runtime_readiness',
      runtime_requirement_key: 'reaper_live_control'
    },
    status: { steps: [] },
    runtimeStatus: status,
    workspaceId: 'ws-1',
    renderDefault: () => {
      calls.defaulted = true;
    },
    setBusy: (value, message) => calls.busy.push({ value, message }),
    setError: message => calls.errors.push(message),
    announce: message => calls.announcements.push(message),
    recheck: async () => {
      calls.rechecks += 1;
    },
    setRuntimeStatus: next => calls.statuses.push(next),
    refreshRuntime: async () => status,
    runtimeRequest: async (path, requestOptions) => {
      calls.requests.push({ path, requestOptions });
      return options.nextStatus || status;
    }
  };
  return { ctx, calls, host: document.createElement('div') };
}

test('registers only one runtime-readiness renderer and claims REAPER requirement steps', () => {
  const { renderer } = load();
  assert.equal(typeof renderer?.render, 'function');
  assert.equal(
    renderer.owns({ kind: 'runtime_readiness', runtime_requirement_key: 'reaper_live_control' }),
    true
  );
  assert.equal(
    renderer.owns({ kind: 'runtime_readiness', runtime_requirement_key: 'another_runtime' }),
    false
  );
});

test('File-only is a complete limited mode with no REAPER repair controls', async () => {
  const { renderer, document } = load();
  const { ctx, host } = context(
    document,
    runtime({
      selected_mode_id: 'file_only',
      durable_state: 'configured',
      live_state: 'not_applicable',
      requirements: [],
      first_blocker: null
    })
  );
  await renderer.render(host, ctx);
  assert.match(host.textContent, /File-only/);
  assert.match(host.textContent, /not configured or tested/);
  assert.equal(findButtons(host).length, 0);
});

test('assisted checklist derives complete/current/waiting states from the authoritative first blocker', async () => {
  const { renderer, document } = load();
  const { ctx, host } = context(document, runtime());
  await renderer.render(host, ctx);

  const checklist = host.querySelector('.reaper-runtime-checklist');
  assert.equal(checklist.children.length, 7);
  assert.match(checklist.children[0].textContent, /REAPER applicationComplete/);
  assert.match(checklist.children[1].textContent, /Web RemoteNeeds attention/);
  assert.match(checklist.children[2].textContent, /REAPER plugin and skillsWaiting/);
  const pluginLink = checklist.children[2].querySelector('.reaper-runtime-plugin-link');
  assert.equal(pluginLink.href, 'https://github.com/johnjallday/reaper-plugin');
  assert.equal(pluginLink.target, '_blank');
  assert.equal(pluginLink.rel, 'noopener noreferrer');
  assert.match(pluginLink.attributes['aria-label'], /opens plugin repository/);
  assert.match(checklist.children[3].textContent, /Ori REAPER runnerWaiting/);
  assert.deepEqual(
    findButtons(host).map(node => node.textContent),
    ['Enable Web Remote']
  );
  assert.doesNotMatch(host.textContent, /Attach REAPER plugin/);
});

test('an unreachable configured interface highlights Web Remote, not the installed application', async () => {
  const { renderer, document } = load();
  const offline = runtime({
    first_blocker: {
      requirement_key: 'reaper_live_control',
      reason_code: 'reaper_offline',
      summary: 'Ori could not reach any configured REAPER Web Remote interface.',
      action: {
        token: 'open_check_reaper',
        code: 'open_check_reaper',
        label: 'Open or check REAPER'
      }
    },
    requirements: [
      {
        key: 'reaper_live_control',
        durable_state: 'in_progress',
        live_state: 'offline',
        reason_code: 'reaper_offline',
        summary: 'Ori could not reach any configured REAPER Web Remote interface.'
      }
    ]
  });
  const { ctx, host } = context(document, offline);
  await renderer.render(host, ctx);

  const checklist = host.querySelector('.reaper-runtime-checklist');
  assert.match(checklist.children[0].className, /is-complete/);
  assert.match(checklist.children[1].className, /is-attention/);
  assert.doesNotMatch(checklist.children[0].className, /is-attention/);
});

test('Web Remote and runner blockers show instructions then an honest Check again action', async () => {
  const { renderer, document } = load();
  const { ctx, host, calls } = context(document, runtime());
  await renderer.render(host, ctx);
  await click(findButtons(host)[0]);
  assert.match(host.textContent, /Preferences, then Control\/OSC\/web/);
  const check = findButtons(host).find(node => node.textContent === 'Check again');
  await click(check);
  assert.equal(calls.requests[0].path, '/recheck');
  assert.equal(calls.rechecks, 1);
});

test('every compiled blocker projects one specific primary repair without redundant plugin controls', async () => {
  const { renderer, document } = load();
  const cases = [
    ['reaper_app_missing', 'download_reaper', 'Download REAPER'],
    ['web_remote_unconfigured', 'enable_web_remote', 'Enable Web Remote'],
    ['reaper_plugin_missing', 'install_reaper_plugin', 'Install REAPER plugin'],
    ['runner_missing', 'set_up_runner', 'Set up runner'],
    ['cli_agent_required', 'choose_reaper_agent', 'Choose compatible agent'],
    ['reaper_access_required', 'grant_reaper_access', 'Grant REAPER access'],
    ['verification_required', 'test_reaper_connection', 'Test REAPER connection'],
    ['wrong_project', 'open_correct_project', 'Open the workspace project']
  ];
  for (const [reason, code, label] of cases) {
    const current = runtime({
      first_blocker: {
        requirement_key: 'reaper_live_control',
        reason_code: reason,
        summary: `${label} is required.`,
        action: { token: code, code, label }
      },
      requirements: [
        {
          key: 'reaper_live_control',
          durable_state: 'in_progress',
          live_state: 'not_checked',
          reason_code: reason,
          summary: `${label} is required.`,
          disclosure: 'Narrow REAPER access disclosure.'
        }
      ]
    });
    const { ctx, host } = context(document, current);
    await renderer.render(host, ctx);
    const labels = findButtons(host).map(node => node.textContent);
    assert.equal(labels[0], label, `${reason} action labels`);
    if (reason !== 'reaper_plugin_missing') {
      assert.equal(
        labels.filter(value => /plugin/i.test(value)).length,
        0,
        `${reason} must not show a redundant plugin action`
      );
    }
  }
});

test('compatible-agent repair opens only CLI choices and returns to the runtime step', async () => {
  const { renderer, document, modelModal, window } = load({
    fetchImpl: async () => ({
      ok: true,
      json: async () => ({
        workspace: {
          entry_agent_name: 'Reaper Producer',
          agent_instances: [{ id: 'agent-1', name: 'Reaper Producer' }],
          tasks: []
        }
      })
    })
  });
  const calls = { close: 0, open: [], model: [] };
  window.SetupWizard.close = async () => {
    calls.close += 1;
  };
  window.SetupWizard.open = step => calls.open.push(step);
  window.workspaceDetail = {
    async openAgentModelModal(agentName, options) {
      calls.model.push({ agentName, options });
      return true;
    }
  };
  const needsAgent = runtime({
    first_blocker: {
      requirement_key: 'reaper_live_control',
      reason_code: 'cli_agent_required',
      summary: 'Choose a Codex or Claude Code workspace agent.',
      action: {
        token: 'choose_reaper_agent',
        code: 'choose_reaper_agent',
        label: 'Choose compatible agent'
      }
    },
    requirements: [
      {
        key: 'reaper_live_control',
        durable_state: 'in_progress',
        live_state: 'not_checked',
        reason_code: 'cli_agent_required',
        summary: 'Choose a Codex or Claude Code workspace agent.'
      }
    ]
  });
  const { ctx, host } = context(document, needsAgent);
  await renderer.render(host, ctx);
  await click(findButtons(host).find(node => node.textContent === 'Choose compatible agent'));

  assert.equal(calls.close, 1);
  assert.equal(calls.model[0].agentName, 'Reaper%20Producer');
  assert.deepEqual(Array.from(calls.model[0].options.allowedProviders), ['codex', 'claude_code']);
  assert.match(calls.model[0].options.help, /Codex or Claude Code CLI/);

  for (const handler of modelModal.listeners['hidden.bs.modal'] || []) await handler();
  assert.deepEqual(calls.open, ['live-control']);
});

test('missing plugin installs from the official repository link', async () => {
  const { renderer, document, window } = load();
  let installOptions;
  window.ReaperPluginInstall = {
    begin(options) {
      installOptions = options;
    }
  };
  const missingPlugin = runtime({
    first_blocker: {
      requirement_key: 'reaper_live_control',
      reason_code: 'reaper_plugin_missing',
      summary: 'Install the REAPER plugin and skills.',
      action: {
        token: 'install_reaper_plugin',
        code: 'install_reaper_plugin',
        label: 'Install REAPER plugin'
      }
    },
    requirements: [
      {
        key: 'reaper_live_control',
        durable_state: 'in_progress',
        live_state: 'not_checked',
        reason_code: 'reaper_plugin_missing',
        summary: 'Install the REAPER plugin and skills.'
      }
    ]
  });
  const { ctx, host } = context(document, missingPlugin);
  await renderer.render(host, ctx);
  await click(findButtons(host).find(node => node.textContent === 'Install REAPER plugin'));

  assert.equal(installOptions.declaredSource, 'https://github.com/johnjallday/reaper-plugin');
  assert.equal(installOptions.host.className, 'reaper-runtime-actions');
});

test('grant disclosure is adjacent to the explicit grant and sends only the selected agent id', async () => {
  const fetchCalls = [];
  const { renderer, document } = load({
    fetchImpl: async (url, options) => {
      fetchCalls.push({ url, options });
      return {
        ok: true,
        json: async () => ({
          workspace: {
            entry_agent_name: 'Reaper Producer',
            agent_instances: [{ id: 'agent-1', name: 'Reaper Producer' }],
            tasks: []
          }
        })
      };
    }
  });
  const grantStatus = runtime({
    first_blocker: {
      requirement_key: 'reaper_live_control',
      reason_code: 'reaper_access_required',
      summary: 'The selected agent does not have REAPER access.',
      action: {
        token: 'grant_reaper_access',
        code: 'grant_reaper_access',
        label: 'Grant REAPER access'
      }
    },
    requirements: [
      {
        key: 'reaper_live_control',
        durable_state: 'in_progress',
        live_state: 'not_checked',
        reason_code: 'reaper_access_required',
        summary: 'Grant access.',
        disclosure:
          'Loopback, dedicated runner directory, selected agent, no per-call confirmation.'
      }
    ]
  });
  const { ctx, host, calls } = context(document, grantStatus);
  await renderer.render(host, ctx);
  assert.match(host.textContent, /Before granting REAPER access/);
  assert.match(host.textContent, /no per-call confirmation/);
  await click(findButtons(host).find(node => node.textContent === 'Grant REAPER access'));
  assert.equal(fetchCalls[0].url, '/api/workspaces/ws-1');
  assert.equal(calls.requests[0].path, '/requirements/reaper_live_control/grants');
  assert.deepEqual(JSON.parse(calls.requests[0].requestOptions.body), {
    agent_instance_id: 'agent-1'
  });
});

test('verification uses the trusted verify endpoint and retains the returned live result', async () => {
  const { renderer, document } = load();
  const verified = runtime({
    durable_state: 'configured',
    live_state: 'available',
    first_blocker: null,
    first_verified_at: '2026-08-17T10:00:00Z',
    last_verified_at: '2026-08-17T10:00:00Z',
    requirements: [
      {
        key: 'reaper_live_control',
        durable_state: 'configured',
        live_state: 'available',
        summary: 'REAPER is connected to this workspace project now.'
      }
    ]
  });
  const needsTest = runtime({
    first_blocker: {
      requirement_key: 'reaper_live_control',
      reason_code: 'verification_required',
      summary: 'Run the harmless REAPER connection test.',
      action: {
        token: 'test_reaper_connection',
        code: 'test_reaper_connection',
        label: 'Test REAPER connection'
      }
    }
  });
  const { ctx, host, calls } = context(document, needsTest, { nextStatus: verified });
  await renderer.render(host, ctx);
  await click(findButtons(host).find(node => node.textContent === 'Test REAPER connection'));
  assert.equal(calls.requests[0].path, '/requirements/reaper_live_control/verify');
  assert.equal(calls.rechecks, 1);
  assert.equal(calls.statuses.at(-1).live_state, 'available');
});

test('configured offline and wrong-project results remain distinct from durable setup', async () => {
  const { renderer, document } = load();
  for (const [live, expected] of [
    ['offline', /REAPER offlineSetup remains configured/],
    ['wrong_target', /Wrong project/],
    ['available', /Connected now/]
  ]) {
    const current = runtime({
      durable_state: 'configured',
      live_state: live,
      first_blocker: null,
      requirements: [
        {
          key: 'reaper_live_control',
          durable_state: 'configured',
          live_state: live,
          summary: live === 'wrong_target' ? 'REAPER has a different project open.' : ''
        }
      ]
    });
    const { ctx, host } = context(document, current);
    await renderer.render(host, ctx);
    assert.match(host.textContent, expected);
    assert.match(host.textContent, /Revoke REAPER access/);
  }
});
