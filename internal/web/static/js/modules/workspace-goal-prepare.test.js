// Tests for workspace-goal-prepare.js — the Goal Prepare step.
//
// Run with: node --test internal/web/static/js/modules/workspace-goal-prepare.test.js
//
// These pin the two properties that make Prepare safe: a proposed brief
// controls nothing until accepted, and a recommendation is inert — choosing one
// pins a version for the goal and does not apply anything to the agent.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-goal-prepare.js', import.meta.url), 'utf8');

class FakeElement {
  constructor(tag = 'div') {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attributes = {};
    this.className = '';
    this.dataset = {};
    this.disabled = false;
    this.value = '';
    this.listeners = {};
    this._text = '';
  }
  set textContent(value) {
    this._text = String(value ?? '');
    if (this._text === '') this.children = [];
  }
  get textContent() {
    return this._text + this.children.map(child => child.textContent).join(' ');
  }
  set innerHTML(value) {
    if (String(value) === '') this.children = [];
  }
  get innerHTML() {
    return '';
  }
  appendChild(child) {
    this.children.push(child);
    child.parentNode = this;
    return child;
  }
  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }
  getAttribute(name) {
    return Object.prototype.hasOwnProperty.call(this.attributes, name)
      ? this.attributes[name]
      : null;
  }
  addEventListener(type, handler) {
    (this.listeners[type] = this.listeners[type] || []).push(handler);
  }
  querySelector(selector) {
    return descendants(this).find(node => matches(node, selector)) || null;
  }
  querySelectorAll(selector) {
    return descendants(this).filter(node => matches(node, selector));
  }
  closest(selector) {
    let node = this;
    while (node) {
      if (matches(node, selector)) return node;
      node = node.parentNode;
    }
    return null;
  }
}

function descendants(node) {
  const out = [];
  for (const child of node.children || []) out.push(child, ...descendants(child));
  return out;
}

function matches(node, selector) {
  const value = String(selector || '');
  if (value.startsWith('.'))
    return String(node.className || '')
      .split(/\s+/)
      .includes(value.slice(1));
  if (value.startsWith('[') && value.endsWith(']')) {
    const inner = value.slice(1, -1);
    const eq = inner.indexOf('=');
    if (eq < 0) return Object.prototype.hasOwnProperty.call(node.attributes, inner);
    const name = inner.slice(0, eq);
    const want = inner.slice(eq + 1).replace(/^["']|["']$/g, '');
    return node.getAttribute(name) === want;
  }
  if (value.startsWith('#')) return node.id === value.slice(1);
  return false;
}

function recommendationsFixture(overrides = {}) {
  return {
    agent_instance_id: 'inst-1',
    agent_name: 'Coder',
    brief_version: 1,
    any_fully_covers: true,
    best_match: 'tbx-lean',
    recommendations: [
      {
        toolbox_id: 'tbx-lean',
        toolbox_name: 'Research Kit',
        toolbox_version: 1,
        rank: 1,
        fully_covers: true,
        readiness: 'Ready',
        focus: { state: 'Focused' },
        skill_spaces: 1,
        operations: 1,
        covers: ['summarize'],
        explanation: 'Covers summarize.'
      },
      {
        toolbox_id: 'tbx-wide',
        toolbox_name: 'Everything Kit',
        toolbox_version: 1,
        rank: 2,
        fully_covers: true,
        readiness: 'Ready',
        focus: { state: 'Flexible' },
        skill_spaces: 2,
        operations: 3,
        covers: ['summarize'],
        introduces_permissions: ['1 operation(s) that change things'],
        exceeds_autonomy: true,
        explanation: 'Covers summarize; goes beyond the autonomy you set for this goal.'
      }
    ],
    ...overrides
  };
}

function load({
  accepted = null,
  proposed = { summary: 'Search and summarize', operations: ['search'], max_autonomy: 'read' },
  policy = null,
  recommendations = recommendationsFixture(),
  failWrites = false
} = {}) {
  const host = new FakeElement('section');
  host.id = 'workspace-goal-prepare';

  const requests = [];
  const document = {
    readyState: 'complete',
    addEventListener() {},
    getElementById: id => (id === 'workspace-goal-prepare' ? host : null),
    createElement: tag => new FakeElement(tag)
  };
  const window = { location: { pathname: '/workspaces/ws-1' }, currentWorkspaceId: 'ws-1' };

  const context = {
    console,
    window,
    document,
    setTimeout,
    JSON,
    fetch: async (url, options = {}) => {
      const method = options.method || 'GET';
      requests.push({
        url: String(url),
        method,
        body: options.body ? JSON.parse(options.body) : null
      });
      if (method !== 'GET' && failWrites) {
        return { ok: false, status: 400, json: async () => ({ message: 'That did not work.' }) };
      }
      if (String(url).includes('/goal/recommendations')) {
        return { ok: true, status: 200, json: async () => ({ recommendations, policy }) };
      }
      if (String(url).includes('/goal/toolbox-policy')) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ message: 'This goal is pinned to version 1.' })
        };
      }
      return { ok: true, status: 200, json: async () => ({ accepted, proposed, policy }) };
    }
  };

  vm.runInNewContext(source, context, { filename: 'workspace-goal-prepare.js' });
  return { api: window.WorkspaceGoalPrepare, host, requests };
}

