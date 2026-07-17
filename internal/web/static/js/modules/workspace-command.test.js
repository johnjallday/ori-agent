import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { WorkspaceCommandView } from './workspace-command.js';

// The constructor only reads the DOM (getElementById) and returns early from
// setup() when there's no container — a null-returning stub is enough to build
// an instance whose pure format helpers we can exercise.
globalThis.document = { getElementById: () => null };
globalThis.localStorage = {
  getItem: () => null,
  setItem() {},
  removeItem() {}
};
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
      const matchedAttr = Object.keys(attrs).find(
        attr =>
          selector === '[' + attr + ']' ||
          selector.startsWith('[' + attr + '=') ||
          selector.includes('][' + attr + ']') ||
          selector.includes('][' + attr + '=')
      );
      if (matchedAttr) {
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
  assert.equal(view.statusTone('waiting', ''), 'waiting');
  assert.equal(view.statusTone('needs-input', ''), 'needs-input');
  assert.equal(view.statusTone('completed', ''), 'done');
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
  const withMode = mode => {
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
  assert.doesNotMatch(
    container.innerHTML,
    /Quest Log|Keeper|Field Unit|Intel|Comms|Supply Lines|Standing Orders|Tools · MCP|Ops mode|Deploy|✦/
  );
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
      workspace: {
        name: 'Mission Workspace',
        description: '',
        mcp_bindings: [],
        skill_bindings: []
      },
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

test('project open command is conditional, path-free, and exposes its busy state', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page: {
      workspace: {
        project_path: 'secret-project',
        shared_data: { project_entry_path: 'private/song.rpp' }
      },
      hasProjectEntry: () => false,
      projectOpenBusy: false
    }
  });

  assert.equal(commandView.projectOpenActionHTML(), '');

  commandView.page.hasProjectEntry = () => true;
  const readyHTML = commandView.projectOpenActionHTML();
  assert.match(readyHTML, /data-cmd-open-project/);
  assert.match(readyHTML, />Open Project<\/button>/);
  assert.match(readyHTML, /aria-label="Open project using the system default application"/);
  assert.match(readyHTML, /aria-busy="false"/);
  assert.doesNotMatch(readyHTML, /secret-project|private\/song\.rpp/);

  commandView.page.projectOpenBusy = true;
  const busyHTML = commandView.projectOpenActionHTML();
  assert.match(busyHTML, /aria-busy="true"/);
  assert.match(busyHTML, / disabled/);
  assert.match(busyHTML, />Opening Project\.\.\.<\/button>/);
});

