import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
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
      if (selector.includes('data-cmd-mission-action') && attrs['data-cmd-mission-action']) {
        return {
          getAttribute(name) {
            return attrs[name] || '';
          }
        };
      }
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
  assert.deepEqual(view.computeStats(), { agents: 2, openTasks: 2, mcp: 2, skills: 1, tools: 3 });
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
      files: [],
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
  assert.match(container.innerHTML, /workspace-command-mission-card/);
  assert.match(container.innerHTML, /Mission/);
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
  assert.match(container.innerHTML, /<div class="ws-l">Tools<\/div>/);
  assert.match(container.innerHTML, />Notes<\/h4>/);
  assert.match(container.innerHTML, />Schedules<\/h4>/);
  assert.match(container.innerHTML, />Sessions<\/h4>/);
  assert.match(container.innerHTML, />Linked Folders<\/h4>/);
  assert.match(container.innerHTML, />Files<\/h4>/);
  assert.match(container.innerHTML, />Systems<\/h4>/);
  assert.match(container.innerHTML, /data-cmd-primary-section="notes"/);
  assert.match(container.innerHTML, /data-cmd-primary-section="folders"/);
  assert.match(container.innerHTML, /data-cmd-primary-section="files"/);
  assert.match(container.innerHTML, /data-cmd-primary-section="systems"/);
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

