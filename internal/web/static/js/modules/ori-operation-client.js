/**
 * ori-operation-client.js
 *
 * Tab-scoped delivery for "Operate Ori". One browser tab that chose to watch
 * carries out the named commands of one operation and reports what it
 * observed. Everything here is per tab (sessionStorage), so another tab never
 * moves because this one asked.
 *
 * Rules this module keeps:
 * - Commands come only from the server's current snapshot, fetched before
 *   anything is carried out. Nothing is replayed from storage.
 * - A full-page navigation is acknowledged by the page that ARRIVES, never by
 *   the page that left.
 * - A page load never navigates by itself. It may finish what the operation
 *   was doing only inside a short window after the user asked; after that, or
 *   when the user is somewhere else or has unsaved work, it waits for Resume.
 * - A tab that turns out to be a copy of another gives up its identity and so
 *   its claim on the view.
 * - Nothing here creates a task. The task form's own save does, and this
 *   module only learns of it from the server's snapshot.
 * - Start is sent only by `start()`, which only the user's click calls. It is
 *   never retried: a Start whose answer was lost is read back, not sent again.
 * - Whether a task is running, waiting, failed or finished comes from the
 *   task through the execution controller. An accepted Start, a timer or a
 *   quiet connection never stands in for it.
 *
 * Dependencies are injected so the protocol is testable without a browser.
 */

import { OUTCOME, isSupportedAction, operationResultURL } from './ori-operation-targets.js';

export const TAB_KEY = 'ori.personalAssistant.tab';
export const WATCH_KEY = 'ori.personalAssistant.watch';
export const OPERATION_KEY = 'ori.personalAssistant.operation';
export const TAB_CHANNEL = 'ori.personalAssistant.tabs';

// How long after the user asked a page load may continue by itself.
export const AUTO_WINDOW_MS = 60 * 1000;
// How long a loading tab waits to hear that its identity is already in use.
export const TAB_GUARD_MS = 200;

// How often a tab that follows a task reads its operation again: quickly while
// a claimed start waits for its run, slowly while nothing is expected to move.
export const FOLLOW_FAST_MS = 3000;
export const FOLLOW_SLOW_MS = 15000;

const ENDPOINT = '/api/home-assistant/operations/';
const TAB_ID = /^[A-Za-z0-9_-]{1,64}$/;
const ENDED = new Set(['cancelled', 'expired', 'invalidated']);
// Statuses in which the bound task is watched through the execution controller.
const MONITORED = new Set(['start_requested', 'started', 'unresolved']);
// How long a page's own execution controller is waited for before this tab
// follows the task with a monitor of its own.
const MONITOR_TRIES = 20;
const MONITOR_RETRY_MS = 300;

export const PHASE = Object.freeze({
  IDLE: 'idle', // no operation in this tab
  RUNNING: 'running', // carrying out commands now
  NAVIGATING: 'navigating', // leaving for the workspace's page
  PAUSED: 'paused', // waiting for the user (moved elsewhere, unsaved work, reload, unreachable)
  WAITING: 'waiting', // steps are pending but this tab is not watching
  SETTLED: 'settled', // no step is pending
  ENDED: 'ended' // cancelled, expired or invalidated
});

function safeGet(storage, key) {
  try {
    return storage.getItem(key) || '';
  } catch (_) {
    return '';
  }
}

function safeSet(storage, key, value) {
  try {
    if (value) storage.setItem(key, value);
    else storage.removeItem(key);
  } catch (_) {
    // A tab without storage still works for the page it is on.
  }
}

/** Pending commands in the order they must run. */
export function pendingCommands(snapshot) {
  return ((snapshot && snapshot.commands) || [])
    .filter(command => command && command.status === 'pending')
    .sort((a, b) => Number(a.sequence || 0) - Number(b.sequence || 0));
}

