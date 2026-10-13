// Shared presentation for subscription windows on the roster and CLI pages.
import { formatRelativeTime } from './relative-time.js';

const sourceNames = {
  codex_session_log: 'session log',
  codex_app_server: 'Codex app server',
  claude_oauth_usage: 'Anthropic usage endpoint',
  claude_statusline_mirror: 'status line mirror'
};

function esc(value) {
  return String(value ?? '').replace(
    /[&<>"']/g,
    char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]
  );
}

export function level(value) {
  return ['ok', 'warn', 'exhausted'].includes(value) ? value : 'ok';
}

function windows(provider, modelSpecific = false) {
  return (provider.windows || []).filter(window => modelSpecific || !window.model_specific);
}

function percent(window) {
  const value = Number(window.used_percent);
  return Number.isFinite(value) ? Math.max(0, value) : 0;
}

export function resetTime(seconds, now = new Date()) {
  if (!seconds) return 'Reset time unavailable';
  const date = new Date(seconds * 1000);
  if (Number.isNaN(date.getTime())) return 'Reset time unavailable';
  const minutes = Math.max(0, Math.ceil((date - now) / 60000));
  let relative;
  if (minutes >= 1440)
    relative = `in ${Math.floor(minutes / 1440)}d ${Math.floor((minutes % 1440) / 60)}h`;
  else if (minutes >= 60) relative = `in ${Math.floor(minutes / 60)}h ${minutes % 60}m`;
  else relative = `in ${minutes}m`;
  const local = date.toLocaleString(undefined, {
    ...(minutes >= 1440 ? { month: 'short', day: 'numeric' } : {}),
    hour: '2-digit',
    minute: '2-digit'
  });
  return minutes ? `resets ${relative} (${local})` : `reset time passed (${local})`;
}

export function readingTime(provider, now = new Date()) {
  const age = formatRelativeTime(provider.fetched_at, { now });
  return `Updated ${age || 'at an unknown time'} · ${sourceNames[provider.source] || 'unknown source'}`;
}

export function pillLabel(provider) {
  const parts = [esc(provider.display_name)];
  for (const window of windows(provider)) {
    const label = { five_hour: '5h', weekly: 'Wk' }[window.id] || window.label;
    const value =
      level(window.level) === 'exhausted' ? 'limit reached' : `${percent(window)}% used`;
    parts.push(
      `${esc(label)} ${level(provider.level) === 'warn' || level(window.level) === 'warn' ? `<strong>${esc(value)}</strong>` : esc(value)}`
    );
  }
  // A reached flag or a hidden model window can exhaust the provider without
  // exhausting every visible window. Do not relabel healthy windows as full.
  if (
    provider.level === 'exhausted' &&
    !windows(provider).some(window => window.level === 'exhausted')
  ) {
    parts.push('limit reached');
  }
  return parts.join(' · ');
}

export function pillDescription(provider, now = new Date()) {
  return [
    provider.display_name,
    ...windows(provider).map(
      window => `${window.label} ${percent(window)}% used, ${resetTime(window.resets_at, now)}`
    ),
    provider.level === 'exhausted'
      ? 'limit reached'
      : provider.level === 'warn'
        ? 'nearing limit'
        : '',
    readingTime(provider, now)
  ]
    .filter(Boolean)
    .join(' · ');
}

