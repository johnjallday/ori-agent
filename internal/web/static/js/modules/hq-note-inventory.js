/**
 * hq-note-inventory.js — the Notes station's inventory, opened over the
 * Personal HQ map.
 *
 * Clicking Notes on the Free map or its Flow node opens this panel inside
 * Workspace Command's stat modal (the same lifecycle as the Daily Brief panel:
 * one overlay, an inert background, Escape and focus back on the building).
 * Every other way in (the rail, the greeting, the old `?station=` link)
 * still goes to the notebook at `/my-notes`; this panel never replaces it.
 *
 * The panel is live DOM with its own state, built once and attached into the
 * modal, so a map repaint behind it never loses what the person was doing.
 *
 * It reads only what it needs, when it is opened: the notebook connection
 * (the map's own copy when it has one, else `?view=connection`), then the
 * names in the notebook's sections (hq-note-inventory-items.js keeps that
 * bounded). It never sets anything up, starts a look or captures by itself,
 * reads a note only when a person selects it, and drops every reply that
 * lands after it was closed, reopened or the connection changed.
 */

import { notesRequest } from './notes-api.js';
import { MY_NOTES_URL } from './hq-quick-capture.js';
import { openSecondBrainFolder } from './personal-assistant-second-brain.js';
import {
  INVENTORY_PAGE_SIZE,
  INVENTORY_SECTIONS,
  INVENTORY_SECTION_PREVIEW,
  InventoryListings,
  NotePreview,
  combinedCount,
  itemPlace,
  kindLabel,
  previewTarget,
  sectionCount
} from './hq-note-inventory-items.js';
import { inspectorNode, slotsNode, tabsNode } from './hq-note-inventory-view.js';

export const NOTE_INVENTORY_TITLE = 'Note inventory';
export const NOTE_INVENTORY_CONNECTION_PATH = '?view=connection';
export const NOTE_INVENTORY_NARROW_QUERY = '(max-width: 760px)';

// inventoryConnection reads a second-brain view into what the panel can show.
// `owned` is a notebook Ori set up (its sections are known, Ori may write to
// its Inbox); any other connected folder is read-only and shown as it is.
export function inventoryConnection(view) {
  const status = String(view?.status || '');
  const name = String(view?.source?.name || '').trim();
  if (status === 'connected') {
    const owned = view?.source?.origin === 'created_by_ori';
    const count = Number(view?.inbox_count);
    return {
      phase: 'ready',
      name,
      owned,
      ref: String(view?.browse_ref || ''),
      // `kind` is `obsidian` or `markdown`; an Ori notebook is `obsidian` too.
      obsidian: view?.source?.kind === 'obsidian',
      // A count is only a count when the server sent one for a notebook Ori
      // keeps; anything else is unknown, never zero.
      inbox: owned && view?.inbox_count != null && Number.isFinite(count) ? count : null
    };
  }
  if (status === 'missing') return { phase: 'missing', name };
  if (status === 'not_connected') return { phase: 'not_connected', name: '' };
  return { phase: 'unknown', name };
}

// inboxLine says what is known about the Inbox: empty is not "no notes", and a
// count the server did not send is unknown.
export function inboxLine(connection) {
  if (connection?.phase !== 'ready' || !connection.owned) return '';
  if (connection.inbox === null || connection.inbox === undefined) return 'Inbox count not known';
  if (connection.inbox === 0) return 'Inbox empty';
  return connection.inbox + ' in Inbox';
}

