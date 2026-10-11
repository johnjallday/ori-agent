// Snapshot-only presentation. No paths, network, model prose or read authority.
// Match foldercontext: every entry in the bounded snapshot can be a topic.
export const MAX_FOLDER_FOCUS = 64;
export const MAX_FOLDER_FOCUS_BYTES = 160 * 1024;

export function folderTreeView(observation) {
  const tree = observation?.tree;
  if (!tree || !Array.isArray(tree.nodes) || tree.nodes.length > 64) return null;
  const entries = [],
    byId = new Map(),
    paths = new Map(),
    labels = new Map();
  for (const [index, node] of tree.nodes.entries()) {
    if (
      node?.id !== `entry-${index}` ||
      typeof node.name !== 'string' ||
      !node.name ||
      !['folder', 'file'].includes(node.kind)
    )
      return null;
    const parentId = node.parent_id || '';
    if (parentId && byId.get(parentId)?.kind !== 'folder') return null;
    const names = [...(paths.get(parentId) || []), node.name];
    if (names.length > 4) return null;
    const key = JSON.stringify(names);
    const entry = {
      id: node.id,
      parentId,
      name: node.name,
      kind: node.kind,
      names,
      label: names.join(' › '),
      children: []
    };
    entries.push(entry);
    byId.set(entry.id, entry);
    paths.set(entry.id, names);
    labels.set(key, (labels.get(key) || 0) + 1);
    if (parentId) byId.get(parentId).children.push(entry);
  }
  for (const entry of entries) entry.ambiguous = labels.get(JSON.stringify(entry.names)) > 1;
  return {
    entries,
    roots: entries.filter(entry => !entry.parentId),
    byId,
    omitted: Math.max(0, Number(tree.omitted) || 0)
  };
}

export function folderFocusView(observation, ids = []) {
  if (!Array.isArray(ids) || ids.length > MAX_FOLDER_FOCUS || new Set(ids).size !== ids.length)
    return null;
  const focus = { folder: observation?.folder || '', topics: [] };
  if (ids.length) {
    const tree = folderTreeView(observation);
    if (!tree) return null;
    for (const id of ids) {
      const entry = tree.byId.get(id);
      if (!entry || entry.ambiguous) return null;
      focus.topics.push({ names: [...entry.names], kind: entry.kind });
    }
  }
  // Mirror Go's escaped JSON byte bound, including hostile-looking names.
  const encoded = JSON.stringify(focus).replace(
    /[<>&\u2028\u2029]/g,
    character => `\\u${character.charCodeAt(0).toString(16).padStart(4, '0')}`
  );
  return new TextEncoder().encode(encoded).length <= MAX_FOLDER_FOCUS_BYTES ? focus : null;
}

// Checked entries are local selection state, not necessarily individual host
// topics. Only an exact full selection may fall back to whole-folder focus;
// an oversized/ambiguous subset must still fail, never silently broaden scope.
export function folderSelectionFocus(observation, ids = []) {
  const focus = folderFocusView(observation, ids);
  if (focus) return focus;
  if (!Array.isArray(ids) || new Set(ids).size !== ids.length) return null;
  const tree = folderTreeView(observation);
  if (
    !tree?.entries.length ||
    ids.length !== tree.entries.length ||
    !ids.every(id => tree.byId.has(id))
  )
    return null;
  return folderFocusView(observation, []);
}

// Keep every checkbox selected even when Send uses existing whole-folder focus.
export function folderSelectAllFocus(observation) {
  const tree = folderTreeView(observation);
  if (!tree?.entries.length) return null;
  const ids = tree.entries.map(entry => entry.id);
  if (!folderSelectionFocus(observation, ids)) return null;
  const wholeFolder = !folderFocusView(observation, ids);
  return { ids, wholeFolder, count: ids.length };
}

export function renderFolderFocus(row, focus) {
  if (!row?.firstElementChild || !focus || !Array.isArray(focus.topics)) return;
  row.querySelector('[data-folder-turn-focus]')?.remove();
  const group = document.createElement('div');
  group.className = 'personal-assistant-folder-focus__sent';
  group.dataset.folderTurnFocus = 'true';
  group.setAttribute('role', 'group');
  group.setAttribute('aria-label', `Discussion focus for this message in ${focus.folder}`);
  const labels = focus.topics.length
    ? focus.topics.map(topic => topic.names.join(' › '))
    : ['Whole folder'];
  for (const label of labels) {
    const badge = document.createElement('span');
    badge.textContent = label;
    group.append(badge);
  }
  row.firstElementChild.prepend(group);
}