test('command subtitle includes mission automation state when mission state is loaded', () => {
  const originalWindow = globalThis.window;
  globalThis.window = {
    workspaceMission: {
      getSummary: () => ({ label: 'Enabled' })
    }
  };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    identityExpanded: false,
    identityEditMode: '',
    page: {
      workspaceId: 'workspace-1',
      workspace: { name: 'Mission Workspace', description: '', mcp_bindings: [], skill_bindings: [] },
      tasks: [],
      buildAgentGroups: () => []
    }
  });

  try {
    const html = commandView.commandBarHTML(
      commandView.page.workspace,
      commandView.page.workspace.name,
      'Guided',
      commandView.computeStats()
    );

    assert.match(html, /Workflow · Guided · Mission · Enabled/);
    assert.match(html, /id="workspace-command-subtitle"/);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('mission panel renders loaded goal state and findings link', () => {
  const originalWindow = globalThis.window;
  globalThis.window = {
    workspaceMission: {
      getSummary: () => ({
        mission: 'Keep launch readiness current.',
        label: 'Enabled',
        className: 'is-enabled',
        title: 'Current goal',
        text: 'Keep launch readiness current.',
        cadenceLabel: 'Cadence: Daily at 09:00',
        nextLabel: 'Next: in 2h',
        lastLabel: 'Last: 1h ago',
        canRun: true,
        runTitle: 'Run this goal check now',
        findingsHref: '/action-center?workspace=workspace-1',
        findingsLabel: 'Findings (2)',
        actionStatus: ''
      })
    }
  };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page: { workspaceId: 'workspace-1', workspace: { id: 'workspace-1' } }
  });

  try {
    const html = commandView.renderMissionPanel();

    assert.match(html, /Keep launch readiness current\./);
    assert.match(html, /ws-cmd-mission-status is-enabled/);
    assert.match(html, /Cadence: Daily at 09:00/);
    assert.match(html, /href="\/action-center\?workspace=workspace-1"/);
    assert.match(html, />Findings \(2\)<\/a>/);
    assert.doesNotMatch(html, /id="workspace-command-mission-run"[^>]* disabled/);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('mission panel disables Run Now when no saved goal is runnable', () => {
  const originalWindow = globalThis.window;
  globalThis.window = {
    workspaceMission: {
      getSummary: () => ({
        mission: '',
        label: 'Not set',
        className: 'is-empty',
        title: 'No goal set',
        text: 'No workspace goal yet.',
        cadenceLabel: 'Cadence: Manual only',
        nextLabel: 'Next: not scheduled',
        lastLabel: 'Last: never',
        canRun: false,
        runTitle: 'Set a goal before running'
      })
    }
  };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page: { workspaceId: 'workspace-1', workspace: { id: 'workspace-1' } }
  });

  try {
    const html = commandView.renderMissionPanel();

    assert.match(html, /No workspace goal yet\./);
    assert.match(html, /id="workspace-command-mission-run"[^>]* disabled/);
    assert.match(html, /title="Set a goal before running"/);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('mission panel actions delegate to workspace mission APIs', () => {
  const missionRoot = makeListenerRoot();
  const calls = [];
  const originalWindow = globalThis.window;
  globalThis.window = {
    workspaceMission: {
      openGoalModal() { calls.push('edit'); },
      runNow(button) { calls.push(['run', button.getAttribute('data-cmd-mission-action')]); }
    }
  };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    container: {
      querySelector(selector) {
        return selector === '.ws-cmd-mission' ? missionRoot : null;
      }
    }
  });

  try {
    commandView.bindMissionPanel();
    missionRoot.listener({
      target: makeAttributeClickTarget({ 'data-cmd-mission-action': 'edit' })
    });
    missionRoot.listener({
      target: makeAttributeClickTarget({ 'data-cmd-mission-action': 'run' })
    });

    assert.deepEqual(calls, ['edit', ['run', 'run']]);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('detailed view is deleted; shared hosts and goal modal survive in the template', () => {
  const template = readFileSync(new URL('../../../templates/pages/workspace-detail.tmpl', import.meta.url), 'utf8');

  // The Detailed subtree and its toggle are gone.
  assert.equal(template.includes('id="workspace-detail-view"'), false);
  assert.equal(template.includes('id="workspace-command-toggle"'), false);
  assert.equal(template.includes('workspaceDetailPanelBackdrop'), false);

  // The four live-mounted shared hosts remain, inside the hidden container.
  const hostsStart = template.indexOf('id="workspace-detail-shared-hosts"');
  const hostsEnd = template.indexOf('{{template "session-modals.tmpl" .}}');
  assert.ok(hostsStart > -1);
  for (const id of [
    'id="workspace-detail-tools-card"',
    'id="workspace-detail-settings-panel"',
    'id="workspace-detail-tasks-board"',
    'id="workspace-detail-members-panel"'
  ]) {
    const idx = template.indexOf(id);
    assert.ok(idx > hostsStart && idx < hostsEnd, id + ' must live inside the shared-hosts container');
  }

  // Goal modal is still present with its accessibility hooks.
  assert.match(template, /aria-labelledby="workspace-detail-goal-modal-title"/);
  assert.match(template, /id="workspace-detail-goal-modal-form"/);

  // The floating assistant is unconditional now (flag removed).
  assert.match(template, /{{template "support-chat.tmpl" \.}}/);
  assert.equal(template.includes('WorkspaceFloatingAssistantEnabled'), false);
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

test('command bar shows group badge and color accent for group workspaces', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    identityExpanded: false,
    identityEditMode: '',
    page: {
      workspaceId: 'grp-1',
      workspace: { name: 'Alpha Group', kind: 'group', color: '#3b82f6', description: '', mcp_bindings: [], skill_bindings: [] },
      tasks: [],
      buildAgentGroups: () => []
    }
  });

  const html = commandView.commandBarHTML(
    commandView.page.workspace,
    commandView.page.workspace.name,
    'Guided',
    commandView.computeStats()
  );

  assert.match(html, /ws-cmd-topbar is-group/);
  assert.match(html, /ws-cmd-group-badge/);
  assert.match(html, /--ws-group-accent: #3b82f6/);
  assert.match(html, /Detachment · Command/);
});

test('group accent color is sourced from the members-panel group node (detail workspace has none)', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    identityExpanded: false,
    identityEditMode: '',
    page: {
      workspaceId: 'grp-1',
      // The detail workspace object carries no color; the tree node does.
      workspace: { name: 'Alpha Group', kind: 'group', description: '', mcp_bindings: [], skill_bindings: [] },
      membersPanel: { group: { color: '#8b5cf6' } },
      tasks: [],
      buildAgentGroups: () => []
    }
  });

  const html = commandView.commandBarHTML(
    commandView.page.workspace,
    commandView.page.workspace.name,
    'Guided',
    commandView.computeStats()
  );

  assert.match(html, /--ws-group-accent: #8b5cf6/);
});

test('command bar omits group treatment for non-group workspaces', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    identityExpanded: false,
    identityEditMode: '',
    page: {
      workspaceId: 'ws-1',
      workspace: { name: 'Solo Outpost', kind: 'workspace', description: '', mcp_bindings: [], skill_bindings: [] },
      tasks: [],
      buildAgentGroups: () => []
    }
  });

  const html = commandView.commandBarHTML(
    commandView.page.workspace,
    commandView.page.workspace.name,
    'Guided',
    commandView.computeStats()
  );

  assert.doesNotMatch(html, /ws-cmd-group-badge/);
  assert.doesNotMatch(html, /is-group/);
  assert.match(html, /Outpost · Command/);
});

