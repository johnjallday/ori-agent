import { test } from 'node:test';
import assert from 'node:assert/strict';

function escapeHTML(value) {
  return String(value || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

global.window = { workspaceDetail: null };
global.document = {
  createElement() {
    return {
      _text: '',
      set textContent(value) {
        this._text = String(value || '');
      },
      get innerHTML() {
        return escapeHTML(this._text);
      }
    };
  }
};

const { WorkspaceDetailPage } = await import('./workspace-detail.js');

function withDocumentElements(elements, fn) {
  const previousDocument = global.document;
  global.document = {
    ...previousDocument,
    getElementById(id) {
      return elements[id] || null;
    }
  };
  try {
    fn();
  } finally {
    global.document = previousDocument;
  }
}

function makeTabButton() {
  return {
    clickCount: 0,
    click() {
      this.clickCount += 1;
    }
  };
}

test('workspace detail activateWorkspaceConfigTab clicks the requested tab', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  const mcpTab = makeTabButton();

  withDocumentElements({ 'workspace-detail-config-mcp-tab': mcpTab }, () => {
    page.activateWorkspaceConfigTab('workspace-detail-config-mcp-tab');
    assert.doesNotThrow(() => page.activateWorkspaceConfigTab('missing-tab'));
  });

  assert.equal(mcpTab.clickCount, 1);
});

test('workspace detail renders reference URL task indicators', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  const indicator = page.renderTaskReferenceURLIndicator({
    reference_url: 'https://example.com/spec?version=1&section=intro'
  });

  assert.match(indicator, /workspace-detail-task-reference-indicator/);
  assert.match(indicator, /href="https:\/\/example\.com\/spec\?version=1&amp;section=intro"/);
  assert.match(indicator, /target="_blank"/);
  assert.match(indicator, /rel="noopener noreferrer"/);
  assert.match(indicator, /onclick="event\.stopPropagation\(\);"/);

  const boardIndicator = page.renderTaskReferenceURLIndicator(
    { reference_url: 'https://example.com/board' },
    'board'
  );
  assert.match(boardIndicator, /workspace-detail-task-reference-indicator-board/);

  assert.equal(page.renderTaskReferenceURLIndicator({ reference_url: '   ' }), '');
});

test('snapshot-backed workspace manager is healthy, not a warning', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  // Fresh-workspace shape: the auto-created "<Workspace> Manager" entry agent
  // is referenced and stored as a workspace-local snapshot, but is not present
  // in the global runnable catalog (agentIndex is empty).
  page.workspace = {
    name: 'testjdas',
    entry_agent_name: 'testjdas Manager',
    agents: ['testjdas Manager'],
    agent_instances: [{ name: 'testjdas Manager' }],
    shared_data: {}
  };
  page.agentIndex = new Map();
  page.workspaceAgentSnapshots = new Set(['testjdas manager']);
  page.files = [];
  // summarizeWorkspaceHealth() (asserted below) reports `checking` until both
  // load flags flip true; set them so it can resolve to `healthy`.
  // collectWorkspaceHealthIssues() itself ignores these flags.
  page.agentCatalogLoaded = true;
  page.filesLoaded = true;

  const issues = page.collectWorkspaceHealthIssues();
  assert.equal(issues.length, 0, 'snapshot-backed agent should not raise a health issue');

  const summary = page.summarizeWorkspaceHealth();
  assert.equal(summary.status, 'healthy');
});

test('referenced agent with no global definition and no snapshot is a missing error', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.workspace = {
    name: 'testjdas',
    entry_agent_name: 'testjdas Manager',
    agents: ['testjdas Manager'],
    agent_instances: [{ name: 'testjdas Manager' }],
    shared_data: {}
  };
  page.agentIndex = new Map();
  page.workspaceAgentSnapshots = new Set(); // no snapshot available
  page.files = [];
  // No agentCatalogLoaded/filesLoaded here: this case only exercises
  // collectWorkspaceHealthIssues(), which doesn't read those flags.

  const issues = page.collectWorkspaceHealthIssues();
  assert.equal(issues.length, 1);
  assert.equal(issues[0].severity, 'error');
  assert.equal(issues[0].title, 'Entry agent is missing');
});

