import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';

// The constructor only reads the DOM (getElementById) and returns early from
// setup() when there's no container — a null-returning stub is enough to build
// an instance whose pure format helpers we can exercise.
globalThis.document = { getElementById: () => null };
const view = new WorkspaceCommandView(null);

function makeToggleButton() {
  return {
    attrs: {},
    classList: { toggle() {} },
    setAttribute(name, value) {
      this.attrs[name] = value;
    }
  };
}

function makeListenerRoot() {
  return {
    listener: null,
    addEventListener(type, listener) {
      if (type === 'click') this.listener = listener;
    }
  };
}

function makeSectionClickTarget(section) {
  return {
    closest(selector) {
      if (selector !== '[data-cmd-section]') return null;
      return {
        getAttribute(name) {
          return name === 'data-cmd-section' ? section : '';
        }
      };
    }
  };
}

function makeAttributeClickTarget(attrs) {
  return {
    closest(selector) {
      if (selector.includes('data-cmd-open-task') && attrs['data-cmd-open-task']) {
        return {
          getAttribute(name) {
            return attrs[name] || '';
          }
        };
      }
      if (selector.includes('data-cmd-primary-section') && attrs['data-cmd-primary-section']) {
        return {
          getAttribute(name) {
            return attrs[name] || '';
          }
        };
      }
      if (selector.includes('data-cmd-manage-section') && attrs['data-cmd-manage-section']) {
        return {
          getAttribute(name) {
            return attrs[name] || '';
          }
        };
      }
      if (
        selector.includes('data-cmd-open-section') &&
        selector.includes('data-cmd-item-id') &&
        attrs['data-cmd-open-section'] &&
        attrs['data-cmd-item-id']
      ) {
        return {
          getAttribute(name) {
            return attrs[name] || '';
          }
        };
      }
      return null;
    }
  };
}

test('statusTone maps an agent status to a visual tone', () => {
  assert.equal(view.statusTone('working', ''), 'working');
  assert.equal(view.statusTone('', 'running a task'), 'working');
  assert.equal(view.statusTone('busy', ''), 'working');
  assert.equal(view.statusTone('error', ''), 'alert');
  assert.equal(view.statusTone('failed', ''), 'alert');
  assert.equal(view.statusTone('idle', ''), 'idle');
  assert.equal(view.statusTone('', ''), 'idle');
});

test('taskTone maps a task status to a visual tone', () => {
  assert.equal(view.taskTone('pending'), 'pending');
  assert.equal(view.taskTone('in_progress'), 'working');
  assert.equal(view.taskTone('completed'), 'done');
  assert.equal(view.taskTone('done'), 'done');
  assert.equal(view.taskTone('failed'), 'alert');
  assert.equal(view.taskTone('cancelled'), 'alert');
  assert.equal(view.taskTone('anything-else'), 'pending');
});

test('opsModeLabel maps workflow modes to friendly labels', () => {
  const withMode = (mode) => {
    view.page = { workspace: { workspace_settings: { workflow: { mode } } } };
    return view.opsModeLabel();
  };
  assert.equal(withMode('guided'), 'Guided');
  assert.equal(withMode('direct'), 'Direct');
  assert.equal(withMode('plan_then_execute'), 'Autonomous');
  assert.equal(withMode(''), '');
});

test('computeStats reads counts from the page instance', () => {
  view.page = {
    buildAgentGroups: () => [
      { isWorkspaceAgent: true, isUnassigned: false },
      { isWorkspaceAgent: true, isUnassigned: false },
      { isWorkspaceAgent: false, isUnassigned: true } // unassigned bucket, not an agent
    ],
    tasks: [{ status: 'pending' }, { status: 'in_progress' }, { status: 'completed' }],
    workspace: { mcp_bindings: [{}, {}], skill_bindings: [{}] }
  };
  assert.deepEqual(view.computeStats(), { agents: 2, openTasks: 2, mcp: 2, skills: 1 });
});

