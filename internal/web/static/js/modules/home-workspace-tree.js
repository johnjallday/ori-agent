// home-workspace-tree.js — the Tree peer view for the Home cockpit.
//
// PRDs: tasks/prd-home-workspace-cockpit.md §4.4 (the Tree as Map's peer) and
// tasks/prd-home-file-tree.md (the Tree as a file explorer with a pane).
//
// Tree is Map's PEER, not a drawer: it renders into the same workspace-area
// slot, and exactly one of the two is active at a time. It is a slim explorer:
// groups hold workspaces, and every workspace and group expands into its own
// contents — Notes, Backlog, Files, Memory and Agents. Whatever is clicked
// opens in the pane beside it (home-tree-pane.js).
//
// This module owns the tree column only: shaping the rows, drawing them, and
// local interaction (expand, keyboard, drag). It never fetches a workspace's
// contents and never opens anything itself. Contents arrive already shaped in
// `state.treeContents` (see home-tree-sources.js), and every action is handed
// back to the cockpit coordinator through callbacks, so the cockpit stays the
// single authority for shared state, refresh, and selection (FR117). Server
// contracts for the management tools are the launcher's existing ones:
//
//   move/reorder   PATCH  /api/workspaces/{id}  { parent_id, order_index }
//   delete         DELETE /api/workspaces/{id}?confirm=true[&delete_mode=...]
//   undo delete    POST   /api/workspaces/{id}/restore
//   rescan         POST   /api/workspaces/rescan
//
// Creating a group goes through the cockpit's shared Create Group dialog.
//
// Pure helpers are exported for home-workspace-tree.test.js; the module is
// imported directly by home-workspace-cockpit.js, so it needs no global.

import { escapeHtml, isGroupWorkspace, workspaceSignals } from './home-workspace-cockpit.js';
import {
  deleteWorkspace as deleteWorkspaceAction,
  deleteWorkspaces as deleteWorkspacesAction
} from './workspace-bulk-actions.js';
import {
  SECTIONS,
  SECTION_FAILED,
  SECTION_LOADING,
  SECTION_READY,
  itemKey,
  parseItemKey,
  sectionInfo,
  sectionKey,
  sectionOfKind
} from './home-tree-sources.js';
import { iconHTML, rowIconName } from './home-tree-icons.js';
import {
  MENU_DELETE,
  MENU_MOVE,
  MENU_OPEN,
  MENU_OPEN_PAGE,
  menuItemsFor,
  openRowMenu,
  rowHasMenu
} from './home-tree-menu.js';

// ---------------------------------------------------------------------------
// Hierarchy shaping
// ---------------------------------------------------------------------------

/** Row kinds that are a workspace or a group, as opposed to their contents. */
export function isWorkspaceRowKind(kind) {
  return kind === 'group' || kind === 'workspace';
}

// Groups and sections start open; workspaces and folders start closed (FR6).
// The two sets below record only where the user differs from that default, so
// a row nobody has touched needs no entry.
function opensByDefault(kind) {
  return kind === 'group' || kind === 'section';
}

/**
 * Whether a row is open.
 *
 * `collapsed` holds the rows that start open and were closed; `expanded` holds
 * the rows that start closed and were opened.
 */
export function isRowExpanded(kind, id, collapsed, expanded) {
  return opensByDefault(kind) ? !collapsed.has(id) : expanded.has(id);
}

/**
 * Open or close a row in the Tree's own state.
 *
 * `state.collapsedGroups` and `state.expandedRows` are the two sets
 * `isRowExpanded` reads. Nothing else changes: opening a workspace here does
 * not fetch its contents, the cockpit does that when it next draws the tree.
 */
export function setRowExpanded(state, kind, id, open) {
  if (opensByDefault(kind)) {
    if (open) state.collapsedGroups.delete(id);
    else state.collapsedGroups.add(id);
    return;
  }
  if (open) state.expandedRows.add(id);
  else state.expandedRows.delete(id);
}

/**
 * The rows that must be open for a row to be on screen (FR28).
 *
 * Given a row key, returns `{ kind, id }` for each ancestor, outermost first:
 * the groups above its workspace, then — for a row inside a workspace — the
 * workspace itself, its section, and any folders above a file. Pass each to
 * `setRowExpanded(state, kind, id, true)`. The row itself is not included:
 * revealing a workspace does not expand it.
 */
export function revealTargets(key, flattened) {
  const parsed = parseItemKey(key);
  if (!parsed) return [];
  const rows = Array.isArray(flattened) ? flattened : [];
  const targets = ancestorIds(rows, parsed.workspaceId).map(id => ({ kind: 'group', id }));
  if (parsed.kind === 'workspace') return targets;

  const owner = rows.find(row => row && row.id === parsed.workspaceId);
  targets.push({
    kind: owner && isGroupWorkspace(owner) ? 'group' : 'workspace',
    id: parsed.workspaceId
  });
  const sectionId = parsed.kind === 'section' ? parsed.itemId : sectionOfKind(parsed.kind);
  const info = sectionInfo(sectionId);
  if (info && info.expandable && parsed.kind !== 'section') {
    targets.push({ kind: 'section', id: sectionKey(parsed.workspaceId, sectionId) });
  }
  if (parsed.kind === 'file' || parsed.kind === 'folder') {
    const folders = parsed.itemId.split('/').slice(0, -1);
    folders.forEach((_, index) => {
      targets.push({
        kind: 'folder',
        id: itemKey(parsed.workspaceId, 'folder', folders.slice(0, index + 1).join('/'))
      });
    });
  }
  return targets;
}

/** The words a failed section shows, for example "Couldn't load Notes" (FR19). */
export function sectionFailedLabel(label) {
  return `Couldn't load ${label}`;
}

function sectionState(contents, workspaceId, sectionId) {
  const entry = contents && contents[workspaceId];
  const state = entry && entry.sections && entry.sections[sectionId];
  // A workspace that has just been expanded has no entry yet: the cockpit asks
  // for its contents right after this render, so "not asked yet" reads as
  // loading rather than as empty.
  return state || { status: SECTION_LOADING, rows: [], count: null, error: '' };
}

// ---------------------------------------------------------------------------
// The filter box (FR65-FR67)
// ---------------------------------------------------------------------------
//
// With text in the filter box the tree keeps a row when its name contains the
// text, and keeps the rows above it so it can be seen. A row kept only as the
// way to a match is shown open whatever its own state (`forced`), with just
// the matching rows inside it. A row that matches is shown as the user has
// it, with everything inside it: finding "Night Drive" and then opening it
// must show its notes, not an empty list.
//
// Only names are matched, and only of things the user made: groups,
// workspaces, notes, tickets, files, folders and agents. Section headings and
// the "Loading…" lines never match.

/** The filter box's text as it is compared: trimmed, case ignored. */
export function normalizeFilter(text) {
  return String(text == null ? '' : text)
    .trim()
    .toLowerCase();
}

const FILTERED_CONTENT_KINDS = new Set(['note', 'ticket', 'file', 'folder', 'agent']);

function nameMatches(name, filter) {
  return String(name || '')
    .toLowerCase()
    .includes(filter);
}

// The same context with the filter off, for what is inside a matching row.
function unfiltered(ctx) {
  return ctx.filter ? { ...ctx, filter: '' } : ctx;
}

/**
 * Whether some workspace or group has never had its contents loaded.
 *
 * The filter can only search what has been loaded, and contents load when a
 * row is expanded; the "nothing matches" message says so when this is true
 * (FR67).
 */
export function hasUnsearchedContents(nodes, contents) {
  const loaded = contents || {};
  const walk = list =>
    (Array.isArray(list) ? list : []).some(
      node => !!node && (!loaded[node.id] || walk(node.children))
    );
  return walk(nodes);
}

// A row from home-tree-sources.js → an item for the tree, with its folder
// children when the folder is open. With a filter on, null for a row that
// neither matches nor holds a match.
function contentItem(source, ctx) {
  const expandable = source.kind === 'folder';
  if (ctx.filter) {
    if (!FILTERED_CONTENT_KINDS.has(source.kind)) return null;
    if (nameMatches(source.label, ctx.filter)) return contentItem(source, unfiltered(ctx));
    const inside = expandable
      ? source.children.map(child => contentItem(child, ctx)).filter(Boolean)
      : [];
    if (!inside.length) return null;
    return {
      row: {
        id: source.id,
        kind: source.kind,
        name: source.label,
        meta: source.meta || {},
        workspaceId: source.workspaceId,
        expandable: true,
        expanded: true,
        forced: true
      },
      children: inside
    };
  }
  const expanded = expandable ? ctx.expanded.has(source.id) : null;
  const item = {
    row: {
      id: source.id,
      kind: source.kind,
      name: source.label,
      meta: source.meta || {},
      workspaceId: source.workspaceId,
      expandable,
      expanded
    },
    children: null
  };
  if (expandable && expanded) {
    item.children = source.children.length
      ? source.children.map(child => contentItem(child, ctx))
      : [placeholderItem(source.workspaceId, 'empty', 'files', 'Empty folder', source.id)];
  }
  return item;
}

function placeholderItem(workspaceId, kind, sectionId, name, scope = '') {
  return {
    row: {
      id: itemKey(workspaceId, kind, scope || sectionId),
      kind,
      name,
      section: sectionId,
      workspaceId,
      expandable: false,
      expanded: null
    },
    children: null
  };
}