test('command bar delegates Open Project through the workspace detail action layer', () => {
  const topbar = makeListenerRoot();
  let openCount = 0;
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    container: {
      querySelector: selector => (selector === '.ws-cmd-topbar' ? topbar : null)
    },
    page: {
      openProject() {
        openCount += 1;
      }
    }
  });

  commandView.bindIdentityControls();
  topbar.listener({
    target: makeAttributeClickTarget({ 'data-cmd-open-project': 'true' })
  });

  assert.equal(openCount, 1);
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
    assert.match(html, />Edit Goal<\/button>/);
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
    assert.match(html, />Set Goal<\/button>/);
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
      openGoalModal() {
        calls.push('edit');
      },
      runNow(button) {
        calls.push(['run', button.getAttribute('data-cmd-mission-action')]);
      }
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
  const template = readFileSync(
    new URL('../../../templates/pages/workspace-detail.tmpl', import.meta.url),
    'utf8'
  );

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
    assert.ok(
      idx > hostsStart && idx < hostsEnd,
      id + ' must live inside the shared-hosts container'
    );
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
      isProjectDirectory: dir => dir.id === 'dir-project'
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
      workspace: {
        name: 'Alpha Group',
        kind: 'group',
        color: '#3b82f6',
        description: '',
        mcp_bindings: [],
        skill_bindings: []
      },
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
      workspace: {
        name: 'Alpha Group',
        kind: 'group',
        description: '',
        mcp_bindings: [],
        skill_bindings: []
      },
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
      workspace: {
        name: 'Solo Outpost',
        kind: 'workspace',
        description: '',
        mcp_bindings: [],
        skill_bindings: []
      },
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
      notes: [
        { id: 'n1', name: 'Alpha' },
        { id: 'n2', name: 'Beta' }
      ],
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
    render() {
      calls.push('render');
    },
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
      filterItems: (items, active) =>
        items.filter(i => (i.tags || []).some(t => active.includes(t)))
    }
  };
  try {
    const commandView = Object.create(WorkspaceCommandView.prototype);
    Object.assign(commandView, {
      noteFilterBar: { getActiveTags: () => ['urgent'] },
      taskFilterBar: { getActiveTags: () => ['urgent'] },
      taskModalShowAll: true,
      page: {
        notes: [
          { id: 'n1', tags: ['urgent'] },
          { id: 'n2', tags: ['later'] }
        ],
        tasks: [
          { id: 't1', tags: ['urgent'] },
          { id: 't2', tags: [] }
        ]
      }
    });

    assert.deepEqual(
      commandView.visibleNotes(commandView.page.notes).map(n => n.id),
      ['n1']
    );
    assert.deepEqual(
      commandView.taskRowData({ includeAll: true }).map(t => t.id),
      ['t1']
    );
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
    page: { setView: v => calls.push(['setView', v]) },
    renderStatModalBody() {
      calls.push(['render']);
    },
    syncBoardSurface(opts) {
      calls.push(['sync', opts && opts.load === true]);
    },
    restoreSharedSurface(key) {
      calls.push(['restore', key]);
    }
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
      showFileModal() {
        calls.push(['modal']);
      },
      async uploadFiles(fileList) {
        calls.push(['upload', fileList]);
      }
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
          ? {
              addEventListener(type, listener) {
                listeners[type] = listener;
              }
            }
          : null;
      }
    },
    page: {
      async uploadFiles(fileList) {
        calls.push(['upload', fileList]);
      }
    }
  });

  commandView.bindRail();
  listeners.drop({
    target: { closest: selector => (selector === '[data-cmd-file-drop]' ? dropZone : null) },
    dataTransfer: { files },
    preventDefault() {
      prevented = true;
    },
    stopPropagation() {
      stopped = true;
    }
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
    render() {
      renders.push([this.activeRailSection, this.activeSystemTab]);
    }
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
    container: { querySelector: selector => (selector === '[data-cmd-system-host]' ? host : null) },
    mountSharedSurface(key, selector) {
      calls.push(['mount', key, selector]);
      return { key };
    },
    restoreSharedSurface(key) {
      calls.push(['restore', key]);
    },
    showConfigTab(tab) {
      calls.push(['tab', tab.key]);
    },
    refreshSystemTabData(tab) {
      calls.push(['refresh', tab.key]);
    },
    expandMountedConfig(node) {
      calls.push(['expand', node.key]);
    }
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
    statModalEl: {
      hidden: false,
      querySelector: sel => (sel === '[data-cmd-tools-host]' ? host : null)
    },
    mountSharedSurface(key, selector) {
      calls.push(['mount', key, selector]);
      return { key };
    },
    restoreSharedSurface(key) {
      calls.push(['restore', key]);
    },
    showConfigTab(tab) {
      calls.push(['tab', tab.tabId]);
    },
    refreshConfigData(key) {
      calls.push(['refresh', key]);
    },
    expandMountedConfig(node) {
      calls.push(['expand', node.key]);
    }
  };

  const plugins = Object.assign(Object.create(WorkspaceCommandView.prototype), base, {
    activeToolsTab: 'plugins'
  });
  plugins.syncToolsModalSurface();

  const find = Object.assign(Object.create(WorkspaceCommandView.prototype), base, {
    activeToolsTab: 'find'
  });
  calls.length = 0;
  find.syncToolsModalSurface();
  assert.deepEqual(calls, [
    ['restore', 'config'],
    ['mount', 'tools', '#workspace-detail-tools-card']
  ]);
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
      showNoteModal() {
        calls.push('new-note');
      },
      showSchedulesModal() {
        calls.push('open-schedules');
      },
      createNewSession() {
        calls.push('new-session');
      },
      showAddDirectoryModal() {
        calls.push('link-folder');
      }
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
      showNoteModal(note) {
        calls.push(['note', note.id]);
      },
      openSchedule(id) {
        calls.push(['schedule', id]);
      },
      openSession(id) {
        calls.push(['session', id]);
      },
      openDirectoryExplorer(id, source) {
        calls.push(['folder', id, source]);
      }
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
      openTask(id) {
        calls.push(['open', id]);
      },
      executeTask(id) {
        calls.push(['run', id]);
      }
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
    setAttribute(name, value) {
      this.attrs[name] = value;
    },
    removeAttribute(name) {
      delete this.attrs[name];
    }
  };
  const layout = {
    attrs: {},
    inert: false,
    setAttribute(name, value) {
      this.attrs[name] = value;
    },
    removeAttribute(name) {
      delete this.attrs[name];
    }
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

  const first = {
    hidden: false,
    focused: 0,
    focus() {
      this.focused += 1;
    }
  };
  const last = {
    hidden: false,
    focused: 0,
    focus() {
      this.focused += 1;
    }
  };
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
    commandView.trapStatModalFocus({
      key: 'Tab',
      preventDefault() {
        prevented += 1;
      }
    });
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
      getWorkspaceSkillBindings: () => [
        { id: 's1', skillName: 'summarize', enabled: true, trusted: true }
      ]
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
      openAddAgentModal() {
        calls.push(['add']);
      },
      openAgentModelModal(id) {
        calls.push(['edit', id]);
      },
      removeAgentFromWorkspace(id) {
        calls.push(['delete', id]);
      }
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
    closeStatModal() {
      calls.push(['close']);
    },
    page: {
      openWorkspaceMCPModal() {
        calls.push(['mcp-modal']);
      },
      deleteWorkspaceMCPBinding() {
        calls.push(['mcp-delete']);
      }
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
  Object.assign(commandView, {
    page: { getWorkspaceMCPBindings: () => [], getWorkspaceSkillBindings: () => [] }
  });
  for (const section of ['mcp', 'skills', 'settings', 'mission', 'intent']) {
    assert.doesNotMatch(
      commandView.statModalHTML(section),
      /Open Systems|data-cmd-modal-action="detailed"/
    );
  }
  // The removed footer builder is gone entirely.
  assert.equal(typeof commandView.statModalFooterHTML, 'undefined');
});

test('closing a config-surface stat modal restores the shared node and re-syncs the rail', () => {
  const calls = [];
  const node = {
    classList: {
      remove(c) {
        calls.push(['unclass', c]);
      }
    }
  };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    statModalSection: 'settings',
    statModalTrigger: null,
    taskModalBoardMode: false,
    statModalEl: { hidden: false },
    sharedSurfaceAnchors: { config: { node } },
    restoreSharedSurface(key) {
      calls.push(['restore', key]);
    },
    syncSystemsSurface() {
      calls.push(['syncSystems']);
    },
    setCommandBackgroundInert() {}
  });

  commandView.closeStatModal();

  // Sheds the modal-only chrome class, hands the node home, then re-offers it to the rail.
  assert.deepEqual(calls, [
    ['unclass', 'is-command-modal'],
    ['restore', 'config'],
    ['syncSystems']
  ]);
  assert.equal(commandView.statModalEl.hidden, true);
});

test('syncSystemsSurface yields while a config-surface stat modal holds the config node', () => {
  const calls = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    statModalSection: 'mcp',
    statModalEl: { hidden: false },
    container: {
      querySelector() {
        calls.push('query');
        return null;
      }
    },
    restoreSharedSurface(key) {
      calls.push(['restore', key]);
    }
  });

  commandView.syncSystemsSurface();

  // Returns before touching the rail host or restoring the shared surface.
  assert.deepEqual(calls, []);
});

test('entry-agent stage exposes manager settings while other agents expose removal', () => {
  const page = {
    isWorkspaceEntryAgent: name => name === 'Atlas',
    getAgentRosterStatus: () => ({ key: 'idle', label: 'Idle' }),
    getAgentProfile: () => ({}),
    getAgentModelPresentation: () => ({ empty: false, model: 'gpt-test' }),
    getAgentSkillSummary: () => ({ count: 1 }),
    getEffectiveWorkspaceMCPServerNames: () => []
  };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page
  });

  const keeperAgent = commandView.agentViewModel({
    key: 'atlas',
    name: 'Atlas',
    isWorkspaceAgent: true,
    isUnassigned: false,
    tasks: []
  });
  const otherAgent = commandView.agentViewModel({
    key: 'builder',
    name: 'Builder',
    isWorkspaceAgent: true,
    isUnassigned: false,
    tasks: []
  });
  const keeper = commandView.agentStageHTML(keeperAgent);
  const other = commandView.agentStageHTML(otherAgent);

  assert.match(keeper, /data-cmd-manager-settings/);
  assert.match(keeper, /Entry Agent/);
  assert.doesNotMatch(keeper, /data-cmd-remove-agent/);
  assert.doesNotMatch(other, /data-cmd-manager-settings/);
  assert.match(other, /data-cmd-remove-agent/);
});

