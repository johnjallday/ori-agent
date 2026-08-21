// reaper-console.js — the one live REAPER surface for a workspace.
//
// The Map station and this overlay read the same current state. Persisted
// runtime readiness decides whether the station exists; this module never
// guesses from a workspace name, template, folder, tag, or agent roster.
(function () {
  'use strict';

  const POLL_INTERVAL_MS = 5000;
  // While the console is open the user is driving REAPER directly, so state
  // must keep up with their own edits rather than lag a five-second tick.
  const CONSOLE_POLL_INTERVAL_MS = 1000;
  const CONSOLE_HOST_ID = 'reaperConsole';
  const REQUIREMENT_KEY = 'reaper_live_control';
  const TOAST_DURATION_MS = 8000;
  const GLOBAL_UNDO_COMMAND_ID = '40029';

  let workspaceId = '';
  let mapVisible = false;
  let pollTimer = null;
  let pollTimerIntervalMs = 0;
  let requestInFlight = false;
  let lastState = null;
  let lastMeaningfulState = '';
  let consoleOpen = false;
  let consoleBodyNode = null;
  let consoleTrigger = null;
  let consoleOverlayId = '';
  let catalog = [];
  let catalogLoaded = false;
  let scripts = [];
  let scriptsLoaded = false;
  let proposals = [];
  let proposalsLoaded = false;
  let proposalRequestInFlight = false;
  let pendingProposal = null;
  let proposalNotice = null;
  let actionRequestInFlight = false;
  let pendingAction = null;
  let lastRun = null;
  let toasts = [];
  let toastSeq = 0;
  // Track-strip edit state. editingIndex is the strip currently showing an
  // inline input; pendingEdit is an optimistic value awaiting the server, and
  // stripNotice is the inline failure message on one strip.
  let editingIndex = 0;
  let pendingEdit = null;
  let stripNotice = null;
  let trackRequestInFlight = false;
  // openPalette is the index of the strip whose color popover is open, 0 for
  // none. One strip at a time, per the Map's own popover convention.
  let openPalette = 0;
  // dragState is null when no drag is active. sourceIndex/sourceName are
  // captured at pointerdown so the eventual move is guarded on the name Ori
  // read before the drag started; targetIndex tracks the current drop slot
  // for the indicator and is what gets sent if the pointer is released.
  let dragState = null;

  // A fixed REAPER-compatible swatch set plus "no color" (PRD open question 1:
  // fixed set over a full picker, to keep the popover small). Values already
  // carry REAPER's 0x1000000 "has a custom color" flag.
  const COLOR_PALETTE = [
    { name: 'Red', hex: 0xef765d },
    { name: 'Orange', hex: 0xe8b54b },
    { name: 'Green', hex: 0x5ed0a7 },
    { name: 'Blue', hex: 0x4f8ff7 },
    { name: 'Purple', hex: 0x9b7fe8 },
    { name: 'Pink', hex: 0xe87fc0 },
    { name: 'Gray', hex: 0x8a97a1 }
  ].map(entry => ({ name: entry.name, value: 0x1000000 | entry.hex }));

  function workspaceIdFromPage() {
    if (workspaceId) return workspaceId;
    if (typeof window === 'undefined') return '';
    if (window.currentWorkspaceId) return String(window.currentWorkspaceId);
    const path = (window.location && window.location.pathname) || '';
    const parts = path.split('/').filter(Boolean);
    return parts[0] === 'workspaces' && parts[1] ? decodeURIComponent(parts[1]) : '';
  }

  function apiPath() {
    const id = workspaceIdFromPage();
    return id ? '/api/workspaces/' + encodeURIComponent(id) + '/reaper/state' : '';
  }

  function actionsApiPath(actionId) {
    const id = workspaceIdFromPage();
    if (!id) return '';
    const base = '/api/workspaces/' + encodeURIComponent(id) + '/reaper/actions';
    return actionId ? base + '/' + encodeURIComponent(actionId) + '/run' : base;
  }

  function scriptsApiPath() {
    const id = workspaceIdFromPage();
    return id ? '/api/workspaces/' + encodeURIComponent(id) + '/reaper/scripts' : '';
  }

  function tracksApiPath(suffix) {
    const id = workspaceIdFromPage();
    if (!id) return '';
    return '/api/workspaces/' + encodeURIComponent(id) + '/reaper/tracks/' + suffix;
  }

  function proposalsApiPath(proposalId, operation) {
    const id = workspaceIdFromPage();
    if (!id) return '';
    let path = '/api/workspaces/' + encodeURIComponent(id) + '/reaper/script-proposals';
    if (proposalId) path += '/' + encodeURIComponent(proposalId);
    if (operation) path += '/' + operation;
    return path;
  }

  function documentVisible() {
    return typeof document === 'undefined' || document.visibilityState !== 'hidden';
  }

  function meaningfulState(state) {
    if (!state) return '';
    return JSON.stringify({
      connected: Boolean(state.connected),
      reason: String(state.reason || ''),
      project: String(state.project || ''),
      tempo: Number(state.tempo || 0),
      time_signature: String(state.time_signature || ''),
      play_state: String(state.play_state || ''),
      position: String(state.position || ''),
      track_editing_available: Boolean(state.track_editing_available),
      // Peaks are deliberately excluded: they move continuously and would
      // defeat change detection entirely.
      tracks: (Array.isArray(state.tracks) ? state.tracks : []).map(track => ({
        index: Number(track.index || 0),
        name: String(track.name || ''),
        muted: Boolean(track.muted),
        soloed: Boolean(track.soloed),
        armed: Boolean(track.armed),
        color: Number(track.color || 0)
      }))
    });
  }

  function publishIfChanged(state) {
    const next = meaningfulState(state);
    if (next === lastMeaningfulState) return false;
    lastMeaningfulState = next;
    publishStateEvent(state);
    return true;
  }

  function publishStateEvent(state) {
    if (typeof document === 'undefined' || typeof document.dispatchEvent !== 'function') return;
    const event =
      typeof CustomEvent === 'function'
        ? new CustomEvent('ori:reaper-state-changed', { detail: state })
        : { type: 'ori:reaper-state-changed', detail: state };
    document.dispatchEvent(event);
  }

  async function refresh() {
    const path = apiPath();
    if (!path || requestInFlight || typeof fetch !== 'function') return lastState;
    requestInFlight = true;
    try {
      const response = await fetch(path, { headers: { Accept: 'application/json' } });
      if (!response.ok) throw new Error('state request failed');
      const state = await response.json();
      if (!state || typeof state.connected !== 'boolean') throw new Error('invalid state');
      lastState = state;
      const changed = publishIfChanged(state);
      // At the one-second console cadence an unconditional re-render would
      // rebuild the panel every tick for no reason, so only redraw when the
      // state the console actually shows has moved.
      // A drag in progress suspends re-render entirely: the transport
      // position or any other change arriving mid-gesture must not rebuild
      // the list under the user's pointer (PRD 4.1 item 5 / group 4.6). The
      // state itself is still recorded above, so the next explicit render —
      // on drop or cancel — starts from current truth.
      if (consoleOpen && changed && !dragState) renderConsole();
      return state;
    } catch (_error) {
      const failed = {
        applies: lastState ? lastState.applies !== false : false,
        connected: false,
        reason: 'check_failed',
        project: '',
        tempo: 0,
        time_signature: '',
        play_state: 'unknown',
        position: '',
        track_count: 0,
        tracks: []
      };
      lastState = failed;
      const changed = publishIfChanged(failed);
      // A drag in progress suspends re-render entirely: the transport
      // position or any other change arriving mid-gesture must not rebuild
      // the list under the user's pointer (PRD 4.1 item 5 / group 4.6). The
      // state itself is still recorded above, so the next explicit render —
      // on drop or cancel — starts from current truth.
      if (consoleOpen && changed && !dragState) renderConsole();
      return failed;
    } finally {
      requestInFlight = false;
    }
  }

  async function loadActions() {
    const path = actionsApiPath();
    if (!path || typeof fetch !== 'function') return catalog;
    try {
      const response = await fetch(path, { headers: { Accept: 'application/json' } });
      if (!response.ok) throw new Error('catalog request failed');
      const actions = await response.json();
      if (!Array.isArray(actions)) throw new Error('invalid catalog');
      catalog = actions.filter(action => action && action.id && action.label);
      catalogLoaded = true;
      if (consoleOpen) renderConsole();
      return catalog;
    } catch (_error) {
      catalogLoaded = true;
      if (consoleOpen) renderConsole();
      return catalog;
    }
  }

  async function loadScripts() {
    const path = scriptsApiPath();
    if (!path || typeof fetch !== 'function') return scripts;
    try {
      const response = await fetch(path, { headers: { Accept: 'application/json' } });
      if (!response.ok) throw new Error('script library request failed');
      const payload = await response.json();
      if (!Array.isArray(payload)) throw new Error('invalid script library');
      scripts = payload.filter(script => script && script.id && script.name);
      scriptsLoaded = true;
      if (consoleOpen) renderConsole();
      return scripts;
    } catch (_error) {
      scriptsLoaded = true;
      if (consoleOpen) renderConsole();
      return scripts;
    }
  }

  async function loadProposals() {
    const path = proposalsApiPath();
    if (!path || typeof fetch !== 'function') return proposals;
    try {
      const response = await fetch(path, { headers: { Accept: 'application/json' } });
      if (!response.ok) throw new Error('proposal request failed');
      const payload = await response.json();
      if (!Array.isArray(payload)) throw new Error('invalid proposals');
      proposals = payload.filter(proposal => proposal && proposal.id && proposal.code);
      proposalsLoaded = true;
      if (consoleOpen) renderConsole();
      return proposals;
    } catch (_error) {
      proposalsLoaded = true;
      if (consoleOpen) renderConsole();
      return proposals;
    }
  }

  function updateProposalFromRun(proposalId, payload) {
    proposals = proposals.map(proposal =>
      proposal.id === proposalId
        ? {
            ...proposal,
            tested_successfully: Boolean(payload.tested_successfully),
            last_run: { outcome: payload.outcome, error_text: payload.error_text || '' }
          }
        : proposal
    );
  }

  async function runProposal(proposal, confirmed) {
    if (!proposal || proposalRequestInFlight || typeof fetch !== 'function') return false;
    proposalRequestInFlight = true;
    pendingProposal = null;
    proposalNotice = { outcome: 'running', text: 'Running draft ' + proposal.name + '…' };
    if (consoleOpen) renderConsole();
    try {
      const response = await fetch(proposalsApiPath(proposal.id, 'run'), {
        method: 'POST',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
        body: JSON.stringify({ confirmed: Boolean(confirmed) })
      });
      const payload = await response.json().catch(() => ({}));
      updateProposalFromRun(proposal.id, payload);
      if (!response.ok || payload.outcome !== 'ok') {
        proposalNotice = {
          outcome: 'error',
          text: proposal.name + ' failed: ' + (payload.error_text || 'The draft did not run.')
        };
        return false;
      }
      proposalNotice = { outcome: 'ok', text: proposal.name + ' ran successfully as a draft.' };
      return true;
    } catch (_error) {
      proposalNotice = { outcome: 'error', text: proposal.name + ' failed: request unavailable.' };
      return false;
    } finally {
      proposalRequestInFlight = false;
      if (consoleOpen) renderConsole();
    }
  }

  async function saveProposal(proposal) {
    if (!proposal || proposalRequestInFlight || typeof fetch !== 'function') return false;
    proposalRequestInFlight = true;
    pendingProposal = null;
    try {
      const response = await fetch(proposalsApiPath(proposal.id, 'save'), {
        method: 'POST',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
        body: JSON.stringify({ confirmed: true })
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok || payload.outcome !== 'saved') {
        proposalNotice = {
          outcome: 'error',
          text: 'Save failed: ' + (payload.error || payload.message || 'Library unavailable.')
        };
        return false;
      }
      proposals = proposals.filter(candidate => candidate.id !== proposal.id);
      proposalNotice = {
        outcome: 'ok',
        text: proposal.name + ' is now available in every REAPER workspace.'
      };
      catalogLoaded = false;
      scriptsLoaded = false;
      void loadActions();
      void loadScripts();
      return true;
    } catch (_error) {
      proposalNotice = { outcome: 'error', text: 'Save failed: library request unavailable.' };
      return false;
    } finally {
      proposalRequestInFlight = false;
      if (consoleOpen) renderConsole();
    }
  }

  async function discardProposal(proposal) {
    if (!proposal || proposalRequestInFlight || typeof fetch !== 'function') return false;
    proposalRequestInFlight = true;
    pendingProposal = null;
    try {
      const response = await fetch(proposalsApiPath(proposal.id), { method: 'DELETE' });
      if (!response.ok) throw new Error('discard failed');
      proposals = proposals.filter(candidate => candidate.id !== proposal.id);
      proposalNotice = {
        outcome: 'ok',
        text: proposal.name + ' was discarded without being saved.'
      };
      return true;
    } catch (_error) {
      proposalNotice = { outcome: 'error', text: 'Discard failed: proposal is still available.' };
      return false;
    } finally {
      proposalRequestInFlight = false;
      if (consoleOpen) renderConsole();
    }
  }

  function requestProposalRun(proposal) {
    if (proposal.needs_confirmation) {
      pendingProposal = { kind: 'run', proposal };
      proposalNotice = null;
      renderConsole();
      return;
    }
    void runProposal(proposal, false);
  }

  function requestProposalSave(proposal) {
    pendingProposal = { kind: 'save', proposal };
    proposalNotice = null;
    renderConsole();
  }

  function catalogAction(actionId) {
    return catalog.find(action => String(action.id) === String(actionId)) || null;
  }

  function actionError(payload, response) {
    if (payload && payload.error_reason) return String(payload.error_reason);
    if (payload && payload.error) return String(payload.error);
    return response && response.status === 409
      ? 'REAPER is not connected. Nothing was run.'
      : 'REAPER did not run the action.';
  }

  async function executeAction(action, confirmed) {
    const path = actionsApiPath(action && action.id);
    if (!path || actionRequestInFlight || typeof fetch !== 'function') return false;
    actionRequestInFlight = true;
    pendingAction = null;
    lastRun = { outcome: 'running', label: action.label };
    if (consoleOpen) renderConsole();
    try {
      const response = await fetch(path, {
        method: 'POST',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
        body: JSON.stringify({ confirmed: Boolean(confirmed) })
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok || payload.outcome !== 'ok') {
        lastRun = { outcome: 'error', label: action.label, reason: actionError(payload, response) };
        if (payload && typeof payload.connected === 'boolean') {
          lastState = payload;
          publishIfChanged(payload);
        }
        return false;
      }
      lastRun = { outcome: 'ok', label: action.label };
      lastState = payload;
      publishIfChanged(payload);
      if (payload.undo && payload.undo.summary) {
        addToast(payload.undo.summary, payload.undo);
      }
      return true;
    } catch (_error) {
      lastRun = {
        outcome: 'error',
        label: action.label,
        reason: 'The action request failed. Nothing else will be attempted.'
      };
      return false;
    } finally {
      actionRequestInFlight = false;
      if (consoleOpen) renderConsole();
    }
  }

  function requestAction(action) {
    if (!action || actionRequestInFlight) return false;
    if (action.needs_confirmation) {
      pendingAction = action;
      lastRun = null;
      if (consoleOpen) renderConsole();
      return true;
    }
    void executeAction(action, false);
    return true;
  }

  // Toasts are the undo-forward teaching surface: a Tier 1 catalog action
  // reports what it did in the response's `undo` field, and the console
  // turns that into a stackable, dismissible notice. Undo and Redo never
  // carry an `undo` field, so they never produce one of their own.
  function scheduleToastDismiss(toast) {
    if (typeof setTimeout !== 'function') return;
    if (toast.remainingMs == null) toast.remainingMs = TOAST_DURATION_MS;
    toast.startedAt = Date.now();
    toast.timer = setTimeout(() => dismissToast(toast.id), toast.remainingMs);
  }

  function pauseToast(toast) {
    if (!toast || !toast.timer) return;
    if (typeof clearTimeout === 'function') clearTimeout(toast.timer);
    toast.timer = null;
    const elapsed = Date.now() - toast.startedAt;
    toast.remainingMs = Math.max(0, toast.remainingMs - elapsed);
  }

  function resumeToast(toast) {
    if (!toast || toast.timer) return;
    scheduleToastDismiss(toast);
  }

  function dismissToast(id) {
    const toast = toasts.find(candidate => candidate.id === id);
    if (toast && toast.timer && typeof clearTimeout === 'function') clearTimeout(toast.timer);
    toasts = toasts.filter(candidate => candidate.id !== id);
    if (consoleOpen) renderConsole();
  }

  function addToast(message, undo) {
    const text = String(message || '').trim();
    if (!text) return null;
    const toast = { id: 'toast-' + ++toastSeq, message: text, undo: undo || null, timer: null };
    toasts = toasts.concat([toast]);
    scheduleToastDismiss(toast);
    if (consoleOpen) renderConsole();
    return toast;
  }

  async function undoFromToast(toastId) {
    const toast = toasts.find(candidate => candidate.id === toastId);
    dismissToast(toastId);
    // Hybrid undo (PRD 4.3 item 16): a single-track edit reverses through its
    // own specific inverse, while catalog actions and saved scripts reverse
    // through REAPER's global undo because they already run in one undo block.
    if (toast && toast.undo && toast.undo.kind === 'track_edit') {
      await undoTrackEdit();
      return;
    }
    const commandId = (toast && toast.undo && toast.undo.command_id) || GLOBAL_UNDO_COMMAND_ID;
    const path = actionsApiPath(commandId);
    if (!path || typeof fetch !== 'function') return;
    try {
      const response = await fetch(path, {
        method: 'POST',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
        body: JSON.stringify({ confirmed: true })
      });
      const payload = await response.json().catch(() => ({}));
      if (response.ok && typeof payload.connected === 'boolean') {
        lastState = payload;
        publishIfChanged(payload);
      }
    } catch (_error) {
      // Fall through to the forced refresh below, which re-reads live truth.
    }
    void refresh();
  }

  // --- Track strips -------------------------------------------------------
  //
  // Every edit is applied optimistically and guarded server-side on the name
  // Ori last read. A refused guard is not an error the user caused: the track
  // list moved underneath them, so the strip says so and re-reads live state.

  function trackEditError(payload, response) {
    if (payload && payload.error_reason) return String(payload.error_reason);
    if (payload && payload.error) return String(payload.error);
    return response && response.status === 409
      ? 'The track list changed — refreshed.'
      : 'REAPER did not apply the change.';
  }

  async function postTrackEdit(path, body) {
    const response = await fetch(path, {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    const payload = await response.json().catch(() => ({}));
    return { response, payload };
  }

  function adoptTrackState(payload) {
    if (payload && typeof payload.connected === 'boolean') {
      lastState = payload;
      publishIfChanged(payload);
    }
  }

  // runTrackEdit is the one path every strip mutation goes through: optimistic
  // patch, server call, revert-with-notice on failure, toast on success, and a
  // forced immediate state re-read either way.
  async function runTrackEdit(index, path, body, patch) {
    if (!path || trackRequestInFlight || typeof fetch !== 'function') return false;
    trackRequestInFlight = true;
    stripNotice = null;
    pendingEdit = { index, patch };
    if (consoleOpen) renderConsole();
    try {
      const { response, payload } = await postTrackEdit(path, body);
      if (!response.ok || payload.outcome !== 'ok') {
        pendingEdit = null;
        stripNotice = { index, text: trackEditError(payload, response) };
        adoptTrackState(payload);
        return false;
      }
      pendingEdit = null;
      adoptTrackState(payload);
      if (payload.undo && payload.undo.summary) {
        addToast(payload.undo.summary, { kind: 'track_edit' });
      }
      return true;
    } catch (_error) {
      pendingEdit = null;
      stripNotice = { index, text: 'The edit request failed. Nothing was applied.' };
      return false;
    } finally {
      trackRequestInFlight = false;
      if (consoleOpen) renderConsole();
      void refresh();
    }
  }

  async function renameTrack(index, expectedName, newName) {
    const name = String(newName || '').trim();
    if (!name) return false;
    editingIndex = 0;
    return runTrackEdit(
      index,
      tracksApiPath(encodeURIComponent(index) + '/rename'),
      { name, expected_name: String(expectedName == null ? '' : expectedName) },
      { name }
    );
  }

  async function setTrackColor(index, expectedName, color) {
    openPalette = 0;
    return runTrackEdit(
      index,
      tracksApiPath(encodeURIComponent(index) + '/color'),
      { color, expected_name: String(expectedName == null ? '' : expectedName) },
      { color }
    );
  }

  const TOGGLE_PATCH_KEY = { mute: 'muted', solo: 'soloed', arm: 'armed' };

  async function setTrackToggle(kind, index, expectedName, value) {
    const patch = {};
    patch[TOGGLE_PATCH_KEY[kind]] = value;
    return runTrackEdit(
      index,
      tracksApiPath(encodeURIComponent(index) + '/' + kind),
      { value, expected_name: String(expectedName == null ? '' : expectedName) },
      patch
    );
  }

  async function moveTrack(index, expectedName, newIndex) {
    return runTrackEdit(
      index,
      tracksApiPath(encodeURIComponent(index) + '/move'),
      { new_index: newIndex, expected_name: String(expectedName == null ? '' : expectedName) },
      {}
    );
  }

  async function undoTrackEdit() {
    const path = tracksApiPath('undo');
    if (!path || typeof fetch !== 'function') return false;
    try {
      const { response, payload } = await postTrackEdit(path, {});
      adoptTrackState(payload);
      if (!response.ok || payload.outcome !== 'ok') {
        stripNotice = { index: 0, text: trackEditError(payload, response) };
        return false;
      }
      return true;
    } catch (_error) {
      stripNotice = { index: 0, text: 'The undo request failed. Nothing was undone.' };
      return false;
    } finally {
      if (consoleOpen) renderConsole();
      void refresh();
    }
  }

  function beginTrackRename(index) {
    editingIndex = Number(index) || 0;
    stripNotice = null;
    if (consoleOpen) renderConsole();
  }

  function cancelTrackRename() {
    editingIndex = 0;
    if (consoleOpen) renderConsole();
  }

  // --- Drag-to-reorder ------------------------------------------------------
  //
  // Pointer coordinates are resolved through document.elementFromPoint, so the
  // drag itself carries only logical track indices — the same functions a
  // test can drive directly without simulating real pixel geometry. The
  // 1-second poll is suspended for the whole gesture (PRD 4.1 item 5 / 4.6):
  // a state change arriving mid-drag must not rebuild the list underneath the
  // user's pointer.

  function beginDrag(index, sourceName) {
    if (!index) return;
    dragState = { sourceIndex: index, sourceName: sourceName || '', targetIndex: index };
    attachDragListeners();
    if (consoleOpen) renderConsole();
  }

  function dragOverIndex(index) {
    if (!dragState || !index || index === dragState.targetIndex) return;
    dragState.targetIndex = index;
    if (consoleOpen) renderConsole();
  }

  async function endDrag() {
    if (!dragState) return;
    const { sourceIndex, sourceName, targetIndex } = dragState;
    detachDragListeners();
    dragState = null;
    if (targetIndex === sourceIndex) {
      // Dropped back where it started: nothing to send, just resume normally.
      if (consoleOpen) renderConsole();
      return;
    }
    await moveTrack(sourceIndex, sourceName, targetIndex);
  }

  function cancelDrag() {
    if (!dragState) return;
    detachDragListeners();
    dragState = null;
    if (consoleOpen) renderConsole();
  }

  function handleDragPointerMove(event) {
    if (
      !dragState ||
      typeof document === 'undefined' ||
      typeof document.elementFromPoint !== 'function'
    ) {
      return;
    }
    const hit = document.elementFromPoint(event.clientX, event.clientY);
    const strip =
      hit && typeof hit.closest === 'function' ? hit.closest('[data-track-index]') : null;
    const index = strip && Number(strip.getAttribute('data-track-index'));
    if (index) dragOverIndex(index);
  }

  function handleDragPointerUp() {
    void endDrag();
  }

  function handleDragKeydown(event) {
    if (event.key === 'Escape') cancelDrag();
  }

  function attachDragListeners() {
    if (typeof document === 'undefined' || typeof document.addEventListener !== 'function') return;
    document.addEventListener('pointermove', handleDragPointerMove);
    document.addEventListener('pointerup', handleDragPointerUp);
    document.addEventListener('pointercancel', cancelDrag);
    document.addEventListener('keydown', handleDragKeydown);
  }

  function detachDragListeners() {
    if (typeof document === 'undefined' || typeof document.removeEventListener !== 'function')
      return;
    document.removeEventListener('pointermove', handleDragPointerMove);
    document.removeEventListener('pointerup', handleDragPointerUp);
    document.removeEventListener('pointercancel', cancelDrag);
    document.removeEventListener('keydown', handleDragKeydown);
  }

  function stopPolling() {
    if (pollTimer !== null && typeof clearInterval === 'function') clearInterval(pollTimer);
    pollTimer = null;
    pollTimerIntervalMs = 0;
  }

  function pollIntervalMs() {
    return consoleOpen ? CONSOLE_POLL_INTERVAL_MS : POLL_INTERVAL_MS;
  }

  function syncPolling({ refreshNow = false } = {}) {
    const shouldPoll = (mapVisible || consoleOpen) && documentVisible();
    if (!shouldPoll) {
      stopPolling();
      return;
    }
    // Restart the timer when the cadence changes, so opening or closing the
    // console takes effect immediately rather than after the current tick.
    if (pollTimer !== null && pollTimerIntervalMs !== pollIntervalMs()) {
      stopPolling();
    }
    if (refreshNow || !lastState) void refresh();
    if (pollTimer === null && typeof setInterval === 'function') {
      pollTimerIntervalMs = pollIntervalMs();
      pollTimer = setInterval(() => {
        if ((mapVisible || consoleOpen) && documentVisible()) void refresh();
      }, pollTimerIntervalMs);
    }
  }

  function setMapVisible(visible) {
    const next = Boolean(visible);
    const changed = next !== mapVisible;
    mapVisible = next;
    syncPolling({ refreshNow: changed && next });
  }

  function reasonLabel(reason) {
    switch (String(reason || '')) {
      case 'web_remote_off':
        return 'Web Remote off';
      case 'reaper_unreachable':
        return 'REAPER not running';
      case 'unsupported':
        return 'Live control unavailable';
      case 'check_failed':
        return 'Connection check failed';
      default:
        return 'Web Remote unavailable';
    }
  }

  function tempoLabel(value) {
    const tempo = Number(value || 0);
    if (!Number.isFinite(tempo) || tempo <= 0) return '— BPM';
    return (Number.isInteger(tempo) ? String(tempo) : tempo.toFixed(2).replace(/0+$/, '')) + ' BPM';
  }

  function trackCountLabel(state) {
    const count = Number(state && state.track_count);
    const safe = Number.isFinite(count) && count >= 0 ? count : 0;
    return safe + (safe === 1 ? ' track' : ' tracks');
  }

  function stationLabel() {
    return lastState && lastState.connected && lastState.project
      ? 'REAPER · ' + String(lastState.project)
      : 'REAPER';
  }

  function stationState() {
    if (lastState && lastState.applies === false) return { applies: false };
    if (!lastState) {
      return {
        applies: true,
        value: 'Checking…',
        description: 'checking the current REAPER connection',
        tone: 'loading'
      };
    }
    if (!lastState.connected) {
      const label = reasonLabel(lastState.reason);
      return { applies: true, value: label, description: label, tone: 'degraded' };
    }
    const value = [
      tempoLabel(lastState.tempo),
      trackCountLabel(lastState),
      lastState.play_state || 'stopped'
    ].join(' · ');
    return {
      applies: true,
      value,
      description: (lastState.project ? lastState.project + ' — ' : '') + value,
      tone: lastState.play_state === 'recording' ? 'attention' : 'clear'
    };
  }

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  }

  function button(label, className, onClick) {
    const node = el('button', className, label);
    node.type = 'button';
    if (onClick) node.addEventListener('click', onClick);
    return node;
  }

  function clear(node) {
    if (node) node.textContent = '';
  }

  function overlayCoordinator() {
    return typeof window === 'undefined' ? null : window.workspaceOverlayCoordinator || null;
  }

  function consoleHost() {
    if (typeof document === 'undefined') return null;
    let host = document.getElementById(CONSOLE_HOST_ID);
    if (host) return host;
    if (!document.body) return null;
    host = el('div', 'reaper-console-host');
    host.id = CONSOLE_HOST_ID;
    host.hidden = true;
    document.body.appendChild(host);
    return host;
  }

  function stateToken(state) {
    const label = state && state.connected ? 'Connected now' : reasonLabel(state && state.reason);
    const token = el(
      'span',
      'reaper-console-token ' + (state && state.connected ? 'is-live' : 'is-offline')
    );
    token.appendChild(el('span', 'reaper-console-token-dot'));
    token.appendChild(el('span', '', label));
    return token;
  }

  function renderHeader(host, state) {
    const header = el('header', 'reaper-console-head');
    const identity = el('div', 'reaper-console-identity');
    const eyebrow = el('span', 'reaper-console-eyebrow', 'LIVE CONTROL SURFACE');
    const title = el('h2', 'reaper-console-title', 'REAPER');
    title.id = 'reaperConsoleTitle';
    identity.appendChild(eyebrow);
    identity.appendChild(title);
    identity.appendChild(stateToken(state));
    header.appendChild(identity);
    header.appendChild(button('×', 'reaper-console-close', () => close()));
    host.appendChild(header);
  }

  function transportAction(actionId, label, needsConfirmation) {
    return (
      catalogAction(actionId) || {
        id: actionId,
        label,
        description: label + ' the current REAPER transport.',
        source: 'builtin',
        mutates: Boolean(needsConfirmation),
        needs_confirmation: Boolean(needsConfirmation)
      }
    );
  }

  function renderToasts(panel) {
    if (!toasts.length) return;
    const stack = el('div', 'reaper-console-toast-stack');
    stack.setAttribute('aria-live', 'polite');
    toasts.forEach(toast => {
      const item = el('div', 'reaper-console-toast');
      item.addEventListener('mouseenter', () => pauseToast(toast));
      item.addEventListener('mouseleave', () => resumeToast(toast));
      item.appendChild(el('span', 'reaper-console-toast-message', toast.message));
      const actions = el('div', 'reaper-console-toast-actions');
      if (toast.undo) {
        actions.appendChild(
          button('Undo', 'reaper-console-toast-undo', () => void undoFromToast(toast.id))
        );
      }
      const dismiss = button('×', 'reaper-console-toast-dismiss', () => dismissToast(toast.id));
      dismiss.setAttribute('aria-label', 'Dismiss notification');
      actions.appendChild(dismiss);
      item.appendChild(actions);
      stack.appendChild(item);
    });
    panel.appendChild(stack);
  }

  function renderTransport(host, state) {
    const section = el('section', 'reaper-console-transport');
    const controls = el('div', 'reaper-console-transport-controls');
    [
      ['1007', 'Play', false, '▶'],
      ['1016', 'Stop', false, '■'],
      ['1013', 'Record', true, '●']
    ].forEach(([id, label, needsConfirmation, symbol]) => {
      const action = transportAction(id, label, needsConfirmation);
      const control = button('', 'reaper-console-transport-btn is-' + label.toLowerCase(), () =>
        requestAction(action)
      );
      control.disabled = actionRequestInFlight;
      control.setAttribute('aria-label', label + ' in REAPER');
      control.appendChild(el('span', 'reaper-console-transport-symbol', symbol));
      control.appendChild(el('span', '', label));
      if (
        (id === '1007' && state.play_state === 'playing') ||
        (id === '1013' && state.play_state === 'recording')
      ) {
        control.classList.add('is-active');
      }
      controls.appendChild(control);
    });
    section.appendChild(controls);
    const position = el('div', 'reaper-console-transport-position');
    position.appendChild(el('span', '', String(state.play_state || 'stopped').toUpperCase()));
    position.appendChild(el('strong', '', state.position || '—'));
    section.appendChild(position);
    host.appendChild(section);
  }

  function renderActionFeedback(host) {
    if (pendingAction) {
      const confirmation = el('section', 'reaper-console-confirm');
      const copy = el('div', '');
      copy.appendChild(el('strong', '', 'Confirm project change'));
      copy.appendChild(
        el('p', '', pendingAction.label + ' can change the open REAPER session. Run it now?')
      );
      confirmation.appendChild(copy);
      const actions = el('div', 'reaper-console-confirm-actions');
      actions.appendChild(
        button('Cancel', 'reaper-console-btn is-secondary', () => {
          pendingAction = null;
          renderConsole();
        })
      );
      actions.appendChild(
        button(
          'Run ' + pendingAction.label,
          'reaper-console-btn is-primary',
          () => void executeAction(pendingAction, true)
        )
      );
      confirmation.appendChild(actions);
      host.appendChild(confirmation);
    }
    if (!lastRun) return;
    const message = el(
      'div',
      'reaper-console-run-result is-' + lastRun.outcome,
      lastRun.outcome === 'running'
        ? 'Running ' + lastRun.label + '…'
        : lastRun.outcome === 'ok'
          ? lastRun.label + ' completed in REAPER.'
          : lastRun.label + ' failed: ' + lastRun.reason
    );
    message.setAttribute('role', 'status');
    host.appendChild(message);
  }

  function renderProposalConfirmation(host) {
    if (!pendingProposal) return;
    const proposal = pendingProposal.proposal;
    const panel = el('section', 'reaper-console-confirm reaper-console-proposal-confirm');
    const copy = el('div', '');
    if (pendingProposal.kind === 'save') {
      copy.appendChild(el('strong', '', 'Save to the global script library?'));
      copy.appendChild(
        el(
          'p',
          '',
          'Saving makes ' +
            proposal.name +
            ' available in every REAPER workspace on this Mac, not only this one.' +
            (proposal.tested_successfully
              ? ''
              : ' This draft is untested because it has never run successfully.')
        )
      );
    } else {
      copy.appendChild(el('strong', '', 'Run this draft in REAPER?'));
      copy.appendChild(
        el('p', '', proposal.name + ' can change the open session before it is saved.')
      );
    }
    panel.appendChild(copy);
    const actions = el('div', 'reaper-console-confirm-actions');
    actions.appendChild(
      button('Cancel', 'reaper-console-btn is-secondary', () => {
        pendingProposal = null;
        renderConsole();
      })
    );
    actions.appendChild(
      button(
        pendingProposal.kind === 'save' ? 'Save for every workspace' : 'Run draft',
        'reaper-console-btn is-primary',
        () =>
          pendingProposal.kind === 'save'
            ? void saveProposal(proposal)
            : void runProposal(proposal, true)
      )
    );
    panel.appendChild(actions);
    host.appendChild(panel);
  }

  function renderProposals(host) {
    if (!proposalsLoaded && !proposals.length) return;
    if (proposalsLoaded && !proposals.length && !proposalNotice) return;
    const section = el('section', 'reaper-console-proposals');
    const head = el('div', 'reaper-console-section-head');
    head.appendChild(el('h3', '', 'Script drafts'));
    head.appendChild(el('span', '', proposals.length + ' awaiting review'));
    section.appendChild(head);
    renderProposalConfirmation(section);
    if (proposalNotice) {
      const notice = el(
        'div',
        'reaper-console-run-result is-' + proposalNotice.outcome,
        proposalNotice.text
      );
      notice.setAttribute('role', 'status');
      section.appendChild(notice);
    }
    proposals.forEach(proposal => {
      const card = el('article', 'reaper-console-proposal');
      const heading = el('div', 'reaper-console-proposal-head');
      const identity = el('div', 'reaper-console-proposal-identity');
      identity.appendChild(el('strong', '', proposal.name));
      identity.appendChild(
        el(
          'span',
          '',
          proposal.filename +
            ' · ' +
            (proposal.needs_confirmation ? 'confirmation required' : 'one-click run')
        )
      );
      heading.appendChild(identity);
      heading.appendChild(
        el(
          'span',
          'reaper-console-proposal-test ' +
            (proposal.tested_successfully ? 'is-tested' : 'is-untested'),
          proposal.tested_successfully ? 'Tested successfully' : 'Untested'
        )
      );
      card.appendChild(heading);
      card.appendChild(el('p', 'reaper-console-proposal-description', proposal.description));
      const code = el('pre', 'reaper-console-proposal-code');
      code.appendChild(el('code', '', proposal.code));
      card.appendChild(code);
      if (proposal.last_run) {
        card.appendChild(
          el(
            'div',
            'reaper-console-proposal-result is-' + proposal.last_run.outcome,
            proposal.last_run.outcome === 'ok'
              ? 'Last draft run succeeded.'
              : 'Last draft run failed: ' +
                  (proposal.last_run.error_text || 'Unknown runner error.')
          )
        );
      }
      const actions = el('div', 'reaper-console-proposal-actions');
      const run = button('Run draft', 'reaper-console-btn is-secondary', () =>
        requestProposalRun(proposal)
      );
      run.disabled = proposalRequestInFlight;
      actions.appendChild(run);
      const save = button('Save', 'reaper-console-btn is-primary', () =>
        requestProposalSave(proposal)
      );
      save.disabled = proposalRequestInFlight;
      actions.appendChild(save);
      const discard = button(
        'Discard',
        'reaper-console-btn is-secondary',
        () => void discardProposal(proposal)
      );
      discard.disabled = proposalRequestInFlight;
      actions.appendChild(discard);
      card.appendChild(actions);
      section.appendChild(card);
    });
    host.appendChild(section);
  }

  function renderScriptLibrary(host) {
    const section = el('section', 'reaper-console-script-library');
    const head = el('div', 'reaper-console-section-head');
    head.appendChild(el('h3', '', 'Script library'));
    head.appendChild(el('span', '', scriptsLoaded ? scripts.length + ' shared' : 'Loading…'));
    section.appendChild(head);
    const list = el('div', 'reaper-console-script-list');
    if (!scriptsLoaded) {
      list.appendChild(el('p', 'reaper-console-empty', 'Loading shared ReaScripts…'));
    } else if (!scripts.length) {
      list.appendChild(
        el(
          'p',
          'reaper-console-empty',
          'Drop a .lua file into ~/Ori Scripts/reaper/ to share it across REAPER workspaces.'
        )
      );
    } else {
      scripts.forEach(script => {
        const row = el('article', 'reaper-console-script-row');
        const copy = el('div', 'reaper-console-script-copy');
        copy.appendChild(el('strong', '', script.name));
        copy.appendChild(
          el(
            'span',
            '',
            script.metadata_valid
              ? script.description || script.filename
              : 'Metadata missing or malformed · confirmation required'
          )
        );
        row.appendChild(copy);
        const action = catalogAction(script.id) || {
          id: script.id,
          label: script.name,
          description: script.description,
          source: 'custom',
          mutates: true,
          needs_confirmation: script.needs_confirmation
        };
        const run = button(
          script.needs_confirmation ? 'Review run' : 'Run',
          'reaper-console-btn is-secondary',
          () => requestAction(action)
        );
        run.disabled = actionRequestInFlight;
        run.setAttribute('aria-label', 'Run shared script ' + script.name + ' in REAPER');
        row.appendChild(run);
        list.appendChild(row);
      });
    }
    section.appendChild(list);
    host.appendChild(section);
  }

  function renderRawCommand(host) {
    const raw = el('div', 'reaper-console-raw');
    const copy = el('div', 'reaper-console-raw-copy');
    copy.appendChild(el('strong', '', 'Raw command ID'));
    copy.appendChild(el('span', '', 'Decimal or _RS hexadecimal IDs always require confirmation.'));
    raw.appendChild(copy);
    const controls = el('div', 'reaper-console-raw-controls');
    const input = el('input', 'reaper-console-raw-input');
    input.type = 'text';
    input.placeholder = '40001 or _RS…';
    input.maxLength = 96;
    input.autocomplete = 'off';
    input.setAttribute('aria-label', 'Raw REAPER command ID');
    controls.appendChild(input);
    const run = button('Review', 'reaper-console-btn is-secondary', () => {
      const id = String(input.value || '').trim();
      if (!id) {
        lastRun = { outcome: 'error', label: 'Raw command', reason: 'Enter a command ID first.' };
        renderConsole();
        return;
      }
      requestAction({
        id,
        label: 'Raw command ' + id,
        description: 'User-entered REAPER command ID.',
        source: 'raw',
        mutates: true,
        needs_confirmation: true
      });
    });
    run.disabled = actionRequestInFlight;
    controls.appendChild(run);
    raw.appendChild(controls);
    host.appendChild(raw);
  }

  function renderActionGrid(host) {
    const section = el('section', 'reaper-console-action-catalog');
    const head = el('div', 'reaper-console-section-head');
    head.appendChild(el('h3', '', 'Actions'));
    head.appendChild(el('span', '', catalogLoaded ? catalog.length + ' available' : 'Loading…'));
    section.appendChild(head);
    const grid = el('div', 'reaper-console-action-grid');
    const remaining = catalog.filter(
      action => action.source !== 'custom' && !['1007', '1016', '1013'].includes(String(action.id))
    );
    if (!catalogLoaded) {
      grid.appendChild(el('p', 'reaper-console-empty', 'Loading REAPER actions…'));
    } else if (!remaining.length) {
      grid.appendChild(el('p', 'reaper-console-empty', 'No additional actions available'));
    } else {
      remaining.forEach(action => {
        const control = button('', 'reaper-console-action-card', () => requestAction(action));
        control.disabled = actionRequestInFlight;
        control.setAttribute('aria-label', action.label + ' in REAPER');
        const title = el('span', 'reaper-console-action-title');
        title.appendChild(el('strong', '', action.label));
        title.appendChild(
          el(
            'span',
            'reaper-console-action-risk',
            action.needs_confirmation ? 'Confirm' : 'One click'
          )
        );
        control.appendChild(title);
        control.appendChild(el('span', 'reaper-console-action-description', action.description));
        grid.appendChild(control);
      });
    }
    section.appendChild(grid);
    host.appendChild(section);
    renderProposals(host);
    renderScriptLibrary(host);
    renderRawCommand(host);
  }

  function renderOffline(host, state) {
    const panel = el('section', 'reaper-console-offline');
    panel.appendChild(el('span', 'reaper-console-offline-mark', '!'));
    const copy = el('div', 'reaper-console-offline-copy');
    copy.appendChild(el('h3', '', reasonLabel(state && state.reason)));
    copy.appendChild(
      el(
        'p',
        '',
        'Ori cannot read this session right now. Your recorded setup is history, not proof of a current connection.'
      )
    );
    const actions = el('div', 'reaper-console-actions');
    actions.appendChild(
      button('Check again', 'reaper-console-btn is-secondary', () => void refresh())
    );
    actions.appendChild(button('Fix setup', 'reaper-console-btn is-primary', openSetupFix));
    copy.appendChild(actions);
    panel.appendChild(copy);
    host.appendChild(panel);
  }

  // trackDisplayValue reads an optimistic patch over the live value, so a
  // pending edit shows immediately, before the server confirms it.
  function trackDisplayValue(track, key, liveValue) {
    if (pendingEdit && pendingEdit.index === track.index && key in pendingEdit.patch) {
      return pendingEdit.patch[key];
    }
    return liveValue;
  }

  function trackDisplayName(track) {
    return trackDisplayValue(track, 'name', track.name || '');
  }

  function trackDisplayColor(track) {
    return Number(trackDisplayValue(track, 'color', track.color || 0));
  }

  function cssColor(value) {
    return '#' + (Number(value) & 0xffffff).toString(16).padStart(6, '0');
  }

  function renderTrackNameEditor(item, track) {
    const input = el('input', 'reaper-console-track-name-input');
    input.type = 'text';
    input.value = trackDisplayName(track);
    input.maxLength = 128;
    input.autocomplete = 'off';
    input.setAttribute('aria-label', 'Rename track ' + track.index);
    let settled = false;
    const commit = () => {
      if (settled) return;
      const next = String(input.value || '').trim();
      // An empty name is rejected client-side, with no server call at all.
      if (!next) {
        settled = true;
        editingIndex = 0;
        stripNotice = { index: track.index, text: 'A track name cannot be empty.' };
        renderConsole();
        return;
      }
      if (next === (track.name || '')) {
        settled = true;
        cancelTrackRename();
        return;
      }
      settled = true;
      void renameTrack(track.index, track.name || '', next);
    };
    input.addEventListener('keydown', event => {
      if (event.key === 'Enter') {
        commit();
      } else if (event.key === 'Escape') {
        settled = true;
        cancelTrackRename();
      }
    });
    input.addEventListener('blur', commit);
    item.appendChild(input);
    if (typeof input.focus === 'function') input.focus();
  }

  function renderColorSwatch(item, track, editable) {
    const color = trackDisplayColor(track);
    const hasColor = (color & 0x1000000) !== 0;
    const swatch = button(
      '',
      'reaper-console-track-swatch' + (hasColor ? ' has-color' : ' is-empty'),
      () => {
        if (!editable || trackRequestInFlight) return;
        openPalette = openPalette === track.index ? 0 : track.index;
        renderConsole();
      }
    );
    if (hasColor) swatch.setAttribute('style', 'background:' + cssColor(color) + ';');
    swatch.disabled = !editable;
    swatch.setAttribute('aria-haspopup', 'true');
    swatch.setAttribute('aria-expanded', String(openPalette === track.index));
    swatch.setAttribute(
      'aria-label',
      'Color for track ' + track.index + (hasColor ? '' : ', no color') + ', open palette'
    );
    item.appendChild(swatch);

    if (editable && openPalette === track.index) {
      // A full-viewport backdrop closes the popover on any outside click,
      // mirroring the console's own backdrop-to-close pattern.
      const backdrop = el('div', 'reaper-console-color-backdrop');
      backdrop.addEventListener('click', () => {
        openPalette = 0;
        renderConsole();
      });
      item.appendChild(backdrop);

      const popover = el('div', 'reaper-console-color-popover');
      popover.setAttribute('role', 'menu');
      popover.addEventListener('keydown', event => {
        if (event.key !== 'Escape') return;
        openPalette = 0;
        renderConsole();
      });
      const none = button('No color', 'reaper-console-color-option is-none', () => {
        void setTrackColor(track.index, track.name || '', 0);
      });
      popover.appendChild(none);
      COLOR_PALETTE.forEach(entry => {
        const option = button('', 'reaper-console-color-option', () => {
          void setTrackColor(track.index, track.name || '', entry.value);
        });
        option.setAttribute('style', 'background:' + cssColor(entry.value) + ';');
        option.setAttribute('aria-label', entry.name);
        if (entry.value === color) option.classList.add('is-selected');
        popover.appendChild(option);
      });
      item.appendChild(popover);
    }
  }

  function renderToggleChip(item, track, kind, letter, label, active, editable) {
    const chip = button(
      letter,
      'reaper-console-track-chip is-' + kind + (active ? ' is-active' : ''),
      () => {
        if (!editable || trackRequestInFlight) return;
        void setTrackToggle(kind, track.index, track.name || '', !active);
      }
    );
    chip.disabled = !editable || trackRequestInFlight;
    chip.setAttribute('aria-pressed', String(active));
    chip.setAttribute('aria-label', label + ' track ' + track.index + (active ? ', on' : ', off'));
    chip.title = label;
    item.appendChild(chip);
  }

  function renderDragGrip(item, track, editable, trackCount) {
    const group = el('span', 'reaper-console-track-grip-group');
    const grip = button('⠿', 'reaper-console-track-grip', null);
    grip.disabled = !editable;
    grip.setAttribute('aria-label', 'Drag to reorder track ' + track.index);
    grip.addEventListener('pointerdown', event => {
      if (!editable) return;
      event.preventDefault();
      beginDrag(track.index, track.name || '');
    });
    group.appendChild(grip);

    // Keyboard-accessible equivalent to dragging (PRD 4.1 item 5): move up
    // and move down, each a normal guarded edit, not a drag gesture.
    const up = button('▲', 'reaper-console-track-move is-up', () => {
      if (!editable || track.index <= 1) return;
      void moveTrack(track.index, track.name || '', track.index - 1);
    });
    up.disabled = !editable || track.index <= 1;
    up.setAttribute('aria-label', 'Move track ' + track.index + ' up');
    group.appendChild(up);

    const down = button('▼', 'reaper-console-track-move is-down', () => {
      if (!editable || track.index >= trackCount) return;
      void moveTrack(track.index, track.name || '', track.index + 1);
    });
    down.disabled = !editable || track.index >= trackCount;
    down.setAttribute('aria-label', 'Move track ' + track.index + ' down');
    group.appendChild(down);

    item.appendChild(group);
  }

  function renderTrackStrip(list, track, editable, trackCount) {
    const item = el('li', 'reaper-console-track');
    const pending = Boolean(pendingEdit && pendingEdit.index === track.index);
    if (pending) item.classList.add('is-pending');
    if (dragState && dragState.sourceIndex === track.index) item.classList.add('is-dragging');
    // Read by the pointer-drag hit test (document.elementFromPoint + closest)
    // to resolve a screen position back to a logical track index.
    item.setAttribute('data-track-index', String(track.index));

    renderDragGrip(item, track, editable, trackCount);
    item.appendChild(
      el('span', 'reaper-console-track-index', String(track.index).padStart(2, '0'))
    );

    renderColorSwatch(item, track, editable);

    const name = trackDisplayName(track);
    if (editable && editingIndex === track.index) {
      renderTrackNameEditor(item, track);
    } else if (editable) {
      const trigger = button(
        name || 'Untitled track',
        'reaper-console-track-name is-editable',
        () => beginTrackRename(track.index)
      );
      trigger.disabled = trackRequestInFlight;
      trigger.setAttribute(
        'aria-label',
        'Rename track ' + track.index + ', currently ' + (name || 'untitled')
      );
      item.appendChild(trigger);
    } else {
      item.appendChild(el('strong', 'reaper-console-track-name', name || 'Untitled track'));
    }

    const chips = el('span', 'reaper-console-track-chips');
    renderToggleChip(
      chips,
      track,
      'mute',
      'M',
      'Mute',
      trackDisplayValue(track, 'muted', Boolean(track.muted)),
      editable
    );
    renderToggleChip(
      chips,
      track,
      'solo',
      'S',
      'Solo',
      trackDisplayValue(track, 'soloed', Boolean(track.soloed)),
      editable
    );
    renderToggleChip(
      chips,
      track,
      'arm',
      'R',
      'Record-arm',
      trackDisplayValue(track, 'armed', Boolean(track.armed)),
      editable
    );
    item.appendChild(chips);
    list.appendChild(item);

    if (stripNotice && stripNotice.index === track.index) {
      const notice = el('li', 'reaper-console-track-notice', stripNotice.text);
      notice.setAttribute('role', 'status');
      list.appendChild(notice);
    }
  }

  function renderDropIndicator(list) {
    const indicator = el('li', 'reaper-console-track-drop-indicator');
    indicator.setAttribute('aria-hidden', 'true');
    list.appendChild(indicator);
  }

  function renderRunnerUnavailable(section) {
    const panel = el('div', 'reaper-console-track-degraded');
    panel.appendChild(
      el(
        'span',
        '',
        'Track editing needs the Ori REAPER runner. These tracks are read-only until it is installed.'
      )
    );
    panel.appendChild(button('Fix setup', 'reaper-console-btn is-secondary', openSetupFix));
    section.appendChild(panel);
  }

  function renderTracks(host, state) {
    const section = el('section', 'reaper-console-tracks');
    const head = el('div', 'reaper-console-section-head');
    head.appendChild(el('h3', '', 'Tracks'));
    head.appendChild(el('span', '', trackCountLabel(state)));
    section.appendChild(head);

    // Strips are interactive only when REAPER is connected and the runner is
    // installed; otherwise the list degrades to a read-only readout rather
    // than offering controls that cannot work (PRD 4.5 item 28).
    const editable = Boolean(state.connected && state.track_editing_available);
    if (state.connected && !state.track_editing_available) renderRunnerUnavailable(section);

    const list = el('ol', 'reaper-console-track-list');
    const tracks = Array.isArray(state.tracks) ? state.tracks : [];
    if (!tracks.length) {
      list.appendChild(el('li', 'reaper-console-empty', 'No project tracks'));
    } else {
      const dropTarget = dragState ? dragState.targetIndex : 0;
      tracks.forEach(track => {
        if (dropTarget && track.index === dropTarget) renderDropIndicator(list);
        renderTrackStrip(list, track, editable, tracks.length);
      });
      // The drop is past every strip Ori knows about (moving to the end).
      if (dropTarget && dropTarget > tracks.length) renderDropIndicator(list);
    }
    if (stripNotice && !stripNotice.index) {
      const notice = el('li', 'reaper-console-track-notice', stripNotice.text);
      notice.setAttribute('role', 'status');
      list.appendChild(notice);
    }
    section.appendChild(list);
    host.appendChild(section);
  }

  function renderOnline(host, state) {
    const project = el('section', 'reaper-console-project');
    const title = el('div', 'reaper-console-project-title');
    title.appendChild(el('span', 'reaper-console-kicker', 'OPEN PROJECT'));
    title.appendChild(el('h3', '', state.project || 'Untitled project'));
    project.appendChild(title);

    const readouts = el('dl', 'reaper-console-readouts');
    [
      ['Tempo', tempoLabel(state.tempo)],
      ['Meter', state.time_signature || '—']
    ].forEach(([label, value]) => {
      const item = el('div', 'reaper-console-readout');
      item.appendChild(el('dt', '', label));
      item.appendChild(el('dd', '', value));
      readouts.appendChild(item);
    });
    project.appendChild(readouts);
    host.appendChild(project);
    renderTransport(host, state);
    renderActionFeedback(host);
    renderActionGrid(host);
    renderTracks(host, state);
  }

  function renderConsole() {
    const host = consoleHost();
    if (!host || !consoleOpen) return;
    // The panel is rebuilt from scratch on every render, so carry the body's
    // scroll offset across. Without this the transport position ticking during
    // playback would yank the user back to the top of the console.
    const previousScroll =
      consoleBodyNode && typeof consoleBodyNode.scrollTop === 'number'
        ? consoleBodyNode.scrollTop
        : 0;
    clear(host);
    host.hidden = false;
    const backdrop = el('div', 'reaper-console-backdrop');
    backdrop.addEventListener('click', () => close());
    const panel = el('section', 'reaper-console-panel');
    panel.setAttribute('role', 'dialog');
    panel.setAttribute('aria-modal', 'true');
    panel.setAttribute('aria-labelledby', 'reaperConsoleTitle');
    renderHeader(panel, lastState);
    renderToasts(panel);
    const body = el('div', 'reaper-console-body');
    consoleBodyNode = body;
    if (!lastState) {
      const checking = el('section', 'reaper-console-checking');
      checking.appendChild(el('span', 'reaper-console-spinner'));
      checking.appendChild(el('p', '', 'Checking the current REAPER session…'));
      body.appendChild(checking);
    } else if (!lastState.connected) {
      renderOffline(body, lastState);
    } else {
      renderOnline(body, lastState);
    }
    panel.appendChild(body);
    host.appendChild(backdrop);
    host.appendChild(panel);
    if (previousScroll > 0) body.scrollTop = previousScroll;
  }

  function open(options) {
    const id = workspaceIdFromPage();
    if (!id) return false;
    const host = consoleHost();
    if (!host) return false;
    consoleTrigger = (options && options.trigger) || (document && document.activeElement) || null;
    consoleOpen = true;

    const coordinator = overlayCoordinator();
    if (coordinator && typeof coordinator.open === 'function') {
      consoleOverlayId = 'reaper-console';
      coordinator.open({
        id: consoleOverlayId,
        kind: 'modal',
        container: host,
        trigger: consoleTrigger,
        onClose: info => {
          if (info && info.reason === 'suspended') return;
          if (consoleOpen) close({ viaCoordinator: true });
        }
      });
    }
    renderConsole();
    // An open console polls at the faster cadence (PRD 4.1 item 8).
    syncPolling();
    void refresh();
    if (!catalogLoaded) void loadActions();
    if (!scriptsLoaded) void loadScripts();
    if (!proposalsLoaded) void loadProposals();
    return true;
  }

  function close(options) {
    if (!consoleOpen) return false;
    consoleOpen = false;
    consoleBodyNode = null;
    editingIndex = 0;
    pendingEdit = null;
    stripNotice = null;
    openPalette = 0;
    if (dragState) detachDragListeners();
    dragState = null;
    // Back to the slower map cadence now that nobody is editing.
    syncPolling();
    const host = typeof document === 'undefined' ? null : document.getElementById(CONSOLE_HOST_ID);
    if (host) {
      host.hidden = true;
      clear(host);
    }
    const coordinator = overlayCoordinator();
    if (
      !(options && options.viaCoordinator) &&
      coordinator &&
      typeof coordinator.close === 'function'
    ) {
      coordinator.close(consoleOverlayId);
    }
    consoleOverlayId = '';
    const trigger = consoleTrigger;
    consoleTrigger = null;
    if (trigger && typeof trigger.focus === 'function') trigger.focus();
    return true;
  }

  function openSetupFix() {
    close();
    if (typeof window === 'undefined' || !window.SetupWizard) return;
    const status =
      typeof window.SetupWizard.getStatus === 'function' ? window.SetupWizard.getStatus() : null;
    const step = (status && Array.isArray(status.steps) ? status.steps : []).find(
      item =>
        item &&
        item.kind === 'runtime_readiness' &&
        item.runtime_requirement_key === REQUIREMENT_KEY
    );
    if (typeof window.SetupWizard.open === 'function') window.SetupWizard.open(step && step.id);
  }

  function init(id) {
    workspaceId = id || workspaceIdFromPage();
    if (!workspaceId) return;
    syncPolling();
  }

  if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
    document.addEventListener('visibilitychange', () => {
      syncPolling({ refreshNow: mapVisible && documentVisible() });
    });
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', () => init(), { once: true });
    } else {
      init();
    }
  }

  const controller = {
    init,
    refresh,
    stationState,
    stationLabel,
    applies: () => Boolean(lastState && lastState.applies === true),
    setMapVisible,
    open,
    close,
    isOpen: () => consoleOpen,
    _setState: state => {
      lastState = state;
      lastMeaningfulState = meaningfulState(state);
      if (consoleOpen) renderConsole();
    },
    _state: () => lastState,
    _actions: () => catalog,
    _setActions: actions => {
      catalog = Array.isArray(actions) ? actions : [];
      catalogLoaded = true;
      if (consoleOpen) renderConsole();
    },
    _setScripts: nextScripts => {
      scripts = Array.isArray(nextScripts) ? nextScripts : [];
      scriptsLoaded = true;
      if (consoleOpen) renderConsole();
    },
    _setProposals: nextProposals => {
      proposals = Array.isArray(nextProposals) ? nextProposals : [];
      proposalsLoaded = true;
      if (consoleOpen) renderConsole();
    },
    _requestProposalRun: requestProposalRun,
    _requestProposalSave: requestProposalSave,
    _runProposal: runProposal,
    _saveProposal: saveProposal,
    _requestAction: requestAction,
    _executeAction: executeAction,
    _lastRun: () => lastRun,
    _polling: () => pollTimer !== null,
    _openSetupFix: openSetupFix,
    _toasts: () => toasts,
    _addToast: addToast,
    _undoFromToast: undoFromToast,
    _renameTrack: renameTrack,
    _setTrackColor: setTrackColor,
    _setTrackToggle: setTrackToggle,
    _undoTrackEdit: undoTrackEdit,
    _beginTrackRename: beginTrackRename,
    _cancelTrackRename: cancelTrackRename,
    _editingIndex: () => editingIndex,
    _pendingEdit: () => pendingEdit,
    _stripNotice: () => stripNotice,
    _pollIntervalMs: () => pollTimerIntervalMs,
    _openPalette: () => openPalette,
    _beginDrag: beginDrag,
    _dragOverIndex: dragOverIndex,
    _endDrag: endDrag,
    _cancelDrag: cancelDrag,
    _dragState: () => dragState,
    _moveTrack: moveTrack,
    _resetForTest: () => {
      stopPolling();
      workspaceId = '';
      mapVisible = false;
      requestInFlight = false;
      lastState = null;
      lastMeaningfulState = '';
      consoleOpen = false;
      consoleBodyNode = null;
      consoleTrigger = null;
      consoleOverlayId = '';
      catalog = [];
      catalogLoaded = false;
      scripts = [];
      scriptsLoaded = false;
      proposals = [];
      proposalsLoaded = false;
      proposalRequestInFlight = false;
      pendingProposal = null;
      proposalNotice = null;
      actionRequestInFlight = false;
      pendingAction = null;
      lastRun = null;
      editingIndex = 0;
      pendingEdit = null;
      stripNotice = null;
      trackRequestInFlight = false;
      openPalette = 0;
      if (dragState) detachDragListeners();
      dragState = null;
      toasts.forEach(toast => {
        if (toast.timer && typeof clearTimeout === 'function') clearTimeout(toast.timer);
      });
      toasts = [];
    }
  };

  if (typeof window !== 'undefined') window.ReaperConsole = controller;
})();
