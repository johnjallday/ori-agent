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
      workspace: {
        name: 'Demo Workspace',
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
  assert.match(container.innerHTML, /<div class="ws-l">Tasks<\/div>/);
  assert.match(container.innerHTML, /<div class="ws-l">MCP<\/div>/);
  assert.match(container.innerHTML, />Notes<\/h4>/);
  assert.match(container.innerHTML, />Schedules<\/h4>/);
  assert.match(container.innerHTML, />Sessions<\/h4>/);
  assert.match(container.innerHTML, />Linked Folders<\/h4>/);
  assert.match(container.innerHTML, /No notes yet\./);
  assert.match(container.innerHTML, /No schedules yet\./);
  assert.match(container.innerHTML, /No sessions yet\./);
  assert.match(container.innerHTML, /No linked folders yet\./);
  assert.match(container.innerHTML, /★ Entry Agent/);
  assert.match(container.innerHTML, />Model<\/span>/);
  assert.match(container.innerHTML, /Tasks · 1/);
  assert.match(container.innerHTML, /title="Run"/);
  assert.match(container.innerHTML, /Manage Notes in Command view/);
  assert.doesNotMatch(container.innerHTML, /Quest Log|Keeper|Field Unit|Intel|Comms|Supply Lines|Standing Orders|Open Tasks|Tools · MCP|Ops mode|Deploy|✦/);
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
