import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  createFolderContextController,
  discussionChips,
  emptySendView,
  folderContextChoices,
  folderDecline,
  folderBlockerView,
  structureCardView,
  FOLDER_DISCLOSURE,
  observationSummary,
  coverageSummary
} from './personal-assistant-folder-context.js';

const observation = id => ({
  version: 1,
  id,
  folder: `Folder ${id}`,
  files: 3,
  scanned_at: '2026-01-01T12:00:00Z',
  kinds: [{ name: '.md', count: 3 }],
  projects: [{ id: 'root', name: `Folder ${id}`, files: 3 }],
  coverage: { max_depth: 3, max_entries: 5000, budget_seconds: 3, partial: true }
});
const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};
function fixture(post) {
  let id = '',
    busy = false;
  const calls = [];
  const controller = createFolderContextController({
    uuid: () => 'opaque-draft',
    currentId: () => id,
    isBusy: () => busy,
    post: (url, body) => {
      calls.push({ url, body });
      return post(url, body);
    }
  });
  return {
    controller,
    calls,
    setId: value => {
      id = value;
    },
    setBusy: value => {
      busy = value;
    }
  };
}

test('select is local, request is references only, and disclosure names provider boundary', async () => {
  const { controller, calls } = fixture(async () => ({
    observation: observation('a'),
    revision: ''
  }));
  assert.equal(await controller.select('chip', 'documents'), true);
  assert.deepEqual(
    calls.map(call => call.url),
    ['/api/home-assistant/folder-context/select']
  );
  assert.deepEqual(calls[0].body, {
    draft_id: 'opaque-draft',
    revision: '',
    mode: 'chip',
    chip: 'documents'
  });
  assert.deepEqual(controller.request(), {
    selection_id: 'a',
    revision: '',
    draft_id: 'opaque-draft'
  });
  assert.equal(controller.state.preview, true);
  assert.match(FOLDER_DISCLOSURE, /contents have not been read/);
  assert.match(FOLDER_DISCLOSURE, /configured model/);
  assert.match(observationSummary(observation('a')), /Partial look: 3 files/);
  assert.match(coverageSummary(observation('a')), /not a complete tree/);
});

test('the Add folder sheet lists the server chips in order and says why the dialog is missing', () => {
  const sentence =
    'Pick a folder from the list; the folder dialog is switched off in this session.';
  const off = folderContextChoices({
    chips: [
      { id: 'music', label: 'Music' },
      { id: 'documents', label: 'Documents' }
    ],
    picker_available: false,
    picker_note: sentence
  });
  assert.deepEqual(off.choices, [
    { label: 'Music', mode: 'chip', chip: 'music' },
    { label: 'Documents', mode: 'chip', chip: 'documents' }
  ]);
  assert.equal(off.note, sentence);

  const on = folderContextChoices({
    chips: [{ id: 'music', label: 'Music' }],
    picker_available: true
  });
  assert.deepEqual(on.choices.at(-1), { label: 'Pick another folder…', mode: 'picker' });
  assert.equal(on.note, '');
});

test('the Add folder sheet never prints a reason code', () => {
  for (const code of ['desktop_off', 'platform', 'picker_unavailable']) {
    const withChips = folderContextChoices({
      chips: [{ id: 'music', label: 'Music' }],
      picker_available: false,
      picker_note: code
    });
    assert.equal(withChips.note, 'Pick a folder from the list for now.');
    const empty = folderContextChoices({ chips: [], picker_available: false, picker_note: code });
    assert.equal(empty.note, 'No folder can be chosen here.');
    assert.deepEqual(empty.choices, []);
  }
});

