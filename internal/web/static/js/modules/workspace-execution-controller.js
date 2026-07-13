/**
 * workspace-execution-controller.js
 *
 * Workspace-scoped execution controller/store. It owns the lifetime of task
 * execution monitoring — selected run, active runs, polling + realtime
 * subscription, activity de-duplication, and reconnect state — DECOUPLED from
 * any Bootstrap modal (PRD FR47, FR51, FR65, FR266).
 *
 * Why this exists: today the execution monitor is started/stopped by the task
 * execution modal's shown/hidden events (workspace-detail.js:1683-1685), so
 * closing the modal stops monitoring even while the task is still running. This
 * controller makes monitoring survive any view change; the tray (group 4) and
 * the dedicated task page (group 7) render FROM it.
 *
 * Monitoring is TASK-ID-KEYED, per the group-1 backend audit: the task object
 * (`GET /api/orchestration/tasks?id=`) is the authoritative execution state and
 * the `window.workspaceRealtime` bus pushes `task.*` events. There is no
 * server-side per-line activity log, so activity is re-derived from task-state
 * transitions and de-duped by a stable key — reload/attach resumes without
 * duplicating entries (FR56, FR58, FR127).
 *
 * The core is dependency-injected (fetchTask, subscribeRealtime, timers) so it
 * is exercisable from fixtures without a DOM or network. Group 2 lands it
 * INERT: it is built and tested but not yet wired into the live surfaces.
 */

import { resolveTaskPresentation, PRESENTATION_STATE } from './task-presentation.js';

// Phases describe the monitor's lifecycle for one tracked run, independent of
// how any view chooses to render (collapsed/expanded is a view concern, never
// a monitor concern — that is the whole point of the decoupling).
export const RUN_PHASE = Object.freeze({
  STARTING: 'starting', // action accepted, first authoritative state not seen yet (FR57)
  LIVE: 'live', // actively polling / receiving events
  RECONNECTING: 'reconnecting', // a fetch failed; retained activity, will retry (FR58)
  SETTLED: 'settled' // terminal or needs-input; monitor stopped, run kept available (FR61)
});

const TERMINAL_STATES = new Set([
  PRESENTATION_STATE.COMPLETED,
  PRESENTATION_STATE.FAILED,
  PRESENTATION_STATE.TIMED_OUT,
  PRESENTATION_STATE.CANCELLED,
  PRESENTATION_STATE.SKIPPED
]);

// A state that stops active polling but keeps the run available for the user
// to act on (terminal outcomes, plus needs-input which awaits the user).
function isSettledState(state) {
  return TERMINAL_STATES.has(state) || state === PRESENTATION_STATE.NEEDS_INPUT;
}

export class WorkspaceExecutionController {
  /**
   * @param {object} deps
   * @param {string} deps.workspaceId
   * @param {(taskId:string)=>Promise<object|null>} deps.fetchTask - resolves the authoritative task payload
   * @param {(workspaceId:string, handler:(event:object)=>void)=>(()=>void)} [deps.subscribeRealtime]
   *        - subscribes to workspace realtime events; returns an unsubscribe fn
   * @param {number} [deps.pollIntervalMs=3000]
   * @param {(fn:Function, ms:number)=>*} [deps.setIntervalFn]
   * @param {(handle:*)=>void} [deps.clearIntervalFn]
   * @param {()=>number} [deps.now]
   */
  constructor(deps = {}) {
    this.workspaceId = deps.workspaceId || '';
    this._fetchTask = typeof deps.fetchTask === 'function' ? deps.fetchTask : async () => null;
    this._subscribeRealtime = typeof deps.subscribeRealtime === 'function' ? deps.subscribeRealtime : null;
    this._pollIntervalMs = Number(deps.pollIntervalMs) > 0 ? Number(deps.pollIntervalMs) : 3000;
    this._setInterval = deps.setIntervalFn || ((fn, ms) => setInterval(fn, ms));
    this._clearInterval = deps.clearIntervalFn || (handle => clearInterval(handle));
    this._now = deps.now || (() => Date.now());

    /** @type {Map<string, object>} taskId -> run state */
    this._runs = new Map();
    /** @type {Set<Function>} renderer listeners */
    this._listeners = new Set();
    this._selectedTaskId = '';
    this._realtimeUnsub = null;
    this._disposed = false;
  }

  // ---- lifecycle ----

  /**
   * Begin monitoring a task by ID. Idempotent: tracking an already-tracked task
   * does NOT spawn a second monitor (FR65 — no duplicate monitors). Selects the
   * run unless `select === false`.
   */
  track(taskId, options = {}) {
    const id = String(taskId || '').trim();
    if (!id || this._disposed) return null;

    this._ensureRealtime();

    let run = this._runs.get(id);
    if (!run) {
      run = this._createRun(id);
      this._runs.set(id, run);
      // Kick an immediate authoritative poll, then settle into the interval.
      this._poll(id);
      run.timer = this._setInterval(() => this._poll(id), this._pollIntervalMs);
    }
    if (options.select !== false) this._selectedTaskId = id;
    this._emit();
    return run;
  }

  /** Stop monitoring one task and forget its run. */
  untrack(taskId) {
    const id = String(taskId || '').trim();
    const run = this._runs.get(id);
    if (!run) return;
    this._stopTimer(run);
    this._runs.delete(id);
    if (this._selectedTaskId === id) {
      // Fall back to another active run, else clear the selection.
      const next = this.getActiveRuns()[0] || this.getRuns()[0] || null;
      this._selectedTaskId = next ? next.taskId : '';
    }
    this._emit();
  }

