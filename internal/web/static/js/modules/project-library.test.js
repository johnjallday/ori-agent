import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ProjectLibraryPanel,
  activationStateLabel,
  projectTeamURL,
  libraryDigestText,
  libraryOpenAction,
  libraryOpenChoices,
  libraryOpenRoute,
  libraryOpenedMessage,
  librarySharingView,
  libraryQuery,
  libraryRunText,
  proposalSourceLabel,
  readActivationQueue,
  recentlySavedView,
  selectionRecovery,
  setupNextStep
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
    assert.equal(ready.children[5].textContent, 'Dismiss <svg onload=alert(1)> suggestion');
    assert.equal(stale.children.filter(child => child.tag === 'button').length, 0);
    panel.state.provider_read_only = true;
    await panel.renderProposals();
    // A read-only Home offers no review, but dismissing only removes.
    const readOnlyButtons = elements
      .get('projectLibraryProposalRows')
      .children[0].children.filter(child => child.tag === 'button');
    assert.deepEqual(
      readOnlyButtons.map(button => button.textContent),
      ['Dismiss <svg onload=alert(1)> suggestion']
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

test('a granted folder goes straight to the scan review, which still needs its own confirmation', async () => {
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
  panel.scanRootFlow = async rootID => calls.push(['/scan', rootID]);
  await panel.addFolder(null);
  // No separate "scan now?" stop: the grant is followed by the scan review of the
  // root just granted (its own disclosure and cancel), never by a scan itself.
  assert.deepEqual(
    calls.map(([path]) => path),
    ['/roots/pick', '/roots/review', '/roots/commit', '/scan']
  );
  assert.equal(calls[3][1], 'root-1');
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
  panel.scanRootFlow = async rootID => calls.push(['/scan', rootID]);
  await panel.addFolder(null);
  assert.deepEqual(
    calls.map(([path]) => path),
    ['/roots/pick-offer', '/roots/review', '/roots/commit', '/scan']
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
  // The inert review advanced the Home revision, so declining must re-read it.
  panel.refresh = async () => {
    calls.push('refresh');
    panel.state = { revision: 2 };
  };
  const messages = [];
  panel.status = message => messages.push(message);
  await panel.addFolder(null);
  assert.match(messages.at(-1), /not connected\. Nothing was granted or read/);
  assert.deepEqual(calls, ['/roots/pick', '/roots/review', 'refresh']);
  assert.equal(panel.state.revision, 2, 'the next attempt starts from the advanced revision');
});

test('a folder this Home already approved goes to its scan review, never a duplicate root grant', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home', program: { is_station: true } });
  panel.state = { revision: 3 };
  panel.offerID = 'resolved-offer';
  panel.run = async (_, __, fn) => fn();
  const calls = [];
  panel.post = async (path, body) => {
    calls.push([path, body]);
    if (path === '/roots/pick-offer')
      return { selection_token: 'scoped-token', existing_root_id: 'root-1' };
    throw new Error(`unexpected ${path}: an approved folder must not be granted again`);
  };
  panel.refresh = async () => {
    calls.push(['refresh']);
    panel.state = { revision: 3, initialized: true };
  };
  panel.status = () => {};
  panel.scanRootFlow = async (rootID, trigger) => calls.push(['scan', rootID, trigger]);
  await panel.addFolder('the-button');
  assert.deepEqual(
    calls.map(([path]) => path),
    ['/roots/pick-offer', 'refresh', 'scan']
  );
  assert.deepEqual(calls.at(-1), ['scan', 'root-1', 'the-button']);
  assert.equal(panel.offerID, '', 'the spent offer is not carried into the next add');
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

test('scan digest line separates connected songs and file choices from what can be set up', () => {
  const roots = [{ id: 'root-1', path: '/Users/owner/Albums' }];
  const text = libraryDigestText(
    { ...DIGEST, projects: 5, new: 5, connected: 1, activatable: 3, needs_file_choice: 1 },
    roots
  );
  assert.match(
    text,
    /5 projects, 5 new, 1 already connected, 3 can be set up \(1 needs a file choice\)/
  );
  const several = libraryDigestText(
    { ...DIGEST, projects: 6, activatable: 4, needs_file_choice: 2, connected: 0 },
    roots
  );
  assert.match(several, /4 can be set up \(2 need a file choice\)/);
  assert.equal(several.includes('already connected'), false, 'zero is not mentioned');
  // A digest stored before these fields existed reads exactly as it always did.
  assert.equal(libraryDigestText(DIGEST, roots).includes('file choice'), false);
  // Forged or negative values are ignored rather than shown.
  const forged = libraryDigestText({ ...DIGEST, connected: -3, needs_file_choice: 'many' }, roots);
  assert.equal(forged.includes('already connected') || forged.includes('file choice'), false);
  // Connected songs are still reported when setup evidence is missing.
  const noEvidence = libraryDigestText(
    { ...DIGEST, connected: 2, setup_note: 'project_provider_unavailable' },
    roots
  );
  assert.match(
    noEvidence,
    /2 already connected, project setup needs a compatible installed integration/
  );
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

test('dismissal needs its own confirmation and a retry reuses the same key', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.run = async (_trigger, _message, work) => work();
  const messages = [];
  panel.status = message => messages.push(message);
  let refreshes = 0;
  panel.refresh = async () => {
    refreshes++;
  };
  const posts = [];
  let failNext = true;
  panel.post = async (path, body) => {
    posts.push({ path, body });
    if (failNext) {
      failNext = false;
      throw new Error('network dropped the reply');
    }
    return { replay: posts.length > 1 };
  };
  const row = {
    name: 'Song <b>',
    status: 'ready',
    proposal: { id: 'p 1', next_action: '<script>x</script>' }
  };
  let answer = false;
  const titles = [];
  panel.confirm = async (title, lines) => {
    titles.push(title);
    assert.ok(
      lines.some(line => line.includes('No notes, sessions, folders or workspaces change'))
    );
    return answer;
  };
  await panel.dismissProposal(row, null);
  assert.deepEqual(posts, [], 'cancelling sends nothing');
  assert.equal(messages.at(-1), 'Nothing was dismissed.');
  answer = true;
  await assert.rejects(panel.dismissProposal(row, null), /network dropped/);
  await panel.dismissProposal(row, null);
  assert.equal(posts.length, 2);
  assert.equal(posts[0].path, '/proposals/p%201/dismiss');
  assert.deepEqual(Object.keys(posts[0].body).sort(), ['confirm', 'idempotency_key']);
  assert.equal(posts[0].body.confirm, true);
  assert.equal(
    posts[1].body.idempotency_key,
    posts[0].body.idempotency_key,
    'a retried dismissal replays the same answer'
  );
  assert.equal(refreshes, 1);
  assert.equal(titles[0], 'Dismiss the suggestion for Song <b>?');
  assert.equal(panel.pendingDismissals.size, 0);
});

test('feedback line totals the owner’s answers and hides when there are none', async () => {
  const { libraryFeedbackText } = await import('./project-library.js');
  assert.equal(libraryFeedbackText(null), '');
  assert.equal(libraryFeedbackText([{ kind: 'next_action', accepted: 0, dismissed: 0 }]), '');
  assert.equal(
    libraryFeedbackText([
      { kind: 'next_action', accepted: 2, dismissed: 1 },
      { kind: 'project_review', accepted: 0, dismissed: 3 },
      { kind: 'forged', accepted: -5, dismissed: '9' }
    ]),
    'Your answers to Manager suggestions so far: 2 accepted, 4 dismissed.'
  );
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

    // A new Home arrives on #projectLibraryPanel: the unfinished-setup card when
    // there is one, else the shelf heading.
    elements.projectSetupNext = { hidden: false };
    elements.projectSetupNextTitle = heading('setup');
    globalThis.location = { hash: '#projectLibraryPanel' };
    new ProjectLibraryPanel({ workspaceId: 'home' }).focusArrival();
    assert.deepEqual(calls, ['setup:tabindex=-1', 'setup:scroll', 'setup:focus:true']);
    calls.length = 0;
    elements.projectSetupNext.hidden = true; // established Home: nothing to finish
    new ProjectLibraryPanel({ workspaceId: 'home' }).focusArrival();
    assert.deepEqual(calls, ['library:tabindex=-1', 'library:scroll', 'library:focus:true']);
    calls.length = 0;

    // Only the first render counts: a later refresh never takes focus back.
    const once = new ProjectLibraryPanel({ workspaceId: 'home' });
    once.focusArrival();
    calls.length = 0;
    once.focusArrival();
    assert.deepEqual(calls, []);

    globalThis.location = { hash: '#somewhereElse' };
    new ProjectLibraryPanel({ workspaceId: 'home' }).focusArrival();
    globalThis.location = { hash: '' };
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

function continuationPanel(response, { search = '' } = {}) {
  const requests = [];
  const originalLocation = globalThis.location;
  globalThis.location = { search };
  const panel = new ProjectLibraryPanel({
    workspaceId: 'home 1',
    fetchImpl: async (url, options) => {
      requests.push({ url, options });
      if (response instanceof Error) throw response;
      return response;
    }
  });
  return { panel, requests, restore: () => (globalThis.location = originalLocation) };
}

const ok = body => ({ ok: true, json: async () => body });

test('a Home reopened without its address restores the one ready collection, sending no path', async () => {
  const { panel, requests, restore } = continuationPanel(
    ok({
      continuations: [
        { offer_id: 'offer-new', folder: 'Albums', state: 'ready' },
        { offer_id: 'offer-old', folder: 'Older', state: 'needs_pick', reason: 'expired' }
      ]
    })
  );
  try {
    await panel.restoreCollectionContinuation();
    assert.equal(panel.offerID, 'offer-new');
    assert.equal(panel.continuations.length, 2, 'the explanation for the other one is kept');
    assert.deepEqual(requests, [
      {
        url: '/api/personal-assistant/folder-digest/continuations?home_id=home%201',
        options: { headers: { Accept: 'application/json' } }
      }
    ]);
    assert.equal(requests[0].options.method, undefined, 'a read, never a POST');
    assert.equal(requests[0].options.body, undefined);
  } finally {
    restore();
  }
});

test('an address that already names the collection wins, and the read only supplies its folder name', async () => {
  const { panel, requests, restore } = continuationPanel(
    ok({
      continuations: [
        { offer_id: 'from-address', folder: 'Albums', state: 'ready' },
        { offer_id: 'another', folder: 'Sketches', state: 'ready' }
      ]
    }),
    { search: '?folder_offer_id=from-address' }
  );
  try {
    await panel.restoreCollectionContinuation();
    assert.equal(panel.offerID, 'from-address', 'the address is never overridden');
    assert.equal(requests.length, 1, 'one read-only GET, no body');
    assert.equal(requests[0].options.method, undefined);
    assert.equal(
      panel.continuations.find(item => item.offer_id === panel.offerID).folder,
      'Albums',
      'so the setup card can name the collection that was carried'
    );
  } finally {
    restore();
  }
});

test('two ready collections are ambiguous, so none is chosen for the user', async () => {
  const { panel, restore } = continuationPanel(
    ok({
      continuations: [
        { offer_id: 'a', folder: 'Albums', state: 'ready' },
        { offer_id: 'b', folder: 'Sketches', state: 'ready' }
      ]
    })
  );
  try {
    await panel.restoreCollectionContinuation();
    assert.equal(panel.offerID, '');
    assert.equal(panel.continuations.length, 2);
  } finally {
    restore();
  }
});

test('a collection that needs a new pick is remembered but never adopted as usable', async () => {
  const { panel, restore } = continuationPanel(
    ok({
      continuations: [{ offer_id: 'gone', folder: 'Albums', state: 'needs_pick', reason: 'lost' }]
    })
  );
  try {
    await panel.restoreCollectionContinuation();
    assert.equal(panel.offerID, '');
    assert.deepEqual(panel.continuations, [
      { offer_id: 'gone', folder: 'Albums', state: 'needs_pick', reason: 'lost' }
    ]);
  } finally {
    restore();
  }
});

test('an unavailable or malformed continuation answer leaves the library exactly as before', async () => {
  for (const response of [
    { ok: false, json: async () => ({ error: 'no' }) },
    ok({}),
    ok({ continuations: 'nope' }),
    ok({
      continuations: [
        null,
        { state: 'ready' },
        { offer_id: '', state: 'ready' },
        { offer_id: 'x'.repeat(161), state: 'ready' },
        { offer_id: 42, state: 'ready' }
      ]
    }),
    new Error('offline')
  ]) {
    const { panel, restore } = continuationPanel(response);
    try {
      await panel.restoreCollectionContinuation();
      assert.equal(panel.offerID, '');
      assert.deepEqual(panel.continuations, []);
    } finally {
      restore();
    }
  }
});

test('each way a folder choice can be unusable gets its own honest message', () => {
  const kept = /Your Home is ready and anything already connected is kept/;
  const expired = selectionRecovery({ continuation: { reason: 'expired' } });
  assert.equal(expired.repick, true);
  assert.match(expired.message, /30 minutes/);
  assert.match(expired.message, kept);

  const lost = selectionRecovery({ continuation: { reason: 'lost' } });
  assert.match(lost.message, /restarted/);
  assert.doesNotMatch(lost.message, /30 minutes|moved, replaced/);

  const changed = selectionRecovery({ continuation: { reason: 'changed' } });
  assert.match(changed.message, /changed after you chose it/);
  assert.doesNotMatch(changed.message, /restarted|30 minutes/);

  const unknown = selectionRecovery({});
  assert.equal(unknown.repick, true);
  assert.match(unknown.message, /no longer has the folder you chose/);
  assert.equal(new Set([expired, lost, changed, unknown].map(item => item.message)).size, 4);
  for (const item of [expired, lost, changed, unknown]) {
    assert.match(item.message, /Choose the folder again/);
    assert.doesNotMatch(item.message, /\/(Users|home|var)\//, 'no path is ever shown');
  }
});

test('problems a new pick cannot fix never offer one', () => {
  for (const [reason, pattern] of [
    ['picker_unavailable', /native folder picker is unavailable/],
    ['provider_unavailable', /package is unavailable/],
    ['home_unavailable', /Home could not be found/]
  ]) {
    const recovery = selectionRecovery({ reason, continuation: { reason: 'expired' } });
    assert.equal(recovery.repick, false, reason);
    assert.match(recovery.message, pattern);
    assert.doesNotMatch(recovery.message, /Choose the folder again to continue/);
  }
  // A library reason wins over a stale continuation: re-picking would not help.
  assert.equal(
    selectionRecovery({ reason: 'provider_unavailable', continuation: { reason: 'lost' } }).repick,
    false
  );
});

function addFolderPanel({ pickOffer, continuations = [], confirmAnswer = true }) {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home', program: { is_station: true } });
  panel.state = { revision: 1 };
  panel.offerID = 'the-offer';
  panel.run = async (_, __, fn) => fn();
  const log = { calls: [], statuses: [], confirms: [] };
  panel.status = message => log.statuses.push(message);
  panel.confirm = async (title, lines) => {
    log.confirms.push({ title, lines });
    return confirmAnswer;
  };
  panel.refresh = async () => log.calls.push('refresh');
  panel.fetchImpl = async () => ({ ok: true, json: async () => ({ continuations }) });
  panel.post = async (path, body) => {
    log.calls.push(path);
    if (path === '/roots/pick-offer') return pickOffer(body);
    if (path === '/roots/pick') return { selection_token: 'fresh' };
    if (path === '/roots/review')
      return { token: 'review', root_path: '/trusted', scope: 'metadata' };
    return { root_id: 'root' };
  };
  return { panel, log };
}

const failure = (status, reason = '') => Object.assign(new Error('nope'), { status, reason });

test('a lost offer asks the server why and only then offers one new pick', async () => {
  for (const [why, pattern] of [
    ['expired', /30 minutes/],
    ['lost', /restarted/],
    ['changed', /changed after you chose it/]
  ]) {
    const { panel, log } = addFolderPanel({
      pickOffer: () => {
        throw failure(409);
      },
      continuations: [
        { offer_id: 'the-offer', folder: 'Albums', state: 'needs_pick', reason: why }
      ],
      confirmAnswer: false
    });
    await panel.addFolder(null);
    assert.equal(log.confirms.length, 1, why);
    assert.match(log.confirms[0].lines[0], pattern, why);
    assert.deepEqual(log.calls, ['/roots/pick-offer'], `${why}: declining opens no picker`);
    assert.equal(panel.offerID, 'the-offer', 'the offer is kept for a later retry');
  }
});

test('accepting the recovery opens exactly one fresh pick and keeps the Home', async () => {
  const { panel, log } = addFolderPanel({
    pickOffer: () => {
      throw failure(409);
    },
    continuations: [{ offer_id: 'the-offer', state: 'needs_pick', reason: 'lost' }],
    confirmAnswer: true
  });
  panel.confirm = async title => {
    log.confirms.push({ title });
    return title === 'Choose the folder again?';
  };
  await panel.addFolder(null);
  assert.deepEqual(log.calls.slice(0, 3), ['/roots/pick-offer', '/roots/pick', '/roots/review']);
  assert.equal(log.calls.filter(path => path === '/roots/pick').length, 1);
});

test('package, Home, or picker problems explain themselves and never open a picker', async () => {
  for (const reason of ['provider_unavailable', 'home_unavailable', 'picker_unavailable']) {
    const { panel, log } = addFolderPanel({
      pickOffer: () => {
        throw failure(409, reason);
      }
    });
    await panel.addFolder(null);
    assert.deepEqual(log.confirms, [], `${reason}: no "choose again" prompt`);
    assert.deepEqual(log.calls, ['/roots/pick-offer'], `${reason}: no picker`);
    assert.equal(log.statuses.length, 1, reason);
  }
});

test('dismissing the native chooser is a quiet cancellation that reads and grants nothing', async () => {
  const { panel, log } = addFolderPanel({ pickOffer: () => ({}) });
  panel.offerID = '';
  panel.post = async path => {
    log.calls.push(path);
    return { cancelled: true };
  };
  await panel.addFolder(null);
  assert.deepEqual(log.calls, ['/roots/pick']);
  assert.match(log.statuses.at(-1), /No folder was chosen\. Nothing was connected or read/);
  assert.deepEqual(log.confirms, []);
});

test('a stale review re-reads the library so the next attempt is not refused again', async () => {
  // run() touches the panel element; give it a page with nothing on it.
  const original = globalThis.document;
  globalThis.document = { getElementById: () => null };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    const log = { calls: [], statuses: [] };
    panel.status = message => log.statuses.push(message);
    panel.refresh = async () => log.calls.push('refresh');
    await panel.run(null, 'Working…', async () => {
      throw failure(409, 'stale_review');
    });
    assert.deepEqual(log.calls, ['refresh']);
    assert.match(log.statuses.at(-1), /changed while you were reviewing, so nothing was granted/);

    const other = new ProjectLibraryPanel({ workspaceId: 'home' });
    const otherCalls = [];
    other.status = () => {};
    other.refresh = async () => otherCalls.push('refresh');
    await other.run(null, 'Working…', async () => {
      throw failure(500, '');
    });
    assert.deepEqual(otherCalls, [], 'only a stale review refreshes');
  } finally {
    globalThis.document = original;
  }
});

const libState = (extra = {}) => ({
  provider_read_only: false,
  initialized: true,
  picker_available: true,
  roots: [],
  ...extra
});
const scannedRoot = (id, status = 'complete') => ({
  id,
  path: `/sandbox/${id}`,
  last_scan: { status, entries_seen: 3, skipped_entries: 0 }
});

test('the next step follows the library’s own state, one action at a time', () => {
  assert.equal(setupNextStep({}).stage, 'unknown', 'no state, no guess');
  assert.equal(setupNextStep({ state: null }).stage, 'unknown');

  const readOnly = setupNextStep({ state: libState({ provider_read_only: true }) });
  assert.equal(readOnly.stage, 'read_only');
  assert.equal(readOnly.action, null, 'nothing to do until the package is back');
  assert.match(readOnly.body, /kept/);

  const fresh = setupNextStep({ state: { provider_read_only: false, initialized: false } });
  assert.equal(fresh.stage, 'not_initialized');
  assert.deepEqual(fresh.action, { id: 'initialize', label: 'Review library setup' });
  assert.match(fresh.body, /opens no folders and scans nothing/);
  assert.match(fresh.body, /saved notes and exact project links/);

  const carried = setupNextStep({
    state: { provider_read_only: false, initialized: false },
    collection: 'Albums'
  });
  assert.match(carried.title, /Albums/);
  assert.match(carried.body, /^Albums is waiting\./);
  assert.equal(carried.action.label, 'Review library setup', 'not carried: the ordinary review');

  // A carried collection is an explicit request to set it up: one button starts the
  // library and then shows the exact folder, instead of a stop of its own.
  const carriedStart = setupNextStep({
    state: { provider_read_only: false, initialized: false },
    collection: 'Albums',
    carried: true
  });
  assert.equal(carriedStart.stage, 'not_initialized');
  assert.deepEqual(carriedStart.action, {
    id: 'initialize',
    label: 'Start library and review Albums'
  });
  assert.match(carriedStart.body, /exact folder to review/);
  assert.match(carriedStart.body, /Nothing is read or scanned until you confirm/);

  const noRoot = setupNextStep({ state: libState(), collection: 'Albums', carried: true });
  assert.equal(noRoot.stage, 'no_root');
  assert.match(noRoot.title, /Connect Albums to this Home/);
  assert.equal(noRoot.action.id, 'add_folder');

  const revokedOnly = setupNextStep({
    state: libState({
      roots: [
        { id: 'a', path: '/x/a', revoked_at: '2026-09-30T00:00:00Z' },
        { id: 'b', path: '/x/b', needs_review: true }
      ]
    })
  });
  assert.equal(revokedOnly.stage, 'no_root', 'a disconnected root is not a usable one');

  const unscanned = setupNextStep({
    state: libState({ roots: [{ id: 'r1', path: '/Users/me/Music/Albums' }] })
  });
  assert.equal(unscanned.stage, 'not_scanned');
  assert.equal(unscanned.title, 'Scan Albums once');
  assert.deepEqual(unscanned.action, { id: 'scan', label: 'Review scan', rootId: 'r1' });
  assert.doesNotMatch(unscanned.title + unscanned.body, /\/Users\/me/, 'only the folder name');

  const failed = setupNextStep({ state: libState({ roots: [scannedRoot('r1', 'failed')] }) });
  assert.equal(failed.stage, 'scan_incomplete');
  assert.equal(failed.action.rootId, 'r1');
  assert.match(failed.body, /kept/);

  for (const finished of ['complete', 'partial']) {
    assert.equal(
      setupNextStep({ state: libState({ roots: [scannedRoot('r1', finished)] }) }).stage,
      'ready',
      `${finished}: an established Home shows no setup card`
    );
  }
  assert.equal(
    setupNextStep({ state: libState({ roots: [scannedRoot('r1', 'failed'), scannedRoot('r2')] }) })
      .stage,
    'ready',
    'one finished scan is enough'
  );
});

test('with no picker and no carried collection the card explains instead of offering a dead button', () => {
  const step = setupNextStep({ state: libState({ picker_available: false }) });
  assert.equal(step.stage, 'no_root');
  assert.equal(step.action, null);
  assert.match(step.body, /picker is unavailable/);
  // A carried collection needs no picker: the server holds the folder.
  assert.equal(
    setupNextStep({ state: libState({ picker_available: false }), carried: true }).action.id,
    'add_folder'
  );
});

function setupCardPage() {
  const elements = new Map();
  const make = id => {
    const classes = new Set();
    const node = {
      id,
      hidden: true,
      disabled: false,
      textContent: '',
      classList: {
        toggle: (name, on) => (on ? classes.add(name) : classes.delete(name)),
        has: name => classes.has(name)
      }
    };
    elements.set(id, node);
    return node;
  };
  for (const id of [
    'projectSetupNext',
    'projectSetupNextTitle',
    'projectSetupNextBody',
    'projectSetupNextAction',
    'assistantProgramPage'
  ]) {
    make(id);
  }
  return { elements, document: { getElementById: id => elements.get(id) || null } };
}

test('the setup card shows the next step as plain text and compacts the hero only while it is shown', () => {
  const original = globalThis.document;
  const page = setupCardPage();
  globalThis.document = page.document;
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    panel.offerID = 'offer-1';
    panel.continuations = [{ offer_id: 'offer-1', folder: '<b>Albums</b>', state: 'ready' }];
    panel.state = { provider_read_only: false, initialized: false };
    panel.renderSetupNext();
    const card = page.elements.get('projectSetupNext');
    assert.equal(card.hidden, false);
    assert.equal(page.elements.get('assistantProgramPage').classList.has('is-setup-first'), true);
    assert.match(page.elements.get('projectSetupNextTitle').textContent, /<b>Albums<\/b>/);
    assert.equal(
      'innerHTML' in page.elements.get('projectSetupNextTitle'),
      false,
      'a folder name is text, never markup'
    );
    const action = page.elements.get('projectSetupNextAction');
    assert.equal(action.hidden, false);
    assert.equal(action.textContent, 'Start library and review <b>Albums</b>');

    // The folder is scanned: an established Home shows nothing and the hero relaxes.
    panel.state = libState({ roots: [scannedRoot('r1')] });
    panel.renderSetupNext();
    assert.equal(card.hidden, true);
    assert.equal(page.elements.get('assistantProgramPage').classList.has('is-setup-first'), false);

    // A failed read hides it rather than guessing.
    panel.state = { provider_read_only: false, initialized: false };
    panel.renderSetupNext();
    assert.equal(card.hidden, false);
    panel.state = null;
    panel.renderSetupNext();
    assert.equal(card.hidden, true);
  } finally {
    globalThis.document = original;
  }
});

test('the setup card button starts exactly the step it names', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  const started = [];
  panel.initialize = async trigger => started.push(['initialize', trigger]);
  panel.addFolder = async trigger => started.push(['add_folder', trigger]);
  panel.scanRoot = async (root, trigger) => started.push(['scan', root.id, trigger]);
  panel.state = { roots: [{ id: 'r1' }, { id: 'r2' }] };

  panel.setupStep = { action: { id: 'initialize' } };
  await panel.runSetupNext('button');
  panel.setupStep = { action: { id: 'add_folder' } };
  await panel.runSetupNext('button');
  panel.setupStep = { action: { id: 'scan', rootId: 'r2' } };
  await panel.runSetupNext('button');
  assert.deepEqual(started, [
    ['initialize', 'button'],
    ['add_folder', 'button'],
    ['scan', 'r2', 'button']
  ]);

  // Nothing to do, a vanished root, or a step already running starts nothing.
  started.length = 0;
  panel.setupStep = { action: null };
  await panel.runSetupNext('button');
  panel.setupStep = { action: { id: 'scan', rootId: 'gone' } };
  await panel.runSetupNext('button');
  panel.setupStep = { action: { id: 'initialize' } };
  panel.busy = true;
  await panel.runSetupNext('button');
  assert.deepEqual(started, []);
});

test('an uninitialized Home still renders its setup card and lands the arrival', async () => {
  const original = globalThis.document;
  const originalLocation = globalThis.location;
  const page = setupCardPage();
  const focus = [];
  for (const id of ['projectLibrarySetup', 'projectLibraryContent', 'projectLibraryAdd']) {
    page.elements.set(id, { id, hidden: false, disabled: false });
  }
  page.elements.set('projectLibraryInitialize', {
    id: 'projectLibraryInitialize',
    disabled: false
  });
  Object.assign(page.elements.get('projectSetupNextTitle'), {
    setAttribute: () => {},
    scrollIntoView: () => focus.push('scroll'),
    focus: () => focus.push('focus')
  });
  globalThis.document = page.document;
  globalThis.location = { hash: '#projectLibraryPanel', search: '' };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    panel.status = () => {};
    panel.request = async () => ({ initialized: false, provider_read_only: false });
    await panel.refresh();
    assert.equal(page.elements.get('projectSetupNext').hidden, false);
    assert.equal(page.elements.get('projectSetupNextAction').textContent, 'Review library setup');
    assert.deepEqual(focus, ['scroll', 'focus'], 'arrival is not skipped before initialization');
    await panel.refresh(); // a later refresh never moves focus again
    assert.deepEqual(focus, ['scroll', 'focus']);
  } finally {
    globalThis.document = original;
    globalThis.location = originalLocation;
  }
});

function refreshFailurePage() {
  const page = setupCardPage();
  for (const id of ['projectLibrarySetup', 'projectLibraryContent', 'projectLibraryAdd']) {
    page.elements.set(id, { id, hidden: false, disabled: false });
  }
  return page;
}

test('a failed re-read keeps a populated library on screen and says the check is unavailable', async () => {
  const original = globalThis.document;
  const page = refreshFailurePage();
  globalThis.document = page.document;
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    const messages = [];
    panel.status = message => messages.push(message);
    const saved = libState({ roots: [scannedRoot('r1')], revision: 4 });
    panel.state = saved;
    panel.request = async () => {
      throw new Error('offline');
    };
    await panel.refresh();
    assert.equal(panel.state, saved, 'the last saved state is kept, not cleared');
    assert.equal(
      page.elements.get('projectLibraryContent').hidden,
      false,
      'saved projects stay visible'
    );
    assert.equal(page.elements.get('projectLibraryAdd').hidden, false);
    assert.match(messages.at(-1), /could not re-check this library just now/);
    assert.match(messages.at(-1), /last saved/);
    assert.match(messages.at(-1), /Nothing was changed/);
    assert.doesNotMatch(messages.at(-1), /offline/, 'no raw error text');
  } finally {
    globalThis.document = original;
  }
});

test('a first load that fails still hides the library rather than inventing one', async () => {
  const original = globalThis.document;
  const page = refreshFailurePage();
  globalThis.document = page.document;
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    const messages = [];
    panel.status = message => messages.push(message);
    panel.request = async () => {
      throw new Error('The Home is unavailable');
    };
    await panel.refresh();
    assert.equal(panel.state, null);
    assert.equal(page.elements.get('projectLibraryContent').hidden, true);
    assert.equal(page.elements.get('projectLibraryAdd').hidden, true);
    assert.equal(messages.at(-1), 'The Home is unavailable');
  } finally {
    globalThis.document = original;
  }
});

function scanFlowPanel({ commits }) {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { revision: 5, roots: [{ id: 'root-1', path: '/x/Albums' }] };
  const log = { posts: [], statuses: [], keys: [] };
  panel.status = message => log.statuses.push(message);
  panel.confirm = async () => true;
  panel.refresh = async () => log.posts.push('refresh');
  panel.post = async (path, body) => {
    log.posts.push(path);
    if (path.endsWith('/scans/review')) {
      return { token: 'review-1', root_path: '/x/Albums', scope: 'names only', max_entries: 5000 };
    }
    log.keys.push(body.idempotency_key);
    const next = commits.shift();
    if (next instanceof Error) throw next;
    return next;
  };
  return { panel, log };
}

const receipt = { status: 'complete', entries_seen: 4, skipped_links: 0, skipped_other: 0 };
const noAnswer = () => Object.assign(new TypeError('Failed to fetch'), {});
const serverError = () => Object.assign(new Error('boom'), { status: 502 });

test('a lost scan reply is replayed once with the same key, so the folder is never scanned twice', async () => {
  const { panel, log } = scanFlowPanel({ commits: [noAnswer(), receipt] });
  await panel.scanRootFlow('root-1', null);
  assert.equal(log.keys.length, 2);
  assert.equal(log.keys[0], log.keys[1], 'the same idempotency key is the replay');
  assert.equal(log.posts.filter(path => path.endsWith('/scans/review')).length, 1, 'no new review');
  assert.match(log.statuses.at(-1), /complete scan · 4 entries seen/);

  const again = scanFlowPanel({ commits: [serverError(), receipt] });
  await again.panel.scanRootFlow('root-1', null);
  assert.equal(again.log.keys[0], again.log.keys[1]);
});

test('a scan that was recorded despite two lost replies is reported, not repeated', async () => {
  const { panel, log } = scanFlowPanel({ commits: [noAnswer(), noAnswer()] });
  panel.refresh = async () => {
    log.posts.push('refresh');
    panel.state.roots = [
      {
        id: 'root-1',
        path: '/x/Albums',
        last_scan: { id: 'scan-new', status: 'complete', entries_seen: 4 }
      }
    ];
  };
  await panel.scanRootFlow('root-1', null);
  assert.equal(log.keys.length, 2, 'exactly one replay, never a third attempt');
  assert.match(log.statuses.at(-1), /reply was lost, but this scan was recorded/);
  assert.match(log.statuses.at(-1), /complete · 4 names checked\. It was not repeated/);
});

test('an uncertain scan with nothing recorded is an error, and a refusal is never retried', async () => {
  const unrecorded = scanFlowPanel({ commits: [noAnswer(), noAnswer()] });
  await assert.rejects(unrecorded.panel.scanRootFlow('root-1', null), /Failed to fetch/);
  assert.equal(unrecorded.log.keys.length, 2);

  // The same scan id as before the review means nothing new was recorded.
  const unchanged = scanFlowPanel({ commits: [serverError(), serverError()] });
  unchanged.panel.state.roots[0].last_scan = {
    id: 'scan-old',
    status: 'complete',
    entries_seen: 1
  };
  unchanged.panel.refresh = async () => {};
  await assert.rejects(unchanged.panel.scanRootFlow('root-1', null), /boom/);

  const refused = scanFlowPanel({
    commits: [Object.assign(new Error('changed'), { status: 409 })]
  });
  await assert.rejects(refused.panel.scanRootFlow('root-1', null), /changed/);
  assert.equal(refused.log.keys.length, 1, 'a definite refusal is final');
});

function integrationPage({ pathname = '/workspaces/music-home/assistant' } = {}) {
  const map = new Map();
  const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  const originalLocation = globalThis.location;
  const navigations = [];
  Object.defineProperty(globalThis, 'sessionStorage', {
    configurable: true,
    value: {
      getItem: key => (map.has(key) ? map.get(key) : null),
      setItem: (key, value) => map.set(key, String(value)),
      removeItem: key => map.delete(key)
    }
  });
  globalThis.location = { pathname, search: '', hash: '', assign: url => navigations.push(url) };
  return {
    map,
    navigations,
    restore() {
      if (originalStorage) Object.defineProperty(globalThis, 'sessionStorage', originalStorage);
      else delete globalThis.sessionStorage;
      globalThis.location = originalLocation;
    }
  };
}

const songDetail = { row: { id: 'entry-9', name: 'Album 5' } };
const reaperOffer = {
  key: 'ori_reaper',
  quest_id: 'install_ori_reaper',
  display_name: 'REAPER'
};

test('reviewing an integration remembers only where to return and opens the reviewed install', () => {
  const page = integrationPage();
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home-1' });
    panel.status = () => {};
    panel.startIntegrationReview(songDetail, reaperOffer);
    assert.deepEqual(page.navigations, ['/?setup=quest&source=host&quest=install_ori_reaper']);
    const stored = JSON.parse(page.map.get('ori:library-return'));
    assert.equal(stored.home_id, 'home-1');
    assert.equal(stored.entry_id, 'entry-9');
    assert.equal(stored.quest_id, 'install_ori_reaper');
    assert.equal(stored.return_path, '/workspaces/music-home/assistant');
    assert.deepEqual(Object.keys(stored).sort(), [
      'created_at',
      'entry_id',
      'home_id',
      'quest_id',
      'return_path'
    ]);
  } finally {
    page.restore();
  }
});

