/*
 * Agent Appearance editor — the one control every surface uses to choose an
 * agent's visual source (unified-agent-appearance FR-26 through FR-48).
 *
 * Before this, each host grew its own arrangement: a colour input here, an
 * upload control there, a character button somewhere else, each with its own
 * idea of what "saved" meant. That is how a roster and a detail page ended up
 * disagreeing about the same agent. This module owns the whole section — the
 * three-way choice, the preview, the per-source actions, and the saving/error
 * lifecycle — so a host supplies data and a persistence adapter and nothing
 * else.
 *
 * Two rules the whole design hangs on:
 *
 *   - Choosing a source is not deleting one. The radio group only changes which
 *     source is *requested*; the saved colour, image, and character selection
 *     all survive until the user presses an explicit Remove (FR-30/FR-35/FR-40).
 *   - Staged is not saved. The preview updates immediately, but nothing is
 *     marked saved until the server returns the canonical appearance, and a
 *     failure restores the last state the server actually confirmed (FR-41).
 *
 * Two host shapes are supported through one adapter interface:
 *
 *   - `edit`   — the agent exists; every action is a server mutation.
 *   - `create` — the agent does not exist yet; the choice is staged locally and
 *     handed to the host at submit time. An upload staged here keeps its File
 *     until after the agent is created, because there is nowhere to put it
 *     before that (FR-46).
 *
 * Loaded as a classic deferred script, like agent-avatar.js, and evaluated in a
 * node:vm sandbox by its unit tests.
 */
