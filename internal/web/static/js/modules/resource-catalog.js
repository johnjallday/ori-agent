// A resource workspace's catalog page. The provider's
// declaration names the categories, kinds and facts; this module words the
// states only. Opening the page reads Ori's own files; nothing is read from a
// source until the owner reviews and approves it.

import { workspaceRootURL } from './workspace-routes.js';

const CATEGORY_STATES = {
  not_inspected: 'Not inspected',
  partial: 'Partial',
  refresh_failed: 'Refresh failed',
  unavailable: 'Unavailable',
  access_withdrawn: 'Access withdrawn',
  history: 'Saved elsewhere; not read on this computer'
};

const ENTRY_STATES = {
  no_longer_found: 'No longer found',
  access_withdrawn: 'Access withdrawn',
  history: 'From another computer or setup'
};

const PROVIDER_REASONS = {
  provider_missing: 'The plugin that provides this catalog is not installed.',
  provider_disabled: 'The plugin that provides this catalog is switched off.',
  catalog_missing: 'The installed plugin no longer offers this catalog. Update it.',
  catalog_incompatible: "The installed plugin's catalog is newer than this version of Ori.",
  service_unavailable: "The plugin's service is unavailable on this computer.",
  detached: 'This workspace is no longer attached to its Home.'
};

const SOURCE_STATES = {
  ready: 'Ready',
  absent: 'Not found on this computer',
  denied: 'Ori is not allowed to read it',
  unsupported: 'Not supported here',
  app_closed: 'Needs the application open',
  reader_missing: 'Needs a reader the plugin sets up separately',
  reader_outdated: 'Needs a newer reader',
  web_remote_off: "Needs the application's remote access on"
};

export function formatDate(value, locale = undefined, timeZone = undefined) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleString(locale, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    ...(timeZone ? { timeZone } : {})
  });
}

// categoryStateLine says what the category's saved entries mean now.
export function categoryStateLine(category, options = {}) {
  const at = formatDate(category?.inspected_at, options.locale, options.timeZone);
  switch (category?.state) {
    case 'inspected':
      return at ? `Inspected ${at}` : 'Inspected';
    case 'partial':
      return at ? `Partial · last complete read ${at}` : CATEGORY_STATES.partial;
    case 'refresh_failed':
      return at ? `Refresh failed · showing what was read ${at}` : CATEGORY_STATES.refresh_failed;
    default:
      return CATEGORY_STATES[category?.state] || CATEGORY_STATES.not_inspected;
  }
}

// emptyCategoryLine explains an empty list without implying anything must be
// installed. Given the category's sources, an unread one says why it was not
// read (for example that the application was closed).
export function emptyCategoryLine(category, filtered, sources = []) {
  if (filtered) return 'Nothing matches this search.';
  switch (category?.state) {
    case 'inspected':
      return 'Nothing was found in the sources that were read.';
    case 'access_withdrawn':
      return 'Access to its sources was withdrawn.';
    case 'unavailable':
    case 'refresh_failed': {
      const reasons = sources
        .filter(source => source.category === category.id && source.approved && !source.withdrawn)
        .map(source => `${source.label}: ${attemptLine(source)}.`);
      return reasons.length
        ? `Nothing could be read. ${reasons.join(' ')} Refresh once that changes.`
        : 'Its sources could not be read. Review them, then try again.';
    }
    default:
      return 'Not inspected yet. Nothing is read until you review its sources.';
  }
}

export function providerLine(provider) {
  if (provider?.available) return '';
  return (
    PROVIDER_REASONS[provider?.reason] || 'The plugin that provides this catalog is unavailable.'
  );
}

export function sourceStateWords(state) {
  return SOURCE_STATES[state] || '';
}

// Each reason reads after any count: "1 symlinked", "3 symlinked".
const SKIP_REASONS = {
  symlink: 'symlinked',
  outside_source: 'outside the folder',
  unreadable: 'unreadable',
  unsupported_name: 'with an unusable name',
  too_large: 'too large',
  hidden: 'hidden',
  external_registration: 'registered from outside the folder',
  depth_limit: 'nested too deeply'
};

// skippedLine is what a read passed over, by reason: "1 skipped: 1 symlinked".
export function skippedLine(skipped) {
  const parts = (skipped || [])
    .filter(skip => Number(skip.count) > 0)
    .map(skip => `${Number(skip.count)} ${SKIP_REASONS[skip.reason] || 'other'}`);
  if (!parts.length) return '';
  const total = (skipped || []).reduce((sum, skip) => sum + (Number(skip.count) || 0), 0);
  return `${total} skipped: ${parts.join(', ')}`;
}