test('workspace detail summary chip counts tasks with reference URLs', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  const referenceChip = { hidden: true, textContent: '' };
  page.elements = { configReferenceChip: referenceChip };
  page.getWorkspaceMCPBindings = () => [];
  page.getWorkspaceSkillBindings = () => [];
  page.tasks = [
    { id: 'task-1', reference_url: 'https://example.com/one' },
    { id: 'task-2', reference_url: '   ' },
    { id: 'task-3', reference_url: 'https://example.com/two' }
  ];

  page.renderWorkspaceConfigSummary();

  assert.equal(referenceChip.hidden, false);
  assert.equal(referenceChip.textContent, 'Refs: 2');

  page.tasks = [{ id: 'task-4' }];
  page.renderWorkspaceConfigSummary();

  assert.equal(referenceChip.hidden, true);
  assert.equal(referenceChip.textContent, 'Refs: 0');
});

test('workspace detail empty project state offers template creation and folder linking', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.workspace = { kind: 'workspace' };

  const markup = page.getUnlinkedProjectEmptyStateMarkup();

  assert.match(markup, /showProjectTemplateModal/);
  assert.match(markup, /Create Project/);
  assert.match(markup, /showAddDirectoryModal/);
  assert.match(markup, /Link Folder/);
});

test('workspace detail renders project_path fallback instead of unlinked empty state', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  const list = { innerHTML: '' };
  page.elements = { directoriesList: list };
  page.workspace = { project_path: 'smoke-song' };
  page.directories = [];

  page.renderDirectories();

  assert.match(list.innerHTML, /smoke-song/);
  assert.match(list.innerHTML, /Project Folder/);
  assert.match(list.innerHTML, /Template project/);
  assert.doesNotMatch(list.innerHTML, /No project folder linked yet/);
});

test('workspace detail entity loaders refresh Command view after completion', async () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.workspace = {};
  page.elements = {};
  page.renderSessions = () => {};
  page.renderNotes = () => {};
  page.renderDirectories = () => {};
  page.renderSchedules = () => {};
  page.refreshHomeAssistantQuickPrompts = () => {};
  page.updateCopyNotesButtonState = () => {};
  page.syncProjectActionState = () => {};
  page.renderWorkspaceMCPBindings = () => {};
  page.renderWorkspaceSkillBindings = () => {};
  page.renderAgentGroups = () => {};

  const originalFetch = global.fetch;
  const originalWindow = global.window;
  let refreshCount = 0;
  global.window = {
    ...originalWindow,
    workspaceCommand: {
      refresh() {
        refreshCount += 1;
      }
    }
  };
  global.fetch = async (url) => {
    const href = String(url);
    if (href.startsWith('/api/sessions')) {
      return { ok: true, json: async () => ({ sessions: [{ id: 's1' }] }) };
    }
    if (href.endsWith('/notes')) {
      return { ok: true, json: async () => ({ notes: [{ id: 'n1' }] }) };
    }
    if (href.startsWith('/api/orchestration/tasks')) {
      return {
        ok: true,
        json: async () => ({
          tasks: [{ id: 'sched-1', description: 'Scheduled', schedule: '* * * * *' }]
        })
      };
    }
    if (href.startsWith('/api/workspaces/')) {
      return {
        ok: true,
        json: async () => ({
          directory_references: [{ id: 'dir-1', name: 'Project', path: '/tmp/project' }],
          attachments: []
        })
      };
    }
    throw new Error(`unexpected fetch: ${href}`);
  };

  try {
    await page.loadSessions();
    await page.loadNotes();
    await page.loadDirectories();
    await page.loadSchedules();
    await page.loadFiles();
  } finally {
    global.fetch = originalFetch;
    global.window = originalWindow;
  }

  assert.equal(refreshCount, 5);
});

test('workspace detail reusable identity and tag saves update workspace state', async () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.workspace = { id: 'workspace-1', name: 'Old Name', description: '', tags: ['old'] };
  page.elements = {};
  page.renderWorkspaceInfo = async () => {};
  page.renderWorkspaceTags = () => {};
  page.loadNotes = async () => {};

  const originalFetch = global.fetch;
  const originalWindow = global.window;
  const requests = [];
  global.window = {
    ...originalWindow,
    Toast: { success() {}, error() {} },
    OriTagInput: { clearTagPoolCache() {} },
    workspaceCommand: { refresh() {} }
  };
  global.fetch = async (url, options = {}) => {
    requests.push({ url: String(url), body: options.body || '' });
    if (String(url).endsWith('/rename')) {
      return {
        ok: true,
        json: async () => ({ folder: { id: 'workspace-1', name: 'New Name', folder_slug: 'new-name' } })
      };
    }
    return {
      ok: true,
      json: async () => ({ folder: { id: 'workspace-1', tags: ['alpha', 'beta'] } })
    };
  };

  try {
    await page.saveWorkspaceIdentityField('name', 'New Name', { currentValue: 'Old Name' });
    const tags = await page.saveWorkspaceTagList(['Alpha', 'beta']);

    assert.equal(page.workspace.name, 'New Name');
    assert.deepEqual(tags, ['alpha', 'beta']);
    assert.deepEqual(page.workspace.tags, ['alpha', 'beta']);
    assert.match(requests[0].url, /\/api\/workspaces\/workspace-1\/rename$/);
    assert.match(requests[1].url, /\/api\/workspaces\/workspace-1$/);
    assert.match(requests[1].body, /"tags":\["alpha","beta"\]/);
  } finally {
    global.fetch = originalFetch;
    global.window = originalWindow;
  }
});