// The words for each state the connection can be in before any note shows.
export function connectionCopy(state) {
  switch (state?.phase) {
    case 'checking':
      return { title: 'Checking your notebook…', body: '', tone: 'loading' };
    case 'error':
      return {
        title: 'Ori couldn’t check your notebook.',
        body: state.message || 'Try again, or open My Notes.',
        tone: 'degraded',
        retry: true
      };
    case 'not_connected':
      return {
        title: 'No notebook is connected.',
        body: 'Set one up or connect a folder in My Notes.',
        tone: 'quiet',
        action: 'Set up in My Notes'
      };
    case 'missing':
      return {
        title: state.name ? 'Can’t find ' + state.name + '.' : 'Can’t find your notes folder.',
        body: 'It may have moved or been renamed. Reconnect it in My Notes.',
        tone: 'attention',
        action: 'Repair in My Notes'
      };
    case 'unknown':
      return {
        title: 'Ori doesn’t know this notebook’s state.',
        body: 'Open My Notes to check the connection.',
        tone: 'degraded',
        action: 'Open My Notes'
      };
    case 'ready':
      // A connected notebook Ori cannot browse (no reference): only the door.
      return state.ref
        ? null
        : {
            title: state.name || 'Your notebook',
            body: 'Open My Notes to browse it.',
            tone: 'quiet',
            action: 'Open My Notes'
          };
    default:
      return null;
  }
}

const SECTION_HEADING = Object.freeze({
  all: 'All notes',
  inbox: 'Inbox notes',
  projects: 'Project notes',
  journal: 'Journal'
});

const SECTION_EMPTY = Object.freeze({
  inbox: 'Inbox empty',
  projects: 'No project folders yet',
  journal: 'No journal pages yet'
});

const PARTIAL_LINE = 'Ori lists up to 200 names in a folder here. Open My Notes to see the rest.';

function el(doc, tag, className = '', text = '') {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (text) node.textContent = text;
  return node;
}

// A small notebook, drawn once: the emblem beside the title.
const EMBLEM_SVG =
  '<svg viewBox="0 0 32 32" width="32" height="32" aria-hidden="true" focusable="false">' +
  '<rect x="7" y="4" width="19" height="24" rx="1.5" fill="#8a5a15"/>' +
  '<rect x="9" y="5.5" width="16" height="21" rx="1" fill="#e8b54b"/>' +
  '<rect x="9" y="5.5" width="3" height="21" fill="#c48a2a"/>' +
  '<rect x="14" y="10" width="8" height="1.6" fill="#5a3a0c"/>' +
  '<rect x="14" y="13.5" width="6" height="1.6" fill="#5a3a0c"/>' +
  '<rect x="5.5" y="9" width="3" height="2" rx="0.6" fill="#f6ead6"/>' +
  '<rect x="5.5" y="15" width="3" height="2" rx="0.6" fill="#f6ead6"/>' +
  '<rect x="5.5" y="21" width="3" height="2" rx="0.6" fill="#f6ead6"/>' +
  '</svg>';

export class HQNoteInventory {
  constructor({
    request = notesRequest,
    doc = globalThis.document,
    myNotesURL = MY_NOTES_URL,
    openFolder = openSecondBrainFolder,
    narrow = () => Boolean(globalThis.matchMedia?.(NOTE_INVENTORY_NARROW_QUERY)?.matches)
  } = {}) {
    this.request = request;
    this.doc = doc;
    this.myNotesURL = myNotesURL;
    this.openFolder = openFolder;
    this.narrow = narrow;
    this.root = null;
    this.session = 0;
    this.opened = false;
    this.hqWorkspaceID = '';
    this.state = { phase: 'checking' };
    this.pending = null;
    this.renderQueued = false;
    const changed = () => this.dataChanged();
    const stale = () => this.connectionChanged();
    this.listings = new InventoryListings({ request, onChange: changed, onStale: stale });
    this.preview = new NotePreview({ request, onChange: changed, onStale: stale });
    this.resetView();
  }

  get isOpen() {
    return this.opened;
  }

  // What the person is looking at: which collection, how far down, which
  // folder (in a connected folder), which note and, on a phone, which pane.
  resetView() {
    this.tab = 'all';
    this.shown = {};
    this.path = [];
    this.selected = null;
    this.pane = 'slots';
    this.pendingProject = '';
    this.folderNotice = '';
    this.focusWhenListed = '';
  }

