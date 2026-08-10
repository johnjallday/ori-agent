// Tests for character-picker.js — the shared chooser used by New Agent and the
// Inspector (cozy-character-experience FR-53–FR-59, FR-65, FR-95, FR-98).
//   node --test internal/web/static/js/modules/character-picker.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./character-picker.js', import.meta.url), 'utf8');

function character(overrides = {}) {
  return {
    id: 'research-archivist',
    name: 'Research Archivist',
    family: 'resident',
    familyLabel: 'Resident',
    purpose: 'Patient source-finder who makes uncertainty visible.',
    silhouette: 'Wide brow, feather cape',
    prop: 'Pocket ledger',
    idleBehavior: 'Straightens a stack of notes',
    roles: ['researcher', 'validator'],
    assets: { portrait: '/characters/research-archivist/portrait.svg' },
    ...overrides
  };
}

const CATALOG = [
  character(),
  character({
    id: 'product-builder',
    name: 'Product Builder',
    family: 'resident',
    familyLabel: 'Resident',
    purpose: 'Practical maker who favors narrow slices.',
    roles: ['specialist'],
    assets: { portrait: '/characters/product-builder/portrait.svg' }
  }),
  character({
    id: 'decision-strategist',
    name: 'Decision Strategist',
    family: 'familiar',
    familyLabel: 'Familiar',
    purpose: 'Sharp advisor who compares paths.',
    roles: ['analyzer', 'synthesizer'],
    assets: { portrait: '/characters/decision-strategist/portrait.svg' }
  })
];

// A DOM just rich enough for the picker: it builds its own markup into a host,
// so the fake needs element creation and querying, not a full document.
function makeDom() {
  const nodes = new Map();
  // Declared up front so makeEl's querySelectorAll can close over it.
  const families = [];

  function makeEl(id) {
    const el = {
      id,
      value: '',
      checked: false,
      disabled: false,
      hidden: false,
      innerHTML: '',
      textContent: '',
      focused: false,
      listeners: {},
      attributes: {},
      classList: {
        _set: new Set(),
        add(c) {
          this._set.add(c);
        },
        remove(c) {
          this._set.delete(c);
        },
        toggle(c, on) {
          if (on) this._set.add(c);
          else this._set.delete(c);
        },
        contains(c) {
          return this._set.has(c);
        }
      },
      addEventListener(type, fn) {
        (this.listeners[type] = this.listeners[type] || []).push(fn);
      },
      getAttribute(k) {
        return Object.prototype.hasOwnProperty.call(this.attributes, k) ? this.attributes[k] : null;
      },
      setAttribute(k, v) {
        this.attributes[k] = v;
      },
      focus() {
        this.focused = true;
      },
      // The picker asks its own host for the family buttons.
      querySelectorAll() {
        return families;
      },
      fire(type, event = {}) {
        (this.listeners[type] || []).forEach(fn => fn(event));
      }
    };
    return el;
  }

  const host = makeEl('charPickerHost');
  nodes.set('charPickerHost', host);

  for (const id of [
    'charPicker',
    'charPickerBackdrop',
    'charPickerClose',
    'charPickerSearch',
    'charPickerGrid',
    'charPickerPreview',
    'charPickerCancel',
    'charPickerSkip',
    'charPickerConfirm'
  ]) {
    nodes.set(id, makeEl(id));
  }

  families.push(
    Object.assign(makeEl('fam-all'), { attributes: { 'data-family': '' } }),
    Object.assign(makeEl('fam-res'), { attributes: { 'data-family': 'resident' } }),
    Object.assign(makeEl('fam-fam'), { attributes: { 'data-family': 'familiar' } }),
    Object.assign(makeEl('fam-con'), { attributes: { 'data-family': 'construct' } })
  );

  return {
    nodes,
    families,
    document: {
      getElementById: id => nodes.get(id) || null,
      querySelectorAll: () => families,
      addEventListener() {},
      contains: () => true,
      activeElement: null
    }
  };
}

function load({ catalog = CATALOG } = {}) {
  const dom = makeDom();
  const sandbox = {
    window: {
      CharacterCatalog: {
        working: () => catalog.slice(),
        get: id => catalog.find(c => c.id === id) || null
      }
    },
    document: dom.document,
    Object,
    String,
    Array,
    Promise,
    Boolean
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);
  return { picker: sandbox.window.CharacterPicker, dom, sandbox };
}

/* ---- recommendation (FR-65) --------------------------------------------------- */

test('an unused character is recommended first', () => {
  const { picker } = load();
  const rec = picker._recommendedId(['research-archivist'], CATALOG);
  assert.notEqual(rec, 'research-archivist');
  assert.equal(rec, 'product-builder');
});

test('recommendation falls back to the first when everything is taken', () => {
  const { picker } = load();
  const all = CATALOG.map(c => c.id);
  // Reuse is allowed; the recommendation just stops being able to avoid it.
  assert.equal(picker._recommendedId(all, CATALOG), 'research-archivist');
});