test('workspace detail protects the managed project directory row', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  const list = { innerHTML: '' };
  page.elements = { directoriesList: list };
  page.workspace = {
    project_path: 'song-x',
    primary_directory_id: 'dir-project',
    shared_data: { project_directory_id: 'dir-project' }
  };
  page.directories = [
    { id: 'dir-project', name: 'Song X', path: '/workspaces/song-x/song-x', source: 'reference' },
    { id: 'dir-root', name: 'song-x', path: '/workspaces/song-x', source: 'reference' }
  ];

  page.renderDirectories();

  assert.match(list.innerHTML, /Template project/);
  assert.match(list.innerHTML, /openDirectoryExplorer\('dir-project'/);
  assert.doesNotMatch(list.innerHTML, /promptRelinkDirectory\('dir-project'/);
  assert.doesNotMatch(list.innerHTML, /deleteDirectory\('dir-project'/);
  assert.match(list.innerHTML, /promptRelinkDirectory\('dir-root'/);
});

test('workspace detail disables create-project action for groups and existing projects', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  const button = {
    disabled: false,
    title: '',
    attributes: {},
    setAttribute(name, value) {
      this.attributes[name] = value;
    }
  };
  page.elements = { createProjectBtn: button };

  page.workspace = { kind: 'workspace' };
  page.syncProjectActionState();
  assert.equal(button.disabled, false);

  page.workspace = { kind: 'workspace', project_path: 'song-x' };
  page.syncProjectActionState();
  assert.equal(button.disabled, true);
  assert.equal(button.attributes['aria-disabled'], 'true');
  assert.match(button.title, /already has a project/);

  page.workspace = { kind: 'group' };
  page.syncProjectActionState();
  assert.equal(button.disabled, true);
  assert.match(button.title, /Groups cannot hold projects/);
});

test('workspace detail normalizes workspace tag drafts', () => {
  const page = new WorkspaceDetailPage('workspace-1');

  const result = page.normalizeWorkspaceTagList([' Music ', 'music', 'Client:Acme', '']);

  assert.deepEqual(result, { tags: ['music', 'client:acme'], error: '' });

  const overlong = page.normalizeWorkspaceTagList(['x'.repeat(65)]);
  assert.match(overlong.error, /64 character limit/);

  const tooMany = page.normalizeWorkspaceTagList(
    Array.from({ length: 21 }, (_, index) => `tag-${index}`)
  );
  assert.match(tooMany.error, /at most 20 tags/);
});

test('workspace detail renders escaped read-only workspace tags', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  const container = { hidden: true };
  const list = { innerHTML: '' };
  page.elements = {
    workspaceTagsContainer: container,
    workspaceTagsList: list
  };
  page.workspace = { tags: ['music', '<script>', 'Client & Research'] };

  page.renderWorkspaceTags();

  assert.equal(container.hidden, false);
  assert.match(list.innerHTML, /title="music">music<\/span>/);
  assert.match(list.innerHTML, /title="&lt;script&gt;">&lt;script&gt;<\/span>/);
  assert.match(list.innerHTML, /title="Client &amp; Research">Client &amp; Research<\/span>/);
});

test('workspace detail aggregates agent group roles for roster cards', () => {
  const page = new WorkspaceDetailPage('workspace-1');

  assert.deepEqual(page.getAgentGroupRolePresentation({ roles: ['Lead', 'lead', '  '] }), {
    label: 'Lead',
    detail: 'Lead',
    roles: ['Lead']
  });

  assert.deepEqual(page.getAgentGroupRolePresentation({ roles: ['Researcher', 'Writer'] }), {
    label: 'Multiple roles',
    detail: 'Researcher, Writer',
    roles: ['Researcher', 'Writer']
  });

  assert.deepEqual(page.getAgentGroupRolePresentation({ roles: [] }), {
    label: 'Agent',
    detail: 'Agent',
    roles: []
  });
});

test('workspace detail carries agent instance roles into roster groups', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.workspace = {
    agent_instances: [
      { name: 'Manager', role: 'Lead' },
      { name: 'Manager', role: 'Reviewer' },
      { name: 'Writer', role: 'Drafting' }
    ]
  };
  page.tasks = [];

  const groups = page.buildAgentGroups();
  const manager = groups.find(group => group.name === 'Manager');
  const writer = groups.find(group => group.name === 'Writer');

  assert.equal(manager.instanceCount, 2);
  assert.deepEqual(manager.roles, ['Lead', 'Reviewer']);
  assert.deepEqual(writer.roles, ['Drafting']);
});