test('detachment rail panel renders only for group workspaces with member count', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    activeRailSection: 'members',
    page: {
      notes: [],
      schedules: [],
      sessions: [],
      directories: [],
      workspace: { name: 'Alpha Group', kind: 'group' },
      membersPanel: { group: { children: [{ id: 'm1' }, { id: 'm2' }] } }
    }
  });

  const html = commandView.renderRail();

  assert.match(html, />Detachment<\/h4>/);
  assert.match(html, /ws-cmd-panel-count">2</);
  assert.match(html, /data-cmd-members-host/);
  assert.match(html, /data-cmd-primary-section="members"/);
});

test('notes panel exposes tag filter host, multi-select toolbar, and per-note checkboxes when managing', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    activeRailSection: 'notes',
    noteFilterBar: null,
    page: {
      workspaceId: 'ws-1',
      notes: [{ id: 'n1', name: 'Alpha' }, { id: 'n2', name: 'Beta' }],
      selectedNoteIds: new Set(['n1'])
    }
  });

  const html = commandView.renderNotesPanel(commandView.page.notes, true);

  assert.match(html, /data-cmd-note-filter/);
  assert.match(html, /data-cmd-note-action="select-all"/);
  assert.match(html, /data-cmd-note-action="copy"/);
  assert.match(html, /data-cmd-note-action="delete"/);
  assert.match(html, /href="\/workspaces\/ws-1\/notes"/);
  assert.match(html, /data-cmd-note-select="n1" checked/);
  assert.match(html, /data-cmd-note-select="n2"/);
  assert.doesNotMatch(html, /data-cmd-note-select="n2" checked/);
});

test('collapsed notes panel stays checkbox-free', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    activeRailSection: '',
    noteFilterBar: null,
    page: { notes: [{ id: 'n1', name: 'Alpha' }] }
  });

  const html = commandView.renderNotesPanel(commandView.page.notes, false);
  assert.doesNotMatch(html, /data-cmd-note-select/);
  assert.doesNotMatch(html, /data-cmd-note-action/);
});

test('handleNoteAction delegates bulk actions to the page', () => {
  const calls = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    render() { calls.push('render'); },
    page: {
      toggleSelectAllNotes: () => calls.push('select-all'),
      copySelectedNotesToClipboard: () => calls.push('copy'),
      deleteSelectedNotes: () => calls.push('delete')
    }
  });

  commandView.handleNoteAction('select-all');
  commandView.handleNoteAction('copy');
  commandView.handleNoteAction('delete');

  assert.deepEqual(calls, ['select-all', 'render', 'copy', 'delete']);
});