// attemptLine is one source's last read on this computer.
export function attemptLine(source, options = {}) {
  if (source?.withdrawn) return 'Access withdrawn';
  const attempt = source?.last_attempt;
  if (!attempt) return 'Approved; not read yet';
  const at = formatDate(attempt.finished_at, options.locale, options.timeZone);
  const read = Number(attempt.entries_read) || 0;
  const skipped = skippedLine(attempt.skipped);
  switch (attempt.outcome) {
    case 'complete':
      return [`Read ${at}`, `${read} found`, skipped].filter(Boolean).join(' · ');
    case 'partial':
      return attempt.reason === 'deadline'
        ? `Partial: time ran out after ${read} · Continue reading for the rest`
        : `Partial: Ori read the first ${read} · Continue reading for the rest`;
    case 'cancelled':
      return 'Cancelled · earlier results kept';
    case 'absent':
      return `Not found on this computer ${at}`;
    case 'denied':
      return 'Ori was not allowed to read it';
    case 'unsupported':
      return 'Not supported here';
    case 'unavailable':
      return sourceStateWords(attempt.reason) || 'Unavailable';
    case 'withdrawn':
      return 'Access withdrawn';
    default:
      return 'Refresh failed · earlier results kept';
  }
}

// sourceLine is one source's row: its words and how its last read went. A
// folder the owner added waits for review before anything is read in it.
export function sourceLine(source, options = {}) {
  const status =
    source?.folder && !source.approved && !source.withdrawn && !source.last_attempt
      ? 'Added; read once you approve it in Review sources'
      : attemptLine(source, options);
  return [source?.label, source?.location, status].filter(Boolean).join(' · ');
}

export function entryStateLabel(state) {
  return ENTRY_STATES[state] || '';
}

// splitTags turns the comma-separated field into the list the server checks.
export function splitTags(value) {
  return String(value || '')
    .split(',')
    .map(tag => tag.trim())
    .filter(Boolean);
}

// rowContext keeps same-named entries apart: kind, source and known facts,
// and the plugin's own identity when another entry of that source and kind
// has the same name.
export function rowContext(row) {
  // A bare "Yes" or "No" says nothing in a row: a yes shows what is true by
  // its label, and a no is left to the detail.
  const facts = (row?.facts || [])
    .filter(fact => fact.known && fact.value !== 'No')
    .map(fact => (fact.value === 'Yes' ? fact.label : fact.value));
  return [row?.kind_label, row?.source_label, ...facts, row?.identity_hint]
    .filter(Boolean)
    .join(' · ');
}

// catalogActions are the page's primary buttons for the catalog's state.
export function catalogActions(view) {
  if (!view?.can_inspect) return [];
  const running = view.operation?.state === 'running';
  if (running) return [{ id: 'cancel', label: 'Cancel', primary: false }];
  const actions = [];
  if (!view.approved || !(view.sources || []).some(source => source.approved)) {
    actions.push({ id: 'review', label: 'Inspect resources', primary: true });
    return actions;
  }
  actions.push({ id: 'refresh', label: 'Refresh', primary: true });
  if ((view.sources || []).some(source => source.can_continue)) {
    actions.push({ id: 'continue', label: 'Continue reading', primary: false });
  }
  actions.push({ id: 'review', label: 'Review sources', primary: false });
  return actions;
}

// operationLine summarizes an inspection in flight or just finished.
export function operationLine(operation) {
  if (!operation) return '';
  const sources = operation.sources || [];
  const done = sources.filter(source => !['pending', 'reading'].includes(source.state)).length;
  const found = sources.reduce((sum, source) => sum + (Number(source.entries) || 0), 0);
  switch (operation.state) {
    case 'running':
      return `Inspecting… ${done} of ${sources.length} sources finished · ${found} found so far`;
    case 'cancelled':
      return `Cancelled · ${done} of ${sources.length} sources finished; their results were kept`;
    case 'interrupted':
      return 'The last inspection was interrupted; nothing from it was saved.';
    case 'superseded':
      return 'The last inspection was replaced by a newer one; nothing from it was saved.';
    case 'failed':
      return 'The last inspection could not be saved.';
    default:
      return '';
  }
}

