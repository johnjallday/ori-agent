/*
 * Avatar Identity — the single frontend renderer for an agent's visual
 * identity (PRD FR66–FR78, extended by cozy-character-experience FR-66/FR-67).
 *
 * Priority is the agent's *explicit* display mode, then that mode's valid
 * asset, then the deterministic fallback. The mode is a stored choice, never
 * inferred from whichever field happens to be non-empty — that is what makes
 * trying a curated character reversible instead of destructive (FR-67).
 *
 *   character -> catalog portrait -> deterministic fallback
 *   uploaded  -> uploaded image   -> deterministic fallback
 *   fallback  -> deterministic fallback
 *
 * Records with no stored mode are legacy and keep the historical rule (uploaded
 * image if present, else fallback), so existing agents look exactly as they did
 * (FR-69).
 *
 * The deterministic fallback is derived synchronously from data the dashboard
 * list response already carries. No network request, no generation step, no new
 * persisted seed, no per-agent image file (FR98). Curated character data is
 * *passed in* by the caller rather than fetched here, so this module stays
 * synchronous and a slow catalog can never delay rendering a name or status
 * (FR-101).
 *
 * The fallback signature is a stable FNV-1a hash over the canonical agent name
 * plus its source, sliced into four independent dimensions — palette, motif,
 * orientation, and tone — so two agents sharing a role still look different
 * (FR69/FR70/FR71). Role contributes an accent ring and emblem only; it never
 * becomes the whole identity.
 *
 * Safety: every value that reaches CSS is either a token this module owns
 * (motif/turn/tone indices) or a hex colour re-serialised from validated input.
 * User-supplied metadata is never interpolated into executable CSS (PRD 7.2).
 *
 * Loaded as a classic deferred script so it is installed before the roster
 * controller runs; `window.AgentAvatar` is also the surface the unit tests
 * evaluate in a node:vm sandbox.
 */
