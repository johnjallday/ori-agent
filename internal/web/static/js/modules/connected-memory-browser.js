// The Connected memory browser on the Profile page: search what the user's
// other agents remember (only sources that are on), filter by source and
// category, and promote one entry to global memory with a confirmed one-line
// wording. Pure helpers are exported for node:test.

import { MAX_FACT_BYTES, factByteLength, reviewTextValid } from './personal-hq-fact.js';

const ENTRIES_API = '/api/external-memory/entries';
export const PAGE_SIZE = 25;
export const GLOBAL_MEMORY_HREF = '/profile#personalHQKnowledge';

// The Personal HQ categories a promoted fact may take.
export const HQ_CATEGORIES = [
  { value: 'how_you_work', label: 'How you work' },
  { value: 'projects', label: 'Projects and priorities' },
  { value: 'people', label: 'People' },
  { value: 'routines', label: 'Routines' }
];

const SOURCE_LABELS = {
  codex: 'Codex',
  claude_code: 'Claude Code',
  gemini_cli: 'Gemini CLI',
  claude_account: 'Claude account'
};

export function sourceLabel(id) {
  return SOURCE_LABELS[id] || id || 'Source';
}

// entriesQuery builds the GET /api/external-memory/entries query string.
export function entriesQuery({
  q = '',
  source = '',
  category = '',
  offset = 0,
  limit = PAGE_SIZE
} = {}) {
  const params = new URLSearchParams();
  if (q.trim()) params.set('q', q.trim());
  if (source) params.set('source', source);
  if (category) params.set('category', category);
  params.set('limit', String(limit));
  if (offset > 0) params.set('offset', String(offset));
  return params.toString();
}

// chipOptions turns facet counts into a chip row: All first, then each value
// by count. A selected value with no matches stays visible so it can be
// cleared.
export function chipOptions(counts, selected, labelFor = value => value) {
  const entries = Object.entries(counts || {}).filter(([value, n]) => value && n > 0);
  if (selected && !entries.some(([value]) => value === selected)) entries.push([selected, 0]);
  entries.sort((a, b) => b[1] - a[1] || labelFor(a[0]).localeCompare(labelFor(b[0])));
  const total = Object.values(counts || {}).reduce((sum, n) => sum + (Number(n) || 0), 0);
  return [
    { value: '', label: 'All', count: total, active: !selected },
    ...entries.map(([value, count]) => ({
      value,
      label: labelFor(value),
      count,
      active: value === selected
    }))
  ];
}

// entryRowModel is everything one browser row shows.
export function entryRowModel(entry) {
  const meta = [entry?.category, entry?.folder_display || entry?.folder, entry?.date].filter(
    Boolean
  );
  return {
    id: entry?.id || '',
    title: entry?.title || 'Untitled',
    source: entry?.source_label || sourceLabel(entry?.source),
    meta,
    folderTitle: entry?.folder_unverified
      ? 'Ori could not find this folder on disk; this is the agent’s own project key.'
      : entry?.folder || '',
    excerpt: entry?.excerpt || '',
    origin: entry?.origin_display || '',
    promoted: Boolean(entry?.promoted_item_id),
    suggestedText: entry?.suggested_text || '',
    suggestedCategory: HQ_CATEGORIES.some(c => c.value === entry?.suggested_category)
      ? entry.suggested_category
      : 'projects'
  };
}

// promoteTextState is the wording field's counter and validity, under the
// same rules as every other global-memory fact.
export function promoteTextState(text) {
  const bytes = factByteLength(text);
  return {
    counter: `${bytes} / ${MAX_FACT_BYTES} UTF-8 bytes`,
    valid: reviewTextValid(text)
  };
}

// resultsLine summarizes the list above it.
export function resultsLine({ total, shown, q, filtered }) {
  if (!total) {
    if (q || filtered) return 'No entries match.';
    return 'No entries yet. Switch a source on above, or wait for its first sync.';
  }
  const noun = total === 1 ? 'entry' : 'entries';
  return shown < total ? `Showing ${shown} of ${total} ${noun}` : `${total} ${noun}`;
}

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function button(label, className = 'modern-btn modern-btn-secondary') {
  const node = element('button', className, label);
  node.type = 'button';
  return node;
}

