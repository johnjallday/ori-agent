// Tests for agent-appearance.js — the shared Appearance editor
// (unified-agent-appearance FR-26 through FR-48, FR-96 through FR-100).
//
// The module is a classic deferred script, so it is evaluated in a node:vm
// sandbox with a minimal DOM, mirroring agent-avatar.test.js. The DOM fake is
// deliberately small: the editor only needs innerHTML, querySelector, and
// listeners, so anything richer would be testing jsdom rather than this module.
//   node --test internal/web/static/js/modules/agent-appearance.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const avatarSource = readFileSync(new URL('./agent-avatar.js', import.meta.url), 'utf8');
const editorSource = readFileSync(new URL('./agent-appearance.js', import.meta.url), 'utf8');

/**
 * A DOM stand-in just rich enough for the editor: it renders markup into a host
 * as a flat element list keyed by id, and dispatches listeners.
 */
function makeHost() {
  const listeners = new Map();
  const elements = new Map();

  function parse(html) {
    elements.clear();
    listeners.clear();
    // Ids are module-generated tokens, so a regex over id="..." is enough to
    // model "the element exists and can be interacted with". The trailing
    // attributes are scanned for `disabled` because several assertions turn on
    // whether a control is offered or merely present.
    const idRe = /id="([^"]+)"([^>]*)>/g;
    let match;
    while ((match = idRe.exec(html))) {
      const el = makeEl(match[1]);
      el.disabled = /\sdisabled/.test(match[2]);
      elements.set(match[1], el);
    }
    // Radios carry a value rather than an id we care about; index them by value.
    const radioRe = /<input type="radio"[^>]*value="([^"]+)"([^>]*)>/g;
    while ((match = radioRe.exec(html))) {
      const el = makeEl('radio:' + match[1]);
      el.value = match[1];
      el.checked = /\schecked/.test(match[2]);
      el.disabled = /\sdisabled/.test(match[2]);
      el.type = 'radio';
      elements.set('radio:' + match[1], el);
    }
  }

  function makeEl(id) {
    return {
      id,
      value: '',
      checked: false,
      disabled: false,
      files: [],
      innerHTML: '',
      className: '',
      attributes: {},
      setAttribute(k, v) {
        this.attributes[k] = v;
      },
      getAttribute(k) {
        return this.attributes[k];
      },
      addEventListener(type, handler) {
        const key = id + ':' + type;
        if (!listeners.has(key)) listeners.set(key, []);
        listeners.get(key).push(handler);
      },
      fire(type) {
        (listeners.get(id + ':' + type) || []).forEach(h => h.call(this, { target: this }));
      }
    };
  }

  const host = {
    html: '',
    set innerHTML(value) {
      this.html = value;
      parse(value);
    },
    get innerHTML() {
      return this.html;
    },
    querySelector(selector) {
      if (selector.startsWith('#')) return elements.get(selector.slice(1)) || null;
      if (selector === '.appearance-editor__preview-frame') {
        return elements.get('__preview') || makeEl('__preview');
      }
      return null;
    },
    querySelectorAll(selector) {
      if (selector === 'input[type="radio"]') {
        return Array.from(elements.values()).filter(el => el.type === 'radio');
      }
      return [];
    },
    get: id => elements.get(id),
    radio: mode => elements.get('radio:' + mode)
  };
  return host;
}

