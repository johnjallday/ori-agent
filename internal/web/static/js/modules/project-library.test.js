import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ProjectLibraryPanel,
  libraryDigestText,
  libraryQuery,
  libraryRunText,
  proposalSourceLabel,
  readActivationQueue
} from './project-library.js';

test('Show project folder requires a fresh connected Home link and never sends a path or starts a DAW', async () => {
  const requests = [];
  const panel = new ProjectLibraryPanel({
    workspaceId: 'home',
    fetchImpl: async (url, options) => {
      requests.push({ url, options });
      return { ok: true, json: async () => ({ message: 'Folder reveal requested' }) };
    }
  });
  panel.run = async (_trigger, _message, work) => work();
  const messages = [];
  panel.status = message => messages.push(message);
  let providerReadOnly = false;
  let currentChild = 'child & project';
  panel.request = async path => {
    if (path === '/roots') return { provider_read_only: providerReadOnly };
    assert.equal(path, '/projects/entry/activation');
    return { state: 'connected', workspace_id: currentChild };
  };
  await panel.showConnectedFolder('entry', currentChild, null);
  assert.deepEqual(requests, [
    {
      url: '/api/workspaces/child%20%26%20project/project/show-folder',
      options: { method: 'POST', headers: { Accept: 'application/json' } }
    }
  ]);
  assert.match(messages.at(-1), /No DAW was started/);
  providerReadOnly = true;
  await assert.rejects(panel.showConnectedFolder('entry', currentChild, null), /read-only/);
  providerReadOnly = false;
  currentChild = 'different-child';
  await assert.rejects(panel.showConnectedFolder('entry', 'child & project', null), /link changed/);
  assert.equal(
    requests.length,
    1,
    'provider loss or a replaced child must not contact the OS route'
  );
});

test('canceling a reviewed scan or disconnect refreshes the Home revision before retry', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { revision: 1 };
  panel.run = async (_trigger, _message, work) => work();
  panel.status = () => {};
  panel.confirm = async () => false;
  const calls = [];
  panel.post = async (path, body) => {
    calls.push({ path, body });
    if (!path.endsWith('/review')) throw new Error('cancel must never commit');
    return {
      token: 'inert-review',
      root_path: '/sandbox/Documents',
      entry_count: 5,
      max_entries: 5000
    };
  };
  panel.refresh = async () => {
    panel.state.revision++;
  };
  await panel.revokeRoot({ id: 'root', path: '/sandbox/Documents' });
  await panel.scanRootFlow('root');
  assert.deepEqual(calls, [
    { path: '/roots/root/revoke/review', body: { if_revision: 1 } },
    { path: '/roots/root/scans/review', body: { if_revision: 2 } }
  ]);
  assert.equal(panel.state.revision, 3);
});

test('forget cancellation erases no saved Home record or session', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { provider_read_only: true };
  panel.run = async (_trigger, _message, work) => work();
  panel.status = () => {};
  panel.confirm = async () => false;
  let refreshes = 0;
  panel.refresh = async () => {
    refreshes++;
  };
  const calls = [];
  panel.post = async (path, body) => {
    calls.push({ path, body });
    if (path.endsWith('/commit')) throw new Error('canceled forget must not commit');
    return { project_name: 'Album-0', source_count: 1, session_count: 2 };
  };
  await panel.forgetRecord({ revision: 8, row: { id: 'album' } });
  assert.deepEqual(calls, [{ path: '/projects/album/forget/review', body: { revision: 8 } }]);
  assert.equal(refreshes, 1);
});

test('user-written linked recap requires its own Home review and explicit confirmation', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.run = async (_trigger, _message, work) => work();
  panel.status = () => {};
  let refreshes = 0;
  panel.refresh = async () => refreshes++;
  const calls = [];
  panel.post = async (path, body) => {
    calls.push({ path, body });
    if (path.endsWith('/review'))
      return {
        token: 'reviewed-share',
        session: { shared_from_project: { workspace_id: 'exact-child' } }
      };
    return { session: { recap: 'A shorter bridge' } };
  };
  const detail = { row: { id: 'song', name: 'Song', fields_revision: 0 } };
  const session = { id: 'session', revision: 1 };
  const input = {
    recap: 'A shorter bridge',
    decisions: [],
    blockers: [],
    next_action: '',
    actual_date: '',
    update_project_next_action: false,
    share_linked_project: true
  };
  panel.confirm = async (_heading, consequences) => {
    assert.ok(consequences.some(line => line.includes('exact-child') && line.includes('No files')));
    return false;
  };
  await panel.saveStudioSession(detail, session, input, null);
  assert.equal(calls.length, 1, 'canceled share writes no recap');
  assert.equal(refreshes, 0);
  panel.confirm = async () => true;
  await panel.saveStudioSession(detail, session, input, null);
  assert.equal(calls.length, 3);
  assert.equal(calls[1].body.recap.share_linked_project, true);
  assert.equal(calls[2].body.review_token, 'reviewed-share');
  assert.equal(calls[2].body.confirm, true);
  assert.equal(refreshes, 1);
});

test('handoff citation remains a separate reviewed historical Home receipt, never child status', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.run = async (_trigger, _message, work) => work();
  panel.status = () => {};
  panel.refresh = async () => {};
  const calls = [];
  let citation = { ticket_id: 'confirmed-ticket', ticket_number: 9 };
  panel.post = async (path, body) => {
    calls.push({ path, body });
    if (path.endsWith('/review'))
      return {
        token: 'receipt-review',
        session: {
          shared_from_project: { workspace_id: 'current-child' },
          handoff_citation: citation
        }
      };
    return { session: { handoff_citation: citation } };
  };
  const input = {
    recap: 'Owner wrote this',
    decisions: [],
    blockers: [],
    next_action: '',
    actual_date: '',
    update_project_next_action: false,
    share_linked_project: true,
    handoff_ticket_id: 'confirmed-ticket'
  };
  const detail = { row: { id: 'entry', name: 'Song', fields_revision: 0 } };
  const session = { id: 'session', revision: 1 };
  panel.confirm = async (_heading, consequences) => {
    assert.ok(
      consequences.some(
        line => line.includes('Ticket #9') && line.includes('not current child status')
      )
    );
    return false;
  };
  await panel.saveStudioSession(detail, session, input, null);
  assert.equal(calls.length, 1, 'canceled citation must not commit a recap');
  citation = { ticket_id: 'different-ticket', ticket_number: 9 };
  await assert.rejects(panel.saveStudioSession(detail, session, input, null), /receipt changed/);
  assert.equal(calls.length, 2, 'changed review must not commit');
  citation = { ticket_id: 'confirmed-ticket', ticket_number: 9 };
  panel.confirm = async () => true;
  await panel.saveStudioSession(detail, session, input, null);
  assert.equal(calls.length, 4);
  assert.equal(calls[3].body.recap.handoff_ticket_id, 'confirmed-ticket');
  assert.equal(calls[3].body.confirm, true);
});