test('rendered command copy uses detailed-view vocabulary', () => {
  const readoutRoot = makeListenerRoot();
  const garrisonRoot = makeListenerRoot();
  const railRoot = makeListenerRoot();
  const backButton = makeListenerRoot();
  const container = {
    hidden: true,
    innerHTML: '',
    querySelector(selector) {
      if (selector === '[data-ws-cmd-detailed]') return backButton;
      if (selector === '.ws-cmd-readout') return readoutRoot;
      if (selector === '.ws-cmd-garrison') return garrisonRoot;
      if (selector === '.ws-cmd-rail') return railRoot;
      return null;
    }
  };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    container,
    detailedView: { hidden: false },
    toggleBtn: makeToggleButton(),
    active: true,
    page: {
      workspaceId: 'workspace-1',
      workspace: {
        name: 'Demo Workspace',
        description: 'A focused production workspace for launch planning and execution.',
        tags: ['launch', 'ops'],
        workspace_settings: { workflow: { mode: 'guided' } },
        mcp_bindings: [],
        skill_bindings: []
      },
      tasks: [{ status: 'pending' }],
      notes: [],
      schedules: [],
      sessions: [],
      directories: [],
      buildAgentGroups: () => [
        {
          name: 'Atlas',
          isWorkspaceAgent: true,
          isUnassigned: false,
          tasks: [{ id: 'task-1', description: 'Draft plan', status: 'pending' }]
        },
        {
          name: 'Builder',
          isWorkspaceAgent: true,
          isUnassigned: false,
          tasks: []
        }
      ],
      isWorkspaceEntryAgent: name => name === 'Atlas',
      getAgentAvatarPresentation: name => ({ initials: name.slice(0, 2), style: '' }),
      getAgentRosterStatus: () => ({ key: 'idle', label: 'Idle' }),
      getAgentProfile: () => ({}),
      getAgentModelPresentation: () => ({ empty: false, model: 'gpt-test' }),
      getAgentSkillSummary: () => ({ count: 0 })
    },
    persist() {}
  });

  commandView.render();

  assert.match(container.innerHTML, /Workflow · Guided/);
  assert.match(container.innerHTML, /A focused production workspace/);
  assert.match(container.innerHTML, /launch/);
  assert.match(container.innerHTML, /href="\/workspaces"/);
  assert.match(container.innerHTML, /href="\/workspaces\/workspace-1\/canvas"/);
  assert.match(container.innerHTML, /href="\/workspaces\/workspace-1\/diagnostics"/);
  assert.match(container.innerHTML, /href="\/workflows"/);
  assert.match(container.innerHTML, /data-cmd-edit-identity="name"/);
  assert.match(container.innerHTML, /data-cmd-edit-identity="description"/);
  assert.match(container.innerHTML, /data-cmd-edit-identity="tags"/);
  assert.match(container.innerHTML, /<div class="ws-l">Open Tasks<\/div>/);
  assert.match(container.innerHTML, /<div class="ws-l">MCP<\/div>/);
  assert.match(container.innerHTML, />Notes<\/h4>/);
  assert.match(container.innerHTML, />Schedules<\/h4>/);
  assert.match(container.innerHTML, />Sessions<\/h4>/);
  assert.match(container.innerHTML, />Linked Folders<\/h4>/);
  assert.match(container.innerHTML, /data-cmd-primary-section="notes"/);
  assert.match(container.innerHTML, /data-cmd-primary-section="folders"/);
  assert.doesNotMatch(container.innerHTML, /No notes yet\./);
  assert.doesNotMatch(container.innerHTML, /No schedules yet\./);
  assert.doesNotMatch(container.innerHTML, /No sessions yet\./);
  assert.doesNotMatch(container.innerHTML, /No linked folders yet\./);
  assert.match(container.innerHTML, /★ Entry Agent/);
  assert.match(container.innerHTML, />Model<\/span>/);
  assert.match(container.innerHTML, /Tasks · 1/);
  assert.match(container.innerHTML, /title="Run"/);
  assert.match(container.innerHTML, /Manage Notes in Command view/);
  // "Open Tasks" is now the deliberate exception: the summary stat chip's
  // label was unified with Map/Cards (see workspace-hub-ui-ux task 5.2). It
  // reflects the filtered open-task count specifically — the modal title and
  // per-agent quest-log header stay "Tasks" since those list every task
  // regardless of status, so "Open Tasks" there would misdescribe them.
  assert.doesNotMatch(container.innerHTML, /Quest Log|Keeper|Field Unit|Intel|Comms|Supply Lines|Standing Orders|Tools · MCP|Ops mode|Deploy|✦/);
});

