/*
 * Character Catalog — the browser's read-only view of the curated character
 * library served by GET /api/characters.
 *
 * Loading is deliberately non-blocking (FR-101): a surface renders agent names,
 * status, and management controls immediately, and asks this module to fill in
 * portraits when — or if — the catalog arrives. Nothing here can delay a name
 * appearing, and a catalog that never loads degrades every agent to the
 * deterministic Avatar Identity fallback rather than an empty card (FR-14).
 *
 * The module holds no mutation path. Assigning a character is an agent PATCH
 * validated server-side against the same catalog; this is only ever a lookup.
 *
 * Loaded as a classic deferred script so `window.CharacterCatalog` is installed
 * before the page controllers run.
 */
(function () {
  'use strict';

  var ENDPOINT = '/api/characters';

  var state = {
    status: 'idle', // idle | loading | ready | error
    byId: Object.create(null),
    working: [],
    guide: null,
    reservedGuideId: '',
    version: '',
    promise: null
  };

  var listeners = [];

  function notify() {
    var snapshot = listeners.slice();
    for (var i = 0; i < snapshot.length; i++) {
      try {
        snapshot[i](api);
      } catch (err) {
        // One bad subscriber must not stop the others from repainting.
        if (typeof console !== 'undefined' && console.warn) {
          console.warn('CharacterCatalog listener failed', err);
        }
      }
    }
  }

  // Only the shapes the renderer actually uses are kept, and each is coerced to
  // the expected type. A malformed entry is dropped rather than half-rendered.
  function normalize(raw) {
    if (!raw || typeof raw !== 'object') return null;
    var id = String(raw.id || '').trim();
    if (!id) return null;

    var assets = raw.assets || {};
    var palette = raw.palette || {};
    return {
      id: id,
      entryVersion: Number(raw.entry_version) || 0,
      kind: String(raw.kind || '').trim(),
      name: String(raw.name || '').trim() || id,
      family: String(raw.family || '').trim(),
      familyLabel: String(raw.family_label || '').trim(),
      purpose: String(raw.purpose || '').trim(),
      description: String(raw.description || '').trim(),
      silhouette: String(raw.silhouette || '').trim(),
      prop: String(raw.signature_prop || '').trim(),
      idleBehavior: String(raw.idle_behavior || '').trim(),
      // The roles a character reads well beside. This is only an ordering hint
      // for recommend(); it never restricts who may wear the character. It was
      // dropped here until now, so role matching worked in the picker's unit
      // tests (which pass a fixture in) and never in the running app.
      roles: (Array.isArray(raw.roles) ? raw.roles : [])
        .map(function (role) {
          return String(role || '').trim();
        })
        .filter(Boolean),
      // Nothing tone-shaped is read here, because the catalog no longer serves
      // it: a character changes how an agent looks and nothing else (FR-22).
      palette: {
        base: String(palette.base || '').trim(),
        accent: String(palette.accent || '').trim(),
        ink: String(palette.ink || '').trim()
      },
      assets: {
        portrait: String(assets.portrait || '').trim(),
        sprite: String(assets.sprite || '').trim(),
        static: String(assets.static || '').trim()
      }
    };
  }

  function ingest(payload) {
    var byId = Object.create(null);
    var working = [];

    var list = (payload && payload.characters) || [];
    for (var i = 0; i < list.length; i++) {
      var entry = normalize(list[i]);
      if (!entry) continue;
      byId[entry.id] = entry;
      working.push(entry);
    }

    var guide = normalize(payload && payload.guide);
    if (guide) byId[guide.id] = guide;

    state.byId = byId;
    state.working = working;
    state.guide = guide;
    state.reservedGuideId = String((payload && payload.reserved_guide_id) || '').trim();
    state.version = String((payload && payload.catalog_version) || '').trim();
    state.status = 'ready';
  }

  // load resolves even on failure. Callers render regardless; the catalog is an
  // enhancement, never a precondition (FR-124).
  function load() {
    if (state.promise) return state.promise;
    state.status = 'loading';
    state.promise = fetch(ENDPOINT, { headers: { Accept: 'application/json' } })
      .then(function (r) {
        if (!r.ok) throw new Error('characters ' + r.status);
        return r.json();
      })
      .then(function (payload) {
        ingest(payload);
        notify();
        return api;
      })
      .catch(function (err) {
        state.status = 'error';
        if (typeof console !== 'undefined' && console.debug) {
          console.debug('Character catalog unavailable; using fallback identities', err);
        }
        notify();
        return api;
      });
    return state.promise;
  }

  // get returns null for an unknown, withdrawn, or not-yet-loaded character, so
  // the avatar resolver treats it as a missing entry and falls back (FR-74).
  function get(id) {
    var key = String(id == null ? '' : id).trim();
    if (!key) return null;
    return state.byId[key] || null;
  }

  // isReserved lets a surface refuse to offer Ori's identity as a choice even
  // before the server rejects it, keeping the UI honest rather than relying on
  // a round trip to discover the option was never valid (FR-19/FR-71).
  function isReserved(id) {
    var key = String(id == null ? '' : id).trim();
    return key !== '' && key === state.reservedGuideId;
  }

  // assetFor picks the right rendering of a character.
  //
  // `sprite` is the animated small form and `static` its motionless twin, so a
  // reduced-motion user gets an equally meaningful image rather than a frozen
  // first frame (FR-120). Portraits carry no animation, so they need no variant.
  function assetFor(character, variant, prefersReducedMotion) {
    if (!character || !character.assets) return '';
    var assets = character.assets;
    if (variant === 'portrait') return assets.portrait || '';
    if (variant === 'sprite') {
      if (prefersReducedMotion) return assets.static || assets.sprite || '';
      return assets.sprite || assets.static || '';
    }
    if (variant === 'static') return assets.static || '';
    return assets.portrait || '';
  }

  function prefersReducedMotion() {
    if (typeof window === 'undefined' || !window.matchMedia) return false;
    try {
      return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    } catch (err) {
      return false;
    }
  }

  // recommend names the character to offer an agent first.
  //
  // Three deterministic preferences, in order: the character fewest agents
  // already wear, then one whose `roles` hint includes this agent's role, then
  // catalog order. "Fewest" rather than "unused" so that a roster larger than
  // the catalog keeps spreading out instead of handing every later Commander
  // the same face; with nothing taken twice the two rules agree.
  //
  // `taken` may repeat an id — each repeat is one more agent wearing it. This
  // only decides what is offered FIRST: every character stays selectable for
  // every agent, and one with no declared roles is never excluded (FR-65).
  function recommend(taken, role, list) {
    var characters = list || state.working;
    if (!characters || !characters.length) return '';

    var uses = Object.create(null);
    (taken || []).forEach(function (id) {
      if (id) uses[String(id)] = (uses[String(id)] || 0) + 1;
    });
    var wanted = role ? String(role) : '';

    var best = null;
    var bestUses = Infinity;
    var bestMatches = false;
    for (var i = 0; i < characters.length; i++) {
      var ch = characters[i];
      var count = uses[ch.id] || 0;
      var matches = !!wanted && (ch.roles || []).indexOf(wanted) !== -1;
      if (count < bestUses || (count === bestUses && matches && !bestMatches)) {
        best = ch;
        bestUses = count;
        bestMatches = matches;
      }
    }
    return best ? best.id : '';
  }

  function onChange(fn) {
    if (typeof fn !== 'function') return function () {};
    listeners.push(fn);
    return function () {
      var i = listeners.indexOf(fn);
      if (i >= 0) listeners.splice(i, 1);
    };
  }

  var api = {
    load: load,
    get: get,
    isReserved: isReserved,
    assetFor: assetFor,
    prefersReducedMotion: prefersReducedMotion,
    recommend: recommend,
    onChange: onChange,
    working: function () {
      return state.working.slice();
    },
    guide: function () {
      return state.guide;
    },
    status: function () {
      return state.status;
    },
    reservedGuideId: function () {
      return state.reservedGuideId;
    },
    version: function () {
      return state.version;
    },
    // Test seam: lets unit tests drive the module without a network stub.
    _ingest: ingest,
    _reset: function () {
      state.status = 'idle';
      state.byId = Object.create(null);
      state.working = [];
      state.guide = null;
      state.reservedGuideId = '';
      state.version = '';
      state.promise = null;
      listeners.length = 0;
    }
  };

  if (typeof window !== 'undefined') {
    window.CharacterCatalog = api;
  }
})();
