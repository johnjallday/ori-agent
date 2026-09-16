import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const asData = value => JSON.parse(JSON.stringify(value));

function environment({ fetch } = {}) {
  const elements = new Map();
  const element = id => {
    if (!elements.has(id)) {
      elements.set(id, {
        id,
        value: '',
        hidden: false,
        readOnly: false,
        disabled: false,
        textContent: '',
        attributes: {},
        children: [],
        setAttribute(name, value) {
          this.attributes[name] = value;
        },
        replaceChildren(...children) {
          this.children = children;
        },
        append(...children) {
          this.children.push(...children);
        }
      });
    }
    return elements.get(id);
  };
  const document = {
    getElementById: id => element(id),
    createElement: tag => ({
      tag,
      dataset: {},
      children: [],
      className: '',
      textContent: '',
      classList: { toggle() {} },
      append(...children) {
        this.children.push(...children);
      }
    })
  };
  const stored = new Map();
  const window = {
    location: { href: '' },
    sessionStorage: {
      stored,
      getItem: key => (stored.has(key) ? stored.get(key) : null),
      setItem: (key, value) => stored.set(key, String(value)),
      removeItem: key => stored.delete(key)
    }
  };
  const calls = [];
  const context = {
    window,
    document,
    TextEncoder,
    crypto: {
      randomUUID: (() => {
        let n = 0;
        return () => `key-${++n}`;
      })()
    },
    bootstrap: { Modal: { getInstance: () => ({ hide() {} }) } },
    fetch: async (url, options = {}) => {
      calls.push({ url, options: asData(options) });
      return fetch
        ? fetch(url, options, calls.length)
        : { ok: true, status: 200, json: async () => ({}) };
    },
    console
  };
  const source = readFileSync(new URL('./group-template-creator.js', import.meta.url), 'utf8');
  vm.runInNewContext(source, context, { filename: 'group-template-creator.js' });
  return { api: window.GroupTemplateCreator, element, calls, window };
}

const managed = (overrides = {}) => ({
  id: 'group-template:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  kind: 'managed_home',
  revision: 'r1',
  name: 'Research Program Home',
  proposed_group_name: 'Research Program Home',
  provider: { kind: 'plugin', plugin_id: 'fixture', plugin_version: '1.0.0' },
  home_roles: [
    { role_id: 'coordinator', label: 'Portfolio Coordinator', required: true },
    { role_id: 'curator', label: 'Archive Curator', required: false }
  ],
  project_roles_note: ['Research Lead'],
  availability: { state: 'creatable', actions: ['review_create_group'] },
  home: { state: 'absent' },
  ...overrides
});

function manager(api, mode = 'ordinary') {
  const context = { kind: 'group', mode, generation: 1 };
  const state = api.stateFor(context);
  state.status = 'ready';
  state.items = [
    { id: 'general', kind: 'ordinary_group', name: 'General' },
    managed(),
    managed({
      id: 'group-template:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
      name: 'Studio Home',
      availability: { state: 'reusable', actions: ['open_group'] },
      home: { state: 'exists', workspace_id: 'home-1', name: 'My Studio' }
    }),
    managed({
      id: 'group-template:cccccccccccccccccccccccccccccccc',
      name: 'Disabled Home',
      availability: { state: 'unavailable', reason: 'plugin_enable_required' }
    })
  ];
  return {
    workspaceCreatorContext: context,
    importModeEnabled: false,
    wizardStep: 2,
    refreshed: 0,
    refreshWizardChrome() {
      this.refreshed += 1;
    },
    refreshWorkspaceReview() {},
    clearWorkspaceNameError() {},
    resetTemplateAgentReview() {},
    refreshWorkspaceSurfacesAfterOrdinaryGroupCreate: async () => {},
    resetAddWorkspaceModalForm() {},
    showWorkspaceCreateError(message) {
      this.createError = message;
    },
    folders: []
  };
}

test('review and commit requests carry only the server-accepted selection fields', () => {
  const { api } = environment();
  const entry = managed();
  assert.deepEqual(asData(api.reviewRequest(entry, '  Lab  ')), {
    group_template_id: entry.id,
    revision: 'r1',
    name: 'Lab'
  });
  assert.deepEqual(Object.keys(api.commitRequest(entry, 'Lab', 'token', 'idem')).sort(), [
    'group_review_token',
    'group_template_id',
    'idempotency_key',
    'name',
    'revision'
  ]);
});