test('command rail renders project_path fallback when no directories are loaded', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    activeRailSection: '',
    page: {
      notes: [],
      schedules: [],
      sessions: [],
      directories: [],
      workspace: { project_path: '/tmp/smoke-song' }
    }
  });

  const html = commandView.renderRail();

  assert.match(html, /smoke-song/);
  assert.match(html, /Project Folder/);
  assert.match(html, /\/tmp\/smoke-song/);
  assert.doesNotMatch(html, /No linked folders yet\./);
});

test('command rail badges project and reference directory roles', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    activeRailSection: '',
    page: {
      notes: [],
      schedules: [],
      sessions: [],
      directories: [
        { id: 'dir-project', name: 'Project', path: '/tmp/project', source: 'reference' },
        { id: 'dir-ref', name: 'Reference', path: '/tmp/reference', source: 'reference' }
      ],
      getPrimaryDirectoryId: () => 'dir-project',
      isProjectDirectory: (dir) => dir.id === 'dir-project'
    }
  });

  const html = commandView.renderRail();

  assert.match(html, /Project Folder/);
  assert.match(html, /Reference/);
  assert.match(html, /data-cmd-item-id="dir-project"/);
  assert.match(html, /data-cmd-item-id="dir-ref"/);
});

test('empty rail panels collapse to the header but keep primary actions', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    activeRailSection: '',
    page: {
      notes: [],
      schedules: [],
      sessions: [],
      directories: [],
      workspace: {}
    }
  });

  const html = commandView.renderRail();

  assert.match(html, /data-cmd-primary-section="notes"/);
  assert.match(html, /data-cmd-primary-section="schedules"/);
  assert.match(html, /data-cmd-primary-section="sessions"/);
  assert.match(html, /data-cmd-primary-section="folders"/);
  assert.doesNotMatch(html, /No notes yet\./);

  commandView.activeRailSection = 'notes';
  const managingHtml = commandView.renderRail();
  assert.match(managingHtml, /No notes yet\./);
});

test('escape closes the active rail manager without leaving Command', () => {
  const renders = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    active: true,
    activeRailSection: 'notes',
    statModalSection: '',
    identityEditMode: '',
    render() {
      renders.push(this.activeRailSection);
    }
  });

  commandView.handleGlobalKeydown({ key: 'Escape' });

  assert.equal(commandView.activeRailSection, '');
  assert.deepEqual(renders, ['']);
});

test('Open Tasks stat is tinted when any task needs attention', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    identityExpanded: false,
    identityEditMode: '',
    page: {
      workspaceId: 'workspace-1',
      workspace: { name: 'Attention Workspace', mcp_bindings: [], skill_bindings: [] },
      tasks: [{ status: 'blocked' }],
      buildAgentGroups: () => []
    }
  });

  const html = commandView.commandBarHTML(
    commandView.page.workspace,
    commandView.page.workspace.name,
    '',
    commandView.computeStats()
  );

  assert.match(html, /class="ws-cmd-stat is-alert" data-cmd-section="tasks"/);
});

test('stat clicks open the in-place manager modal without leaving Command view', () => {
  const readoutRoot = makeListenerRoot();
  const opened = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    active: true,
    container: {
      hidden: false,
      querySelector(selector) {
        return selector === '.ws-cmd-readout' ? readoutRoot : null;
      }
    },
    detailedView: { hidden: true },
    openStatModal(section) {
      opened.push(section);
    }
  });

  commandView.bindReadout();

  ['agents', 'tasks', 'mcp', 'skills'].forEach(section => {
    readoutRoot.listener({ target: makeSectionClickTarget(section) });
  });

  assert.deepEqual(opened, ['agents', 'tasks', 'mcp', 'skills']);
  // The Command view stays put — no deactivate, no view switch.
  assert.equal(commandView.container.hidden, false);
  assert.equal(commandView.detailedView.hidden, true);
});

test('rail more toggles command-local management without leaving Command view', () => {
  const railRoot = makeListenerRoot();
  const renders = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    activeRailSection: '',
    container: {
      hidden: false,
      querySelector(selector) {
        return selector === '.ws-cmd-rail' ? railRoot : null;
      }
    },
    detailedView: { hidden: true },
    render() {
      renders.push(this.activeRailSection);
    }
  });

  commandView.bindRail();

  railRoot.listener({ target: makeAttributeClickTarget({ 'data-cmd-manage-section': 'notes' }) });
  railRoot.listener({ target: makeAttributeClickTarget({ 'data-cmd-manage-section': 'notes' }) });

  assert.deepEqual(renders, ['notes', '']);
  assert.equal(commandView.container.hidden, false);
  assert.equal(commandView.detailedView.hidden, true);
});