export class ResourceCatalogPage {
  constructor({
    workspaceId,
    workspaceSlug,
    fetchImpl = globalThis.fetch?.bind(globalThis),
    documentImpl = globalThis.document,
    locationImpl = globalThis.location,
    pollMs = 1000
  } = {}) {
    this.workspaceId = String(workspaceId || '');
    this.workspaceSlug = String(workspaceSlug || '');
    this.fetchImpl = fetchImpl;
    this.document = documentImpl;
    this.location = locationImpl;
    this.pollMs = pollMs;
    this.view = null;
    this.category = '';
    this.rows = [];
    this.cursor = '';
    this.review = null;
    this.withdraw = null;
    this.removing = null;
    this.pollTimer = null;
  }

  el(id) {
    return this.document?.getElementById(id) || null;
  }

  api(suffix = '') {
    return `/api/workspaces/${encodeURIComponent(this.workspaceId)}/resource-catalog${suffix}`;
  }

  async request(url, options = {}) {
    try {
      const response = await this.fetchImpl(url, {
        headers: {
          Accept: 'application/json',
          ...(options.body ? { 'Content-Type': 'application/json' } : {})
        },
        ...options
      });
      const body = await response.json().catch(() => ({}));
      return { ok: response.ok, status: response.status, body };
    } catch (_) {
      return { ok: false, status: 0, body: {} };
    }
  }

  async init() {
    const back = this.el('resourceCatalogWorkspaceLink');
    if (back && this.workspaceSlug) back.href = workspaceRootURL(this.workspaceSlug);
    this.bind();
    await this.loadOverview();
    const entry = new URLSearchParams(this.location?.search || '').get('entry');
    if (entry) await this.showDetail(entry);
  }

  bind() {
    this.el('resourceCatalogFilters')?.addEventListener?.('submit', event => {
      event.preventDefault();
      void this.loadEntries();
    });
    this.el('resourceCatalogSearch')?.addEventListener?.('input', () => this.scheduleSearch());
    this.el('resourceCatalogSource')?.addEventListener?.('change', () => void this.loadEntries());
    this.el('resourceCatalogFavorites')?.addEventListener?.(
      'change',
      () => void this.loadEntries()
    );
    this.el('resourceCatalogMore')?.addEventListener?.(
      'click',
      () => void this.loadEntries({ append: true })
    );
    this.el('resourceCatalogReviewCommit')?.addEventListener?.(
      'click',
      () => void this.commitReview()
    );
    this.el('resourceCatalogWithdrawCommit')?.addEventListener?.(
      'click',
      () => void this.commitWithdraw()
    );
    this.el('resourceCatalogAddFolder')?.addEventListener?.('click', () => this.openFolder());
    this.el('resourceCatalogFolderChoose')?.addEventListener?.(
      'click',
      () => void this.chooseFolder()
    );
    this.el('resourceCatalogFolderCommit')?.addEventListener?.(
      'click',
      () => void this.commitFolder()
    );
    // Enter in the path adds the folder rather than submitting Cancel.
    this.el('resourceCatalogFolderPath')?.addEventListener?.('keydown', event => {
      if (event.key !== 'Enter') return;
      event.preventDefault?.();
      void this.commitFolder();
    });
    this.el('resourceCatalogRemoveFolderCommit')?.addEventListener?.(
      'click',
      () => void this.commitRemoveFolder()
    );
  }

  scheduleSearch() {
    clearTimeout(this.searchTimer);
    this.searchTimer = setTimeout(() => void this.loadEntries(), 250);
  }

  async loadOverview() {
    const status = this.el('resourceCatalogStatus');
    const result = await this.request(this.api());
    if (!result.ok) {
      if (status) status.textContent = 'This workspace has no catalog, or it is unavailable.';
      this.el('resourceCatalogPage')?.setAttribute?.('aria-busy', 'false');
      return;
    }
    this.view = result.body;
    if (status) status.hidden = true;
    const content = this.el('resourceCatalogContent');
    if (content) content.hidden = false;
    this.el('resourceCatalogPage')?.setAttribute?.('aria-busy', 'false');
    if (!this.category || !this.view.categories.some(category => category.id === this.category)) {
      this.category = this.view.categories[0]?.id || '';
    }
    this.render();
    await this.loadEntries();
    this.watchOperation();
  }

  render() {
    const view = this.view;
    if (!view) return;
    const title = this.el('resourceCatalogTitle');
    if (title) title.textContent = view.name || 'Catalog';
    const provider = this.el('resourceCatalogProvider');
    if (provider) {
      provider.textContent = providerLine(view.attached ? view.provider : { reason: 'detached' });
      provider.hidden = provider.textContent === '';
    }
    this.renderActions();
    this.renderOperation();
    this.renderTabs();
    this.renderSources();
  }

