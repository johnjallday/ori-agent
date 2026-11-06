import test from 'node:test';
import assert from 'node:assert/strict';
import {
  visibleTreeRows,
  renderTreeHTML,
  updateTreeWorkspaceBadges
} from './home-workspace-tree.js';
import { workspaceUpdateSignals } from './workspace-update-signals.js';

function badge({ seen = false, pending = 2 } = {}) {
  return workspaceUpdateSignals({
    state: 'ready',
    data: {
      workspace_id: 'ws',
      name: 'Desk',
      folder_slug: 'desk',
      welcome: {
        state: 'ready',
        unread_count: seen ? 0 : 1,
        agent_name: 'Mira',
        agent_instance_id: 'lead',
        session_id: 'chat',
        messages: [{ message_id: 'hello', revision: 1 }]
      },
      review: { state: 'ready', root_id: 'root', batch_id: 'batch', pending_count: pending }
    }
  });
}

test('Tree update links follow the roving row and do not change its ticket marker or group hierarchy', () => {
  const rows = visibleTreeRows(
    [
      {
        id: 'group',
        name: 'Group',
        kind: 'group',
        children: [{ id: 'ws', name: 'Desk', kind: 'workspace', needs_attention_count: 7 }]
      }
    ],
    new Set()
  );
  const html = renderTreeHTML(rows, {
    activeId: 'ws',
    tabbableId: 'ws',
    workspaceUpdateBadges: { ws: badge() }
  });
  assert.match(html, /role="treeitem"/);
  assert.match(html, /cockpit-tree-badge">7<\/span>/);
  assert.match(html, /Needs attention/);
  assert.match(html, /New message/);
  assert.match(html, /2 files to review/);
  assert.match(html, /data-workspace-update="welcome"[^>]*tabindex="0"/);
  assert.match(html, /data-workspace-update="review"[^>]*tabindex="0"/);
  const otherRow = renderTreeHTML(rows, {
    activeId: 'group',
    tabbableId: 'group',
    workspaceUpdateBadges: { ws: badge() }
  });
  assert.match(otherRow, /data-workspace-update="welcome"[^>]*tabindex="-1"/);
  const wrong = renderTreeHTML(rows, {
    tabbableId: 'ws',
    workspaceUpdateBadges: { ws: { ...badge(), workspaceId: 'other' } }
  });
  assert.doesNotMatch(wrong, /data-workspace-update="welcome"/);
});

test('Tree refresh patches only markers, retains focused controls, and leaves another input and scroll alone', t => {
  const original = globalThis.document;
  const document = { activeElement: null };
  globalThis.document = document;
  t.after(() => {
    if (original === undefined) delete globalThis.document;
    else globalThis.document = original;
  });
  const focused = [];
  const row = {
    getAttribute: () => '0',
    focus: options => {
      assert.equal(options.preventScroll, true);
      focused.push('row');
      document.activeElement = row;
    }
  };
  const link = kind => ({
    getAttribute: name => (name === 'data-workspace-update' ? kind : null),
    focus: options => {
      assert.equal(options.preventScroll, true);
      focused.push(kind);
      document.activeElement = links[kind];
    }
  });
  const links = { welcome: link('welcome'), review: link('review') };
  let html = '';
  let writes = 0;
  const host = {
    getAttribute: () => 'ws',
    closest: () => row,
    get innerHTML() {
      return html;
    },
    set innerHTML(value) {
      writes++;
      html = value;
    },
    contains: node => Object.values(links).includes(node),
    querySelectorAll: () =>
      Object.entries(links)
        .filter(([kind]) => html.includes(`data-workspace-update="${kind}"`))
        .map(([, value]) => value)
  };
  const container = {
    scrollTop: 73,
    querySelectorAll: selector => {
      assert.equal(selector, '[data-tree-updates]');
      return [host];
    },
    set innerHTML(_) {
      throw new Error('must not remount the tree');
    }
  };
  document.activeElement = links.welcome;
  updateTreeWorkspaceBadges(container, { ws: badge() });
  assert.deepEqual(focused, ['welcome']);
  updateTreeWorkspaceBadges(container, { ws: badge() });
  assert.equal(writes, 1);
  assert.equal(focused.length, 1, 'no repeated focus/announcement on identical observations');
  updateTreeWorkspaceBadges(container, { ws: badge({ seen: true }) });
  assert.deepEqual(
    focused,
    ['welcome', 'row'],
    'a removed message action returns focus to its row'
  );
  const draft = { value: 'unsent note' };
  document.activeElement = draft;
  updateTreeWorkspaceBadges(container, { ws: badge({ seen: true, pending: 1 }) });
  assert.equal(document.activeElement, draft);
  assert.equal(draft.value, 'unsent note');
  assert.equal(container.scrollTop, 73);
  assert.equal(focused.length, 2);
});
