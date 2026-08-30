/*
 * workspace-agent-portrait.js — deterministic inline Pocket Resident portraits
 * shared by Workspace Map contexts and the Workspace Details page.
 *
 * This is intentionally separate from AgentAvatar. It needs only the name that
 * workspace payloads already carry, never reads or writes persisted appearance,
 * and changes no app-wide Generated-mode behavior. Its warm resident body,
 * expressive face, stick limbs, and bounded name-derived accessory echo the
 * curated character family without impersonating one. Consumers may use it as
 * the local default while continuing to honor explicit uploaded/character portraits.
 */
(function () {
  'use strict';

  var COLORS = ['#6f96b8', '#d3a44a', '#c0714c', '#8f78ad', '#6a9a5f', '#5c9aa3'];
  var RESIDENT_VARIANTS = ['ears', 'sprout', 'tuft', 'antenna'];

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

  function variantFor(name) {
    return RESIDENT_VARIANTS[(hashKey(name) >>> 8) % RESIDENT_VARIANTS.length];
  }

  function accessoryMarkup(variant) {
    switch (variant) {
      case 'ears':
        return (
          '<g class="ws-map-av-accessory is-ears">' +
          '<circle class="ws-map-av-accessory-shape" cx="19" cy="18" r="5"></circle>' +
          '<circle class="ws-map-av-accessory-shape" cx="45" cy="18" r="5"></circle>' +
          '</g>'
        );
      case 'sprout':
        return (
          '<g class="ws-map-av-accessory is-sprout">' +
          '<path class="ws-map-av-accessory-stem" d="M32 11 V3"></path>' +
          '<path class="ws-map-av-accessory-leaf" d="M32 6 Q23 6 24 0 Q31 -1 32 6 Z"></path>' +
          '<path class="ws-map-av-accessory-leaf is-right" d="M32 8 Q41 8 40 2 Q33 1 32 8 Z"></path>' +
          '</g>'
        );
      case 'tuft':
        return (
          '<g class="ws-map-av-accessory is-tuft">' +
          '<path class="ws-map-av-accessory-shape" d="M24 13 L27 3 L32 10 L37 2 L40 14 Z"></path>' +
          '</g>'
        );
      default:
        return (
          '<g class="ws-map-av-accessory is-antenna">' +
          '<path class="ws-map-av-accessory-stem" d="M32 11 V3"></path>' +
          '<circle class="ws-map-av-accessory-shape" cx="32" cy="2.5" r="2.5"></circle>' +
          '</g>'
        );
    }
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

    var residentVariant = variantFor(fullName);
    var headwear = settings.isKeeper
      ? '<path class="ws-map-av-crown" d="M19 15 L21 5 L27 10 L32 2 L37 10 L43 5 L45 15 Z"></path>'
      : accessoryMarkup(residentVariant);
    var commanderMark = settings.isKeeper
      ? '<span class="ws-map-av-commander-mark" aria-hidden="true">★</span>'
      : '';

    return (
      '<span class="' +
      classes.join(' ') +
      '" style="--av:' +
      colorFor(fullName) +
      '" title="' +
      safeName +
      '">' +
      '<svg class="ws-map-av-figure" viewBox="0 0 64 74" aria-hidden="true" focusable="false">' +
      headwear +
      '<path class="ws-map-av-legs" d="M27 57 V67 L22 71 M37 57 V67 L42 71"></path>' +
      '<path class="ws-map-av-arms" d="M21 39 Q13 41 11 50 M43 39 Q51 41 53 50"></path>' +
      '<circle class="ws-map-av-hand" cx="10" cy="52" r="3"></circle>' +
      '<circle class="ws-map-av-hand" cx="54" cy="52" r="3"></circle>' +
      '<rect class="ws-map-av-body" x="20" y="34" width="24" height="25" rx="10"></rect>' +
      '<path class="ws-map-av-scarf" d="M22 36 Q32 42 42 36 V43 Q32 47 22 43 Z"></path>' +
      '<path class="ws-map-av-scarf-tail" d="M38 42 L45 56 L40 58 L34 44 Z"></path>' +
      '<circle class="ws-map-av-head" cx="32" cy="23" r="13"></circle>' +
      '<circle class="ws-map-av-eye" cx="27" cy="22" r="1.8"></circle>' +
      '<circle class="ws-map-av-eye" cx="37" cy="22" r="1.8"></circle>' +
      '<path class="ws-map-av-smile" d="M28 28 Q32 32 36 28"></path>' +
      (settings.isKeeper
        ? '<circle class="ws-map-av-keeper-badge" cx="25" cy="49" r="3.5"></circle>'
        : '') +
      '</svg>' +
      commanderMark +
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
    colorFor: colorFor,
    variantFor: variantFor
  };
})();
