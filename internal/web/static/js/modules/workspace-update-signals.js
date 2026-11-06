import { workspacePageURL } from './workspace-routes.js';

const INTENT_KEYS = [
  'update',
  'update_workspace',
  'update_session',
  'update_lead',
  'update_message'
];
const text = value => String(value ?? '');
const escape = value =>
  text(value).replace(
    /[&<>"']/g,
    char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]
  );
const hasControl = value =>
  Array.from(value).some(char => {
    const code = char.codePointAt(0);
    return code < 32 || (code >= 127 && code <= 159);
  });
const id = value =>
  typeof value === 'string' &&
  value.length > 0 &&
  value.length <= 128 &&
  value.trim() === value &&
  !hasControl(value);
const count = value => (Number.isSafeInteger(value) && value > 0 ? value : 0);
const known = state => ['ready', 'not_applicable', 'unconfigured'].includes(state);

// URLs carry a one-shot navigation request, never a seen mark or approval. A
// welcome names the saved identity selected here so a replacement lead/thread
// cannot inherit a stale link. Review always reopens the current saved review.
export function workspaceUpdateRoute(data, kind) {
  const slug = data?.folder_slug;
  if (
    !id(data?.workspace_id) ||
    typeof slug !== 'string' ||
    !slug ||
    slug.length > 255 ||
    slug.trim() !== slug ||
    slug === '.' ||
    slug === '..' ||
    /[\\/]/.test(slug) ||
    hasControl(slug)
  )
    return '';
  if (!['welcome', 'review'].includes(kind) || data[kind]?.state !== 'ready' || data[kind]?.stale)
    return '';
  if (kind === 'review' && (!id(data.review.root_id) || !id(data.review.batch_id))) return '';
  const query = new URLSearchParams({ update: kind, update_workspace: data.workspace_id });
  if (kind === 'welcome') {
    const welcome = data.welcome;
    const message = welcome?.messages?.find(item => !item.seen_at);
    if (!id(welcome?.session_id) || !id(welcome?.agent_instance_id) || !id(message?.message_id))
      return '';
    query.set('update_session', welcome.session_id);
    query.set('update_lead', welcome.agent_instance_id);
    query.set('update_message', message.message_id);
  }
  return workspacePageURL(slug, [], { search: query });
}

export function parseWorkspaceUpdateIntent(search) {
  const params = new URLSearchParams(search);
  if (!INTENT_KEYS.some(key => params.has(key))) return null;
  const kind = params.get('update');
  const intent = {
    kind,
    workspaceId: params.get('update_workspace'),
    sessionId: params.get('update_session') || '',
    leadId: params.get('update_lead') || '',
    messageId: params.get('update_message') || ''
  };
  const valid =
    INTENT_KEYS.every(key => params.getAll(key).length <= 1) &&
    ['welcome', 'review'].includes(kind) &&
    id(intent.workspaceId) &&
    (kind === 'welcome'
      ? id(intent.sessionId) && id(intent.leadId) && id(intent.messageId)
      : !intent.sessionId && !intent.leadId && !intent.messageId);
  return valid ? intent : { invalid: true };
}

export function withoutWorkspaceUpdateIntent(search) {
  const params = new URLSearchParams(search);
  for (const key of INTENT_KEYS) params.delete(key);
  return params.toString();
}

export function matchesWorkspaceUpdateIntent(intent, data) {
  if (!intent || intent.invalid || intent.workspaceId !== data?.workspace_id) return false;
  if (intent.kind === 'review')
    return data.review?.state === 'ready' && !data.review.stale && Boolean(data.review.root_id);
  const welcome = data.welcome;
  return (
    welcome?.state === 'ready' &&
    !welcome.stale &&
    welcome.session_id === intent.sessionId &&
    welcome.agent_instance_id === intent.leadId &&
    Boolean(welcome.messages?.some(message => message.message_id === intent.messageId))
  );
}

// All surfaces share these two independently labelled facts. No ticket count,
// per-file notification, historical welcome prose or local unread counter is used.
export function workspaceUpdateSignals(view, { stale = false } = {}) {
  const data = view?.data;
  if (!data || view.state === 'not_found')
    return { state: view?.state || 'loading', signals: [], name: '', workspaceId: '' };
  const name = text(data.name || 'Workspace');
  const fresh = view.state === 'ready' && !view.stale && !stale;
  const signals = [];
  const welcome = data.welcome || {};
  const review = data.review || {};
  const add = (kind, label, spoken, source) => {
    const current = fresh && source.state === 'ready' && !source.stale;
    const route = current ? workspaceUpdateRoute(data, kind) : '';
    signals.push({
      kind,
      text: current ? label : `${label} · last known`,
      label: `${name}: ${spoken}${current ? '' : ' Last known; refresh to confirm.'}`,
      route,
      stale: !current
    });
  };
  const unread = count(welcome.unread_count);
  if (unread)
    add(
      'welcome',
      'New message',
      `${unread} unread ${unread === 1 ? 'message' : 'messages'}${welcome.agent_name ? ` from ${welcome.agent_name}` : ''}. Open the saved conversation.`,
      welcome
    );
  const pending = count(review.pending_count);
  if (pending)
    add(
      'review',
      `${pending} ${pending === 1 ? 'file' : 'files'} to review`,
      `${pending} ${pending === 1 ? 'file' : 'files'} to review. Nothing moves without your approval.`,
      review
    );
  return {
    workspaceId: data.workspace_id,
    name,
    signals,
    state: fresh && known(welcome.state) && known(review.state) ? 'ready' : 'unavailable'
  };
}

