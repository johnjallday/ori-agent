// Tests for field-labels.js — the host's stage and status labels.
//   node --test internal/web/static/js/modules/field-labels.test.js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { STAGE_LABELS, STATUS_LABELS, stageLabel, statusLabel } from './field-labels.js';

const template = readFileSync(
  new URL('../../../templates/pages/workspace-assistant.tmpl', import.meta.url),
  'utf8'
);

function options(selectID) {
  const select = template.match(new RegExp(`<select id="${selectID}"[^>]*>(.*?)</select>`));
  assert.ok(select, `${selectID} is in the shelf template`);
  return Object.fromEntries(
    [...select[1].matchAll(/<option value="([a-z_]+)">([^<]+)<\/option>/g)]
      .filter(([, value]) => value !== 'unknown')
      .map(([, value, label]) => [value, label])
  );
}

test('the labels are the shelf filter’s own words', () => {
  assert.deepEqual({ ...STAGE_LABELS }, options('projectLibraryStage'));
  assert.deepEqual({ ...STATUS_LABELS }, options('projectLibraryStatusFilter'));
});

test('unset and unknown values have no label', () => {
  assert.equal(stageLabel('mixing'), 'Mixing');
  assert.equal(statusLabel('on_hold'), 'On hold');
  for (const value of ['', undefined, null, 'unknown', 'toString', '__proto__']) {
    assert.equal(stageLabel(value), '');
    assert.equal(statusLabel(value), '');
  }
});