export class OperationClient {
  /**
   * @param {object} deps
   * @param {(method:string, path:string, body?:object)=>Promise<object>} deps.request
   *        resolves the JSON body, or rejects with {status, data:{code}}
   * @param {Storage} deps.storage - this tab's sessionStorage
   * @param {{execute:Function}} deps.targets - the named-action adapters
   * @param {(url:string)=>void} deps.navigate
   * @param {()=>number} [deps.now]
   * @param {()=>string} [deps.newId]
   * @param {(ms:number)=>Promise<void>} [deps.wait]
   */
  constructor(deps = {}) {
    this._request = deps.request;
    this._storage = deps.storage;
    this._targets = deps.targets;
    this._navigate = deps.navigate || (() => {});
    this._now = deps.now || (() => Date.now());
    this._newId = deps.newId || (() => defaultId());
    this._wait = deps.wait || (ms => new Promise(resolve => setTimeout(resolve, ms)));
    this._taskMonitor = typeof deps.taskMonitor === 'function' ? deps.taskMonitor : null;
    this._later = deps.later || defaultLater;
    this._listeners = new Set();
    this._running = false;
    this._guard = Promise.resolve();
    this._monitored = null;
    this._monitorTries = 0;
    this._cancelMonitorRetry = null;
    this._cancelRefresh = null;
    this._awaitingBinding = false;
    this._startedHere = false;
    // Bumped whenever the request in front of the user changes or is taken
    // over, so a step that was waiting on the page knows not to act.
    this._generation = 0;
    this._halted = false;
    // True after a page load rebuilt this from the server (see restore()).
    this.restored = false;

    this.snapshot = null;
    this.phase = PHASE.IDLE;
    this.pauseReason = '';
    this.highlight = null;
    // Set when the user saved the task form that held a draft from here.
    this.saved = null;
    // The bound task as its own monitor last read it, and how it presents.
    // Execution is shown from these and never from the operation's status.
    this.task = null;
    this.taskPresentation = null;
    this.taskPhase = '';
    // True while this tab's Start request is on its way.
    this.starting = false;
    // Set when a Start request got no answer, so its outcome is not known here.
    this.startUnconfirmed = false;
  }

  // ---- identity and preference ----

  tabId() {
    const stored = safeGet(this._storage, TAB_KEY);
    if (TAB_ID.test(stored)) return stored;
    return this.renewTabId();
  }

  renewTabId() {
    const id =
      String(this._newId() || '')
        .replace(/[^A-Za-z0-9_-]/g, '')
        .slice(0, 64) || 'tab';
    safeSet(this._storage, TAB_KEY, id);
    return id;
  }

  /** Whether this tab chose to let the assistant's operations move its view. */
  watchPreference() {
    return safeGet(this._storage, WATCH_KEY) === '1';
  }

  setWatchPreference(on) {
    safeSet(this._storage, WATCH_KEY, on ? '1' : '');
    this._emit();
  }

  /** What a turn sends about this tab: who is asking and whether it watches. */
  askRef() {
    return { tab_id: this.tabId(), watch: this.watchPreference() };
  }

  subscribe(listener) {
    if (typeof listener !== 'function') return () => {};
    this._listeners.add(listener);
    return () => this._listeners.delete(listener);
  }

  // ---- tab copies ----

  /**
   * A duplicated tab starts with a copy of this tab's storage, identity
   * included. The copy asks whether its identity is in use; the original
   * answers; the copy then takes a new identity and so holds no watch.
   */
  guardTab(channel) {
    if (!channel || typeof channel.postMessage !== 'function') return;
    const nonce = String(this._newId());
    channel.onmessage = event => {
      const message = (event && event.data) || {};
      if (message.nonce === nonce || message.tab !== this.tabId()) return;
      if (message.type === 'hello') channel.postMessage({ type: 'taken', tab: message.tab, nonce });
      if (message.type === 'taken') {
        this.renewTabId();
        this._emit();
      }
    };
    channel.postMessage({ type: 'hello', tab: this.tabId(), nonce });
    this._guard = this._wait(TAB_GUARD_MS);
  }

  // ---- lifecycle ----

  /** A turn just returned this operation for this tab. */
  async adopt(snapshot) {
    if (!snapshot || !snapshot.id) return;
    // A step still waiting for an earlier request must not act for it now.
    this._generation += 1;
    this._halted = false;
    this.restored = false;
    this._remember(snapshot.id);
    this._accept(snapshot);
    await this.run({ allowNavigate: true });
  }

