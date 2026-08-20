// reaper-console.js — live connection state for the REAPER Map station.
(function () {
  'use strict';

  const POLL_INTERVAL_MS = 5000;
  let workspaceId = '';
  let mapVisible = false;
  let pollTimer = null;
  let requestInFlight = false;
  let lastState = null;
  let lastMeaningfulState = '';

  function workspaceIdFromPage() {
    if (workspaceId) return workspaceId;
    if (typeof window === 'undefined') return '';
    if (window.currentWorkspaceId) return String(window.currentWorkspaceId);
    const path = (window.location && window.location.pathname) || '';
    const parts = path.split('/').filter(Boolean);
    return parts[0] === 'workspaces' && parts[1] ? decodeURIComponent(parts[1]) : '';
  }

  function documentVisible() {
    return typeof document === 'undefined' || document.visibilityState !== 'hidden';
  }

  function meaningfulState(state) {
    return JSON.stringify({
      applies: Boolean(state && state.applies),
      connected: Boolean(state && state.connected),
      reason: String((state && state.reason) || '')
    });
  }

  function publishIfChanged(state) {
    const next = meaningfulState(state);
    if (next === lastMeaningfulState) return;
    lastMeaningfulState = next;
    if (typeof document === 'undefined' || typeof document.dispatchEvent !== 'function') return;
    const event =
      typeof CustomEvent === 'function'
        ? new CustomEvent('ori:reaper-state-changed', { detail: state })
        : { type: 'ori:reaper-state-changed', detail: state };
    document.dispatchEvent(event);
  }

  async function refresh() {
    const id = workspaceIdFromPage();
    if (!id || requestInFlight || typeof fetch !== 'function') return lastState;
    requestInFlight = true;
    try {
      const response = await fetch('/api/workspaces/' + encodeURIComponent(id) + '/reaper/state', {
        headers: { Accept: 'application/json' }
      });
      if (!response.ok) throw new Error('state request failed');
      const state = await response.json();
      if (!state || typeof state.connected !== 'boolean') throw new Error('invalid state');
      lastState = state;
      publishIfChanged(state);
      return state;
    } catch (_error) {
      const failed = {
        applies: lastState ? lastState.applies === true : false,
        connected: false,
        reason: 'check_failed'
      };
      lastState = failed;
      publishIfChanged(failed);
      return failed;
    } finally {
      requestInFlight = false;
    }
  }

  function stopPolling() {
    if (pollTimer !== null && typeof clearInterval === 'function') clearInterval(pollTimer);
    pollTimer = null;
  }

  function syncPolling(refreshNow) {
    if (!mapVisible || !documentVisible()) {
      stopPolling();
      return;
    }
    if (refreshNow || !lastState) void refresh();
    if (pollTimer === null && typeof setInterval === 'function') {
      pollTimer = setInterval(() => {
        if (mapVisible && documentVisible()) void refresh();
      }, POLL_INTERVAL_MS);
    }
  }

  function setMapVisible(visible) {
    const next = Boolean(visible);
    const changed = next !== mapVisible;
    mapVisible = next;
    syncPolling(changed && next);
  }

  function reasonLabel(reason) {
    switch (String(reason || '')) {
      case 'web_remote_off':
        return 'Web Remote off';
      case 'reaper_unreachable':
        return 'REAPER not running';
      case 'check_failed':
        return 'Connection check failed';
      default:
        return 'Web Remote unavailable';
    }
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
    return {
      applies: true,
      value: 'Connected',
      description: 'REAPER is connected now',
      tone: 'clear'
    };
  }

  function init(id) {
    workspaceId = id || workspaceIdFromPage();
    syncPolling(false);
  }

  if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
    document.addEventListener('visibilitychange', () =>
      syncPolling(mapVisible && documentVisible())
    );
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
    stationLabel: () => 'REAPER',
    applies: () => Boolean(lastState && lastState.applies === true),
    setMapVisible,
    open: () => false,
    close: () => false,
    isOpen: () => false,
    _setState: state => {
      lastState = state;
      lastMeaningfulState = meaningfulState(state);
    },
    _state: () => lastState,
    _polling: () => pollTimer !== null,
    _resetForTest: () => {
      stopPolling();
      workspaceId = '';
      mapVisible = false;
      requestInFlight = false;
      lastState = null;
      lastMeaningfulState = '';
    }
  };

  if (typeof window !== 'undefined') window.ReaperConsole = controller;
})();
