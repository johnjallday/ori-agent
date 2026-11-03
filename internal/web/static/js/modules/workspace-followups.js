// workspace-followups.js — the follow-up management panel for a workspace that
// owns follow-ups (the Personal HQ, an Email Ops workspace, or an imported
// workspace with its own history). This is the mutation surface: Home surfaces
// follow-ups read-only, and completing/dismissing happens here. Additive: no-op
// without #workspaceFollowupMount or when the workspace has no follow-ups.
//
// The panel is live DOM. On the workspace Details page the command view moves
// the mount into its layout and back (workspace-command.js, shared surfaces);
// this module never depends on where the mount sits.
//
// Follow-up mutations reuse the existing /api/personal-hq/followups/* endpoints,
// which are keyed by follow-up id + user, so they operate correctly regardless
// of which surface invokes them.

import { followUpView } from './personal-hq-onboarding.js';
import { renderRelatedTicket, startMakeTicket } from './follow-up-ticket.js';

const DAY_MS = 24 * 60 * 60 * 1000;
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const OPEN_STATUSES = new Set(['active', 'snoozed', 'reopened', 'candidate']);
const OBSIDIAN_OPEN_PREFIX = 'obsidian://open?';

// The source groups in their tie-break order (FR9).
export const FOLLOW_UP_GROUPS = [
  { key: 'note_item', label: 'From your notes' },
  { key: 'email_thread', label: 'From email' },
  { key: 'journal', label: 'From your journal' },
  { key: 'manual', label: 'Added by you' },
  { key: 'other', label: 'Other' }
];

// managementView maps a raw follow-up list to the panel's view-model: card
// views and whether the panel should show at all.
export function managementView(items) {
  const views = (Array.isArray(items) ? items : []).map(followUpView).filter(Boolean);
  return { show: views.length > 0, count: views.length, items: views };
}

// followUpIDFromSearch accepts one exact, bounded record identity. Duplicate,
// empty, traversal-shaped, or otherwise malformed query values are inert.
export function followUpIDFromSearch(search) {
  try {
    const values = new URLSearchParams(String(search || '')).getAll('follow_up');
    if (values.length !== 1 || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$/.test(values[0])) return '';
    return values[0];
  } catch (_) {
    return '';
  }
}

// followUpActionFor returns the {url, label} for a follow-up card button.
export function followUpActionFor(kind) {
  switch (kind) {
    case 'confirm':
      return { url: '/api/personal-hq/followups/confirm', label: 'confirm' };
    case 'dismiss':
      return { url: '/api/personal-hq/followups/dismiss', label: 'dismiss' };
    case 'complete':
      return { url: '/api/personal-hq/followups/complete', label: 'complete' };
    case 'snooze':
      return { url: '/api/personal-hq/followups/snooze', label: 'snooze' };
    default:
      return null;
  }
}

// --- dates ------------------------------------------------------------------

