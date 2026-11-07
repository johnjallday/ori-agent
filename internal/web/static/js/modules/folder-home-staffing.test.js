import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

class Element {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.listeners = {};
    this.attributes = {};
    this.value = '';
  }
  set textContent(value) {
    this.text = String(value);
    this.children = [];
  }
  get textContent() {
    return (this.text || '') + this.children.map(child => child.textContent).join(' ');
  }
  set innerHTML(_value) {
    throw new Error('Untrusted text must not use HTML');
  }
  append(...children) {
    this.children.push(...children);
  }
  replaceChildren(...children) {
    this.text = '';
    this.children = children;
  }
  setAttribute(key, value) {
    this.attributes[key] = value;
  }
  addEventListener(type, fn) {
    this.listeners[type] = fn;
  }
  removeEventListener(type) {
    delete this.listeners[type];
  }
  querySelectorAll(tag) {
    return this.children.flatMap(child => [
      ...(child.tag === tag ? [child] : []),
      ...child.querySelectorAll(tag)
    ]);
  }
  focus() {
    this.focused = true;
  }
  click() {
    if (!this.disabled) this.listeners.click?.({ target: this });
  }
}
const plain = value => JSON.parse(JSON.stringify(value));
const view = (extra = {}) => ({
  context_digest: 'c'.repeat(64),
  revision: 0,
  state: 'needs_choice',
  roles: [{ role_id: 'manager', label: 'Manager', required: true }],
  choices: [],
  ...extra
});
function fixture(options = {}) {
  const renders = [];
  const window = {
    WorkspaceRoleRoster: {
      render: (_host, value, callbacks) => renders.push({ value, callbacks }),
      focusRole() {}
    }
  };
  const sandbox = {
    window,
    document: { createElement: tag => new Element(tag) },
    AbortController,
    fetch: async () => {
      throw new Error('Rendering must not fetch');
    },
    ...options
  };
  vm.createContext(sandbox);
  for (const file of ['create-workspace-team-draft.js', 'folder-home-staffing.js'])
    vm.runInContext(readFileSync(new URL(file, import.meta.url), 'utf8'), sandbox);
  const host = new Element('div');
  return { api: window.FolderHomeStaffing, host, renders, window };
}
const tick = () => new Promise(resolve => setImmediate(resolve));

const recommendedView = (state = 'recommended') =>
  view({
    revision: 1,
    advice: {
      version: 2,
      state,
      coverage: { complete: true },
      roles: [
        {
          role_id: 'manager',
          scope: 'home',
          state,
          choices: [
            {
              handle: 'saved-handle',
              name: 'Aster',
              source: 'root',
              configured: true,
              reasons: [{ kind: 'declared_purpose', value: 'Coordinates music projects.' }]
            }
          ]
        }
      ]
    }
  });

test('review open checks once and preselects a host-recommended required role without executing setup', async () => {
  const { api, host, renders } = fixture();
  const calls = [];
  const onAction = async input => {
    calls.push(plain(input));
    return input.action === 'recommend_staffing'
      ? recommendedView()
      : { ...recommendedView(), revision: 2, choices: [input.choice] };
  };
  const controller = api.mount(host, { owner: 'offer', view: view(), onAction });
  assert.equal(calls.length, 0, 'mount is observational');
  await controller.suggest();
  assert.deepEqual(
    calls.map(input => input.action),
    ['recommend_staffing', 'choose_staffing']
  );
  assert.deepEqual(calls[1].choice, {
    role_id: 'manager',
    mode: 'assign',
    name: 'Aster',
    recommendation: 'saved-handle'
  });
  assert.equal(renders.at(-1).value.roles[0].recommended, true);
  assert.ok(host.textContent.includes('Recommended saved agent preselected'));
  await controller.suggest();
  assert.equal(calls.length, 2);
});

