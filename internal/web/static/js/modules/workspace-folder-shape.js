// Presentation only. Paths sent to the server always remain absolute.
export function shortenWorkspacePath(path, workspace = {}) {
  workspace ||= {};
  const value = String(path || '');
  const home = String(workspace.home_directory || '').replace(/[\\/]+$/, '');
  if (home && value === home) return '~';
  if (home && (value.startsWith(home + '/') || value.startsWith(home + '\\')))
    return '~' + value.slice(home.length);
  return value;
}

export function anchorFolder(workspace = {}) {
  workspace ||= {};
  const id = String(
    workspace.anchor?.directory_reference_id ||
      workspace.shared_data?.anchor_directory_reference_id ||
      ''
  );
  if (!id && workspace.shape !== 'anchored') return null;
  const references = workspace.directory_references || [];
  const reference =
    references.find(ref => ref.id === id) || (references.length === 1 ? references[0] : null);
  return {
    id,
    path: workspace.anchor?.path || reference?.path || '',
    missing: workspace.anchor?.missing === true || reference?.missing === true
  };
}

const readTools = new Set([
  'list_directory',
  'list_directory_with_sizes',
  'directory_tree',
  'search_files',
  'get_file_info',
  'list_allowed_directories',
  'read_file',
  'read_text_file',
  'read_media_file',
  'read_multiple_files'
]);

export function filesystemBindingSubtitle(item, workspace = {}) {
  if (String(item?.name || '').toLowerCase() !== 'filesystem') return '';
  const parts = [item.alias, shortenWorkspacePath(item.roots?.[0], workspace)].filter(Boolean);
  // Missing/null is legacy all-tools, never read-only. Unknown future tools
  // also cannot acquire a read-only label just because they aren't on a denylist.
  if (Array.isArray(item.allowedTools) && item.allowedTools.every(tool => readTools.has(tool)))
    parts.push('read-only');
  return parts.join(' · ');
}

export function anchoredDeletionCopy(workspace = {}) {
  const anchor = anchorFolder(workspace);
  if (!anchor) return '';
  const sidecar = shortenWorkspacePath(workspace.sidecar_path, workspace);
  const path = shortenWorkspacePath(anchor.path, workspace);
  return `This removes Ori's notes, settings and agents for this workspace${sidecar ? ` from ${sidecar}` : ' from its separate workspace folder'}. Your folder${path ? ` ${path}` : ''} is not touched.`;
}
