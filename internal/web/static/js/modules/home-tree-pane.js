// home-tree-pane.js — the tabs and the reading pane beside the Home file tree.
//
// PRD: tasks/prd-home-file-tree.md, sections 4.D ("Opening items and tabs") and
// 4.E ("What the pane shows").
//
// Whatever is clicked in the tree opens here as a tab, without leaving Home.
// This module owns three things:
//
//   1. tab state — plain functions over a list of tabs (open, activate, close);
//   2. the view model of a tab — what the pane says about a note, a ticket, a
//      file, a workspace… worked out from data, with no markup involved;
//   3. drawing the tab strip and the pane, and reporting clicks.
//
// Like the tree, it changes nothing itself: the cockpit holds the tabs and the
// loaded items in its own state and passes them in. Clicks go back through
// callbacks. That keeps one authority for what is open and what is selected.
//
// The pure parts are exported for home-tree-pane.test.js and run without a DOM.

import {
  escapeHtml,
  formatCount,
  groupRailView,
  isGroupWorkspace,
  workspaceRailView,
  workspaceSignals
} from './home-workspace-cockpit.js';
import {
  PREVIEW_IMAGE,
  PREVIEW_MARKDOWN,
  PREVIEW_TEXT,
  SECTIONS,
  SECTION_READY,
  itemKey,
  sectionInfo,
  sectionOfKind
} from './home-tree-sources.js';
import { iconHTML, rowIconName } from './home-tree-icons.js';
import { renderMarkdown } from './note-editor.js';
import { workspaceNotePath } from './note-routes.js';
import { workspacePageURL } from './workspace-routes.js';

// ---------------------------------------------------------------------------
// Tab state (FR25, FR26)
// ---------------------------------------------------------------------------

/**
 * The tab for a tree row.
 *
 * `key` is the row's own id, so "is this already open?" is one comparison and
 * the open tab's row can be highlighted without a lookup table.
 */
export function tabFromRow(row) {
  return {
    key: String(row.id),
    kind: String(row.kind),
    workspaceId: String(row.workspaceId || row.id),
    label: String(row.name || row.label || ''),
    meta: row.meta ? { ...row.meta } : {}
  };
}

/**
 * Open a tab, or switch to it when the item is already open (FR25).
 *
 * Returns new `{ tabs, activeKey }`; the input list is never changed. An item
 * that is already open keeps its place in the strip but takes the newer label,
 * so a renamed note does not keep its old name on the tab.
 */
export function openTab(tabs, activeKey, tab) {
  const list = Array.isArray(tabs) ? tabs : [];
  if (!tab || !tab.key) return { tabs: list, activeKey: activeKey || '' };
  const at = list.findIndex(entry => entry.key === tab.key);
  if (at >= 0) {
    const next = list.slice();
    next[at] = { ...list[at], ...tab, meta: { ...list[at].meta, ...tab.meta } };
    return { tabs: next, activeKey: tab.key };
  }
  return { tabs: [...list, tab], activeKey: tab.key };
}

/** Switch to a tab that is open; an unknown key changes nothing. */
export function activateTab(tabs, activeKey, key) {
  const list = Array.isArray(tabs) ? tabs : [];
  return { tabs: list, activeKey: list.some(tab => tab.key === key) ? key : activeKey || '' };
}

/**
 * Close a tab (FR26).
 *
 * Closing the active tab activates the one to its right, or to its left when
 * there is none; closing the last tab leaves nothing active. Closing a tab that
 * is not active leaves the active tab alone.
 */
export function closeTab(tabs, activeKey, key) {
  const list = Array.isArray(tabs) ? tabs : [];
  const at = list.findIndex(tab => tab.key === key);
  if (at < 0) return { tabs: list, activeKey: activeKey || '' };
  const next = list.filter(tab => tab.key !== key);
  if (activeKey !== key) return { tabs: next, activeKey: activeKey || '' };
  const neighbour = next[at] || next[at - 1] || null;
  return { tabs: next, activeKey: neighbour ? neighbour.key : '' };
}

// ---------------------------------------------------------------------------
// View models (FR31)
// ---------------------------------------------------------------------------

function findWorkspace(flattened, id) {
  return (Array.isArray(flattened) ? flattened : []).find(ws => ws && ws.id === id) || null;
}

// The names of every group above a workspace, outermost first.
function ancestorNames(flattened, workspace) {
  const names = [];
  let current = workspace;
  const seen = new Set();
  while (current && current.parent_id && !seen.has(current.parent_id)) {
    seen.add(current.parent_id);
    current = findWorkspace(flattened, current.parent_id);
    if (current) names.unshift(String(current.name || 'Group'));
  }
  return names;
}

function workspaceName(workspace) {
  if (!workspace) return 'a workspace that no longer exists';
  return String(workspace.name || (isGroupWorkspace(workspace) ? 'Group' : 'Untitled workspace'));
}

const KIND_NOUNS = {
  note: 'Note',
  ticket: 'Ticket',
  file: 'File',
  agent: 'Agent'
};

/**
 * The line under the title that says what the item is and where (FR31),
 * for example "Note in My HQ".
 */
