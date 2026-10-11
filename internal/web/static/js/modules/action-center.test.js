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
    hidden: false,
    setAttribute(name, value) {
      this[name] = value;
    },
    focus() {},
    addEventListener() {},
    ...overrides
  };
}

// Minimal DOM for state and request ownership. Browser tests exercise actual
// node lifetimes, native navigation, event delegation and focus restoration.
function loadActionCenter(overrides = {}) {
  const elements = {
    '#action-center-list': makeEl(),
    '#action-center-empty': makeEl({ hidden: true }),
    '#action-center-error': makeEl({ hidden: true }),
    '#action-center-retained': makeEl({ hidden: true }),
    '#action-center-results': makeEl(),
    '#action-center-status': makeEl(),
    '#action-center-status-filter': makeEl({ value: '' }),
    '#action-center-sort': makeEl({ value: 'priority' }),
    '#action-center-filter-banner': makeEl(),
    ...(overrides.elements || {})
  };
  const document = {
    readyState: 'loading',
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
    fetch: overrides.fetch || (async () => ({ ok: true, json: async () => ({ items: [] }) })),
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
  assert.match(api.statusChip('planned'), /color: var\(--text-secondary\)/);
  assert.match(api.statusChip('new'), /color: var\(--text-primary\)/);
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
          items: [],
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
      return {
        ok: true,
        json: async () => ({ items: [], item: { id: 'task-9', workspace_id: 'ws-1' } })
      };
    }
  });

  await api.handleAddToBacklog('ws-1', 'o1', makeEl());

  // The first call is the mutation itself; reload()'s own GET refresh follows.
  assert.equal(calls[0].url, '/api/action-center/opportunities/ws-1/o1/add-to-backlog');
  assert.equal(calls[0].method, 'POST');
});

const FINDING = {
  id: 'o1',
  workspace_id: 'ws-1',
  workspace_slug: 'triage-demo',
  title: 'Review launch',
  status: 'new'
};

function response(items = []) {
  return { ok: true, json: async () => ({ items, total: items.length }) };
}

function deferred() {
  let resolve;
  const promise = new Promise(done => {
    resolve = done;
  });
  return { promise, resolve };
}

test('durable empty state survives populated → empty → populated → empty', () => {
  const { api, elements } = loadActionCenter();
  const empty = elements['#action-center-empty'];
  const list = elements['#action-center-list'];
  for (let i = 0; i < 2; i++) {
    api.render([FINDING], '');
    assert.equal(empty.hidden, true);
    assert.equal(list.hidden, false);
    assert.match(list.innerHTML, /Review launch/);
    api.render([], '');
    assert.equal(empty.hidden, false);
    assert.equal(list.hidden, true);
    assert.match(empty.innerHTML, /No active findings/);
    assert.doesNotMatch(empty.innerHTML, /Nothing yet|first/i);
  }
});

test('empty copy and recovery name status and workspace scope, never first use', () => {
  const { api } = loadActionCenter();
  assert.match(api.emptyHTML(''), /No active findings/);
  assert.match(api.emptyHTML(''), /Show all findings/);
  for (const status of ['new', 'resolved', 'dismissed', 'snoozed', 'planned']) {
    assert.match(api.emptyHTML(status), /No matching findings/);
    assert.ok(api.emptyHTML(status).includes(`no ${status} findings`));
  }
  assert.match(api.emptyHTML('all'), /No findings/);
  assert.doesNotMatch(api.emptyHTML('all'), /Show all findings|Clear filters/);
  const scoped = loadActionCenter({ search: '?workspace=ws-1' }).api;
  assert.match(scoped.emptyHTML(''), /No active findings in this workspace/);
  assert.match(scoped.emptyHTML('all'), /No matching findings/);
  assert.match(scoped.emptyHTML('all'), /href="\/action-center\?status=all"/);
});

test('failure retains explicitly labelled rows, hides empty success, and retry recovers', async () => {
  let fail = false;
  const { api, elements } = loadActionCenter({
    fetch: async () => {
      if (fail) throw new Error('offline');
      return response([FINDING]);
    }
  });
  await api.reload();
  fail = true;
  elements['#action-center-status-filter'].value = 'resolved';
  assert.equal(await api.reload(), false);
  assert.equal(elements['#action-center-error'].hidden, false);
  assert.equal(elements['#action-center-empty'].hidden, true);
  assert.equal(elements['#action-center-retained'].hidden, false);
  assert.match(elements['#action-center-retained'].textContent, /last-loaded findings: Active/);
  assert.match(elements['#action-center-list'].innerHTML, /Review launch/);
  fail = false;
  await api.reload();
  assert.equal(elements['#action-center-error'].hidden, true);
  assert.equal(elements['#action-center-retained'].hidden, true);
});

