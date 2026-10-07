import test from 'node:test';
import assert from 'node:assert/strict';
import {
  collectWorkspaceContext,
  workspaceContextLabel,
  renderTurnWorkspace,
  turnSourcesView,
  renderTurnSources
} from './personal-assistant-workspace-context.js';

const page = {
  pathname: '/workspaces/album-1/assistant',
  workspaceId: 'uuid-a',
  workspaceSlug: 'album-1',
  selectionWorkspaceId: 'uuid-b'
};
test('page wins over Home selection and the route slug is not an ID', () => {
  const result = collectWorkspaceContext(page);
  assert.equal(result.context_version, 1);
  assert.equal(result.workspace_id, 'uuid-a');
  assert.equal(result.workspace_slug, 'album-1');
  assert.equal(result.selection_workspace_id, '');
  assert.equal(collectWorkspaceContext({ ...page, workspaceId: '' }).workspace_id, '');
});
test('in-page navigation/back-forward drops stale page IDs and app-wide selections', () => {
  const next = collectWorkspaceContext({ ...page, pathname: '/workspaces/second/canvas' });
  assert.equal(next.workspace_id, '');
  assert.equal(next.workspace_slug, 'second');
  assert.equal(next.surface, 'workspace_canvas');
  assert.equal(collectWorkspaceContext(page).workspace_id, 'uuid-a');
  const global = collectWorkspaceContext({ ...page, pathname: '/settings' });
  assert.equal(global.workspace_id, '');
  assert.equal(global.workspace_slug, '');
  assert.equal(global.selection_workspace_id, '');
});
test('Home has an explicit canonical selection; task routes use their own task only', () => {
  const home = collectWorkspaceContext({ ...page, pathname: '/', taskId: 'selected-task' });
  assert.equal(home.selection_workspace_id, 'uuid-b');
  assert.equal(home.task_id, 'selected-task');
  assert.equal(collectWorkspaceContext({ pathname: '/' }).workspace_id, '');
  const task = collectWorkspaceContext({
    ...page,
    pathname: '/workspaces/album-1/tasks/current-task',
    taskId: 'stale-task'
  });
  assert.equal(task.task_id, 'current-task');
  // The app's own task page is /task/<id>.
  const real = collectWorkspaceContext({ ...page, pathname: '/workspaces/album-1/task/open-task' });
  assert.equal(real.surface, 'workspace_task');
  assert.equal(real.task_id, 'open-task');
  assert.equal(
    collectWorkspaceContext({ ...page, pathname: '/workspaces/album-1/notes/a-note' }).task_id,
    ''
  );
});
test('display separates app-wide, group, Home, project, explicit subject and old unknown scope', () => {
  assert.equal(workspaceContextLabel({ version: 1, status: 'available' }), 'App-wide');
  assert.equal(workspaceContextLabel(null, { historical: true }), 'Earlier workspace unknown');
  assert.equal(
    workspaceContextLabel({ version: 1, status: 'denied' }),
    'Workspace context unavailable'
  );
  for (const [kind, label] of [
    ['home', 'Home'],
    ['group', 'Group'],
    ['project', 'Project']
  ]) {
    assert.match(
      workspaceContextLabel({ version: 1, status: 'available', subject: { name: 'Album', kind } }),
      new RegExp(label + ': Album')
    );
  }
  assert.equal(
    workspaceContextLabel({
      version: 1,
      status: 'available',
      subject: { id: 'b', name: 'B', kind: 'project' },
      parent: { name: 'Music' },
      location: { id: 'a', name: 'A' },
      selected_task_id: 'task'
    }),
    'Project: B · Music · Selected task · from A'
  );
});
test('a delayed/historical row uses its accepted attribution, never the current page', () => {
  const element = { dataset: {}, textContent: '' };
  const row = {
    ownerDocument: { createElement: () => element },
    querySelector: () => null,
    append: child => assert.equal(child, element)
  };
  renderTurnWorkspace(row, {
    version: 1,
    status: 'available',
    subject: { id: 'a', name: '<script>A</script>', kind: 'project' }
  });
  assert.equal(element.textContent, 'Project: <script>A</script>');
  assert.equal(element.dataset.workspaceId, 'a');
  assert.equal(element.innerHTML, undefined, 'names are text, never markup');
});