/**
 * The section items of one workspace or group, in FR9's order.
 *
 * A workspace always shows every section. A group shows only the sections that
 * hold something, and only once their data has arrived; a section that failed
 * to load still shows, so it can be retried (FR13, FR16, FR19).
 */
function sectionItems(node, isGroup, ctx) {
  const items = [];
  SECTIONS.forEach(info => {
    const state = sectionState(ctx.contents, node.id, info.id);
    // A note or ticket being named here keeps its section on screen and open,
    // even a group's section that is hidden for being empty (FR47, FR52).
    const drafting =
      !!ctx.draft && ctx.draft.workspaceId === node.id && ctx.draft.section === info.id;

    // With a filter on, a section is only the way to the matches inside it —
    // and only what has been loaded can be matched. A row being named stays.
    if (ctx.filter) {
      if (!info.expandable) return;
      const matches =
        state.status === SECTION_READY
          ? state.rows.map(source => contentItem(source, ctx)).filter(Boolean)
          : [];
      if (drafting) matches.unshift(draftItem(node.id, info.id, ctx.draft));
      if (!matches.length) return;
      items.push({
        row: {
          id: sectionKey(node.id, info.id),
          kind: 'section',
          name: info.label,
          section: info.id,
          count: state.count,
          status: state.status,
          workspaceId: node.id,
          expandable: true,
          expanded: true,
          forced: true
        },
        children: matches
      });
      return;
    }

    if (isGroup && !drafting) {
      if (state.status === SECTION_LOADING) return;
      if (state.status === SECTION_READY && !(state.count > 0)) return;
    }

    // Memory is one row you open, not a list you expand. When it could not be
    // loaded there is nothing to open, so the row is the retry itself.
    if (!info.expandable) {
      if (state.status === SECTION_FAILED) {
        items.push(placeholderItem(node.id, 'failed', info.id, sectionFailedLabel(info.label)));
        return;
      }
      items.push({
        row: {
          id: itemKey(node.id, info.id),
          kind: info.id,
          name: info.label,
          section: info.id,
          count: state.count,
          status: state.status,
          workspaceId: node.id,
          expandable: false,
          expanded: null
        },
        children: null
      });
      return;
    }

    const id = sectionKey(node.id, info.id);
    const expanded = drafting || isRowExpanded('section', id, ctx.collapsed, ctx.expanded);
    const item = {
      row: {
        id,
        kind: 'section',
        name: info.label,
        section: info.id,
        count: state.count,
        status: state.status,
        workspaceId: node.id,
        expandable: true,
        expanded
      },
      children: null
    };
    if (expanded) {
      if (state.status === SECTION_LOADING) {
        item.children = [placeholderItem(node.id, 'loading', info.id, 'Loading…')];
      } else if (state.status === SECTION_FAILED) {
        item.children = [
          placeholderItem(node.id, 'failed', info.id, sectionFailedLabel(info.label))
        ];
      } else if (state.rows.length === 0) {
        // The "No notes yet" line gives way to the row being named.
        item.children = drafting ? [] : [placeholderItem(node.id, 'empty', info.id, info.empty)];
      } else {
        item.children = state.rows.map(source => contentItem(source, ctx));
      }
      if (drafting) item.children.unshift(draftItem(node.id, info.id, ctx.draft));
    }
    items.push(item);
  });
  return items;
}

// The editable row where a new note or ticket is named, first in its section.
function draftItem(workspaceId, sectionId, draft) {
  return {
    row: {
      id: itemKey(workspaceId, 'draft', sectionId),
      kind: 'draft',
      name: String(draft.value || ''),
      section: sectionId,
      draftKind: draft.kind,
      busy: !!draft.busy,
      workspaceId,
      expandable: false,
      expanded: null
    },
    children: null
  };
}

function workspaceItems(nodes, ctx) {
  const siblings = (Array.isArray(nodes) ? nodes : []).filter(Boolean);
  return siblings
    .map((node, index) => {
      const children = Array.isArray(node.children) ? node.children.filter(Boolean) : [];
      const group = isGroupWorkspace(node) || children.length > 0;
      const kind = group ? 'group' : 'workspace';
      const name = node.name || (group ? 'Group' : 'Untitled workspace');
      const item = {
        row: {
          workspace: node,
          id: node.id,
          kind,
          name,
          workspaceId: node.id,
          isGroup: group,
          hasChildren: children.length > 0,
          expandable: true,
          expanded: false,
          childCount: children.length,
          // The next workspace or group at this level, never a section row: it
          // is what a drop "after this row" is placed in front of. It is the
          // real next one even when a filter hides it.
          nextSiblingId: (siblings[index + 1] && siblings[index + 1].id) || ''
        },
        children: null
      };
      // With a filter on, a row whose own name does not match stays only as
      // the way to the matches inside it, and is shown open.
      if (ctx.filter && !nameMatches(name, ctx.filter)) {
        const inside = [...workspaceItems(children, ctx), ...sectionItems(node, group, ctx)];
        if (!inside.length) return null;
        item.row.expanded = true;
        item.row.forced = true;
        item.children = inside;
        return item;
      }
      const own = unfiltered(ctx);
      item.row.expanded = isRowExpanded(kind, node.id, own.collapsed, own.expanded);
      if (item.row.expanded) {
        // A group's own children come first, then its sections (FR16).
        item.children = [...workspaceItems(children, own), ...sectionItems(node, group, own)];
      }
      return item;
    })
    .filter(Boolean);
}

function appendLevel(rows, items, depth, parentId) {
  items.forEach((item, index) => {
    rows.push({
      isGroup: false,
      hasChildren: false,
      childCount: 0,
      nextSiblingId: '',
      ...item.row,
      depth,
      parentId,
      posInSet: index + 1,
      setSize: items.length
    });
    if (item.children) appendLevel(rows, item.children, depth + 1, item.row.id);
  });
}

/**
 * Flatten the workspace tree into the rows Tree actually renders.
 *
 * Rows inside a closed row are omitted entirely rather than hidden with CSS, so
 * arrow-key navigation and `aria-setsize`/`aria-posinset` describe the rows a
 * user can actually reach (FR41, FR127).
 *
 * `options.expanded` is the set of opened workspaces and folders, and
 * `options.contents` is what has been loaded so far, keyed by workspace id:
 * `{ [id]: { sections: { notes: { status, rows, count }, … } } }`. With neither
 * (the Map's view of the tree, and the older tests) the result is the plain
 * group and workspace hierarchy.
 *
 * `options.draft` is a note or ticket being named:
 * `{ workspaceId, section, kind, value }`. It adds one editable row at the top
 * of that section, and shows the section if it would otherwise be hidden.
 *
 * `options.filter` is the filter box's text. With it, only rows whose name
 * contains it are kept, together with the rows above them, which are shown
 * open and marked `forced` (FR65, FR66).
 */
export function visibleTreeRows(nodes, collapsedIds, depth = 0, parentId = '', options = {}) {
  const ctx = {
    collapsed: collapsedIds instanceof Set ? collapsedIds : new Set(collapsedIds || []),
    expanded: options.expanded instanceof Set ? options.expanded : new Set(options.expanded || []),
    contents: options.contents || {},
    draft: options.draft || null,
    filter: normalizeFilter(options.filter)
  };
  const rows = [];
  appendLevel(rows, workspaceItems(nodes, ctx), depth, parentId);
  return rows;
}

/** Every descendant id of `id`, from the FLAT row list. */
export function descendantIds(flattened, id) {
  const rows = Array.isArray(flattened) ? flattened : [];
  const out = [];
  const walk = parentId => {
    rows.forEach(row => {
      if (!row || row.parent_id !== parentId) return;
      out.push(row.id);
      walk(row.id);
    });
  };
  walk(id);
  return out;
}

/** The chain of ancestor group ids above `id`, nearest last. */
export function ancestorIds(flattened, id) {
  const rows = Array.isArray(flattened) ? flattened : [];
  const byId = new Map(rows.map(row => [row.id, row]));
  const out = [];
  let current = byId.get(id);
  while (current && current.parent_id) {
    out.unshift(current.parent_id);
    current = byId.get(current.parent_id);
  }
  return out;
}

// ---------------------------------------------------------------------------
// Move validation (FR49, FR50, FR51)
// ---------------------------------------------------------------------------

/**
 * Why a move is not allowed, or '' when it is.
 *
 * The explanation is the user-facing copy: FR50 requires an actionable reason,
 * not a silent no-op.
 */
export function moveRejectionReason(flattened, movingId, targetParentId) {
  if (!movingId) return 'Nothing is selected to move.';
  const rows = Array.isArray(flattened) ? flattened : [];
  const moving = rows.find(row => row && row.id === movingId);
  if (!moving) return 'That workspace no longer exists.';
  const parent = targetParentId || '';
  if (parent === movingId) {
    return `"${moving.name}" cannot be moved into itself.`;
  }
  if (parent && descendantIds(rows, movingId).includes(parent)) {
    const target = rows.find(row => row && row.id === parent);
    return `"${moving.name}" cannot be moved into "${(target && target.name) || 'a group'}", which is inside it.`;
  }
  if (parent) {
    const target = rows.find(row => row && row.id === parent);
    if (target && !isGroupWorkspace(target)) {
      return `"${(target && target.name) || 'That item'}" is a workspace, not a group.`;
    }
  }
  if ((moving.parent_id || '') === parent) return '';
  return '';
}

