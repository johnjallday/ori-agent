import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createFolderContextController,
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
  f.controller.accepted('saved', { revision: 'r2', observation: observation('a'), historical: true });
  assert.equal(f.controller.state.authority, 'lost');
  assert.deepEqual(f.controller.request(), { selection_id: 'a', revision: 'r2', historical: true });
  f.setBusy(true);
  assert.equal(await f.controller.select('picker'), false);
  assert.equal(await f.controller.remove(), false);
  assert.equal(f.calls.length, 0);
});
