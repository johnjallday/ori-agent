// Tests for agent-avatar.js — Avatar Identity v1 (PRD FR66–FR78).
//
// agent-avatar.js is a classic deferred script (it must install itself before
// the roster controller runs), so it is evaluated in a node:vm sandbox with a
// minimal window/document, mirroring action-center.test.js.
//   node --test internal/web/static/js/modules/agent-avatar.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./agent-avatar.js', import.meta.url), 'utf8');

function load() {
  const listeners = [];
  const sandbox = {
    window: {},
    document: {
      addEventListener(type, handler, capture) {
        listeners.push({ type, handler, capture });
      }
    },
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
  return { api: sandbox.window.AgentAvatar, listeners, sandbox };
}

const { api: AgentAvatar, listeners: docListeners } = load();

function agent(overrides = {}) {
  return { name: 'Atlas', source: 'user', ...overrides };
}

/* ---- determinism (FR69, Success Metric 3) --------------------------------- */

test('the same input always produces the same signature', () => {
  const a = AgentAvatar.signature(agent());
  const b = AgentAvatar.signature(agent());
  assert.deepEqual(a, b);
});

test('signature survives a fresh module evaluation (reload equivalence)', () => {
  const { api: reloaded } = load();
  // Compared as plain data: the two objects come from different vm realms, so
  // deepEqual would otherwise fail on prototype identity alone.
  assert.deepEqual(
    JSON.parse(JSON.stringify(reloaded.signature(agent()))),
    JSON.parse(JSON.stringify(AgentAvatar.signature(agent())))
  );
});

test('signature ignores theme and view context because it takes neither', () => {
  // The renderer is a pure function of the agent record: there is no theme or
  // view parameter it could branch on, so Gallery, List, and Inspector renders
  // in either theme are identical by construction.
  const gallery = AgentAvatar.markup(agent(), { size: 72, className: 'roster-card__avatar' });
  const list = AgentAvatar.markup(agent(), { size: 54, className: 'roster-row__avatar' });
  const hero = AgentAvatar.markup(agent(), { size: 88, className: 'stage__avatar' });
  const motif = /data-aa-motif="([a-z]+)"/;
  const turn = /data-aa-turn="(\d)"/;
  const tone = /data-aa-tone="(\d)"/;
  for (const re of [motif, turn, tone]) {
    assert.equal(gallery.match(re)[1], list.match(re)[1]);
    assert.equal(gallery.match(re)[1], hero.match(re)[1]);
  }
});

test('renaming an agent changes its identity; editing its model does not', () => {
  const base = AgentAvatar.signature(agent());
  const renamed = AgentAvatar.signature(agent({ name: 'Atlas Prime' }));
  const remodelled = AgentAvatar.signature(agent({ model: 'gpt-4o-mini' }));
  assert.notEqual(base.hash, renamed.hash);
  assert.equal(base.hash, remodelled.hash);
});

/* ---- initials (FR70) ------------------------------------------------------ */

test('initials use first and last word, or two characters of a single word', () => {
  assert.equal(AgentAvatar.initialsFor('Claude Code'), 'CC');
  assert.equal(AgentAvatar.initialsFor('Codex'), 'CO');
  assert.equal(AgentAvatar.initialsFor('Gemini CLI'), 'GC');
  assert.equal(AgentAvatar.initialsFor('Ori'), 'OR');
  assert.equal(AgentAvatar.initialsFor('mail triage bot'), 'MB');
});

test('initials handle separators, punctuation, and non-Latin names', () => {
  assert.equal(AgentAvatar.initialsFor('data_pipeline'), 'DP');
  assert.equal(AgentAvatar.initialsFor('release-gate'), 'RG');
  assert.equal(AgentAvatar.initialsFor('  spaced   out  '), 'SO');
  assert.equal(AgentAvatar.initialsFor('Ø'), 'Ø');
  assert.equal(AgentAvatar.initialsFor('研究 助手'), '研助');
});

test('initials never render blank for empty or punctuation-only names', () => {
  assert.equal(AgentAvatar.initialsFor(''), '?');
  assert.equal(AgentAvatar.initialsFor('   '), '?');
  assert.equal(AgentAvatar.initialsFor('***'), '?');
  assert.equal(AgentAvatar.initialsFor(undefined), '?');
});

/* ---- contrast (FR77, Success Metric 6) ------------------------------------ */

test('every reviewed palette clears WCAG AA against its chosen ink', () => {
  for (const palette of [...AgentAvatar.PALETTES, ...AgentAvatar.SYSTEM_PALETTES]) {
    const sig = AgentAvatar.signature({ name: 'probe', source: 'user' });
    const ratio = AgentAvatar.contrastRatio('#ffffff', palette.base);
    assert.ok(
      ratio >= 4.5,
      `${palette.id} (${palette.base}) is ${ratio.toFixed(2)}:1 against white, needs 4.5:1`
    );
    assert.ok(sig.ink === '#ffffff' || sig.ink === '#0b0f14');
  }
});

test('the gradient end is always darker than the base, so contrast cannot drop', () => {
  const names = ['Atlas', 'Forge', 'Ledger', 'Muse', 'Scout', 'Sentinel'];
  for (const name of names) {
    const sig = AgentAvatar.signature({ name, source: 'user' });
    assert.ok(
      AgentAvatar.contrastRatio(sig.ink, sig.deep) >= AgentAvatar.contrastRatio(sig.ink, sig.base),
      `${name}: gradient end must not reduce contrast`
    );
    assert.ok(AgentAvatar.contrastRatio(sig.ink, sig.base) >= 4.5);
  }
});

test('a custom avatar colour still gets an ink that clears AA', () => {
  for (const color of ['#ffffff', '#ffe600', '#000000', '#7f7f7f', '#0b4f52']) {
    const sig = AgentAvatar.signature(agent({ avatarColor: color }));
    assert.ok(
      AgentAvatar.contrastRatio(sig.ink, sig.base) >= 4.5,
      `${color} → ink ${sig.ink} is only ${AgentAvatar.contrastRatio(sig.ink, sig.base).toFixed(2)}:1`
    );
  }
});

/* ---- custom colour (FR72) ------------------------------------------------- */

test('a valid avatar_color becomes the base but keeps motif, tone, and initials', () => {
  const plain = AgentAvatar.signature(agent());
  const custom = AgentAvatar.signature(agent({ avatarColor: '#7c3aed' }));
  assert.equal(custom.base, '#7c3aed');
  assert.equal(custom.custom, true);
  assert.equal(custom.paletteId, 'custom');
  // Every non-palette dimension is untouched, so the identity is still the
  // agent's own rather than a flat colour swatch.
  assert.equal(custom.motif, plain.motif);
  assert.equal(custom.turn, plain.turn);
  assert.equal(custom.toneIndex, plain.toneIndex);
  assert.equal(custom.initials, plain.initials);
});

test('short hex is expanded and case-normalized', () => {
  assert.equal(AgentAvatar.normalizeHex('#ABC'), '#aabbcc');
  assert.equal(AgentAvatar.normalizeHex('#A1B2C3'), '#a1b2c3');
});

test('an invalid avatar_color is ignored rather than reaching CSS', () => {
  const plain = AgentAvatar.signature(agent());
  for (const bad of [
    'red',
    'rgb(1,2,3)',
    '#12',
    '#12345',
    'url(x)',
    '#fff;background:url(evil)',
    'expression(alert(1))',
    ''
  ]) {
    const sig = AgentAvatar.signature(agent({ avatarColor: bad }));
    assert.equal(sig.custom, false, `${bad} must not be accepted`);
    assert.equal(sig.base, plain.base);
  }
});

/* ---- role accent (FR70, FR71) --------------------------------------------- */

test('role contributes an accent and emblem, not the whole identity', () => {
  const noRole = AgentAvatar.signature(agent());
  const withRole = AgentAvatar.signature(
    agent({ role: 'orchestrator', roleAccent: '#f59e0b', roleEmblem: 'diagram-3' })
  );
  assert.equal(withRole.accent, '#f59e0b');
  assert.equal(withRole.emblem, 'diagram-3');
  // Palette, motif, and tone come from the agent, so the role cannot flatten
  // two different agents into one look.
  assert.equal(withRole.base, noRole.base);
  assert.equal(withRole.motif, noRole.motif);
});

test('two agents sharing a role stay visually distinct', () => {
  const role = { role: 'researcher', roleAccent: '#0ea5e9', roleEmblem: 'search' };
  const a = AgentAvatar.signature({ name: 'Scout', source: 'user', ...role });
  const b = AgentAvatar.signature({ name: 'Probe', source: 'user', ...role });
  const differs =
    a.paletteIndex !== b.paletteIndex ||
    a.motif !== b.motif ||
    a.toneIndex !== b.toneIndex ||
    a.turn !== b.turn ||
    a.initials !== b.initials;
  assert.ok(differs, 'same-role agents must differ by palette, motif, tone, turn, or initials');
});

test('a missing catalog role yields no accent rather than borrowing one', () => {
  const sig = AgentAvatar.signature(agent({ role: 'general' }));
  assert.equal(sig.accent, '');
  assert.equal(sig.emblem, '');
});

test('an unsafe emblem slug is dropped instead of being rendered', () => {
  for (const bad of ['../../x', 'a b', 'x"><script>', 'Diagram3']) {
    assert.equal(AgentAvatar.signature(agent({ roleEmblem: bad })).emblem, '');
  }
  const html = AgentAvatar.fallbackHTML(agent({ roleEmblem: 'x"><script>alert(1)</script>' }));
  assert.ok(!html.includes('<script>'));
});

/* ---- built-in identities (FR73) ------------------------------------------- */

test('built-in CLI agents get the system treatment', () => {
  for (const input of [
    { name: 'Claude Code', source: 'cli' },
    { name: 'Codex', source: 'cli' },
    { name: 'Gemini CLI', source: 'cli' },
    { name: 'Ori', source: 'user' },
    { name: 'Some Agent', source: 'user', role: 'cli_agent' },
    { name: 'Flagged', source: 'user', builtIn: true }
  ]) {
    const sig = AgentAvatar.signature(input);
    assert.equal(sig.system, true, `${input.name} should read as system`);
    assert.ok(
      AgentAvatar.SYSTEM_PALETTES.some(p => p.id === sig.paletteId),
      `${input.name} should use a system palette, got ${sig.paletteId}`
    );
  }
});

test('the three built-in CLI agents remain distinct from one another', () => {
  const sigs = ['Claude Code', 'Codex', 'Gemini CLI'].map(name =>
    AgentAvatar.signature({ name, source: 'cli' })
  );
  const keys = sigs.map(s => [s.paletteIndex, s.motif, s.turn, s.toneIndex, s.initials].join('|'));
  assert.equal(new Set(keys).size, keys.length, `built-in identities collided: ${keys.join(', ')}`);
});

test('a user agent never picks up the system treatment', () => {
  const sig = AgentAvatar.signature(agent({ name: 'Origami' }));
  assert.equal(sig.system, false);
  assert.ok(AgentAvatar.PALETTES.some(p => p.id === sig.paletteId));
});

/* ---- distinctness fixture (Success Metrics 4 and 5) ----------------------- */

// 25 reviewed names covering short/long, one/multi word, shared prefixes,
// anagrams, and case variants — the cases a character-sum hash collapses.
const FIXTURE_NAMES = [
  'Atlas',
  'Forge',
  'Ledger',
  'Muse',
  'Scout',
  'Sentinel',
  'Beacon',
  'Cascade',
  'Drift',
  'Ember',
  'Harbor',
  'Ivory',
  'Juniper',
  'Kestrel',
  'Lantern',
  'Meridian',
  'Nimbus',
  'Orchard',
  'Pilot',
  'Quarry',
  'Mail Triage Bot',
  'Mail Triage Bots',
  'Release Gate',
  'Gate Release',
  'release gate'
];

test('25 reviewed names produce no duplicate fallback signature', () => {
  const seen = new Map();
  for (const name of FIXTURE_NAMES) {
    const s = AgentAvatar.signature({ name, source: 'user' });
    const key = [s.paletteIndex, s.motif, s.turn, s.toneIndex, s.initials].join('|');
    assert.ok(
      !seen.has(key),
      `"${name}" collides with "${seen.get(key)}" on the full signature ${key}`
    );
    seen.set(key, name);
  }
  assert.equal(seen.size, FIXTURE_NAMES.length);
});

test('anagrams and case variants do not collapse the way a character sum does', () => {
  const a = AgentAvatar.signature({ name: 'Release Gate', source: 'user' });
  const b = AgentAvatar.signature({ name: 'Gate Release', source: 'user' });
  const c = AgentAvatar.signature({ name: 'release gate', source: 'user' });
  assert.notEqual(a.hash, b.hash);
  assert.notEqual(a.hash, c.hash);
});

test('the fixture spreads across many palettes rather than clustering', () => {
  const palettes = new Set(
    FIXTURE_NAMES.map(name => AgentAvatar.signature({ name, source: 'user' }).paletteIndex)
  );
  assert.ok(palettes.size >= 8, `expected wide palette spread, got ${palettes.size}`);
});

/* ---- image priority and failure (FR67, FR74, FR97) ------------------------ */

test('a valid uploaded image renders ahead of the fallback', () => {
  const html = AgentAvatar.markup(agent({ avatarImage: 'atlas.png' }), { size: 72 });
  assert.ok(html.includes('agent-avatar--image'));
  assert.ok(html.includes('src="/avatars/atlas.png"'));
  assert.ok(!html.includes('agent-avatar__initials'));
});

test('an uploaded image reserves its box and defers decoding', () => {
  const html = AgentAvatar.markup(agent({ avatarImage: 'atlas.png' }), { size: 72 });
  assert.ok(html.includes('width="72"'));
  assert.ok(html.includes('height="72"'));
  assert.ok(html.includes('loading="lazy"'));
  assert.ok(html.includes('decoding="async"'));
  assert.ok(html.includes('alt=""'));
});

test('a blank or whitespace-only image path falls through to the fallback', () => {
  for (const image of ['', '   ', undefined, null]) {
    const html = AgentAvatar.markup(agent({ avatarImage: image }));
    assert.ok(html.includes('agent-avatar--fallback'), `image=${JSON.stringify(image)}`);
  }
});

test('an image path is escaped rather than closing the attribute', () => {
  const html = AgentAvatar.markup(agent({ avatarImage: 'x" onerror="alert(1)' }));
  // The payload survives as inert text inside src; what matters is that it
  // never closes the attribute and so never becomes a live handler.
  assert.ok(html.includes('src="/avatars/x&quot; onerror=&quot;alert(1)"'));
  assert.equal(html.match(/\son[a-z]+="/g), null, 'no live event-handler attribute');
  assert.equal((html.match(/<img/g) || []).length, 1, 'exactly the one intended <img>');
});

test("a failed image is replaced in place by that agent's own fallback", () => {
  // Minimal element stand-in: the swap only touches dataset, className,
  // attributes, and innerHTML.
  const attrs = {};
  const host = {
    dataset: { aaName: 'Atlas', aaSource: 'user', aaColor: '#7c3aed' },
    className: 'agent-avatar agent-avatar--image agent-avatar--md roster-card__avatar',
    style: { getPropertyValue: () => '' },
    innerHTML: '<img class="agent-avatar__img">',
    setAttribute(k, v) {
      attrs[k] = v;
    },
    getAttribute(k) {
      return attrs[k];
    }
  };
  AgentAvatar.replaceWithFallback(host);

  const expected = AgentAvatar.signature({ name: 'Atlas', source: 'user', avatarColor: '#7c3aed' });
  assert.ok(host.className.includes('agent-avatar--fallback'));
  assert.ok(!host.className.includes('agent-avatar--image'));
  // The failed <img> is gone entirely, so no broken-image glyph can paint.
  assert.ok(!host.innerHTML.includes('<img'));
  assert.ok(host.innerHTML.includes(expected.initials));
  assert.equal(attrs['data-aa-motif'], expected.motif);
  assert.equal(attrs['data-aa-tone'], String(expected.toneIndex));
  // The size class survives, so the replacement fills exactly the box the
  // image reserved and the collection does not reflow.
  assert.ok(host.className.includes('agent-avatar--md'));
  assert.ok(attrs.style.includes('--aa-base:#7c3aed'));
});

test('size is expressed as a class so CSS can resize a portrait per view', () => {
  assert.match(AgentAvatar.markup(agent(), { size: 54 }), /agent-avatar--sm/);
  assert.match(AgentAvatar.markup(agent(), { size: 72 }), /agent-avatar--md/);
  assert.match(AgentAvatar.markup(agent(), { size: 88 }), /agent-avatar--lg/);
  // Default when a caller passes nothing.
  assert.match(AgentAvatar.markup(agent()), /agent-avatar--md/);
  // An inline --aa-size would beat any stylesheet rule, so it must not appear.
  for (const size of [54, 72, 88]) {
    assert.ok(!AgentAvatar.markup(agent(), { size }).includes('--aa-size'));
    assert.ok(!AgentAvatar.markup(agent({ avatarImage: 'a.png' }), { size }).includes('--aa-size'));
  }
});

test('replacing an already-replaced avatar is a no-op', () => {
  let writes = 0;
  const host = {
    dataset: { aaName: 'Atlas', aaSource: 'user', aaFallback: '1' },
    className: 'agent-avatar agent-avatar--fallback',
    style: { getPropertyValue: () => '' },
    innerHTML: 'kept',
    setAttribute() {
      writes++;
    }
  };
  AgentAvatar.replaceWithFallback(host);
  assert.equal(writes, 0);
  assert.equal(host.innerHTML, 'kept');
});

test('image failure is handled by one delegated capture listener', () => {
  const errorListeners = docListeners.filter(l => l.type === 'error');
  assert.equal(errorListeners.length, 1);
  assert.equal(errorListeners[0].capture, true);
});

/* ---- markup contract (FR70, FR76, FR98) ----------------------------------- */

test('the fallback carries initials, motif, and role emblem', () => {
  const html = AgentAvatar.fallbackHTML(
    agent({ role: 'orchestrator', roleAccent: '#f59e0b', roleEmblem: 'diagram-3' })
  );
  assert.ok(html.includes('agent-avatar__initials'));
  assert.ok(html.includes('>AT<'));
  assert.ok(html.includes('agent-avatar__motif'));
  assert.ok(html.includes('bi bi-diagram-3'));
  assert.ok(html.includes('--aa-accent:#f59e0b'));
});

test('the avatar is decorative for assistive technology', () => {
  assert.ok(AgentAvatar.markup(agent()).includes('aria-hidden="true"'));
  assert.ok(AgentAvatar.markup(agent({ avatarImage: 'a.png' })).includes('aria-hidden="true"'));
});

test('the fallback needs no image file or remote reference', () => {
  const html = AgentAvatar.fallbackHTML(agent());
  assert.ok(!/<img/.test(html));
  assert.ok(!/https?:\/\//.test(html));
  assert.ok(!/url\(/.test(html));
});

test('status is never encoded in the avatar', () => {
  const ready = AgentAvatar.signature(agent({ status: 'active' }));
  const broken = AgentAvatar.signature(agent({ status: 'error' }));
  assert.deepEqual(ready, broken);
});

test('agent names are escaped everywhere they reach markup', () => {
  const html = AgentAvatar.markup({ name: '<img src=x onerror=alert(1)>', source: 'user' });
  // The name only ever lands in an attribute value; escaping the angle brackets
  // and quotes is what makes the payload inert.
  assert.ok(html.includes('data-aa-name="&lt;img src=x onerror=alert(1)&gt;"'));
  assert.ok(!html.includes('<img'), 'no tag may be created from a name');
  assert.equal(html.match(/\son[a-z]+="/g), null, 'no live event-handler attribute');
});

test('only tokens this module owns reach the style attribute', () => {
  const html = AgentAvatar.fallbackHTML(agent({ avatarColor: '#7c3aed', roleAccent: '#f59e0b' }));
  const style = html.match(/style="([^"]*)"/)[1];
  for (const decl of style.split(';')) {
    assert.match(
      decl,
      /^--aa-(base|deep|ink|accent):#[0-9a-f]{6}$|^--aa-angle:\d+deg$|^--aa-motif-alpha:[\d.]+$/,
      `unexpected style declaration: ${decl}`
    );
  }
});

test('motif, turn, and tone are emitted as bounded tokens', () => {
  for (const name of FIXTURE_NAMES) {
    const html = AgentAvatar.fallbackHTML({ name, source: 'user' });
    const motif = html.match(/data-aa-motif="([^"]+)"/)[1];
    const turn = Number(html.match(/data-aa-turn="(\d+)"/)[1]);
    const tone = Number(html.match(/data-aa-tone="(\d+)"/)[1]);
    assert.ok(AgentAvatar.MOTIFS.includes(motif));
    assert.ok(turn >= 0 && turn < AgentAvatar.ANGLES.length);
    assert.ok(tone >= 0 && tone < AgentAvatar.TONES.length);
  }
});

test('optional id and className are applied without breaking the base classes', () => {
  const html = AgentAvatar.markup(agent(), {
    id: 'stageAvatar',
    className: 'stage__avatar',
    size: 88
  });
  assert.ok(html.includes('id="stageAvatar"'));
  assert.ok(
    html.includes('class="agent-avatar agent-avatar--fallback agent-avatar--lg stage__avatar"')
  );
});
