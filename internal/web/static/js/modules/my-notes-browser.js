import { notesRequest } from './notes-api.js';
import { isCreatedByOri } from './notes-station.js';
import { previewParagraphs, secondBrainPreviewPath } from './personal-assistant-second-brain.js';

export function initialNotesFolder(view) {
  return isCreatedByOri(view) ? ['Inbox'] : [];
}

export function notesDirectoryQuery(ref, parts) {
  const params = new URLSearchParams({ ref });
  for (const part of parts) params.append('part', part);
  return '/browse?' + params;
}

export function notebookNoteActions(view, parts, name) {
  let preview = '';
  if (isCreatedByOri(view)) {
    const [first, project] = parts;
    if (parts.length === 1 && ['Inbox', 'Daily'].includes(first) && !/^readme\.md$/i.test(name)) {
      preview = secondBrainPreviewPath(first.toLowerCase(), name);
    } else if (parts.length === 2 && first === 'Projects' && name === 'README.md') {
      preview = secondBrainPreviewPath('project', project);
    }
    if (preview)
      preview =
        preview.slice(preview.indexOf('/preview/')) +
        '?' +
        new URLSearchParams({ ref: view.browse_ref });
  }
  // Use the existing vault-relative Obsidian handoff, never a fabricated link
  // for a plain Markdown folder (including an Ori notebook without Obsidian).
  const external =
    view?.source?.kind === 'obsidian'
      ? 'obsidian://open?vault=' +
        encodeURIComponent(view.source.name) +
        '&file=' +
        encodeURIComponent([...parts, name.replace(/\.(?:md|markdown)$/i, '')].join('/'))
      : '';
  return { preview, external };
}

// The server refuses a slash in any folder name, so a folder's names joined by
// "/" identify it within one connection.
export function folderKey(parts) {
  return parts.join('/');
}

function keyParts(key) {
  return key ? key.split('/') : [];
}

function sameFolder(a, b) {
  return Array.isArray(a) && Array.isArray(b) && folderKey(a) === folderKey(b);
}

export function directoryProblem(status, root = false) {
  if (status === 404) {
    return root
      ? 'Ori could not reach your notes folder. It may have moved or be unavailable.'
      : 'This folder may have been moved or removed. Choose another folder to keep browsing.';
  }
  if (status === 409) return 'Your notes connection changed. Try this folder again.';
  if (status === 403) return 'Ori cannot list this folder. Use Open folder to see it outside Ori.';
  return 'Ori could not list this folder. It may be unavailable or unreadable.';
}

export function noteProblem(error) {
  if (error?.status === 404)
    return { status: 'missing', message: 'This note may have been moved or removed.' };
  // The server's own sentence: the note is the user's, or the user edited it.
  if (error?.status === 403)
    return {
      status: 'yours',
      message: error.message || 'That note is yours; open it in your editor.'
    };
  if (error?.status === 409)
    return { status: 'error', stale: true, message: 'Your notes connection changed.' };
  return { status: 'error', message: 'Ori could not open this note. Try again.' };
}

// Listing a note is not the same as being able to show it: Ori previews only
// the notes it wrote, in the places it writes them.
export function unsupportedNoteLine(view) {
  return isCreatedByOri(view)
    ? 'Ori shows the notes it wrote in Inbox, Daily and each project’s README here. Open this one in your editor.'
    : 'Ori does not show notes from a connected folder here. Open this one in your editor.';
}

function listedEntry(entry) {
  return typeof entry?.name === 'string' && entry.name !== '';
}

// NotebookState is the browser without the page: which folders are listed,
// which folder and note are chosen, and what the reader shows. Only the newest
// answer for a folder or note, within one connection, can change it.
export class NotebookState {
  constructor({ request = notesRequest, onChange = () => {}, onStale = () => {} } = {}) {
    this.request = request;
    this.onChange = onChange;
    this.onStale = onStale;
    this.generation = 0;
    this.controllers = new Set();
    this.reset();
  }

  reset() {
    this.view = null;
    this.nodes = new Map();
    this.folder = null;
    this.note = null;
    this.reader = null;
    this.previewController = null;
    this.landing = null;
    this.staleReported = false;
  }

  changed() {
    this.onChange();
  }

