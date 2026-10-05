// home-tree-state.js — what the Home file tree remembers between visits.
//
// PRD: tasks/prd-home-file-tree.md §4.J (FR68-FR70).
//
// Three things are kept in the browser's localStorage: which rows are open,
// which tabs are open, and which tab is active. Nothing else is: not the text
// of a note (the server has that), not the filter box, not a bulk selection.
//
// This module only reads and writes the stored text and turns it into plain
// data. It never fetches. Putting the data back into Home's state is one call
// (`applyTreeState`), and the cockpit decides when contents are loaded — never
// while Home is in Map view (FR70).
//
// Stored text is treated as untrusted: it may be from an older version, cut
// short, or edited by hand. Anything that does not read as expected is dropped
// rather than repaired.

import { parseItemKey } from './home-tree-sources.js';

/** The storage key. The version is part of it, so a new shape starts clean. */
export const TREE_STATE_VERSION = 1;
export const TREE_STATE_KEY = `ori.home.fileTree.v${TREE_STATE_VERSION}`;

// Bounds, so a tree used for years cannot grow its stored state without end.
export const MAX_STORED_TABS = 40;
export const MAX_STORED_ROWS = 2000;

// The kinds of row that open in a tab.
const TAB_KINDS = new Set([
  'note',
  'ticket',
  'file',
  'output',
  'linkedFile',
  'chat',
  'memory',
  'agent',
  'workspace',
  'group'
]);

function isText(value) {
  return typeof value === 'string' && value !== '';
}

// A tab's `meta` holds what its loader needs (a note id, a file path and
// size). Only plain values are kept; anything nested is left behind.
function cleanMeta(meta) {
  const out = {};
  if (!meta || typeof meta !== 'object' || Array.isArray(meta)) return out;
  Object.keys(meta).forEach(name => {
    const value = meta[name];
    if (typeof value === 'string' || typeof value === 'boolean') out[name] = value;
    else if (typeof value === 'number' && Number.isFinite(value)) out[name] = value;
  });
  return out;
}

function cleanTab(tab) {
  if (!tab || typeof tab !== 'object') return null;
  if (!isText(tab.key) || !isText(tab.workspaceId) || !TAB_KINDS.has(tab.kind)) return null;
  return {
    key: tab.key,
    kind: tab.kind,
    workspaceId: tab.workspaceId,
    label: typeof tab.label === 'string' ? tab.label : '',
    meta: cleanMeta(tab.meta)
  };
}

function cleanIds(list) {
  if (!Array.isArray(list)) return [];
  return Array.from(new Set(list.filter(isText))).slice(0, MAX_STORED_ROWS);
}

function cleanTabs(list) {
  if (!Array.isArray(list)) return [];
  const seen = new Set();
  const tabs = [];
  list.forEach(entry => {
    const tab = cleanTab(entry);
    if (!tab || seen.has(tab.key)) return;
    seen.add(tab.key);
    tabs.push(tab);
  });
  // Too many: keep the newest, which are at the end of the strip.
  return tabs.slice(-MAX_STORED_TABS);
}

/**
 * What to store for Home's current state.
 *
 * `collapsed` and `expanded` are the two sets the tree keeps (rows that start
 * open and were closed; rows that start closed and were opened).
 */
export function snapshotTreeState(state) {
  const source = state || {};
  const tabs = cleanTabs(source.treeTabs);
  const activeKey = tabs.some(tab => tab.key === source.activeTabKey) ? source.activeTabKey : '';
  return {
    version: TREE_STATE_VERSION,
    collapsed: cleanIds(Array.from(source.collapsedGroups || [])),
    expanded: cleanIds(Array.from(source.expandedRows || [])),
    tabs,
    activeKey
  };
}

/**
 * Stored text → a snapshot, or null when there is nothing usable.
 *
 * Another version, text that is not JSON, or JSON of another shape all read
 * as "nothing stored".
 */
export function parseTreeState(text) {
  if (!isText(text)) return null;
  let raw;
  try {
    raw = JSON.parse(text);
  } catch (_) {
    return null;
  }
  if (!raw || typeof raw !== 'object' || raw.version !== TREE_STATE_VERSION) return null;
  const tabs = cleanTabs(raw.tabs);
  return {
    version: TREE_STATE_VERSION,
    collapsed: cleanIds(raw.collapsed),
    expanded: cleanIds(raw.expanded),
    tabs,
    activeKey: tabs.some(tab => tab.key === raw.activeKey) ? raw.activeKey : ''
  };
}

/** Read the stored snapshot. A browser that refuses storage has none. */
export function readTreeState(storage) {
  try {
    return parseTreeState(storage.getItem(TREE_STATE_KEY));
  } catch (_) {
    return null;
  }
}

/**
 * Store a snapshot, unless it is the text already stored.
 *
 * Returns the text that is now stored — pass it back as `lastText` next time
 * so an unchanged state costs no write. A full or refused storage is not an
 * error worth telling the user about: the tree simply will not be remembered.
 */
export function writeTreeState(storage, snapshot, lastText = '') {
  const text = JSON.stringify(snapshot);
  if (text === lastText) return lastText;
  try {
    storage.setItem(TREE_STATE_KEY, text);
  } catch (_) {
    return lastText;
  }
  return text;
}

/**
 * Put a stored snapshot back into Home's state (FR68).
 *
 * Fills the two row sets, the tabs and the active tab. Every restored tab is
 * marked `restored`, which is how the cockpit knows to drop it without a word
 * if its item turns out to be gone (FR69). Nothing is fetched.
 */