(function () {
  'use strict';

  var MODES = { GENERATED: 'generated', CHARACTER: 'character', UPLOADED: 'uploaded' };

  // Accepted upload types, kept in step with the server's content-sniffed
  // allowlist. This is a convenience filter in the file dialog, never a
  // security boundary — the server sniffs the bytes regardless (FR-63).
  var ACCEPT = 'image/png,image/jpeg,image/gif,image/webp';
  var MAX_UPLOAD_BYTES = 5 * 1024 * 1024;

  function esc(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  /* ---- canonical state ------------------------------------------------------- */

  // normalize accepts whatever the API returned and produces the canonical
  // object the renderer expects, defaulting to Generated.
  //
  // Defaulting rather than inferring from a populated field is the whole point:
  // Generated is the source that can always render, and inference is what made
  // switching feel destructive in the old model (FR-5/FR-13).
  function normalize(raw) {
    var appearance = raw || {};
    var mode = String(appearance.mode || '').trim();
    if (mode !== MODES.CHARACTER && mode !== MODES.UPLOADED) mode = MODES.GENERATED;

    var out = { mode: mode, generated: {} };
    var color = window.AgentAvatar
      ? window.AgentAvatar.normalizeHex((appearance.generated && appearance.generated.color) || '')
      : '';
    if (color) out.generated.color = color;

    var image = String((appearance.uploaded && appearance.uploaded.image) || '').trim();
    if (image) out.uploaded = { image: image };

    var catalogId = String((appearance.character && appearance.character.catalog_id) || '').trim();
    if (catalogId) {
      out.character = {
        catalog_id: catalogId,
        catalog_version: (appearance.character && appearance.character.catalog_version) || 0
      };
    }
    return out;
  }

  function clone(appearance) {
    return normalize(appearance);
  }

  function hasCharacter(appearance) {
    return !!(appearance && appearance.character && appearance.character.catalog_id);
  }

  function hasUpload(appearance) {
    return !!(appearance && appearance.uploaded && appearance.uploaded.image);
  }

  /* ---- editor ---------------------------------------------------------------- */

  function create(options) {
    var opts = options || {};
    var host = opts.host;
    if (!host) return null;

    var state = {
      // The last appearance the server confirmed. Every failure path restores
      // this rather than whatever the user was reaching for (FR-41).
      confirmed: normalize(opts.appearance),
      staged: normalize(opts.appearance),
      // A create-time upload held locally until the agent exists (FR-46).
      pendingFile: null,
      pendingFileURL: '',
      mode: opts.mode === 'create' ? 'create' : 'edit',
      readOnly: !!opts.readOnly,
      status: 'idle',
      message: '',
      messageKind: 'info'
    };

    var adapter = opts.adapter || {};
    var agent = opts.agent || {};
    var idPrefix = opts.idPrefix || 'appearance';

    function id(suffix) {
      return idPrefix + '-' + suffix;
    }

    /* ---- preview ------------------------------------------------------------ */

    // The preview is the shared renderer, given the staged appearance. Using the
    // same code path as every read-only surface is what makes "what you see is
    // what you will get" true rather than approximately true (FR-28/FR-80).
    function previewHTML() {
      if (!window.AgentAvatar) return '';
      var catalogId = state.staged.character && state.staged.character.catalog_id;
      // A staged create-time file has no server URL yet, so it previews from a
      // local object URL. Nothing else in the app ever sees that URL.
      var appearance = state.staged;
      if (state.pendingFileURL && state.staged.mode === MODES.UPLOADED) {
        appearance = clone(state.staged);
        appearance.uploaded = { image: state.pendingFileURL };
      }
      return window.AgentAvatar.markup(
        {
          name: agent.name || '',
          source: agent.source || 'user',
          role: agent.role || '',
          roleAccent: agent.roleAccent || '',
          roleEmblem: agent.roleEmblem || '',
          builtIn: !!agent.builtIn,
          appearance: appearance,
          character:
            catalogId && window.CharacterCatalog ? window.CharacterCatalog.get(catalogId) : null
        },
        { size: 88, className: 'appearance-editor__portrait' }
      );
    }

    // The rendered source, named for the accessible label. A standalone preview
    // has to say what it is showing, unlike an inline avatar sitting beside the
    // agent's own name (FR-99).
    function previewLabel() {
      if (!window.AgentAvatar) return 'Appearance preview';
      var res = window.AgentAvatar.resolve({
        name: agent.name || '',
        appearance: state.staged,
        character:
          hasCharacter(state.staged) && window.CharacterCatalog
            ? window.CharacterCatalog.get(state.staged.character.catalog_id)
            : null
      });
      if (res.mode === MODES.CHARACTER) return 'Preview: character art';
      if (res.mode === MODES.UPLOADED) return 'Preview: uploaded image';
      if (res.reason === 'character-missing') {
        return 'Preview: generated appearance — the saved character art is unavailable';
      }
      if (res.reason === 'upload-missing') {
        return 'Preview: generated appearance — the saved image is unavailable';
      }
      return 'Preview: generated appearance';
    }

    /* ---- markup ------------------------------------------------------------- */

    function sourceCard(mode, title, description, available, unavailableNote, controls) {
      var selected = state.staged.mode === mode;
      var disabled = state.readOnly || !available;
      var cls = 'appearance-source';
      if (selected) cls += ' is-selected';
      if (!available) cls += ' is-unavailable';
      return (
        '<div class="' +
        cls +
        '">' +
        '<label class="appearance-source__choice">' +
        '<input type="radio" name="' +
        esc(id('mode')) +
        '" value="' +
        esc(mode) +
        '" id="' +
        esc(id('mode-' + mode)) +
        '"' +
        (selected ? ' checked' : '') +
        (disabled ? ' disabled' : '') +
        '>' +
        '<span class="appearance-source__title">' +
        esc(title) +
        '</span>' +
        '</label>' +
        '<p class="appearance-source__desc">' +
        esc(description) +
        '</p>' +
        // The unavailable state is carried by text, not only by a colour or a
        // dimmed control (FR-100).
        (available
          ? ''
          : '<p class="appearance-source__unavailable">' + esc(unavailableNote) + '</p>') +
        (state.readOnly ? '' : '<div class="appearance-source__actions">' + controls + '</div>') +
        '</div>'
      );
    }

    function generatedControls() {
      var color = (state.staged.generated && state.staged.generated.color) || '';
      var swatch = color || opts.defaultColor || '#4f46e5';
      return (
        '<label class="appearance-color">' +
        '<span class="appearance-color__label">Color</span>' +
        '<input type="color" id="' +
        esc(id('color')) +
        '" value="' +
        esc(swatch) +
        '">' +
        '</label>' +
        // Reset is not a removal of anything the user can lose: it hands the
        // colour back to the deterministic algorithm (FR-31).
        '<button type="button" class="appearance-btn" id="' +
        esc(id('color-reset')) +
        '"' +
        (color ? '' : ' disabled') +
        '>Reset to generated</button>'
      );
    }

    function characterControls() {
      var chosen = hasCharacter(state.staged);
      var entry =
        chosen && window.CharacterCatalog
          ? window.CharacterCatalog.get(state.staged.character.catalog_id)
          : null;
      return (
        (entry
          ? '<p class="appearance-source__selection">Character art: ' + esc(entry.name) + '</p>'
          : '') +
        '<button type="button" class="appearance-btn" id="' +
        esc(id('character-choose')) +
        '">' +
        (chosen ? 'Change character' : 'Choose character') +
        '</button>' +
        // Explicitly destructive wording, reserved for the one action that
        // actually discards data (FR-30/design 6.3).
        (chosen
          ? '<button type="button" class="appearance-btn" id="' +
            esc(id('character-remove')) +
            '">Remove character selection</button>'
          : '')
      );
    }

    function uploadControls() {
      var uploaded = hasUpload(state.staged) || !!state.pendingFile;
      return (
        '<label class="appearance-upload">' +
        '<span class="visually-hidden">' +
        (uploaded ? 'Replace uploaded image' : 'Upload an image') +
        '</span>' +
        '<input type="file" id="' +
        esc(id('file')) +
        '" accept="' +
        ACCEPT +
        '">' +
        '</label>' +
        '<p class="appearance-upload__help">PNG, JPEG, GIF, or WebP, up to 5&nbsp;MB.' +
        (uploaded ? ' Uploading a new image replaces the current one.' : '') +
        '</p>' +
        (uploaded
          ? '<button type="button" class="appearance-btn" id="' +
            esc(id('upload-remove')) +
            '">Remove uploaded image</button>'
          : '')
      );
    }

    // Repaints only the preview box. A create flow follows the name field as it
    // is typed — the generated portrait is seeded from the name — and a full
    // re-render would take focus out of the field the user is in.
    function renderPreview() {
      var frame = host.querySelector('.appearance-editor__preview-frame');
      if (!frame) return;
      frame.setAttribute('aria-label', previewLabel());
      frame.innerHTML = previewHTML();
    }

    function render() {
      var characterAvailable = hasCharacter(state.staged);
      var uploadAvailable = hasUpload(state.staged) || !!state.pendingFile;

      host.innerHTML =
        '<fieldset class="appearance-editor" id="' +
        esc(id('root')) +
        '">' +
        '<legend class="appearance-editor__legend">Appearance</legend>' +
        '<div class="appearance-editor__preview">' +
        '<span class="appearance-editor__preview-frame" role="img" aria-label="' +
        esc(previewLabel()) +
        '">' +
        previewHTML() +
        '</span>' +
        '</div>' +
        // A radiogroup rather than three loose radios, so the group has one
        // programmatic label and arrow keys move within it (FR-96).
        '<div class="appearance-editor__sources" role="radiogroup" aria-label="Appearance source">' +
        sourceCard(
          MODES.GENERATED,
          'Generated',
          "Ori's generated portrait, derived from the agent's name.",
          true,
          '',
          generatedControls()
        ) +
        sourceCard(
          MODES.CHARACTER,
          'Character',
          'Curated character art. Appearance only — this does not change how the agent works.',
          characterAvailable,
          'Choose a character to use this source.',
          characterControls()
        ) +
        sourceCard(
          MODES.UPLOADED,
          'Upload',
          'An image you supply.',
          uploadAvailable,
          'Upload an image to use this source.',
          uploadControls()
        ) +
        '</div>' +
        (state.readOnly
          ? '<p class="appearance-editor__readonly">This agent is built in, so its appearance is fixed and cannot be changed here.</p>'
          : '') +
        '<p class="appearance-editor__status" id="' +
        esc(id('status')) +
        '" role="status" aria-live="polite">' +
        esc(state.message) +
        '</p>' +
        '</fieldset>';

      var statusEl = host.querySelector('#' + cssId(id('status')));
      if (statusEl) statusEl.className = 'appearance-editor__status is-' + state.messageKind;

      wire();
    }

    // Ids here are module-generated tokens, but querySelector still needs them
    // escaped for the general case where a host supplies its own prefix.
    function cssId(value) {
      if (window.CSS && typeof window.CSS.escape === 'function') return window.CSS.escape(value);
      return String(value).replace(/[^a-zA-Z0-9_-]/g, '\\$&');
    }

    function byId(suffix) {
      return host.querySelector('#' + cssId(id(suffix)));
    }

    /* ---- wiring ------------------------------------------------------------- */

    function wire() {
      if (state.readOnly) return;

      var radios = host.querySelectorAll('input[type="radio"]');
      Array.prototype.forEach.call(radios, function (radio) {
        radio.addEventListener('change', function () {
          if (!radio.checked) return;
          selectMode(radio.value);
        });
      });

      var color = byId('color');
      if (color) {
        color.addEventListener('change', function () {
          setColor(color.value);
        });
      }
      var reset = byId('color-reset');
      if (reset) reset.addEventListener('click', resetColor);

      var choose = byId('character-choose');
      if (choose) choose.addEventListener('click', chooseCharacter);
      var removeCharacter = byId('character-remove');
      if (removeCharacter) removeCharacter.addEventListener('click', clearCharacter);

      var file = byId('file');
      if (file) {
        file.addEventListener('change', function () {
          stageUpload(file.files && file.files[0]);
        });
      }
      var removeUpload = byId('upload-remove');
      if (removeUpload) removeUpload.addEventListener('click', clearUpload);
    }

    /* ---- operations --------------------------------------------------------- */

    // Selecting a source changes only which one is requested. Nothing is
    // deleted, which is why switching back later needs no rebuild (FR-30).
    function selectMode(mode) {
      if (mode !== MODES.GENERATED && mode !== MODES.CHARACTER && mode !== MODES.UPLOADED) return;
      if (mode === MODES.CHARACTER && !hasCharacter(state.staged)) return;
      if (mode === MODES.UPLOADED && !hasUpload(state.staged) && !state.pendingFile) return;
      state.staged.mode = mode;
      commit({ mode: mode });
    }

    function setColor(value) {
      var normalized = window.AgentAvatar ? window.AgentAvatar.normalizeHex(value) : '';
      if (!normalized) {
        fail('That is not a valid colour.');
        return;
      }
      state.staged.generated = { color: normalized };
      commit({ generated: { color: normalized } });
    }

    function resetColor() {
      state.staged.generated = {};
      // Explicit null is the documented reset; omitting the key would mean
      // "leave unchanged" (FR-54).
      commit({ generated: { color: null } });
    }

    function chooseCharacter() {
      if (!window.CharacterPicker) return;
      var trigger = byId('character-choose');
      window.CharacterPicker.open({
        trigger: trigger,
        selectedId: hasCharacter(state.staged) ? state.staged.character.catalog_id : '',
        taken: typeof opts.takenCharacterIds === 'function' ? opts.takenCharacterIds() : [],
        role: agent.role || '',
        showSkip: false
      }).then(function (result) {
        if (!result || result.action !== 'choose') return;
        // Only the id travels. The version is server-assigned from the
        // validated entry, so a client that could choose it could claim an art
        // revision that does not exist (FR-10/FR-55).
        state.staged.character = { catalog_id: result.catalogId, catalog_version: 0 };
        state.staged.mode = MODES.CHARACTER;
        commit({ character: { catalog_id: result.catalogId } });
      });
    }

    function clearCharacter() {
      var wasActive = state.staged.mode === MODES.CHARACTER;
      delete state.staged.character;
      // Back to Generated only when Character was the source actually being
      // rendered; removing an inactive selection leaves the active mode and
      // every other source alone (FR-33/FR-34/FR-35).
      if (wasActive) state.staged.mode = MODES.GENERATED;
      commit({ character: null });
    }

    function stageUpload(file) {
      if (!file) return;
      if (file.size > MAX_UPLOAD_BYTES) {
        fail('That file is larger than 5 MB.');
        return;
      }
      releasePendingURL();
      state.pendingFile = file;
      if (window.URL && typeof window.URL.createObjectURL === 'function') {
        state.pendingFileURL = window.URL.createObjectURL(file);
      }
      // A successful upload activates Upload in the same operation, so the
      // staged state says so immediately and the preview matches (FR-36).
      state.staged.mode = MODES.UPLOADED;

      if (state.mode === 'create') {
        // Nowhere to put the bytes until the agent exists. The host collects the
        // File at submit time and uploads it after creation (FR-46).
        render();
        notify();
        return;
      }
      run(function () {
        return adapter.uploadImage(file);
      }, 'Image uploaded.');
    }

    function clearUpload() {
      var wasActive = state.staged.mode === MODES.UPLOADED;
      releasePendingURL();
      state.pendingFile = null;
      delete state.staged.uploaded;
      if (wasActive) state.staged.mode = MODES.GENERATED;

      if (state.mode === 'create') {
        render();
        notify();
        return;
      }
      run(function () {
        return adapter.removeImage();
      }, 'Image removed.');
    }

    // Object URLs are a real leak in a long-lived roster: every staged file
    // holds its bytes until revoked (FR-106 hygiene).
    function releasePendingURL() {
      if (state.pendingFileURL && window.URL && typeof window.URL.revokeObjectURL === 'function') {
        window.URL.revokeObjectURL(state.pendingFileURL);
      }
      state.pendingFileURL = '';
    }

    /* ---- persistence -------------------------------------------------------- */

    // commit sends one appearance patch, or stages it in a create flow.
    function commit(patch) {
      if (state.mode === 'create') {
        render();
        notify();
        return;
      }
      run(function () {
        return adapter.saveAppearance(patch);
      }, 'Saved.');
    }

    // run is the single mutation lifecycle: staged preview immediately, saved
    // only on the canonical server response, last-confirmed state restored on
    // failure (FR-41).
    function run(operation, successMessage) {
      state.status = 'saving';
      state.message = 'Saving…';
      state.messageKind = 'info';
      render();

      var promise;
      try {
        promise = operation();
      } catch (err) {
        rollback(err && err.message);
        return;
      }
      if (!promise || typeof promise.then !== 'function') {
        rollback('This surface cannot save appearance changes.');
        return;
      }

      promise.then(
        function (appearance) {
          // Adopt the server's object rather than the local staged one: only
          // the response carries the assigned catalog version and the stored
          // filename (FR-59).
          state.confirmed = normalize(appearance);
          state.staged = clone(state.confirmed);
          releasePendingURL();
          state.pendingFile = null;
          state.status = 'idle';
          state.message = successMessage;
          state.messageKind = 'success';
          render();
          notify();
        },
        function (err) {
          rollback(err && err.message);
        }
      );
    }

    function rollback(message) {
      state.staged = clone(state.confirmed);
      releasePendingURL();
      state.pendingFile = null;
      state.status = 'error';
      state.message = message || 'That change could not be saved.';
      state.messageKind = 'error';
      render();
      notify();
    }

    function fail(message) {
      state.status = 'error';
      state.message = message;
      state.messageKind = 'error';
      render();
    }

    function notify() {
      if (typeof opts.onChange === 'function') opts.onChange(api);
    }

    /* ---- public surface ----------------------------------------------------- */

    var api = {
      render: render,
      // The staged appearance as a create request would send it: no uploaded
      // image (the server owns that) and no catalog version (server-assigned).
      createRequest: function () {
        var request = { mode: state.staged.mode, generated: {} };
        if (state.staged.generated && state.staged.generated.color) {
          request.generated.color = state.staged.generated.color;
        }
        if (hasCharacter(state.staged)) {
          request.character = { catalog_id: state.staged.character.catalog_id };
        }
        // A create request cannot activate Upload: the file has not been sent
        // yet, and the server would reject a mode with no stored image (FR-57).
        if (request.mode === MODES.UPLOADED) request.mode = MODES.GENERATED;
        return request;
      },
      // The File a create flow must upload after the agent exists (FR-46).
      pendingFile: function () {
        return state.pendingFile;
      },
      // A create flow's agent does not exist yet, so its name and role can still
      // change while the editor is open. Both feed the generated portrait, so
      // both repaint the preview — and only the preview.
      setAgentName: function (value) {
        agent.name = String(value || '');
        renderPreview();
      },
      setAgentRole: function (value) {
        agent.role = String(value || '');
        renderPreview();
      },
      appearance: function () {
        return clone(state.staged);
      },
      confirmed: function () {
        return clone(state.confirmed);
      },
      // Adopt a server response as the new confirmed state, for a host that
      // performed the mutation itself.
      setConfirmed: function (appearance) {
        state.confirmed = normalize(appearance);
        state.staged = clone(state.confirmed);
        releasePendingURL();
        state.pendingFile = null;
        render();
      },
      setMessage: function (message, kind) {
        state.message = message || '';
        state.messageKind = kind || 'info';
        render();
      },
      status: function () {
        return state.status;
      },
      destroy: releasePendingURL
    };

    // The catalog does not fetch on its own — each surface asks for it, so a
    // page showing no characters pays nothing. The editor asks on the editor's
    // behalf rather than relying on its host to remember: forgetting left the
    // picker's grid empty and the preview on the generated portrait even for an
    // agent that had a character saved (FR-103).
    if (window.CharacterCatalog) {
      if (typeof window.CharacterCatalog.onChange === 'function') {
        window.CharacterCatalog.onChange(function () {
          // Only the preview needs repainting; re-rendering the whole editor
          // would drop focus if the catalog lands mid-interaction.
          renderPreview();
        });
      }
      if (typeof window.CharacterCatalog.load === 'function') window.CharacterCatalog.load();
    }

    render();
    return api;
  }

  var moduleApi = {
    create: create,
    normalize: normalize,
    MODES: MODES,
    ACCEPT: ACCEPT,
    MAX_UPLOAD_BYTES: MAX_UPLOAD_BYTES
  };

  if (typeof window !== 'undefined') window.AgentAppearanceEditor = moduleApi;
})();