test('command deck defaults to the entry agent and groups same-name instances', () => {
  const groups = [
    {
      key: 'builder',
      name: 'Builder',
      isWorkspaceAgent: true,
      isUnassigned: false,
      instanceCount: 2,
      roles: ['Writer', 'Reviewer'],
      tasks: []
    },
    {
      key: 'atlas',
      name: 'Atlas',
      isWorkspaceAgent: true,
      isUnassigned: false,
      instanceCount: 1,
      roles: ['Coordinator'],
      tasks: []
    }
  ];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    selectedAgentKey: '',
    agentSelectionInitialized: false,
    activeAgentTab: 'overview',
    page: {
      workspaceId: 'workspace-1',
      workspace: { id: 'workspace-1' },
      buildAgentGroups: () => groups,
      isWorkspaceEntryAgent: name => name === 'Atlas',
      getAgentRosterStatus: () => ({ key: 'idle', label: 'Idle', detail: 'No active tasks' }),
      getAgentGroupRolePresentation: group => ({
        label: group.roles.length > 1 ? 'Multiple roles' : group.roles[0],
        detail: group.roles.join(', '),
        roles: group.roles
      }),
      getAgentSkillSummary: () => ({ count: 0, names: [] }),
      getEffectiveWorkspaceMCPServerNames: () => [],
      getAgentProfile: () => ({}),
      getAgentModelPresentation: () => ({ empty: true, label: 'Model not set' })
    },
    applyTaskTagFilter: tasks => tasks || [],
    taskFilterActiveTags: () => []
  });

  const html = commandView.renderGarrison();

  assert.equal(commandView.selectedAgentKey, 'atlas');
  assert.match(html, /data-cmd-select-agent="Atlas"/);
  assert.match(html, /data-cmd-select-agent="Builder"/);
  assert.match(html, /aria-label="2 instances">2×/);
  assert.match(html, /ws-cmd-agent-stage idle/);
  assert.match(html, /role="tab"/);
  assert.doesNotMatch(html, /ws-cmd-unit/);
});

test('persisted command-deck selection wins when the agent still exists and falls back after removal', () => {
  const originalLocalStorage = globalThis.localStorage;
  const saved = new Map([['ori-workspace-command-agent:workspace-1', 'builder']]);
  globalThis.localStorage = {
    getItem: key => saved.get(key) || null,
    setItem: (key, value) => saved.set(key, value)
  };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    selectedAgentKey: '',
    agentSelectionInitialized: false,
    page: {
      workspaceId: 'workspace-1',
      isWorkspaceEntryAgent: name => name === 'Atlas'
    }
  });
  const atlas = { key: 'atlas', name: 'Atlas' };
  const builder = { key: 'builder', name: 'Builder' };

  try {
    assert.equal(commandView.reconcileAgentSelection([atlas, builder]), 'builder');
    assert.equal(commandView.reconcileAgentSelection([atlas]), 'atlas');
    assert.equal(saved.get('ori-workspace-command-agent:workspace-1'), 'atlas');
  } finally {
    globalThis.localStorage = originalLocalStorage;
  }
});

test('geometric agent character is deterministic and does not inject agent markup', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  const agent = { key: '<img src=x>', name: '<img src=x>', tone: 'working' };
  const first = commandView.agentCharacterHTML(agent, 'stage');
  const second = commandView.agentCharacterHTML(agent, 'stage');

  assert.equal(first, second);
  assert.match(first, /<svg viewBox="0 0 100 118"/);
  assert.match(first, /ws-cmd-character is-stage working/);
  assert.doesNotMatch(first, /<img/);
});

test('recent activity uses attributable persisted timestamps and orders newest first', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page: {
      tasks: [
        {
          id: 'old',
          to: 'Atlas',
          description: 'Old task',
          status: 'pending',
          updated_at: '2026-07-01T10:00:00Z'
        },
        {
          id: 'new',
          to: 'Atlas',
          description: 'New task',
          status: 'completed',
          updated_at: '2026-07-03T10:00:00Z'
        },
        { id: 'invalid', to: 'Atlas', description: 'Invalid task', updated_at: 'not-a-date' },
        {
          id: 'other',
          to: 'Builder',
          description: 'Other task',
          updated_at: '2026-07-04T10:00:00Z'
        }
      ],
      sessions: [
        {
          id: 'session-1',
          agent_name: 'Atlas',
          title: 'Planning session',
          created_at: '2026-07-02T10:00:00Z'
        }
      ]
    }
  });

  const items = commandView.recentActivityItems({ key: 'atlas', tasks: [] });

  assert.deepEqual(
    items.map(item => item.id),
    ['new', 'session-1', 'old']
  );
  assert.deepEqual(
    items.map(item => item.kind),
    ['Task', 'Session', 'Task']
  );
});

test('effective prompt hydration is Loadout-only and retries with force', async () => {
  const calls = [];
  const group = { key: 'atlas', name: 'Atlas', isWorkspaceAgent: true, tasks: [] };
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    active: true,
    activeAgentTab: 'overview',
    selectedAgentKey: 'atlas',
    agentPromptLoadingKey: '',
    page: {
      buildAgentGroups: () => [group],
      ensureAgentPromptData: async (name, options) => {
        calls.push([name, options]);
        return { effective_prompt: 'Prompt' };
      },
      getAgentRosterStatus: () => ({ key: 'idle', label: 'Idle' }),
      getAgentSkillSummary: () => ({ count: 0, names: [] }),
      getEffectiveWorkspaceMCPServerNames: () => [],
      getAgentProfile: () => ({}),
      getAgentModelPresentation: () => ({ empty: true })
    },
    render() {}
  });

  commandView.hydrateActiveAgentPrompt();
  assert.deepEqual(calls, []);

  commandView.activeAgentTab = 'loadout';
  commandView.hydrateActiveAgentPrompt({ force: true });
  await new Promise(resolve => setTimeout(resolve, 0));

  assert.deepEqual(calls, [['Atlas', { force: true }]]);
});

