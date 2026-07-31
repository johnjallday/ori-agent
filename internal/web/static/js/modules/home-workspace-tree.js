// home-workspace-tree.js — the Tree peer view for the Home cockpit.
//
// PRD: tasks/prd-home-workspace-cockpit.md §4.4 (FR38-FR56) and §4.9
// (FR124, FR126, FR127, FR136, FR137).
//
// Tree is Map's PEER, not a drawer: it renders into the same workspace-area
// slot, and exactly one of the two is active at a time. It carries the complete
// management toolset — hierarchy, expand/collapse, active selection, bulk
// checkbox selection, grouping, move (drag AND keyboard), delete, and Undo —
// so no management capability requires visiting the legacy launcher.
//
// This module owns rendering and local interaction only. Every mutation is
// handed back to the cockpit coordinator through callbacks, so the cockpit
// stays the single authority for shared state, refresh, and rail context
// (FR117). Server contracts are the launcher's existing ones, unchanged:
//
//   move/reorder   PATCH  /api/workspaces/{id}  { parent_id, order_index }
//   create group   POST   /api/workspaces       { name, kind: 'group' }
//   delete         DELETE /api/workspaces/{id}?confirm=true[&delete_mode=...]
//   undo delete    POST   /api/workspaces/{id}/restore
//   rescan         POST   /api/workspaces/rescan
//
// Pure helpers are exported for home-workspace-tree.test.js; the module is
// imported directly by home-workspace-cockpit.js, so it needs no global.

import {
  escapeHtml,
  formatCount,
  isGroupWorkspace,
  workspaceSignals
} from './home-workspace-cockpit.js';

// ---------------------------------------------------------------------------
// Hierarchy shaping
// ---------------------------------------------------------------------------

/**
 * Flatten the workspace tree into the rows Tree actually renders.
 *
 * Rows inside a collapsed group are omitted entirely rather than hidden with
 * CSS, so arrow-key navigation and `aria-setsize`/`aria-posinset` describe the
 * rows a user can actually reach (FR41, FR127).
 */
