// Tests for the curated-character branch of agent-avatar.js
// (cozy-character-experience FR-14, FR-66–FR-75, FR-99, FR-101, FR-120–FR-124).
//
// Same node:vm sandbox approach as agent-avatar.test.js, which covers the
// pre-existing uploaded/fallback behaviour this must not disturb.
//   node --test internal/web/static/js/modules/agent-avatar-character.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./agent-avatar.js', import.meta.url), 'utf8');

function load() {
  const sandbox = {
    window: {},
    document: { addEventListener() {} },
    Math,
    Object,
    Number,
    String,
    Array,
    parseInt
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);
  return sandbox.window.AgentAvatar;
}

const AgentAvatar = load();
const { FALLBACK, UPLOADED, CHARACTER } = AgentAvatar.MODES;

function character(overrides = {}) {
  return {
    id: 'sable',
    name: 'Sable',
    archetype: 'Research Archivist',
    palette: { base: '#4f744a', accent: '#4f744a', ink: '#0f1a0e' },
    assets: {
      portrait: '/characters/sable/portrait.svg',
      sprite: '/characters/sable/sprite.svg',
      static: '/characters/sable/static.svg'
    },
    ...overrides
  };
}

function agent(overrides = {}) {
  return { name: 'Atlas', source: 'user', ...overrides };
}

/* ---- explicit mode beats field presence (FR-67) ---------------------------- */

test('an explicit character mode wins even when an upload is present', () => {
  const res = AgentAvatar.resolve(
    agent({ displayMode: CHARACTER, avatarImage: 'atlas.png', character: character() })
  );
  assert.equal(res.mode, CHARACTER);
  assert.equal(res.reason, 'ok');
  assert.equal(res.character.id, 'sable');
});

test('an explicit uploaded mode wins even when a character is selected', () => {
  const res = AgentAvatar.resolve(
    agent({ displayMode: UPLOADED, avatarImage: 'atlas.png', character: character() })
  );
  assert.equal(res.mode, UPLOADED);
  assert.equal(res.avatarImage, 'atlas.png');
});

test('an explicit fallback mode wins over both', () => {
  const res = AgentAvatar.resolve(
    agent({ displayMode: FALLBACK, avatarImage: 'atlas.png', character: character() })
  );
  assert.equal(res.mode, FALLBACK);
});

/* ---- legacy records are untouched (FR-69) ---------------------------------- */

test('a record with no stored mode keeps the historical upload-first rule', () => {
  const res = AgentAvatar.resolve(agent({ avatarImage: 'atlas.png' }));
  assert.equal(res.mode, UPLOADED);
  assert.equal(res.requested, UPLOADED);
  assert.equal(res.reason, 'legacy');
});

test('a record with nothing stored falls back', () => {
  const res = AgentAvatar.resolve(agent());
  assert.equal(res.mode, FALLBACK);
  assert.equal(res.reason, 'legacy');
});

test('an unrecognized stored mode degrades to the legacy rule rather than breaking', () => {
  const res = AgentAvatar.resolve(agent({ displayMode: 'hologram', avatarImage: 'atlas.png' }));
  assert.equal(res.mode, UPLOADED);
});

/* ---- missing / withdrawn catalog entries (FR-74/FR-124) -------------------- */

test('a character mode with no catalog entry falls back and says why', () => {
  const res = AgentAvatar.resolve(agent({ displayMode: CHARACTER }));
  assert.equal(res.mode, FALLBACK);
  assert.equal(res.requested, CHARACTER);
  assert.equal(res.reason, 'character-missing');
});

test('a withdrawn character with no portrait falls back and stays distinguishable', () => {
  const res = AgentAvatar.resolve(
    agent({ displayMode: CHARACTER, character: character({ assets: {} }) })
  );
  assert.equal(res.mode, FALLBACK);
  // Separate reason from "never chose one": the Inspector uses this to offer a
  // reselect action rather than silently showing initials.
  assert.equal(res.reason, 'character-asset-missing');
});

test('an uploaded mode with no file falls back and says why', () => {
  const res = AgentAvatar.resolve(agent({ displayMode: UPLOADED }));
  assert.equal(res.mode, FALLBACK);
  assert.equal(res.reason, 'upload-missing');
});

test('a fallen-back character still renders the agent own deterministic identity', () => {
  const html = AgentAvatar.markup(agent({ displayMode: CHARACTER }), { size: 72 });
  assert.ok(html.includes('agent-avatar--fallback'));
  assert.ok(html.includes(AgentAvatar.signature(agent()).initials));
  assert.ok(!html.includes('<img'));
});

/* ---- markup (FR-101/FR-120–FR-123) ----------------------------------------- */

test('a curated portrait renders as a lazily decoded image', () => {
  const html = AgentAvatar.markup(agent({ displayMode: CHARACTER, character: character() }), {
    size: 72
  });
  assert.ok(html.includes('agent-avatar--character'));
  assert.ok(html.includes('/characters/sable/portrait.svg'));
  assert.ok(html.includes('loading="lazy"'));
  assert.ok(html.includes('decoding="async"'));
});