test('owner Wrap up offers only selected Home handoff IDs and requires linked attribution', async () => {
  const original = globalThis.document;
  const elements = [];
  const makeNode = tag => ({
    tag,
    children: [],
    value: '',
    checked: false,
    disabled: false,
    isConnected: false,
    append(...items) {
      this.children.push(...items);
    },
    setAttribute() {},
    addEventListener(event, fn) {
      this[event] = fn;
    },
    showModal() {},
    close() {
      this.isConnected = false;
    },
    focus() {},
    remove() {},
    setCustomValidity(error) {
      this.validation = error;
    },
    reportValidity() {
      return !this.validation;
    }
  });
  globalThis.document = {
    createElement: tag => {
      const element = makeNode(tag);
      elements.push(element);
      return element;
    },
    body: {
      append(dialog) {
        dialog.isConnected = true;
      }
    },
    querySelector: () => null
  };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    const requests = [];
    panel.request = async path => {
      requests.push(path);
      return {
        rows: [{ ticket_id: 'saved-ticket', ticket_number: 3, recorded_at: '2026-09-28T00:00:00Z' }]
      };
    };
    const saves = [];
    panel.saveStudioSession = async (_detail, _session, input) => saves.push(input);
    panel.sessionForm({ row: { id: 'song', connection: 'connected' } }, { id: 'session' }, null);
    await Promise.resolve();
    assert.deepEqual(requests, ['/projects/song/handoff-receipts']);
    const handoff = elements.find(element => element.tag === 'select');
    const checkboxes = elements.filter(
      element => element.tag === 'input' && element.type === 'checkbox'
    );
    const form = elements.find(element => element.tag === 'form');
    assert.equal(checkboxes.length, 2);
    assert.equal(handoff.children.length, 2);
    assert.equal(handoff.children[1].value, 'saved-ticket');
    handoff.value = 'saved-ticket';
    elements.find(element => element.tag === 'textarea').value = 'Owner wrote this';
    form.submit({ preventDefault() {} });
    assert.equal(
      saves.length,
      0,
      'selecting a receipt does not auto-consent to project attribution'
    );
    checkboxes[1].checked = true;
    form.submit({ preventDefault() {} });
    assert.equal(saves.length, 1);
    assert.equal(saves[0].handoff_ticket_id, 'saved-ticket');
    assert.equal(saves[0].share_linked_project, true);
  } finally {
    globalThis.document = original;
  }
});

test('pending direct links require a separate review and never commit on cancellation', async () => {
  const original = globalThis.document;
  const elements = new Map();
  const makeNode = tag => ({
    tag,
    children: [],
    append(...items) {
      this.children.push(...items);
    },
    replaceChildren() {
      this.children = [];
    },
    addEventListener(_event, fn) {
      this.click = fn;
    }
  });
  elements.set('projectLibraryPendingLinks', makeNode('section'));
  elements.set('projectLibraryPendingRows', makeNode('div'));
  globalThis.document = {
    createElement: makeNode,
    getElementById: id => elements.get(id) || null
  };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    panel.state = { provider_read_only: false };
    panel.run = async (_trigger, _message, work) => work();
    const calls = [];
    let refreshes = 0;
    panel.refresh = async () => {
      refreshes++;
    };
    panel.status = () => {};
    panel.request = async path => {
      assert.equal(path, '/linked-projects/pending');
      return { revision: 6, total: 1, rows: [{ name: 'Song', workspace_id: 'child' }] };
    };
    panel.post = async (path, body) => {
      calls.push({ path, body });
      if (path.endsWith('/review'))
        return { token: 'review-token', project_name: 'Song', link_only: true };
      return { entry_id: 'association' };
    };
    panel.confirm = async () => false;
    await panel.renderPendingLinks();
    assert.equal(elements.get('projectLibraryPendingLinks').hidden, false);
    elements.get('projectLibraryPendingRows').children[0].children[1].click();
    await new Promise(setImmediate);
    assert.deepEqual(calls, [{ path: '/linked-projects/child/review', body: { revision: 6 } }]);
    assert.equal(refreshes, 1); // Canceled reviews still advance the Home revision.
    panel.confirm = async () => true;
    await panel.renderPendingLinks();
    elements.get('projectLibraryPendingRows').children[0].children[1].click();
    await new Promise(setImmediate);
    assert.equal(calls[2].path, '/linked-projects/child/commit');
    assert.equal(calls[2].body.review_token, 'review-token');
    assert.equal(calls[2].body.confirm, true);
    assert.match(calls[2].body.idempotency_key, /^library-associate-linked-project-/);
    assert.equal(refreshes, 2);
  } finally {
    globalThis.document = original;
  }
});

