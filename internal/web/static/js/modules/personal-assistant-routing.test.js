import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

// Execute the actual dashboard entrypoint, not a second local intent detector.
// Rendering is absent here; browser acceptance covers the mounted drawer.
const source = await readFile(new URL('./dashboard.js', import.meta.url), 'utf8');

function harness(route, options = {}) {
  const calls = [];
  const notices = [];
  const window = {
    location: { pathname: '/' },
    PersonalAssistantFolderContext: options.folderRef
      ? {
          request: () => options.folderRef
        }
      : null,
    PersonalAssistantConversation: {
      request: () => ({ id: 'canonical-thread' }),
      currentId: () => (options.changedThread ? 'another-thread' : 'canonical-thread'),
      notify: notice => notices.push(notice),
      applyReply: () => ({ stored: true })
    }
  };
  const context = vm.createContext({
    window,
    document: {
      addEventListener() {},
      getElementById: () => null,
      querySelector: () => null,
      querySelectorAll: () => []
    },
    console: { debug() {}, warn() {}, error() {} },
    setTimeout,
    clearTimeout,
    URL,
    API: {
      post: async (path, payload) => {
        calls.push({ path, payload });
        if (path === '/api/home-assistant/route') {
          if (options.refused) throw new Error('conversation_out_of_scope');
          return route;
        }
        if (path === '/api/home-assistant/ask') {
          return {
            response: 'Fixture discussion only.',
            intent: payload.intent,
            conversation: { id: 'canonical-thread', stored: true }
          };
        }
        if (
          options.utility &&
          (path === '/api/chat' || path === '/api/home-assistant/intake-trace')
        )
          return { response: 'Fixture utility reply.' };
        throw new Error(`Unexpected mutation/dispatch ${path}`);
      }
    }
  });
  vm.runInContext(source, context, { filename: 'dashboard.js' });
  return { window, calls, notices };
}

for (const refs of [
  { surface: 'home', page_path: '/' },
  { surface: 'home', page_path: '/', selection_workspace_id: 'home-selected' },
  { surface: 'workspace', page_path: '/workspaces/community', workspace_id: 'project' },
  { surface: 'workspace_canvas', page_path: '/workspaces/portfolio/canvas', workspace_id: 'group' }
]) {
  test(`canonical conversation wins over local build/agent heuristics on ${JSON.stringify(refs)}`, async () => {
    const h = harness({ route_mode: 'home_inline', intent: 'assistant_conversation' });
    await h.window.OriAskRouting.submit(
      'Should we build a community platform with an API and a database?',
      {
        openThinkingModal: false,
        routeContext: { ...refs, origin: 'personal_assistant_panel' }
      }
    );
    assert.deepEqual(
      h.calls.map(call => call.path),
      ['/api/home-assistant/route', '/api/home-assistant/ask']
    );
    assert.equal(h.calls[0].payload.conversation.id, 'canonical-thread');
    assert.equal(h.calls[1].payload.conversation.id, 'canonical-thread');
    assert.equal(h.calls[1].payload.intent, 'assistant_conversation');
  });
}

test('folder context keeps its validated conversation path without local build setup', async () => {
  const folderRef = { selection_id: 'host-observation', revision: 'host-revision' };
  const h = harness({ route_mode: 'home_inline', intent: 'assistant_conversation' }, { folderRef });
  await h.window.OriAskRouting.submit(
    'Should we build a platform for the material in this folder?',
    {
      openThinkingModal: false,
      routeContext: { origin: 'personal_assistant_panel' }
    }
  );
  assert.deepEqual(
    h.calls.map(call => call.path),
    ['/api/home-assistant/route', '/api/home-assistant/ask']
  );
  assert.equal(h.calls[0].payload.folder_context, folderRef);
  assert.equal(h.calls[1].payload.folder_context, folderRef);
});

test('utility consumes the accepted route once, without a workspace-manager shortcut', async () => {
  const h = harness(
    {
      route_mode: 'utility_direct',
      intent: 'utility_direct',
      matched_agent: 'Ori',
      score: 4,
      requires_creation: false,
      routing_policy: 'assistant_only'
    },
    { utility: true }
  );
  await h.window.OriAskRouting.submit('What time is it in Seoul?', {
    openThinkingModal: false,
    routeContext: {
      origin: 'personal_assistant_panel',
      surface: 'workspace',
      workspace_id: 'project',
      page_path: '/workspaces/project'
    }
  });
  assert.equal(h.calls.filter(call => call.path === '/api/home-assistant/route').length, 1);
  assert.ok(h.calls.some(call => call.path === '/api/chat'));
  assert.ok(
    h.calls.every(call =>
      ['/api/home-assistant/route', '/api/chat', '/api/home-assistant/intake-trace'].includes(
        call.path
      )
    )
  );
});

test('explicit workspace creation uses Ask review rather than local create-by-name', async () => {
  const h = harness({ route_mode: 'workspace_task', intent: 'workspace_create' });
  await h.window.OriAskRouting.submit('Create a workspace called Community', {
    openThinkingModal: false,
    routeContext: { origin: 'personal_assistant_panel', page_path: '/' }
  });
  assert.deepEqual(
    h.calls.map(call => call.path),
    ['/api/home-assistant/route', '/api/home-assistant/ask']
  );
  assert.equal(h.calls[1].payload.intent, 'workspace_create');
  assert.equal(h.calls[1].payload.confirmed_action, undefined);
});

for (const options of [{ refused: true }, { changedThread: true }]) {
  test(`refused/stale routing never falls back to local execution: ${JSON.stringify(options)}`, async () => {
    const h = harness({ route_mode: 'home_inline', intent: 'assistant_conversation' }, options);
    await h.window.OriAskRouting.submit('Build a community platform', {
      openThinkingModal: false,
      routeContext: { origin: 'personal_assistant_panel', page_path: '/' }
    });
    assert.deepEqual(
      h.calls.map(call => call.path),
      ['/api/home-assistant/route']
    );
    assert.ok(h.notices.length);
  });
}
