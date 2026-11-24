// home-tree-menu.js — the row menu of the Home file tree.
//
// PRD: tasks/prd-home-file-tree.md, section 4.H (FR54, FR55).
//
// Every row that can do something has a menu. It opens three ways: a
// right-click on the row, Shift+F10 or the Menu key with the row focused, and
// the "⋯" button a row shows on hover or focus (always, on a touch screen).
//
// This module decides what a row's menu holds (a pure function, exported for
// the tests) and runs one menu at a time. It does not act on a choice: it hands
// the chosen action back to whoever opened it. It looks like the Map's menu
// because it uses the same `.ori-context-menu` styles (context-menu.css); it
// shares none of the Map's menu contents.

import { escapeHtml } from './home-workspace-cockpit.js';
import { canSearchFolder, folderSearchState, folderStatusLines } from './home-folder-search.js';

export const MENU_NEW_NOTE = 'new-note';
export const MENU_NEW_TICKET = 'new-ticket';
export const MENU_UPLOAD = 'upload';
export const MENU_REFRESH = 'refresh';
// Open in the pane, which is what a click on the row's name does.
export const MENU_OPEN = 'open';
// Go to the workspace's or group's own page.
export const MENU_OPEN_PAGE = 'open-page';
// Go to the item's own place on the workspace page: the full note, the ticket
// in Tickets, the agent's page.
export const MENU_OPEN_IN_WORKSPACE = 'open-in-workspace';
export const MENU_MOVE = 'move';
export const MENU_DELETE = 'delete';
export const MENU_FILE_OPEN = 'file-open';
export const MENU_FILE_REVEAL = 'file-reveal';
// Show the workspace's outputs/ folder in the file manager.
export const MENU_SHOW_OUTPUTS = 'show-outputs';
// Make Files' search look only inside this workspace, group or folder.
export const MENU_SEARCH_IN = 'search-in';

const item = (action, label, extra) => ({ action, label, ...extra });
const DIVIDER = { divider: true };

/**
 * The menu for a row, as a list of `{ action, label }` (and `{ divider: true }`
 * between groups). An empty list means the row has no menu, and no "⋯" button.
 *
 * This is the PRD's menu table (FR55):
 *
 *   group              Open group, New note, New ticket, Upload file…,
 *                      Search in this group, Refresh, Move…, Delete
 *   workspace          Open workspace, New note, New ticket, Upload file…,
 *                      Search in this workspace, Refresh, Move…, Delete
 *   Notes section      New note
 *   Backlog section    New ticket
 *   Files section      Upload file…
 *   a folder in Files  Upload file…, Search in this folder
 *   a folder in
 *   Outputs            Search in this folder
 *   a linked folder    Search in this folder (only while it is searchable)
 *   Outputs section    Show outputs folder
 *   note, ticket,
 *   agent              Open, Open in workspace
 *   file under files/  Open, Open in default app, Reveal in Finder
 *   output file,
 *   linked-folder file Open
 *
 * A folder in Outputs has nothing to upload into (outputs/ is written by task
 * runs) and one output file is not opened or revealed on its own (decision
 * D15), so searching it is all its menu holds. A linked folder's menu starts
 * with a status line (whether it is searchable, how many filenames, when it
 * was last indexed; home-folder-search.js), followed by Search in this folder
 * while the filename index has something of it to search. `context.folderSearch`
 * is the list of searchable folders for this Files visit; without it a linked
 * folder has no menu. The folders inside it have none. The workspace's Refresh
 * reloads the linked folders that are open.
 */
