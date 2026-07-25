// Tests for downloads-janitor.js — the workspace-detail Downloads Janitor
// panel. Inline DOM stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/downloads-janitor.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeElement {
  constructor(tag) {
    this.tagName = (tag || 'div').toUpperCase();
    this.hidden = false;
    this._text = '';
    this.className = '';
    this.value = '';
    this.disabled = false;
    this.children = [];
    this._attrs = {};
    this._listeners = {};
  }
  get textContent() {
    if (this.children.length) return this._text + this.children.map(c => c.textContent).join(' ');
    return this._text;
  }
  set textContent(v) {
    this._text = v;
    if (v === '') this.children = [];
  }
  appendChild(el) {
    this.children.push(el);
    if (el.id) globalThis.document.byId.set(el.id, el);
    return el;
  }
  addEventListener(ev, fn) {
    (this._listeners[ev] ||= []).push(fn);
  }
  click() {
    (this._listeners.click || []).forEach(fn => fn());
  }
  setAttribute(k, v) {
    this._attrs[k] = v;
  }
  getAttribute(k) {
    return this._attrs[k];
  }
  get selected() {
    return this._selected === true;
  }
  set selected(v) {
    this._selected = v;
  }
  dispatch(event) {
    (this._listeners[event] || []).forEach(fn => fn());
  }
  // Depth-first collection helpers used by the assertions below.
  all(predicate, out = []) {
    if (predicate(this)) out.push(this);
    this.children.forEach(c => c.all(predicate, out));
    return out;
  }
}

class FakeDocument {
  constructor() {
    this.byId = new Map();
    this.readyState = 'complete';
  }
  register(id) {
    const el = new FakeElement('div');
    el.id = id;
    this.byId.set(id, el);
    return el;
  }
  getElementById(id) {
    return this.byId.get(id) || null;
  }
  createElement(tag) {
    return new FakeElement(tag);
  }
  addEventListener() {}
}

function setup() {
  const doc = new FakeDocument();
  doc.register('downloadsJanitorMount');
  globalThis.document = doc;
  globalThis.window = globalThis;
  globalThis.window.currentWorkspaceId = 'ws-1';
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ status: { applies: false } }) });
  return doc;
}

const panel = await (async () => {
  setup();
  await import('./downloads-janitor.js');
  return globalThis.window.DownloadsJanitorPanel;
})();

const setupRequiredStatus = {
  applies: true,
  settings: {
    workspace_id: 'ws-1',
    filing_root_name: 'Filed',
    daily_scan_local_time: '09:00',
    content_mode: 'metadata_only'
  },
  readiness: { state: 'setup_required', checks: [] },
  suggestion: {
    key: 'downloads-root',
    label: 'Downloads folder',
    suggested_path: '~/Downloads',
    access_disclosure: 'Ori can list files here.',
    filing_root_name: 'Filed',
    daily_scan_local_time: '09:00'
  }
};

function text(doc) {
  return doc.getElementById('downloadsJanitorMount').textContent;
}

test('a workspace that is not a Downloads Janitor workspace renders nothing', () => {
  const doc = setup();
  panel.render({ applies: false });
  const host = doc.getElementById('downloadsJanitorMount');
  assert.equal(host.hidden, true);
  assert.equal(host.children.length, 0);
});

test('setup card pre-fills the suggested folder without selecting it', () => {
  const doc = setup();
  panel.render(setupRequiredStatus);
  const host = doc.getElementById('downloadsJanitorMount');
  assert.equal(host.hidden, false);
  const input = doc.getElementById('downloadsJanitorPath');
  assert.ok(input, 'expected a folder input');
  // Still the unresolved suggestion: the card offers it, the user confirms it.
  assert.equal(input.value, '~/Downloads');
  assert.match(text(doc), /Setup required/);
});

test('setup card discloses moves, Trash, no permanent deletion, and the daily time', () => {
  const doc = setup();
  panel.render(setupRequiredStatus);
  const body = text(doc);
  assert.match(body, /Filed/);
  assert.match(body, /system Trash/i);
  assert.match(body, /never deletes anything permanently/i);
  assert.match(body, /09:00/);
  assert.match(body, /Nothing moves without your approval/i);
});

test('setup card states that content reading is off and separate', () => {
  const doc = setup();
  panel.render(setupRequiredStatus);
  assert.match(text(doc), /Reading what is inside your files is off/i);
});

