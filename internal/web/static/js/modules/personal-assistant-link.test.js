import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  LINK_COPY,
  createLinkController,
  linkDisplayName,
  linkSummary,
  startLinkBuild
} from './personal-assistant-link.js';

const repo = {
  id: 'obs-1',
  kind: 'github_repo',
  host: 'github.com',
  title: 'golang/go',
  url: 'https://github.com/golang/go',
  readable: true,
  repo: { full_name: 'golang/go', language: 'Go', open_issues: 10354, reachable: 'yes' }
};

function fakePost(answers) {
  const calls = [];
  const post = async (url, body) => {
    calls.push({ url, body });
    const answer = answers.shift();
    if (answer instanceof Error) throw answer;
    return answer || { ok: true, body: {} };
  };
  return { post, calls };
}

test('the summary and name say what the link is', () => {
  assert.equal(linkSummary(repo), 'GitHub repository · Go · 10,354 open issues');
  assert.equal(
    linkSummary({ ...repo, repo: { ...repo.repo, reachable: 'unknown' } }),
    'GitHub repository · not verified'
  );
  assert.equal(
    linkSummary({ kind: 'page', host: 'arxiv.org', readable: true }),
    'Page · arxiv.org'
  );
  assert.equal(
    linkSummary({ kind: 'document', host: 'arxiv.org', readable: false }),
    'Document · arxiv.org · not read'
  );
  assert.equal(
    linkSummary({ kind: 'page', host: 'x.example', readable: false }),
    'Could only read the address · x.example'
  );
  assert.equal(linkDisplayName({ host: 'x.example' }), 'x.example');
  assert.equal(linkDisplayName({ repo: { full_name: 'a/b' }, host: 'github.com' }), 'a/b');
  assert.equal(linkSummary(null), '');
});

test('a link is attached once, sent only from its conversation, and adopted by the conversation it starts', async () => {
  let current = '';
  const changes = [];
  const { post, calls } = fakePost([{ ok: true, body: { link_id: 'obs-1', observation: repo } }]);
  const controller = createLinkController({
    post,
    changed: state => changes.push({ ...state }),
    currentId: () => current
  });
  assert.equal(await controller.select('   '), false);
  assert.equal(controller.state.notice, LINK_COPY.empty);
  assert.equal(calls.length, 0);

  assert.equal(await controller.select(' https://github.com/golang/go '), true);
  assert.deepEqual(calls[0], {
    url: '/api/home-assistant/link-context/select',
    body: { url: 'https://github.com/golang/go' }
  });
  assert.ok(changes.some(state => state.pending && state.notice === LINK_COPY.checking));
  assert.deepEqual(controller.request(), { link_id: 'obs-1' });
  assert.equal(controller.shown(), true);

  // The new draft's first stored turn gives it an id; the link goes with it.
  current = 'conv-1';
  assert.equal(controller.request(), null, 'not before the conversation adopts it');
  controller.adopt('conv-1');
  assert.deepEqual(controller.request(), { link_id: 'obs-1' });

  // On another conversation it is neither shown nor sent.
  current = 'conv-2';
  assert.equal(controller.request(), null);
  assert.equal(controller.shown(), false);
  controller.adopt('conv-2');
  assert.equal(controller.request(), null, 'an adopted link never moves again');
  current = 'conv-1';
  assert.deepEqual(controller.request(), { link_id: 'obs-1' });
});

test('replacing names the old link, a refusal keeps the old one, and remove lets it go', async () => {
  const { post, calls } = fakePost([
    { ok: true, body: { link_id: 'obs-1', observation: repo } },
    { ok: false, status: 422, body: { error: 'That address can’t be used here.', code: 'link' } },
    { ok: true, body: { link_id: 'obs-2', observation: { ...repo, id: 'obs-2' } } },
    { ok: true, body: { detached: true } }
  ]);
  const controller = createLinkController({ post, currentId: () => '' });
  await controller.select('https://github.com/golang/go');
  assert.equal(await controller.select('http://127.0.0.1/'), false);
  assert.equal(controller.state.notice, 'That address can’t be used here.');
  assert.equal(controller.state.failed, true);
  assert.deepEqual(controller.request(), { link_id: 'obs-1' });
  assert.equal(calls[1].body.replaces, 'obs-1');

  assert.equal(await controller.select('https://github.com/golang/tools'), true);
  assert.equal(calls[2].body.replaces, 'obs-1');
  assert.deepEqual(controller.request(), { link_id: 'obs-2' });

  assert.equal(await controller.remove(), true);
  assert.deepEqual(calls[3], {
    url: '/api/home-assistant/link-context/detach',
    body: { link_id: 'obs-2' }
  });
  assert.equal(controller.request(), null);
});

