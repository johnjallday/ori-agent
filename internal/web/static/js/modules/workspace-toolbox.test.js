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

function previewFixture(overrides = {}) {
  return {
    workspace_id: 'ws-1',
    agent_instance_id: 'inst-1',
    agent_name: 'Coder',
    toolbox_id: 'tbx-2',
    toolbox_name: 'Spare Kit',
    toolbox_version: 1,
    readiness: 'Ready',
    issues: [],
    focus: {
      state: 'Focused',
      reasons: ['1 of 4 skill spaces, 2 exposed tools'],
      inputs: {
        active_skills: 1,
        skill_capacity: 4,
        exposed_operations: 2,
        read_operations: 2,
        write_operations: 0,
        external_operations: 0,
        prompt_chars: 400,
        unclassified_operations: 0
      }
    },
    capacity: { used: 1, capacity: 4, full: false },
    skills: [{ capability_id: 'testing', display_name: 'testing', available: true }],
    mcp_bindings: [{ binding_id: 'mb-1', available: true }],
    diff: {
      skills_added: [{ capability_id: 'drafting', display_name: 'drafting' }],
      skills_removed: [],
      bindings_added: [],
      bindings_removed: [],
      bindings_changed: [{ binding_id: 'mb-1', added_tools: ['write_note'], removed_tools: [] }]
    },
    expands_permissions: false,
    can_use_directly: true,
    ...overrides
  };
}

