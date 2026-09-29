// workspace-daily-briefs.js — a workspace's own Daily Brief history on its
// page, read-only. It appears only for a workspace that owns briefs but is
// not this installation's Personal HQ (whose briefs live on Home) — in
// practice a workspace imported with its history while another HQ is
// designated here. Nothing here generates, schedules or notifies.
//
// Pure helpers are exported for workspace-daily-briefs.test.js; the DOM
// wiring at the bottom no-ops without #workspaceBriefHistoryMount.

import { parseContent, renderContent } from './home-daily-brief.js';

// historyView decides whether to show the panel and lists dated entries,
// newest first. Only one date holds the workspace's current revision; every
// other date opens its newest readable revision.
export function historyView(history, { workspaceId, hqWorkspaceId }) {
  const rows = Array.isArray(history) ? history : [];
  const items = rows
    .filter(row => row && row.local_date && (row.current_revision_id || row.latest_revision_id))
    .map(row => ({
      date: String(row.local_date),
      revisionId: String(row.current_revision_id || row.latest_revision_id),
      revisions: Number(row.revision_count) > 0 ? Number(row.revision_count) : 1
    }))
    .sort((a, b) => (a.date < b.date ? 1 : a.date > b.date ? -1 : 0));
  const show = items.length > 0 && Boolean(workspaceId) && workspaceId !== hqWorkspaceId;
  return { show, items };
}

// dateLabel renders a stored local date (YYYY-MM-DD) without shifting it
// through the viewer's time zone.
export function dateLabel(date) {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(date || ''));
  if (!match) return String(date || '');
  const value = new Date(Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3])));
  return value.toLocaleDateString(undefined, {
    weekday: 'short',
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    timeZone: 'UTC'
  });
}

export function renderHistoryPanel(doc, mount, view, openEntry) {
  if (!mount) return;
  mount.innerHTML = '';
  mount.hidden = !view.show;
  if (!view.show) return;
  const head = doc.createElement('div');
  head.className = 'workspace-brief-history-head';
  const title = doc.createElement('h2');
  title.className = 'workspace-brief-history-title';
  title.textContent = 'Daily Briefs';
  const sub = doc.createElement('p');
  sub.className = 'workspace-brief-history-sub';
  sub.textContent =
    'Briefs this workspace kept from before. They are history: new briefs come from your Personal HQ here.';
  head.append(title, sub);
  mount.appendChild(head);
  const list = doc.createElement('div');
  list.className = 'workspace-brief-history-list';
  for (const item of view.items) {
    const entry = doc.createElement('details');
    entry.className = 'workspace-brief-history-entry';
    const summary = doc.createElement('summary');
    summary.textContent =
      item.revisions > 1
        ? `${dateLabel(item.date)} · ${item.revisions} versions`
        : dateLabel(item.date);
    const body = doc.createElement('div');
    body.className = 'workspace-brief-history-body';
    entry.append(summary, body);
    entry.addEventListener('toggle', () =>
      entry.open && !body.dataset.loaded ? openEntry(item, body) : undefined
    );
    list.appendChild(entry);
  }
  mount.appendChild(list);
}

export async function wireWorkspaceDailyBriefs({ doc, workspaceId, mount, fetchImpl }) {
  if (!mount || !workspaceId) return;
  const getJSON = async url => {
    const res = await fetchImpl(url, { headers: { Accept: 'application/json' } });
    if (!res || !res.ok) return null;
    return res.json();
  };
  const base = `/api/workspaces/${encodeURIComponent(workspaceId)}/daily-briefs`;
  let history;
  try {
    history = (await getJSON(base))?.history;
  } catch (_) {
    return;
  }
  if (!Array.isArray(history) || history.length === 0) return;
  let hqWorkspaceId = '';
  try {
    hqWorkspaceId = (await getJSON('/api/personal-hq/status'))?.status?.workspace_id || '';
  } catch (_) {
    return; // unknown designation: stay hidden rather than duplicate Home
  }
  const view = historyView(history, { workspaceId, hqWorkspaceId });
  renderHistoryPanel(doc, mount, view, async (item, body) => {
    body.textContent = 'Loading…';
    try {
      const data = await getJSON(`${base}/${encodeURIComponent(item.revisionId)}`);
      if (!data || !data.revision) throw new Error('missing');
      body.innerHTML = renderContent(parseContent(data.revision));
      body.dataset.loaded = 'true';
    } catch (_) {
      body.textContent = 'This brief could not be opened.';
    }
  });
}

(function () {
  if (typeof document === 'undefined') return;
  const mount = document.getElementById('workspaceBriefHistoryMount');
  if (!mount) return;
  const workspaceId =
    (typeof window !== 'undefined' && window.currentWorkspaceId) ||
    document.body?.dataset?.workspaceId ||
    '';
  if (!workspaceId) return;
  void wireWorkspaceDailyBriefs({
    doc: document,
    workspaceId: String(workspaceId).trim(),
    mount,
    fetchImpl: (url, options) => fetch(url, options)
  });
})();