test('the folder input is labelled and described for screen readers', () => {
  const doc = setup();
  panel.render(setupRequiredStatus);
  const host = doc.getElementById('downloadsJanitorMount');
  const label = host.all(n => n.tagName === 'LABEL')[0];
  assert.ok(label, 'expected a label');
  assert.equal(label.getAttribute('for'), 'downloadsJanitorPath');
  const input = doc.getElementById('downloadsJanitorPath');
  assert.equal(input.getAttribute('aria-describedby'), 'downloadsJanitorDisclosure');
  const error = doc.getElementById('downloadsJanitorError');
  assert.equal(error.getAttribute('aria-live'), 'polite');
});

test('confirming with an empty folder reports an error and calls no endpoint', async () => {
  const doc = setup();
  panel.render(setupRequiredStatus);
  let called = false;
  globalThis.fetch = async () => {
    called = true;
    return { ok: true, json: async () => ({}) };
  };
  doc.getElementById('downloadsJanitorPath').value = '   ';
  doc.getElementById('downloadsJanitorConfirm').click();
  await new Promise(r => setTimeout(r, 0));
  assert.equal(called, false, 'an empty selection must not reach the server');
  assert.equal(doc.getElementById('downloadsJanitorError').hidden, false);
});

test('confirming posts the confirmed path and renders the returned status', async () => {
  const doc = setup();
  panel.render(setupRequiredStatus);
  let sent = null;
  globalThis.fetch = async (url, opts) => {
    sent = { url, body: JSON.parse(opts.body) };
    return {
      ok: true,
      json: async () => ({
        status: {
          applies: true,
          settings: {
            root_path: '/tmp/Inbox',
            directory_reference_id: 'ref-1',
            filing_root_name: 'Filed'
          },
          readiness: {
            state: 'needs_attention',
            checks: [
              { component: 'directory_access', status: 'ok' },
              {
                component: 'watcher',
                status: 'pending',
                message: 'Folder watching is not running yet.'
              }
            ]
          }
        }
      })
    };
  };
  doc.getElementById('downloadsJanitorPath').value = '/tmp/Inbox';
  doc.getElementById('downloadsJanitorConfirm').click();
  await new Promise(r => setTimeout(r, 0));

  assert.match(sent.url, /\/api\/workspaces\/ws-1\/downloads-janitor\/setup$/);
  assert.equal(sent.body.path, '/tmp/Inbox');
  const body = text(doc);
  assert.match(body, /\/tmp\/Inbox/);
  assert.match(body, /Needs attention/);
});

test('a setup failure shows the server message and re-enables the button', async () => {
  const doc = setup();
  panel.render(setupRequiredStatus);
  globalThis.fetch = async () => ({
    ok: false,
    json: async () => ({
      error: {
        code: 'permission_denied',
        message: 'Ori does not have permission to read that folder.'
      }
    })
  });
  doc.getElementById('downloadsJanitorPath').value = '/tmp/blocked';
  doc.getElementById('downloadsJanitorConfirm').click();
  await new Promise(r => setTimeout(r, 0));

  const error = doc.getElementById('downloadsJanitorError');
  assert.equal(error.hidden, false);
  assert.match(error.textContent, /permission/i);
  assert.equal(doc.getElementById('downloadsJanitorConfirm').disabled, false);
});

test('readiness rows carry a non-color mark and a status word per component', () => {
  const doc = setup();
  panel.render({
    applies: true,
    settings: { root_path: '/tmp/Inbox', directory_reference_id: 'ref-1' },
    readiness: {
      state: 'needs_attention',
      checks: [
        {
          component: 'directory_access',
          status: 'failed',
          message: 'The folder is no longer there.',
          repair: 'relink_folder'
        },
        { component: 'destination', status: 'ok' },
        { component: 'scheduler', status: 'pending', message: 'Not scheduled yet.' }
      ]
    }
  });
  const host = doc.getElementById('downloadsJanitorMount');
  const marks = host.all(n => n.className === 'dj-row-mark');
  assert.equal(marks.length, 3);
  assert.deepEqual(
    marks.map(m => m.textContent),
    ['!', '✓', '–']
  );
  const body = text(doc);
  assert.match(body, /Folder access/);
  assert.match(body, /Needs attention/);
  assert.match(body, /Not running yet/);
  // A failing folder check offers the repair path.
  assert.match(body, /Choose the folder again/);
});

// ------------------------------------------------------------------- review

const CATEGORIES = [
  { id: 'documents', label: 'Documents' },
  { id: 'images', label: 'Images' },
  { id: 'archives', label: 'Archives' },
  { id: 'other', label: 'Other' }
];

