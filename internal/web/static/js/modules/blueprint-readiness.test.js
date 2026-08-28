// Tests for blueprint-readiness.js — the browser half of the readiness
// contract. Inline DOM stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/blueprint-readiness.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

class FakeElement {
  constructor(tag) {
    this.tagName = String(tag || 'div').toUpperCase();
    this.className = '';
    this.type = '';
    this.hidden = false;
    this.id = '';
    this.dataset = {};
    this.attributes = {};
    this.children = [];
    this._text = '';
    this._listeners = {};
    this.classList = {
      add: (...names) => {
        const set = new Set(String(this.className).split(/\s+/).filter(Boolean));
        names.forEach(n => set.add(n));
        this.className = [...set].join(' ');
      }
    };
  }
  get textContent() {
    return this._text + this.children.map(c => c.textContent).join('');
  }
  set textContent(value) {
    this._text = String(value);
    this.children = [];
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
  append(...nodes) {
    nodes.forEach(n => this.children.push(n));
  }
  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }
  getAttribute(name) {
    return Object.hasOwn(this.attributes, name) ? this.attributes[name] : null;
  }
  removeAttribute(name) {
    delete this.attributes[name];
  }
  addEventListener(name, fn) {
    (this._listeners[name] = this._listeners[name] || []).push(fn);
  }
  fire(name) {
    (this._listeners[name] || []).forEach(fn => fn());
  }
  descendants() {
    return this.children.flatMap(c => [c, ...c.descendants()]);
  }
  find(predicate) {
    return this.descendants().find(predicate) || null;
  }
  findAll(predicate) {
    return this.descendants().filter(predicate);
  }
  hasClass(name) {
    return String(this.className).split(/\s+/).includes(name);
  }
}

function loadModule() {
  globalThis.window = {};
  globalThis.document = { createElement: tag => new FakeElement(tag) };
  const source = readFileSync(new URL('./blueprint-readiness.js', import.meta.url), 'utf8');
  vm.runInThisContext(source, { filename: 'blueprint-readiness.js' });
  return globalThis.window.BlueprintReadiness;
}

const BR = loadModule();

function projection(overrides = {}) {
  return {
    state: 'action_required',
    ownership: 'plugin',
    reason: 'plugin_enable_required',
    summary: "This blueprint's plugin is installed but disabled.",
    detail: 'Enabling it makes the blueprint usable. Nothing runs until you enable it.',
    dependency: {
      plugin_name: 'owner-plugin',
      plugin_version: '1.2.0',
      installed: true,
      enabled: false,
      source_declared: true
    },
    actions: ['enable_plugin', 'manage_plugins'],
    generation: 7,
    ...overrides
  };
}

function actionButtons(panel) {
  return panel.findAll(node => node.hasClass('workspace-blueprint-readiness-action'));
}

// --- normalize -------------------------------------------------------------

test('a missing projection is treated as ready', () => {
  // A server that predates the contract, and the synthetic Blank entry, both
  // arrive with no readiness. Refusing to show them would break the picker.
  for (const raw of [undefined, null, '', 0]) {
    assert.equal(BR.normalize(raw).state, 'ready');
    assert.equal(BR.isReady(raw), true);
    assert.equal(BR.isBlocked(raw), false);
  }
});

test('an unrecognized state is treated as unavailable, never as ready', () => {
  const got = BR.normalize({ state: 'probably_fine', ownership: 'vendor', reason: 'because' });
  assert.equal(got.state, 'unavailable');
  assert.equal(got.ownership, 'plugin');
  assert.equal(got.reason, '');
});

test('actions off the allowlist are dropped and duplicates collapse', () => {
  const got = BR.normalize(
    projection({
      actions: ['enable_plugin', 'enable_plugin', 'run_shell', '', null, 'manage_plugins']
    })
  );
  assert.deepEqual(got.actions, ['enable_plugin', 'manage_plugins']);
});

test('a diagnostic survives only for a user-owned blueprint', () => {
  const diagnostic = 'invalid setup wizard: unknown kind widget';
  for (const [ownership, expected] of [
    ['user', diagnostic],
    ['builtin', ''],
    ['plugin', '']
  ]) {
    const got = BR.normalize(projection({ ownership, reason: 'manifest_invalid', diagnostic }));
    assert.equal(got.diagnostic, expected, ownership);
  }
});