test('rail primary actions use command-view management hooks', () => {
  const railRoot = makeListenerRoot();
  const calls = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    container: {
      querySelector(selector) {
        return selector === '.ws-cmd-rail' ? railRoot : null;
      }
    },
    page: {
      showNoteModal() { calls.push('new-note'); },
      showSchedulesModal() { calls.push('open-schedules'); },
      createNewSession() { calls.push('new-session'); },
      showAddDirectoryModal() { calls.push('link-folder'); }
    }
  });

  commandView.bindRail();

  ['notes', 'schedules', 'sessions', 'folders'].forEach(section => {
    railRoot.listener({
      target: makeAttributeClickTarget({ 'data-cmd-primary-section': section })
    });
  });

  assert.deepEqual(calls, ['new-note', 'open-schedules', 'new-session', 'link-folder']);
});

test('rail item actions open existing management flows from Command view', () => {
  const railRoot = makeListenerRoot();
  const calls = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    container: {
      querySelector(selector) {
        return selector === '.ws-cmd-rail' ? railRoot : null;
      }
    },
    page: {
      notes: [{ id: 'note-1', name: 'Launch note' }],
      showNoteModal(note) { calls.push(['note', note.id]); },
      openSchedule(id) { calls.push(['schedule', id]); },
      openSession(id) { calls.push(['session', id]); },
      openDirectoryExplorer(id, source) { calls.push(['folder', id, source]); }
    }
  });

  commandView.bindRail();

  [
    { 'data-cmd-open-section': 'notes', 'data-cmd-item-id': 'note-1' },
    { 'data-cmd-open-section': 'schedules', 'data-cmd-item-id': 'sched-1' },
    { 'data-cmd-open-section': 'sessions', 'data-cmd-item-id': 'session-1' },
    {
      'data-cmd-open-section': 'folders',
      'data-cmd-item-id': 'dir-1',
      'data-cmd-item-source': 'owned'
    }
  ].forEach(attrs => {
    railRoot.listener({ target: makeAttributeClickTarget(attrs) });
  });

  assert.deepEqual(calls, [
    ['note', 'note-1'],
    ['schedule', 'sched-1'],
    ['session', 'session-1'],
    ['folder', 'dir-1', 'owned']
  ]);
});

test('quest-log task name opens the existing task flow', () => {
  const garrisonRoot = makeListenerRoot();
  const calls = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    container: {
      querySelector(selector) {
        return selector === '.ws-cmd-garrison' ? garrisonRoot : null;
      }
    },
    page: {
      openTask(id) { calls.push(['open', id]); },
      executeTask(id) { calls.push(['run', id]); }
    }
  });

  commandView.bindGarrison();

  garrisonRoot.listener({
    target: makeAttributeClickTarget({ 'data-cmd-open-task': 'task-1' })
  });

  assert.deepEqual(calls, [['open', 'task-1']]);
});

test('identity saves delegate to the workspace detail save helpers', async () => {
  const calls = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    identityEditMode: 'name',
    identitySaving: false,
    renderCalls: 0,
    render() {
      this.renderCalls += 1;
    },
    page: {
      workspace: { name: 'Old Name', description: 'Old description' },
      async saveWorkspaceIdentityField(field, value, options) {
        calls.push([field, value, options.currentValue]);
        this.workspace.name = value;
        return { changed: true, workspace: this.workspace };
      }
    }
  });

  await commandView.saveIdentityField('name', 'New Name');

  assert.deepEqual(calls, [['name', 'New Name', 'Old Name']]);
  assert.equal(commandView.identityEditMode, '');
  assert.equal(commandView.identitySaving, false);
  assert.equal(commandView.renderCalls, 1);
});

test('command tag saves use saveWorkspaceTagList and close the editor', async () => {
  const saved = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    identityEditMode: 'tags',
    identitySaving: false,
    commandTagDraft: ['alpha', 'beta'],
    commandTagInput: null,
    renderCalls: 0,
    render() {
      this.renderCalls += 1;
    },
    destroyCommandTagInput() {},
    page: {
      async saveWorkspaceTagList(tags) {
        saved.push(tags);
      }
    }
  });

  await commandView.saveCommandTags();

  assert.deepEqual(saved, [['alpha', 'beta']]);
  assert.equal(commandView.identityEditMode, '');
  assert.equal(commandView.identitySaving, false);
  assert.equal(commandView.renderCalls, 1);
});