test('a bad quest or a page that is not a Home starts nothing and says so', () => {
  for (const [offer, pathname] of [
    [{ ...reaperOffer, quest_id: '../install' }, '/workspaces/music-home/assistant'],
    [{ ...reaperOffer, quest_id: 'Install_Ori' }, '/workspaces/music-home/assistant'],
    [reaperOffer, '/settings'],
    [reaperOffer, '/workspaces/music-home/assistant/extra']
  ]) {
    const page = integrationPage({ pathname });
    try {
      const panel = new ProjectLibraryPanel({ workspaceId: 'home-1' });
      const messages = [];
      panel.status = message => messages.push(message);
      panel.startIntegrationReview(songDetail, offer);
      assert.deepEqual(page.navigations, [], `${offer.quest_id} at ${pathname}`);
      assert.equal(page.map.size, 0, 'nothing is remembered for a refused start');
      assert.match(messages.at(-1), /nothing was changed/i);
    } finally {
      page.restore();
    }
  }
});

function returningPanel({ request } = {}) {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home-1' });
  const log = { requests: [], details: [], statuses: [] };
  panel.status = message => log.statuses.push(message);
  panel.request = async path => {
    log.requests.push(path);
    if (request) return request(path);
    return {};
  };
  panel.details = async (entryID, trigger) => log.details.push([entryID, trigger]);
  return { panel, log };
}

