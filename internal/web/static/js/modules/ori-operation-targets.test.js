import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createOperationTargets,
  operationWorkspaceURL,
  comparablePath,
  isSupportedAction,
  OUTCOME
} from './ori-operation-targets.js';

/** A workspace page whose controller mounts after `mountAfter` polls. */
function makeEnv(overrides = {}) {
  const state = {
    pathname: '/',
    workspaceId: 'ws-album',
    roster: [{ key: 'mixer', name: 'Mixer' }],
    selected: '',
    control: { tag: 'mixer-control' },
    mountAfter: 0,
    rosterAfter: 0,
    unsaved: false,
    waits: 0,
    selections: [],
    ...overrides
  };
  const controller = {
    normalizeAgentKey: name =>
      String(name || '')
        .trim()
        .toLowerCase(),
    agentGroups: () => ({ agents: state.waits >= state.rosterAfter ? state.roster : [] }),
    selectAgent: (encoded, options) => {
      state.selections.push({ name: decodeURIComponent(encoded), options });
      if (state.selects !== false) state.selected = decodeURIComponent(encoded).toLowerCase();
    },
    get selectedAgentKey() {
      return state.selected;
    }
  };
  const env = {
    pathname: () => state.pathname,
    workspacePage: () => ({
      workspaceId: state.workspaceId,
      command: state.noController || state.waits < state.mountAfter ? null : controller
    }),
    agentControl: key => (state.control && key === 'mixer' ? state.control : null),
    hasUnsavedWork: () => state.unsaved,
    wait: async () => {
      state.waits += 1;
    }
  };
  return { env, state, targets: createOperationTargets(env) };
}

const open = { action: 'open_workspace', workspace_id: 'ws-album', workspace_slug: 'album-1' };
const select = {
  action: 'select_agent',
  workspace_id: 'ws-album',
  agent_name: 'Mixer',
  agent_node_id: 'Mixer-node-1'
};

test('the only navigable address is a workspace page built from a plain slug', () => {
  assert.equal(operationWorkspaceURL('album-1'), '/workspaces/album-1');
  assert.equal(operationWorkspaceURL('My Album'), '');
  for (const hostile of [
    '',
    '../settings',
    'a/b',
    'https://evil.test',
    '//evil.test',
    'album?x=1',
    'album#frag',
    'a\\b',
    'x'.repeat(201)
  ]) {
    assert.equal(operationWorkspaceURL(hostile), '', `refused: ${hostile.slice(0, 20)}`);
  }
  assert.equal(comparablePath('/workspaces/album-1/'), '/workspaces/album-1');
  assert.equal(comparablePath('/workspaces/caf%C3%A9'), '/workspaces/café');
});

test('only named actions are supported', () => {
  assert.equal(isSupportedAction('open_workspace'), true);
  assert.equal(isSupportedAction('select_agent'), true);
  for (const action of ['', 'click', 'eval', 'navigate', 'open_url', 'constructor']) {
    assert.equal(isSupportedAction(action), false);
  }
});

test('open_workspace asks for navigation only when the user just asked', async () => {
  const { targets, state } = makeEnv({ pathname: '/' });
  assert.deepEqual(await targets.execute(open, { allowNavigate: true }), {
    outcome: OUTCOME.NAVIGATING,
    url: '/workspaces/album-1'
  });
  // A page load somewhere else is the user having moved: nothing navigates.
  assert.deepEqual(await targets.execute(open, { allowNavigate: false }), {
    outcome: OUTCOME.YIELD,
    reason: 'moved_elsewhere'
  });
  state.unsaved = true;
  assert.deepEqual(await targets.execute(open, { allowNavigate: true }), {
    outcome: OUTCOME.YIELD,
    reason: 'unsaved_work'
  });
});

test('open_workspace is acknowledged only by the page that arrived', async () => {
  const { targets } = makeEnv({ pathname: '/workspaces/album-1/' });
  assert.deepEqual(await targets.execute(open, { allowNavigate: false }), {
    outcome: OUTCOME.ACKNOWLEDGED,
    observed: { path: '/workspaces/album-1/' }
  });
  const hostile = makeEnv({ pathname: '/' });
  const refused = await hostile.targets.execute(
    { ...open, workspace_slug: 'https://evil.test/x' },
    { allowNavigate: true }
  );
  assert.equal(refused.outcome, OUTCOME.UNAVAILABLE);
});

test('select_agent waits for the controller and roster, then reports the observed selection', async () => {
  const { targets, state } = makeEnv({ mountAfter: 3, rosterAfter: 5 });
  const result = await targets.execute(select, {});
  assert.equal(result.outcome, OUTCOME.ACKNOWLEDGED);
  assert.deepEqual(result.observed, { agent_key: 'Mixer' });
  assert.equal(result.element, state.control);
  // The page may replace the node; the control is found again from its key.
  const replacement = { tag: 'mixer-control-rerendered' };
  state.control = replacement;
  assert.equal(result.locate(), replacement);
  // The page's own selection owner did the selecting, without taking focus.
  assert.deepEqual(state.selections, [{ name: 'Mixer', options: { focus: false } }]);
});

test('select_agent reports unavailable when the page cannot show it', async () => {
  const noController = makeEnv({ noController: true });
  assert.equal((await noController.targets.execute(select, {})).outcome, OUTCOME.UNAVAILABLE);
  assert.ok(noController.state.waits >= 40, 'the wait for a mounting controller is bounded');

  const notInRoster = makeEnv({ roster: [{ key: 'engineer', name: 'Engineer' }] });
  const missing = await notInRoster.targets.execute(select, {});
  assert.equal(missing.outcome, OUTCOME.UNAVAILABLE);
  assert.match(missing.reason, /not in this workspace's roster/);
  assert.deepEqual(notInRoster.state.selections, []);

  const noControl = makeEnv({ control: null });
  const hidden = await noControl.targets.execute(select, {});
  assert.equal(hidden.outcome, OUTCOME.UNAVAILABLE);
  assert.match(hidden.reason, /not shown in this view/);

  const ignored = makeEnv({ selects: false });
  assert.equal((await ignored.targets.execute(select, {})).outcome, OUTCOME.UNAVAILABLE);
});

test('select_agent never acts on another workspace', async () => {
  const { targets, state } = makeEnv({ workspaceId: 'ws-other' });
  assert.deepEqual(await targets.execute(select, {}), {
    outcome: OUTCOME.YIELD,
    reason: 'moved_elsewhere'
  });
  assert.deepEqual(state.selections, []);
});

test('unknown actions and thrown controllers cannot drive the page', async () => {
  const { targets, state } = makeEnv();
  for (const command of [
    { action: 'click', selector: '#delete' },
    { action: 'open_url', url: 'https://evil.test' },
    { action: 'eval', script: 'alert(1)' },
    null
  ]) {
    assert.equal(
      (await targets.execute(command, { allowNavigate: true })).outcome,
      OUTCOME.UNAVAILABLE
    );
  }
  assert.deepEqual(state.selections, []);

  const broken = makeEnv();
  broken.env.workspacePage = () => {
    throw new Error('page controller blew up');
  };
  assert.equal(
    (await createOperationTargets(broken.env).execute(select, {})).outcome,
    OUTCOME.UNAVAILABLE
  );
});