export function visibleTreeRows(nodes, collapsedIds, depth = 0, parentId = '') {
  const collapsed = collapsedIds instanceof Set ? collapsedIds : new Set(collapsedIds || []);
  const rows = [];
  const siblings = (Array.isArray(nodes) ? nodes : []).filter(Boolean);
  siblings.forEach((node, index) => {
    const children = Array.isArray(node.children) ? node.children.filter(Boolean) : [];
    const group = isGroupWorkspace(node) || children.length > 0;
    const isCollapsed = group && collapsed.has(node.id);
    rows.push({
      workspace: node,
      id: node.id,
      name: node.name || (group ? 'Group' : 'Untitled workspace'),
      depth,
      parentId,
      isGroup: group,
      hasChildren: children.length > 0,
      expanded: group ? !isCollapsed : null,
      childCount: children.length,
      posInSet: index + 1,
      setSize: siblings.length,
      nextSiblingId: (siblings[index + 1] && siblings[index + 1].id) || ''
    });
    if (group && !isCollapsed && children.length > 0) {
      rows.push(...visibleTreeRows(children, collapsed, depth + 1, node.id));
    }
  });
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
 * Checkbox state for every row, including group subtree roll-up.
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

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

/** Scan data for a row. Missing metrics read as unavailable, never as 0 (FR44). */
export function rowMetaParts(workspace) {
  if (isGroupWorkspace(workspace)) return [];
  const signals = workspaceSignals(workspace);
  return [
    { label: 'agents', value: formatCount(signals.agents) },
    { label: 'open', value: formatCount(signals.openTasks) },
    { label: 'attention', value: formatCount(signals.attention) }
  ];
}

function statusChipHTML(workspace) {
  if (isGroupWorkspace(workspace)) {
    return '<span class="cockpit-tree-kind">Group</span>';
  }
  const signals = workspaceSignals(workspace);
  return (
    `<span class="cockpit-tree-status is-${escapeHtml(signals.status)}">` +
    '<span class="cockpit-tree-status-dot" aria-hidden="true"></span>' +
    escapeHtml(signals.label) +
    '</span>'
  );
}

function rowHTML(row, ctx) {
  const ws = row.workspace || {};
  const id = escapeHtml(row.id);
  const name = escapeHtml(row.name);
  const bulk = ctx.bulkState[row.id] || { checked: false, indeterminate: false };
  const isActive = ctx.activeId === row.id;
  const isHQ = ws.is_personal_hq === true || ws.designation === 'personal_hq';
  const nextRun = String(ws.next_scheduled_run || '').trim();

  const meta = rowMetaParts(ws)
    .map(
      part =>
        `<span class="cockpit-tree-metric"><b${part.value === '—' ? ' data-unavailable="true"' : ''}>${escapeHtml(part.value)}</b> ${escapeHtml(part.label)}</span>`
    )
    .join('');

  // Tags stay filterable and removable, matching the launcher (FR54).
  const tags = (ctx.tagsById[row.id] || [])
    .slice(0, 3)
    .map(
      tag =>
        '<span class="cockpit-tree-tag' +
        (ctx.activeTags && ctx.activeTags.has(tag) ? ' is-active' : '') +
        '">' +
        `<button type="button" class="cockpit-tree-tag-filter" data-tree-tag-filter="${escapeHtml(tag)}" tabindex="-1" ` +
        `aria-pressed="${ctx.activeTags && ctx.activeTags.has(tag) ? 'true' : 'false'}" ` +
        `aria-label="Filter by tag ${escapeHtml(tag)}">${escapeHtml(tag)}</button>` +
        `<button type="button" class="cockpit-tree-tag-remove" data-tree-tag-remove="${escapeHtml(tag)}" ` +
        `data-tree-tag-workspace="${id}" tabindex="-1" aria-label="Remove tag ${escapeHtml(tag)} from ${name}">×</button>` +
        '</span>'
    )
    .join('');

  return (
    `<li class="cockpit-tree-node${row.isGroup ? ' is-group' : ''}" role="none">` +
    `<div class="cockpit-tree-row${isActive ? ' is-active' : ''}" role="treeitem" ` +
    `id="cockpit-tree-row-${id}" ` +
    `data-tree-row="${id}" data-tree-kind="${row.isGroup ? 'group' : 'workspace'}" ` +
    `data-parent-id="${escapeHtml(row.parentId)}" ` +
    `data-next-sibling-id="${escapeHtml(row.nextSiblingId)}" ` +
    `tabindex="${ctx.tabbableId === row.id ? '0' : '-1'}" ` +
    `draggable="true" ` +
    `aria-level="${row.depth + 1}" aria-posinset="${row.posInSet}" aria-setsize="${row.setSize}" ` +
    (row.isGroup ? `aria-expanded="${row.expanded ? 'true' : 'false'}" ` : '') +
    `aria-selected="${isActive ? 'true' : 'false'}" ` +
    `style="--tree-depth:${row.depth}">` +
    // Bulk checkbox. Visually and semantically distinct from active selection,
    // which is carried by aria-selected and the row highlight (FR46).
    '<span class="cockpit-tree-check">' +
    `<input type="checkbox" data-tree-check="${id}" ${bulk.checked ? 'checked' : ''} ` +
    `aria-label="Select ${name} for bulk actions">` +
    '</span>' +
    (row.isGroup
      ? `<button type="button" class="cockpit-tree-caret" data-tree-toggle="${id}" tabindex="-1" ` +
        `aria-label="${row.expanded ? 'Collapse' : 'Expand'} ${name}">` +
        `<span aria-hidden="true">${row.expanded ? '▾' : '▸'}</span></button>`
      : '<span class="cockpit-tree-caret-space" aria-hidden="true"></span>') +
    `<span class="cockpit-tree-name">${name}</span>` +
    (isHQ ? '<span class="cockpit-tree-kind">Personal HQ</span>' : '') +
    statusChipHTML(ws) +
    `<span class="cockpit-tree-metrics">${meta}</span>` +
    (nextRun
      ? `<span class="cockpit-tree-next">${escapeHtml(nextRun)}</span>`
      : row.isGroup
        ? ''
        : '<span class="cockpit-tree-next" data-unavailable="true">No schedule</span>') +
    `<span class="cockpit-tree-tags">${tags}</span>` +
    '<span class="cockpit-tree-actions">' +
    `<button type="button" class="cockpit-tree-action" data-tree-move="${id}" tabindex="-1" aria-label="Move ${name}">Move</button>` +
    `<button type="button" class="cockpit-tree-action is-danger" data-tree-delete="${id}" tabindex="-1" aria-label="Delete ${name}">Delete</button>` +
    '</span>' +
    '</div>' +
    (row.isGroup && row.expanded && !row.hasChildren
      ? `<ul class="cockpit-tree-children is-empty" role="group"><li role="none">` +
        `<div class="cockpit-tree-empty-drop" data-tree-drop-into="${id}" style="--tree-depth:${row.depth + 1}">Drop a workspace here</div>` +
        '</li></ul>'
      : '') +
    '</li>'
  );
}

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
    tagsById: {},
    activeTags: new Set(),
    ...(ctx || {})
  };
  if (!rows.length) {
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
      const children =
        index < rows.length && rows[index].depth === depth + 1 ? build(depth + 1) : '';
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
    '<ul class="cockpit-tree-root" role="tree" aria-label="Workspaces" aria-multiselectable="true">' +
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
 * null when the key is not ours. ArrowRight on a collapsed group expands it;
 * on an expanded one it descends. ArrowLeft collapses, or climbs to the parent.
 */
export function resolveTreeKey(key, currentId, rows) {
  const list = Array.isArray(rows) ? rows : [];
  const at = list.findIndex(row => row.id === currentId);
  if (at < 0) return null;
  const row = list[at];

  if (key === 'ArrowDown') {
    return at + 1 < list.length ? { focusId: list[at + 1].id } : null;
  }
  if (key === 'ArrowUp') {
    return at > 0 ? { focusId: list[at - 1].id } : null;
  }
  if (key === 'Home') return list.length ? { focusId: list[0].id } : null;
  if (key === 'End') return list.length ? { focusId: list[list.length - 1].id } : null;
  if (key === 'ArrowRight') {
    if (row.isGroup && !row.expanded) return { toggle: row.id, expand: true };
    if (row.isGroup && row.expanded && at + 1 < list.length && list[at + 1].depth > row.depth) {
      return { focusId: list[at + 1].id };
    }
    return null;
  }
  if (key === 'ArrowLeft') {
    if (row.isGroup && row.expanded) return { toggle: row.id, expand: false };
    if (row.parentId) return { focusId: row.parentId };
    return null;
  }
  return null;
}

// ---------------------------------------------------------------------------
// Mount + interaction
// ---------------------------------------------------------------------------

/**
 * Mount the Tree into the cockpit's workspace-area slot.
 *
 * `callbacks` is the seam back to the coordinator: the Tree never mutates
 * shared state or refetches for itself.
 *
 *   onSelect(id)            active-item selection changed
 *   onOpen(id)              explicit open requested (Enter / row action)
 *   onChanged()             a mutation landed; reload authoritative state
 *   onAnnounce(message)     polite live-region message
 *
 * Idempotent: re-mounting replaces the container's content and rebinds, so a
 * repeated view change cannot stack listeners (FR122).
 */
export function mountTree(container, state, callbacks) {
  if (!container) return null;
  const cb = callbacks || {};
  const collapsed = state.collapsedGroups instanceof Set ? state.collapsedGroups : new Set();
  const selected = state.bulkSelection instanceof Set ? state.bulkSelection : new Set();
  const activeTags = state.activeTags instanceof Set ? state.activeTags : new Set();
  const visibleTree = filterTreeByTags(
    state.tree,
    (state.metadata && state.metadata.tagsById) || {},
    activeTags
  );
  const rows = visibleTreeRows(visibleTree, collapsed);

  // Roving tabindex: exactly one row is tabbable, and it follows the active
  // item when there is one (FR127).
  const tabbableId = rows.some(r => r.id === state.focusId)
    ? state.focusId
    : rows.some(r => r.id === state.selectedId)
      ? state.selectedId
      : (rows[0] && rows[0].id) || '';

  container.innerHTML =
    renderToolbarHTML(state) +
    renderTagFilterBarHTML(state) +
    renderBulkBarHTML(selected.size) +
    '<div class="cockpit-tree-scroll">' +
    renderTreeHTML(rows, {
      activeId: state.selectedId,
      tabbableId,
      bulkState: bulkSelectionState(state.flattened, selected),
      tagsById: (state.metadata && state.metadata.tagsById) || {},
      activeTags: state.activeTags instanceof Set ? state.activeTags : new Set()
    }) +
    '<div class="cockpit-tree-root-drop" data-tree-drop-into="">Drop here to move to the top level</div>' +
    '</div>' +
    '<div class="cockpit-tree-move-dialog" data-tree-move-dialog hidden></div>';

  // Group checkboxes carry indeterminate state, which HTML cannot express as an
  // attribute — it must be set on the element.
  const bulkState = bulkSelectionState(state.flattened, selected);
  container.querySelectorAll('[data-tree-check]').forEach(box => {
    const id = box.getAttribute('data-tree-check');
    box.indeterminate = !!(bulkState[id] && bulkState[id].indeterminate);
  });

  bindTree(container, state, cb, rows);
  return { rows, tabbableId };
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
    '<div class="cockpit-tree-toolbar-actions">' +
    '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-bs-toggle="modal" data-bs-target="#addFolderModal" data-workspace-import-mode="false" data-workspace-entry-point="home_cockpit_tree_create">Create Workspace</button>' +
    '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-tree-new-group>New Group</button>' +
    '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-bs-toggle="modal" data-bs-target="#addFolderModal" data-workspace-import-mode="true" data-workspace-entry-point="home_cockpit_tree_import">Import Folder</button>' +
    '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-tree-rescan>Rescan</button>' +
    '<a class="modern-btn modern-btn-secondary modern-btn-sm" href="/settings">Manage directory</a>' +
    `<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-tree-undo ${state.canUndo ? '' : 'disabled'}>Undo</button>` +
    '</div>' +
    '</div>'
  );
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
    '<button type="button" class="modern-btn modern-btn-primary modern-btn-sm" data-tree-group-selected>Group selected</button>' +
    '<button type="button" class="modern-btn modern-btn-danger modern-btn-sm" data-tree-delete-selected>Delete selected</button>' +
    '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-tree-cancel-selection>Cancel</button>' +
    '</div>'
  );
}