  // clear forgets the notebook and makes every request still in flight
  // obsolete, so nothing from an earlier connection can be shown again.
  clear() {
    this.generation += 1;
    for (const controller of this.controllers) controller.abort();
    this.controllers.clear();
    this.reset();
  }

  start(view, folder = initialNotesFolder(view)) {
    this.clear();
    if (!view?.browse_ref) return;
    this.view = view;
    this.openFolder(folder, { landing: true });
  }

  node(parts) {
    const key = folderKey(parts);
    let node = this.nodes.get(key);
    if (!node) {
      node = {
        parts: [...parts],
        status: 'idle',
        entries: [],
        partial: false,
        expanded: false,
        code: 0,
        error: '',
        controller: null
      };
      this.nodes.set(key, node);
    }
    return node;
  }

  // openFolder chooses a folder: its ancestors stay listed and open in the tree
  // and the folder itself is listed again, so the note list is current.
  openFolder(parts, { landing = false } = {}) {
    if (!this.view || !Array.isArray(parts)) return;
    this.landing = landing ? folderKey(parts) : null;
    this.closeNote();
    this.folder = [...parts];
    for (let depth = 0; depth < parts.length; depth++) {
      const ancestor = parts.slice(0, depth);
      this.node(ancestor).expanded = true;
      void this.load(ancestor);
    }
    const node = this.node(parts);
    node.expanded = true;
    // A listing already on its way is as current as a new one would be.
    void this.load(parts, { force: node.status !== 'loading' });
    this.changed();
  }

  toggleFolder(parts) {
    if (!this.view || !parts.length) return;
    const node = this.node(parts);
    node.expanded = !node.expanded;
    if (node.expanded) void this.load(parts);
    this.changed();
  }

  refresh(parts) {
    if (!this.view || !Array.isArray(parts) || !this.nodes.has(folderKey(parts))) return;
    void this.load(parts, { force: true });
    this.changed();
  }

  async load(parts, { force = false } = {}) {
    if (!this.view) return;
    const node = this.node(parts);
    if (!force && (node.status === 'ready' || node.status === 'loading')) return;
    node.controller?.abort();
    const controller = new AbortController();
    const generation = this.generation;
    node.controller = controller;
    this.controllers.add(controller);
    // A folder already listed keeps its names while it is listed again.
    if (node.status !== 'ready') node.status = 'loading';
    let result;
    try {
      const payload = await this.request(notesDirectoryQuery(this.view.browse_ref, parts), {
        signal: controller.signal
      });
      const directory = payload?.directory;
      if (!Array.isArray(directory?.entries)) throw new Error('No listing was returned.');
      result = {
        status: 'ready',
        entries: directory.entries
          .filter(listedEntry)
          .map(entry => ({ name: entry.name, folder: entry.folder === true })),
        partial: directory.partial === true,
        code: 0,
        error: ''
      };
    } catch (error) {
      result = {
        status: 'error',
        entries: [],
        partial: false,
        code: error?.status || 0,
        error: directoryProblem(error?.status, parts.length === 0)
      };
    }
    this.controllers.delete(controller);
    if (
      controller.signal.aborted ||
      generation !== this.generation ||
      node.controller !== controller
    )
      return;
    node.controller = null;
    Object.assign(node, result);
    if (result.status === 'error') this.directoryFailed(node);
    this.changed();
  }

  directoryFailed(node) {
    const root = node.parts.length === 0;
    if (node.code === 409 || (root && node.code === 404)) this.reportStale();
    // Inbox is only where an Ori notebook opens. When it is gone, open the
    // notebook's real root rather than an error about a folder never chosen.
    if (
      !root &&
      node.code === 404 &&
      this.landing === folderKey(node.parts) &&
      sameFolder(this.folder, node.parts)
    ) {
      this.openFolder([]);
    }
  }

  reportStale() {
    if (this.staleReported) return;
    this.staleReported = true;
    this.onStale();
  }

