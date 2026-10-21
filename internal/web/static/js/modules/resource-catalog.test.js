import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ResourceCatalogPage,
  attemptLine,
  catalogActions,
  categoryStateLine,
  emptyCategoryLine,
  entryStateLabel,
  operationLine,
  providerLine,
  rowContext,
  splitTags
} from './resource-catalog.js';

const DATE = { locale: 'en-US', timeZone: 'UTC' };

test('category states read as the contract names them', () => {
  assert.equal(categoryStateLine({ state: 'not_inspected' }), 'Not inspected');
  assert.match(
    categoryStateLine({ state: 'inspected', inspected_at: '2026-10-21T09:00:00Z' }, DATE),
    /^Inspected Oct 21, 2026/
  );
  assert.match(
    categoryStateLine({ state: 'partial', inspected_at: '2026-10-21T09:00:00Z' }, DATE),
    /^Partial/
  );
  assert.match(
    categoryStateLine({ state: 'refresh_failed', inspected_at: '2026-10-21T09:00:00Z' }, DATE),
    /^Refresh failed/
  );
  assert.equal(categoryStateLine({ state: 'unavailable' }), 'Unavailable');
  assert.equal(categoryStateLine({ state: 'access_withdrawn' }), 'Access withdrawn');
  assert.match(categoryStateLine({ state: 'history' }), /not read on this computer/);
});

test('an empty category says why, never that something must be installed', () => {
  for (const state of [
    'not_inspected',
    'inspected',
    'access_withdrawn',
    'unavailable',
    'refresh_failed'
  ]) {
    const line = emptyCategoryLine({ state }, false);
    assert.ok(line.length > 0, state);
    assert.doesNotMatch(line, /install/i, state);
  }
  assert.equal(emptyCategoryLine({ state: 'inspected' }, true), 'Nothing matches this search.');
  // An unread category says why each approved source was not read.
  const sources = [
    {
      category: 'audio_plugins',
      label: 'REAPER plugin list',
      approved: true,
      last_attempt: { outcome: 'unavailable', reason: 'app_closed' }
    },
    { category: 'templates', label: 'Project templates', approved: true }
  ];
  const line = emptyCategoryLine({ id: 'audio_plugins', state: 'unavailable' }, false, sources);
  assert.match(line, /REAPER plugin list: Needs the application open\./);
  assert.doesNotMatch(line, /Project templates|install/i);
});

test('provider and entry states have their own words', () => {
  assert.equal(providerLine({ available: true }), '');
  assert.match(providerLine({ available: false, reason: 'provider_disabled' }), /switched off/);
  assert.match(providerLine({ reason: 'detached' }), /no longer attached/);
  assert.equal(entryStateLabel('present'), '');
  assert.equal(entryStateLabel('no_longer_found'), 'No longer found');
  assert.equal(entryStateLabel('access_withdrawn'), 'Access withdrawn');
});

test('a source line says how its last read ended', () => {
  assert.equal(attemptLine({ approved: true }), 'Approved; not read yet');
  assert.equal(attemptLine({ withdrawn: true }), 'Access withdrawn');
  assert.match(
    attemptLine(
      {
        last_attempt: { outcome: 'complete', entries_read: 4, finished_at: '2026-10-21T09:00:00Z' }
      },
      DATE
    ),
    /4 found/
  );
  assert.match(
    attemptLine({ last_attempt: { outcome: 'partial', reason: 'limit', entries_read: 20000 } }),
    /first 20000 · Continue/
  );
  assert.match(
    attemptLine({ last_attempt: { outcome: 'unavailable', reason: 'app_closed' } }),
    /application open/
  );
  assert.match(attemptLine({ last_attempt: { outcome: 'failed' } }), /earlier results kept/);
  // What a complete read passed over is part of its coverage.
  assert.match(
    attemptLine({
      last_attempt: {
        outcome: 'complete',
        entries_read: 3,
        skipped: [
          { reason: 'external_registration', count: 1 },
          { reason: 'symlink', count: 2 }
        ]
      }
    }),
    /3 found · 3 skipped: 1 registered from outside the folder, 2 symlinked$/
  );
  // Every reason reads after a count of one.
  assert.match(
    attemptLine({
      last_attempt: {
        outcome: 'complete',
        entries_read: 1,
        skipped: [
          { reason: 'symlink', count: 1 },
          { reason: 'unsupported_name', count: 1 }
        ]
      }
    }),
    /1 found · 2 skipped: 1 symlinked, 1 with an unusable name$/
  );
});

