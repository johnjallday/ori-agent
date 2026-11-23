// Tree metadata is only a UI hint (to avoid reads on unrelated maps).
// The scoped endpoint independently verifies the owner, Home and membership.
export function hasMusicFolderContext(tree, groupID) {
  const stack = (tree || []).map(row => [row, false]);
  while (stack.length) {
    const [row, inherited] = stack.pop();
    if (row?.kind !== 'group') continue;
    const summary = row.group_template;
    const music =
      summary?.kind === 'managed_home'
        ? summary.plugin_id === 'music-project-management'
        : inherited;
    if (row.id === groupID) return music;
    for (const child of row.children || []) stack.push([child, music]);
  }
  return false;
}

// Presentation only: the same verified section gets the same hue on its own
// group map and on the Home map. Never allocate by visible order or use the
// hue as identity. Labels pick a color family; the opaque ID varies its shade.
function hashText(text) {
  let hash = 2166136261;
  for (let i = 0; i < text.length; i++) hash = Math.imul(hash ^ text.charCodeAt(i), 16777619);
  return hash >>> 0;
}

const folderHues = [30, 276, 210, 340, 82, 173];
function sourceHue(section) {
  return folderHues[hashText(section.label) % folderHues.length] + (hashText(section.id) % 25) - 12;
}

export async function readWorkspaceSourceFolders(id, { signal, fetchImpl = fetch } = {}) {
  const res = await fetchImpl(`/api/workspaces/${encodeURIComponent(id)}/source-folders`, {
    headers: { Accept: 'application/json' },
    cache: 'no-store',
    signal
  });
  if (!res.ok) throw new Error('Workspace source folders unavailable');
  const page = await res.json();
  // This read is a single bounded snapshot, never a partial page.
  if (!Number.isSafeInteger(page?.revision) || page.revision < 1 || !Array.isArray(page.rows)) {
    throw new Error('Workspace source folders incomplete');
  }
  const labels = new Map();
  for (const row of page.rows) {
    const workspaceID = row?.workspace_id;
    const section = row?.folder_section;
    if (
      typeof workspaceID !== 'string' ||
      !workspaceID ||
      labels.has(workspaceID) ||
      typeof section?.id !== 'string' ||
      !section.id ||
      typeof section.label !== 'string' ||
      !section.label ||
      (section.breadcrumb !== undefined && typeof section.breadcrumb !== 'string')
    ) {
      throw new Error('Workspace source folders incomplete');
    }
    labels.set(workspaceID, { ...section, hue: sourceHue(section) });
  }
  return labels;
}
