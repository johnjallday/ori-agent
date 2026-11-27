/*
 * linked-agents.js — the agents that work in the workspaces inside a group.
 *
 * A group's Details lists its own agents under Team. The workspaces linked
 * into it, and into the groups nested in it at any depth, have agents of their
 * own. The tree read (`GET /api/workspaces?tree=true`) already carries them on
 * every node as `agents` and `entry_agent_name`, so this module only shapes
 * that tree for the page; it reads nothing itself.
 *
 * Agents stay owned by the workspace they work in. Overview and member rows
 * show text; the group map's team deck uses the place ids for in-map navigation.
 */

const text = value => (typeof value === 'string' ? value.trim() : '');

/** A row names this many agents before it says how many more there are. */
export const ROW_AGENT_NAMES = 3;

/** One node's agents, its lead first, once each (names compare without case). */
export function nodeAgentNames(node) {
  if (!node || typeof node !== 'object') return [];
  const seen = new Set();
  const names = [];
  const add = value => {
    const name = text(value && typeof value === 'object' ? value.name || value.agent_name : value);
    const key = name.toLowerCase();
    if (!key || seen.has(key)) return;
    seen.add(key);
    names.push(name);
  };
  add(node.entry_agent_name);
  (Array.isArray(node.agents) ? node.agents : []).forEach(add);
  return names;
}

/**
 * Every agent in the workspaces inside `group`, once each, in tree order: a
 * nested group's own agents come before the agents of the workspaces in it.
 * `places` are the workspaces (or groups) the agent works in, in that order.
 */
export function linkedAgents(group) {
  const byKey = new Map();
  const visited = new Set();
  const walk = node => {
    (node && Array.isArray(node.children) ? node.children : []).forEach(child => {
      if (!child || !child.id || visited.has(child.id)) return;
      visited.add(child.id);
      nodeAgentNames(child).forEach(name => {
        const key = name.toLowerCase();
        if (!byKey.has(key)) byKey.set(key, { name, places: [] });
        byKey.get(key).places.push({ id: child.id, name: text(child.name) || 'Untitled' });
      });
      walk(child);
    });
  };
  walk(group);
  return [...byKey.values()];
}

/** Where a linked agent works: the one place by name, several as a count. */
export function placesLine(places) {
  const list = Array.isArray(places) ? places : [];
  if (!list.length) return '';
  return list.length === 1 ? list[0].name : `${list.length} workspaces`;
}

/** A linked workspace row's agents: the first few names, then "+N more". */
export function rowAgentsLine(names, max = ROW_AGENT_NAMES) {
  const list = (Array.isArray(names) ? names : []).map(text).filter(Boolean);
  if (list.length <= max) return list.join(', ');
  return `${list.slice(0, max).join(', ')} +${list.length - max} more`;
}
