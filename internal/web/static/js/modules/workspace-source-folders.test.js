import assert from 'node:assert/strict';
import test from 'node:test';
import { hasMusicFolderContext, readWorkspaceSourceFolders } from './workspace-source-folders.js';
import { WorkspaceMembersPanel } from './workspace-detail-members.js';

const child = {
  id: 'g',
  kind: 'group',
  children: [
    { id: 'b', name: 'Track' },
    { id: 'a', name: 'Track' }
  ]
};
const home = {
  kind: 'group',
  id: 'h',
  group_template: { kind: 'managed_home', plugin_id: 'music-project-management' },
  children: [child]
};
const section = label => ({ id: `${label}-section`, label });
const row = (id, label) => ({ workspace_id: id, folder_section: section(label) });
const response = body => ({ ok: true, json: async () => body });

test('only the nearest managed Music Home hints at a scoped read', () => {
  assert.ok(hasMusicFolderContext([home], 'g'));
  assert.ok(hasMusicFolderContext([home], 'h'));
  assert.equal(hasMusicFolderContext([child], 'g'), false);
  assert.equal(hasMusicFolderContext([home], 'a'), false);
  assert.equal(
    hasMusicFolderContext(
      [
        {
          ...home,
          children: [
            { ...home, id: 'other', group_template: { kind: 'managed_home', plugin_id: 'other' } }
          ]
        }
      ],
      'g'
    ),
    false
  );
});

test('read one scoped snapshot; reject partial, duplicate or failed reads', async () => {
  const read = body =>
    readWorkspaceSourceFolders('a/b', {
      fetchImpl: async (url, options) => {
        assert.equal(url, '/api/workspaces/a%2Fb/source-folders');
        assert.equal(options.cache, 'no-store');
        return response(body);
      }
    });
  const labels = await read({ revision: 1, rows: [row('a', 'Albums'), row('b', 'Picker Songs')] });
  assert.deepEqual([...labels.keys()], ['a', 'b']);
  assert.notEqual(labels.get('a').hue, labels.get('b').hue);
  const subset = await read({ revision: 2, rows: [row('other-song', 'Picker Songs')] });
  assert.equal(labels.get('b').hue, subset.get('other-song').hue);
  for (const body of [
    { rows: [] },
    { revision: 1, rows: [row('a', 'A'), row('a', 'B')] },
    { revision: 1, rows: [{}] }
  ])
    await assert.rejects(read(body));
  await assert.rejects(readWorkspaceSourceFolders('h', { fetchImpl: async () => ({ ok: false }) }));
});

test('member reload preserves the controls and ignores a late label read', async t => {
  const panel = new WorkspaceMembersPanel('g');
  let lines = [];
  let html = '';
  panel.els.list = {
    set innerHTML(value) {
      html = value;
      lines = [...html.matchAll(/data-member-id="([^"]+)"/g)].map(([, id]) => ({
        id: `member-source-${id}`,
        hidden: true,
        badge: { style: { setProperty() {}, removeProperty() {} } },
        querySelector() {
          return this.badge;
        },
        parentElement: { dataset: { memberId: id }, setAttribute() {}, removeAttribute() {} }
      }));
    },
    querySelectorAll(selector) {
      return selector === '[data-member-folder]' ? lines : [];
    }
  };
  const pending = [];
  let tree = [home];
  t.mock.method(globalThis, 'fetch', async url => {
    if (url === '/api/workspaces?tree=true') return response({ workspaces: tree });
    assert.equal(url, '/api/workspaces/g/source-folders');
    return new Promise(resolve => pending.push(resolve));
  });
  await panel.reload();
  const late = panel.sourceFoldersRead;
  assert.deepEqual(
    lines.map(x => x.parentElement.dataset.memberId),
    ['b', 'a']
  );
  const first = lines;
  pending.shift()(
    response({ revision: 1, rows: [row('a', 'Albums'), row('b', '<Picker Songs>')] })
  );
  await late;
  assert.equal(lines, first);
  assert.deepEqual(
    lines.map(x => x.badge.textContent),
    ['<Picker Songs>', 'Albums']
  );
  assert.match(html, /data-member-up="b"[^>]+disabled/);
  assert.match(html, /data-member-remove="a"/);
  assert.match(html, /data-member-down="a"[^>]+disabled/);
  await panel.reload();
  const stale = panel.sourceFoldersRead;
  tree = [{ ...home, children: [{ ...child, children: [child.children[1]] }] }];
  await panel.reload();
  pending.pop()({ ok: false });
  await panel.sourceFoldersRead;
  pending.pop()(response({ revision: 1, rows: [row('a', 'stale')] }));
  await stale;
  assert.equal(lines.length, 1);
  assert.ok(lines[0].hidden);
  assert.equal(panel.sourceFolders, null);
  tree = [child];
  await panel.reload();
  assert.equal(panel.sourceFoldersRead, null);
  assert.doesNotMatch(html, /data-member-folder/);
});
