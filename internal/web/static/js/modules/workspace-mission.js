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
    heartbeat: '#workspace-detail-mission-heartbeat',
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
    commandSubtitle: '#workspace-command-subtitle',
    commandCard: '#workspace-command-mission-card',
    commandStatus: '#workspace-command-mission-status',
    commandTitle: '#workspace-command-mission-title',
    commandText: '#workspace-command-mission-text',
    commandCadence: '#workspace-command-mission-cadence',
    commandNextRun: '#workspace-command-mission-next-run',
    commandLastRun: '#workspace-command-mission-last-run',
    commandActionStatus: '#workspace-command-mission-action-status',
    commandEditBtn: '#workspace-command-mission-edit',
    commandRunBtn: '#workspace-command-mission-run',
    commandFindingsBtn: '#workspace-command-mission-findings'
  };

  // A goal run applies Propose only when the user chose it and Watch otherwise,
  // including a workspace that never set a level (workspace.EffectiveAutonomyPolicy).
  function effectiveAutonomy(policy) {
    return policy === 'propose' ? 'propose' : 'watch';
  }

  // Plain-language summary of each autonomy policy. The quick-edit modal only
  // displays the current value (read-only) — it's changed in Advanced settings.
  const AUTONOMY_LABELS = {
    watch: { label: 'Watch', desc: 'reads the workspace and suggests work, makes no changes' },
    propose: {
      label: 'Propose',
      desc: 'during a run the agent may also write notes and drafts inside this workspace'
    }
  };

  // The goal leads every goal card: the heading is the goal text itself, and
  // the line under it appears only when there is no goal yet (FR8).
  const EMPTY_GOAL_TITLE = 'No goal yet';
  const EMPTY_GOAL_PROMPT = 'Set a goal so your agents can suggest work';

  function goalCardCopy(mission) {
    const goal = String(mission || '').trim();
    return goal ? { title: goal, text: '' } : { title: EMPTY_GOAL_TITLE, text: EMPTY_GOAL_PROMPT };
  }

  let latestState = null;

  // Guards against overlapping runs and keeps the goal card's Run button
  // disabled while a run is in flight (see renderGoalCard).
  let runInProgress = false;
  let lastRunOutcome = null;
  // The last run's result line ("Run complete — 3 tasks suggested…"). It stays
  // under the goal until the next run starts or the goal state reloads, so a
  // re-render of the card (e.g. after the Backlog refreshes) cannot clear it.
  let lastRunReport = null;
  const RUN_POLL_INTERVAL_MS = 2000;
  // A goal run is bounded server-side at 15 minutes; follow it a little longer.
  const RUN_POLL_TIMEOUT_MS = 16 * 60 * 1000;
  const RUN_IN_FLIGHT_COPY = 'Looking for things to work on…';

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
      enabledHint: SELECTORS.enabledHint
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
      enabledHint: SELECTORS.goalModalEnabledHint
    }
  };

  function $(sel) {
    return document.querySelector(sel);
  }

  function getWorkspaceId() {
    return String(window.currentWorkspaceId || document.body?.dataset?.workspaceId || '');
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

  // Relative time in both directions: "in 3h" for the future, "2d ago" for the
  // past. Falls back to an absolute date once it's more than a week out.
  function fmtRelative(iso) {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    const diff = d.getTime() - Date.now(); // > 0 future, < 0 past
    const abs = Math.abs(diff);
    const ahead = diff >= 0;
    if (abs < 60000) return 'just now';
    const mins = Math.round(abs / 60000);
    if (mins < 60) return ahead ? `in ${mins}m` : `${mins}m ago`;
    const hours = Math.round(abs / 3600000);
    if (hours < 24) return ahead ? `in ${hours}h` : `${hours}h ago`;
    const days = Math.round(abs / 86400000);
    if (days < 7) return ahead ? `in ${days}d` : `${days}d ago`;
    return d.toLocaleDateString();
  }

  // Next scheduled run: nothing scheduled, already due (past but not yet picked
  // up by the scheduler), or a relative time in the future.
  function fmtNextRun(iso) {
    if (!iso) return 'not scheduled';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    if (d.getTime() <= Date.now()) return 'due now';
    return fmtRelative(iso);
  }

  function fmtLastRun(iso) {
    if (!iso) return 'never';
    return fmtRelative(iso);
  }

  function setGoalActionStatus(msg, kind) {
    const el = $(SELECTORS.goalActionStatus);
    if (!el) return;
    el.textContent = msg || '';
    el.classList.toggle('is-error', kind === 'error');
  }

  function setCommandActionStatus(msg, kind) {
    const el = $(SELECTORS.commandActionStatus);
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
      interval: ['intervalWrap']
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
        day_of_week: parseInt($(fields.dow).value, 10)
      };
    }
    if (type === 'monthly') {
      return {
        type: 'monthly',
        time_of_day: $(fields.time).value || '09:00',
        day_of_month: parseInt($(fields.dom).value, 10) || 1
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
    const policy = effectiveAutonomy(state && state.autonomy_policy);
    const info = AUTONOMY_LABELS[policy];
    el.innerHTML = `Autonomy: <strong>${info.label}</strong> — ${info.desc}. Adjust in Advanced settings.`;
  }

  function readGoalModalState() {
    return {
      mission: $(SELECTORS.goalModalText).value,
      cadence: readCadenceFields(CADENCE_SURFACES.modal),
      mission_enabled: $(SELECTORS.goalModalEnabled).checked
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

  function buildFindingsMeta(state) {
    const wsId = getWorkspaceId();
    const openFindings = Number(state.open_findings_count) || 0;
    return {
      href: wsId ? `/action-center?workspace=${encodeURIComponent(wsId)}` : '/action-center',
      label: openFindings > 0 ? `Findings (${openFindings})` : 'Findings',
      hasFindings: openFindings > 0
    };
  }

  // A goal run is in flight when this page started or is following one, or
  // when the server says one is executing (a first run, a cadence run, a run
  // started in another tab).
  function runIsInFlight(state) {
    return runInProgress || !!(state && state.run_in_flight);
  }

  // The line under the goal while nothing is being reported: the run in
  // flight, why a run could not start (e.g. no model), or unclassified bindings.
  function goalStatusLine(state, mission) {
    if (runIsInFlight(state)) return { text: RUN_IN_FLIGHT_COPY, kind: null };
    if (mission && lastRunReport) return lastRunReport;
    const reason = String((state && state.needs_attention) || '').trim();
    if (mission && reason) return { text: reason, kind: 'error' };
    const mcp = Array.isArray(state && state.unclassified_mcp_ids)
      ? state.unclassified_mcp_ids
      : [];
    const skills = Array.isArray(state && state.unclassified_skill_ids)
      ? state.unclassified_skill_ids
      : [];
    if (mission && mcp.length + skills.length > 0) {
      return { text: 'Classify MCP or skill bindings before scheduled runs.', kind: 'error' };
    }
    return { text: '', kind: null };
  }

  function commandSummary(state) {
    const current = state || latestState || {};
    const mission = String(current.mission || '').trim();
    const running = runIsInFlight(current);
    const status = running
      ? { label: 'Running', className: 'is-running' }
      : mission && lastRunOutcome
        ? lastRunOutcome
        : missionStatus(current);
    const findings = buildFindingsMeta(current);
    const statusLine = goalStatusLine(current, mission);
    const actionStatus = statusLine.text;
    const copy = goalCardCopy(mission);
    return {
      mission,
      label: status.label,
      className: status.className,
      title: copy.title,
      text: copy.text,
      cadenceLabel: `Cadence: ${cadenceLabel(current.cadence)}`,
      nextLabel: `Next: ${fmtNextRun(current.next_mission_run_at)}`,
      lastLabel: `Last: ${fmtLastRun(current.last_mission_run_at)}`,
      nextTitle: current.next_mission_run_at ? fmtTime(current.next_mission_run_at) : '',
      lastTitle: current.last_mission_run_at ? fmtTime(current.last_mission_run_at) : '',
      canRun: !!mission && !running,
      runTitle: running
        ? 'A goal run is already in progress'
        : mission
          ? 'Run this goal check now'
          : 'Set a goal before running',
      findingsHref: findings.href,
      findingsLabel: findings.label,
      hasFindings: findings.hasFindings,
      running,
      actionStatus,
      actionStatusKind: statusLine.kind
    };
  }

  function renderCommandSubtitle(state) {
    const el = $(SELECTORS.commandSubtitle);
    if (!el) return;
    const workflow = String(el.getAttribute('data-workflow-label') || '').trim();
    const summary = commandSummary(state);
    const missionPart = summary.label ? `Goal · ${summary.label}` : '';
    const text = [workflow, missionPart].filter(Boolean).join(' · ');
    el.textContent = text;
    el.hidden = !text;
  }

  function renderCommandPanel(state) {
    if (!$(SELECTORS.commandCard)) {
      renderCommandSubtitle(state);
      return;
    }
    const summary = commandSummary(state);
    const statusEl = $(SELECTORS.commandStatus);
    const titleEl = $(SELECTORS.commandTitle);
    const textEl = $(SELECTORS.commandText);
    const cadenceEl = $(SELECTORS.commandCadence);
    const nextEl = $(SELECTORS.commandNextRun);
    const lastEl = $(SELECTORS.commandLastRun);
    const editBtn = $(SELECTORS.commandEditBtn);
    const runBtn = $(SELECTORS.commandRunBtn);
    const findingsBtn = $(SELECTORS.commandFindingsBtn);

    if (statusEl) {
      statusEl.textContent = summary.label;
      statusEl.className = `ws-cmd-mission-status ${summary.className}`;
    }
    if (titleEl) {
      titleEl.textContent = summary.title;
      titleEl.classList.toggle('is-goal', !!summary.mission);
    }
    if (textEl) {
      textEl.textContent = summary.text;
      textEl.hidden = !summary.text;
      textEl.classList.toggle('is-empty', !summary.mission);
    }
    if (cadenceEl) cadenceEl.textContent = summary.cadenceLabel;
    if (nextEl) {
      nextEl.textContent = summary.nextLabel;
      nextEl.title = summary.nextTitle;
    }
    if (lastEl) {
      lastEl.textContent = summary.lastLabel;
      lastEl.title = summary.lastTitle;
    }
    if (editBtn) editBtn.textContent = summary.mission ? 'Edit goal' : 'Set goal';
    if (runBtn) {
      runBtn.disabled = !summary.canRun;
      runBtn.title = summary.runTitle;
    }
    if (findingsBtn) {
      findingsBtn.href = summary.findingsHref;
      findingsBtn.textContent = summary.findingsLabel;
      findingsBtn.classList.toggle('has-findings', summary.hasFindings);
    }
    if (!runInProgress) setCommandActionStatus(summary.actionStatus, summary.actionStatusKind);
    renderCommandSubtitle(state);
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
    const copy = goalCardCopy(mission);
    if (titleEl) titleEl.textContent = copy.title;
    if (textEl) {
      textEl.textContent = copy.text;
      textEl.hidden = !copy.text;
      textEl.classList.toggle('is-empty', !mission);
    }
    if (cadenceEl) cadenceEl.textContent = `Cadence: ${cadenceLabel(state.cadence)}`;
    if (nextEl) {
      nextEl.textContent = `Next: ${fmtNextRun(state.next_mission_run_at)}`;
      nextEl.title = state.next_mission_run_at ? fmtTime(state.next_mission_run_at) : '';
    }
    if (lastEl) {
      lastEl.textContent = `Last: ${fmtLastRun(state.last_mission_run_at)}`;
      lastEl.title = state.last_mission_run_at ? fmtTime(state.last_mission_run_at) : '';
    }
    if (editBtn) editBtn.textContent = mission ? 'Edit goal' : 'Set goal';
    if (runBtn) {
      runBtn.disabled = !mission || runIsInFlight(state);
      runBtn.title = mission ? 'Run this goal check now' : 'Set a goal before running';
    }

    // Scope the Findings link to this workspace so the user lands on its
    // opportunities rather than the cross-workspace firehose, and show how many
    // are open so the card reflects whether runs are producing anything.
    const findingsBtn = $(SELECTORS.goalFindingsBtn);
    if (findingsBtn) {
      const findings = buildFindingsMeta(state);
      findingsBtn.href = findings.href;
      findingsBtn.textContent = findings.label;
      findingsBtn.classList.toggle('has-findings', findings.hasFindings);
    }

    // Don't touch the action-status line mid-run — handleRunClick owns it then
    // (the live "Running…" / result message must not be cleared by a poll).
    if (!runInProgress) {
      const line = goalStatusLine(state, mission);
      setGoalActionStatus(line.text, line.kind);
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
    renderCommandPanelError(message);
  }

  function renderCommandPanelError(message) {
    renderCommandSubtitle({});
    if (!$(SELECTORS.commandCard)) return;
    const statusEl = $(SELECTORS.commandStatus);
    const titleEl = $(SELECTORS.commandTitle);
    const textEl = $(SELECTORS.commandText);
    const cadenceEl = $(SELECTORS.commandCadence);
    const nextEl = $(SELECTORS.commandNextRun);
    const lastEl = $(SELECTORS.commandLastRun);
    const runBtn = $(SELECTORS.commandRunBtn);
    if (statusEl) {
      statusEl.textContent = 'Unavailable';
      statusEl.className = 'ws-cmd-mission-status is-empty';
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
    setCommandActionStatus(message || 'Could not load workspace goal.', 'error');
  }

  function escapeHtml(value) {
    return String(value == null ? '' : value)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#39;');
  }

  const RUN_ORIGIN_LABELS = {
    cadence: 'Scheduled',
    manual: 'Run now',
    first_goal: 'First goal',
    event: 'Event trigger'
  };

  function plural(count, one, many) {
    return `${count} ${count === 1 ? one : many}`;
  }

  // One line per recent goal run: what it created, skipped and dropped (FR21).
  // A run whose answer could not be read keeps the end of that answer behind
  // "Show output", so the user can see what the model actually said.
  function runHistoryHTML(runs) {
    const list = Array.isArray(runs) ? runs : [];
    if (!list.length) return '<p class="workspace-mission-runs-empty">No goal runs yet.</p>';
    const items = list
      .map(run => {
        const when = run.finished_at || run.started_at;
        const origin = RUN_ORIGIN_LABELS[run.origin] || 'Run';
        const outcome = run.succeeded ? 'Completed' : 'Failed';
        const error =
          !run.succeeded && run.error
            ? `<div class="workspace-mission-run-error">${escapeHtml(run.error)}</div>`
            : '';
        const output = run.output_tail
          ? `<details class="workspace-mission-run-output"><summary>Show output</summary><pre>${escapeHtml(run.output_tail)}</pre></details>`
          : '';
        return `
          <li class="workspace-mission-run ${run.succeeded ? 'is-succeeded' : 'is-failed'}">
            <div class="workspace-mission-run-head">
              <strong>${outcome}</strong>
              <span>${escapeHtml(origin)}</span>
              <span title="${escapeHtml(when ? fmtTime(when) : '')}">${escapeHtml(when ? fmtRelative(when) : '')}</span>
            </div>
            <div class="workspace-mission-run-counts">${escapeHtml(runCountsText(run))}</div>
            ${error}${output}
          </li>`;
      })
      .join('');
    return `<ul class="workspace-mission-runs">${items}</ul>`;
  }

  function renderStatus(state) {
    const body = $(SELECTORS.statusBody);
    if (!body) return;
    const enabled = state.mission_enabled ? 'Enabled' : 'Paused';
    const nextRun = fmtNextRun(state.next_mission_run_at);
    const lastRun = fmtLastRun(state.last_mission_run_at);
    const runs = state.mission_execution_count || 0;
    const failures = state.mission_failure_count || 0;
    body.innerHTML = `
      <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 0.5rem 1rem;">
        <div><strong>State:</strong> ${enabled}</div>
        <div><strong>Next run:</strong> ${nextRun}</div>
        <div><strong>Last run:</strong> ${lastRun}</div>
        <div><strong>Runs:</strong> ${runs} <span style="color: var(--muted-color);">(${failures} failed)</span></div>
      </div>
      <div class="workspace-mission-history">
        <div class="workspace-mission-history-title">Run history</div>
        ${runHistoryHTML(state.mission_runs)}
      </div>
    `;
  }

  // Counts of bindings that still need a side-effect classification before the
  // goal can run (manually or on a schedule).
  function unclassifiedCounts(state) {
    const mcp = Array.isArray(state.unclassified_mcp_ids) ? state.unclassified_mcp_ids.length : 0;
    const skills = Array.isArray(state.unclassified_skill_ids)
      ? state.unclassified_skill_ids.length
      : 0;
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
    const heartbeatEl = $(SELECTORS.heartbeat);
    if (heartbeatEl) heartbeatEl.checked = !!state.mission_cadence_heartbeat;

    // Autonomy
    const radios = document.querySelectorAll(SELECTORS.autonomyRadios);
    const policy = effectiveAutonomy(state.autonomy_policy);
    radios.forEach(r => {
      r.checked = r.value === policy;
    });

    populateCadenceFields(CADENCE_SURFACES.form, state.cadence);

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
      travel: 'Maintain trip plans, bookings, and travel-readiness checklists.'
    };
    for (const [key, hint] of Object.entries(hints)) {
      if (name.includes(key)) {
        textarea.placeholder = hint;
        return;
      }
    }
  }

  function readFormState() {
    const policy = Array.from(document.querySelectorAll(SELECTORS.autonomyRadios)).find(
      r => r.checked
    );
    const cadence = readCadenceFields(CADENCE_SURFACES.form);

    // notification_policy is not sent: notifications are a separate feature,
    // and an absent field keeps whatever the workspace already stored.
    const heartbeatEl = $(SELECTORS.heartbeat);
    return {
      mission: $(SELECTORS.text).value,
      autonomy_policy: effectiveAutonomy(policy ? policy.value : ''),
      cadence,
      mission_enabled: $(SELECTORS.enabled).checked,
      mission_cadence_heartbeat: heartbeatEl ? heartbeatEl.checked : false
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
      body: JSON.stringify(payload)
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
      method: 'POST'
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
      lastRunOutcome = null;
      lastRunReport = null;
      renderReadOnly(state);
      populateFormFromState(state);
      populateGoalModalFromState(state);
      setStatusText('');
      afterStateLoaded(state);
    } catch (e) {
      renderGoalCardError(`Failed to load goal: ${e.message}`);
      setStatusText(`Failed to load goal settings: ${e.message}`, 'error');
    }
  }

  // A workspace created with a goal starts its first goal run the first time
  // it is opened (FR25); a run already executing — started on the first goal
  // save, by the cadence or in another tab — is followed until it ends (FR27).
  function afterStateLoaded(state) {
    if (runInProgress || !state) return;
    if (state.first_run_pending) {
      startDeferredFirstRun();
      return;
    }
    if (state.run_in_flight) {
      const detail = state.run_in_flight_detail || {};
      followRun(detail.run_id || '', reportToCards);
    }
  }

  async function startDeferredFirstRun() {
    const wsId = getWorkspaceId();
    if (!wsId) return;
    try {
      const resp = await fetch(`/api/workspaces/${encodeURIComponent(wsId)}/mission/first-open`, {
        method: 'POST'
      });
      if (!resp.ok) return;
      const body = await resp.json().catch(() => ({}));
      const firstRun = body.first_run || {};
      if (firstRun.started) {
        followRun(firstRun.run_id || '', reportToCards);
      } else if (body.pending) {
        // The run could not start; the reason now lives on the goal.
        await reload();
      }
    } catch (e) {
      console.warn('Goal first-open check failed:', e);
    }
  }

  // Report a run's progress on both goal cards (Details and Command view).
  function reportToCards(msg, kind) {
    setGoalActionStatus(msg, kind);
    setCommandActionStatus(msg, kind);
  }

  function openGoalSettings() {
    // Command view: the Goal Settings Systems tab is gone — open the full goal config
    // (autonomy, cadence, notifications) as a modal over the command surface instead.
    if (
      window.workspaceCommand &&
      window.workspaceCommand.active &&
      typeof window.workspaceCommand.openStatModal === 'function'
    ) {
      window.workspaceCommand.openStatModal('mission');
      return;
    }

    // Legacy (non-command) fallback: expand the config panel + mission tab directly.
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
    return new Promise(resolve => setTimeout(resolve, ms));
  }

  // Repaint the read-only surfaces (goal card + advanced status strip + run
  // button modes) without touching the editable form/modal inputs, so a poll
  // mid-run never clobbers anything the user is typing.
  function renderReadOnly(state) {
    renderGoalCard(state);
    renderCommandPanel(state);
    renderStatus(state);
    renderBindingsWarning(state);
    applyRunButtonModes(state);
  }

  // "Run baseline now" and "Run now" are functionally identical; showing both
  // at once is just confusing. The first run is the baseline, so surface only
  // "Run baseline now" until a run has happened, then only "Run now". Also
  // disable whichever is shown when there's no saved goal to run (mirrors the
  // card's Run button) — running uses the saved goal, so an empty one is a
  // no-op that would just fail.
  function applyRunButtonModes(state) {
    const isFirstRun = (state.mission_execution_count || 0) === 0;
    const noGoal = !String(state.mission || '').trim();
    const buttons = [
      [$(SELECTORS.baselineBtn), isFirstRun],
      [$(SELECTORS.triggerBtn), !isFirstRun]
    ];
    for (const [btn, visible] of buttons) {
      if (!btn) continue;
      btn.style.display = visible ? '' : 'none';
      btn.disabled = noGoal || runIsInFlight(state);
      btn.title = noGoal ? 'Set a goal before running' : '';
    }
  }

  // "3 tasks suggested · 2 findings · 1 duplicate skipped" for a run record.
  function runCountsText(run) {
    const counts = [
      plural(run.tasks_created || 0, 'task suggested', 'tasks suggested'),
      plural(run.findings_created || 0, 'finding', 'findings')
    ];
    if (run.duplicates_skipped)
      counts.push(plural(run.duplicates_skipped, 'duplicate skipped', 'duplicates skipped'));
    if (run.dropped_by_cap) counts.push(`${run.dropped_by_cap} over the limit`);
    if (run.items_rejected)
      counts.push(plural(run.items_rejected, 'item unreadable', 'items unreadable'));
    return counts.join(' · ');
  }

  // Poll the goal state until no run is in flight. Returns {done, failed,
  // record, runId}; done=false means it was still running at the deadline.
  async function waitForRunToEnd(runId) {
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
      if (!state.run_in_flight) {
        const runs = Array.isArray(state.mission_runs) ? state.mission_runs : [];
        const record = (runId && runs.find(run => run.run_id === runId)) || runs[0] || null;
        return { done: true, failed: !!record && !record.succeeded, record, runId };
      }
    }
    return { done: false, runId };
  }

  // Start a goal run: the server answers 202 at once and runs it in the
  // background (FR26); a run already in flight (409) is followed instead.
  async function executeRun(endpoint, onProgress) {
    let runId = '';
    try {
      const started = await triggerMission(endpoint);
      runId = (started && started.run_id) || '';
    } catch (e) {
      if (e.status !== 409) throw e;
      if (onProgress) onProgress('A goal run is already in progress — following it.');
    }
    if (onProgress) onProgress(RUN_IN_FLIGHT_COPY);
    return waitForRunToEnd(runId);
  }

  // Turn a finished (or abandoned) run into the card's outcome and message.
  function settleRun(result) {
    if (!result.done) {
      lastRunOutcome = { label: 'Running', className: 'is-running' };
      return rememberRunReport(
        'Still running — this is taking a while. Use Refresh to check status.',
        'error'
      );
    }
    const record = result.record;
    if (result.failed) {
      lastRunOutcome = { label: 'Failed', className: 'is-failed' };
      const error = String((record && record.error) || '')
        .trim()
        .replace(/[.!?]+$/, '');
      const why = error ? `: ${error}.` : '.';
      return rememberRunReport(`Run failed${why} See Goal Settings for details.`, 'error');
    }
    lastRunOutcome = { label: 'Completed', className: 'is-completed' };
    return rememberRunReport(
      record ? `Run complete — ${runCountsText(record)}.` : 'Run complete.',
      null
    );
  }

  function rememberRunReport(message, kind) {
    lastRunReport = { text: message, kind: kind || null };
    return { message, kind: kind || null };
  }

  // A run ended: show its suggestions wherever they appear (FR27).
  function onRunEnded() {
    const page = typeof window !== 'undefined' ? window.workspaceDetail : null;
    if (page && typeof page.loadBacklog === 'function') {
      Promise.resolve(page.loadBacklog()).catch(() => {});
    }
    repaintBacklogDrawer();
  }

  // The Backlog drawer says "Looking for things to work on…" while a run is in
  // flight; repaint it when that changes.
  function repaintBacklogDrawer() {
    const view = typeof window !== 'undefined' ? window.workspaceCommand : null;
    if (view && typeof view.renderBacklogDrawerBody === 'function') view.renderBacklogDrawerBody();
  }

  // Follow a run this page did not start (first goal save, first open, a run
  // already executing when the page loaded) until it ends.
  async function followRun(runId, report) {
    if (runInProgress) return;
    runInProgress = true;
    lastRunOutcome = null;
    lastRunReport = null;
    renderReadOnly(latestState || {});
    repaintBacklogDrawer();
    report(RUN_IN_FLIGHT_COPY);
    const outcome = settleRun(await waitForRunToEnd(runId));
    runInProgress = false;
    renderReadOnly(latestState || {});
    onRunEnded();
    report(outcome.message, outcome.kind);
  }

  // Shared handler for every run button (card "Run now", advanced "Run now"
  // and baseline). Disables the button with a live "Running…" label, follows
  // the run until it ends, then reports the real outcome.
  async function handleRunClick(endpoint, button, report) {
    if (runInProgress) return;
    runInProgress = true;
    lastRunOutcome = null;
    lastRunReport = null;
    let originalLabel;
    if (button) {
      button.disabled = true;
      originalLabel = button.textContent;
      button.textContent = 'Running…';
    }
    repaintBacklogDrawer();
    report('Starting run…');

    let message;
    let kind;
    try {
      ({ message, kind } = settleRun(await executeRun(endpoint, msg => report(msg))));
    } catch (e) {
      kind = 'error';
      if (e.status === 412 && e.code === 'goal_required') {
        message = 'Set a goal before running.';
      } else if (e.status === 412 && e.code === 'mission_bindings_unclassified') {
        message = 'Classify workspace bindings before running.';
      } else if (e.status === 412) {
        // No agent, no model or an unusable toolbox: the server says which.
        message = e.message || 'This goal cannot run yet.';
      } else if (e.status === 503) {
        message = 'Goal runner is not configured on this server.';
      } else {
        message = `Run failed: ${e.message}`;
      }
      lastRunOutcome =
        e.status === 412
          ? { label: 'Blocked', className: 'is-blocked' }
          : { label: 'Failed', className: 'is-failed' };
      rememberRunReport(message, kind);
    }

    runInProgress = false;
    if (button) {
      if (originalLabel !== undefined) button.textContent = originalLabel;
    }
    // Restore the read-only surfaces (card button + advanced run buttons,
    // which may have swapped baseline->run after a first run), then set the
    // result message last so the render pass doesn't overwrite it.
    renderReadOnly(latestState || {});
    onRunEnded();
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
      goalModalForm.addEventListener('submit', async evt => {
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
          lastRunOutcome = null;
          // A first goal starts a run server-side; reload() follows it.
          await reload();
          hideGoalModal();
          if (!runInProgress) setGoalActionStatus('Goal saved.');
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
      form.addEventListener('submit', async evt => {
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
          lastRunOutcome = null;
          setStatusText('Goal settings saved.');
          await reload();
          if (!runInProgress) setGoalActionStatus('Goal saved.');
        } catch (e) {
          if (e.status === 412) {
            setStatusText(
              'Classify your workspace bindings before enabling goal automation.',
              'error'
            );
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

  function runNowFromCommand(button) {
    handleRunClick('trigger', button || $(SELECTORS.commandRunBtn), reportToCards);
  }

  function exposeAPI() {
    if (typeof window === 'undefined') return;
    window.workspaceMission = Object.assign(window.workspaceMission || {}, {
      getState: () => latestState,
      getSummary: () => (latestState ? commandSummary(latestState) : {}),
      // Pure renderers of a given state, for module tests.
      summarize: state => commandSummary(state || {}),
      runHistoryHTML,
      effectiveAutonomy,
      runCountsText,
      settleRun,
      // Whether a goal run is executing, for the Backlog drawer (FR27).
      isRunning: () => runIsInFlight(latestState),
      renderCommandSurfaces: () => {
        if (latestState) renderCommandPanel(latestState);
      },
      openGoalModal,
      runNow: runNowFromCommand,
      reload
    });
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
  exposeAPI();
})();
