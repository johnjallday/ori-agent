// Tests for economy-price-line.js — the four states of the task editor's price
// line (city-economy FR39, FR40, FR41).
//
// The module is a classic script (see its header for why), so it is loaded in a
// sandbox the way workspace-map.test.js loads the map:
//   node --test internal/web/static/js/modules/economy-price-line.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./economy-price-line.js', import.meta.url), 'utf8');
const css = readFileSync(new URL('../../css/sessions.css', import.meta.url), 'utf8');

function loadPriceLine() {
  const window = {};
  vm.runInNewContext(source, { window, console }, { filename: 'economy-price-line.js' });
  return window.OriEconomyPriceLine;
}

const buildQuote = {
  action: 'build',
  resource: 'craft',
  cost: 25,
  balance: 40,
  affordable: true,
  to_tier: 2,
  to_tier_name: 'Daily',
  runs_per_day_after: 1,
  creative_mode: false
};

const upgradeQuote = {
  action: 'upgrade',
  resource: 'harvest',
  cost: 50,
  balance: 26,
  affordable: false,
  from_tier: 2,
  from_tier_name: 'Daily',
  to_tier: 4,
  to_tier_name: 'Hourly',
  runs_per_day_before: 1,
  runs_per_day_after: 24,
  creative_mode: false
};

// State 1: a build the user can afford.
test('the build state names the price and the balance', () => {
  const view = loadPriceLine().priceLineView(buildQuote);

  assert.equal(view.visible, true);
  assert.equal(view.text, 'Build Farm: 25 Craft (you have 40)');
  assert.equal(view.tone, 'neutral');
  assert.equal(view.blockSave, false);
  // A build has no "before", so there is nothing to compare runs against.
  assert.equal(view.runsPerDay, '');
});

// State 2: an upgrade the user cannot afford (the PRD's own example).
test('an unaffordable upgrade says so and blocks Save', () => {
  const view = loadPriceLine().priceLineView(upgradeQuote);

  assert.equal(view.text, 'Upgrade Daily → Hourly: 50 Harvest (you have 26 — not enough)');
  assert.equal(view.runsPerDay, 'Runs per day: 1 → 24');
  assert.equal(view.tone, 'warning');
  assert.equal(view.blockSave, true);
});

test('an affordable upgrade does not block Save', () => {
  const view = loadPriceLine().priceLineView({ ...upgradeQuote, balance: 60, affordable: true });

  assert.equal(view.text, 'Upgrade Daily → Hourly: 50 Harvest (you have 60)');
  assert.equal(view.tone, 'neutral');
  assert.equal(view.blockSave, false);
});

// State 3: nothing to charge — a decrease, an unchanged cadence, a non-cadence
// edit, or an install with the feature off. The line is hidden, not zeroed.
test('a free change hides the line entirely', () => {
  const priceLine = loadPriceLine();
  for (const quote of [null, undefined, {}, { action: 'none' }]) {
    const view = priceLine.priceLineView(quote);
    assert.equal(view.visible, false, JSON.stringify(quote) ?? 'undefined');
    assert.equal(view.blockSave, false);
    assert.equal(view.text, '');
  }
});

// State 4: creative mode — the real price, struck through, never blocking.
test('creative mode shows the price it is waiving and never blocks', () => {
  const priceLine = loadPriceLine();

  const build = priceLine.priceLineView({
    ...buildQuote,
    cost: 0,
    balance: 0,
    creative_mode: true
  });
  assert.equal(build.creativeMode, true);
  assert.equal(build.tone, 'muted');
  assert.equal(build.blockSave, false);
  // The struck-through number is the REAL price, not the zeroed one — "0 Craft"
  // struck through would tell the user nothing.
  assert.equal(build.struckCost, 25);
  assert.equal(build.text, 'Build Farm: 25 Craft');

  const upgrade = priceLine.priceLineView({
    ...upgradeQuote,
    cost: 0,
    balance: 0,
    creative_mode: true
  });
  assert.equal(upgrade.struckCost, 50, 'Daily -> Hourly is 20 + 30');
  assert.equal(upgrade.text, 'Upgrade Daily → Hourly: 50 Harvest');
  assert.equal(upgrade.blockSave, false);
});

test('runs per day is only shown for an upgrade', () => {
  const priceLine = loadPriceLine();
  assert.equal(priceLine.runsPerDayLine(buildQuote), '');
  assert.equal(priceLine.runsPerDayLine({ action: 'none' }), '');
  assert.equal(priceLine.runsPerDayLine(upgradeQuote), 'Runs per day: 1 → 24');
  // Weekly reports a fraction rather than rounding to zero: "0 runs a day" would
  // read as a broken Farm.
  assert.equal(
    priceLine.runsPerDayLine({ ...upgradeQuote, runs_per_day_before: 0.14 }),
    'Runs per day: 0.14 → 24'
  );
});

test('a 409 body is read only when it is an insufficient-resources refusal', () => {
  const priceLine = loadPriceLine();

  const refusal = priceLine.readInsufficientBody({
    error: 'insufficient_resources',
    resource: 'craft',
    required: 25,
    balance: 12,
    action: 'build'
  });
  // Field by field rather than deepEqual: the object is built inside the vm
  // sandbox, so its prototype is the sandbox's and a structural comparison
  // fails on that alone.
  assert.equal(refusal.resource, 'craft');
  assert.equal(refusal.required, 25);
  assert.equal(refusal.balance, 12);
  assert.equal(refusal.action, 'build');

  for (const body of [null, undefined, {}, { error: 'something_else' }, 'nope']) {
    assert.equal(priceLine.readInsufficientBody(body), null);
  }
});

// The refusal copy is the PRD's own, built from the body's numbers rather than
// from a server string.
test('the refusal message names the resource, the price, and how to earn more', () => {
  const priceLine = loadPriceLine();

  assert.equal(
    priceLine.insufficientMessage({ resource: 'craft', required: 25, balance: 12 }),
    'Not enough Craft. Building a Farm costs 25 Craft; you have 12. ' +
      'Send messages or finish tasks by hand to earn more.'
  );
  assert.equal(
    priceLine.insufficientMessage({ resource: 'harvest', required: 50, balance: 26 }),
    'Not enough Harvest. This upgrade costs 50 Harvest; you have 26. ' +
      "Open this Farm's results after it runs to earn more."
  );
});

test('the quote is debounced rather than fired per keystroke', () => {
  assert.equal(loadPriceLine().QUOTE_DEBOUNCE_MS, 300);
});

// The price line's three tones must all resolve to theme tokens: a hardcoded
// color here is the bug --border-light's inversion between themes causes.
test('every price-line tone is styled from theme tokens', () => {
  for (const tone of ['warning', 'muted']) {
    assert.match(
      css,
      new RegExp(`\\.task-modal-economy-price\\[data-tone=["']${tone}["']\\]`),
      `no styling for the ${tone} tone`
    );
  }
  assert.match(css, /\.task-modal-economy-price__cost\.is-creative/);
  assert.match(css, /content: ["'] Creative mode["'];/);
});
