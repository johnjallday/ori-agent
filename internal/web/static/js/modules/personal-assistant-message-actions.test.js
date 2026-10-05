import test from 'node:test';
import assert from 'node:assert/strict';
import { messageActions, messageReviewTrigger } from './personal-assistant-message-actions.js';

test('unsaved or missing messages never receive action controls', () => {
  assert.equal(messageActions(null), null);
  assert.equal(messageActions({ dataset: {}, firstElementChild: {} }), null);
  assert.equal(messageActions({ dataset: { messageId: 'm' } }), null);
});

test('redecorating a canonical message reuses its one disclosure', () => {
  const items = {};
  const row = {
    dataset: { messageId: 'm', conversationId: 'c' },
    firstElementChild: {
      querySelector(selector) {
        assert.equal(selector, '.personal-assistant-message__menu-items');
        return items;
      }
    }
  };
  assert.equal(messageActions(row), items);
  assert.equal(messageActions(row), items);
});

test('a review closes the disclosure and returns to its visible summary', () => {
  const summary = {};
  const menu = {
    open: true,
    querySelector: selector => (selector === 'summary' ? summary : null)
  };
  const button = { closest: () => menu };
  assert.equal(messageReviewTrigger(button), summary);
  assert.equal(menu.open, false);
});

test('typed reviews retain their original composer trigger', () => {
  const input = { closest: () => null };
  assert.equal(messageReviewTrigger(input), input);
  assert.equal(messageReviewTrigger(null), null);
});