test('workspace detail derives roster status from all assigned tasks and subtasks', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.tasks = [
    { id: 'parent-1', to: 'Manager', status: 'pending' },
    { id: 'subtask-1', parent_task_id: 'parent-1', to: 'Writer', status: 'waiting_for_choice' },
    { id: 'subtask-2', parent_task_id: 'parent-1', to: 'Researcher', status: 'in_progress' },
    { id: 'task-2', to: 'Writer', status: 'in_progress' }
  ];

  assert.equal(page.getAgentRosterStatus('Researcher').label, 'Working');
  assert.equal(page.getAgentRosterStatus('Writer').label, 'Working');

  page.tasks = [
    { id: 'parent-1', to: 'Manager', status: 'pending' },
    { id: 'subtask-1', parent_task_id: 'parent-1', to: 'Writer', status: 'waiting_for_choice' }
  ];

  assert.equal(page.getAgentRosterStatus('Writer').label, 'Needs input');
  assert.equal(page.getAgentRosterStatus('Manager').label, 'Idle');
});

test('workspace detail generates stable deterministic agent avatars', () => {
  const page = new WorkspaceDetailPage('workspace-1');

  const first = page.getAgentAvatarPresentation('Trip Planning Manager');
  const second = page.getAgentAvatarPresentation('Trip Planning Manager');
  const other = page.getAgentAvatarPresentation('Trip Planning Writer');

  assert.equal(first.initials, 'TM');
  assert.equal(first.hue, second.hue);
  assert.equal(first.style, second.style);
  assert.notEqual(first.hue, other.hue);
});

test('workspace detail skill summary uses workspace-effective skills', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.getEffectiveWorkspaceSkillNamesForAgent = () => [
    'workspace-planning',
    'browser:control-in-app-browser',
    'pdf:pdf',
    'documents:documents'
  ];

  const summary = page.getAgentSkillSummary('Manager');
  const markup = page.renderAgentSkillSummary('Manager');

  assert.equal(summary.count, 4);
  assert.deepEqual(summary.visible, [
    'workspace-planning',
    'browser:control-in-app-browser',
    'pdf:pdf'
  ]);
  assert.equal(summary.overflow, 1);
  assert.match(markup, /workspace-planning/);
  assert.match(markup, /\+1/);
  assert.doesNotMatch(markup, /Browser<\/span>/);
});

test('workspace detail links catalog-backed agents to the global detail page', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.workspace = {
    entry_agent_name: 'Catalog Manager',
    agent_instances: [{ name: 'Catalog Manager', role: 'Coordinator', entry_point: true }]
  };
  page.agentIndex = new Map([['catalog manager', { name: 'Catalog Manager' }]]);
  page.workspaceAgentSnapshots = new Set();

  const target = page.getAgentDetailTarget('Catalog Manager');
  const markup = page.renderAgentIdentityLink(
    { name: 'Catalog Manager' },
    page.getAgentAvatarPresentation('Catalog Manager'),
    { label: 'Coordinator' },
    'summary-catalog'
  );

  assert.equal(target.kind, 'global');
  assert.equal(target.interactive, true);
  assert.equal(target.href, '/agents/Catalog%20Manager');
  assert.match(markup, /href="\/agents\/Catalog%20Manager"/);
  assert.match(markup, /data-agent-detail-kind="global"/);
});