test('Manager suggestions render as inert text and stale rows have no confirmation action', async () => {
  const original = globalThis.document;
  const elements = new Map();
  const makeNode = tag => ({
    tag,
    children: [],
    append(...items) {
      this.children.push(...items);
    },
    replaceChildren() {
      this.children = [];
    },
    addEventListener(_event, fn) {
      this.click = fn;
    }
  });
  elements.set('projectLibraryProposals', makeNode('section'));
  elements.set('projectLibraryProposalRows', makeNode('div'));
  globalThis.document = {
    createElement: makeNode,
    getElementById: id => elements.get(id) || null
  };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    panel.state = { provider_read_only: false };
    const destinations = [];
    panel.details = id => destinations.push(id);
    panel.request = async path => {
      if (path === '/summary') return { initialized: true, digest: null, ready_proposals: 5 };
      assert.equal(path, '/proposals');
      return {
        total: 6,
        rows: [
          {
            name: '<svg onload=alert(1)>',
            status: 'ready',
            proposal: {
              id: 'ready-id',
              next_action: '<script>untrusted</script>',
              reason: 'Just a suggestion',
              agent_name: 'Manager'
            }
          },
          {
            name: 'Old',
            status: 'stale',
            proposal: {
              id: 'stale-id',
              next_action: 'Old note',
              reason: '',
              agent_name: 'Manager'
            }
          },
          {
            name: 'Album-4',
            status: 'ready',
            proposal: {
              id: 'navigation-id',
              kind: 'project_review',
              entry_id: 'entry-4',
              reason: '<script>untrusted setup note</script>',
              agent_name: 'Manager'
            }
          },
          {
            name: 'Album-1',
            status: 'ready',
            proposal: {
              id: 'goal-id',
              kind: 'session_goal',
              entry_id: 'entry-1',
              entry_revision: 2,
              digest: 'exact-draft',
              agent_name: 'Manager',
              goal: {
                goal: '<script>do not execute</script>',
                desired_outcome: 'Draft only',
                time_minutes: 20
              }
            }
          },
          {
            name: 'Discovery folders',
            status: 'ready',
            proposal: {
              id: 'root-id',
              kind: 'root_review',
              digest: 'root-draft',
              reason: '<script>untrusted</script>',
              agent_name: 'Manager'
            }
          },
          {
            name: 'Album-4',
            status: 'ready',
            proposal: {
              id: 'recap-id',
              kind: 'session_recap',
              entry_id: 'entry-4',
              digest: 'recap-draft',
              recap: '<script>untrusted recap</script>',
              agent_name: 'Manager'
            }
          }
        ]
      };
    };
    await panel.renderProposals();
    assert.equal(elements.get('projectLibraryProposals').hidden, false);
    const [ready, stale, navigation, goal, root, recap] = elements.get(
      'projectLibraryProposalRows'
    ).children;
    assert.equal(ready.children[0].textContent, '<svg onload=alert(1)>');
    assert.equal(
      ready.children[1].textContent,
      'Suggested next action: <script>untrusted</script>'
    );
    assert.equal(ready.children[4].tag, 'button');
    assert.equal(stale.children.filter(child => child.tag === 'button').length, 0);
    assert.match(navigation.children[1].textContent, /No setup was reviewed or authorized/);
    assert.equal(
      navigation.children[2].textContent,
      "Manager's reason (untrusted note): <script>untrusted setup note</script>"
    );
    assert.equal(navigation.children[4].textContent, 'View Album-4 setup options');
    navigation.children[4].click();
    assert.deepEqual(destinations, ['entry-4']); // only normal owner Details, no creator call
    assert.equal(goal.children[1].textContent.includes('<script>do not execute</script>'), true);
    assert.equal(goal.children[4].textContent, 'Edit Album-1 suggested goal');
    assert.match(root.children[1].textContent, /No folder was selected, approved or scanned/);
    assert.equal(
      root.children[2].textContent,
      "Manager's reason (untrusted note): <script>untrusted</script>"
    );
    assert.equal(root.children[4].textContent, 'View discovery folder options');
    assert.match(recap.children[1].textContent, /<script>untrusted recap<\/script>/);
    assert.match(recap.children[1].textContent, /not evidence that work happened/);
    assert.equal(recap.children[4].textContent, 'Edit Album-4 suggested recap');
    panel.state.provider_read_only = true;
    await panel.renderProposals();
    assert.equal(
      elements
        .get('projectLibraryProposalRows')
        .children[0].children.filter(child => child.tag === 'button').length,
      0
    );
  } finally {
    globalThis.document = original;
  }
});

test('Manager discovery suggestion focuses only current owner controls after freshness check', async () => {
  const original = globalThis.document;
  const events = [];
  const roots = {
    setAttribute: (...args) => events.push(args),
    scrollIntoView: () => events.push('scroll'),
    focus: () => events.push('focus')
  };
  globalThis.document = { getElementById: id => (id === 'projectLibraryRoots' ? roots : null) };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    const row = { proposal: { id: 'root', digest: 'exact', kind: 'root_review' } };
    let status = 'ready';
    let refreshes = 0;
    panel.run = async (_trigger, _message, work) => work();
    panel.refresh = async () => {
      refreshes++;
    };
    panel.status = text => events.push(text);
    panel.request = async path => {
      assert.equal(path, '/proposals');
      return { rows: [{ ...row, status }] };
    };
    await panel.viewRootProposal(row, null);
    assert.equal(refreshes, 1);
    assert.deepEqual(events.slice(0, 3), [['tabindex', '-1'], 'scroll', 'focus']);
    status = 'stale';
    await assert.rejects(() => panel.viewRootProposal(row, null), /Discovery folders changed/);
    assert.equal(events.filter(event => event === 'focus').length, 1);
  } finally {
    globalThis.document = original;
  }
});

test('Manager recap draft rechecks entry and exact accepted session before editable owner form', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  const row = {
    proposal: {
      id: 'draft',
      digest: 'exact',
      kind: 'session_recap',
      entry_id: 'song',
      entry_revision: 2,
      fields_revision: 3,
      session_id: 'session',
      session_revision: 1,
      recap: '<script>inert</script>'
    }
  };
  let status = 'ready';
  let fields = 3;
  let sessionRevision = 1;
  const opened = [];
  panel.run = async (_trigger, _message, work) => work();
  panel.request = async path => {
    if (path === '/proposals') return { rows: [{ ...row, status }] };
    if (path === '/projects/song')
      return { entry_revision: 2, row: { id: 'song', fields_revision: fields } };
    assert.equal(path, '/projects/song/sessions/session');
    return {
      id: 'session',
      entry_id: 'song',
      revision: sessionRevision,
      state: 'accepted',
      recap: ''
    };
  };
  panel.sessionForm = (...args) => opened.push(args);
  await panel.editRecapProposal(row, null);
  assert.equal(opened.length, 1);
  assert.equal(opened[0][1].id, 'session');
  assert.equal(opened[0][3], null);
  assert.equal(opened[0][4], '<script>inert</script>'); // Data only, not a saved recap.
  status = 'stale';
  await assert.rejects(() => panel.editRecapProposal(row, null), /no longer current/);
  status = 'ready';
  fields = 4;
  await assert.rejects(() => panel.editRecapProposal(row, null), /song changed/);
  fields = 3;
  sessionRevision = 2;
  await assert.rejects(() => panel.editRecapProposal(row, null), /saved session changed/);
  assert.equal(opened.length, 1);
});

test('Manager goal suggestion rechecks freshness then opens only the editable owner form', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  const goal = {
    goal: '<script>untrusted</script>',
    desired_outcome: 'Write a take list',
    time_minutes: 20
  };
  const row = {
    proposal: {
      id: 'suggestion',
      digest: 'exact',
      kind: 'session_goal',
      entry_id: 'entry',
      entry_revision: 2,
      goal
    }
  };
  let status = 'ready';
  let revision = 2;
  const opened = [];
  panel.run = async (_trigger, _message, work) => work();
  panel.request = async path => {
    if (path === '/proposals') return { rows: [{ ...row, status }] };
    assert.equal(path, '/projects/entry');
    return { entry_revision: revision, row: { id: 'entry' } };
  };
  panel.sessionForm = (...args) => opened.push(args);
  await panel.editGoalProposal(row, null);
  assert.equal(opened.length, 1);
  assert.equal(opened[0][1], null); // Not a recap or an automatic save.
  assert.deepEqual(opened[0][3], goal);
  status = 'stale';
  await assert.rejects(() => panel.editGoalProposal(row, null), /no longer current/);
  status = 'ready';
  revision = 3;
  await assert.rejects(() => panel.editGoalProposal(row, null), /song changed/);
  assert.equal(opened.length, 1);
});