  renderActions() {
    const root = this.el('resourceCatalogActions');
    if (!root) return;
    root.replaceChildren();
    for (const action of catalogActions(this.view)) {
      const button = this.document.createElement('button');
      button.type = 'button';
      button.className = `modern-btn ${action.primary ? 'modern-btn-primary' : 'modern-btn-secondary'}`;
      button.textContent = action.label;
      button.dataset.action = action.id;
      button.addEventListener('click', () => void this.act(action.id));
      root.append(button);
    }
  }

  async act(id) {
    if (id === 'review') return this.openReview();
    if (id === 'cancel') return this.cancelOperation();
    return this.startInspection(id);
  }

  renderOperation() {
    const root = this.el('resourceCatalogOperation');
    if (!root) return;
    const line = operationLine(this.view?.operation);
    root.textContent = line;
    root.hidden = line === '';
  }

  renderTabs() {
    const root = this.el('resourceCatalogTabs');
    if (!root) return;
    root.replaceChildren();
    for (const category of this.view.categories) {
      const tab = this.document.createElement('button');
      tab.type = 'button';
      tab.id = `resourceCatalogTab-${category.id}`;
      tab.className = 'resource-catalog-tab';
      tab.setAttribute('role', 'tab');
      tab.setAttribute('aria-selected', String(category.id === this.category));
      tab.tabIndex = category.id === this.category ? 0 : -1;
      const label = this.document.createElement('span');
      label.className = 'resource-catalog-tab-label';
      label.textContent = category.label;
      const count = this.document.createElement('span');
      count.className = 'resource-catalog-tab-count';
      count.textContent = String(category.counts?.present ?? 0);
      tab.append(label, count);
      tab.addEventListener('click', () => this.selectCategory(category.id));
      tab.addEventListener('keydown', event => this.tabKey(event, category.id));
      root.append(tab);
    }
    this.renderCategory();
  }

  tabKey(event, id) {
    const ids = this.view.categories.map(category => category.id);
    const index = ids.indexOf(id);
    let next = -1;
    if (event.key === 'ArrowRight') next = (index + 1) % ids.length;
    if (event.key === 'ArrowLeft') next = (index - 1 + ids.length) % ids.length;
    if (event.key === 'Home') next = 0;
    if (event.key === 'End') next = ids.length - 1;
    if (next < 0) return;
    event.preventDefault();
    this.selectCategory(ids[next]);
    this.el(`resourceCatalogTab-${ids[next]}`)?.focus?.();
  }

  selectCategory(id) {
    this.category = id;
    this.renderTabs();
    void this.loadEntries();
  }

  currentCategory() {
    return this.view?.categories.find(category => category.id === this.category) || null;
  }

  renderCategory() {
    const category = this.currentCategory();
    const panel = this.el('resourceCatalogCategory');
    if (panel && category)
      panel.setAttribute('aria-labelledby', `resourceCatalogTab-${category.id}`);
    const coverage = this.el('resourceCatalogCoverage');
    if (coverage) {
      const parts = [category?.description, category ? categoryStateLine(category) : ''];
      const counts = category?.counts || {};
      if (counts.no_longer_found) parts.push(`${counts.no_longer_found} no longer found`);
      if (counts.withdrawn) parts.push(`${counts.withdrawn} with access withdrawn`);
      if (counts.history) parts.push(`${counts.history} from another computer or setup`);
      coverage.textContent = parts.filter(Boolean).join(' · ');
    }
    const select = this.el('resourceCatalogSource');
    if (select) {
      const chosen = select.value;
      select.replaceChildren();
      const all = this.document.createElement('option');
      all.value = '';
      all.textContent = 'All sources';
      select.append(all);
      for (const source of (this.view?.sources || []).filter(
        source => source.category === this.category
      )) {
        const option = this.document.createElement('option');
        option.value = source.source_id;
        option.textContent = source.label;
        select.append(option);
      }
      select.value = chosen;
    }
  }

  query(append) {
    const params = new URLSearchParams();
    if (this.category) params.set('category', this.category);
    const q = String(this.el('resourceCatalogSearch')?.value || '').trim();
    if (q) params.set('q', q);
    const source = String(this.el('resourceCatalogSource')?.value || '');
    if (source) params.set('source', source);
    if (this.el('resourceCatalogFavorites')?.checked) params.set('favorites', '1');
    if (append && this.cursor) params.set('cursor', this.cursor);
    return params;
  }

