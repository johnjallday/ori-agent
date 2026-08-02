// Tests for workspace-toolbox.js — the Workshop.
//
// Run with: node --test internal/web/static/js/modules/workspace-toolbox.test.js
//
// These pin the behavior that separates a Toolbox from the old chip editor:
// selection is a DRAFT until saved, saving is one request that produces a new
// version, an unapproved capability can only enter as an explicit requirement,
// and nothing in this module installs or connects anything.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-toolbox.js', import.meta.url), 'utf8');

class FakeElement {
  constructor(tag = 'div') {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attributes = {};
    this.style = {};
    this.className = '';
    this.dataset = {};
    this.hidden = false;
    this.disabled = false;
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
  for (const child of node.children || []) {
    out.push(child, ...descendants(child));
  }
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

function workshopFixture(overrides = {}) {
  return {
    workspace_id: 'ws-1',
    agent_instance_id: 'inst-1',
    agent_name: 'Coder',
    toolbox_id: 'tbx-1',
    toolbox_version: 2,
    capacity: { used: 1, capacity: 4, full: false },
    core: [
      {
        kind: 'mcp',
        capability_id: 'workspace-filesystem',
        display_name: 'workspace_filesystem',
        source: 'core',
        binding_id: 'workspace-filesystem',
        locked: true,
        selected: true,
        available: true,
        connected: true,
        consumes_skill_space: false
      },
      {
        kind: 'skill',
        capability_id: 'workspace-settings',
        display_name: 'workspace-settings',
        source: 'core',
        locked: true,
        selected: true,
        available: true,
        consumes_skill_space: false,
        summary: 'Always available in this workspace.'
      }
    ],
    agent_learned: [
      {
        kind: 'skill',
        capability_id: 'code-review',
        display_name: 'code-review',
        source: 'agent_learned',
        available: true,
        selected: true,
        required: true,
        consumes_skill_space: true,
        summary: 'Reviews diffs for defects.',
        prompt: 'Read the diff carefully before commenting.'
      },
      {
        kind: 'skill',
        capability_id: 'refactoring',
        display_name: 'refactoring',
        source: 'agent_learned',
        available: true,
        selected: false,
        consumes_skill_space: true
      }
    ],
    workspace_provided: [
      {
        kind: 'mcp',
        capability_id: 'mb-1',
        display_name: 'Notes',
        source: 'workspace_provided',
        binding_id: 'mb-1',
        server_name: 'notes',
        available: true,
        connected: true,
        selected: true,
        selected_tools: ['read_note'],
        exposed_tools: ['read_note', 'write_note'],
        default_side_effect: 'read',
        tool_risks: { write_note: 'write' },
        scope: { folder: 'notes/' },
        consumes_skill_space: false
      },
      {
        kind: 'mcp',
        capability_id: 'mb-2',
        display_name: 'Tracker',
        source: 'workspace_provided',
        binding_id: 'mb-2',
        server_name: 'tracker',
        available: true,
        connected: true,
        selected: false,
        exposed_tools: ['list_issues'],
        unclassified_tools: ['list_issues'],
        consumes_skill_space: false
      }
    ],
    global_library: [
      {
        kind: 'skill',
        capability_id: 'summarizing',
        display_name: 'summarizing',
        source: 'global_library',
        available: false,
        consumes_skill_space: true,
        unavailable_reason: 'Ori knows this skill, but this workspace has not added it yet.'
      }
    ],
    requirements: [],
    collisions: [],
    ...overrides
  };
}

function load({
  workshop = workshopFixture(),
  toolboxes,
  failWrites = false,
  promptAnswer = 'Research Kit'
} = {}) {
  const host = new FakeElement('div');
  host.id = 'workspace-toolbox-panel';

  const requests = [];
  const savedToolboxes = toolboxes || [
    {
      id: 'tbx-1',
      name: 'Research Kit',
      version: 2,
      status: 'active',
      skill_count: 1,
      mcp_binding_count: 1,
      operation_count: 1,
      assigned_instance_ids: ['inst-1']
    },
    {
      id: 'tbx-2',
      name: 'Spare Kit',
      version: 1,
      status: 'active',
      skill_count: 0,
      mcp_binding_count: 0,
      operation_count: 0,
      assigned_instance_ids: []
    }
  ];

  const document = {
    readyState: 'complete',
    addEventListener() {},
    getElementById: id => (id === 'workspace-toolbox-panel' ? host : null),
    createElement: tag => new FakeElement(tag)
  };
  const window = {
    location: { pathname: '/workspaces/ws-1' },
    currentWorkspaceId: 'ws-1',
    prompt: () => promptAnswer
  };

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
        return { ok: false, status: 400, json: async () => ({ message: 'Toolbox is full.' }) };
      }
      if (String(url).includes('/toolbox-workshop')) {
        return { ok: true, status: 200, json: async () => ({ workshop, workspace_version: 7 }) };
      }
      if (String(url).includes('/compare')) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            diff: {
              skills_added: [{ capability_id: 'refactoring', display_name: 'refactoring' }],
              skills_removed: [],
              skills_changed: [],
              bindings_added: [],
              bindings_removed: [],
              bindings_changed: [
                {
                  binding_id: 'mb-1',
                  added_tools: ['write_note'],
                  removed_tools: [],
                  fields: ['allowed_tools']
                }
              ],
              skill_spaces_before: 1,
              skill_spaces_after: 2
            },
            identical: false,
            expands_operations: true,
            from: { version: 1 },
            to: { version: 2 }
          })
        };
      }
      if (String(url).endsWith('/toolboxes') && method === 'GET') {
        return {
          ok: true,
          status: 200,
          json: async () => ({ toolboxes: savedToolboxes, workspace_version: 7 })
        };
      }
      return {
        ok: true,
        status: 200,
        json: async () => ({ toolbox: { id: 'tbx-new', version: 1 } })
      };
    }
  };

  vm.runInNewContext(source, context, { filename: 'workspace-toolbox.js' });
  return { api: window.WorkspaceToolbox, host, requests, window };
}