  // Builds the panel once; later opens reuse the same live element.
  ensureRoot() {
    if (this.root) return this.root;
    const doc = this.doc;
    if (!doc || typeof doc.createElement !== 'function') return null;
    const root = el(doc, 'section', 'hq-note-inventory');
    root.setAttribute('data-note-inventory', '');

    const head = el(doc, 'header', 'hq-note-inventory__head');
    const emblem = el(doc, 'span', 'hq-note-inventory__emblem');
    emblem.setAttribute('aria-hidden', 'true');
    emblem.innerHTML = EMBLEM_SVG;
    const ident = el(doc, 'div', 'hq-note-inventory__ident');
    ident.append(
      el(doc, 'p', 'hq-note-inventory__eyebrow', 'Personal HQ / My Notes'),
      el(doc, 'h2', 'hq-note-inventory__title', NOTE_INVENTORY_TITLE)
    );
    const close = el(doc, 'button', 'hq-note-inventory__close');
    close.type = 'button';
    // The stat modal's own close action: Escape and this button take the
    // same path, and focus goes back to the building that opened the panel.
    close.setAttribute('data-cmd-modal-action', 'close');
    close.setAttribute('data-note-inventory-control', 'close');
    close.setAttribute('aria-label', 'Close note inventory');
    close.append(el(doc, 'span', 'hq-note-inventory__close-word', 'Close'));
    const kbd = el(doc, 'kbd', 'hq-note-inventory__close-key', 'Esc');
    kbd.setAttribute('aria-hidden', 'true');
    close.append(kbd);
    head.append(emblem, ident, close);

    const body = el(doc, 'div', 'hq-note-inventory__body');
    body.setAttribute('data-note-inventory-body', '');
    const actions = el(doc, 'footer', 'hq-note-inventory__actions');
    actions.setAttribute('data-note-inventory-actions', '');
    root.append(head, body, actions);

    root.addEventListener('click', event => {
      const control = event.target?.closest?.('[data-note-inventory-action]');
      if (!control || !root.contains(control)) return;
      this.act(
        control.getAttribute('data-note-inventory-action'),
        control.getAttribute('data-note-inventory-value') || ''
      );
    });
    this.root = root;
    this.render();
    return root;
  }

  // Opens a fresh session. `known` is the map's copy of the connection, used
  // when it is a settled answer; otherwise the connection is read now.
  open({ hqWorkspaceID = '', known = null } = {}) {
    this.cancel();
    this.session += 1;
    this.opened = true;
    this.hqWorkspaceID = String(hqWorkspaceID || '');
    this.state = { phase: 'checking' };
    this.connectionView = null;
    this.resetView();
    this.ensureRoot();
    this.render();
    if (known && typeof known.status === 'string' && known.status) {
      this.accept(known);
      return Promise.resolve(this.state);
    }
    return this.loadConnection();
  }

  // Closing drops everything in flight; a late reply finds a newer session.
  close() {
    this.cancel();
    this.session += 1;
    this.opened = false;
  }

  cancel() {
    if (this.pending) {
      try {
        this.pending.abort();
      } catch {
        /* already settled */
      }
    }
    this.pending = null;
    this.listings.clear();
    this.preview.clear();
  }

  accept(view) {
    const before = this.state?.ref || '';
    this.connectionView = view || null;
    const connection = inventoryConnection(view);
    this.state = connection;
    // Another connection is another notebook: nothing chosen in the old one
    // carries over.
    if (connection.ref !== before) {
      this.preview.clear();
      this.resetView();
    }
    if (connection.phase === 'ready' && connection.ref) {
      this.listings.start(view, { owned: connection.owned });
    } else {
      this.listings.clear();
    }
    this.render();
  }

  async loadConnection() {
    const session = this.session;
    if (this.pending) this.pending.abort();
    const controller = typeof AbortController === 'function' ? new AbortController() : null;
    this.pending = controller;
    this.listings.clear();
    this.preview.clear();
    // Reading the connection again (a retry, or the server said it changed)
    // starts a clean view: what was chosen belonged to the earlier answer.
    this.state = { phase: 'checking' };
    this.resetView();
    this.render();
    try {
      const payload = await this.request(
        NOTE_INVENTORY_CONNECTION_PATH,
        controller ? { signal: controller.signal } : {}
      );
      if (session !== this.session) return this.state;
      this.pending = null;
      this.accept(payload?.second_brain || null);
    } catch (error) {
      if (session !== this.session) return this.state;
      this.pending = null;
      if (error?.name === 'AbortError') return this.state;
      this.state = {
        phase: 'error',
        message: String(error?.message || '').trim() || 'Try again, or open My Notes.'
      };
      this.render();
    }
    return this.state;
  }