function rememberReturn(page, patch = {}) {
  page.map.set(
    'ori:library-return',
    JSON.stringify({
      home_id: 'home-1',
      entry_id: 'entry-9',
      quest_id: 'install_ori_reaper',
      return_path: '/workspaces/music-home/assistant',
      created_at: Date.now(),
      ...patch
    })
  );
}

function queuePanel({ eligibility, queueChoice }) {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home-1' });
  panel.state = { provider_read_only: false };
  panel.queue = {
    home_id: 'home-1',
    ids: ['song-a', 'song-b'],
    index: 0,
    created_at: Date.now(),
    pending: null
  };
  const log = { posts: [], statuses: [], choices: [], progressed: 0 };
  panel.run = async (_trigger, _message, work) => work();
  panel.saveQueue = () => true;
  panel.restoreQueue = async () => {};
  panel.renderQueueControls = () => {};
  panel.status = message => log.statuses.push(message);
  panel.post = async path => {
    log.posts.push(path);
    throw new Error('no review, creator, or queue write is allowed here');
  };
  panel.progressQueue = async () => {
    log.progressed++;
  };
  panel.request = async path =>
    path.endsWith('/activation')
      ? eligibility
      : { row: { id: 'song-a', name: 'Album 5', connection: 'catalog_only' } };
  panel.queueChoice = async (name, position, count, trigger, reason, offer) => {
    log.choices.push({ name, position, count, reason, offer });
    return queueChoice;
  };
  return { panel, log };
}

