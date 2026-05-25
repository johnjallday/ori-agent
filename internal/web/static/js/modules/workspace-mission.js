/**
 * workspace-mission.js
 *
 * Controls goal settings on the workspace detail page. Talks to the mission
 * HTTP endpoints (GET/PUT /api/workspaces/{id}/mission, POST .../trigger,
 * POST .../baseline). Lives as a standalone module rather than being folded
 * into the giant workspace-detail.js so it can be reviewed and iterated on
 * independently.
 *
 * Lifecycle:
 *   - Init on DOMContentLoaded; read the workspace ID from window.currentWorkspaceId
 *     or the URL.
 *   - Load mission state immediately for the visible Workspace Goal card.
 *   - Refresh button + after-save pull a fresh state snapshot.
 */

(function () {
  'use strict';

  const SELECTORS = {
    tab: '#workspace-detail-config-mission-tab',
    pane: '#workspace-detail-config-mission-pane',
    form: '#workspace-detail-mission-form',
    statusBody: '#workspace-detail-mission-status-body',
    statusText: '#workspace-detail-mission-status-text',
    warning: '#workspace-detail-mission-bindings-warning',
    unclassifiedList: '#workspace-detail-mission-unclassified-list',
    refreshBtn: '#workspace-detail-mission-refresh',
    saveBtn: '#workspace-detail-mission-save',
    baselineBtn: '#workspace-detail-mission-baseline',
    triggerBtn: '#workspace-detail-mission-trigger',
    enabled: '#workspace-detail-mission-enabled',
    text: '#workspace-detail-mission-text',
    autonomyRadios: 'input[name="workspace-detail-mission-autonomy"]',
    cadenceType: '#workspace-detail-mission-cadence-type',
    cadenceTime: '#workspace-detail-mission-cadence-time',
    cadenceDow: '#workspace-detail-mission-cadence-dow',
    cadenceDom: '#workspace-detail-mission-cadence-dom',
    cadenceInterval: '#workspace-detail-mission-cadence-interval',
    cadenceTimeWrap: '#workspace-detail-mission-cadence-time-wrap',
    cadenceDowWrap: '#workspace-detail-mission-cadence-dow-wrap',
    cadenceDomWrap: '#workspace-detail-mission-cadence-dom-wrap',
    cadenceIntervalWrap: '#workspace-detail-mission-cadence-interval-wrap',
    enabledHint: '#workspace-detail-mission-enabled-hint',
    notifPriority: '#workspace-detail-mission-notif-priority',
    notifOnFindings: '#workspace-detail-mission-notif-onfindings',
    goalCard: '#workspace-detail-goal-card',
    goalStatus: '#workspace-detail-goal-status',
    goalTitle: '#workspace-detail-goal-title',
    goalText: '#workspace-detail-goal-text',
    goalCadence: '#workspace-detail-goal-cadence',
    goalNextRun: '#workspace-detail-goal-next-run',
    goalLastRun: '#workspace-detail-goal-last-run',
    goalActionStatus: '#workspace-detail-goal-action-status',
    goalEditBtn: '#workspace-detail-goal-edit',
    goalRunBtn: '#workspace-detail-goal-run',
    goalFindingsBtn: '#workspace-detail-goal-findings',
    goalModal: '#workspace-detail-goal-modal',
    goalModalForm: '#workspace-detail-goal-modal-form',
    goalModalTitle: '#workspace-detail-goal-modal-title',
    goalModalText: '#workspace-detail-goal-modal-text',
    goalModalEnabled: '#workspace-detail-goal-modal-enabled',
    goalModalStatus: '#workspace-detail-goal-modal-status',
    goalModalSaveBtn: '#workspace-detail-goal-modal-save',
    goalModalAdvancedBtn: '#workspace-detail-goal-modal-advanced',
    goalModalCadenceType: '#workspace-detail-goal-modal-cadence-type',
    goalModalCadenceTime: '#workspace-detail-goal-modal-cadence-time',
    goalModalCadenceDow: '#workspace-detail-goal-modal-cadence-dow',
    goalModalCadenceDom: '#workspace-detail-goal-modal-cadence-dom',
    goalModalCadenceInterval: '#workspace-detail-goal-modal-cadence-interval',
    goalModalCadenceTimeWrap: '#workspace-detail-goal-modal-cadence-time-wrap',
    goalModalCadenceDowWrap: '#workspace-detail-goal-modal-cadence-dow-wrap',
    goalModalCadenceDomWrap: '#workspace-detail-goal-modal-cadence-dom-wrap',
    goalModalCadenceIntervalWrap: '#workspace-detail-goal-modal-cadence-interval-wrap',
    goalModalEnabledHint: '#workspace-detail-goal-modal-enabled-hint',
    goalModalAutonomy: '#workspace-detail-goal-modal-autonomy',
    goalModalBindingsWarning: '#workspace-detail-goal-modal-bindings-warning',
    goalModalUnclassifiedList: '#workspace-detail-goal-modal-unclassified-list',
  };

  // Plain-language summary of each autonomy policy. The quick-edit modal only
  // displays the current value (read-only) — it's changed in Advanced settings.
  const AUTONOMY_LABELS = {
    watch: { label: 'Watch', desc: 'reports findings only, makes no changes' },
    propose: { label: 'Propose', desc: 'may draft inside the workspace, no external changes' },
  };

  let latestState = null;

  // Guards against overlapping runs and keeps the goal card's Run button
  // disabled while a run is in flight (see renderGoalCard).
  let runInProgress = false;
  const RUN_POLL_INTERVAL_MS = 2000;
  const RUN_POLL_TIMEOUT_MS = 120000;

  // Cadence intervals come back from the API as Go durations in nanoseconds.
  const NS_PER_HOUR = 3.6e12;

  // The goal cadence editor exists twice — in the quick-edit modal and the
  // advanced form. Both share identical logic over different element ids, so
  // describe each surface once and drive the shared helpers below from it.
  const CADENCE_SURFACES = {
    form: {
      type: SELECTORS.cadenceType,
      time: SELECTORS.cadenceTime,
      dow: SELECTORS.cadenceDow,
      dom: SELECTORS.cadenceDom,
      interval: SELECTORS.cadenceInterval,
      timeWrap: SELECTORS.cadenceTimeWrap,
      dowWrap: SELECTORS.cadenceDowWrap,
      domWrap: SELECTORS.cadenceDomWrap,
      intervalWrap: SELECTORS.cadenceIntervalWrap,
      enabled: SELECTORS.enabled,
      enabledHint: SELECTORS.enabledHint,
    },
    modal: {
      type: SELECTORS.goalModalCadenceType,
      time: SELECTORS.goalModalCadenceTime,
      dow: SELECTORS.goalModalCadenceDow,
      dom: SELECTORS.goalModalCadenceDom,
      interval: SELECTORS.goalModalCadenceInterval,
      timeWrap: SELECTORS.goalModalCadenceTimeWrap,
      dowWrap: SELECTORS.goalModalCadenceDowWrap,
      domWrap: SELECTORS.goalModalCadenceDomWrap,
      intervalWrap: SELECTORS.goalModalCadenceIntervalWrap,
      enabled: SELECTORS.goalModalEnabled,
      enabledHint: SELECTORS.goalModalEnabledHint,
    },
  };

  function $(sel) {
    return document.querySelector(sel);
  }

  function getWorkspaceId() {
    if (window.currentWorkspaceId) return window.currentWorkspaceId;
    // Fallback: parse from URL.
    const parts = window.location.pathname.split('/');
    if (parts[1] === 'workspaces' && parts[2]) return parts[2];
    return '';
  }

  function setStatusText(msg, kind) {
    const el = $(SELECTORS.statusText);
    if (!el) return;
    el.textContent = msg || '';
    el.style.color = kind === 'error' ? 'var(--danger-color, #c0392b)' : '';
  }

  function fmtTime(iso) {
    if (!iso) return '—';
    try {
      const d = new Date(iso);
      if (Number.isNaN(d.getTime())) return iso;
      return d.toLocaleString();
    } catch {
      return iso;
    }
  }

  function setGoalActionStatus(msg, kind) {
    const el = $(SELECTORS.goalActionStatus);
    if (!el) return;
    el.textContent = msg || '';
    el.classList.toggle('is-error', kind === 'error');
  }

  function setGoalModalStatus(msg, kind) {
    const el = $(SELECTORS.goalModalStatus);
    if (!el) return;
    el.textContent = msg || '';
    el.classList.toggle('is-error', kind === 'error');
  }

  function cadenceLabel(cadence) {
    const config = cadence || {};
    const type = config.type || '';
    if (!type) return 'Manual only';
    if (type === 'daily') return `Daily at ${config.time_of_day || '09:00'}`;
    if (type === 'weekly') {
      const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
      const day = typeof config.day_of_week === 'number' ? days[config.day_of_week] : 'Monday';
      return `Weekly ${day || 'Monday'} at ${config.time_of_day || '09:00'}`;
    }
    if (type === 'monthly') {
      const day = typeof config.day_of_month === 'number' ? config.day_of_month : 1;
      return `Monthly day ${day} at ${config.time_of_day || '09:00'}`;
    }
    if (type === 'interval') {
      const hours = config.interval ? Math.max(1, Math.round(config.interval / NS_PER_HOUR)) : 24;
      return `Every ${hours} hour${hours === 1 ? '' : 's'}`;
    }
    return type;
  }

  // Show only the cadence sub-fields relevant to the selected type, and keep
  // the "enable automation" toggle honest: a goal with no cadence can't run on
  // its own, so force the toggle off + disabled and reveal the hint. Shared by
  // both the modal and the advanced form via their CADENCE_SURFACES entry.
  function applyCadenceVisibility(fields, type) {
    const wraps = {
      daily: ['timeWrap'],
      weekly: ['timeWrap', 'dowWrap'],
      monthly: ['timeWrap', 'domWrap'],
      interval: ['intervalWrap'],
    };
    const visible = new Set(wraps[type] || []);
    for (const key of ['timeWrap', 'dowWrap', 'domWrap', 'intervalWrap']) {
      const el = $(fields[key]);
      if (el) el.style.display = visible.has(key) ? '' : 'none';
    }
    const manualOnly = !type;
    const enabled = $(fields.enabled);
    if (enabled) {
      if (manualOnly) enabled.checked = false;
      enabled.disabled = manualOnly;
    }
    const hint = $(fields.enabledHint);
    if (hint) hint.style.display = manualOnly ? '' : 'none';
  }

  // Fill a cadence editor's inputs from a cadence object and sync visibility.
  function populateCadenceFields(fields, cadence) {
    const config = cadence || {};
    const setVal = (sel, val) => {
      const el = $(sel);
      if (el) el.value = val;
    };
    setVal(fields.type, config.type || '');
    if (config.time_of_day) setVal(fields.time, config.time_of_day);
    if (typeof config.day_of_week === 'number') setVal(fields.dow, String(config.day_of_week));
    if (typeof config.day_of_month === 'number') setVal(fields.dom, String(config.day_of_month));
    if (config.interval) {
      setVal(fields.interval, String(Math.max(1, Math.round(config.interval / NS_PER_HOUR))));
    }
    applyCadenceVisibility(fields, config.type || '');
  }

  // Build a cadence object from a cadence editor's inputs (null = manual only).
  function readCadenceFields(fields) {
    const type = $(fields.type).value;
    if (type === 'daily') {
      return { type: 'daily', time_of_day: $(fields.time).value || '09:00' };
    }
    if (type === 'weekly') {
      return {
        type: 'weekly',
        time_of_day: $(fields.time).value || '09:00',
        day_of_week: parseInt($(fields.dow).value, 10),
      };
    }
    if (type === 'monthly') {
      return {
        type: 'monthly',
        time_of_day: $(fields.time).value || '09:00',
        day_of_month: parseInt($(fields.dom).value, 10) || 1,
      };
    }
    if (type === 'interval') {
      const hours = parseInt($(fields.interval).value, 10) || 24;
      return { type: 'interval', interval: hours * NS_PER_HOUR };
    }
    return null;
  }

  function populateGoalModalFromState(state) {
    const current = state || latestState || {};
    const mission = String(current.mission || '');
    const title = $(SELECTORS.goalModalTitle);
    const text = $(SELECTORS.goalModalText);
    const enabled = $(SELECTORS.goalModalEnabled);

    if (title) title.textContent = mission.trim() ? 'Edit goal' : 'Set goal';
    if (text) text.value = mission;
    if (enabled) enabled.checked = !!current.mission_enabled;
    populateCadenceFields(CADENCE_SURFACES.modal, current.cadence);
    renderGoalModalAutonomy(current);
    renderGoalModalBindingsWarning(current);
    setGoalModalStatus('');
  }

  // Show the active autonomy policy in the modal so first-time users (who may
  // never open Advanced settings) can see how the manager will act.
  function renderGoalModalAutonomy(state) {
    const el = $(SELECTORS.goalModalAutonomy);
    if (!el) return;
    const policy = (state && state.autonomy_policy) || 'propose';
    const info = AUTONOMY_LABELS[policy] || AUTONOMY_LABELS.propose;
    el.innerHTML = `Autonomy: <strong>${info.label}</strong> — ${info.desc}. Adjust in Advanced settings.`;
  }

  function readGoalModalState() {
    return {
      mission: $(SELECTORS.goalModalText).value,
      cadence: readCadenceFields(CADENCE_SURFACES.modal),
      mission_enabled: $(SELECTORS.goalModalEnabled).checked,
    };
  }

  function openGoalModal() {
    populateGoalModalFromState(latestState);
    const modalEl = $(SELECTORS.goalModal);
    if (typeof bootstrap !== 'undefined' && modalEl) {
      bootstrap.Modal.getOrCreateInstance(modalEl).show();
    }
    setTimeout(() => {
      const text = $(SELECTORS.goalModalText);
      if (text) text.focus();
    }, 120);
  }

  function hideGoalModal() {
    const modalEl = $(SELECTORS.goalModal);
    if (typeof bootstrap !== 'undefined' && modalEl) {
      bootstrap.Modal.getOrCreateInstance(modalEl).hide();
    }
  }

  function missionStatus(state) {
    const mission = String(state.mission || '').trim();
    if (!mission) return { label: 'Not set', className: 'is-empty' };
    const hasCadence = !!(state.cadence && state.cadence.type);
    // "Enabled" means automation will actually fire — that requires a cadence.
    // An enabled goal with no cadence never runs on its own, so report it as
    // manual-only rather than showing a misleading green badge.
    if (state.mission_enabled && hasCadence) return { label: 'Enabled', className: 'is-enabled' };
    if (hasCadence) return { label: 'Paused', className: 'is-paused' };
    return { label: 'Manual only', className: 'is-manual' };
  }

  function renderGoalCard(state) {
    if (!$(SELECTORS.goalCard)) return;
    const mission = String(state.mission || '').trim();
    const status = missionStatus(state);
    const statusEl = $(SELECTORS.goalStatus);
    const titleEl = $(SELECTORS.goalTitle);
    const textEl = $(SELECTORS.goalText);
    const cadenceEl = $(SELECTORS.goalCadence);
    const nextEl = $(SELECTORS.goalNextRun);
    const lastEl = $(SELECTORS.goalLastRun);
    const editBtn = $(SELECTORS.goalEditBtn);
    const runBtn = $(SELECTORS.goalRunBtn);

    if (statusEl) {
      statusEl.textContent = status.label;
      statusEl.className = `workspace-detail-goal-status ${status.className}`;
    }
    if (titleEl) titleEl.textContent = mission ? 'Current goal' : 'No goal set';
    if (textEl) {
      textEl.textContent = mission || 'No workspace goal yet.';
      textEl.classList.toggle('is-empty', !mission);
    }
    if (cadenceEl) cadenceEl.textContent = `Cadence: ${cadenceLabel(state.cadence)}`;
    if (nextEl) nextEl.textContent = `Next: ${fmtTime(state.next_mission_run_at)}`;
    if (lastEl) lastEl.textContent = `Last: ${fmtTime(state.last_mission_run_at)}`;
    if (editBtn) editBtn.textContent = mission ? 'Edit goal' : 'Set goal';
    if (runBtn) {
      runBtn.disabled = !mission || runInProgress;
      runBtn.title = mission ? 'Run this goal check now' : 'Set a goal before running';
    }

    // Scope the Findings link to this workspace so the user lands on its
    // opportunities rather than the cross-workspace firehose, and show how many
    // are open so the card reflects whether runs are producing anything.
    const findingsBtn = $(SELECTORS.goalFindingsBtn);
    if (findingsBtn) {
      const wsId = getWorkspaceId();
      findingsBtn.href = wsId
        ? `/action-center?workspace=${encodeURIComponent(wsId)}`
        : '/action-center';
      const openFindings = Number(state.open_findings_count) || 0;
      findingsBtn.textContent = openFindings > 0 ? `Findings (${openFindings})` : 'Findings';
      findingsBtn.classList.toggle('has-findings', openFindings > 0);
    }

    // Don't touch the action-status line mid-run — handleRunClick owns it then
    // (the live "Running…" / result message must not be cleared by a poll).
    if (!runInProgress) {
      const mcp = Array.isArray(state.unclassified_mcp_ids) ? state.unclassified_mcp_ids : [];
      const skills = Array.isArray(state.unclassified_skill_ids) ? state.unclassified_skill_ids : [];
      if (mission && mcp.length + skills.length > 0) {
        setGoalActionStatus('Classify MCP or skill bindings before scheduled runs.', 'error');
      } else {
        setGoalActionStatus('');
      }
    }
  }

  function renderGoalCardError(message) {
    if (!$(SELECTORS.goalCard)) return;
    const statusEl = $(SELECTORS.goalStatus);
    const titleEl = $(SELECTORS.goalTitle);
    const textEl = $(SELECTORS.goalText);
    const cadenceEl = $(SELECTORS.goalCadence);
    const nextEl = $(SELECTORS.goalNextRun);
    const lastEl = $(SELECTORS.goalLastRun);
    const runBtn = $(SELECTORS.goalRunBtn);
    if (statusEl) {
      statusEl.textContent = 'Unavailable';
      statusEl.className = 'workspace-detail-goal-status is-empty';
    }
    if (titleEl) titleEl.textContent = 'Goal unavailable';
    if (textEl) {
      textEl.textContent = message || 'Could not load workspace goal.';
      textEl.classList.add('is-empty');
    }
    if (cadenceEl) cadenceEl.textContent = 'Cadence: unavailable';
    if (nextEl) nextEl.textContent = 'Next: —';
    if (lastEl) lastEl.textContent = 'Last: —';
    if (runBtn) runBtn.disabled = true;
    setGoalActionStatus(message || 'Could not load workspace goal.', 'error');
  }

  function renderStatus(state) {
    const body = $(SELECTORS.statusBody);
    if (!body) return;
    const enabled = state.mission_enabled ? 'Enabled' : 'Paused';
    const nextRun = fmtTime(state.next_mission_run_at);
    const lastRun = fmtTime(state.last_mission_run_at);
    const runs = state.mission_execution_count || 0;
    const failures = state.mission_failure_count || 0;
    body.innerHTML = `
      <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 0.5rem 1rem;">
        <div><strong>State:</strong> ${enabled}</div>
        <div><strong>Next run:</strong> ${nextRun}</div>
        <div><strong>Last run:</strong> ${lastRun}</div>
        <div><strong>Runs:</strong> ${runs} <span style="color: var(--muted-color);">(${failures} failed)</span></div>
      </div>
    `;
  }

  // Counts of bindings that still need a side-effect classification before the
  // goal can run (manually or on a schedule).
  function unclassifiedCounts(state) {
    const mcp = Array.isArray(state.unclassified_mcp_ids) ? state.unclassified_mcp_ids.length : 0;
    const skills = Array.isArray(state.unclassified_skill_ids) ? state.unclassified_skill_ids.length : 0;
    return { mcp, skills, total: mcp + skills };
  }

  function unclassifiedSummaryHTML(state) {
    const { mcp, skills } = unclassifiedCounts(state);
    const parts = [];
    if (mcp) parts.push(`<strong>${mcp}</strong> MCP binding${mcp === 1 ? '' : 's'}`);
    if (skills) parts.push(`<strong>${skills}</strong> skill binding${skills === 1 ? '' : 's'}`);
    return `Affected: ${parts.join(' and ')}.`;
  }

  function renderBindingsWarning(state) {
    const wrap = $(SELECTORS.warning);
    const list = $(SELECTORS.unclassifiedList);
    if (!wrap) return;
    if (unclassifiedCounts(state).total === 0) {
      wrap.style.display = 'none';
      return;
    }
    wrap.style.display = 'block';
    if (list) list.innerHTML = unclassifiedSummaryHTML(state);
  }

  // Surface the same readiness warning inside the quick-edit modal so users see
  // it up front instead of hitting a 412 only after pressing Save.
  function renderGoalModalBindingsWarning(state) {
    const wrap = $(SELECTORS.goalModalBindingsWarning);
    const list = $(SELECTORS.goalModalUnclassifiedList);
    if (!wrap) return;
    if (unclassifiedCounts(state).total === 0) {
      wrap.style.display = 'none';
      return;
    }
    wrap.style.display = 'block';
    if (list) list.innerHTML = unclassifiedSummaryHTML(state);
  }

  function populateFormFromState(state) {
    $(SELECTORS.text).value = state.mission || '';
    $(SELECTORS.enabled).checked = !!state.mission_enabled;

    // Autonomy
    const radios = document.querySelectorAll(SELECTORS.autonomyRadios);
    const policy = state.autonomy_policy || 'propose';
    radios.forEach((r) => {
      r.checked = r.value === policy;
    });

    populateCadenceFields(CADENCE_SURFACES.form, state.cadence);

    // Notification policy
    const notif = state.notification_policy || {};
    $(SELECTORS.notifPriority).value = notif.min_priority || '';
    $(SELECTORS.notifOnFindings).value = notif.on_findings || '';

    // Starter hint placeholder based on workspace name in the page header.
    applyStarterHint();
  }

  function applyStarterHint() {
    const textarea = $(SELECTORS.text);
    if (!textarea || textarea.value || textarea.placeholder) return; // Don't override user input or existing placeholder.
    const titleEl = document.querySelector('.workspace-detail-title, h1');
    if (!titleEl) return;
    const name = titleEl.textContent.toLowerCase();
    const hints = {
      brand: 'Keep brand voice, visuals, and messaging consistent across channels.',
      home: 'Watch over home devices, maintenance, and recurring household needs.',
      finance: 'Monitor finance for risks, opportunities, and reconciliation issues.',
      health: 'Track health routines, appointments, and check-ins.',
      travel: 'Maintain trip plans, bookings, and travel-readiness checklists.',
    };
    for (const [key, hint] of Object.entries(hints)) {
      if (name.includes(key)) {
        textarea.placeholder = hint;
        return;
      }
    }
  }

  function readFormState() {
    const policy = Array.from(document.querySelectorAll(SELECTORS.autonomyRadios)).find((r) => r.checked);
    const cadence = readCadenceFields(CADENCE_SURFACES.form);

    const minPriority = $(SELECTORS.notifPriority).value;
    const onFindings = $(SELECTORS.notifOnFindings).value;
    let notification_policy = null;
    if (minPriority || onFindings) {
      notification_policy = {};
      if (minPriority) notification_policy.min_priority = minPriority;
      if (onFindings) notification_policy.on_findings = onFindings;
    }

    return {
      mission: $(SELECTORS.text).value,
      autonomy_policy: policy ? policy.value : 'propose',
      cadence,
      notification_policy,
      mission_enabled: $(SELECTORS.enabled).checked,
    };
  }

  async function fetchMissionState() {
    const wsId = getWorkspaceId();
    if (!wsId) throw new Error('workspace id unavailable');
    const resp = await fetch(`/api/workspaces/${encodeURIComponent(wsId)}/mission`);
    if (!resp.ok) throw new Error(`status ${resp.status}`);
    return resp.json();
  }

  async function saveMission(payload) {
    const wsId = getWorkspaceId();
    const resp = await fetch(`/api/workspaces/${encodeURIComponent(wsId)}/mission`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({}));
      const err = new Error(body.message || `status ${resp.status}`);
      err.code = body.code;
      err.status = resp.status;
      throw err;
    }
    return resp.json();
  }

  async function triggerMission(endpoint) {
    const wsId = getWorkspaceId();
    const resp = await fetch(`/api/workspaces/${encodeURIComponent(wsId)}/mission/${endpoint}`, {
      method: 'POST',
    });
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({}));
      const err = new Error(body.message || `status ${resp.status}`);
      err.code = body.code;
      err.status = resp.status;
      throw err;
    }
    return resp.json();
  }

  async function reload() {
    try {
      const state = await fetchMissionState();
      latestState = state;
      renderGoalCard(state);
      populateFormFromState(state);
      populateGoalModalFromState(state);
      renderStatus(state);
      renderBindingsWarning(state);
      setStatusText('');
    } catch (e) {
      renderGoalCardError(`Failed to load goal: ${e.message}`);
      setStatusText(`Failed to load goal settings: ${e.message}`, 'error');
    }
  }

  function openGoalSettings() {
    if (
      window.workspaceDetail &&
      typeof window.workspaceDetail.setWorkspaceConfigExpanded === 'function'
    ) {
      window.workspaceDetail.setWorkspaceConfigExpanded(true);
    } else {
      const panel = document.getElementById('workspace-detail-settings-panel');
      const content = document.getElementById('workspace-detail-config-content');
      const toggle = document.getElementById('workspace-detail-config-toggle');
      const label = document.getElementById('workspace-detail-config-toggle-label');
      if (panel) panel.classList.remove('is-collapsed');
      if (content) content.hidden = false;
      if (toggle) toggle.setAttribute('aria-expanded', 'true');
      if (label) label.textContent = 'Hide Configuration';
    }

    const tab = $(SELECTORS.tab);
    if (tab && typeof bootstrap !== 'undefined' && bootstrap.Tab) {
      bootstrap.Tab.getOrCreateInstance(tab).show();
    } else if (tab) {
      tab.click();
    }

    setTimeout(() => {
      const textarea = $(SELECTORS.text);
      if (textarea) textarea.focus();
    }, 80);
  }

  function sleep(ms) {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }

  // Repaint the read-only surfaces (goal card + advanced status strip) without
  // touching the editable form/modal inputs, so a poll mid-run never clobbers
  // anything the user is typing.
  function renderReadOnly(state) {
    renderGoalCard(state);
    renderStatus(state);
    renderBindingsWarning(state);
  }

  // Trigger a goal run and wait for it to actually finish. The execution
  // counter always advances on completion (success or failure), so poll
  // GetMission until it moves past the pre-run value. Returns
  // {done, failed, runId}; done=false means it's still running at the timeout.
  async function executeRun(endpoint, onProgress) {
    const before = latestState || {};
    const beforeRuns = before.mission_execution_count || 0;
    const beforeFailures = before.mission_failure_count || 0;

    const r = await triggerMission(endpoint);
    if (onProgress) onProgress(`Run started (${r.run_id || 'no id'}) — working…`);

    const deadline = Date.now() + RUN_POLL_TIMEOUT_MS;
    while (Date.now() < deadline) {
      await sleep(RUN_POLL_INTERVAL_MS);
      let state;
      try {
        state = await fetchMissionState();
      } catch {
        continue; // transient error — keep trying until the deadline
      }
      latestState = state;
      renderReadOnly(state);
      if ((state.mission_execution_count || 0) > beforeRuns) {
        return {
          done: true,
          failed: (state.mission_failure_count || 0) > beforeFailures,
          runId: r.run_id,
        };
      }
    }
    return { done: false, runId: r.run_id };
  }

  // Shared handler for every run button (card "Run now", advanced "Run now"
  // and baseline). Disables the button with a live "Running…" label, polls for
  // completion, then reports the real outcome instead of just "started".
  async function handleRunClick(endpoint, button, report) {
    if (runInProgress) return;
    runInProgress = true;
    let originalLabel;
    if (button) {
      button.disabled = true;
      originalLabel = button.textContent;
      button.textContent = 'Running…';
    }
    report('Starting run…');

    let message = 'Run complete.';
    let kind = null;
    try {
      const result = await executeRun(endpoint, (msg) => report(msg));
      if (!result.done) {
        message = 'Still running — this is taking a while. Use Refresh to check status.';
        kind = 'error';
      } else if (result.failed) {
        message = 'Run finished with an error. Check the Action Center or server logs.';
        kind = 'error';
      }
    } catch (e) {
      kind = 'error';
      if (e.status === 412) {
        message = 'Classify workspace bindings before running.';
      } else if (e.status === 503) {
        message = 'Goal runner is not configured on this server.';
      } else {
        message = `Run failed: ${e.message}`;
      }
    }

    runInProgress = false;
    if (button) {
      button.disabled = false;
      if (originalLabel !== undefined) button.textContent = originalLabel;
    }
    // Restore the card's button/bindings state, then set the result message
    // last so the render pass doesn't overwrite it.
    renderGoalCard(latestState || {});
    report(message, kind);
  }

  function wireForm() {
    const cadenceType = $(SELECTORS.cadenceType);
    if (cadenceType) {
      cadenceType.addEventListener('change', () =>
        applyCadenceVisibility(CADENCE_SURFACES.form, cadenceType.value)
      );
    }

    const goalModalCadenceType = $(SELECTORS.goalModalCadenceType);
    if (goalModalCadenceType) {
      goalModalCadenceType.addEventListener('change', () =>
        applyCadenceVisibility(CADENCE_SURFACES.modal, goalModalCadenceType.value)
      );
    }

    const goalModalForm = $(SELECTORS.goalModalForm);
    if (goalModalForm) {
      goalModalForm.addEventListener('submit', async (evt) => {
        evt.preventDefault();
        const payload = readGoalModalState();
        if (payload.mission_enabled && !String(payload.mission || '').trim()) {
          setGoalModalStatus('Add a goal before enabling automation.', 'error');
          return;
        }
        if (payload.mission_enabled && !payload.cadence) {
          setGoalModalStatus('Pick a cadence before enabling automation.', 'error');
          return;
        }

        const saveBtn = $(SELECTORS.goalModalSaveBtn);
        if (saveBtn) saveBtn.disabled = true;
        setGoalModalStatus('Saving...');
        try {
          await saveMission(payload);
          await reload();
          hideGoalModal();
          setGoalActionStatus('Goal saved.');
        } catch (e) {
          if (e.status === 412) {
            setGoalModalStatus('Classify workspace bindings before enabling automation.', 'error');
          } else {
            setGoalModalStatus(`Save failed: ${e.message}`, 'error');
          }
        } finally {
          if (saveBtn) saveBtn.disabled = false;
        }
      });
    }

    const goalModalAdvancedBtn = $(SELECTORS.goalModalAdvancedBtn);
    if (goalModalAdvancedBtn) {
      goalModalAdvancedBtn.addEventListener('click', () => {
        hideGoalModal();
        openGoalSettings();
      });
    }

    const form = $(SELECTORS.form);
    if (form) {
      form.addEventListener('submit', async (evt) => {
        evt.preventDefault();
        const payload = readFormState();
        if (payload.mission_enabled && !payload.cadence) {
          setStatusText('Pick a cadence before enabling goal automation.', 'error');
          return;
        }
        const saveBtn = $(SELECTORS.saveBtn);
        if (saveBtn) saveBtn.disabled = true;
        setStatusText('Saving...');
        try {
          await saveMission(payload);
          setStatusText('Goal settings saved.');
          await reload();
          setGoalActionStatus('Goal saved.');
        } catch (e) {
          if (e.status === 412) {
            setStatusText('Classify your workspace bindings before enabling goal automation.', 'error');
            setGoalActionStatus('Classify workspace bindings before enabling this goal.', 'error');
          } else {
            setStatusText(`Save failed: ${e.message}`, 'error');
            setGoalActionStatus(`Save failed: ${e.message}`, 'error');
          }
        } finally {
          if (saveBtn) saveBtn.disabled = false;
        }
      });
    }

    // Advanced status strip + card both reflect run progress/outcome.
    const reportAdvanced = (msg, kind) => {
      setStatusText(msg, kind);
      setGoalActionStatus(msg, kind);
    };

    const baselineBtn = $(SELECTORS.baselineBtn);
    if (baselineBtn) {
      baselineBtn.addEventListener('click', () =>
        handleRunClick('baseline', baselineBtn, reportAdvanced)
      );
    }

    const triggerBtn = $(SELECTORS.triggerBtn);
    if (triggerBtn) {
      triggerBtn.addEventListener('click', () =>
        handleRunClick('trigger', triggerBtn, reportAdvanced)
      );
    }

    const refreshBtn = $(SELECTORS.refreshBtn);
    if (refreshBtn) refreshBtn.addEventListener('click', reload);

    const goalEditBtn = $(SELECTORS.goalEditBtn);
    if (goalEditBtn) goalEditBtn.addEventListener('click', openGoalModal);

    const goalRunBtn = $(SELECTORS.goalRunBtn);
    if (goalRunBtn) {
      goalRunBtn.addEventListener('click', () =>
        handleRunClick('trigger', goalRunBtn, (msg, kind) => setGoalActionStatus(msg, kind))
      );
    }
  }

  function init() {
    // Bail only if neither the visible goal card nor the advanced settings tab
    // is present — the card must function on its own, without depending on the
    // advanced tab existing on the page.
    if (!$(SELECTORS.goalCard) && !$(SELECTORS.tab)) return;
    wireForm();

    // The visible goal card needs immediate state. Pages without the card can
    // still lazy-load when the advanced goal settings tab opens.
    let loaded = false;
    const loadOnce = () => {
      if (loaded) return;
      loaded = true;
      reload();
    };

    if ($(SELECTORS.goalCard)) {
      loadOnce();
    }

    const trigger = $(SELECTORS.tab);
    if (trigger) {
      trigger.addEventListener('shown.bs.tab', () => {
        loadOnce();
      });
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