function click(host, selector) {
  const node = host.querySelector(selector);
  assert.ok(node, 'expected a clickable node matching ' + selector);
  const listeners = host.listeners.click || [];
  for (const handler of listeners) {
    handler({ target: node, preventDefault() {} });
  }
  return node;
}

test('the Workshop groups capabilities by where they come from', async () => {
  const { api, host } = load();
  await api.init({ agentInstanceId: 'inst-1' });

  const text = host.textContent;
  // FR-43: approved-here and known-to-Ori are visibly different groups.
  assert.match(text, /Core/);
  assert.match(text, /From this agent/);
  assert.match(text, /From this workspace/);
  assert.match(text, /Elsewhere in Ori/);
  assert.match(text, /does not install or connect anything/i);
});

test('core capabilities are locked and cost no skill space (FR-47, FR-48)', async () => {
  const { api, host } = load();
  await api.init({ agentInstanceId: 'inst-1' });

  const lock = host.querySelector('.ws-toolbox-lock');
  assert.ok(lock, 'expected core items to render a locked marker rather than a toggle');
  // The group says it once, and a core skill card says it again on the card —
  // capacity is the number a user is reasoning about, so the exemption has to
  // be visible where the count is.
  assert.match(host.textContent, /These use no skill spaces/);
  assert.match(host.textContent, /No skill space/);
  assert.match(host.textContent, /Always available in this workspace/);
});

