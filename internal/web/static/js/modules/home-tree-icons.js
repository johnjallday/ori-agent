// home-tree-icons.js — the line icons the Home file tree and its pane share.
//
// One 16×16 path per icon, drawn with the current text colour (the stroke
// rules are `.cockpit-tree-icon` in home-workspace-cockpit.css). Every icon is
// decoration: the row or button it sits in carries the words, so each one is
// `aria-hidden`.

const PATHS = {
  caretRight: 'M6 3.5 10.5 8 6 12.5',
  caretDown: 'M3.5 6 8 10.5 12.5 6',
  group: 'M1.5 4.5h4.2l1.6 1.7h7.2v6.8h-13z',
  folder: 'M1.5 4.5h4.2l1.6 1.7h7.2v6.8h-13z',
  workspace: 'M2 3h12v10H2zM2 6.2h12',
  note: 'M4 1.8h5.5l3 3v9.4H4zM9.5 1.8v3h3M6 8.5h4.5M6 11h4.5',
  backlog: 'M6 4h7.5M6 8h7.5M6 12h7.5M2.6 4h.8M2.6 8h.8M2.6 12h.8',
  memory: 'M4.5 2h7v12L8 11.4 4.5 14z',
  agent:
    'M8 7.6a2.6 2.6 0 1 0 0-5.2 2.6 2.6 0 0 0 0 5.2zM3 13.8c.5-2.5 2.4-3.9 5-3.9s4.5 1.4 5 3.9',
  file: 'M4 1.8h5.5l3 3v9.4H4zM9.5 1.8v3h3',
  ticket: 'M3 3h10v10H3z',
  ticketDone: 'M3 3h10v10H3zM5.6 8.2l1.8 1.8 3.2-3.8',
  close: 'M4.5 4.5l7 7M11.5 4.5l-7 7',
  back: 'M9.5 3.5 5 8l4.5 4.5',
  more: 'M3.4 8h.2M7.9 8h.2M12.4 8h.2',
  newNote: 'M4 1.8h5.5l3 3v9.4H4zM9.5 1.8v3h3M8.2 7.8v3.8M6.3 9.7h3.8',
  newWorkspace: 'M2 3h12v10H2zM2 6.2h12M8 8v3.4M6.3 9.7h3.4',
  newGroup: 'M1.5 4.5h4.2l1.6 1.7h7.2v6.8h-13zM8 8v3.4M6.3 9.7h3.4',
  importFolder: 'M1.5 4.5h4.2l1.6 1.7h7.2v6.8h-13zM8 7.6v3.8M6.4 9.9 8 11.5l1.6-1.6',
  rescan: 'M13 8a5 5 0 1 1-1.6-3.7M13 2.6v2.9h-2.9',
  settings: 'M2.5 4.5h6M11.5 4.5h2M2.5 11.5h2M7.5 11.5h6M10 3v3M6 10v3',
  undo: 'M5.5 3.5 2.5 6.5l3 3M2.8 6.5h6.4a3.3 3.3 0 0 1 0 6.6H6.5'
};

const SECTION_ICONS = {
  notes: 'note',
  backlog: 'backlog',
  files: 'folder',
  memory: 'memory',
  agents: 'agent'
};

/** The icon name for a tree row. */
export function rowIconName(row) {
  const kind = row && row.kind;
  if (kind === 'section') return SECTION_ICONS[row.section] || 'folder';
  if (kind === 'ticket') return row.meta && row.meta.finished ? 'ticketDone' : 'ticket';
  if (kind === 'memory') return 'memory';
  if (PATHS[kind]) return kind;
  return '';
}

/** Inline SVG markup for an icon, or '' for a name this module does not draw. */
export function iconHTML(name, { size = 15, className = 'cockpit-tree-icon' } = {}) {
  const path = PATHS[name];
  if (!path) return '';
  return (
    `<svg class="${className}" width="${size}" height="${size}" viewBox="0 0 16 16" ` +
    `aria-hidden="true" focusable="false"><path d="${path}"></path></svg>`
  );
}