  async openNote(name) {
    if (!this.view || !this.folder || typeof name !== 'string' || !name) return;
    this.closeNote();
    const parts = this.folder;
    const actions = notebookNoteActions(this.view, parts, name);
    this.note = name;
    if (!actions.preview) {
      this.reader = { status: 'unsupported', name, parts, actions };
      this.changed();
      return;
    }
    const reader = { status: 'loading', name, parts, actions };
    const controller = new AbortController();
    const generation = this.generation;
    this.reader = reader;
    this.previewController = controller;
    this.controllers.add(controller);
    this.changed();
    let result;
    try {
      const payload = await this.request(actions.preview, { signal: controller.signal });
      if (!payload?.preview) throw new Error('No preview was returned.');
      result = { status: 'ready', preview: payload.preview };
    } catch (error) {
      result = noteProblem(error);
    }
    this.controllers.delete(controller);
    if (controller.signal.aborted || generation !== this.generation || this.reader !== reader)
      return;
    this.previewController = null;
    this.reader = { ...reader, ...result };
    // A note that is gone may mean the folder changed: list it again.
    if (result.status === 'missing') void this.load(parts, { force: true });
    if (result.stale) this.reportStale();
    this.changed();
  }

  closeNote() {
    this.previewController?.abort();
    this.previewController = null;
    this.note = null;
    this.reader = null;
  }
}

// notebookFolderTree is the Folders pane: the root and every folder opened in
// it, each from a real listing. Nothing is shown that was not listed.
export function notebookFolderTree(state) {
  if (!state.view) return null;
  const build = parts => {
    const node = state.nodes.get(folderKey(parts));
    const ready = node?.status === 'ready';
    const folders = ready ? node.entries.filter(entry => entry.folder) : [];
    const root = parts.length === 0;
    const expanded = root || Boolean(node?.expanded);
    return {
      key: folderKey(parts),
      name: root ? state.view.source?.name || 'Notebook' : parts.at(-1),
      selected: sameFolder(parts, state.folder),
      // A folder not listed yet may hold folders; a listed one shows a toggle
      // only when it does.
      expandable: !root && (!ready || folders.length > 0),
      expanded,
      status: expanded ? node?.status || 'idle' : 'idle',
      partial: expanded && ready && node.partial,
      children: expanded ? folders.map(entry => build([...parts, entry.name])) : []
    };
  };
  return build([]);
}

// notebookListView is the chosen folder's notes.
export function notebookListView(state) {
  if (!state.view || !state.folder) return null;
  const node = state.nodes.get(folderKey(state.folder));
  const title = state.folder.at(-1) || state.view.source?.name || 'Notebook';
  if (!node || node.status === 'idle' || node.status === 'loading') {
    return { title, status: 'loading', message: 'Loading names…', notes: [] };
  }
  if (node.status === 'error') return { title, status: 'error', message: node.error, notes: [] };
  const notes = node.entries.filter(entry => !entry.folder);
  const folders = node.entries.length - notes.length;
  let message = '';
  if (node.partial) {
    message = 'Only some names could be listed. Open the notebook folder for the full contents.';
  } else if (!notes.length) {
    message = folders
      ? `No notes directly in ${title}. Choose one of its folders.`
      : 'No folders or Markdown notes here.';
  }
  return {
    title,
    status: 'ready',
    message,
    partial: node.partial,
    notes: notes.map(entry => ({ name: entry.name, selected: entry.name === state.note }))
  };
}

// notebookReaderView is the chosen note: Ori's preview when it can show one,
// otherwise a plain reason and a way to open it outside Ori.
export function notebookReaderView(state) {
  if (!state.view || !state.folder) return null;
  const reader = state.reader;
  if (!reader)
    return { status: 'none', title: 'No note open', message: 'Choose a note to read it here.' };
  const { name, parts, actions } = reader;
  const base = {
    status: reader.status,
    name,
    title: name,
    place: [...parts, name].join(' / '),
    external: actions.external,
    message: ''
  };
  switch (reader.status) {
    case 'loading':
      return { ...base, message: 'Opening…' };
    case 'ready': {
      const title = reader.preview.title || name;
      const paragraphs = previewParagraphs(reader.preview.body);
      // The title is the note's first heading; don't print it twice.
      if (paragraphs[0]?.trim() === `# ${title}`) paragraphs.shift();
      return {
        ...base,
        title,
        paragraphs,
        truncated: reader.preview.truncated === true
      };
    }
    case 'unsupported':
      return { ...base, message: unsupportedNoteLine(state.view) };
    default:
      return { ...base, message: reader.message, retry: reader.status === 'error' };
  }
}