  async loadEntries({ append = false } = {}) {
    if (!this.view) return;
    const params = this.query(append);
    const result = await this.request(`${this.api('/entries')}?${params.toString()}`);
    if (!result.ok) {
      if (append && result.body?.code === 'page_stale') return this.loadEntries();
      this.rows = append ? this.rows : [];
      this.cursor = '';
    } else {
      this.rows = append ? [...this.rows, ...result.body.rows] : result.body.rows;
      this.cursor = result.body.next_cursor || '';
    }
    this.renderList(params.has('q') || params.has('source') || params.has('favorites'));
  }

  renderList(filtered) {
    const list = this.el('resourceCatalogList');
    if (!list) return;
    list.replaceChildren();
    for (const row of this.rows) {
      const item = this.document.createElement('li');
      const button = this.document.createElement('button');
      button.type = 'button';
      button.className = 'resource-catalog-row';
      button.dataset.entryId = row.entry_id;
      if (row.entry_id === this.detail?.entry_id) button.setAttribute('aria-current', 'true');
      const name = this.document.createElement('span');
      name.className = 'resource-catalog-row-name';
      name.textContent = row.favorite ? `★ ${row.name}` : row.name;
      const context = this.document.createElement('span');
      context.className = 'resource-catalog-row-context';
      context.textContent = rowContext(row);
      button.append(name, context);
      const state = entryStateLabel(row.state);
      if (state) {
        const badge = this.document.createElement('span');
        badge.className = 'resource-catalog-row-state';
        badge.textContent = state;
        button.append(badge);
      }
      button.setAttribute(
        'aria-label',
        [row.name, rowContext(row), state].filter(Boolean).join(', ')
      );
      button.addEventListener('click', () => void this.showDetail(row.entry_id, { focus: true }));
      item.append(button);
      list.append(item);
    }
    const empty = this.el('resourceCatalogEmpty');
    if (empty) {
      empty.textContent = emptyCategoryLine(
        this.currentCategory(),
        filtered,
        this.view?.sources || []
      );
      empty.hidden = this.rows.length > 0;
    }
    const more = this.el('resourceCatalogMore');
    if (more) more.hidden = !this.cursor;
  }

  // showDetail opens one entry. Chosen from the list, it takes the focus, so
  // a keyboard user lands on the entry rather than behind every other row.
  async showDetail(entryId, { focus = false } = {}) {
    const panel = this.el('resourceCatalogDetail');
    if (!panel) return;
    const result = await this.request(this.api(`/entries/${encodeURIComponent(entryId)}`));
    panel.replaceChildren();
    panel.hidden = false;
    if (!result.ok) {
      panel.textContent = 'This entry is unavailable.';
      return;
    }
    this.detail = result.body;
    for (const button of this.el('resourceCatalogList')?.querySelectorAll?.(
      '.resource-catalog-row'
    ) || []) {
      if (button.dataset.entryId === entryId) button.setAttribute('aria-current', 'true');
      else button.removeAttribute('aria-current');
    }
    this.renderDetail(panel, result.body);
    if (focus) panel.querySelector?.('h2')?.focus?.();
  }

  renderDetail(panel, detail) {
    const doc = this.document;
    const title = doc.createElement('h2');
    title.textContent = detail.name;
    title.tabIndex = -1;
    panel.append(title);
    const line = (label, value) => {
      if (!value) return;
      const row = doc.createElement('p');
      const term = doc.createElement('strong');
      term.textContent = `${label}: `;
      row.append(term, doc.createTextNode(value));
      panel.append(row);
    };
    line('Kind', detail.kind_label);
    line('Source', detail.source_label);
    line('Where', detail.source_location);
    line('Identity', detail.identity);
    for (const fact of detail.facts || []) line(fact.label, fact.value);
    line('State', entryStateLabel(detail.state) || 'Found in the last read');
    line('Seen', formatDate(detail.observed_at));
    if (detail.description?.text) {
      line(`Description (from ${detail.description.source})`, detail.description.text);
    }
    const note = doc.createElement('p');
    note.className = 'resource-catalog-detail-note';
    note.textContent =
      'Listed means seen during inspection. It does not mean licensed, loaded, working or tested.';
    panel.append(note);
    this.renderAnnotationForm(panel, detail);
  }

