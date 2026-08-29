/*
 * workspace-agent-portrait.js — deterministic inline agent portraits shared by
 * Workspace Map contexts and the Workspace Details page.
 *
 * This is intentionally separate from AgentAvatar. It needs only the name that
 * workspace payloads already carry, never reads or writes persisted appearance,
 * and changes no app-wide Generated-mode behavior. Consumers may use it as the
 * local default while continuing to honor explicit uploaded/character portraits.
 */
(function () {
  'use strict';

  var COLORS = ['#6f96b8', '#d3a44a', '#c0714c', '#8f78ad', '#6a9a5f', '#5c9aa3'];

  function escapeHtml(value) {
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (character) {
      return {
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;'
      }[character];
    });
  }

  // FNV-1a, matching the Workspace Map's stable palette selection.
  function hashKey(value) {
    var hash = 2166136261;
    var text = String(value == null ? '' : value);
    for (var index = 0; index < text.length; index++) {
      hash ^= text.charCodeAt(index);
      hash = Math.imul(hash, 16777619);
    }
    return hash >>> 0;
  }

  function colorFor(name) {
    return COLORS[hashKey(name) % COLORS.length];
  }

  // Consumers supply only internal layout classes. Still constrain them before
  // they reach an attribute so this helper remains safe if a future caller
  // accidentally forwards user input.
  function safeClasses(value) {
    var seen = Object.create(null);
    return String(value || '')
      .split(/\s+/)
      .filter(function (className) {
        if (!/^[a-zA-Z0-9_-]+$/.test(className) || seen[className]) return false;
        seen[className] = true;
        return true;
      });
  }

  function markup(name, options) {
    var settings = options && typeof options === 'object' ? options : {};
    var fullName = String(name == null ? '' : name);
    var safeName = escapeHtml(fullName);
    var classes = ['ws-map-av'];
    if (settings.isKeeper) classes.push('is-keeper');
    safeClasses(settings.className).forEach(function (className) {
      if (classes.indexOf(className) === -1) classes.push(className);
    });

    return (
      '<span class="' +
      classes.join(' ') +
      '" style="--av:' +
      colorFor(fullName) +
      '" title="' +
      safeName +
      '">' +
      '<svg class="ws-map-av-figure" viewBox="0 0 48 56" aria-hidden="true" focusable="false">' +
      '<circle class="ws-map-av-head" cx="24" cy="13" r="8"></circle>' +
      '<line class="ws-map-av-body" x1="24" y1="21" x2="24" y2="39"></line>' +
      '<line class="ws-map-av-arms" x1="11" y1="28" x2="37" y2="28"></line>' +
      '<path class="ws-map-av-legs" d="M24 39 L13 52 M24 39 L35 52"></path>' +
      '</svg>' +
      '<span class="ws-map-av-label" title="' +
      safeName +
      '">' +
      safeName +
      '</span>' +
      '</span>'
    );
  }

  var root = typeof window !== 'undefined' ? window : globalThis;
  root.OriWorkspaceAgentPortrait = {
    markup: markup,
    colorFor: colorFor
  };
})();
