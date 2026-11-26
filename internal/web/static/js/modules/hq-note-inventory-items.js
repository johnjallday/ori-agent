/**
 * hq-note-inventory-items.js — what the note inventory lists and previews,
 * without the panel.
 *
 * Everything here reads through the notebook's existing, bounded endpoints:
 * `/browse` for names (never bodies) and `/preview` for one note a person
 * chose. The budget is fixed and does not grow with the notebook:
 *
 *   - opening an Ori notebook lists three folders (Inbox, Projects, Daily),
 *     each a single `/browse` the server caps at 200 names;
 *   - a project is a folder until its `README.md` is seen: only the projects
 *     on screen are checked (one `/browse` each, at most
 *     PROJECT_CHECK_CONCURRENCY at a time, never all of them);
 *   - a connected folder Ori did not set up lists its real root, then only the
 *     folders a person opens;
 *   - a preview is read only when a person selects a note, one at a time.
 *
 * Every answer is tied to the connection (`browse_ref`) and a generation that
 * a new connection, a close or a newer request makes obsolete, so nothing
 * from an earlier selection or connection is ever shown.
 */

import { notesRequest } from './notes-api.js';
import {
  directoryProblem,
  notebookNoteActions,
  notesDirectoryQuery,
  noteProblem
} from './my-notes-browser.js';

export const INVENTORY_PAGE_SIZE = 8;
export const INVENTORY_SECTION_PREVIEW = 4;
export const PROJECT_CHECK_CONCURRENCY = 2;

// The sections of a notebook Ori set up. Journal is the Daily folder.
export const INVENTORY_SECTIONS = Object.freeze([
  Object.freeze({ key: 'inbox', label: 'Inbox', folder: 'Inbox', kind: 'inbox' }),
  Object.freeze({ key: 'projects', label: 'Projects', folder: 'Projects', kind: 'project' }),
  Object.freeze({ key: 'journal', label: 'Journal', folder: 'Daily', kind: 'daily' })
]);

const KIND_LABEL = Object.freeze({
  inbox: 'Inbox note',
  project: 'Project',
  daily: 'Journal',
  folder: 'Folder',
  file: 'Note'
});

export function kindLabel(kind) {
  return KIND_LABEL[kind] || 'Note';
}

const MARKDOWN = /\.(?:md|markdown)$/i;
const README = /^readme\.md$/i;
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

function stem(name) {
  return String(name || '').replace(MARKDOWN, '');
}

// "20261126-1012-ask-about-the-roof-warranty.md" → when, and the words after.
function capturedParts(name) {
  const match = stem(name).match(/^(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})-(.*)$/);
  if (!match) return null;
  const [, year, month, day, hour, minute, rest] = match;
  const m = Number(month);
  if (m < 1 || m > 12) return null;
  return { year, month: m, day: Number(day), time: hour + ':' + minute, rest };
}