export function isMoveAllowed(flattened, movingId, targetParentId) {
  return moveRejectionReason(flattened, movingId, targetParentId) === '';
}

/**
 * Destinations offered by the keyboard/button Move action.
 *
 * Drag-and-drop must never be the only way to move something (FR51, FR137), so
 * this is the same destination set expressed as a list: top level plus every
 * group that is a legal target.
 */
export function moveDestinations(flattened, movingId) {
  const rows = Array.isArray(flattened) ? flattened : [];
  const moving = rows.find(row => row && row.id === movingId);
  const currentParent = (moving && moving.parent_id) || '';
  const destinations = [];
  if (currentParent !== '') {
    destinations.push({ id: '', name: 'Top level', depth: 0 });
  }
  rows
    .filter(row => row && isGroupWorkspace(row))
    .forEach(row => {
      if (row.id === currentParent) return; // already there
      if (!isMoveAllowed(rows, movingId, row.id)) return;
      destinations.push({ id: row.id, name: row.name || 'Group', depth: row.depth || 0 });
    });
  return destinations;
}

/**
 * PATCH payloads that place `movingId` under `targetParentId` and renumber that
 * parent's children, matching the launcher's atomic move contract.
 */
export function moveOrderUpdates(flattened, movingId, targetParentId, beforeId = '') {
  const rows = Array.isArray(flattened) ? flattened : [];
  const parent = targetParentId || '';
  const siblings = rows
    .filter(row => row && (row.parent_id || '') === parent && row.id !== movingId)
    .map(row => row.id);
  const insertAt = beforeId ? siblings.indexOf(beforeId) : siblings.length;
  const ordered = [...siblings];
  ordered.splice(insertAt < 0 ? siblings.length : insertAt, 0, movingId);

  const updates = {};
  ordered.forEach((id, index) => {
    updates[id] = { order_index: index + 1 };
    if (id === movingId) updates[id].parent_id = parent;
  });
  return updates;
}

// ---------------------------------------------------------------------------
// Bulk selection (FR46, FR47, FR48)
// ---------------------------------------------------------------------------

/**
 * Selection state for every row, including group subtree roll-up.
 *
 * A group is `checked` when every descendant is checked, `indeterminate` when
 * only some are, matching the launcher's existing selection rules.
 */
export function bulkSelectionState(flattened, selectedIds) {
  const rows = Array.isArray(flattened) ? flattened : [];
  const selected = selectedIds instanceof Set ? selectedIds : new Set(selectedIds || []);
  const stateById = {};
  rows.forEach(row => {
    if (!row) return;
    const kids = descendantIds(rows, row.id);
    if (kids.length === 0) {
      stateById[row.id] = { checked: selected.has(row.id), indeterminate: false };
      return;
    }
    const checkedKids = kids.filter(id => selected.has(id)).length;
    const selfChecked = selected.has(row.id);
    if (checkedKids === kids.length && (selfChecked || checkedKids > 0)) {
      stateById[row.id] = { checked: true, indeterminate: false };
    } else if (checkedKids > 0 || selfChecked) {
      stateById[row.id] = { checked: false, indeterminate: true };
    } else {
      stateById[row.id] = { checked: false, indeterminate: false };
    }
  });
  return stateById;
}

/**
 * The workspace and group rows a Shift-click selects (FR60): every one on
 * screen from the anchor row to the clicked row, in either direction.
 *
 * `rows` is the visible row list. Content rows in between are skipped — only
 * workspaces and groups can be selected (FR62). With no usable anchor the
 * clicked row alone is returned.
 */
