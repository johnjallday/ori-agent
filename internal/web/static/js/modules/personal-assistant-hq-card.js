// The assistant's one-confirmation HQ proposal in Today. All durable effects
// reuse the HQ form's request identity, root confirmation, and PAF payload.
import {
  chooseHQWorkspaceRoot,
  clearHQRequestID,
  hqBuildRequestPayload,
  hqBuildTarget,
  hqRequestID,
  hqWorkspaceRootView,
  loadHQWorkspaceRoot,
  saveHQWorkspaceRoot,
  skipHQObjective
} from './personal-hq-onboarding.js';
import { JUST_HIRED_FLAG } from './personal-assistant-hire.js';

export function hqCardView(relationship, rootState, plan = {}) {
  const state = String(relationship?.state || '');
  const receipt = Array.isArray(plan.receipt) && plan.receipt.length > 0;
  const visible = ['needs_hq', 'provisioning_hq'].includes(state) || receipt;
  const root = hqWorkspaceRootView(rootState);
  const building = state === 'provisioning_hq' && !plan.failed;
  const collapsed = plan.collapsed === true && !receipt;
  return {
    visible,
    receipt,
    collapsed,
    building,
    name: String(relationship?.display_name || '').trim() || 'Your assistant',
    root,
    canBuild: visible && !receipt && !collapsed && !building && root.confirmed,
    status: building
      ? 'Building…'
      : !root.confirmed && !collapsed && !receipt
        ? root.status
        : plan.failed
          ? 'The build did not finish. Retry the same request.'
          : ''
  };
}

export function hqReceiptRows(status, plan, directory) {
  const workspace = status?.workspace;
  if (!status?.valid || !workspace) return [];
  const slug = String(workspace.folder_slug || '').trim();
  return [
    {
      kind: 'workspace',
      name: String(workspace.name || plan.name || 'My HQ'),
      detail: '',
      route: slug ? `/workspaces/${encodeURIComponent(slug)}` : ''
    },
    ...(plan.time
      ? [{ kind: 'schedule', name: `Daily Brief at ${plan.time} on weekdays`, detail: '' }]
      : []),
    ...(directory ? [{ kind: 'directory', name: String(directory), detail: '' }] : [])
  ];
}

const plan = {
  name: 'My HQ',
  time: '08:00',
  collapsed: false,
  failed: false,
  receipt: [],
  receiptWorkspaceID: ''
};
const state = { relationship: null, root: null, busy: false, sequence: 0, justHired: false };

function el(id) {
  return document.getElementById(`personalAssistantHQ${id}`);
}

function error(message) {
  const box = el('Error');
  box.textContent = String(message || '');
  box.hidden = !message;
}

function renderReceipt(rows) {
  const list = el('Receipt');
  list.replaceChildren();
  for (const row of rows) {
    const item = document.createElement('li');
    const label =
      row.kind === 'workspace' ? 'Workspace' : row.kind === 'schedule' ? 'Schedule' : 'Directory';
    item.append(`${label} · `);
    if (row.route) {
      const link = document.createElement('a');
      link.href = row.route;
      link.textContent = row.name;
      item.append(link);
    } else {
      item.append(document.createTextNode(row.name));
    }
    list.append(item);
  }
}