test('rows keep same-named entries apart by kind, source and known facts', () => {
  const row = {
    kind_label: 'Script',
    source_label: 'REAPER Scripts',
    facts: [
      { label: 'Language', value: 'Lua', known: true },
      { label: 'Package', value: 'Unknown', known: false }
    ]
  };
  assert.equal(rowContext(row), 'Script · REAPER Scripts · Lua');
  // A yes reads as what is true; a no stays in the detail.
  const registered = value => ({
    ...row,
    facts: [...row.facts, { label: 'Registered in REAPER', value, known: true }]
  });
  assert.equal(
    rowContext(registered('Yes')),
    'Script · REAPER Scripts · Lua · Registered in REAPER'
  );
  assert.equal(rowContext(registered('No')), 'Script · REAPER Scripts · Lua');
  // The server adds the plugin's identity only when the name repeats.
  assert.equal(
    rowContext({
      kind_label: 'Project template',
      source_label: 'Project templates',
      identity_hint: 'Band/Vocals.RPP'
    }),
    'Project template · Project templates · Band/Vocals.RPP'
  );
});

test('actions follow the catalog state', () => {
  assert.deepEqual(catalogActions({ can_inspect: false }), []);
  assert.deepEqual(
    catalogActions({ can_inspect: true, approved: false, sources: [] }).map(action => action.id),
    ['review']
  );
  assert.deepEqual(
    catalogActions({
      can_inspect: true,
      approved: true,
      sources: [{ approved: true, can_continue: true }]
    }).map(action => action.id),
    ['refresh', 'continue', 'review']
  );
  assert.deepEqual(
    catalogActions({
      can_inspect: true,
      approved: true,
      sources: [],
      operation: { state: 'running' }
    }).map(action => action.id),
    ['cancel']
  );
});

test('an operation line never calls a cancelled inspection complete', () => {
  const sources = [
    { state: 'complete', entries: 3 },
    { state: 'cancelled', entries: 0 }
  ];
  assert.match(
    operationLine({ state: 'cancelled', sources }),
    /^Cancelled · 2 of 2 sources finished/
  );
  assert.match(
    operationLine({ state: 'running', sources: [{ state: 'reading', entries: 2 }] }),
    /Inspecting… 0 of 1/
  );
  assert.equal(operationLine({ state: 'published', sources }), '');
});

function fakeDocument() {
  const elements = new Map();
  const make = tag => {
    const node = {
      tag,
      children: [],
      dataset: {},
      attributes: {},
      listeners: {},
      hidden: false,
      textContent: '',
      value: '',
      checked: false,
      append(...nodes) {
        this.children.push(...nodes);
      },
      replaceChildren(...nodes) {
        this.children = nodes;
      },
      setAttribute(name, value) {
        this.attributes[name] = value;
      },
      addEventListener(type, fn) {
        this.listeners[type] = fn;
      },
      querySelectorAll(selector) {
        const all = [];
        const walk = item => {
          for (const child of item.children || []) {
            if (selector === 'input:checked' && child.tag === 'input' && child.checked)
              all.push(child);
            walk(child);
          }
        };
        walk(this);
        return all;
      },
      showModal() {
        this.open = true;
      },
      close() {
        this.open = false;
      }
    };
    return node;
  };
  const doc = {
    createElement: make,
    createTextNode: text => ({ tag: '#text', textContent: text, children: [] }),
    getElementById(id) {
      if (!elements.has(id)) elements.set(id, make('div'));
      return elements.get(id);
    }
  };
  return doc;
}

function texts(node) {
  return [node.textContent, ...(node.children || []).flatMap(texts)].filter(Boolean);
}

const OVERVIEW = {
  studio_id: 'studio-1',
  name: 'REAPER Studio',
  attached: true,
  provider: { available: true },
  can_inspect: true,
  approved: false,
  revision: 0,
  categories: [
    {
      id: 'templates',
      label: 'Templates',
      description: 'Project templates.',
      state: 'not_inspected',
      counts: { present: 0 },
      kinds: [],
      facts: []
    },
    {
      id: 'scripts',
      label: 'Scripts',
      description: 'Scripts.',
      state: 'not_inspected',
      counts: { present: 0 },
      kinds: [],
      facts: []
    }
  ],
  sources: []
};