test('group names follow the declaration display-name rule', () => {
  const { api } = environment();
  assert.equal(api.nameProblem('Lab Portfolio'), '');
  assert.match(api.nameProblem(' '), /required/);
  assert.match(api.nameProblem('lab/portfolio'), /path/);
  assert.match(api.nameProblem('see https://x'), /protocol/);
  assert.match(api.nameProblem('line\nbreak'), /control/);
  assert.match(api.nameProblem('é'.repeat(61)), /120 bytes/);
});

test('templates are offered only to ordinary Group creators; bulk grouping is General-only', () => {
  const { api } = environment();
  const ordinary = manager(api);
  assert.equal(api.chooserMode(ordinary), 'full');
  assert.equal(api.chooserMode({ ...ordinary, importModeEnabled: true }), 'hidden');
  assert.equal(api.chooserMode(manager(api, 'guided')), 'hidden');
  const guided = manager(api, 'guided');
  guided.workspaceCreatorContext.guided = { groupTemplateId: managed().id };
  assert.equal(api.chooserMode(guided), 'fixed');
  assert.equal(api.managedActive(guided), false, 'guided setup keeps its own review owner');
  assert.equal(api.hasBlueprintStep(ordinary), true);
  const bulk = manager(api, 'selected-members');
  assert.equal(api.chooserMode(bulk), 'general-only');
  assert.equal(api.hasBlueprintStep(bulk), false);
  assert.equal(api.select(bulk, managed().id), false);
  assert.equal(api.managedActive(bulk), false);
  assert.equal(
    api.chooserMode({
      ...ordinary,
      workspaceCreatorContext: { ...ordinary.workspaceCreatorContext, kind: 'workspace' }
    }),
    'hidden'
  );
});

const textOf = node =>
  [node.textContent, ...(node.children || []).map(textOf)].filter(Boolean).join(' | ');
const tagsOf = node => [node.tag, ...(node.children || []).flatMap(tagsOf)].filter(Boolean);

test('guided setup describes its one template read-only with the chooser wording', () => {
  const { api, element } = environment();
  const guided = manager(api, 'guided');
  const context = guided.workspaceCreatorContext;
  context.guided = { groupTemplateId: managed().id, state: { review: null } };

  api.render(guided);
  // Guided setup describes its blueprint on Details; the Blueprint-step chooser
  // stays hidden.
  const container = element('workspaceGroupTemplateFixed');
  const list = element('workspaceGroupTemplateFixedOptions');
  assert.equal(container.hidden, false);
  assert.equal(element('workspaceGroupBlueprintStep').hidden, true);
  assert.equal(api.hasBlueprintStep(guided), false);
  assert.equal(list.children.length, 1);
  assert.equal(tagsOf(list.children[0]).includes('input'), false, 'no selectable control');
  const text = textOf(list.children[0]);
  assert.match(text, /Research Program Home/);
  assert.match(text, /Plugin: fixture 1\.0\.0/);
  assert.match(text, /Creates one group only/);
  assert.match(text, /Set up after: Portfolio Coordinator \(required\)/);
  assert.match(text, /Optional: Archive Curator/);
  assert.match(text, /Stays project-local: Research Lead/);
  assert.equal(api.fixedEntry(guided).id, managed().id);
  assert.equal(
    api.templateMeta(api.fixedEntry(guided)),
    'Template: Research Program Home · Plugin: fixture 1.0.0'
  );

  // After setup reports the group exists, the same card says it is reused.
  context.guided.state.review = { existing: true, input: { name: 'My Lab' } };
  api.render(guided);
  assert.match(textOf(list.children[0]), /Reuses the existing group “My Lab” unchanged/);

  // A source the catalog does not list is not described or guessed.
  context.guided.groupTemplateId = 'group-template:dddddddddddddddddddddddddddddddd';
  api.render(guided);
  assert.equal(list.children.length, 0);
  assert.equal(container.hidden, true);
  assert.equal(api.fixedEntry(guided), null);
  assert.equal(api.rolesCardHTML(guided, null), '');
});