  // The owner's notes sit apart from what was observed: a refresh never
  // touches them, and a write against older notes is refused, not merged.
  renderAnnotationForm(panel, detail) {
    const doc = this.document;
    const form = doc.createElement('form');
    form.className = 'resource-catalog-annotation';
    const heading = doc.createElement('h3');
    heading.textContent = 'Your notes';
    form.append(heading);
    const favoriteLabel = doc.createElement('label');
    favoriteLabel.className = 'resource-catalog-check';
    const favorite = doc.createElement('input');
    favorite.type = 'checkbox';
    favorite.checked = Boolean(detail.favorite);
    const favoriteText = doc.createElement('span');
    favoriteText.textContent = 'Favorite';
    favoriteLabel.append(favorite, favoriteText);
    const tagsLabel = doc.createElement('label');
    tagsLabel.className = 'resource-catalog-field';
    const tagsText = doc.createElement('span');
    tagsText.textContent = 'Tags, separated by commas';
    const tags = doc.createElement('input');
    tags.type = 'text';
    tags.maxLength = 700;
    tags.value = (detail.tags || []).join(', ');
    tagsLabel.append(tagsText, tags);
    const noteLabel = doc.createElement('label');
    noteLabel.className = 'resource-catalog-field';
    const noteText = doc.createElement('span');
    noteText.textContent = 'Note';
    const noteInput = doc.createElement('textarea');
    noteInput.rows = 4;
    noteInput.maxLength = 4000;
    noteInput.value = detail.note || '';
    noteLabel.append(noteText, noteInput);
    const status = doc.createElement('p');
    status.className = 'resource-catalog-annotation-status';
    status.setAttribute('role', 'status');
    const save = doc.createElement('button');
    save.type = 'submit';
    save.className = 'modern-btn modern-btn-primary';
    save.textContent = 'Save notes';
    form.append(favoriteLabel, tagsLabel, noteLabel, save, status);
    let baseRevision = Number(detail.annotation_revision) || 0;
    form.addEventListener('submit', async event => {
      event.preventDefault?.();
      const result = await this.request(
        this.api(`/entries/${encodeURIComponent(detail.entry_id)}/annotation`),
        {
          method: 'PUT',
          body: JSON.stringify({
            favorite: favorite.checked,
            tags: splitTags(tags.value),
            note: noteInput.value,
            base_revision: baseRevision
          })
        }
      );
      if (result.ok) {
        baseRevision = Number(result.body?.annotation?.revision) || 0;
        status.textContent = 'Saved.';
        void this.loadEntries();
        return;
      }
      if (result.body?.code === 'annotation_conflict') {
        // Keep the owner's text; show what is saved now. Saving again
        // replaces it knowingly.
        baseRevision = Number(result.body.current?.revision) || 0;
        status.textContent = `These notes changed somewhere else. Saved now: “${result.body.current?.note || ''}”. Save again to replace it with yours.`;
        return;
      }
      status.textContent =
        result.body?.message || result.body?.error || 'The notes could not be saved.';
    });
    panel.append(form);
    const replacement = detail.suggested_replacement;
    if (replacement && detail.annotation_revision) {
      const offer = doc.createElement('p');
      offer.className = 'resource-catalog-replacement';
      offer.textContent = `This looks like ${replacement.name} in ${replacement.source_label || 'another source'}. `;
      const move = doc.createElement('button');
      move.type = 'button';
      move.className = 'modern-btn modern-btn-secondary';
      move.textContent = 'Move my notes there';
      move.addEventListener('click', async () => {
        const result = await this.request(
          this.api(`/entries/${encodeURIComponent(detail.entry_id)}/annotation/move`),
          {
            method: 'POST',
            body: JSON.stringify({ to_entry_id: replacement.entry_id, base_revision: baseRevision })
          }
        );
        if (result.ok) {
          await this.loadEntries();
          await this.showDetail(replacement.entry_id);
          return;
        }
        status.textContent =
          result.body?.message || result.body?.error || 'The notes could not be moved.';
      });
      offer.append(move);
      panel.append(offer);
    }
  }

