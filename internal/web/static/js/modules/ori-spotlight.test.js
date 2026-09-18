// Tests for ori-spotlight.js — the geometry of Ori's blocking layer. The DOM
// itself is driven in a real browser by scripts/demo-meet-assistant.mjs and the
// personal-assistant-foundation Playwright spec.
//   node --test internal/web/static/js/modules/ori-spotlight.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  holeFor,
  placeBeside,
  placeCallout,
  roundedRectPath,
  scrimClipPath
} from './ori-spotlight.js';

const view = { width: 1280, height: 900 };

// The command letters of a path, which must not change with the size: the
// browser only animates between paths whose commands line up.
const commands = path => path.replace(/[^A-Za-z]/g, '');

test('a rounded hole has the same commands at every size, a point included', () => {
  const point = roundedRectPath(640, 450, 0, 0, 0);
  const hole = roundedRectPath(500, 10, 110, 44, 12);
  assert.equal(commands(point), commands(hole));
  assert.match(hole, /^M512 10H598A12 12 0 0 1 610 22/);
  // The radius never exceeds half the smaller side.
  assert.match(roundedRectPath(0, 0, 10, 40, 12), /A5 5 /);
});

test('the hole is padded so the ring around the control shows inside it', () => {
  assert.deepEqual(holeFor({ left: 500, top: 10, width: 90, height: 24 }), {
    x: 490,
    y: 0,
    width: 110,
    height: 44
  });
});

test('the dimmed layer is the viewport minus the hole, evenodd', () => {
  const clip = scrimClipPath(view, { x: 490, y: 0, width: 110, height: 44 });
  assert.match(clip, /^path\(evenodd, "M0 0H1280V900H0Z M/);
  // No hole yet (a briefing): a point at the centre, which the hole grows from.
  const closed = scrimClipPath(view, null);
  assert.match(closed, /M640 450H640/);
  assert.equal(commands(closed), commands(clip));
});

test('the callout sits under the control, centred, inside the gutters', () => {
  const hole = { x: 490, y: 0, width: 110, height: 44 };
  const place = placeCallout(hole, view, { width: 340, height: 160 });
  assert.equal(place.side, 'below');
  assert.equal(place.top, 66);
  assert.equal(place.left, 375); // centre 545 - 170
  assert.equal(place.arrow, 170);
});

test('near an edge the callout is pushed inside and its notch still aims at the control', () => {
  const hole = { x: 1180, y: 130, width: 90, height: 50 };
  const place = placeCallout(hole, view, { width: 340, height: 160 });
  assert.equal(place.left, 1280 - 16 - 340);
  assert.equal(place.arrow, 1225 - place.left);
  const lowHole = { x: 100, y: 800, width: 100, height: 60 };
  const above = placeCallout(lowHole, view, { width: 340, height: 160 });
  assert.equal(above.side, 'above');
  assert.equal(above.top, 800 - 22 - 160);
  // The notch never leaves the callout's rounded corner.
  assert.equal(
    placeCallout({ x: 0, y: 0, width: 10, height: 10 }, view, { width: 340, height: 100 }).arrow,
    20
  );
});

test('beside a form: to its left when there is room, level with the field', () => {
  const form = { left: 858, top: 250, width: 380, height: 650 };
  const field = { left: 1056, top: 420, width: 160, height: 44 };
  const place = placeBeside(form, field, view, { width: 340, height: 150 });
  assert.equal(place.side, 'left');
  assert.equal(place.left, 858 - 18 - 340);
  // Level with the field's middle; the notch points at it.
  assert.equal(place.top, 442 - 36);
  assert.equal(place.arrow, 36);
});

test('beside a form: the right side when the left is full, and none on a phone', () => {
  const leftForm = { left: 40, top: 100, width: 380, height: 600 };
  const field = { left: 60, top: 200, width: 300, height: 40 };
  assert.equal(placeBeside(leftForm, field, view, { width: 340, height: 150 }).side, 'right');
  const sheet = { left: 0, top: 0, width: 400, height: 900 };
  assert.equal(
    placeBeside(sheet, field, { width: 400, height: 900 }, { width: 340, height: 150 }),
    null
  );
});

test('beside a form: a field near the bottom keeps the callout on screen', () => {
  const form = { left: 858, top: 0, width: 380, height: 900 };
  const hire = { left: 980, top: 870, width: 130, height: 40 };
  const place = placeBeside(form, hire, view, { width: 340, height: 150 });
  assert.equal(place.top, 900 - 16 - 150);
  assert.ok(place.arrow <= 150 - 20);
});