function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (value === undefined || value === null || value === false) continue;
    if (key === 'className') node.className = value;
    else if (key === 'text') node.textContent = value;
    else if (key === 'dataset') Object.assign(node.dataset, value);
    else node.setAttribute(key, value === true ? '' : String(value));
  }
  node.append(...children.filter(Boolean));
  return node;
}

function actionButton(text, action, label) {
  return el('button', {
    type: 'button',
    className: 'modern-btn modern-btn-secondary modern-btn-sm',
    'aria-label': label,
    dataset: { browserAction: action, focusKey: 'action:' + action },
    text
  });
}

export class MyNotesBrowser {
  constructor(root, { openFolder, recheck = () => {} }) {
    this.root = root;
    this.openFolder = openFolder;
    this.state = new NotebookState({ onChange: () => this.render(), onStale: () => recheck() });
    this.panes = null;
    this.signatures = {};
    root.addEventListener('click', event => this.click(event));
  }

  get view() {
    return this.state.view;
  }

  clear() {
    this.state.clear();
    this.panes = null;
    this.signatures = {};
    this.root.hidden = true;
    this.root.replaceChildren();
  }

  start(view, folder) {
    this.clear();
    this.state.start(view, folder);
  }

  // refreshInbox lists Inbox again only while it is the open folder; any
  // other time, choosing Inbox lists it fresh anyway.
  refreshInbox() {
    if (sameFolder(this.state.folder, ['Inbox'])) this.state.refresh(['Inbox']);
  }

  click(event) {
    const control = event.target.closest('[data-browser-action]');
    if (!control || !this.root.contains(control) || control.disabled) return;
    const state = this.state;
    const { browserAction, folderKey: key = '', note } = control.dataset;
    switch (browserAction) {
      case 'folder':
        return state.openFolder(keyParts(key));
      case 'toggle':
        return state.toggleFolder(keyParts(key));
      case 'note':
        return void state.openNote(note);
      case 'folder-retry':
        return state.refresh(state.folder);
      case 'note-retry':
        return void state.openNote(state.note);
      case 'open-folder':
        return void this.openFolder();
    }
  }

  render() {
    const state = this.state;
    if (!state.view) return;
    if (!this.panes) {
      const heading = el('h3', { id: 'my-notes-folders-title', text: 'Folders' });
      this.panes = {
        folders: el('nav', {
          className: 'my-notes-pane my-notes-folders',
          'aria-labelledby': 'my-notes-folders-title'
        }),
        list: el('section', {
          className: 'my-notes-pane my-notes-list',
          'aria-labelledby': 'my-notes-list-title'
        }),
        reader: el('section', {
          className: 'my-notes-pane my-notes-reader',
          'aria-labelledby': 'my-notes-reader-title'
        })
      };
      this.folderHeading = heading;
      this.root.replaceChildren(this.panes.folders, this.panes.list, this.panes.reader);
      this.root.hidden = false;
    }
    const active = this.root.contains(document.activeElement) ? document.activeElement : null;
    const focusKey = active?.dataset.focusKey || '';
    const pane = active?.closest('.my-notes-pane');
    this.paint('folders', notebookFolderTree(state));
    this.paint('list', notebookListView(state));
    this.paint('reader', notebookReaderView(state));
    // A repaint replaces the focused control; focus its replacement, or the
    // heading of the pane it was in.
    if (active && !this.root.contains(document.activeElement)) {
      const replacement = [...this.root.querySelectorAll('[data-focus-key]')].find(
        node => node.dataset.focusKey === focusKey
      );
      const fallback = pane?.querySelector('h3[tabindex]') || pane?.querySelector('[aria-current]');
      (replacement || fallback)?.focus({ preventScroll: true });
    }
  }

  paint(name, model) {
    const signature = JSON.stringify(model);
    if (this.signatures[name] === signature) return;
    this.signatures[name] = signature;
    const pane = this.panes[name];
    if (name === 'folders') {
      pane.replaceChildren(
        this.folderHeading,
        el('ul', { className: 'my-notes-tree' }, this.folderItem(model))
      );
    } else if (name === 'list') {
      pane.replaceChildren(...this.listContent(model));
    } else {
      pane.replaceChildren(...this.readerContent(model));
    }
  }

