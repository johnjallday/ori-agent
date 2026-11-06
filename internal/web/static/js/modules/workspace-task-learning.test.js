import { test } from 'node:test';
import assert from 'node:assert/strict';

globalThis.window = globalThis.window || {};
const { WorkspaceTaskPage } = await import('./workspace-task.js');

test('learning draft hero Run hands off to explicit review without invoking execution', async () => {
  const page = new WorkspaceTaskPage('workspace', 'ticket', 'workshop');
  page.task = { status: 'pending', context: { folder_first_task_recipe: 'learning_draft' } };
  const notices = [];
  page.notify = (...args) => notices.push(args);
  const previousDocument = globalThis.document;
  const previousFetch = globalThis.fetch;
  let focused = false,
    scrolled = false;
  globalThis.document = {
    getElementById: id => {
      assert.equal(id, 'learningTicketReview');
      return {
        hidden: false,
        scrollIntoView: () => {
          scrolled = true;
        },
        querySelector: () => ({
          focus: () => {
            focused = true;
          }
        })
      };
    }
  };
  globalThis.fetch = () => {
    throw new Error('Hero must not execute before review');
  };
  try {
    await page.executeTask();
  } finally {
    globalThis.document = previousDocument;
    globalThis.fetch = previousFetch;
  }
  assert.ok(focused && scrolled);
  assert.equal(notices[0][0], 'info');
});
