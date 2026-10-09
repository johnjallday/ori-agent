import test from 'node:test';
import assert from 'node:assert/strict';
import {
  currentFolderDiscussion,
  folderDiscussionBinding,
  folderDiscussionOptions,
  folderDiscussionText,
  folderPresentation,
  renderFolderSummary
} from './personal-assistant-folder-presentation.js';
import { folderResponseFixtures as fixtures } from '../../../../../tests/fixtures/assistant-folder-response.js';

test('folder heading owns the root; visible rows stop at three without aggregate project counts', () => {
  const view = folderPresentation(fixtures.album, { locale: 'en-US' });
  assert.equal(view.heading, 'Album collection');
  assert.deepEqual(
    view.visibleRows.map(row => row.name),
    ['Aurora', 'Tide', 'Artwork']
  );
  assert.deepEqual(
    view.moreRows.map(row => row.name),
    ['Notes']
  );
  assert.equal(view.moreLabel, 'Show 1 more observed folder');
  assert.equal(view.date, 'Oct 5, 2026');
  assert.ok(!view.visibleRows.some(row => row.root));
  assert.match(view.details.join(' '), /Whole folder “Album collection”: 12 files/);
  assert.match(view.details.join(' '), /counts overlap/);
});

test('bounded/partial, omitted and historical facts never wait for a disclosure', () => {
  const view = folderPresentation(fixtures.partial, { historical: true });
  assert.match(view.status, /Saved observations · historical · Partial, bounded look/);
  assert.match(view.omission, /4 folder summaries and 2 file-kind summaries omitted/);
  assert.match(view.omission, /not available in Show more/);
  assert.equal(view.moreRows.length, 5);
  assert.match(view.disclosure, /Attached folder: contents not read/);
  assert.match(folderPresentation(fixtures.album).status, /Bounded look/);
  assert.match(
    folderPresentation(fixtures.album, { local: true }).status,
    /Local preview · not sent/
  );
});

test('root-only and no observed files do not claim an exhaustive empty tree', () => {
  for (const observation of [fixtures.rootOnly, fixtures.empty]) {
    const view = folderPresentation(observation);
    assert.equal(view.visibleRows.length, 0);
    assert.equal(view.moreRows.length, 0);
    assert.match(view.empty, /recorded in this bounded snapshot/);
    assert.equal(view.omission, '');
  }
  assert.equal(folderPresentation(fixtures.documents).rootMarker, '');
  assert.match(folderPresentation(fixtures.rootOnly).rootMarker, /marker in the whole folder/);
});

test('literal hostile, duplicate and Unicode names are preserved without fabricated ranges', () => {
  const view = folderPresentation(fixtures.hostile);
  assert.equal(view.heading, fixtures.hostile.folder);
  assert.equal(view.visibleRows[0].name, view.visibleRows[1].name);
  assert.equal(view.visibleRows[2].name, fixtures.hostile.projects[2].name);
  assert.equal(view.moreRows[0].name, fixtures.hostile.projects[3].name);
  assert.ok(!JSON.stringify(view).includes('mixing'));
});

test('unknown date and absent coverage stay conservative, never a live inspection', () => {
  const view = folderPresentation({ folder: 'Legacy', scanned_at: 'invalid', files: 1 });
  assert.equal(view.date, 'Unknown snapshot date');
  assert.equal(view.scannedAt, '');
  assert.match(view.status, /Bounded look/);
  assert.match(view.details.join(' '), /not a complete tree/);
  assert.equal(folderPresentation(null), null);
});

test('discussion binding needs the latest local canonical folder answer, not prose/options/review events', () => {
  const saved = { revision: 'event', observation: fixtures.album };
  const messages = [
    {
      id: 'event',
      role: 'folder_context',
      folder_context: { version: 1, observation: fixtures.album }
    },
    { id: 'user', role: 'user' },
    { id: 'answer', role: 'assistant', content: 'A concise answer' }
  ];
  const binding = folderDiscussionBinding(messages, 'conversation', saved);
  assert.equal(binding.messageId, 'answer');
  for (const rows of [
    [],
    messages.slice(0, 1),
    [...messages, { role: 'folder_context' }],
    messages.map(row => ({ ...row, imported: true })),
    messages.map(row => (row.role === 'assistant' ? { ...row, content: '' } : row))
  ]) {
    assert.equal(folderDiscussionBinding(rows, 'conversation', saved), null);
  }
  assert.equal(
    folderDiscussionBinding(messages, 'conversation', { ...saved, revision: 'new-event' }),
    null
  );
  assert.equal(
    folderDiscussionBinding(messages, 'conversation', {
      ...saved,
      observation: fixtures.documents
    }),
    null
  );
  assert.equal(folderDiscussionBinding(undefined, 'conversation', saved), null);
});