function render() {
  const card = el('Card');
  if (!card) return;
  const view = hqCardView(state.relationship, state.root, plan);
  // Once built, the compact canonical Done item owns the presentation. Keep
  // these server-observed rows for its receipt disclosure, not a second card
  // under Needs you.
  card.hidden = !view.visible || view.receipt;
  if (!view.visible) return;
  el('Eyebrow').textContent = state.justHired ? '✓ Mission 01 complete' : 'Personal HQ';
  el('Headline').textContent = view.receipt
    ? `${view.name}’s Personal HQ is ready.`
    : view.collapsed
      ? 'Build My HQ'
      : `${view.name} is hired. Let me set up my Personal HQ, where I prepare your Daily Brief and track follow-ups.`;
  el('Plan').hidden = view.collapsed || view.receipt;
  el('Receipt').hidden = !view.receipt;
  if (view.receipt) renderReceipt(plan.receipt);
  el('Name').value = plan.name;
  el('Time').value = plan.time;
  el('Root').value = view.root.path;
  el('RootStatus').textContent = view.root.status;
  el('Build').textContent = view.building ? 'Building…' : view.collapsed ? 'Build My HQ' : 'Build';
  el('Build').disabled = state.busy || (!view.collapsed && !view.canBuild);
  el('Build').hidden = view.receipt;
  el('Adjust').hidden = view.collapsed || view.receipt;
  el('NotNow').hidden = view.collapsed || view.receipt;
  el('Change').disabled = state.busy || view.building;
  el('Adjust').disabled = state.busy || view.building;
  el('NotNow').disabled = state.busy || view.building;
  el('Status').textContent = view.status;
  document.dispatchEvent(new CustomEvent('personal-assistant:hq-receipt'));
}

async function readJSON(url, options) {
  const response = await fetch(url, options || { headers: { Accept: 'application/json' } });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error || 'Could not finish Personal HQ setup. Retry.');
  return body;
}

async function load(relationship = null) {
  const seq = ++state.sequence;
  try {
    const current =
      relationship || (await readJSON('/api/personal-assistant')).personal_assistant || null;
    const root = await loadHQWorkspaceRoot();
    const hq = await readJSON('/api/personal-hq/status');
    if (seq !== state.sequence) return;
    state.relationship = current;
    state.root = root;
    if (current?.state === 'active' && hq?.status?.valid) {
      // After a reload the HQ and its schedule are still canonical, but the
      // current workspace-root setting is not proof of the original directory.
      // Keep that third row only for the fresh build in this session.
      if (!plan.receipt.length || plan.receiptWorkspaceID !== hq.status.workspace?.id) {
        const time = String(current.daily_brief?.schedule_time || '').trim();
        plan.receipt = hqReceiptRows(hq.status, { time }, '');
        plan.receiptWorkspaceID = hq.status.workspace?.id || '';
      }
    } else {
      plan.receipt = [];
      plan.receiptWorkspaceID = '';
    }
    plan.collapsed = hq?.status?.hq_onboarding_state === 'skipped' && !plan.receipt.length;
    if (['needs_hq', 'provisioning_hq'].includes(current?.state) && !plan.collapsed) {
      try {
        state.justHired = window.sessionStorage.getItem(JUST_HIRED_FLAG) === '1';
        if (state.justHired) window.sessionStorage.removeItem(JUST_HIRED_FLAG);
      } catch (_) {
        // Storage is optional; the card itself still renders.
      }
    }
    render();
  } catch (_) {
    // Never enable a build if its root or relationship cannot be confirmed.
    if (seq !== state.sequence) return;
    state.root = null;
    render();
    if (state.relationship?.state === 'needs_hq')
      error('Could not verify the workspace directory. Retry.');
  }
}