test('a queued song that needs an integration pauses in place while its review opens', async () => {
  const page = integrationPage();
  try {
    const { panel, log } = queuePanel({
      eligibility: {
        state: 'project_provider_unavailable',
        reason: 'A compatible installed project integration is required.',
        integration_offer: reaperOffer
      },
      queueChoice: 'integration'
    });
    await panel.continueQueue();
    assert.equal(log.choices.length, 1);
    assert.deepEqual(log.choices[0].offer, reaperOffer, 'the dialog is told what it may offer');
    assert.equal(panel.queue.index, 0, 'the queue stays on the same song');
    assert.equal(log.progressed, 0, 'nothing was handled, skipped, or connected');
    assert.deepEqual(log.posts, [], 'no review, creator, or queue request was made');
    assert.equal(panel.queue.pending, null, 'no review token is held across the detour');
    assert.deepEqual(page.navigations, ['/?setup=quest&source=host&quest=install_ori_reaper']);
    assert.equal(JSON.parse(page.map.get('ori:library-return')).entry_id, 'song-a');
    assert.match(log.statuses.at(-1), /paused on this song/);
  } finally {
    page.restore();
  }
});

// A Home with several catalog-only songs, for select-all and connect-all. Each
// song's detail, eligibility and review are served from `songs`.
function batchPanel(songs, { confirmed = true, reviewFile = null } = {}) {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home-1' });
  panel.state = { provider_read_only: false };
  panel.rows = songs.map(song => ({ id: song.id, name: song.name, connection: 'catalog_only' }));
  const log = { posts: [], statuses: [], confirms: [], refreshes: 0 };
  panel.run = async (_trigger, _message, work) => work();
  panel.renderRows = () => {};
  panel.renderQueueControls = () => {};
  panel.status = message => log.statuses.push(message);
  panel.refresh = async () => {
    log.refreshes++;
  };
  panel.confirm = async (title, lines, action) => {
    log.confirms.push({ title, lines, action });
    return confirmed;
  };
  const byID = id => songs.find(song => song.id === id);
  panel.request = async path => {
    const id = decodeURIComponent(path.split('/')[2]);
    const song = byID(id);
    if (path.endsWith('/activation')) return song.eligibility;
    return { row: { id, name: song.name, connection: 'catalog_only' }, revision: 7 };
  };
  panel.post = async (path, body) => {
    log.posts.push({ path, body });
    if (path.endsWith('/activation/review')) {
      const id = decodeURIComponent(path.split('/')[2]);
      return { token: `token-${id}`, project_file: reviewFile || body.project_file };
    }
    return {};
  };
  return { panel, log };
}