test('an empty taken-list recommends the first character', () => {
  const { picker } = load();
  assert.equal(picker._recommendedId([], CATALOG), 'research-archivist');
  assert.equal(picker._recommendedId(null, CATALOG), 'research-archivist');
});

test('a role-matching character is recommended over an earlier non-matching one', () => {
  const { picker } = load();
  // Research Archivist is first in the list and unused, so it would win on
  // position alone; the analyst role is what moves the strategist ahead.
  assert.equal(picker._recommendedId([], CATALOG, 'analyzer'), 'decision-strategist');
  assert.equal(picker._recommendedId([], CATALOG, 'specialist'), 'product-builder');
});

test('being unused outranks matching the role', () => {
  const { picker } = load();
  // The strategist matches "analyzer" but is already assigned. A duplicate
  // identity is what a user notices; a slightly-off affinity is not.
  const rec = picker._recommendedId(['decision-strategist'], CATALOG, 'analyzer');
  assert.equal(rec, 'research-archivist');
});

test('an unknown role falls back to plain unused-first ordering', () => {
  const { picker } = load();
  assert.equal(picker._recommendedId([], CATALOG, 'not-a-role'), 'research-archivist');
  assert.equal(picker._recommendedId([], CATALOG, ''), 'research-archivist');
});

test('a character with no declared roles is still recommendable', () => {
  const { picker } = load();
  const noRoles = [character({ id: 'plain-one', roles: undefined })];
  assert.equal(picker._recommendedId([], noRoles, 'analyzer'), 'plain-one');
});

/* ---- search matches only visible labels (FR-98) --------------------------------- */

test('search covers the labels a user can actually see', () => {
  const { picker } = load();
  const hay = picker._haystack(character());

  for (const visible of ['research archivist', 'resident', 'patient source-finder']) {
    assert.ok(hay.includes(visible), `expected search to cover ${visible}`);
  }
});

test('search does not match hidden fields', () => {
  const { picker } = load();
  const hay = picker._haystack(character());

  // The id, asset paths, silhouette, prop, and sample line are not searchable:
  // a user cannot see why a hidden field matched.
  for (const hidden of ['portrait.svg', 'feather cape', 'pocket ledger', 'indexed the sources']) {
    assert.ok(!hay.includes(hidden.toLowerCase()), `search should not cover ${hidden}`);
  }
});

/* ---- opening and resolving (FR-95) ---------------------------------------------- */

test('choosing resolves with the catalog id and nothing else', async () => {
  const { picker, dom } = load();
  const promise = picker.open({ selectedId: 'product-builder' });

  dom.nodes.get('charPickerConfirm').fire('click');
  const result = await promise;

  // Field-by-field: objects built inside the vm realm carry that realm's
  // prototype, which assert/strict treats as a mismatch.
  assert.equal(result.action, 'choose');
  assert.equal(result.catalogId, 'product-builder');
  // The version is server-assigned, so the picker has nothing to say about it
  // and must not invent one (FR-10/FR-55).
  assert.equal(result.catalogVersion, undefined);
  assert.equal(picker.isOpen(), false);
});

test('the picker offers no voice control and resolves no voice state', async () => {
  const { picker, dom } = load();
  const promise = picker.open({ selectedId: 'product-builder' });

  // The control is gone from the DOM entirely, not merely hidden or ignored:
  // a toggle that implied a character changes how an agent speaks is the exact
  // promise this feature removed (FR-19/FR-23).
  assert.equal(dom.nodes.get('charPickerVoice'), undefined);

  dom.nodes.get('charPickerConfirm').fire('click');
  const result = await promise;
  assert.equal(result.voiceEnabled, undefined);
});

test('skip resolves as an explicit no-character', async () => {
  const { picker, dom } = load();
  const promise = picker.open({});
  dom.nodes.get('charPickerSkip').fire('click');
  const result = await promise;
  assert.equal(result.action, 'skip');
  assert.equal(result.catalogId, undefined);
});

test('cancel resolves as cancel, so the caller leaves everything alone', async () => {
  const { picker, dom } = load();
  const promise = picker.open({ selectedId: 'product-builder' });
  dom.nodes.get('charPickerCancel').fire('click');

  const result = await promise;
  assert.equal(result.action, 'cancel');
  // Cancel carries no choice at all, so a caller cannot half-apply it.
  assert.equal(result.catalogId, undefined);
});

test('the backdrop and close control both cancel', async () => {
  for (const control of ['charPickerBackdrop', 'charPickerClose']) {
    const { picker, dom } = load();
    const promise = picker.open({});
    dom.nodes.get(control).fire('click');
    assert.equal((await promise).action, 'cancel', `${control} should cancel`);
  }
});

/* ---- selection (FR-53/FR-59) ----------------------------------------------------- */

test('opening pre-selects the agent existing character', async () => {
  const { picker, dom } = load();
  const promise = picker.open({ selectedId: 'decision-strategist' });
  dom.nodes.get('charPickerConfirm').fire('click');
  assert.equal((await promise).catalogId, 'decision-strategist');
});