test('discussion activation rechecks owner, saved revision, snapshot, generation and busy selection', () => {
  const state = { conversationId: 'c', revision: 'e', observation: fixtures.album, generation: 4 };
  const binding = {
    conversationId: 'c',
    revision: 'e',
    observationId: fixtures.album.id,
    generation: 4,
    messageId: 'a'
  };
  assert.equal(currentFolderDiscussion(state, binding, 'c'), true);
  for (const change of [
    { conversationId: 'other' },
    { revision: 'new' },
    { observation: fixtures.documents },
    { generation: 5 },
    { preview: fixtures.album },
    { pending: true },
    { selecting: true }
  ]) {
    assert.equal(currentFolderDiscussion({ ...state, ...change }, binding, 'c'), false);
  }
  assert.equal(currentFolderDiscussion(state, binding, 'other'), false);
  assert.equal(currentFolderDiscussion(state, null, 'c'), false);
});

test('local chooser uses all typed non-root observations, independent of setup eligibility', () => {
  const choices = folderDiscussionOptions(fixtures.album);
  assert.deepEqual(
    choices.map(row => row.id),
    fixtures.album.projects.filter(row => !row.root).map(row => row.id)
  );
  assert.deepEqual(folderDiscussionOptions(fixtures.rootOnly), []);
  assert.deepEqual(folderDiscussionOptions(fixtures.empty), []);
  assert.match(folderDiscussionText(fixtures.album), /whole folder/);
  assert.match(
    folderDiscussionText(fixtures.documents, '', { historical: true }),
    /saved metadata observations/
  );
  assert.equal(folderDiscussionText(fixtures.album, fixtures.album.projects[0].id), '');
  assert.equal(folderDiscussionText(fixtures.album, 'missing'), '');
});

test('duplicate names require distinguishable literal markers, never silently select by hidden IDs', () => {
  const project = { id: 'a', name: '<b>同じ名前 🎼</b>', root: false, marker: '*.rpp' };
  const observation = { folder: 'Literal root', projects: [project, { ...project, id: 'b' }] };
  assert.equal(
    folderDiscussionOptions(observation).every(row => row.ambiguous),
    true
  );
  assert.equal(folderDiscussionText(observation, 'a'), '');
  observation.projects[1].marker = '*.logicx';
  assert.equal(
    folderDiscussionOptions(observation).some(row => row.ambiguous),
    false
  );
  assert.ok(folderDiscussionText(observation, 'a').includes(project.name));
  assert.match(folderDiscussionText(observation, 'b'), /logicx/);
});

// Minimal text-only document: no HTML parser. Browser specs check actual markup,
// disclosure keyboard behavior, themes and layout on the built drawer.
function fixtureDocument() {
  const document = {
    createElement: tag => ({
      tag,
      ownerDocument: document,
      children: [],
      textContent: '',
      append(...nodes) {
        this.children.push(...nodes);
      },
      replaceChildren(...nodes) {
        this.children = nodes;
      }
    })
  };
  return document;
}
function allNodes(node) {
  return [
    node,
    ...(node.children || []).flatMap(child => (typeof child === 'string' ? [] : allNodes(child)))
  ];
}

test('local and saved cards build headings, semantic lists and disclosures from text nodes', () => {
  for (const local of [true, false]) {
    const container = fixtureDocument().createElement('section');
    renderFolderSummary(container, fixtures.hostile, { local });
    const nodes = allNodes(container);
    assert.equal(nodes.find(node => node.tag === 'h3').textContent, fixtures.hostile.folder);
    assert.equal(nodes.filter(node => node.tag === 'li').length, 4);
    assert.ok(nodes.some(node => node.tag === 'summary' && node.textContent === 'Scan details'));
    assert.equal(nodes.filter(node => 'innerHTML' in node).length, 0);
    assert.equal(
      nodes.some(node => node.textContent.includes('Send shares')),
      local
    );
  }
});
