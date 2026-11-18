import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./note-page-tickets.js', import.meta.url), 'utf8');
function element() {
  return {
    children: [],
    hidden: false,
    dataset: {},
    textContent: '',
    appendChild(child) {
      this.children.push(child);
    },
    replaceChildren(...children) {
      this.children = children;
    }
  };
}
function setup(read) {
  const root = element(),
    section = element(),
    list = element();
  root.dataset = { workspaceId: 'workspace-uuid', noteId: 'note-a' };
  const nodes = { noteMainContent: root, notePageTickets: section, notePageTicketsList: list };
  const context = {
    document: {
      readyState: 'loading',
      addEventListener() {},
      getElementById: id => nodes[id],
      createElement: () => element()
    },
    window: { WorkspaceHubTickets: { api: { ticketsForNote: read } } }
  };
  vm.runInNewContext(source, context);
  return { root, section, list, load: context.window.NotePageTickets.load };
}
const ticket = title => ({
  id: 'ticket/a',
  owningWorkspaceSlug: 'visible-slug',
  owningWorkspaceName: 'Fictional studio',
  displayNumber: '#1',
  state: 'backlog',
  stateLabel: 'Backlog',
  title
});
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

test('linked tickets read canonical UUID context and retain a native slug anchor with escaped text', async () => {
  const view = setup(async (workspace, note) => {
    assert.equal(workspace, 'workspace-uuid');
    assert.equal(note, 'note-a');
    return [ticket('<untrusted>')];
  });
  await view.load();
  assert.equal(view.section.hidden, false);
  const link = view.list.children[0].children[0];
  assert.equal(link.href, '/workspaces/visible-slug?ticket=ticket%2Fa');
  assert.equal(link.children[2].textContent, '<untrusted>');
});

for (const fails of [false, true])
  test(`an obsolete note read ${fails ? 'failure' : 'success'} cannot replace or hide the selected note links`, async () => {
    const old = deferred(),
      next = deferred();
    const view = setup((_workspace, note) => (note === 'note-a' ? old.promise : next.promise));
    const first = view.load();
    view.root.dataset.noteId = 'note-b';
    const second = view.load();
    next.resolve([ticket('Note B ticket')]);
    await second;
    if (fails) old.reject(new Error('Obsolete read failed'));
    else old.resolve([ticket('Note A ticket')]);
    await first;
    assert.equal(view.section.hidden, false);
    assert.equal(view.list.children[0].children[0].children[2].textContent, 'Note B ticket');
  });

test('clearing the selected note removes stale links without issuing another read', async () => {
  let reads = 0;
  const view = setup(async () => {
    reads++;
    return [ticket('A')];
  });
  await view.load();
  view.root.dataset.noteId = '';
  await view.load();
  assert.equal(reads, 1);
  assert.equal(view.section.hidden, true);
  assert.equal(view.list.children.length, 0);
});
