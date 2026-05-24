/**
 * workspace-mission.js
 *
 * Controls the Mission tab on the workspace detail page. Talks to the mission
 * HTTP endpoints (GET/PUT /api/workspaces/{id}/mission, POST .../trigger,
 * POST .../baseline). Lives as a standalone module rather than being folded
 * into the giant workspace-detail.js so it can be reviewed and iterated on
 * independently.
 *
 * Lifecycle:
 *   - Init on DOMContentLoaded after the workspace ID is known (window.currentWorkspaceId).
 *   - Lazy-load mission state the first time the Mission tab is shown.
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
    notifPriority: '#workspace-detail-mission-notif-priority',
    notifOnFindings: '#workspace-detail-mission-notif-onfindings',
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

  function renderBindingsWarning(state) {
    const wrap = $(SELECTORS.warning);
    const list = $(SELECTORS.unclassifiedList);
    if (!wrap) return;
    const mcp = Array.isArray(state.unclassified_mcp_ids) ? state.unclassified_mcp_ids : [];
    const skills = Array.isArray(state.unclassified_skill_ids) ? state.unclassified_skill_ids : [];
    const total = mcp.length + skills.length;
    if (total === 0) {
      wrap.style.display = 'none';
      return;
    }
    wrap.style.display = 'block';
    const parts = [];
    if (mcp.length) parts.push(`<strong>${mcp.length}</strong> MCP binding${mcp.length === 1 ? '' : 's'}`);
    if (skills.length) parts.push(`<strong>${skills.length}</strong> skill binding${skills.length === 1 ? '' : 's'}`);
    if (list) list.innerHTML = `Affected: ${parts.join(' and ')}.`;
  }

  function applyCadenceVisibility(type) {
    const wraps = {
      daily: ['cadenceTimeWrap'],
      weekly: ['cadenceTimeWrap', 'cadenceDowWrap'],
      monthly: ['cadenceTimeWrap', 'cadenceDomWrap'],
      interval: ['cadenceIntervalWrap'],
    };
    const visible = new Set(wraps[type] || []);
    for (const key of ['cadenceTimeWrap', 'cadenceDowWrap', 'cadenceDomWrap', 'cadenceIntervalWrap']) {
      const el = $(SELECTORS[key]);
      if (el) el.style.display = visible.has(key) ? '' : 'none';
    }
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

    // Cadence
    const cadence = state.cadence || {};
    const type = cadence.type || '';
    $(SELECTORS.cadenceType).value = type;
    if (cadence.time_of_day) $(SELECTORS.cadenceTime).value = cadence.time_of_day;
    if (typeof cadence.day_of_week === 'number') $(SELECTORS.cadenceDow).value = String(cadence.day_of_week);
    if (typeof cadence.day_of_month === 'number') $(SELECTORS.cadenceDom).value = String(cadence.day_of_month);
    if (cadence.interval) {
      // Interval comes back as a Go duration in nanoseconds (number).
      const hours = Math.max(1, Math.round(cadence.interval / 3.6e12));
      $(SELECTORS.cadenceInterval).value = String(hours);
    }
    applyCadenceVisibility(type);

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
    const cadenceType = $(SELECTORS.cadenceType).value;
    let cadence = null;
    if (cadenceType === 'daily') {
      cadence = { type: 'daily', time_of_day: $(SELECTORS.cadenceTime).value || '09:00' };
    } else if (cadenceType === 'weekly') {
      cadence = {
        type: 'weekly',
        time_of_day: $(SELECTORS.cadenceTime).value || '09:00',
        day_of_week: parseInt($(SELECTORS.cadenceDow).value, 10),
      };
    } else if (cadenceType === 'monthly') {
      cadence = {
        type: 'monthly',
        time_of_day: $(SELECTORS.cadenceTime).value || '09:00',
        day_of_month: parseInt($(SELECTORS.cadenceDom).value, 10) || 1,
      };
    } else if (cadenceType === 'interval') {
      const hours = parseInt($(SELECTORS.cadenceInterval).value, 10) || 24;
      cadence = { type: 'interval', interval: hours * 3.6e12 }; // hours → nanoseconds
    }

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
      populateFormFromState(state);
      renderStatus(state);
      renderBindingsWarning(state);
      setStatusText('');
    } catch (e) {
      setStatusText(`Failed to load mission: ${e.message}`, 'error');
    }
  }

  function wireForm() {
    const cadenceType = $(SELECTORS.cadenceType);
    if (cadenceType) {
      cadenceType.addEventListener('change', () => applyCadenceVisibility(cadenceType.value));
    }

    const form = $(SELECTORS.form);
    if (form) {
      form.addEventListener('submit', async (evt) => {
        evt.preventDefault();
        const saveBtn = $(SELECTORS.saveBtn);
        if (saveBtn) saveBtn.disabled = true;
        setStatusText('Saving...');
        try {
          await saveMission(readFormState());
          setStatusText('Mission saved.');
          await reload();
        } catch (e) {
          if (e.status === 412) {
            setStatusText('Classify your workspace bindings before enabling this mission.', 'error');
          } else {
            setStatusText(`Save failed: ${e.message}`, 'error');
          }
        } finally {
          if (saveBtn) saveBtn.disabled = false;
        }
      });
    }

    const baselineBtn = $(SELECTORS.baselineBtn);
    if (baselineBtn) {
      baselineBtn.addEventListener('click', async () => {
        baselineBtn.disabled = true;
        setStatusText('Starting baseline run...');
        try {
          const r = await triggerMission('baseline');
          setStatusText(`Baseline run started (${r.run_id || 'no id'}).`);
          await reload();
        } catch (e) {
          if (e.status === 412) {
            setStatusText('Classify your workspace bindings before running.', 'error');
          } else if (e.status === 503) {
            setStatusText('Mission runner is not configured on this server.', 'error');
          } else {
            setStatusText(`Trigger failed: ${e.message}`, 'error');
          }
        } finally {
          baselineBtn.disabled = false;
        }
      });
    }

    const triggerBtn = $(SELECTORS.triggerBtn);
    if (triggerBtn) {
      triggerBtn.addEventListener('click', async () => {
        triggerBtn.disabled = true;
        setStatusText('Triggering mission run...');
        try {
          const r = await triggerMission('trigger');
          setStatusText(`Run started (${r.run_id || 'no id'}).`);
          await reload();
        } catch (e) {
          if (e.status === 412) {
            setStatusText('Classify your workspace bindings before running.', 'error');
          } else if (e.status === 503) {
            setStatusText('Mission runner is not configured on this server.', 'error');
          } else {
            setStatusText(`Trigger failed: ${e.message}`, 'error');
          }
        } finally {
          triggerBtn.disabled = false;
        }
      });
    }

    const refreshBtn = $(SELECTORS.refreshBtn);
    if (refreshBtn) refreshBtn.addEventListener('click', reload);
  }

  function init() {
    if (!$(SELECTORS.tab)) return; // Mission tab not on this page.
    wireForm();

    // Lazy-load the first time the user opens the tab so we don't hit the
    // API on every workspace detail page even when missions are unused.
    let loaded = false;
    const trigger = $(SELECTORS.tab);
    trigger.addEventListener('shown.bs.tab', () => {
      if (loaded) return;
      loaded = true;
      reload();
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
