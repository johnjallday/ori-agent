import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./templates-page.js', import.meta.url), 'utf8');

function harness(template) {
  const nodes = new Map();
  const requests = [];
  const notices = [];
  let fail = false;
  let refreshes = 0;
  let modal;
  const node = id => {
    if (!nodes.has(id))
      nodes.set(id, {
        value: '',
        textContent: '',
        innerHTML: '',
        style: {},
        hidden: false,
        classList: { add() {}, remove() {} },
        querySelectorAll: () => [],
        appendChild() {}
      });
    return nodes.get(id);
  };
  const sandbox = {
    console,
    document: {
      readyState: 'loading',
      addEventListener() {},
      getElementById: node,
      querySelectorAll: () => []
    },
    window: { notifyToast: (message, kind) => notices.push({ message, kind }) },
    fetch: async (url, options) => {
      requests.push({ url, ...options, body: JSON.parse(options.body) });
      return {
        ok: !fail,
        status: fail ? 500 : 200,
        json: async () => (fail ? { error: 'Disk unavailable' } : { template: { id: 'my-copy' } })
      };
    }
  };
  vm.createContext(sandbox);
  vm.runInContext(
    source +
      `\nglobalThis.subject = {
    tplState, tplResetOverviewFields, tplSaveOverview, tplApplyReadOnly, tplDuplicate,
    install(refresh, open) { tplRefresh = refresh; tplOpenNameModal = open; }
  };`,
    sandbox
  );
  const subject = sandbox.subject;
  subject.tplState.templates = [template];
  subject.tplState.selectedId = template.id;
  subject.install(
    async () => {
      refreshes++;
    },
    options => {
      modal = options;
    }
  );
  subject.tplResetOverviewFields();
  return {
    subject,
    node,
    requests,
    notices,
    fail: () => {
      fail = true;
    },
    refreshes: () => refreshes,
    modal: () => modal
  };
}

test('ordinary goal load, edit, reset, clear and save use the existing Overview request', async () => {
  const h = harness({ id: 'mine', name: 'Mine', goal: 'Écrire 日本語 <b>literally</b>' });
  const input = h.node('tplEditGoal');
  assert.equal(input.value, 'Écrire 日本語 <b>literally</b>');
  input.value = 'Unsaved';
  h.subject.tplResetOverviewFields();
  assert.equal(input.value, 'Écrire 日本語 <b>literally</b>');
  input.value = '  Mon résultat <img src=x>  ';
  await h.subject.tplSaveOverview();
  assert.equal(h.requests[0].body.goal, 'Mon résultat <img src=x>');
  assert.equal(h.requests[0].method, 'PUT');
  input.value = '   ';
  await h.subject.tplSaveOverview();
  assert.equal(h.requests[1].body.goal, '');
  assert.equal(h.refreshes(), 2);
});

test('failed goal save retains the typed goal for repair rather than resetting it', async () => {
  const h = harness({ id: 'mine', goal: 'Old' });
  h.fail();
  h.node('tplEditGoal').value = 'Keep my edit';
  await h.subject.tplSaveOverview();
  assert.equal(h.node('tplEditGoal').value, 'Keep my edit');
  assert.equal(h.refreshes(), 0);
  assert.equal(h.notices.at(-1).kind, 'error');
});

test('built-ins display a selectable read-only goal and expose ordinary duplicate, never save', async () => {
  const h = harness({
    id: 'code-project',
    name: 'Code Project',
    builtin: true,
    goal: 'Read only after consent.'
  });
  h.subject.tplApplyReadOnly();
  assert.equal(h.node('tplEditGoal').value, 'Read only after consent.');
  assert.equal(h.node('tplEditGoal').readOnly, true);
  assert.equal(h.node('tplDuplicateBtn').disabled, false);
  assert.equal(h.node('tplDuplicateBtn').textContent, 'Duplicate to customize');
  await h.subject.tplSaveOverview();
  assert.equal(h.requests.length, 0);
  h.subject.tplDuplicate();
  await h.modal().run('My code');
  assert.equal(h.requests[0].url, '/api/project-templates/code-project/duplicate');
  assert.deepEqual(h.requests[0].body, { name: 'My code' });
});

test('plugin sources and variants never send a goal override even if a control is tampered with', async () => {
  for (const template of [
    { id: 'plugin:owner:source', plugin_owner: { plugin_id: 'owner' }, goal: 'Source outcome' },
    {
      id: 'variant',
      name: 'Variant',
      template_variant: { source: {} },
      variant_source_state: 'ready',
      variant_revision: 'a'.repeat(64),
      goal: 'Inherited outcome'
    }
  ]) {
    const h = harness(template);
    h.subject.tplApplyReadOnly();
    assert.equal(h.node('tplEditGoal').readOnly, true);
    assert.equal(h.node('tplEditGoal').value, template.goal);
    h.node('tplEditGoal').value = 'Forbidden';
    await h.subject.tplSaveOverview();
    assert.equal(h.requests.length, template.template_variant ? 1 : 0);
    for (const request of h.requests) assert.equal(Object.hasOwn(request.body, 'goal'), false);
  }
});

test('a goal-less ordinary template shows an empty editable field', () => {
  const h = harness({ id: 'blank', name: 'Blank' });
  h.subject.tplApplyReadOnly();
  assert.equal(h.node('tplEditGoal').value, '');
  assert.equal(h.node('tplEditGoal').readOnly, false);
});
