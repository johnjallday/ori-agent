import test from 'node:test';
import assert from 'node:assert/strict';

import {
  firstTaskBannerView,
  firstTaskStartMessage,
  renderFirstTaskBanner
} from './workspace-first-task-banner.js';

// A minimal element, enough for the banner's renderer.
function fakeDoc() {
  const make = tag => ({
    tag,
    className: '',
    dataset: {},
    children: [],
    listeners: {},
    textContent: '',
    hidden: false,
    setAttribute() {},
    append(...nodes) {
      this.children.push(...nodes);
    },
    replaceChildren(...nodes) {
      this.children = [...nodes];
    },
    addEventListener(type, handler) {
      this.listeners[type] = handler;
    }
  });
  return { createElement: make };
}

test('a first look nobody has started offers the one button that starts it', () => {
  const view = firstTaskBannerView({
    state: 'assigned',
    agentName: 'Writing Coach',
    taskTitle: 'Summarize the current draft',
    href: '/workspaces/thesis?ticket=t1',
    seeded: true
  });
  assert.equal(view.phase, 'ready');
  assert.equal(view.heading, 'Writing Coach is ready for its first look at this folder');
  assert.match(view.detail, /Summarize the current draft/);
  assert.match(view.detail, /read-only/);
  // The click is what spends tokens, and the banner says so before it.
  assert.match(view.detail, /Starting it spends model tokens\./);
  assert.deepEqual(view.action, {
    id: 'start-first-look',
    label: 'Start first look',
    starting: false
  });
  // Nothing to watch yet: the only control is the button.
  assert.equal(view.href, '');
  assert.equal(view.note, '');
});

test('the button shows the click landing, and a refusal replaces the detail', () => {
  const starting = firstTaskBannerView({ agentName: 'Atlas', seeded: true, starting: true });
  assert.deepEqual(starting.action, {
    id: 'start-first-look',
    label: 'Starting…',
    starting: true
  });

  const refused = firstTaskBannerView({
    agentName: 'Atlas',
    seeded: true,
    message: firstTaskStartMessage('setup_wizard_opening')
  });
  assert.equal(refused.phase, 'ready');
  assert.equal(refused.detail, 'Finish this workspace’s setup first, then start the first look.');
  assert.equal(refused.action.label, 'Start first look');
});

test('a first look with no agent says so instead of offering a button', () => {
  for (const agentName of ['', '  ', 'unassigned', 'Unassigned']) {
    const view = firstTaskBannerView({ state: 'pending', agentName, seeded: true });
    assert.equal(view.phase, 'ready', agentName);
    assert.equal(view.heading, 'The first look at this folder is ready', agentName);
    assert.equal(view.action, null, agentName);
    assert.equal(
      view.note,
      'The first task has no agent yet. Assign one, then start the first look.',
      agentName
    );
  }
});

test('every refusal has a sentence, and an unknown one only says it did not start', () => {
  assert.equal(
    firstTaskStartMessage('local_activation_required'),
    'Activate this workspace on this computer to run the first look.'
  );
  for (const reason of ['start_failed', 'execution_unavailable', 'something_new', '', undefined]) {
    assert.equal(firstTaskStartMessage(reason), 'The first look could not start. Try again.');
  }
});

test('the rendered banner wires the button to its start and disables it while starting', () => {
  const doc = fakeDoc();
  const mount = doc.createElement('section');
  mount.hidden = true;
  let pressed = 0;
  renderFirstTaskBanner(
    mount,
    firstTaskBannerView({ agentName: 'Atlas', taskTitle: 'Look', seeded: true }),
    doc,
    { onStart: () => pressed++ }
  );
  assert.equal(mount.hidden, false);
  assert.equal(mount.dataset.phase, 'ready');
  const button = mount.children.find(node => node.tag === 'button');
  assert.equal(button.textContent, 'Start first look');
  assert.equal(button.dataset.firstTaskAction, 'start-first-look');
  assert.equal(button.disabled, false);
  button.listeners.click();
  assert.equal(pressed, 1);
  assert.equal(
    mount.children.some(node => node.tag === 'a'),
    false
  );

  renderFirstTaskBanner(
    mount,
    firstTaskBannerView({ agentName: 'Atlas', seeded: true, starting: true }),
    doc
  );
  assert.equal(mount.children.find(node => node.tag === 'button').disabled, true);

  // A started look has a link and no button; a hidden view clears the banner.
  renderFirstTaskBanner(
    mount,
    firstTaskBannerView({
      state: 'in_progress',
      agentName: 'Atlas',
      href: '/workspaces/a?ticket=t'
    }),
    doc
  );
  assert.equal(
    mount.children.some(node => node.tag === 'button'),
    false
  );
  assert.equal(mount.children.find(node => node.tag === 'a').textContent, 'Watch the task');
  renderFirstTaskBanner(mount, null, doc);
  assert.equal(mount.hidden, true);
  assert.deepEqual(mount.children, []);
});

test('a running first task says the agent is working and that it is read-only', () => {
  const view = firstTaskBannerView({
    state: 'in_progress',
    agentName: 'REAPER Assistant',
    taskTitle: 'Summarize the session',
    href: '/workspaces/session/task/t1'
  });
  assert.equal(view.phase, 'working');
  assert.equal(view.heading, 'REAPER Assistant is working on its first task');
  assert.match(view.detail, /Summarize the session/);
  assert.match(view.detail, /read-only/);
  assert.equal(view.linkLabel, 'Watch the task');
  assert.equal(view.href, '/workspaces/session/task/t1');
});

test('the banner follows the task to its end', () => {
  const at = state => firstTaskBannerView({ state, agentName: 'Atlas' });
  assert.equal(at('completed').phase, 'done');
  assert.equal(at('completed').heading, 'Atlas finished its first task');
  assert.equal(at('blocked').phase, 'needs-you');
  assert.equal(at('waiting_for_choice').phase, 'needs-you');
  for (const failed of ['failed', 'timeout', 'cancelled']) {
    assert.equal(at(failed).phase, 'failed');
  }
  assert.equal(at('pending').phase, 'working');
  assert.equal(at('something-new').phase, 'working');
});

test('without an agent name the banner still reads, and only same-site links are kept', () => {
  assert.equal(
    firstTaskBannerView({ state: 'pending' }).heading,
    'Your agent is working on its first task'
  );
  assert.equal(firstTaskBannerView({ state: 'pending', href: '//evil.example/x' }).href, '');
  assert.equal(firstTaskBannerView({ state: 'pending', href: 'https://evil.example/x' }).href, '');
});