test('agent tabs support arrow, Home, and End keyboard navigation', () => {
  const calls = [];
  const tabs = ['overview', 'tasks', 'loadout', 'recent'].map(key => ({
    key,
    closest: selector => (selector === '[data-cmd-agent-tab]' ? null : null),
    getAttribute: name => (name === 'data-cmd-agent-tab' ? key : '')
  }));
  tabs.forEach(tab => {
    tab.closest = selector => (selector === '[data-cmd-agent-tab]' ? tab : null);
  });
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    container: {
      querySelector: selector =>
        selector === '.ws-cmd-agent-tabs' ? { querySelectorAll: () => tabs } : null
    },
    setActiveAgentTab(key) {
      calls.push(key);
    }
  });
  const event = (key, target) => ({
    key,
    target,
    preventDefault() {
      calls.push('prevented');
    }
  });

  commandView.handleAgentTabKeydown(event('ArrowRight', tabs[0]));
  commandView.handleAgentTabKeydown(event('Home', tabs[2]));
  commandView.handleAgentTabKeydown(event('End', tabs[1]));

  assert.deepEqual(calls, ['prevented', 'tasks', 'prevented', 'overview', 'prevented', 'recent']);
});

test('retireLegacyViewPreference clears the stored pref and strips ?view= deep links', () => {
  const originalWindow = globalThis.window;
  const originalLocalStorage = globalThis.localStorage;
  const removed = [];
  const replaceStateCalls = [];
  try {
    globalThis.localStorage = {
      removeItem(key) {
        removed.push(key);
      }
    };
    globalThis.window = {
      location: { search: '?view=detailed&tab=notes', pathname: '/workspaces/abc' },
      history: {
        replaceState(state, title, url) {
          replaceStateCalls.push(url);
        }
      }
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
    querySelector() {
      return null;
    },
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
      history: {
        replaceState() {
          throw new Error('must not rewrite a clean URL');
        }
      }
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

test('command view mode preference defaults to details and persists map globally', () => {
  const originalLocalStorage = globalThis.localStorage;
  const store = new Map();
  try {
    globalThis.localStorage = {
      getItem(key) {
        return store.has(key) ? store.get(key) : null;
      },
      setItem(key, value) {
        store.set(key, String(value));
      }
    };
    const commandView = Object.create(WorkspaceCommandView.prototype);

    assert.equal(commandView.readCommandViewModePreference(), 'details');
    commandView.persistCommandViewMode('map');
    assert.equal(store.get('oriWorkspaceCommandViewMode'), 'map');
    assert.equal(commandView.readCommandViewModePreference(), 'map');

    commandView.persistCommandViewMode('bad-value');
    assert.equal(commandView.readCommandViewModePreference(), 'details');
  } finally {
    globalThis.localStorage = originalLocalStorage;
  }
});

test('Operations Map agent status prioritizes attention states before working', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  const agent = {
    status: { key: 'idle', label: 'Idle', detail: 'No active tasks' },
    tasks: [
      { id: 'task-1', status: 'in_progress' },
      { id: 'task-2', status: 'blocked' }
    ]
  };

  assert.equal(commandView.mapAgentStatus(agent).key, 'blocked');

  agent.tasks = [
    { id: 'task-1', status: 'in_progress' },
    { id: 'task-2', status: 'waiting_for_choice' }
  ];
  assert.equal(commandView.mapAgentStatus(agent).key, 'needs-input');

  agent.tasks = [{ id: 'task-1', status: 'in_progress' }];
  assert.equal(commandView.mapAgentStatus(agent).key, 'working');

  agent.tasks = [{ id: 'task-1', status: 'pending' }];
  assert.equal(commandView.mapAgentStatus(agent).key, 'waiting');
});

test('Operations Map inventory derives counts from existing page data', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page: {
      notes: [{ id: 'n1', title: 'Note' }],
      schedules: [{ id: 'sch1', name: 'Daily' }],
      sessions: [{ id: 's1', title: 'Session' }],
      directories: [{ id: 'd1', name: 'Project', path: '/tmp/project' }],
      files: [{ id: 'f1', title: 'Brief.md' }]
    },
    activeSystemTab: 'memory'
  });

  const counts = Object.fromEntries(
    commandView.mapInventoryGroups().map(group => [group.key, group.count])
  );

  assert.equal(counts.notes, 1);
  assert.equal(counts.schedules, 1);
  assert.equal(counts.sessions, 1);
  assert.equal(counts.folders, 1);
  assert.equal(counts.files, 1);
  assert.equal(counts.systems, 2);
});

test('Operations Map controls expose accessible pressed and dialog state', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    viewMode: 'map',
    page: { notes: [], schedules: [], sessions: [], directories: [], files: [] },
    activeMapWindow: 'inventory',
    mapInventorySection: 'notes',
    activeSystemTab: 'memory'
  });

  const switcher = commandView.commandViewSwitchHTML();
  assert.match(switcher, /role="group" aria-label="Workspace view"/);
  assert.match(switcher, /data-cmd-view-mode="map" aria-pressed="true"/);

  const tray = commandView.renderMapToolTray();
  assert.match(tray, /data-cmd-map-window="inventory" aria-label="Inventory" aria-pressed="true"/);

  const windowHTML = commandView.renderMapWindow(null);
  assert.match(windowHTML, /role="dialog" aria-modal="true" aria-label="Inventory"/);
  assert.match(windowHTML, /ws-cmd-map-inventory-grid/);
  assert.match(windowHTML, /ws-cmd-map-inventory-slot is-empty/);
  assert.match(windowHTML, /ws-cmd-map-slot-type">Note/);
  assert.match(windowHTML, /Open Slot/);
});

test('Operations Map agent command menu delegates to existing workspace flows', () => {
  const mapRoot = makeListenerRoot();
  const calls = [];
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    container: {
      querySelector(selector) {
        return selector === '.ws-cmd-map-shell' ? mapRoot : null;
      }
    },
    page: {
      showAddTaskModalForAgent(encodedName) {
        calls.push(['add-task', encodedName]);
      },
      createNewSessionForAgent(encodedName) {
        calls.push(['new-session', encodedName]);
      },
      openSession(id) {
        calls.push(['open-session', id]);
      },
      openTask(id) {
        calls.push(['open-task', id]);
      }
    },
    selectAgent(encodedName, options) {
      calls.push(['select-agent', encodedName, options.focus]);
    },
    setCommandViewMode(mode, options) {
      calls.push(['view-mode', mode, options.focus]);
    },
    setActiveAgentTab(tab) {
      calls.push(['agent-tab', tab]);
    }
  });

  commandView.bindOperationsMap();

  mapRoot.listener({
    target: makeAttributeClickTarget({ 'data-cmd-add-task': 'Researcher' })
  });
  mapRoot.listener({
    target: makeAttributeClickTarget({ 'data-cmd-map-new-session': 'Researcher' })
  });
  mapRoot.listener({
    target: makeAttributeClickTarget({ 'data-cmd-open-session': 'session-1' })
  });
  mapRoot.listener({
    target: makeAttributeClickTarget({ 'data-cmd-open-task': 'task-1' })
  });
  mapRoot.listener({
    target: makeAttributeClickTarget({
      'data-cmd-map-agent-tab': 'loadout',
      'data-cmd-agent-name': 'Researcher'
    })
  });

  assert.deepEqual(calls, [
    ['add-task', 'Researcher'],
    ['new-session', 'Researcher'],
    ['open-session', 'session-1'],
    ['open-task', 'task-1'],
    ['select-agent', 'Researcher', false],
    ['view-mode', 'details', false],
    ['agent-tab', 'loadout']
  ]);
});

