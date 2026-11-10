import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  searchWithoutSetupArrival,
  setupArrivalFromSearch,
  setupArrivalShouldOpen,
  setupArrivalTile
} from './home-setup-arrival.js';

const LANDING = '?mode=map&setup_beside=fc7a3486-d10a-4a02-b119-b3b2753a0770&setup_projects=1';

test('a landing asks for the sheet once, beside the folder it came from', () => {
  assert.deepEqual(setupArrivalFromSearch(LANDING), {
    beside: 'fc7a3486-d10a-4a02-b119-b3b2753a0770'
  });
  assert.deepEqual(setupArrivalFromSearch('?setup_projects=1'), { beside: '' });
  // Only the literal 1 counts; an id that is not one is dropped, not used.
  assert.equal(setupArrivalFromSearch('?setup_projects=true'), null);
  assert.equal(setupArrivalFromSearch('?mode=map'), null);
  assert.equal(setupArrivalFromSearch(''), null);
  assert.deepEqual(setupArrivalFromSearch('?setup_projects=1&setup_beside="]%20x'), { beside: '' });
});

test('the parameters leave the address, so a reload or Back never reopens the sheet', () => {
  assert.equal(searchWithoutSetupArrival(LANDING), '?mode=map');
  assert.equal(searchWithoutSetupArrival('?setup_projects=1'), '');
  assert.equal(searchWithoutSetupArrival('?mode=map&panel=notes'), '?mode=map&panel=notes');
  // After the replace, the page's own URL reads as no arrival.
  assert.equal(setupArrivalFromSearch(searchWithoutSetupArrival(LANDING)), null);
});

test('the sheet opens only when something can be set up', () => {
  const arrival = { beside: '' };
  assert.equal(setupArrivalShouldOpen({ arrival, toSetUp: 5 }), true);
  assert.equal(setupArrivalShouldOpen({ arrival, toSetUp: 0 }), false);
  assert.equal(setupArrivalShouldOpen({ arrival, toSetUp: undefined }), false);
  assert.equal(setupArrivalShouldOpen({ arrival: null, toSetUp: 5 }), false);
});

test("beside the folder's tile when the map draws it, else none (the chip)", () => {
  const drawn = { getBoundingClientRect: () => ({ width: 120, height: 80 }) };
  const hidden = { getBoundingClientRect: () => ({ width: 0, height: 0 }) };
  const asked = [];
  const root = found => ({
    querySelector: selector => {
      asked.push(selector);
      return found;
    }
  });
  assert.equal(setupArrivalTile(root(drawn), 'ws-albums'), drawn);
  assert.deepEqual(asked, ['[data-ws-id="ws-albums"]']);
  assert.equal(setupArrivalTile(root(hidden), 'ws-albums'), null);
  assert.equal(setupArrivalTile(root(null), 'ws-albums'), null);
  assert.equal(setupArrivalTile(root(drawn), ''), null);
  assert.equal(setupArrivalTile(root(drawn), 'a"] b'), null);
  assert.equal(setupArrivalTile(null, 'ws-albums'), null);
});

// The Home page reads the arrival before URL normalization drops parameters
// it does not own, consumes it on the first summary read (not on equality of
// the count, so a re-read never opens it twice), and opens through the same
// openLibrarySetupSheet the chip uses.
test('the Home page wires the arrival once, through the chip’s own sheet', () => {
  const source = readFileSync(new URL('./workspace-command.js', import.meta.url), 'utf8');
  const ctor = source.indexOf('this._setupArrival =');
  const normalize = source.indexOf('this.viewMode = resolveEffectiveMode(');
  assert.ok(ctor > 0 && ctor < normalize, 'captured in the constructor, before the mode resolves');
  assert.match(source, /const arrival = this\._setupArrival;\s*this\._setupArrival = null;/);
  assert.match(source, /this\.openLibrarySetupSheet\(chip \|\| tile, tile\)/);
  assert.match(source, /this\.openLibrarySetupSheet\(chip, zone\)/);
});
