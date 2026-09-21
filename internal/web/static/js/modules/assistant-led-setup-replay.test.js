import test from 'node:test';
import assert from 'node:assert/strict';

import { blueprintCards, replaySources } from './assistant-led-setup-replay.js';

test('blueprint cards put Blank first, keep only built-ins, and never drop File Janitor', () => {
  const cards = blueprintCards([
    {
      id: 'calendar-ops',
      builtin: true,
      label: 'Calendar Ops',
      tagline: 'Your calendar command center.'
    },
    { id: 'my-template', builtin: false, label: 'My own template' },
    {
      id: 'file-janitor',
      builtin: true,
      label: 'File Janitor',
      description: 'Review and file. Safely.'
    }
  ]);
  assert.deepEqual(
    cards.map(card => card.id),
    ['blank', 'calendar-ops', 'file-janitor']
  );
  assert.equal(cards[2].tagline, 'Review and file.');
  assert.equal(cards[2].description, 'Review and file. Safely.');
});

test('a missing or broken template list still yields a clickable File Janitor', () => {
  for (const input of [undefined, null, [], 'nope', [{ id: 'x' }]]) {
    const ids = blueprintCards(input).map(card => card.id);
    assert.deepEqual(ids, ['blank', 'file-janitor'], String(input));
  }
});

test('card data carries display fields only, never a path or server root', () => {
  const cards = blueprintCards([
    {
      id: 'file-janitor',
      builtin: true,
      label: 'File Janitor',
      description: 'Files things.',
      source_path: '/Users/someone/templates/file-janitor',
      templates_root: '/Users/someone/templates'
    }
  ]);
  assert.doesNotMatch(JSON.stringify(cards), /\/Users|source_path|templates_root/);
});

test('long taglines are shortened the way the real blueprint grid shortens them', () => {
  const long = 'A very long description without any sentence ending that just keeps going '.repeat(
    4
  );
  const card = blueprintCards([{ id: 'file-janitor', builtin: true, description: long }]).find(
    item => item.id === 'file-janitor'
  );
  assert.ok(card.tagline.length <= 80);
  assert.ok(card.tagline.endsWith('…'));
});

test('replay sources are absent on a page without the real modals', () => {
  const doc = { getElementById: () => null };
  assert.deepEqual(replaySources(doc), { workspace: null, agent: null, agentForm: null });
  assert.deepEqual(replaySources(undefined), { workspace: null, agent: null, agentForm: null });
});