  // The server said the connection changed while a listing or a note was
  // read: read the connection again rather than show the old notebook.
  connectionChanged() {
    if (!this.opened) return;
    void this.loadConnection();
  }

  // Listings and previews answer later; their repaints are batched so one
  // burst of answers paints once.
  dataChanged() {
    if (this.renderQueued) return;
    this.renderQueued = true;
    queueMicrotask(() => {
      this.renderQueued = false;
      this.settlePendingProject();
      this.render();
      this.settleFolderFocus();
    });
  }

  // A folder opened before its names arrived: once they have, focus moves to
  // its first slot, unless the person has moved on.
  settleFolderFocus() {
    const key = this.focusWhenListed;
    if (!key || this.path.join('/') !== key) return;
    if (this.listings.folder(this.path).status === 'loading') return;
    this.focusWhenListed = '';
    const active = this.doc?.activeElement;
    const resting =
      !active ||
      active === this.doc?.body ||
      active.getAttribute?.('data-note-inventory-control') === 'crumb:0' ||
      !this.root?.contains(active);
    if (resting) this.focusFirstSlot();
  }

  // A project chosen before its folder was looked inside is read once the
  // look says it has a project note.
  settlePendingProject() {
    const id = this.pendingProject;
    if (!id || this.selected?.id !== id) return;
    const item = this.findItem(id);
    if (!item || ['unchecked', 'queued', 'checking'].includes(item.check)) return;
    this.pendingProject = '';
    if (item.check === 'note') void this.preview.select(this.connectionView, item);
  }

  // ---- what is listed ---------------------------------------------------------

  sections() {
    return INVENTORY_SECTIONS.map(section => ({
      ...section,
      data: this.listings.section(section.key)
    }));
  }

  findItem(id) {
    if (!id) return null;
    if (this.state.owned) {
      for (const section of INVENTORY_SECTIONS) {
        const item = this.listings.section(section.key).items.find(entry => entry.id === id);
        if (item) return item;
      }
      return null;
    }
    return this.listings.folder(this.path).items.find(entry => entry.id === id) || null;
  }

  shownFor(key) {
    return this.shown[key] || INVENTORY_PAGE_SIZE;
  }

  tabsModel() {
    const sections = this.sections();
    const all = combinedCount(sections.map(section => section.data));
    return [
      { key: 'all', label: 'All notes', count: all.text, exact: all.exact },
      ...sections.map(section => {
        const count = sectionCount(section.data);
        return { key: section.key, label: section.label, count: count.text, exact: count.exact };
      })
    ];
  }

  // The slots on screen, in order, with the groups they are drawn in.
  slotsModel() {
    if (!this.state.owned) return this.folderModel();
    const sections = this.sections();
    const selectedId = this.selected?.id || '';
    if (this.tab === 'all') {
      const all = combinedCount(sections.map(section => section.data));
      let start = 1;
      return {
        heading: SECTION_HEADING.all,
        countText: all.text,
        scope:
          'Inbox, Projects and Journal (Daily) in ' +
          (this.state.name || 'your notebook') +
          '. Not every file in the notebook.',
        selectedId,
        groups: sections.map(section => {
          const count = sectionCount(section.data);
          const items = section.data.items.slice(0, INVENTORY_SECTION_PREVIEW);
          const first = start;
          start += items.length;
          return {
            key: section.key,
            label: section.label,
            status: section.data.status,
            error: section.data.error,
            countText: count.text,
            start: first,
            items,
            empty: SECTION_EMPTY[section.key],
            seeAll:
              section.data.items.length > items.length
                ? 'See all ' + count.text + ' in ' + section.label
                : ''
          };
        })
      };
    }
    const section = sections.find(entry => entry.key === this.tab) || sections[0];
    const count = sectionCount(section.data);
    const items = section.data.items.slice(0, this.shownFor(section.key));
    const remaining = section.data.items.length - items.length;
    return {
      heading: SECTION_HEADING[section.key],
      countText: count.text,
      selectedId,
      groups: [
        {
          key: section.key,
          label: section.label,
          status: section.data.status,
          error: section.data.error,
          items,
          empty: SECTION_EMPTY[section.key],
          more: remaining > 0 ? 'Show more (' + remaining + ' more listed)' : '',
          partial: section.data.partial ? PARTIAL_LINE : ''
        }
      ]
    };
  }