test('an MCP card shows connection, scope, operations, and risk in text (FR-50)', async () => {
  const { api, host } = load();
  await api.init({ agentInstanceId: 'inst-1' });

  const text = host.textContent;
  assert.match(text, /Connected/);
  assert.match(text, /Scope: folder: notes\//);
  assert.match(text, /Default effect: read/);
  assert.match(text, /read_note/);
  assert.match(text, /write_note/);
  // An operation with no classification must be visible, not silently absent.
  assert.match(text, /Unclassified operations \(blocked until classified\): list_issues/);
});

test('a skill card explains what it adds and can reveal what it instructs (FR-49)', async () => {
  const { api, host } = load();
  await api.init({ agentInstanceId: 'inst-1' });

  assert.match(host.textContent, /Reviews diffs for defects\./);
  // The prompt is behind an expander, so the summary can be checked.
  assert.doesNotMatch(host.textContent, /Read the diff carefully/);
  click(host, '[data-toolbox-expand]');
  assert.match(host.textContent, /Read the diff carefully/);
});

test('selection is a draft until saved, and saving is ONE request that versions', async () => {
  const { api, host, requests } = load();
  await api.init({ agentInstanceId: 'inst-1' });

  // Nothing is editable until the user starts an edit.
  assert.equal(api._draft(), null);
  click(host, '[data-toolbox-edit]');
  assert.ok(api._draft(), 'expected an edit to open a draft');

  const before = requests.length;
  click(host, '[data-toolbox-toggle-skill=refactoring]');
  click(host, '[data-toolbox-toggle-op=write_note]');
  // Toggling changes nothing on the server — the old editor issued a request
  // per toggle, which is exactly what a versioned draft replaces.
  assert.equal(requests.length, before, 'expected toggles to be local to the draft');

  const draft = api._draft();
  assert.ok(draft.skills.some(entry => entry.capability_id === 'refactoring'));
  assert.ok(
    draft.bindings.find(entry => entry.binding_id === 'mb-1').allowed_tools.includes('write_note')
  );

  await api.saveDraft();
  const writes = requests.filter(request => request.method === 'POST');
  assert.equal(writes.length, 1, 'expected exactly one write for the whole edit');
  assert.match(writes[0].url, /\/toolboxes\/tbx-1\/versions$/);
  assert.equal(writes[0].body.expected_version, 2);
  assert.equal(writes[0].body.expected_workspace_version, 7);
});

test('adding an unapproved capability records a requirement and installs nothing (FR-45, FR-46)', async () => {
  const { api, host, requests } = load();
  await api.init({ agentInstanceId: 'inst-1' });
  click(host, '[data-toolbox-edit]');

  const before = requests.length;
  click(host, '[data-toolbox-add-requirement=summarizing]');

  assert.equal(requests.length, before, 'expected Add requirement to issue no request');
  const draft = api._draft();
  const requirement = draft.skills.find(entry => entry.capability_id === 'summarizing');
  assert.ok(requirement, 'expected the requirement to enter the draft');
  assert.equal(requirement.required, true);
  assert.equal(requirement.binding_id, '', 'expected an unmet requirement to name no binding');
  assert.match(host.textContent, /Missing capability/);
});

test('an unmet requirement is not sent as toolbox content, so the recipe stays saveable', async () => {
  const { api, host, requests } = load();
  await api.init({ agentInstanceId: 'inst-1' });
  click(host, '[data-toolbox-edit]');
  click(host, '[data-toolbox-add-requirement=summarizing]');
  await api.saveDraft();

  const write = requests.filter(request => request.method === 'POST').pop();
  assert.ok(
    !write.body.skills.some(entry => entry.capability_id === 'summarizing'),
    'expected the unmet requirement to be excluded from saved content'
  );
});

test('a toolbox that permits every operation asks for an explicit list (FR-13)', async () => {
  const workshop = workshopFixture();
  workshop.workspace_provided.push({
    kind: 'mcp',
    capability_id: 'mb-open',
    display_name: 'Open Server',
    source: 'workspace_provided',
    binding_id: 'mb-open',
    available: true,
    connected: true,
    exposes_all_tools: true,
    consumes_skill_space: false
  });
  const { api, host } = load({ workshop });
  await api.init({ agentInstanceId: 'inst-1' });

  assert.match(host.textContent, /needs an explicit list before it can be saved/i);
});

test('a full toolbox says so, and says a grandfathered one can still be edited (FR-33)', async () => {
  const { api, host } = load({
    workshop: workshopFixture({
      capacity: { used: 5, capacity: 3, full: true, grandfathered: true }
    })
  });
  await api.init({ agentInstanceId: 'inst-1' });

  assert.match(host.textContent, /Toolbox full/);
  assert.match(host.textContent, /remove or swap, but not add/i);
});

test('a source collision is surfaced for the user to resolve, not silently decided (FR-6)', async () => {
  const { api, host } = load({
    workshop: workshopFixture({ collisions: [{ capability_id: 'code-review', sources: [] }] })
  });
  await api.init({ agentInstanceId: 'inst-1' });

  const alert = host.querySelector('.ws-toolbox-error');
  assert.ok(alert, 'expected the collision to be announced');
  assert.match(alert.textContent, /come from two places/i);
  assert.equal(alert.getAttribute('role'), 'alert');
});

test('an instance with only core capabilities reads as Core only, not empty (FR-39)', async () => {
  const { api, host } = load({
    workshop: workshopFixture({
      agent_learned: [],
      workspace_provided: [],
      global_library: [],
      requirements: []
    })
  });
  await api.init({ agentInstanceId: 'inst-1' });

  assert.match(host.textContent, /Core only/);
});

test('comparison names exactly what moved between versions (FR-51, FR-52)', async () => {
  const { api, host } = load();
  await api.init({ agentInstanceId: 'inst-1' });

  click(host, '[data-toolbox-compare=tbx-1]');
  await new Promise(resolve => setTimeout(resolve, 0));

  const text = host.textContent;
  assert.match(text, /Comparing v1 → v2/);
  assert.match(text, /Skills added: refactoring/);
  assert.match(text, /Operations changed: mb-1 \+write_note/);
  assert.match(text, /Skill spaces: 1 → 2/);
  assert.match(text, /exposes operations the earlier one did not/);
});

test('delete is offered only for a toolbox nothing references (FR-21)', async () => {
  const { api, host } = load();
  await api.init({ agentInstanceId: 'inst-1' });

  assert.equal(
    host.querySelector('[data-toolbox-delete=tbx-1]'),
    null,
    'expected no delete action for an assigned toolbox'
  );
  assert.ok(
    host.querySelector('[data-toolbox-delete=tbx-2]'),
    'expected an unreferenced toolbox to offer delete'
  );
});

test('a failed save reports the reason and leaves the draft intact', async () => {
  const { api, host } = load({ failWrites: true });
  await api.init({ agentInstanceId: 'inst-1' });
  click(host, '[data-toolbox-edit]');
  click(host, '[data-toolbox-toggle-skill=refactoring]');

  await api.saveDraft();

  assert.match(host.textContent, /Toolbox is full\./);
  const draft = api._draft();
  assert.ok(draft, 'expected a failed save to keep the draft');
  assert.ok(draft.skills.some(entry => entry.capability_id === 'refactoring'));
});

test('every selection control carries a meaningful accessible name (FR-163)', async () => {
  const { api, host } = load();
  await api.init({ agentInstanceId: 'inst-1' });
  click(host, '[data-toolbox-edit]');

  for (const node of host.querySelectorAll('.ws-toolbox-select')) {
    assert.ok(node.getAttribute('aria-label'), 'expected an accessible name on every selector');
    assert.ok(['true', 'false'].includes(node.getAttribute('aria-checked')));
    assert.equal(node.getAttribute('role'), 'switch');
  }
  for (const node of host.querySelectorAll('.ws-toolbox-op')) {
    assert.ok(node.getAttribute('aria-label'), 'expected an accessible name on every operation');
  }
});

test('the Workshop uses the cozy vocabulary and no military terms (FR-168)', async () => {
  const { api, host } = load();
  await api.init({ agentInstanceId: 'inst-1' });

  const text = host.textContent;
  assert.match(text, /toolbox/i);
  assert.doesNotMatch(text, /loadout/i);
  assert.doesNotMatch(text, /armory/i);
  assert.doesNotMatch(text, /\bequip\b/i);
  assert.doesNotMatch(text, /\bdeploy\b/i);
});