test('serial queue recovery retains only bounded Home-scoped navigation and exact pending retry', () => {
  const now = Date.now();
  const key = 'ori:library-queue:home';
  const values = new Map();
  const storage = { getItem: id => values.get(id) || null };
  const valid = {
    home_id: 'home',
    ids: ['one', 'two'],
    index: 1,
    created_at: now,
    pending: { id: 'two', token: 'review-token', key: 'confirmed-key' }
  };
  values.set(key, JSON.stringify(valid));
  assert.deepEqual(readActivationQueue('home', storage, now), valid);
  assert.equal(readActivationQueue('foreign', storage, now), null);
  for (const changed of [
    { ids: ['one', 'one'] },
    { ids: Array.from({ length: 101 }, (_, i) => `id-${i}`) },
    { index: 2 },
    { pending: { id: 'one', token: 'review-token', key: 'confirmed-key' } },
    { created_at: now - 25 * 60 * 60 * 1000 }
  ]) {
    values.set(key, JSON.stringify({ ...valid, ...changed }));
    assert.equal(readActivationQueue('home', storage, now), null);
  }
});

test('Home queue outcomes show bounded counts without retaining song identity', async () => {
  const original = globalThis.document;
  const section = { hidden: true };
  const rows = {
    children: [],
    replaceChildren() {
      this.children = [];
    },
    append(child) {
      this.children.push(child);
    }
  };
  globalThis.document = {
    createElement: tag => ({ tag, textContent: '' }),
    getElementById: id =>
      ({ projectLibraryQueueHistory: section, projectLibraryQueueHistoryRows: rows })[id]
  };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    panel.recentQueues = [
      {
        status: 'complete',
        finished_at: '2026-09-27T12:00:00Z',
        connected_count: 1,
        skipped_count: 2
      },
      {
        status: 'discarded',
        finished_at: '2026-09-26T12:00:00Z',
        connected_count: 0,
        skipped_count: 0
      }
    ];
    panel.renderQueueHistory();
    assert.equal(section.hidden, false);
    assert.equal(rows.children.length, 2);
    assert.match(rows.children[0].textContent, /1 connected · 2 skipped/);
    assert.match(rows.children[1].textContent, /Unreviewed songs were not activated/);
    assert.doesNotMatch(rows.children[0].textContent, /private|Song\.rpp/);
    panel.recentQueues = [];
    panel.renderQueueHistory();
    assert.equal(section.hidden, true);
    assert.equal(rows.children.length, 0);
  } finally {
    globalThis.document = original;
  }
});

test('Home queue restores order after browser storage loss and never treats local state as authority', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  const serverQueue = {
    id: 'home-queue',
    ids: ['first', 'second'],
    index: 1,
    skipped: ['first'],
    revision: 2,
    status: 'active',
    created_at: new Date().toISOString()
  };
  panel.request = async path => {
    assert.equal(path, '/queue');
    return { queue: serverQueue };
  };
  panel.saveQueue = () => true; // no storage needed for Home queue navigation
  await panel.restoreQueue();
  assert.equal(panel.queue.id, 'home-queue');
  assert.equal(panel.queue.index, 1);
  assert.deepEqual(panel.queue.skipped, ['first']);
  assert.equal(panel.queue.pending, null);
  let outcome = 'none';
  panel.post = async (path, input) => {
    assert.equal(path, '/queue/home-queue/progress');
    assert.equal(input.entry_id, 'second');
    assert.equal(input.action, 'skip');
    assert.equal(input.if_revision, 2);
    outcome = 'skip';
    return {
      queue: {
        ...serverQueue,
        index: 2,
        skipped: ['first', 'second'],
        revision: 3,
        status: 'complete'
      }
    };
  };
  await panel.progressQueue('skip', 'second');
  assert.equal(outcome, 'skip');
  assert.equal(panel.queue.index, 2);
  assert.equal(panel.queue.revision, 3);
});

test('serial activation pauses after a skip without issuing even a review request', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { provider_read_only: false };
  panel.queue = {
    home_id: 'home',
    ids: ['first', 'second'],
    index: 0,
    created_at: Date.now(),
    pending: null
  };
  panel.run = async (_trigger, _message, work) => work();
  panel.saveQueue = () => true;
  panel.restoreQueue = async () => {};
  panel.status = () => {};
  const requests = [];
  panel.request = async path => {
    requests.push(path);
    return path.endsWith('/activation')
      ? { state: 'review_available' }
      : {
          row: {
            id: path.split('/').at(-1),
            name: path.split('/').at(-1),
            connection: 'catalog_only'
          }
        };
  };
  panel.post = async () => {
    throw new Error('skip/pause must not create a review or project');
  };
  const actions = ['skip', 'pause'];
  panel.queueChoice = async () => actions.shift();
  await panel.continueQueue();
  assert.equal(panel.queue.index, 1);
  assert.equal(panel.queue.pending, null);
  assert.deepEqual(requests, [
    '/projects/first',
    '/projects/first/activation',
    '/projects/second',
    '/projects/second/activation'
  ]);
});

test('saved queue can explicitly skip revoked or forgotten songs without offering creator review', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { provider_read_only: false };
  panel.queue = {
    home_id: 'home',
    id: 'saved-queue',
    ids: ['revoked', 'forgotten'],
    index: 0,
    revision: 1,
    status: 'active',
    skipped: [],
    created_at: Date.now(),
    pending: null
  };
  panel.run = async (_trigger, _message, work) => work();
  panel.restoreQueue = async () => {};
  panel.saveQueue = () => true;
  panel.status = () => {};
  const requests = [],
    choices = [],
    progress = [];
  panel.request = async path => {
    requests.push(path);
    if (path === '/projects/revoked')
      return { row: { name: 'Revoked song', connection: 'catalog_only' } };
    if (path === '/projects/revoked/activation')
      return { state: 'revoked_source', reason: 'Discovery folder disconnected' };
    const error = new Error('No saved record');
    error.status = 404;
    throw error;
  };
  panel.queueChoice = async (_name, position, _count, _trigger, reason) => {
    choices.push({ position, reason });
    return choices.length === 1 ? 'pause' : 'skip';
  };
  panel.post = async (path, body) => {
    assert.equal(path, '/queue/saved-queue/progress');
    assert.equal(body.action, 'skip');
    assert.equal(body.entry_id, panel.queue.ids[panel.queue.index]);
    progress.push(body.entry_id);
    return {
      queue: {
        ...panel.queue,
        index: panel.queue.index + 1,
        revision: panel.queue.revision + 1,
        skipped: [...panel.queue.skipped, body.entry_id],
        status: panel.queue.index === 1 ? 'complete' : 'active',
        created_at: new Date().toISOString()
      }
    };
  };
  await panel.continueQueue();
  assert.deepEqual(progress, []);
  assert.equal(panel.queue.index, 0);
  await panel.continueQueue();
  assert.deepEqual(progress, ['revoked', 'forgotten']);
  assert.equal(panel.queue, null);
  assert.deepEqual(
    choices.map(choice => choice.position),
    [1, 1, 2]
  );
  assert.match(choices[0].reason, /Discovery folder disconnected/);
  assert.match(choices[2].reason, /catalog record was removed/);
  assert.deepEqual(requests, [
    '/projects/revoked',
    '/projects/revoked/activation',
    '/projects/revoked',
    '/projects/revoked/activation',
    '/projects/forgotten'
  ]);
});

