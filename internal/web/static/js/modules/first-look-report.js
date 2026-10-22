// A hired agent's first look, reported back to the assistant (folder-hire-brief
// FR16-FR21): who did it and where, what it found in at most two sentences,
// the one task it suggests first with its reason, and Start this, Not now and
// Read the full report. Home's Today and the drawer on every other page draw
// the same card from the same Today item.
//
// Every field is text from the server's Today item; the suggestion and the
// summary are words a model wrote, so they are set as text, never as markup.

export const FIRST_LOOK_REPORT_KIND = 'folder_first_look';
// An agent hired from the Agents page reports through the same card, keyed by
// its hire (agents-hire-with-a-brief); a look that did not finish offers Retry.
export const HIRE_LOOK_REPORT_KIND = 'hire_first_look_report';
const REPORT_ACTIONS = ['start_this', 'not_now', 'retry'];
// A task's own page in a workspace, and nothing else.
const REPORT_ROUTE = /^\/workspaces\/[a-z0-9][a-z0-9-]*\/task\/[A-Za-z0-9_-]+$/;
// A workspace's own page, and nothing else.
const WORKSPACE_ROUTE = /^\/workspaces\/[a-z0-9][a-z0-9-]*$/;

// isFirstLookReportKind says whether a Today item is drawn as a report card
// rather than a row.
export function isFirstLookReportKind(kind) {
  return kind === FIRST_LOOK_REPORT_KIND || kind === HIRE_LOOK_REPORT_KIND;
}
const LOOK_ID = /^[A-Za-z0-9_-]{1,200}$/;
const WORKSPACE_ID = /^[A-Za-z0-9_-]{1,200}$/;

// mapPlacementView is a folder setup's "On the Map" row (FR23): where the
// automatic layout put the hired agent's building, in words, and Move, which
// opens Home's Map with that building selected and Move on. null without a
// workspace to move.
export function mapPlacementView({ agent, home, workspaceId } = {}) {
  const id = String(workspaceId || '').trim();
  if (!WORKSPACE_ID.test(id)) return null;
  const who = String(agent || '').trim();
  const building = who ? `${who}'s building` : 'Its building';
  const where = String(home || '').trim();
  return {
    label: 'On the Map',
    text: where ? `${building} sits inside ${where}.` : `${building} was placed automatically.`,
    move: { label: 'Move', href: `/?move=${encodeURIComponent(id)}` }
  };
}

// renderMapPlacement draws the "On the Map" row: its label, the sentence and
// the Move link.
export function renderMapPlacement(view, doc = document, className = 'first-look-report__map') {
  const row = doc.createElement('p');
  row.className = className;
  const label = doc.createElement('span');
  label.className = `${className}-label`;
  label.textContent = view.label;
  const link = doc.createElement('a');
  link.href = view.move.href;
  link.textContent = view.move.label;
  row.append(label, ` ${view.text} `, link);
  return row;
}

function initialsOf(name) {
  return String(name || '')
    .split(/\s+/)
    .filter(word => /^[\p{L}\p{N}]/u.test(word))
    .slice(0, 2)
    .map(word => Array.from(word)[0].toUpperCase())
    .join('');
}