export function paneSubline(tab, workspace, parent) {
  const where = workspaceName(workspace);
  if (tab.kind === 'memory') return `Memory for ${where}`;
  if (tab.kind === 'group') return parent ? `Group in ${workspaceName(parent)}` : 'Group';
  if (tab.kind === 'workspace') {
    if (
      workspace &&
      (workspace.is_personal_hq === true || workspace.designation === 'personal_hq')
    ) {
      return 'Personal HQ';
    }
    return parent ? `Workspace in ${workspaceName(parent)}` : 'Workspace';
  }
  return `${KIND_NOUNS[tab.kind] || 'Item'} in ${where}`;
}

/**
 * The breadcrumb, outermost first: groups, the workspace, the section, any
 * folders, then the item (FR31). A workspace or group tab stops at itself.
 */
export function paneCrumbs(tab, flattened) {
  const workspace = findWorkspace(flattened, tab.workspaceId);
  const crumbs = [...ancestorNames(flattened, workspace), workspaceName(workspace)];
  if (tab.kind === 'workspace' || tab.kind === 'group') return crumbs;
  const section = sectionInfo(sectionOfKind(tab.kind));
  if (section) crumbs.push(section.label);
  if (tab.kind === 'memory') return crumbs;
  if (tab.kind === 'file') {
    const folders = String((tab.meta && tab.meta.path) || '')
      .split('/')
      .filter(Boolean)
      .slice(0, -1);
    crumbs.push(...folders);
  }
  crumbs.push(tab.label);
  return crumbs;
}

// What a tab's item is called on the tab and in the title. A workspace or
// group follows its current name, so a rename shows without reopening.
function tabTitle(tab, workspace) {
  if (tab.kind === 'workspace' || tab.kind === 'group') return workspaceName(workspace);
  if (tab.kind === 'memory') return 'Memory';
  return tab.label || 'Untitled';
}

export const ITEM_LOADING = 'loading';
export const ITEM_READY = 'ready';
export const ITEM_FAILED = 'failed';

/**
 * Everything the pane shows for one tab, as plain data.
 *
 * `item` is what the cockpit has loaded for the tab so far:
 * `{ status, value, error }`. A tab with nothing to load (it is described
 * entirely by its row) is ready from the start.
 */
export function paneView(tab, context = {}) {
  const { flattened = [], item = null } = context;
  const workspace = findWorkspace(flattened, tab.workspaceId);
  const parent =
    workspace && workspace.parent_id ? findWorkspace(flattened, workspace.parent_id) : null;
  const loaded = item || { status: ITEM_READY, value: null, error: '' };
  // One shape for every kind of item; each kind fills the parts it has and the
  // renderer skips the rest.
  const view = {
    key: tab.key,
    kind: tab.kind,
    crumbs: paneCrumbs(tab, flattened),
    title: tabTitle(tab, workspace),
    sub: paneSubline(tab, workspace, parent),
    status: loaded.status,
    error: loaded.error || '',
    chip: null, // { label, tone }
    tags: [],
    actions: [], // { label, href } to go somewhere, { label, action } to do something
    lead: '',
    notice: '',
    stats: [], // { value, label }
    fields: [], // { label, value }
    body: null, // { type: 'markdown' | 'text' | 'image' | 'none', … }
    list: null, // { items: [{ text, meta }], empty }
    linkGroups: [] // { label, links: [{ label, icon, meta, action, target }] }
  };
  const fill = VIEW_FILLERS[tab.kind];
  if (fill) {
    fill(view, {
      tab,
      workspace,
      flattened,
      value: loaded.status === ITEM_READY ? loaded.value : null,
      context
    });
  }
  return view;
}

function slugOf(workspace) {
  return String((workspace && workspace.folder_slug) || '').trim();
}

// The last breadcrumb is the item's own name; keep it in step with the title.
function retitle(view, title) {
  if (!title) return;
  view.title = title;
  view.crumbs = [...view.crumbs.slice(0, -1), title];
}

const TICKET_TONES = {
  backlog: 'mute',
  ready: 'info',
  in_progress: 'ok',
  review: 'amber',
  done: 'mute',
  cancelled: 'mute'
};

const STATUS_TONES = {
  attention: 'warn',
  running: 'ok',
  active: 'ok',
  idle: 'mute',
  unknown: 'mute'
};

/**
 * When a workspace next has scheduled work, in words (FR39).
 *
 * `scheduleIndex` is Home's own schedule join (`null` until it has loaded, or
 * when it failed). Unknown reads "—"; known and empty reads "No schedule".
 */
export function nextRunLabel(workspaceId, scheduleIndex, locale) {
  if (!scheduleIndex) return '—';
  const times = (scheduleIndex[workspaceId] || [])
    .map(row => new Date(String((row && (row.next_run || row.next_run_at)) || '')))
    .filter(at => !Number.isNaN(at.getTime()))
    .sort((a, b) => a - b);
  if (times.length === 0) return 'No schedule';
  return times[0].toLocaleString(locale, { dateStyle: 'medium', timeStyle: 'short' });
}

/**
 * The sections of a workspace or group as overview links, with counts.
 *
 * A count that has not loaded reads "—", never "0". A group lists only the
 * sections that hold something, as its tree does (FR16, FR39, FR40).
 */
