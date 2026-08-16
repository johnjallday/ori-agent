// Plan editor operations.
//
// Every function here is pure: it takes plan content and returns new content,
// so the editor's structural rules can be tested without a DOM and the page
// never mutates what it is rendering.
//
// Two rules hold throughout:
//
//   - Stable ids never change. Reordering, duplicating a neighbour, or editing
//     text leaves every existing id exactly as it was, because dependencies
//     reference ids and a renumbering would silently rewire the plan
//     (FR-8, FR-52).
//   - Removing something other work depends on is refused, not silently
//     repaired. The user is told which items would be orphaned and chooses
//     what to do (FR-51).

let clientIdCounter = 0;

// newClientId mints an id for an element the user just created. The server
// preserves ids it is given, so an element keeps this id for its whole life.
// The prefixes match the server's so ids read consistently wherever they
// appear.
export function newClientId(prefix) {
  clientIdCounter += 1;
  const random = Math.random().toString(36).slice(2, 10);
  return `${prefix}_local_${Date.now().toString(36)}${clientIdCounter}${random}`;
}

function cloneContent(content) {
  return JSON.parse(JSON.stringify(content ?? {}));
}

function groups(content) {
  return Array.isArray(content?.groups) ? content.groups : [];
}

export function findGroup(content, groupId) {
  return groups(content).find(group => group.id === groupId) || null;
}

export function findItem(content, itemId) {
  for (const group of groups(content)) {
    const item = (group.items || []).find(candidate => candidate.id === itemId);
    if (item) return { item, group };
  }
  return null;
}

export function addGroup(content, title = 'New group') {
  const next = cloneContent(content);
  next.groups = groups(next);
  next.groups.push({
    id: newClientId('grp'),
    title,
    outcome: '',
    items: [],
    depends_on: []
  });
  return next;
}

export function addItem(content, groupId, description = 'New task') {
  const next = cloneContent(content);
  const group = findGroup(next, groupId);
  if (!group) return next;
  group.items = group.items || [];
  group.items.push({
    id: newClientId('itm'),
    description,
    details: '',
    assignee: '',
    depends_on: [],
    expected_result: ''
  });
  return next;
}

// duplicateItem copies an item's content under a NEW id. It deliberately does
// not copy dependents: a duplicate is new work, and silently making other
// items depend on it too would change the graph the user was looking at.
export function duplicateItem(content, itemId) {
  const next = cloneContent(content);
  const found = findItem(next, itemId);
  if (!found) return next;

  const { item, group } = found;
  const index = group.items.findIndex(candidate => candidate.id === itemId);
  const copy = {
    ...JSON.parse(JSON.stringify(item)),
    id: newClientId('itm'),
    description: `${item.description} (copy)`
  };
  group.items.splice(index + 1, 0, copy);
  return next;
}

// moveGroup and moveItem reorder by position only. Ids are untouched, so a
// dependency that pointed at an element still points at the same element
// (FR-52).
export function moveGroup(content, groupId, direction) {
  const next = cloneContent(content);
  const list = groups(next);
  const index = list.findIndex(group => group.id === groupId);
  const target = index + (direction === 'up' ? -1 : 1);
  if (index < 0 || target < 0 || target >= list.length) return next;
  [list[index], list[target]] = [list[target], list[index]];
  return next;
}

export function moveItem(content, itemId, direction) {
  const next = cloneContent(content);
  const found = findItem(next, itemId);
  if (!found) return next;

  const list = found.group.items;
  const index = list.findIndex(item => item.id === itemId);
  const target = index + (direction === 'up' ? -1 : 1);
  if (target < 0 || target >= list.length) return next;
  [list[index], list[target]] = [list[target], list[index]];
  return next;
}

// dependentsOf returns the items that depend on the given id. Removal consults
// it first so the user learns what would break before anything is deleted
// (FR-51).
export function dependentsOf(content, itemId) {
  const dependents = [];
  for (const group of groups(content)) {
    for (const item of group.items || []) {
      if ((item.depends_on || []).includes(itemId)) {
        dependents.push(item);
      }
    }
  }
  return dependents;
}

export function groupDependentsOf(content, groupId) {
  return groups(content).filter(group => (group.depends_on || []).includes(groupId));
}

