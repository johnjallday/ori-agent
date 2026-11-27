// Place only the server's owner/link-qualified relationships. A display name
// never identifies a source. Opening a nested grant loads its own directory;
// actions on the containing folder still use the outer listing's tokens.
export function diskLayout(folders, allowed) {
  const visible = folders.filter(row => allowed(row.workspaceId));
  const key = (owner, id) => JSON.stringify([owner, id]);
  const sources = new Map(visible.map(r => [key(r.workspaceId, r.meta.dirId), r]));
  const roots = [],
    nested = new Map();
  for (const row of visible) {
    const parent = row.inside && sources.get(key(row.inside.workspace_id, row.inside.id));
    if (!parent) roots.push(row);
    else if (row.inside.path !== '.') {
      if (!nested.has(parent.id)) nested.set(parent.id, []);
      nested.get(parent.id).push(row);
    }
  }
  return { roots, nested };
}

export function placedEntries(parent, entries, layout) {
  const source = parent.contentsSource || parent;
  const sourceKey = `${source.workspaceId}/l/${source.meta.dirId}`;
  const nested = layout?.nested.get(sourceKey);
  if (!nested?.length) return entries;
  const prefix = source.meta.path ? `${source.meta.path}/` : '';
  const rows = [...entries];
  const listed = new Map(entries.filter(r => r.kind === 'linkedFolder').map(r => [r.meta.path, r]));
  for (const grant of nested) {
    const path = grant.inside.path;
    if (!path.startsWith(prefix) || path === source.meta.path) continue;
    const next = prefix + path.slice(prefix.length).split('/')[0];
    const entry = listed.get(next);
    if (!entry)
      rows.push(grant); // missing, filtered, or outside a bounded page
    else if (next === path) rows[rows.indexOf(entry)] = { ...entry, contentsSource: grant };
  }
  return rows;
}