function click(host, selector) {
  const node = host.querySelector(selector);
  assert.ok(node, 'expected a clickable node matching ' + selector);
  for (const handler of host.listeners.click || []) {
    handler({ target: node, preventDefault() {} });
  }
  return node;
}

test('an unaccepted proposal is offered for review, not presented as decided (FR-94)', async () => {
  const { api, host, requests } = load();
  await api.init();

  assert.match(host.textContent, /Ori has a suggestion/);
  assert.match(host.textContent, /recommendations only start once you accept it/i);
  // Loading proposes and ranks; it writes nothing.
  assert.equal(requests.filter(request => request.method !== 'GET').length, 0);
});

test('accepting the brief is one explicit write (FR-94)', async () => {
  const { api, host, requests } = load();
  await api.init();

  click(host, '[data-goal-edit-brief]');
  assert.ok(api._draft(), 'expected an editable draft');
  assert.match(host.textContent, /Nothing is recommended from this until you accept it/);

  click(host, '[data-goal-accept-brief]');
  await new Promise(resolve => setTimeout(resolve, 0));

  const writes = requests.filter(request => request.method === 'PUT');
  assert.equal(writes.length, 1);
  assert.match(writes[0].url, /\/goal\/brief$/);
  assert.deepEqual(writes[0].body.operations, ['search']);
  assert.equal(writes[0].body.max_autonomy, 'read');
});

test('an accepted brief is shown as what the goal needs (FR-93)', async () => {
  const { api, host } = load({
    accepted: {
      expected_output: 'a short summary',
      source_types: ['public web'],
      operations: ['search'],
      required_capabilities: ['summarize'],
      max_autonomy: 'read',
      version: 1
    }
  });
  await api.init();

  const text = host.textContent;
  assert.match(text, /Produces: a short summary/);
  assert.match(text, /Sources: public web/);
  assert.match(text, /Needs to be able to: search/);
  assert.match(text, /Must have: summarize/);
  assert.match(text, /At most: read/);
});

test('recommendations explain themselves and flag what they introduce (FR-98)', async () => {
  const { api, host } = load({ accepted: { required_capabilities: ['summarize'], version: 1 } });
  await api.init();

  const text = host.textContent;
  assert.match(text, /Research Kit v1/);
  assert.match(text, /Best match/);
  assert.match(text, /Covers summarize\./);
  assert.match(text, /1 skill spaces · 1 tools/);
  assert.match(text, /Focus: Focused/);
  // The wider option's cost is visible before it can be chosen.
  assert.match(text, /Introduces 1 operation\(s\) that change things\./);
  assert.match(text, /goes beyond the autonomy you set for this goal/i);
});