function load() {
  const sandbox = {
    window: {},
    document: { addEventListener() {} },
    Math,
    Object,
    Number,
    String,
    Array,
    Boolean,
    parseInt,
    JSON,
    Promise
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(avatarSource, sandbox);
  vm.runInContext(editorSource, sandbox);
  // The catalog is passed in by real hosts; a stub keeps character rendering
  // synchronous and predictable here.
  sandbox.window.CharacterCatalog = {
    get: id =>
      id === 'sable'
        ? { id, name: 'Field Scout', assets: { portrait: '/p.webp' }, palette: {} }
        : null
  };
  return sandbox.window;
}

// Objects built inside the vm realm carry that realm's prototype, which
// assert/strict treats as a mismatch, so patches are compared as plain data.
function assertPatch(actual, expected, message) {
  assert.deepEqual(JSON.parse(JSON.stringify(actual)), expected, message);
}

function appearance(mode, extra = {}) {
  const out = { mode, generated: {} };
  if (extra.color) out.generated.color = extra.color;
  if (extra.image) out.uploaded = { image: extra.image };
  if (extra.catalogId) out.character = { catalog_id: extra.catalogId, catalog_version: 1 };
  return out;
}

/** An adapter that records calls and resolves with whatever the test supplies. */
function recordingAdapter(resolveWith) {
  const calls = [];
  const make = kind => arg => {
    calls.push({ kind, arg });
    return Promise.resolve(
      typeof resolveWith === 'function' ? resolveWith(kind, arg) : resolveWith
    );
  };
  return {
    calls,
    saveAppearance: make('save'),
    uploadImage: make('upload'),
    removeImage: make('remove')
  };
}

function mount(win, options = {}) {
  const host = makeHost();
  const editor = win.AgentAppearanceEditor.create({
    host,
    agent: { name: 'Atlas', source: 'user' },
    ...options
  });
  return { host, editor };
}

/* ---- the three-way choice (FR-27, FR-96) ---------------------------------- */

test('the editor renders one labelled radio group with all three sources', () => {
  const win = load();
  const { host } = mount(win);

  assert.match(host.innerHTML, /role="radiogroup"/);
  assert.match(host.innerHTML, /aria-label="Appearance source"/);
  for (const mode of ['generated', 'character', 'uploaded']) {
    assert.ok(host.radio(mode), `${mode} must be offered`);
  }
  // The section names itself programmatically, not only visually.
  assert.match(host.innerHTML, /<legend[^>]*>Appearance<\/legend>/);
});

test('a create host may explicitly limit modes when it cannot persist uploads yet', () => {
  const win = load();
  const { host } = mount(win, {
    mode: 'create',
    allowedModes: ['generated', 'character'],
    appearance: appearance('generated')
  });

  assert.ok(host.radio('generated'));
  assert.ok(host.radio('character'));
  assert.equal(host.radio('uploaded'), undefined);
  assert.doesNotMatch(host.innerHTML, /Upload an image/);
});

test('a source with nothing saved is offered but not selectable, and says why', () => {
  const win = load();
  const { host } = mount(win, { appearance: appearance('generated') });

  // Presenting all three and explaining the gap beats hiding two of them: the
  // user can see what is possible (FR-27/FR-100).
  assert.equal(host.radio('character').disabled, true);
  assert.equal(host.radio('uploaded').disabled, true);
  assert.match(host.innerHTML, /Choose a character to use this source\./);
  assert.match(host.innerHTML, /Upload an image to use this source\./);
});

test('a saved source becomes selectable', () => {
  const win = load();
  const { host } = mount(win, {
    appearance: appearance('generated', { catalogId: 'sable', image: 'a.png' })
  });
  assert.equal(host.radio('character').disabled, false);
  assert.equal(host.radio('uploaded').disabled, false);
});

/* ---- choosing is not deleting (FR-30, FR-35, FR-40) ------------------------ */

test('switching the requested source keeps every other source', () => {
  const win = load();
  const adapter = recordingAdapter((kind, patch) =>
    appearance(patch.mode || 'generated', { color: '#6d5dfc', catalogId: 'sable', image: 'a.png' })
  );
  const { host, editor } = mount(win, {
    appearance: appearance('uploaded', {
      color: '#6d5dfc',
      catalogId: 'sable',
      image: 'a.png'
    }),
    adapter
  });

  const radio = host.radio('character');
  radio.checked = true;
  radio.fire('change');

  // The patch names only the mode. Nothing else is sent, so nothing else can be
  // lost on the server either.
  assertPatch(adapter.calls[0].arg, { mode: 'character' });
  const staged = editor.appearance();
  assert.equal(staged.uploaded.image, 'a.png');
  assert.equal(staged.generated.color, '#6d5dfc');
});

test('removing a character returns to generated only when it was active', () => {
  const win = load();
  const activeAdapter = recordingAdapter(appearance('generated', { image: 'a.png' }));
  const active = mount(win, {
    appearance: appearance('character', { catalogId: 'sable', image: 'a.png' }),
    adapter: activeAdapter
  });
  active.host.get('appearance-character-remove').fire('click');
  assertPatch(activeAdapter.calls[0].arg, { character: null });
  assert.equal(active.editor.appearance().mode, 'generated');
  // The upload is untouched by a character removal (FR-35).
  assert.equal(active.editor.appearance().uploaded.image, 'a.png');

  const inactiveAdapter = recordingAdapter(appearance('uploaded', { image: 'a.png' }));
  const inactive = mount(win, {
    appearance: appearance('uploaded', { catalogId: 'sable', image: 'a.png' }),
    adapter: inactiveAdapter
  });
  inactive.host.get('appearance-character-remove').fire('click');
  assert.equal(inactive.editor.appearance().mode, 'uploaded');
});

test('removing an upload returns to generated only when it was active', () => {
  const win = load();
  const activeAdapter = recordingAdapter(appearance('generated', { catalogId: 'sable' }));
  const active = mount(win, {
    appearance: appearance('uploaded', { catalogId: 'sable', image: 'a.png' }),
    adapter: activeAdapter
  });
  active.host.get('appearance-upload-remove').fire('click');
  assert.equal(activeAdapter.calls[0].kind, 'remove');
  assert.equal(active.editor.appearance().mode, 'generated');
  // The character selection is untouched by an upload removal (FR-40).
  assert.equal(active.editor.appearance().character.catalog_id, 'sable');

  const inactiveAdapter = recordingAdapter(appearance('character', { catalogId: 'sable' }));
  const inactive = mount(win, {
    appearance: appearance('character', { catalogId: 'sable', image: 'a.png' }),
    adapter: inactiveAdapter
  });
  inactive.host.get('appearance-upload-remove').fire('click');
  assert.equal(inactive.editor.appearance().mode, 'character');
});

/* ---- colour (FR-31, FR-54) -------------------------------------------------- */

test('the colour reset sends an explicit null, not an omission', () => {
  const win = load();
  const adapter = recordingAdapter(appearance('generated'));
  const { host } = mount(win, {
    appearance: appearance('generated', { color: '#6d5dfc' }),
    adapter
  });

  host.get('appearance-color-reset').fire('click');
  // Omitting the key would mean "leave unchanged"; only an explicit null clears
  // the saved override.
  assertPatch(adapter.calls[0].arg, { generated: { color: null } });
});

test('reset is disabled when there is no override to reset', () => {
  const win = load();
  const { host } = mount(win, { appearance: appearance('generated') });
  assert.equal(host.get('appearance-color-reset').disabled, true);
});

test('an invalid colour is refused without touching the server', () => {
  const win = load();
  const adapter = recordingAdapter(appearance('generated'));
  const { host } = mount(win, { appearance: appearance('generated'), adapter });

  const input = host.get('appearance-color');
  input.value = 'not-a-colour';
  input.fire('change');

  assert.equal(adapter.calls.length, 0);
  assert.match(host.innerHTML, /not a valid colour/);
});

/* ---- staged versus saved (FR-41) -------------------------------------------- */

test('a failed mutation restores the last confirmed state', async () => {
  const win = load();
  const confirmed = appearance('generated', { color: '#111111', catalogId: 'sable' });
  const failing = {
    saveAppearance: () => Promise.reject(new Error('Server said no.')),
    uploadImage: () => Promise.reject(new Error('nope')),
    removeImage: () => Promise.reject(new Error('nope'))
  };
  const { host, editor } = mount(win, { appearance: confirmed, adapter: failing });

  const radio = host.radio('character');
  radio.checked = true;
  radio.fire('change');
  await new Promise(resolve => setTimeout(resolve, 0));

  // Staged reached for Character; the server refused, so the editor shows what
  // the server actually has — not what the user was reaching for.
  assert.equal(editor.appearance().mode, 'generated');
  assert.equal(editor.status(), 'error');
  assert.match(host.innerHTML, /Server said no\./);
});

test('a successful mutation adopts the server object, not the staged one', async () => {
  const win = load();
  // The client never knows the catalog version — the server assigns it from the
  // validated entry — so the confirmed state has to come from the response
  // rather than from what the client staged (FR-10/FR-59).
  const adapter = recordingAdapter({
    mode: 'character',
    generated: {},
    character: { catalog_id: 'sable', catalog_version: 7 }
  });
  const { host, editor } = mount(win, {
    appearance: appearance('generated', { catalogId: 'sable', image: 'a.png' }),
    adapter
  });

  const radio = host.radio('character');
  radio.checked = true;
  radio.fire('change');
  await new Promise(resolve => setTimeout(resolve, 0));

  assert.equal(editor.confirmed().character.catalog_version, 7);
  assert.equal(editor.appearance().mode, 'character');
  assert.equal(editor.status(), 'idle');
  // The response omitted the upload, so the confirmed state does too: the
  // server's object is adopted whole rather than merged into the staged one.
  assert.equal(editor.confirmed().uploaded, undefined);
});

/* ---- create staging (FR-45, FR-46, FR-57) ----------------------------------- */

test('a create request omits the uploaded image and the catalog version', () => {
  const win = load();
  const { editor } = mount(win, {
    mode: 'create',
    appearance: appearance('character', { color: '#6d5dfc', catalogId: 'sable', image: 'a.png' })
  });

  const request = editor.createRequest();
  assert.equal(request.mode, 'character');
  assert.equal(request.generated.color, '#6d5dfc');
  // Both are server-managed; sending either is a 400 (FR-55).
  assert.equal(request.uploaded, undefined);
  assert.equal(request.character.catalog_version, undefined);
  assert.equal(request.character.catalog_id, 'sable');
});

test('a create request never activates Upload, because the file is not sent yet', () => {
  const win = load();
  const { editor } = mount(win, {
    mode: 'create',
    appearance: appearance('uploaded', { image: 'a.png' })
  });
  // Activating Upload with no stored image is a guaranteed server rejection, so
  // the request asks for Generated and the upload follows separately (FR-57).
  assert.equal(editor.createRequest().mode, 'generated');
});

test('a create flow stages the file locally rather than uploading it', () => {
  const win = load();
  const adapter = recordingAdapter(appearance('uploaded', { image: 'a.png' }));
  const { host, editor } = mount(win, { mode: 'create', adapter });

  const file = { name: 'a.png', size: 1024 };
  const input = host.get('appearance-file');
  input.files = [file];
  input.fire('change');

  // Nothing was sent — there is no agent to send it to yet (FR-46).
  assert.equal(adapter.calls.length, 0);
  assert.equal(editor.pendingFile(), file);
  assert.equal(editor.appearance().mode, 'uploaded');
});

test('an oversize file is refused before any request', () => {
  const win = load();
  const adapter = recordingAdapter(appearance('generated'));
  const { host } = mount(win, { adapter });

  const input = host.get('appearance-file');
  input.files = [{ name: 'big.png', size: 6 * 1024 * 1024 }];
  input.fire('change');

  assert.equal(adapter.calls.length, 0);
  assert.match(host.innerHTML, /larger than 5 MB/);
});

/* ---- read-only hosts (FR-44) ------------------------------------------------ */

test('a read-only agent gets the explanation and no controls', () => {
  const win = load();
  const { host } = mount(win, {
    readOnly: true,
    appearance: appearance('generated', { catalogId: 'sable' })
  });

  assert.match(host.innerHTML, /built in, so its appearance is fixed/);
  // Offering controls the server would reject is worse than offering none.
  assert.equal(host.get('appearance-color'), undefined);
  assert.equal(host.get('appearance-character-choose'), undefined);
  assert.equal(host.get('appearance-file'), undefined);
  assert.equal(host.radio('generated').disabled, true);
});

/* ---- preview and labelling (FR-28, FR-99) ----------------------------------- */

test('the standalone preview names the source it is showing', () => {
  const win = load();
  const generated = mount(win, { appearance: appearance('generated') });
  assert.match(generated.host.innerHTML, /aria-label="Preview: generated appearance"/);

  const character = mount(win, {
    appearance: appearance('character', { catalogId: 'sable' })
  });
  assert.match(character.host.innerHTML, /aria-label="Preview: character art"/);

  const uploaded = mount(win, { appearance: appearance('uploaded', { image: 'a.png' }) });
  assert.match(uploaded.host.innerHTML, /aria-label="Preview: uploaded image"/);
});

test('an unavailable requested asset is explained in the preview label', () => {
  const win = load();
  // The saved choice is intact; only the art is missing, and the label says so
  // rather than implying the user lost their character (FR-84).
  const { host } = mount(win, {
    appearance: appearance('character', { catalogId: 'withdrawn' })
  });
  assert.match(host.innerHTML, /the saved character art is unavailable/);
});

test('the selected source is marked for assistive technology and visually', () => {
  const win = load();
  const { host } = mount(win, { appearance: appearance('character', { catalogId: 'sable' }) });
  assert.equal(host.radio('character').checked, true);
  // A class as well as the radio state, so selection does not depend on colour
  // alone (FR-100).
  assert.match(host.innerHTML, /appearance-source is-selected/);
});

/* ---- normalization ---------------------------------------------------------- */

test('normalize defaults to generated and drops junk', () => {
  const win = load();
  const normalize = win.AgentAppearanceEditor.normalize;

  assert.equal(normalize(null).mode, 'generated');
  assert.equal(normalize({ mode: 'fallback' }).mode, 'generated');
  assert.equal(normalize({ mode: 'hologram' }).mode, 'generated');
  assert.equal(normalize({ generated: { color: 'chartreuse' } }).generated.color, undefined);
  assert.equal(normalize({ generated: { color: '#ABC' } }).generated.color, '#aabbcc');
  assert.equal(normalize({ uploaded: { image: '   ' } }).uploaded, undefined);
  assert.equal(normalize({ character: { catalog_id: '' } }).character, undefined);
});