function toDate(value) {
  if (!value) return null;
  const date = value instanceof Date ? value : new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

function startOfDay(date) {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
}

// dayOffset is how many local calendar days `date` is after `now` (0 = today,
// negative = past).
function dayOffset(date, now) {
  return Math.round((startOfDay(date) - startOfDay(now)) / DAY_MS);
}

function shortDate(date) {
  return `${MONTHS[date.getMonth()]} ${date.getDate()}`;
}

function isSnoozed(view) {
  return view.status === 'snoozed' && Boolean(toDate(view.snoozedUntil));
}

// effectiveDue is the date a card sorts by: a snoozed item's snooze end, or
// its due date (FR10).
function effectiveDue(view) {
  return isSnoozed(view) ? toDate(view.snoozedUntil) : toDate(view.dueAt);
}

// dueOffset is the due date's day offset, or null when the item has no due
// date or is snoozed (a snoozed item waits; it is not late).
function dueOffset(view, now) {
  if (isSnoozed(view)) return null;
  const due = toDate(view.dueAt);
  return due ? dayOffset(due, now) : null;
}

const needsDecision = view => view.categoryKey === 'needs_decision';
const isOpen = view => OPEN_STATUSES.has(view.status || 'active');

// --- pure view-model ------------------------------------------------------

// followUpSummary counts the loaded list for the header line (FR6). "Due this
// week" is today and the six days after; overdue items are counted apart.
export function followUpSummary(items, now = new Date()) {
  const summary = { open: 0, decision: 0, dueThisWeek: 0, overdue: 0, toConfirm: 0 };
  (items || []).forEach(view => {
    if (!view || !isOpen(view)) return;
    summary.open++;
    if (needsDecision(view)) summary.decision++;
    if (view.isCandidate) summary.toConfirm++;
    const offset = dueOffset(view, now);
    if (offset === null) return;
    if (offset < 0) summary.overdue++;
    else if (offset <= 6) summary.dueThisWeek++;
  });
  return summary;
}

// followUpSummaryText renders the summary, leaving out zero counts.
export function followUpSummaryText(summary) {
  const parts = [];
  if (summary.open) parts.push(`${summary.open} open`);
  if (summary.decision) {
    parts.push(`${summary.decision} ${summary.decision === 1 ? 'needs' : 'need'} a decision`);
  }
  if (summary.dueThisWeek) parts.push(`${summary.dueThisWeek} due this week`);
  if (summary.overdue) parts.push(`${summary.overdue} overdue`);
  if (summary.toConfirm) parts.push(`${summary.toConfirm} to confirm`);
  return parts.join(' · ');
}

function groupKeyOf(view) {
  const type = view && view.source ? view.source.type : '';
  return FOLLOW_UP_GROUPS.some(g => g.key === type && g.key !== 'other') ? type : 'other';
}

// orderWithinGroup orders one group's cards (FR10): dated items first, the
// earliest (so the overdue) first, a snoozed item at its snooze end; then
// needs-decision; then candidates; then the most recently updated.
export function orderWithinGroup(items, now = new Date()) {
  void now; // dates order themselves; kept for a stable call shape
  const rank = view => {
    if (effectiveDue(view)) return 0;
    if (needsDecision(view)) return 1;
    if (view.isCandidate) return 2;
    return 3;
  };
  const updated = view => {
    const date = toDate(view.updatedAt);
    return date ? date.getTime() : 0;
  };
  return (items || [])
    .map((view, index) => ({ view, index }))
    .sort((a, b) => {
      const ra = rank(a.view);
      const rb = rank(b.view);
      if (ra !== rb) return ra - rb;
      if (ra === 0) {
        const diff = effectiveDue(a.view).getTime() - effectiveDue(b.view).getTime();
        if (diff !== 0) return diff;
      }
      const recency = updated(b.view) - updated(a.view);
      return recency !== 0 ? recency : a.index - b.index;
    })
    .map(entry => entry.view);
}

// groupFollowUps groups cards by where they came from (FR9). Only groups with
// items are returned, the largest first; ties keep FOLLOW_UP_GROUPS order.
export function groupFollowUps(items, now = new Date()) {
  const buckets = new Map(FOLLOW_UP_GROUPS.map(group => [group.key, []]));
  (items || []).forEach(view => {
    if (view) buckets.get(groupKeyOf(view)).push(view);
  });
  return FOLLOW_UP_GROUPS.map((group, order) => ({
    key: group.key,
    label: group.label,
    order,
    items: orderWithinGroup(buckets.get(group.key), now)
  }))
    .filter(group => group.items.length > 0)
    .sort((a, b) => b.items.length - a.items.length || a.order - b.order)
    .map(({ key, label, items: grouped }) => ({ key, label, items: grouped }));
}

// dueChip is a card's due chip (FR13), or null when it has no date. The
// attention tone marks overdue and due-today items.
export function dueChip(view, now = new Date()) {
  if (!view) return null;
  if (isSnoozed(view)) {
    return { text: `Snoozed until ${shortDate(toDate(view.snoozedUntil))}`, tone: 'neutral' };
  }
  const due = toDate(view.dueAt);
  if (!due) return null;
  const offset = dayOffset(due, now);
  if (offset < 0) return { text: `Overdue · ${shortDate(due)}`, tone: 'attention' };
  if (offset === 0) return { text: 'Due today', tone: 'attention' };
  return { text: `Due ${shortDate(due)}`, tone: 'neutral' };
}

// safeNoteLink returns the link only when it opens a note in Obsidian (FR17).
export function safeNoteLink(link) {
  const value = String(link || '').trim();
  return value.startsWith(OBSIDIAN_OPEN_PREFIX) ? value : '';
}

// sourceLine is where a card came from (FR14, FR15): `name` (linked when
// `href` is set) followed by ` · label`, or null when there is nothing to say.
// It reads only the server's presentation; the detail text is never parsed.
export function sourceLine(view) {
  if (!view) return null;
  const presentation = view.presentation || {};
  switch (groupKeyOf(view)) {
    case 'note_item': {
      const name = presentation.sourceName || '';
      const label = presentation.sourceLabel || 'Notes';
      return { name, label, href: name ? safeNoteLink(presentation.link) : '' };
    }
    case 'email_thread': {
      const name = presentation.sourceName || view.counterparty || '';
      return { name, label: 'Email Ops', href: '' };
    }
    case 'journal':
      return { name: '', label: 'Journal', href: '' };
    default:
      return null;
  }
}

// sourceLineText is the source line as plain text.
export function sourceLineText(line) {
  if (!line) return '';
  return [line.name, line.label].filter(Boolean).join(' · ');
}

// defaultOpen reports whether the list starts open (FR7): something is
// overdue, due within a week, needs a decision, or awaits confirmation.
export function defaultOpen(items, now = new Date()) {
  return (items || []).some(view => {
    if (!view || !isOpen(view)) return false;
    if (needsDecision(view) || view.isCandidate) return true;
    const offset = dueOffset(view, now);
    return offset !== null && offset <= 6;
  });
}

// --- collapse memory (FR8) ------------------------------------------------

export function followUpsOpenKey(workspaceId) {
  return `ori:followups-open:${workspaceId}`;
}

// readFollowUpsOpen returns the remembered choice (true/false), or null when
// there is none or storage refuses.
export function readFollowUpsOpen(storage, workspaceId) {
  if (!storage || !workspaceId) return null;
  try {
    const value = storage.getItem(followUpsOpenKey(workspaceId));
    if (value === 'true') return true;
    if (value === 'false') return false;
  } catch (_) {
    // A browser that refuses storage simply forgets.
  }
  return null;
}

export function writeFollowUpsOpen(storage, workspaceId, open) {
  if (!storage || !workspaceId) return;
  try {
    storage.setItem(followUpsOpenKey(workspaceId), open ? 'true' : 'false');
  } catch (_) {
    // Ignored: the choice lasts for this page only.
  }
}

function defaultStorage() {
  try {
    return typeof window !== 'undefined' ? window.localStorage : null;
  } catch (_) {
    return null;
  }
}

// --- rendering ------------------------------------------------------------

function el(doc, tag, className, text) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function renderSourceLine(doc, line) {
  const p = el(doc, 'p', 'workspace-followup-source');
  if (line.name) {
    if (line.href) {
      const a = el(doc, 'a', 'workspace-followup-source-link', line.name);
      a.href = line.href;
      p.appendChild(a);
    } else {
      p.appendChild(el(doc, 'span', 'workspace-followup-source-name', line.name));
    }
  }
  if (line.label) {
    p.appendChild(
      el(
        doc,
        'span',
        'workspace-followup-source-label',
        line.name ? ` · ${line.label}` : line.label
      )
    );
  }
  return p;
}

// renderManagementCard builds a single follow-up card with its mutation
// buttons. `act(kind, view)` is invoked on click; kept injectable for tests.
export function renderManagementCard(doc, view, act, now = new Date()) {
  const card = el(doc, 'article', 'hq-followup-card workspace-followup-card');
  card.dataset.followupId = view.id;

  const meta = el(doc, 'div', 'workspace-followup-meta');
  meta.appendChild(
    el(
      doc,
      'span',
      'hq-followup-category',
      view.counterparty ? `${view.category}: ${view.counterparty}` : view.category
    )
  );
  const chip = dueChip(view, now);
  if (chip) {
    const due = el(doc, 'span', `workspace-followup-due is-${chip.tone}`, chip.text);
    due.dataset.tone = chip.tone;
    meta.appendChild(due);
  }
  card.appendChild(meta);

  card.appendChild(el(doc, 'p', 'hq-followup-title', view.title));

  const line = sourceLine(view);
  if (line && (line.name || line.label)) card.appendChild(renderSourceLine(doc, line));

  // A note follow-up's detail is its path and link, shown above as the source
  // line. Other sources keep their own words.
  if (view.detail && groupKeyOf(view) !== 'note_item') {
    card.appendChild(el(doc, 'p', 'hq-followup-detail', view.detail));
  }

  if (view.relatedTicket) card.appendChild(renderRelatedTicket(doc, view.relatedTicket));

  const actions = el(doc, 'div', 'hq-followup-actions');
  const button = (text, primary, kind) => {
    const b = el(
      doc,
      'button',
      `modern-btn ${primary ? 'modern-btn-primary' : 'modern-btn-secondary'} modern-btn-sm`,
      text
    );
    b.type = 'button';
    b.addEventListener('click', () => act(kind, view, { card, trigger: b }));
    return b;
  };

  if (view.isCandidate) {
    actions.append(
      button('Track this', true, 'confirm'),
      button('Not a follow-up', false, 'dismiss')
    );
  } else {
    actions.append(button('Done', true, 'complete'), button('Snooze 1 day', false, 'snooze'));
    if (!view.relatedTicket) actions.append(button('Make a ticket', false, 'ticket'));
  }
  card.appendChild(actions);
  return card;
}

// focusManagementCard marks and focuses one exact, already-rendered owner-local
// card. It never chooses a fallback card and performs no follow-up mutation.
export function focusManagementCard(card, title = '') {
  if (!card) return false;
  if (!/\bis-deep-linked\b/.test(card.className)) {
    card.className = `${card.className} is-deep-linked`.trim();
  }
  card.tabIndex = -1;
  if (typeof card.setAttribute === 'function') {
    card.setAttribute('aria-label', `Selected follow-up: ${String(title || '').trim()}`);
  }
  if (typeof card.scrollIntoView === 'function') card.scrollIntoView({ block: 'center' });
  if (typeof card.focus === 'function') card.focus({ preventScroll: true });
  return true;
}

// renderManagementPanel populates the mount with the panel, or hides it when
// there is nothing to show (FR5). The list is collapsed unless `options.open`
// says otherwise; a deep-linked card always opens it. Toggling and filtering
// change the rendered DOM in place, so keyboard focus stays where it was.
// It returns the deep-linked card, if any.
export function renderManagementPanel(doc, mount, view, act, focusID = '', options = {}) {
  if (!mount) return null;
  if (!view.show) {
    mount.hidden = true;
    mount.innerHTML = '';
    return null;
  }
  const now = options.now || new Date();
  const groups = groupFollowUps(view.items, now);
  const focused = focusID ? view.items.find(item => item.id === focusID) : null;
  let open = focused ? true : Boolean(options.open);
  let filter =
    !focused && options.filter && groups.some(group => group.key === options.filter)
      ? options.filter
      : 'all';

  mount.innerHTML = '';
  const section = el(doc, 'section', 'workspace-followups');
  section.setAttribute('aria-labelledby', 'workspaceFollowupsTitle');

  const head = el(doc, 'header', 'workspace-followups-head');
  const heading = el(doc, 'div', 'workspace-followups-heading');
  const title = el(doc, 'h2', 'workspace-followups-title', 'Follow-ups');
  title.id = 'workspaceFollowupsTitle';
  heading.appendChild(title);
  const summaryText = followUpSummaryText(followUpSummary(view.items, now));
  if (summaryText) heading.appendChild(el(doc, 'p', 'workspace-followups-summary', summaryText));
  head.appendChild(heading);

  const toggle = el(
    doc,
    'button',
    'workspace-followups-toggle modern-btn modern-btn-secondary modern-btn-sm'
  );
  toggle.type = 'button';
  toggle.setAttribute('aria-controls', 'workspaceFollowupsBody');
  head.appendChild(toggle);

  const chips = el(doc, 'div', 'workspace-followups-filters');
  chips.setAttribute('role', 'group');
  chips.setAttribute('aria-label', 'Show follow-ups from');
  const chipButtons = [];
  const addChip = (key, label, count) => {
    const chip = el(doc, 'button', 'workspace-followups-chip', `${label} ${count}`);
    chip.type = 'button';
    chip.dataset.filter = key;
    chip.addEventListener('click', () => {
      filter = key;
      if (!open) setOpen(true, true);
      applyFilter();
      if (typeof options.onFilter === 'function') options.onFilter(key);
    });
    chipButtons.push(chip);
    chips.appendChild(chip);
  };
  addChip('all', 'All', view.items.length);
  groups.forEach(group => addChip(group.key, group.label, group.items.length));
  head.appendChild(chips);
  section.appendChild(head);

  const body = el(doc, 'div', 'workspace-followups-body');
  body.id = 'workspaceFollowupsBody';
  const groupSections = [];
  let focusedCard = null;
  groups.forEach(group => {
    const groupSection = el(doc, 'section', 'workspace-followups-group');
    groupSection.dataset.group = group.key;
    const h3 = el(doc, 'h3', 'workspace-followups-group-title', group.label);
    h3.appendChild(el(doc, 'span', 'workspace-followups-group-count', ` ${group.items.length}`));
    groupSection.appendChild(h3);
    const list = el(doc, 'div', 'workspace-followup-list');
    group.items.forEach(item => {
      const card = renderManagementCard(doc, item, act, now);
      list.appendChild(card);
      if (focused && item.id === focused.id) focusedCard = card;
    });
    groupSection.appendChild(list);
    groupSections.push(groupSection);
    body.appendChild(groupSection);
  });
  section.appendChild(body);

  function setOpen(next, remember) {
    open = next;
    body.hidden = !open;
    toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
    toggle.textContent = open ? 'Hide follow-ups' : 'Show follow-ups';
    section.dataset.open = open ? 'true' : 'false';
    if (remember && typeof options.onToggle === 'function') options.onToggle(open);
  }
  function applyFilter() {
    chipButtons.forEach(chip => {
      chip.setAttribute('aria-pressed', chip.dataset.filter === filter ? 'true' : 'false');
    });
    groupSections.forEach(groupSection => {
      groupSection.hidden = filter !== 'all' && groupSection.dataset.group !== filter;
    });
  }
  toggle.addEventListener('click', () => setOpen(!open, true));
  setOpen(open, false);
  applyFilter();

  mount.appendChild(section);
  mount.hidden = false;
  if (focusedCard) focusManagementCard(focusedCard, focused.title);
  return focusedCard;
}

// isRendered reports whether the node has a box on screen, i.e. it is not
// inside a hidden host.
function isRendered(node) {
  return typeof node.getClientRects === 'function' && node.getClientRects().length > 0;
}

// wireWorkspaceFollowUps fetches the workspace's follow-ups and renders the
// management panel, re-fetching after each mutation. Dependencies are injected
// for testability. It returns a controller whose refocus() scrolls a
// deep-linked card into view once the mount has a visible place: the command
// view parks the mount in a hidden host and moves it into its layout later.
export async function wireWorkspaceFollowUps({
  doc,
  workspaceId,
  mount,
  fetchImpl,
  postImpl,
  toast,
  locationSearch = '',
  storage = defaultStorage(),
  now = () => new Date()
}) {
  const controller = { refocus: () => false, reload: async () => {} };
  if (!mount || !workspaceId) return controller;
  const focusID = followUpIDFromSearch(locationSearch);
  const state = { filter: 'all', open: null };
  let focusedCard = null;
  let focusedTitle = '';
  let deepLinkConsumed = false;
  // True while a deep-linked card was focused out of sight. refocus() does the
  // focus once, so later re-renders of the command view never scroll the page.
  let refocusPending = false;
  controller.refocus = () => {
    if (!refocusPending) return false;
    refocusPending = false;
    return focusManagementCard(focusedCard, focusedTitle);
  };

  const load = async () => {
    let items = [];
    try {
      const res = await fetchImpl(`/api/workspaces/${encodeURIComponent(workspaceId)}/followups`, {
        headers: { Accept: 'application/json' }
      });
      if (!res || !res.ok) return;
      const data = await res.json();
      items = Array.isArray(data.followups) ? data.followups : [];
    } catch (_) {
      return; // leave the panel as-is on a transient failure
    }
    const act = async (kind, view, place) => {
      if (kind === 'ticket') {
        await startMakeTicket(doc, place.card, place.trigger, view.id, {
          fetchImpl,
          onChanged: load,
          root: mount
        });
        return;
      }
      const action = followUpActionFor(kind);
      if (!action) return;
      const body = { id: view.id };
      if (kind === 'snooze') {
        body.until = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
      }
      try {
        await postImpl(action.url, body);
        await load();
      } catch (_) {
        if (toast) toast(`Could not ${action.label} the follow-up.`, 'Error', 'danger');
      }
    };
    const current = now();
    const view = managementView(items);
    if (state.open === null) {
      const remembered = readFollowUpsOpen(storage, workspaceId);
      state.open = remembered === null ? defaultOpen(view.items, current) : remembered;
    }
    // The deep link focuses its card on the first render only; later
    // re-renders after a mutation leave focus where the user put it.
    const linkID = deepLinkConsumed ? '' : focusID;
    if (view.show) deepLinkConsumed = true;
    const card = renderManagementPanel(doc, mount, view, act, linkID, {
      now: current,
      open: state.open,
      filter: state.filter,
      onToggle: open => {
        state.open = open;
        writeFollowUpsOpen(storage, workspaceId, open);
      },
      onFilter: key => {
        state.filter = key;
      }
    });
    if (card) {
      focusedCard = card;
      focusedTitle = view.items.find(item => item.id === focusID)?.title || '';
      state.open = true;
      refocusPending = !isRendered(mount);
    }
    // The Personal HQ header counts follow-ups without a second request: the
    // count rides on the mount, and the change is announced (PRD 766 FR7).
    if (mount.dataset) mount.dataset.followupCount = String(view.count);
    if (typeof doc.dispatchEvent === 'function' && typeof CustomEvent === 'function') {
      doc.dispatchEvent(
        new CustomEvent('ori:followups-changed', { detail: { count: view.count } })
      );
    }
  };
  // Another surface made follow-ups (the HQ's Notes panel adding its open
  // items): read them again in place, keeping the panel's open state, filter
  // and focus (PRD 766, demo 3.9).
  controller.reload = load;
  await load();
  return controller;
}

// Auto-wire on the workspace detail page.
(function () {
  if (typeof document === 'undefined') return;
  const mount = document.getElementById('workspaceFollowupMount');
  if (!mount) return;
  const workspaceId =
    (typeof window !== 'undefined' && window.currentWorkspaceId) ||
    document.body?.dataset?.workspaceId ||
    '';
  if (!workspaceId) return;

  const postJSON = async (url, body) => {
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(body || {})
    });
    if (!res.ok) throw new Error(`${url} ${res.status}`);
    return res.json();
  };
  const toast = (msg, title, kind) => {
    if (typeof window !== 'undefined' && typeof window.showToast === 'function') {
      window.showToast(msg, title, kind);
    }
  };

  const ready = wireWorkspaceFollowUps({
    doc: document,
    workspaceId: String(workspaceId).trim(),
    mount,
    fetchImpl: (u, o) => fetch(u, o),
    postImpl: postJSON,
    toast,
    locationSearch: typeof window !== 'undefined' ? window.location.search : ''
  });
  if (typeof window !== 'undefined') {
    // The command view calls refocus() after it moves the mount, so a
    // deep-linked card is scrolled into view in its new place (FR4).
    window.WorkspaceFollowUps = {
      ready,
      refocus: () => ready.then(controller => controller.refocus()),
      reload: () => ready.then(controller => controller.reload())
    };
  }
})();