const readySong = (id, name, file = 'Song.rpp') => ({
  id,
  name,
  eligibility: {
    state: 'review_available',
    project_files: [file],
    project_role_labels: ['REAPER Assistant']
  }
});

test('select all picks every selectable song shown and clears them on the next press', () => {
  const { panel } = batchPanel([readySong('a', 'Album-1'), readySong('b', 'Album-2')]);
  panel.rows.push({ id: 'c', name: 'Album-3', connection: 'connected' });
  panel.toggleSelectAll();
  assert.deepEqual(
    [...panel.selectedProjects].sort(),
    ['a', 'b'],
    'connected songs are not picked'
  );
  panel.toggleSelectAll();
  assert.equal(panel.selectedProjects.size, 0);
});

test('connect all confirms once with every exact file, then reviews and commits each song on its own', async () => {
  const songs = [readySong('a', 'Album-1'), readySong('b', 'Album-2', 'Take.rpp')];
  const { panel, log } = batchPanel(songs);
  panel.selectedProjects = new Set(['a', 'b']);
  await panel.connectSelected();
  assert.equal(log.confirms.length, 1, 'one review for the whole selection');
  assert.match(log.confirms[0].lines.join('\n'), /Album-1 — Song\.rpp/);
  assert.match(log.confirms[0].lines.join('\n'), /Album-2 — Take\.rpp/);
  assert.deepEqual(
    log.posts.map(post => post.path.split('/').slice(-2).join('/')),
    ['activation/review', 'activation/commit', 'activation/review', 'activation/commit']
  );
  const commits = log.posts.filter(post => post.path.endsWith('/commit'));
  assert.notEqual(commits[0].body.idempotency_key, commits[1].body.idempotency_key);
  assert.deepEqual(
    commits.map(post => post.body.review_token),
    ['token-a', 'token-b'],
    'each commit uses its own song’s review'
  );
  assert.equal(panel.selectedProjects.size, 0);
  assert.match(log.statuses.at(-1), /Connected 2 of 2/);
});

test('connect all leaves a song that needs a choice or an integration alone and names it', async () => {
  const songs = [
    readySong('a', 'Album-1'),
    readySong('b', 'Album-2'),
    {
      id: 'c',
      name: 'Album-5',
      eligibility: { state: 'file_choice_required', project_files: ['A.rpp', 'B.rpp'] }
    },
    {
      id: 'd',
      name: 'Logic Sketch',
      eligibility: { state: 'unsupported_format', reason: 'Catalog record only.' }
    }
  ];
  const { panel, log } = batchPanel(songs);
  panel.selectedProjects = new Set(['a', 'b', 'c', 'd']);
  await panel.connectSelected();
  const text = log.confirms[0].lines.join('\n');
  assert.match(text, /Album-5: choose its project file/);
  assert.match(text, /Logic Sketch: Catalog record only\./);
  assert.equal(log.posts.filter(post => post.path.endsWith('/commit')).length, 2);
  assert.ok(!log.posts.some(post => post.path.includes('/c/') || post.path.includes('/d/')));
  assert.match(log.statuses.at(-1), /2 need their own review/);
});