test('a typed folder is selected by its token; the drawer never sends a path', async () => {
  const { controller, calls } = fixture(async () => ({
    observation: observation('typed'),
    revision: ''
  }));
  assert.equal(await controller.select('selection', '', 'tok-1'), true);
  assert.deepEqual(calls[0].body, {
    draft_id: 'opaque-draft',
    revision: '',
    mode: 'selection',
    selection_token: 'tok-1'
  });
  assert.equal(controller.state.observation.id, 'typed');
  assert.equal(controller.state.preview, true);

  // The sheet mounts the shared control, and only a token crosses to select.
  const source = readFileSync(
    new URL('./personal-assistant-folder-context.js', import.meta.url),
    'utf8'
  );
  assert.match(source, /createFolderPathEntry\(\{/);
  assert.match(source, /choose\('selection', '', selection\.selectionToken\)/);
  assert.doesNotMatch(source, /\bpath\s*:/);
});

test('one project Ori can hire for is reviewed on attach; a group or any other folder is not', async () => {
  let result = { observation: observation('song'), revision: '', hire_candidate: 'root' };
  const { controller } = fixture(async () => result);
  assert.equal(await controller.select('selection', '', 'tok-song'), true);
  assert.equal(controller.state.hireCandidate, 'root');
  // A proposed group is reviewed from its own card, never as one project.
  result = {
    observation: observation('albums'),
    revision: '',
    structure: { version: 1 },
    hire_candidate: 'root'
  };
  await controller.select('chip', 'music');
  assert.equal(controller.state.hireCandidate, '');
  result = { observation: observation('notes'), revision: '' };
  await controller.select('chip', 'documents');
  assert.equal(controller.state.hireCandidate, '');
  // While another setup review waits, the drawer says so in one line.
  result = { observation: observation('song-2'), revision: '', hire_unavailable: 'review_pending' };
  await controller.select('selection', '', 'tok-song-2');
  assert.equal(controller.state.hireCandidate, '');
  assert.equal(controller.state.hireUnavailable, 'review_pending');
  assert.deepEqual(structureCardView(controller.state), {
    kind: 'unavailable',
    key: 'song-2:unavailable',
    line: 'Another setup review is open. Finish or close it to set up this folder.'
  });
  await controller.remove();
  assert.equal(controller.state.hireCandidate, '');
  assert.equal(controller.state.hireUnavailable, '');

  // The attach opens that review in the conversation instead of the tree.
  const source = readFileSync(
    new URL('./personal-assistant-folder-context.js', import.meta.url),
    'utf8'
  );
  assert.match(source, /window\.PersonalAssistantFolderSetup\?\.reviewCandidate\?\.\(hire\)/);
  assert.match(source, /!reviewed &&\s+!controller\.state\.structure &&/);
  const setup = readFileSync(
    new URL('./personal-assistant-folder-setup.js', import.meta.url),
    'utf8'
  );
  assert.match(setup, /export function reviewCandidate\(candidateId\)/);
  assert.match(setup, /reviewFrom\(elements\.open, candidateId\);/);
  // Review setup on such a folder brings the same review back, with no chooser.
  assert.match(setup, /else if \(!reviewCandidate\(current\?\.hireCandidate\)\) openChoices\(\);/);
});

test('a single hire names its verified blocker, keeping unknown contracts on the legacy line', async () => {
  const blocker = {
    kind: 'conversation',
    subject: '<img src=x onerror=alert(1)>',
    status: 'pending',
    offer_id: 'offer-5',
    conversation_id: 'album-5',
    blocks: true,
    message: 'Finish or close this setup in its owning conversation.'
  };
  const f = fixture(async () => ({
    observation: observation('album-3'),
    hire_unavailable: 'review_pending',
    blocker
  }));
  await f.controller.select('chip', 'music');
  assert.equal(structureCardView(f.controller.state).kind, 'unavailable');
  assert.deepEqual(folderBlockerView(blocker), {
    line: `${blocker.subject} · Waiting for review. ${blocker.message}`,
    open: true,
    label: `Open ${blocker.subject} review`
  });
  for (const value of [null, { ...blocker, kind: 'unknown' }]) {
    assert.equal(folderBlockerView(value), null);
    assert.match(
      structureCardView({ ...f.controller.state, blocker: value }).line,
      /Another setup review/
    );
  }
  assert.equal(folderBlockerView({ ...blocker, conversation_id: '' }).open, false);
});

test('refresh and return re-read hire eligibility without opening a review', async () => {
  const f = fixture(async url =>
    url.endsWith('/select')
      ? {
          observation: observation('album-3'),
          hire_unavailable: 'review_pending',
          blocker: { subject: 'Album-5' }
        }
      : { observation_id: 'album-3', hire_candidate: 'root', hire_unavailable: '', blocker: null }
  );
  await f.controller.select('chip', 'music');
  const saved = f.controller.snapshot();
  assert.equal(await f.controller.refreshRecommendation(), true);
  assert.equal(f.controller.state.hireCandidate, 'root');
  assert.equal(f.controller.state.hireUnavailable, '');
  assert.equal(f.controller.state.blocker, null);
  f.controller.reset();
  assert.equal(await f.controller.restore(saved), true);
  assert.equal(f.controller.state.hireCandidate, 'root');
  assert.equal(f.controller.state.hireUnavailable, '');
  assert.deepEqual(
    f.calls.map(call => call.url.split('/').at(-1)),
    ['select', 'recommendation', 'recommendation']
  );
});

for (const change of ['replacement', 'removal', 'conversation']) {
  test(`a late hire refresh cannot cross ${change}`, async () => {
    const wait = deferred();
    const f = fixture(async url =>
      url.endsWith('/recommendation')
        ? wait.promise
        : { observation: observation('new'), hire_unavailable: 'review_pending' }
    );
    await f.controller.select('chip', 'music');
    const read = f.controller.refreshRecommendation();
    if (change === 'replacement') await f.controller.select('chip', 'documents');
    else if (change === 'removal') await f.controller.remove();
    else {
      f.setId('other');
      f.controller.reset('other');
    }
    wait.resolve({ observation_id: 'new', hire_candidate: 'root', hire_unavailable: '' });
    assert.equal(await read, false);
    assert.notEqual(f.controller.state.hireCandidate, 'root');
  });
}

test('cancel and failed replacement retain the exact previous selection', async () => {
  let result = { observation: observation('a') };
  const { controller } = fixture(async () => {
    if (result instanceof Error) throw result;
    return result;
  });
  await controller.select('chip', 'documents');
  result = { cancelled: true };
  assert.equal(await controller.select('picker'), false);
  assert.equal(controller.state.observation.id, 'a');
  result = new Error('Access denied');
  assert.equal(await controller.select('picker'), false);
  assert.equal(controller.state.observation.id, 'a');
  assert.equal(controller.state.notice, 'Access denied');
});

test('late selection cannot follow New or a different conversation', async () => {
  const pending = deferred();
  const f = fixture(() => pending.promise);
  const selecting = f.controller.select('picker');
  f.setId('other');
  f.controller.reset('other');
  pending.resolve({ observation: observation('wrong') });
  assert.equal(await selecting, false);
  assert.equal(f.controller.state.observation, null);
  assert.equal(f.controller.state.pending, false);
});

test('overlapping selections never use last-response-wins', async () => {
  const first = deferred(),
    second = deferred();
  let call = 0;
  const { controller } = fixture(() => (++call === 1 ? first.promise : second.promise));
  const a = controller.select('picker');
  const b = controller.select('chip', 'documents');
  second.resolve({ observation: observation('b') });
  assert.equal(await b, true);
  first.resolve({ observation: observation('a') });
  assert.equal(await a, false);
  assert.equal(controller.state.observation.id, 'b');
});

test('removing local preview is local and invalidates an outstanding choice', async () => {
  const pending = deferred();
  const { controller, calls } = fixture(() => pending.promise);
  const select = controller.select('picker');
  assert.equal(await controller.remove(), true);
  pending.resolve({ observation: observation('late') });
  assert.equal(await select, false);
  assert.equal(controller.state.observation, null);
  assert.equal(calls.length, 1);
  assert.match(controller.state.notice, /Earlier discussion remains history/);
});

test('accepted context detach checks revision, failure keeps attachment and success clears it', async () => {
  let fail = true;
  const f = fixture(async () => {
    if (fail) throw new Error('Stale revision');
    return { revision: 'r2' };
  });
  f.setId('saved');
  f.controller.reset('saved', { revision: 'r1', observation: observation('a') });
  assert.equal(await f.controller.remove(), false);
  assert.equal(f.controller.state.observation.id, 'a');
  assert.deepEqual(f.calls[0].body, { conversation_id: 'saved', revision: 'r1' });
  fail = false;
  assert.equal(await f.controller.remove(), true);
  assert.equal(f.controller.request(), null);
  assert.equal(f.controller.state.revision, 'r2');
});

test('accepted first turn binds the exact folder to its conversation and followup revision', async () => {
  const f = fixture(async () => ({ observation: observation('a'), revision: '' }));
  await f.controller.select('chip', 'documents');
  f.setId('canonical');
  f.controller.accepted('canonical', { revision: 'event-1', observation: observation('a') });
  assert.deepEqual(f.controller.request(), { selection_id: 'a', revision: 'event-1' });
  assert.equal(f.controller.state.preview, false);
  f.controller.accepted('canonical', { revision: 'event-2', observation: observation('a') });
  assert.deepEqual(f.controller.request(), { selection_id: 'a', revision: 'event-2' });
  f.setId('');
  f.controller.reset();
  assert.equal(f.controller.request(), null);
});

test('successful local replacement adopts the retirement revision without saving new evidence', async () => {
  const f = fixture(async () => ({ observation: observation('b'), revision: 'retired-a' }));
  f.setId('canonical');
  f.controller.reset('canonical', { revision: 'event-a', observation: observation('a') });
  await f.controller.select('chip', 'desktop');
  assert.equal(f.controller.state.preview, true);
  assert.equal(f.controller.state.accepted, null);
  assert.deepEqual(f.controller.request(), { selection_id: 'b', revision: 'retired-a' });
});

test('historical snapshot is explicit; busy turn cannot mutate its context', async () => {
  const f = fixture(async () => {
    throw new Error('must not call');
  });
  f.setId('saved');
  f.controller.reset('saved', { revision: 'r1', observation: observation('a'), authority: 'lost' });
  assert.deepEqual(f.controller.request(), { selection_id: 'a', revision: 'r1', historical: true });
  f.controller.accepted('saved', {
    revision: 'r2',
    observation: observation('a'),
    historical: true
  });
  assert.equal(f.controller.state.authority, 'lost');
  assert.deepEqual(f.controller.request(), { selection_id: 'a', revision: 'r2', historical: true });
  f.setBusy(true);
  assert.equal(await f.controller.select('picker'), false);
  assert.equal(await f.controller.remove(), false);
  assert.equal(f.calls.length, 0);
});

test('a conversation saved by a turn without a folder can take a folder afterwards', async () => {
  const f = fixture(async () => ({ observation: observation('a'), revision: '' }));
  // The first plain turn saved the conversation; the controller still holds a draft.
  f.setId('saved');
  assert.equal(await f.controller.select('chip', 'documents'), false);
  assert.equal(f.controller.state.preview, false, 'a draft-targeted result is not shown');
  assert.equal(f.controller.adopt('saved'), true);
  assert.equal(await f.controller.select('chip', 'documents'), true);
  assert.deepEqual(f.calls[1].body, {
    conversation_id: 'saved',
    revision: '',
    mode: 'chip',
    chip: 'documents'
  });
  assert.equal(f.controller.state.preview, true);
  assert.equal(f.controller.request().selection_id, 'a');
  assert.equal('draft_id' in f.controller.request(), false);
  // Adopting never discards a folder already chosen, and is a no-op when current.
  assert.equal(f.controller.adopt('other'), false);
  assert.equal(f.controller.state.conversationId, 'saved');
  assert.equal(f.controller.adopt(''), false);
});

test('explicit review sends opaque candidate and scope, never contents or a model turn', async () => {
  const f = fixture(async url =>
    url.endsWith('/select')
      ? { observation: observation('a') }
      : { conversation: { id: 'saved', stored: true } }
  );
  await f.controller.select('chip', 'documents');
  const result = await f.controller.review('root');
  assert.equal(result.conversation.id, 'saved');
  assert.deepEqual(f.calls[1], {
    url: '/api/home-assistant/folder-context/review',
    body: { draft_id: 'opaque-draft', revision: '', selection_id: 'a', candidate_id: 'root' }
  });
  assert.equal(f.controller.state.pending, false);
  f.setId('saved');
  f.controller.accepted('saved', {
    observation: observation('a'),
    revision: 'r1',
    offer_id: 'offer'
  });
  await f.controller.review('', true);
  assert.deepEqual(f.calls[2].body, {
    conversation_id: 'saved',
    revision: 'r1',
    offer_id: 'offer'
  });
});

test('explicit readiness recheck retains scope and suppresses late responses without execution', async () => {
  let finish;
  const f = fixture(async url =>
    url.endsWith('/select')
      ? { observation: observation('a') }
      : new Promise(resolve => {
          finish = resolve;
        })
  );
  await f.controller.select('chip', 'documents');
  const pending = f.controller.review('root', false, undefined, { recheck: true });
  assert.deepEqual(f.calls[1].body, {
    draft_id: 'opaque-draft',
    revision: '',
    selection_id: 'a',
    candidate_id: 'root',
    recheck: true
  });
  assert.equal(await f.controller.review('root', false, undefined, { recheck: true }), null);
  f.setId('another-conversation');
  finish({ conversation: { id: 'stale' } });
  assert.equal(await pending, null);
  assert.equal(f.calls.length, 2);
  assert.ok(f.calls.every(call => !/decide|install|setup\/run/.test(call.url)));
});

test('review carries a named workspace as a reference while the page stays the location', async t => {
  globalThis.window = { location: { pathname: '/workspaces/album-1' } };
  globalThis.document = { body: { dataset: { workspaceId: 'ws-1', workspaceSlug: 'album-1' } } };
  t.after(() => {
    delete globalThis.window;
    delete globalThis.document;
  });
  const f = fixture(async url =>
    url.endsWith('/select') ? { observation: observation('a') } : { folder_placement_choice: {} }
  );
  await f.controller.select('chip', 'documents');
  await f.controller.review('root', false, undefined, { subject_workspace_id: 'ws-home' });
  const named = f.calls[1].body.context;
  assert.equal(named.subject_workspace_id, 'ws-home');
  assert.equal(named.workspace_id, 'ws-1');
  assert.equal(named.page_path, '/workspaces/album-1');
  assert.equal(named.origin, 'personal_assistant_panel');
  // The chosen operation keeps the same named workspace; nothing else names one.
  await f.controller.review('root', false, undefined, {
    operation: 'create_project_workspace',
    destination_id: 'ws-home',
    subject_workspace_id: 'ws-home'
  });
  assert.equal(f.calls[2].body.context.subject_workspace_id, 'ws-home');
  assert.equal(f.calls[2].body.destination_id, 'ws-home');
  await f.controller.review('root');
  assert.equal('subject_workspace_id' in f.calls[3].body.context, false);
});

test('an attached collection keeps its proposed group as a local preview until Send', async () => {
  const structure = {
    version: 1,
    folder: 'Albums',
    group: { name: 'Albums', type: 'group', destination_state: 'new' },
    members: [{ candidate_id: 'candidate-1', name: 'Album-1', included: true }]
  };
  let reply = { observation: observation('a'), revision: '', structure };
  const f = fixture(async url => (url.endsWith('/select') ? reply : { conversation: {} }));
  assert.equal(await f.controller.select('chip', 'downloads'), true);
  assert.equal(f.controller.state.structure, structure);
  assert.equal(f.controller.state.preview, true);
  // Only the select call was made: showing the proposal costs no request.
  assert.equal(f.calls.length, 1);
  // What the user changed in Adjust rides the ordinary review request.
  const adjustment = {
    group_type: 'group',
    group_name: 'Albums 2026',
    members: [{ candidate_id: 'candidate-1', included: true, name: 'First' }]
  };
  await f.controller.review('root', false, undefined, { structure: adjustment });
  assert.deepEqual(f.calls[1].body.structure, adjustment);
  await f.controller.review('root');
  assert.equal('structure' in f.calls[2].body, false);
  // A single folder, an unknown record version, and a saved turn carry none.
  reply = { observation: observation('b'), revision: '' };
  await f.controller.select('chip', 'desktop');
  assert.equal(f.controller.state.structure, null);
  reply = { observation: observation('c'), revision: '', structure: { ...structure, version: 2 } };
  await f.controller.select('chip', 'downloads');
  assert.equal(f.controller.state.structure, null);
  reply = { observation: observation('d'), revision: '', structure };
  await f.controller.select('chip', 'downloads');
  f.controller.accepted('chat', { observation: observation('d'), revision: 'r1' });
  assert.equal(f.controller.state.structure, null);
});

test('the attach card offers explicit review, Adjust and Not now, or one line', async () => {
  const structure = {
    version: 1,
    folder: 'Albums',
    group: { name: 'Albums', type: 'group', destination_state: 'new' },
    members: [{ candidate_id: 'candidate-1', name: 'Album-1', included: true }]
  };
  const declined = { folder_key: 'a'.repeat(64), name: 'Albums', at: '2026-10-06T08:02:00Z' };
  let reply = { observation: observation('a'), revision: '', structure };
  const f = fixture(async () => reply);
  await f.controller.select('chip', 'downloads');
  let view = structureCardView(f.controller.state);
  assert.equal(view.kind, 'proposal');
  assert.deepEqual(
    view.actions.map(action => [action.label, action.disabled]),
    [
      ['Review group setup', false],
      ['Adjust…', false],
      ['Not now', false]
    ]
  );
  // Not now keeps this folder's card away; the next attach is a new folder.
  assert.equal(structureCardView(f.controller.state, 'a').kind, 'none');
  assert.equal(structureCardView(f.controller.state, 'other').kind, 'proposal');

  // An earlier No thanks keeps the card; its chip stands where Set up would.
  reply = { observation: observation('b'), revision: '', structure, declined };
  await f.controller.select('chip', 'downloads');
  assert.deepEqual(f.controller.state.declined, declined);
  view = structureCardView(f.controller.state);
  assert.equal(view.kind, 'proposal');
  assert.deepEqual(
    view.actions.map(action => [action.id, action.label]),
    [
      ['ask', 'You said no to Albums on Oct 6 · Ask again'],
      ['adjust', 'Adjust…'],
      ['later', 'Not now']
    ]
  );
  // A decline with no usable key is not shown as one.
  reply = { observation: observation('c'), revision: '', structure, declined: { name: 'Albums' } };
  await f.controller.select('chip', 'downloads');
  assert.equal(f.controller.state.declined, null);

  // No card: the reason in one line, never a code.
  for (const [reason, line] of [
    [
      'review_pending',
      'Another setup review is open. Finish or close it to see the proposal for this folder.'
    ],
    ['setup_unavailable', 'Setting up workspaces is not available here.']
  ]) {
    reply = { observation: observation(reason), revision: '', structure_unavailable: reason };
    await f.controller.select('chip', 'downloads');
    view = structureCardView(f.controller.state);
    assert.equal(view.kind, 'unavailable');
    assert.equal(view.line, line);
  }
  reply = { observation: observation('e'), revision: '', structure_unavailable: 'surprise' };
  await f.controller.select('chip', 'downloads');
  assert.equal(structureCardView(f.controller.state).kind, 'none');
  // A single folder has neither.
  reply = { observation: observation('f'), revision: '' };
  await f.controller.select('chip', 'desktop');
  assert.equal(structureCardView(f.controller.state).kind, 'none');
  assert.equal(folderDecline({ ...declined, folder_key: '../etc' }), null);
});

test('library discovery coexists with a pending review and never sends on attach', async () => {
  let reply = {
    observation: observation('albums'),
    revision: '',
    structure_unavailable: 'review_pending',
    structure: {
      version: 1,
      folder: 'Albums',
      group: { type: 'group', name: 'Albums' },
      members: [],
      recommendation: {
        operation: 'library',
        destination: { state: 'existing', name: 'My Music', readiness: 'review' }
      }
    }
  };
  const requests = [];
  const f = fixture(async (url, body) => {
    requests.push([url, body]);
    return reply;
  });
  await f.controller.select('chip', 'downloads');
  const view = structureCardView(f.controller.state);
  assert.equal(view.kind, 'proposal');
  assert.equal(view.actions[0].label, 'Review library setup');
  assert.equal(view.actions[0].disabled, true);
  assert.equal(f.controller.state.structureUnavailable, 'review_pending');
  assert.equal(requests.length, 1);
  assert.match(requests[0][0], /folder-context\/select$/);
  reply = { ...reply, observation: observation('other'), structure_unavailable: '' };
  await f.controller.select('chip', 'downloads');
  assert.equal(structureCardView(f.controller.state).actions[0].disabled, false);
});

test('the proposal card stays when the turn sent with it gets no answer', async () => {
  const structure = {
    version: 1,
    folder: 'Albums',
    group: { name: 'Albums', type: 'group', destination_state: 'new' },
    members: [{ candidate_id: 'candidate-1', name: 'Album-1', included: true }]
  };
  const f = fixture(async () => ({ observation: observation('a'), revision: '', structure }));
  await f.controller.select('chip', 'downloads');
  // Send asks for the references; an unanswered turn saves nothing, so the
  // folder is never accepted into a conversation and the preview stays.
  assert.ok(f.controller.request());
  assert.equal(structureCardView(f.controller.state).kind, 'proposal');
  assert.equal(structureCardView(f.controller.state).actions[0].label, 'Review group setup');
  // Retry asks again with the same references.
  assert.deepEqual(f.controller.request(), f.controller.request());
});

test('Ask again sends only the decline key and gives the card its Set up back', async () => {
  const structure = {
    version: 1,
    folder: 'Albums',
    group: { name: 'Albums', type: 'group', destination_state: 'new' },
    members: [{ candidate_id: 'candidate-1', name: 'Album-1', included: true }]
  };
  const declined = { folder_key: 'b'.repeat(64), name: 'Albums', at: '2026-10-06T08:02:00Z' };
  let forget = async () => ({ folder_digest: {} });
  const f = fixture(async (url, body) =>
    url.endsWith('/select')
      ? { observation: observation('a'), revision: '', structure, declined }
      : forget(url, body)
  );
  await f.controller.select('chip', 'downloads');
  assert.equal(await f.controller.askAgain(), true);
  assert.equal(f.calls.length, 2);
  assert.equal(f.calls[1].url, '/api/personal-assistant/folder-digest/forget');
  assert.deepEqual(f.calls[1].body, { folder_key: declined.folder_key });
  assert.equal(f.controller.state.declined, null);
  // The folder was not looked at again: same observation, same proposal.
  assert.equal(f.controller.state.observation.id, 'a');
  assert.equal(f.controller.state.structure, structure);
  assert.equal(structureCardView(f.controller.state).actions[0].label, 'Review group setup');
  // Nothing to ask again about: no request.
  assert.equal(await f.controller.askAgain(), false);
  assert.equal(f.calls.length, 2);

  // A failed Ask again keeps the chip and says so.
  await f.controller.select('chip', 'downloads');
  forget = async () => {
    throw new Error('That folder could not be asked about again.');
  };
  assert.equal(await f.controller.askAgain(), false);
  assert.deepEqual(f.controller.state.declined, declined);
  assert.equal(f.controller.state.notice, 'That folder could not be asked about again.');
  assert.equal(f.controller.state.pending, false);
});

test('an empty Send asks for the proposal on a collection and explores any other folder', () => {
  assert.deepEqual(emptySendView('Albums', true), {
    placeholder: 'Explain setup for Albums',
    text: 'Explain the setup recommendation for this folder'
  });
  assert.deepEqual(emptySendView('Thesis', false), {
    placeholder: 'Explore this folder',
    text: 'Explore this folder'
  });
  assert.equal(emptySendView('', true), null);
});

test('a proposed group replaces the discussion shortcuts with its own three controls', () => {
  assert.deepEqual(
    discussionChips({ structure: true, children: 6, markers: true }).map(chip => chip.label),
    ['Set up', 'Adjust…', 'Just discuss']
  );
  // Without a proposal the two shortcuts are what they were.
  assert.deepEqual(
    discussionChips({ children: 6, markers: true }).map(chip => chip.label),
    ['Discuss the collection', 'Choose a project…']
  );
  assert.deepEqual(
    discussionChips({ children: 1 }).map(chip => chip.label),
    ['Discuss this folder', 'Choose a folder…']
  );
  assert.deepEqual(
    discussionChips({}).map(chip => chip.label),
    ['Discuss this folder']
  );
  // A saved snapshot has no live proposal to set up.
  assert.deepEqual(
    discussionChips({ structure: true, historical: true, children: 2 }).map(chip => chip.label),
    ['Discuss saved observations', 'Choose a folder…']
  );
});

test('late review cannot bind a new conversation and historical context cannot request setup', async () => {
  const pending = deferred();
  const f = fixture(() => pending.promise);
  f.controller.reset('', { observation: observation('a') });
  const review = f.controller.review('root');
  f.setId('other');
  f.controller.reset('other');
  pending.resolve({ conversation: { id: 'wrong', stored: true } });
  assert.equal(await review, null);
  assert.equal(f.controller.state.observation, null);
  f.controller.reset('other', {
    observation: observation('old'),
    revision: 'r1',
    authority: 'lost'
  });
  assert.equal(await f.controller.review('root'), null);
  assert.equal(f.calls.length, 1);
});

test('return restores an unsaved selection and rechecks references without scanning or extending authority', async () => {
  const { controller, calls, setId } = fixture(async (url, body) =>
    url.endsWith('/recommendation')
      ? { observation_id: body.selection_id, structure: { version: 1, folder: 'Albums' } }
      : {
          observation: {
            ...observation('a'),
            tree: { nodes: [{ id: 'entry-0', name: 'Album-5', kind: 'folder' }] }
          },
          structure: { version: 1 },
          blocker: { subject: 'Album-5' },
          structure_unavailable: 'review_pending'
        }
  );
  await controller.select('chip', 'music');
  assert.equal(controller.setFocus(['entry-0']), true);
  const saved = controller.snapshot();
  setId('album-5');
  controller.reset('album-5');
  setId('');
  controller.reset();
  assert.equal(await controller.restore(saved), true);
  assert.equal(controller.state.draftId, saved.draftId);
  assert.deepEqual(controller.state.focusIDs, ['entry-0']);
  assert.equal(controller.state.observation.id, 'a');
  assert.equal(controller.state.blocker, null);
  assert.equal(controller.state.authority, '');
  assert.deepEqual(
    calls.map(c => c.url),
    [
      '/api/home-assistant/folder-context/select',
      '/api/home-assistant/folder-context/recommendation'
    ]
  );
  assert.deepEqual(calls[1].body, { draft_id: saved.draftId, revision: '', selection_id: 'a' });
});

test('expired return keeps the observation but refuses execution; a late refresh cannot change a replacement', async () => {
  let pending = deferred();
  const f = fixture(async (url, body) =>
    url.endsWith('/recommendation') ? pending.promise : { observation: observation(body.chip) }
  );
  await f.controller.select('chip', 'old');
  const saved = f.controller.snapshot();
  const returning = f.controller.restore(saved);
  pending.reject(new Error('expired'));
  assert.equal(await returning, false);
  assert.equal(f.controller.state.observation.id, 'old');
  assert.equal(f.controller.state.authority, 'expired');
  assert.match(f.controller.state.notice, /draft is kept/);
  assert.equal(await f.controller.review('root'), null);
  pending = deferred();
  const refreshing = f.controller.refreshRecommendation();
  await f.controller.select('chip', 'new');
  pending.resolve({ observation_id: 'old', blocker: { subject: 'Old' } });
  assert.equal(await refreshing, false);
  assert.equal(f.controller.state.observation.id, 'new');
  assert.equal(f.controller.state.blocker, null);
  f.setId('foreign');
  assert.equal(await f.controller.restore(saved), false);
});
