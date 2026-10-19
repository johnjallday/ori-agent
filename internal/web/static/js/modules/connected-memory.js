// Connected memory on the Profile page: one card per external memory source
// (what Codex, Claude Code, Gemini CLI, and the Claude account remember about
// the user), mirrored read-only by the server. Only the personal assistant
// reads the mirror; this section is where the user switches sources on and
// off, leaves folders out, and syncs.
//
// Pure helpers are exported for node:test; mountConnectedMemory() wires the
// DOM inside #connectedMemory.

import { formatRelativeTime } from './relative-time.js';
import { createTagInput } from './tag-input.js';

const API = '/api/external-memory';
const POLL_INTERVAL_MS = 1000;
const POLL_LIMIT = 30;
const MAX_EXCLUSIONS = 100;
const MAX_EXCLUSION_LENGTH = 1024;

function plural(n, one, many = `${one}s`) {
  return `${n} ${n === 1 ? one : many}`;
}

function relativeTime(iso, now) {
  return formatRelativeTime(iso, { now: new Date(now) });
}

// entriesLine is the card's "N entries across M folders" line.
export function entriesLine(source) {
  const entries = Number(source?.entry_count) || 0;
  const folders = Number(source?.folder_count) || 0;
  if (entries === 0) return 'No entries yet';
  if (folders === 0) return plural(entries, 'entry', 'entries');
  return `${plural(entries, 'entry', 'entries')} across ${plural(folders, 'folder')}`;
}

// changesPhrase summarizes what the last sync changed.
export function changesPhrase(changes) {
  const parts = [];
  if (changes?.added) parts.push(`${changes.added} added`);
  if (changes?.updated) parts.push(`${changes.updated} updated`);
  if (changes?.removed) parts.push(`${changes.removed} removed`);
  return parts.length ? parts.join(', ') : 'no changes';
}

// sourceCardState picks the one state a card shows.
export function sourceCardState(source, sync) {
  if (!source) return 'unavailable';
  if (!source.available) return 'unavailable';
  if (!source.enabled) return 'off';
  if (sync?.running) return 'syncing';
  if (source.last_result === 'error') return 'error';
  if (!source.last_sync_finished) return 'pending';
  return 'synced';
}

// sourceCardModel is everything a card renders, derived from one source of
// GET /api/external-memory/sources.
export function sourceCardModel(source, { sync, now = Date.now() } = {}) {
  const state = sourceCardState(source, sync);
  const location = source?.location_display || source?.location || '';
  let status;
  switch (state) {
    case 'unavailable':
      status = source?.enabled
        ? `Not found. Ori looked in ${location}; it will sync when that folder appears.`
        : `Not found on this computer. Ori looked in ${location}.`;
      break;
    case 'off':
      status = 'Available and off. Nothing is read until you switch it on.';
      break;
    case 'syncing':
      status = 'Syncing…';
      break;
    case 'pending':
      status = 'Waiting for the first sync…';
      break;
    case 'error':
      status = `The last sync failed${source.last_error ? `: ${source.last_error}` : ''}. The mirror was left as it was.`;
      break;
    default: {
      const notes = [];
      if (source.skipped_files)
        notes.push(`${plural(source.skipped_files, 'file')} skipped, see the server log.`);
      if (source.store_full) notes.push('The mirror is full (200 MB); new entries were not added.');
      status = notes.length
        ? notes.join(' ')
        : 'On. Your personal assistant can use these entries, labeled as external.';
    }
  }

  let lastSync = 'Never';
  if (source?.last_sync_finished) {
    const when = relativeTime(source.last_sync_finished, now) || source.last_sync_finished;
    const outcome = source.last_result === 'error' ? 'failed' : changesPhrase(source.last_changes);
    lastSync = `${when} (${outcome})`;
  }
  const nextSync = source?.next_due ? relativeTime(source.next_due, now) || 'soon' : '';

  return {
    id: source?.id || '',
    label: source?.label || source?.id || 'Source',
    state,
    dimmed: state === 'unavailable',
    enabled: Boolean(source?.enabled),
    location,
    status,
    entries: entriesLine(source),
    lastSync,
    nextSync,
    folders: Array.isArray(source?.folders) ? source.folders : [],
    generalCount: generalEntryCount(source),
    exclusions: Array.isArray(source?.exclusions) ? source.exclusions : [],
    isFiles: source?.kind !== 'paste',
    canSync: Boolean(source?.enabled) && state !== 'syncing'
  };
}

// needsPoll reports whether the cards are about to change on their own: a sync
// is running, or an enabled source has not finished its first sync.
export function needsPoll(payload) {
  if (payload?.sync?.running) return true;
  return (payload?.sources || []).some(
    source => source.enabled && source.available && !source.last_sync_finished
  );
}

