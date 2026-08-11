/*
 * Agent Appearance — the single frontend renderer for an agent's visual
 * identity (unified-agent-appearance FR-80 through FR-84, FR-99 through FR-106).
 *
 * It consumes the canonical `appearance` object exactly as the API returns it:
 *
 *   { mode, generated: { color }, uploaded: { image }, character: { catalog_id } }
 *
 * and renders only the source named by `mode`. There is deliberately no
 * priority rule such as "an upload wins when present" — the active source is an
 * explicit saved choice, and inferring it from whichever field happens to be
 * populated is what made trying a character feel destructive in the old model
 * (FR-5/FR-82).
 *
 *   generated -> deterministic portrait (always renderable)
 *   character -> catalog portrait  -> falls back to generated
 *   uploaded  -> uploaded image    -> falls back to generated
 *
 * Falling back is a *runtime outcome*, never a saved mode. `resolve()` reports
 * the requested mode alongside the rendered one plus a machine-readable reason,
 * so an editor can explain "your character art is unavailable" instead of
 * silently showing initials and appearing to have lost the choice (FR-15/FR-84).
 *
 * The generated portrait is derived synchronously from data every list response
 * already carries: no network request, no generation step, no per-agent image
 * file. Curated character data is *passed in* by the caller rather than fetched
 * here, so this module stays synchronous and a slow catalog can never delay
 * rendering a name or status (FR-103).
 *
 * The generated signature is a stable FNV-1a hash over the agent name plus its
 * source, sliced into four independent dimensions — palette, motif,
 * orientation, and tone — so two agents sharing a role still look different.
 * Role contributes an accent ring and emblem only; it never becomes the whole
 * identity.
 *
 * Safety: every value that reaches CSS is either a token this module owns
 * (motif/turn/tone indices) or a hex colour re-serialised from validated input.
 * User-supplied data is never interpolated into executable CSS (FR-106).
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
  // colour override the user picked without contrast in mind (FR-101).
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

    // A valid colour override replaces the palette's base but keeps every other
    // dimension, so the motif, frame, and initials all survive (FR-6).
    //
    // `color` is accepted alongside the canonical object because
    // replaceWithGenerated rebuilds the signature from data attributes on an
    // element that no longer has the full appearance — the two callers agree on
    // this one field.
    var custom = normalizeHex((input && input.color) || appearanceOf(input).color);
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

  // Seed inputs travel with the element so a failed image can be replaced in
  // place by this agent's own generated portrait, with no re-fetch and no
  // broken-image glyph ever being painted (FR-83).
  function seedAttrs(input) {
    var appearance = appearanceOf(input);
    var attrs =
      ' data-aa-name="' +
      esc((input && input.name) || '') +
      '" data-aa-source="' +
      esc((input && input.source) || '') +
      '" data-aa-requested="' +
      esc(appearance.mode) +
      '"';
    if (input && input.role) attrs += ' data-aa-role="' + esc(input.role) + '"';
    if (input && input.roleAccent) attrs += ' data-aa-accent="' + esc(input.roleAccent) + '"';
    if (input && input.roleEmblem) attrs += ' data-aa-emblem="' + esc(input.roleEmblem) + '"';
    if (appearance.color) attrs += ' data-aa-color="' + esc(appearance.color) + '"';
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
      color: d.aaColor || '',
      builtIn: d.aaBuiltin === '1'
    };
  }

  // The avatar is decorative: the agent's name, role, source, and status are
  // all present as text beside it, so repeating them here would only make
  // screen-reader output noisier (FR-99).
  function innerGeneratedHTML(sig) {
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

  function generatedHTML(input, options) {
    var opts = options || {};
    var sig = signature(input);
    var cls =
      'agent-avatar agent-avatar--generated ' +
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
      innerGeneratedHTML(sig) +
      '</span>'
    );
  }

  // imageHTML renders an uploaded image. The filename is server-generated, but
  // it is still URL-encoded here rather than trusted: this module must be safe
  // to call with any input, including a record hand-edited on disk (FR-106).
  function imageHTML(input, options) {
    var opts = options || {};
    var size = Number(opts.size) > 0 ? Number(opts.size) : SIZES.md;
    var uploadedImage = String((input && input.uploadedImage) || appearanceOf(input).image);
    var cls =
      'agent-avatar agent-avatar--image ' +
      sizeClass(opts.size) +
      (opts.className ? ' ' + opts.className : '');
    // width/height give the box a 1:1 aspect ratio before the bitmap arrives,
    // and lazy+async decoding keeps a large collection interactive while images
    // stream in (FR-103/FR-105). The rendered size comes from the size class, so
    // a view or breakpoint can resize the portrait without re-rendering.
    var dims = ' width="' + size + '" height="' + size + '"';
    return (
      '<span class="' +
      esc(cls) +
      '"' +
      (opts.id ? ' id="' + esc(opts.id) + '"' : '') +
      seedAttrs(input) +
      ' aria-hidden="true">' +
      '<img class="agent-avatar__img" src="/avatars/' +
      encodeURIComponent(uploadedImage) +
      '" alt=""' +
      dims +
      ' loading="lazy" decoding="async">' +
      '</span>'
    );
  }

  /* ---- appearance resolution ------------------------------------------------ */

  var MODE_GENERATED = 'generated';
  var MODE_UPLOADED = 'uploaded';
  var MODE_CHARACTER = 'character';

  // Reason codes are part of the contract with editors, which use them to
  // explain a temporary problem without implying the saved choice was lost
  // (FR-84).
  var REASON_OK = 'ok';
  var REASON_UPLOAD_MISSING = 'upload-missing';
  var REASON_CHARACTER_MISSING = 'character-missing';
  var REASON_ASSET_LOAD_FAILED = 'asset-load-failed';

  function isValidMode(mode) {
    return mode === MODE_GENERATED || mode === MODE_UPLOADED || mode === MODE_CHARACTER;
  }

  // appearanceOf reads the canonical object off the input.
  //
  // An absent or unrecognized mode resolves to Generated rather than to a
  // field-presence guess. Generated is the source that can always render, so it
  // is the only safe default (FR-13).
  function appearanceOf(input) {
    var appearance = (input && input.appearance) || {};
    var generated = appearance.generated || {};
    var uploaded = appearance.uploaded || {};
    var character = appearance.character || {};
    var mode = String(appearance.mode || '').trim();
    return {
      mode: isValidMode(mode) ? mode : MODE_GENERATED,
      color: normalizeHex(generated.color),
      image: String(uploaded.image || '').trim(),
      catalogId: String(character.catalog_id || '').trim()
    };
  }

  // A catalog entry is only usable if it actually carries the asset we need. A
  // withdrawn or half-populated entry resolves to Generated rather than
  // rendering a broken portrait (FR-83).
  function characterPortrait(character) {
    var assets = (character && character.assets) || {};
    return String(assets.portrait || '').trim();
  }

  // resolve reports what will actually render and why.
  //
  // `requested` is the saved mode and `mode` is what can be shown right now.
  // When they differ, `reason` names the gap — and `requested` deliberately
  // keeps its original value, because a missing asset must not look like the
  // user's choice changed (FR-84).
  function resolve(input) {
    var appearance = appearanceOf(input);
    var character = (input && input.character) || null;

    var result = {
      requested: appearance.mode,
      mode: MODE_GENERATED,
      reason: REASON_OK,
      character: null,
      portrait: '',
      uploadedImage: '',
      color: appearance.color,
      catalogId: appearance.catalogId
    };

    if (appearance.mode === MODE_CHARACTER) {
      var portrait = characterPortrait(character);
      if (portrait) {
        result.mode = MODE_CHARACTER;
        result.character = character;
        result.portrait = portrait;
      } else {
        // The agent still *has* a character choice; the catalog just cannot
        // supply it right now. Saying so is what lets an editor offer a
        // re-selection instead of showing an unexplained revert.
        result.reason = REASON_CHARACTER_MISSING;
      }
      return result;
    }

    if (appearance.mode === MODE_UPLOADED) {
      if (appearance.image) {
        result.mode = MODE_UPLOADED;
        result.uploadedImage = appearance.image;
      } else {
        result.reason = REASON_UPLOAD_MISSING;
      }
      return result;
    }

    return result;
  }

  // markup is the one entry point every surface uses, so the Gallery, the
  // Inspector, a workspace row, and a chat header cannot disagree about what an
  // agent looks like (FR-80).
  function markup(input, options) {
    var res = resolve(input);
    if (res.mode === MODE_CHARACTER) {
      return characterHTML(input, res, options);
    }
    if (res.mode === MODE_UPLOADED) {
      return imageHTML(Object.assign({}, input, { uploadedImage: res.uploadedImage }), options);
    }
    return generatedHTML(input, options);
  }

  // characterHTML renders a curated portrait. The <img> keeps the same reserved
  // box and lazy/async decoding as an uploaded image, so a slow or failed
  // portrait cannot shift the layout or block the controls beside it
  // (FR-103). A failure is caught by the same delegated listener that handles
  // uploads and swaps in this agent's own generated portrait.
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

  // replaceWithGenerated converts an already-rendered image avatar into this
  // agent's generated portrait in place, keeping the reserved box and dropping
  // the failed <img> entirely so no broken-image glyph is ever painted (FR-83).
  //
  // `data-aa-requested` is deliberately left alone. The saved mode has not
  // changed — only what could be displayed right now — and an editor reads that
  // attribute to explain the gap instead of implying the choice was lost
  // (FR-84).
  function replaceWithGenerated(host) {
    if (!host || host.dataset.aaFallback === '1') return;
    var sig = signature(inputFromElement(host));
    host.dataset.aaFallback = '1';
    host.dataset.aaReason = REASON_ASSET_LOAD_FAILED;
    // The size class is left untouched, so the replacement occupies exactly the
    // box the image reserved and the collection does not reflow. Uploaded and
    // curated-character avatars degrade to the same deterministic portrait.
    host.className = host.className
      .replace('agent-avatar--image', 'agent-avatar--generated')
      .replace('agent-avatar--character', 'agent-avatar--generated');
    if (typeof host.removeAttribute === 'function') host.removeAttribute('data-aa-character');
    host.setAttribute('style', styleFor(sig));
    host.setAttribute('data-aa-motif', sig.motif);
    host.setAttribute('data-aa-turn', String(sig.turn));
    host.setAttribute('data-aa-tone', String(sig.toneIndex));
    if (sig.system) host.setAttribute('data-aa-system', '1');
    host.innerHTML = innerGeneratedHTML(sig);
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
        replaceWithGenerated(target.parentNode);
      },
      true
    );
  }

  var api = {
    markup: markup,
    resolve: resolve,
    generatedHTML: generatedHTML,
    imageHTML: imageHTML,
    characterHTML: characterHTML,
    signature: signature,
    MODES: {
      GENERATED: MODE_GENERATED,
      UPLOADED: MODE_UPLOADED,
      CHARACTER: MODE_CHARACTER
    },
    REASONS: {
      OK: REASON_OK,
      UPLOAD_MISSING: REASON_UPLOAD_MISSING,
      CHARACTER_MISSING: REASON_CHARACTER_MISSING,
      ASSET_LOAD_FAILED: REASON_ASSET_LOAD_FAILED
    },
    initialsFor: initialsFor,
    replaceWithGenerated: replaceWithGenerated,
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