function batchFixture(overrides = {}) {
  return Object.assign(
    {
      id: 'batch-1',
      source: 'manual',
      started_at: new Date().toISOString(),
      completed_at: new Date().toISOString(),
      state: 'pending',
      summary: { proposed: 2, needs_review: 1, skipped: 1, ineligible: 3, total: 3 }
    },
    overrides
  );
}

function candidatesFixture() {
  return [
    {
      id: 'c1',
      name: 'invoice-2026-07.pdf',
      extension: '.pdf',
      size: 204800,
      modified_at: new Date(Date.now() - 3600_000).toISOString(),
      category: 'documents',
      destination: 'Filed/Documents',
      reason: 'pdf file',
      confidence: 'high',
      state: 'pending'
    },
    {
      id: 'c2',
      name: 'payload.bin',
      extension: '.bin',
      size: 1024,
      modified_at: new Date().toISOString(),
      category: 'other',
      destination: 'Filed/Other',
      reason: '.bin files can hold anything',
      confidence: 'low',
      needs_review: true,
      state: 'pending'
    },
    {
      id: 'c3',
      name: 'ad.png',
      extension: '.png',
      size: 500,
      modified_at: new Date().toISOString(),
      category: 'images',
      destination: 'Filed/Images',
      reason: 'png file',
      confidence: 'high',
      state: 'skipped'
    }
  ];
}

// renderReview paints the configured card with a batch already loaded.
function renderReview(doc, batch = batchFixture(), candidates = candidatesFixture()) {
  panel._setBatch(batch, candidates, CATEGORIES);
  panel.render({
    applies: true,
    settings: {
      root_path: '/tmp/Inbox',
      directory_reference_id: 'ref-1',
      filing_root_name: 'Filed'
    },
    readiness: {
      state: 'needs_attention',
      checks: [{ component: 'directory_access', status: 'ok' }]
    }
  });
  return doc.getElementById('downloadsJanitorMount');
}

function rowsIn(host) {
  return host.all(n => n.className && String(n.className).includes('dj-row-item'));
}

test('review table renders a row per candidate with the facts needed to judge it', () => {
  const doc = setup();
  const host = renderReview(doc);
  const rows = rowsIn(host);
  assert.equal(rows.length, 3);

  const body = text(doc);
  assert.match(body, /invoice-2026-07\.pdf/);
  assert.match(body, /Filed\/Documents/); // resolved destination
  assert.match(body, /pdf file/); // reason
  assert.match(body, /high confidence/);
  assert.match(body, /200 KB/); // human-readable size
});

test('the batch summary states counts, scan source, and when it ran', () => {
  const doc = setup();
  renderReview(doc);
  const body = text(doc);
  assert.match(body, /2 proposed/);
  assert.match(body, /1 needing review/);
  assert.match(body, /1 skipped/);
  assert.match(body, /3 not eligible/);
  assert.match(body, /Scan now/); // the source label
});

test('every row starts unselected', () => {
  const doc = setup();
  const host = renderReview(doc);
  const boxes = host.all(n => n.className === 'dj-select');
  assert.equal(boxes.length, 3);
  boxes.forEach(box => assert.equal(box.checked, false));
  assert.deepEqual(panel._selected(), []);
  assert.match(text(doc), /No files selected/);
});

test('a low-confidence candidate is flagged for review in text, not colour alone', () => {
  const doc = setup();
  const host = renderReview(doc);
  const flags = host.all(n => n.className === 'dj-flag');
  assert.equal(flags.length, 1);
  assert.match(flags[0].textContent, /Needs review/);
});

test('changing a category records the decision immediately', async () => {
  const doc = setup();
  const host = renderReview(doc);
  let sent = null;
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST') sent = { url, body: JSON.parse(opts.body) };
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  const select = host.all(n => n.className === 'dj-category')[0];
  select.value = 'archives';
  select.dispatch('change');
  await new Promise(r => setTimeout(r, 0));

  assert.match(sent.url, /\/downloads-janitor\/decisions$/);
  assert.equal(sent.body.decisions[0].decision, 'move');
  assert.equal(sent.body.decisions[0].category, 'archives');
  // IDs only: the browser never names a path.
  assert.equal(sent.body.decisions[0].candidate_id, 'c1');
  assert.ok(!JSON.stringify(sent.body).includes('/tmp/Inbox'));
});