test('a result that arrives after the conversation changed is dropped, and lost or reset clears it', async () => {
  let current = 'conv-1';
  let release;
  const post = url =>
    url.endsWith('/select')
      ? new Promise(resolve => {
          release = () => resolve({ ok: true, body: { link_id: 'obs-1', observation: repo } });
        })
      : Promise.resolve({ ok: true, body: {} });
  const controller = createLinkController({ post, currentId: () => current });
  const pending = controller.select('https://github.com/golang/go');
  assert.equal(controller.request(), null, 'nothing is sent while it is checked');
  current = 'conv-2';
  release();
  assert.equal(await pending, false);
  assert.equal(controller.state.link, null);

  current = 'conv-1';
  const again = createLinkController({
    post: async () => ({ ok: true, body: { link_id: 'obs-1', observation: repo } }),
    currentId: () => current
  });
  await again.select('https://github.com/golang/go');
  again.lost();
  assert.equal(again.request(), null);
  assert.equal(again.state.notice, LINK_COPY.lost);
  await again.select('https://github.com/golang/go');
  again.reset();
  assert.equal(again.request(), null);
  assert.equal(again.state.notice, '');
});

test('Set up a workspace from this link starts a build, attaches the link, then opens it', async () => {
  const requests = [];
  let resumed = 0;
  const notes = [];
  const request = async (method, url, body) => {
    requests.push({ method, url, body });
    if (url === '/api/workspaces/build-sessions')
      return { ok: true, body: { session: { id: 'build 1', version: 4 }, resumed: true } };
    return { ok: true, body: { session: { id: 'build 1', version: 5 } } };
  };
  const ok = await startLinkBuild(' https://arxiv.org/abs/1706.03762 ', {
    request,
    resume: () => resumed++,
    notify: message => notes.push(message)
  });
  assert.equal(ok, true);
  assert.deepEqual(requests, [
    {
      method: 'POST',
      url: '/api/workspaces/build-sessions',
      body: { entry_point: 'personal_assistant_ask' }
    },
    {
      method: 'POST',
      url: '/api/workspaces/build-sessions/build%201/link',
      body: { url: 'https://arxiv.org/abs/1706.03762', version: 4 }
    }
  ]);
  assert.equal(resumed, 1);
  assert.deepEqual(notes, []);
});

test('a build that cannot start or take the link opens nothing and says why', async () => {
  let resumed = 0;
  const notes = [];
  const unavailable = await startLinkBuild('https://example.com', {
    request: async () => ({ ok: false, status: 409, body: { code: 'unavailable' } }),
    resume: () => resumed++,
    notify: message => notes.push(message)
  });
  assert.equal(unavailable, false);
  assert.equal(notes.at(-1), LINK_COPY.buildUnavailable);

  const refused = await startLinkBuild('https://example.com', {
    request: async (method, url) =>
      url.endsWith('/link')
        ? { ok: false, status: 422, body: { error: 'That link could not be used.' } }
        : { ok: true, body: { session: { id: 'b', version: 1 } } },
    resume: () => resumed++,
    notify: message => notes.push(message)
  });
  assert.equal(refused, false);
  assert.equal(notes.at(-1), 'That link could not be used.');
  assert.equal(resumed, 0);
  assert.equal(
    await startLinkBuild('  ', {
      request: async () => {
        throw new Error('no request for an empty address');
      },
      resume: () => resumed++,
      notify: () => {}
    }),
    false
  );
});