  // A connected folder Ori did not set up: its own root and the folders a
  // person opened, nothing assumed about its layout.
  folderModel() {
    const folder = this.listings.folder(this.path);
    const key = 'folder:' + this.path.join('/');
    const items = folder.items.slice(0, this.shownFor(key));
    const remaining = folder.items.length - items.length;
    const name = this.state.name || 'Connected folder';
    const count = sectionCount(folder);
    return {
      heading: 'In this folder',
      countText: count.text,
      scope: name + ' is a connected folder: Ori lists its names here and never changes it.',
      crumbs: [{ label: name }, ...this.path.map(part => ({ label: part }))],
      selectedId: this.selected?.id || '',
      groups: [
        {
          key,
          label: this.path.at(-1) || name,
          status: folder.status,
          error: folder.error,
          items,
          empty: 'Nothing here Ori can list: no Markdown notes or folders.',
          more: remaining > 0 ? 'Show more (' + remaining + ' more listed)' : '',
          partial: folder.partial ? PARTIAL_LINE : ''
        }
      ]
    };
  }

  visibleItems(model) {
    return model.groups.flatMap(group => group.items);
  }

  inspectorModel(model) {
    const item = this.selected ? this.findItem(this.selected.id) : null;
    const back = this.pane === 'inspector' ? model.heading : '';
    if (!item) {
      return {
        back: this.selected ? back : '',
        empty: this.state.owned
          ? 'Pick a note to look at it here. Ori reads a note only when you choose it.'
          : 'Pick a note to see where it is. Notes in a connected folder open in your editor.'
      };
    }
    const view = this.connectionView;
    const target = previewTarget(view, item);
    const key = String(view?.browse_ref || '') + '|' + item.id;
    const state = this.preview.state?.key === key ? this.preview.state : null;
    const index = this.visibleItems(model).findIndex(entry => entry.id === item.id) + 1;
    const checking =
      item.kind === 'project' && ['unchecked', 'queued', 'checking'].includes(item.check);
    let message = '';
    let retry = false;
    if (!this.state.owned) {
      message = 'Ori doesn’t show notes from a connected folder here. Open it in your editor.';
    } else if (checking) {
      message = 'Looking for this project’s note…';
    } else if (item.kind === 'project' && item.check !== 'note') {
      message =
        item.check === 'none'
          ? 'This project folder has no project note (README.md) yet.'
          : item.detail || 'Ori could not look inside this folder.';
    } else if (!state || state.status === 'loading') {
      message = 'Opening the note…';
    } else if (state.status === 'missing') {
      message = state.message + ' Ori is listing this folder again.';
    } else if (state.status === 'error') {
      message = state.message;
      retry = true;
    } else if (state.status !== 'ready') {
      message = state.message || 'Open this note in your editor.';
    }
    // The existing doors out, offered where Ori shows no text: Obsidian's
    // vault-relative link for an Obsidian folder, else the open-folder door.
    const showDoor =
      !this.state.owned ||
      (item.kind === 'project' && !checking && item.check !== 'note') ||
      ['yours', 'missing', 'unsupported'].includes(state?.status);
    return {
      item,
      index: index || 0,
      back,
      state,
      checking: checking || state?.status === 'loading',
      message,
      retry,
      paperEyebrow: this.paperEyebrow(item),
      external: showDoor ? target.external : '',
      openFolder: showDoor && !target.external,
      folderNotice: showDoor && !target.external ? this.folderNotice : '',
      place: itemPlace(item, this.state.name)
    };
  }