export function renderSection(
  provider,
  { modelSpecific = false, refresh = false, pending = false, now = new Date() } = {}
) {
  if (!provider) return '';
  let body = '';
  if (provider.state === 'ok') {
    if (provider.plan) body += `<p class="stage__limits-plan">${esc(provider.plan)}</p>`;
    if (provider.level === 'exhausted') body += '<p class="stage__limits-detail">Limit reached</p>';
    body += windows(provider, modelSpecific)
      .map(window => {
        const value = percent(window);
        const severity = level(window.level);
        const status =
          severity === 'exhausted'
            ? ' · limit reached'
            : severity === 'warn'
              ? ' · nearing limit'
              : '';
        return `<div class="stage__limits-row">
        <div class="stage__limits-label"><span>${esc(window.label)}</span><strong>${value}% used${status}</strong></div>
        <div class="stage__progression-xpbar" role="meter" aria-label="${esc(window.label)} usage"
          aria-valuenow="${Math.min(100, value)}" aria-valuemin="0" aria-valuemax="100" aria-valuetext="${value}% used${status}">
          <div class="stage__progression-xpfill stage__limits-fill--${severity}" style="width: ${Math.min(100, value)}%"></div>
        </div><p class="stage__limits-reset">${esc(resetTime(window.resets_at, now))}</p>
      </div>`;
      })
      .join('');
  }
  if (provider.detail) body += `<p class="stage__limits-detail">${esc(provider.detail)}</p>`;
  if (provider.fetched_at)
    body += `<p class="stage__limits-footer">${esc(readingTime(provider, now))}</p>`;
  if (refresh)
    body += `<button type="button" class="stage__limits-refresh" data-limits-refresh ${pending ? 'disabled' : ''}>${pending ? 'Refreshing…' : 'Refresh'}</button>`;
  return `<section class="stage__limits" aria-label="Usage limits"><h3>Usage limits</h3>${body}</section>`;
}

export const refreshInterval = 3 * 60 * 1000;

// On-demand browser polling only: hidden tabs and pages in the back/forward
// cache have no timer. The returned disposer also supports detail-card remounts.
export function startRefreshCadence(
  load,
  {
    doc = document,
    win = window,
    now = Date.now,
    setTimer = setTimeout,
    clearTimer = clearTimeout
  } = {}
) {
  let timer = null;
  let hiddenAt = null;
  let started = false;
  let suspended = false;
  let disposed = false;
  function pause() {
    if (timer !== null) clearTimer(timer);
    timer = null;
    if (hiddenAt === null) hiddenAt = now();
  }
  function schedule() {
    if (timer !== null) clearTimer(timer);
    timer = setTimer(() => {
      timer = null;
      if (disposed || suspended || doc.visibilityState !== 'visible') return;
      load(true);
      schedule();
    }, refreshInterval);
  }
  function resume() {
    if (disposed || suspended) return;
    if (doc.visibilityState !== 'visible') {
      pause();
      return;
    }
    if (!started || (hiddenAt !== null && now() - hiddenAt > refreshInterval)) load(true);
    started = true;
    hiddenAt = null;
    schedule();
  }
  function pageHide() {
    suspended = true;
    pause();
  }
  function pageShow() {
    suspended = false;
    resume();
  }
  doc.addEventListener('visibilitychange', resume);
  win.addEventListener('pagehide', pageHide);
  win.addEventListener('pageshow', pageShow);
  resume();
  return () => {
    disposed = true;
    pause();
    doc.removeEventListener('visibilitychange', resume);
    win.removeEventListener('pagehide', pageHide);
    win.removeEventListener('pageshow', pageShow);
  };
}

export function renderMirrorControl(mirror, pending = false, error = '') {
  if (!mirror) return '';
  const action = mirror.installed ? 'remove' : 'install';
  return `<section class="stage__limits-mirror" aria-label="Status-line fallback">
    <h4>Status-line fallback</h4>
    <p>Updates <code>~/.claude/settings.json</code> to copy the status line to Ori while keeping your existing command. Remove restores the previous setting.</p>
    <p>Updates only during interactive Claude Code sessions on Pro or Max, after the first API response. Headless Ori turns do not update it.</p>
    ${mirror.installed ? '<p>Installed</p>' : ''}
    <button type="button" class="modern-btn modern-btn-secondary" data-limits-mirror="${action}" ${pending ? 'disabled' : ''}>${pending ? 'Updating…' : mirror.installed ? 'Remove' : 'Mirror the status line to Ori'}</button>
    ${error ? `<p role="alert">${esc(error)}</p>` : ''}
  </section>`;
}

const detailMounts = new WeakMap();