test('Skip records a skip decision for that candidate only', async () => {
  const doc = setup();
  const host = renderReview(doc);
  let sent = null;
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST') sent = JSON.parse(opts.body);
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  const skip = host.all(n => n.tagName === 'BUTTON' && n.textContent === 'Skip')[0];
  assert.ok(skip, 'expected a Skip control on a pending row');
  skip.click();
  await new Promise(r => setTimeout(r, 0));

  assert.equal(sent.decisions.length, 1);
  assert.equal(sent.decisions[0].decision, 'skip');
  assert.equal(sent.decisions[0].candidate_id, 'c1');
});

test('an already-skipped row offers no category control and no Skip', () => {
  const doc = setup();
  const host = renderReview(doc);
  const skipped = rowsIn(host).find(row => String(row.className).includes('dj-row-state-skipped'));
  assert.ok(skipped, 'expected the skipped row');
  assert.equal(skipped.all(n => n.className === 'dj-category').length, 0);
  assert.equal(skipped.all(n => n.tagName === 'BUTTON' && n.textContent === 'Skip').length, 0);
  const box = skipped.all(n => n.className === 'dj-select')[0];
  assert.equal(box.disabled, true, 'a resolved row cannot be selected for action');
});

test('filters narrow the table without losing the batch', () => {
  const doc = setup();
  const host = renderReview(doc);
  assert.equal(rowsIn(host).length, 3);

  const needsReview = host.all(n => n.tagName === 'BUTTON' && n.textContent === 'Needs review')[0];
  needsReview.click();
  assert.equal(rowsIn(doc.getElementById('downloadsJanitorMount')).length, 1);

  const all = doc
    .getElementById('downloadsJanitorMount')
    .all(n => n.tagName === 'BUTTON' && n.textContent === 'All')[0];
  all.click();
  assert.equal(rowsIn(doc.getElementById('downloadsJanitorMount')).length, 3);
});

test('a filter matching nothing says so rather than showing an empty table', () => {
  const doc = setup();
  const host = renderReview(doc, batchFixture(), [candidatesFixture()[2]]);
  const pending = host.all(n => n.tagName === 'BUTTON' && n.textContent === 'Pending')[0];
  pending.click();
  assert.match(text(doc), /No files match this filter/);
});

test('the table and its controls carry screen-reader labels', () => {
  const doc = setup();
  const host = renderReview(doc);
  const table = host.all(n => n.tagName === 'TABLE')[0];
  assert.ok(table.getAttribute('aria-label'), 'the table needs a label');

  const headers = host.all(n => n.tagName === 'TH');
  assert.equal(headers.length, 9);
  headers.forEach(header => assert.equal(header.getAttribute('scope'), 'col'));

  const box = host.all(n => n.className === 'dj-select')[0];
  assert.match(box.getAttribute('aria-label'), /Select invoice-2026-07\.pdf/);
  const select = host.all(n => n.className === 'dj-category')[0];
  assert.match(select.getAttribute('aria-label'), /Category for invoice-2026-07\.pdf/);

  const filterButton = host.all(
    n => n.tagName === 'BUTTON' && String(n.className).startsWith('dj-filter')
  )[0];
  assert.ok(filterButton.getAttribute('aria-pressed'), 'filters expose pressed state');
});

test('an empty workspace explains what will appear rather than showing a bare table', () => {
  const doc = setup();
  renderReview(doc, null, []);
  const body = text(doc);
  assert.match(body, /Nothing to review/);
  assert.match(body, /Nothing is moved without your approval/);
  assert.match(body, /No files waiting for review/);
});

test('the status line reports the pending count and the last scan', () => {
  const doc = setup();
  renderReview(doc);
  assert.match(text(doc), /2 files waiting for review/);
  assert.match(text(doc), /last scan/);
});

test('Scan now reports honestly when a scan finds nothing new', async () => {
  const doc = setup();
  renderReview(doc);
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST') {
      return { ok: true, json: async () => ({ success: true, created: false }) };
    }
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  doc.getElementById('downloadsJanitorScan').click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  const error = doc.getElementById('downloadsJanitorError');
  assert.equal(error.hidden, false);
  assert.match(error.textContent, /Nothing new to review/);
  // The button is usable again.
  assert.equal(doc.getElementById('downloadsJanitorScan').disabled, false);
});

