import assert from 'node:assert/strict';
import test from 'node:test';
import {
  NEEDS_YOU,
  renderTodayAnswer,
  todayAnswerDestination
} from './personal-assistant-today-answer.js';

function dom(tag = 'div') {
  return {
    tag,
    ownerDocument: { createElement: dom },
    children: [],
    append(child) {
      this.children.push(child);
    },
    setAttribute() {}
  };
}

const reply = () => ({
  conversation: { id: 'current', stored: true },
  today: {
    truncated: true,
    rows: [
      { title: 'Reply', owner: 'HQ', kind: 'Follow-up', href: '/workspaces/hq?follow_up=1' },
      {
        title: 'Reply',
        owner: 'Email Ops',
        kind: 'Follow-up',
        href: '/workspaces/email-ops?follow_up=2'
      },
      { title: '<script>record</script>', kind: 'Report', href: '//example.com' }
    ]
  }
});

test('the saved live answer renders canonical links in order and falls back to Needs you', () => {
  const row = dom();
  const card = renderTodayAnswer(row, reply(), { isCurrent: () => true });
  const [list, more, home] = card.children;
  assert.deepEqual(
    list.children.map(li => li.children[0].href),
    ['/workspaces/hq?follow_up=1', '/workspaces/email-ops?follow_up=2', undefined]
  );
  assert.deepEqual(
    list.children.map(li => li.children[1].textContent),
    ['Follow-up · HQ', 'Follow-up · Email Ops', 'Report']
  );
  assert.equal(list.children[2].children[0].textContent, '<script>record</script>');
  assert.match(more.textContent, /first 8 records/);
  assert.equal(home.href, NEEDS_YOU);
});

test('a failed save, recipient switch or history never mounts the live card', () => {
  const row = dom();
  assert.equal(renderTodayAnswer(row, reply(), { isCurrent: () => false }), null);
  assert.equal(renderTodayAnswer(row, { today: reply().today }, { isCurrent: () => true }), null);
  assert.equal(
    renderTodayAnswer(row, { conversation: reply().conversation }, { isCurrent: () => true }),
    null
  );
  assert.deepEqual(row.children, []);
});

test('record links do not accept external, malformed, or invented controls', () => {
  for (const path of [
    '//evil.test',
    '/workspaces/hq',
    '/workspaces/hq?follow_up=1&ticket=2',
    '/workspaces/hq?ticket=%2F',
    '/workspaces/hq?ticket=%',
    '/workspaces/../?ticket=1'
  ]) {
    assert.equal(todayAnswerDestination(path), '', path);
  }
  assert.equal(
    todayAnswerDestination('/workspaces/hq?ticket=source%3Aid'),
    '/workspaces/hq?ticket=source%3Aid'
  );
});