test('stat modal inerts command background and traps tab focus', () => {
  const topbar = {
    attrs: {},
    inert: false,
    setAttribute(name, value) { this.attrs[name] = value; },
    removeAttribute(name) { delete this.attrs[name]; }
  };
  const layout = {
    attrs: {},
    inert: false,
    setAttribute(name, value) { this.attrs[name] = value; },
    removeAttribute(name) { delete this.attrs[name]; }
  };
  const modal = {};
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    container: { children: [topbar, layout, modal] },
    statModalEl: modal
  });

  commandView.setCommandBackgroundInert(true);
  assert.equal(topbar.attrs['aria-hidden'], 'true');
  assert.equal(layout.inert, true);

  commandView.setCommandBackgroundInert(false);
  assert.equal(topbar.attrs['aria-hidden'], undefined);
  assert.equal(layout.inert, false);

  const first = { hidden: false, focused: 0, focus() { this.focused += 1; } };
  const last = { hidden: false, focused: 0, focus() { this.focused += 1; } };
  const originalDocument = globalThis.document;
  globalThis.document = { activeElement: last };
  commandView.statModalEl = {
    querySelector() {
      return {
        querySelectorAll() {
          return [first, last];
        }
      };
    }
  };
  let prevented = 0;
  try {
    commandView.trapStatModalFocus({ key: 'Tab', preventDefault() { prevented += 1; } });
  } finally {
    globalThis.document = originalDocument;
  }

  assert.equal(prevented, 1);
  assert.equal(first.focused, 1);
});

test('mcp manager modal lists bindings with add and detailed-view escape hatch', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page: {
      getWorkspaceMCPBindings: () => [
        { id: 'b1', serverName: 'filesystem', source: 'workspace', enabled: true },
        { id: 'b2', serverName: 'gmail', source: 'synthesized', enabled: false }
      ]
    }
  });

  const html = commandView.statModalHTML('mcp');
  assert.match(html, /MCP Servers/);
  assert.match(html, /filesystem/);
  assert.match(html, /gmail/);
  assert.match(html, /Disabled/);
  assert.match(html, /Synthesized/);
  assert.match(html, /data-cmd-modal-action="add"/);
  assert.match(html, /data-cmd-modal-action="edit" data-cmd-id="b1"/);
  assert.match(html, /data-cmd-modal-action="delete" data-cmd-id="b1"/);
  assert.match(html, /data-cmd-modal-action="detailed"/);
  // Synthesized bindings are not workspace-owned, so no remove control.
  assert.doesNotMatch(html, /data-cmd-modal-action="delete" data-cmd-id="b2"/);
});

test('skills manager modal lists bindings and offers add', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page: {
      getWorkspaceSkillBindings: () => [{ id: 's1', skillName: 'summarize', enabled: true, trusted: true }]
    }
  });

  const html = commandView.statModalHTML('skills');
  assert.match(html, />Skills</);
  assert.match(html, /summarize/);
  assert.match(html, /Trusted/);
  assert.match(html, /data-cmd-modal-action="edit" data-cmd-id="s1"/);
  assert.match(html, /data-cmd-modal-action="delete" data-cmd-id="s1"/);
});

test('agents manager modal locks the entry agent from removal', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page: {
      buildAgentGroups: () => [
        { name: 'Atlas', isWorkspaceAgent: true, isUnassigned: false },
        { name: 'Builder', isWorkspaceAgent: true, isUnassigned: false },
        { name: 'Unassigned', isWorkspaceAgent: false, isUnassigned: true }
      ],
      isWorkspaceEntryAgent: name => name === 'Atlas',
      getAgentAvatarPresentation: name => ({ initials: name.slice(0, 2), style: '' }),
      getAgentRosterStatus: () => ({ key: 'idle', label: 'Idle' }),
      getAgentProfile: () => ({}),
      getAgentModelPresentation: () => ({ empty: false, model: 'gpt-test' }),
      getAgentSkillSummary: () => ({ count: 2 })
    }
  });

  const html = commandView.statModalHTML('agents');
  assert.match(html, /Atlas/);
  assert.match(html, /Builder/);
  assert.match(html, /★ Entry Agent/);
  // Builder is removable; Atlas (entry agent) shows a lock, not a delete button.
  assert.match(html, /data-cmd-modal-action="delete" data-cmd-id="Builder"/);
  assert.doesNotMatch(html, /data-cmd-modal-action="delete" data-cmd-id="Atlas"/);
  // The unassigned bucket is not a real agent and should be excluded.
  assert.doesNotMatch(html, /Unassigned/);
});

