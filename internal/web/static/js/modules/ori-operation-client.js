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
 *
 * Dependencies are injected so the protocol is testable without a browser.
 */

import { OUTCOME, isSupportedAction } from './ori-operation-targets.js';

export const TAB_KEY = 'ori.personalAssistant.tab';
export const WATCH_KEY = 'ori.personalAssistant.watch';
export const OPERATION_KEY = 'ori.personalAssistant.operation';
export const TAB_CHANNEL = 'ori.personalAssistant.tabs';

// How long after the user asked a page load may continue by itself.
export const AUTO_WINDOW_MS = 60 * 1000;
// How long a loading tab waits to hear that its identity is already in use.
export const TAB_GUARD_MS = 200;

const ENDPOINT = '/api/home-assistant/operations/';
const TAB_ID = /^[A-Za-z0-9_-]{1,64}$/;
const ENDED = new Set(['cancelled', 'expired', 'invalidated']);

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
    this._listeners = new Set();
    this._running = false;
    this._guard = Promise.resolve();

    this.snapshot = null;
    this.phase = PHASE.IDLE;
    this.pauseReason = '';
    this.highlight = null;
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
    this._remember(snapshot.id);
    this._accept(snapshot);
    await this.run({ allowNavigate: true });
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
    if (!this.snapshot.watching && !(await this.setWatching(true, { adopt: true }))) return;
    this._remember(this.snapshot.id);
    await this.run({ allowNavigate: true });
  }

  /** Turn watching on or off for this operation in this tab. Off cancels nothing. */
  async setWatching(watch, { adopt = false } = {}) {
    if (!this.snapshot) return false;
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

  /** Dismiss the operation. A task it created or started is not touched. */
  async dismiss() {
    const snapshot = this.snapshot;
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
  async run({ allowNavigate = false } = {}) {
    if (this._running) return;
    this._running = true;
    try {
      let conflicts = 0;
      for (;;) {
        const snapshot = this.snapshot;
        if (!snapshot || ENDED.has(snapshot.status) || !snapshot.watching) return;
        const command = pendingCommands(snapshot)[0];
        if (!command) return;
        this._set(PHASE.RUNNING);

        const result = isSupportedAction(command.action)
          ? await this._targets.execute(command, { allowNavigate })
          : { outcome: OUTCOME.UNAVAILABLE, reason: 'This step is not supported here.' };

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

  async _refresh() {
    const pointer = this._pointer();
    const id = (this.snapshot && this.snapshot.id) || (pointer && pointer.id);
    if (!id) return false;
    try {
      const reply = await this._request(
        'GET',
        ENDPOINT + encodeURIComponent(id) + '?tab=' + encodeURIComponent(this.tabId())
      );
      this._accept(reply.operation);
      return true;
    } catch (error) {
      if (errorCode(error) === 'operation_not_found') {
        this._forget();
        this._clear();
        return false;
      }
      // Could not be read right now: keep following, move nothing.
      this._pause('unreachable');
      return false;
    }
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
    this.snapshot = snapshot;
    this.pauseReason = '';
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

function defaultId() {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return 'tab-' + Math.random().toString(36).slice(2) + Date.now().toString(36);
}