  folderItem(row) {
    const toggle = row.expandable
      ? el(
          'button',
          {
            type: 'button',
            className: 'my-notes-tree-toggle',
            'aria-expanded': String(row.expanded),
            'aria-label': `${row.expanded ? 'Hide' : 'Show'} folders in ${row.name}`,
            dataset: { browserAction: 'toggle', folderKey: row.key, focusKey: 'toggle:' + row.key }
          },
          el('i', { className: 'bi bi-chevron-right', 'aria-hidden': 'true' })
        )
      : el('span', { className: 'my-notes-tree-spacer', 'aria-hidden': 'true' });
    const name = el('button', {
      type: 'button',
      className: 'my-notes-tree-folder',
      'aria-current': row.selected ? 'true' : null,
      dataset: { browserAction: 'folder', folderKey: row.key, focusKey: 'folder:' + row.key },
      text: row.name
    });
    const item = el('li', {}, el('div', { className: 'my-notes-tree-row' }, toggle, name));
    const children = row.children.map(child => this.folderItem(child));
    if (row.status === 'loading' && !children.length)
      children.push(el('li', { className: 'my-notes-tree-line', text: 'Loading…' }));
    if (row.status === 'error')
      children.push(el('li', { className: 'my-notes-tree-line', text: 'Not available' }));
    if (row.partial)
      children.push(
        el('li', { className: 'my-notes-tree-line', text: 'Some names are not shown' })
      );
    if (children.length) item.append(el('ul', {}, ...children));
    return item;
  }

  listContent(model) {
    const out = [el('h3', { id: 'my-notes-list-title', tabindex: '-1', text: model.title })];
    if (model.status !== 'ready') {
      out.push(
        el('p', {
          className: 'my-notes-pane-line',
          role: model.status === 'error' ? 'alert' : 'status',
          text: model.message
        })
      );
      if (model.status === 'error') out.push(actionButton('Try this folder again', 'folder-retry'));
      return out;
    }
    if (model.message)
      out.push(el('p', { className: 'my-notes-pane-line', role: 'status', text: model.message }));
    if (model.notes.length) {
      out.push(
        el(
          'ul',
          { className: 'my-notes-notes' },
          ...model.notes.map(note =>
            el(
              'li',
              {},
              el('button', {
                type: 'button',
                className: 'my-notes-note',
                'aria-current': note.selected ? 'true' : null,
                dataset: { browserAction: 'note', note: note.name, focusKey: 'note:' + note.name },
                text: note.name
              })
            )
          )
        )
      );
    }
    return out;
  }

  readerContent(model) {
    const out = [el('h3', { id: 'my-notes-reader-title', tabindex: '-1', text: model.title })];
    if (model.status === 'none') {
      out.push(el('p', { className: 'my-notes-pane-line', text: model.message }));
      return out;
    }
    out.push(el('p', { className: 'my-notes-reader-place', text: model.place }));
    if (model.status === 'ready') {
      out.push(
        el(
          'div',
          { className: 'my-notes-reader-body' },
          ...model.paragraphs.map(text => el('p', { text }))
        )
      );
      if (model.truncated)
        out.push(
          el('p', { className: 'my-notes-pane-line', text: 'Only the beginning is shown.' })
        );
    } else {
      out.push(
        el('p', {
          className: 'my-notes-pane-line',
          role: model.status === 'loading' ? 'status' : model.retry ? 'alert' : null,
          text: model.message
        })
      );
    }
    if (model.status === 'loading') return out;
    const actions = el('div', { className: 'my-notes-reader-actions' });
    if (model.retry)
      actions.append(actionButton('Try again', 'note-retry', `Try ${model.name} again`));
    // A vault's own link when the folder is an Obsidian vault; the folder
    // handoff always, since a vault on disk does not prove Obsidian is here.
    if (model.external) {
      actions.append(
        el('a', {
          className: 'modern-btn modern-btn-secondary modern-btn-sm',
          href: model.external,
          'aria-label': `Open ${model.name} in Obsidian`,
          dataset: { focusKey: 'action:obsidian' },
          text: 'Open in Obsidian'
        })
      );
    }
    actions.append(actionButton('Open notebook folder', 'open-folder'));
    out.push(actions);
    return out;
  }
}