(function () {
  'use strict';

  // Reviewed palettes. Every base is dark enough that #fff initials clear WCAG
  // AA (4.5:1) — the lowest ratio in this table is 9.3:1, and the gradient only
  // ever gets darker from the base, so the whole portrait is safe in both
  // themes (FR77). agent-avatar.test.js asserts this rather than trusting it.
  var PALETTES = [
    { id: 'indigo', base: '#2c2f7a' },
    { id: 'teal', base: '#0b4f52' },
    { id: 'violet', base: '#4a2472' },
    { id: 'emerald', base: '#0b4d33' },
    { id: 'bronze', base: '#5a3a06' },
    { id: 'rose', base: '#6b1236' },
    { id: 'azure', base: '#123a7a' },
    { id: 'crimson', base: '#6d1418' },
    { id: 'cyan', base: '#0a4257' },
    { id: 'olive', base: '#33470b' },
    { id: 'magenta', base: '#5e1157' },
    { id: 'slate', base: '#263243' },
    { id: 'copper', base: '#5d2c10' },
    { id: 'forest', base: '#14401f' },
    { id: 'plum', base: '#401a4d' },
    { id: 'steel', base: '#1f3b4d' }
  ];

  // Built-in CLI agents and the system assistant read as system infrastructure
  // rather than as library identities, but still differ from one another via
  // motif, orientation, tone, and initials (FR73).
  var SYSTEM_PALETTES = [
    { id: 'graphite', base: '#2b3038' },
    { id: 'gunmetal', base: '#22303a' },
    { id: 'pewter', base: '#333a42' },
    { id: 'iron', base: '#2a2f36' }
  ];

  // Motif tokens only. The geometry itself lives in agents-roster.css keyed on
  // [data-aa-motif], so nothing derived from agent data becomes CSS.
  var MOTIFS = ['rings', 'grid', 'bars', 'orbit', 'chevron', 'dots', 'prism', 'wave'];

  // Gradient angles paired with the motif rotation quarter-turns.
  var ANGLES = [155, 205, 335, 25];

  // Tone varies how far the gradient end is pushed toward black and how present
  // the motif is. It never lightens the base, so palette contrast is preserved.
  var TONES = [
    { mix: 0.45, motif: 0.2 },
    { mix: 0.55, motif: 0.14 },
    { mix: 0.62, motif: 0.26 },
    { mix: 0.5, motif: 0.1 },
    { mix: 0.68, motif: 0.18 },
    { mix: 0.58, motif: 0.24 }
  ];

  var DARK_INK = '#0b0f14';
  var LIGHT_INK = '#ffffff';
  var GRADIENT_FLOOR = '#080b0f';

  var HEX_RE = /^#(?:[0-9a-f]{3}|[0-9a-f]{6})$/i;
  var EMBLEM_RE = /^[a-z0-9-]+$/;

  /* ---- hashing ------------------------------------------------------------- */

  // FNV-1a over UTF-16 code units. Deterministic, dependency-free, and far
  // better distributed than the character-sum it replaces: names differing only
  // by letter order no longer collapse onto the same treatment (PRD 7.2).
  function fnv1a(str, seed) {
    var h = typeof seed === 'number' ? seed >>> 0 : 0x811c9dc5;
    for (var i = 0; i < str.length; i++) {
      h ^= str.charCodeAt(i);
      h = (h + ((h << 1) + (h << 4) + (h << 7) + (h << 8) + (h << 24))) >>> 0;
    }
    return h >>> 0;
  }

  // Independent slice of one seed hash. Re-hashing with a per-dimension salt
  // decorrelates the dimensions, so palette and motif do not move together.
  function pick(hash, salt, length) {
    return fnv1a(salt, hash) % length;
  }

  // The seed is the agent's identity, not its mutable configuration: renaming an
  // agent is meant to change its look, editing its model is not (FR68/FR69).
  function seedFor(input) {
    var name = String((input && input.name) || '').trim();
    var source = String((input && input.source) || '')
      .trim()
      .toLowerCase();
    return name + ' ' + source;
  }

  /* ---- colour -------------------------------------------------------------- */

  function normalizeHex(value) {
    var hex = String(value || '').trim();
    if (!HEX_RE.test(hex)) return '';
    if (hex.length === 4) {
      return ('#' + hex[1] + hex[1] + hex[2] + hex[2] + hex[3] + hex[3]).toLowerCase();
    }
    return hex.toLowerCase();
  }

  function toRgb(hex) {
    return [
      parseInt(hex.slice(1, 3), 16),
      parseInt(hex.slice(3, 5), 16),
      parseInt(hex.slice(5, 7), 16)
    ];
  }

  function toHex(rgb) {
    return (
      '#' +
      rgb
        .map(function (v) {
          var c = Math.max(0, Math.min(255, Math.round(v)));
          return (c < 16 ? '0' : '') + c.toString(16);
        })
        .join('')
    );
  }

  function mixHex(from, to, ratio) {
    var a = toRgb(from);
    var b = toRgb(to);
    var t = Math.max(0, Math.min(1, ratio));
    return toHex([a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t, a[2] + (b[2] - a[2]) * t]);
  }

  function channelLuminance(value) {
    var s = value / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  }

  function relativeLuminance(hex) {
    var rgb = toRgb(hex);
    return (
      0.2126 * channelLuminance(rgb[0]) +
      0.7152 * channelLuminance(rgb[1]) +
      0.0722 * channelLuminance(rgb[2])
    );
  }

  function contrastRatio(a, b) {
    var la = relativeLuminance(a);
    var lb = relativeLuminance(b);
    var hi = Math.max(la, lb);
    var lo = Math.min(la, lb);
    return (hi + 0.05) / (lo + 0.05);
  }

  // Initials must stay legible on whatever base we end up with, including a
  // custom avatar_color the user picked without contrast in mind (FR72/FR77).
  function inkFor(base) {
    return contrastRatio(LIGHT_INK, base) >= contrastRatio(DARK_INK, base) ? LIGHT_INK : DARK_INK;
  }

  /* ---- initials ------------------------------------------------------------ */

  // Readable initials: first letters of the first and last meaningful words, or
  // the first two characters of a single word. Punctuation-only or empty names
  // fall back to a neutral marker rather than rendering blank (FR70).
  function initialsFor(name) {
    var words = String(name || '')
      .replace(/[_\-/\\.]+/g, ' ')
      .split(/\s+/)
      .map(function (w) {
        return w.replace(/[^\p{L}\p{N}]/gu, '');
      })
      .filter(function (w) {
        return w.length > 0;
      });
    if (words.length === 0) return '?';
    if (words.length === 1) {
      return words[0].slice(0, 2).toUpperCase();
    }
    return (words[0][0] + words[words.length - 1][0]).toUpperCase();
  }

  /* ---- signature ----------------------------------------------------------- */

  // isSystemIdentity mirrors the roster's own "permanent agent" rule so the two
  // never disagree about which agents are built-in.
  function isSystemIdentity(input) {
    if (!input) return false;
    if (input.builtIn === true) return true;
    var source = String(input.source || '').toLowerCase();
    var role = String(input.role || '').toLowerCase();
    if (source === 'cli' || role === 'cli_agent') return true;
    var name = String(input.name || '')
      .trim()
      .toLowerCase();
    // Legacy names stay recognized so an install that has not yet run the
    // startup rename migration still renders the system treatment.
    return name === 'workspace manager' || name === 'ori' || name === '__assistant__';
  }

  // signature is the deterministic description of one agent's fallback identity.
  // Given the same input it returns the same object every time, in every theme,
  // in every view — that property is what the unit tests pin down.
  function signature(input) {
    var system = isSystemIdentity(input);
    var table = system ? SYSTEM_PALETTES : PALETTES;
    var hash = fnv1a(seedFor(input));

    var paletteIndex = pick(hash, 'palette', table.length);
    var motifIndex = pick(hash, 'motif', MOTIFS.length);
    var turn = pick(hash, 'turn', ANGLES.length);
    var toneIndex = pick(hash, 'tone', TONES.length);

    var palette = table[paletteIndex];
    var tone = TONES[toneIndex];

    // A valid custom colour replaces the palette's base but keeps every other
    // dimension, so the motif, frame, and initials all survive (FR72).
    var custom = normalizeHex(input && input.avatarColor);
    var base = custom || palette.base;
    var deep = mixHex(base, GRADIENT_FLOOR, tone.mix);

    var roleAccent = normalizeHex(input && input.roleAccent);
    var emblem = String((input && input.roleEmblem) || '').trim();
    if (!EMBLEM_RE.test(emblem)) emblem = '';

    return {
      hash: hash,
      system: system,
      paletteId: custom ? 'custom' : palette.id,
      paletteIndex: paletteIndex,
      motif: MOTIFS[motifIndex],
      motifIndex: motifIndex,
      turn: turn,
      angle: ANGLES[turn],
      toneIndex: toneIndex,
      motifAlpha: tone.motif,
      base: base,
      deep: deep,
      ink: inkFor(base),
      custom: custom !== '',
      // Role is an accent on the frame and emblem only (FR71). With no catalog
      // role the portrait simply has no accent ring — it never falls back to
      // some other agent's colour.
      accent: roleAccent,
      emblem: emblem,
      initials: initialsFor(input && input.name)
    };
  }

  /* ---- markup -------------------------------------------------------------- */

  function esc(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  // Named sizes rather than an inline --aa-size: an inline custom property beats
  // any stylesheet rule, which would stop a denser view or a breakpoint from
  // resizing the portrait. The class lets CSS stay in charge of geometry.
  var SIZES = { sm: 54, md: 72, lg: 88 };

  function sizeClass(size) {
    var px = Number(size) > 0 ? Number(size) : SIZES.md;
    if (px <= SIZES.sm) return 'agent-avatar--sm';
    if (px >= SIZES.lg) return 'agent-avatar--lg';
    return 'agent-avatar--md';
  }

  function styleFor(sig) {
    var parts = [
      '--aa-base:' + sig.base,
      '--aa-deep:' + sig.deep,
      '--aa-ink:' + sig.ink,
      '--aa-angle:' + sig.angle + 'deg',
      '--aa-motif-alpha:' + sig.motifAlpha
    ];
    if (sig.accent) parts.push('--aa-accent:' + sig.accent);
    return parts.join(';');
  }

  // Seed inputs travel with the element so a failed uploaded image can be
  // replaced in place by this agent's own fallback, with no re-fetch and no
  // broken-image control ever being shown (FR74).
  function seedAttrs(input) {
    var attrs =
      ' data-aa-name="' +
      esc((input && input.name) || '') +
      '" data-aa-source="' +
      esc((input && input.source) || '') +
      '"';
    if (input && input.role) attrs += ' data-aa-role="' + esc(input.role) + '"';
    if (input && input.roleAccent) attrs += ' data-aa-accent="' + esc(input.roleAccent) + '"';
    if (input && input.roleEmblem) attrs += ' data-aa-emblem="' + esc(input.roleEmblem) + '"';
    if (input && input.avatarColor) attrs += ' data-aa-color="' + esc(input.avatarColor) + '"';
    if (input && input.builtIn) attrs += ' data-aa-builtin="1"';
    return attrs;
  }

  function inputFromElement(el) {
    var d = el && el.dataset ? el.dataset : {};
    return {
      name: d.aaName || '',
      source: d.aaSource || '',
      role: d.aaRole || '',
      roleAccent: d.aaAccent || '',
      roleEmblem: d.aaEmblem || '',
      avatarColor: d.aaColor || '',
      builtIn: d.aaBuiltin === '1'
    };
  }

  // The visual identity is decorative: the agent's name, role, source, and
  // status are all present as text beside it, so repeating them here would only
  // make screen-reader output noisier (FR76).
  function innerFallbackHTML(sig) {
    var emblem = sig.emblem
      ? '<span class="agent-avatar__emblem"><i class="bi bi-' + sig.emblem + '"></i></span>'
      : '';
    return (
      '<span class="agent-avatar__motif"></span>' +
      '<span class="agent-avatar__initials">' +
      esc(sig.initials) +
      '</span>' +
      emblem
    );
  }

  function fallbackHTML(input, options) {
    var opts = options || {};
    var sig = signature(input);
    var cls =
      'agent-avatar agent-avatar--fallback ' +
      sizeClass(opts.size) +
      (opts.className ? ' ' + opts.className : '');
    return (
      '<span class="' +
      esc(cls) +
      '"' +
      (opts.id ? ' id="' + esc(opts.id) + '"' : '') +
      ' style="' +
      esc(styleFor(sig)) +
      '" data-aa-motif="' +
      esc(sig.motif) +
      '" data-aa-turn="' +
      sig.turn +
      '" data-aa-tone="' +
      sig.toneIndex +
      '"' +
      (sig.system ? ' data-aa-system="1"' : '') +
      seedAttrs(input) +
      ' aria-hidden="true">' +
      innerFallbackHTML(sig) +
      '</span>'
    );
  }

  function imageHTML(input, options) {
    var opts = options || {};
    var size = Number(opts.size) > 0 ? Number(opts.size) : SIZES.md;
    var cls =
      'agent-avatar agent-avatar--image ' +
      sizeClass(opts.size) +
      (opts.className ? ' ' + opts.className : '');
    // width/height give the box a 1:1 aspect ratio before the bitmap arrives,
    // and lazy+async decoding keeps a large collection interactive while images
    // stream in (FR97). The rendered size comes from the size class, so a view
    // or breakpoint can resize the portrait without re-rendering.
    var dims = ' width="' + size + '" height="' + size + '"';
    return (
      '<span class="' +
      esc(cls) +
      '"' +
      (opts.id ? ' id="' + esc(opts.id) + '"' : '') +
      seedAttrs(input) +
      ' aria-hidden="true">' +
      '<img class="agent-avatar__img" src="/avatars/' +
      esc(input.avatarImage) +
      '" alt=""' +
      dims +
      ' loading="lazy" decoding="async">' +
      '</span>'
    );
  }

  /* ---- identity resolution -------------------------------------------------- */

  var MODE_FALLBACK = 'fallback';
  var MODE_UPLOADED = 'uploaded';
  var MODE_CHARACTER = 'character';

  function isValidMode(mode) {
    return mode === MODE_FALLBACK || mode === MODE_UPLOADED || mode === MODE_CHARACTER;
  }

  // A catalog entry is only usable if it actually carries the asset we need.
  // A withdrawn or half-populated entry resolves to the fallback rather than
  // rendering a broken portrait (FR-74).
  function characterPortrait(character) {
    var assets = (character && character.assets) || {};
    var portrait = String(assets.portrait || '').trim();
    return portrait;
  }

  // resolve reports what will actually render and why.
  //
  // `requested` is what the agent asked for and `mode` is what it gets; when
  // they differ, `reason` names the gap. Surfaces use that to explain the
  // current state honestly and to offer a re-selection when a character has
  // gone missing, instead of silently showing initials (FR-74/FR-124).
  function resolve(input) {
    var stored = String((input && input.displayMode) || '').trim();
    var image = String((input && input.avatarImage) || '').trim();
    var character = (input && input.character) || null;

    var requested;
    if (isValidMode(stored)) {
      requested = stored;
    } else {
      // Legacy record, or an unrecognized stored mode: fall back to the
      // historical field-presence rule.
      requested = image ? MODE_UPLOADED : MODE_FALLBACK;
    }

    var result = {
      requested: requested,
      mode: MODE_FALLBACK,
      reason: isValidMode(stored) ? 'ok' : 'legacy',
      character: null,
      portrait: '',
      avatarImage: ''
    };

    if (requested === MODE_CHARACTER) {
      var portrait = characterPortrait(character);
      if (portrait) {
        result.mode = MODE_CHARACTER;
        result.character = character;
        result.portrait = portrait;
      } else {
        // The agent still *has* a character choice; the catalog just cannot
        // supply it right now. Naming that separately from "never chose one"
        // is what lets the Inspector offer a reselect action.
        result.reason = character ? 'character-asset-missing' : 'character-missing';
      }
      return result;
    }

    if (requested === MODE_UPLOADED) {
      if (image) {
        result.mode = MODE_UPLOADED;
        result.avatarImage = image;
      } else {
        result.reason = 'upload-missing';
      }
      return result;
    }

    return result;
  }

  // markup is the one entry point every surface uses (FR66), so Home, the
  // Gallery, and the Inspector cannot disagree about what an agent looks like
  // (FR-99).
  function markup(input, options) {
    var res = resolve(input);
    if (res.mode === MODE_CHARACTER) {
      return characterHTML(input, res, options);
    }
    if (res.mode === MODE_UPLOADED) {
      return imageHTML(Object.assign({}, input, { avatarImage: res.avatarImage }), options);
    }
    return fallbackHTML(input, options);
  }

  // characterHTML renders a curated portrait. The <img> keeps the same reserved
  // box and lazy/async decoding as an uploaded avatar, so a slow or failed
  // portrait cannot shift the layout or block the controls beside it
  // (FR-101/FR-120–FR-123). A failure is caught by the same delegated listener
  // that handles uploads and swaps in this agent's own fallback.
  function characterHTML(input, res, options) {
    var opts = options || {};
    var size = Number(opts.size) > 0 ? Number(opts.size) : SIZES.md;
    var cls =
      'agent-avatar agent-avatar--character ' +
      sizeClass(opts.size) +
      (opts.className ? ' ' + opts.className : '');

    var character = res.character || {};
    var palette = character.palette || {};
    var accent = normalizeHex(palette.accent) || normalizeHex(palette.base);
    var style = accent ? ' style="--aa-accent:' + esc(accent) + '"' : '';

    return (
      '<span class="' +
      esc(cls) +
      '"' +
      (opts.id ? ' id="' + esc(opts.id) + '"' : '') +
      style +
      ' data-aa-character="' +
      esc(character.id || '') +
      '"' +
      seedAttrs(input) +
      ' aria-hidden="true">' +
      '<img class="agent-avatar__img agent-avatar__portrait" src="' +
      esc(res.portrait) +
      '" alt=""' +
      ' width="' +
      size +
      '" height="' +
      size +
      '" loading="lazy" decoding="async">' +
      '</span>'
    );
  }

  // replaceWithFallback converts an already-rendered image avatar into this
  // agent's fallback in place, keeping the reserved box and dropping the failed
  // <img> entirely so no broken-image glyph is ever painted (FR74).
  function replaceWithFallback(host) {
    if (!host || host.dataset.aaFallback === '1') return;
    var sig = signature(inputFromElement(host));
    host.dataset.aaFallback = '1';
    // The size class is left untouched, so the replacement occupies exactly the
    // box the image reserved and the collection does not reflow. Both the
    // uploaded-image and curated-character variants degrade to the same
    // deterministic identity (FR-14/FR-74).
    host.className = host.className
      .replace('agent-avatar--image', 'agent-avatar--fallback')
      .replace('agent-avatar--character', 'agent-avatar--fallback');
    if (typeof host.removeAttribute === 'function') host.removeAttribute('data-aa-character');
    host.setAttribute('style', styleFor(sig));
    host.setAttribute('data-aa-motif', sig.motif);
    host.setAttribute('data-aa-turn', String(sig.turn));
    host.setAttribute('data-aa-tone', String(sig.toneIndex));
    if (sig.system) host.setAttribute('data-aa-system', '1');
    host.innerHTML = innerFallbackHTML(sig);
  }

  // One capture-phase listener covers every avatar on the page, however many
  // times the collection is re-rendered (PRD 7.5 event delegation).
  function installImageFallback(doc) {
    if (!doc || !doc.addEventListener) return;
    doc.addEventListener(
      'error',
      function (event) {
        var target = event && event.target;
        if (!target || target.nodeName !== 'IMG') return;
        if (!target.classList || !target.classList.contains('agent-avatar__img')) return;
        replaceWithFallback(target.parentNode);
      },
      true
    );
  }

  var api = {
    markup: markup,
    resolve: resolve,
    fallbackHTML: fallbackHTML,
    imageHTML: imageHTML,
    characterHTML: characterHTML,
    signature: signature,
    MODES: {
      FALLBACK: MODE_FALLBACK,
      UPLOADED: MODE_UPLOADED,
      CHARACTER: MODE_CHARACTER
    },
    initialsFor: initialsFor,
    replaceWithFallback: replaceWithFallback,
    installImageFallback: installImageFallback,
    // Exposed so the unit tests can assert contrast and dimension counts over
    // the real tables rather than a copy that could drift.
    PALETTES: PALETTES,
    SYSTEM_PALETTES: SYSTEM_PALETTES,
    MOTIFS: MOTIFS,
    TONES: TONES,
    ANGLES: ANGLES,
    contrastRatio: contrastRatio,
    normalizeHex: normalizeHex,
    SIZES: SIZES
  };

  if (typeof window !== 'undefined') {
    window.AgentAvatar = api;
    installImageFallback(typeof document !== 'undefined' ? document : null);
  }
})();