export function applyTreeState(state, snapshot) {
  if (!state || !snapshot) return false;
  state.collapsedGroups = new Set(snapshot.collapsed);
  state.expandedRows = new Set(snapshot.expanded);
  state.treeTabs = snapshot.tabs.map(tab => ({ ...tab, meta: { ...tab.meta }, restored: true }));
  state.activeTabKey = snapshot.activeKey;
  return true;
}

// The workspace or group a row key belongs to; '' for a key that is not one.
function workspaceOfKey(key) {
  const parsed = parseItemKey(key);
  return parsed ? parsed.workspaceId : '';
}

/**
 * Leave out the tabs whose workspace or group is gone (FR69).
 *
 * Returns `{ tabs, activeKey }`. When the active tab is dropped the tab that
 * took its place becomes active: its right-hand neighbour, else the one to
 * its left, as when a tab is closed by hand.
 */
export function dropMissingTabs(tabs, activeKey, liveWorkspaceIds) {
  const list = Array.isArray(tabs) ? tabs : [];
  const live = liveWorkspaceIds instanceof Set ? liveWorkspaceIds : new Set(liveWorkspaceIds || []);
  const kept = list.filter(tab => tab && live.has(tab.workspaceId));
  if (kept.some(tab => tab.key === activeKey)) return { tabs: kept, activeKey };
  const at = list.findIndex(tab => tab && tab.key === activeKey);
  if (at < 0) return { tabs: kept, activeKey: '' };
  const survives = tab => kept.includes(tab);
  const next = list.slice(at + 1).find(survives) || list.slice(0, at).reverse().find(survives);
  return { tabs: kept, activeKey: next ? next.key : '' };
}

/** Leave out the remembered rows of workspaces and groups that are gone. */
export function dropMissingRows(ids, liveWorkspaceIds) {
  const live = liveWorkspaceIds instanceof Set ? liveWorkspaceIds : new Set(liveWorkspaceIds || []);
  return Array.from(ids || []).filter(id => live.has(workspaceOfKey(id)));
}

// Every row id in a section's rows, folders included.
function collectIds(rows, into) {
  (Array.isArray(rows) ? rows : []).forEach(row => {
    if (!row) return;
    into.add(row.id);
    if (row.children) collectIds(row.children, into);
  });
  return into;
}

function hasMoreRow(rows) {
  return (Array.isArray(rows) ? rows : []).some(
    row => !!row && (row.kind === 'more' || hasMoreRow(row.children))
  );
}

// The sections whose list is everything the workspace has. Backlog is not one
// of them: it lists open, top-level tickets, so a ticket missing from it may
// simply be finished.
const COMPLETE_LIST_KINDS = {
  note: 'notes',
  file: 'files',
  output: 'outputs',
  chat: 'chats',
  agent: 'agents'
};

/**
 * The restored tabs a loaded section proves are gone (FR69).
 *
 * A note, file, output or agent that is not in its workspace's list no longer
 * exists
 * — provided the list is whole, which it is not once it has been cut at the
 * row limit. Only tabs still marked `restored` are considered: one opened in
 * this visit is never closed behind the user's back.
 */
export function vanishedTabKeys(tabs, workspaceId, sectionId, section) {
  if (!section || section.status !== 'ready' || hasMoreRow(section.rows)) return [];
  const present = collectIds(section.rows, new Set());
  return (Array.isArray(tabs) ? tabs : [])
    .filter(
      tab =>
        !!tab &&
        tab.restored === true &&
        tab.workspaceId === workspaceId &&
        COMPLETE_LIST_KINDS[tab.kind] === sectionId &&
        !present.has(tab.key)
    )
    .map(tab => tab.key);
}

// The linked folder a remembered file tab belongs to. Its key says so
// (`ws1/lf/<directory id>/docs/plan.md`), so a tab stored without its `meta`
// is still placed.
function linkedFolderOfTab(tab) {
  const parsed = parseItemKey(tab.key);
  return parsed && parsed.kind === 'linkedFile' ? parsed.itemId.split('/')[0] : '';
}

/**
 * The restored linked-file tabs that a loaded linked folder, or the loaded
 * list of linked folders, proves are gone (FR69).
 *
 * Pass `dirId` and that folder's own loaded files (`folder`): a file tab of
 * that folder that is not among them is gone. Pass no `dirId` and the loaded
 * Linked folders section: a file tab whose folder is no longer linked is
 * gone. Either list proves something only when it is whole — ready, and not
 * cut at the row limit.
 */
export function vanishedLinkedTabKeys(tabs, workspaceId, { dirId = '', folder, section } = {}) {
  const list = dirId ? folder : section;
  if (!list || list.status !== 'ready' || hasMoreRow(list.rows)) return [];
  const present = collectIds(list.rows, new Set());
  const linkedIds = new Set(
    (Array.isArray(list.rows) ? list.rows : []).map(row =>
      String((row.meta && row.meta.dirId) || '')
    )
  );
  return (Array.isArray(tabs) ? tabs : [])
    .filter(tab => {
      if (!tab || tab.restored !== true || tab.kind !== 'linkedFile') return false;
      if (tab.workspaceId !== workspaceId) return false;
      const owner = linkedFolderOfTab(tab);
      if (!owner) return false;
      return dirId ? owner === dirId && !present.has(tab.key) : !linkedIds.has(owner);
    })
    .map(tab => tab.key);
}

/** Whether a failed load means the item is gone rather than unreachable. */
export function isGoneError(error) {
  return !!error && (error.status === 404 || error.status === 410);
}