test('a rejected decision is not left on screen as though it were saved', async () => {
  const doc = setup();
  const host = renderReview(doc);
  let reloaded = false;
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST') {
      return {
        ok: false,
        json: async () => ({ error: { message: 'That category is not allowed.' } })
      };
    }
    reloaded = true;
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  const select = host.all(n => n.className === 'dj-category')[0];
  select.value = 'archives';
  select.dispatch('change');
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  assert.match(doc.getElementById('downloadsJanitorError').textContent, /not allowed/);
  assert.ok(reloaded, 'the panel must repaint from the server after a rejected change');
});

// -------------------------------------------------- confirmation and results

const PREVIEW = {
  batch_id: 'batch-1',
  token: 'tok-abc',
  move_count: 2,
  items: [
    {
      candidate_id: 'c1',
      name: 'invoice-2026-07.pdf',
      destination: 'Filed/Documents/invoice-2026-07.pdf',
      renamed: false
    },
    {
      candidate_id: 'c2',
      name: 'payload.bin',
      destination: 'Filed/Other/payload (2).bin',
      renamed: true
    }
  ]
};

test('the approve control states the count and is disabled until something is selected', () => {
  const doc = setup();
  renderReview(doc);
  const approve = doc.getElementById('downloadsJanitorApprove');
  assert.equal(approve.disabled, true);
  assert.match(approve.textContent, /Approve selected/);

  panel._select('c1');
  assert.equal(approve.disabled, false);
  assert.match(approve.textContent, /Approve 1 move$/);

  panel._select('c2');
  assert.match(approve.textContent, /Approve 2 moves$/);
});

test('approving previews the plan without moving anything', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');

  const calls = [];
  globalThis.fetch = async url => {
    calls.push(url);
    if (String(url).endsWith('/preview')) {
      return { ok: true, json: async () => ({ preview: PREVIEW }) };
    }
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  assert.ok(
    calls.some(url => String(url).endsWith('/preview')),
    'the preview endpoint should be called'
  );
  assert.ok(
    !calls.some(url => String(url).endsWith('/apply')),
    'previewing must not apply anything'
  );

  const body = text(doc);
  assert.match(body, /Confirm these moves/);
  assert.match(body, /Ori will move 2 files/);
  assert.match(body, /Nothing is deleted/);
});

test('the confirmation shows each resolved destination and flags forced renames', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  globalThis.fetch = async url =>
    String(url).endsWith('/preview')
      ? { ok: true, json: async () => ({ preview: PREVIEW }) }
      : {
          ok: true,
          json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
        };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  const body = text(doc);
  assert.match(body, /Filed\/Documents\/invoice-2026-07\.pdf/);
  assert.match(body, /Filed\/Other\/payload \(2\)\.bin/);
  // The user is told a name was taken rather than discovering it afterwards.
  assert.match(body, /renamed — a file with that name is already there/);
});

test('confirming sends the approval token and reports per-file results', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  let applyBody = null;
  globalThis.fetch = async (url, opts) => {
    if (String(url).endsWith('/preview')) {
      return { ok: true, json: async () => ({ preview: PREVIEW }) };
    }
    if (String(url).endsWith('/apply')) {
      applyBody = JSON.parse(opts.body);
      return {
        ok: true,
        json: async () => ({
          result: {
            applied: 1,
            failed: 1,
            stale: 0,
            outcomes: [
              {
                candidate_id: 'c1',
                name: 'invoice-2026-07.pdf',
                result: 'applied',
                destination: 'Filed/Documents/invoice-2026-07.pdf'
              },
              {
                candidate_id: 'c2',
                name: 'payload.bin',
                result: 'failed',
                message: 'Ori could not move this file.'
              }
            ]
          }
        })
      };
    }
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));
  doc.getElementById('downloadsJanitorConfirmApply').click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  assert.equal(applyBody.approval_token, 'tok-abc');
  assert.equal(applyBody.batch_id, 'batch-1');
  assert.equal(applyBody.decisions[0].candidate_id, 'c1');
  // Still IDs only — the browser never names a path.
  assert.ok(!JSON.stringify(applyBody).includes('/tmp/Inbox'));

  // A mixed result is stated as mixed, never as success.
  const body = text(doc);
  assert.match(body, /1 file filed/);
  assert.match(body, /1 could not be moved/);
  assert.match(body, /Ori could not move this file/);
  assert.doesNotMatch(body, /All files? moved/);
});