// Each detail card owns its reading without replacing the synced settings.
export function mountDetail(root, providerID) {
  detailMounts.get(root)?.();
  const host = root.querySelector('[data-provider-limits]');
  if (!host) return async () => {};
  let provider = null;
  let pending = false;
  let pendingRequest = null;
  let warningLogged = false;
  let disposed = false;
  let mirrorPending = false;
  let mirrorError = '';
  function render() {
    if (disposed || !provider) return;
    host.hidden = false;
    host.innerHTML = renderSection(provider, { modelSpecific: true, refresh: true, pending });
    if (providerID === 'claude_code') {
      host.insertAdjacentHTML(
        'beforeend',
        renderMirrorControl(provider.mirror, mirrorPending, mirrorError)
      );
    }
    if (provider.credits) {
      const credits = provider.credits;
      const value = credits.unlimited
        ? 'unlimited'
        : !credits.has_credits
          ? 'none'
          : credits.balance;
      host
        .querySelector('.stage__limits')
        .insertAdjacentHTML(
          'beforeend',
          `<p class="stage__limits-detail">Credits: ${esc(value)}</p>`
        );
    }
  }
  function load(refresh = true, notify = false) {
    if (disposed) return Promise.resolve();
    // An explicit refresh arriving during the initial live read must still
    // issue refresh=1 afterwards, rather than silently dropping the click.
    if (pending) return refresh ? pendingRequest.then(() => load(true, notify)) : pendingRequest;
    const refocus = host.contains(document.activeElement);
    pending = true;
    render();
    pendingRequest = (async () => {
      try {
        const response = await fetch('/api/usage/limits' + (refresh ? '?refresh=1' : ''));
        if (!response.ok) throw new Error('Usage limits unavailable');
        const snapshot = await response.json();
        if (!snapshot || !Array.isArray(snapshot.providers))
          throw new Error('Invalid limits snapshot');
        if (disposed) return;
        provider = snapshot.providers.find(value => value.provider === providerID) || null;
        host.hidden = !provider;
        warningLogged = false;
      } catch {
        if (!disposed && !warningLogged) console.warn('[provider limits] usage limits unavailable');
        warningLogged = true;
        if (!disposed && notify && window.Toast)
          window.Toast.error('Could not refresh usage limits');
      } finally {
        pending = false;
        render();
        if (refocus && host.isConnected && document.activeElement === document.body) {
          host.querySelector('[data-limits-refresh]')?.focus();
        }
      }
    })();
    return pendingRequest;
  }
  async function changeMirror(action) {
    if (mirrorPending || disposed) return;
    mirrorPending = true;
    mirrorError = '';
    render();
    try {
      const response = await fetch('/api/usage/limits/claude-mirror', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action })
      });
      const result = await response.json();
      if (!response.ok)
        throw new Error(
          typeof result.error === 'string' ? result.error : 'Could not update status-line mirror'
        );
      if (disposed || !provider) return;
      provider.mirror = result;
      await load(true);
    } catch (error) {
      mirrorError = error?.message || 'Could not update status-line mirror';
      if (!disposed && window.Toast) window.Toast.error(esc(mirrorError));
    } finally {
      mirrorPending = false;
      render();
      if (!disposed && host.isConnected && document.activeElement === document.body) {
        host.querySelector('[data-limits-mirror]')?.focus();
      }
    }
  }
  function onClick(event) {
    if (event.target.closest('[data-limits-refresh]')) void load(true, true);
    const mirror = event.target.closest('[data-limits-mirror]');
    if (mirror) void changeMirror(mirror.dataset.limitsMirror);
  }
  host.addEventListener('click', onClick);
  const stop = startRefreshCadence(load);
  detailMounts.set(root, () => {
    disposed = true;
    stop();
    host.removeEventListener('click', onClick);
  });
  return (refresh = true) => load(refresh, true);
}

if (typeof window !== 'undefined') {
  window.ProviderLimitsUI = {
    level,
    pillLabel,
    pillDescription,
    renderSection,
    mountDetail,
    startRefreshCadence
  };
}
