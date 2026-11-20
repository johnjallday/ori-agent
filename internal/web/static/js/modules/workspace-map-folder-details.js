import { workspaceFolderFacts } from './workspace-folder-shape.js';

const escape = value =>
  String(value || '').replace(
    /[&<>"']/g,
    char =>
      ({
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;'
      })[char]
  );

export function renderMapFolderDetails(state) {
  if (!state?.id) return '';
  if (state.status !== 'ready')
    return `<p class="ws-map-folder-state" role="status">${state.status === 'loading' ? 'Loading folder details…' : 'Folder details unavailable. Open Workspace to view Details and Files.'}</p>`;
  const facts = workspaceFolderFacts(state.workspace);
  if (!facts) return '<p class="ws-map-folder-state">Folder details unavailable.</p>';
  const path = row =>
    `<span class="ws-map-folder-detail-path" title="${escape(row.path)}">${escape(row.displayPath || 'Location unavailable')}</span>${row.missing ? '<strong class="ws-map-folder-missing">Folder not found</strong>' : ''}`;
  return (
    '<section class="ws-map-folder-details" aria-label="Workspace folders"><dl>' +
    facts.locations
      .map(
        row =>
          `<div><dt>${escape(row.label)}</dt><dd>${path(row)}${row.note ? `<span class="ws-map-folder-note">${escape(row.note)}</span>` : ''}</dd></div>`
      )
      .join('') +
    '</dl>' +
    (facts.linked.length
      ? '<h4>Linked folders</h4><ul>' +
        facts.linked
          .map(row => `<li>${row.name ? `<span>${escape(row.name)}</span>` : ''}${path(row)}</li>`)
          .join('') +
        '</ul>'
      : '') +
    (facts.locations.some(row => row.missing)
      ? '<p class="ws-map-folder-note">Open Workspace, then Details or Files to relink the folder. Tool access is managed there.</p>'
      : '') +
    '</section>'
  );
}

// One selection, one read. Generations guard A → B → A as well as late errors,
// refreshes, deselection and owner changes. No cross-selection stale cache.
export function createMapFolderSelection({
  fetchImpl = (...args) => fetch(...args),
  onChange = () => {}
} = {}) {
  let state = { id: '', status: 'idle', workspace: null };
  let generation = 0;
  let pending = null;
  function select(id, { force = false } = {}) {
    id = String(id || '');
    if (id === state.id && !force) return pending || Promise.resolve(state);
    const token = ++generation;
    state = { id, status: id ? 'loading' : 'idle', workspace: null };
    onChange(state);
    if (!id) {
      pending = null;
      return Promise.resolve(state);
    }
    pending = (async () => {
      try {
        const response = await fetchImpl(`/api/workspaces/${encodeURIComponent(id)}`, {
          headers: { Accept: 'application/json' },
          cache: 'no-store'
        });
        if (!response.ok) throw new Error('Folder detail unavailable');
        const body = await response.json();
        const workspace = body.workspace || body.folder || body;
        if (workspace.id !== id) throw new Error('Folder detail identity mismatch');
        if (token !== generation || state.id !== id) return state;
        state = { id, status: 'ready', workspace };
      } catch (_) {
        if (token !== generation || state.id !== id) return state;
        state = { id, status: 'unavailable', workspace: null };
      }
      if (token === generation) {
        pending = null;
        onChange(state);
      }
      return state;
    })();
    return pending;
  }
  return {
    read: () => state,
    select,
    refresh: () => (state.id ? select(state.id, { force: true }) : Promise.resolve(state))
  };
}