const note = {
  key: 'S1',
  kind: 'note',
  label: 'Release plan',
  workspace: 'Album-1',
  coverage: 'full',
  cited: true,
  href: '/workspaces/album-1/notes/note-1',
  updated_at: '2026-01-02T10:00:00Z',
  read_at: '2026-01-03T10:00:00Z'
};
const stamp = { formatTime: value => String(value).slice(0, 10) };

test('sources list what the reply read, how much, and link only in-app pages', () => {
  const view = turnSourcesView(
    {
      sources: [
        note,
        { ...note, key: 'S2', kind: 'task', label: '#4 Master the single', cited: false },
        {
          ...note,
          key: 'S3',
          label: 'Long log',
          coverage: 'partial',
          start: 0,
          end: 40000,
          total: 126000
        }
      ]
    },
    stamp
  );
  assert.equal(view.visible, true);
  // Only the sources the reply pointed at are listed as used.
  assert.equal(view.summary, 'Sources used (2)');
  assert.deepEqual(
    view.rows.map(row => row.key),
    ['S1', 'S3']
  );
  assert.equal(view.rows[0].title, 'Note: Release plan');
  assert.equal(view.rows[0].href, '/workspaces/album-1/notes/note-1');
  assert.equal(
    view.rows[0].detail,
    'Album-1 · read in full · updated 2026-01-02 · read 2026-01-03'
  );
  assert.match(view.rows[1].detail, /part read \(characters 1–40000 of 126000\)/);
  assert.equal(view.note, '');
  for (const href of [
    'https://example.test/notes/1',
    '//example.test',
    'javascript:void(0)',
    '/Users/me/Music/album.rpp',
    '/workspaces/album-1/notes/a/b',
    '/settings'
  ])
    assert.equal(turnSourcesView({ sources: [{ ...note, href }] }, stamp).rows[0].href, '', href);
});

test('uncited and saved sources are labeled for what they are; no sources shows nothing', () => {
  const read = turnSourcesView({ sources: [{ ...note, cited: false }] }, stamp);
  assert.equal(read.summary, 'Sources read (1)');
  assert.match(read.note, /did not point at a specific one/);
  const saved = turnSourcesView({ sources: [note] }, { ...stamp, historical: true });
  assert.match(saved.note, /read at the time\. It is not a fresh read/);
  for (const empty of [null, {}, { sources: [] }, { sources: [{ key: 'S1' }, null, 'S2'] }])
    assert.equal(turnSourcesView(empty).visible, false);
  // A missing time is left out rather than shown as an invented date.
  assert.equal(
    turnSourcesView(
      { sources: [{ ...note, updated_at: '0001-01-01T00:00:00Z', read_at: '' }] },
      stamp
    ).rows[0].detail,
    'Album-1 · read in full'
  );
});

test('the disclosure is built from text nodes and sits above the message actions', () => {
  const made = [];
  const create = tag => {
    const node = {
      tag,
      children: [],
      className: '',
      textContent: '',
      append: (...items) => node.children.push(...items)
    };
    made.push(node);
    return node;
  };
  const actions = { marker: 'actions' };
  const inserted = [];
  const bubble = {
    querySelector: selector => (selector.includes('__actions') ? actions : null),
    insertBefore: (node, before) => inserted.push([node, before]),
    append: () => assert.fail('sources must not be placed after the message actions')
  };
  const row = {
    ownerDocument: { createElement: create },
    querySelector: () => null,
    firstElementChild: bubble
  };
  assert.equal(
    renderTurnSources(
      row,
      { sources: [{ ...note, label: '<img src=x onerror=alert(1)>' }] },
      stamp
    ),
    true
  );
  const details = inserted[0][0];
  assert.equal(details.tag, 'details');
  assert.equal(inserted[0][1], actions);
  assert.equal(made.find(node => node.tag === 'summary').textContent, 'Sources used (1)');
  const link = made.find(node => node.tag === 'a');
  assert.equal(link.href, '/workspaces/album-1/notes/note-1');
  assert.equal(link.textContent, 'Note: <img src=x onerror=alert(1)>');
  assert.equal(link.innerHTML, undefined, 'a source label is text, never markup');
  assert.equal(renderTurnSources(row, { sources: [] }), false);
});