  /**
   * The user took over: they turned to another conversation, or otherwise
   * made clear this request is not what they are doing now. Steps stop, the
   * highlight goes, and nothing is cancelled: the operation stays valid, its
   * task keeps running, and Resume carries on from here.
   */
  standDown(reason) {
    if (!this.snapshot || ENDED.has(this.snapshot.status)) return;
    this._generation += 1;
    this._halted = true;
    this.highlight = null;
    if (pendingCommands(this.snapshot).length) this._pause(reason || 'stood_down');
    else this._emit();
  }

  /** Whether the request a step belongs to is still the one in front of the user. */
  _wanted(snapshot) {
    const generation = this._generation;
    return () =>
      this._generation === generation && Boolean(this.snapshot) && this.snapshot.id === snapshot.id;
  }

  /**
   * A page loaded. Read the operation this tab was following, then continue
   * only what a page load may continue.
   */
  async restore() {
    const pointer = this._pointer();
    if (!pointer) return;
    await this._guard;
    if (!(await this._refresh())) return;
    // What this page shows was rebuilt from the server. Anything the user had
    // typed into a form before the reload is gone, and the drawer says so.
    this.restored = true;
    this._emit();
    if (this.phase !== PHASE.RUNNING) return;
    if (this._now() < pointer.autoUntil) {
      await this.run({ allowNavigate: false });
      return;
    }
    this._pause('reloaded');
  }

  /** The user asked to continue here: watch if needed, then carry on. */
  async resume() {
    if (!this.snapshot || ENDED.has(this.snapshot.status)) return;
    // Resuming a step that waited for unsaved work is the user choosing Ori's
    // step over what they had typed.
    const replace = this.phase === PHASE.PAUSED && this.pauseReason === 'unsaved_work';
    this._halted = false;
    this.restored = false;
    if (!this.snapshot.watching && !(await this.setWatching(true, { adopt: true }))) return;
    this._remember(this.snapshot.id);
    await this.run({ allowNavigate: true, replace });
  }

  /**
   * Show the prepared draft in the task form again, at the user's request. It
   * is a local view of a draft this tab may already read: it reports nothing
   * and changes no operation. Unsaved edits in the form are never replaced.
   * Returns what happened: 'opened', 'kept' (the form holds unsaved work),
   * 'elsewhere' (not on that workspace's page) or 'unavailable'.
   */
  async openDraft() {
    const snapshot = this.snapshot;
    if (!snapshot || !snapshot.draft || ENDED.has(snapshot.status)) return 'unavailable';
    this.restored = false;
    const result = await this._targets.execute(
      {
        action: 'open_task_draft',
        workspace_id: snapshot.context && snapshot.context.workspace_id,
        draft_revision: snapshot.draft.revision
      },
      {
        snapshot,
        replace: false,
        onSaved: payload => this._draftSaved(snapshot.id, payload),
        wanted: this._wanted(snapshot)
      }
    );
    if (result.outcome === OUTCOME.ACKNOWLEDGED) return 'opened';
    if (result.outcome === OUTCOME.YIELD) {
      if (result.reason === 'unsaved_work') return 'kept';
      return result.reason === 'stood_down' ? 'unavailable' : 'elsewhere';
    }
    return 'unavailable';
  }

  /**
   * Open the result of this operation's own run, at the user's request. It is
   * offered only when the server says the task's present result is that run's
   * (`outcome.result === 'current'`): a later run's result is never opened as
   * this one's. Nothing is reported to the server and nothing changes.
   * Returns 'opened' (this page's own result view), 'navigating' (the page's
   * own one-shot result link) or 'unavailable'.
   */
  openResult() {
    const snapshot = this.snapshot;
    const outcome = snapshot && snapshot.outcome;
    const taskId = snapshot && snapshot.task ? String(snapshot.task.task_id || '') : '';
    if (!taskId || !outcome || outcome.result !== 'current') return 'unavailable';
    const context = snapshot.context || {};
    if (this._targets.showResult && this._targets.showResult(context.workspace_id, taskId)) {
      return 'opened';
    }
    const url = operationResultURL(context.workspace_slug, taskId);
    if (!url) return 'unavailable';
    this._navigate(url);
    return 'navigating';
  }