export function menuItemsFor(row, context = {}) {
  const kind = row && row.kind;
  if (kind === 'group' || kind === 'workspace') {
    return [
      item(MENU_OPEN_PAGE, kind === 'group' ? 'Open group' : 'Open workspace'),
      DIVIDER,
      item(MENU_NEW_NOTE, 'New note'),
      item(MENU_NEW_TICKET, 'New ticket'),
      item(MENU_UPLOAD, 'Upload file…'),
      DIVIDER,
      item(MENU_SEARCH_IN, kind === 'group' ? 'Search in this group' : 'Search in this workspace'),
      item(MENU_REFRESH, 'Refresh'),
      DIVIDER,
      item(MENU_MOVE, 'Move…'),
      item(MENU_DELETE, 'Delete', { danger: true })
    ];
  }
  if (kind === 'section') {
    if (row.section === 'notes') return [item(MENU_NEW_NOTE, 'New note')];
    if (row.section === 'backlog') return [item(MENU_NEW_TICKET, 'New ticket')];
    if (row.section === 'files') return [item(MENU_UPLOAD, 'Upload file…')];
    if (row.section === 'outputs') return [item(MENU_SHOW_OUTPUTS, 'Show outputs folder')];
    return [];
  }
  if (kind === 'folder') {
    return [item(MENU_UPLOAD, 'Upload file…'), item(MENU_SEARCH_IN, 'Search in this folder')];
  }
  if (kind === 'outputFolder') return [item(MENU_SEARCH_IN, 'Search in this folder')];
  if (kind === 'linked') {
    const state = folderSearchState(row, context && context.folderSearch);
    if (!canSearchFolder(state)) return [];
    return [
      statusEntry(state, context && context.statusOptions),
      item(MENU_SEARCH_IN, 'Search in this folder')
    ];
  }
  if (kind === 'note' || kind === 'ticket' || kind === 'agent' || kind === 'chat') {
    return [item(MENU_OPEN, 'Open'), item(MENU_OPEN_IN_WORKSPACE, 'Open in workspace')];
  }
  if (kind === 'file') {
    return [
      item(MENU_OPEN, 'Open'),
      item(MENU_FILE_OPEN, 'Open in default app'),
      item(MENU_FILE_REVEAL, 'Reveal in Finder')
    ];
  }
  if (kind === 'output' || kind === 'linkedFile') return [item(MENU_OPEN, 'Open')];
  return [];
}

/**
 * The status line a linked folder's menu starts with: not a choice, only what
 * is true of the folder now. It is drawn above the items and is the menu's
 * description for a screen reader.
 */
function statusEntry(state, options) {
  const lines = folderStatusLines(state, options || {});
  return { status: true, tone: state.tone, ...lines };
}

/** The entries of a menu that can be chosen (not dividers or status lines). */
export function choosableEntries(items) {
  return (Array.isArray(items) ? items : []).filter(
    entry => entry && !entry.divider && !entry.status
  );
}

export function rowHasMenu(row, context) {
  return choosableEntries(menuItemsFor(row, context)).length > 0;
}

/**
 * Where to put a menu so that it stays on screen.
 *
 * `point` is where it was asked for (the cursor, or the foot of the row),
 * `size` the menu's own size, `viewport` the window's. The menu opens down and
 * to the right of the point and is pulled back inside an 8px margin when it
 * would run off an edge.
 */
export function menuPosition(point, size, viewport, margin = 8) {
  const width = Math.max(0, Number(size && size.width) || 0);
  const height = Math.max(0, Number(size && size.height) || 0);
  const maxLeft = Math.max(margin, (Number(viewport && viewport.width) || 0) - width - margin);
  const maxTop = Math.max(margin, (Number(viewport && viewport.height) || 0) - height - margin);
  return {
    left: Math.min(Math.max(margin, Number(point && point.x) || 0), maxLeft),
    top: Math.min(Math.max(margin, Number(point && point.y) || 0), maxTop)
  };
}

export const MENU_STATUS_ID = 'cockpitTreeMenuStatus';

function statusHTML(entry) {
  const line = (cls, text) =>
    text ? `<span class="cockpit-tree-menu-status__${cls}">${escapeHtml(text)}</span>` : '';
  return (
    `<div class="cockpit-tree-menu-status" id="${MENU_STATUS_ID}" data-tree-menu-status ` +
    `data-tone="${escapeHtml(entry.tone || '')}">` +
    '<span class="cockpit-tree-menu-status__title">' +
    '<span class="cockpit-folder-dot" aria-hidden="true"></span>' +
    `${escapeHtml(entry.title)}</span>` +
    line('detail', entry.detail) +
    line('reason', entry.reason) +
    '</div>'
  );
}