test('note and task tag filters reuse OriTagFilterBar.filterItems on active tags', () => {
  const originalWindow = globalThis.window;
  globalThis.window = {
    OriTagFilterBar: {
      filterItems: (items, active) => items.filter(i => (i.tags || []).some(t => active.includes(t)))
    }
  };
  try {
    const commandView = Object.create(WorkspaceCommandView.prototype);
    Object.assign(commandView, {
      noteFilterBar: { getActiveTags: () => ['urgent'] },
      taskFilterBar: { getActiveTags: () => ['urgent'] },
      taskModalShowAll: true,
      page: {
        notes: [{ id: 'n1', tags: ['urgent'] }, { id: 'n2', tags: ['later'] }],
        tasks: [{ id: 't1', tags: ['urgent'] }, { id: 't2', tags: [] }]
      }
    });

    assert.deepEqual(commandView.visibleNotes(commandView.page.notes).map(n => n.id), ['n1']);
    assert.deepEqual(commandView.taskRowData({ includeAll: true }).map(t => t.id), ['t1']);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('tasks modal exposes a List/Board view toggle and renders the board host in board mode', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    taskModalShowAll: false,
    taskModalBoardMode: false,
    page: { tasks: [] }
  });

  const listHtml = commandView.statModalHTML('tasks');
  assert.match(listHtml, /data-cmd-modal-action="view-list"/);
  assert.match(listHtml, /data-cmd-modal-action="view-board"/);
  assert.doesNotMatch(listHtml, /data-cmd-board-host/);

  commandView.taskModalBoardMode = true;
  const boardHtml = commandView.statModalHTML('tasks');
  assert.match(boardHtml, /data-cmd-board-host/);
  // The list-only "Show all" filter is hidden while the board is showing.
  assert.doesNotMatch(boardHtml, /data-cmd-modal-action="toggle-task-filter"/);
});

test('view-board / view-list toggle flips board mode and hands the board node back on exit', () => {
  const calls = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    statModalSection: 'tasks',
    taskModalBoardMode: false,
    page: { setView: (v) => calls.push(['setView', v]) },
    renderStatModalBody() { calls.push(['render']); },
    syncBoardSurface(opts) { calls.push(['sync', opts && opts.load === true]); },
    restoreSharedSurface(key) { calls.push(['restore', key]); }
  });

  commandView.handleStatModalAction('view-board');
  assert.equal(commandView.taskModalBoardMode, true);
  assert.deepEqual(calls, [['render'], ['sync', true]]);

  calls.length = 0;
  commandView.handleStatModalAction('view-list');
  assert.equal(commandView.taskModalBoardMode, false);
  assert.deepEqual(calls, [['restore', 'board'], ['setView', 'list'], ['render']]);
});

test('detachment rail panel is absent for non-group workspaces', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    activeRailSection: '',
    page: {
      notes: [],
      schedules: [],
      sessions: [],
      directories: [],
      workspace: { name: 'Solo', kind: 'workspace' }
    }
  });

  const html = commandView.renderRail();

  assert.doesNotMatch(html, /Detachment/);
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

test('files rail panel lists workspace files and exposes upload, browse, and drop target', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    activeRailSection: 'files',
    page: {
      notes: [],
      schedules: [],
      sessions: [],
      directories: [],
      files: [
        {
          id: 'file-1',
          title: 'Launch Plan.pdf',
          created_at: '2026-07-04T05:00:00Z',
          file_meta: { size: 4096, relative_path: 'docs/Launch Plan.pdf' }
        }
      ],
      workspace: {},
      formatFileSize: size => `${size} B`
    }
  });

  const html = commandView.renderRail();

  assert.match(html, />Files<\/h4>/);
  assert.match(html, /Launch Plan\.pdf/);
  assert.match(html, /4096 B/);
  assert.match(html, /docs\/Launch Plan\.pdf/);
  assert.match(html, /data-cmd-file-drop/);
  assert.match(html, /Browse workspace files/);
  assert.match(html, /data-cmd-primary-section="files"/);
  assert.match(html, /data-cmd-open-section="files" data-cmd-item-id="file-1"/);
});

test('files rail actions delegate to existing file modal and upload paths', async () => {
  const calls = [];
  const files = { length: 1, 0: { name: 'brief.txt' } };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page: {
      showFileModal() { calls.push(['modal']); },
      async uploadFiles(fileList) { calls.push(['upload', fileList]); }
    }
  });

  commandView.runRailPrimaryAction('files');
  await commandView.uploadDroppedFiles({ dataTransfer: { files } });

  assert.deepEqual(calls, [['modal'], ['upload', files]]);
});