test('initial failure and malformed success do not imply an empty collection', async () => {
  for (const fetch of [
    async () => ({ ok: false }),
    async () => ({ ok: true, json: async () => ({}) })
  ]) {
    const { api, elements } = loadActionCenter({ fetch });
    assert.equal(await api.reload(), false);
    assert.equal(elements['#action-center-error'].hidden, false);
    assert.equal(elements['#action-center-empty'].hidden, true);
    assert.equal(elements['#action-center-retained'].hidden, true);
  }
});

test('latest selection owns rows, library, status, empty state and loading after stale success or failure', async () => {
  for (const oldResponse of [response([FINDING]), { ok: false }]) {
    const oldList = deferred();
    const oldLibrary = deferred();
    let listCalls = 0;
    let libraryCalls = 0;
    const section = makeEl({ hidden: true });
    const { api, elements } = loadActionCenter({
      elements: { '#action-center-library': section, '#action-center-library-cards': makeEl() },
      fetch: url =>
        url.endsWith('/library')
          ? ++libraryCalls === 1
            ? oldLibrary.promise
            : Promise.resolve(response([]))
          : ++listCalls === 1
            ? oldList.promise
            : Promise.resolve(response([]))
    });
    const old = api.reload();
    elements['#action-center-status-filter'].value = 'resolved';
    await api.reload();
    oldList.resolve(oldResponse);
    oldLibrary.resolve(response([LIBRARY_CARD]));
    await old;
    assert.equal(elements['#action-center-list'].innerHTML, '');
    assert.match(elements['#action-center-empty'].innerHTML, /no resolved findings/);
    assert.equal(elements['#action-center-error'].hidden, true);
    assert.equal(elements['#action-center-status'].textContent, '0 findings');
    assert.equal(elements['#action-center-results']['aria-busy'], 'false');
    assert.equal(section.hidden, true);
  }
});

test('findings request captures current sort/status and workspace before awaiting', async () => {
  const calls = [];
  const { api, elements } = loadActionCenter({
    search: '?workspace=ws-1',
    fetch: async url => {
      calls.push(url);
      return response([]);
    }
  });
  elements['#action-center-status-filter'].value = 'planned';
  elements['#action-center-sort'].value = 'recency';
  await api.reload();
  assert.ok(
    calls.includes('/api/action-center/opportunities?status=planned&sort=recency&workspace=ws-1')
  );
});

test('titles are native links; missing owner slugs never produce fake destinations', () => {
  const { api } = loadActionCenter();
  assert.match(
    api.rowHTML(FINDING),
    /<a href="\/workspaces\/triage-demo" data-action="open">Review launch<\/a>/
  );
  const unavailable = api.rowHTML({
    ...FINDING,
    workspace_slug: '',
    status: 'planned',
    linked_task_id: 't1'
  });
  assert.match(unavailable, /Workspace unavailable/);
  assert.match(unavailable, /Backlog workspace unavailable/);
  assert.doesNotMatch(unavailable, /href="#"|data-action="open"|data-action="add-to-backlog"/);
});

test('ordinary, modified and middle title clicks mark seen without overriding browser navigation', async () => {
  const calls = [];
  const { api } = loadActionCenter({
    fetch: async (url, options) => {
      calls.push({ url, options });
      throw new Error('seen endpoint unavailable');
    }
  });
  const row = { dataset: { ws: 'ws 1', id: 'o/1' } };
  const target = {
    closest: sel => (sel === '.action-center-row' ? row : { dataset: { action: 'open' } })
  };
  for (const event of [
    { type: 'click', button: 0 },
    { type: 'click', button: 0, metaKey: true },
    { type: 'auxclick', button: 1 }
  ]) {
    api.handleRowClick({
      ...event,
      target,
      preventDefault() {
        assert.fail('must keep native links');
      }
    });
  }
  api.handleRowClick({ type: 'auxclick', button: 2, target });
  await Promise.resolve();
  assert.equal(calls.length, 3);
  for (const call of calls) {
    assert.equal(call.url, '/api/action-center/opportunities/ws%201/o%2F1');
    assert.equal(call.options.keepalive, true);
  }
});

test('Add to Backlog cannot overwrite a failed reload with success copy', async () => {
  const { api, elements } = loadActionCenter({
    fetch: async (_url, options) =>
      options?.method === 'POST'
        ? { ok: true, json: async () => ({ item: { id: 't1' }, workspace_slug: 'triage-demo' }) }
        : { ok: false }
  });
  const button = makeEl({ textContent: 'Add to Backlog' });
  await api.handleAddToBacklog('ws-1', 'o1', button);
  assert.match(elements['#action-center-status'].textContent, /could not be refreshed/);
  assert.equal(elements['#action-center-error'].hidden, false);
  assert.equal(button.disabled, false);
});