function words(text) {
  const spaced = String(text || '')
    .replace(/[-_]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
  return spaced ? spaced.charAt(0).toUpperCase() + spaced.slice(1) : '';
}

function journalDate(name) {
  const match = stem(name).match(/^(\d{4})-(\d{2})-(\d{2})$/);
  if (!match) return null;
  const [, year, month, day] = match.map(Number);
  const date = new Date(Date.UTC(year, month - 1, day));
  if (date.getUTCMonth() !== month - 1 || date.getUTCDate() !== day) return null;
  return { label: WEEKDAYS[date.getUTCDay()] + ' ' + day + ' ' + MONTHS[month - 1], year };
}

// itemFromEntry turns one listed name into a slot. The title comes from the
// real name (a project's folder, a note's filename); nothing is inferred from
// a note's text, which has not been read.
export function inventoryItem(kind, name, parts) {
  const base = { kind, name, parts: [...parts], id: kind + ':' + name };
  if (kind === 'project') {
    return {
      ...base,
      title: name,
      detail: 'Project folder',
      file: 'README.md',
      check: 'unchecked'
    };
  }
  if (kind === 'inbox') {
    const captured = capturedParts(name);
    return {
      ...base,
      title: words(captured ? captured.rest : stem(name)) || stem(name),
      detail: captured
        ? 'Inbox · ' + captured.day + ' ' + MONTHS[captured.month - 1] + ' · ' + captured.time
        : 'Inbox',
      file: name
    };
  }
  if (kind === 'daily') {
    const date = journalDate(name);
    return {
      ...base,
      title: date ? date.label : stem(name),
      detail: date ? 'Journal · ' + stem(name) : 'Journal',
      file: name
    };
  }
  if (kind === 'folder') {
    return { ...base, title: name, detail: 'Folder', folder: true };
  }
  return { ...base, title: stem(name) || name, detail: 'Note', file: name };
}

// sectionItems reads one Ori section's listing. Inbox and Journal hold notes
// (their layout README is not one); Projects holds project folders.
export function sectionItems(section, entries) {
  const list = Array.isArray(entries) ? entries : [];
  if (section.kind === 'project') {
    return list
      .filter(entry => entry.folder)
      .map(entry => inventoryItem('project', entry.name, [section.folder]));
  }
  const notes = list
    .filter(entry => !entry.folder && MARKDOWN.test(entry.name) && !README.test(entry.name))
    .map(entry => inventoryItem(section.kind, entry.name, [section.folder]));
  // Dated names sort as time: newest first.
  return notes.sort((a, b) => (a.name < b.name ? 1 : a.name > b.name ? -1 : 0));
}

// folderItems reads a connected folder's listing exactly as it is: its own
// names, folders first, nothing assumed about its layout.
export function folderItems(parts, entries) {
  return (Array.isArray(entries) ? entries : []).map(entry =>
    inventoryItem(entry.folder ? 'folder' : 'file', entry.name, parts)
  );
}

// A badge says only what is known: an exact count, a lower bound when the
// server stopped listing, or nothing yet.
export function sectionCount(section) {
  if (!section || section.status !== 'ready') return { text: '', exact: false, known: false };
  const n = section.items.length;
  return section.partial
    ? { text: n + '+', exact: false, known: true, n }
    : { text: String(n), exact: true, known: true, n };
}

export function combinedCount(sections) {
  const list = Array.isArray(sections) ? sections : [];
  const ready = list.filter(section => section?.status === 'ready');
  if (!ready.length) return { text: '', exact: false, known: false };
  const n = ready.reduce((sum, section) => sum + section.items.length, 0);
  const exact = ready.length === list.length && ready.every(section => !section.partial);
  return { text: exact ? String(n) : n + '+', exact, known: true, n };
}

// The slot's picture is chosen from what is known: its kind, and for a
// project a stable pick from its name. Never from a note's contents.
const PROJECT_ART = ['map', 'blueprint', 'clipboard', 'frame'];
export function itemArt(item) {
  if (!item) return 'page';
  if (item.kind === 'inbox') return 'letter';
  if (item.kind === 'daily') return 'calendar';
  if (item.kind === 'folder') return 'folder';
  if (item.kind === 'project') {
    let hash = 0;
    for (const char of String(item.name)) hash = (hash * 31 + char.codePointAt(0)) >>> 0;
    return PROJECT_ART[hash % PROJECT_ART.length];
  }
  return 'page';
}

// The note's place inside the notebook, for people: never a machine path.
export function itemPlace(item, notebookName = 'My Notes') {
  if (!item) return [];
  const place = [notebookName || 'My Notes', ...item.parts];
  if (item.kind === 'project') return [...place, item.name];
  return [...place, item.name];
}

function settle(controller) {
  return controller?.signal?.aborted === true;
}

// InventoryListings holds the listings for one open of the inventory.
export class InventoryListings {
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
    this.ref = '';
    this.owned = false;
    this.sections = new Map();
    this.folders = new Map();
    this.checkQueue = [];
    this.checking = 0;
    this.staleReported = false;
  }

  // clear makes everything in flight obsolete; nothing it returns is applied.
  clear() {
    this.generation += 1;
    for (const controller of this.controllers) controller.abort();
    this.controllers.clear();
    this.reset();
  }

  start(view, { owned }) {
    this.clear();
    if (!view?.browse_ref) return;
    this.view = view;
    this.ref = String(view.browse_ref);
    this.owned = Boolean(owned);
    if (this.owned) {
      for (const section of INVENTORY_SECTIONS) void this.loadSection(section.key);
    } else {
      void this.loadFolder([]);
    }
  }

  section(key) {
    return this.sections.get(key) || { status: 'idle', items: [], partial: false, code: 0 };
  }

  folder(parts) {
    return (
      this.folders.get(parts.join('/')) || { status: 'idle', items: [], partial: false, code: 0 }
    );
  }

  async list(parts) {
    const controller = typeof AbortController === 'function' ? new AbortController() : null;
    const generation = this.generation;
    if (controller) this.controllers.add(controller);
    try {
      const payload = await this.request(
        notesDirectoryQuery(this.ref, parts),
        controller ? { signal: controller.signal } : {}
      );
      const directory = payload?.directory;
      if (!Array.isArray(directory?.entries)) throw new Error('No listing was returned.');
      return {
        current: !settle(controller) && generation === this.generation,
        ok: true,
        entries: directory.entries
          .filter(entry => typeof entry?.name === 'string' && entry.name)
          .map(entry => ({ name: entry.name, folder: entry.folder === true })),
        partial: directory.partial === true
      };
    } catch (error) {
      return {
        current: !settle(controller) && generation === this.generation,
        ok: false,
        code: error?.status || 0,
        error: directoryProblem(error?.status, parts.length === 0)
      };
    } finally {
      if (controller) this.controllers.delete(controller);
    }
  }

  // A listing or check the server refused because the connection changed
  // means everything here is about an earlier notebook.
  stale(code) {
    if (code !== 409 || this.staleReported) return;
    this.staleReported = true;
    this.onStale();
  }

  async loadSection(key) {
    const section = INVENTORY_SECTIONS.find(entry => entry.key === key);
    if (!section || !this.view) return;
    const before = this.sections.get(key);
    // A section already listed keeps its slots while it is listed again.
    const keep = before?.status === 'ready' ? before : null;
    this.sections.set(key, {
      status: keep ? 'ready' : 'loading',
      items: keep ? keep.items : [],
      partial: keep ? keep.partial : false,
      code: 0,
      refreshing: Boolean(keep)
    });
    this.onChange();
    const result = await this.list([section.folder]);
    if (!result.current) return;
    if (!result.ok) {
      this.sections.set(key, {
        status: 'error',
        items: [],
        partial: false,
        code: result.code,
        error:
          result.code === 404
            ? 'There is no ' + section.folder + ' folder in this notebook.'
            : result.error
      });
      this.stale(result.code);
      this.onChange();
      return;
    }
    const items = sectionItems(section, result.entries);
    // A project already checked keeps what was seen.
    if (keep && section.kind === 'project') {
      const seen = new Map(keep.items.map(item => [item.id, item]));
      items.forEach((item, index) => {
        const old = seen.get(item.id);
        if (old && old.check !== 'checking') items[index] = { ...item, ...pickCheck(old) };
      });
    }
    this.sections.set(key, { status: 'ready', items, partial: result.partial, code: 0 });
    this.onChange();
  }

  async loadFolder(parts) {
    if (!this.view) return;
    const key = parts.join('/');
    this.folders.set(key, { status: 'loading', items: [], partial: false, code: 0 });
    this.onChange();
    const result = await this.list(parts);
    if (!result.current) return;
    this.folders.set(
      key,
      result.ok
        ? { status: 'ready', items: folderItems(parts, result.entries), partial: result.partial }
        : { status: 'error', items: [], partial: false, code: result.code, error: result.error }
    );
    if (!result.ok) this.stale(result.code);
    this.onChange();
  }

  // checkProjects looks inside the given (visible) project folders for their
  // README.md, a few at a time. A folder already checked, or being checked,
  // is not asked again.
  // It reports a change only when it queued something, so a repaint that asks
  // for the projects it shows settles instead of asking forever.
  checkProjects(items) {
    if (!this.owned) return;
    let queued = 0;
    for (const given of items) {
      if (given?.kind !== 'project') continue;
      // The caller's slot may be an older copy; what counts is the current one.
      const item = this.section('projects').items.find(entry => entry.id === given.id);
      if (!item || item.check !== 'unchecked') continue;
      this.checkQueue.push(item.id);
      this.updateProject(item.id, { check: 'queued' });
      queued += 1;
    }
    if (!queued) return;
    this.pumpChecks();
    this.onChange();
  }

  updateProject(id, patch) {
    const section = this.sections.get('projects');
    if (!section || section.status !== 'ready') return;
    const items = section.items.map(item => (item.id === id ? { ...item, ...patch } : item));
    this.sections.set('projects', { ...section, items });
  }

  pumpChecks() {
    while (this.checking < PROJECT_CHECK_CONCURRENCY && this.checkQueue.length) {
      const id = this.checkQueue.shift();
      const item = this.section('projects').items.find(entry => entry.id === id);
      if (!item) continue;
      this.checking += 1;
      void this.checkProject(item);
    }
  }

  async checkProject(item) {
    const generation = this.generation;
    this.updateProject(item.id, { check: 'checking' });
    const result = await this.list([...item.parts, item.name]);
    if (generation !== this.generation) return;
    this.checking = Math.max(0, this.checking - 1);
    if (result.current) {
      if (!result.ok) {
        this.updateProject(item.id, {
          check: 'error',
          detail: result.code === 404 ? 'Folder not found' : 'Could not look inside'
        });
        this.stale(result.code);
      } else {
        const found = result.entries.some(entry => !entry.folder && entry.name === 'README.md');
        this.updateProject(
          item.id,
          found
            ? { check: 'note', detail: 'Project note' }
            : result.partial
              ? { check: 'unknown', detail: 'Too many files to check here' }
              : { check: 'none', detail: 'No project note' }
        );
      }
      this.onChange();
    }
    this.pumpChecks();
  }
}