export function sectionLinks(workspaceId, sections, { hideEmpty = false } = {}) {
  const links = [];
  SECTIONS.forEach(info => {
    const state = (sections && sections[info.id]) || null;
    const ready = !!state && state.status === SECTION_READY;
    if (hideEmpty && !(ready && state.count > 0)) return;
    links.push({
      label: info.label,
      icon: rowIconName({ kind: 'section', section: info.id }),
      meta: formatCount(ready ? state.count : null),
      // Memory is one item, so its link opens it; the others reveal the
      // section in the tree.
      action: info.expandable ? 'reveal-section' : 'open-item',
      target: info.expandable ? info.id : itemKey(workspaceId, info.id)
    });
  });
  return links;
}

// A workspace's or group's tags, in its overview. Unlike a note's or a
// ticket's, these can be used: each one filters the tree, as its chip in the
// tag bar does, and can be removed (FR63).
function fillOverviewTags(view, tab, context) {
  view.tags = (context.tagsById && context.tagsById[tab.workspaceId]) || [];
  view.tagsEditable = true;
  const active = context.activeTags instanceof Set ? context.activeTags : new Set();
  view.activeTags = view.tags.filter(tag => active.has(tag));
}

const VIEW_FILLERS = {
  // FR32, FR41. The body is a slot for the existing note editor, which the
  // cockpit mounts into it; the note's text never goes through this view, so
  // redrawing the pane does not disturb what is being typed. Tags are shown
  // read-only, and everything else the note page offers is one click away.
  note(view, { tab, workspace, value }) {
    const slug = slugOf(workspace);
    if (slug) {
      view.actions.push({
        label: 'Open full note',
        href: workspaceNotePath(slug, tab.meta.noteId),
        primary: true
      });
    }
    if (!value) return;
    retitle(view, value.name);
    view.tags = Array.isArray(value.tags) ? value.tags : [];
    view.body = { type: 'editor', noteId: String(tab.meta.noteId || '') };
  },

  // FR33, read-only.
  ticket(view, { tab, workspace, value }) {
    const slug = slugOf(workspace);
    if (slug) {
      view.actions.push({
        label: 'Open in Tickets',
        href: workspacePageURL(slug, [], {
          search: new URLSearchParams({ ticket: tab.meta.ticketId })
        }),
        primary: true
      });
    }
    // The row already knows the state, so the chip shows before the ticket loads.
    const state = value ? value.state : tab.meta.state;
    const label = value ? value.stateLabel : tab.meta.stateLabel;
    if (label) view.chip = { label, tone: TICKET_TONES[state] || 'mute' };
    if (!value) return;
    retitle(view, value.title);
    view.tags = value.tags;
    if (value.description) view.lead = value.description;
    else view.notice = 'This ticket has no description.';
    view.fields = [
      { label: 'Source', value: value.sourceLabel },
      { label: 'Workspace', value: workspaceName(workspace) }
    ];
    if (value.number) view.fields.unshift({ label: 'Number', value: value.number });
  },

  // FR34, read-only: the path inside the workspace and a preview.
  file(view, { tab, value }) {
    const path = String(tab.meta.path || '');
    view.actions = [
      { label: 'Open', action: 'file-open', primary: true },
      { label: 'Reveal in Finder', action: 'file-reveal' }
    ];
    view.fields = [{ label: 'Path', value: `files/${path}` }];
    if (path === 'BACKLOG.md') view.lead = 'Ori keeps this file in step with the backlog.';
    if (!value) return;
    if (value.tooLarge) {
      view.body = { type: 'none', text: 'This file is too large to preview here.' };
    } else if (value.kind === PREVIEW_MARKDOWN) {
      view.body = { type: 'markdown', markdown: value.text, empty: 'This file is empty.' };
    } else if (value.kind === PREVIEW_TEXT) {
      view.body = { type: 'text', text: value.text, empty: 'This file is empty.' };
    } else if (value.kind === PREVIEW_IMAGE) {
      view.body = { type: 'image', src: value.url, alt: tab.label };
    } else {
      view.body = { type: 'none', text: 'No preview' };
    }
  },

  // FR37, read-only: the entries as a list.
  memory(view, { workspace, value }) {
    const slug = slugOf(workspace);
    if (slug) {
      view.actions.push({
        label: 'Open Memory',
        href: workspacePageURL(slug, [], { hash: 'memory' }),
        primary: true
      });
    }
    view.lead = isGroupWorkspace(workspace)
      ? 'What the agents in this group remember.'
      : 'What the agents in this workspace remember.';
    if (!value) return;
    view.list = {
      items: value.entries.map(entry => ({
        text: entry.text,
        meta: [entry.type, entry.date].filter(Boolean).join(' · ')
      })),
      empty: 'No memory entries yet.'
    };
  },

  // FR38, read-only. Everything it shows came with the tree row.
  agent(view, { tab, workspace }) {
    const slug = slugOf(workspace);
    if (slug) {
      view.actions.push({
        label: 'Open agent',
        href: workspacePageURL(slug, ['agents', tab.meta.name || tab.label]),
        primary: true
      });
    }
    view.fields = [
      { label: 'Role', value: tab.meta.role || '—' },
      { label: 'Model', value: tab.meta.model || '—' }
    ];
  },

  // FR39. The facts are the Home rail's own (workspaceRailView), so the two
  // can never disagree; only the layout is the pane's.
  workspace(view, { tab, workspace, context }) {
    if (!workspace) return;
    const rail = workspaceRailView(workspace);
    view.chip = { label: rail.status.label, tone: STATUS_TONES[rail.status.status] || 'mute' };
    fillOverviewTags(view, tab, context);
    if (rail.openHref)
      view.actions.push({ label: 'Open workspace', href: rail.openHref, primary: true });
    view.actions.push(
      { label: 'Move…', action: 'move' },
      { label: 'Delete', action: 'delete', danger: true }
    );
    view.lead = rail.mission;
    view.stats = [
      { value: formatCount(rail.status.agents), label: 'Agents' },
      { value: formatCount(rail.status.openTasks), label: 'Open tasks' },
      { value: formatCount(rail.status.attention), label: 'Need attention' }
    ];
    view.fields = [
      { label: 'Next run', value: nextRunLabel(tab.workspaceId, context.scheduleIndex) }
    ];
    view.linkGroups = [
      { label: 'In this workspace', links: sectionLinks(tab.workspaceId, context.sections) }
    ];
  },

  // FR40. The facts are groupRailView's.
  group(view, { tab, workspace, flattened, context }) {
    if (!workspace) return;
    const rail = groupRailView(workspace, flattened, { view: 'tree' });
    fillOverviewTags(view, tab, context);
    if (rail.openHref)
      view.actions.push({ label: 'Open group', href: rail.openHref, primary: true });
    view.actions.push(
      { label: 'Move…', action: 'move' },
      { label: 'Delete', action: 'delete', danger: true }
    );
    view.lead = rail.description;
    view.stats = [
      { value: formatCount(rail.aggregates.descendantWorkspaces), label: 'Workspaces' },
      { value: formatCount(rail.aggregates.openTasks.total), label: 'Open tasks' },
      { value: formatCount(rail.aggregates.attention.total), label: 'Need attention' }
    ];
    const children = flattened
      .filter(ws => ws && ws.parent_id === tab.workspaceId)
      .map(child => ({
        label: workspaceName(child),
        icon: isGroupWorkspace(child) ? 'group' : 'workspace',
        meta: isGroupWorkspace(child) ? 'Group' : workspaceSignals(child).label,
        action: 'open-workspace',
        target: child.id
      }));
    view.linkGroups = [
      { label: 'Workspaces in this group', links: children, empty: 'This group is empty.' }
    ];
    const own = sectionLinks(tab.workspaceId, context.sections, { hideEmpty: true });
    if (own.length) view.linkGroups.push({ label: "The group's own contents", links: own });
  }
};

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