  /** Select which run the tray shows, without affecting any other run (FR55). */
  select(taskId) {
    const id = String(taskId || '').trim();
    if (id && this._runs.has(id)) {
      this._selectedTaskId = id;
      this._emit();
    }
  }

  // ---- reads ----

  getSelectedTaskId() {
    return this._selectedTaskId;
  }

  getSelected() {
    return this._selectedTaskId ? this._runs.get(this._selectedTaskId) || null : null;
  }

  getRun(taskId) {
    return this._runs.get(String(taskId || '').trim()) || null;
  }

  getRuns() {
    return Array.from(this._runs.values());
  }

  /** Runs still in flight (not settled) — drives "N other active runs" (FR53). */
  getActiveRuns() {
    return this.getRuns().filter(run => run.phase !== RUN_PHASE.SETTLED);
  }

  /** Runs that need the user (needs-input or a terminal failure) — attention count. */
  getAttentionRuns() {
    return this.getRuns().filter(run => {
      const s = run.presentation && run.presentation.state;
      return (
        s === PRESENTATION_STATE.NEEDS_INPUT ||
        s === PRESENTATION_STATE.FAILED ||
        s === PRESENTATION_STATE.TIMED_OUT ||
        s === PRESENTATION_STATE.BLOCKED
      );
    });
  }

  // ---- renderer subscription ----

  /** Subscribe a renderer (tray/task page). Returns an unsubscribe fn. */
  subscribe(listener) {
    if (typeof listener !== 'function') return () => {};
    this._listeners.add(listener);
    return () => this._listeners.delete(listener);
  }

  // ---- realtime ----

  /**
   * Handle a workspace realtime event. If it names a tracked task we re-derive
   * authoritative state via an immediate poll rather than trusting the event
   * payload — this keeps one source of truth and lets de-dup absorb repeats
   * (FR58). Exposed for direct testing.
   */
  handleRealtimeEvent(event) {
    const taskId = this._extractTaskId(event);
    if (taskId && this._runs.has(taskId)) {
      this._poll(taskId);
    }
  }

  dispose() {
    this._disposed = true;
    this.getRuns().forEach(run => this._stopTimer(run));
    this._runs.clear();
    if (this._realtimeUnsub) {
      this._realtimeUnsub();
      this._realtimeUnsub = null;
    }
    this._listeners.clear();
  }

  // ---- internals ----

  _createRun(taskId) {
    return {
      taskId,
      phase: RUN_PHASE.STARTING,
      task: null,
      presentation: null,
      activity: [],
      _seenActivityKeys: new Set(),
      startedAt: this._now(),
      lastActivityAt: 0,
      lastStatus: '',
      timer: null
    };
  }

  async _poll(taskId) {
    const run = this._runs.get(taskId);
    if (!run || this._disposed) return;
    let task;
    try {
      task = await this._fetchTask(taskId);
    } catch (_err) {
      // Transient interruption — retain rendered activity, retry next tick (FR58).
      if (run.phase !== RUN_PHASE.SETTLED) run.phase = RUN_PHASE.RECONNECTING;
      this._emit();
      return;
    }
    // The run may have been untracked/disposed while awaiting.
    if (this._disposed || !this._runs.has(taskId)) return;
    if (!task || String(task.id || '') !== taskId) return;

    run.task = task;
    const presentation = resolveTaskPresentation(task);
    const prevStatus = run.lastStatus;
    run.presentation = presentation;
    run.lastStatus = presentation.state;

    // Synthesize an activity entry on a status change, de-duped by a stable key
    // so reload/attach/realtime bursts never double-log (FR58, FR127).
    if (presentation.state !== prevStatus) {
      this._appendActivity(run, {
        kind: 'status',
        state: presentation.state,
        label: presentation.label,
        at: presentation.latestActivityAt || this._now(),
        key: `${taskId}:status:${presentation.state}:${task.updated_at || ''}`
      });
    }
    if (presentation.latestActivityAt) run.lastActivityAt = presentation.latestActivityAt;

    if (isSettledState(presentation.state)) {
      run.phase = RUN_PHASE.SETTLED;
      this._stopTimer(run); // stop polling, but keep the run available (FR61)
    } else {
      run.phase = RUN_PHASE.LIVE;
    }
    this._emit();
  }

  _appendActivity(run, entry) {
    if (!entry || !entry.key) return;
    if (run._seenActivityKeys.has(entry.key)) return;
    run._seenActivityKeys.add(entry.key);
    run.activity.push(entry);
  }

  _ensureRealtime() {
    if (this._realtimeUnsub || !this._subscribeRealtime) return;
    this._realtimeUnsub = this._subscribeRealtime(this.workspaceId, event => this.handleRealtimeEvent(event));
  }

  _extractTaskId(event) {
    const data = event && typeof event === 'object' ? event.data || {} : {};
    const payload = data && typeof data.data === 'object' ? data.data : data;
    return String((payload && payload.task_id) || (data && data.task_id) || '').trim();
  }

  _stopTimer(run) {
    if (run && run.timer != null) {
      this._clearInterval(run.timer);
      run.timer = null;
    }
  }

  _emit() {
    if (this._disposed) return;
    const snapshot = {
      selectedTaskId: this._selectedTaskId,
      runs: this.getRuns(),
      activeRuns: this.getActiveRuns()
    };
    this._listeners.forEach(listener => {
      try {
        listener(snapshot);
      } catch (_err) {
        /* a renderer error must not break the controller */
      }
    });
  }
}

if (typeof window !== 'undefined') {
  window.WorkspaceExecutionController = WorkspaceExecutionController;
}