test('normalize is idempotent', () => {
  // Callers hold on to a normalized projection and hand it back to renderPanel.
  // A second pass reads camelCase keys, so it must not quietly empty what the
  // first pass already read.
  const once = BR.normalize(projection());
  const twice = BR.normalize(once);
  assert.deepEqual(twice.dependency, once.dependency);
  assert.equal(twice.dependency.pluginName, 'owner-plugin');
  assert.equal(twice.dependency.sourceDeclared, true);
  assert.deepEqual(twice.actions, once.actions);
  assert.equal(twice.summary, once.summary);
});

test('a non-object dependency is dropped rather than half-read', () => {
  assert.equal(BR.normalize(projection({ dependency: 'owner-plugin' })).dependency, null);
  assert.equal(BR.normalize(projection({ dependency: null })).dependency, null);
});

test('missing optional fields normalize to empty rather than undefined', () => {
  const got = BR.normalize({ state: 'unavailable', ownership: 'builtin', reason: '' });
  assert.equal(got.summary, '');
  assert.equal(got.detail, '');
  assert.equal(got.diagnostic, '');
  assert.deepEqual(got.actions, []);
  assert.equal(got.dependency, null);
  assert.equal(got.generation, 0);
});

// --- badges and descriptions ----------------------------------------------

test('a ready blueprint gets no badge and no description', () => {
  const ready = { state: 'ready', ownership: 'builtin', reason: '' };
  assert.equal(BR.badgeLabel(ready), '');
  assert.equal(BR.describe(ready), '');
  assert.equal(BR.renderBadge(ready), null);
  assert.equal(BR.renderPanel(ready, {}), null);
});

test('badges carry text and a glyph, not color alone', () => {
  const recoverable = BR.renderBadge(projection());
  assert.equal(recoverable.dataset.state, 'action_required');
  assert.match(recoverable.textContent, /Setup required/);
  assert.equal(BR.badgeGlyph(projection()), '!');

  const unavailable = BR.renderBadge(projection({ state: 'unavailable' }));
  assert.match(unavailable.textContent, /Unavailable/);
  assert.equal(BR.badgeGlyph({ state: 'unavailable' }), '×');

  // The glyph is decorative: the label is what carries the meaning.
  const glyph = recoverable.find(node => node.hasClass('workspace-template-readiness-glyph'));
  assert.equal(glyph.getAttribute('aria-hidden'), 'true');
});

test('the accessible description states the label and the reason', () => {
  assert.equal(
    BR.describe(projection()),
    "Setup required. This blueprint's plugin is installed but disabled."
  );
  // With no summary it still says what state the card is in.
  assert.equal(BR.describe(projection({ summary: '' })), 'Setup required');
});

// --- the panel -------------------------------------------------------------

test('the panel states the summary, detail, and lifecycle position', () => {
  const panel = BR.renderPanel(projection(), {});
  assert.equal(panel.dataset.state, 'action_required');
  assert.equal(panel.dataset.reason, 'plugin_enable_required');
  assert.match(panel.textContent, /installed but disabled/);
  assert.match(panel.textContent, /Nothing runs until you enable it/);
  // "Installed, still disabled" is the distinction install-versus-enable turns
  // on; the panel must not blur it.
  assert.match(panel.textContent, /owner-plugin 1\.2\.0 — installed, still disabled/);
});

test('dependencyLine distinguishes not installed, disabled, and enabled', () => {
  const line = raw => BR.dependencyLine(BR.normalize(raw));
  assert.equal(
    line(projection({ dependency: { plugin_name: 'p', installed: false } })),
    'p — not installed'
  );
  assert.equal(
    line(projection({ dependency: { plugin_name: 'p', installed: true, enabled: false } })),
    'p — installed, still disabled'
  );
  assert.equal(
    line(
      projection({
        dependency: { plugin_name: 'p', plugin_version: '2.0', installed: true, enabled: true }
      })
    ),
    'p 2.0 — installed and enabled'
  );
});

