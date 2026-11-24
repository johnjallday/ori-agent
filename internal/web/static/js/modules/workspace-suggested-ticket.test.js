import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ARRIVAL_MESSAGES,
  TITLE_LIMIT,
  isSuggestedTicketRoute,
  offerSuggestedTicket,
  searchWithoutSuggestedTicketArrival,
  splitSuggestion,
  suggestedTicketArrivalFromSearch,
  suggestedTicketRoute,
  suggestionFromFirstTask
} from './workspace-suggested-ticket.js';

const finished = (overrides = {}) => ({
  first_task: {
    recipe: 'software-assessment',
    state: 'finished',
    next_request: 'Add a health check endpoint.',
    folder_name: 'widget',
    ...overrides
  }
});

test('only the literal next=assessment asks for the suggestion, and it can be taken off the address', () => {
  assert.deepEqual(suggestedTicketArrivalFromSearch('?next=assessment'), { kind: 'assessment' });
  assert.deepEqual(suggestedTicketArrivalFromSearch('?mode=map&next=assessment'), {
    kind: 'assessment'
  });
  for (const search of ['', '?next=', '?next=other', '?next=Assessment', '?other=assessment', null])
    assert.equal(suggestedTicketArrivalFromSearch(search), null, String(search));
  assert.equal(searchWithoutSuggestedTicketArrival('?next=assessment'), '');
  assert.equal(
    searchWithoutSuggestedTicketArrival('?mode=map&next=assessment&panel=settings'),
    '?mode=map&panel=settings'
  );
});

test('the canonical route is the workspace itself, and only this workspace is accepted', () => {
  assert.equal(suggestedTicketRoute('/workspaces/widget'), '/workspaces/widget?next=assessment');
  assert.equal(suggestedTicketRoute(''), '');
  const ok = route => isSuggestedTicketRoute(route, '/workspaces/widget');
  assert.equal(ok('/workspaces/widget?next=assessment'), true);
  // The retired Plans address stays valid for the same workspace.
  assert.equal(ok('/workspaces/widget/plans?next=assessment'), true);
  for (const route of [
    '',
    '/workspaces/widget',
    '/workspaces/widget?next=assessment&run=1',
    '/workspaces/widget?next=other',
    '/workspaces/widget/plans',
    '/workspaces/other?next=assessment',
    '/workspaces/other/plans?next=assessment',
    '/workspaces/widget2?next=assessment',
    'https://evil.example/workspaces/widget?next=assessment',
    '//evil.example/workspaces/widget?next=assessment',
    '/workspaces\\widget?next=assessment',
    '/workspaces/widget?next=assessment\n',
    '/workspaces/widget?next=assessment\u0000'
  ])
    assert.equal(ok(route), false, JSON.stringify(route));
  // Without this workspace's own route there is nothing to compare, so no link.
  for (const base of ['', '//evil.example', 'workspaces/widget', null])
    assert.equal(isSuggestedTicketRoute('/workspaces/widget?next=assessment', base), false);
});

test('a short suggestion is the title; a long or multi-line one keeps every word', () => {
  assert.deepEqual(splitSuggestion('Add a health check endpoint.'), {
    title: 'Add a health check endpoint.',
    details: ''
  });
  assert.deepEqual(splitSuggestion('  Add a health check.  ', { source: 'Suggested by x.' }), {
    title: 'Add a health check.',
    details: 'Suggested by x.'
  });
  const multi = splitSuggestion('Add a health check.\nIt should return 200.', {
    source: 'Suggested by x.'
  });
  assert.equal(multi.title, 'Add a health check.');
  assert.equal(multi.details, 'Add a health check.\nIt should return 200.\n\nSuggested by x.');

  const words = 'document the build '.repeat(40).trim();
  const long = splitSuggestion(words);
  assert.ok(long.title.length <= TITLE_LIMIT, `title is ${long.title.length} characters`);
  assert.match(long.title, /…$/);
  assert.equal(long.details, words, 'the whole request is kept in Details');
  const unbroken = splitSuggestion('x'.repeat(TITLE_LIMIT + 50));
  assert.equal(unbroken.title.length, TITLE_LIMIT);
  assert.equal(unbroken.details.length, TITLE_LIMIT + 50);
});

test('untrusted text stays text: it is returned verbatim, never turned into markup', () => {
  const hostile = '<img src=x onerror=alert(1)>\n<script>alert(2)</script>';
  const { title, details } = splitSuggestion(hostile, { source: 'Suggested by <b>x</b>.' });
  assert.equal(title, '<img src=x onerror=alert(1)>');
  assert.match(details, /<script>alert\(2\)<\/script>/);
  assert.match(details, /<b>x<\/b>/);
});