/** The tab strip (FR25-FR27). Returns '' when nothing is open. */
export function renderTabStripHTML(tabs, activeKey, flattened = []) {
  return (Array.isArray(tabs) ? tabs : [])
    .map(tab => {
      const active = tab.key === activeKey;
      const title = escapeHtml(tabTitle(tab, findWorkspace(flattened, tab.workspaceId)));
      const key = escapeHtml(tab.key);
      return (
        `<div class="cockpit-pane-tab${active ? ' is-active' : ''}" role="presentation">` +
        `<button type="button" class="cockpit-pane-tab-label" role="tab" data-pane-tab="${key}" ` +
        `aria-selected="${active ? 'true' : 'false'}" aria-controls="cockpitPanePanel" ` +
        `tabindex="${active ? '0' : '-1'}" title="${title}">${title}</button>` +
        `<button type="button" class="cockpit-pane-tab-close" data-pane-close="${key}" ` +
        `aria-label="Close ${title}" tabindex="${active ? '0' : '-1'}">${iconHTML('close', { size: 12 })}</button>` +
        '</div>'
      );
    })
    .join('');
}

export function renderEmptyPaneHTML() {
  return (
    '<div class="cockpit-pane-empty">' +
    '<h3 class="cockpit-pane-empty-title">Nothing open</h3>' +
    '<p>Pick a note, ticket or file from the tree and it opens here.</p>' +
    '</div>'
  );
}

function actionHTML(action) {
  const tone = action.primary
    ? 'modern-btn-primary'
    : action.danger
      ? 'modern-btn-danger'
      : 'modern-btn-secondary';
  const classes = `modern-btn ${tone} modern-btn-sm`;
  if (action.href) {
    return `<a class="${classes}" href="${escapeHtml(action.href)}">${escapeHtml(action.label)}</a>`;
  }
  return (
    `<button type="button" class="${classes}" data-pane-action="${escapeHtml(action.action)}">` +
    `${escapeHtml(action.label)}</button>`
  );
}

/** The id the note editor draws into. One note is mounted at a time (FR41). */
export const NOTE_EDITOR_ID = 'cockpitPaneNoteEditor';

