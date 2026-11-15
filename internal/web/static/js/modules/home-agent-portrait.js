import { agentFace, fetchAgentRecords } from './folder-agent-card.js';

function escapeHtml(value) {
  return String(value ?? '').replace(
    /[&<>"']/g,
    c =>
      ({
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;'
      })[c]
  );
}

// Home owns the identity lookup and all markup supplied to the shared Map.
// The resident remains the local default; an explicit face is never inferred
// from dormant uploaded/character fields in a generated appearance.
export function homeAgentPortraitHTML(
  name,
  record,
  pending,
  isCommander = false,
  win = globalThis.window,
  options = {}
) {
  const mode = String(record?.appearance?.mode || '')
    .trim()
    .toLowerCase();
  const chosen = mode === 'character' || mode === 'uploaded';
  if (!pending && (!chosen || !win?.AgentAvatar?.markup)) {
    return (
      win?.OriWorkspaceAgentPortrait?.markup?.(name, {
        isKeeper: isCommander,
        className: options.className,
        href: options.href
      }) ||
      win?.OriWorkspaceMap?.agentPortraitHTML?.(name, isCommander) ||
      `<span class="cockpit-rail-portrait-fallback">${escapeHtml(name)}</span>`
    );
  }
  const href = /^\/[^/\\]/.test(String(options.href || '')) ? options.href : '';
  const tag = href ? 'a' : 'span';
  const safeName = escapeHtml(name);
  const className = String(options.className || '')
    .split(/\s+/)
    .filter(c => /^[a-zA-Z0-9_-]+$/.test(c))
    .join(' ');
  const face = pending
    ? '<span class="home-agent-face-slot" aria-hidden="true"></span>'
    : win.AgentAvatar.markup(agentFace({ name }, record, win), { size: options.size || 40 });
  return (
    `<${tag} class="ws-map-av home-agent-portrait${isCommander ? ' is-keeper' : ''}${pending ? ' is-pending' : ''}${href ? ' is-link' : ''} ${className}"${href ? ` href="${escapeHtml(href)}"` : ''} title="${safeName}">` +
    face +
    (isCommander ? '<span class="ws-map-av-commander-mark" aria-hidden="true">★</span>' : '') +
    `<span class="ws-map-av-label" title="${safeName}">${safeName}</span></${tag}>`
  );
}

// A page-level directory, shared by panel and tiles. Synchronous callers batch
// into one microtask/request. New names during a request queue behind it; the
// returned full list can satisfy them without another request. Absent names and
// failures are remembered, so a 2-second folder poll cannot become an API loop.
export function createHomeAgentDirectory({ fetchImpl, onChange = () => {} }) {
  let records = new Map();
  const asked = new Set();
  const pending = new Set();
  let scheduled = false;
  let inFlight = false;
  async function drain() {
    scheduled = false;
    if (inFlight) return;
    const names = [...pending].filter(name => !records.has(name) && !asked.has(name));
    if (!names.length) {
      pending.clear();
      return;
    }
    names.forEach(name => asked.add(name));
    inFlight = true;
    const found = await fetchAgentRecords(fetchImpl);
    if (found) records = found;
    names.forEach(name => pending.delete(name));
    for (const name of pending) if (records.has(name)) pending.delete(name);
    inFlight = false;
    onChange();
    if (pending.size) schedule();
  }
  function schedule() {
    if (scheduled || inFlight) return;
    scheduled = true;
    queueMicrotask(() => void drain());
  }
  return {
    get records() {
      return records;
    },
    pending,
    ensure(names) {
      for (const value of names) {
        const name = String(value || '').trim();
        if (name && !records.has(name) && !asked.has(name)) pending.add(name);
      }
      if (pending.size) schedule();
    }
  };
}