test('workspace detail links snapshot-backed local agents to their detail page', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.workspace = {
    entry_agent_name: 'Local Manager',
    agent_instances: [{ name: 'Local Manager', role: 'Coordinator', entry_point: true }]
  };
  page.agentIndex = new Map();
  page.workspaceAgentSnapshots = new Set(['local manager']);

  const target = page.getAgentDetailTarget('Local Manager');
  const markup = page.renderAgentIdentityLink(
    { name: 'Local Manager' },
    page.getAgentAvatarPresentation('Local Manager'),
    { label: 'Coordinator' },
    'summary-local'
  );

  // Snapshot-backed local agents are hydrated into the agent store on startup,
  // so /agents/<name> resolves (degrading to a repair view if it does not).
  assert.equal(target.kind, 'workspace-local');
  assert.equal(target.interactive, true);
  assert.equal(target.href, '/agents/Local%20Manager');
  assert.match(markup, /data-agent-detail-kind="workspace-local"/);
  assert.match(markup, /href="\/agents\/Local%20Manager"/);
  assert.doesNotMatch(markup, /is-static/);
});

test('workspace detail routes missing entry agents to workspace recovery', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.workspace = {
    entry_agent_name: 'Missing Manager',
    agent_instances: [{ name: 'Missing Manager', role: 'Coordinator', entry_point: true }]
  };
  page.agentIndex = new Map();
  page.workspaceAgentSnapshots = new Set();

  const target = page.getAgentDetailTarget('Missing Manager');
  const frontMarkup = page.renderAgentIdentityLink(
    { name: 'Missing Manager' },
    page.getAgentAvatarPresentation('Missing Manager'),
    { label: 'Coordinator' },
    'summary-missing'
  );
  const backMarkup = page.renderAgentDetailLink(
    'Missing Manager',
    encodeURIComponent('Missing Manager')
  );

  assert.equal(target.kind, 'missing-entry');
  assert.equal(target.interactive, true);
  assert.equal(target.href, '/workspaces/workspace-1?addAgent=1&seedAgentName=Missing+Manager');
  assert.match(frontMarkup, /href="\/workspaces\/workspace-1\?addAgent=1&amp;seedAgentName=Missing\+Manager"/);
  assert.match(backMarkup, /href="\/workspaces\/workspace-1\?addAgent=1&amp;seedAgentName=Missing\+Manager"/);
  assert.doesNotMatch(frontMarkup, /\/agents\/Missing%20Manager/);
  assert.doesNotMatch(backMarkup, /\/agents\/Missing%20Manager/);
});

test('buildAgentPromptPreview collapses whitespace and truncates long prompts', () => {
  const page = new WorkspaceDetailPage('workspace-1');

  assert.equal(
    page.buildAgentPromptPreview({ effective_prompt: 'You are\n  a  helpful\tagent.' }),
    'You are a helpful agent.'
  );
  assert.equal(
    page.buildAgentPromptPreview({ base_system_prompt: '', effective_prompt: '' }),
    'No system prompt set for this agent.'
  );

  const long = 'x'.repeat(400);
  const preview = page.buildAgentPromptPreview({ effective_prompt: long });
  assert.ok(preview.length <= 161, `preview should be capped, got ${preview.length}`);
  assert.ok(preview.endsWith('…'), 'truncated preview should end with an ellipsis');

  // Falls back to the base prompt when no composed prompt is present.
  assert.equal(
    page.buildAgentPromptPreview({ base_system_prompt: 'Base only.', effective_prompt: '' }),
    'Base only.'
  );
});

test('workspace detail agent back face does not render agent level copy', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.getEffectiveWorkspaceSkillNamesForAgent = () => ['workspace-planning'];
  page.getEffectiveWorkspaceMCPServerNames = () => ['filesystem'];
  page.workspace = { entry_agent_name: 'Manager' };

  const markup = page.renderAgentBackFace(
    { key: 'manager', name: 'Manager', instanceCount: 1, isWorkspaceAgent: true, roles: ['Lead'] },
    '1 task',
    'Manager'
  );

  assert.doesNotMatch(markup, /Agent Lvl/);
  assert.doesNotMatch(markup, /Lv /);
  assert.match(markup, /Status/);
  assert.match(markup, /Role/);
  assert.match(markup, /Lead/);
});