test('role receipts escape source text even without a caller escaper', () => {
  const { api } = environment();
  const html = api.rolesCardHTML(
    {},
    managed({
      home_roles: [{ role_id: 'x', label: '<img src=x onerror="alert(1)">', required: true }],
      project_roles_note: ["Lead's <b>"]
    })
  );
  assert.doesNotMatch(html, /<img|<b>/);
  assert.match(html, /&lt;img src=x onerror=&quot;alert\(1\)&quot;&gt;/);
  assert.match(html, /Lead&#39;s &lt;b&gt;/);
});

test('catalog lookups for other surfaces resolve only managed entries by exact ID', async () => {
  const { api } = environment({
    fetch: async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        group_templates: [{ id: 'general', kind: 'ordinary_group', name: 'General' }, managed()]
      })
    })
  });
  assert.equal((await api.catalogEntry(managed().id)).name, 'Research Program Home');
  assert.equal(await api.catalogEntry('general'), null);
  assert.equal(await api.catalogEntry(''), null);
  assert.deepEqual(asData(api.roleSummary(managed())), [
    'Set up after: Portfolio Coordinator (required)',
    'Optional: Archive Curator',
    'Stays project-local: Research Lead'
  ]);
});

test('choosing a blueprint stays on the Blueprint step and renders the step-1 chooser', () => {
  const { api, element } = environment();
  const creator = manager(api);
  creator.wizardStep = 1;
  creator.creatorWizardSteps = () => (api.managedActive(creator) ? [1, 2, 4] : [1, 2, 3, 4]);
  api.render(creator);
  assert.equal(element('workspaceGroupBlueprintStep').hidden, false);
  assert.equal(element('workspaceGroupTemplateChoice').hidden, false);
  assert.equal(element('workspaceGroupTemplateOptions').children.length, 4);
  assert.equal(api.select(creator, managed().id), true);
  assert.equal(creator.wizardStep, 1, 'arrow-key browsing never jumps to Details');

  // A choice made from a step the new blueprint no longer has lands on a valid one.
  creator.wizardStep = 3;
  assert.equal(api.select(creator, 'group-template:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'), true);
  assert.equal(creator.wizardStep, 1);
});

test('selection keeps per-template names, fixes a reused name, and refuses unusable templates', () => {
  const { api, element } = environment();
  const creator = manager(api);
  const input = element('folderNameInput');
  input.value = 'My general group';

  assert.equal(api.select(creator, managed().id), true);
  assert.equal(api.managedActive(creator), true);
  assert.equal(input.value, 'Research Program Home');
  input.value = 'Lab Portfolio';

  assert.equal(api.select(creator, 'group-template:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'), true);
  assert.equal(input.value, 'My Studio');

  assert.equal(api.select(creator, 'general'), true);
  assert.equal(input.value, 'My general group');
  assert.equal(api.managedActive(creator), false);

  assert.equal(api.select(creator, managed().id), true);
  assert.equal(input.value, 'Lab Portfolio');

  assert.equal(api.select(creator, 'group-template:cccccccccccccccccccccccccccccccc'), false);
  assert.equal(api.selectedManaged(creator.workspaceCreatorContext).id, managed().id);
});

test('a stale catalog response cannot overwrite a newer creator generation', async () => {
  let release;
  const { api } = environment({
    fetch: () =>
      new Promise(resolve => {
        release = () =>
          resolve({ ok: true, status: 200, json: async () => ({ group_templates: [managed()] }) });
      })
  });
  const creator = manager(api);
  const older = creator.workspaceCreatorContext;
  older.groupTemplates = undefined;
  const loading = api.load(creator);
  creator.workspaceCreatorContext = { kind: 'group', mode: 'ordinary', generation: 2 };
  release();
  await loading;
  assert.equal(older.groupTemplates.status, 'loading');
  assert.equal(creator.workspaceCreatorContext.groupTemplates, undefined);
});

test('submit is enabled only for a ready review of the current name', async () => {
  const { api, element } = environment({
    fetch: async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        group_template_review: {
          review_token: 'tok',
          reuse: false,
          home_name: 'Lab',
          summary: 'Only this group.'
        }
      })
    })
  });
  const creator = manager(api);
  api.select(creator, managed().id);
  const input = element('folderNameInput');
  input.value = 'Lab';
  assert.equal(api.canSubmit(creator), false);
  await api.ensureReview(creator);
  assert.equal(api.canSubmit(creator), true);
  assert.equal(api.ctaLabel(creator), 'Create group “Lab” only');
  input.value = 'Renamed after review';
  assert.equal(api.canSubmit(creator), false);
});

