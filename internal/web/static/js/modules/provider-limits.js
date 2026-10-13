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
    const value = level(provider.level) === 'exhausted' ? 'limit reached' : `${percent(window)}%`;
    parts.push(
      `${esc(label)} ${level(window.level) === 'warn' ? `<strong>${esc(value)}</strong>` : esc(value)}`
    );
  }
  if (!windows(provider).length && provider.level === 'exhausted') parts.push('limit reached');
  return parts.join(' · ');
}

export function pillDescription(provider, now = new Date()) {
  return [
    provider.display_name,
    ...windows(provider).map(
      window => `${window.label} ${percent(window)}% used, ${resetTime(window.resets_at, now)}`
    ),
    provider.level === 'exhausted' ? 'limit reached' : '',
    readingTime(provider, now)
  ]
    .filter(Boolean)
    .join(' · ');
}

export function renderSection(provider, { modelSpecific = false, now = new Date() } = {}) {
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
        <div class="stage__limits-label"><span>${esc(window.label)}</span><strong>${value}%${status}</strong></div>
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
  return `<section class="stage__limits" aria-label="Usage limits"><h3>Usage limits</h3>${body}</section>`;
}

if (typeof window !== 'undefined') {
  window.ProviderLimitsUI = { level, pillLabel, pillDescription, renderSection };
}