test('workspace-local agent profile resolves its model and renders an editable badge', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  // Not in the global catalog; only present as a workspace-local profile.
  page.agentIndex = new Map();
  page.workspaceAgentProfiles = new Map([
    [
      'reaperdaw manager',
      {
        name: 'ReaperDAW Manager',
        type: 'orchestration',
        model: 'google/gemma-4-e4b',
        provider: 'lmstudio',
        source: 'workspace'
      }
    ]
  ]);

  const profile = page.getAgentProfile('ReaperDAW Manager');
  assert.ok(profile, 'profile resolves from the workspace-local map');
  assert.equal(profile.model, 'google/gemma-4-e4b');
  assert.equal(profile.source, 'workspace');

  const presentation = page.getAgentModelPresentation(profile);
  assert.equal(presentation.empty, false, 'model is set, must not show "Model not set"');
  assert.match(presentation.label, /gemma-4-e4b/);

  assert.equal(page.agentAllowsModelEditing(profile), true);

  const badge = page.renderAgentModelPowerBadge(
    'ReaperDAW Manager',
    profile,
    encodeURIComponent('ReaperDAW Manager')
  );
  assert.match(badge, /<button/);
  assert.match(badge, /openAgentModelModal/);
  assert.doesNotMatch(badge, /Model not set/);
});

test('workspace-local model picker lists every model; global picker filters by type', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.providerCatalog = [
    {
      name: 'claude',
      display_name: 'Claude',
      models: [
        { value: 'claude-opus-4', type: 'orchestration', label: 'Opus' },
        { value: 'claude-haiku', type: 'research', label: 'Haiku' }
      ]
    },
    {
      name: 'lmstudio',
      display_name: 'LM Studio',
      models: [{ value: 'google/gemma-4-e4b', type: 'research', label: 'Gemma' }]
    }
  ];

  const makeEl = tag => ({
    tagName: tag,
    children: [],
    attributes: {},
    _text: '',
    label: '',
    value: '',
    selected: false,
    disabled: false,
    set innerHTML(v) {
      if (v === '') this.children = [];
      this._html = v;
    },
    get innerHTML() {
      return this._html || '';
    },
    set textContent(v) {
      this._text = String(v || '');
    },
    get textContent() {
      return this._text;
    },
    setAttribute(k, v) {
      this.attributes[k] = v;
    },
    getAttribute(k) {
      return this.attributes[k];
    },
    appendChild(c) {
      this.children.push(c);
      return c;
    }
  });

  const origCreate = global.document.createElement;
  global.document.createElement = tag => makeEl(tag);
  try {
    const select = makeEl('select');
    page.elements = { agentModelSelect: select, agentModelSubmitBtn: makeEl('button') };
    const values = () => select.children.flatMap(g => g.children.map(o => o.value));

    // Workspace-local: no type filter, every model from every provider.
    page.activeAgentModelEdit = {
      agentType: 'orchestration',
      currentModel: '',
      currentProvider: '',
      isWorkspaceAgent: true
    };
    page.populateAgentModelSelect();
    const wsValues = values();
    assert.equal(wsValues.length, 3, 'workspace-local lists every model');
    assert.ok(wsValues.includes('google/gemma-4-e4b'), 'local provider model present');
    assert.ok(wsValues.includes('claude-haiku'), 'non-matching type present');

    // Global: only models whose type matches the agent type.
    page.activeAgentModelEdit = {
      agentType: 'orchestration',
      currentModel: '',
      currentProvider: '',
      isWorkspaceAgent: false
    };
    page.populateAgentModelSelect();
    assert.deepEqual(values(), ['claude-opus-4'], 'global picker keeps only matching type');
  } finally {
    global.document.createElement = origCreate;
  }
});

test('workspace-local model save posts to the workspace-scoped endpoint', async () => {
  const page = new WorkspaceDetailPage('workspace-1');
  page.activeAgentModelEdit = {
    agentName: 'ReaperDAW Manager',
    isWorkspaceAgent: true,
    workspaceId: 'ws-1',
    currentModel: '',
    currentProvider: ''
  };
  page.elements = {
    agentModelSelect: { value: 'claude-opus-4', selectedOptions: [{ getAttribute: () => 'claude' }] },
    agentModelSubmitBtn: { disabled: false, textContent: '' },
    agentModelModal: null
  };
  page.loadWorkspaceAgentSnapshots = async () => {};
  page.renderAgentGroups = () => {};

  let captured = null;
  const origFetch = global.fetch;
  global.fetch = async (url, opts) => {
    captured = { url, opts };
    return { ok: true, text: async () => '' };
  };
  try {
    await page.submitAgentModelChange();
  } finally {
    global.fetch = origFetch;
  }

  assert.ok(captured, 'fetch was called');
  assert.equal(captured.url, '/api/workspaces/ws-1/agents/ReaperDAW%20Manager');
  assert.equal(captured.opts.method, 'PATCH');
  const body = JSON.parse(captured.opts.body);
  assert.equal(body.model, 'claude-opus-4');
  assert.equal(body.llm_provider, 'claude');
});