function bindTree(container, state, cb, rows) {
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
  };

  // --- row activation -----------------------------------------------------
  container.querySelectorAll('[data-tree-row]').forEach(row => {
    const id = row.getAttribute('data-tree-row');

    // Pointer click selects only — opening is always explicit (FR45).
    row.addEventListener('click', e => {
      if (e.target.closest('[data-tree-check]')) return;
      if (e.target.closest('[data-tree-toggle]')) return;
      if (e.target.closest('[data-tree-move]')) return;
      if (e.target.closest('[data-tree-delete]')) return;
      state.focusId = id;
      if (typeof cb.onSelect === 'function') cb.onSelect(id);
    });

    row.addEventListener('keydown', e => {
      // Space toggles BULK selection; Enter opens (FR126).
      if (e.key === ' ' || e.key === 'Spacebar') {
        e.preventDefault();
        toggleBulk(id);
        return;
      }
      if (e.key === 'Enter') {
        e.preventDefault();
        if (typeof cb.onOpen === 'function') cb.onOpen(id);
        return;
      }
      const action = resolveTreeKey(e.key, id, rows);
      if (!action) return;
      e.preventDefault();
      if (action.focusId) {
        focusRow(action.focusId);
        return;
      }
      if (action.toggle) toggleGroup(action.toggle);
    });

    row.addEventListener('dragstart', e => {
      state.draggingId = id;
      if (e.dataTransfer) {
        e.dataTransfer.effectAllowed = 'move';
        try {
          e.dataTransfer.setData('text/plain', id);
        } catch (_) {
          /* some browsers restrict this outside user gestures */
        }
      }
    });
    // A row exposes three drop zones, which together with the top-level drop
    // strip give the launcher's full destination set — before, after, into a
    // group, and back to the top level (FR49). The pointer's position within
    // the row picks which one.
    const dropIntent = e => {
      const rect = row.getBoundingClientRect();
      const offset = rect.height > 0 ? (e.clientY - rect.top) / rect.height : 0.5;
      const isGroupRow = row.getAttribute('data-tree-kind') === 'group';
      const ownParent = row.getAttribute('data-parent-id') || '';
      if (isGroupRow && offset > 0.25 && offset < 0.75) {
        return { parent: id, before: '', zone: 'into' };
      }
      if (offset < 0.5) return { parent: ownParent, before: id, zone: 'before' };
      return {
        parent: ownParent,
        before: row.getAttribute('data-next-sibling-id') || '',
        zone: 'after'
      };
    };
    const clearDropClasses = () =>
      row.classList.remove('is-drop-target', 'is-drop-before', 'is-drop-after', 'is-drop-into');

    row.addEventListener('dragover', e => {
      if (!state.draggingId) return;
      const intent = dropIntent(e);
      if (!isMoveAllowed(state.flattened, state.draggingId, intent.parent)) return;
      e.preventDefault();
      clearDropClasses();
      row.classList.add('is-drop-target', `is-drop-${intent.zone}`);
    });
    row.addEventListener('dragleave', clearDropClasses);
    row.addEventListener('drop', e => {
      e.preventDefault();
      clearDropClasses();
      const movingId = state.draggingId;
      state.draggingId = '';
      if (!movingId || movingId === id) return;
      const intent = dropIntent(e);
      void performMove(movingId, intent.parent, intent.before);
    });
  });

  // --- expand / collapse (never selects or opens, FR42) -------------------
  container.querySelectorAll('[data-tree-toggle]').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      toggleGroup(btn.getAttribute('data-tree-toggle'));
    });
  });

  // --- bulk checkboxes ----------------------------------------------------
  container.querySelectorAll('[data-tree-check]').forEach(box => {
    box.addEventListener('click', e => e.stopPropagation());
    box.addEventListener('change', () => toggleBulk(box.getAttribute('data-tree-check')));
  });

  // --- drop zones ---------------------------------------------------------
  container.querySelectorAll('[data-tree-drop-into]').forEach(zone => {
    const targetParent = zone.getAttribute('data-tree-drop-into');
    zone.addEventListener('dragover', e => {
      if (!state.draggingId) return;
      if (!isMoveAllowed(state.flattened, state.draggingId, targetParent)) return;
      e.preventDefault();
      zone.classList.add('is-drop-target');
    });
    zone.addEventListener('dragleave', () => zone.classList.remove('is-drop-target'));
    zone.addEventListener('drop', e => {
      e.preventDefault();
      zone.classList.remove('is-drop-target');
      const movingId = state.draggingId;
      state.draggingId = '';
      if (movingId) void performMove(movingId, targetParent);
    });
  });

  // --- per-row actions ----------------------------------------------------
  container.querySelectorAll('[data-tree-move]').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      openMoveDialog(btn.getAttribute('data-tree-move'));
    });
  });
  container.querySelectorAll('[data-tree-delete]').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      void performDelete(btn.getAttribute('data-tree-delete'));
    });
  });

  // --- tags (FR54) --------------------------------------------------------
  container.querySelectorAll('[data-tree-tag-filter]').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      const tag = btn.getAttribute('data-tree-tag-filter');
      if (state.activeTags.has(tag)) state.activeTags.delete(tag);
      else state.activeTags.add(tag);
      rerender();
    });
  });
  container.querySelectorAll('[data-tree-tag-remove]').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      void removeTag(
        btn.getAttribute('data-tree-tag-workspace'),
        btn.getAttribute('data-tree-tag-remove')
      );
    });
  });
  bindClick(container, '[data-tree-tag-clear]', () => {
    state.activeTags.clear();
    rerender();
  });

  // --- toolbar ------------------------------------------------------------
  bindClick(container, '[data-tree-new-group]', () => void createGroup([]));
  bindClick(container, '[data-tree-rescan]', () => void rescan());
  bindClick(container, '[data-tree-undo]', () => void undoLast());
  bindClick(container, '[data-tree-select-all]', () => {
    state.flattened.forEach(row => state.bulkSelection.add(row.id));
    rerender();
  });
  bindClick(container, '[data-tree-cancel-selection]', () => {
    state.bulkSelection.clear();
    rerender();
  });
  bindClick(
    container,
    '[data-tree-group-selected]',
    () => void createGroup(Array.from(state.bulkSelection))
  );
  bindClick(container, '[data-tree-delete-selected]', () => void deleteSelected());

  // ---- behaviors ---------------------------------------------------------

  function rerender() {
    if (typeof cb.onRerender === 'function') cb.onRerender();
  }

  function toggleGroup(id) {
    if (state.collapsedGroups.has(id)) state.collapsedGroups.delete(id);
    else state.collapsedGroups.add(id);
    state.focusId = id;
    rerender();
  }

  function toggleBulk(id) {
    if (state.bulkSelection.has(id)) state.bulkSelection.delete(id);
    else state.bulkSelection.add(id);
    // Checking a group checks its whole subtree, matching the launcher.
    descendantIds(state.flattened, id).forEach(childId => {
      if (state.bulkSelection.has(id)) state.bulkSelection.add(childId);
      else state.bulkSelection.delete(childId);
    });
    state.focusId = id;
    rerender();
  }

  function openMoveDialog(id) {
    const dialog = container.querySelector('[data-tree-move-dialog]');
    if (!dialog) return;
    const moving = state.flattened.find(row => row.id === id);
    const destinations = moveDestinations(state.flattened, id);
    dialog.innerHTML =
      '<div class="cockpit-tree-move-panel" role="dialog" aria-label="Move workspace">' +
      renderMoveDialogHTML((moving && moving.name) || 'this workspace', destinations) +
      '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-tree-move-cancel>Cancel</button>' +
      '</div>';
    dialog.hidden = false;
    dialog.querySelectorAll('[data-tree-move-to]').forEach(btn =>
      btn.addEventListener('click', () => {
        dialog.hidden = true;
        void performMove(id, btn.getAttribute('data-tree-move-to'));
      })
    );
    bindClick(dialog, '[data-tree-move-cancel]', () => {
      dialog.hidden = true;
      focusRow(id);
    });
    const first = dialog.querySelector('button');
    if (first) first.focus();
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

  async function performDelete(id) {
    const row = state.flattened.find(r => r.id === id);
    if (!row) return;
    const group = isGroupWorkspace(row);
    const confirmed = await confirmDelete(row, group);
    if (!confirmed) return;
    const query =
      group && confirmed.mode ? `&delete_mode=${encodeURIComponent(confirmed.mode)}` : '';
    try {
      const res = await fetch(`/api/workspaces/${encodeURIComponent(id)}?confirm=true${query}`, {
        method: 'DELETE'
      });
      if (!res.ok) throw new Error(await errorText(res, 'Failed to delete'));
      let trashed = false;
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        trashed = !!(data && data.trashed);
      }
      if (trashed && typeof cb.onTrashed === 'function') cb.onTrashed(id, row.name);
      announce(`${row.name} deleted.`);
      if (typeof cb.onChanged === 'function') await cb.onChanged();
    } catch (err) {
      const message = err && err.message ? err.message : 'Failed to delete.';
      announce(message);
      toast(message, 'error');
    }
  }

  async function deleteSelected() {
    const ids = Array.from(state.bulkSelection);
    if (ids.length === 0) return;
    const ok = await confirmBulkDelete(ids.length);
    if (!ok) return;
    for (const id of ids) {
      const row = state.flattened.find(r => r.id === id);
      if (!row) continue;
      try {
        const res = await fetch(`/api/workspaces/${encodeURIComponent(id)}?confirm=true`, {
          method: 'DELETE'
        });
        if (!res.ok) continue;
        if (res.status !== 204) {
          const data = await res.json().catch(() => ({}));
          if (data && data.trashed && typeof cb.onTrashed === 'function') {
            cb.onTrashed(id, row.name);
          }
        }
      } catch (_) {
        /* keep going; the reload below reports the real outcome */
      }
    }
    state.bulkSelection.clear();
    announce(`${ids.length} items deleted.`);
    if (typeof cb.onChanged === 'function') await cb.onChanged();
  }

  async function createGroup(memberIds) {
    const name = window.prompt('Name for the new group:');
    if (!name || !name.trim()) return;
    try {
      const res = await fetch('/api/workspaces', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: name.trim(), kind: 'group' })
      });
      if (!res.ok) throw new Error(await errorText(res, 'Failed to create group'));
      const body = await res.json().catch(() => ({}));
      const groupId = body && body.folder && body.folder.id;
      if (groupId && memberIds.length) {
        const updates = {};
        memberIds.forEach((id, index) => {
          updates[id] = { parent_id: groupId, order_index: index + 1 };
        });
        await patchWorkspaces(updates);
      }
      state.bulkSelection.clear();
      announce(`Group "${name.trim()}" created.`);
      if (typeof cb.onChanged === 'function') await cb.onChanged();
    } catch (err) {
      const message = err && err.message ? err.message : 'Failed to create group.';
      announce(message);
      toast(message, 'error');
    }
  }

  /**
   * Remove a tag from a workspace using the existing tags PATCH contract.
   * The cockpit reloads authoritative state afterwards rather than trusting an
   * optimistic local edit (FR117/FR118).
   */
  async function removeTag(workspaceId, tag) {
    const row = state.flattened.find(r => r.id === workspaceId);
    if (!row) return;
    const nextTags = (Array.isArray(row.tags) ? row.tags : []).filter(t => t !== tag);
    try {
      const res = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tags: nextTags })
      });
      if (!res.ok) throw new Error(await errorText(res, 'Failed to remove tag'));
      announce(`Removed tag ${tag}.`);
      if (typeof cb.onChanged === 'function') await cb.onChanged();
    } catch (err) {
      const message = err && err.message ? err.message : 'Failed to remove tag.';
      announce(message);
      toast(message, 'error');
    }
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
}

