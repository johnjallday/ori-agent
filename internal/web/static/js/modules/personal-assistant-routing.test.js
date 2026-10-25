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
  const restored = [];
  const window = {
    location: { pathname: '/' },
    PersonalAssistantPanel: { restoreDraft: text => restored.push(text) },
    PersonalAssistantFolderContext: options.folderRef
      ? {
          request: () => options.folderRef
        }
      : null,
    PersonalAssistantConversation: {
      request: () => ({ id: 'canonical-thread' }),
      currentId: () => (options.changedThread ? 'another-thread' : 'canonical-thread'),
      notify: notice => notices.push(notice),
      applyReply: () => ({
        stored: !options.unavailable,
        restoreInput: Boolean(options.unavailable)
      })
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
            model_unavailable: Boolean(options.unavailable),
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
  return { window, calls, notices, restored };
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

// The recipient picker: a drawer turn addressed to the workspace's entry agent
// goes to it as typed, through a workspace chat session and /api/chat.
function entryHarness(options = {}) {
  const calls = [];
  const remembered = [];
  const restored = [];
  const window = {
    location: { pathname: '/workspaces/test-reaper' },
    PersonalAssistantConversation: {
      request: () => {
        throw new Error('the personal conversation must not be consulted');
      },
      currentId: () => 'canonical-thread',
      notify() {}
    },
    PersonalAssistantFolderContext: { request: () => ({ selection_id: 'stale-folder' }) },
    PersonalAssistantRecipient: {
      rememberSession: (workspaceId, session) => remembered.push({ workspaceId, id: session.id })
    },
    PersonalAssistantPanel: { restoreDraft: text => restored.push(text) }
  };
  const context = vm.createContext({
    window,
    document: {
      addEventListener() {},
      getElementById: () => null,
      querySelector: () => null,
      querySelectorAll: () => []
    },
    localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
    console: { debug() {}, warn() {}, error() {} },
    setTimeout,
    clearTimeout,
    URL,
    API: {
      post: async (path, payload, config) => {
        calls.push({ path, payload, config });
        if (path === '/api/sessions') return { success: true, session: { id: 'sess-new' } };
        if (path === '/api/chat') {
          if (options.chatError) throw options.chatError;
          return { response: 'Reverb bus added.' };
        }
        if (path === '/api/workspaces/ws-1/notes') return { success: true };
        throw new Error(`Unexpected request ${path}`);
      }
    }
  });
  vm.runInContext(source, context, { filename: 'dashboard.js' });
  return { window, calls, remembered, restored };
}

const entryRecipient = {
  kind: 'workspace_entry',
  name: 'REAPER Assistant',
  workspaceId: 'ws-1',
  workspaceName: 'test_reaper'
};
const workspacePanelContext = {
  context_version: 1,
  origin: 'personal_assistant_panel',
  surface: 'workspace_detail',
  page_path: '/workspaces/test-reaper',
  workspace_id: 'ws-1',
  workspace_slug: 'test-reaper'
};

test('an entry-agent turn creates its session and chats, never touching personal routing', async () => {
  const h = entryHarness();
  await h.window.OriAskRouting.submit('Create a workspace called Community', {
    openThinkingModal: false,
    routeContext: workspacePanelContext,
    recipient: entryRecipient
  });
  assert.deepEqual(
    h.calls.map(call => call.path),
    ['/api/sessions', '/api/chat']
  );
  assert.equal(h.calls[0].payload.folder_id, 'ws-1');
  assert.equal(h.calls[0].payload.agent_name, 'REAPER Assistant');
  const chat = h.calls[1];
  // As typed: no local create-by-name or dispatch rewrite.
  assert.equal(chat.payload.question, 'Create a workspace called Community');
  assert.equal(chat.payload.agent_name, 'REAPER Assistant');
  assert.equal(chat.payload.conversation, undefined);
  assert.equal(chat.payload.route_context.workspace_id, 'ws-1');
  assert.equal(chat.config.headers['X-Session-ID'], 'sess-new');
  assert.deepEqual(h.remembered, [{ workspaceId: 'ws-1', id: 'sess-new' }]);
  assert.deepEqual(h.restored, []);
});

test('a follow-up reuses the current entry-agent session', async () => {
  const h = entryHarness();
  await h.window.OriAskRouting.submit('Route the vocal to it', {
    openThinkingModal: false,
    routeContext: workspacePanelContext,
    recipient: { ...entryRecipient, sessionId: 'sess-7' }
  });
  assert.deepEqual(
    h.calls.map(call => call.path),
    ['/api/chat']
  );
  assert.equal(h.calls[0].config.headers['X-Session-ID'], 'sess-7');
  assert.deepEqual(h.remembered, []);
});

test('/ask sends its text as a question and /note saves a workspace note', async () => {
  const ask = entryHarness();
  await ask.window.OriAskRouting.submit('/ask what tracks are armed?', {
    openThinkingModal: false,
    routeContext: workspacePanelContext,
    recipient: { ...entryRecipient, sessionId: 'sess-7' }
  });
  assert.deepEqual(
    ask.calls.map(call => call.path),
    ['/api/chat']
  );
  assert.equal(ask.calls[0].payload.question, 'what tracks are armed?');

  const note = entryHarness();
  await note.window.OriAskRouting.submit('/note mix at -14 LUFS', {
    openThinkingModal: false,
    routeContext: workspacePanelContext,
    recipient: entryRecipient
  });
  assert.deepEqual(
    note.calls.map(call => call.path),
    ['/api/workspaces/ws-1/notes']
  );
  assert.equal(note.calls[0].payload.content, 'mix at -14 LUFS');
});

test('a failed entry-agent turn keeps the message; a timed-out one does not resend it', async () => {
  const failed = entryHarness({ chatError: Object.assign(new Error('boom'), { status: 500 }) });
  await failed.window.OriAskRouting.submit('Add a reverb bus', {
    openThinkingModal: false,
    routeContext: workspacePanelContext,
    recipient: entryRecipient
  });
  assert.deepEqual(failed.restored, ['Add a reverb bus']);

  const delayed = entryHarness({ chatError: new Error('Request cancelled') });
  await delayed.window.OriAskRouting.submit('Add a reverb bus', {
    openThinkingModal: false,
    routeContext: workspacePanelContext,
    recipient: entryRecipient
  });
  assert.deepEqual(delayed.restored, []);
  // The delay is said honestly, under the entry agent's name.
  const summary = delayed.window.OriAskRouting.getState().routingSummary;
  assert.equal(summary.title, 'REAPER Assistant Delayed');
  assert.equal(summary.state, 'timeout');
  assert.match(summary.text, /reply is saved in this conversation/);
});

test('a recipient outside the drawer, or the Personal Assistant, keeps personal routing', async () => {
  const h = harness({ route_mode: 'home_inline', intent: 'assistant_conversation' });
  await h.window.OriAskRouting.submit('What did I leave unfinished?', {
    openThinkingModal: false,
    routeContext: { ...workspacePanelContext },
    recipient: { kind: 'personal', name: 'Mina' }
  });
  assert.deepEqual(
    h.calls.map(call => call.path),
    ['/api/home-assistant/route', '/api/home-assistant/ask']
  );
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

test('a failed folder turn restores exact composer whitespace without sending extra draft data', async () => {
  const draft = '  Explain this choice. 🎼\n  ';
  for (const refused of [false, true]) {
    const h = harness(
      { route_mode: 'home_inline', intent: 'assistant_conversation' },
      {
        folderRef: { selection_id: 'held-folder' },
        unavailable: true,
        refused
      }
    );
    await h.window.OriAskRouting.submit(draft.trim(), {
      composerDraft: draft,
      openThinkingModal: false,
      routeContext: { origin: 'personal_assistant_panel' }
    });
    assert.deepEqual(h.restored, [draft]);
    for (const call of h.calls) {
      assert.equal(call.payload.prompt, draft.trim());
      assert.equal(call.payload.composerDraft, undefined);
      assert.equal(call.payload.restoreText, undefined);
    }
  }
});
