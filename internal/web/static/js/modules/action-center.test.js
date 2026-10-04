import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./action-center.js', import.meta.url), 'utf8');

function makeEl(overrides = {}) {
  return {
    textContent: '',
    innerHTML: '',
    style: {},
    value: '',
    disabled: false,
    appendChild() {},
    addEventListener() {},
    ...overrides
  };
}

// Loads the module with a minimal fake DOM. Deliberately omits
// '#action-center-list' so init()'s auto-wiring/reload() no-ops on load —
// tests drive the exported window.ActionCenter surface directly instead.
function loadActionCenter(overrides = {}) {
  const elements = {
    '#action-center-status': makeEl(),
    '#action-center-status-filter': makeEl({ value: '' }),
    '#action-center-sort': makeEl({ value: 'priority' }),
    '#action-center-filter-banner': makeEl(),
    ...(overrides.elements || {})
  };
  const document = {
    readyState: 'complete',
    addEventListener() {},
    querySelector: sel => elements[sel] || null,
    querySelectorAll: () => []
  };
  const window = { location: { search: overrides.search || '' } };
  const context = {
    console,
    window,
    document,
    URLSearchParams,
    fetch: overrides.fetch || (async () => ({ ok: true, json: async () => ({}) })),
    bootstrap: undefined
  };
  vm.runInNewContext(source, context, { filename: 'action-center.js' });
  return { api: window.ActionCenter, elements };
}

test('rowHTML shows Add to Backlog for a non-planned finding (FR26)', () => {
  const { api } = loadActionCenter();
  const html = api.rowHTML({
    id: 'o1',
    workspace_id: 'ws-1',
    title: 'Brand voice drift',
    status: 'new'
  });
  assert.match(html, /data-action="add-to-backlog"/);
  assert.match(html, />Add to Backlog</);
  assert.ok(!html.includes('View in Backlog'), 'not yet planned, so no linked-item shortcut');
});

test('rowHTML marks assistant suggestions with escaped provenance, confidence, evidence, and source navigation', () => {
  const { api } = loadActionCenter();
  const html = api.rowHTML({
    id: 'o-assistant',
    workspace_id: 'ws-1',
    workspace_slug: 'song-one',
    workspace_name: 'Song One',
    source_type: 'assistant_suggestion',
    source_label: '<Producer & Co>',
    source_url: '/workspaces/song-one/assistant',
    title: 'Review the pattern',
    summary: 'Repeated workflow preference',
    evidence: '<script>alert(1)</script>\nThree linked projects',
    confidence: 'high',
    status: 'new'
  });
  assert.match(html, /Assistant suggestion/);
  assert.match(html, /&lt;Producer &amp; Co&gt;/);
  assert.match(html, /high confidence/);
  assert.match(html, /Three linked projects/);
  assert.match(html, /href="\/workspaces\/song-one\/assistant"/);
  assert.ok(!html.includes('<script>'));
});

test('assistantSourceHTML refuses client-forged external source URLs', () => {
  const { api } = loadActionCenter();
  const html = api.assistantSourceHTML({
    source_type: 'assistant_suggestion',
    source_url: 'javascript:alert(1)',
    evidence: 'Evidence'
  });
  assert.ok(!html.includes('javascript:'));
  assert.ok(!html.includes('Open source'));
});

test('a Daily Brief ready item links to the Daily Brief station in My HQ', () => {
  const { api } = loadActionCenter();
  const html = api.rowHTML({
    id: 'o-brief',
    workspace_id: 'hq-uuid',
    workspace_slug: 'my-hq',
    workspace_name: 'My HQ',
    title: 'Daily Brief ready — 2026-10-07',
    summary: 'Your scheduled Daily Brief has been generated.',
    source_url: '/workspaces/my-hq?station=daily-brief',
    status: 'new'
  });
  assert.match(html, /<a href="\/workspaces\/my-hq\?station=daily-brief">Open Daily Brief<\/a>/);
  // It is not an assistant suggestion, so it carries none of that framing.
  assert.ok(!html.includes('Assistant suggestion'));
  assert.ok(!html.includes('Open source'));
});

test('dailyBriefLinkHTML accepts exactly the station link and nothing broader', () => {
  const { api } = loadActionCenter();
  for (const source_url of [
    '/workspaces/my-hq',
    '/workspaces/my-hq?station=watchtower',
    '/workspaces/my-hq?station=daily-brief&next=//evil.example',
    '/workspaces/my-hq/assistant?station=daily-brief',
    '/workspaces/my-hq?station=daily-brief#x',
    '/workspaces/My HQ?station=daily-brief',
    'https://evil.example/workspaces/my-hq?station=daily-brief',
    '//evil.example/workspaces/my-hq?station=daily-brief',
    'javascript:alert(1)',
    ''
  ]) {
    assert.equal(api.dailyBriefLinkHTML({ source_url }), '', source_url);
  }
  // An assistant suggestion's own link is untouched by this rule.
  const suggestion = api.rowHTML({
    id: 'o-assistant',
    workspace_id: 'ws-1',
    source_type: 'assistant_suggestion',
    source_url: '/workspaces/song-one/assistant',
    title: 'Review the pattern',
    status: 'new'
  });
  assert.match(suggestion, /href="\/workspaces\/song-one\/assistant"[^>]*>Open source</);
  assert.ok(!suggestion.includes('Open Daily Brief'));
});

