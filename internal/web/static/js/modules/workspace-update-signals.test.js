import test from 'node:test';
import assert from 'node:assert/strict';
import {
  workspaceUpdateSignals,
  workspaceUpdateBadgeText,
  renderWorkspaceUpdateSignals,
  workspaceUpdateRoute,
  parseWorkspaceUpdateIntent,
  withoutWorkspaceUpdateIntent,
  matchesWorkspaceUpdateIntent,
  renderWorkspaceUpdateSection,
  renderWorkspaceUpdateMapMarkers
} from './workspace-update-signals.js';

function fixture() {
  return {
    state: 'ready',
    data: {
      workspace_id: 'ws',
      folder_slug: 'current-desk',
      name: 'Desk',
      welcome: {
        state: 'ready',
        unread_count: 2,
        agent_name: 'Mira',
        agent_instance_id: 'lead',
        session_id: 'session',
        messages: [
          { message_id: 'intro', revision: 1 },
          { message_id: 'assessment', revision: 2 }
        ]
      },
      review: {
        state: 'ready',
        root_id: 'root',
        batch_id: 'batch',
        pending_count: 10,
        needs_review_count: 2
      }
    }
  };
}

test('header labels keep a verified review usable when chat is unavailable, and distinguish unknown from last known', () => {
  const input = fixture();
  input.data.welcome = { state: 'unavailable' };
  assert.equal(
    workspaceUpdateBadgeText({ state: 'ready' }, [workspaceUpdateSignals(input)]),
    'Files to review'
  );
  assert.equal(workspaceUpdateBadgeText({ state: 'unavailable' }), 'Updates unavailable');
  assert.equal(workspaceUpdateBadgeText({ state: 'ready', hasMore: true }), 'More to check');
  assert.equal(workspaceUpdateBadgeText({ state: 'ready' }), '');
  assert.equal(
    workspaceUpdateBadgeText({ state: 'unavailable' }, [
      workspaceUpdateSignals(fixture(), { stale: true })
    ]),
    'Last known updates'
  );
  assert.equal(
    workspaceUpdateBadgeText({ state: 'ready' }, [workspaceUpdateSignals(fixture())]),
    'New message · Files to review'
  );
});

test('Home groups by workspace, offers explicit continuation and never calls an unvisited page clear', () => {
  const first = fixture();
  const second = fixture();
  second.data.workspace_id = 'other';
  second.data.folder_slug = 'other-desk';
  const read = id => (id === 'ws' ? first : second);
  const html = renderWorkspaceUpdateSection(
    { state: 'ready', ids: ['ws', 'other'], hasMore: true, nextAfter: 'other' },
    read
  );
  assert.equal((html.match(/class="cockpit-workspace-update"/g) || []).length, 2);
  assert.match(html, /data-update-row="ws"/);
  assert.match(html, /data-update-row="other"/);
  assert.match(html, /Nothing moves without your approval/);
  assert.match(html, /More workspaces have not been checked yet/);
  assert.match(html, /Check next workspaces/);
  assert.match(html, /\/workspaces\/current-desk/);
  assert.match(html, /\/workspaces\/other-desk/);
  const empty = renderWorkspaceUpdateSection(
    { state: 'ready', ids: [], after: 'last', hasMore: false },
    read
  );
  assert.match(empty, /in this page/);
  assert.match(empty, /Earlier pages may still have updates/);
  assert.match(empty, /First page/);
});

test('Home loading and unavailable windows stay distinct from a known empty page; Map emits no nested controls', () => {
  assert.match(
    renderWorkspaceUpdateSection({ state: 'loading', ids: [] }, fixture),
    /Checking workspace messages/
  );
  const unavailable = renderWorkspaceUpdateSection(
    { state: 'unavailable', stale: true, ids: ['ws'] },
    fixture
  );
  assert.match(unavailable, /Previous results may be out of date/);
  assert.match(unavailable, /last known/);
  assert.doesNotMatch(unavailable, /<a\b|No pending/);
  assert.match(unavailable, /Retry updates/);
  const map = renderWorkspaceUpdateMapMarkers(workspaceUpdateSignals(fixture()));
  assert.doesNotMatch(map, /<(?:a|button)\b/);
  assert.match(map, /data-map-update-route/);
  assert.match(map, /New message/);
  assert.match(map, /10 files to review/);
});