// Hosts place this outside existing buttons (Map) or inside a non-button row
// (Tree). Text-only stale observations never carry a destination. Keyed hosts
// can preserve focused controls and avoid repainting unchanged labels.
export function safeSignalRoute(view, signal) {
  if (typeof signal.route !== 'string' || !/^\/workspaces\/[^/?#]+\?/.test(signal.route)) return '';
  const parsed = new URL(signal.route, 'https://ori.invalid');
  const intent = parseWorkspaceUpdateIntent(parsed.search);
  return parsed.origin === 'https://ori.invalid' &&
    /^\/workspaces\/[^/]+$/.test(parsed.pathname) &&
    !parsed.hash &&
    intent &&
    !intent.invalid &&
    intent.workspaceId === view.workspaceId &&
    intent.kind === signal.kind &&
    [...parsed.searchParams.keys()].every(key => INTENT_KEYS.includes(key))
    ? signal.route
    : '';
}

export function renderWorkspaceUpdateSignals(view, { tabIndex = 0 } = {}) {
  if (view?.state === 'unavailable' && !view.signals.length)
    return '<span class="workspace-update-signal is-stale">Updates unavailable</span>';
  return (view?.signals || [])
    .map(signal =>
      safeSignalRoute(view, signal)
        ? `<a class="workspace-update-signal is-${escape(signal.kind)}" href="${escape(signal.route)}" data-workspace-update="${escape(signal.kind)}" data-update-workspace="${escape(view.workspaceId)}" draggable="false" tabindex="${tabIndex === -1 ? '-1' : '0'}" aria-label="${escape(signal.label)}">${escape(signal.text)}</a>`
        : `<span class="workspace-update-signal is-stale" title="${escape(signal.label)}">${escape(signal.text)}</span>`
    )
    .join('');
}

// Map labels live inside its existing tile button, so they must not introduce
// nested links/buttons. Pointer activation and the existing Shift+F10 menu
// expose the same exact routes; the tile's accessible name announces the menu.
export function renderWorkspaceUpdateMapMarkers(view) {
  if (!view?.signals?.length)
    return view?.state === 'unavailable'
      ? '<span class="ws-map-update-markers"><span class="workspace-update-signal is-stale">Updates unavailable</span></span>'
      : '';
  return (
    '<span class="ws-map-update-markers">' +
    view.signals
      .map(signal => {
        const route = safeSignalRoute(view, signal);
        return `<span class="workspace-update-signal${route ? '' : ' is-stale'}"${route ? ` data-map-update-route="${escape(route)}"` : ''} title="${escape(signal.label)}">${escape(signal.text)}</span>`;
      })
      .join('') +
    '</span>'
  );
}

export function workspaceUpdateBadgeText(page, views = []) {
  const signals = views.flatMap(view => view.signals);
  const message = signals.some(signal => signal.kind === 'welcome' && !signal.stale);
  const review = signals.some(signal => signal.kind === 'review' && !signal.stale);
  const label = [
    message ? 'New message' : '',
    review ? 'Files to review' : '',
    signals.some(signal => signal.stale) ? 'Last known updates' : ''
  ]
    .filter(Boolean)
    .join(' · ');
  if (label) return label;
  if (page?.state === 'unavailable' || views.some(view => view.state === 'unavailable'))
    return 'Updates unavailable';
  return page?.hasMore ? 'More to check' : '';
}

export function renderWorkspaceUpdateSection(page, read) {
  const current = page || { state: 'loading', ids: [] };
  const rows = (current.ids || []).map(id =>
    workspaceUpdateSignals(read(id), { stale: current.stale })
  );
  const shown = rows.filter(row => row.signals.length || row.state === 'unavailable');
  const busy = current.state === 'loading';
  const status =
    current.state === 'unavailable'
      ? 'Workspace updates are unavailable. Previous results may be out of date.'
      : busy
        ? 'Checking workspace messages and filing reviews…'
        : shown.length
          ? 'Messages and filing decisions are separate. Nothing moves without your approval.'
          : 'No pending workspace messages or filing reviews in this page.';
  const list = shown
    .map(
      row =>
        `<li class="cockpit-workspace-update" data-update-row="${escape(row.workspaceId)}"><span class="cockpit-workspace-update-name">${escape(row.name || 'Workspace')}</span><div>${renderWorkspaceUpdateSignals(row)}</div></li>`
    )
    .join('');
  return (
    `<h3>Workspace messages &amp; filing</h3><p class="cockpit-workspace-update-status">${escape(status)}</p>` +
    (list ? `<ul class="cockpit-workspace-update-list">${list}</ul>` : '') +
    `<p class="cockpit-workspace-update-coverage">${current.ids?.length || 0} workspaces in this page.${current.hasMore ? ' More workspaces have not been checked yet.' : current.after ? ' End of the workspace list. Earlier pages may still have updates.' : ''}</p>` +
    '<div class="cockpit-workspace-update-controls">' +
    `<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-workspace-updates-refresh${busy ? ' aria-disabled="true"' : ''}>${current.state === 'unavailable' ? 'Retry updates' : 'Refresh updates'}</button>` +
    (current.hasMore
      ? `<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-workspace-updates-more${busy ? ' aria-disabled="true"' : ''}>Check next workspaces</button>`
      : '') +
    (current.after
      ? `<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-workspace-updates-start${busy ? ' aria-disabled="true"' : ''}>First page</button>`
      : '') +
    '</div>'
  );
}

// The existing Map is a classic script. Share the same presenter rather than
// duplicating source/count/route rules there; importing performs no reads.
export const WorkspaceUpdateSignals = {
  view: workspaceUpdateSignals,
  render: renderWorkspaceUpdateSignals,
  renderMap: renderWorkspaceUpdateMapMarkers,
  safeRoute: safeSignalRoute
};
if (typeof window !== 'undefined') window.OriWorkspaceUpdateSignals = WorkspaceUpdateSignals;