test('a lost commit response retries the identical confirmed request; a refusal requires a fresh review', async () => {
  let mode = 'review';
  const { api, element, calls } = environment({
    fetch: async (url, options, count) => {
      if (url.endsWith('/review')) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            group_template_review: { review_token: `tok-${count}`, reuse: false, home_name: 'Lab' }
          })
        };
      }
      if (mode === 'network') throw new Error('offline');
      if (mode === 'refused') {
        return {
          ok: false,
          status: 409,
          json: async () => ({ group_template: { summary: 'This group template changed.' } })
        };
      }
      return {
        ok: true,
        status: 200,
        json: async () => ({
          group_template: {
            home_workspace_id: 'home-9',
            home_name: 'Lab',
            created_by_this_operation: true
          }
        })
      };
    }
  });
  const creator = manager(api);
  api.select(creator, managed().id);
  element('folderNameInput').value = 'Lab';
  await api.ensureReview(creator);

  mode = 'network';
  assert.equal(await api.submit(creator), false);
  const state = creator.workspaceCreatorContext.groupTemplates;
  assert.equal(state.pending.uncertain, true);
  assert.equal(api.ctaLabel(creator), 'Retry confirmed change');
  assert.equal(
    api.select(creator, 'general'),
    false,
    'a pending confirmed change fixes the selection'
  );

  mode = 'ok';
  assert.equal(await api.submit(creator), true);
  const commits = calls.filter(call => call.url.endsWith('/commit'));
  assert.equal(commits.length, 2);
  assert.equal(commits[0].options.body, commits[1].options.body);
  assert.equal(state.pending, null);

  const refusedCreator = manager(api);
  api.select(refusedCreator, managed().id);
  element('folderNameInput').value = 'Lab';
  mode = 'review';
  await api.ensureReview(refusedCreator);
  mode = 'refused';
  assert.equal(await api.submit(refusedCreator), false);
  const refused = refusedCreator.workspaceCreatorContext.groupTemplates;
  assert.equal(refused.pending, null);
  assert.equal(refused.review.status, 'error');
  assert.match(refused.review.error, /Review it again/);
});

test('launcher adoption runs only for a Home this operation created, never on reuse', async () => {
  let created = true;
  const { api, element } = environment({
    fetch: async url =>
      url.endsWith('/review')
        ? {
            ok: true,
            status: 200,
            json: async () => ({
              group_template_review: { review_token: 'tok', reuse: !created, home_name: 'Lab' }
            })
          }
        : {
            ok: true,
            status: 200,
            json: async () => ({
              group_template: {
                home_workspace_id: 'home-1',
                home_name: 'Lab',
                created_by_this_operation: created
              }
            })
          }
  });
  for (const expectCall of [true, false]) {
    created = expectCall;
    const creator = manager(api);
    const adopted = [];
    creator.workspaceCreatorContext.onCreated = async outcome => adopted.push(outcome.groupId);
    api.select(creator, managed().id);
    element('folderNameInput').value = 'Lab';
    await api.ensureReview(creator);
    assert.equal(await api.submit(creator), true);
    assert.deepEqual(adopted, expectCall ? ['home-1'] : []);
  }
});

// ---- Home role staffing ------------------------------------------------------

const staffedTemplate = (overrides = {}) =>
  managed({
    home_roles: [
      {
        role_id: 'coordinator',
        label: 'Portfolio Coordinator',
        required: true,
        primary: true,
        default_name: 'Portfolio Manager'
      },
      {
        role_id: 'curator',
        label: 'Archive Curator',
        required: false,
        default_name: 'Archive Curator'
      }
    ],
    ...overrides
  });

function staffingCreator(api, entry = staffedTemplate()) {
  const creator = manager(api);
  const state = creator.workspaceCreatorContext.groupTemplates;
  state.items = state.items.map(item => (item.id === entry.id ? entry : item));
  if (!state.items.some(item => item.id === entry.id)) state.items.push(entry);
  api.select(creator, entry.id);
  creator.wizardStep = 3;
  return creator;
}