test('connect all with the integration missing says so in a dialog and offers its review', async () => {
  const missing = (id, name) => ({
    id,
    name,
    eligibility: {
      state: 'project_provider_unavailable',
      reason: 'A compatible installed project integration is required.',
      integration_offer: reaperOffer
    }
  });
  const { panel, log } = batchPanel([missing('a', 'Album-1'), missing('b', 'Album-2')]);
  const started = [];
  panel.startIntegrationReview = (detail, offer) => started.push({ detail, offer });
  panel.selectedProjects = new Set(['a', 'b']);
  await panel.connectSelected();
  assert.equal(log.confirms.length, 1, 'a dialog, not only a line of small text');
  assert.match(log.confirms[0].title, /Install the REAPER integration first\?/);
  assert.match(log.confirms[0].lines.join('\n'), /Album-1: needs the REAPER integration/);
  assert.equal(log.confirms[0].action, 'Review the REAPER integration');
  assert.deepEqual(log.posts, [], 'nothing was reviewed or created');
  assert.equal(started.length, 1);
  assert.deepEqual(started[0].offer, reaperOffer);
});

test('declining the one review connects nothing and reviews nothing', async () => {
  const { panel, log } = batchPanel([readySong('a', 'Album-1'), readySong('b', 'Album-2')], {
    confirmed: false
  });
  panel.selectedProjects = new Set(['a', 'b']);
  await panel.connectSelected();
  assert.deepEqual(log.posts, [], 'no server review was taken before the person agreed');
  assert.equal(log.statuses.at(-1), 'Nothing was connected.');
});

test('connect all stops when a song’s review names a different file than the one confirmed', async () => {
  const { panel, log } = batchPanel([readySong('a', 'Album-1'), readySong('b', 'Album-2')], {
    reviewFile: 'Other.rpp'
  });
  panel.selectedProjects = new Set(['a', 'b']);
  await panel.connectSelected();
  assert.equal(log.posts.filter(post => post.path.endsWith('/commit')).length, 0);
  assert.match(log.statuses.at(-1), /Connected 0 of 2\. Stopped at Album-1/);
  assert.ok(panel.selectedProjects.has('a') && panel.selectedProjects.has('b'));
});

test('the queue offers an integration only for a song that cannot be set up yet', async () => {
  const page = integrationPage();
  try {
    const eligible = queuePanel({
      eligibility: {
        state: 'review_available',
        reason: '',
        integration_offer: reaperOffer // a stray offer is ignored for a ready song
      },
      queueChoice: 'pause'
    });
    await eligible.panel.continueQueue();
    assert.equal(eligible.log.choices[0].offer, null);
    assert.equal(eligible.log.choices[0].reason, '');

    const noOffer = queuePanel({
      eligibility: { state: 'unsupported_format', reason: 'Catalog record only.' },
      queueChoice: 'pause'
    });
    await noOffer.panel.continueQueue();
    assert.equal(noOffer.log.choices[0].offer, null, 'Logic or Ableton get no install promise');
    assert.deepEqual(page.navigations, [], 'neither choice started an integration review');
  } finally {
    page.restore();
  }
});

const rosterOf = rows => ({ roles: { roles: rows, filled_count: 0, total_count: rows.length } });

test('the project team link asks for the first empty role, never a filled, read-only, or odd one', () => {
  const route = '/workspaces/existing-song';
  const song = { role_id: 'reaper-assistant', state: 'empty', required: true, primary: true };
  assert.equal(projectTeamURL(route, rosterOf([song])), `${route}?role=reaper-assistant`);
  // Primary wins over required, and required over the rest.
  const other = { role_id: 'mixer', state: 'empty', required: true };
  const optional = { role_id: 'extras', state: 'empty' };
  assert.match(projectTeamURL(route, rosterOf([optional, other, song])), /role=reaper-assistant$/);
  assert.match(projectTeamURL(route, rosterOf([optional, other])), /role=mixer$/);
  assert.match(projectTeamURL(route, rosterOf([optional])), /role=extras$/);
  // Nothing to fill, or nothing safe to ask for: just the project page.
  for (const rows of [
    [{ ...song, state: 'filled' }],
    [{ ...song, read_only: true }],
    [{ ...song, needs_clear: true, state: 'needs_clear' }],
    [{ ...song, role_id: '../x' }],
    [{ ...song, role_id: 'a b' }],
    [{ ...song, role_id: 'x'.repeat(129) }],
    [{ ...song, role_id: 7 }],
    [null, undefined, 'x'],
    []
  ]) {
    assert.equal(projectTeamURL(route, rosterOf(rows)), route, JSON.stringify(rows));
  }
  for (const response of [null, undefined, {}, { roles: null }, { roles: { roles: 'no' } }]) {
    assert.equal(projectTeamURL(route, response), route);
  }
});

test('the team link only ever extends a workspace page route, never another address', () => {
  const roster = rosterOf([
    { role_id: 'reaper-assistant', state: 'empty', required: true, primary: true }
  ]);
  for (const route of [
    '',
    undefined,
    null,
    '/workspaces/',
    '/workspaces/a/b',
    '/workspaces/a?x=1',
    '/workspaces/a#x',
    '//evil.example/workspaces/a',
    'https://evil.example/workspaces/a',
    '/settings',
    'javascript:alert(1)'
  ]) {
    assert.equal(projectTeamURL(route, roster), '', String(route));
  }
});

function routePanel(handlers) {
  const calls = [];
  const panel = new ProjectLibraryPanel({
    workspaceId: 'home',
    fetchImpl: async (url, options) => {
      calls.push({ url, options });
      const handler = handlers[url];
      if (!handler) throw new Error(`unexpected ${url}`);
      return handler();
    }
  });
  return { panel, calls };
}

test('a workspace page is reached by its slug, because an ID path is a 404', async () => {
  const { panel, calls } = routePanel({
    '/api/workspaces/9a28bf04-7260-4909-8522-2d62f46217b2': () => ({
      ok: true,
      json: async () => ({ id: '9a28bf04-7260-4909-8522-2d62f46217b2', folder_slug: 'album three' })
    })
  });
  const route = await panel.workspaceRoute('9a28bf04-7260-4909-8522-2d62f46217b2');
  assert.equal(route, '/workspaces/album%20three');
  assert.doesNotMatch(route, /9a28bf04/, 'the workspace ID is never used as the page address');
  assert.equal(calls[0].options.method, undefined, 'a read');
});

test('a project page reports whether it has chosen how it works, from the one canonical read', async () => {
  const read = body => ({ ok: true, json: async () => body });
  for (const [workspace, expected] of [
    [{ folder_slug: 'song', runtime_state: { selected_mode_id: 'file_only' } }, true],
    [{ folder_slug: 'song', runtime_state: { selected_mode_id: '  ' } }, false],
    [{ folder_slug: 'song', runtime_state: {} }, false],
    [{ folder_slug: 'song' }, false]
  ]) {
    const { panel, calls } = routePanel({ '/api/workspaces/w-1': () => read(workspace) });
    assert.deepEqual(await panel.workspacePage('w-1'), {
      route: '/workspaces/song',
      modeChosen: expected
    });
    assert.equal(calls.length, 1, 'route and mode come from a single read');
  }
  // Nothing readable means no route and no claim about the mode.
  const { panel } = routePanel({
    '/api/workspaces/w-1': () => ({ ok: false, json: async () => ({}) })
  });
  assert.deepEqual(await panel.workspacePage('w-1'), { route: '', modeChosen: false });
});

test('with no resolvable page there is no route, so no dead link is ever shown', async () => {
  for (const handler of [
    () => ({ ok: true, json: async () => ({ id: 'x' }) }),
    () => ({ ok: true, json: async () => ({ folder_slug: '   ' }) }),
    () => ({ ok: false, json: async () => ({ folder_slug: 'nope' }) }),
    () => ({ ok: true, json: async () => null }),
    () => {
      throw new Error('offline');
    }
  ]) {
    const { panel } = routePanel({ '/api/workspaces/w-1': handler });
    assert.equal(await panel.workspaceRoute('w-1'), '');
  }
  assert.equal(await new ProjectLibraryPanel({ workspaceId: 'h' }).workspaceRoute(''), '');
});

test('the connected dialog reads the project’s own roster and never fails the connect over it', async () => {
  const roster = () => ({
    ok: true,
    json: async () =>
      rosterOf([{ role_id: 'reaper-assistant', state: 'empty', required: true, primary: true }])
  });
  const { panel, calls } = routePanel({ '/api/workspaces/child-9/roles': roster });
  assert.equal(
    await panel.projectTeamLink('child-9', '/workspaces/existing-song'),
    '/workspaces/existing-song?role=reaper-assistant'
  );
  assert.deepEqual(calls, [
    { url: '/api/workspaces/child-9/roles', options: { headers: { Accept: 'application/json' } } }
  ]);
  assert.equal(calls[0].options.method, undefined, 'a read, never a write');

  const refused = routePanel({
    '/api/workspaces/child-9/roles': () => ({ ok: false, json: async () => ({}) })
  });
  assert.equal(
    await refused.panel.projectTeamLink('child-9', '/workspaces/existing-song'),
    '/workspaces/existing-song'
  );
  const offline = routePanel({
    '/api/workspaces/child-9/roles': () => {
      throw new Error('offline');
    }
  });
  assert.equal(
    await offline.panel.projectTeamLink('child-9', '/workspaces/existing-song'),
    '/workspaces/existing-song'
  );
});