  /** Turn watching on or off for this operation in this tab. Off cancels nothing. */
  async setWatching(watch, { adopt = false } = {}) {
    if (!this.snapshot) return false;
    if (!watch) {
      // Turning Watch off takes effect at once, before the server answers: a
      // step already waiting on the page does nothing, and the mark goes.
      this._generation += 1;
      this.highlight = null;
    }
    try {
      const reply = await this._request(
        'POST',
        ENDPOINT + encodeURIComponent(this.snapshot.id) + '/watch',
        {
          tab_id: this.tabId(),
          revision: this.snapshot.revision,
          watch: Boolean(watch),
          adopt: Boolean(adopt)
        }
      );
      this._accept(reply.operation);
      return true;
    } catch (error) {
      await this._afterRefusal(error);
      return false;
    }
  }

  /**
   * The user saved the task form that held this operation's draft. The form
   * created the task through its own owner. When the server's own record of
   * that task names this operation, the operation now has its task and this
   * tab follows it; the server's snapshot, not this callback, says so. When it
   * does not (the save carried a schedule or inputs, or the request had
   * ended), the task is the user's own and this tab stops offering the draft.
   */
  async _draftSaved(operationId, payload) {
    if (!this.snapshot || this.snapshot.id !== operationId) return;
    const task = (payload && payload.task) || null;
    this.saved = { task };
    if (task && String(task.assistant_operation_id || '') === operationId) {
      this._awaitingBinding = true;
      this._remember(operationId);
      if (!(await this._refresh({ quiet: true }))) this._schedule();
      return;
    }
    await this.dismiss();
  }

  /**
   * The user pressed Start for the task this operation created. The server
   * claims Start once: a second click, another tab or a retry finds the claim
   * and starts nothing. What comes back is an acceptance or a refusal; the
   * task itself is what later shows that it runs.
   */
  async start() {
    const snapshot = this.snapshot;
    if (!snapshot || this.starting) return false;
    if (snapshot.status !== 'reviewed' && snapshot.status !== 'unresolved') return false;
    this.starting = true;
    this.startUnconfirmed = false;
    this._emit();
    try {
      const reply = await this._request(
        'POST',
        ENDPOINT + encodeURIComponent(snapshot.id) + '/start',
        { tab_id: this.tabId() }
      );
      this._startedHere = true;
      this._remember(snapshot.id);
      this.starting = false;
      this._accept(reply.operation);
      return true;
    } catch (error) {
      this.starting = false;
      const refused = error && error.data && error.data.details && error.data.details.operation;
      if (refused && refused.id === snapshot.id) {
        // Refused, with the operation as it now stands: its receipts say why.
        this._accept(refused);
        return false;
      }
      const code = errorCode(error);
      if (code && code !== 'operation_unavailable') {
        await this._afterRefusal(error);
        return false;
      }
      // No answer, or the server could not say. The request may or may not
      // have arrived, so claim nothing about the task until the operation has
      // been read again. Reading never starts anything.
      this.startUnconfirmed = !(await this._refresh({ quiet: true }));
      this._emit();
      return false;
    }
  }

  /** Read the operation again at the user's request. Starts nothing. */
  async check() {
    const ok = await this._refresh({ quiet: true });
    if (ok) this.startUnconfirmed = false;
    this._emit();
    return ok;
  }

  /**
   * Show the bound task's live progress in this page's own execution tray,
   * when this page is the task's workspace. Returns whether it was shown.
   */
  showTask() {
    if (!this.canShowTask()) return false;
    this._monitored.monitor.show(this._monitored.taskId);
    return true;
  }

  /** Whether this page has its own place to show the bound task's progress. */
  canShowTask() {
    return Boolean(this._monitored && typeof this._monitored.monitor.show === 'function');
  }