  async openReview() {
    const dialog = this.el('resourceCatalogReview');
    const error = this.el('resourceCatalogReviewError');
    if (error) error.textContent = '';
    const result = await this.request(this.api('/sources/review'), { method: 'POST', body: '{}' });
    if (!result.ok) {
      this.showStatus(result.body?.message || 'The sources could not be reviewed.');
      return;
    }
    this.review = result.body;
    const intro = this.el('resourceCatalogReviewIntro');
    if (intro) {
      intro.textContent = result.body.replaces_installation
        ? `${result.body.installation.label}: a different setup than the one approved before. What was read from that one stays as history.`
        : result.body.installation.label;
    }
    const root = this.el('resourceCatalogReviewSources');
    root?.replaceChildren();
    for (const source of result.body.sources) {
      const label = this.document.createElement('label');
      label.className = 'resource-catalog-review-source';
      const box = this.document.createElement('input');
      box.type = 'checkbox';
      box.value = source.source_id;
      box.checked = source.approved || !source.withdrawn;
      const text = this.document.createElement('span');
      const state = sourceStateWords(source.state);
      text.textContent = [
        `${source.category_label}: ${source.label}`,
        source.location,
        state,
        source.detail
      ]
        .filter(Boolean)
        .join(' · ');
      label.append(box, text);
      root?.append(label);
    }
    dialog?.showModal?.();
  }

  async commitReview() {
    const ids = [
      ...(this.el('resourceCatalogReviewSources')?.querySelectorAll?.('input:checked') || [])
    ].map(box => box.value);
    const error = this.el('resourceCatalogReviewError');
    if (!ids.length) {
      if (error) error.textContent = 'Choose at least one source.';
      return;
    }
    const result = await this.request(this.api('/sources/commit'), {
      method: 'POST',
      body: JSON.stringify({
        review_token: this.review?.review_token,
        idempotency_key: crypto.randomUUID(),
        source_ids: ids
      })
    });
    if (!result.ok) {
      if (error) error.textContent = result.body?.message || 'The sources could not be approved.';
      return;
    }
    this.el('resourceCatalogReview')?.close?.();
    await this.loadOverview();
  }

  async startInspection(mode) {
    const result = await this.request(this.api('/inspections'), {
      method: 'POST',
      body: JSON.stringify({ idempotency_key: crypto.randomUUID(), mode })
    });
    if (!result.ok && result.body?.code === 'scope_review_required') return this.openReview();
    if (!result.ok && result.body?.code !== 'inspection_active') {
      this.showStatus(result.body?.message || 'The inspection could not start.');
    }
    await this.loadOverview();
  }

  async cancelOperation() {
    const id = this.view?.operation?.id;
    if (!id) return;
    await this.request(this.api(`/inspections/${encodeURIComponent(id)}/cancel`), {
      method: 'POST',
      body: '{}'
    });
    await this.loadOverview();
  }

  watchOperation() {
    clearTimeout(this.pollTimer);
    if (this.view?.operation?.state !== 'running') return;
    this.pollTimer = setTimeout(() => void this.loadOverview(), this.pollMs);
  }

  renderSources() {
    const add = this.el('resourceCatalogAddFolder');
    if (add) add.hidden = !this.view?.can_add_folder;
    const root = this.el('resourceCatalogSources');
    if (!root) return;
    root.replaceChildren();
    const sources = this.view?.sources || [];
    if (!sources.length) {
      const item = this.document.createElement('li');
      item.textContent = 'No source is approved on this computer.';
      root.append(item);
      return;
    }
    for (const source of sources) {
      const item = this.document.createElement('li');
      item.className = 'resource-catalog-source';
      const text = this.document.createElement('span');
      text.textContent = sourceLine(source);
      item.append(text);
      // A folder the owner added is removed; any other source is withdrawn.
      const action = source.folder
        ? { label: 'Remove folder', run: () => this.openRemoveFolder(source) }
        : source.approved
          ? { label: 'Withdraw access', run: () => void this.openWithdraw(source.source_id) }
          : null;
      if (action) {
        const button = this.document.createElement('button');
        button.type = 'button';
        button.className = 'modern-btn modern-btn-secondary';
        button.textContent = action.label;
        button.addEventListener('click', action.run);
        item.append(button);
      }
      root.append(item);
    }
  }

  openFolder() {
    const root = this.el('resourceCatalogFolderCategories');
    root?.replaceChildren();
    const categories = this.view?.folder_categories || [];
    const preferred = categories.some(category => category.id === this.category)
      ? this.category
      : categories[0]?.id;
    for (const category of categories) {
      const label = this.document.createElement('label');
      label.className = 'resource-catalog-check';
      const radio = this.document.createElement('input');
      radio.type = 'radio';
      radio.name = 'resourceCatalogFolderCategory';
      radio.value = category.id;
      radio.checked = category.id === preferred;
      const text = this.document.createElement('span');
      text.textContent = category.label;
      label.append(radio, text);
      root?.append(label);
    }
    for (const id of ['resourceCatalogFolderNote', 'resourceCatalogFolderError']) {
      const line = this.el(id);
      if (line) line.textContent = '';
    }
    const path = this.el('resourceCatalogFolderPath');
    if (path) path.value = '';
    this.el('resourceCatalogFolder')?.showModal?.();
    path?.focus?.();
  }