test('one grouped file signal and one message signal remain distinct from ticket/library counts', () => {
  const input = fixture();
  input.data.needs_attention_count = 99;
  input.data.ready_proposals = 37;
  const view = workspaceUpdateSignals(input);
  assert.deepEqual(
    view.signals.map(item => item.text),
    ['New message', '10 files to review']
  );
  assert.match(view.signals[0].label, /2 unread messages from Mira/);
  assert.match(view.signals[1].label, /Nothing moves without your approval/);
  assert.equal(view.signals.length, 2);
  assert.equal(input.data.needs_attention_count, 99);
  input.data.welcome.unread_count = 0;
  assert.deepEqual(
    workspaceUpdateSignals(input).signals.map(item => item.text),
    ['10 files to review']
  );
  input.data.review.pending_count = 1;
  assert.equal(workspaceUpdateSignals(input).signals[0].text, '1 file to review');
});

test('missing chat leaves valid review; unavailable observations are qualified and never actionable', () => {
  const input = fixture();
  input.data.welcome = { state: 'unavailable' };
  assert.equal(workspaceUpdateSignals(input).signals[0].route.includes('update=review'), true);
  assert.equal(workspaceUpdateSignals(input).state, 'unavailable');
  input.data.review = { ...input.data.review, state: 'unavailable', stale: true };
  const view = workspaceUpdateSignals(input);
  assert.equal(view.signals[0].text, '10 files to review · last known');
  assert.equal(view.signals[0].route, '');
  assert.equal(renderWorkspaceUpdateSignals(view).includes('<a'), false);
  assert.equal(
    workspaceUpdateSignals(fixture(), { stale: true }).signals.every(
      item => !item.route && item.stale
    ),
    true
  );
});

test('unknown and unavailable do not claim known zero, and removed authority carries no private label', () => {
  assert.equal(workspaceUpdateSignals({ state: 'loading' }).state, 'loading');
  assert.equal(workspaceUpdateSignals({ state: 'unavailable' }).state, 'unavailable');
  const input = fixture();
  input.data.welcome.unread_count = 0;
  input.data.review.pending_count = 0;
  assert.deepEqual(workspaceUpdateSignals(input).signals, []);
  assert.equal(workspaceUpdateSignals(input).state, 'ready');
  input.state = 'not_found';
  assert.deepEqual(workspaceUpdateSignals(input), {
    state: 'not_found',
    signals: [],
    name: '',
    workspaceId: ''
  });
});

test('page coverage uses singular and plural without mixing workspace and file counts', () => {
  for (const [ids, text] of [
    [[], '0 workspaces'],
    [['ws'], '1 workspace'],
    [['ws', 'other'], '2 workspaces']
  ]) {
    const html = renderWorkspaceUpdateSection({ state: 'ready', ids }, () => fixture());
    assert.ok(html.includes(`${text} in this page.`));
    assert.ok(!html.includes('1 workspaces'));
  }
});