  /** Dismiss the operation. A task it created or started is not touched. */
  async dismiss() {
    const snapshot = this.snapshot;
    // A step still waiting on the page must not act once this is dismissed.
    this._generation += 1;
    this._forget();
    if (!snapshot || ENDED.has(snapshot.status)) {
      this._clear();
      return;
    }
    try {
      await this._request('POST', ENDPOINT + encodeURIComponent(snapshot.id) + '/cancel', {
        tab_id: this.tabId(),
        revision: snapshot.revision
      });
    } catch (_) {
      // Dismissing is local first: the tab stops following it either way.
    }
    this._clear();
  }

  /**
   * Carry out this tab's pending commands in order, reporting each observed
   * outcome before the next one starts.
   */
  async run({ allowNavigate = false, replace = false } = {}) {
    if (this._running) return;
    this._running = true;
    try {
      let conflicts = 0;
      for (;;) {
        const snapshot = this.snapshot;
        if (this._halted) return;
        if (!snapshot || ENDED.has(snapshot.status) || !snapshot.watching) return;
        const command = pendingCommands(snapshot)[0];
        if (!command) return;
        this._set(PHASE.RUNNING);

        const wanted = this._wanted(snapshot);
        const result = isSupportedAction(command.action)
          ? await this._targets.execute(command, {
              allowNavigate,
              replace,
              snapshot,
              onSaved: payload => this._draftSaved(snapshot.id, payload),
              wanted
            })
          : { outcome: OUTCOME.UNAVAILABLE, reason: 'This step is not supported here.' };

        if (!wanted()) {
          // While the step waited, the request was dismissed, replaced or
          // taken over. Its outcome is not reported and nothing more is done
          // for it; whatever is in front of the user now is looked at afresh.
          if (this._halted) return;
          continue;
        }

        if (result.outcome === OUTCOME.NAVIGATING) {
          // The arriving page reports arrival. Keep following for a short while.
          this._remember(snapshot.id);
          this._set(PHASE.NAVIGATING);
          this._navigate(result.url);
          return;
        }
        if (result.outcome === OUTCOME.YIELD) {
          this._pause(result.reason);
          return;
        }
        try {
          const reply = await this._request(
            'POST',
            ENDPOINT + encodeURIComponent(snapshot.id) + '/ack',
            {
              command_id: command.id,
              revision: snapshot.revision,
              tab_id: this.tabId(),
              outcome: result.outcome,
              observed: result.observed,
              reason: result.reason
            }
          );
          if (result.outcome === OUTCOME.ACKNOWLEDGED && result.element) {
            this.highlight = {
              element: result.element,
              locate: result.locate,
              commandId: command.id
            };
          }
          this._accept(reply.operation);
        } catch (error) {
          const code = errorCode(error);
          // Someone else changed the operation: read it again and look again,
          // a bounded number of times.
          if (code === 'operation_conflict' && conflicts < 2) {
            conflicts += 1;
            if (!(await this._refresh())) return;
            continue;
          }
          await this._afterRefusal(error);
          return;
        }
      }
    } finally {
      this._running = false;
    }
  }

  // ---- internals ----

  /**
   * Read the operation as the server has it now. `quiet` is for reads the
   * user did not ask for (following a task): a failed one changes nothing on
   * screen and is simply tried again later.
   */
  async _refresh({ quiet = false } = {}) {
    const pointer = this._pointer();
    const id = (this.snapshot && this.snapshot.id) || (pointer && pointer.id);
    if (!id) return false;
    try {
      const reply = await this._request(
        'GET',
        ENDPOINT + encodeURIComponent(id) + '?tab=' + encodeURIComponent(this.tabId())
      );
      // The user may have dismissed it while the read was on its way.
      if (quiet && (!this.snapshot || this.snapshot.id !== id)) return false;
      this._accept(reply.operation);
      return true;
    } catch (error) {
      if (errorCode(error) === 'operation_not_found') {
        this._forget();
        this._clear();
        return false;
      }
      // Could not be read right now: keep following, move nothing.
      if (!quiet) this._pause('unreachable');
      return false;
    }
  }

  // ---- following the task ----