test('a managed template adds a Team step; every other group creator keeps its own steps', () => {
  const { api } = environment();
  const creator = manager(api);
  assert.equal(api.wizardSteps(creator), null, 'General keeps its Group Manager roster steps');
  api.select(creator, managed().id);
  assert.deepEqual(asData(api.wizardSteps(creator)), [1, 2, 3, 4]);
  api.select(creator, 'group-template:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb');
  assert.deepEqual(asData(api.wizardSteps(creator)), [1, 2, 3, 4], 'reuse stages fills too');

  const guided = manager(api, 'guided');
  guided.workspaceCreatorContext.guided = { groupTemplateId: managed().id };
  assert.equal(api.wizardSteps(guided), null, 'guided setup keeps its own steps');
  assert.equal(api.wizardSteps(manager(api, 'selected-members')), null);
});

test('required Home roles seed as Create under their default name; optional roles stay empty', () => {
  const { api } = environment();
  const creator = staffingCreator(api);
  assert.equal(api.seedFills(creator), true);
  assert.deepEqual(asData(api.stagedFill(creator, 'coordinator')), {
    mode: 'create',
    name: 'Portfolio Manager',
    provider: '',
    model: ''
  });
  assert.equal(api.stagedFill(creator, 'curator'), null);

  // Seeding happens once: a cleared role stays cleared.
  assert.equal(api.clearFill(creator, 'coordinator'), true);
  assert.equal(api.seedFills(creator), false);
  assert.equal(api.stagedFill(creator, 'coordinator'), null);

  // Choosing another template forgets the plan; coming back seeds afresh.
  api.select(creator, 'general');
  api.select(creator, managed().id);
  assert.equal(api.seedFills(creator), true);
  assert.equal(api.stagedFill(creator, 'coordinator').name, 'Portfolio Manager');

  // The roster the Team step draws: program Home roles carry no proposal.
  const roster = api.teamRoster(creator);
  assert.equal(roster.filled_count, 1);
  assert.equal(roster.roles[0].state, 'filled');
  assert.equal(roster.roles[0].agent.name, 'Portfolio Manager');
  assert.match(roster.roles[0].description, /^Required/);
  assert.equal(roster.roles[1].description, '', 'an empty row says Optional in its own tag');
  assert.equal(
    roster.roles.some(role => 'proposed' in role),
    false
  );
});

test('the review receipt and button name every Home role outcome', () => {
  const { api, element } = environment();
  const creator = staffingCreator(api);
  element('folderNameInput').value = 'Lab';
  api.seedFills(creator);
  const entry = api.selectedManaged(creator.workspaceCreatorContext);

  let html = api.rolesCardHTML(creator, entry);
  assert.match(html, /Portfolio Coordinator · Create “Portfolio Manager”/);
  assert.match(html, /Archive Curator · Not staffed/);
  assert.equal(api.ctaLabel(creator), 'Create group and staff 1 role');

  assert.equal(api.setFill(creator, 'curator', { mode: 'assign', name: 'Librarian' }), true);
  html = api.rolesCardHTML(creator, entry);
  assert.match(html, /Archive Curator · Assign “Librarian”/);
  assert.equal(api.ctaLabel(creator), 'Create group and staff 2 roles');

  api.clearFill(creator, 'coordinator');
  api.clearFill(creator, 'curator');
  assert.match(api.rolesCardHTML(creator, entry), /Portfolio Coordinator · Not staffed/);
  assert.equal(api.ctaLabel(creator), 'Create group “Lab” only');
});

function staffingFetch({ commit = 'ok', roles = {} } = {}) {
  return async url => {
    if (url.endsWith('/review')) {
      return {
        ok: true,
        status: 200,
        json: async () => ({
          group_template_review: { review_token: 'tok', reuse: false, home_name: 'Lab' }
        })
      };
    }
    if (url.endsWith('/commit')) {
      if (commit === 'network') throw new Error('offline');
      if (commit === 'refused') {
        return { ok: false, status: 409, json: async () => ({ error: 'changed' }) };
      }
      return {
        ok: true,
        status: 200,
        json: async () => ({
          group_template: {
            home_workspace_id: 'home-9',
            home_name: 'Lab',
            created_by_this_operation: true
          }
        })
      };
    }
    const roleId = decodeURIComponent(url.split('/').pop());
    const answer = roles[roleId] || { status: 200, body: { roles: { roles: [] } } };
    if (answer.throws) throw new Error(answer.throws);
    return {
      ok: answer.status >= 200 && answer.status < 300,
      status: answer.status,
      json: async () => answer.body || {}
    };
  };
}

async function confirmStaffing(
  api,
  element,
  creator,
  folders = [{ id: 'home-9', folder_slug: 'lab' }]
) {
  creator.folders = folders;
  element('folderNameInput').value = 'Lab';
  api.seedFills(creator);
  await api.ensureReview(creator);
  return api.submit(creator);
}

