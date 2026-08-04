/*
 * Character picker — one reusable chooser over the server-owned catalog.
 *
 * Used by both New Agent and the Inspector's Identity section, so the two can
 * never drift into different selection rules (PRD FR-53/FR-55).
 *
 * Two things this module deliberately does not do:
 *
 *   - It never assigns. `open()` resolves with a choice; the caller decides
 *     whether and when to persist it, so opening the picker cannot mutate an
 *     agent and Cancel genuinely cancels (FR-95).
 *   - It never offers Ori. The catalog serves the guide on its own key and the
 *     server rejects the reserved id anyway, but a picker that could display it
 *     would be a UI lying about what is possible (FR-19/FR-71).
 *
 * Search matches only labels the user can actually see, so a hidden field can
 * never explain why something matched (FR-98).
 */
(function () {
  'use strict';

  function esc(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  var state = {
    open: false,
    els: null,
    characters: [],
    filtered: [],
    selectedId: '',
    voiceEnabled: false,
    lastTrigger: null,
    resolve: null
  };

  /* ---- filtering ------------------------------------------------------------- */

  // Only visible labels: name, family, purpose, and tone traits. The catalog id,
  // asset paths, and silhouette text are not searchable, because a user cannot
  // see why they matched.
  function haystack(ch) {
    return [ch.name, ch.familyLabel, ch.purpose, (ch.toneTraits || []).join(' ')]
      .join(' ')
      .toLowerCase();
  }

  function applyFilter() {
    var q = String((state.els && state.els.search.value) || '')
      .trim()
      .toLowerCase();
    var family = state.family || '';

    state.filtered = state.characters.filter(function (ch) {
      if (family && ch.family !== family) return false;
      if (!q) return true;
      return haystack(ch).indexOf(q) !== -1;
    });
    renderGrid();
  }

  /* ---- recommendation --------------------------------------------------------- */

  // Suggest a character that suits the agent's role and that no other agent is
  // using yet, so a fresh roster spreads out instead of everyone landing on the
  // first card.
  //
  // Two deterministic preferences, in order: unused beats used, and
  // role-matching beats not. Both come from data already on the client — the
  // roster's assigned IDs and the catalog's own `roles` hint — so the same
  // inputs always produce the same suggestion and nothing is fetched to compute
  // it.
  //
  // This only changes what is offered FIRST. Every character stays selectable
  // for every agent, and a character with no declared roles is never excluded —
  // it just does not get the role bonus (FR-65).
  function recommendedId(taken, list, role) {
    var characters = list || state.characters;
    if (!characters.length) return '';

    var used = {};
    (taken || []).forEach(function (id) {
      if (id) used[String(id)] = true;
    });
    var wanted = role ? String(role) : '';

    var best = null;
    var bestScore = -1;
    for (var i = 0; i < characters.length; i++) {
      var ch = characters[i];
      var roles = ch.roles || [];
      var matches = wanted && roles.indexOf(wanted) !== -1;
      // Unused is worth more than a role match: a duplicate identity is the
      // thing a user notices, a slightly-off affinity is not.
      var score = (used[ch.id] ? 0 : 2) + (matches ? 1 : 0);
      if (score > bestScore) {
        bestScore = score;
        best = ch;
      }
    }
    return best ? best.id : characters[0].id;
  }

  /* ---- rendering --------------------------------------------------------------- */

  function cardHTML(ch, isSelected, isRecommended) {
    var cls = 'char-card' + (isSelected ? ' is-selected' : '');
    return (
      '<button type="button" class="' +
      cls +
      '" role="radio" aria-checked="' +
      (isSelected ? 'true' : 'false') +
      '" data-char-id="' +
      esc(ch.id) +
      '">' +
      '<img class="char-card__art" src="' +
      esc(ch.assets.portrait) +
      '" alt="" width="72" height="72" loading="lazy" decoding="async">' +
      '<span class="char-card__body">' +
      '<span class="char-card__name">' +
      esc(ch.name) +
      '</span>' +
      '<span class="char-card__family">' +
      esc(ch.familyLabel) +
      (isRecommended ? ' · <span class="char-card__rec">Unused</span>' : '') +
      '</span>' +
      '<span class="char-card__purpose">' +
      esc(ch.purpose) +
      '</span>' +
      '</span>' +
      '</button>'
    );
  }

  function renderGrid() {
    var els = state.els;
    if (!els) return;

    if (!state.characters.length) {
      els.grid.innerHTML =
        '<p class="char-picker__empty">The character catalog is unavailable right now. ' +
        'Agents keep their generated identity until it loads.</p>';
      // Nothing to choose, so Choose must not look available.
      els.confirm.disabled = true;
      return;
    }
    if (!state.filtered.length) {
      els.grid.innerHTML = '<p class="char-picker__empty">No character matches that search.</p>';
      // The current selection may be filtered out of view, but it is still the
      // selection, so confirming stays valid.
      renderPreview();
      return;
    }

    var html = '';
    for (var i = 0; i < state.filtered.length; i++) {
      var ch = state.filtered[i];
      html += cardHTML(ch, ch.id === state.selectedId, ch.id === state.recommendedId);
    }
    els.grid.innerHTML = html;
    renderPreview();
  }

  // The preview is what FR-59 requires before a save: art, silhouette, prop,
  // idle behaviour, tone traits, and a sample line.
  function renderPreview() {
    var els = state.els;
    if (!els) return;
    var ch = byId(state.selectedId);

    if (!ch) {
      els.preview.innerHTML = '<p class="char-preview__empty">Pick a character to preview it.</p>';
      els.confirm.disabled = true;
      return;
    }
    els.confirm.disabled = false;

    els.preview.innerHTML =
      '<img class="char-preview__art" src="' +
      esc(ch.assets.portrait) +
      '" alt="" width="120" height="120" decoding="async">' +
      '<h4 class="char-preview__name">' +
      esc(ch.name) +
      '</h4>' +
      '<p class="char-preview__family">' +
      esc(ch.familyLabel) +
      '</p>' +
      '<dl class="char-preview__facts">' +
      fact('Silhouette', ch.silhouette) +
      fact('Signature prop', ch.prop) +
      fact('Idle', ch.idleBehavior) +
      fact('Tone', (ch.toneTraits || []).join(', ')) +
      '</dl>' +
      '<p class="char-preview__sample">“' +
      esc(ch.sampleLine) +
      '”</p>' +
      // The prop is identity, not capability. Saying so here stops a user
      // reading "tool belt" as "this agent has tools" (FR-97).
      '<p class="char-preview__note">Character art and props are identity only. ' +
      "They never change an agent's role, model, tools, or permissions.</p>";
  }

  function fact(label, value) {
    if (!value) return '';
    return '<dt>' + esc(label) + '</dt><dd>' + esc(value) + '</dd>';
  }

  function byId(id) {
    for (var i = 0; i < state.characters.length; i++) {
      if (state.characters[i].id === id) return state.characters[i];
    }
    return null;
  }

  /* ---- open / close -------------------------------------------------------------- */

  function buildShell() {
    var host = document.getElementById('charPickerHost');
    if (!host) return null;

    host.innerHTML =
      '<div class="char-picker" id="charPicker" role="dialog" aria-modal="true" ' +
      'aria-labelledby="charPickerTitle">' +
      '<header class="char-picker__head">' +
      '<h3 class="char-picker__title" id="charPickerTitle">Choose a character</h3>' +
      '<button type="button" class="char-picker__close" id="charPickerClose" ' +
      'aria-label="Close character picker">&times;</button>' +
      '</header>' +
      '<p class="char-picker__lede">A character is how this agent looks. Its name, role, ' +
      'model, tools, and permissions are unchanged.</p>' +
      '<div class="char-picker__controls">' +
      '<label class="visually-hidden" for="charPickerSearch">Search characters</label>' +
      '<input type="search" id="charPickerSearch" class="char-picker__search" ' +
      'placeholder="Search by name, family, or tone…" autocomplete="off">' +
      '<div class="char-picker__families" role="group" aria-label="Filter by family">' +
      '<button type="button" class="char-picker__family is-active" data-family="">All</button>' +
      '<button type="button" class="char-picker__family" data-family="resident">Residents</button>' +
      '<button type="button" class="char-picker__family" data-family="familiar">Familiars</button>' +
      '<button type="button" class="char-picker__family" data-family="construct">Constructs</button>' +
      '</div>' +
      '</div>' +
      '<div class="char-picker__main">' +
      '<div class="char-picker__grid" id="charPickerGrid" role="radiogroup" ' +
      'aria-label="Available characters"></div>' +
      '<aside class="char-preview" id="charPickerPreview"></aside>' +
      '</div>' +
      '<label class="char-picker__voice">' +
      '<input type="checkbox" id="charPickerVoice">' +
      '<span>' +
      "<strong>Use this character's voice style</strong>" +
      '<span class="char-picker__voice-note">Adjusts tone only — brevity and warmth. ' +
      'It never overrides your system prompt, task instructions, tools, ' +
      'permissions, or confirmation rules.</span>' +
      '</span>' +
      '</label>' +
      '<div class="char-picker__actions">' +
      '<button type="button" class="btn-ghost" id="charPickerCancel">Cancel</button>' +
      '<button type="button" class="btn-ghost" id="charPickerSkip">Skip for now</button>' +
      '<button type="button" class="btn-primary" id="charPickerConfirm">Choose character</button>' +
      '</div>' +
      '</div>' +
      '<div class="char-picker__backdrop" id="charPickerBackdrop"></div>';

    return {
      root: document.getElementById('charPicker'),
      backdrop: document.getElementById('charPickerBackdrop'),
      close: document.getElementById('charPickerClose'),
      search: document.getElementById('charPickerSearch'),
      families: host.querySelectorAll('.char-picker__family'),
      grid: document.getElementById('charPickerGrid'),
      preview: document.getElementById('charPickerPreview'),
      voice: document.getElementById('charPickerVoice'),
      cancel: document.getElementById('charPickerCancel'),
      skip: document.getElementById('charPickerSkip'),
      confirm: document.getElementById('charPickerConfirm')
    };
  }

  /**
   * open resolves with the user's decision and nothing else happens on its own.
   *
   *   { action: 'choose', catalogId, voiceEnabled }
   *   { action: 'skip' }        // explicitly no character
   *   { action: 'cancel' }      // leave whatever was there alone
   */
  function open(options) {
    var opts = options || {};
    if (state.open) return Promise.resolve({ action: 'cancel' });

    var catalog = window.CharacterCatalog;
    state.characters = catalog ? catalog.working() : [];
    state.family = '';
    state.voiceEnabled = !!opts.voiceEnabled;
    state.recommendedId = recommendedId(opts.taken, null, opts.role);
    // Pre-select what the agent already has, else the recommendation.
    state.selectedId = opts.selectedId || state.recommendedId;
    state.lastTrigger = opts.trigger || document.activeElement || null;

    var els = buildShell();
    if (!els) return Promise.resolve({ action: 'cancel' });
    state.els = els;
    state.open = true;

    els.voice.checked = state.voiceEnabled;
    if (opts.showSkip === false) els.skip.hidden = true;

    applyFilter();
    wire();

    // Focus the search box: typing is the fastest way through a grid.
    if (els.search && typeof els.search.focus === 'function') els.search.focus();

    return new Promise(function (resolve) {
      state.resolve = resolve;
    });
  }

  function finish(result) {
    if (!state.open) return;
    state.open = false;

    var host = document.getElementById('charPickerHost');
    if (host) host.innerHTML = '';

    var trigger = state.lastTrigger;
    state.lastTrigger = null;
    state.els = null;

    var resolve = state.resolve;
    state.resolve = null;

    // Focus returns to whatever opened the picker, so a keyboard user is not
    // dropped at the top of the page (FR-116).
    if (trigger && document.contains(trigger) && typeof trigger.focus === 'function') {
      trigger.focus();
    }
    if (resolve) resolve(result);
  }

  function wire() {
    var els = state.els;

    els.search.addEventListener('input', applyFilter);

    Array.prototype.forEach.call(els.families, function (btn) {
      btn.addEventListener('click', function () {
        state.family = btn.getAttribute('data-family') || '';
        Array.prototype.forEach.call(els.families, function (b) {
          b.classList.toggle('is-active', b === btn);
        });
        applyFilter();
      });
    });

    els.grid.addEventListener('click', function (event) {
      var card = event.target.closest('[data-char-id]');
      if (!card) return;
      state.selectedId = card.getAttribute('data-char-id');
      renderGrid();
    });

    els.voice.addEventListener('change', function () {
      state.voiceEnabled = !!els.voice.checked;
    });

    els.confirm.addEventListener('click', function () {
      if (!state.selectedId) return;
      finish({
        action: 'choose',
        catalogId: state.selectedId,
        voiceEnabled: state.voiceEnabled
      });
    });

    els.skip.addEventListener('click', function () {
      finish({ action: 'skip' });
    });

    els.cancel.addEventListener('click', cancel);
    els.close.addEventListener('click', cancel);
    els.backdrop.addEventListener('click', cancel);
    document.addEventListener('keydown', onKeydown);
  }

  function cancel() {
    finish({ action: 'cancel' });
  }

  function onKeydown(event) {
    if (!state.open) return;
    if (event.key === 'Escape') {
      event.stopPropagation();
      cancel();
    }
  }

  var api = {
    open: open,
    isOpen: function () {
      return state.open;
    },
    // Test seams.
    _recommendedId: recommendedId,
    _haystack: haystack,
    _cancel: cancel
  };

  if (typeof window !== 'undefined') {
    window.CharacterPicker = api;
  }
})();