// createConnectedMemoryBrowser mounts the browser in container. request is
// the section's fetch wrapper ({ status, payload } or a thrown Error).
export function createConnectedMemoryBrowser(container, { request }) {
  const state = { q: '', source: '', category: '', entries: [], total: 0, loading: false };

  const heading = element('h3', 'connected-memory-browser-title', 'Browse connected memory');
  heading.id = 'connectedMemoryBrowserTitle';
  const hint = element(
    'p',
    'connected-memory-browser-hint',
    'Search what your other agents remember. Only entries from sources that are on appear here; your assistant sees the same ones.'
  );

  const form = element('form', 'connected-memory-search');
  form.setAttribute('role', 'search');
  const searchLabel = element('label', 'visually-hidden', 'Search connected memory');
  searchLabel.htmlFor = 'connectedMemorySearch';
  const search = element('input', 'form-control');
  search.type = 'search';
  search.id = 'connectedMemorySearch';
  search.placeholder = 'Search, e.g. fee sensitivity or "an exact phrase"';
  search.autocomplete = 'off';
  const submit = element('button', 'modern-btn modern-btn-secondary', 'Search');
  submit.type = 'submit';
  form.append(searchLabel, search, submit);

  const sourceChips = element('div', 'connected-memory-chips');
  sourceChips.setAttribute('aria-label', 'Filter by source');
  sourceChips.setAttribute('role', 'group');
  const categoryChips = element('div', 'connected-memory-chips');
  categoryChips.setAttribute('aria-label', 'Filter by category');
  categoryChips.setAttribute('role', 'group');

  const summary = element('p', 'connected-memory-results-line');
  summary.setAttribute('role', 'status');
  summary.setAttribute('aria-live', 'polite');
  const list = element('ol', 'connected-memory-entries');
  const more = button('Show more');
  more.hidden = true;

  container.setAttribute('aria-labelledby', heading.id);
  container.append(heading, hint, form, sourceChips, categoryChips, summary, list, more);

  function renderChips(target, counts, selected, labelFor, onPick) {
    target.replaceChildren(
      ...chipOptions(counts, selected, labelFor).map(option => {
        const chip = button(
          option.value ? `${option.label} (${option.count})` : 'All',
          `connected-memory-chip${option.active ? ' is-active' : ''}`
        );
        chip.setAttribute('aria-pressed', String(option.active));
        chip.addEventListener('click', () => onPick(option.value));
        return chip;
      })
    );
    target.hidden = Object.keys(counts || {}).length === 0 && !selected;
  }

  function renderRow(entry) {
    const model = entryRowModel(entry);
    const item = element('li', 'connected-memory-entry');
    item.dataset.entryId = model.id;

    const meta = element('div', 'connected-memory-entry-meta');
    meta.append(element('span', 'connected-memory-entry-source', model.source));
    model.meta.forEach((part, index) => {
      const span = element('span', '', part);
      if (index === 1 && model.folderTitle) span.title = model.folderTitle;
      meta.append(span);
    });
    const title = element('h4', 'connected-memory-entry-title', model.title);
    const excerpt = element('p', 'connected-memory-entry-excerpt', model.excerpt);
    excerpt.hidden = !model.excerpt;
    if (model.origin) excerpt.title = `From ${model.origin}`;

    const actions = element('div', 'connected-memory-entry-actions');
    const message = element('p', 'connected-memory-entry-message');
    message.setAttribute('role', 'status');
    message.hidden = true;
    const promote = button('Promote to global');
    const promoted = element('span', 'connected-memory-entry-promoted');
    promoted.append('In global memory · ');
    const view = element('a', '', 'View');
    view.href = GLOBAL_MEMORY_HREF;
    promoted.append(view);

    const showPromoted = isPromoted => {
      promote.hidden = isPromoted;
      promoted.hidden = !isPromoted;
    };
    showPromoted(model.promoted);
    actions.append(promote, promoted);

    const editor = element('div', 'connected-memory-promote');
    editor.hidden = true;
    const textID = `connectedMemoryPromoteText-${model.id}`;
    const textLabel = element('label', '', 'Wording for global memory (one line)');
    textLabel.htmlFor = textID;
    const text = element('textarea', 'form-control');
    text.id = textID;
    text.rows = 2;
    text.value = model.suggestedText;
    const counter = element('small');
    const categoryID = `connectedMemoryPromoteCategory-${model.id}`;
    const categoryLabel = element('label', '', 'Category');
    categoryLabel.htmlFor = categoryID;
    const category = element('select', 'form-select');
    category.id = categoryID;
    HQ_CATEGORIES.forEach(option => {
      const node = element('option', '', option.label);
      node.value = option.value;
      category.append(node);
    });
    category.value = model.suggestedCategory;
    const confirm = button('Promote', 'modern-btn modern-btn-primary');
    const cancel = button('Cancel');
    const editorActions = element('div', 'connected-memory-promote-actions');
    editorActions.append(confirm, cancel);
    editor.append(textLabel, text, counter, categoryLabel, category, editorActions);

    const refreshCounter = () => {
      const textState = promoteTextState(text.value);
      counter.textContent = textState.counter;
      confirm.disabled = !textState.valid;
    };
    text.addEventListener('input', refreshCounter);
    refreshCounter();

    promote.addEventListener('click', () => {
      editor.hidden = false;
      promote.hidden = true;
      text.focus();
    });
    cancel.addEventListener('click', () => {
      editor.hidden = true;
      showPromoted(false);
      message.hidden = true;
    });
    confirm.addEventListener('click', async () => {
      confirm.disabled = true;
      try {
        const { payload } = await request(
          `${ENTRIES_API}/${encodeURIComponent(model.id)}/promote`,
          {
            method: 'POST',
            body: JSON.stringify({ text: text.value, category: category.value })
          }
        );
        editor.hidden = true;
        // A fact already waiting for review is not in global memory yet.
        showPromoted(payload.status !== 'already_pending');
        message.textContent = payload.message || 'Promoted to global memory.';
        message.hidden = false;
        document.dispatchEvent(new CustomEvent('personal-assistant-knowledge-changed'));
      } catch (error) {
        message.textContent = error.message || 'The entry could not be promoted.';
        message.hidden = false;
        refreshCounter();
      }
    });

    item.append(meta, title, excerpt, actions, editor, message);
    return item;
  }

  function render() {
    renderChips(sourceChips, state.facets?.sources, state.source, sourceLabel, value => {
      state.source = value;
      void reload();
    });
    renderChips(
      categoryChips,
      state.facets?.categories,
      state.category,
      value => value,
      value => {
        state.category = value;
        void reload();
      }
    );
    summary.textContent = resultsLine({
      total: state.total,
      shown: state.entries.length,
      q: state.q,
      filtered: Boolean(state.source || state.category)
    });
    more.hidden = state.entries.length >= state.total;
  }

  async function fetchPage(offset) {
    const query = entriesQuery({
      q: state.q,
      source: state.source,
      category: state.category,
      offset
    });
    const { payload } = await request(`${ENTRIES_API}?${query}`);
    return payload;
  }

  async function reload() {
    if (state.loading) return;
    state.loading = true;
    container.setAttribute('aria-busy', 'true');
    try {
      const payload = await fetchPage(0);
      state.entries = payload.entries || [];
      state.total = payload.total || 0;
      state.facets = payload.facets || {};
      list.replaceChildren(...state.entries.map(renderRow));
      render();
    } catch (error) {
      summary.textContent = error.message || 'Connected memory could not be searched.';
    } finally {
      state.loading = false;
      container.setAttribute('aria-busy', 'false');
    }
  }

  async function loadMore() {
    if (state.loading) return;
    state.loading = true;
    try {
      const payload = await fetchPage(state.entries.length);
      const next = payload.entries || [];
      state.entries = state.entries.concat(next);
      state.total = payload.total || state.total;
      list.append(...next.map(renderRow));
      render();
    } catch (error) {
      summary.textContent = error.message || 'More entries could not be loaded.';
    } finally {
      state.loading = false;
    }
  }

  form.addEventListener('submit', event => {
    event.preventDefault();
    state.q = search.value;
    void reload();
  });
  more.addEventListener('click', () => void loadMore());

  return { reload };
}
