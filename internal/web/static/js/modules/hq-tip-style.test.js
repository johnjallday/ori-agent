import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

// A contract on the first-visit tip's stylesheet, for what a browser check
// cannot show here: prefers-reduced-motion cannot be emulated, and the colours
// must never read as another mark the page already uses.
const css = readFileSync(new URL('../../css/workspace-command.css', import.meta.url), 'utf8');
const guide = readFileSync(new URL('../../css/ori-guide.css', import.meta.url), 'utf8');

function block(source, selector) {
  const start = source.indexOf('\n' + selector + ' {');
  assert.ok(start >= 0, selector + ' is defined');
  return source.slice(start, source.indexOf('}', start) + 1);
}

// The `@media <query> {` block that contains `marker`, found by matching braces
// (the stylesheet has several blocks with the same query).
function mediaBlock(source, query, marker) {
  const opener = '@media ' + query + ' {';
  for (let start = source.indexOf(opener); start >= 0; start = source.indexOf(opener, start + 1)) {
    let depth = 0;
    for (let at = source.indexOf('{', start); at < source.length; at += 1) {
      if (source[at] === '{') depth += 1;
      if (source[at] === '}') depth -= 1;
      if (depth === 0) {
        const found = source.slice(start, at + 1);
        if (found.includes(marker)) return found;
        break;
      }
    }
  }
  assert.fail(query + ' has no block containing ' + marker);
}

// Amber is "an agent is waiting on you", green is ready, red is an error,
// dashed is "not set up", and nothing here may pulse.
const NOT_THE_MARK =
  /--ws-keeper|--ws-waiting|--ws-faction|--ws-alert|--ws-input|#e8b54b|dashed|animation|transition/;

test('the mark and the outline are neutral: no amber, green, red, dashed or pulsing', () => {
  for (const selector of [
    '.ws-hq-station.is-tip-marked:not(:focus-visible)',
    '.ws-hq-panel-host.is-tip-marked',
    '.ws-hq-tip-mark',
    '.ws-hq-tip-mark > span'
  ]) {
    assert.doesNotMatch(block(css, selector), NOT_THE_MARK, selector);
  }
  assert.match(block(css, '.ws-hq-station.is-tip-marked:not(:focus-visible)'), /1px solid/);
  assert.match(block(css, '.ws-hq-panel-host.is-tip-marked'), /1px solid/);
});

test('the outline never hides the keyboard focus ring', () => {
  assert.match(
    block(css, '.ws-hq-station.is-tip-marked:not(:focus-visible)'),
    /outline: /,
    'the outline is declared only while the card is not keyboard focused'
  );
  assert.match(block(css, '.ws-hq-tip-mark:focus-visible'), /outline: 2px solid/);
});

test('the ? is a 44px target around a 20px ring', () => {
  const mark = block(css, '.ws-hq-tip-mark');
  assert.match(mark, /width: 44px/);
  assert.match(mark, /height: 44px/);
  const ring = block(css, '.ws-hq-tip-mark > span');
  assert.match(ring, /width: 20px/);
  assert.match(ring, /height: 20px/);
});

test('the tip is a fixed, neutral box whose only motion is a fade, and only when motion is allowed', () => {
  const tip = block(css, '.ws-cmd.ws-hq-tip');
  assert.match(tip, /position: fixed/);
  assert.match(tip, /max-width: min\(320px, calc\(100vw - 32px\)\)/);
  assert.doesNotMatch(tip, NOT_THE_MARK);

  const allowed = mediaBlock(css, '(prefers-reduced-motion: no-preference)', 'ws-hq-tip-in');
  const everywhere = css.match(/ws-hq-tip-in/g) || [];
  const inside = allowed.match(/ws-hq-tip-in/g) || [];
  assert.ok(everywhere.length >= 2, 'a keyframe and an animation exist');
  assert.equal(everywhere.length, inside.length, 'every use of the fade is inside no-preference');
  assert.match(allowed, /\.ws-cmd\.ws-hq-tip:not\(\[hidden\]\)/);
});

test('the tip sits above the assistant drawer and below the stat modal', () => {
  const zOf = (source, selector) => Number(block(source, selector).match(/z-index: (\d+)/)[1]);
  const tip = zOf(css, '.ws-cmd.ws-hq-tip');
  const drawer = zOf(guide, '.ori-guide');
  const statModal = Number(css.match(/--wsx-layer-legacy-stat-modal: (\d+)/)[1]);
  assert.ok(tip > drawer, `tip ${tip} is above the drawer ${drawer}`);
  assert.ok(tip < statModal, `tip ${tip} is below the stat modal ${statModal}`);
});