function previewHTML(body) {
  if (!body) return '';
  if (body.type === 'editor') {
    // The save line sits above the editor; both are filled in after drawing
    // (see createNoteController), never by a redraw of the pane.
    return (
      '<div class="cockpit-pane-savebar">' +
      '<span class="cockpit-pane-save-status" data-pane-save-status role="status" aria-live="polite"></span>' +
      '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-pane-save-retry hidden>Retry</button>' +
      '</div>' +
      '<p class="cockpit-pane-note" data-pane-note-elsewhere hidden>' +
      'This note is also open in another browser tab. Whichever is saved last is kept.</p>' +
      `<div id="${NOTE_EDITOR_ID}" class="note-preview-content note-live-editor cockpit-pane-editor" ` +
      `data-pane-editor="${escapeHtml(body.noteId)}" role="textbox" aria-multiline="true" ` +
      'aria-label="Note text. Click a line to edit it." tabindex="0"></div>'
    );
  }
  if (body.type === 'markdown') {
    return body.markdown.trim()
      ? `<div class="cockpit-pane-markdown">${renderMarkdown(body.markdown)}</div>`
      : `<p class="cockpit-pane-note">${escapeHtml(body.empty || 'Nothing here yet.')}</p>`;
  }
  if (body.type === 'text') {
    return body.text
      ? `<pre class="cockpit-pane-pre">${escapeHtml(body.text)}</pre>`
      : `<p class="cockpit-pane-note">${escapeHtml(body.empty || 'Nothing here yet.')}</p>`;
  }
  if (body.type === 'image') {
    return (
      `<img class="cockpit-pane-image" src="${escapeHtml(body.src)}" ` +
      `alt="${escapeHtml(body.alt || '')}">`
    );
  }
  return `<p class="cockpit-pane-note">${escapeHtml(body.text || 'No preview')}</p>`;
}

function listHTML(list) {
  if (!list) return '';
  if (list.items.length === 0) {
    return `<p class="cockpit-pane-note">${escapeHtml(list.empty || 'Nothing here yet.')}</p>`;
  }
  return (
    '<ul class="cockpit-pane-list">' +
    list.items
      .map(
        entry =>
          `<li>${escapeHtml(entry.text)}` +
          (entry.meta
            ? ` <span class="cockpit-pane-list-meta">${escapeHtml(entry.meta)}</span>`
            : '') +
          '</li>'
      )
      .join('') +
    '</ul>'
  );
}

function linkGroupHTML(group) {
  const links = group.links
    .map(
      link =>
        `<button type="button" class="cockpit-pane-link" data-pane-action="${escapeHtml(link.action)}" ` +
        `data-pane-target="${escapeHtml(link.target)}">` +
        iconHTML(link.icon) +
        `<span class="cockpit-pane-link-label">${escapeHtml(link.label)}</span>` +
        `<span class="cockpit-pane-link-meta">${escapeHtml(link.meta)}</span>` +
        '</button>'
    )
    .join('');
  return (
    '<section class="cockpit-pane-links">' +
    `<h4 class="cockpit-pane-kicker">${escapeHtml(group.label)}</h4>` +
    (links
      ? `<div class="cockpit-pane-link-list">${links}</div>`
      : `<p class="cockpit-pane-note">${escapeHtml(group.empty || 'Nothing here yet.')}</p>`) +
    '</section>'
  );
}

// Everything under the rule: what was loaded, or why it was not.
function contentHTML(view) {
  const parts = [];
  if (view.lead) parts.push(`<p class="cockpit-pane-lead">${escapeHtml(view.lead)}</p>`);
  if (view.notice) parts.push(`<p class="cockpit-pane-note">${escapeHtml(view.notice)}</p>`);
  if (view.stats.length) {
    parts.push(
      '<div class="cockpit-pane-stats">' +
        view.stats
          .map(
            stat =>
              '<div class="cockpit-pane-stat">' +
              `<span class="cockpit-pane-stat-value">${escapeHtml(stat.value)}</span>` +
              `<span class="cockpit-pane-kicker">${escapeHtml(stat.label)}</span>` +
              '</div>'
          )
          .join('') +
        '</div>'
    );
  }
  if (view.fields.length) {
    parts.push(
      '<dl class="cockpit-pane-fields">' +
        view.fields
          .map(
            field =>
              `<div><dt>${escapeHtml(field.label)}</dt><dd>${escapeHtml(field.value)}</dd></div>`
          )
          .join('') +
        '</dl>'
    );
  }
  if (view.status === ITEM_LOADING) {
    parts.push('<p class="cockpit-pane-note" role="status">Loading…</p>');
  } else if (view.status === ITEM_FAILED) {
    parts.push(
      '<div class="cockpit-pane-failed" role="alert">' +
        `<p>Couldn't load this. ${escapeHtml(view.error)}</p>` +
        `<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-pane-retry="${escapeHtml(view.key)}">Retry</button>` +
        '</div>'
    );
  } else {
    parts.push(previewHTML(view.body), listHTML(view.list));
  }
  view.linkGroups.forEach(group => parts.push(linkGroupHTML(group)));
  return parts.filter(Boolean).join('');
}

function readOnlyTagHTML(tag) {
  return `<span class="cockpit-pane-tag">#${escapeHtml(tag)}</span>`;
}

// A tag that filters the tree when pressed and can be removed. Whether it is
// filtering is said by `aria-pressed` and a tick, not by colour alone.
function editableTagHTML(tag, view) {
  const name = escapeHtml(tag);
  const active = (view.activeTags || []).includes(tag);
  return (
    `<span class="cockpit-pane-tag is-editable${active ? ' is-active' : ''}">` +
    `<button type="button" class="cockpit-pane-tag-filter" data-pane-action="tag-filter" ` +
    `data-pane-target="${name}" aria-pressed="${active ? 'true' : 'false'}" ` +
    `aria-label="Filter the tree by tag ${name}">#${name}</button>` +
    `<button type="button" class="cockpit-pane-tag-remove" data-pane-action="tag-remove" ` +
    `data-pane-target="${name}" aria-label="Remove tag ${name} from ${escapeHtml(view.title)}">` +
    `${iconHTML('close', { size: 10 })}</button>` +
    '</span>'
  );
}

