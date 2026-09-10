/**
 * economy-price-line.js — what a schedule change costs, shown before it is paid.
 *
 * The task editor calls /api/economy/quote whenever the cadence or the enabled
 * state changes, and renders the answer directly under the schedule fields
 * (city-economy FR39). Goal 2 of the PRD is that no save ever charges a resource
 * the user did not see quoted, so this is the surface that makes that true.
 *
 * Deliberately a classic script rather than an ES module: its only consumer is
 * task-modal-controller.js, which is loaded with `defer` on six pages, and a
 * deferred script runs BEFORE any `type="module"` — a module here would not
 * exist yet when the controller binds its fields.
 *
 * The view is a pure function of the quote so every state can be asserted
 * without a browser, and so the editor's job is reduced to putting strings in
 * the DOM.
 */
(function () {
  'use strict';

  /** How long to wait after the last cadence keystroke before quoting (FR39). */
  var QUOTE_DEBOUNCE_MS = 300;

  // The pre-creative-mode prices, so a struck-through number is the real one.
  // These mirror internal/economy/tuning.go and are the only place the frontend
  // knows a price at all.
  var FARM_BUILD_COST = 25;
  var UPGRADE_STEP_MULTIPLIER = 10;

  /**
   * Build the price line from a quote.
   *
   * Returns { visible, text, runsPerDay, tone, blockSave, struckCost,
   * creativeMode }:
   *
   *   - `visible` false means there is nothing to charge and the line is hidden.
   *   - `tone` is 'neutral' when affordable, 'warning' when not, and 'muted' in
   *     creative mode. Never red-on-red: the caller maps these to theme tokens.
   *   - `struckCost` is the price to show struck through in creative mode — the
   *     user should still see what this would have cost.
   *   - `blockSave` is true only when the quote is unaffordable and creative
   *     mode is off (FR40). The server enforces this regardless (FR22);
   *     disabling Save is a courtesy, not the control.
   */
  function priceLineView(quote) {
    if (!quote || !quote.action || quote.action === 'none') {
      return { visible: false, text: '', runsPerDay: '', tone: 'neutral', blockSave: false };
    }

    var creative = !!quote.creative_mode;
    var cost = Number(quote.cost || 0);
    var balance = Number(quote.balance || 0);
    var resource = resourceName(quote.resource);
    var affordable = quote.affordable !== false;
    var struckCost = creative ? creativeCost(quote) : 0;

    var text;
    if (quote.action === 'build') {
      text = creative
        ? 'Build Farm: ' + struckCost + ' ' + resource
        : 'Build Farm: ' +
          cost +
          ' ' +
          resource +
          ' (you have ' +
          balance +
          (affordable ? '' : ' — not enough') +
          ')';
    } else {
      var from = String(quote.from_tier_name || '').trim();
      var to = String(quote.to_tier_name || '').trim();
      var step = from && to ? from + ' → ' + to : 'cadence';
      text = creative
        ? 'Upgrade ' + step + ': ' + struckCost + ' ' + resource
        : 'Upgrade ' +
          step +
          ': ' +
          cost +
          ' ' +
          resource +
          ' (you have ' +
          balance +
          (affordable ? '' : ' — not enough') +
          ')';
    }

    return {
      visible: true,
      text: text,
      runsPerDay: runsPerDayLine(quote),
      tone: creative ? 'muted' : affordable ? 'neutral' : 'warning',
      blockSave: !creative && !affordable,
      struckCost: creative ? struckCost : 0,
      creativeMode: creative
    };
  }

  /**
   * "Runs per day: 1 → 24" — what an upgrade actually buys (FR39).
   *
   * Only upgrades get this line: a build has no "before" to compare against, so
   * a single number there would read as a change that did not happen.
   */
  function runsPerDayLine(quote) {
    if (!quote || quote.action !== 'upgrade') return '';
    var after = Number(quote.runs_per_day_after || 0);
    if (!after) return '';
    return (
      'Runs per day: ' +
      formatRuns(Number(quote.runs_per_day_before || 0)) +
      ' → ' +
      formatRuns(after)
    );
  }

  function formatRuns(value) {
    if (!isFinite(value)) return '0';
    return value === Math.round(value) ? String(value) : String(Math.round(value * 100) / 100);
  }

  function resourceName(resource) {
    return String(resource || '') === 'harvest' ? 'Harvest' : 'Craft';
  }

  function creativeCost(quote) {
    if (quote.action === 'build') return FARM_BUILD_COST;
    var from = Number(quote.from_tier || 0);
    var to = Number(quote.to_tier || 0);
    var total = 0;
    for (var tier = from; tier < to; tier++) {
      if (tier >= 1 && tier <= 4) total += UPGRADE_STEP_MULTIPLIER * tier;
    }
    return total;
  }

  /**
   * The explanation shown when a save is refused, matching the PRD's own copy.
   *
   * Built from the 409 body's fields rather than from a server string, so the
   * wording lives in one place on the client and the server stays a data API
   * (FR22, FR41).
   */
  function insufficientMessage(body) {
    var required = Number((body && body.required) || 0);
    var balance = Number((body && body.balance) || 0);
    if (String((body && body.resource) || '') === 'harvest') {
      return (
        'Not enough Harvest. This upgrade costs ' +
        required +
        ' Harvest; you have ' +
        balance +
        ". Open this Farm's results after it runs to earn more."
      );
    }
    return (
      'Not enough Craft. Building a Farm costs ' +
      required +
      ' Craft; you have ' +
      balance +
      '. Send messages or finish tasks by hand to earn more.'
    );
  }

  /**
   * Read a 409 body, or null when the response is not an insufficient-resources
   * refusal. Anything else is somebody else's error to report.
   */
  function readInsufficientBody(body) {
    if (!body || typeof body !== 'object') return null;
    if (body.error !== 'insufficient_resources') return null;
    return {
      resource: String(body.resource || 'craft'),
      required: Number(body.required || 0),
      balance: Number(body.balance || 0),
      action: String(body.action || 'build')
    };
  }

  /**
   * Ask the server what a schedule change would cost.
   *
   * Returns null when there is no economy here (the flag is off, so the route
   * 404s) or the request failed — the caller hides the line rather than guessing
   * at a price.
   */
  function fetchQuote(request) {
    var payload = request || {};
    return fetch('/api/economy/quote', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        workspace_id: String(payload.workspaceId || ''),
        task_id: String(payload.taskId || ''),
        schedule: payload.schedule || null,
        schedule_enabled: !!payload.scheduleEnabled
      })
    })
      .then(function (response) {
        if (response.status === 404) return null;
        if (!response.ok) throw new Error('HTTP ' + response.status);
        return response.json();
      })
      .catch(function (err) {
        console.warn('economy-price-line: could not price this change', err);
        return null;
      });
  }

  window.OriEconomyPriceLine = {
    QUOTE_DEBOUNCE_MS: QUOTE_DEBOUNCE_MS,
    priceLineView: priceLineView,
    runsPerDayLine: runsPerDayLine,
    insufficientMessage: insufficientMessage,
    readInsufficientBody: readInsufficientBody,
    fetchQuote: fetchQuote
  };
})();