test('a suggestion is offered only from a finished assessment that kept one', () => {
  const good = suggestionFromFirstTask(finished());
  assert.equal(good.ok, true);
  assert.equal(good.request, 'Add a health check endpoint.');
  assert.equal(good.source, 'Suggested by the first assessment of widget.');
  assert.equal(
    suggestionFromFirstTask(finished({ folder_name: '' })).source,
    'Suggested by the first assessment.'
  );
  for (const body of [
    null,
    {},
    { first_task: null },
    finished({ state: 'running' }),
    finished({ state: 'failed' }),
    finished({ recipe: '' }),
    finished({ next_request: '   ' }),
    finished({ next_request: undefined })
  ])
    assert.deepEqual(suggestionFromFirstTask(body), { ok: false, reason: 'missing' });
});

const harness = (overrides = {}) => {
  const calls = { requests: [], opened: [] };
  const options = {
    workspaceId: 'ws-1',
    workspaceName: 'Widget',
    agents: ['Writer'],
    request: async path => {
      calls.requests.push(path);
      return finished();
    },
    openGiveWork: opts => {
      calls.opened.push(opts);
      return {};
    },
    ...overrides
  };
  return { calls, options };
};

test('the suggestion opens Give work with plain starting text, nothing assigned and nothing saved', async () => {
  const { calls, options } = harness();
  assert.equal(await offerSuggestedTicket(options), 'opened');
  assert.deepEqual(calls.requests, ['/api/workspaces/ws-1/folder-first-task']);
  assert.equal(calls.opened.length, 1);
  const opened = calls.opened[0];
  assert.equal(opened.what, 'Add a health check endpoint.');
  assert.equal(opened.details, 'Suggested by the first assessment of widget.');
  // The dialog is told no assignee and no Start now: nothing is sent to run.
  for (const key of ['assignee', 'startNow', 'start', 'who'])
    assert.equal(key in opened, false, key);
});

test('a workspace id is encoded into the read, never trusted into the path', async () => {
  const { calls, options } = harness({ workspaceId: 'a/b?c' });
  await offerSuggestedTicket(options);
  assert.deepEqual(calls.requests, ['/api/workspaces/a%2Fb%3Fc/folder-first-task']);
});

test('a missing suggestion opens nothing and says so', async () => {
  for (const body of [{}, finished({ state: 'failed' }), finished({ next_request: '' })]) {
    const { calls, options } = harness({ request: async () => body });
    assert.equal(await offerSuggestedTicket(options), 'missing');
    assert.equal(calls.opened.length, 0);
  }
  assert.match(ARRIVAL_MESSAGES.missing, /no finished assessment/);
});

test('a failed read opens nothing and is reported as unavailable, not as missing', async () => {
  const { calls, options } = harness({
    request: async () => {
      throw new Error('HTTP 500');
    }
  });
  assert.equal(await offerSuggestedTicket(options), 'unavailable');
  assert.equal(calls.opened.length, 0);
  assert.match(ARRIVAL_MESSAGES.unavailable, /could not be read/);
});

test('an answer that arrives after the workspace changed is dropped, even a failure', async () => {
  let current = true;
  const late = harness({
    isCurrent: () => current,
    request: async () => {
      current = false;
      return finished();
    }
  });
  assert.equal(await offerSuggestedTicket(late.options), 'stale');
  assert.equal(late.calls.opened.length, 0);

  current = true;
  const lateFailure = harness({
    isCurrent: () => current,
    request: async () => {
      current = false;
      throw new Error('gone');
    }
  });
  assert.equal(await offerSuggestedTicket(lateFailure.options), 'stale');
});

test('a Give work dialog the person already opened is never overwritten', async () => {
  const { calls, options } = harness({ dialogOpen: () => true });
  assert.equal(await offerSuggestedTicket(options), 'busy');
  assert.equal(calls.opened.length, 0);
  assert.match(ARRIVAL_MESSAGES.busy, /already open/);
});

test('a page that cannot show the dialog reports it instead of pretending', async () => {
  assert.equal(
    await offerSuggestedTicket(harness({ openGiveWork: () => null }).options),
    'unsupported'
  );
  assert.equal(
    await offerSuggestedTicket({ workspaceId: '', request: async () => ({}) }),
    'unsupported'
  );
  assert.equal(await offerSuggestedTicket({ workspaceId: 'ws' }), 'unsupported');
});