async function build() {
  if (plan.collapsed) {
    plan.collapsed = false;
    render();
    return;
  }
  if (state.busy || !hqCardView(state.relationship, state.root, plan).canBuild) return;
  plan.name = el('Name').value.trim() || 'My HQ';
  plan.time = el('Time').value || '08:00';
  state.busy = true;
  plan.failed = false;
  error('');
  render();
  try {
    const latest = (await readJSON('/api/personal-assistant')).personal_assistant;
    const target = hqBuildTarget(latest);
    const root = await loadHQWorkspaceRoot();
    if (
      !target.paf ||
      !hqWorkspaceRootView(root).confirmed ||
      root.workspace_root !== state.root?.workspace_root
    ) {
      throw new Error('The workspace directory or relationship changed. Check the plan and retry.');
    }
    let timezone = 'UTC';
    try {
      timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    } catch (_) {
      // The server validates the fallback too.
    }
    const result = await readJSON(target.endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(
        hqBuildRequestPayload(
          target,
          {
            name: plan.name,
            timezone,
            schedule_days: ['mon', 'tue', 'wed', 'thu', 'fri'],
            schedule_time: plan.time,
            scope: 'all',
            include_future_workspaces: true,
            notify_on_ready: false
          },
          hqRequestID(window.localStorage)
        )
      )
    });
    const relationship = result.personal_assistant;
    if (relationship?.state !== 'active')
      throw new Error('HQ is still being built. Retry this request.');
    clearHQRequestID(window.localStorage);
    const hq = (await readJSON('/api/personal-hq/status')).status;
    const rows = hqReceiptRows(hq, plan, hqWorkspaceRootView(root).path);
    if (!rows.length) throw new Error('HQ was built. Reload to see its workspace.');
    plan.receipt = rows;
    plan.receiptWorkspaceID = hq?.workspace?.id || '';
    state.relationship = relationship;
    for (const name of [
      'ori:personal-hq-changed',
      'ori:workspaces-changed',
      'ori:progression-refresh'
    ]) {
      window.dispatchEvent(new CustomEvent(name, { detail: { source: 'personal_hq_setup' } }));
    }
    await window.PersonalAssistantPanel?.refresh?.();
    await window.PersonalAssistantToday?.refresh?.();
    await window.PersonalAssistantFolder?.reload?.();
  } catch (cause) {
    plan.failed = true;
    error(cause?.message || 'Could not build Personal HQ. Retry the same request.');
  } finally {
    state.busy = false;
    render();
  }
}

async function changeRoot() {
  if (state.busy) return;
  state.busy = true;
  error('');
  render();
  try {
    const path = await chooseHQWorkspaceRoot();
    if (path) state.root = await saveHQWorkspaceRoot(path);
  } catch (cause) {
    error(cause?.message || 'Could not choose the workspace directory.');
  } finally {
    state.busy = false;
    render();
  }
}

async function notNow() {
  if (state.busy) return;
  state.busy = true;
  error('');
  render();
  try {
    await skipHQObjective();
    plan.collapsed = true;
    window.dispatchEvent(new CustomEvent('ori:progression-refresh'));
  } catch (cause) {
    error(cause?.message || 'Could not defer Personal HQ. Try again.');
  } finally {
    state.busy = false;
    render();
  }
}

async function adjust() {
  if (state.busy) return;
  plan.name = el('Name').value.trim() || 'My HQ';
  plan.time = el('Time').value || '08:00';
  el('Name').value = plan.name;
  const modalName = document.getElementById('hqBuildName');
  const modalTime = document.getElementById('hqBuildTime');
  if (modalName) modalName.value = plan.name;
  if (modalTime) modalTime.value = plan.time;
  if (window.PersonalHQOnboarding?.openBuildModal) {
    await window.PersonalHQOnboarding.openBuildModal();
  } else {
    error('The HQ form is unavailable. Reload to try again.');
  }
}

function init() {
  if (!el('Card')) return;
  el('Build').addEventListener('click', build);
  el('Change').addEventListener('click', changeRoot);
  el('Adjust').addEventListener('click', adjust);
  el('NotNow').addEventListener('click', notNow);
  for (const [field, key] of [
    ['Name', 'name'],
    ['Time', 'time']
  ]) {
    el(field).addEventListener('input', event => {
      plan[key] = event.target.value;
    });
  }
  document.addEventListener('personal-assistant:status', event => {
    const relationship = event.detail?.personalAssistant;
    if (relationship && !state.busy) void load(relationship);
  });
  window.addEventListener('ori:hq-quest-signal', event => {
    if (event.detail?.stage === 'setup-succeeded') void load();
  });
  const current = window.PersonalAssistantPanel?._state?.personalAssistant;
  if (current) void load(current);
}

const api = {
  load,
  receiptRows: () =>
    state.relationship?.state === 'active' ? plan.receipt.map(row => ({ ...row })) : [],
  show: () => {
    plan.collapsed = false;
    render();
  },
  _state: state
};
if (typeof window !== 'undefined') window.PersonalAssistantHQCard = api;
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
