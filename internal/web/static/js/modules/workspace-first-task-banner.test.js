import test from 'node:test';
import assert from 'node:assert/strict';

import {
  assessmentBannerView,
  assessmentStartNote,
  firstTaskBannerView,
  firstTaskStartMessage,
  renderFirstTaskBanner,
  sameSitePath
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

// --- A software project's first assessment -----------------------------------

// The server's reading of an assessment, as the first-task endpoint sends it.
const assessment = (overrides = {}) => ({
  recipe: 'software_assessment',
  state: 'seeded',
  can_start: true,
  task_id: 'task-1',
  workspace_id: 'ws-1',
  workspace_name: 'Widget',
  workspace_route: '/workspaces/widget',
  folder_name: 'widget',
  ...overrides
});
// The page's own link to the task: its page, where its result is shown.
const taskHref = '/workspaces/widget/task/task-1';
const linkIds = view => view.links.map(link => link.id);

test('an assessment that can start says what one press spends and does not do', () => {
  const view = assessmentBannerView(assessment(), { taskHref });
  assert.equal(view.phase, 'ready');
  assert.equal(view.heading, 'The first assessment of widget is ready to start');
  assert.match(view.detail, /spends model tokens once/);
  assert.match(view.detail, /no tools/);
  assert.match(view.detail, /nothing in the project is run or changed/);
  assert.deepEqual(view.action, {
    id: 'start-first-look',
    label: 'Start assessment',
    starting: false
  });
  // Nothing to watch or open yet: the only control is the button.
  assert.deepEqual(view.links, []);
  assert.equal(view.dismiss, false);
  assert.deepEqual(assessmentBannerView(assessment(), { busy: true }).action, {
    id: 'start-first-look',
    label: 'Starting…',
    starting: true
  });
  // A start the server could not explain is said beside the button.
  const refused = assessmentBannerView(assessment(), {
    message: assessmentStartNote('start_failed')
  });
  assert.equal(refused.note, 'The assessment could not start. Try again.');
  assert.equal(refused.action.label, 'Start assessment');
});

test('an assessment that cannot start offers its repair and no button', () => {
  const view = assessmentBannerView(
    assessment({
      can_start: false,
      reason: 'model_unsupported',
      message: 'Robin uses a model that cannot run a read-only assessment. Choose another model.',
      repair_route: '/agents',
      repair_label: 'Choose a model'
    }),
    { taskHref }
  );
  assert.equal(view.phase, 'needs-you');
  assert.equal(view.heading, 'The assessment of widget cannot start yet');
  assert.match(view.detail, /cannot run a read-only assessment/);
  assert.equal(view.action, null);
  assert.deepEqual(view.links, [
    { id: 'repair', label: 'Choose a model', href: '/agents', primary: true }
  ]);

  // Nothing the server can point at: no control that would do nothing.
  const bare = assessmentBannerView(assessment({ can_start: false }));
  assert.equal(bare.detail, 'Something it needs is missing.');
  assert.equal(bare.action, null);
  assert.deepEqual(bare.links, []);
});

test('a running assessment says nothing is being run and has nothing to press', () => {
  const view = assessmentBannerView(assessment({ state: 'running', can_start: false }), {
    taskHref
  });
  assert.equal(view.phase, 'working');
  assert.equal(view.heading, 'Assessing widget…');
  assert.match(view.detail, /Nothing in it is being run or changed\./);
  assert.equal(view.action, null);
  assert.deepEqual(view.links, [
    { id: 'task', label: 'Watch the task', href: taskHref, primary: false }
  ]);
});

test('a finished assessment opens its report and hands on one suggestion', () => {
  const done = assessment({
    state: 'finished',
    can_start: false,
    ticket_route: '/workspaces/widget?ticket=task-1',
    report_route: '/workspaces/widget/task/task-1',
    next_request: 'Add a health check endpoint.',
    next_route: '/workspaces/widget/plans?next=assessment'
  });
  const view = assessmentBannerView(done, { taskHref });
  assert.equal(view.phase, 'done');
  assert.equal(view.heading, 'The first assessment of widget is ready');
  assert.equal(view.detail, 'It suggests one next task: Add a health check endpoint.');
  assert.equal(view.action, null);
  assert.deepEqual(view.links, [
    {
      id: 'report',
      label: 'Open the assessment',
      // The task's page shows what the run wrote; its ticket does not.
      href: '/workspaces/widget/task/task-1',
      primary: true
    },
    {
      id: 'next',
      label: 'Plan the suggested task',
      href: '/workspaces/widget/plans?next=assessment',
      primary: false
    }
  ]);
  assert.equal(view.dismiss, true);
  // Put away, it stays away; nothing else is ever hidden that way.
  assert.equal(assessmentBannerView(done, { dismissed: true }).visible, false);
  assert.equal(
    assessmentBannerView(assessment({ state: 'failed' }), { dismissed: true }).visible,
    true
  );
});

test('a suggestion is linked only to its own plans page, and a report only to its task', () => {
  const done = overrides =>
    assessmentBannerView(
      assessment({ state: 'finished', next_request: 'Add a health check.', ...overrides }),
      { taskHref }
    );
  for (const route of [
    'https://evil.example/workspaces/widget/plans?next=assessment',
    '//evil.example/plans?next=assessment',
    '/workspaces/widget/plans?next=assessment&run=1',
    '/workspaces/widget/plans',
    ''
  ]) {
    const view = done({ next_route: route });
    assert.deepEqual(linkIds(view), ['report'], route);
    assert.equal(view.detail, 'Open it to read what was found.', route);
  }
  // A route with nothing to carry is not offered.
  assert.deepEqual(
    linkIds(done({ next_request: '', next_route: '/workspaces/widget/plans?next=assessment' })),
    ['report']
  );
  // A report link that is not the task's page falls back to the page's own
  // link to the task; the ticket, which shows no result, is never used.
  for (const report of [
    'https://evil.example/report',
    '/workspaces/widget?ticket=task-1',
    '/workspaces/widget/task/task-1/run'
  ])
    assert.equal(done({ report_route: report }).links[0].href, taskHref, report);
  assert.deepEqual(
    assessmentBannerView(assessment({ state: 'finished', report_route: '//evil.example' })).links,
    []
  );
  assert.deepEqual(
    assessmentBannerView(
      assessment({ state: 'finished', ticket_route: '/workspaces/widget?ticket=task-1' })
    ).links,
    []
  );
});

test('an assessment that did not finish is tried again only when nothing needs repair', () => {
  const retryable = assessmentBannerView(
    assessment({
      state: 'failed',
      can_start: false,
      can_retry: true,
      message: 'The model did not answer in time. Nothing was changed.'
    }),
    { taskHref }
  );
  assert.equal(retryable.phase, 'failed');
  assert.equal(retryable.heading, 'The assessment of widget did not finish');
  assert.equal(retryable.detail, 'The model did not answer in time. Nothing was changed.');
  assert.deepEqual(retryable.action, {
    id: 'retry-first-look',
    label: 'Try again',
    starting: false
  });
  assert.deepEqual(linkIds(retryable), ['task']);

  const needsRepair = assessmentBannerView(
    assessment({
      state: 'failed',
      can_start: false,
      can_retry: false,
      reason: 'project_folder_unavailable',
      message: 'The project folder is not where it was. Show it again.',
      repair_route: '/?panel=today&folder=show',
      repair_label: 'Show the folder'
    }),
    { taskHref }
  );
  assert.equal(needsRepair.action, null);
  assert.deepEqual(needsRepair.links[0], {
    id: 'repair',
    label: 'Show the folder',
    href: '/?panel=today&folder=show',
    primary: true
  });

  // A repair that leaves the app is not a link, and no retry appears instead.
  const offSite = assessmentBannerView(
    assessment({
      state: 'failed',
      can_retry: false,
      repair_route: 'https://evil.example',
      repair_label: 'Fix it'
    })
  );
  assert.equal(offSite.action, null);
  assert.deepEqual(offSite.links, []);
  assert.equal(offSite.detail, 'Nothing in the project was changed.');

  // A browser reads a backslash as a slash, so these are other sites too.
  for (const route of ['/\\evil.example', '/\\/evil.example', '//evil.example', '/agents\nx']) {
    const view = assessmentBannerView(
      assessment({
        state: 'failed',
        can_retry: false,
        repair_route: route,
        repair_label: 'Fix it'
      }),
      { taskHref: route }
    );
    assert.deepEqual(view.links, [], JSON.stringify(route));
  }
  assert.equal(sameSitePath('/agents'), '/agents');
  assert.equal(sameSitePath('/settings#api-keys'), '/settings#api-keys');
  assert.equal(sameSitePath('/?panel=today&folder=show'), '/?panel=today&folder=show');
  for (const route of ['', 'agents', 'https://x.example', '//x.example', '/\\x.example', '/a\tb'])
    assert.equal(sameSitePath(route), '', JSON.stringify(route));
});

test('only a recipe task in a state the server names is an assessment banner', () => {
  assert.equal(assessmentBannerView(null).visible, false);
  assert.equal(assessmentBannerView(assessment({ recipe: '' })).visible, false);
  assert.equal(assessmentBannerView(assessment({ state: 'bogus' })).visible, false);
  // Without a folder name the workspace's is used, then a plain phrase.
  assert.equal(
    assessmentBannerView(assessment({ folder_name: '' })).heading,
    'The first assessment of Widget is ready to start'
  );
  assert.equal(
    assessmentBannerView(assessment({ folder_name: '', workspace_name: '' })).heading,
    'The first assessment of this project is ready to start'
  );
});

test('a refused start is explained by the server, and only an unexplained one here', () => {
  for (const reason of ['start_failed', 'execution_unavailable', 'admission_unavailable'])
    assert.equal(assessmentStartNote(reason), 'The assessment could not start. Try again.');
  for (const reason of ['model_unsupported', 'no_model', 'project_folder_unavailable', ''])
    assert.equal(assessmentStartNote(reason), '', reason);
});

test('the rendered assessment banner wires each control to its own handler', () => {
  const doc = fakeDoc();
  const mount = doc.createElement('section');
  const pressed = [];
  const handlers = {
    onStart: () => pressed.push('start'),
    onRetry: () => pressed.push('retry'),
    onDismiss: () => pressed.push('dismiss')
  };
  const buttons = () => mount.children.filter(node => node.tag === 'button');
  const links = () => mount.children.filter(node => node.tag === 'a');

  renderFirstTaskBanner(mount, assessmentBannerView(assessment()), doc, handlers);
  assert.equal(mount.dataset.phase, 'ready');
  assert.deepEqual(
    buttons().map(node => [node.textContent, node.dataset.firstTaskAction]),
    [['Start assessment', 'start-first-look']]
  );
  buttons()[0].listeners.click();

  renderFirstTaskBanner(
    mount,
    assessmentBannerView(assessment({ state: 'failed', can_retry: true }), { taskHref }),
    doc,
    handlers
  );
  assert.equal(mount.dataset.phase, 'failed');
  assert.equal(buttons()[0].textContent, 'Try again');
  buttons()[0].listeners.click();
  assert.deepEqual(
    links().map(node => [node.textContent, node.href]),
    [['Open the task', taskHref]]
  );

  renderFirstTaskBanner(
    mount,
    assessmentBannerView(
      assessment({
        state: 'finished',
        report_route: taskHref,
        // The model's words reach the page as text only.
        next_request: '<img src=x onerror=alert(1)>',
        next_route: '/workspaces/widget/plans?next=assessment'
      })
    ),
    doc,
    handlers
  );
  assert.deepEqual(
    links().map(node => [node.dataset.firstTaskLink, node.className]),
    [
      ['report', 'btn btn-sm btn-primary'],
      ['next', 'btn btn-sm btn-outline-secondary']
    ]
  );
  const text = mount.children.find(node => node.className === 'first-task-banner__text');
  assert.equal(
    text.children[1].textContent,
    'It suggests one next task: <img src=x onerror=alert(1)>'
  );
  assert.equal(text.children[1].innerHTML, undefined);
  const dismiss = buttons().find(node => node.dataset.firstTaskAction === 'dismiss');
  assert.equal(dismiss.textContent, 'Dismiss');
  dismiss.listeners.click();
  assert.deepEqual(pressed, ['start', 'retry', 'dismiss']);
});