test('files drop handler owns Command drop zones without duplicate page fallback', () => {
  const listeners = {};
  const files = { length: 1, 0: { name: 'brief.txt' } };
  const dropZone = { classList: { toggle() {} } };
  let prevented = false;
  let stopped = false;
  const calls = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    container: {
      querySelector(selector) {
        return selector === '.ws-cmd-rail'
          ? { addEventListener(type, listener) { listeners[type] = listener; } }
          : null;
      }
    },
    page: {
      async uploadFiles(fileList) { calls.push(['upload', fileList]); }
    }
  });

  commandView.bindRail();
  listeners.drop({
    target: { closest: selector => selector === '[data-cmd-file-drop]' ? dropZone : null },
    dataTransfer: { files },
    preventDefault() { prevented = true; },
    stopPropagation() { stopped = true; }
  });

  assert.equal(prevented, true);
  assert.equal(stopped, true);
  assert.deepEqual(calls, [['upload', files]]);
});

test('systems rail panel renders Command-native tabs and a shared host', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    activeSystemTab: 'memory'
  });

  const html = commandView.renderSystemsPanel(true);

  assert.match(html, />Systems<\/h4>/);
  // Systems keeps only workspace state/automation now.
  assert.match(html, /data-cmd-system-tab="memory" aria-selected="true"/);
  assert.match(html, /data-cmd-system-tab="triggers"/);
  assert.match(html, /data-cmd-system-host/);
  // Capability providers + Find Tools moved to the header Tools stat-box modal.
  assert.doesNotMatch(html, /data-cmd-system-tab="mcp"/);
  assert.doesNotMatch(html, /data-cmd-system-tab="skills"/);
  assert.doesNotMatch(html, /data-cmd-system-tab="plugins"/);
  assert.doesNotMatch(html, /data-cmd-system-tab="tools"/);
  assert.doesNotMatch(html, /data-cmd-system-tab="settings"/);
  assert.doesNotMatch(html, /data-cmd-system-tab="intent"/);
  assert.doesNotMatch(html, /data-cmd-system-tab="mission"/);
});

test('openSystemTab keeps Command active and selects the requested Systems tab', () => {
  const renders = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    activeRailSection: '',
    activeSystemTab: 'memory',
    render() { renders.push([this.activeRailSection, this.activeSystemTab]); }
  });

  commandView.openSystemTab('triggers');

  assert.equal(commandView.activeRailSection, 'systems');
  assert.equal(commandView.activeSystemTab, 'triggers');
  assert.deepEqual(renders, [['systems', 'triggers']]);
});

test('systems shared surface mounts config without cloning persistence logic', () => {
  const calls = [];
  const host = { innerHTML: '', appendChild() {} };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    active: true,
    activeRailSection: 'systems',
    activeSystemTab: 'memory',
    statModalSection: '',
    statModalEl: null,
    container: { querySelector: selector => selector === '[data-cmd-system-host]' ? host : null },
    mountSharedSurface(key, selector) {
      calls.push(['mount', key, selector]);
      return { key };
    },
    restoreSharedSurface(key) { calls.push(['restore', key]); },
    showConfigTab(tab) { calls.push(['tab', tab.key]); },
    refreshSystemTabData(tab) { calls.push(['refresh', tab.key]); },
    expandMountedConfig(node) { calls.push(['expand', node.key]); }
  });

  commandView.syncSharedSurfaces();

  assert.deepEqual(calls, [
    ['restore', 'tools'],
    ['mount', 'config', '#workspace-detail-settings-panel'],
    ['tab', 'memory'],
    ['refresh', 'memory'],
    ['expand', 'config'],
    ['restore', 'members']
  ]);
});