// generalEntryCount is how many entries apply to no folder: what the agent
// knows about the user in general.
export function generalEntryCount(source) {
  const total = Number(source?.entry_count) || 0;
  const inFolders = (Array.isArray(source?.folders) ? source.folders : []).reduce(
    (sum, folder) => sum + (Number(folder?.count) || 0),
    0
  );
  return Math.max(0, total - inFolders);
}

// exclusionSync decides whether the exclusion editor takes the server's list.
// It does only when that list changed since it was last applied (or after a
// refused save), so a refresh that still carries the old list while the
// user's change is being saved never wipes the chip they just added.
export function exclusionSync({ lastServer, server, shown, force = false }) {
  const key = (Array.isArray(server) ? server : []).join('\n');
  const changed = force || key !== lastServer;
  const differs = (Array.isArray(shown) ? shown : []).join('\n') !== key;
  return { apply: changed && differs, lastServer: key };
}

// folderSuggestions turns a card's folder counts into the exclusion editor's
// suggestion pool.
export function folderSuggestions(folders) {
  return (Array.isArray(folders) ? folders : [])
    .filter(folder => folder && folder.display)
    .map(folder => ({ name: folder.display, total: Number(folder.count) || 0 }));
}

export function trimValue(value) {
  return String(value ?? '').trim();
}

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

async function request(url, options = {}) {
  const response = await fetch(url, {
    headers: { 'Content-Type': 'application/json' },
    ...options
  });
  let payload = {};
  try {
    payload = await response.json();
  } catch {
    payload = {};
  }
  if (!response.ok) {
    throw new Error(payload.message || payload.error || `Request failed (${response.status})`);
  }
  return { status: response.status, payload };
}

// createSourceCard builds one card once and updates it in place, so a refresh
// never steals focus from the exclusion editor.
function createSourceCard(initial, actions) {
  let current = initial;
  const id = initial.id;
  const root = element('article', 'modern-card connected-memory-card');
  root.dataset.source = id;

  const header = element('header', 'connected-memory-card-header');
  const heading = element('div', 'connected-memory-card-heading');
  const title = element('h3', '', initial.label || id);
  title.id = `connectedMemoryTitle-${id}`;
  const location = element('p', 'connected-memory-location');
  heading.append(title, location);

  const toggle = element('div', 'form-check form-switch connected-memory-switch');
  const switchInput = element('input', 'form-check-input');
  switchInput.type = 'checkbox';
  switchInput.id = `connectedMemorySwitch-${id}`;
  switchInput.setAttribute('role', 'switch');
  const switchLabel = element('label', 'form-check-label');
  switchLabel.htmlFor = switchInput.id;
  toggle.append(switchInput, switchLabel);
  header.append(heading, toggle);
  root.setAttribute('aria-labelledby', title.id);

  const status = element('p', 'connected-memory-state');

  const facts = element('dl', 'connected-memory-facts');
  const factValue = label => {
    facts.append(element('dt', '', label));
    const value = element('dd');
    facts.append(value);
    return value;
  };
  const entriesValue = factValue('Entries');
  const lastSyncValue = factValue('Last synced');
  const nextSyncValue = factValue('Next sync');

  const folders = element('details', 'connected-memory-folders');
  const foldersSummary = element('summary', '', 'Entries by folder');
  const foldersList = element('ul');
  folders.append(foldersSummary, foldersList);

  const exclusions = element('div', 'connected-memory-exclusions');
  const exclusionsLabel = element('span', 'connected-memory-exclusions-label', 'Leave out folders');
  const exclusionsHost = element('div');
  const exclusionsHint = element(
    'small',
    '',
    'Entries from these folders (and everything under them) are not mirrored. Use a full path or ~/…'
  );
  exclusions.append(exclusionsLabel, exclusionsHost, exclusionsHint);
  const tagInput = createTagInput({
    container: exclusionsHost,
    initialTags: initial.exclusions || [],
    maxTags: MAX_EXCLUSIONS,
    maxTagLength: MAX_EXCLUSION_LENGTH,
    normalize: trimValue,
    placeholder: 'Add a folder, e.g. ~/Projects/private',
    ariaLabel: `Leave out a folder from ${initial.label || id}`,
    suggestedLimit: 6,
    fetchPool: async () => folderSuggestions(current.folders),
    onChange: tags => actions.setExclusions(id, tags)
  });

  const actionsRow = element('div', 'connected-memory-card-actions');
  const syncButton = element('button', 'modern-btn modern-btn-secondary', 'Sync now');
  syncButton.type = 'button';
  actionsRow.append(syncButton);

  root.append(header, status, facts, folders, exclusions, actionsRow);

  switchInput.addEventListener('change', () => actions.setEnabled(id, switchInput.checked));
  syncButton.addEventListener('click', () => actions.sync());

  let lastServerExclusions = null;

  function update(source, context) {
    current = source;
    const model = sourceCardModel(source, context);
    root.className = `modern-card connected-memory-card is-${model.state}`;
    root.classList.toggle('is-dimmed', model.dimmed);
    title.textContent = model.label;
    location.textContent = model.location ? `Looks in ${model.location}` : '';
    switchInput.checked = model.enabled;
    switchInput.disabled = Boolean(context?.busy);
    switchLabel.textContent = model.enabled ? 'On' : 'Off';
    status.textContent = model.status;
    entriesValue.textContent = model.entries;
    lastSyncValue.textContent = model.lastSync;
    nextSyncValue.textContent = model.nextSync || '—';

    const folderRows = model.folders.map(folder => {
      const item = element('li');
      const path = element('span', 'connected-memory-folder-path', folder.display || folder.folder);
      path.title = folder.folder || '';
      item.append(path, element('span', 'connected-memory-folder-count', String(folder.count)));
      return item;
    });
    if (model.generalCount > 0) {
      const item = element('li', 'connected-memory-folder-general');
      item.append(
        element('span', '', 'About you in general (no folder)'),
        element('span', 'connected-memory-folder-count', String(model.generalCount))
      );
      folderRows.push(item);
    }
    foldersList.replaceChildren(...folderRows);
    folders.hidden = folderRows.length === 0;
    exclusions.hidden = !model.isFiles;
    const sync = exclusionSync({
      lastServer: lastServerExclusions,
      server: model.exclusions,
      shown: tagInput.getTags(),
      force: Boolean(context?.resyncExclusions)
    });
    lastServerExclusions = sync.lastServer;
    if (sync.apply) tagInput.setTags(model.exclusions);
    syncButton.hidden = !model.isFiles;
    syncButton.disabled = !model.canSync || Boolean(context?.busy);
  }

  update(initial, {});
  return { root, update };
}

