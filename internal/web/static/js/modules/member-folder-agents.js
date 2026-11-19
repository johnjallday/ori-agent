// member-folder-agents.js — the folder agents of the workspaces under one group,
// shaped for that group's page map (waiting-agents-on-parent-map).
//
// The folder-agent listing holds every view the user has. A group's page needs
// only the ones whose workspace stands under that group, at any depth and never
// the group's own (the page's own Commander speaks for the group), and from
// them two small things the map draws: the agents that wait on the owner (a
// quiet mark at their place) and the agents that are working (a status line on
// their tile). Everything is pure: the host reads the listing and the members
// tree, hands both here, and hands the result to the map's in-place updates.
//
// A wait is whatever the Commander already calls one: commanderOneLine(...).waiting.
// This module never defines a second meaning, so the mark, the Next action
// strip and the balloon cannot disagree.

import { commanderOneLine } from './commander-speech.js';
import {
  folderAgentLive,
  folderAgentReportView,
  folderAgentTileLine
} from './folder-agent-card.js';

// While a member is looking, setting up or reading the songs its line moves, so
// the page reads the listing this often. Otherwise it reads on mount and when a
// folder agent changed (ori:folder-agent-changed), never on a timer.
export const MEMBER_POLL_FAST_MS = 2000;

/**
 * The ids of every workspace under `groupId`, at any depth, never `groupId`
 * itself. `rows` are the flat members rows (`{id, parent_id}`), the same ones
 * the page hands its map. Breadth-first with a visited set, so a malformed
 * cycle ends instead of looping.
 */
export function memberWorkspaceIds(rows, groupId) {
  const group = String(groupId || '').trim();
  const ids = new Set();
  if (!group) return ids;
  const childrenOf = new Map();
  (Array.isArray(rows) ? rows : []).forEach(row => {
    if (!row || !row.id || !row.parent_id) return;
    const list = childrenOf.get(row.parent_id) || [];
    list.push(row.id);
    childrenOf.set(row.parent_id, list);
  });
  const visited = new Set([group]);
  const queue = [group];
  for (let at = 0; at < queue.length; at += 1) {
    (childrenOf.get(queue[at]) || []).forEach(id => {
      if (visited.has(id)) return;
      visited.add(id);
      ids.add(id);
      queue.push(id);
    });
  }
  return ids;
}

/** The listed views whose workspace stands under the group (never its own). */
export function memberFolderAgentViews(views, rows, groupId) {
  const ids = memberWorkspaceIds(rows, groupId);
  return (Array.isArray(views) ? views : []).filter(
    view => view && view.workspace_id && ids.has(view.workspace_id)
  );
}

/**
 * The agents that wait on the owner, by workspace id: for each, the Commander's
 * own words (the status words, the line's text, its button's label, the phase)
 * and the agent's name. The map trims, caps, escapes and rolls them up.
 */
export function memberWaits(views) {
  const waits = {};
  (Array.isArray(views) ? views : []).forEach(view => {
    const report = folderAgentReportView(view);
    const line = report ? commanderOneLine(report) : null;
    if (!line || !line.waiting) return;
    waits[report.workspaceID] = {
      status: line.status,
      text: line.text,
      action: line.action,
      phase: line.phase,
      agent: report.agent
    };
  });
  return waits;
}

/**
 * The live line of every member whose agent is working (looking, setting up,
 * reading the songs), by workspace id: the line the tile's status slot shows,
 * as on Home's map. Empty for every member that is not working, so a place goes
 * back to its own status the moment the work ends.
 */
export function memberTileLines(views) {
  const lines = {};
  (Array.isArray(views) ? views : []).forEach(view => {
    const line = view && view.workspace_id ? folderAgentTileLine(view) : '';
    if (line) lines[view.workspace_id] = line;
  });
  return lines;
}

/** Whether to read the listing again soon: a member's agent is working. */
export function shouldPollMembersFast(views) {
  return (Array.isArray(views) ? views : []).some(view => folderAgentLive(view));
}

/**
 * Whether a member's setup run finished since the last read. A run can add
 * workspaces under the group, so the page reloads its members tree when one
 * does. `previous` is what the last call returned as `next` (null before the
 * first read, so a page load never replays an old run). A run is finished when
 * its place is set up and its finish time is new for a member the page already
 * knew; a member first seen now, or one still working, is not.
 */
export function memberSetupsFinished(previous, views) {
  const next = {};
  let finished = false;
  (Array.isArray(views) ? views : []).forEach(view => {
    if (!view || !view.workspace_id) return;
    const at = (view.setup && view.setup.finished_at) || '';
    next[view.workspace_id] = at;
    if (
      previous &&
      Object.hasOwn(previous, view.workspace_id) &&
      view.state === 'set_up' &&
      at &&
      previous[view.workspace_id] !== at
    ) {
      finished = true;
    }
  });
  return { finished, next };
}

/**
 * Everything the page needs from one read of the listing: the member views,
 * their waits, their tile lines, and whether to poll fast.
 */
export function memberFolderAgentState(views, rows, groupId) {
  const members = memberFolderAgentViews(views, rows, groupId);
  return {
    views: members,
    waits: memberWaits(members),
    lines: memberTileLines(members),
    fast: shouldPollMembersFast(members)
  };
}