  /**
   * Keep the bound task in view: monitor it through the execution controller
   * once a start was claimed, and read the operation again on a slow beat so
   * a change made elsewhere (another tab, the task's own controls, a lost
   * response) shows up. Nothing here starts, retries or completes anything.
   */
  _follow() {
    const snapshot = this.snapshot;
    if (snapshot && snapshot.status !== 'prepared') this._awaitingBinding = false;
    const taskId =
      snapshot && !ENDED.has(snapshot.status) && snapshot.task
        ? String(snapshot.task.task_id || '')
        : '';
    if (taskId && MONITORED.has(snapshot.status)) this._monitor(snapshot, taskId);
    else this._stopMonitor();
    this._schedule();
  }

  _monitor(snapshot, taskId) {
    if (this._monitored && this._monitored.taskId === taskId) return;
    this._stopMonitor();
    if (!this._taskMonitor) return;
    // On the task's own workspace page the page's controller is used, and it
    // mounts after the page's data loads. Ask again shortly, a bounded number
    // of times, then follow with a monitor of this tab's own.
    const monitor = this._taskMonitor(String((snapshot.context || {}).workspace_id || ''), {
      final: this._monitorTries >= MONITOR_TRIES
    });
    if (monitor && monitor.pending) {
      this._monitorTries += 1;
      this._cancelMonitorRetry = this._later(() => {
        this._cancelMonitorRetry = null;
        if (this.snapshot) this._follow();
      }, MONITOR_RETRY_MS);
      return;
    }
    if (!monitor || !monitor.controller) return;
    this._monitorTries = 0;
    // Reveal the page's own tray only for a start the user just made here or
    // is arriving from; otherwise follow without opening anything.
    const pointer = this._pointer();
    const reveal = this._startedHere || Boolean(pointer && this._now() < pointer.autoUntil);
    if (reveal && typeof monitor.show === 'function') monitor.show(taskId);
    else monitor.controller.track(taskId, { select: false });
    const unsubscribe = monitor.controller.subscribe(() => this._taskChanged());
    this._monitored = { taskId, monitor, unsubscribe, seen: '' };
    this._taskChanged();
  }

  _stopMonitor() {
    if (this._cancelMonitorRetry) {
      this._cancelMonitorRetry();
      this._cancelMonitorRetry = null;
    }
    const monitored = this._monitored;
    this._monitored = null;
    this.task = null;
    this.taskPresentation = null;
    this.taskPhase = '';
    if (!monitored) return;
    try {
      if (typeof monitored.unsubscribe === 'function') monitored.unsubscribe();
      // A monitor this tab made for itself goes away with it; the page's own
      // controller keeps running for the page.
      if (monitored.monitor.owned) monitored.monitor.controller.dispose();
    } catch (_) {
      /* a monitor that is already gone needs no cleanup */
    }
  }

  _taskChanged() {
    const monitored = this._monitored;
    if (!monitored) return;
    const run = monitored.monitor.controller.getRun(monitored.taskId);
    const presentation = (run && run.presentation) || null;
    const state = presentation ? String(presentation.state || '') : '';
    const seen = [state, run ? run.phase : '', run ? run.lastActivityAt : ''].join('|');
    if (seen === monitored.seen) return;
    const moved = state !== monitored.seen.split('|')[0];
    monitored.seen = seen;
    this.task = (run && run.task) || null;
    this.taskPresentation = presentation;
    this.taskPhase = run ? String(run.phase || '') : '';
    this._emit();
    // The task moved: read the operation now rather than on the next beat, so
    // the server settles it and says what became of this request's own run.
    if (state && moved && this.snapshot) void this._refresh({ quiet: true });
  }

  /** Whether the operation already names the run it observed. */
  _settledRun(snapshot) {
    return snapshot.status === 'started' && Boolean(snapshot.task && snapshot.task.run_id);
  }

  /** How soon to read the operation again, or 0 for "no need". */
  _refreshInterval() {
    const snapshot = this.snapshot;
    if (!snapshot || snapshot.kind !== 'task' || ENDED.has(snapshot.status)) return 0;
    switch (snapshot.status) {
      case 'prepared':
        // The form reported a bound save the server has not shown yet.
        return this._awaitingBinding ? FOLLOW_FAST_MS : 0;
      case 'start_requested':
        return FOLLOW_FAST_MS;
      case 'reviewed':
      case 'unresolved':
        return FOLLOW_SLOW_MS;
      case 'started': {
        if (this._settledRun(snapshot)) return 0;
        // No run identity yet. Keep looking while the task is still moving.
        const settled = this.taskPhase === 'settled';
        return settled ? 0 : FOLLOW_FAST_MS;
      }
      default:
        return 0;
    }
  }