test('tools modal mounts the config surface for MCP/Skills/Plugins and the tools card for Find Tools', () => {
  const calls = [];
  const host = { innerHTML: '', appendChild() {} };
  const base = {
    statModalSection: 'tools',
    statModalEl: { hidden: false, querySelector: sel => sel === '[data-cmd-tools-host]' ? host : null },
    mountSharedSurface(key, selector) { calls.push(['mount', key, selector]); return { key }; },
    restoreSharedSurface(key) { calls.push(['restore', key]); },
    showConfigTab(tab) { calls.push(['tab', tab.tabId]); },
    refreshConfigData(key) { calls.push(['refresh', key]); },
    expandMountedConfig(node) { calls.push(['expand', node.key]); }
  };

  const plugins = Object.assign(Object.create(WorkspaceCommandView.prototype), base, { activeToolsTab: 'plugins' });
  plugins.syncToolsModalSurface();

  const find = Object.assign(Object.create(WorkspaceCommandView.prototype), base, { activeToolsTab: 'find' });
  calls.length = 0;
  find.syncToolsModalSurface();
  assert.deepEqual(calls, [['restore', 'config'], ['mount', 'tools', '#workspace-detail-tools-card']]);
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

test('mcp manager modal hosts the live config surface with the binding count, no Systems link', () => {
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
  // Mounts the live manager surface instead of a summary list.
  assert.match(html, /data-cmd-config-host/);
  // Binding count still shown in the header.
  assert.match(html, /ws-cmd-modal-count">2</);
  // No "Open Systems" escape hatch and no inline summary rows anymore.
  assert.doesNotMatch(html, /data-cmd-modal-action="detailed"/);
  assert.doesNotMatch(html, /Open Systems/);
  assert.doesNotMatch(html, /data-cmd-modal-action="edit"/);
});

test('skills manager modal hosts the live config surface', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page: {
      getWorkspaceSkillBindings: () => [{ id: 's1', skillName: 'summarize', enabled: true, trusted: true }]
    }
  });

  const html = commandView.statModalHTML('skills');
  assert.match(html, />Skills</);
  assert.match(html, /data-cmd-config-host/);
  assert.match(html, /ws-cmd-modal-count">1</);
  assert.doesNotMatch(html, /Open Systems/);
});

test('manager settings modal hosts the settings surface without a count chip', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  const html = commandView.statModalHTML('settings');
  assert.match(html, /Manager Settings/);
  assert.match(html, /data-cmd-config-host/);
  // Settings/goal/intent carry no list count.
  assert.doesNotMatch(html, /ws-cmd-modal-count/);
  assert.doesNotMatch(html, /Open Systems/);
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

test('agents modal add/edit hand off to page flows and close the modal; delete stays open', () => {
  const calls = [];
  let closed = 0;
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    statModalSection: 'agents',
    closeStatModal() {
      closed += 1;
      this.statModalSection = '';
    },
    page: {
      openAddAgentModal() { calls.push(['add']); },
      openAgentModelModal(id) { calls.push(['edit', id]); },
      removeAgentFromWorkspace(id) { calls.push(['delete', id]); }
    }
  });

  commandView.handleStatModalAction('add');
  commandView.statModalSection = 'agents';
  commandView.handleStatModalAction('edit', 'a1');
  commandView.statModalSection = 'agents';
  commandView.handleStatModalAction('delete', 'a2');

  assert.deepEqual(calls, [['add'], ['edit', 'a1'], ['delete', 'a2']]);
  assert.equal(closed, 2); // add + edit close the modal; delete keeps it open
});

test('mcp/skills CRUD is not routed through handleStatModalAction (lives in the mounted panel)', () => {
  const calls = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    statModalSection: 'mcp',
    closeStatModal() { calls.push(['close']); },
    page: {
      openWorkspaceMCPModal() { calls.push(['mcp-modal']); },
      deleteWorkspaceMCPBinding() { calls.push(['mcp-delete']); }
    }
  });

  // The mounted Workspace MCP manager owns its own controls, so a stray edit/delete
  // routed to the stat modal handler is a no-op (no page flow, no close).
  commandView.handleStatModalAction('edit', 'b1');
  commandView.handleStatModalAction('delete', 'b1');

  assert.deepEqual(calls, []);
});

test('config-surface stat sections no longer emit an Open Systems footer', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, { page: { getWorkspaceMCPBindings: () => [], getWorkspaceSkillBindings: () => [] } });
  for (const section of ['mcp', 'skills', 'settings', 'mission', 'intent']) {
    assert.doesNotMatch(commandView.statModalHTML(section), /Open Systems|data-cmd-modal-action="detailed"/);
  }
  // The removed footer builder is gone entirely.
  assert.equal(typeof commandView.statModalFooterHTML, 'undefined');
});