// removeItem refuses while other work depends on the item, and reports which
// items those are. Resolving the dependencies is a decision the user makes
// (FR-51).
export function removeItem(content, itemId) {
  const dependents = dependentsOf(content, itemId);
  if (dependents.length > 0) {
    return {
      content,
      removed: false,
      blockedBy: dependents.map(item => ({ id: item.id, description: item.description }))
    };
  }

  const next = cloneContent(content);
  for (const group of groups(next)) {
    group.items = (group.items || []).filter(item => item.id !== itemId);
  }
  return { content: next, removed: true, blockedBy: [] };
}

// removeGroup refuses while another group depends on it, or while any of its
// items are depended on from outside the group.
export function removeGroup(content, groupId) {
  const blockedBy = groupDependentsOf(content, groupId).map(group => ({
    id: group.id,
    description: group.title
  }));

  const group = findGroup(content, groupId);
  for (const item of group?.items || []) {
    for (const dependent of dependentsOf(content, item.id)) {
      const stillInside = (group.items || []).some(candidate => candidate.id === dependent.id);
      if (!stillInside) {
        blockedBy.push({ id: dependent.id, description: dependent.description });
      }
    }
  }

  if (blockedBy.length > 0) {
    return { content, removed: false, blockedBy };
  }

  const next = cloneContent(content);
  next.groups = groups(next).filter(candidate => candidate.id !== groupId);
  return { content: next, removed: true, blockedBy: [] };
}

// resolveDependencies drops references to an id so the user can remove the
// element afterwards. It is the explicit repair the refusal above points at,
// never something that happens on their behalf.
export function resolveDependencies(content, itemId) {
  const next = cloneContent(content);
  for (const group of groups(next)) {
    group.depends_on = (group.depends_on || []).filter(id => id !== itemId);
    for (const item of group.items || []) {
      item.depends_on = (item.depends_on || []).filter(id => id !== itemId);
    }
  }
  return next;
}

// unavailableAssignees lists items assigned to an agent the workspace no longer
// has. Those items block review until they are removed, replaced, or left
// unassigned — an assignee that vanished must not be quietly dropped (FR-48).
export function unavailableAssignees(content, availableAgents) {
  if (!Array.isArray(availableAgents)) return [];
  const available = new Set(availableAgents);
  const unavailable = [];
  for (const group of groups(content)) {
    for (const item of group.items || []) {
      if (item.assignee && !available.has(item.assignee)) {
        unavailable.push({ id: item.id, description: item.description, assignee: item.assignee });
      }
    }
  }
  return unavailable;
}

export function itemCount(content) {
  return groups(content).reduce((total, group) => total + (group.items || []).length, 0);
}

// SAVE_STATES are the four states the editor distinguishes. "Conflicted" is
// separate from "error" because it has its own resolution — recover or
// discard — rather than a retry (FR-151).
export const SAVE_STATES = {
  saved: { label: 'All changes saved', tone: 'neutral', icon: '✓' },
  unsaved: { label: 'Unsaved changes', tone: 'attention', icon: '●' },
  saving: { label: 'Saving…', tone: 'info', icon: '⟳' },
  conflicted: { label: 'Someone else saved first', tone: 'danger', icon: '!' }
};

export function saveStateMeta(state) {
  return SAVE_STATES[state] || SAVE_STATES.saved;
}

// EditorState tracks the draft being edited and whether it is safe to save.
export class EditorState {
  constructor(plan) {
    this.reset(plan);
  }

  reset(plan) {
    this.planId = plan?.id || '';
    this.title = plan?.title || '';
    this.objective = plan?.objective || '';
    this.content = cloneContent(plan?.draft || {});
    this.revision = Number(plan?.draft_revision || 0);
    this.state = 'saved';
    this.conflict = null;
  }

  apply(mutator) {
    const next = mutator(this.content);
    // Structural helpers return either content or a {content, ...} result.
    this.content = next && next.content ? next.content : next;
    this.state = 'unsaved';
    return next;
  }

  markSaving() {
    this.state = 'saving';
  }

  markSaved(plan) {
    this.revision = Number(plan?.draft_revision || this.revision);
    this.state = 'saved';
    this.conflict = null;
  }

  // markConflicted keeps the user's unsaved content AND the winning version,
  // so neither is lost while they decide (FR-30, FR-151).
  markConflicted(details) {
    this.state = 'conflicted';
    this.conflict = {
      currentRevision: details?.current_revision ?? null,
      current: details?.current ?? null,
      snapshots: details?.recoverable_snapshots ?? [],
      mine: cloneContent(this.content)
    };
  }

  payload({ autosave = false } = {}) {
    return {
      title: this.title,
      objective: this.objective,
      content: this.content,
      revision: this.revision,
      autosave
    };
  }
}