test('serial activation persists a distinct confirmed key before each creator commit', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { provider_read_only: false };
  panel.queue = {
    home_id: 'home',
    ids: ['first', 'second'],
    index: 0,
    created_at: Date.now(),
    pending: null
  };
  panel.run = async (_trigger, _message, work) => work();
  panel.status = () => {};
  panel.refresh = async () => {};
  panel.restoreQueue = async () => {};
  panel.queueChoice = async () => 'review';
  panel.activationInput = async detail => ({
    workspace_name: detail.row.name,
    project_file: 'Song.rpp',
    if_revision: detail.revision
  });
  panel.confirm = async () => true;
  const connected = new Set();
  panel.request = async path =>
    path.endsWith('/activation')
      ? { state: 'review_available' }
      : {
          revision: 1,
          row: {
            id: path.split('/').at(-1),
            name: path.split('/').at(-1),
            connection: connected.has(path.split('/').at(-1)) ? 'connected' : 'catalog_only'
          }
        };
  const storedBeforeCommit = [];
  let lastSaved = null;
  panel.saveQueue = () => {
    lastSaved = panel.queue && structuredClone(panel.queue);
    return true;
  };
  panel.post = async (path, body) => {
    if (path.endsWith('/review'))
      return {
        token: path,
        workspace_name: path.split('/')[2],
        project_file: 'Song.rpp',
        statement: 'File-only'
      };
    storedBeforeCommit.push(lastSaved?.pending);
    assert.equal(lastSaved?.pending?.key, body.idempotency_key);
    connected.add(path.split('/')[2]);
    return { workspace_id: 'child' };
  };
  await panel.continueQueue();
  assert.equal(panel.queue, null);
  assert.equal(connected.size, 2);
  assert.equal(storedBeforeCommit.length, 2);
  assert.notEqual(storedBeforeCommit[0].key, storedBeforeCommit[1].key);
});

test('serial queue resumes after a lost creator response without creating the connected song twice', async () => {
  const previousStorage = globalThis.sessionStorage;
  const values = new Map();
  globalThis.sessionStorage = {
    getItem: key => values.get(key) || null,
    setItem: (key, value) => values.set(key, value),
    removeItem: key => values.delete(key)
  };
  try {
    let connected = false;
    let commits = 0;
    let refreshes = 0;
    let lastStatus = '';
    const configure = panel => {
      panel.state = { provider_read_only: false };
      panel.run = async (_trigger, _message, work) => work();
      panel.status = message => {
        lastStatus = message;
      };
      panel.refresh = async () => {
        refreshes++;
      };
      panel.renderQueueControls = () => {};
      panel.restoreQueue = async () => {};
      panel.request = async path =>
        path.endsWith('/activation')
          ? { state: 'review_available' }
          : {
              row: {
                id: path.split('/')[2],
                name: 'Album',
                connection: connected ? 'connected' : 'catalog_only'
              }
            };
    };
    const first = new ProjectLibraryPanel({ workspaceId: 'home' });
    configure(first);
    first.queue = {
      home_id: 'home',
      ids: ['first', 'second'],
      index: 0,
      created_at: Date.now(),
      pending: null
    };
    first.queueChoice = async () => 'review';
    first.activationInput = async () => ({ workspace_name: 'Album', project_file: 'Song.rpp' });
    first.confirm = async () => true;
    first.post = async path => {
      if (path.endsWith('/review'))
        return { token: 'exact-review', workspace_name: 'Album', project_file: 'Song.rpp' };
      commits++;
      connected = true;
      throw new Error('response lost after creator committed');
    };
    await first.continueQueue();
    assert.equal(commits, 1, lastStatus);
    assert.equal(first.queue.index, 0);
    assert.equal(refreshes, 1);
    assert.match(lastStatus, /resume with this tab’s confirmed key/);
    assert.match(lastStatus, /Review linked projects shelf/);
    const persisted = readActivationQueue('home', globalThis.sessionStorage);
    assert.equal(persisted.pending.id, 'first');
    assert.equal(persisted.pending.token, 'exact-review');

    const resumed = new ProjectLibraryPanel({ workspaceId: 'home' });
    configure(resumed);
    resumed.queueChoice = async () => 'skip';
    resumed.post = async () => {
      throw new Error('no creator or review request permitted on replay');
    };
    await resumed.continueQueue();
    assert.equal(commits, 1);
    assert.equal(connected, true);
    assert.equal(resumed.queue, null);
    assert.equal(values.has('ori:library-queue:home'), false);
  } finally {
    globalThis.sessionStorage = previousStorage;
  }
});

test('serial queue retries the exact confirmed operation after creator success but absent Home association', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { provider_read_only: false };
  panel.queue = {
    home_id: 'home',
    ids: ['first', 'second'],
    index: 0,
    created_at: Date.now(),
    pending: { id: 'first', token: 'review-1', key: 'first-confirmed-key' }
  };
  panel.run = async (_trigger, _message, work) => work();
  panel.saveQueue = () => true;
  panel.restoreQueue = async () => {};
  panel.status = () => {};
  panel.refresh = async () => {};
  panel.request = async path =>
    path.endsWith('/activation')
      ? { state: 'review_available' }
      : { row: { id: path.split('/')[2], name: 'Album', connection: 'catalog_only' } };
  panel.queueChoice = async () => 'pause';
  const attempts = [];
  panel.post = async (path, body) => {
    attempts.push({ path, body });
    return { workspace_id: 'creator-child' };
  };
  await panel.continueQueue();
  assert.deepEqual(attempts, [
    {
      path: '/projects/first/activation/commit',
      body: { review_token: 'review-1', idempotency_key: 'first-confirmed-key', confirm: true }
    }
  ]);
  assert.equal(panel.queue.index, 1);
  assert.equal(panel.queue.pending, null);
});