/**
 * The pane for one tab: breadcrumb, title, the "what and where" line with the
 * item's state and tags, its buttons, then its contents (FR31-FR40).
 */
export function renderPaneHTML(view) {
  const chip = view.chip
    ? `<span class="cockpit-pane-chip is-${escapeHtml(view.chip.tone)}">${escapeHtml(view.chip.label)}</span>`
    : '';
  const tags = (view.tags || [])
    .map(tag => (view.tagsEditable ? editableTagHTML(tag, view) : readOnlyTagHTML(tag)))
    .join('');
  const actions = view.actions.length
    ? `<div class="cockpit-pane-actions">${view.actions.map(actionHTML).join('')}</div>`
    : '';
  const content = contentHTML(view);
  return (
    `<article class="cockpit-pane-article" data-pane-kind="${escapeHtml(view.kind)}">` +
    `<div class="cockpit-pane-crumbs">${view.crumbs.map(escapeHtml).join(' <span aria-hidden="true">/</span> ')}</div>` +
    '<header class="cockpit-pane-head">' +
    `<h3 class="cockpit-pane-title" data-pane-title tabindex="-1">${escapeHtml(view.title)}</h3>` +
    `<div class="cockpit-pane-sub"><span>${escapeHtml(view.sub)}</span>${chip}${tags}</div>` +
    '</header>' +
    actions +
    (content ? `<div class="cockpit-pane-rule" role="presentation"></div>${content}` : '') +
    '</article>'
  );
}

// ---------------------------------------------------------------------------
// Mount
// ---------------------------------------------------------------------------

// What each host last drew, so a redraw that would produce the same pane leaves
// the DOM alone. The tree redraws often (every expand, every loaded section);
// the pane must not lose its scroll position, or later an editor, each time.
const drawn = new WeakMap();
// The callbacks each host's click listener reports to.
const bindings = new WeakMap();

/**
 * Draw the tab strip and the active tab's pane into `host`.
 *
 * `state` supplies `treeTabs`, `activeTabKey`, `treeTabItems` (what has been
 * loaded per tab), `flattened`, and what the overviews read: `treeContents`,
 * `metadata.tagsById` and `scheduleIndex`. Callbacks:
 *
 *   onActivateTab(key)        a tab was chosen
 *   onCloseTab(key)           a tab's close button was pressed
 *   onRetryTab(key)           "Retry" on a tab whose item failed to load
 *   onAction(action, target)  a button in the pane was pressed: 'move',
 *                             'delete', 'file-open', 'file-reveal',
 *                             'reveal-section', 'open-item', 'open-workspace'
 *
 * `focusTitle` moves keyboard focus to the pane's title after drawing, which
 * is what opening an item with Enter must do (FR72).
 */
