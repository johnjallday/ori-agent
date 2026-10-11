import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

// Exercise the actual data-only opener; the built browser case also crosses
// the real creator reset/prefill and final Create boundaries.
const source = await readFile(new URL('./dashboard.js', import.meta.url), 'utf8');
const start = source.indexOf('function openPreparedWorkspaceReview(');
const end = source.indexOf('// confirmHomeAction shows', start);
const opener = source.slice(start, end).trim();

for (const changed of [
  '',
  'thread',
  'page',
  'workspace',
  'selection',
  'relationship',
  'paused-version',
  'folder'
]) {
  test(`workspace proposal opens only in its original current context: ${changed || 'unchanged'}`, () => {
    const calls = [];
    const notices = [];
    const window = {
      location: { pathname: changed === 'page' ? '/settings' : '/' },
      PersonalAssistantConversation: {
        currentId: () => (changed === 'thread' ? 'another-thread' : 'canonical'),
        notify: text => notices.push(text)
      },
      PersonalAssistantPanel: {
        _state: {
          personalAssistant: {
            state: changed === 'relationship' ? 'needs_hq' : 'active',
            state_version: changed === 'paused-version' ? 8 : 7
          }
        },
        close: () => calls.push('close')
      },
      PersonalAssistantFolderContext: {
        request: () => (changed === 'folder' ? { selection_id: 'different' } : null)
      },
      sessionManager: { showAddWorkspaceModal: seed => calls.push(seed) }
    };
    const open = vm.runInNewContext(`(${opener})`, {
      window,
      buildHomeRouteContext: () => ({
        workspace_id: changed === 'workspace' ? 'other' : '',
        selection_workspace_id: changed === 'selection' ? 'selected' : ''
      }),
      normalizeHomeRouteContext: value => value
    });
    open(
      {
        conversation_id: 'canonical',
        state_version: 7,
        name: 'Membership',
        description: 'A pilot, not talent coaching.'
      },
      { page_path: '/', workspace_id: '', selection_workspace_id: '' }
    );
    if (changed) {
      assert.equal(calls.length, 0);
      assert.equal(notices.length, 1);
    } else {
      assert.equal(calls[0], 'close');
      const seed = calls[1];
      assert.equal(seed.entryPoint, 'assistant_workspace_review');
      assert.equal(seed.name, 'Membership');
      assert.equal(seed.description, 'A pilot, not talent coaching.');
      assert.equal(seed.stayAfterCreate, true);
      for (const forbidden of [
        'parentId',
        'blueprint',
        'folderOfferId',
        'buildFirstMessage',
        'teamLock',
        'stageBlueprintRoles'
      ])
        assert.equal(seed[forbidden], undefined);
    }
  });
}
