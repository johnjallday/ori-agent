import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  isStationKey,
  isStationLink,
  stationFromSearch,
  stationLink
} from './workspace-station-link.js';

test('stationLink builds the station address and refuses an unusable slug or key', () => {
  assert.equal(stationLink('album-three', 'memory'), '/workspaces/album-three?station=memory');
  assert.equal(stationLink(' my-hq ', 'daily-brief'), '/workspaces/my-hq?station=daily-brief');
  for (const [slug, key] of [
    ['', 'memory'],
    ['Album Three', 'memory'],
    ['album/three', 'memory'],
    ['-album', 'memory'],
    ['album-three', ''],
    ['album-three', 'Memory'],
    ['album-three', 'memory&next=//evil.example'],
    [undefined, undefined],
    ['a'.repeat(81), 'memory']
  ]) {
    assert.equal(stationLink(slug, key), '', `${slug} / ${key}`);
  }
});

test('stationFromSearch returns any station-shaped key and nothing else', () => {
  assert.equal(stationFromSearch('?station=memory'), 'memory');
  assert.equal(stationFromSearch('?mode=map&station=daily-brief'), 'daily-brief');
  assert.equal(stationFromSearch('?station=file-janitor'), 'file-janitor');
  assert.equal(stationFromSearch('?station=Memory'), '');
  assert.equal(stationFromSearch('?station=%3Cscript%3E'), '');
  assert.equal(stationFromSearch('?station='), '');
  assert.equal(stationFromSearch(''), '');
  assert.equal(stationFromSearch(undefined), '');
  assert.equal(isStationKey('memory'), true);
  assert.equal(isStationKey('../memory'), false);
});

test('isStationLink accepts exactly the shape stationLink builds', () => {
  assert.equal(isStationLink(stationLink('album-three', 'memory')), true);
  assert.equal(isStationLink('/workspaces/album-three?station=memory', 'memory'), true);
  assert.equal(isStationLink('/workspaces/album-three?station=memory', 'daily-brief'), false);
  for (const bad of [
    '/workspaces/album-three',
    '/workspaces/album-three?station=memory&next=//evil.example',
    '/workspaces/album-three/runs/r1?station=memory',
    'https://evil.example/workspaces/album-three?station=memory',
    '//evil.example/workspaces/album-three?station=memory',
    '/workspaces/album-three?station=memory#x',
    ''
  ]) {
    assert.equal(isStationLink(bad), false, bad);
  }
});