test('Operations Map renders units first and keeps support panels hidden by default', () => {
  const originalLocalStorage = globalThis.localStorage;
  try {
    globalThis.localStorage = { getItem: () => null, setItem() {} };
    const commandView = Object.create(WorkspaceCommandView.prototype);
    Object.assign(commandView, {
      page: {
        workspace: { id: 'ws-1', name: 'Research', mcp_bindings: [], skill_bindings: [] },
        buildAgentGroups() {
          return [
            {
              key: 'researcher',
              name: 'Researcher',
              isWorkspaceAgent: true,
              instanceCount: 1,
              roles: ['Agent'],
              tasks: [
                {
                  id: 'task-1',
                  status: 'in_progress',
                  description: 'Collect sources'
                }
              ]
            }
          ];
        },
        getAgentRosterStatus() {
          return { key: 'working', label: 'Working', detail: 'Task in progress' };
        },
        isWorkspaceEntryAgent(name) {
          return name === 'Researcher';
        },
        getAgentSkillSummary() {
          return { count: 1, names: ['workspace-planning'] };
        },
        getEffectiveWorkspaceMCPServerNames() {
          return ['filesystem'];
        },
        getAgentWorkspaceSkillLoadout() {
          return [{ bindingId: 'sk-1', name: 'workspace-planning', enabled: true, locked: false }];
        },
        getAgentWorkspaceMCPLoadout() {
          return [{ bindingId: 'mcp-1', name: 'filesystem', enabled: true, locked: false }];
        },
        getAgentModelPresentation() {
          return { model: '', label: 'Model not set', empty: true };
        },
        tasks: [{ id: 'task-1', status: 'in_progress', description: 'Collect sources' }],
        sessions: [
          {
            id: 'session-1',
            title: 'Research chat',
            agent_name: 'Researcher',
            updated_at: '2026-07-09T12:00:00Z'
          }
        ]
      },
      selectedAgentKey: '',
      agentSelectionInitialized: true,
      activeMapWindow: '',
      mapInventorySection: '',
      activeSystemTab: 'memory'
    });

    const html = commandView.renderOperationsMap();

    assert.match(html, /ws-cmd-map-shell/);
    assert.match(html, /data-map-zone="agents"/);
    assert.match(html, /data-cmd-map-window="inventory"/);
    assert.doesNotMatch(html, /ws-cmd-map-stations/);
    assert.doesNotMatch(html, /ws-cmd-map-station-node/);
    assert.match(html, /Researcher/);
    // The sole entry agent renders as the command node (group 5), not the small
    // entry star badge (which is reserved for a specialist card, never shown
    // here since there is no specialist in this fixture).
    assert.match(html, /is-command-node/);
    assert.match(html, /ws-cmd-map-command-role/);
    assert.match(html, /Entry Agent/);
    assert.doesNotMatch(html, /data-map-zone="mission"/);
    assert.doesNotMatch(html, /data-map-zone="tasks"/);
    assert.doesNotMatch(html, /data-map-zone="tools"/);
    assert.doesNotMatch(html, /role="dialog"/);

    commandView.activeMapWindow = 'objectives';
    const windowHTML = commandView.renderOperationsMap();
    // 'objectives' is the panel KEY (unchanged, FR5a); its user-visible label
    // is now "Tasks" (group 8 terminology sweep, FR4).
    assert.match(windowHTML, /role="dialog" aria-modal="true" aria-label="Tasks"/);
    assert.match(windowHTML, /Collect sources/);

    commandView.activeMapWindow = 'inspector';
    const agentSheetHTML = commandView.renderOperationsMap();
    assert.match(agentSheetHTML, /Unit Sheet/);
    assert.match(agentSheetHTML, /Class/);
    assert.match(agentSheetHTML, /Loadout/);
    assert.match(agentSheetHTML, /workspace-planning/);
    assert.match(agentSheetHTML, /filesystem/);
    assert.match(agentSheetHTML, /ws-cmd-rpg-stat-grid/);
    assert.match(agentSheetHTML, /Current Quest/);
    assert.match(agentSheetHTML, /Command Menu/);
    assert.match(agentSheetHTML, /Track Quest/);
    assert.match(agentSheetHTML, /Continue Session/);
    assert.match(agentSheetHTML, /Configure Loadout/);
  } finally {
    globalThis.localStorage = originalLocalStorage;
  }
});

function makeQuestComposerView(overrides = {}) {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(
    commandView,
    {
      taskComposerOpen: false,
      taskComposerDraft: '',
      taskComposerError: '',
      taskComposerSubmitting: false,
      renderCalls: 0,
      render() {
        this.renderCalls += 1;
      }
    },
    overrides
  );
  return commandView;
}

test('operations map shows a New Quest button and reveals the composer when open', () => {
  const closed = makeQuestComposerView();
  const closedHTML = closed.renderMapQuickTask();
  assert.match(closedHTML, /data-cmd-map-quest-toggle/);
  assert.match(closedHTML, /New Quest/);
  assert.match(closedHTML, /aria-expanded="false"/);
  assert.doesNotMatch(closedHTML, /data-cmd-map-quest-input/);

  const open = makeQuestComposerView({ taskComposerOpen: true, taskComposerDraft: 'Draft copy edits' });
  const openHTML = open.renderMapQuickTask();
  assert.match(openHTML, /aria-expanded="true"/);
  assert.match(openHTML, /data-cmd-map-quest-input/);
  assert.match(openHTML, /Draft copy edits/);
  assert.match(openHTML, /data-cmd-map-quest-create/);
  assert.match(openHTML, /data-cmd-map-quest-start/);
});

