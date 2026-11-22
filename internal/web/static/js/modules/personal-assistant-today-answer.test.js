import assert from 'node:assert/strict';
import test from 'node:test';
import {
  NEEDS_YOU,
  renderTodayAnswer,
  renderSnoozeCard,
  todayAnswerDestination
} from './personal-assistant-today-answer.js';

function dom(tag = 'div') {
  return {
    tag,
    ownerDocument: { createElement: dom },
    children: [],
    events: {},
    append(...children) {
      this.children.push(...children);
    },
    replaceChildren(...children) {
      this.children = children;
    },
    querySelectorAll(tagName) {
      return this.children.flatMap(child => [
        ...(child.tag === tagName ? [child] : []),
        ...child.querySelectorAll(tagName)
      ]);
    },
    addEventListener(event, handler) {
      this.events[event] = handler;
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

test('snooze choices retain non-HQ links without offering a selection', () => {
  const host = dom();
  const first = reply().today.rows[0];
  renderSnoozeCard(
    host,
    {
      status: 'choose',
      choices: [{ ...first, selection: 'held-hq' }, reply().today.rows[1]],
      when: 'Monday',
      view: 'live-view'
    },
    { isCurrent: () => true }
  );
  assert.equal(host.querySelectorAll('button').length, 1);
  assert.equal(host.querySelectorAll('button')[0].textContent, 'Choose HQ: Reply');
  assert.equal(host.querySelectorAll('a')[1].href, '/workspaces/email-ops?follow_up=2');
});

test('review shows the exact local instant and cannot submit when stale or disabled', async () => {
  const host = dom();
  let current = true;
  const calls = [];
  renderSnoozeCard(
    host,
    {
      status: 'review',
      title: 'Reply',
      owner: 'HQ',
      suggested: true,
      until: '2026-11-02T09:00:00-05:00',
      until_label: 'Monday, 2 November 2026 at 09:00 EST (UTC-05:00) · America/New_York',
      review: 'review-id'
    },
    {
      isCurrent: () => current,
      send: async body => {
        calls.push(body);
        return { status: 'cancelled', message: 'Nothing changed.' };
      }
    }
  );
  assert.match(
    host.querySelectorAll('time')[0].textContent,
    /Suggested: Monday, 2 November 2026 at 09:00 EST/
  );
  const [, cancel, confirm] = host.querySelectorAll('button');
  assert.equal(confirm.disabled, true);
  current = false;
  cancel.events.click();
  assert.deepEqual(calls, []);
  current = true;
  cancel.events.click();
  await Promise.resolve();
  assert.deepEqual(calls, [{ action: 'cancel', review: 'review-id' }]);
  assert.equal(host.children[0].textContent, 'Nothing changed.');
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