test('Home resume renders only bounded saved-user cards with trusted detail navigation', async () => {
  const previousDocument = globalThis.document;
  const makeElement = tag => ({
    tag,
    children: [],
    textContent: '',
    hidden: false,
    append(...items) {
      this.children.push(...items);
    },
    replaceChildren() {
      this.children = [];
    },
    addEventListener(name, callback) {
      this[name] = callback;
    }
  });
  const section = makeElement('section');
  const container = makeElement('div');
  globalThis.document = {
    createElement: makeElement,
    getElementById(id) {
      return { projectLibraryResume: section, projectLibraryResumeCards: container }[id];
    }
  };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    const requests = [];
    panel.request = async path => {
      requests.push(path);
      return {
        cards: Array.from({ length: 4 }, (_, index) => ({
          entry_id: `id-${index}`,
          name: `<Song ${index}>`,
          project_next_action: 'Next',
          session: { goal: 'Listen', recap: 'Saved by user', updated_at: '2026-09-27T00:00:00Z' }
        }))
      };
    };
    panel.details = id => requests.push(`open:${id}`);
    await panel.renderResume();
    assert.deepEqual(requests, ['/resume']);
    assert.equal(section.hidden, false);
    assert.equal(container.children.length, 3);
    assert.equal(container.children[0].children[0].textContent, '<Song 0>');
    const button = container.children[0].children.at(-1);
    button.click();
    assert.deepEqual(requests, ['/resume', 'open:id-0']);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('resume workspace action refuses navigation after the exact link changes', async () => {
  const previousDocument = globalThis.document;
  const makeElement = tag => ({
    tag,
    children: [],
    textContent: '',
    hidden: false,
    append(...items) {
      this.children.push(...items);
    },
    replaceChildren() {
      this.children = [];
    },
    addEventListener(name, callback) {
      this[name] = callback;
    }
  });
  const section = makeElement('section');
  const container = makeElement('div');
  globalThis.document = {
    createElement: makeElement,
    getElementById(id) {
      return { projectLibraryResume: section, projectLibraryResumeCards: container }[id];
    }
  };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    panel.run = async (_trigger, _message, work) => work();
    const messages = [];
    panel.status = message => messages.push(message);
    const requests = [];
    panel.request = async path => {
      requests.push(path);
      if (path.endsWith('/activation'))
        return { state: 'link_needs_review', workspace_id: 'stale-child' };
      return {
        cards: [
          {
            entry_id: 'saved-song',
            name: 'Song',
            workspace_id: 'stale-child',
            session: { goal: 'Listen', updated_at: '2026-09-27T00:00:00Z' }
          }
        ]
      };
    };
    await panel.renderResume();
    const workspace = container.children[0].children.at(-1);
    assert.equal(workspace.textContent, 'Open Song workspace');
    workspace.click();
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(requests, ['/resume', '/projects/saved-song/activation', '/resume']);
    assert.match(messages.at(-1), /link changed/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('libraryQuery includes only bounded search fields and cursor', () => {
  const params = new URLSearchParams(
    libraryQuery({
      text: '  Mix  ',
      stage: 'mixing',
      format: 'reaper',
      rootID: 'server-root-id',
      availability: 'available',
      priority: '0',
      sort: 'scanned_at',
      direction: 'desc',
      cursor: 'next'
    })
  );
  assert.equal(params.get('text'), 'Mix');
  assert.equal(params.get('page_size'), '25');
  assert.equal(params.get('stage'), 'mixing');
  assert.equal(params.get('format'), 'reaper');
  assert.equal(params.get('root_id'), 'server-root-id');
  assert.equal(params.get('availability'), 'available');
  assert.equal(params.get('priority'), '0');
  assert.equal(params.get('sort'), 'scanned_at');
  assert.equal(params.get('direction'), 'desc');
  assert.equal(params.get('cursor'), 'next');
  assert.equal(params.has('path'), false);
});

test('out-of-order search responses and double-clicked More cannot mix or duplicate pages', async () => {
  const priorDocument = globalThis.document;
  const elements = {
    projectLibraryRows: { replaceChildren() {} },
    projectLibraryMore: { hidden: false, disabled: false },
    projectLibraryCount: { textContent: '' },
    projectLibrarySearch: { value: 'first' },
    projectLibraryStage: { value: '' },
    projectLibraryStatusFilter: { value: '' },
    projectLibraryConnection: { value: '' },
    projectLibraryAvailability: { value: '' },
    projectLibraryPriority: { value: '' },
    projectLibrarySort: { value: 'name' }
  };
  globalThis.document = { getElementById: id => elements[id] };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    panel.state = { provider_read_only: false };
    panel.renderRows = () => {};
    panel.status = () => {};
    const requests = [];
    panel.request = path =>
      new Promise(resolve => {
        requests.push({ path, resolve });
      });
    const first = panel.search(false);
    elements.projectLibrarySearch.value = 'second';
    const second = panel.search(false);
    assert.equal(requests.length, 2);
    requests[1].resolve({ rows: [{ id: 'new' }], total: 1, next_cursor: 'next' });
    await second;
    requests[0].resolve({ rows: [{ id: 'old' }], total: 1, next_cursor: 'old-next' });
    await first;
    assert.deepEqual(
      panel.rows.map(row => row.id),
      ['new']
    );
    assert.equal(panel.cursor, 'next');
    assert.match(requests[1].path, /text=second/);

    const more = panel.search(true);
    const duplicate = panel.search(true);
    assert.equal(requests.length, 3, 'only one cursor request may be in flight');
    assert.equal(elements.projectLibraryMore.disabled, true);
    requests[2].resolve({ rows: [{ id: 'next' }], total: 2 });
    await Promise.all([more, duplicate]);
    assert.deepEqual(
      panel.rows.map(row => row.id),
      ['new', 'next']
    );
    assert.equal(elements.projectLibraryMore.disabled, false);
    assert.equal(elements.projectLibraryMore.hidden, true);
  } finally {
    globalThis.document = priorDocument;
  }
});

test('folder selection and consent never scan after a canceled second review', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home', program: { is_station: true } });
  panel.state = { revision: 1 };
  const calls = [];
  panel.run = async (_, __, fn) => fn();
  panel.post = async (path, body) => {
    calls.push([path, body]);
    if (path === '/roots/pick') return { selection_token: 'picker' };
    if (path === '/roots/review')
      return { token: 'review', root_path: '/chosen', scope: 'metadata only' };
    return { root_id: 'root-1' };
  };
  panel.confirm = async () => calls.filter(([path]) => path === '/roots/commit').length === 0;
  panel.refresh = async () => {
    panel.state.revision = 3;
  };
  panel.scanRootFlow = async () => calls.push(['/scan', null]);
  await panel.addFolder(null);
  assert.deepEqual(
    calls.map(([path]) => path),
    ['/roots/pick', '/roots/review', '/roots/commit']
  );
  assert.equal(calls[2][1].confirm, true);
  assert.equal(calls[1][1].selection_token, 'picker');
  assert.equal(Object.hasOwn(calls[1][1], 'path'), false);
});

test('resolved portfolio source uses the offer ID, not a browser path or another picker', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home', program: { is_station: true } });
  panel.state = { revision: 1 };
  panel.offerID = 'resolved-offer';
  panel.run = async (_, __, fn) => fn();
  const calls = [];
  panel.post = async (path, body) => {
    calls.push([path, body]);
    if (path === '/roots/pick-offer') return { selection_token: 'scoped-token' };
    if (path === '/roots/review')
      return { token: 'review', root_path: '/trusted', scope: 'metadata only' };
    return { root_id: 'new-root' };
  };
  panel.confirm = async () => calls.filter(([path]) => path === '/roots/commit').length === 0;
  panel.refresh = async () => {
    panel.state = { revision: 3, initialized: true };
  };
  await panel.addFolder(null);
  assert.deepEqual(
    calls.map(([path]) => path),
    ['/roots/pick-offer', '/roots/review', '/roots/commit']
  );
  assert.deepEqual(calls[0][1], { offer_id: 'resolved-offer' });
  assert.equal(panel.offerID, '');
});

test('declining the first review leaves neither a root nor a scan', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home', program: { is_station: true } });
  panel.state = { revision: 1 };
  const calls = [];
  panel.run = async (_, __, fn) => fn();
  panel.post = async path => {
    calls.push(path);
    return {
      selection_token: 'picker',
      token: 'review',
      root_path: '/chosen',
      scope: 'metadata only'
    };
  };
  panel.confirm = async () => false;
  await panel.addFolder(null);
  assert.deepEqual(calls, ['/roots/pick', '/roots/review']);
});

test('root paging never merges a page from a different library revision', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = {
    revision: 7,
    total_roots: 21,
    next_offset: 20,
    provider_read_only: false,
    roots: [{ id: 'first' }]
  };
  panel.run = async (_, __, fn) => fn();
  const calls = [];
  panel.renderRoots = () => calls.push('render');
  panel.refresh = async () => {
    calls.push('refresh');
    panel.state = { revision: 8, roots: [] };
  };
  panel.status = message => calls.push(message);
  panel.request = async path => {
    assert.equal(path, '/roots?offset=20');
    return {
      revision: 7,
      total_roots: 21,
      next_offset: 0,
      provider_read_only: false,
      roots: [{ id: 'last' }]
    };
  };
  await panel.moreRoots(null);
  assert.deepEqual(
    panel.state.roots.map(root => root.id),
    ['first', 'last']
  );
  assert.equal(panel.state.next_offset, 0);
  assert.deepEqual(calls, ['render']);
  panel.state.next_offset = 20;
  panel.request = async () => ({
    revision: 8,
    total_roots: 21,
    next_offset: 0,
    provider_read_only: false,
    roots: [{ id: 'unsafe' }]
  });
  await panel.moreRoots(null);
  assert.deepEqual(panel.state.roots, []);
  assert.equal(calls[1], 'refresh');
});

