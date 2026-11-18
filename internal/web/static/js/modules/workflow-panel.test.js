import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

function fixture() {
  const attributes = {},
    list = {
      innerHTML: '',
      contains: () => false,
      querySelectorAll: () => [],
      setAttribute(k, v) {
        attributes[k] = v;
      }
    };
  let focused = 0;
  const refresh = {
    disabled: false,
    focus() {
      focused++;
    },
    setAttribute(k, v) {
      attributes[`refresh-${k}`] = v;
    }
  };
  const category = { value: '', innerHTML: '' },
    content = { style: {} },
    chevron = { style: {} };
  const toggle = {
    setAttribute(k, v) {
      attributes[`toggle-${k}`] = v;
    }
  };
  const nodes = {
    'workflow-list': list,
    'workflow-refresh': refresh,
    'workflow-category-filter': category,
    'workflow-panel-content': content,
    'workflow-panel-chevron': chevron,
    'workflow-panel-toggle': toggle
  };
  const doc = {
    activeElement: null,
    getElementById: id => nodes[id] || null,
    createElement: () => ({
      textContent: '',
      get innerHTML() {
        return String(this.textContent)
          .replaceAll('&', '&amp;')
          .replaceAll('<', '&lt;')
          .replaceAll('>', '&gt;');
      }
    })
  };
  const context = { window: {}, document: doc, console: { error() {} }, confirm: () => false };
  vm.runInNewContext(
    readFileSync(new URL('./workflow-panel.js', import.meta.url), 'utf8'),
    context
  );
  const panel = new context.window.WorkflowPanel('host', 'owned-workspace');
  return {
    panel,
    context,
    doc,
    list,
    refresh,
    category,
    content,
    chevron,
    attributes,
    focused: () => focused
  };
}
const workflow = {
  id: 'owned-workflow',
  name: '<literal title>',
  source: 'custom',
  category: 'Local review',
  description: 'Long fictional description. '.repeat(10),
  nodes: [{ type: 'task' }]
};
test('saved rows use shared native actions and badges, not another card/control skin', () => {
  const f = fixture();
  const html = f.panel.renderWorkflowItem(workflow);
  assert.match(html, /data-workflow-action="delete"/);
  assert.match(html, /data-workflow-action="instantiate"/);
  assert.match(html, /modern-btn modern-btn-danger/);
  assert.match(html, /badge badge-success/);
  assert.match(html, /&lt;literal title&gt;/);
  assert.ok(html.includes(workflow.description));
  assert.doesNotMatch(html, /onclick=|#3b82f6|#10b981|font-size: 0.65rem/);
  assert.doesNotMatch(
    f.panel.renderWorkflowItem({ ...workflow, source: 'built-in' }),
    /data-workflow-action="delete"/
  );
});
test('workflow IDs and category values stay inert in quoted attributes', () => {
  const f = fixture();
  const html = f.panel.renderWorkflowItem({ ...workflow, id: '" autofocus onfocus="bad' });
  assert.match(html, /data-workflow-id="&quot; autofocus onfocus=&quot;bad"/);
  f.panel.workflows = [{ ...workflow, category: '" literal category' }];
  f.panel.renderWorkflowList();
  assert.match(f.category.innerHTML, /value="&quot; literal category"/);
});
test('native collapse keeps expanded state in sync with visible content', () => {
  const f = fixture();
  f.panel.toggleCollapse();
  assert.equal(f.attributes['toggle-aria-expanded'], 'false');
  assert.equal(f.content.style.display, 'none');
  f.panel.toggleCollapse();
  assert.equal(f.attributes['toggle-aria-expanded'], 'true');
  assert.equal(f.content.style.display, 'block');
});
test('a failed availability read is unknown, never a false all-agents-available claim', async () => {
  const f = fixture();
  f.panel.workflows = [{ ...workflow }];
  f.context.fetch = async () => ({ ok: false });
  await f.panel.checkAllAgentAvailability();
  assert.equal(f.panel.workflows[0].allAgentsAvailable, false);
  assert.equal(f.panel.workflows[0].agentCheckUnavailable, true);
  assert.match(
    f.panel.renderWorkflowItem(f.panel.workflows[0]),
    /Agent availability could not be checked/
  );
});
test('a library fetch failure is distinct from a genuinely empty library and retains category', async () => {
  const f = fixture();
  f.panel.categoryFilter = 'Local review';
  f.context.fetch = async () => ({ ok: false });
  await f.panel.loadWorkflows();
  assert.match(f.list.innerHTML, /role="alert"/);
  assert.match(f.list.innerHTML, /Unable to load saved workflows/);
  assert.doesNotMatch(f.list.innerHTML, /No workflows saved yet/);
  assert.match(f.category.innerHTML, /value="Local review" selected/);
  assert.equal(f.attributes['aria-busy'], 'false');
});
test('refresh coalesces concurrent requests without disabling the actual invoker', async () => {
  const f = fixture();
  let calls = 0,
    release;
  const pending = new Promise(resolve => {
    release = resolve;
  });
  f.context.fetch = async () => {
    calls++;
    return pending;
  };
  const first = f.panel.loadWorkflows();
  await f.panel.loadWorkflows();
  assert.equal(calls, 1);
  assert.equal(f.refresh.disabled, false);
  assert.equal(f.attributes['refresh-aria-busy'], 'true');
  release({ ok: true, json: async () => ({ workflows: [] }) });
  await first;
  assert.equal(f.attributes['refresh-aria-busy'], 'false');
  assert.match(f.list.innerHTML, /No workflows saved yet/);
});
test('redrawing a list restores only its own focus, not an outside editor', () => {
  const f = fixture();
  f.panel.workflows = [workflow];
  f.panel.filteredWorkflows = [workflow];
  f.panel.renderWorkflowList();
  assert.equal(f.focused(), 0);
  f.list.contains = () => true;
  f.doc.activeElement = { dataset: { workflowId: 'removed', workflowAction: 'delete' } };
  f.panel.renderWorkflowList();
  assert.equal(f.focused(), 1);
});
test('canceled deletion never sends a mutation and built-ins remain protected', async () => {
  const f = fixture();
  f.panel.workflows = [workflow];
  f.context.fetch = () => {
    throw new Error('Canceled Delete must not reach the API');
  };
  await f.panel.deleteWorkflow(workflow.id);
  f.panel.workflows = [{ ...workflow, source: 'built-in' }];
  await f.panel.deleteWorkflow(workflow.id);
});