export function mountPane(host, state, callbacks, { focusTitle = false } = {}) {
  if (!host) return;
  const cb = callbacks || {};
  const tabs = Array.isArray(state.treeTabs) ? state.treeTabs : [];
  const active = tabs.find(tab => tab.key === state.activeTabKey) || null;

  let strip = host.querySelector(':scope > .cockpit-pane-tabs');
  let panel = host.querySelector(':scope > .cockpit-pane-panel');
  if (!strip || !panel) {
    host.innerHTML =
      '<div class="cockpit-pane-tabs" role="tablist" aria-label="Open items"></div>' +
      '<div class="cockpit-pane-panel" id="cockpitPanePanel" role="tabpanel" tabindex="-1"></div>';
    strip = host.querySelector(':scope > .cockpit-pane-tabs');
    panel = host.querySelector(':scope > .cockpit-pane-panel');
    drawn.delete(host);
  }

  const stripHTML = renderTabStripHTML(tabs, state.activeTabKey, state.flattened);
  const contents = (state.treeContents || {})[active ? active.workspaceId : ''];
  const panelHTML = active
    ? renderPaneHTML(
        paneView(active, {
          flattened: state.flattened,
          item: (state.treeTabItems || {})[active.key] || null,
          sections: contents ? contents.sections : null,
          tagsById: (state.metadata && state.metadata.tagsById) || {},
          scheduleIndex: state.scheduleIndex,
          activeTags: state.activeTags
        })
      )
    : renderEmptyPaneHTML();

  // Redrawing replaces the elements, so focus that sat on one of them has to be
  // put back on its replacement: the tab that was clicked, the title that
  // Enter moved focus to before the item finished loading, or the button that
  // was just pressed.
  const focused = document.activeElement;
  const focusedTabKey =
    focused && strip.contains(focused) ? focused.getAttribute('data-pane-tab') : null;
  const inPanel = !!focused && panel.contains(focused);
  const titleHadFocus = inPanel && focused.hasAttribute('data-pane-title');
  const focusedAction = inPanel ? focused.getAttribute('data-pane-action') : null;
  const focusedTarget = inPanel ? focused.getAttribute('data-pane-target') : null;

  const last = drawn.get(host) || { strip: null, panel: null, key: '' };
  const activeKey = active ? active.key : '';
  if (last.strip !== stripHTML) {
    strip.innerHTML = stripHTML;
    strip.hidden = tabs.length === 0;
    // Keep the active tab in view when the strip is scrolled (FR27).
    const activeTab = strip.querySelector('.cockpit-pane-tab.is-active');
    if (activeTab && typeof activeTab.scrollIntoView === 'function') {
      activeTab.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    }
    if (focusedTabKey) {
      const again = Array.from(strip.querySelectorAll('[data-pane-tab]')).find(
        el => el.getAttribute('data-pane-tab') === focusedTabKey
      );
      if (again) again.focus({ preventScroll: true });
    }
  }
  if (last.panel !== panelHTML) {
    panel.innerHTML = panelHTML;
    // A different item starts at its top; the same item redrawn (its contents
    // arrived, a count changed) stays where the reader was.
    if (last.key !== activeKey) panel.scrollTop = 0;
    if (titleHadFocus) focusTitle = true;
    if (focusedAction) {
      const again = Array.from(panel.querySelectorAll('[data-pane-action]')).find(
        el =>
          el.getAttribute('data-pane-action') === focusedAction &&
          el.getAttribute('data-pane-target') === focusedTarget
      );
      if (again) again.focus({ preventScroll: true });
    }
  }
  panel.setAttribute('aria-label', active ? `${active.label || 'Open item'}` : 'Nothing open');
  drawn.set(host, { strip: stripHTML, panel: panelHTML, key: activeKey });

  // The listener is bound once per host and always reads the latest callbacks.
  const bound = bindings.has(host);
  bindings.set(host, cb);
  if (!bound) {
    host.addEventListener('click', event => {
      const handlers = bindings.get(host) || {};
      const close = event.target.closest('[data-pane-close]');
      if (close) {
        if (typeof handlers.onCloseTab === 'function') {
          handlers.onCloseTab(close.getAttribute('data-pane-close'));
        }
        return;
      }
      const tab = event.target.closest('[data-pane-tab]');
      if (tab) {
        if (typeof handlers.onActivateTab === 'function') {
          handlers.onActivateTab(tab.getAttribute('data-pane-tab'));
        }
        return;
      }
      const retry = event.target.closest('[data-pane-retry]');
      if (retry) {
        if (typeof handlers.onRetryTab === 'function') {
          handlers.onRetryTab(retry.getAttribute('data-pane-retry'));
        }
        return;
      }
      if (event.target.closest('[data-pane-save-retry]')) {
        if (typeof handlers.onAction === 'function') handlers.onAction('note-save-retry', '');
        return;
      }
      const action = event.target.closest('[data-pane-action]');
      if (action && typeof handlers.onAction === 'function') {
        handlers.onAction(
          action.getAttribute('data-pane-action'),
          action.getAttribute('data-pane-target') || ''
        );
      }
    });
  }

  if (focusTitle) {
    const title = panel.querySelector('[data-pane-title]');
    if (title) title.focus({ preventScroll: true });
  }
}

// ---------------------------------------------------------------------------
// Note editing (FR41-FR45)
// ---------------------------------------------------------------------------

// The note page's own words for each save state.
const SAVE_STATUS_WORDS = {
  unsaved: 'Unsaved',
  saving: 'Saving…',
  saved: 'Saved',
  error: 'Save failed'
};

/** The save line's text: the state, and for a failure the server's reason. */
export function saveStatusText(status, error = '') {
  const words = SAVE_STATUS_WORDS[status] || '';
  if (status === 'error' && error) return `${words}: ${error}. Your text is kept.`;
  if (status === 'error') return `${words}. Your text is kept.`;
  return words;
}

const savedTimers = new WeakMap();

/**
 * Show a save state on the pane's save line (FR42, FR44).
 *
 * Written straight into the two elements rather than by redrawing the pane,
 * because a redraw would rebuild the editor under the cursor. "Saved" clears
 * itself after a moment, as it does on the note page; a failure stays, with
 * Retry beside it.
 */
export function showSaveStatus(host, status, error = '') {
  if (!host) return;
  const line = host.querySelector('[data-pane-save-status]');
  const retry = host.querySelector('[data-pane-save-retry]');
  if (!line) return;
  clearTimeout(savedTimers.get(host));
  line.textContent = saveStatusText(status, error);
  line.dataset.status = status || '';
  if (retry) retry.hidden = status !== 'error';
  if (status === 'saved') {
    savedTimers.set(
      host,
      setTimeout(() => {
        if (line.isConnected && line.dataset.status === 'saved') line.textContent = '';
      }, 1500)
    );
  }
}

/**
 * The one note being edited in the pane.
 *
 * It mounts the EXISTING note editor (`window.NoteEditor.mount`, passed in as
 * `mountEditor`) for the active note tab and takes it down again, so only one
 * editor ever exists (FR41). The editor's own autosave timer decides when to
 * save; this supplies what a save does. The text lives here, in memory, which
 * is what lets a failed save keep it (FR44).
 *
 *   mountEditor(host)                 window.NoteEditor.mount
 *   saveContent(noteId, text, opts)   resolves when saved, rejects with the reason
 *   presence                          window.NotePresence, or null
 *   onStatus(status, error, key)      'unsaved' | 'saving' | 'saved' | 'error'
 *   onSaved(key, text, updatedAt)     a save landed
 *   onElsewhere(key, open)            the note is (not) open in another tab
 *
 * A note changed somewhere else is handled the way the note page handles it,
 * with the same module (note-presence.js): the note is claimed while it is
 * mounted and released after, and another tab holding it is reported. There is
 * no overwrite rule here; as on the note page, the last save is the one kept
 * (FR45).
 */