test('a curated portrait reserves its box so layout does not shift', () => {
  const html = AgentAvatar.markup(agent({ displayMode: CHARACTER, character: character() }), {
    size: 88
  });
  assert.ok(html.includes('width="88"'));
  assert.ok(html.includes('height="88"'));
});

test('portrait geometry comes from the size class, never an inline --aa-size', () => {
  for (const size of [54, 72, 88]) {
    const html = AgentAvatar.markup(agent({ displayMode: CHARACTER, character: character() }), {
      size
    });
    assert.ok(!html.includes('--aa-size'));
  }
});

test('the character portrait is decorative for assistive technology', () => {
  // The agent name, role, and status are all present as adjacent text, so
  // repeating the identity here would only make screen-reader output noisier.
  const html = AgentAvatar.markup(agent({ displayMode: CHARACTER, character: character() }), {});
  assert.ok(html.includes('aria-hidden="true"'));
  assert.ok(html.includes('alt=""'));
});

test('the catalog id travels with the element for debugging and styling', () => {
  const html = AgentAvatar.markup(agent({ displayMode: CHARACTER, character: character() }), {});
  assert.ok(html.includes('data-aa-character="sable"'));
});

/* ---- safety ----------------------------------------------------------------- */

test('a hostile catalog id cannot break out of its attribute', () => {
  const html = AgentAvatar.markup(
    agent({ displayMode: CHARACTER, character: character({ id: 'x" onload="alert(1)' }) }),
    {}
  );
  // The payload text survives as inert content; what matters is that its quotes
  // are escaped, so it stays inside the attribute value and never becomes a
  // second attribute.
  assert.ok(html.includes('data-aa-character="x&quot; onload=&quot;alert(1)"'));
  assert.ok(!html.includes('onload="alert'));
});

test('a hostile portrait path cannot break out of the src attribute', () => {
  const html = AgentAvatar.markup(
    agent({
      displayMode: CHARACTER,
      character: character({ assets: { portrait: 'x" onerror="alert(1)' } })
    }),
    {}
  );
  assert.ok(!html.includes('onerror="alert'));
});

test('only a validated hex accent reaches the style attribute', () => {
  const html = AgentAvatar.markup(
    agent({
      displayMode: CHARACTER,
      character: character({ palette: { accent: 'red; background:url(x)', base: 'nope' } })
    }),
    {}
  );
  assert.ok(!html.includes('background:url'));
  assert.ok(!html.includes('--aa-accent'));
});

test('a valid accent is applied as a bounded custom property', () => {
  const html = AgentAvatar.markup(agent({ displayMode: CHARACTER, character: character() }), {});
  assert.ok(html.includes('--aa-accent:#4f744a'));
});

/* ---- cross-surface consistency (FR-99) -------------------------------------- */

test('the same agent resolves identically at every size and on every surface', () => {
  const input = agent({ displayMode: CHARACTER, character: character() });
  const a = AgentAvatar.resolve(input);
  const b = AgentAvatar.resolve(input);
  assert.deepEqual(a, b);

  // Home and the Gallery differ only in size, never in identity.
  for (const size of [54, 72, 88]) {
    assert.ok(AgentAvatar.markup(input, { size }).includes('/characters/sable/portrait.svg'));
  }
});

/* ---- reversibility (FR-64/FR-68) -------------------------------------------- */

test('switching modes never discards the other identity source', () => {
  const base = { avatarImage: 'atlas.png', character: character() };

  const asCharacter = AgentAvatar.resolve(agent({ ...base, displayMode: CHARACTER }));
  const asUpload = AgentAvatar.resolve(agent({ ...base, displayMode: UPLOADED }));

  // Both directions stay available from the same stored record, which is what
  // makes trying a character a reversible experiment.
  assert.equal(asCharacter.mode, CHARACTER);
  assert.equal(asUpload.mode, UPLOADED);
  assert.equal(asUpload.avatarImage, 'atlas.png');
});

/* ---- failure recovery (FR-14) ------------------------------------------------ */

test('a failed character portrait is replaced in place by the deterministic identity', () => {
  const host = {
    dataset: { aaName: 'Atlas', aaSource: 'user', aaCharacter: 'sable' },
    className: 'agent-avatar agent-avatar--character agent-avatar--md',
    attributes: {},
    innerHTML: '<img>',
    removedAttributes: [],
    setAttribute(k, v) {
      this.attributes[k] = v;
    },
    removeAttribute(k) {
      this.removedAttributes.push(k);
    }
  };

  AgentAvatar.replaceWithFallback(host);

  assert.ok(host.className.includes('agent-avatar--fallback'));
  assert.ok(!host.className.includes('agent-avatar--character'));
  // The size class survives, so the replacement occupies exactly the box the
  // portrait reserved and the collection does not reflow.
  assert.ok(host.className.includes('agent-avatar--md'));
  assert.ok(host.removedAttributes.includes('data-aa-character'));
  assert.ok(!host.innerHTML.includes('<img'));
});

test('status is never encoded in a curated portrait', () => {
  const html = AgentAvatar.markup(
    agent({ displayMode: CHARACTER, character: character(), status: 'active', health: 'error' }),
    {}
  );
  for (const leak of ['active', 'error', 'running', 'attention']) {
    assert.ok(!html.includes(leak), `portrait leaked status ${leak}`);
  }
});