  // chooseFolder opens the system's folder dialog; where it is unavailable
  // the owner types the path instead.
  async chooseFolder() {
    const note = this.el('resourceCatalogFolderNote');
    const result = await this.request('/api/folder-picker/select-path', {
      method: 'POST',
      body: JSON.stringify({ title: 'Choose a folder for this catalog' })
    });
    if (result.ok && result.body?.selected && result.body?.path) {
      const path = this.el('resourceCatalogFolderPath');
      if (path) path.value = result.body.path;
      if (note) note.textContent = '';
      return;
    }
    if (result.ok && result.body?.success && !result.body?.selected) return;
    if (note) {
      note.textContent =
        result.body?.note ||
        result.body?.error ||
        'The folder dialog is not available here. Type or paste the folder path instead.';
    }
  }

  async commitFolder() {
    const error = this.el('resourceCatalogFolderError');
    const category = this.el('resourceCatalogFolderCategories')?.querySelector?.(
      'input:checked'
    )?.value;
    const path = String(this.el('resourceCatalogFolderPath')?.value || '').trim();
    if (!path) {
      if (error) error.textContent = 'Type or paste a folder path first.';
      return;
    }
    const result = await this.request(this.api('/folders'), {
      method: 'POST',
      body: JSON.stringify({ category, path })
    });
    if (!result.ok) {
      if (error) error.textContent = result.body?.message || 'The folder could not be added.';
      return;
    }
    this.el('resourceCatalogFolder')?.close?.();
    await this.loadOverview();
    if (result.body?.warning_note) this.showStatus(result.body.warning_note);
    // Nothing is read until the owner approves the new folder.
    await this.openReview();
  }

  openRemoveFolder(source) {
    this.removing = source;
    const text = this.el('resourceCatalogRemoveFolderText');
    if (text) {
      text.textContent =
        source.approved || source.last_attempt
          ? `Ori stops reading ${source.label} and forgets this folder on this computer. What it found stays here marked Access withdrawn, with your notes. Nothing in the folder changes.`
          : `Ori forgets ${source.label} on this computer. Nothing was read in it, and nothing in the folder changes.`;
    }
    const error = this.el('resourceCatalogRemoveFolderError');
    if (error) error.textContent = '';
    this.el('resourceCatalogRemoveFolder')?.showModal?.();
  }

  async commitRemoveFolder() {
    const id = this.removing?.source_id;
    if (!id) return;
    const result = await this.request(this.api(`/folders/${encodeURIComponent(id)}`), {
      method: 'DELETE'
    });
    if (!result.ok) {
      const error = this.el('resourceCatalogRemoveFolderError');
      if (error) error.textContent = result.body?.message || 'The folder could not be removed.';
      return;
    }
    this.el('resourceCatalogRemoveFolder')?.close?.();
    await this.loadOverview();
  }

  async openWithdraw(sourceId) {
    const result = await this.request(
      this.api(`/sources/${encodeURIComponent(sourceId)}/withdraw/review`),
      {
        method: 'POST',
        body: '{}'
      }
    );
    if (!result.ok) {
      this.showStatus(result.body?.message || 'Access could not be reviewed.');
      return;
    }
    this.withdraw = { ...result.body, sourceId };
    const text = this.el('resourceCatalogWithdrawText');
    if (text) text.textContent = result.body.consequence;
    this.el('resourceCatalogWithdraw')?.showModal?.();
  }

  async commitWithdraw() {
    const sourceId = this.withdraw?.sourceId;
    const result = await this.request(
      this.api(`/sources/${encodeURIComponent(sourceId)}/withdraw/commit`),
      {
        method: 'POST',
        body: JSON.stringify({ review_token: this.withdraw?.review_token })
      }
    );
    if (!result.ok) {
      const error = this.el('resourceCatalogWithdrawError');
      if (error) error.textContent = result.body?.message || 'Access could not be withdrawn.';
      return;
    }
    this.el('resourceCatalogWithdraw')?.close?.();
    await this.loadOverview();
  }

  showStatus(message) {
    const operation = this.el('resourceCatalogOperation');
    if (!operation) return;
    operation.textContent = message;
    operation.hidden = false;
  }
}