  paperEyebrow(item) {
    if (item.kind === 'project') return 'Project note · README.md';
    if (item.kind === 'inbox' || item.kind === 'daily') {
      const when = String(item.detail || '')
        .split(' · ')
        .slice(1)
        .join(' · ');
      return kindLabel(item.kind) + (when ? ' · ' + when : '');
    }
    return kindLabel(item.kind);
  }

  // ---- actions ------------------------------------------------------------------

  act(action, value) {
    switch (action) {
      case 'retry':
        void this.loadConnection();
        this.focusControl('retry') || this.focusControl('open-my-notes');
        return;
      case 'tab':
        this.tab = INVENTORY_SECTIONS.some(section => section.key === value) ? value : 'all';
        this.render();
        this.focusControl('tab:' + this.tab);
        return;
      case 'more': {
        const before = this.visibleItems(this.slotsModel()).length;
        this.shown[value] = this.shownFor(value) + INVENTORY_PAGE_SIZE;
        this.render();
        const first = this.visibleItems(this.slotsModel())[before];
        if (!this.focusControl('more:' + value) && first) this.focusControl('item:' + first.id);
        return;
      }
      case 'select':
        this.select(value);
        return;
      case 'back':
        this.pane = 'slots';
        this.render();
        if (!this.selected || !this.focusControl('item:' + this.selected.id)) {
          this.focusControl('tab:' + this.tab);
        }
        return;
      case 'crumb': {
        const depth = Number(value);
        if (!Number.isInteger(depth) || depth < 0 || depth >= this.path.length + 1) return;
        this.path = this.path.slice(0, depth);
        this.selected = null;
        this.pane = 'slots';
        this.preview.clear();
        this.render();
        this.focusFirstSlot();
        return;
      }
      case 'retry-section':
        if (this.state.owned) void this.listings.loadSection(value);
        else void this.listings.loadFolder(this.path);
        return;
      case 'retry-preview': {
        const item = this.selected ? this.findItem(this.selected.id) : null;
        if (item) void this.preview.select(this.connectionView, item);
        return;
      }
      case 'open-folder':
        void this.openNotebookFolder();
        return;
      default:
    }
  }

  // Selecting a slot is choosing it: only now is its note read. A folder in a
  // connected notebook opens instead.
  select(id) {
    const item = this.findItem(id);
    if (!item) return;
    if (item.kind === 'folder') {
      this.path = [...item.parts, item.name];
      this.selected = null;
      this.pane = 'slots';
      this.preview.clear();
      if (this.listings.folder(this.path).status !== 'ready') {
        // Focus waits for the folder's names, then goes to the first of them.
        this.focusWhenListed = this.path.join('/');
        void this.listings.loadFolder(this.path);
      }
      this.render();
      this.focusFirstSlot();
      return;
    }
    this.selected = { ref: this.state.ref, id: item.id };
    this.pane = 'inspector';
    this.folderNotice = '';
    this.pendingProject = '';
    if (item.kind === 'project' && ['unchecked', 'queued', 'checking'].includes(item.check)) {
      this.preview.clear();
      this.pendingProject = item.id;
      this.listings.checkProjects([item]);
    } else if (this.state.owned) {
      void this.preview.select(this.connectionView, item);
    } else {
      this.preview.clear();
    }
    this.render();
    // On a phone the slots give way to the inspector: focus moves with them.
    if (this.narrow()) this.focusControl('inspector-title');
    else this.focusControl('item:' + item.id);
  }

  async openNotebookFolder() {
    const session = this.session;
    this.folderNotice = 'Opening the folder…';
    this.render();
    const result = await this.openFolder();
    if (session !== this.session) return;
    this.folderNotice = result?.ok ? '' : result?.message || 'The folder did not open. Try again.';
    this.render();
  }

  // ---- focus --------------------------------------------------------------------

  controls() {
    return this.root ? Array.from(this.root.querySelectorAll('[data-note-inventory-control]')) : [];
  }

  // Names can hold any character, so controls are found by comparing their
  // name, never by building a selector from it.
  focusControl(name) {
    const target = this.controls().find(
      node => node.getAttribute('data-note-inventory-control') === name
    );
    if (!target || typeof target.focus !== 'function' || target.disabled) return false;
    target.focus({ preventScroll: true });
    return true;
  }