test('a stale result explains itself and offers a rescan', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  globalThis.fetch = async url => {
    if (String(url).endsWith('/preview'))
      return { ok: true, json: async () => ({ preview: PREVIEW }) };
    if (String(url).endsWith('/apply')) {
      return {
        ok: true,
        json: async () => ({
          result: {
            applied: 0,
            failed: 0,
            stale: 1,
            outcomes: [
              {
                candidate_id: 'c1',
                name: 'invoice-2026-07.pdf',
                result: 'stale',
                message: 'This file changed after you approved it, so Ori left it alone.'
              }
            ]
          }
        })
      };
    }
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));
  doc.getElementById('downloadsJanitorConfirmApply').click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  const body = text(doc);
  assert.match(body, /changed since you approved/);
  assert.match(body, /Ori left it alone/);
  assert.match(body, /Scan again/);
});

test('cancelling abandons the approval and moves nothing', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  const calls = [];
  globalThis.fetch = async url => {
    calls.push(String(url));
    if (String(url).endsWith('/preview'))
      return { ok: true, json: async () => ({ preview: PREVIEW }) };
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  const host = doc.getElementById('downloadsJanitorMount');
  const cancel = host.all(n => n.tagName === 'BUTTON' && n.textContent === 'Cancel')[0];
  assert.ok(cancel, 'expected a Cancel control');
  cancel.click();
  await new Promise(r => setTimeout(r, 0));

  assert.ok(!calls.some(url => url.endsWith('/apply')), 'cancelling must not apply anything');
  assert.doesNotMatch(text(doc), /Confirm these moves/);
});

test('a rejected approval is reported and nothing is left looking approved', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  globalThis.fetch = async url => {
    if (String(url).endsWith('/preview')) {
      return {
        ok: false,
        json: async () => ({
          error: {
            code: 'candidate_changed',
            message: 'report.pdf changed since it was proposed — rescan to review it again.'
          }
        })
      };
    }
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  assert.match(doc.getElementById('downloadsJanitorError').textContent, /rescan/);
  assert.doesNotMatch(text(doc), /Confirm these moves/);
  // The approve control is usable again.
  assert.equal(doc.getElementById('downloadsJanitorApprove').disabled, false);
});

// Applying every file in a batch empties it. The report of what happened must
// still be on screen afterwards — that moment is the whole point of the flow.
test('results survive the batch they emptied', async () => {
  const doc = setup();
  renderReview(doc, batchFixture(), [candidatesFixture()[0]]);
  panel._select('c1');
  globalThis.fetch = async url => {
    if (String(url).endsWith('/preview')) {
      return {
        ok: true,
        json: async () => ({ preview: { ...PREVIEW, move_count: 1, items: [PREVIEW.items[0]] } })
      };
    }
    if (String(url).endsWith('/apply')) {
      return {
        ok: true,
        json: async () => ({
          result: {
            applied: 1,
            failed: 0,
            stale: 0,
            outcomes: [
              {
                candidate_id: 'c1',
                name: 'invoice-2026-07.pdf',
                result: 'applied',
                destination: 'Filed/Documents/invoice-2026-07.pdf'
              }
            ]
          }
        })
      };
    }
    // The batch is now resolved: nothing pending remains.
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));
  doc.getElementById('downloadsJanitorConfirmApply').click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  const body = text(doc);
  assert.match(body, /1 file filed/, 'the outcome must remain visible after the batch empties');
  assert.match(body, /Filed\/Documents\/invoice-2026-07\.pdf/);
  // And the now-empty batch still explains itself.
  assert.match(body, /Nothing to review/);
});

// ------------------------------------------------------------------- Trash

function trashToggleFor(doc, name) {
  const host = doc.getElementById('downloadsJanitorMount');
  return host.all(
    n => n.tagName === 'BUTTON' && String(n.getAttribute('aria-label') || '').includes(name)
  )[0];
}

test('Trash is a per-file choice, never part of the move selection', () => {
  const doc = setup();
  renderReview(doc);

  const toggle = trashToggleFor(doc, 'invoice-2026-07.pdf');
  assert.ok(toggle, 'expected a Trash control on a pending row');
  assert.match(toggle.getAttribute('aria-label'), /Mark .* for Trash/);
  assert.equal(toggle.getAttribute('aria-pressed'), 'false');

  toggle.click();
  const marked = trashToggleFor(doc, 'invoice-2026-07.pdf');
  assert.equal(marked.getAttribute('aria-pressed'), 'true');
  // Marking for Trash does not put the file in the move selection.
  assert.deepEqual(panel._selected(), []);
  assert.match(text(doc), /1 file marked for Trash/);
});

