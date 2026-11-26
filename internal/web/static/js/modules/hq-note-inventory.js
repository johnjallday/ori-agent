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
 * (the map's own copy when it has one, else `?view=connection`). It never
 * sets anything up, starts a look, captures or previews by itself, and it
 * drops every reply that lands after it was closed or reopened.
 */

import { notesRequest } from './notes-api.js';
import { MY_NOTES_URL } from './hq-quick-capture.js';

export const NOTE_INVENTORY_TITLE = 'Note inventory';
export const NOTE_INVENTORY_CONNECTION_PATH = '?view=connection';

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
    default:
      return null;
  }
}

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
    myNotesURL = MY_NOTES_URL
  } = {}) {
    this.request = request;
    this.doc = doc;
    this.myNotesURL = myNotesURL;
    this.root = null;
    this.session = 0;
    this.opened = false;
    this.hqWorkspaceID = '';
    this.state = { phase: 'checking' };
    this.pending = null;
  }

  get isOpen() {
    return this.opened;
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
      this.act(control.getAttribute('data-note-inventory-action'), control);
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
  }

  accept(view) {
    this.connectionView = view || null;
    const connection = inventoryConnection(view);
    this.state = connection;
    this.render();
  }

  async loadConnection() {
    const session = this.session;
    this.cancel();
    const controller = typeof AbortController === 'function' ? new AbortController() : null;
    this.pending = controller;
    this.state = { phase: 'checking' };
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

  act(action) {
    if (action === 'retry') {
      void this.loadConnection();
      if (this.root) this.focusControl('retry') || this.focusControl('open-my-notes');
    }
  }

  focusControl(name) {
    const target = this.root?.querySelector?.('[data-note-inventory-control="' + name + '"]');
    if (!target || typeof target.focus !== 'function') return false;
    target.focus({ preventScroll: true });
    return true;
  }

  // What has focus inside the panel, by name, so a repaint can put it back.
  focusedControl() {
    const active = this.doc?.activeElement;
    if (!active || !this.root || !this.root.contains(active)) return '';
    return active.getAttribute?.('data-note-inventory-control') || '';
  }

  render() {
    const root = this.root;
    if (!root) return;
    const focused = this.focusedControl();
    const body = root.querySelector('[data-note-inventory-body]');
    const actions = root.querySelector('[data-note-inventory-actions]');
    if (body) body.replaceChildren(...this.bodyNodes());
    if (actions) actions.replaceChildren(...this.actionNodes());
    root.setAttribute('data-note-inventory-phase', this.state.phase);
    if (focused && focused !== 'close') this.focusControl(focused);
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
      return [card];
    }
    return this.readyNodes();
  }

  // A connected notebook, before any listing: its name, who keeps it and what
  // is known about the Inbox.
  readyNodes() {
    const doc = this.doc;
    const state = this.state;
    const card = el(doc, 'div', 'hq-note-inventory__state is-ready');
    card.setAttribute('data-note-inventory-state', 'ready');
    card.append(el(doc, 'p', 'hq-note-inventory__state-title', state.name || 'Your notebook'));
    card.append(
      el(
        doc,
        'p',
        'hq-note-inventory__state-body',
        state.owned ? 'Set up by Ori · ' + inboxLine(state) : 'Connected folder · read-only for Ori'
      )
    );
    return [card];
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