test('every eligibility state has its own plain label, and none promises an install or a live check', () => {
  const states = {
    review_available: 'Ready to review',
    file_choice_required: 'Needs a file choice',
    connected: 'Connected to a project workspace',
    link_needs_review: 'Saved link needs review',
    revoked_source: 'Discovery consent ended',
    unavailable: 'Not found at the last scan',
    unsupported_format: 'Catalog record only',
    project_provider_unavailable: 'Needs a project integration',
    home_provider_unavailable: 'Home package unavailable',
    provider_ambiguous: 'Integration needs review',
    folder_owned: 'Folder already used by another workspace'
  };
  for (const [state, expected] of Object.entries(states)) {
    assert.equal(activationStateLabel(state), expected, state);
  }
  assert.equal(new Set(Object.values(states)).size, Object.keys(states).length, 'each is distinct');
  for (const state of ['', 'made_up', undefined, null, '__proto__', 'toString']) {
    assert.equal(activationStateLabel(state), 'Setup status unknown', String(state));
  }
  for (const text of Object.values(states)) {
    assert.doesNotMatch(text, /install (it|now)|REAPER|live|ready to (record|play)/i);
  }
});

test('coming back reopens the same song once, after re-reading it from the server', async () => {
  const page = integrationPage();
  try {
    rememberReturn(page);
    const { panel, log } = returningPanel();
    await panel.resumeFromIntegration();
    assert.deepEqual(log.requests, ['/projects/entry-9'], 'the song is re-read, not trusted');
    assert.deepEqual(log.details, [['entry-9', null]]);
    assert.match(log.statuses.at(-1), /Its integration status was checked again/);
    assert.equal(page.map.size, 0, 'the hint is spent');
    await panel.resumeFromIntegration();
    assert.equal(log.details.length, 1, 'a reload or second call never repeats the return');
  } finally {
    page.restore();
  }
});

test('a hint for another Home, or a tampered or expired one, returns nowhere', async () => {
  const page = integrationPage();
  try {
    rememberReturn(page, { home_id: 'some-other-home' });
    const other = returningPanel();
    await other.panel.resumeFromIntegration();
    assert.deepEqual(other.log.details, []);
    assert.deepEqual(other.log.requests, []);
    assert.equal(page.map.size, 1, 'another Home’s hint is left for that Home');

    for (const patch of [
      { return_path: 'https://evil.example/' },
      { quest_id: '../x' },
      { entry_id: 'e'.repeat(161) },
      { created_at: Date.now() - 2 * 3600 * 1000 }
    ]) {
      rememberReturn(page, patch);
      const { panel, log } = returningPanel();
      await panel.resumeFromIntegration();
      assert.deepEqual(log.details, [], JSON.stringify(patch));
      assert.deepEqual(log.requests, [], 'nothing is asked of the server for a bad hint');
    }
  } finally {
    page.restore();
  }
});

test('a song that is gone, or a re-check that fails, says so and creates nothing', async () => {
  const page = integrationPage();
  try {
    rememberReturn(page);
    const gone = returningPanel({
      request: async () => {
        throw Object.assign(new Error('missing'), { status: 404 });
      }
    });
    await gone.panel.resumeFromIntegration();
    assert.deepEqual(gone.log.details, []);
    assert.match(gone.log.statuses.at(-1), /no longer in this library\. Nothing was changed/);
    assert.equal(page.map.size, 0);

    rememberReturn(page);
    const down = returningPanel({
      request: async () => {
        throw Object.assign(new Error('boom'), { status: 503 });
      }
    });
    await down.panel.resumeFromIntegration();
    assert.deepEqual(down.log.details, []);
    assert.match(down.log.statuses.at(-1), /could not re-check that song/);
    // Nothing on the return path issues a review, activation, or creator call.
    for (const path of [...gone.log.requests, ...down.log.requests]) {
      assert.match(path, /^\/projects\/[^/]+$/);
    }
  } finally {
    page.restore();
  }
});

test('a row offers Open only when the server says it can be opened', () => {
  const song = { id: 's1', name: 'Song 002', connection: 'catalog_only', can_open: true };
  assert.equal(libraryOpenAction(song, false).label, 'Open');
  assert.match(libraryOpenAction(song, false).ariaLabel, /Song 002: makes its workspace/);
  assert.equal(
    libraryOpenAction({ ...song, connection: 'connected' }, false).ariaLabel,
    'Open Song 002'
  );
  // Read-only Home, unsupported format or a lost source: Details explain it.
  assert.equal(libraryOpenAction(song, true), null);
  assert.equal(libraryOpenAction({ ...song, can_open: false }, false), null);
  assert.equal(libraryOpenAction(null, false), null);
});

test('the open action goes only to one workspace', () => {
  assert.equal(libraryOpenRoute({ route: '/workspaces/song-002' }), '/workspaces/song-002');
  for (const route of [
    '',
    '/agents',
    'https://example.com/workspaces/x',
    '/workspaces/a/b',
    '//x'
  ]) {
    assert.equal(libraryOpenRoute({ route }), '', route);
  }
});

test('the status line says how the assistant came to the song', () => {
  const name = 'Song 002';
  const agent = { agent_name: 'REAPER Assistant' };
  assert.match(
    libraryOpenedMessage(name, { ...agent, staffing: 'added' }),
    /was added and joins each song/
  );
  assert.match(
    libraryOpenedMessage(name, { ...agent, staffing: 'joined' }),
    /REAPER Assistant joined it/
  );
  assert.match(libraryOpenedMessage(name, { staffing: 'off' }), /switched off, so no agent/);
  assert.match(libraryOpenedMessage(name, { staffing: 'consent_stale' }), /review it on this Home/);
  assert.match(libraryOpenedMessage(name, { staffing: 'assistant_missing' }), /gone, so no agent/);
  assert.equal(
    libraryOpenedMessage(name, { staffing: 'none', created: true }),
    'Song 002 is ready.'
  );
  for (const staffing of ['added', 'joined', 'off', 'consent_stale', 'assistant_missing']) {
    assert.doesNotMatch(libraryOpenedMessage(name, { staffing }), /_/, staffing);
  }
});

test('several project files come back as chips of bare file names', () => {
  const error = Object.assign(new Error('Choose'), {
    reason: 'needs_choice',
    payload: { project_files: ['Song 001.rpp', 'Song 001 alt.rpp', '../x.rpp', 'a/b.rpp', ''] }
  });
  assert.deepEqual(libraryOpenChoices(error), ['Song 001.rpp', 'Song 001 alt.rpp']);
  assert.deepEqual(libraryOpenChoices({ reason: 'stale_review', payload: error.payload }), []);
  assert.deepEqual(libraryOpenChoices(null), []);
});

test('Open posts one request ID per song, keeps it for a retry, and goes to the song', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { provider_read_only: false };
  const statuses = [];
  panel.status = message => statuses.push(message);
  const posts = [];
  let answer = 'needs_choice';
  panel.post = async (path, body) => {
    posts.push({ path, body });
    if (answer === 'needs_choice') {
      throw Object.assign(new Error('Choose which project file to open'), {
        reason: 'needs_choice',
        payload: { project_files: ['A.rpp', 'B.rpp'] }
      });
    }
    if (answer === 'conflict') throw Object.assign(new Error('Busy'), { reason: 'stale_review' });
    return { route: '/workspaces/song-001', staffing: 'joined', agent_name: 'REAPER Assistant' };
  };
  const assigned = [];
  const previous = globalThis.location;
  globalThis.location = { search: '', assign: url => assigned.push(url) };
  try {
    const row = { id: 'song 1', name: 'Song 001' };
    const button = { disabled: false, textContent: 'Open', isConnected: true };
    await panel.openRow(row, button);
    assert.equal(posts[0].path, '/projects/song%201/open');
    assert.match(posts[0].body.request_id, /^open-/);
    assert.equal(posts[0].body.selected_file, undefined);
    assert.equal(assigned.length, 0, 'a question never navigates');
    assert.equal(button.disabled, false);
    assert.equal(button.textContent, 'Open');
    answer = 'conflict';
    await panel.openRow(row, button);
    answer = 'ok';
    await panel.openRow(row, button, undefined, 'B.rpp');
    // The same song keeps its request ID until it opens, so a retry replays.
    assert.equal(new Set(posts.map(post => post.body.request_id)).size, 1);
    assert.equal(posts.at(-1).body.selected_file, 'B.rpp');
    assert.deepEqual(assigned, ['/workspaces/song-001']);
    assert.match(statuses.at(-1), /REAPER Assistant joined it/);
    // A read-only Home or a busy panel sends nothing.
    panel.state = { provider_read_only: true };
    await panel.openRow(row, button);
    panel.state = { provider_read_only: false };
    panel.busy = true;
    await panel.openRow(row, button);
    assert.equal(posts.length, 3);
  } finally {
    globalThis.location = previous;
  }
});