test('tasks manager modal exposes run / open / delete controls', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page: { tasks: [{ id: 't1', description: 'Draft plan', status: 'pending', to: 'Atlas' }] }
  });

  const html = commandView.statModalHTML('tasks');
  assert.match(html, /Draft plan/);
  assert.match(html, /Atlas/);
  assert.match(html, /data-cmd-modal-action="run" data-cmd-id="t1"/);
  assert.match(html, /data-cmd-modal-action="open" data-cmd-id="t1"/);
  assert.match(html, /data-cmd-modal-action="delete" data-cmd-id="t1"/);
});

test('tasks manager modal defaults to open tasks and can show all rows', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    taskModalShowAll: false,
    page: {
      tasks: [
        { id: 't1', description: 'Pending task', status: 'pending' },
        { id: 't2', description: 'Running task', status: 'in_progress' },
        { id: 't3', description: 'Done task', status: 'completed' },
        { id: 't4', description: 'Failed task', status: 'failed' }
      ]
    }
  });

  let html = commandView.statModalHTML('tasks');
  assert.match(html, /Open Tasks/);
  assert.match(html, /<span class="ws-cmd-modal-count">2<\/span>/);
  assert.match(html, /Pending task/);
  assert.match(html, /Running task/);
  assert.doesNotMatch(html, /Done task/);
  assert.doesNotMatch(html, /Failed task/);
  assert.match(html, /Show all/);

  commandView.taskModalShowAll = true;
  html = commandView.statModalHTML('tasks');
  assert.match(html, /<span class="ws-cmd-modal-count">4<\/span>/);
  assert.match(html, /Done task/);
  assert.match(html, /Failed task/);
  assert.match(html, /Show open/);
});

test('tasks manager filter action refreshes visible row count', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    taskModalShowAll: false,
    statModalSection: 'tasks',
    renderCalls: 0,
    renderStatModalBody() {
      this.renderCalls += 1;
    }
  });

  commandView.handleStatModalAction('toggle-task-filter');

  assert.equal(commandView.taskModalShowAll, true);
  assert.equal(commandView.renderCalls, 1);
});

test('modal add/edit hand off to page flows and close the modal; delete stays open', () => {
  const calls = [];
  let closed = 0;
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    statModalSection: 'mcp',
    closeStatModal() {
      closed += 1;
      this.statModalSection = '';
    },
    page: {
      openWorkspaceMCPModal(id) { calls.push(['open', id]); },
      deleteWorkspaceMCPBinding(id) { calls.push(['delete', id]); }
    }
  });

  commandView.handleStatModalAction('add');
  commandView.statModalSection = 'mcp';
  commandView.handleStatModalAction('edit', 'b1');
  commandView.statModalSection = 'mcp';
  commandView.handleStatModalAction('delete', 'b2');

  assert.deepEqual(calls, [['open', undefined], ['open', 'b1'], ['delete', 'b2']]);
  assert.equal(closed, 2); // add + edit close the modal; delete keeps it open
});

test('modal detailed action closes the modal and deep-links the detailed view', () => {
  const calls = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    statModalSection: 'skills',
    closeStatModal() {
      calls.push(['close', this.statModalSection]);
      this.statModalSection = '';
    },
    openDetailedSection(section) {
      calls.push(['detailed', section]);
    }
  });

  commandView.handleStatModalAction('detailed');

  // Section is captured before close resets it, then forwarded to the deep-link.
  assert.deepEqual(calls, [['close', 'skills'], ['detailed', 'skills']]);
});

test('default deactivate path still persists detailed preference', () => {
  const persisted = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    active: true,
    container: { hidden: false },
    detailedView: { hidden: true },
    toggleBtn: makeToggleButton(),
    persist(value) {
      persisted.push(value);
    }
  });

  commandView.deactivate();

  assert.deepEqual(persisted, ['detailed']);
  assert.equal(commandView.container.hidden, true);
  assert.equal(commandView.detailedView.hidden, false);
});

