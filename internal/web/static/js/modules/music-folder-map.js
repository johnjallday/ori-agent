// A read-only view of this Home's real workspace tree. A section is only a
// disclosure; it has no coordinates, drag targets, or synthetic workspace ID.
export function folderMapTree(groupID, members, linked) {
  const byParent = new Map();
  for (const row of members) {
    if (!byParent.has(row.parent_id)) byParent.set(row.parent_id, []);
    byParent.get(row.parent_id).push(row);
  }
  const seen = new Set([groupID]);
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
        groups.set(key, { id: key, label: s?.label || 'Other workspaces', hue: s?.hue, tiles: [] });
      groups.get(key).tiles.push({ id: row.id, row, crumb: s?.breadcrumb || '' });
    }
    node.sections = [...groups.values()].sort(
      (a, b) => a.label.localeCompare(b.label) || a.id.localeCompare(b.id)
    );
    return node;
  }
  return visit(groupID);
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
      const title = el('summary');
      const label = el('span', section.label);
      if (section.id) {
        label.className = 'music-source-tag';
        label.style.setProperty('--folder-hue', section.hue);
      }
      title.append(label, ` · ${section.tiles.length}`);
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
