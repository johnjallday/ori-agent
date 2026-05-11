// note-tabs.js — pure state reducer for the multi-note tab / split-pane
// workflow on the dedicated /notes/<id> page. Renderers and DOM wiring live
// in note-page.js (or a later renderer module); this file only owns state
// transitions so the logic is exhaustively testable in node --test.
//
// State shape:
//   {
//     panes: [
//       { activeId: 'note-x', tabs: ['note-x', 'note-y'] },
//       // optional second pane when splitMode !== 'none'
//     ],
//     splitMode: 'none' | 'horizontal',
//     focusedPaneIndex: 0 | 1,
//   }
//
// Reducer rules:
// - openTab(noteId, paneIndex): if the tab already exists in that pane, just
//   switches the pane's active tab to it. Otherwise appends a new tab and
//   makes it active. Capped at one pane unless splitRight() ran.
// - closeTab(noteId, paneIndex): removes the tab from that pane. If it was
//   active, the previous tab in the pane becomes active. If it was the last
//   tab and the pane is the split (right) pane, the split collapses. If it
//   was the last tab in a non-split state, the pane keeps an empty tab list.
// - splitRight(): clones the focused pane's active tab into a new pane on
//   the right. Cap at 2 panes total.
// - unsplit(removePaneIndex): keep only the surviving pane.
// - focusPane(paneIndex): sets focusedPaneIndex.
// - reorder(paneIndex, fromIdx, toIdx): moves a tab within a pane.
// - hydrate(saved): merges a saved state, sanitizing shape mismatches.

export const MAX_PANES = 2;

export function initialState(initialNoteId = null) {
  const tabs = initialNoteId ? [initialNoteId] : [];
  return {
    panes: [{ activeId: initialNoteId, tabs }],
    splitMode: 'none',
    focusedPaneIndex: 0,
  };
}

function clonePane(pane) {
  return { activeId: pane.activeId, tabs: pane.tabs.slice() };
}

function cloneState(state) {
  return {
    panes: state.panes.map(clonePane),
    splitMode: state.splitMode,
    focusedPaneIndex: state.focusedPaneIndex,
  };
}

function validPaneIndex(state, idx) {
  return Number.isInteger(idx) && idx >= 0 && idx < state.panes.length;
}

export function openTab(state, noteId, paneIndex = state.focusedPaneIndex) {
  if (!noteId) return state;
  const next = cloneState(state);
  if (!validPaneIndex(next, paneIndex)) paneIndex = next.focusedPaneIndex;
  const pane = next.panes[paneIndex];
  if (pane.tabs.includes(noteId)) {
    pane.activeId = noteId;
  } else {
    pane.tabs.push(noteId);
    pane.activeId = noteId;
  }
  next.focusedPaneIndex = paneIndex;
  return next;
}

export function setActiveTab(state, paneIndex, noteId) {
  if (!validPaneIndex(state, paneIndex)) return state;
  if (!state.panes[paneIndex].tabs.includes(noteId)) return state;
  const next = cloneState(state);
  next.panes[paneIndex].activeId = noteId;
  next.focusedPaneIndex = paneIndex;
  return next;
}

export function closeTab(state, noteId, paneIndex = state.focusedPaneIndex) {
  if (!validPaneIndex(state, paneIndex)) return state;
  const pane = state.panes[paneIndex];
  const idx = pane.tabs.indexOf(noteId);
  if (idx < 0) return state;

  const next = cloneState(state);
  const nextPane = next.panes[paneIndex];
  nextPane.tabs.splice(idx, 1);

  // If the removed tab was active, pick the previous (or next) tab as active.
  if (nextPane.activeId === noteId) {
    if (nextPane.tabs.length === 0) {
      nextPane.activeId = null;
    } else if (idx > 0) {
      nextPane.activeId = nextPane.tabs[idx - 1];
    } else {
      nextPane.activeId = nextPane.tabs[0];
    }
  }

  // Collapse split when the right pane is empty.
  if (next.splitMode !== 'none' && next.panes.length === 2 && next.panes[1].tabs.length === 0) {
    next.panes.pop();
    next.splitMode = 'none';
    next.focusedPaneIndex = 0;
  }

  // Clamp focused pane in case the right pane went away.
  if (!validPaneIndex(next, next.focusedPaneIndex)) {
    next.focusedPaneIndex = Math.max(0, next.panes.length - 1);
  }

  return next;
}