test('a file marked for Trash cannot also be selected for a move', () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  trashToggleFor(doc, 'invoice-2026-07.pdf').click();

  // The move selection released it, and its checkbox is no longer selectable.
  assert.deepEqual(panel._selected(), []);
  const host = doc.getElementById('downloadsJanitorMount');
  const box = host.all(n => n.className === 'dj-select')[0];
  assert.equal(box.disabled, true, 'a file bound for Trash is not a move candidate');
});

test('the approve control counts moves and removals separately', () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  trashToggleFor(doc, 'payload.bin').click();

  const approve = doc.getElementById('downloadsJanitorApprove');
  assert.match(approve.textContent, /1 move/);
  assert.match(approve.textContent, /1 to Trash/);
});

test('a batch containing a removal requires a separate acknowledgement of the exact count', async () => {
  const doc = setup();
  renderReview(doc);
  trashToggleFor(doc, 'payload.bin').click();
  globalThis.fetch = async url =>
    String(url).endsWith('/preview')
      ? {
          ok: true,
          json: async () => ({
            preview: {
              batch_id: 'batch-1',
              token: 'tok-abc',
              move_count: 0,
              trash_count: 1,
              items: [{ candidate_id: 'c2', name: 'payload.bin', operation: 'trash' }]
            }
          })
        }
      : {
          ok: true,
          json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
        };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  const body = text(doc);
  // The consequence is stated plainly, including that it is recoverable.
  assert.match(body, /system Trash/);
  assert.match(body, /restore them/);
  assert.match(body, /Nothing is deleted permanently/);
  assert.match(body, /Trash \(restorable\)/);

  // The confirm control is blocked until the removal is acknowledged by count.
  const confirm = doc.getElementById('downloadsJanitorConfirmApply');
  assert.equal(confirm.disabled, true, 'a removal must not be one click away');
  const ack = doc.getElementById('downloadsJanitorTrashAck');
  assert.ok(ack, 'expected a separate acknowledgement for the removal');
  assert.match(body, /Yes, move 1 file to the Trash/);

  ack.checked = true;
  ack.dispatch('change');
  assert.equal(confirm.disabled, false);
});

test('a move-only batch needs no extra acknowledgement', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  globalThis.fetch = async url =>
    String(url).endsWith('/preview')
      ? { ok: true, json: async () => ({ preview: { ...PREVIEW, trash_count: 0 } }) }
      : {
          ok: true,
          json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
        };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  assert.equal(doc.getElementById('downloadsJanitorTrashAck'), null);
  assert.equal(doc.getElementById('downloadsJanitorConfirmApply').disabled, false);
  assert.match(text(doc), /Nothing is deleted/);
});

test('approving sends each file under the operation it was marked with', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  trashToggleFor(doc, 'payload.bin').click();
  let sent = null;
  globalThis.fetch = async (url, opts) => {
    if (String(url).endsWith('/preview')) {
      sent = JSON.parse(opts.body);
      return {
        ok: true,
        json: async () => ({ preview: { ...PREVIEW, trash_count: 1, move_count: 1 } })
      };
    }
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  const byId = Object.fromEntries(sent.decisions.map(d => [d.candidate_id, d]));
  assert.equal(byId.c1.operation, 'move');
  assert.equal(byId.c2.operation, 'trash');
  assert.equal(byId.c2.category, '', 'a removal has no category');
});

// A removal mark belongs to the batch it was made in. Carrying one into a
// different set of files is how the wrong file gets thrown away.
test('Trash marks never survive a repaint into a different batch', () => {
  const doc = setup();
  renderReview(doc);
  trashToggleFor(doc, 'payload.bin').click();
  assert.match(text(doc), /1 file marked for Trash/);

  // A fresh batch arrives.
  renderReview(doc, batchFixture(), candidatesFixture());
  assert.doesNotMatch(text(doc), /marked for Trash/);
  const approve = doc.getElementById('downloadsJanitorApprove');
  assert.equal(approve.disabled, true, 'nothing should be pending from the previous batch');
});

// A batch of removals alone is still approvable — guarding on the move
// selection would make Trash unreachable.
test('a Trash-only batch can be approved', async () => {
  const doc = setup();
  renderReview(doc);
  trashToggleFor(doc, 'payload.bin').click();
  let previewed = false;
  globalThis.fetch = async url => {
    if (String(url).endsWith('/preview')) {
      previewed = true;
      return {
        ok: true,
        json: async () => ({
          preview: {
            batch_id: 'batch-1',
            token: 'tok',
            move_count: 0,
            trash_count: 1,
            items: [{ candidate_id: 'c2', name: 'payload.bin', operation: 'trash' }]
          }
        })
      };
    }
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));
  assert.ok(previewed, 'a Trash-only batch must reach the preview');
  assert.match(text(doc), /Confirm these moves/);
});