test('the panel is focusable and is not a live region', () => {
  const panel = BR.renderPanel(projection(), {});
  assert.equal(panel.getAttribute('tabindex'), '-1');
  assert.equal(panel.getAttribute('aria-live'), null);
  assert.match(panel.getAttribute('aria-label'), /Blueprint status/);
});

test('the author diagnostic is collapsed behind a disclosure', () => {
  const panel = BR.renderPanel(
    projection({
      ownership: 'user',
      reason: 'manifest_invalid',
      diagnostic: 'invalid setup wizard: names unregistered adapter "nope"'
    }),
    {}
  );
  const details = panel.find(node => node.tagName === 'DETAILS');
  assert.ok(details, 'expected a disclosure');
  assert.match(details.textContent, /Technical details/);
  assert.match(details.textContent, /unregistered adapter/);
});

test('a shipped blueprint never shows a parser diagnostic', () => {
  const panel = BR.renderPanel(
    projection({
      ownership: 'builtin',
      reason: 'manifest_invalid',
      diagnostic: 'invalid setup wizard: names unregistered adapter "nope"',
      actions: ['change_blueprint']
    }),
    {}
  );
  assert.equal(
    panel.find(node => node.tagName === 'DETAILS'),
    null
  );
  assert.doesNotMatch(panel.textContent, /unregistered adapter/);
});

test('actions render as buttons and report back by name', () => {
  const pressed = [];
  const panel = BR.renderPanel(projection(), {
    blueprintName: 'Song Production',
    onAction: action => pressed.push(action)
  });
  const buttons = actionButtons(panel);
  assert.equal(buttons.length, 2);
  assert.equal(buttons[0].textContent, 'Enable plugin');
  assert.equal(buttons[0].dataset.readinessAction, 'enable_plugin');
  // Distinct accessible names: several blueprints' panels can coexist.
  assert.equal(buttons[0].getAttribute('aria-label'), 'Enable plugin for Song Production');
  assert.equal(buttons[1].textContent, 'Manage plugins');

  buttons[0].fire('click');
  buttons[1].fire('click');
  assert.deepEqual(pressed, ['enable_plugin', 'manage_plugins']);
});

test('the panel performs nothing without a handler', () => {
  const panel = BR.renderPanel(projection(), {});
  // No onAction supplied: pressing must be inert rather than throwing.
  assert.doesNotThrow(() => actionButtons(panel).forEach(button => button.fire('click')));
});

test('only the leading recovery action is styled as the call to action', () => {
  const buttons = actionButtons(BR.renderPanel(projection(), {}));
  assert.ok(buttons[0].hasClass('modern-btn-primary'));
  assert.ok(buttons[1].hasClass('modern-btn-secondary'));

  // A hard blocker offers escape routes only — none of them is a primary call
  // to action, because none of them fixes the blueprint.
  const blocked = actionButtons(
    BR.renderPanel(
      projection({
        state: 'unavailable',
        reason: 'platform_unsupported',
        actions: ['manage_plugins', 'change_blueprint']
      }),
      {}
    )
  );
  assert.ok(blocked.every(button => button.hasClass('modern-btn-secondary')));
});

test('copy reaches the DOM as text, never as markup', () => {
  const hostile = '<img src=x onerror="alert(1)">';
  const panel = BR.renderPanel(
    projection({ summary: hostile, detail: hostile, ownership: 'user', diagnostic: hostile }),
    {}
  );
  // The stub records assigned text verbatim; what matters is that the module
  // assigns textContent rather than innerHTML, so the markup is inert.
  const summary = panel.find(node => node.hasClass('workspace-blueprint-readiness-summary'));
  assert.equal(summary.textContent, hostile);
  assert.equal(summary._text, hostile);
});

test('an unknown action name produces no button rather than an unlabelled one', () => {
  const panel = BR.renderPanel(
    projection({ actions: ['enable_plugin'], reason: 'plugin_enable_required' }),
    {}
  );
  assert.equal(actionButtons(panel).length, 1);

  const stripped = BR.renderPanel(projection({ actions: ['grant_everything'] }), {});
  assert.equal(actionButtons(stripped).length, 0);
});