// finishedAgo says when a report's look finished, the way a person would.
export function finishedAgo(sourceAt, now = new Date()) {
  const at = new Date(sourceAt || '');
  if (Number.isNaN(at.getTime()) || at.getFullYear() < 2000) return '';
  const minutes = Math.max(0, Math.round((now.getTime() - at.getTime()) / 60000));
  if (minutes < 1) return 'finished just now';
  if (minutes < 60) return `finished ${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `finished ${hours} h ago`;
  if (hours < 48) return 'finished yesterday';
  return `finished ${at.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}`;
}

// firstLookReportView is the render decision for one Today item, or null for
// an item that is not a finished first look's report.
export function firstLookReportView(item, now = new Date()) {
  if (!item || !isFirstLookReportKind(item.kind)) return null;
  const hire = item.kind === HIRE_LOOK_REPORT_KIND;
  const failed = hire && item.state === 'failed';
  if (item.state !== 'finished' && !failed) return null;
  const lookId = String(item.ref?.entity_id || '').trim();
  if (!LOOK_ID.test(lookId)) return null;
  const agent = String(item.attribution || '').trim() || 'Your agent';
  const workspace = String(item.workspace || '').trim();
  const ago = finishedAgo(item.source_at, now);
  const text = String(item.suggestion?.text || '').trim();
  const suggestion = text
    ? { text, evidence: String(item.suggestion?.evidence || '').trim() }
    : null;
  const offered = (Array.isArray(item.actions) ? item.actions : []).filter(action =>
    REPORT_ACTIONS.includes(action)
  );
  const actions = [];
  // Without a suggestion there is nothing to start.
  if (suggestion && offered.includes('start_this'))
    actions.push({ id: 'start_this', label: 'Start this', primary: true });
  if (offered.includes('not_now') && !failed)
    actions.push({ id: 'not_now', label: 'Not now', primary: false });
  if (failed && offered.includes('retry'))
    actions.push({ id: 'retry', label: 'Retry', primary: true });
  const route = String(item.report_route || '').trim();
  const workspaceRoute = String(item.route || '').trim();
  let report = null;
  if (hire && WORKSPACE_ROUTE.test(workspaceRoute))
    report = { label: 'Open workspace', href: workspaceRoute };
  else if (!hire && REPORT_ROUTE.test(route))
    report = { label: 'Read the full report', href: route };
  return {
    kind: hire ? 'hire' : 'folder',
    lookId,
    agent,
    initials: initialsOf(agent) || 'A',
    meta: [workspace, failed ? '' : ago].filter(Boolean).join(' · '),
    title:
      String(item.title || '').trim() ||
      (failed ? 'First look did not finish' : 'Finished its first look'),
    summary: String(item.detail || '').trim(),
    suggestion: failed ? null : suggestion,
    actions,
    report,
    map: mapPlacementView({ agent, home: item.home, workspaceId: item.ref?.workspace_id })
  };
}

const ACTION_VERBS = { start_this: 'start-suggestion', not_now: 'dismiss', retry: 'retry' };

// reportActionPath is where one report's action posts: a folder look by its
// task, a hire by its hire id. Retry is the hire's alone.
function reportActionPath(view, action) {
  const verb = ACTION_VERBS[action];
  if (!verb) return '';
  if (view.kind === 'hire')
    return `/api/personal-assistant/hires/${encodeURIComponent(view.lookId)}/${verb}`;
  if (action === 'retry') return '';
  return `/api/personal-assistant/first-look/${encodeURIComponent(view.lookId)}/${verb}`;
}

// firstLookReportAction posts Start this or Not now for one look. The request
// names only the look; the server reads the suggestion again from the look.
export async function firstLookReportAction(view, action, fetchImpl = fetch) {
  if (!view?.lookId || !REPORT_ACTIONS.includes(action)) return { ok: false, payload: null };
  const path = reportActionPath(view, action);
  if (!path) return { ok: false, payload: null };
  try {
    const response = await fetchImpl(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: '{}'
    });
    let payload = null;
    try {
      payload = await response.json();
    } catch (_) {
      payload = null;
    }
    // A suggestion already started is not a failure to report: the read that
    // follows shows it under Working on.
    const ok = Boolean(response?.ok) || payload?.code === 'already_acted';
    return { ok, payload };
  } catch (_) {
    return { ok: false, payload: null };
  }
}

// renderFirstLookReport draws one report as a card. onAction(action, button)
// is called for Start this and Not now; the caller posts and refreshes.
export function renderFirstLookReport(view, doc = document, { onAction } = {}) {
  const card = doc.createElement('article');
  card.className = 'first-look-report';
  card.dataset.lookId = view.lookId;
  card.dataset.reportKind = view.kind || 'folder';
  const titleId = `firstLookReportTitle-${view.lookId}`;
  card.setAttribute('aria-labelledby', titleId);

  const by = doc.createElement('div');
  by.className = 'first-look-report__by';
  const badge = doc.createElement('span');
  badge.className = 'first-look-report__initials';
  badge.setAttribute('aria-hidden', 'true');
  badge.textContent = view.initials;
  const who = doc.createElement('div');
  const agent = doc.createElement('div');
  agent.className = 'first-look-report__agent';
  agent.textContent = view.agent;
  who.append(agent);
  if (view.meta) {
    const meta = doc.createElement('div');
    meta.className = 'first-look-report__meta';
    meta.textContent = view.meta;
    who.append(meta);
  }
  by.append(badge, who);

  const title = doc.createElement('h3');
  title.className = 'first-look-report__title';
  title.id = titleId;
  title.textContent = view.title;
  card.append(by, title);

  if (view.summary) {
    const summary = doc.createElement('p');
    summary.className = 'first-look-report__summary';
    summary.textContent = view.summary;
    card.append(summary);
  }
  if (view.suggestion) {
    const box = doc.createElement('div');
    box.className = 'first-look-report__suggestion';
    const eyebrow = doc.createElement('div');
    eyebrow.className = 'first-look-report__eyebrow';
    eyebrow.textContent = 'Suggests first';
    const text = doc.createElement('div');
    text.className = 'first-look-report__suggestion-text';
    text.textContent = view.suggestion.text;
    box.append(eyebrow, text);
    if (view.suggestion.evidence) {
      const evidence = doc.createElement('div');
      evidence.className = 'first-look-report__evidence';
      evidence.textContent = view.suggestion.evidence;
      box.append(evidence);
    }
    card.append(box);
  }
  if (view.map) card.append(renderMapPlacement(view.map, doc));
  if (view.actions.length) {
    const actions = doc.createElement('div');
    actions.className = 'first-look-report__actions';
    for (const action of view.actions) {
      const button = doc.createElement('button');
      button.type = 'button';
      button.className = action.primary
        ? 'personal-assistant-today__action personal-assistant-today__action--primary'
        : 'personal-assistant-today__action';
      button.dataset.firstLookAction = action.id;
      button.textContent = action.label;
      button.addEventListener('click', () => onAction?.(action.id, button));
      actions.append(button);
    }
    card.append(actions);
  }
  if (view.report) {
    const link = doc.createElement('a');
    link.className = 'first-look-report__report';
    link.href = view.report.href;
    link.textContent = view.report.label;
    card.append(link);
  }
  return card;
}

// The assistant's own lines in its conversation about a hire (FR20): written
// by the host, never by a model turn.
export function hiredLine(agent) {
  return `Hired. ${String(agent || '').trim() || 'Your agent'} is on it. I'll tell you here when it has something.`;
}

export function reportedLine(agent, folder) {
  const who = String(agent || '').trim() || 'Your agent';
  const where = String(folder || '').trim();
  return `${who} finished its first look${where ? ` at ${where}` : ''}. It's under Needs you, with what it suggests first.`;
}

// mountFirstLookReports draws every report among a section's raw Today items
// into host and returns how many it drew. onDone runs after an action posted.
export function mountFirstLookReports(
  host,
  items,
  { doc = document, now = new Date(), onDone, fetchImpl } = {}
) {
  if (!host) return 0;
  const views = (Array.isArray(items) ? items : [])
    .map(item => firstLookReportView(item, now))
    .filter(Boolean);
  host.replaceChildren(
    ...views.map(view =>
      renderFirstLookReport(view, doc, {
        onAction: async (action, button) => {
          const buttons = button?.parentElement?.querySelectorAll('button') || [];
          buttons.forEach(control => {
            control.disabled = true;
          });
          const result = await firstLookReportAction(view, action, fetchImpl);
          if (!result.ok)
            buttons.forEach(control => {
              control.disabled = false;
            });
          onDone?.(action, result, view);
        }
      })
    )
  );
  host.hidden = views.length === 0;
  return views.length;
}
