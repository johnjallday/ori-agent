// workspace-map-snapshot.js — shapes a `?tree=true` workspace hierarchy into
// the flat rows and per-id metadata that OriWorkspaceMap.mount consumes.
//
// Shared by Home's cockpit and the group page's Detachment map, so the Command
// view never has to import the Home cockpit coordinator.

export function normalizeKind(kind) {
  return String(kind || '')
    .trim()
    .toLowerCase();
}

export function isGroupWorkspace(workspace) {
  return normalizeKind(workspace && workspace.kind) === 'group';
}

/** Flatten the `?tree=true` hierarchy into the flat rows the Map consumes. */
export function flattenWorkspaceTree(nodes, depth = 0, path = [], parentId = '') {
  const rows = [];
  (Array.isArray(nodes) ? nodes : []).forEach(workspace => {
    if (!workspace) return;
    const currentPath = [...path, workspace.name || 'Untitled'];
    rows.push({
      ...workspace,
      depth,
      parent_id: workspace.parent_id || parentId || '',
      path: currentPath.join(' / ')
    });
    const children = Array.isArray(workspace.children) ? workspace.children : [];
    if (children.length > 0) {
      rows.push(...flattenWorkspaceTree(children, depth + 1, currentPath, workspace.id));
    }
  });
  return rows;
}

export function normalizeTags(tags) {
  if (!Array.isArray(tags)) return [];
  return tags.map(tag => String(tag || '').trim()).filter(Boolean);
}

/**
 * Build the per-id metadata bundle the Map's renderer expects.
 *
 * Mirrors the launcher's `buildLauncherMapMetadata` contract so the production
 * Map renders identically from cockpit state, without importing the launcher.
 */
export function buildMapMetadata(flattened, tree) {
  const folderDisplayById = {};
  const tagsById = {};
  const groupPreviewById = {};

  (Array.isArray(flattened) ? flattened : []).forEach(workspace => {
    if (!workspace || !workspace.id) return;
    folderDisplayById[workspace.id] = folderDisplayFor(workspace);
    tagsById[workspace.id] = normalizeTags(workspace.tags);
  });

  const visit = nodes => {
    (Array.isArray(nodes) ? nodes : []).forEach(workspace => {
      if (!workspace || !workspace.id) return;
      const children = Array.isArray(workspace.children) ? workspace.children : [];
      if (isGroupWorkspace(workspace)) {
        const previewNames = children
          .slice(0, 3)
          .map(child => String((child && (child.name || child.id)) || 'Untitled Workspace').trim())
          .filter(Boolean);
        groupPreviewById[workspace.id] = {
          childCount: children.length,
          previewNames,
          overflowCount: Math.max(0, children.length - previewNames.length)
        };
      }
      if (children.length > 0) visit(children);
    });
  };
  visit(tree);

  return { folderDisplayById, tagsById, groupPreviewById };
}

/**
 * Linked-folder badge data for a workspace, matching the Map's expectations.
 *
 * The wire field is `directory_references` (see `/api/workspaces?tree=true` and
 * the launcher's collectWorkspaceLinkedDirectories). `directories` is accepted
 * as a fallback because some callers pass an already-normalized shape.
 */
export function folderDisplayFor(workspace) {
  const refs = Array.isArray(workspace && workspace.directory_references)
    ? workspace.directory_references
    : Array.isArray(workspace && workspace.directories)
      ? workspace.directories
      : [];
  const directories = refs.filter(Boolean);
  const primary = directories.find(dir => dir && dir.is_primary) || directories[0] || null;
  const path = String((primary && (primary.path || primary.name)) || '').trim();
  if (!path) {
    return {
      linked: false,
      badgeLabel: 'No folder linked',
      badgeClass: 'is-unlinked',
      detail: 'No local folder attached.',
      detailTitle: 'This workspace is not linked to a local folder.',
      ariaLabel: 'no linked folder'
    };
  }
  const short = path.split('/').filter(Boolean).slice(-2).join('/') || path;
  return {
    linked: true,
    badgeLabel: 'Folder',
    badgeClass: 'is-linked',
    detail: short,
    detailTitle: path,
    ariaLabel: `linked folder ${short}`
  };
}
