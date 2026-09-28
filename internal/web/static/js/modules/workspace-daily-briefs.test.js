import { test } from 'node:test';
import assert from 'node:assert/strict';
import { historyView, dateLabel, wireWorkspaceDailyBriefs } from './workspace-daily-briefs.js';

function fakeDoc() {
  const make = tag => ({
    tagName: tag,
    className: '',
    textContent: '',
    innerHTML: '',
    hidden: false,
    open: false,
    dataset: {},
    children: [],
    listeners: {},
    appendChild(child) {
      this.children.push(child);
      return child;
    },
    append(...children) {
      this.children.push(...children);
    },
    addEventListener(type, fn) {
      this.listeners[type] = fn;
    }
  });
  return { createElement: make, make };
}

// Only the newest date holds the workspace's current revision.
const history = [
  {
    local_date: '2026-08-30',
    current_revision_id: '',
    latest_revision_id: 'rev-a',
    revision_count: 1
  },
  { local_date: '2026-08-31', current_revision_id: 'rev-b', revision_count: 2 }
];

test('shows a workspace’s own briefs only when it is not the HQ here', () => {
  const imported = historyView(history, { workspaceId: 'imported', hqWorkspaceId: 'current-hq' });
  assert.equal(imported.show, true);
  assert.deepEqual(
    imported.items.map(item => [item.date, item.revisionId]),
    [
      ['2026-08-31', 'rev-b'],
      ['2026-08-30', 'rev-a']
    ]
  );
  assert.equal(historyView(history, { workspaceId: 'hq', hqWorkspaceId: 'hq' }).show, false);
  assert.equal(historyView([], { workspaceId: 'imported', hqWorkspaceId: '' }).show, false);
});

test('stored local dates are labelled without shifting across time zones', () => {
  assert.match(dateLabel('2026-08-31'), /31/);
  assert.equal(dateLabel('not-a-date'), 'not-a-date');
});

test('the panel lists dates and opens a brief as escaped history on demand', async () => {
  const doc = fakeDoc();
  const mount = doc.make('div');
  mount.hidden = true;
  const requested = [];
  const responses = {
    '/api/workspaces/imported/daily-briefs': { history },
    '/api/personal-hq/status': { status: { valid: true, workspace_id: 'current-hq' } },
    '/api/workspaces/imported/daily-briefs/rev-b': {
      revision: { content_json: JSON.stringify({ opening_summary: '<b>Garden</b> day' }) }
    }
  };
  const fetchImpl = async url => {
    requested.push(url);
    return { ok: url in responses, json: async () => responses[url] };
  };
  await wireWorkspaceDailyBriefs({ doc, workspaceId: 'imported', mount, fetchImpl });
  assert.equal(mount.hidden, false);
  const list = mount.children[1];
  assert.equal(list.children.length, 2);
  const newest = list.children[0];
  assert.match(newest.children[0].textContent, /2 versions/);
  assert.ok(!requested.some(url => url.endsWith('/rev-b')), 'briefs load only when opened');
  newest.open = true;
  await newest.listeners.toggle();
  const body = newest.children[1];
  assert.match(body.innerHTML, /&lt;b&gt;Garden&lt;\/b&gt; day/);
  assert.equal(body.dataset.loaded, 'true');
});

test('the HQ’s own page stays unchanged and an ordinary workspace makes one request', async () => {
  const doc = fakeDoc();
  const mount = doc.make('div');
  mount.hidden = true;
  const requested = [];
  const fetchImpl = async url => {
    requested.push(url);
    return { ok: true, json: async () => ({ history: [] }) };
  };
  await wireWorkspaceDailyBriefs({ doc, workspaceId: 'plain', mount, fetchImpl });
  assert.equal(mount.hidden, true);
  assert.deepEqual(requested, ['/api/workspaces/plain/daily-briefs']);
});
