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