test('automatic checks never select ties or renew a clear tombstone on reopen', async () => {
  const { api, host } = fixture();
  const calls = [];
  const onAction = async input => {
    calls.push(input);
    return recommendedView('needs_choice');
  };
  const controller = api.mount(host, { owner: 'offer', view: view(), onAction });
  await controller.suggest();
  assert.deepEqual(
    calls.map(input => input.action),
    ['recommend_staffing']
  );
  const next = fixture();
  await next.api
    .mount(next.host, { owner: 'offer', view: view({ revision: 1 }), onAction })
    .suggest();
  assert.equal(calls.length, 1, 'a durable empty revision is an explicit user edit');
});

test('manual clear or closing the review defeats late automatic preselection', async () => {
  for (const action of ['clear', 'close', 'manual']) {
    const { api, host, renders } = fixture();
    const calls = [];
    let finish;
    let open = true;
    const onAction = input => {
      calls.push(input);
      return input.action === 'recommend_staffing'
        ? new Promise(resolve => {
            finish = resolve;
          })
        : Promise.resolve(view({ revision: 1 }));
    };
    const controller = api.mount(host, { owner: 'offer', view: view(), onAction });
    const pending = controller.suggest(() => open);
    await tick();
    if (action === 'clear') await controller.act('clear_staffing');
    else if (action === 'manual') renders.at(-1).callbacks.onAssign('manager');
    else open = false;
    finish(recommendedView());
    await pending;
    assert.equal(
      calls.some(input => input.action === 'choose_staffing'),
      false
    );
    assert.equal(renders.at(-1).value.filled_count, 0);
  }
});

test('held and optional-only roles need no automatic check; unavailable candidates are never selected', async () => {
  for (const roles of [
    [{ role_id: 'manager', required: true, held_name: 'Existing' }],
    [{ role_id: 'manager', required: false }]
  ]) {
    const { api, host } = fixture();
    let calls = 0;
    await api
      .mount(host, {
        owner: 'offer',
        view: view({ roles }),
        onAction: async () => {
          calls++;
        }
      })
      .suggest();
    assert.equal(calls, 0);
  }
  const { api, host } = fixture();
  const calls = [];
  const unavailable = recommendedView();
  unavailable.advice.roles[0].choices[0].configured = false;
  await api
    .mount(host, {
      owner: 'offer',
      view: view(),
      onAction: async input => {
        calls.push(input.action);
        return unavailable;
      }
    })
    .suggest();
  assert.deepEqual(calls, ['recommend_staffing']);
});

test('Home role projection separates held roles, original choices and advice', () => {
  const { api } = fixture();
  const data = view({
    state: 'stale',
    roles: [
      { role_id: 'manager', required: true },
      { role_id: 'held', held_name: 'Existing' },
      { role_id: 'optional' }
    ],
    choices: [{ role_id: 'manager', mode: 'assign', name: '<script>Aster</script>' }]
  });
  const roles = plain(api.roster(data).roles);
  assert.equal(roles[0].agent.name, '<script>Aster</script>');
  assert.equal(roles[0].needs_clear, true);
  assert.equal(roles[1].read_only, true);
  assert.equal(roles[2].state, 'empty');
  assert.equal(data.choices.length, 1);
});

test('mount, update and read-only presentation never request ranking or select a profile', () => {
  const { api, host, renders } = fixture();
  const onAction = () => {
    throw new Error('No action was clicked');
  };
  const controller = api.mount(host, { owner: 'offer', view: view(), onAction });
  controller.update({ owner: 'offer', view: view(), onAction });
  assert.equal(renders.length, 1, 'unchanged reads retain focused role nodes');
  assert.equal(renders.at(-1).value.filled_count, 0);
  assert.match(host.textContent, /Nothing is staffed until you confirm setup/);
});

