/*
 * reasoning-effort.js — which models take a reasoning level, and which levels.
 *
 * Reasoning effort is configurable only for the CLI providers whose runtime
 * accepts it: Codex (`model_reasoning_effort`, low…xhigh, default medium) and
 * Claude Code (`--effort`, low…max). Claude Code has no default here: leaving
 * the level unset sends no flag, so the CLI's own default applies. Every other
 * provider ignores the setting, so its forms hide the field.
 *
 * This mirrors types.ReasoningEffortLevels on the server. Every agent form
 * reads it — the shared Create form, the agent page editor, and the legacy
 * create page — so they can never disagree about which models offer a level.
 *
 * Loaded as a classic deferred script from head.tmpl; `window.OriReasoningEffort`
 * is also the surface the unit tests evaluate in a node:vm sandbox.
 */
(function () {
  'use strict';

  var LABELS = {
    low: 'Low',
    medium: 'Medium',
    high: 'High',
    xhigh: 'Extra High',
    max: 'Max'
  };

  var CODEX_LEVELS = ['low', 'medium', 'high', 'xhigh'];
  var CLAUDE_CODE_LEVELS = ['low', 'medium', 'high', 'xhigh', 'max'];

  function text(value) {
    return String(value == null ? '' : value)
      .trim()
      .toLowerCase();
  }

  function family(provider, model) {
    var p = text(provider);
    if (p === 'codex' || text(model).indexOf('codex') !== -1) return 'codex';
    if (p === 'claude_code') return 'claude_code';
    return '';
  }

  // The levels a provider/model accepts, ascending, or [] when it has none.
  function levels(provider, model) {
    var kind = family(provider, model);
    if (kind === 'codex') return CODEX_LEVELS.slice();
    if (kind === 'claude_code') return CLAUDE_CODE_LEVELS.slice();
    return [];
  }

  function supports(provider, model) {
    return levels(provider, model).length > 0;
  }

  // value when this provider/model accepts it, otherwise ''.
  function normalize(provider, model, value) {
    var level = text(value);
    return levels(provider, model).indexOf(level) !== -1 ? level : '';
  }

  // What a form selects when nothing valid was chosen.
  function defaultFor(provider, model) {
    return family(provider, model) === 'codex' ? 'medium' : '';
  }

  // The choices a select offers, in display order.
  function options(provider, model) {
    var kind = family(provider, model);
    var choices = levels(provider, model).map(function (level) {
      return {
        value: level,
        label: kind === 'codex' && level === 'medium' ? 'Medium (Recommended)' : LABELS[level]
      };
    });
    if (kind === 'claude_code') {
      choices.unshift({ value: '', label: 'Claude Code default' });
    }
    return choices;
  }

  function helpText(provider, model) {
    var kind = family(provider, model);
    if (kind === 'claude_code') {
      return 'Claude Code effort. Higher levels think longer on hard problems at the cost of speed; the default lets Claude Code decide.';
    }
    if (kind === 'codex') {
      return 'Codex reasoning. Higher levels improve difficult reasoning at the cost of speed.';
    }
    return '';
  }

  // Rebuilds a <select> for this provider/model and selects `preferred` when it
  // is accepted, otherwise the provider's default. Returns the selected value.
  // A provider without levels leaves the select empty and returns ''.
  function syncSelect(select, provider, model, preferred) {
    if (!select) return '';
    var choices = options(provider, model);
    var wanted = normalize(provider, model, preferred);
    if (!wanted && !(family(provider, model) === 'claude_code' && text(preferred) === '')) {
      wanted = defaultFor(provider, model);
    }
    var doc = select.ownerDocument || (typeof document !== 'undefined' ? document : null);
    select.replaceChildren();
    choices.forEach(function (choice) {
      var option = doc.createElement('option');
      option.value = choice.value;
      option.textContent = choice.label;
      if (choice.value === wanted) option.selected = true;
      select.appendChild(option);
    });
    select.value = wanted;
    return choices.length ? wanted : '';
  }

  var api = {
    LABELS: LABELS,
    levels: levels,
    supports: supports,
    normalize: normalize,
    defaultFor: defaultFor,
    options: options,
    helpText: helpText,
    syncSelect: syncSelect
  };

  if (typeof window !== 'undefined') {
    window.OriReasoningEffort = api;
  }
})();
