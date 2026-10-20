// A resource workspace's catalog page (REAPER Studio). The provider's
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
  reaper_closed: 'Needs the application open',
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
// installed.
export function emptyCategoryLine(category, filtered) {
  if (filtered) return 'Nothing matches this search.';
  switch (category?.state) {
    case 'inspected':
      return 'Nothing was found in the sources that were read.';
    case 'access_withdrawn':
      return 'Access to its sources was withdrawn.';
    case 'unavailable':
    case 'refresh_failed':
      return 'Its sources could not be read. Review them, then try again.';
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

// attemptLine is one source's last read on this computer.
export function attemptLine(source, options = {}) {
  if (source?.withdrawn) return 'Access withdrawn';
  const attempt = source?.last_attempt;
  if (!attempt) return 'Approved; not read yet';
  const at = formatDate(attempt.finished_at, options.locale, options.timeZone);
  const read = Number(attempt.entries_read) || 0;
  switch (attempt.outcome) {
    case 'complete':
      return `Read ${at} · ${read} found`;
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

export function entryStateLabel(state) {
  return ENTRY_STATES[state] || '';
}

// rowContext keeps same-named entries apart: kind, source and known facts.
export function rowContext(row) {
  const facts = (row?.facts || []).filter(fact => fact.known).map(fact => fact.value);
  return [row?.kind_label, row?.source_label, ...facts].filter(Boolean).join(' · ');
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
      button.addEventListener('click', () => void this.showDetail(row.entry_id));
      item.append(button);
      list.append(item);
    }
    const empty = this.el('resourceCatalogEmpty');
    if (empty) {
      empty.textContent = emptyCategoryLine(this.currentCategory(), filtered);
      empty.hidden = this.rows.length > 0;
    }
    const more = this.el('resourceCatalogMore');
    if (more) more.hidden = !this.cursor;
  }

  async showDetail(entryId) {
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
    this.renderDetail(panel, result.body);
  }

  renderDetail(panel, detail) {
    const doc = this.document;
    const title = doc.createElement('h2');
    title.textContent = detail.name;
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
      text.textContent = [source.label, source.location, attemptLine(source)]
        .filter(Boolean)
        .join(' · ');
      item.append(text);
      if (source.approved) {
        const button = this.document.createElement('button');
        button.type = 'button';
        button.className = 'modern-btn modern-btn-secondary';
        button.textContent = 'Withdraw access';
        button.addEventListener('click', () => void this.openWithdraw(source.source_id));
        item.append(button);
      }
      root.append(item);
    }
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