test('the shared-assistant switch says where the Home stands and offers the one fix', () => {
  const base = { role_label: 'REAPER Assistant', team_digest: 'd1' };
  assert.equal(librarySharingView({ ...base, state: 'unavailable' }).visible, false);
  assert.equal(librarySharingView({ ...base, state: 'on' }, true).visible, false);
  assert.equal(librarySharingView(null).visible, false);
  const on = librarySharingView({ ...base, state: 'on', agent_name: 'REAPER Assistant' });
  assert.equal(on.checked, true);
  assert.equal(on.switchLabel, 'Add my REAPER Assistant to songs I open');
  assert.match(on.note, /joins each song you open\. File-only/);
  assert.equal(on.action, null);
  assert.match(
    librarySharingView({ ...base, state: 'on' }).note,
    /added to the first song you open, then joins each one/
  );
  const off = librarySharingView({ ...base, state: 'off', agent_name: 'REAPER Assistant' });
  assert.equal(off.checked, false);
  assert.match(off.note, /get no agent/);
  // A Home built before sharing existed opts in with the same switch.
  const none = librarySharingView({ ...base, state: 'none' });
  assert.equal(none.checked, false);
  assert.match(none.note, /Turn this on/);
  const stale = librarySharingView({ ...base, state: 'stale', agent_name: 'REAPER Assistant' });
  assert.equal(stale.switchDisabled, true);
  assert.deepEqual(stale.action, { id: 'review', label: 'Review the updated assistant' });
  const missing = librarySharingView({
    ...base,
    state: 'on',
    agent_name: 'REAPER Assistant',
    assistant_missing: true
  });
  assert.deepEqual(missing.action, { id: 'readd', label: 'Add the assistant again' });
  assert.match(missing.note, /is gone/);
});

test('the switch sends the team it showed, and a stale review asks before renewing', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.run = async (_trigger, _message, work) => work();
  panel.renderSharing = () => {};
  const statuses = [];
  panel.status = message => statuses.push(message);
  const posts = [];
  let reply = { sharing: { state: 'off', role_label: 'REAPER Assistant', team_digest: 'd1' } };
  panel.post = async (path, body) => {
    posts.push({ path, body });
    return reply;
  };
  panel.state = {
    provider_read_only: false,
    sharing: { state: 'on', role_label: 'REAPER Assistant', team_digest: 'd1' }
  };
  await panel.setSharing({ checked: false });
  assert.equal(posts[0].path, '/sharing');
  assert.equal(posts[0].body.enabled, false);
  assert.equal(posts[0].body.team_digest, undefined);
  assert.match(posts[0].body.request_id, /^sharing-/);
  assert.equal(panel.state.sharing.state, 'off');
  reply = { sharing: { state: 'on', role_label: 'REAPER Assistant', team_digest: 'd1' } };
  await panel.setSharing({ checked: true });
  assert.deepEqual(
    { enabled: posts[1].body.enabled, team_digest: posts[1].body.team_digest },
    { enabled: true, team_digest: 'd1' }
  );
  // Review the updated assistant: cancelled sends nothing, confirmed renews.
  panel.state.sharing = { state: 'stale', role_label: 'REAPER Assistant', team_digest: 'd2' };
  const asked = [];
  panel.confirm = async (title, lines) => {
    asked.push({ title, lines });
    return asked.length > 1;
  };
  const trigger = { dataset: { sharingAction: 'review' } };
  await panel.runSharingAction(trigger);
  assert.equal(posts.length, 2);
  assert.match(asked[0].lines.join(' '), /keeps its own model, instructions and tools/);
  await panel.runSharingAction(trigger);
  assert.deepEqual(
    { path: posts[2].path, enabled: posts[2].body.enabled, digest: posts[2].body.team_digest },
    { path: '/sharing', enabled: true, digest: 'd2' }
  );
  // Add the assistant again.
  panel.state.sharing = {
    state: 'on',
    role_label: 'REAPER Assistant',
    agent_name: 'REAPER Assistant',
    assistant_missing: true
  };
  await panel.runSharingAction({ dataset: { sharingAction: 'readd' } });
  assert.equal(posts[3].path, '/sharing/assistant');
  assert.match(statuses.at(-1), /next song you open gets a new REAPER Assistant/);
});

const recentNow = new Date(2026, 9, 2, 15, 0);
const savedAgo = days =>
  new Date(
    recentNow.getFullYear(),
    recentNow.getMonth(),
    recentNow.getDate() - days,
    11
  ).toISOString();
const recentRows = count =>
  Array.from({ length: count }, (_, index) => ({
    id: `song-${index + 1}`,
    name: `Song ${index + 1}`,
    connection: 'catalog_only',
    can_open: true,
    last_saved_at: savedAgo(index)
  }));

test('Recently saved shows at most six songs that can be opened, each with Open', () => {
  const rows = [
    { id: 'ableton', name: 'Arrangement', can_open: false, last_saved_at: savedAgo(0) },
    { id: 'undated', name: 'Undated', can_open: true },
    ...recentRows(8)
  ];
  const view = recentlySavedView({ provider_read_only: false, rows }, recentNow);
  assert.equal(view.visible, true);
  assert.equal(view.cards.length, 6);
  assert.deepEqual(view.cards.map(card => [card.name, card.saved]).slice(0, 3), [
    ['Song 1', 'Saved today'],
    ['Song 2', 'Saved yesterday'],
    ['Song 3', 'Saved 2 days ago']
  ]);
  assert.equal(view.cards[0].open.label, 'Open');
  assert.match(view.cards[0].open.ariaLabel, /^Open Song 1: makes its workspace/);
  assert.equal(view.cards[0].row.id, 'song-1');
});

test('a read-only Home still lists its recent songs, without Open', () => {
  // The server marks no row openable while the Home provider is unavailable.
  const rows = recentRows(3).map(row => ({ ...row, can_open: false }));
  for (const flag of [true, undefined]) {
    const view = recentlySavedView({ provider_read_only: flag, rows }, recentNow);
    assert.equal(view.visible, true);
    assert.deepEqual(
      view.cards.map(card => [card.name, card.open]),
      [
        ['Song 1', null],
        ['Song 2', null],
        ['Song 3', null]
      ]
    );
  }
  const many = recentlySavedView({ provider_read_only: true, rows: recentRows(9) }, recentNow);
  assert.equal(many.cards.length, 6);
});

test('each library row shows its save time and song facts on one line', () => {
  const previousDocument = globalThis.document;
  const makeElement = tag => ({
    tag,
    children: [],
    textContent: '',
    className: '',
    dataset: {},
    append(...items) {
      this.children.push(...items);
    },
    replaceChildren() {
      this.children = [];
    },
    addEventListener() {},
    setAttribute() {}
  });
  const tbody = makeElement('tbody');
  globalThis.document = {
    createElement: makeElement,
    getElementById: id => (id === 'projectLibraryRows' ? tbody : null)
  };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' }); // no state: read-only
    panel.rows = [
      {
        id: 'with-facts',
        name: 'Night Drive',
        last_saved_at: savedAgo(3),
        facts: { track_count: 14, tempo_bpm: 92, tempo_varies: true, length_seconds: 221 }
      },
      { id: 'saved-only', name: 'Ableton Set', last_saved_at: savedAgo(3) },
      { id: 'undated', name: 'Undated' }
    ];
    panel.renderRows();
    const lines = tbody.children.map(tr =>
      tr.children[1].children.find(child => child.className === 'project-library-saved')
    );
    assert.match(lines[0].textContent, /^Saved .+ · 14 tracks · 92 BPM, varies · 3:41$/);
    assert.equal(lines[0].dataset.songFacts, '14 tracks · 92 BPM, varies · 3:41');
    assert.match(lines[1].textContent, /^Saved [^·]+$/, 'no facts: only "Saved …"');
    assert.equal(lines[1].dataset.songFacts, undefined);
    assert.equal(lines[2], undefined, 'no save time and no facts: no line');
    assert.doesNotMatch(
      tbody.children.map(tr => tr.children[1].children.map(c => c.textContent).join(' ')).join(' '),
      /\b(progress|complete|ready|unknown)\b/i
    );
  } finally {
    globalThis.document = previousDocument;
  }
});

test('Recently saved is hidden when no song has a save time', () => {
  for (const page of [
    null,
    { provider_read_only: false, rows: [] },
    { provider_read_only: false, rows: [{ id: 'a', name: 'A', can_open: true }] },
    {
      provider_read_only: true,
      rows: [{ id: 'a', name: 'A', last_saved_at: '0001-01-01T00:00:00Z' }]
    }
  ]) {
    const view = recentlySavedView(page, recentNow);
    assert.equal(view.visible, false);
    assert.deepEqual(view.cards, []);
  }
});