// ----------------------------------------------------------------- history

const HISTORY = [
  {
    id: 'a1',
    operation: 'move',
    source_name: 'report.pdf',
    destination_relative: 'Filed/Documents/report.pdf',
    result: 'applied',
    undo: 'available'
  },
  {
    id: 'a2',
    operation: 'trash',
    source_name: 'ad.png',
    result: 'applied',
    undo: 'available'
  },
  {
    id: 'a3',
    operation: 'move',
    source_name: 'blocked.pdf',
    result: 'failed',
    error_summary: 'Ori could not move this file.',
    undo: 'unavailable'
  },
  {
    id: 'a4',
    operation: 'move',
    source_name: 'restored.pdf',
    destination_relative: 'Filed/Documents/restored.pdf',
    result: 'applied',
    undo: 'undone'
  }
];

test('history lists what happened and names the reversal each entry offers', () => {
  const doc = setup();
  renderReview(doc);
  panel._setHistory(HISTORY);

  const body = text(doc);
  assert.match(body, /History/);
  assert.match(body, /report\.pdf\s+— filed to Filed\/Documents\/report\.pdf/);
  assert.match(body, /ad\.png\s+— moved to Trash/);
  // The control says what it will actually do, not a generic "undo".
  assert.match(body, /Undo move/);
  assert.match(body, /Restore from Trash/);
});

test('history explains entries that cannot be undone instead of hiding them', () => {
  const doc = setup();
  renderReview(doc);
  panel._setHistory(HISTORY);

  const body = text(doc);
  // A failed action is listed with its reason and offers no undo.
  assert.match(body, /blocked\.pdf\s+— not moved/);
  assert.match(body, /Ori could not move this file/);
  // An already-undone action says so.
  assert.match(body, /restored\.pdf/);
  assert.match(body, /put back/);
});

test('undoing calls the action-specific endpoint and reports the outcome', async () => {
  const doc = setup();
  renderReview(doc);
  panel._setHistory(HISTORY);

  let undoUrl = null;
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST' && String(url).includes('/undo')) {
      undoUrl = String(url);
      return {
        ok: true,
        json: async () => ({
          undo: { result: 'undone', message: 'Put back in the folder.', restored_to: 'report.pdf' }
        })
      };
    }
    if (String(url).includes('/history'))
      return { ok: true, json: async () => ({ actions: HISTORY }) };
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  const host = doc.getElementById('downloadsJanitorMount');
  host.all(n => n.tagName === 'BUTTON' && n.textContent === 'Undo move')[0].click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  assert.match(undoUrl, /\/downloads-janitor\/history\/a1\/undo$/);
});

test('a refused undo is reported with its reason, not as a failure of the app', async () => {
  const doc = setup();
  renderReview(doc);
  panel._setHistory(HISTORY);

  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST' && String(url).includes('/undo')) {
      return {
        ok: true,
        json: async () => ({
          undo: {
            result: 'failed',
            message:
              'Something else is already using the original name, so Ori did not overwrite it.'
          }
        })
      };
    }
    if (String(url).includes('/history'))
      return { ok: true, json: async () => ({ actions: HISTORY }) };
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  const host = doc.getElementById('downloadsJanitorMount');
  host.all(n => n.tagName === 'BUTTON' && n.textContent === 'Undo move')[0].click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  assert.match(
    doc.getElementById('downloadsJanitorHistoryStatus').textContent,
    /already using the original name/
  );
});

test('history filters are exposed as pressed-state controls', () => {
  const doc = setup();
  renderReview(doc);
  panel._setHistory(HISTORY);

  const host = doc.getElementById('downloadsJanitorMount');
  const filters = host.all(
    n => n.tagName === 'BUTTON' && ['Filed', 'Trashed', 'Can undo'].includes(n.textContent)
  );
  assert.equal(filters.length, 3);
  filters.forEach(control => assert.ok(control.getAttribute('aria-pressed')));
});

test('an empty history explains what will appear there', () => {
  const doc = setup();
  renderReview(doc);
  panel._setHistory([]);
  assert.match(text(doc), /Applied moves and Trash actions are listed here/);
});