test('a saved narrower scope requires its own review and never sends a browser path', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { revision: 7 };
  const calls = [];
  panel.post = async (path, body) => {
    calls.push([path, body]);
    return {
      token: 'scope-review',
      relative_folder: 'Track/Extra',
      root_path: '/reviewed',
      scope: 'metadata only',
      max_entries: 5000
    };
  };
  panel.confirm = async () => false;
  panel.refresh = async () => {
    panel.state.revision++;
  };
  panel.status = () => {};
  await panel.scanRootFlow('root-1', null, 'server-scope-id');
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0], [
    '/roots/root-1/scans/review',
    { if_revision: 7, scope_id: 'server-scope-id' }
  ]);
  panel.confirm = async () => true;
  panel.post = async (path, body) => {
    calls.push([path, body]);
    if (path.endsWith('/review'))
      return {
        token: 'scope-review',
        relative_folder: 'Track/Extra',
        root_path: '/reviewed',
        scope: 'metadata only',
        max_entries: 5000
      };
    return { status: 'complete', entries_seen: 1, skipped_links: 0, skipped_other: 0 };
  };
  await panel.scanRootFlow('root-1', null, 'server-scope-id');
  assert.deepEqual(calls[1][1], { if_revision: 8, scope_id: 'server-scope-id' });
  assert.equal(calls[2][0], '/roots/root-1/scans/commit');
  assert.equal(calls[2][1].scope_id, 'server-scope-id');
  assert.equal(calls[2][1].confirm, true);
  assert.equal(Object.hasOwn(calls[2][1], 'relative_folder'), false);
});

test('Manager suggestion requires a separate owner review and confirmation to edit the Home note', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.run = async (_trigger, _message, work) => work();
  panel.status = () => {};
  const calls = [];
  panel.post = async (path, body) => {
    calls.push({ path, body });
    if (path.endsWith('/review'))
      return {
        token: 'owner-review',
        before: { next_action: 'Current note' },
        after: { next_action: 'Proposed note' }
      };
    return { fields: { next_action: 'Proposed note' } };
  };
  panel.refresh = async () => {
    calls.push({ path: 'refresh' });
  };
  const row = {
    name: 'Song',
    proposal: {
      id: 'server-proposal',
      next_action: 'Proposed note',
      reason: 'Untrusted text <script>'
    }
  };
  panel.confirm = async (_title, lines) => {
    assert.ok(lines.some(line => line.includes('Untrusted text <script>')));
    return false;
  };
  await panel.reviewProposal(row);
  assert.deepEqual(
    calls.map(call => call.path),
    ['/proposals/server-proposal/review', 'refresh']
  );
  calls.length = 0;
  panel.confirm = async () => true;
  await panel.reviewProposal(row);
  assert.deepEqual(
    calls.map(call => call.path),
    ['/proposals/server-proposal/review', '/proposals/server-proposal/commit', 'refresh']
  );
  assert.deepEqual(Object.keys(calls[1].body).sort(), [
    'confirm',
    'idempotency_key',
    'review_token'
  ]);
});

test('manual edit review cannot commit without a second user confirmation', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  const calls = [];
  panel.run = async (_, __, fn) => fn();
  panel.post = async path => {
    calls.push(path);
    return { token: 'review' };
  };
  panel.confirm = async () => false;
  await panel.saveFields(
    { row: { id: 'entry-1', name: 'Track', fields_revision: 0 } },
    { next_action: 'Review mix' },
    null
  );
  assert.deepEqual(calls, ['/projects/entry-1/fields/review']);
});

const DIGEST = {
  scan_id: 'scan-1',
  root_id: 'root-1',
  scanned_at: '2026-09-29T12:00:00Z',
  coverage: 'complete',
  projects: 6,
  new: 6,
  updated: 0,
  unavailable: 0,
  unsupported_format: 1,
  activatable: 5
};

test('scan digest line names the folder, not its path, and lists only nonzero extras', () => {
  const roots = [{ id: 'root-1', path: '/Users/owner/Music Projects' }];
  const text = libraryDigestText(DIGEST, roots);
  assert.match(
    text,
    /^Scanned Music Projects on .*2026: 6 projects, 6 new, 5 can be set up, 1 unsupported format\.$/
  );
  assert.equal(text.includes('/Users/owner'), false);
  const quiet = libraryDigestText(
    { ...DIGEST, projects: 1, new: 0, unsupported_format: 0, activatable: 1 },
    roots
  );
  assert.match(quiet, /: 1 project, 1 can be set up\.$/);
  const gone = libraryDigestText({ ...DIGEST, new: 0, unavailable: 2 }, roots);
  assert.match(gone, /5 can be set up, 1 unsupported format, 2 no longer found\.$/);
});