test('choosing a recommendation pins a version and applies nothing (FR-99, FR-104)', async () => {
  const { api, host, requests } = load({
    accepted: { required_capabilities: ['summarize'], version: 1 }
  });
  await api.init();

  click(host, '[data-goal-pin=tbx-lean]');
  await new Promise(resolve => setTimeout(resolve, 0));

  const writes = requests.filter(request => request.method === 'PUT');
  assert.equal(writes.length, 1, 'expected exactly one write');
  assert.match(writes[0].url, /\/goal\/toolbox-policy$/);
  assert.equal(writes[0].body.toolbox_id, 'tbx-lean');
  assert.equal(writes[0].body.toolbox_version, 1);
  assert.equal(writes[0].body.use_current_at_start, false);
  // Nothing was applied to the agent — no /use call anywhere.
  assert.equal(requests.filter(request => request.url.includes('/use')).length, 0);
});

test('a pinned goal says an edit elsewhere will not change it (FR-104)', async () => {
  const { api, host } = load({
    accepted: { required_capabilities: ['summarize'], version: 1 },
    policy: { entry_agent_instance_id: 'inst-1', toolbox_id: 'tbx-lean', toolbox_version: 2 }
  });
  await api.init();

  assert.match(host.textContent, /Pinned to version 2/);
  assert.match(host.textContent, /Editing that toolbox will not change this goal/);
  assert.ok(
    host.querySelector('[data-goal-use-current]'),
    'expected the labelled alternative to be offered'
  );
});

test('current-at-start is an explicit, labelled alternative (FR-103)', async () => {
  const { api, host, requests } = load({
    accepted: { required_capabilities: ['summarize'], version: 1 },
    policy: { entry_agent_instance_id: 'inst-1', toolbox_id: 'tbx-lean', toolbox_version: 1 }
  });
  await api.init();

  click(host, '[data-goal-use-current]');
  await new Promise(resolve => setTimeout(resolve, 0));

  const write = requests.filter(request => request.method === 'PUT').pop();
  assert.equal(write.body.use_current_at_start, true);
});

test('a goal whose pinned toolbox broke says so, loudly (FR-105)', async () => {
  const { api, host } = load({
    accepted: { required_capabilities: ['summarize'], version: 1 },
    policy: {
      entry_agent_instance_id: 'inst-1',
      toolbox_id: 'tbx-lean',
      toolbox_version: 1,
      needs_attention: true,
      needs_attention_reason: '"Research Kit" is archived, so this goal cannot start.'
    }
  });
  await api.init();

  const alert = host.querySelector('.ws-goal-error');
  assert.ok(alert, 'expected the stop to be announced');
  assert.match(alert.textContent, /Needs attention/);
  assert.match(alert.textContent, /archived/);
  assert.equal(alert.getAttribute('role'), 'alert');
});

test('when nothing covers the goal, the gap is honest and the variant is inert (FR-101, FR-102)', async () => {
  const { api, host, requests } = load({
    accepted: { required_capabilities: ['summarize', 'translation'], version: 1 },
    recommendations: recommendationsFixture({
      any_fully_covers: false,
      message: 'No saved toolbox covers everything this goal needs. The closest options are below.',
      proposed_variant: {
        based_on_toolbox_id: 'tbx-lean',
        based_on_toolbox_name: 'Research Kit',
        unavailable_requirements: ['translation'],
        explanation: 'Nothing in this workspace provides translation yet.'
      }
    })
  });
  await api.init();

  const text = host.textContent;
  assert.match(text, /No saved toolbox covers everything/);
  assert.match(text, /Nothing in this workspace provides translation yet/);
  assert.match(text, /Not set up in this workspace yet: translation/);
  assert.match(text, /Nothing has been created/);
  // The variant is a description, not an action.
  assert.equal(requests.filter(request => request.method !== 'GET').length, 0);
});

test('every pin control carries an accessible name (FR-163)', async () => {
  const { api, host } = load({ accepted: { required_capabilities: ['summarize'], version: 1 } });
  await api.init();

  for (const node of host.querySelectorAll('[data-goal-pin]')) {
    assert.match(node.getAttribute('aria-label') || '', /^Pin .+ for this goal$/);
  }
});