test('the page loads, offers Inspect, and approves only the sources the owner kept', async () => {
  const doc = fakeDocument();
  const requests = [];
  const respond = (body, ok = true) => ({ ok, status: ok ? 200 : 409, json: async () => body });
  const fetchImpl = async (url, options = {}) => {
    requests.push({ url, method: options.method || 'GET', body: options.body });
    if (url.endsWith('/resource-catalog')) return respond(OVERVIEW);
    if (url.includes('/resource-catalog/entries?')) return respond({ rows: [], total: 0 });
    if (url.endsWith('/sources/review')) {
      return respond({
        review_token: 'rv-1',
        installation: { label: 'REAPER 7.28' },
        sources: [
          {
            source_id: 'project_templates',
            category_label: 'Templates',
            label: 'Project templates',
            location: '~/T',
            state: 'ready'
          },
          {
            source_id: 'reaper_scripts',
            category_label: 'Scripts',
            label: 'REAPER Scripts',
            location: '~/S',
            state: 'ready'
          }
        ]
      });
    }
    if (url.endsWith('/sources/commit'))
      return respond({ operation: { id: 'op-1', state: 'running' } });
    return respond({}, false);
  };
  const page = new ResourceCatalogPage({
    workspaceId: 'studio-1',
    workspaceSlug: 'reaper-studio',
    fetchImpl,
    documentImpl: doc,
    locationImpl: { search: '' }
  });
  await page.init();
  assert.equal(requests[0].url, '/api/workspaces/studio-1/resource-catalog');
  assert.ok(
    !requests.some(request => request.url.includes('/inspections')),
    'opening the page started nothing'
  );
  const tabs = doc.getElementById('resourceCatalogTabs');
  assert.equal(tabs.children.length, 2);
  assert.equal(tabs.children[0].attributes['aria-selected'], 'true');
  assert.ok(
    texts(doc.getElementById('resourceCatalogEmpty')).some(text => /Not inspected yet/.test(text))
  );
  const action = doc.getElementById('resourceCatalogActions').children[0];
  assert.equal(action.textContent, 'Inspect resources');

  await page.act('review');
  const review = doc.getElementById('resourceCatalogReview');
  assert.equal(review.open, true);
  const boxes = doc
    .getElementById('resourceCatalogReviewSources')
    .children.map(label => label.children[0]);
  assert.equal(boxes.length, 2);
  boxes[1].checked = false; // the owner keeps only templates
  await page.commitReview();
  const commit = requests.find(request => request.url.endsWith('/sources/commit'));
  const body = JSON.parse(commit.body);
  assert.deepEqual(body.source_ids, ['project_templates']);
  assert.equal(body.review_token, 'rv-1');
  assert.equal(review.open, false);
});

test('tags split on commas and drop blanks', () => {
  assert.deepEqual(splitTags(' live, vocals ,, '), ['live', 'vocals']);
  assert.deepEqual(splitTags(''), []);
});

test('saving notes sends the read revision and a conflict keeps the owner text', async () => {
  const doc = fakeDocument();
  const writes = [];
  let conflict = true;
  const detail = {
    entry_id: 'rc_1',
    name: 'Lyrics',
    kind_label: 'Script',
    facts: [],
    favorite: false,
    tags: [],
    note: 'Old',
    annotation_revision: 3
  };
  const fetchImpl = async (url, options = {}) => {
    if (url.endsWith('/entries/rc_1')) return { ok: true, status: 200, json: async () => detail };
    if (url.endsWith('/entries/rc_1/annotation')) {
      writes.push(JSON.parse(options.body));
      if (conflict) {
        conflict = false;
        return {
          ok: false,
          status: 409,
          json: async () => ({
            code: 'annotation_conflict',
            current: { note: 'From another tab', revision: 4 }
          })
        };
      }
      return { ok: true, status: 200, json: async () => ({ annotation: { revision: 5 } }) };
    }
    return { ok: true, status: 200, json: async () => ({ rows: [] }) };
  };
  const page = new ResourceCatalogPage({
    workspaceId: 's1',
    fetchImpl,
    documentImpl: doc,
    locationImpl: { search: '' }
  });
  page.view = { categories: [], sources: [] };
  await page.showDetail('rc_1');
  const panel = doc.getElementById('resourceCatalogDetail');
  const form = panel.children.find(child => child.className === 'resource-catalog-annotation');
  const note = form.children[3].children[1];
  const status = form.children[5];
  note.value = 'Mine';
  await form.listeners.submit({ preventDefault() {} });
  assert.equal(writes[0].base_revision, 3);
  assert.equal(writes[0].note, 'Mine');
  assert.match(status.textContent, /From another tab/);
  assert.equal(note.value, 'Mine', 'the owner text stays in the field');
  await form.listeners.submit({ preventDefault() {} });
  assert.equal(writes[1].base_revision, 4, 'saving again replaces knowingly');
  assert.equal(status.textContent, 'Saved.');
});