export function mountConnectedMemory(doc = document) {
  const section = doc.getElementById('connectedMemory');
  if (!section) return null;
  const statusLine = doc.getElementById('connectedMemoryStatus');
  const list = doc.getElementById('connectedMemorySources');
  const cards = new Map();
  let payload = null;
  let busy = false;
  let pollTimer = null;
  let polls = 0;

  function setStatus(message, isError = false) {
    if (!statusLine) return;
    statusLine.textContent = message || '';
    statusLine.classList.toggle('is-error', Boolean(isError));
  }

  function render(extra = {}) {
    if (!payload) return;
    const context = { sync: payload.sync, busy, ...extra };
    const seen = new Set();
    for (const source of payload.sources || []) {
      seen.add(source.id);
      let card = cards.get(source.id);
      if (!card) {
        card = createSourceCard(source, actions);
        cards.set(source.id, card);
        list.append(card.root);
      }
      card.update(source, context);
    }
    for (const [id, card] of cards) {
      if (!seen.has(id)) {
        card.root.remove();
        cards.delete(id);
      }
    }
    section.setAttribute('aria-busy', String(Boolean(payload.sync?.running)));
  }

  function schedulePoll() {
    clearTimeout(pollTimer);
    if (!needsPoll(payload) || polls >= POLL_LIMIT) return;
    polls += 1;
    pollTimer = setTimeout(() => void load(), POLL_INTERVAL_MS);
  }

  async function load() {
    try {
      const { payload: next } = await request(`${API}/sources`);
      payload = next;
      setStatus('');
      render();
      schedulePoll();
    } catch (error) {
      setStatus(error.message || 'Connected memory could not be loaded.', true);
    }
  }

  async function mutate(run, failure) {
    busy = true;
    render();
    let failed = false;
    try {
      const { payload: next } = await run();
      payload = next;
      setStatus('');
      polls = 0;
    } catch (error) {
      failed = true;
      setStatus(error.message || failure, true);
    } finally {
      busy = false;
      // A refused change puts the saved folder list back in the editor.
      render({ resyncExclusions: failed });
      schedulePoll();
    }
  }

  const actions = {
    setEnabled: (id, enabled) =>
      mutate(
        () =>
          request(`${API}/sources/${encodeURIComponent(id)}`, {
            method: 'PATCH',
            body: JSON.stringify({ enabled })
          }),
        'The switch could not be saved.'
      ),
    setExclusions: (id, exclusions) =>
      mutate(
        () =>
          request(`${API}/sources/${encodeURIComponent(id)}`, {
            method: 'PATCH',
            body: JSON.stringify({ exclusions })
          }),
        'The folder list could not be saved.'
      ),
    sync: () => mutate(() => request(`${API}/sync`, { method: 'POST' }), 'The sync could not run.')
  };

  void load();
  return { reload: load };
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => mountConnectedMemory(), { once: true });
  } else {
    mountConnectedMemory();
  }
}