test('getViewFromURL trusts explicit command and detailed params', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  const originalWindow = globalThis.window;
  try {
    globalThis.window = { location: { search: '?view=command' } };
    assert.equal(commandView.getViewFromURL(), 'command');

    globalThis.window = { location: { search: '?view=detailed' } };
    assert.equal(commandView.getViewFromURL(), 'detailed');

    globalThis.window = { location: { search: '' } };
    assert.equal(commandView.getViewFromURL(), '');
  } finally {
    globalThis.window = originalWindow;
  }
});

test('syncURL clears command default and marks detailed via replaceState', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  const originalWindow = globalThis.window;
  const replaceStateCalls = [];
  globalThis.window = {
    location: { search: '', pathname: '/workspaces/abc' },
    history: {
      replaceState(state, title, url) {
        replaceStateCalls.push(url);
      }
    }
  };

  try {
    commandView.syncURL('command');
    assert.deepEqual(replaceStateCalls, ['/workspaces/abc']);

    globalThis.window.location.search = '';
    commandView.syncURL('detailed');
    assert.deepEqual(replaceStateCalls, ['/workspaces/abc', '/workspaces/abc?view=detailed']);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('setup defaults to Command view without URL or saved preference', () => {
  const originalDocument = globalThis.document;
  const originalWindow = globalThis.window;
  const originalLocalStorage = globalThis.localStorage;
  const toggleBtn = {
    hidden: true,
    attrs: {},
    classList: { toggle() {} },
    addEventListener() {},
    setAttribute(name, value) {
      this.attrs[name] = value;
    }
  };
  const container = {
    hidden: true,
    innerHTML: '',
    querySelector() { return null; },
    appendChild() {}
  };
  const detailedView = { hidden: false };
  const replaceStateCalls = [];

  try {
    globalThis.document = {
      getElementById(id) {
        if (id === 'workspaceCommandView') return container;
        if (id === 'workspace-detail-view') return detailedView;
        if (id === 'workspace-command-toggle') return toggleBtn;
        return null;
      }
    };
    globalThis.window = {
      location: { search: '', pathname: '/workspaces/abc' },
      history: { replaceState(state, title, url) { replaceStateCalls.push(url); } }
    };
    globalThis.localStorage = { getItem: () => '', setItem() {} };

    const commandView = new WorkspaceCommandView({
      workspace: { name: 'Default Command', mcp_bindings: [], skill_bindings: [] },
      tasks: [],
      notes: [],
      schedules: [],
      sessions: [],
      directories: [],
      buildAgentGroups: () => []
    });

    assert.equal(commandView.active, true);
    assert.equal(container.hidden, false);
    assert.equal(detailedView.hidden, true);
    assert.deepEqual(replaceStateCalls, []);
  } finally {
    globalThis.document = originalDocument;
    globalThis.window = originalWindow;
    globalThis.localStorage = originalLocalStorage;
  }
});

test('activate/deactivate sync the URL by default but bootstrap can opt out', () => {
  const replaceStateCalls = [];
  const originalWindow = globalThis.window;
  globalThis.window = {
    location: { search: '', pathname: '/workspaces/abc' },
    history: {
      replaceState(state, title, url) {
        replaceStateCalls.push(url);
      }
    }
  };
  const persisted = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    active: false,
    container: { hidden: true },
    detailedView: { hidden: false },
    toggleBtn: makeToggleButton(),
    render() {},
    persist(value) {
      persisted.push(value);
    }
  });

  try {
    // Bootstrap path (setup()) passes syncUrl: false — a plain visit must
    // not gain a ?view= param just because a preference is stored.
    commandView.activate({ syncUrl: false });
    assert.deepEqual(replaceStateCalls, []);
    assert.deepEqual(persisted, ['command']);

    commandView.deactivate({ syncUrl: false });
    assert.deepEqual(replaceStateCalls, []);

    // Real user toggles (default options) sync the URL via replaceState.
    commandView.activate();
    assert.deepEqual(replaceStateCalls, ['/workspaces/abc']);

    commandView.deactivate();
    assert.deepEqual(replaceStateCalls, ['/workspaces/abc', '/workspaces/abc?view=detailed']);
  } finally {
    globalThis.window = originalWindow;
  }
});