test('closing a config-surface stat modal restores the shared node and re-syncs the rail', () => {
  const calls = [];
  const node = { classList: { remove(c) { calls.push(['unclass', c]); } } };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    statModalSection: 'settings',
    statModalTrigger: null,
    taskModalBoardMode: false,
    statModalEl: { hidden: false },
    sharedSurfaceAnchors: { config: { node } },
    restoreSharedSurface(key) { calls.push(['restore', key]); },
    syncSystemsSurface() { calls.push(['syncSystems']); },
    setCommandBackgroundInert() {}
  });

  commandView.closeStatModal();

  // Sheds the modal-only chrome class, hands the node home, then re-offers it to the rail.
  assert.deepEqual(calls, [['unclass', 'is-command-modal'], ['restore', 'config'], ['syncSystems']]);
  assert.equal(commandView.statModalEl.hidden, true);
});

test('syncSystemsSurface yields while a config-surface stat modal holds the config node', () => {
  const calls = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    statModalSection: 'mcp',
    statModalEl: { hidden: false },
    container: { querySelector() { calls.push('query'); return null; } },
    restoreSharedSurface(key) { calls.push(['restore', key]); }
  });

  commandView.syncSystemsSurface();

  // Returns before touching the rail host or restoring the shared surface.
  assert.deepEqual(calls, []);
});

test('entry-agent unit card exposes a manager-settings gear; other agents do not', () => {
  const page = {
    isWorkspaceEntryAgent: name => name === 'Atlas',
    getAgentAvatarPresentation: name => ({ initials: name.slice(0, 2), style: '' }),
    getAgentRosterStatus: () => ({ key: 'idle', label: 'Idle' }),
    getAgentProfile: () => ({}),
    getAgentModelPresentation: () => ({ empty: false, model: 'gpt-test' }),
    getAgentSkillSummary: () => ({ count: 1 })
  };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page,
    applyTaskTagFilter: tasks => tasks || [],
    taskFilterActiveTags: () => []
  });

  const keeper = commandView.unitCardHTML({ name: 'Atlas', isWorkspaceAgent: true, isUnassigned: false, tasks: [] });
  const other = commandView.unitCardHTML({ name: 'Builder', isWorkspaceAgent: true, isUnassigned: false, tasks: [] });

  assert.match(keeper, /data-cmd-manager-settings/);
  assert.match(keeper, /ws-cmd-unit is-keeper/);
  assert.doesNotMatch(other, /data-cmd-manager-settings/);
});

test('retireLegacyViewPreference clears the stored pref and strips ?view= deep links', () => {
  const originalWindow = globalThis.window;
  const originalLocalStorage = globalThis.localStorage;
  const removed = [];
  const replaceStateCalls = [];
  try {
    globalThis.localStorage = { removeItem(key) { removed.push(key); } };
    globalThis.window = {
      location: { search: '?view=detailed&tab=notes', pathname: '/workspaces/abc' },
      history: { replaceState(state, title, url) { replaceStateCalls.push(url); } }
    };

    const commandView = Object.create(WorkspaceCommandView.prototype);
    commandView.retireLegacyViewPreference();

    assert.deepEqual(removed, ['oriWorkspaceDetailView']);
    assert.deepEqual(replaceStateCalls, ['/workspaces/abc?tab=notes']);

    // No ?view= param → URL untouched.
    globalThis.window.location.search = '?tab=notes';
    commandView.retireLegacyViewPreference();
    assert.deepEqual(replaceStateCalls, ['/workspaces/abc?tab=notes']);
  } finally {
    globalThis.window = originalWindow;
    globalThis.localStorage = originalLocalStorage;
  }
});

test('setup always activates Command view', () => {
  const originalDocument = globalThis.document;
  const originalWindow = globalThis.window;
  const originalLocalStorage = globalThis.localStorage;
  const container = {
    hidden: true,
    innerHTML: '',
    querySelector() { return null; },
    appendChild() {}
  };

  try {
    globalThis.document = {
      getElementById(id) {
        if (id === 'workspaceCommandView') return container;
        return null;
      },
      addEventListener() {}
    };
    globalThis.window = {
      location: { search: '', pathname: '/workspaces/abc' },
      history: { replaceState() { throw new Error('must not rewrite a clean URL'); } }
    };
    globalThis.localStorage = { removeItem() {} };

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
  } finally {
    globalThis.document = originalDocument;
    globalThis.window = originalWindow;
    globalThis.localStorage = originalLocalStorage;
  }
});