test('fills are sent one role at a time, only after the commit, and never carry a prompt', async () => {
  const env = environment({ fetch: staffingFetch() });
  const creator = staffingCreator(env.api);
  env.api.setFill(creator, 'curator', { mode: 'assign', name: 'Librarian', model: 'ignored' });
  assert.equal(await confirmStaffing(env.api, env.element, creator), true);

  const order = env.calls.map(call => `${call.options.method || 'GET'} ${call.url}`);
  assert.deepEqual(order, [
    'POST /api/workspaces/group-templates/review',
    'POST /api/workspaces/group-templates/commit',
    'PUT /api/workspaces/home-9/roles/coordinator',
    'PUT /api/workspaces/home-9/roles/curator'
  ]);
  const commit = JSON.parse(env.calls[1].options.body);
  assert.deepEqual(Object.keys(commit).sort(), [
    'group_review_token',
    'group_template_id',
    'idempotency_key',
    'name',
    'revision'
  ]);
  assert.deepEqual(JSON.parse(env.calls[2].options.body), {
    mode: 'create',
    name: 'Portfolio Manager',
    provider: '',
    model: ''
  });
  assert.deepEqual(JSON.parse(env.calls[3].options.body), {
    mode: 'assign',
    name: 'Librarian',
    provider: '',
    model: ''
  });
  for (const call of env.calls.slice(2)) {
    assert.doesNotMatch(call.options.body, /system_prompt/);
  }
  assert.equal(env.window.location.href, '/workspaces/lab');
  const landing = JSON.parse(env.window.sessionStorage.getItem('ori:group-template-landing'));
  assert.equal(landing.workspace_id, 'home-9');
  assert.equal(landing.tone, 'success');
  assert.equal(landing.message, 'Lab is ready. Portfolio Coordinator and Archive Curator staffed.');
  assert.equal(landing.role_id, '');
});

test('no role is filled when the commit fails or its response is lost', async () => {
  for (const commit of ['refused', 'network']) {
    const env = environment({ fetch: staffingFetch({ commit }) });
    const creator = staffingCreator(env.api);
    assert.equal(await confirmStaffing(env.api, env.element, creator), false);
    assert.equal(
      env.calls.some(call => call.options.method === 'PUT'),
      false,
      `${commit}: no PUT`
    );
    assert.equal(env.window.location.href, '');
  }
});

test('an already-filled 409 counts as staffed; a failed fill still lands on the group at that role', async () => {
  const retried = environment({
    fetch: staffingFetch({
      roles: {
        coordinator: {
          status: 409,
          body: {
            code: 'CONFLICT',
            message: '"Portfolio Manager" already fills this role. Clear it first.'
          }
        }
      }
    })
  });
  const again = staffingCreator(retried.api);
  assert.equal(await confirmStaffing(retried.api, retried.element, again), true);
  assert.equal(retried.window.location.href, '/workspaces/lab');

  const failing = environment({
    fetch: staffingFetch({
      roles: {
        coordinator: {
          status: 409,
          body: { message: 'That role could not be filled; reload and try again.' }
        }
      }
    })
  });
  const creator = staffingCreator(failing.api);
  failing.api.setFill(creator, 'curator', { mode: 'create', name: 'Archivist' });
  assert.equal(await confirmStaffing(failing.api, failing.element, creator), true);
  assert.equal(
    failing.calls.filter(call => call.options.method === 'PUT').length,
    2,
    'a failed role never stops the remaining fills'
  );
  assert.equal(failing.window.location.href, '/workspaces/lab?role=coordinator');
});

