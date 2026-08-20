// reaper-console.js — the one live REAPER surface for a workspace.
//
// The Map station and this overlay read the same current state. Persisted
// runtime readiness decides whether the station exists; this module never
// guesses from a workspace name, template, folder, tag, or agent roster.
(function () {
  'use strict';

  const POLL_INTERVAL_MS = 5000;
  const CONSOLE_HOST_ID = 'reaperConsole';
  const REQUIREMENT_KEY = 'reaper_live_control';

  let workspaceId = '';
  let mapVisible = false;
  let pollTimer = null;
  let requestInFlight = false;
  let lastState = null;
  let lastMeaningfulState = '';
  let consoleOpen = false;
  let consoleTrigger = null;
  let consoleOverlayId = '';

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
      tracks: (Array.isArray(state.tracks) ? state.tracks : []).map(track => ({
        index: Number(track.index || 0),
        name: String(track.name || ''),
        muted: Boolean(track.muted),
        soloed: Boolean(track.soloed),
        armed: Boolean(track.armed)
      }))
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
    const path = apiPath();
    if (!path || requestInFlight || typeof fetch !== 'function') return lastState;
    requestInFlight = true;
    try {
      const response = await fetch(path, { headers: { Accept: 'application/json' } });
      if (!response.ok) throw new Error('state request failed');
      const state = await response.json();
      if (!state || typeof state.connected !== 'boolean') throw new Error('invalid state');
      lastState = state;
      publishIfChanged(state);
      if (consoleOpen) renderConsole();
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
      publishIfChanged(failed);
      if (consoleOpen) renderConsole();
      return failed;
    } finally {
      requestInFlight = false;
    }
  }

  function stopPolling() {
    if (pollTimer !== null && typeof clearInterval === 'function') clearInterval(pollTimer);
    pollTimer = null;
  }

  function syncPolling({ refreshNow = false } = {}) {
    const shouldPoll = mapVisible && documentVisible();
    if (!shouldPoll) {
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

  function trackStateLabel(track) {
    const states = [];
    if (track.muted) states.push('Muted');
    if (track.soloed) states.push('Solo');
    if (track.armed) states.push('Armed');
    return states.length ? states.join(' · ') : 'Ready';
  }

  function renderTracks(host, state) {
    const section = el('section', 'reaper-console-tracks');
    const head = el('div', 'reaper-console-section-head');
    head.appendChild(el('h3', '', 'Tracks'));
    head.appendChild(el('span', '', trackCountLabel(state)));
    section.appendChild(head);
    const list = el('ol', 'reaper-console-track-list');
    const tracks = Array.isArray(state.tracks) ? state.tracks : [];
    if (!tracks.length) {
      list.appendChild(el('li', 'reaper-console-empty', 'No project tracks'));
    } else {
      tracks.forEach(track => {
        const item = el('li', 'reaper-console-track');
        item.appendChild(
          el('span', 'reaper-console-track-index', String(track.index).padStart(2, '0'))
        );
        item.appendChild(el('strong', 'reaper-console-track-name', track.name || 'Untitled track'));
        const status = el('span', 'reaper-console-track-state', trackStateLabel(track));
        if (track.muted) status.classList.add('is-muted');
        if (track.soloed) status.classList.add('is-soloed');
        if (track.armed) status.classList.add('is-armed');
        item.appendChild(status);
        list.appendChild(item);
      });
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
      ['Meter', state.time_signature || '—'],
      ['Transport', state.play_state || 'stopped'],
      ['Position', state.position || '—']
    ].forEach(([label, value]) => {
      const item = el('div', 'reaper-console-readout');
      item.appendChild(el('dt', '', label));
      item.appendChild(el('dd', '', value));
      readouts.appendChild(item);
    });
    project.appendChild(readouts);
    host.appendChild(project);
    renderTracks(host, state);
  }

  function renderConsole() {
    const host = consoleHost();
    if (!host || !consoleOpen) return;
    clear(host);
    host.hidden = false;
    const backdrop = el('div', 'reaper-console-backdrop');
    backdrop.addEventListener('click', () => close());
    const panel = el('section', 'reaper-console-panel');
    panel.setAttribute('role', 'dialog');
    panel.setAttribute('aria-modal', 'true');
    panel.setAttribute('aria-labelledby', 'reaperConsoleTitle');
    renderHeader(panel, lastState);
    const body = el('div', 'reaper-console-body');
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
    void refresh();
    return true;
  }

  function close(options) {
    if (!consoleOpen) return false;
    consoleOpen = false;
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
    _polling: () => pollTimer !== null,
    _openSetupFix: openSetupFix,
    _resetForTest: () => {
      stopPolling();
      workspaceId = '';
      mapVisible = false;
      requestInFlight = false;
      lastState = null;
      lastMeaningfulState = '';
      consoleOpen = false;
      consoleTrigger = null;
      consoleOverlayId = '';
    }
  };

  if (typeof window !== 'undefined') window.ReaperConsole = controller;
})();
