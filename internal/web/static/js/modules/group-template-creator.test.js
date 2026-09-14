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
  const window = {};
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
  return { api: window.GroupTemplateCreator, element, calls };
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
  const bulk = manager(api, 'selected-members');
  assert.equal(api.chooserMode(bulk), 'general-only');
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
  const container = element('workspaceGroupTemplateChoice');
  const list = element('workspaceGroupTemplateOptions');
  assert.equal(container.hidden, false);
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