test('landing words name the staffed, failed, and unstaffed roles', () => {
  const { api } = environment();
  const entry = staffedTemplate();
  const staffing = [
    { roleId: 'coordinator', label: 'Portfolio Coordinator', fill: { mode: 'create', name: 'PM' } }
  ];
  const created = { home_name: 'Lab', created_by_this_operation: true };

  const ready = api.landingNotice(created, entry, staffing, [
    { roleId: 'coordinator', label: 'Portfolio Coordinator', ok: true }
  ]);
  assert.equal(ready.tone, 'success');
  assert.equal(ready.message, 'Lab is ready. Portfolio Coordinator staffed.');
  assert.equal(ready.roleId, '');

  const reused = api.landingNotice({ home_name: 'Lab' }, entry, staffing, [
    { roleId: 'coordinator', label: 'Portfolio Coordinator', ok: true }
  ]);
  assert.equal(reused.message, 'Lab reused. Portfolio Coordinator staffed.');

  const failed = api.landingNotice(created, entry, staffing, [
    { roleId: 'coordinator', label: 'Portfolio Coordinator', ok: false, error: 'Nope.' }
  ]);
  assert.equal(failed.tone, 'warning');
  assert.equal(failed.message, 'Lab is ready. Portfolio Coordinator was not staffed: Nope.');
  assert.equal(failed.roleId, 'coordinator');
  assert.equal(failed.error, 'Nope.');

  const cleared = api.landingNotice(created, entry, [], []);
  assert.equal(cleared.tone, 'success');
  assert.equal(cleared.message, 'Lab is ready. Portfolio Coordinator is not staffed yet.');
  assert.equal(cleared.roleId, 'coordinator');
  assert.equal(api.landingURL('my lab', 'coordinator'), '/workspaces/my%20lab?role=coordinator');
});

test('seeding waits for saved agents and assigns a saved agent that already has the default name', () => {
  const { api } = environment();
  let saved = 'loading';
  const agents = { 'portfolio manager': { name: 'Portfolio Manager', source: 'user' } };
  const creator = staffingCreator(api);
  creator.groupTemplateSavedAgentsState = () => saved;
  creator.findAttachableSavedAgent = name => agents[String(name).toLowerCase()] || null;

  assert.equal(api.seedFills(creator), false, 'no proposal before saved agents are known');
  assert.equal(api.stagedFill(creator, 'coordinator'), null);
  saved = 'ready';
  assert.equal(api.seedFills(creator), true);
  assert.deepEqual(asData(api.stagedFill(creator, 'coordinator')), {
    mode: 'assign',
    name: 'Portfolio Manager',
    provider: '',
    model: ''
  });

  // No saved agent with that name (or the saved list failed): Create.
  const fresh = staffingCreator(api);
  fresh.groupTemplateSavedAgentsState = () => 'error';
  fresh.findAttachableSavedAgent = () => null;
  assert.equal(api.seedFills(fresh), true);
  assert.equal(api.stagedFill(fresh, 'coordinator').mode, 'create');
});

test('one agent name fills one role; the prompt note names the template source', () => {
  const { api } = environment();
  const creator = staffingCreator(api);
  api.seedFills(creator);
  assert.match(
    api.fillNameProblem(creator, 'curator', 'portfolio manager'),
    /Portfolio Coordinator is already staffed by “Portfolio Manager”/
  );
  assert.equal(
    api.setFill(creator, 'curator', { mode: 'create', name: 'Portfolio Manager' }),
    false
  );
  assert.equal(api.fillNameProblem(creator, 'coordinator', 'Portfolio Manager'), '', 'itself');
  assert.equal(
    api.setFill(creator, 'coordinator', { mode: 'create', name: 'Studio Manager' }),
    true
  );
  assert.equal(api.stagedFill(creator, 'coordinator').name, 'Studio Manager');

  const entry = api.selectedManaged(creator.workspaceCreatorContext);
  assert.equal(
    api.promptNote(entry),
    'Instructions for this role come from Plugin: fixture 1.0.0 and are applied by Ori.'
  );
  assert.equal(
    api.promptNote({ provider: { kind: 'user_template' } }),
    'Instructions for this role come from Your template and are applied by Ori.'
  );
});

test('an empty required role is a Review warning, never a blocker', async () => {
  const { api, element } = environment({ fetch: staffingFetch() });
  const creator = staffingCreator(api);
  element('folderNameInput').value = 'Lab';
  api.seedFills(creator);
  const entry = api.selectedManaged(creator.workspaceCreatorContext);
  assert.doesNotMatch(api.rolesCardHTML(creator, entry), /data-group-template-role-warning/);

  api.clearFill(creator, 'coordinator');
  const html = api.rolesCardHTML(creator, entry);
  assert.match(
    html,
    /This group will have no Portfolio Coordinator until you set one up\./,
    'the required role is named'
  );
  assert.doesNotMatch(html, /no Archive Curator/, 'optional roles never warn');
  await api.ensureReview(creator);
  assert.equal(api.canSubmit(creator), true, 'Create stays enabled');
});