test('with no existing choice the recommendation is pre-selected', async () => {
  const { picker, dom } = load();
  const promise = picker.open({ taken: ['research-archivist'] });
  dom.nodes.get('charPickerConfirm').fire('click');
  assert.equal((await promise).catalogId, 'product-builder');
});

test('clicking a card changes the selection', async () => {
  const { picker, dom } = load();
  const promise = picker.open({ selectedId: 'research-archivist' });

  dom.nodes.get('charPickerGrid').fire('click', {
    target: {
      closest: () => ({ getAttribute: () => 'decision-strategist' })
    }
  });
  dom.nodes.get('charPickerConfirm').fire('click');

  assert.equal((await promise).catalogId, 'decision-strategist');
});

test('the grid renders every character with its art and purpose', () => {
  const { picker, dom } = load();
  picker.open({});
  const html = dom.nodes.get('charPickerGrid').innerHTML;

  for (const ch of CATALOG) {
    assert.ok(html.includes(ch.name), `missing ${ch.name}`);
    assert.ok(html.includes(ch.assets.portrait), `missing art for ${ch.name}`);
  }
  picker._cancel();
});

// FR-28: the preview must show what the user is committing to before they save.
test('the preview shows the visual facts and nothing about how the agent talks', () => {
  const { picker, dom } = load();
  picker.open({ selectedId: 'research-archivist' });
  const html = dom.nodes.get('charPickerPreview').innerHTML;

  for (const expected of ['Wide brow', 'Pocket ledger', 'Straightens a stack of notes']) {
    assert.ok(html.includes(expected), `preview missing ${expected}`);
  }
  // No tone line and no sample speech. Copy describing how a character talks
  // would keep the removed promise alive even with the behavior gone (FR-23).
  for (const gone of ['Tone', 'measured', 'I indexed the sources.', 'char-preview__sample']) {
    assert.ok(!html.includes(gone), `preview must not mention ${gone}`);
  }
  picker._cancel();
});

// FR-29: a prop is appearance, not an installed capability.
test('the preview says the character grants nothing', () => {
  const { picker, dom } = load();
  picker.open({ selectedId: 'research-archivist' });
  const html = dom.nodes.get('charPickerPreview').innerHTML.toLowerCase();

  assert.ok(html.includes('appearance only'));
  assert.ok(html.includes('does not change how the agent works'));
  assert.ok(html.includes('never change'));
  picker._cancel();
});

/* ---- the guide is never offered (FR-19/FR-71) ------------------------------------ */

test('the picker only ever lists assignable working characters', () => {
  // CharacterCatalog.working() excludes the guide by construction; this pins
  // that the picker reads that list rather than assembling its own.
  const { picker, dom } = load();
  picker.open({});
  const html = dom.nodes.get('charPickerGrid').innerHTML;

  assert.ok(!html.includes('ori-guide'));
  assert.ok(!html.includes('>Ori<'));
  picker._cancel();
});

/* ---- degraded catalog (FR-14/FR-124) ---------------------------------------------- */

test('an unavailable catalog explains itself instead of showing an empty grid', () => {
  const { picker, dom } = load({ catalog: [] });
  picker.open({});

  const html = dom.nodes.get('charPickerGrid').innerHTML;
  assert.ok(/unavailable/i.test(html));
  // And confirming is impossible, so a user cannot save nothing.
  assert.equal(dom.nodes.get('charPickerConfirm').disabled, true);
  picker._cancel();
});

test('a search matching nothing says so', () => {
  const { picker, dom } = load();
  picker.open({});

  const search = dom.nodes.get('charPickerSearch');
  search.value = 'zzzz-no-match';
  search.fire('input');

  assert.ok(/no character matches/i.test(dom.nodes.get('charPickerGrid').innerHTML));
  picker._cancel();
});

/* ---- filtering ------------------------------------------------------------------- */

test('a family filter narrows the grid', () => {
  const { picker, dom } = load();
  picker.open({});

  dom.families[2].fire('click'); // familiar
  const html = dom.nodes.get('charPickerGrid').innerHTML;

  assert.ok(html.includes('Decision Strategist'));
  assert.ok(!html.includes('Research Archivist'));
  picker._cancel();
});

test('search narrows on a visible label', () => {
  const { picker, dom } = load();
  picker.open({});

  const search = dom.nodes.get('charPickerSearch');
  search.value = 'builder';
  search.fire('input');

  const html = dom.nodes.get('charPickerGrid').innerHTML;
  assert.ok(html.includes('Product Builder'));
  assert.ok(!html.includes('Decision Strategist'));
  picker._cancel();
});

/* ---- escaping ---------------------------------------------------------------------- */

test('hostile catalog copy cannot inject markup', () => {
  const hostile = [
    character({
      id: 'x',
      name: '<img src=x onerror="alert(1)">',
      purpose: '</button><script>alert(1)</script>'
    })
  ];
  const { picker, dom } = load({ catalog: hostile });
  picker.open({});

  const html = dom.nodes.get('charPickerGrid').innerHTML;
  assert.ok(!html.includes('<img src=x'));
  assert.ok(!html.includes('<script>'));
  assert.ok(html.includes('&lt;img'));
  picker._cancel();
});