// ---------------------------------------------------------------------------
// Small shared helpers
// ---------------------------------------------------------------------------

function bindClick(scope, selector, handler) {
  const el = scope.querySelector(selector);
  if (el) el.addEventListener('click', handler);
}

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

async function errorText(response, fallback) {
  try {
    const text = await response.text();
    if (!text) return fallback;
    try {
      const parsed = JSON.parse(text);
      return parsed && parsed.message ? parsed.message : text;
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

/**
 * Delete confirmation. Groups keep their two-mode choice (group only vs group
 * and contents), and nothing is ever deleted without a confirmation (FR52/FR53).
 */
async function confirmDelete(row, isGroup) {
  if (!isGroup) {
    const ok = window.confirm(
      `Delete "${row.name}"?\n\nIt moves to the Trash and can be restored with Undo.`
    );
    return ok ? { mode: '' } : null;
  }
  const withContents = window.confirm(
    `Delete the group "${row.name}"?\n\n` +
      'OK — delete the group AND everything inside it.\n' +
      'Cancel — choose to keep the workspaces instead.'
  );
  if (withContents) return { mode: 'contents' };
  const groupOnly = window.confirm(
    `Delete only the group "${row.name}" and move its workspaces back to the top level?`
  );
  return groupOnly ? { mode: 'group_only' } : null;
}

async function confirmBulkDelete(count) {
  return window.confirm(
    `Delete ${count} selected item${count === 1 ? '' : 's'}?\n\n` +
      'They move to the Trash and can be restored with Undo.'
  );
}