export function createNoteController({
  mountEditor,
  saveContent,
  presence = null,
  onStatus = () => {},
  onSaved = () => {},
  onElsewhere = () => {}
}) {
  let current = null;

  const isDirty = () => !!current && current.bundle.autosave.isDirty();

  function detach() {
    if (!current) return;
    const session = current;
    current = null;
    session.bundle.destroy();
    if (presence) presence.releaseOpenNote(session.noteId);
  }

  function start(element, key, note) {
    const session = {
      key,
      noteId: String(note.id),
      content: String(note.content ?? ''),
      updatedAt: String(note.updatedAt || ''),
      element,
      status: '',
      error: '',
      bundle: null
    };
    session.bundle = mountEditor({
      previewPaneId: element.id,
      // The Outline rail and AI assist belong to the note page (FR46).
      enableToc: false,
      getContent: () => session.content,
      setContent: value => {
        session.content = String(value ?? '');
      },
      getContentLines: () => (session.content.length > 0 ? session.content.split('\n') : ['']),
      setContentLines: lines => {
        session.content = (lines || []).join('\n');
      },
      isPreviewMode: () => true,
      onAutosaveFlush: async () => {
        // Send what is on screen now; typing during the request marks the
        // editor dirty again and the timer saves once more.
        const sending = session.content;
        try {
          const result = await saveContent(session.noteId, sending);
          session.error = '';
          if (result && result.updatedAt) session.updatedAt = result.updatedAt;
          onSaved(session.key, sending, session.updatedAt);
          return true;
        } catch (err) {
          session.error = err && err.message ? String(err.message) : 'Request failed';
          return false;
        }
      },
      onAutosaveStatusChange: status => {
        session.status = status;
        onStatus(status, session.error, session.key);
      }
    });
    current = session;
    if (presence) {
      presence.claimOpenNote(session.noteId, 'home');
      Promise.resolve(presence.isOpenElsewhere(session.noteId)).then(
        answer => {
          if (current === session) onElsewhere(session.key, !!(answer && answer.open));
        },
        () => {}
      );
    }
    return session;
  }

  /**
   * Show `note` in `element`.
   *
   * The same note again is the common case and changes nothing — unless the
   * pane was redrawn and handed over a new, empty element, or a newer version
   * of the note has arrived and nothing is unsaved here. A different note
   * replaces the mounted one; the caller must have saved first (see
   * `leaveNoteThen`). `focus` puts the cursor on the first line.
   */
  function attach(element, key, note, { focus = false } = {}) {
    if (current && current.key === key) {
      const redrawn = current.element !== element;
      current.element = element;
      const newer =
        !isDirty() &&
        String(note.content ?? '') !== current.content &&
        Date.parse(note.updatedAt || '') > Date.parse(current.updatedAt || '');
      if (newer) {
        current.content = String(note.content ?? '');
        current.updatedAt = String(note.updatedAt || '');
        current.bundle.history.reset();
      }
      if (redrawn || newer) {
        current.bundle.render();
        onStatus(current.status, current.error, current.key);
      }
      return;
    }
    detach();
    const session = start(element, key, note);
    session.bundle.render(focus ? { focusLineIndex: 0, cursorPosition: 0 } : {});
  }

  /** Save now if anything is unsaved. Resolves `false` when the save failed. */
  function flush() {
    if (!current) return Promise.resolve(true);
    return current.bundle.autosave.flushImmediate();
  }

  /**
   * Send a last save that can outlive the page (FR43, leaving Home). There is
   * no answer to wait for, so nothing is reported.
   */
  function keepalive() {
    // `pagehide` and `beforeunload` both call this; send a given text once.
    if (!isDirty() || current.keepaliveSent === current.content) return;
    current.keepaliveSent = current.content;
    current.bundle.autosave.cancel();
    try {
      Promise.resolve(saveContent(current.noteId, current.content, { keepalive: true })).catch(
        () => {}
      );
    } catch (_) {
      // The page is closing; there is nowhere left to report this.
    }
  }

  return {
    attach,
    detach,
    flush,
    keepalive,
    isDirty,
    activeKey: () => (current ? current.key : ''),
    content: () => (current ? current.content : ''),
    status: () => (current ? { status: current.status, error: current.error } : null)
  };
}

/**
 * Save the mounted note, then do something that takes it off screen (FR43).
 *
 * `proceed` runs only after the save has finished and worked; it is where the
 * caller switches tab, closes the tab, or changes view. When the save fails
 * nothing happens: the note stays mounted with the user's text and its save
 * line says so (FR44). Resolves `true` when `proceed` ran.
 */
export async function leaveNoteThen(controller, proceed) {
  const saved = await controller.flush();
  if (!saved) return false;
  controller.detach();
  proceed();
  return true;
}