  _schedule() {
    if (this._cancelRefresh) {
      this._cancelRefresh();
      this._cancelRefresh = null;
    }
    const ms = this._refreshInterval();
    if (!ms) return;
    this._cancelRefresh = this._later(async () => {
      this._cancelRefresh = null;
      // A successful read reschedules itself through _accept.
      if (!(await this._refresh({ quiet: true }))) this._schedule();
    }, ms);
  }

  async _afterRefusal(error) {
    const code = errorCode(error);
    if (code === 'operation_not_found') {
      this._forget();
      this._clear();
      return;
    }
    if (
      code === 'operation_conflict' ||
      code === 'operation_tab_mismatch' ||
      code === 'operation_expired' ||
      code === 'operation_invalidated' ||
      code === 'operation_ended'
    ) {
      // The server knows more than this tab does; show what it says now. A
      // step that is still this tab's to do waits for the user rather than
      // looking as if it were under way.
      if ((await this._refresh()) && this.phase === PHASE.RUNNING) this._pause('conflict');
      return;
    }
    this._pause(code === 'operation_invalid' ? 'refused' : 'unreachable');
  }

  _accept(snapshot) {
    if (!snapshot || !snapshot.id) return;
    if (this.snapshot && this.snapshot.id !== snapshot.id) this._startedHere = false;
    this.snapshot = snapshot;
    this.pauseReason = '';
    // The server has spoken: whatever was unknown about a Start is now known.
    this.startUnconfirmed = false;
    // Follow before announcing, so the first render already has the task.
    this._follow();
    if (ENDED.has(snapshot.status)) this._set(PHASE.ENDED);
    else if (!Number(snapshot.pending || 0)) this._set(PHASE.SETTLED);
    else if (!snapshot.watching) this._set(PHASE.WAITING);
    else this._set(PHASE.RUNNING);
  }

  _pause(reason) {
    this.pauseReason = String(reason || '');
    this._set(PHASE.PAUSED);
  }

  _clear() {
    this.snapshot = null;
    this.highlight = null;
    this.pauseReason = '';
    this._generation += 1;
    this._halted = false;
    this.restored = false;
    this.starting = false;
    this.startUnconfirmed = false;
    this._startedHere = false;
    this._awaitingBinding = false;
    this._monitorTries = 0;
    // Stops following: no monitor and no further reads for a dismissed operation.
    this._follow();
    this._set(PHASE.IDLE);
  }

  _set(phase) {
    this.phase = phase;
    this._emit();
  }

  _emit() {
    this._listeners.forEach(listener => {
      try {
        listener(this);
      } catch (_) {
        /* a renderer error must not break delivery */
      }
    });
  }

  _remember(id) {
    safeSet(
      this._storage,
      OPERATION_KEY,
      JSON.stringify({ id: String(id), auto_until: this._now() + AUTO_WINDOW_MS })
    );
  }

  _forget() {
    safeSet(this._storage, OPERATION_KEY, '');
  }

  _pointer() {
    try {
      const value = JSON.parse(safeGet(this._storage, OPERATION_KEY) || 'null');
      if (!value || typeof value.id !== 'string' || !value.id) return null;
      return { id: value.id, autoUntil: Number(value.auto_until) || 0 };
    } catch (_) {
      return null;
    }
  }
}

function errorCode(error) {
  return String((error && error.data && error.data.code) || '');
}

/** Run fn once after ms; returns how to cancel it. Never keeps a process alive. */
function defaultLater(fn, ms) {
  const handle = setTimeout(fn, ms);
  if (handle && typeof handle.unref === 'function') handle.unref();
  return () => clearTimeout(handle);
}

function defaultId() {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return 'tab-' + Math.random().toString(36).slice(2) + Date.now().toString(36);
}