test('scan digest line handles zero, partial, unknown-root and setup-unavailable digests', () => {
  assert.equal(libraryDigestText(null, []), '');
  assert.equal(libraryDigestText({ projects: 3 }, []), '', 'a digest without a scan is not shown');
  const empty = libraryDigestText(
    { ...DIGEST, projects: 0, new: 0, activatable: 0, unsupported_format: 0 },
    []
  );
  assert.match(empty, /^Scanned an approved folder on .*: 0 projects, 0 can be set up\.$/);
  const partial = libraryDigestText({ ...DIGEST, coverage: 'partial' }, []);
  assert.match(partial, /Partial scan: folders it did not reach were left unchanged\.$/);
  const noIntegration = libraryDigestText(
    {
      ...DIGEST,
      activatable: 0,
      unsupported_format: 0,
      setup_note: 'project_provider_unavailable'
    },
    []
  );
  assert.match(
    noIntegration,
    /6 projects, 6 new, project setup needs a compatible installed integration\./
  );
  assert.equal(noIntegration.includes('can be set up'), false);
  const forged = libraryDigestText(
    { ...DIGEST, projects: -4, new: '9', activatable: 1.5, scanned_at: 'not a date' },
    []
  );
  assert.match(forged, /on an unknown date: 0 projects, 0 can be set up, 1 unsupported format\./);
});

test('the Manager review line names skips, results and stop reasons without claiming any change', () => {
  assert.equal(libraryRunText(null), '');
  assert.equal(
    libraryRunText({ status: 'skipped', reason: 'no_model' }),
    'Manager review skipped: the Manager has no tool-capable model configured.'
  );
  assert.equal(
    libraryRunText({ status: 'skipped', reason: 'interrupted' }),
    'Manager review skipped: Ori stopped during the review.'
  );
  assert.equal(
    libraryRunText({ status: 'skipped', reason: '<img src=x>' }),
    'Manager review skipped: it could not run.',
    'an unknown reason is never echoed'
  );
  assert.equal(
    libraryRunText({
      status: 'finished',
      proposals: 3,
      model: 'claude-x',
      reason: 'proposal_limit'
    }),
    'The Manager (claude-x) reviewed this scan and left 3 suggestions for your review. It stopped at the three-suggestion limit.'
  );
  assert.equal(
    libraryRunText({ status: 'finished', proposals: 0, reason: '' }),
    'The Manager reviewed this scan and had no suggestions.'
  );
  assert.match(libraryRunText({ status: 'started' }), /in progress/);
  assert.equal(
    proposalSourceLabel({ source: 'manager_model', model: 'claude-x' }),
    'From the scan review · claude-x'
  );
  assert.equal(proposalSourceLabel({}), 'From a Manager chat');
  assert.equal(proposalSourceLabel({ source: 'forged' }), 'From a Manager chat');
});

test('arriving with the shelf hash focuses the suggestions heading once, after it renders', () => {
  const originalDocument = globalThis.document;
  const originalLocation = globalThis.location;
  const calls = [];
  const heading = id => ({
    setAttribute: (name, value) => calls.push(`${id}:${name}=${value}`),
    scrollIntoView: () => calls.push(`${id}:scroll`),
    focus: options => calls.push(`${id}:focus:${options?.preventScroll}`)
  });
  const section = { hidden: false };
  const elements = {
    projectLibraryProposals: section,
    projectLibraryProposalsTitle: heading('suggestions'),
    projectLibraryTitle: heading('library')
  };
  globalThis.document = { getElementById: id => elements[id] || null };
  try {
    globalThis.location = { hash: '#projectLibraryProposals' };
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    panel.focusArrival();
    panel.focusArrival(); // a later refresh never steals focus again
    assert.deepEqual(calls, [
      'suggestions:tabindex=-1',
      'suggestions:scroll',
      'suggestions:focus:true'
    ]);
    calls.length = 0;
    section.hidden = true; // nothing to review: land on the library heading
    new ProjectLibraryPanel({ workspaceId: 'home' }).focusArrival();
    assert.deepEqual(calls, ['library:tabindex=-1', 'library:scroll', 'library:focus:true']);
    calls.length = 0;
    globalThis.location = { hash: '#projectLibraryPanel' };
    new ProjectLibraryPanel({ workspaceId: 'home' }).focusArrival();
    assert.deepEqual(calls, [], 'other arrivals keep the page’s normal focus');
  } finally {
    globalThis.document = originalDocument;
    globalThis.location = originalLocation;
  }
});

test('scan digest renders as inert text and shows the shelf even with no suggestions', async () => {
  const original = globalThis.document;
  const elements = new Map();
  const makeNode = tag => ({
    tag,
    children: [],
    hidden: true,
    textContent: '',
    append(...items) {
      this.children.push(...items);
    },
    replaceChildren() {
      this.children = [];
    },
    addEventListener() {}
  });
  for (const [id, tag] of [
    ['projectLibraryProposals', 'section'],
    ['projectLibraryProposalRows', 'div'],
    ['projectLibraryDigest', 'p']
  ])
    elements.set(id, makeNode(tag));
  globalThis.document = {
    createElement: tag => {
      const element = makeNode(tag);
      element.hidden = false;
      return element;
    },
    getElementById: id => elements.get(id) || null
  };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    panel.state = {
      provider_read_only: false,
      roots: [{ id: 'root-1', path: '/Music/<img src=x onerror=alert(1)>' }]
    };
    const requests = [];
    panel.request = async path => {
      requests.push(path);
      if (path === '/summary') return { initialized: true, digest: DIGEST, ready_proposals: 0 };
      return { total: 0, rows: [] };
    };
    await panel.renderProposals();
    const line = elements.get('projectLibraryDigest');
    assert.equal(line.hidden, false);
    assert.match(line.textContent, /^Scanned <img src=x onerror=alert\(1\)> on /);
    assert.equal('innerHTML' in line, false, 'the digest is assigned as text, never markup');
    assert.equal(elements.get('projectLibraryProposals').hidden, false);
    const [empty] = elements.get('projectLibraryProposalRows').children;
    assert.equal(empty.textContent, 'No Manager suggestions to review for this scan.');
    assert.deepEqual(requests, ['/summary', '/proposals'], 'rendering never starts a scan');

    // No digest and no suggestions: the shelf stays hidden.
    panel.request = async path => (path === '/summary' ? { digest: null } : { total: 0 });
    await panel.renderProposals();
    assert.equal(elements.get('projectLibraryProposals').hidden, true);
    assert.equal(line.hidden, true);

    // An unavailable summary never hides real suggestions or invents a digest.
    panel.request = async path => {
      if (path === '/summary') throw new Error('unavailable');
      return {
        total: 1,
        rows: [
          {
            name: 'Song',
            status: 'ready',
            proposal: { id: 'p', next_action: 'Mix', reason: '', agent_name: 'Manager' }
          }
        ]
      };
    };
    await panel.renderProposals();
    assert.equal(elements.get('projectLibraryProposals').hidden, false);
    assert.equal(line.hidden, true);
    assert.equal(elements.get('projectLibraryProposalRows').children.length, 1);
  } finally {
    globalThis.document = original;
  }
});