export function rangeSelection(rows, anchorId, toId) {
  const order = (Array.isArray(rows) ? rows : [])
    .filter(row => isWorkspaceRowKind(row.kind))
    .map(row => row.id);
  const to = order.indexOf(toId);
  if (to < 0) return [];
  const from = order.indexOf(anchorId);
  if (from < 0) return [toId];
  return order.slice(Math.min(from, to), Math.max(from, to) + 1);
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

/**
 * What a workspace row shows after its name (FR8).
 *
 * At most one marker: the count of things needing attention, else a dot when
 * the workspace is running or active, else nothing. Personal HQ also carries
 * the label "HQ". `statusLabel` is the same state in words, for screen readers
 * and for anyone who cannot tell a dot from a badge by colour.
 */
export function workspaceMarker(workspace) {
  const ws = workspace || {};
  if (isGroupWorkspace(ws)) {
    return { hq: false, attention: 0, dot: false, statusLabel: 'Group' };
  }
  const signals = workspaceSignals(ws);
  const attention = signals.attention !== null && signals.attention > 0 ? signals.attention : 0;
  return {
    hq: ws.is_personal_hq === true || ws.designation === 'personal_hq',
    attention,
    dot: attention === 0 && (signals.status === 'running' || signals.status === 'active'),
    statusLabel: signals.label
  };
}

function markerHTML(row) {
  if (row.kind === 'workspace') {
    const marker = workspaceMarker(row.workspace);
    return (
      (marker.hq ? '<span class="cockpit-tree-count">HQ</span>' : '') +
      (marker.attention > 0
        ? `<span class="cockpit-tree-badge">${escapeHtml(marker.attention)}</span>`
        : marker.dot
          ? '<span class="cockpit-tree-dot" aria-hidden="true"></span>'
          : '') +
      `<span class="visually-hidden">${escapeHtml(marker.statusLabel)}</span>`
    );
  }
  if (row.kind === 'group') return '<span class="visually-hidden">Group</span>';
  if (row.kind === 'section' || row.kind === 'memory') {
    return row.count === null || row.count === undefined
      ? ''
      : `<span class="cockpit-tree-count">${escapeHtml(row.count)}</span>`;
  }
  if (row.kind === 'ticket') {
    return `<span class="cockpit-tree-count">${escapeHtml((row.meta && row.meta.stateLabel) || '')}</span>`;
  }
  if (row.kind === 'failed') {
    return (
      `<button type="button" class="cockpit-tree-retry" data-tree-retry="${escapeHtml(row.workspaceId)}" ` +
      `data-tree-retry-section="${escapeHtml(row.section)}" tabindex="-1">Retry</button>`
    );
  }
  return '';
}

// Rows that read as secondary: section headings, finished tickets, and the
// loading / empty / failed lines.
function isDimRow(row) {
  if (row.kind === 'ticket') return !!(row.meta && row.meta.finished);
  return ['section', 'loading', 'empty', 'failed', 'more'].includes(row.kind);
}

// What a new item of each kind is called while it is being named.
const DRAFT_COPY = {
  note: { icon: 'note', placeholder: 'Note name', label: 'Name of the new note' },
  ticket: { icon: 'ticket', placeholder: 'Ticket title', label: 'Title of the new ticket' }
};

/**
 * The row where a new note or ticket is named (FR47, FR48): an input in the
 * place of a name. Enter creates the item and Escape cancels; both are handled
 * where the tree is mounted.
 */
function draftRowHTML(row) {
  const copy = DRAFT_COPY[row.draftKind] || DRAFT_COPY.note;
  return (
    '<li class="cockpit-tree-node" role="none">' +
    '<div class="cockpit-tree-row is-kind-draft" role="treeitem" ' +
    `data-tree-row="${escapeHtml(row.id)}" data-tree-kind="draft" ` +
    `data-parent-id="${escapeHtml(row.parentId)}" tabindex="-1" ` +
    `aria-level="${row.depth + 1}" aria-posinset="${row.posInSet}" aria-setsize="${row.setSize}" ` +
    `aria-selected="false" style="--tree-depth:${row.depth}">` +
    '<span class="cockpit-tree-caret-space" aria-hidden="true"></span>' +
    iconHTML(copy.icon) +
    '<input type="text" class="cockpit-tree-input" data-tree-draft autocomplete="off" ' +
    `maxlength="300" value="${escapeHtml(row.name)}" placeholder="${copy.placeholder}" ` +
    `aria-label="${copy.label}. Enter to create, Escape to cancel."${row.busy ? ' disabled' : ''}>` +
    '</div>' +
    '</li>'
  );
}

function rowHTML(row, ctx) {
  if (row.kind === 'draft') return draftRowHTML(row);
  const id = escapeHtml(row.id);
  const name = escapeHtml(row.name);
  const isActive = ctx.activeId === row.id;
  const isWorkspaceRow = isWorkspaceRowKind(row.kind);
  // Picked for a bulk action — a different thing from being the open item, and
  // said differently: `aria-checked` here, `aria-selected` for the open item.
  // A group with only some of its workspaces picked is "mixed" (FR60).
  const bulk = (isWorkspaceRow && ctx.bulkState[row.id]) || {};
  const picked = !!bulk.checked;
  const partlyPicked = !picked && !!bulk.indeterminate;
  const classes = ['cockpit-tree-row', `is-kind-${row.kind}`];
  if (isActive) classes.push('is-active');
  if (picked) classes.push('is-picked');
  if (partlyPicked) classes.push('is-partly-picked');
  if (isDimRow(row)) classes.push('is-dim');
  // The "⋯" button. It is out of the tab order — the keyboard opens the same
  // menu with Shift+F10 or the Menu key on the row itself (FR54).
  const more = rowHasMenu(row)
    ? `<button type="button" class="cockpit-tree-more" data-tree-menu-for="${id}" tabindex="-1" ` +
      `aria-haspopup="menu" aria-label="Actions for ${name}">${iconHTML('more', { size: 16 })}</button>`
    : '';

  return (
    `<li class="cockpit-tree-node${row.isGroup ? ' is-group' : ''}" role="none">` +
    `<div class="${classes.join(' ')}" role="treeitem" ` +
    `data-tree-row="${id}" data-tree-kind="${escapeHtml(row.kind)}" ` +
    `data-parent-id="${escapeHtml(row.parentId)}" ` +
    (isWorkspaceRow
      ? `id="cockpit-tree-row-${id}" data-next-sibling-id="${escapeHtml(row.nextSiblingId)}" draggable="true" `
      : '') +
    `tabindex="${ctx.tabbableId === row.id ? '0' : '-1'}" ` +
    `aria-level="${row.depth + 1}" aria-posinset="${row.posInSet}" aria-setsize="${row.setSize}" ` +
    (row.expandable ? `aria-expanded="${row.expanded ? 'true' : 'false'}" ` : '') +
    `aria-selected="${isActive ? 'true' : 'false'}" ` +
    (isWorkspaceRow
      ? `aria-checked="${picked ? 'true' : partlyPicked ? 'mixed' : 'false'}" `
      : '') +
    `style="--tree-depth:${row.depth}">` +
    (row.expandable
      ? `<button type="button" class="cockpit-tree-caret" data-tree-toggle="${id}" tabindex="-1" ` +
        `aria-label="${row.expanded ? 'Collapse' : 'Expand'} ${name}">` +
        iconHTML(row.expanded ? 'caretDown' : 'caretRight', { size: 14 }) +
        '</button>'
      : '<span class="cockpit-tree-caret-space" aria-hidden="true"></span>') +
    iconHTML(rowIconName(row)) +
    `<span class="cockpit-tree-name" title="${name}">${name}</span>` +
    markerHTML(row) +
    more +
    '</div>' +
    '</li>'
  );
}

function emptyDropHTML(row) {
  return (
    '<li role="none">' +
    `<div class="cockpit-tree-empty-drop" data-tree-drop-into="${escapeHtml(row.id)}" ` +
    `style="--tree-depth:${row.depth + 1}">Empty group. Drop a workspace here.</div>` +
    '</li>'
  );
}

export const FILTER_EMPTY_TEXT = 'Nothing matches that filter.';
export const FILTER_UNSEARCHED_TEXT =
  'Workspaces that have not been expanded yet are searched only after they are expanded.';

/**
 * Render the whole tree.
 *
 * Nesting is expressed with real nested `role="group"` lists so the hierarchy is
 * conveyed structurally, not only by indentation (FR41).
 */
export function renderTreeHTML(rows, ctx) {
  const context = {
    activeId: '',
    tabbableId: (rows[0] && rows[0].id) || '',
    bulkState: {},
    activeTags: new Set(),
    filter: '',
    unsearched: false,
    ...(ctx || {})
  };
  if (!rows.length) {
    // FR67. The second line is there only when it is true: a workspace nobody
    // has expanded has no contents loaded for the filter to look through.
    if (normalizeFilter(context.filter)) {
      return (
        `<p class="cockpit-tree-empty">${FILTER_EMPTY_TEXT}</p>` +
        (context.unsearched
          ? `<p class="cockpit-tree-empty-note">${FILTER_UNSEARCHED_TEXT}</p>`
          : '')
      );
    }
    return context.activeTags && context.activeTags.size > 0
      ? '<p class="cockpit-tree-empty">No workspaces match the selected tags.</p>'
      : '<p class="cockpit-tree-empty">No workspaces yet.</p>';
  }

  // Rebuild nesting from the flat visible-row list.
  let index = 0;
  const build = depth => {
    let html = '';
    while (index < rows.length && rows[index].depth === depth) {
      const row = rows[index];
      index += 1;
      let children = index < rows.length && rows[index].depth === depth + 1 ? build(depth + 1) : '';
      // An open group with no workspaces in it offers itself as a drop target,
      // ahead of any sections of its own.
      if (row.isGroup && row.expanded && !row.hasChildren) children = emptyDropHTML(row) + children;
      const node = rowHTML(row, context);
      html += children
        ? node.replace(
            /<\/li>$/,
            `<ul class="cockpit-tree-children" role="group">${children}</ul></li>`
          )
        : node;
    }
    return html;
  };

  return (
    '<ul class="cockpit-tree-root" role="tree" aria-label="Workspaces and their contents" aria-multiselectable="true">' +
    build(0) +
    '</ul>'
  );
}

/** The Move dialog's destination list. */
export function renderMoveDialogHTML(movingName, destinations) {
  if (!destinations.length) {
    return (
      `<p class="cockpit-tree-move-empty">There is nowhere to move "${escapeHtml(movingName)}". ` +
      'Create a group first.</p>'
    );
  }
  return (
    `<p class="cockpit-tree-move-intro">Move <strong>${escapeHtml(movingName)}</strong> to:</p>` +
    '<ul class="cockpit-tree-move-list">' +
    destinations
      .map(
        dest =>
          '<li><button type="button" class="cockpit-tree-move-option" ' +
          `data-tree-move-to="${escapeHtml(dest.id)}" style="--tree-depth:${dest.depth}">` +
          `${escapeHtml(dest.name)}</button></li>`
      )
      .join('') +
    '</ul>'
  );
}

// ---------------------------------------------------------------------------
// Keyboard navigation (FR126, FR127)
// ---------------------------------------------------------------------------

/**
 * Resolve an arrow-key press against the visible rows.
 *
 * Returns { focusId } to move roving focus, { toggle } to expand/collapse, or
 * null when the key is not ours. ArrowRight on a closed row opens it; on an
 * open one it steps in. ArrowLeft closes, or climbs to the parent. Arrow keys
 * never open anything in the pane (FR72).
 */
export function resolveTreeKey(key, currentId, rows) {
  const list = Array.isArray(rows) ? rows : [];
  const at = list.findIndex(row => row.id === currentId);
  if (at < 0) return null;
  const row = list[at];
  // Older callers describe an expandable row as `isGroup`.
  const expandable = row.expandable === undefined ? !!row.isGroup : !!row.expandable;

  if (key === 'ArrowDown') {
    return at + 1 < list.length ? { focusId: list[at + 1].id } : null;
  }
  if (key === 'ArrowUp') {
    return at > 0 ? { focusId: list[at - 1].id } : null;
  }
  if (key === 'Home') return list.length ? { focusId: list[0].id } : null;
  if (key === 'End') return list.length ? { focusId: list[list.length - 1].id } : null;
  if (key === 'ArrowRight') {
    if (expandable && !row.expanded) return { toggle: row.id, expand: true };
    if (expandable && row.expanded && at + 1 < list.length && list[at + 1].depth > row.depth) {
      return { focusId: list[at + 1].id };
    }
    return null;
  }
  if (key === 'ArrowLeft') {
    if (expandable && row.expanded) return { toggle: row.id, expand: false };
    if (row.parentId) return { focusId: row.parentId };
    return null;
  }
  return null;
}

// ---------------------------------------------------------------------------
// Mount + interaction
// ---------------------------------------------------------------------------

/**
 * What activating a row does (a click on its name, or Enter) (FR22-FR24).
 *
 *   'open'    open the row in the pane; a workspace or group also expands
 *   'toggle'  expand or collapse, nothing else (sections and folders)
 *   'retry'   reload the section that failed
 *   'visit'   go to the workspace page (the "Open workspace to see all N" row)
 *   ''        nothing (the loading and empty lines)
 */
export function rowActivation(row) {
  const kind = row && row.kind;
  if (isWorkspaceRowKind(kind)) return 'open';
  if (kind === 'section' || kind === 'folder') return 'toggle';
  if (kind === 'failed') return 'retry';
  if (kind === 'more') return 'visit';
  // The naming row is an input; clicking it must not open or toggle anything.
  if (kind === 'loading' || kind === 'empty' || kind === 'draft') return '';
  return kind ? 'open' : '';
}

/**
 * The workspace or group the "current row" belongs to (FR50).
 *
 * The header's New note button acts on it. The current row is the one with
 * keyboard focus, else the open tab's, else Home's selection; '' when none of
 * those names a workspace that still exists.
 */
export function contextWorkspaceId(state) {
  const live = new Set(
    (Array.isArray(state && state.flattened) ? state.flattened : []).map(ws => ws && ws.id)
  );
  for (const key of [
    state && state.focusId,
    state && state.activeTabKey,
    state && state.selectedId
  ]) {
    const parsed = parseItemKey(key);
    if (parsed && live.has(parsed.workspaceId)) return parsed.workspaceId;
  }
  return '';
}

/**
 * The row to highlight: the open tab's item, or — with no tab open — Home's
 * selected workspace, so a selection made on the Map is still visible here.
 */
export function treeActiveRowId(state) {
  return String((state && (state.activeTabKey || state.selectedId)) || '');
}

// One set of delegated listeners per host. Re-mounting aborts the previous set
// before binding again, so repeated renders cannot stack listeners (FR122).
const bindings = new WeakMap();

/**
 * Mount the Tree into its column of the cockpit's workspace-area slot.
 *
 * `callbacks` is the seam back to the coordinator: the Tree never mutates
 * shared state or refetches for itself.
 *
 *   onOpenItem(row, { keyboard })  open a row in the pane (a workspace or
 *                                  group row also becomes Home's selection)
 *   onOpen(id)                     go to a workspace's own page
 *   onRetry(workspaceId, section)  reload a section that failed
 *   onMenuAction(action, row)      an item was chosen from a row's menu, or the
 *                                  header's New note was pressed
 *   onDraftCommit(draft)           Enter in the row being named; `draft.value`
 *                                  is the name typed (`state.treeDraft` is the
 *                                  row being named, set by the coordinator)
 *   onRerender()                   the Tree's own state changed; draw it again
 *   onChanged()                    a mutation landed; reload authoritative state
 *   onAnnounce(message)            polite live-region message
 *
 * Returns the visible rows plus `openMoveDialog(id)` and `deleteWorkspace(id)`,
 * which the pane's overview buttons and the row menu call.
 *
 * Idempotent: re-mounting replaces the column's content and rebinds.
 */
export function mountTree(container, state, callbacks) {
  if (!container) return null;
  const cb = callbacks || {};
  const collapsed = state.collapsedGroups instanceof Set ? state.collapsedGroups : new Set();
  const expanded = state.expandedRows instanceof Set ? state.expandedRows : new Set();
  const selected = state.bulkSelection instanceof Set ? state.bulkSelection : new Set();
  const activeTags = state.activeTags instanceof Set ? state.activeTags : new Set();
  const visibleTree = filterTreeByTags(
    state.tree,
    (state.metadata && state.metadata.tagsById) || {},
    activeTags
  );
  const filter = normalizeFilter(state.treeFilter);
  const rows = visibleTreeRows(visibleTree, collapsed, 0, '', {
    expanded,
    contents: state.treeContents || {},
    draft: state.treeDraft || null,
    filter
  });
  const activeId = treeActiveRowId(state);

  // Roving tabindex: exactly one row is tabbable, and it follows the active
  // item when there is one (FR127).
  const tabbableId = rows.some(r => r.id === state.focusId)
    ? state.focusId
    : rows.some(r => r.id === activeId)
      ? activeId
      : (rows[0] && rows[0].id) || '';

  // The rows are redrawn on every change; the place the user had scrolled to
  // must survive that, and so must the cursor in a name being typed.
  const previousScroller = container.querySelector('.cockpit-tree-scroll');
  const scrollTop = previousScroller ? previousScroller.scrollTop : 0;
  const typing = document.activeElement;
  const draftHadFocus =
    !!typing && container.contains(typing) && typing.hasAttribute('data-tree-draft');
  const draftCaret = draftHadFocus ? typing.selectionStart : null;
  // The Move dialog is drawn with the tree, so it too keeps focus over a
  // redraw. A row that has gone (deleted meanwhile) takes its dialog with it.
  const moveHadFocus =
    !!typing && container.contains(typing) && !!typing.closest('[data-tree-move-dialog]');
  const moving = state.treeMoveId
    ? (state.flattened || []).find(ws => ws && ws.id === state.treeMoveId) || null
    : null;
  if (!moving) state.treeMoveId = '';

  const toolbarHTML = renderToolbarHTML(state);
  const belowFilterHTML =
    renderTagFilterBarHTML(state) +
    renderBulkBarHTML(selected.size) +
    '<div class="cockpit-tree-scroll">' +
    renderTreeHTML(rows, {
      activeId,
      tabbableId,
      bulkState: bulkSelectionState(state.flattened, selected),
      activeTags,
      filter,
      unsearched: hasUnsearchedContents(visibleTree, state.treeContents)
    }) +
    '<div class="cockpit-tree-root-drop" data-tree-drop-into="">Drop here to move to the top level</div>' +
    '</div>' +
    (moving
      ? '<div class="cockpit-tree-move-dialog" data-tree-move-dialog>' +
        `<div class="cockpit-tree-move-panel" role="dialog" aria-label="Move ${escapeHtml(moving.name || 'workspace')}">` +
        renderMoveDialogHTML(
          moving.name || 'this workspace',
          moveDestinations(state.flattened, moving.id)
        ) +
        '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-tree-move-cancel>Cancel</button>' +
        '</div></div>'
      : '<div class="cockpit-tree-move-dialog" data-tree-move-dialog hidden></div>');

  // The filter box is the one part of the column that is never redrawn. The
  // tree redraws on every character typed into it, and replacing the input
  // would drop its cursor and break text being composed (an IME).
  redrawing.add(container);
  const filterBox = container.querySelector(':scope > [data-tree-filter-box]');
  if (filterBox) {
    Array.from(container.children).forEach(child => {
      if (child !== filterBox) child.remove();
    });
    filterBox.insertAdjacentHTML('beforebegin', toolbarHTML);
    filterBox.insertAdjacentHTML('afterend', belowFilterHTML);
  } else {
    container.innerHTML = toolbarHTML + renderFilterBoxHTML() + belowFilterHTML;
  }
  redrawing.delete(container);
  syncFilterBox(container, state.treeFilter);

  // "Nothing matches" is said once, when the tree first runs out of matches.
  const nothingMatches = filter !== '' && rows.length === 0;
  if (nothingMatches && !state.treeFilterEmpty && typeof cb.onAnnounce === 'function') {
    cb.onAnnounce(FILTER_EMPTY_TEXT);
  }
  state.treeFilterEmpty = nothingMatches;

  const scroller = container.querySelector('.cockpit-tree-scroll');
  if (scroller && scrollTop) scroller.scrollTop = scrollTop;

  if (moving && (state.treeMoveFocus || moveHadFocus)) {
    state.treeMoveFocus = false;
    const first = container.querySelector('[data-tree-move-dialog] button');
    if (first) first.focus();
  }

  // A row being named takes focus when it first appears, and keeps it across
  // redraws (a section finishing its load must not interrupt typing).
  const draft = state.treeDraft || null;
  const draftInput = container.querySelector('[data-tree-draft]');
  if (draft && draftInput && !draftInput.disabled && (draft.pendingFocus || draftHadFocus)) {
    draft.pendingFocus = false;
    draftInput.focus();
    const caret = draftCaret === null ? draftInput.value.length : draftCaret;
    draftInput.setSelectionRange(caret, caret);
  }

  const actions = bindTree(container, state, cb, rows);
  return { rows, tabbableId, ...actions };
}

// Columns whose rows are being replaced right now. Removing a focused input
// can fire its blur; that blur is the redraw's doing, not the user leaving.
const redrawing = new WeakSet();

/**
 * Whether the toolbar's Undo has anything to restore.
 *
 * The cockpit records every trashed delete on `state.undoStack` (FR52). The
 * button used to read a `canUndo` flag nothing ever set, so it stayed disabled
 * for every delete; the stack is the real source. An explicit `canUndo` still
 * wins so a host can force the state either way.
 */
export function treeCanUndo(state) {
  if (!state) return false;
  if (typeof state.canUndo === 'boolean') return state.canUndo;
  return Array.isArray(state.undoStack) && state.undoStack.length > 0;
}

// A header action: an icon with its name as the accessible name and tooltip,
// so seven of them fit the tree column and each is still named (FR53, FR64).
function toolbarButtonHTML(label, icon, attributes) {
  return (
    `<button type="button" class="cockpit-tree-tool" aria-label="${escapeHtml(label)}" ` +
    `title="${escapeHtml(label)}" ${attributes}>${iconHTML(icon, { size: 16 })}</button>`
  );
}

const NEW_NOTE_HINT = 'Pick a workspace first';

/**
 * The header's New note button (FR47, FR50).
 *
 * It acts on the workspace the current row belongs to. With none in context it
 * is disabled — `aria-disabled`, not `disabled`, so it can still be reached and
 * its tooltip says what is missing.
 */
function newNoteButtonHTML(enabled) {
  return (
    '<button type="button" class="cockpit-tree-tool" data-tree-new-note aria-label="New note" ' +
    `title="${enabled ? 'New note' : NEW_NOTE_HINT}" aria-disabled="${enabled ? 'false' : 'true'}">` +
    `${iconHTML('newNote', { size: 16 })}</button>`
  );
}

function renderToolbarHTML(state) {
  const root = state.workspaceRoot || {};
  const rootLabel =
    root.state === 'loading'
      ? 'Loading workspace directory…'
      : root.state === 'unavailable'
        ? 'Workspace directory unavailable'
        : root.path || 'No workspace directory set';
  // "Not confirmed" is a real, distinct state: the server reports a default
  // location the user has not yet accepted. Labelling that "Built-in" would
  // imply a setting that does not exist yet (FR39).
  const rootBadge =
    root.state === 'loading'
      ? 'Loading'
      : root.state === 'unavailable'
        ? 'Unavailable'
        : root.custom
          ? 'Custom'
          : root.confirmed === false
            ? 'Not confirmed'
            : 'Built-in';
  return (
    '<div class="cockpit-tree-toolbar">' +
    '<div class="cockpit-tree-root-info">' +
    `<span class="cockpit-tree-root-badge is-${escapeHtml(String(root.state || 'ready'))}">${escapeHtml(rootBadge)}</span>` +
    `<span class="cockpit-tree-root-path" title="${escapeHtml(rootLabel)}">${escapeHtml(rootLabel)}</span>` +
    '</div>' +
    '<div class="cockpit-tree-toolbar-actions" role="group" aria-label="Workspace tree actions">' +
    newNoteButtonHTML(contextWorkspaceId(state) !== '') +
    toolbarButtonHTML(
      'Create Workspace',
      'newWorkspace',
      'data-bs-toggle="modal" data-bs-target="#addFolderModal" data-workspace-import-mode="false" data-workspace-entry-point="home_cockpit_tree_create"'
    ) +
    toolbarButtonHTML(
      'Create Group',
      'newGroup',
      'data-tree-new-group aria-haspopup="dialog" aria-controls="addFolderModal"'
    ) +
    toolbarButtonHTML(
      'Import Folder',
      'importFolder',
      'data-bs-toggle="modal" data-bs-target="#addFolderModal" data-workspace-import-mode="true" data-workspace-entry-point="home_cockpit_tree_import"'
    ) +
    toolbarButtonHTML('Rescan', 'rescan', 'data-tree-rescan') +
    `<a class="cockpit-tree-tool" href="/settings" aria-label="Manage directory" title="Manage directory">${iconHTML('settings', { size: 16 })}</a>` +
    toolbarButtonHTML('Undo', 'undo', `data-tree-undo ${treeCanUndo(state) ? '' : 'disabled'}`) +
    '</div>' +
    '</div>'
  );
}

/**
 * The filter box above the tree (FR65).
 *
 * Drawn once and then left in place: `mountTree` redraws everything around
 * it. The clear button is shown only while there is something to clear.
 */
export function renderFilterBoxHTML() {
  return (
    '<div class="cockpit-tree-filter" data-tree-filter-box role="search">' +
    iconHTML('filter', { size: 14, className: 'cockpit-tree-filter-icon' }) +
    '<input type="text" class="cockpit-tree-filter-input" data-tree-filter ' +
    'placeholder="Filter" aria-label="Filter the tree by name" ' +
    'autocomplete="off" autocapitalize="off" spellcheck="false" enterkeyhint="search">' +
    '<button type="button" class="cockpit-tree-filter-clear" data-tree-filter-clear ' +
    `aria-label="Clear the filter" title="Clear the filter" hidden>${iconHTML('close', { size: 12 })}</button>` +
    '</div>'
  );
}

// Bring the box in line with the state: its text when something other than
// typing changed it (the clear button, Escape), and the clear button itself.
function syncFilterBox(container, text) {
  const value = String(text || '');
  const input = container.querySelector('[data-tree-filter]');
  if (input && input.value !== value && document.activeElement !== input) input.value = value;
  const clear = container.querySelector('[data-tree-filter-clear]');
  if (clear) clear.hidden = value === '';
}

/**
 * Tag filter bar. Only rendered when tags exist, and it always offers a clear
 * action so a filter can never trap the user (FR54).
 */
export function renderTagFilterBarHTML(state) {
  const tagsById = (state.metadata && state.metadata.tagsById) || {};
  const active = state.activeTags instanceof Set ? state.activeTags : new Set();
  const all = new Set();
  Object.keys(tagsById).forEach(id => (tagsById[id] || []).forEach(tag => all.add(tag)));
  if (all.size === 0) return '<div class="cockpit-tree-tagbar" hidden></div>';
  return (
    '<div class="cockpit-tree-tagbar" role="group" aria-label="Filter workspaces by tag">' +
    '<span class="cockpit-tree-tagbar-label">Tags</span>' +
    Array.from(all)
      .sort()
      .map(
        tag =>
          `<button type="button" class="cockpit-tree-tagbar-chip" data-tree-tag-filter="${escapeHtml(tag)}" ` +
          `aria-pressed="${active.has(tag) ? 'true' : 'false'}">${escapeHtml(tag)}</button>`
      )
      .join('') +
    (active.size > 0
      ? '<button type="button" class="cockpit-tree-tagbar-clear" data-tree-tag-clear>Clear tag filters</button>'
      : '') +
    '</div>'
  );
}

/**
 * Keep only the branches that contain a matching workspace.
 *
 * A group survives when any descendant matches, so filtering never hides the
 * path to a match. An empty result is reported honestly rather than as an
 * empty tree (FR54).
 */
export function filterTreeByTags(nodes, tagsById, activeTags) {
  const active = activeTags instanceof Set ? activeTags : new Set(activeTags || []);
  if (active.size === 0) return Array.isArray(nodes) ? nodes : [];
  const walk = list =>
    (Array.isArray(list) ? list : [])
      .map(node => {
        if (!node) return null;
        const children = walk(node.children);
        const own = (tagsById[node.id] || []).some(tag => active.has(tag));
        if (!own && children.length === 0) return null;
        return { ...node, children };
      })
      .filter(Boolean);
  return walk(nodes);
}

function renderBulkBarHTML(count) {
  if (count === 0) {
    return '<div class="cockpit-tree-bulkbar" data-tree-bulkbar hidden></div>';
  }
  return (
    '<div class="cockpit-tree-bulkbar" data-tree-bulkbar role="group" aria-label="Bulk actions">' +
    `<span class="cockpit-tree-bulkcount">${count} selected</span>` +
    '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-tree-select-all>Select all</button>' +
    '<button type="button" class="modern-btn modern-btn-primary modern-btn-sm" data-tree-group-selected aria-haspopup="dialog" aria-controls="addFolderModal">Group selected</button>' +
    '<button type="button" class="modern-btn modern-btn-danger modern-btn-sm" data-tree-delete-selected>Delete selected</button>' +
    '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-tree-cancel-selection>Cancel</button>' +
    '</div>'
  );
}

function bindTree(container, state, cb, rows) {
  const previous = bindings.get(container);
  if (previous) previous.abort();
  const controller = new AbortController();
  bindings.set(container, controller);
  const on = (type, handler) =>
    container.addEventListener(type, handler, { signal: controller.signal });

  const rowsById = new Map(rows.map(row => [row.id, row]));
  const rowFor = el => {
    const host = el && el.closest ? el.closest('[data-tree-row]') : null;
    return host ? rowsById.get(host.getAttribute('data-tree-row')) || null : null;
  };

  const announce = msg => {
    if (typeof cb.onAnnounce === 'function') cb.onAnnounce(msg);
  };

  const focusRow = id => {
    const el = container.querySelector(`[data-tree-row="${cssEscape(id)}"]`);
    if (!el) return;
    container.querySelectorAll('[data-tree-row]').forEach(r => r.setAttribute('tabindex', '-1'));
    el.setAttribute('tabindex', '0');
    el.focus();
    state.focusId = id;
    syncNewNoteButton();
  };

  // The header's New note button follows the current row, which arrow keys
  // change without a redraw (FR50).
  const syncNewNoteButton = () => {
    const button = container.querySelector('[data-tree-new-note]');
    if (!button) return;
    const enabled = contextWorkspaceId(state) !== '';
    button.setAttribute('aria-disabled', enabled ? 'false' : 'true');
    button.setAttribute('title', enabled ? 'New note' : NEW_NOTE_HINT);
  };

  // --- the row menu (FR54) -------------------------------------------------
  const openMenuFor = (row, at, event) => {
    const items = menuItemsFor(row);
    if (items.length === 0) return false;
    const findOrigin = () =>
      Array.from(container.querySelectorAll('[data-tree-row]')).find(
        el => el.getAttribute('data-tree-row') === row.id
      ) || null;
    state.focusId = row.id;
    return openRowMenu({
      items,
      label: `Actions for ${row.name}`,
      at,
      origin: findOrigin(),
      findOrigin,
      event,
      onChoose: action => {
        // What the tree can do itself it does; creating, refreshing and the
        // rest go back to the coordinator.
        if (action === MENU_OPEN) {
          // Opened from the keyboard, the item takes focus as it does on Enter.
          activateRow(row, { keyboard: !!event && event.type === 'keydown' });
        } else if (action === MENU_OPEN_PAGE) {
          if (typeof cb.onOpen === 'function') cb.onOpen(row.id);
        } else if (action === MENU_MOVE) {
          openMoveDialog(row.id);
        } else if (action === MENU_DELETE) {
          void performDelete(row.id);
        } else if (typeof cb.onMenuAction === 'function') {
          cb.onMenuAction(action, row);
        }
      }
    });
  };

  on('contextmenu', e => {
    const row = rowFor(e.target);
    // A row with no menu keeps the browser's own; so does the naming input.
    if (!row || e.target.closest('[data-tree-draft]')) return;
    if (openMenuFor(row, { x: e.clientX, y: e.clientY }, e)) e.preventDefault();
  });

  // --- the filter box (FR65) -------------------------------------------------
  // The tree narrows as the text changes. While text is being composed (an
  // IME) the input reports every keystroke; the tree waits for the composed
  // text, which arrives with `compositionend`.
  const applyFilter = value => {
    if (state.treeFilter === value) return;
    state.treeFilter = value;
    rerender();
  };
  on('input', e => {
    if (!e.target.matches('[data-tree-filter]') || e.isComposing) return;
    applyFilter(e.target.value);
  });
  on('compositionend', e => {
    if (e.target.matches('[data-tree-filter]')) applyFilter(e.target.value);
  });

  function clearFilter() {
    const input = container.querySelector('[data-tree-filter]');
    if (input) {
      input.value = '';
      input.focus();
    }
    applyFilter('');
  }

  // --- the row being named (FR47, FR48) ------------------------------------
  on('input', e => {
    if (!e.target.matches('[data-tree-draft]') || !state.treeDraft) return;
    state.treeDraft.value = e.target.value;
  });
  on('focusout', e => {
    if (!e.target.matches || !e.target.matches('[data-tree-draft]')) return;
    if (redrawing.has(container) || !state.treeDraft || state.treeDraft.busy) return;
    // Leaving an empty name is a cancel. A typed name is kept: it is only
    // created by Enter and only thrown away by Escape.
    if (e.target.value.trim() !== '') return;
    cancelDraft({ refocus: false });
  });

  function cancelDraft({ refocus = true } = {}) {
    const draft = state.treeDraft;
    if (!draft) return;
    state.treeDraft = null;
    if (refocus) state.focusId = sectionKey(draft.workspaceId, draft.section);
    rerender();
    if (refocus) focusAfterRerender(state.focusId);
  }

  // rerender() replaces this column and its bindings, so the row is found
  // again in the live document rather than through this closure.
  function focusAfterRerender(id) {
    const el = Array.from(container.querySelectorAll('[data-tree-row]')).find(
      node => node.getAttribute('data-tree-row') === id
    );
    if (el) el.focus();
  }

  // --- clicks --------------------------------------------------------------
  const clickActions = [
    // Expand / collapse only: the caret never selects or opens (FR23).
    ['[data-tree-toggle]', el => toggleRow(rowsById.get(el.getAttribute('data-tree-toggle')))],
    ['[data-tree-filter-clear]', () => clearFilter()],
    [
      '[data-tree-menu-for]',
      el => {
        const row = rowsById.get(el.getAttribute('data-tree-menu-for'));
        const rect = el.getBoundingClientRect();
        if (row) openMenuFor(row, { x: rect.left, y: rect.bottom + 2 }, null);
      }
    ],
    [
      '[data-tree-new-note]',
      () => {
        const workspaceId = contextWorkspaceId(state);
        if (!workspaceId) {
          announce(`${NEW_NOTE_HINT}.`);
          return;
        }
        if (typeof cb.onMenuAction === 'function') {
          cb.onMenuAction('new-note', { kind: 'workspace', id: workspaceId, workspaceId });
        }
      }
    ],
    [
      '[data-tree-retry]',
      el => retry(el.getAttribute('data-tree-retry'), el.getAttribute('data-tree-retry-section'))
    ],
    [
      '[data-tree-tag-filter]',
      el => {
        const tag = el.getAttribute('data-tree-tag-filter');
        if (state.activeTags.has(tag)) state.activeTags.delete(tag);
        else state.activeTags.add(tag);
        rerender();
      }
    ],
    [
      '[data-tree-tag-clear]',
      () => {
        state.activeTags.clear();
        rerender();
      }
    ],
    ['[data-tree-new-group]', () => void createGroup([])],
    ['[data-tree-rescan]', () => void rescan()],
    ['[data-tree-undo]', () => void undoLast()],
    [
      '[data-tree-select-all]',
      () => {
        state.flattened.forEach(row => state.bulkSelection.add(row.id));
        rerender();
      }
    ],
    [
      '[data-tree-cancel-selection]',
      () => {
        state.bulkSelection.clear();
        rerender();
      }
    ],
    ['[data-tree-group-selected]', () => void createGroup(Array.from(state.bulkSelection))],
    ['[data-tree-delete-selected]', () => void deleteSelected()],
    [
      '[data-tree-move-to]',
      el => {
        const id = state.treeMoveId;
        state.treeMoveId = '';
        state.focusId = id;
        rerender();
        focusAfterRerender(id);
        void performMove(id, el.getAttribute('data-tree-move-to'));
      }
    ],
    ['[data-tree-move-cancel]', () => closeMoveDialog()]
  ];

  on('click', e => {
    for (const [selector, handler] of clickActions) {
      const el = e.target.closest(selector);
      if (el && container.contains(el)) {
        handler(el);
        return;
      }
    }
    const row = rowFor(e.target);
    if (!row) return;
    // Multi-select is for workspace and group rows only; on a content row a
    // modified click is a plain click (FR60, FR62).
    if (isWorkspaceRowKind(row.kind) && (e.metaKey || e.ctrlKey)) {
      toggleBulk(row.id);
      return;
    }
    if (isWorkspaceRowKind(row.kind) && e.shiftKey) {
      selectBulkRange(row.id);
      return;
    }
    activateRow(row, { keyboard: false });
  });

  // --- keyboard ------------------------------------------------------------
  // Arrow keys step over the row being named: it is typed in, not walked to.
  const walkable = rows.filter(row => row.kind !== 'draft');

  on('keydown', e => {
    // The row being named: Enter creates it, Escape cancels.
    if (e.target.matches && e.target.matches('[data-tree-draft]')) {
      if (e.key === 'Enter') {
        e.preventDefault();
        const draft = state.treeDraft;
        if (draft && !draft.busy && typeof cb.onDraftCommit === 'function') {
          cb.onDraftCommit({ ...draft, value: e.target.value });
        }
      } else if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        cancelDraft();
      }
      return;
    }

    // In the filter box: Escape clears it, and Down or Enter goes to the rows.
    if (e.target.matches && e.target.matches('[data-tree-filter]')) {
      if (e.isComposing) return;
      if (e.key === 'Escape' && e.target.value !== '') {
        e.preventDefault();
        e.stopPropagation();
        clearFilter();
      } else if (e.key === 'ArrowDown' || e.key === 'Enter') {
        const first = container.querySelector('[data-tree-row][tabindex="0"]');
        if (first) {
          e.preventDefault();
          first.focus();
        }
      }
      return;
    }

    // Escape closes the Move dialog and returns to the row being moved.
    if (e.key === 'Escape' && e.target.closest && e.target.closest('[data-tree-move-dialog]')) {
      e.preventDefault();
      e.stopPropagation();
      closeMoveDialog();
      return;
    }

    // Only keys pressed on the row itself: a focused Retry button keeps its
    // own Enter and Space.
    if (!e.target.matches || !e.target.matches('[data-tree-row]')) return;
    const row = rowFor(e.target);
    if (!row) return;

    // The Menu key, or Shift+F10, opens the row's menu at the row (FR54).
    if (e.key === 'ContextMenu' || (e.key === 'F10' && e.shiftKey)) {
      const rect = e.target.getBoundingClientRect();
      if (openMenuFor(row, { x: rect.left + 24, y: rect.bottom }, e)) e.preventDefault();
      return;
    }

    // Space adds a workspace or group to the bulk selection; Enter does what a
    // click on the name does (FR72, FR126).
    if (e.key === ' ' || e.key === 'Spacebar') {
      e.preventDefault();
      if (isWorkspaceRowKind(row.kind)) toggleBulk(row.id);
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      activateRow(row, { keyboard: true });
      return;
    }
    const action = resolveTreeKey(e.key, row.id, walkable);
    if (!action) return;
    e.preventDefault();
    if (action.focusId) {
      focusRow(action.focusId);
      return;
    }
    if (action.toggle) toggleRow(rowsById.get(action.toggle), action.expand);
  });

  // --- drag and drop (workspace and group rows only, FR57, FR62) -----------
  //
  // A row exposes three drop zones, which together with the top-level drop
  // strip give the launcher's full destination set — before, after, into a
  // group, and back to the top level (FR49). The pointer's position within the
  // row picks which one.
  const dropRowFor = el => {
    const host = el && el.closest ? el.closest('[data-tree-row]') : null;
    return host && isWorkspaceRowKind(host.getAttribute('data-tree-kind')) ? host : null;
  };
  const dropIntent = (rowEl, e) => {
    const id = rowEl.getAttribute('data-tree-row');
    const rect = rowEl.getBoundingClientRect();
    const offset = rect.height > 0 ? (e.clientY - rect.top) / rect.height : 0.5;
    const isGroupRow = rowEl.getAttribute('data-tree-kind') === 'group';
    const ownParent = rowEl.getAttribute('data-parent-id') || '';
    if (isGroupRow && offset > 0.25 && offset < 0.75) {
      return { parent: id, before: '', zone: 'into' };
    }
    if (offset < 0.5) return { parent: ownParent, before: id, zone: 'before' };
    return {
      parent: ownParent,
      before: rowEl.getAttribute('data-next-sibling-id') || '',
      zone: 'after'
    };
  };
  const clearDropClasses = el =>
    el.classList.remove('is-drop-target', 'is-drop-before', 'is-drop-after', 'is-drop-into');
  const endDrag = () => {
    state.draggingId = '';
    container.classList.remove('is-dragging');
  };

  on('dragstart', e => {
    const rowEl = dropRowFor(e.target);
    if (!rowEl) return;
    state.draggingId = rowEl.getAttribute('data-tree-row');
    container.classList.add('is-dragging');
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move';
      try {
        e.dataTransfer.setData('text/plain', state.draggingId);
      } catch (_) {
        /* some browsers restrict this outside user gestures */
      }
    }
  });
  on('dragend', endDrag);

  on('dragover', e => {
    if (!state.draggingId) return;
    const zone = e.target.closest('[data-tree-drop-into]');
    if (zone) {
      const targetParent = zone.getAttribute('data-tree-drop-into');
      if (!isMoveAllowed(state.flattened, state.draggingId, targetParent)) return;
      e.preventDefault();
      zone.classList.add('is-drop-target');
      return;
    }
    const rowEl = dropRowFor(e.target);
    if (!rowEl) return;
    const intent = dropIntent(rowEl, e);
    if (!isMoveAllowed(state.flattened, state.draggingId, intent.parent)) return;
    e.preventDefault();
    clearDropClasses(rowEl);
    rowEl.classList.add('is-drop-target', `is-drop-${intent.zone}`);
  });

  on('dragleave', e => {
    const zone = e.target.closest('[data-tree-drop-into]');
    if (zone) {
      zone.classList.remove('is-drop-target');
      return;
    }
    const rowEl = dropRowFor(e.target);
    if (rowEl) clearDropClasses(rowEl);
  });

  on('drop', e => {
    const movingId = state.draggingId;
    const zone = e.target.closest('[data-tree-drop-into]');
    const rowEl = zone ? null : dropRowFor(e.target);
    if (!zone && !rowEl) return;
    e.preventDefault();
    endDrag();
    if (zone) {
      zone.classList.remove('is-drop-target');
      if (movingId) void performMove(movingId, zone.getAttribute('data-tree-drop-into'));
      return;
    }
    clearDropClasses(rowEl);
    if (!movingId || movingId === rowEl.getAttribute('data-tree-row')) return;
    const intent = dropIntent(rowEl, e);
    void performMove(movingId, intent.parent, intent.before);
  });

  // ---- behaviors ---------------------------------------------------------

  function rerender() {
    if (typeof cb.onRerender === 'function') cb.onRerender();
  }

  function toggleRow(row, open) {
    if (!row || !row.expandable) return;
    setRowExpanded(state, row.kind, row.id, open === undefined ? !row.expanded : open);
    state.focusId = row.id;
    rerender();
  }

  function retry(workspaceId, sectionId) {
    if (typeof cb.onRetry === 'function') cb.onRetry(workspaceId, sectionId);
  }

  function activateRow(row, { keyboard }) {
    state.focusId = row.id;
    const action = rowActivation(row);
    if (action === 'toggle') {
      toggleRow(row);
    } else if (action === 'retry') {
      retry(row.workspaceId, row.section);
    } else if (action === 'visit') {
      if (typeof cb.onOpen === 'function') cb.onOpen(row.workspaceId);
    } else if (action === 'open') {
      // A workspace or group opens its overview AND expands (FR23).
      if (isWorkspaceRowKind(row.kind)) setRowExpanded(state, row.kind, row.id, true);
      if (typeof cb.onOpenItem === 'function') cb.onOpenItem(row, { keyboard });
      else rerender();
    }
  }

  // Cmd/Ctrl-click, or Space on the row: add a workspace or group to the bulk
  // selection, or take it out (FR60).
  function toggleBulk(id) {
    if (state.bulkSelection.has(id)) state.bulkSelection.delete(id);
    else state.bulkSelection.add(id);
    // Selecting a group selects its whole subtree, matching the launcher.
    descendantIds(state.flattened, id).forEach(childId => {
      if (state.bulkSelection.has(id)) state.bulkSelection.add(childId);
      else state.bulkSelection.delete(childId);
    });
    // The next Shift-click selects from here.
    state.bulkAnchorId = id;
    state.focusId = id;
    rerender();
  }

  // Shift-click: select every workspace and group row from the last row
  // toggled (or focused) to this one (FR60).
  function selectBulkRange(toId) {
    rangeSelection(rows, state.bulkAnchorId || state.focusId, toId).forEach(id => {
      state.bulkSelection.add(id);
      descendantIds(state.flattened, id).forEach(childId => state.bulkSelection.add(childId));
    });
    state.focusId = toId;
    rerender();
  }

  /**
   * Open the Move dialog for a workspace or group: the list of legal
   * destinations, so moving never depends on drag-and-drop (FR56).
   *
   * Which row is being moved is kept in `state.treeMoveId` and the dialog is
   * drawn with the tree, so a redraw (a section finishing its load) does not
   * close a dialog that is open.
   */
  function openMoveDialog(id) {
    state.treeMoveId = id;
    state.treeMoveFocus = true;
    rerender();
  }

  function closeMoveDialog() {
    const id = state.treeMoveId;
    state.treeMoveId = '';
    state.focusId = id;
    rerender();
    focusAfterRerender(id);
  }

  async function performMove(movingId, targetParentId, beforeId = '') {
    const reason = moveRejectionReason(state.flattened, movingId, targetParentId);
    if (reason) {
      announce(reason);
      toast(reason, 'error');
      return;
    }
    const updates = moveOrderUpdates(state.flattened, movingId, targetParentId, beforeId);
    try {
      await patchWorkspaces(updates);
      announce('Workspace moved.');
      if (typeof cb.onChanged === 'function') await cb.onChanged();
    } catch (err) {
      const message = err && err.message ? err.message : 'Failed to move workspace.';
      announce(message);
      toast(message, 'error');
      // Roll back to authoritative server state rather than leaving the
      // optimistic position on screen (FR118).
      if (typeof cb.onChanged === 'function') await cb.onChanged();
    }
  }

  // Delete and group live in workspace-bulk-actions.js so the Map runs the very
  // same code — see that module's header for why the Map could not before.
  function bulkContext() {
    return {
      rows: state.flattened,
      announce,
      toast,
      onTrashed: cb.onTrashed,
      onChanged: cb.onChanged
    };
  }

  async function performDelete(id) {
    await deleteWorkspaceAction(id, bulkContext());
  }

  async function deleteSelected() {
    const ids = Array.from(state.bulkSelection);
    if (ids.length === 0) return;
    const deleted = await deleteWorkspacesAction(ids, bulkContext());
    // Only clear the selection once something actually went; a declined
    // confirmation must leave the selection exactly as the user left it.
    if (deleted > 0) state.bulkSelection.clear();
  }

  async function createGroup(memberIds) {
    // The cockpit owns the shared dialog and mutation callback. Tree only
    // captures its current selection and clears it after the durable group is
    // confirmed, never falling back to a second form or a native prompt.
    if (typeof cb.onCreateGroup !== 'function') {
      const message = 'Create Group is unavailable. Refresh the page and try again.';
      announce(message);
      toast(message, 'error');
      return;
    }
    cb.onCreateGroup(memberIds, () => {
      state.bulkSelection.clear();
      rerender();
    });
  }

  async function rescan() {
    try {
      const res = await fetch('/api/workspaces/rescan', { method: 'POST' });
      if (!res.ok) throw new Error(await errorText(res, 'Failed to rescan'));
      announce('Workspaces rescanned from disk.');
      if (typeof cb.onChanged === 'function') await cb.onChanged();
    } catch (err) {
      const message = err && err.message ? err.message : 'Failed to rescan.';
      announce(message);
      toast(message, 'error');
    }
  }

  async function undoLast() {
    if (typeof cb.onUndo === 'function') await cb.onUndo();
  }

  return { openMoveDialog, deleteWorkspace: performDelete, focusRow };
}