test('canonical slug is required; URLs select navigation only and carry the exact saved welcome identity', () => {
  const data = fixture().data;
  const route = workspaceUpdateRoute(data, 'welcome');
  const url = new URL(route, 'https://ori.invalid');
  assert.equal(url.pathname, '/workspaces/current-desk');
  const intent = parseWorkspaceUpdateIntent(url.search);
  assert.deepEqual(intent, {
    kind: 'welcome',
    workspaceId: 'ws',
    sessionId: 'session',
    leadId: 'lead',
    messageId: 'intro'
  });
  assert.equal(matchesWorkspaceUpdateIntent(intent, data), true);
  data.welcome.messages[0].seen_at = 'now';
  assert.equal(matchesWorkspaceUpdateIntent(intent, data), true, 'a seen message remains readable');
  assert.equal(
    parseWorkspaceUpdateIntent(
      new URL(workspaceUpdateRoute(data, 'welcome'), 'https://ori.invalid').search
    ).messageId,
    'assessment'
  );
  for (const slug of ['', '..', '/other', ' nested ', 'nested/path', 'nested\\path'])
    assert.equal(workspaceUpdateRoute({ ...data, folder_slug: slug }, 'welcome'), '');
  assert.equal(
    workspaceUpdateRoute({ ...data, folder_slug: '' }, 'review'),
    '',
    'do not guess a slug from name or ID'
  );
});

test('stale identity cannot select a namesake, another workspace, or a deleted message', () => {
  const data = fixture().data;
  const intent = parseWorkspaceUpdateIntent(
    new URL(workspaceUpdateRoute(data, 'welcome'), 'https://ori.invalid').search
  );
  for (const changed of [
    { ...data, workspace_id: 'other' },
    { ...data, welcome: { ...data.welcome, session_id: 'replacement' } },
    { ...data, welcome: { ...data.welcome, agent_instance_id: 'namesake' } },
    { ...data, welcome: { ...data.welcome, messages: [] } },
    { ...data, welcome: { ...data.welcome, stale: true } }
  ])
    assert.equal(matchesWorkspaceUpdateIntent(intent, changed), false);
  const review = parseWorkspaceUpdateIntent(
    new URL(workspaceUpdateRoute(data, 'review'), 'https://ori.invalid').search
  );
  assert.equal(
    matchesWorkspaceUpdateIntent(review, {
      ...data,
      review: { ...data.review, batch_id: 'new-current-batch' }
    }),
    true
  );
  assert.equal(
    matchesWorkspaceUpdateIntent(review, { ...data, review: { state: 'not_applicable' } }),
    false
  );
});

test('malformed/repeated arrival parameters never create a second action; consuming preserves unrelated view state', () => {
  for (const query of [
    'update=apply&update_workspace=ws',
    'update=review',
    'update=review&update_workspace=ws&update_workspace=other',
    'update=welcome&update_workspace=ws',
    'update=review&update_workspace=ws&update_session=s',
    'update_message=m'
  ])
    assert.deepEqual(parseWorkspaceUpdateIntent(query), { invalid: true });
  assert.equal(parseWorkspaceUpdateIntent('?mode=map'), null);
  assert.equal(
    withoutWorkspaceUpdateIntent('?mode=map&update=review&update_workspace=ws&panel=backlog'),
    'mode=map&panel=backlog'
  );
});

test('labels are escaped and unsafe executable/foreign routes cannot become controls', () => {
  const input = fixture();
  input.data.name = '<img src=x onerror=alert(1)>';
  input.data.welcome.agent_name = '" onclick="unsafe';
  const view = workspaceUpdateSignals(input);
  const html = renderWorkspaceUpdateSignals(view, { tabIndex: -1 });
  assert.equal(html.includes('<img'), false);
  assert.match(html, /&lt;img/);
  assert.match(html, /tabindex="-1"/);
  for (const route of [
    'javascript:alert(1)',
    '//outside.invalid',
    '/workspaces/desk?update=review&update_workspace=other',
    '/workspaces/%2e%2e?update=review&update_workspace=ws',
    '/workspaces/desk?update=review&update_workspace=ws&panel=assistant'
  ]) {
    assert.equal(
      renderWorkspaceUpdateSignals({
        workspaceId: 'ws',
        signals: [{ kind: 'review', route, text: 'Review', label: 'Review' }]
      }).includes('<a'),
      false
    );
  }
});