export function splitRight(state) {
  if (state.panes.length >= MAX_PANES) return state;
  const sourcePane = state.panes[state.focusedPaneIndex];
  if (!sourcePane?.activeId) return state;

  const next = cloneState(state);
  next.panes.push({ activeId: sourcePane.activeId, tabs: [sourcePane.activeId] });
  next.splitMode = 'horizontal';
  next.focusedPaneIndex = next.panes.length - 1;
  return next;
}

export function unsplit(state, removePaneIndex = state.panes.length - 1) {
  if (state.panes.length <= 1) return state;
  if (!validPaneIndex(state, removePaneIndex)) return state;
  const next = cloneState(state);
  next.panes.splice(removePaneIndex, 1);
  next.splitMode = 'none';
  next.focusedPaneIndex = 0;
  return next;
}

export function focusPane(state, paneIndex) {
  if (!validPaneIndex(state, paneIndex)) return state;
  if (state.focusedPaneIndex === paneIndex) return state;
  const next = cloneState(state);
  next.focusedPaneIndex = paneIndex;
  return next;
}

export function reorder(state, paneIndex, fromIdx, toIdx) {
  if (!validPaneIndex(state, paneIndex)) return state;
  const pane = state.panes[paneIndex];
  if (fromIdx < 0 || fromIdx >= pane.tabs.length) return state;
  if (toIdx < 0 || toIdx >= pane.tabs.length) return state;
  if (fromIdx === toIdx) return state;
  const next = cloneState(state);
  const nextPane = next.panes[paneIndex];
  const [moved] = nextPane.tabs.splice(fromIdx, 1);
  nextPane.tabs.splice(toIdx, 0, moved);
  return next;
}

// hydrate merges saved state into the current state. Defends against shape
// drift by sanitizing each field; falls back to initialState on garbage.
export function hydrate(saved, fallbackNoteId = null) {
  const fallback = initialState(fallbackNoteId);
  if (!saved || typeof saved !== 'object') return fallback;
  const panes = Array.isArray(saved.panes) ? saved.panes : null;
  if (!panes || panes.length === 0 || panes.length > MAX_PANES) return fallback;

  const sanitizedPanes = [];
  for (const pane of panes) {
    if (!pane || typeof pane !== 'object') return fallback;
    const tabs = Array.isArray(pane.tabs) ? pane.tabs.filter((t) => typeof t === 'string' && t) : null;
    if (!tabs || tabs.length === 0) return fallback;
    let activeId = typeof pane.activeId === 'string' ? pane.activeId : null;
    if (!activeId || !tabs.includes(activeId)) activeId = tabs[0];
    sanitizedPanes.push({ activeId, tabs });
  }

  const splitMode = sanitizedPanes.length > 1 ? 'horizontal' : 'none';
  let focusedPaneIndex = Number.isInteger(saved.focusedPaneIndex) ? saved.focusedPaneIndex : 0;
  if (focusedPaneIndex < 0 || focusedPaneIndex >= sanitizedPanes.length) focusedPaneIndex = 0;

  return { panes: sanitizedPanes, splitMode, focusedPaneIndex };
}

// allOpenNoteIds is a convenience for callers that need to know which notes
// are currently displayed (e.g., the presence claim broadcaster).
export function allOpenNoteIds(state) {
  const set = new Set();
  for (const pane of state.panes) {
    for (const id of pane.tabs) set.add(id);
  }
  return Array.from(set);
}

export function activeNoteIdFor(state, paneIndex = state.focusedPaneIndex) {
  if (!validPaneIndex(state, paneIndex)) return null;
  return state.panes[paneIndex].activeId || null;
}

if (typeof window !== 'undefined') {
  window.NoteTabs = {
    MAX_PANES,
    initialState,
    openTab,
    setActiveTab,
    closeTab,
    splitRight,
    unsplit,
    focusPane,
    reorder,
    hydrate,
    allOpenNoteIds,
    activeNoteIdFor,
  };
}

export default {
  MAX_PANES,
  initialState,
  openTab,
  setActiveTab,
  closeTab,
  splitRight,
  unsplit,
  focusPane,
  reorder,
  hydrate,
  allOpenNoteIds,
  activeNoteIdFor,
};