test('clear aborts and fences late recommendation without hiding manual controls', async () => {
  const { api, host, renders } = fixture();
  const calls = [];
  let resolveRank;
  let rankSignal;
  const onAction = (input, { signal }) => {
    calls.push(plain(input));
    if (input.action === 'recommend_staffing') {
      rankSignal = signal;
      return new Promise(resolve => {
        resolveRank = resolve;
      });
    }
    return Promise.resolve(view({ revision: 1 }));
  };
  const controller = api.mount(host, { owner: 'offer', view: view(), onAction });
  const ranking = controller.act('recommend_staffing');
  assert.equal(typeof renders.at(-1).callbacks.onAssign, 'function');
  await controller.act('clear_staffing');
  resolveRank(
    view({ revision: 1, choices: [{ role_id: 'manager', mode: 'assign', name: 'Late' }] })
  );
  await ranking;
  assert.equal(rankSignal.aborted, true);
  assert.equal(renders.at(-1).value.filled_count, 0);
  assert.deepEqual(
    calls.map(item => [item.action, item.staffing_revision]),
    [
      ['recommend_staffing', 0],
      ['clear_staffing', 0]
    ]
  );
});

test('older responses and another owner cannot replace the current choice', async () => {
  const { api, host, renders } = fixture();
  let finish;
  const onAction = () =>
    new Promise(resolve => {
      finish = resolve;
    });
  const controller = api.mount(host, { owner: 'old', view: view({ revision: 2 }), onAction });
  controller.update({
    owner: 'old',
    view: view({ revision: 1, choices: [{ role_id: 'manager', name: 'Stale', mode: 'assign' }] }),
    onAction
  });
  assert.equal(renders.at(-1).value.filled_count, 0);
  const pending = controller.act('recommend_staffing');
  controller.update({ owner: 'new', view: view(), onAction });
  finish(
    view({ revision: 3, choices: [{ role_id: 'manager', name: 'Other owner', mode: 'assign' }] })
  );
  await pending;
  assert.equal(renders.at(-1).value.filled_count, 0);
});

test('first explicit recommendation prepares the existing owner before asking for advice', async () => {
  const { api, host } = fixture();
  const calls = [];
  api.mount(host, {
    owner: 'build',
    view: view({ state: 'needs_review', context_digest: '' }),
    onAction: async input => {
      calls.push(plain(input));
      return view();
    }
  });
  assert.equal(calls.length, 0);
  host
    .querySelectorAll('button')
    .find(button => button.textContent === 'Recommend saved agents')
    .click();
  await tick();
  assert.deepEqual(
    calls.map(input => input.action),
    ['prepare_staffing', 'recommend_staffing']
  );
  assert.deepEqual(calls[0], { action: 'prepare_staffing' });
  assert.equal(calls[1].context_digest, 'c'.repeat(64));
});

test('manual creation mounts the shared form without prompts and submits only a public choice', async () => {
  const { api, host, renders, window } = fixture({
    fetch: async () => ({ ok: true, json: async () => ({ providers: [] }) })
  });
  let mounted;
  let destroyed = 0;
  const name = new Element('input');
  window.AgentCreateForm = {
    mount: (_host, options) => {
      mounted = options;
      return {
        get: () => name,
        focus() {},
        extract: () => ({
          valid: true,
          values: { name: 'Cedar', provider: 'fixture', model: 'model', reasoningEffort: '' }
        })
      };
    }
  };
  window.AgentAppearanceEditor = {
    create: () => ({
      setAgentName() {},
      appearance: () => ({ version: 1, mode: 'generated' }),
      destroy: () => destroyed++
    })
  };
  const calls = [];
  api.mount(host, {
    owner: 'offer',
    view: view(),
    onAction: async input => {
      calls.push(plain(input));
      return view({ revision: 1, choices: [input.choice] });
    }
  });
  renders.at(-1).callbacks.onRequestCreate('manager');
  await tick();
  assert.equal(mounted.profile, 'template');
  assert.deepEqual(plain(mounted.omitFields), ['systemPrompt']);
  host
    .querySelectorAll('button')
    .find(button => button.textContent === 'Use for this role')
    .click();
  await tick();
  assert.equal(calls.length, 1);
  assert.equal(calls[0].action, 'choose_staffing');
  assert.equal(calls[0].choice.mode, 'create');
  assert.equal(calls[0].choice.name, 'Cedar');
  assert.equal(Object.hasOwn(calls[0].choice, 'system_prompt'), false);
  assert.equal(destroyed, 1);
});
