import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import * as shared from './home-song-offer.js';
import { folderDigestOffer, songSuggestionView } from './home-song-suggestions.js';

const source = readFileSync(new URL('./home-song-suggestions.js', import.meta.url), 'utf8');

// The view, press and follow are home-song-offer.js's (tested there); the
// dashboard card and the manager's offer in the drawer read the same ones.
test('the Home page’s card reads the shared view and makes the shared press', () => {
  assert.equal(songSuggestionView, shared.songSuggestionView);
  assert.equal(folderDigestOffer, shared.folderDigestOffer);
  assert.match(source, /from '\.\/home-song-offer\.js'/);
  assert.match(source, /pressSongOpen\(this\.fetchImpl, view, typed\)/);
  assert.match(source, /followSongOpen\(this\.fetchImpl, view\)/);
  assert.match(source, /readSongSuggestions\(this\.fetchImpl, this\.workspaceId\)/);
  // No second copy of the press or the read.
  assert.doesNotMatch(source, /\/setup`/);
  assert.doesNotMatch(source, /fetchImpl\(\s*`/);
  assert.doesNotMatch(source, /\b(folder_path|path):/);
});

test('a redraw while opening keeps the typed brief', () => {
  assert.match(source, /this\.briefs\.get\(view\.offerID\) \?\? view\.brief/);
});

test('the card keeps its copy: suggests, opening, opened', () => {
  assert.match(source, /`\$\{view\.manager\} suggests`/);
  assert.match(source, /'What should it do with it\?'/);
  assert.match(source, /'Go to your assistant'/);
  assert.match(source, /songOpeningLabel\(view\)/);
  assert.match(source, /songOpenedView\(view, outcome\)/);
});