function pickCheck(item) {
  return { check: item.check, detail: item.detail };
}

// previewTarget says whether a slot can be read here, and how: the same rule
// My Notes uses (Ori's own notes in Inbox, Daily and a project's README).
export function previewTarget(view, item) {
  if (!item || item.folder) return { preview: '', external: '' };
  const parts = item.kind === 'project' ? [...item.parts, item.name] : item.parts;
  const file = item.kind === 'project' ? 'README.md' : item.name;
  if (item.kind === 'project' && item.check !== 'note') {
    // No README was seen: there is no note to read, and none is invented.
    return { preview: '', external: notebookNoteActions(view, parts, file).external };
  }
  return notebookNoteActions(view, parts, file);
}

// NotePreview reads the one note a person selected. Selecting another, or
// closing, makes the earlier answer obsolete.
export class NotePreview {
  constructor({ request = notesRequest, onChange = () => {}, onStale = () => {} } = {}) {
    this.request = request;
    this.onChange = onChange;
    this.onStale = onStale;
    this.generation = 0;
    this.controller = null;
    this.state = null;
  }

  clear() {
    this.generation += 1;
    this.controller?.abort();
    this.controller = null;
    this.state = null;
  }

  async select(view, item) {
    this.clear();
    if (!view || !item) return;
    const target = previewTarget(view, item);
    const key = String(view.browse_ref || '') + '|' + item.id;
    if (!target.preview) {
      this.state = { key, status: 'unsupported', item, target };
      this.onChange();
      return;
    }
    const generation = this.generation;
    const controller = typeof AbortController === 'function' ? new AbortController() : null;
    this.controller = controller;
    this.state = { key, status: 'loading', item, target };
    this.onChange();
    let result;
    try {
      const payload = await this.request(
        target.preview,
        controller ? { signal: controller.signal } : {}
      );
      if (!payload?.preview) throw new Error('No preview was returned.');
      result = { status: 'ready', preview: payload.preview };
    } catch (error) {
      result = noteProblem(error);
    }
    if (settle(controller) || generation !== this.generation) return;
    this.controller = null;
    this.state = { key, item, target, ...result };
    if (result.stale) this.onStale();
    this.onChange();
  }
}