test('quest composer Create posts with no assignee so the entry-agent default applies', async () => {
  const originalWindow = globalThis.window;
  const toasts = [];
  globalThis.window = { Toast: { success: msg => toasts.push(msg) } };
  try {
    const createArgs = [];
    const view = makeQuestComposerView({
      taskComposerOpen: true,
      taskComposerDraft: 'Summarize the weekly report',
      page: {
        async createTask(name, description, columnId, options) {
          createArgs.push([name, description, columnId, options]);
          return { id: 't-42', to: 'Atlas' };
        }
      }
    });

    await view.submitTaskComposer({ start: false });

    assert.equal(createArgs.length, 1);
    const [name, , columnId, options] = createArgs[0];
    assert.equal(name, 'Summarize the weekly report');
    assert.equal(columnId, '');
    assert.equal(options.successToast, false);
    assert.ok(!('to' in options) && !('assignee' in options), 'no assignee is sent');
    assert.deepEqual(toasts, ['Quest assigned to Atlas']);
    assert.equal(view.taskComposerOpen, false);
    assert.equal(view.taskComposerDraft, '');
  } finally {
    globalThis.window = originalWindow;
  }
});

test('quest composer Create & Start executes the created task and names the assignee', async () => {
  const originalWindow = globalThis.window;
  const toasts = [];
  globalThis.window = { Toast: { success: msg => toasts.push(msg) } };
  try {
    const executed = [];
    const view = makeQuestComposerView({
      taskComposerOpen: true,
      taskComposerDraft: 'Kick off ingest',
      page: {
        async createTask() {
          return { id: 't-9', to: 'Scribe' };
        },
        async executeTask(id, options) {
          executed.push([id, options]);
        }
      }
    });

    await view.submitTaskComposer({ start: true });

    assert.deepEqual(executed, [['t-9', { skipConfirm: true }]]);
    assert.deepEqual(toasts, ['Quest started · assigned to Scribe']);
    assert.equal(view.taskComposerOpen, false);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('quest composer keeps the draft and shows an error when creation fails', async () => {
  const originalWindow = globalThis.window;
  globalThis.window = { Toast: { success() {} } };
  try {
    const view = makeQuestComposerView({
      taskComposerOpen: true,
      taskComposerDraft: 'Retryable quest',
      page: {
        async createTask() {
          return null;
        }
      }
    });

    await view.submitTaskComposer({ start: false });

    assert.equal(view.taskComposerOpen, true);
    assert.equal(view.taskComposerDraft, 'Retryable quest');
    assert.equal(view.taskComposerSubmitting, false);
    assert.match(view.taskComposerError, /could not create/i);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('quest composer rejects an empty draft without calling the page', async () => {
  let called = false;
  const view = makeQuestComposerView({
    taskComposerOpen: true,
    taskComposerDraft: '   ',
    page: {
      async createTask() {
        called = true;
        return { id: 'x' };
      }
    }
  });

  await view.submitTaskComposer({ start: false });

  assert.equal(called, false);
  assert.match(view.taskComposerError, /enter a quest/i);
});

function makeLoadoutView(overrides = {}) {
  const view = Object.create(WorkspaceCommandView.prototype);
  Object.assign(
    view,
    {
      loadoutAddOpen: '',
      loadoutAddOptions: [],
      loadoutAddLoading: false,
      loadoutBusyKey: '',
      loadoutError: '',
      renderCalls: 0,
      render() {
        this.renderCalls += 1;
      }
    },
    overrides
  );
  return view;
}

test('loadout editor renders interactive toggles, locked chips, and add buttons', () => {
  const view = makeLoadoutView({
    page: {
      getAgentWorkspaceSkillLoadout() {
        return [
          { bindingId: 'sk-1', name: 'planner', enabled: true, locked: false },
          { bindingId: 'sk-2', name: 'writer', enabled: false, locked: false }
        ];
      },
      getAgentWorkspaceMCPLoadout() {
        return [{ bindingId: 'fs', name: 'filesystem', enabled: true, locked: true }];
      }
    }
  });

  const html = view.renderLoadoutEditor({ name: 'Atlas', encodedName: 'Atlas' });
  assert.match(html, /data-cmd-loadout-toggle="skill" data-cmd-loadout-binding="sk-1"/);
  assert.match(html, /aria-checked="true"[^>]*data-cmd-loadout-binding="sk-1"|data-cmd-loadout-binding="sk-1"[^>]*aria-checked="true"/);
  assert.match(html, /data-cmd-loadout-binding="sk-2"/);
  assert.match(html, /ws-cmd-loadout-chip is-locked/);
  assert.doesNotMatch(html, /data-cmd-loadout-toggle="mcp" data-cmd-loadout-binding="fs"/);
  assert.match(html, /data-cmd-loadout-add="skill"/);
  assert.match(html, /data-cmd-loadout-add="mcp"/);
});

test('toggling a loadout chip delegates to the page with decoded agent and inverted state', async () => {
  const calls = [];
  const view = makeLoadoutView({
    page: {
      async setAgentWorkspaceCapabilityEnabled(kind, agentName, bindingId, enabled) {
        calls.push([kind, agentName, bindingId, enabled]);
        return true;
      }
    }
  });

  await view.toggleLoadoutBinding('skill', encodeURIComponent('Atlas Prime'), 'sk-9', false);

  assert.deepEqual(calls, [['skill', 'Atlas Prime', 'sk-9', false]]);
  assert.equal(view.loadoutBusyKey, '');
});

test('handleLoadoutClick routes a toggle target to toggleLoadoutBinding', () => {
  const args = [];
  const view = makeLoadoutView();
  view.toggleLoadoutBinding = (...a) => args.push(a);
  const target = {
    closest(selector) {
      if (selector === '[data-cmd-loadout-toggle]') {
        return {
          getAttribute(name) {
            return {
              'data-cmd-loadout-toggle': 'mcp',
              'data-cmd-loadout-agent': 'Atlas',
              'data-cmd-loadout-binding': 'mcp-3',
              'aria-checked': 'false'
            }[name];
          }
        };
      }
      return null;
    }
  };

  const handled = view.handleLoadoutClick({ target });

  assert.equal(handled, true);
  assert.deepEqual(args, [['mcp', 'Atlas', 'mcp-3', true]]);
});

test('opening a loadout picker loads registry additions; reopening the same kind closes it', async () => {
  const view = makeLoadoutView({
    page: {
      async listAgentLoadoutAdditions(kind) {
        return kind === 'skill' ? ['reviewer', 'summarizer'] : [];
      }
    }
  });

  await view.openLoadoutPicker('skill', 'Atlas');
  assert.equal(view.loadoutAddOpen, 'skill');
  assert.deepEqual(view.loadoutAddOptions, ['reviewer', 'summarizer']);
  assert.equal(view.loadoutAddLoading, false);

  await view.openLoadoutPicker('skill', 'Atlas');
  assert.equal(view.loadoutAddOpen, '');
});

test('binding a loadout capability delegates to the page and closes the picker on success', async () => {
  const originalWindow = globalThis.window;
  const toasts = [];
  globalThis.window = { Toast: { success: m => toasts.push(m), error() {} } };
  try {
    const calls = [];
    const view = makeLoadoutView({
      loadoutAddOpen: 'mcp',
      loadoutAddOptions: ['filesystem'],
      page: {
        async addAgentWorkspaceCapability(kind, agentName, name) {
          calls.push([kind, agentName, name]);
          return true;
        }
      }
    });

    await view.bindLoadoutCapability('mcp', encodeURIComponent('Atlas'), 'filesystem');

    assert.deepEqual(calls, [['mcp', 'Atlas', 'filesystem']]);
    assert.equal(view.loadoutAddOpen, '');
    assert.deepEqual(toasts, ['Tool "filesystem" added']);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('a failed loadout binding surfaces an error and preserves the picker', async () => {
  const originalWindow = globalThis.window;
  globalThis.window = { Toast: { success() {}, error() {} } };
  try {
    const view = makeLoadoutView({
      loadoutAddOpen: 'skill',
      page: {
        async addAgentWorkspaceCapability() {
          throw new Error('registry offline');
        }
      }
    });

    await view.bindLoadoutCapability('skill', 'Atlas', 'reviewer');

    assert.equal(view.loadoutAddOpen, 'skill');
    assert.match(view.loadoutError, /could not add reviewer/i);
    assert.match(view.loadoutError, /registry offline/);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('objectives panel adds a Start action to pending quest rows only', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  view.openMapTasks = () => [
    { id: 't-pending', status: 'pending', description: 'Draft report' },
    { id: 't-running', status: 'in_progress', description: 'Compile data' }
  ];

  const html = view.renderMapTasksPanel();

  assert.match(html, /data-cmd-map-start-task="t-pending"/);
  assert.match(html, /ws-cmd-map-task-row-wrap/);
  assert.doesNotMatch(html, /data-cmd-map-start-task="t-running"/);
  assert.match(html, /data-cmd-open-task="t-pending"/);
  assert.match(html, /data-cmd-open-task="t-running"/);
});

test('agent command menu offers Start Quest for a pending priority task', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  view.priorityTaskForAgent = () => ({ id: 'q1', status: 'pending', description: 'Draft' });
  view.latestAgentSession = () => null;
  const agent = {
    skills: { count: 0 },
    mcpNames: [],
    encodedName: 'Atlas',
    status: { label: 'Idle' }
  };

  const html = view.renderMapAgentCommandMenu(agent, null);

  assert.match(html, /Start Quest/);
  assert.match(html, /data-cmd-map-start-task="q1"/);
  assert.doesNotMatch(html, /data-cmd-open-task="q1"/);
});

test('agent command menu keeps Track Quest (open) for an in-progress task', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  view.priorityTaskForAgent = () => ({ id: 'q2', status: 'in_progress', description: 'Run' });
  view.latestAgentSession = () => null;
  const agent = { skills: { count: 0 }, mcpNames: [], encodedName: 'Atlas', status: { label: 'Working' } };

  const html = view.renderMapAgentCommandMenu(agent, null);

  assert.match(html, /data-cmd-open-task="q2"/);
  assert.doesNotMatch(html, /data-cmd-map-start-task="q2"/);
});

test('startMapQuest executes a pending task via the page with skipConfirm', async () => {
  const calls = [];
  const view = Object.create(WorkspaceCommandView.prototype);
  view.page = {
    tasks: [{ id: 'q1', status: 'pending' }],
    async executeTask(id, options) {
      calls.push([id, options]);
    }
  };

  await view.startMapQuest('q1');

  assert.deepEqual(calls, [['q1', { skipConfirm: true, skipModal: true }]]);
});

test('startMapQuest reports and refreshes when the quest is no longer pending', async () => {
  const originalWindow = globalThis.window;
  const infos = [];
  globalThis.window = { Toast: { info: m => infos.push(m) } };
  try {
    let executed = false;
    let refreshed = false;
    const view = Object.create(WorkspaceCommandView.prototype);
    view.page = {
      tasks: [{ id: 'q1', status: 'in_progress' }],
      async executeTask() {
        executed = true;
      },
      async loadTasks() {
        refreshed = true;
      }
    };

    await view.startMapQuest('q1');

    assert.equal(executed, false);
    assert.equal(refreshed, true);
    assert.match(infos[0], /no longer pending/i);
  } finally {
    globalThis.window = originalWindow;
  }
});

// ---------- HQ station registry (FR8-FR14) ----------

function makeHQCommandView(workspaceOverrides) {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    page: {
      workspace: { designation: 'personal_hq', ...workspaceOverrides },
      tasks: [],
      notes: [],
      schedules: [],
      sessions: [],
      directories: [],
      files: []
    },
    activeSystemTab: 'memory'
  });
  return commandView;
}

test('isPersonalHQ reads the workspace designation field', () => {
  const hq = makeHQCommandView();
  assert.equal(hq.isPersonalHQ(), true);

  const plain = Object.create(WorkspaceCommandView.prototype);
  Object.assign(plain, { page: { workspace: {} } });
  assert.equal(plain.isPersonalHQ(), false);

  const unknown = Object.create(WorkspaceCommandView.prototype);
  Object.assign(unknown, { page: { workspace: { designation: 'something_else' } } });
  assert.equal(unknown.isPersonalHQ(), false);
});

test('renderMapStationsPanel appends the HQ station registry only for HQ payloads', () => {
  const originalWindow = globalThis.window;
  globalThis.window = {};
  try {
    const hq = makeHQCommandView();
    const hqPanel = hq.renderMapStationsPanel();
    assert.match(hqPanel, /data-cmd-hq-station="email"/);
    assert.match(hqPanel, /is-hq-station/);

    const plain = Object.create(WorkspaceCommandView.prototype);
    Object.assign(plain, {
      page: { workspace: {}, tasks: [] },
      activeSystemTab: 'memory'
    });
    const plainPanel = plain.renderMapStationsPanel();
    assert.doesNotMatch(plainPanel, /data-cmd-hq-station/);
    assert.doesNotMatch(plainPanel, /is-hq-station/);
    // Non-HQ stations panel is unchanged from today (FR14): just the two
    // base stations.
    assert.match(plainPanel, /data-cmd-map-open-modal="tools"/);
    assert.match(plainPanel, /data-cmd-map-inventory-action="systems"/);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('Email station reflects neutral, unconnected, and connected states', () => {
  const originalWindow = globalThis.window;
  try {
    const hq = makeHQCommandView();

    // Neutral: personal-hq-email-setup.js has not published its state yet.
    globalThis.window = {};
    let panel = hq.renderMapStationsPanel();
    assert.match(panel, /Email station, loading/);
    assert.match(panel, /<strong>—<\/strong>/);

    // Unconnected: module loaded, no account linked.
    globalThis.window = { OriHQEmailSetup: { isHQ: true, connected: false } };
    panel = hq.renderMapStationsPanel();
    assert.match(panel, /Email station, not set up/);
    assert.match(panel, /<strong>Set up Email<\/strong>/);

    // Connected: shows the linked address.
    globalThis.window = {
      OriHQEmailSetup: { isHQ: true, connected: true, address: 'me@example.com' }
    };
    panel = hq.renderMapStationsPanel();
    assert.match(panel, /Email station, me@example\.com/);
    assert.match(panel, /<strong>me@example\.com<\/strong>/);

    // Connected but no address on record: falls back to "Connected".
    globalThis.window = { OriHQEmailSetup: { isHQ: true, connected: true, address: '' } };
    panel = hq.renderMapStationsPanel();
    assert.match(panel, /Email station, connected/);
    assert.match(panel, /<strong>Connected<\/strong>/);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('clicking the Email station opens the HQ email setup modal', () => {
  const originalWindow = globalThis.window;
  const opened = [];
  globalThis.window = { OriHQEmailSetup: { isHQ: true, open: () => opened.push('opened') } };
  try {
    const hq = makeHQCommandView();
    hq.runHQStationAction('email');
    assert.deepEqual(opened, ['opened']);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('HQ station click delegation dispatches through the registry', () => {
  const originalWindow = globalThis.window;
  const opened = [];
  globalThis.window = { OriHQEmailSetup: { isHQ: true, open: () => opened.push('opened') } };
  try {
    const mapRoot = makeListenerRoot();
    const hq = makeHQCommandView();
    Object.assign(hq, {
      container: {
        querySelector(selector) {
          return selector === '.ws-cmd-map-shell' ? mapRoot : null;
        }
      }
    });
    hq.bindOperationsMap();

    mapRoot.listener({
      target: {
        closest(selector) {
          return selector === '[data-cmd-hq-station]'
            ? { getAttribute: name => (name === 'data-cmd-hq-station' ? 'email' : '') }
            : null;
        }
      }
    });

    assert.deepEqual(opened, ['opened']);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('mapInventoryGroups never includes an email entry (station is its only home)', () => {
  const originalWindow = globalThis.window;
  globalThis.window = { OriHQEmailSetup: { isHQ: true, connected: true, address: 'me@example.com' } };
  try {
    const hq = makeHQCommandView();
    const keys = hq.mapInventoryGroups().map(group => group.key);
    assert.equal(keys.includes('email'), false);
  } finally {
    globalThis.window = originalWindow;
  }
});

test('openRailItem no longer special-cases an "email" section', () => {
  const originalWindow = globalThis.window;
  const opened = [];
  globalThis.window = { OriHQEmailSetup: { isHQ: true, open: () => opened.push('opened') } };
  try {
    const hq = makeHQCommandView();
    // No page.workspaceDetail-style handlers wired for 'email' — if this were
    // still special-cased to open the modal, `opened` would be non-empty.
    hq.openRailItem('email', 'hq-email', '');
    assert.deepEqual(opened, []);
  } finally {
    globalThis.window = originalWindow;
  }
});

// ---------- HQ accent styling (FR15/FR16) ----------

test('renderOperationsMap adds is-hq to the map shell only for the designated HQ', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    selectedAgentKey: '',
    activeMapWindow: '',
    mapInventorySection: '',
    activeSystemTab: 'memory',
    page: {
      workspace: { id: 'ws-1', designation: 'personal_hq', mcp_bindings: [], skill_bindings: [] },
      tasks: [],
      buildAgentGroups: () => []
    }
  });
  const hqHTML = commandView.renderOperationsMap();
  assert.match(hqHTML, /ws-cmd-map-shell is-hq/);

  commandView.page = {
    workspace: { id: 'ws-2', mcp_bindings: [], skill_bindings: [] },
    tasks: [],
    buildAgentGroups: () => []
  };
  const plainHTML = commandView.renderOperationsMap();
  assert.doesNotMatch(plainHTML, /is-hq/);
});

test('commandBarHTML shows the Personal HQ badge in map mode only', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    identityExpanded: false,
    identityEditMode: '',
    viewMode: 'map',
    page: {
      workspaceId: 'hq-1',
      workspace: {
        name: 'My Personal HQ',
        designation: 'personal_hq',
        description: '',
        mcp_bindings: [],
        skill_bindings: []
      },
      tasks: [],
      buildAgentGroups: () => []
    }
  });

  const mapHTML = commandView.commandBarHTML(
    commandView.page.workspace,
    commandView.page.workspace.name,
    'Guided',
    commandView.computeStats()
  );
  assert.match(mapHTML, /ws-cmd-hq-badge/);
  assert.match(mapHTML, />Personal HQ</);

  // Non-Goals: Command view's non-map mode must not change (FR15 is map-only).
  commandView.viewMode = 'details';
  const detailsHTML = commandView.commandBarHTML(
    commandView.page.workspace,
    commandView.page.workspace.name,
    'Guided',
    commandView.computeStats()
  );
  assert.doesNotMatch(detailsHTML, /ws-cmd-hq-badge/);
});

test('commandBarHTML omits the Personal HQ badge for non-HQ workspaces in map mode', () => {
  const commandView = Object.create(WorkspaceCommandView.prototype);
  Object.assign(commandView, {
    identityExpanded: false,
    identityEditMode: '',
    viewMode: 'map',
    page: {
      workspaceId: 'plain-1',
      workspace: { name: 'Regular Project', description: '', mcp_bindings: [], skill_bindings: [] },
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
  assert.doesNotMatch(html, /ws-cmd-hq-badge/);
});
