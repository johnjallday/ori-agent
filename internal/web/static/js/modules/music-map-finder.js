// A Home-local metadata browser. Search results and history are hints; the
// current detail and reciprocal card must agree before we reveal a building.
import { STAGE_LABELS, stageLabel } from './field-labels.js';
import { createLibraryClient } from './library-setup.js';
import { ReadEpoch } from './project-library-browser.js';
import { songCardURL, songCardView } from './song-card.js';

const changed = 'This project or its link changed. Search again to see the current projects.';

export function resultLabel(row) {
  return [
    row.connection === 'connected'
      ? 'On map'
      : row.connection === 'catalog_only'
        ? 'Library only'
        : 'Link needs review',
    row.folder_label,
    stageLabel(row.stage) || 'Stage unknown'
  ]
    .filter(Boolean)
    .join(' · ');
}

export async function readFinderSelection(home, row, client, signal, fetchImpl = fetch) {
  const detail = await client.request(`/projects/${encodeURIComponent(row.id)}`, {
    signal,
    cache: 'no-store'
  });
  if (
    detail?.row?.id !== row.id ||
    detail.row.connection !== row.connection ||
    (detail.workspace_id || '') !== (row.workspace_id || '')
  )
    throw new Error(changed);
  if (row.connection !== 'connected') return { detail };
  if (!row.workspace_id) throw new Error(changed);
  const response = await fetchImpl(songCardURL(row.workspace_id), { signal, cache: 'no-store' });
  if (!response.ok) throw new Error('The song card could not be read. Try again.');
  const card = await response.json();
  if (!card.linked || card.home_id !== home || card.entry_id !== row.id) throw new Error(changed);
  return { detail, view: songCardView(card) };
}

export function openMusicMapFinder({
  home,
  query = {},
  remember,
  reveal,
  restoreFocus,
  current,
  onClose
}) {
  const client = createLibraryClient(home);
  const listRead = new ReadEpoch();
  const selectionRead = new ReadEpoch();
  const dialog = document.createElement('dialog');
  dialog.className = 'music-map-finder';
  dialog.setAttribute('aria-labelledby', 'music-map-finder-title');
  dialog.innerHTML = `<header><div><small>Music library</small><h2 id="music-map-finder-title">Find a project</h2></div><button type="button" data-close>Close</button></header>
    <form role="search"><label>Project name<input type="search" maxlength="120" placeholder="Search this Home" autofocus></label><label>Recorded stage<select><option value="">Any stage</option></select></label><button>Search</button></form>
    <p role="status"></p><ul aria-label="Matching projects"></ul><button type="button" data-more hidden>More projects</button><section data-preview hidden tabindex="-1" aria-label="Project preview"></section>`;
  const find = sel => dialog.querySelector(sel);
  const input = find('input'),
    stage = find('select'),
    status = find('[role=status]'),
    rows = find('ul'),
    more = find('[data-more]'),
    preview = find('[data-preview]');
  for (const [key, label] of Object.entries({ ...STAGE_LABELS, unknown: 'Unknown' }))
    stage.add(new Option(label, key));
  input.value = query.text || '';
  stage.value = query.stage || '';
  let cursor = '',
    timer,
    closed = false;
  const live = turn => !closed && turn.current() && current();
  function close(focus = true) {
    if (closed) return;
    closed = true;
    clearTimeout(timer);
    listRead.cancel();
    selectionRead.cancel();
    dialog.close();
    dialog.remove();
    onClose();
    if (focus) restoreFocus();
  }
  async function select(row) {
    const turn = selectionRead.begin();
    preview.hidden = true;
    status.textContent = 'Checking project…';
    try {
      const selection = await readFinderSelection(home, row, client, turn.signal);
      if (!live(turn)) return;
      if (selection.view) {
        const focus = reveal(row, selection.view);
        if (!focus)
          throw new Error(
            'The linked building is not visible on this Home map. Search again after refreshing the map.'
          );
        close(false);
        focus();
        return;
      }
      const heading = document.createElement('h3');
      heading.textContent = row.name;
      const meta = document.createElement('p');
      meta.textContent = resultLabel(row);
      const next = document.createElement('p');
      next.textContent = selection.detail.fields?.next_action || 'No next action recorded.';
      preview.replaceChildren(heading, meta, next);
      preview.hidden = false;
      status.textContent = 'Project preview';
      preview.focus();
    } catch (err) {
      if (live(turn)) status.textContent = err.message || 'Project details unavailable. Try again.';
    }
  }
  function rowElement(row) {
    const li = document.createElement('li'),
      button = document.createElement('button');
    button.type = 'button';
    const title = document.createElement('strong');
    title.textContent = row.name;
    const meta = document.createElement('span');
    meta.textContent = resultLabel(row);
    button.append(title, meta);
    li.append(button);
    button.addEventListener('click', () => void select(row));
    return li;
  }
  async function search(append = false) {
    clearTimeout(timer);
    const turn = listRead.begin();
    selectionRead.cancel();
    preview.hidden = true;
    const query = { text: input.value.trim(), stage: stage.value };
    remember(query);
    const params = new URLSearchParams({ ...query, folder_sections: '1' });
    if (append && cursor) params.set('cursor', cursor);
    else rows.replaceChildren();
    more.hidden = true;
    status.textContent = 'Searching the library…';
    try {
      const page = await client.request(`/projects?${params}`, {
        signal: turn.signal,
        cache: 'no-store'
      });
      if (!live(turn)) return;
      if (!Array.isArray(page?.rows)) throw new Error();
      const added = page.rows.map(rowElement);
      rows.append(...added);
      cursor = page.next_cursor || '';
      more.hidden = !cursor;
      status.textContent = rows.childElementCount
        ? `Showing ${rows.childElementCount} of ${page.total} projects`
        : 'No matching projects.';
      if (append) added[0]?.firstElementChild?.focus();
    } catch {
      if (live(turn)) status.textContent = 'Search unavailable. Choose Search to try again.';
    }
  }
  input.addEventListener('input', () => {
    clearTimeout(timer);
    listRead.cancel();
    selectionRead.cancel();
    rows.replaceChildren();
    more.hidden = preview.hidden = true;
    status.textContent = 'Searching the library…';
    timer = setTimeout(search, 180);
  });
  stage.onchange = () => void search();
  find('form').onsubmit = e => {
    e.preventDefault();
    void search();
  };
  more.onclick = () => void search(true);
  find('[data-close]').onclick = () => close();
  dialog.oncancel = e => {
    e.preventDefault();
    close();
  };
  document.body.append(dialog);
  dialog.showModal();
  void search();
  return { close };
}
