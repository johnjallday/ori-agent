import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./dashboard.js', import.meta.url), 'utf8');
const summary = vm.runInNewContext(
  `(${source.slice(source.indexOf('function formatHomeAskSummary('), source.indexOf('async function createWorkspaceChatSessionWithMessage(')).trim()})`
);
for (const failure of [
  'invalid_workspace_proposal',
  'model_timeout',
  'request_cancelled',
  'model_not_configured',
  'context_changed',
  'provider_unavailable'
]) {
  test(`an unanswered turn never claims app-data success: ${failure}`, () => {
    const text = summary({ model_unavailable: true, failure_reason: failure });
    assert.ok(text);
    assert.doesNotMatch(text, /Answered|From \d|Reply ready/);
  });
}
test('unsaved context does not become a success summary', () => {
  assert.match(summary({ conversation: { error: 'context_save_failed' } }), /could not be saved/);
});

test('manual recovery opens only the blank manual creator, regardless of model or stale proposal', () => {
  const calls = [];
  const menu = { open: true };
  const window = {
    PersonalAssistantPanel: { close: () => calls.push('close') },
    sessionManager: { showAddWorkspaceModal: value => calls.push(value) }
  };
  const start = source.indexOf('function openManualWorkspaceReview(');
  const end = source.indexOf('function manualWorkspaceReviewButton(', start);
  const open = vm.runInNewContext(`(${source.slice(start, end).trim()})`, {
    window,
    document: { getElementById: () => menu }
  });
  open({
    name: 'forged',
    description: 'stale',
    buildFirstMessage: 'run this',
    parentId: 'foreign'
  });
  assert.equal(menu.open, false);
  assert.equal(calls[0], 'close');
  assert.deepEqual(JSON.parse(JSON.stringify(calls[1])), {
    entryPoint: 'assistant_workspace_manual',
    stayAfterCreate: true
  });
});
