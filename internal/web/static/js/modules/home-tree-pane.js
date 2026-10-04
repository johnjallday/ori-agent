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

import { escapeHtml, isGroupWorkspace } from './home-workspace-cockpit.js';
import { sectionInfo, sectionOfKind } from './home-tree-sources.js';
import { iconHTML } from './home-tree-icons.js';
import { renderMarkdown } from './note-editor.js';

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
export function paneView(tab, { flattened = [], item = null } = {}) {
  const workspace = findWorkspace(flattened, tab.workspaceId);
  const parent =
    workspace && workspace.parent_id ? findWorkspace(flattened, workspace.parent_id) : null;
  const loaded = item || { status: ITEM_READY, value: null, error: '' };
  const view = {
    key: tab.key,
    kind: tab.kind,
    crumbs: paneCrumbs(tab, flattened),
    title: tabTitle(tab, workspace),
    sub: paneSubline(tab, workspace, parent),
    status: loaded.status,
    error: loaded.error || '',
    tags: [],
    body: null
  };

  if (tab.kind === 'note' && loaded.status === ITEM_READY && loaded.value) {
    view.title = loaded.value.name || view.title;
    view.crumbs = [...view.crumbs.slice(0, -1), view.title];
    view.tags = Array.isArray(loaded.value.tags) ? loaded.value.tags : [];
    view.body = { type: 'markdown', markdown: String(loaded.value.content ?? '') };
  }
  return view;
}

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

function bodyHTML(view) {
  if (view.status === ITEM_LOADING) {
    return '<p class="cockpit-pane-note" role="status">Loading…</p>';
  }
  if (view.status === ITEM_FAILED) {
    return (
      '<div class="cockpit-pane-failed" role="alert">' +
      `<p>Couldn't load this. ${escapeHtml(view.error)}</p>` +
      `<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-pane-retry="${escapeHtml(view.key)}">Retry</button>` +
      '</div>'
    );
  }
  if (view.body && view.body.type === 'markdown') {
    return view.body.markdown.trim()
      ? `<div class="cockpit-pane-markdown">${renderMarkdown(view.body.markdown)}</div>`
      : '<p class="cockpit-pane-note">This note is empty.</p>';
  }
  return '';
}

/** The pane for one tab: breadcrumb, title, the "what and where" line, body. */
export function renderPaneHTML(view) {
  const tags = (view.tags || [])
    .map(tag => `<span class="cockpit-pane-tag">#${escapeHtml(tag)}</span>`)
    .join('');
  const body = bodyHTML(view);
  return (
    `<article class="cockpit-pane-article" data-pane-kind="${escapeHtml(view.kind)}">` +
    `<div class="cockpit-pane-crumbs">${view.crumbs.map(escapeHtml).join(' <span aria-hidden="true">/</span> ')}</div>` +
    '<header class="cockpit-pane-head">' +
    `<h3 class="cockpit-pane-title" data-pane-title tabindex="-1">${escapeHtml(view.title)}</h3>` +
    `<div class="cockpit-pane-sub"><span>${escapeHtml(view.sub)}</span>${tags}</div>` +
    '</header>' +
    (body ? `<div class="cockpit-pane-rule" role="presentation"></div>${body}` : '') +
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
 * loaded per tab) and `flattened`. Callbacks:
 *
 *   onActivateTab(key)   a tab was chosen
 *   onCloseTab(key)      a tab's close button was pressed
 *   onRetryTab(key)      "Retry" on a tab whose item failed to load
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
  const panelHTML = active
    ? renderPaneHTML(
        paneView(active, {
          flattened: state.flattened,
          item: (state.treeTabItems || {})[active.key] || null
        })
      )
    : renderEmptyPaneHTML();

  // Redrawing replaces the elements, so focus that sat on one of them has to be
  // put back on its replacement: the tab that was clicked, or the title that
  // Enter moved focus to before the item finished loading.
  const focused = document.activeElement;
  const focusedTabKey =
    focused && strip.contains(focused) ? focused.getAttribute('data-pane-tab') : null;
  const titleHadFocus =
    !!focused && panel.contains(focused) && focused.hasAttribute('data-pane-title');

  const last = drawn.get(host) || { strip: null, panel: null };
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
    panel.scrollTop = 0;
    if (titleHadFocus) focusTitle = true;
  }
  panel.setAttribute('aria-label', active ? `${active.label || 'Open item'}` : 'Nothing open');
  drawn.set(host, { strip: stripHTML, panel: panelHTML });

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
      if (retry && typeof handlers.onRetryTab === 'function') {
        handlers.onRetryTab(retry.getAttribute('data-pane-retry'));
      }
    });
  }

  if (focusTitle) {
    const title = panel.querySelector('[data-pane-title]');
    if (title) title.focus({ preventScroll: true });
  }
}