/**
 * Menu markup: a `role="menu"` of `menuitem` buttons, one tabbable at a time,
 * inside the positioned panel. A status entry is drawn above the menu, outside
 * it (a menu holds only items and separators), and describes it.
 */
export function renderMenuHTML(items, label) {
  const entries = (Array.isArray(items) ? items : []).filter(Boolean);
  const status = entries.find(entry => entry.status);
  const body = entries
    .map(entry => {
      if (entry.status) return '';
      if (entry.divider) return '<div class="ori-context-divider" role="separator"></div>';
      return (
        `<button type="button" class="ori-context-item${entry.danger ? ' ori-context-danger' : ''}" ` +
        `role="menuitem" tabindex="-1" data-tree-menu-action="${escapeHtml(entry.action)}"` +
        `${entry.ariaLabel ? ` aria-label="${escapeHtml(entry.ariaLabel)}"` : ''}>` +
        `${escapeHtml(entry.label)}</button>`
      );
    })
    .join('');
  return (
    '<div class="ori-context-menu cockpit-tree-menu" data-tree-menu>' +
    (status ? statusHTML(status) : '') +
    `<div class="cockpit-tree-menu-items" role="menu" aria-label="${escapeHtml(label || 'Actions')}"` +
    `${status ? ` aria-describedby="${MENU_STATUS_ID}"` : ''}>${body}</div>` +
    '</div>'
  );
}

/**
 * The next item for an arrow key, wrapping at both ends.
 *
 * `count` is the number of items, `from` the current one (-1 for none), `step`
 * +1 or -1.
 */
export function nextMenuIndex(from, step, count) {
  if (count <= 0) return -1;
  if (from < 0) return step > 0 ? 0 : count - 1;
  return (((from + step) % count) + count) % count;
}

// ---------------------------------------------------------------------------
// One open menu at a time
// ---------------------------------------------------------------------------

let open = null;

/**
 * Close the menu, if one is open.
 *
 * Every way out lands here — choosing an item, Escape, a click or right-click
 * elsewhere, a resize, a scroll — so focus goes back to the row the menu was
 * opened from exactly once.
 */
export function closeRowMenu({ restoreFocus = true } = {}) {
  const state = open;
  if (!state) return;
  open = null;
  state.teardown.forEach(off => off());
  state.host.remove();
  if (!restoreFocus) return;
  // The tree may have been redrawn while the menu was open, which replaces the
  // row's element; `findOrigin` looks the row up again in the live tree.
  const origin =
    state.origin && state.origin.isConnected
      ? state.origin
      : typeof state.findOrigin === 'function'
        ? state.findOrigin()
        : null;
  if (origin) origin.focus();
}

export function isRowMenuOpen() {
  return open !== null;
}

/**
 * Open a menu.
 *
 *   items      from `menuItemsFor`
 *   label      the menu's accessible name, for example "Actions for My HQ"
 *   at         { x, y } in viewport coordinates
 *   origin     the element focus returns to when the menu closes
 *   findOrigin finds that element again if it was replaced meanwhile
 *   event      the gesture that opened it, so that same event, still on its
 *              way up to the document, does not also close it
 *   onChoose   called with the chosen action after the menu has closed
 *
 * Returns false when there is nothing to show.
 */
