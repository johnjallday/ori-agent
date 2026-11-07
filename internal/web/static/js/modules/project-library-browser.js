// Navigation intent and read coordination only. This module has no mutation,
// setup, grant, model, OS-open or workspace-activation capability.
export const LIBRARY_TABS = ['overview', 'files', 'sessions'];
export const LIBRARY_FILTERS = {
  view: 'projectLibraryView',
  collectionID: 'projectLibraryCollection',
  text: 'projectLibrarySearch',
  stage: 'projectLibraryStage',
  status: 'projectLibraryStatusFilter',
  format: 'projectLibraryFormat',
  rootID: 'projectLibraryRoot',
  connection: 'projectLibraryConnection',
  availability: 'projectLibraryAvailability',
  priority: 'projectLibraryPriority',
  sort: 'projectLibrarySort',
  direction: 'projectLibraryDirection'
};
const id = value => typeof value === 'string' && /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,159}$/.test(value);
export const LIBRARY_VIEWS = {
  all: 'All',
  recently_saved: 'Recently saved',
  needs_attention: 'Needs attention',
  archived: 'Archived'
};
const choices = {
  view: ['', ...Object.keys(LIBRARY_VIEWS)],
  stage: ['', 'unknown', 'idea', 'writing', 'recording', 'editing', 'mixing', 'mastering'],
  status: ['', 'unknown', 'planning', 'active', 'on_hold', 'complete', 'archived'],
  connection: ['', 'connected', 'catalog_only', 'needs_review'],
  availability: ['', 'available', 'ambiguous', 'unavailable', 'revoked_source', 'not_scanned'],
  priority: ['', '0', '1', '2', '3', '4', '5'],
  sort: [
    'name',
    'last_saved',
    'status',
    'priority',
    'next_action',
    'sourced_activity',
    'scanned_at'
  ],
  direction: ['asc', 'desc']
};

export function navigationIntent(value = {}) {
  const query = {};
  for (const field of Object.keys(LIBRARY_FILTERS)) {
    const entry = value.query?.[field];
    if (field === 'text') {
      // Search is intent, not a place to retain source paths. Bound UTF-8, not
      // JavaScript character count; malformed storage never supplies authority.
      if (
        typeof entry === 'string' &&
        new TextEncoder().encode(entry).length <= 120 &&
        !/[\\/]/.test(entry) &&
        ![...entry].some(char => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127)
      )
        query[field] = entry;
    } else if (choices[field]?.includes(entry) || (!choices[field] && id(entry))) {
      query[field] = entry;
    }
  }
  query.sort ||= 'name';
  query.direction ||= 'asc';
  return {
    version: 1,
    query,
    pages: Math.max(1, Math.min(20, Number.isInteger(value.pages) ? value.pages : 1)),
    selected: id(value.selected) ? value.selected : '',
    tab: LIBRARY_TABS.includes(value.tab) ? value.tab : 'overview',
    scroll: Math.max(0, Math.min(100000, Number.isFinite(value.scroll) ? value.scroll : 0))
  };
}

export function navigationKey(user, home) {
  return id(user) && id(home) ? `ori:library-browser:v1:${user}:${home}` : '';
}

export function readNavigation(storage, user, home) {
  try {
    const key = navigationKey(user, home);
    const raw = key && storage?.getItem(key);
    if (!raw || raw.length > 4096) return null;
    const value = JSON.parse(raw);
    return value?.version === 1 ? navigationIntent(value) : null;
  } catch (_) {
    return null;
  }
}

export function saveNavigation(storage, user, home, value) {
  try {
    const key = navigationKey(user, home);
    if (key) storage?.setItem(key, JSON.stringify(navigationIntent(value)));
  } catch (_) {
    // Storage is optional. It never caches metadata or permission.
  }
}

export class ReadEpoch {
  generation = 0;
  controller = null;
  begin() {
    this.cancel();
    const generation = this.generation;
    this.controller = new AbortController();
    const signal = this.controller.signal;
    return { signal, current: () => this.generation === generation && !signal.aborted };
  }
  cancel() {
    this.generation++;
    this.controller?.abort();
    this.controller = null;
  }
}

export class LibraryBrowserState {
  constructor({ home, request, changed = () => {} }) {
    this.home = home;
    this.request = request;
    this.changed = changed;
    this.detailRead = new ReadEpoch();
    this.tabRead = new ReadEpoch();
    this.selected = '';
    this.tab = 'overview';
    this.detail = null;
    this.status = 'idle';
    this.error = '';
    this.destroyed = false;
  }
  async select(entry, tab = this.tab) {
    if (this.destroyed || !id(entry)) return;
    const turn = this.detailRead.begin();
    this.tabRead.cancel();
    this.selected = entry;
    this.tab = LIBRARY_TABS.includes(tab) ? tab : 'overview';
    this.detail = null;
    this.status = 'loading';
    this.error = '';
    this.changed(this);
    try {
      const detail = await this.request(`/projects/${encodeURIComponent(entry)}`, {
        signal: turn.signal
      });
      if (!turn.current()) return;
      if (detail?.row?.id !== entry)
        throw new Error('This project changed. Return to projects and refresh.');
      this.detail = detail;
      this.status = 'ready';
    } catch (error) {
      if (!turn.current()) return;
      this.status = 'error';
      this.error = error.message || 'This project is unavailable. Return to projects and refresh.';
    }
    if (turn.current()) this.changed(this);
  }
  changeTab(tab) {
    if (this.destroyed || !LIBRARY_TABS.includes(tab)) return;
    this.tabRead.cancel();
    this.tab = tab;
    this.changed(this);
  }
  clear() {
    this.detailRead.cancel();
    this.tabRead.cancel();
    this.selected = '';
    this.detail = null;
    this.status = 'idle';
    this.error = '';
    if (!this.destroyed) this.changed(this);
  }
  setHome(home) {
    if (home === this.home) return;
    this.home = home;
    this.tab = 'overview';
    this.clear();
  }
  destroy() {
    this.destroyed = true;
    this.clear();
  }
}

export function savedMetadataTime(value) {
  if (!value || String(value).startsWith('0001-')) return '';
  const time = new Date(value);
  return Number.isFinite(time.getTime()) ? time.toLocaleString() : '';
}

export function rowException(row) {
  if (row.connection === 'needs_review') return 'Project link needs review';
  return (
    {
      unavailable: 'Unavailable at last scan',
      ambiguous: 'Project file choice needed',
      revoked_source: 'Discovery consent ended'
    }[row.last_observed_availability] || ''
  );
}