  focusFirstSlot() {
    const slot = this.controls().find(node =>
      String(node.getAttribute('data-note-inventory-control')).startsWith('item:')
    );
    if (slot) slot.focus({ preventScroll: true });
    else this.focusControl('crumb:0') || this.focusControl('open-my-notes');
  }

  // What has focus inside the panel, by name, so a repaint can put it back.
  focusedControl() {
    const active = this.doc?.activeElement;
    if (!active || !this.root || !this.root.contains(active)) return '';
    return active.getAttribute?.('data-note-inventory-control') || '';
  }

  // ---- drawing ------------------------------------------------------------------

  scrollers() {
    return this.root
      ? Array.from(
          this.root.querySelectorAll('.hq-note-inventory__slots, .hq-note-inventory__inspector')
        )
      : [];
  }

  render() {
    const root = this.root;
    if (!root) return;
    const focused = this.focusedControl();
    const scrolled = this.scrollers().map(node => [node.className.split(' ')[0], node.scrollTop]);
    const body = root.querySelector('[data-note-inventory-body]');
    const actions = root.querySelector('[data-note-inventory-actions]');
    const { nodes, visible } = this.bodyNodes();
    if (body) body.replaceChildren(...nodes);
    if (actions) actions.replaceChildren(...this.actionNodes());
    root.setAttribute('data-note-inventory-phase', this.state.phase);
    root.setAttribute('data-note-inventory-pane', this.pane);
    root.setAttribute('data-note-inventory-mode', this.state.owned ? 'owned' : 'folder');
    for (const node of this.scrollers()) {
      const saved = scrolled.find(([name]) => name === node.className.split(' ')[0]);
      if (saved) node.scrollTop = saved[1];
    }
    if (focused && focused !== 'close') this.focusControl(focused);
    // Only the projects on screen are looked inside.
    if (visible.length) this.listings.checkProjects(visible);
  }

  bodyNodes() {
    const doc = this.doc;
    const state = this.state;
    const copy = connectionCopy(state);
    if (copy) {
      const card = el(doc, 'div', 'hq-note-inventory__state is-' + copy.tone);
      card.setAttribute('role', state.phase === 'checking' ? 'status' : 'group');
      card.setAttribute('data-note-inventory-state', state.phase);
      card.append(el(doc, 'p', 'hq-note-inventory__state-title', copy.title));
      if (copy.body) card.append(el(doc, 'p', 'hq-note-inventory__state-body', copy.body));
      const row = el(doc, 'div', 'hq-note-inventory__state-actions');
      if (copy.retry) {
        const retry = el(doc, 'button', 'hq-note-inventory__button', 'Try again');
        retry.type = 'button';
        retry.setAttribute('data-note-inventory-action', 'retry');
        retry.setAttribute('data-note-inventory-control', 'retry');
        row.append(retry);
      }
      if (copy.action) {
        const link = el(doc, 'a', 'hq-note-inventory__button', copy.action);
        link.href = this.myNotesURL;
        link.setAttribute('data-note-inventory-control', 'repair');
        row.append(link);
      }
      if (row.childNodes.length) card.append(row);
      return { nodes: [card], visible: [] };
    }
    const slots = this.slotsModel();
    const nodes = [];
    if (state.owned) nodes.push(tabsNode(doc, { tabs: this.tabsModel(), active: this.tab }));
    const layout = el(doc, 'div', 'hq-note-inventory__layout');
    layout.append(slotsNode(doc, slots), inspectorNode(doc, this.inspectorModel(slots)));
    nodes.push(layout);
    return { nodes, visible: this.visibleItems(slots) };
  }

  actionNodes() {
    const doc = this.doc;
    const open = el(doc, 'a', 'hq-note-inventory__open');
    open.href = this.myNotesURL;
    open.setAttribute('data-note-inventory-control', 'open-my-notes');
    open.append(el(doc, 'span', '', 'Open My Notes'));
    const arrow = el(doc, 'span', 'hq-note-inventory__arrow', '→');
    arrow.setAttribute('aria-hidden', 'true');
    open.append(arrow);
    return [open];
  }
}