/**
 * Remove a tag from a workspace using the existing tags PATCH contract.
 *
 * Throws with the server's reason on failure. The caller reloads authoritative
 * state afterwards rather than trusting an optimistic local edit (FR117/FR118).
 */
export async function removeWorkspaceTag(flattened, workspaceId, tag) {
  const row = (Array.isArray(flattened) ? flattened : []).find(r => r && r.id === workspaceId);
  if (!row) throw new Error('That workspace no longer exists.');
  const nextTags = (Array.isArray(row.tags) ? row.tags : []).filter(t => t !== tag);
  const res = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ tags: nextTags })
  });
  if (!res.ok) throw new Error(await errorText(res, 'Failed to remove tag'));
}

// ---------------------------------------------------------------------------
// Small shared helpers
// ---------------------------------------------------------------------------

function cssEscape(value) {
  if (typeof CSS !== 'undefined' && typeof CSS.escape === 'function') return CSS.escape(value);
  return String(value).replace(/["\\]/g, '\\$&');
}

function toast(message, variant) {
  if (typeof window === 'undefined') return;
  if (window.Toast && typeof window.Toast[variant] === 'function') {
    window.Toast[variant](message);
  }
}

// The workspace API reports failures as `{"error": ...}`, with a few endpoints
// using `message`. Reading only `message` surfaced raw JSON to the user on the
// most common real failure (a folder-slug conflict on move).
async function errorText(response, fallback) {
  try {
    const text = await response.text();
    if (!text) return fallback;
    try {
      const parsed = JSON.parse(text);
      if (parsed && typeof parsed.error === 'string' && parsed.error) return parsed.error;
      if (parsed && typeof parsed.message === 'string' && parsed.message) return parsed.message;
      return text;
    } catch (_) {
      return text;
    }
  } catch (_) {
    return fallback;
  }
}

/** Batched reorder/move, matching the launcher's atomic move contract. */
async function patchWorkspaces(updates) {
  const entries = Object.entries(updates);
  if (entries.length === 0) return;
  const responses = await Promise.all(
    entries.map(([id, payload]) =>
      fetch(`/api/workspaces/${encodeURIComponent(id)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      })
    )
  );
  const failed = responses.find(res => !res.ok);
  if (failed) throw new Error(await errorText(failed, 'Failed to move workspace'));
}

// The delete confirmations moved to workspace-bulk-actions.js along with the
// operations they guard (FR52/FR53), so Map and Tree ask the same questions.