export function openRowMenu({
  items,
  label,
  at,
  origin = null,
  findOrigin = null,
  event = null,
  onChoose
}) {
  closeRowMenu({ restoreFocus: false });
  const entries = (Array.isArray(items) ? items : []).filter(Boolean);
  if (choosableEntries(entries).length === 0) return false;

  const host = document.createElement('div');
  host.className = 'cockpit-tree-menu-host';
  host.innerHTML = renderMenuHTML(entries, label);
  document.body.appendChild(host);
  const menu = host.querySelector('[data-tree-menu]');
  const buttons = Array.from(menu.querySelectorAll('[data-tree-menu-action]'));

  const rect = menu.getBoundingClientRect();
  const placed = menuPosition(
    at,
    { width: rect.width, height: rect.height },
    { width: window.innerWidth, height: window.innerHeight }
  );
  menu.style.left = `${placed.left}px`;
  menu.style.top = `${placed.top}px`;

  const state = { host, origin, findOrigin, teardown: [] };
  open = state;
  const listen = (target, type, handler, options) => {
    target.addEventListener(type, handler, options);
    state.teardown.push(() => target.removeEventListener(type, handler, options));
  };
  const focusItem = index => {
    buttons.forEach((button, i) => button.setAttribute('tabindex', i === index ? '0' : '-1'));
    if (buttons[index]) buttons[index].focus();
  };
  const choose = button => {
    const action = button.getAttribute('data-tree-menu-action');
    // Close first: focus is back on the row before the action runs, so an
    // inline input or a dialog the action opens starts from a sane place.
    closeRowMenu();
    if (typeof onChoose === 'function') onChoose(action);
  };

  buttons.forEach(button => listen(button, 'click', () => choose(button)));
  // A press on the status line (or any gap between items) must not take focus
  // away from the menu, or its keys (Escape, the arrows) would stop working.
  listen(menu, 'mousedown', pressEvent => {
    if (!pressEvent.target.closest('[data-tree-menu-action]')) pressEvent.preventDefault();
  });
  listen(menu, 'keydown', keyEvent => {
    const at = buttons.indexOf(document.activeElement);
    if (keyEvent.key === 'ArrowDown' || keyEvent.key === 'ArrowUp') {
      keyEvent.preventDefault();
      focusItem(nextMenuIndex(at, keyEvent.key === 'ArrowDown' ? 1 : -1, buttons.length));
    } else if (keyEvent.key === 'Home' || keyEvent.key === 'End') {
      keyEvent.preventDefault();
      focusItem(keyEvent.key === 'Home' ? 0 : buttons.length - 1);
    } else if (keyEvent.key === 'Escape') {
      keyEvent.preventDefault();
      keyEvent.stopPropagation();
      closeRowMenu();
    } else if (keyEvent.key === 'Tab') {
      // A menu is left by choosing or by Escape; Tab closes it and lets focus
      // move on from the row.
      closeRowMenu();
    }
  });

  // Dismissal from outside the menu. The opening gesture is still travelling
  // up to the document when these are added, so it is told apart by identity.
  const outside = pointerEvent => {
    if (pointerEvent === event) return;
    if (pointerEvent.target && pointerEvent.target.closest('[data-tree-menu]')) return;
    closeRowMenu({ restoreFocus: false });
  };
  listen(document, 'mousedown', outside, true);
  listen(document, 'contextmenu', outside, true);
  listen(window, 'resize', () => closeRowMenu({ restoreFocus: false }));
  listen(window, 'blur', () => closeRowMenu({ restoreFocus: false }));
  // Scrolling by hand would leave the menu behind its row, so it closes it.
  // This listens for the gesture, not for `scroll` events: the tree puts its
  // own scroll position back each time it redraws (a section finishing its
  // load is enough), and that must not close a menu that was just opened.
  listen(
    document,
    'wheel',
    wheelEvent => {
      if (wheelEvent.target && wheelEvent.target.closest('[data-tree-menu]')) return;
      closeRowMenu({ restoreFocus: false });
    },
    { capture: true, passive: true }
  );
  listen(
    document,
    'touchmove',
    touchEvent => {
      if (touchEvent.target && touchEvent.target.closest('[data-tree-menu]')) return;
      closeRowMenu({ restoreFocus: false });
    },
    { capture: true, passive: true }
  );

  focusItem(0);
  return true;
}