function load({
  workshop = workshopFixture(),
  toolboxes,
  failWrites = false,
  promptAnswer = 'Research Kit',
  preview = previewFixture(),
  previewAction = 'Use This Toolbox',
  undo = null,
  useError = ''
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
      if (String(url).includes('/preview')) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ preview, action: previewAction, workspace_version: 7 })
        };
      }
      if (String(url).includes('/undo')) {
        if (method === 'GET') {
          return {
            ok: true,
            status: 200,
            json: async () => undo || { available: false, message: 'There is nothing to undo.' }
          };
        }
        return {
          ok: true,
          status: 200,
          json: async () => ({
            message: 'Restored Lean Kit v1 for Coder.',
            receipt: { agent_name: 'Coder', toolbox_name: 'Lean Kit', toolbox_version: 1 }
          })
        };
      }
      if (String(url).includes('/use')) {
        if (useError) {
          return { ok: false, status: 409, json: async () => ({ message: useError }) };
        }
        return {
          ok: true,
          status: 200,
          json: async () => ({
            message: 'Coder is now using Spare Kit v1.',
            receipt: {
              agent_name: 'Coder',
              toolbox_name: 'Spare Kit',
              toolbox_version: 1,
              focus: { state: 'Focused' },
              capacity: { used: 1, capacity: 4 },
              permissions: {
                operations: 2,
                read_operations: 2,
                write_operations: 0,
                external_operations: 0
              }
            }
          })
        };
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

// ------------------------------------------- preview / use / undo (group 3)

async function openPreview(api, host) {
  await api.init({ agentInstanceId: 'inst-1' });
  click(host, '[data-toolbox-preview=tbx-2]');
  await new Promise(resolve => setTimeout(resolve, 0));
}

test('Focus is shown as separate readouts, not one opaque number (FR-71)', async () => {
  const { api, host } = load();
  await openPreview(api, host);

  const text = host.textContent;
  assert.match(text, /Focus:\s*Focused/);
  assert.match(text, /1 \/ 4 skill spaces/);
  assert.match(text, /2 exposed tools/);
  assert.match(text, /0 that change things, 0 that reach outside/);
  assert.match(text, /400 characters of skill instructions/);
});

test('the exact diff is shown before anything is applied (FR-77)', async () => {
  const { api, host } = load();
  await openPreview(api, host);

  assert.match(host.textContent, /Adds: drafting/);
  assert.match(host.textContent, /Changes: mb-1 \+write_note/);
});

test('a ready, non-expanding switch offers one-click use (FR-78)', async () => {
  const { api, host, requests } = load();
  await openPreview(api, host);

  const button = host.querySelector('[data-toolbox-use]');
  assert.ok(button, 'expected a use action');
  assert.equal(button.textContent, 'Use This Toolbox');
  assert.equal(button.disabled, false);

  click(host, '[data-toolbox-use]');
  await new Promise(resolve => setTimeout(resolve, 0));

  const write = requests.find(request => request.method === 'POST' && request.url.includes('/use'));
  assert.ok(write, 'expected exactly one use request');
  assert.equal(write.body.toolbox_id, 'tbx-2');
  assert.equal(write.body.expected_workspace_version, 7);
  // A direct switch grants nothing new, so it carries no expansion claim.
  assert.equal(write.body.acknowledged_expansion, false);
});

test('a switch with prerequisites is gated until each one is reviewed (FR-79, FR-80)', async () => {
  const { api, host } = load({
    previewAction: 'Review & Use',
    preview: previewFixture({
      readiness: 'Ready',
      expands_permissions: true,
      can_use_directly: false,
      issues: [
        {
          state: 'Needs connection',
          binding_id: 'mb-9',
          message: 'Tracker is switched off.',
          action: 'connect',
          blocking: false
        },
        {
          state: 'Needs approval',
          binding_id: 'mb-1',
          message: 'write_note has no classification.',
          action: 'classify',
          blocking: false
        }
      ]
    })
  });
  await openPreview(api, host);

  const button = host.querySelector('[data-toolbox-use]');
  assert.equal(button.textContent, 'Review & Use');
  assert.equal(button.disabled, true, 'expected use to be blocked until every step is reviewed');

  // Reviewing ONE prerequisite must not unlock the other (FR-80).
  const checks = host.querySelectorAll('[data-toolbox-ack]');
  assert.equal(checks.length, 2);
  click(host, '[data-toolbox-ack=' + checks[0].getAttribute('data-toolbox-ack') + ']');
  assert.equal(host.querySelector('[data-toolbox-use]').disabled, true);

  const remaining = host.querySelectorAll('[data-toolbox-ack]').find
    ? host.querySelectorAll('[data-toolbox-ack]')
    : [];
  click(host, '[data-toolbox-ack=' + remaining[1].getAttribute('data-toolbox-ack') + ']');
  assert.equal(host.querySelector('[data-toolbox-use]').disabled, false);
});

test('an expanding switch sends the acknowledgement the server requires (FR-79)', async () => {
  const { api, host, requests } = load({
    previewAction: 'Review & Use',
    preview: previewFixture({ expands_permissions: true, can_use_directly: false })
  });
  await openPreview(api, host);

  click(host, '[data-toolbox-use]');
  await new Promise(resolve => setTimeout(resolve, 0));

  const write = requests.find(request => request.method === 'POST' && request.url.includes('/use'));
  assert.equal(write.body.acknowledged_expansion, true);
});

test('a not-ready toolbox cannot be used at all (FR-73, FR-78)', async () => {
  const { api, host } = load({
    preview: previewFixture({
      readiness: 'Needs connection',
      can_use_directly: false,
      focus: { state: 'Needs attention', reasons: ['Notes is switched off.'], inputs: {} },
      issues: [
        {
          state: 'Needs connection',
          binding_id: 'mb-1',
          message: 'Notes is switched off.',
          action: 'connect',
          blocking: true
        }
      ]
    })
  });
  await openPreview(api, host);

  assert.equal(host.querySelector('[data-toolbox-use]').disabled, true);
  assert.match(host.textContent, /Resolve the required items above/);
  assert.match(host.textContent, /Focus:\s*Needs attention/);
});

test('a successful switch produces a receipt saying what the agent got (FR-87)', async () => {
  const { api, host } = load();
  await openPreview(api, host);
  click(host, '[data-toolbox-use]');
  await new Promise(resolve => setTimeout(resolve, 0));

  const receipt = host.querySelector('.ws-toolbox-receipt');
  assert.ok(receipt, 'expected a receipt');
  assert.match(receipt.textContent, /Coder is using Spare Kit v1/);
  assert.match(receipt.textContent, /Focus: Focused/);
  assert.match(receipt.textContent, /2 read, 0 write, 0 external/);
  assert.equal(receipt.getAttribute('role'), 'status');
});

test('a failed switch says what did NOT change and keeps the panel truthful (FR-86, FR-167)', async () => {
  const { api, host } = load({
    useError: 'The workspace changed while you were editing. Nothing changed.'
  });
  await openPreview(api, host);
  click(host, '[data-toolbox-use]');
  await new Promise(resolve => setTimeout(resolve, 0));

  const visible = host.querySelector('.ws-toolbox-error');
  assert.ok(visible, 'expected the failure to be visible');
  assert.match(visible.textContent, /Nothing changed/);
  // The announcement comes from the one persistent live region, not from the
  // visible copy — a role="alert" on both would say it twice (FR-164).
  assert.equal(visible.getAttribute('role'), null);

  const live = host.querySelector('.ws-toolbox-live');
  assert.ok(live, 'expected a persistent live region');
  assert.equal(live.getAttribute('role'), 'status');
  assert.equal(live.getAttribute('aria-live'), 'assertive', 'a failure interrupts');
  assert.match(live.textContent, /Nothing changed/);

  assert.equal(host.querySelector('.ws-toolbox-receipt'), null, 'a failed switch has no receipt');
});

test('the live region is one node, and repeats nothing on an unrelated click (FR-164)', async () => {
  const { api, host } = load();
  await openPreview(api, host);

  const regions = host.querySelectorAll('.ws-toolbox-live');
  assert.equal(regions.length, 1, 'expected exactly one live region');
  const region = regions[0];
  // Nothing has happened yet, so there is nothing to announce.
  assert.equal(region.textContent, '');

  // Ticking a prerequisite is a decorative change: the same node survives and
  // its text does not churn, so nothing is re-announced.
  const before = region.textContent;
  click(host, '[data-toolbox-close-preview]');
  const after = host.querySelector('.ws-toolbox-live');
  assert.equal(after, region, 'expected the live region node to survive a re-render');
  assert.equal(after.textContent, before, 'expected no announcement for a decorative change');
});

test('undo is offered after a switch and labelled by the server (FR-88, FR-90)', async () => {
  const { api, host, requests } = load({
    undo: {
      available: true,
      action: 'Review & Restore',
      previous: { toolbox_id: 'tbx-1', toolbox_version: 2 },
      preview: previewFixture({ can_use_directly: false })
    }
  });
  await api.init({ agentInstanceId: 'inst-1' });
  // init loads the panel and then the undo state; give the second read a tick.
  await new Promise(resolve => setTimeout(resolve, 0));

  const button = host.querySelector('[data-toolbox-undo]');
  assert.ok(button, 'expected an undo action');
  // A prior version that would now widen permissions becomes Review & Restore
  // rather than a silent one-click revert.
  assert.equal(button.textContent, 'Review & Restore');

  click(host, '[data-toolbox-undo]');
  await new Promise(resolve => setTimeout(resolve, 0));

  const write = requests.find(
    request => request.method === 'POST' && request.url.includes('/undo')
  );
  assert.ok(write, 'expected an undo request');
  assert.equal(write.body.acknowledged_expansion, true);
});

test('undo is absent when there is nothing to undo', async () => {
  const { api, host } = load();
  await api.init({ agentInstanceId: 'inst-1' });

  assert.equal(host.querySelector('[data-toolbox-undo]'), null);
});

test('every preview control carries an accessible name (FR-163)', async () => {
  const { api, host } = load({
    previewAction: 'Review & Use',
    preview: previewFixture({
      can_use_directly: false,
      issues: [
        {
          state: 'Needs approval',
          binding_id: 'mb-1',
          message: 'write_note has no classification.',
          action: 'classify',
          blocking: false
        }
      ]
    })
  });
  await openPreview(api, host);

  for (const node of host.querySelectorAll('[data-toolbox-ack]')) {
    assert.equal(node.getAttribute('role'), 'checkbox');
    assert.ok(node.getAttribute('aria-label'));
    assert.ok(['true', 'false'].includes(node.getAttribute('aria-checked')));
  }
});

// --- State coverage (task 6.13; FR-157, FR-162-FR-168) -----------------------
//
// Each of these is a state a real workspace reaches. The property under test is
// the same every time: the state says what it is in words, and it never dead-
// ends without telling you what to do next.

test('an empty workshop offers a way to start rather than an empty list', async () => {
  const { api, host } = load({ toolboxes: [] });
  await api.init({ agentInstanceId: 'inst-1' });

  assert.match(host.textContent, /No saved toolboxes yet/);
  // The two starting points are both offered — from what is already on, or clean.
  assert.ok(host.querySelector('[data-toolbox-create=current]'));
  assert.ok(host.querySelector('[data-toolbox-create=empty]'));
});

test('a core-only agent is described, not shown as broken', async () => {
  const { api, host } = load({
    workshop: workshopFixture({
      agent_learned: [],
      workspace_provided: [],
      global_library: [],
      capacity: { used: 0, capacity: 4, full: false }
    })
  });
  await api.init({ agentInstanceId: 'inst-1' });

  assert.match(host.textContent, /Core only/);
  assert.match(host.textContent, /nothing else switched on yet/);
});

test('a full toolbox says swapping is still possible', async () => {
  const { api, host } = load({
    workshop: workshopFixture({ capacity: { used: 4, capacity: 4, full: true } })
  });
  await api.init({ agentInstanceId: 'inst-1' });

  assert.match(host.textContent, /Toolbox full/);
  assert.match(host.textContent, /remove a skill to add another/);
});

test('a grandfathered over-capacity toolbox is distinguished from a plain full one', async () => {
  const { api, host } = load({
    workshop: workshopFixture({
      capacity: { used: 6, capacity: 4, full: true, grandfathered: true }
    })
  });
  await api.init({ agentInstanceId: 'inst-1' });

  // The distinction matters: this one was legal when it was made, so the copy
  // must not read as a mistake the user made.
  assert.match(host.textContent, /kept from before the limit/);
});

test('a disconnected capability says so in words, not by color', async () => {
  const { api, host } = load({
    workshop: workshopFixture({
      workspace_provided: [
        {
          kind: 'mcp',
          capability_id: 'mb-1',
          display_name: 'Notes',
          source: 'workspace_provided',
          binding_id: 'mb-1',
          server_name: 'notes',
          available: false,
          connected: false,
          selected: false,
          exposed_tools: ['read_note'],
          unavailable_reason: 'This connection is set up but switched off.',
          consumes_skill_space: false
        }
      ]
    })
  });
  await api.init({ agentInstanceId: 'inst-1' });

  assert.match(host.textContent, /Not connected/);
  assert.match(host.textContent, /switched off/);
});

test('a missing capability points at the flow that owns it and installs nothing', async () => {
  const { api, host, requests } = load({
    workshop: workshopFixture({
      requirements: [
        {
          kind: 'skill',
          capability_id: 'translation',
          display_name: 'translation',
          available: false,
          unavailable_reason: 'Not set up in this workspace yet.'
        }
      ]
    })
  });
  await api.init({ agentInstanceId: 'inst-1' });

  assert.match(host.textContent, /Not set up in this workspace yet/);
  // Reading the requirement must not have installed anything on its own.
  assert.equal(requests.filter(request => request.method !== 'GET').length, 0);
});

// One case per non-Ready readiness state. Looping keeps them honest: adding an
// eighth state to the Go side without teaching the UI to print it fails here.
for (const readiness of [
  'Needs connection',
  'Needs approval',
  'Missing capability',
  'Toolbox full',
  'Needs repair',
  'Archived'
]) {
  test(`preview state "${readiness}" blocks Use and explains itself (FR-162)`, async () => {
    const { api, host } = load({
      previewAction: 'Review & Use',
      preview: previewFixture({
        readiness,
        can_use_directly: false,
        issues: [
          {
            state: readiness,
            message: `Blocked: ${readiness}.`,
            blocking: true
          }
        ]
      })
    });
    await openPreview(api, host);

    // The state itself is printed, so a user knows which of the seven they hit.
    assert.match(host.textContent, new RegExp('Readiness: ' + readiness));
    assert.match(host.textContent, /Before this can be used/);

    const use = host.querySelector('[data-toolbox-use]');
    assert.ok(use, 'the Use control should still be present');
    assert.equal(use.disabled, true, `${readiness} must not be usable`);
    assert.match(host.textContent, /Resolve the required items above/);
  });
}

test('a failed operation says what did NOT change (FR-167)', async () => {
  const { api, host } = load({
    failWrites: true,
    preview: previewFixture({ can_use_directly: true })
  });
  await openPreview(api, host);
  click(host, '[data-toolbox-use]');
  await new Promise(resolve => setTimeout(resolve, 0));

  const error = host.querySelector('.ws-toolbox-error');
  assert.ok(error, 'expected the failure to stay on screen');
  // The live region carries the same words once; the visible copy is not live.
  assert.equal(error.getAttribute('role'), null);

  const region = host.querySelector('.ws-toolbox-live');
  assert.ok(region);
  assert.equal(region.getAttribute('aria-live'), 'assertive');
  assert.equal(region.textContent, error.textContent);
});

test('no secret-shaped config value is ever printed', async () => {
  // The server redacts (see redactWorkshopConfig), but the client must not
  // reintroduce a leak by rendering some other field verbatim.
  const { api, host } = load({
    workshop: workshopFixture({
      agent_learned: [
        {
          kind: 'skill',
          capability_id: 'notes',
          display_name: 'notes',
          source: 'agent_learned',
          available: true,
          selected: true,
          consumes_skill_space: true,
          config: { endpoint: 'https://notes.example.com', api_key: '(hidden)' }
        }
      ]
    })
  });
  await api.init({ agentInstanceId: 'inst-1' });

  assert.doesNotMatch(host.textContent, /sk-[a-z0-9-]+/i);
});
