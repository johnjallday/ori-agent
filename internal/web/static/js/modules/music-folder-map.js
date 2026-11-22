// A read-only view of this Home's real workspace tree. A section is only a
// disclosure; it has no coordinates, drag targets, or synthetic workspace ID.
export function folderMapTree(homeID, members, catalog) {
  const linked = new Map();
  for (const row of catalog) {
    if (row.connection !== 'connected' || !row.workspace_id) continue;
    linked.set(row.workspace_id, linked.has(row.workspace_id) ? null : row.folder_section);
  }
  const byParent = new Map();
  for (const row of members) {
    if (!byParent.has(row.parent_id)) byParent.set(row.parent_id, []);
    byParent.get(row.parent_id).push(row);
  }
  const seen = new Set([homeID]);
  function visit(id) {
    const node = { id, children: [], sections: [] };
    const groups = new Map();
    for (const row of byParent.get(id) || []) {
      if (seen.has(row.id)) continue;
      seen.add(row.id);
      if (byParent.has(row.id) || row.kind === 'group') {
        node.children.push({ ...visit(row.id), row });
        continue;
      }
      const s = linked.get(row.id);
      const key = s?.id || '';
      if (!groups.has(key))
        groups.set(key, { id: key, label: s?.label || 'Other workspaces', tiles: [] });
      groups.get(key).tiles.push({ id: row.id, row, crumb: s?.breadcrumb || '' });
    }
    node.sections = [...groups.values()].sort(
      (a, b) => a.label.localeCompare(b.label) || a.id.localeCompare(b.id)
    );
    return node;
  }
  return visit(homeID);
}

// Only reads project metadata, never activation eligibility or project files.
export async function readFolderMap(client) {
  let cursor = '';
  const rows = [];
  const seen = new Set();
  do {
    const query = new URLSearchParams({
      folder_sections: '1',
      page_size: '50',
      connection: 'connected'
    });
    if (cursor) query.set('cursor', cursor);
    const page = await client.request(`/projects?${query}`);
    rows.push(...page.rows);
    cursor = page.next_cursor || '';
    if (cursor && (seen.has(cursor) || rows.length >= 5000))
      throw new Error('Folder listing incomplete');
    seen.add(cursor);
  } while (cursor);
  return rows;
}

export function drawFolderMap(doc, tree, open) {
  const el = (tag, text) => {
    const e = doc.createElement(tag);
    if (text) e.textContent = text;
    return e;
  };
  const tile = row => {
    const b = el('button', row.name || 'Workspace');
    b.type = 'button';
    b.dataset.folderWorkspace = row.id;
    b.addEventListener('click', () => open(row.id));
    return b;
  };
  const info = row =>
    [row.status, ...(Array.isArray(row.agents) ? row.agents : [])].filter(Boolean).join(' · ');
  function draw(node, host) {
    for (const child of node.children) {
      const g = el('section');
      g.className = 'music-folder-map__district';
      const h = el('h3');
      h.append(tile(child.row));
      g.append(h, el('small', info(child.row)));
      draw(child, g);
      host.append(g);
    }
    for (const section of node.sections) {
      const details = el('details');
      details.open = true;
      const title = el('summary', `${section.label} · ${section.tiles.length}`);
      details.append(title);
      const tiles = el('ul');
      for (const t of section.tiles) {
        const li = el('li');
        if (t.crumb) li.append(el('small', t.crumb));
        li.append(tile(t.row), el('small', info(t.row)));
        tiles.append(li);
      }
      details.append(tiles);
      host.append(details);
    }
  }
  const panel = el('section');
  panel.className = 'music-folder-map';
  panel.setAttribute('aria-label', 'Workspaces by folder');
  draw(tree, panel);
  return panel;
}