test("rowHTML shows a View in Backlog deep link once planned, using Group 5's panel=backlog contract (FR26, 29, 59)", () => {
  const { api } = loadActionCenter();
  const html = api.rowHTML({
    id: 'o1',
    workspace_id: 'workspace-uuid',
    workspace_slug: 'marketing-site',
    title: 'Brand voice drift',
    status: 'planned',
    linked_task_id: 'task-9',
    linked_workspace_id: 'workspace-uuid',
    linked_workspace_slug: 'marketing-site'
  });
  assert.ok(
    !html.includes('data-action="add-to-backlog"'),
    'no duplicate-capture affordance once planned'
  );
  assert.match(html, /href="\/workspaces\/marketing-site\?panel=backlog&task=task-9"/);
  assert.match(html, />View in Backlog</);
});

test('statusChip labels "planned" distinctly and mutes it like resolved/dismissed', () => {
  const { api } = loadActionCenter();
  assert.match(api.statusChip('planned'), />Planned</);
  assert.match(api.statusChip('planned'), /opacity: 0\.6/);
  assert.match(api.statusChip('new'), /opacity: 1/);
});

test('handleAddToBacklog shows a pending state on the clicked button while the request is in flight', async () => {
  let resolveFetch;
  const pending = new Promise(resolve => {
    resolveFetch = resolve;
  });
  const { api, elements } = loadActionCenter({
    fetch: async () => {
      await pending;
      return {
        ok: true,
        json: async () => ({
          status: 'planned',
          workspace_slug: 'marketing-site',
          item: { id: 'task-9', workspace_id: 'workspace-uuid' }
        })
      };
    }
  });
  const button = makeEl({ textContent: 'Add to Backlog' });

  const done = api.handleAddToBacklog('ws-1', 'o1', button);
  assert.equal(button.disabled, true, 'button disabled while in flight');
  assert.equal(button.textContent, 'Adding…');

  resolveFetch();
  await done;
  assert.equal(elements['#action-center-status'].innerHTML.includes('Added to backlog.'), true);
  assert.match(
    elements['#action-center-status'].innerHTML,
    /href="\/workspaces\/marketing-site\?panel=backlog&task=task-9"/
  );
  assert.match(elements['#action-center-status'].innerHTML, />Open item</);
});

test('handleAddToBacklog re-enables the button and shows an error status on failure', async () => {
  const { api, elements } = loadActionCenter({
    fetch: async () => ({ ok: false, json: async () => ({ message: 'workspace is trashed' }) })
  });
  const button = makeEl({ textContent: 'Add to Backlog' });

  await api.handleAddToBacklog('ws-1', 'o1', button);

  assert.equal(button.disabled, false, 'button re-enabled after failure');
  assert.equal(button.textContent, 'Add to Backlog', 'label restored after failure');
  assert.match(
    elements['#action-center-status'].textContent,
    /Add to Backlog failed: workspace is trashed/
  );
});

const LIBRARY_CARD = {
  home_id: 'home-1',
  home_name: 'Music <Home>',
  route: '/workspaces/music-home/assistant#projectLibraryProposals',
  projects: 6,
  new: 6,
  activatable: 5,
  ready_proposals: 1,
  coverage: 'complete'
};

test('a Home library card only links to that Home’s suggestions shelf', () => {
  const { api } = loadActionCenter();
  const html = api.libraryCardHTML(LIBRARY_CARD);
  assert.match(html, /<strong>Music &lt;Home&gt;<\/strong>/);
  assert.match(html, /6 new · 5 ready to set up · 1 suggestion to review/);
  assert.match(
    html,
    /<a class="modern-btn modern-btn-secondary action-center-library-open" href="\/workspaces\/music-home\/assistant#projectLibraryProposals" aria-label="Open Music &lt;Home&gt; suggestions shelf">Open shelf<\/a>/
  );
  assert.doesNotMatch(html, /data-action=/, 'no dismiss, snooze or resolve on a derived card');
  assert.match(api.libraryCardHTML({ ...LIBRARY_CARD, coverage: 'partial' }), /partial scan/);
  for (const route of [
    'https://evil.example/workspaces/x/assistant#projectLibraryProposals',
    '/workspaces/x/assistant',
    'javascript:alert(1)'
  ])
    assert.equal(api.libraryCardHTML({ ...LIBRARY_CARD, route }), '', route);
  assert.equal(
    api.libraryCardHTML({ ...LIBRARY_CARD, activatable: 0, ready_proposals: 0 }),
    '',
    'nothing ready: no card'
  );
});

test('renderLibrary shows the section only with a card and respects the workspace filter', () => {
  const section = makeEl({ hidden: true });
  const container = makeEl();
  const elements = {
    '#action-center-library': section,
    '#action-center-library-cards': container
  };
  const { api } = loadActionCenter({ elements });
  api.renderLibrary([
    LIBRARY_CARD,
    { ...LIBRARY_CARD, home_id: 'home-2', activatable: 0, ready_proposals: 0 }
  ]);
  assert.equal(section.hidden, false);
  assert.equal((container.innerHTML.match(/<article /g) || []).length, 1);
  api.renderLibrary([]);
  assert.equal(section.hidden, true);
  assert.equal(container.innerHTML, '');

  const filtered = loadActionCenter({ elements, search: '?workspace=home-2' });
  filtered.api.renderLibrary([LIBRARY_CARD]);
  assert.equal(section.hidden, true, 'a workspace-scoped view hides other Homes');
});

test('handleAddToBacklog posts to the add-to-backlog endpoint, not resolve/dismiss', async () => {
  const calls = [];
  const { api } = loadActionCenter({
    fetch: async (url, options) => {
      calls.push({ url, method: options?.method });
      return { ok: true, json: async () => ({ item: { id: 'task-9', workspace_id: 'ws-1' } }) };
    }
  });

  await api.handleAddToBacklog('ws-1', 'o1', makeEl());

  // The first call is the mutation itself; reload()'s own GET refresh follows.
  assert.equal(calls[0].url, '/api/action-center/opportunities/ws-1/o1/add-to-backlog');
  assert.equal(calls[0].method, 'POST');
});
