/**
 * Workspace Memory panel manager.
 *
 * Owns the memory pane that the Memory station's panel shows: loads MEMORY.md
 * entries from the memory API and renders what agents are shown (the budget
 * meter), what recent runs learned, the entries grouped by kind with their
 * provenance, and the workspace's link to global memory; handles add / inline
 * edit / delete / promote. MEMORY.md on disk is canonical; this is a thin
 * CRUD surface over /api/workspaces/{id}/memory.
 *
 * Instantiated by WorkspaceDetailPage, which provides workspaceId,
 * workspaceSlug and escapeHtml through the host. The Command view's Memory
 * station loads it when its panel opens.
 *
 * @module workspace-detail-memory
 */

import { workspacePageURL } from './workspace-routes.js';

const MEMORY_TYPES = ['fact', 'feedback', 'decision', 'dead-end', 'watch', 'thread'];

// The panel's sections, in order (PRD memory-station FR18). An entry copied
// from global memory is listed under "Copied from global" whatever its type;
// an entry of a type outside these lands with the facts.
export const MEMORY_SECTIONS = [
  { key: 'watching', title: 'Watching', types: ['watch', 'thread'] },
  { key: 'decisions', title: 'Decisions and dead ends', types: ['decision', 'dead-end'] },
  { key: 'facts', title: 'Facts and feedback', types: ['fact', 'feedback'] },
  { key: 'global', title: 'Copied from global', types: [] }
];

const RECENT_LEARNINGS_LIMIT = 5;

// Categories an entry can be promoted into: the explicit-save path takes the
// dossier sections a user states facts in.
const PROMOTE_CATEGORIES = [
  ['how_you_work', 'How you work'],
  ['people', 'People'],
  ['projects', 'Projects'],
  ['routines', 'Routines']
];

const PROFILE_GLOBAL_MEMORY = '/profile#personalHQKnowledge';

// memoryEntryKind: "managed" is a reviewed global-memory line (read-only here),
// "global_copy" was copied from global memory, everything else is "ordinary".
export function memoryEntryKind(entry) {
  const provenance = String(entry?.provenance || '').trim();
  if (entry?.managed || provenance.startsWith('ori-hq:')) return 'managed';
  if (provenance.startsWith('global:')) return 'global_copy';
  return 'ordinary';
}

// defaultPromoteCategory: user feedback is about how the user works; any other
// entry most often describes their projects.
export function defaultPromoteCategory(type) {
  return type === 'feedback' ? 'how_you_work' : 'projects';
}

// memoryBudgetMeter is what the budget meter shows (PRD memory-station FR15):
// how full the memory is against what agents are shown, capped at 100%, and
// how many entries the prompt drops (`in_prompt: false`, the server's own
// selection). Reviewed global-memory lines are never part of that section.
export function memoryBudgetMeter(meta, entries) {
  const rawSize = Number(meta?.rawSize) || 0;
  const charBudget = Number(meta?.charBudget) || 0;
  const list = Array.isArray(entries) ? entries : [];
  return {
    available: charBudget > 0,
    percent: charBudget > 0 ? Math.min(100, Math.round((rawSize / charBudget) * 100)) : 0,
    overBudget: Boolean(meta?.overBudget),
    dropped: list.filter(
      entry => memoryEntryKind(entry) !== 'managed' && entry?.in_prompt === false
    ).length
  };
}

// memorySections groups the entries for display (FR18): each section's rows
// newest first by date, file order kept for equal dates; empty sections are
// left out. Rows keep their file index, which edit and delete address, so
// the sort is display only. Reviewed global-memory lines are not here: they
// belong with the reviewed assistant learnings.
export function memorySections(entries) {
  const rows = (Array.isArray(entries) ? entries : []).map((entry, index) => ({ entry, index }));
  const sectionKeyFor = entry => {
    if (memoryEntryKind(entry) === 'global_copy') return 'global';
    const type = String(entry?.type || '');
    const section = MEMORY_SECTIONS.find(candidate => candidate.types.includes(type));
    return section ? section.key : 'facts';
  };
  return MEMORY_SECTIONS.map(section => ({
    key: section.key,
    title: section.title,
    rows: rows
      .filter(row => memoryEntryKind(row.entry) !== 'managed')
      .filter(row => sectionKeyFor(row.entry) === section.key)
      .sort((a, b) => String(b.entry?.date || '').localeCompare(String(a.entry?.date || '')))
  })).filter(section => section.rows.length);
}

// recentMemoryLearnings picks the latest runs that wrote to memory (FR20):
// a run counts when its memory_diff artifact added or removed a line. The
// diff already leaves out reviewed global-memory lines.
export function recentMemoryLearnings(runs, limit = RECENT_LEARNINGS_LIMIT) {
  const list = Array.isArray(runs) ? runs : [];
  return list
    .map(run => {
      const artifacts = Array.isArray(run?.artifacts) ? run.artifacts : [];
      const diff = artifacts.find(artifact => artifact?.metadata?.role === 'memory_diff');
      const added = Array.isArray(diff?.metadata?.added) ? diff.metadata.added.length : 0;
      const removed = Array.isArray(diff?.metadata?.removed) ? diff.metadata.removed.length : 0;
      if (!run?.id || (!added && !removed)) return null;
      return {
        id: String(run.id),
        at: String(run.finished_at || run.created_at || ''),
        added,
        removed
      };
    })
    .filter(Boolean)
    .sort((a, b) => b.at.localeCompare(a.at))
    .slice(0, limit);
}

function shortDate(iso) {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return 'Earlier';
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

// Only same-origin page links are rendered from server data.
function safeLocalHref(href, fallback) {
  const value = String(href || '');
  return value.startsWith('/') && !value.startsWith('//') ? value : fallback;
}

export class WorkspaceMemoryManager {
  constructor(host) {
    this.host = host;
    this.loaded = false;
    this.entries = [];
    this.managedLearnings = [];
    this.unstructured = [];
    this.globalMemory = {};
    this.editingIndex = -1;
    this.promotingIndex = -1;
    this.notice = null;
    this.elements = {};
    // Runs that wrote to memory, newest first (the "What recent runs learned"
    // strip); a token drops a slow response from an earlier opening.
    this.learnings = [];
    this.learningsRequest = 0;
    // Set by the Command view's Memory station; called with every payload.
    this.onPayload = null;
  }

  get workspaceSlug() {
    return String(this.host?.workspaceSlug || '').trim();
  }

  get workspaceId() {
    return this.host?.workspaceId || '';
  }

  esc(text) {
    return this.host?.escapeHtml ? this.host.escapeHtml(text) : String(text ?? '');
  }

  bindEvents() {
    this.elements = {
      tab: document.getElementById('workspace-detail-config-memory-tab'),
      meta: document.getElementById('workspace-detail-memory-meta'),
      learnings: document.getElementById('workspace-detail-memory-learnings'),
      list: document.getElementById('workspace-detail-memory-list'),
      global: document.getElementById('workspace-detail-memory-global'),
      addForm: document.getElementById('workspace-detail-memory-add-form'),
      addType: document.getElementById('workspace-detail-memory-add-type'),
      addText: document.getElementById('workspace-detail-memory-add-text')
    };

    // Lazy-load the first time the tab is shown (Bootstrap fires shown.bs.tab).
    this.elements.tab?.addEventListener('shown.bs.tab', () => {
      if (!this.loaded) this.load();
    });

    this.elements.addForm?.addEventListener('submit', event => {
      event.preventDefault();
      this.addEntry();
    });

    // Event delegation for edit/delete/save/cancel on the list.
    this.elements.list?.addEventListener('click', event => this.handleListClick(event));
  }

  async load() {
    if (!this.workspaceId || !this.elements.list) return;
    void this.loadLearnings();
    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/memory`
      );
      if (!response.ok) throw new Error('Failed to load workspace memory');
      this.applyPayload(await response.json());
      this.loaded = true;
      this.editingIndex = -1;
      this.promotingIndex = -1;
      this.render();
    } catch (error) {
      if (window.Toast) window.Toast.error(error.message || 'Failed to load workspace memory');
    }
  }

  // applyPayload takes the authoritative memory response of a read or a
  // mutation.
  applyPayload(payload) {
    this.entries = Array.isArray(payload?.entries) ? payload.entries : [];
    this.managedLearnings = Array.isArray(payload?.managed_learnings)
      ? payload.managed_learnings
      : [];
    this.unstructured = Array.isArray(payload?.unstructured) ? payload.unstructured : [];
    this.globalMemory =
      payload?.global_memory && typeof payload.global_memory === 'object'
        ? payload.global_memory
        : {};
    this.meta = {
      rawSize: Number(payload?.raw_size) || 0,
      charBudget: Number(payload?.char_budget) || 0,
      overBudget: Boolean(payload?.over_budget)
    };
    // The Memory station's badge follows every authoritative payload.
    if (typeof this.onPayload === 'function') this.onPayload(payload);
  }

  // loadLearnings reads the workspace's runs, which carry their artifacts
  // inline, for the "What recent runs learned" strip (FR20). Asked for only
  // when the panel loads; a failure just hides the strip.
  async loadLearnings() {
    if (!this.workspaceId) return;
    const request = this.learningsRequest + 1;
    this.learningsRequest = request;
    let learnings = [];
    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/runs`);
      if (response.ok) learnings = recentMemoryLearnings((await response.json())?.runs);
    } catch {
      learnings = [];
    }
    if (request !== this.learningsRequest) return;
    this.learnings = learnings;
    this.renderLearnings();
  }

  render() {
    this.renderMeta();
    this.renderLearnings();
    this.renderList();
    this.renderGlobal();
  }

  runHref(runId) {
    try {
      return workspacePageURL(this.workspaceSlug, ['runs', runId]);
    } catch {
      return '';
    }
  }

  // renderLearnings draws "What recent runs learned": each run's date (a link
  // to its record) and how many lines it remembered and forgot. Hidden when
  // no recent run wrote to memory.
  renderLearnings() {
    const strip = this.elements.learnings;
    if (!strip) return;
    if (!this.learnings.length) {
      strip.hidden = true;
      strip.innerHTML = '';
      return;
    }
    const rows = this.learnings
      .map(run => {
        const href = this.runHref(run.id);
        const date = this.esc(shortDate(run.at));
        const when = href
          ? `<a class="workspace-detail-memory-learning-link" href="${this.esc(href)}">${date}</a>`
          : `<span>${date}</span>`;
        return `<li class="workspace-detail-memory-learning">${when}<span class="workspace-detail-memory-learning-counts">remembered ${run.added} · forgot ${run.removed}</span></li>`;
      })
      .join('');
    strip.hidden = false;
    strip.innerHTML = `
      <h4 class="workspace-detail-memory-heading" id="workspace-detail-memory-learnings-title">What recent runs learned</h4>
      <ul class="workspace-detail-memory-learnings-list" aria-labelledby="workspace-detail-memory-learnings-title">${rows}</ul>`;
  }

  // renderGlobal is the workspace's line to global memory (FR21): how many
  // entries were copied from it and where to browse it, or why it is not
  // there yet. The Personal HQ's memory is global memory, so it has none.
  renderGlobal() {
    const row = this.elements.global;
    if (!row) return;
    const global = this.globalMemory || {};
    if (global.is_personal_hq) {
      row.hidden = true;
      row.innerHTML = '';
      return;
    }
    row.hidden = false;
    if (!global.available) {
      const message = global.message || 'Global memory is unavailable.';
      const setup = global.setup_href
        ? ` <a href="${this.esc(safeLocalHref(global.setup_href, '/'))}">Go to Home</a>`
        : '';
      row.innerHTML = `<p class="workspace-detail-memory-global-text">${this.esc(message)}${setup}</p>`;
      return;
    }
    const copies = this.entries.filter(entry => memoryEntryKind(entry) === 'global_copy').length;
    const href = safeLocalHref(global.review_href, PROFILE_GLOBAL_MEMORY);
    row.innerHTML = `<p class="workspace-detail-memory-global-text">${copies} copied from global memory · <a href="${this.esc(href)}">Browse global memory on Profile</a></p>`;
  }

  // renderMeta draws the budget meter. The fill width is set through the
  // CSSOM after the markup is in place: no inline style attribute (CSP).
  renderMeta() {
    const meta = this.elements.meta;
    if (!meta) return;
    const meter = memoryBudgetMeter(this.meta, this.entries);
    if (!meter.available) {
      meta.innerHTML = '';
      return;
    }
    const shown = `${meter.percent}% of what agents are shown`;
    let over = '';
    if (meter.overBudget) {
      over = meter.dropped
        ? `Over budget: ${meter.dropped} ${meter.dropped === 1 ? 'entry is' : 'entries are'} not shown to agents`
        : 'Over budget: every entry is still shown to agents, but the next ones may not be';
    }
    meta.innerHTML = `
      <div class="workspace-detail-memory-meter${meter.overBudget ? ' is-over-budget' : ''}">
        <div class="workspace-detail-memory-meter-bar" role="meter" aria-label="Share of the memory agents are shown" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${meter.percent}" aria-valuetext="${this.esc(shown)}">
          <span class="workspace-detail-memory-meter-fill"></span>
        </div>
        <div class="workspace-detail-memory-meter-text">${this.esc(shown)}</div>
        ${over ? `<div class="workspace-detail-memory-meter-over">${this.esc(over)}</div>` : ''}
      </div>`;
    const fill =
      typeof meta.querySelector === 'function'
        ? meta.querySelector('.workspace-detail-memory-meter-fill')
        : null;
    if (fill && fill.style) fill.style.width = `${meter.percent}%`;
  }

  renderList() {
    const list = this.elements.list;
    if (!list) return;

    if (!this.entries.length && !this.managedLearnings.length && !this.unstructured.length) {
      list.innerHTML = `
        <div class="workspace-detail-memory-empty">
          <p>No memory yet. As missions and chats run in this workspace, agents record durable facts here — or add one yourself.</p>
          <p class="workspace-detail-memory-empty-example">Example: <code>[watch] build baseline is ~7 min; flag if &gt;10</code></p>
          <p class="workspace-detail-memory-empty-note">Agents write here under every autonomy policy, including Watch.</p>
        </div>`;
      return;
    }

    const rowHtml = ({ entry, index }) => {
      if (index === this.editingIndex) return this.editRowHtml(entry, index);
      if (index === this.promotingIndex) return this.promoteRowHtml(entry, index);
      return this.entryRowHtml(entry, index);
    };
    const sectionsHtml = memorySections(this.entries)
      .map(section =>
        this.sectionHtml(
          section.key,
          section.title,
          section.rows.length,
          section.rows.map(rowHtml).join('')
        )
      )
      .join('');

    // Reviewed assistant learnings (Personal HQ): the reviewed global-memory
    // lines in this file and the learnings the assistant had approved, both
    // read-only here.
    const managedRows = this.entries
      .map((entry, index) => ({ entry, index }))
      .filter(row => memoryEntryKind(row.entry) === 'managed');
    const managedCount = managedRows.length + this.managedLearnings.length;
    const managedHtml = managedCount
      ? this.sectionHtml(
          'reviewed',
          'Reviewed assistant learnings',
          managedCount,
          managedRows.map(row => this.managedEntryHtml(row.entry, row.index)).join('') +
            this.managedLearnings.map(learning => this.managedLearningHtml(learning)).join('')
        )
      : '';

    const unstructuredHtml = this.unstructured.length
      ? this.sectionHtml(
          'unstructured',
          'Unstructured lines',
          this.unstructured.length,
          `<p class="workspace-detail-memory-section-note">Not injected as entries.</p>
          <ul class="workspace-detail-memory-unstructured-list">${this.unstructured
            .map(
              line => `<li class="workspace-detail-memory-unstructured-line">${this.esc(line)}</li>`
            )
            .join('')}</ul>`
        )
      : '';

    list.innerHTML = this.noticeHtml() + sectionsHtml + managedHtml + unstructuredHtml;
  }

  // sectionHtml is one titled group of the panel: a real heading with its
  // count, then its rows.
  sectionHtml(key, title, count, body) {
    const headingId = `workspace-detail-memory-section-${key}`;
    return `
      <section class="workspace-detail-memory-section" data-memory-section="${key}" aria-labelledby="${headingId}">
        <h4 class="workspace-detail-memory-heading" id="${headingId}">${this.esc(title)} <span class="workspace-detail-memory-heading-count">${count}</span></h4>
        ${body}
      </section>`;
  }

  noticeHtml() {
    if (!this.notice?.message) return '';
    const link = this.notice.href
      ? ` <a href="${this.esc(safeLocalHref(this.notice.href, PROFILE_GLOBAL_MEMORY))}">${this.esc(this.notice.label || 'Open')}</a>`
      : '';
    return `<div class="workspace-detail-memory-notice" role="status">${this.esc(this.notice.message)}${link}</div>`;
  }

  // promoteActionHtml: none in the Personal HQ itself (its memory already is
  // global memory), a disabled-looking button that explains why when there is
  // no assistant with a Personal HQ, and the Promote action otherwise.
  promoteActionHtml(index) {
    const global = this.globalMemory || {};
    if (global.is_personal_hq) return '';
    if (!global.available) {
      const reason = global.message || 'Global memory is unavailable.';
      return `<button type="button" class="workspace-detail-memory-action is-unavailable" data-action="promote" data-index="${index}" aria-disabled="true" title="${this.esc(reason)}">Promote to global</button>`;
    }
    return `<button type="button" class="workspace-detail-memory-action" data-action="promote" data-index="${index}">Promote to global</button>`;
  }

  // managedEntryHtml renders a reviewed global-memory line read-only: it
  // changes only through review on the Profile page.
  managedEntryHtml(entry, index) {
    const type = this.esc(entry?.type || 'fact');
    const href = safeLocalHref(this.globalMemory?.review_href, PROFILE_GLOBAL_MEMORY);
    return `
      <article class="workspace-detail-memory-entry is-global" data-index="${index}">
        <div class="workspace-detail-memory-entry-main">
          <span class="workspace-detail-memory-badge workspace-detail-memory-badge-${type}">${type}</span>
          <span class="workspace-detail-memory-text">${this.esc(entry?.text || '')}</span>
        </div>
        <div class="workspace-detail-memory-entry-foot">
          <span class="workspace-detail-memory-prov">Global memory (reviewed)</span>
          <span class="workspace-detail-memory-actions">
            <a class="workspace-detail-memory-action" href="${this.esc(href)}">Review on Profile</a>
          </span>
        </div>
      </article>`;
  }

  promoteRowHtml(entry, index) {
    const selected = defaultPromoteCategory(entry?.type);
    const options = PROMOTE_CATEGORIES.map(
      ([value, label]) =>
        `<option value="${value}"${value === selected ? ' selected' : ''}>${label}</option>`
    ).join('');
    return `
      <article class="workspace-detail-memory-entry is-promoting" data-index="${index}">
        <div class="workspace-detail-memory-promote-note">Promote to global memory: your assistant will know this on every page. The entry stays in this workspace.</div>
        <div class="workspace-detail-memory-edit">
          <select class="form-select workspace-detail-memory-type is-category" data-promote-category="${index}" aria-label="Global memory category">${options}</select>
          <input type="text" class="form-control workspace-detail-memory-input" data-promote-text="${index}" maxlength="500" value="${this.esc(entry?.text || '')}" aria-label="Wording for global memory">
        </div>
        <div class="workspace-detail-memory-entry-foot">
          <span class="workspace-detail-memory-prov">${this.esc(entry?.provenance || '')}</span>
          <span class="workspace-detail-memory-actions">
            <button type="button" class="workspace-detail-memory-action" data-action="promote-confirm" data-index="${index}">Promote</button>
            <button type="button" class="workspace-detail-memory-action" data-action="promote-cancel" data-index="${index}">Cancel</button>
          </span>
        </div>
      </article>`;
  }

  managedLearningHtml(learning) {
    const evidence = Array.isArray(learning?.evidence) ? learning.evidence : [];
    const evidenceItems = evidence
      .map(item => {
        const summary = this.esc(item?.summary || 'Evidence');
        const route = String(item?.route || '');
        return route.startsWith('/workspaces/')
          ? `<li><a href="${this.esc(route)}">${summary}</a></li>`
          : `<li>${summary}</li>`;
      })
      .join('');
    return `
      <article class="workspace-detail-memory-entry is-managed" data-learning-id="${this.esc(learning?.id || '')}">
        <div class="workspace-detail-memory-entry-main">
          <span class="workspace-detail-memory-badge">${this.esc(learning?.type || 'learning')}</span>
          <span class="workspace-detail-memory-text">${this.esc(learning?.text || '')}</span>
        </div>
        <div class="workspace-detail-memory-entry-foot">
          <span class="workspace-detail-memory-prov">assistant-approved · ${this.esc(learning?.confidence || '')} · ${evidence.length} evidence</span>
          <details><summary>Evidence</summary><ul>${evidenceItems}</ul></details>
        </div>
      </article>`;
  }

  entryRowHtml(entry, index) {
    const kind = memoryEntryKind(entry);
    if (kind === 'managed') return this.managedEntryHtml(entry, index);
    const type = this.esc(entry?.type || 'fact');
    // A copy of a global fact is ordinary memory; it no longer follows the
    // global fact, so say where it came from instead of its raw provenance.
    const origin = kind === 'global_copy' ? 'copied from global' : entry?.provenance;
    const meta = [origin, entry?.date]
      .filter(Boolean)
      .map(v => this.esc(v))
      .join(' · ');
    // Dropped by the prompt budget (FR17): said in words, not only by the
    // muted badge and rule; edit, delete and promote stay so it can be pruned.
    const dropped = entry?.in_prompt === false;
    const classes = [
      'workspace-detail-memory-entry',
      kind === 'global_copy' ? 'is-global-copy' : '',
      dropped ? 'is-not-in-prompt' : ''
    ]
      .filter(Boolean)
      .join(' ');
    return `
      <article class="${classes}" data-index="${index}">
        <div class="workspace-detail-memory-entry-main">
          <span class="workspace-detail-memory-badge workspace-detail-memory-badge-${type}">${type}</span>
          <span class="workspace-detail-memory-text">${this.esc(entry?.text || '')}</span>
        </div>
        <div class="workspace-detail-memory-entry-foot">
          <span class="workspace-detail-memory-prov">${[
            dropped
              ? '<span class="workspace-detail-memory-dropped">not shown to agents</span>'
              : '',
            meta
          ]
            .filter(Boolean)
            .join(' · ')}</span>
          <span class="workspace-detail-memory-actions">
            ${kind === 'ordinary' ? this.promoteActionHtml(index) : ''}
            <button type="button" class="workspace-detail-memory-action" data-action="edit" data-index="${index}">Edit</button>
            <button type="button" class="workspace-detail-memory-action is-danger" data-action="delete" data-index="${index}">Delete</button>
          </span>
        </div>
      </article>`;
  }

  editRowHtml(entry, index) {
    const options = MEMORY_TYPES.map(
      t => `<option value="${t}"${t === (entry?.type || 'fact') ? ' selected' : ''}>${t}</option>`
    ).join('');
    return `
      <article class="workspace-detail-memory-entry is-editing" data-index="${index}">
        <div class="workspace-detail-memory-edit">
          <select class="form-select workspace-detail-memory-type" data-edit-type="${index}" aria-label="Entry type">${options}</select>
          <input type="text" class="form-control workspace-detail-memory-input" data-edit-text="${index}" maxlength="500" value="${this.esc(entry?.text || '')}" aria-label="Entry text">
        </div>
        <div class="workspace-detail-memory-entry-foot">
          <span class="workspace-detail-memory-prov">${this.esc(entry?.provenance || '')}</span>
          <span class="workspace-detail-memory-actions">
            <button type="button" class="workspace-detail-memory-action" data-action="save" data-index="${index}">Save</button>
            <button type="button" class="workspace-detail-memory-action" data-action="cancel" data-index="${index}">Cancel</button>
          </span>
        </div>
      </article>`;
  }

  // The list is rebuilt from a string, so the control that was focused is
  // replaced and focus falls to <body>. Inside the Memory station's modal that
  // would also lose Escape and the focus trap, so focus goes back to the first
  // of `selectors` that exists in the list, else to the add input. Focus the
  // user has moved elsewhere is left alone.
  keepFocus(...selectors) {
    if (typeof document === 'undefined') return;
    const active = document.activeElement;
    if (active && active !== document.body) return;
    const list = this.elements.list;
    const found =
      list && typeof list.querySelector === 'function'
        ? selectors.map(selector => list.querySelector(selector)).find(Boolean)
        : null;
    const target = found || this.elements.addText;
    if (target && typeof target.focus === 'function') target.focus();
  }

  handleListClick(event) {
    const button = event.target.closest('[data-action]');
    if (!button) return;
    const index = Number(button.dataset.index);
    if (!Number.isInteger(index)) return;
    const promoteButton = `[data-action="promote"][data-index="${index}"]`;
    const editButton = `[data-action="edit"][data-index="${index}"]`;
    switch (button.dataset.action) {
      case 'edit':
        this.editingIndex = index;
        this.promotingIndex = -1;
        this.renderList();
        this.keepFocus(`[data-edit-text="${index}"]`);
        break;
      case 'cancel':
        this.editingIndex = -1;
        this.renderList();
        this.keepFocus(editButton);
        break;
      case 'promote':
        if (!this.globalMemory?.available) {
          this.notice = {
            message: this.globalMemory?.message || 'Global memory is unavailable.',
            href: this.globalMemory?.setup_href || '/',
            label: 'Go to Home'
          };
        } else {
          this.promotingIndex = index;
          this.editingIndex = -1;
        }
        this.renderList();
        this.keepFocus(`[data-promote-text="${index}"]`, promoteButton);
        break;
      case 'promote-cancel':
        this.promotingIndex = -1;
        this.renderList();
        this.keepFocus(promoteButton, editButton);
        break;
      case 'promote-confirm':
        this.promoteEntry(index);
        break;
      case 'save':
        this.saveEdit(index);
        break;
      case 'delete':
        this.deleteEntry(index);
        break;
      default:
        break;
    }
  }

  async addEntry() {
    const text = (this.elements.addText?.value || '').trim();
    const type = this.elements.addType?.value || 'fact';
    if (!text) return;
    await this.mutate(
      'POST',
      `/api/workspaces/${encodeURIComponent(this.workspaceId)}/memory/entries`,
      { text, type },
      () => {
        if (this.elements.addText) this.elements.addText.value = '';
        if (window.Toast) window.Toast.success('Saved to workspace memory');
      }
    );
  }

  async saveEdit(index) {
    const typeEl = this.elements.list.querySelector(`[data-edit-type="${index}"]`);
    const textEl = this.elements.list.querySelector(`[data-edit-text="${index}"]`);
    const text = (textEl?.value || '').trim();
    const type = typeEl?.value || 'fact';
    if (!text) return;
    await this.mutate(
      'PUT',
      `/api/workspaces/${encodeURIComponent(this.workspaceId)}/memory/entries/${index}`,
      { text, type },
      () => {
        this.editingIndex = -1;
        if (window.Toast) window.Toast.success('Memory entry updated');
      }
    );
    this.keepFocus(`[data-action="edit"][data-index="${index}"]`);
  }

  async deleteEntry(index) {
    await this.mutate(
      'DELETE',
      `/api/workspaces/${encodeURIComponent(this.workspaceId)}/memory/entries/${index}`,
      null,
      () => {
        if (window.Toast) window.Toast.info('Memory entry removed');
      }
    );
    // The entry that took its place, else the one before it.
    this.keepFocus(
      `[data-action="edit"][data-index="${index}"]`,
      `[data-action="edit"][data-index="${index - 1}"]`
    );
  }

  // promoteEntry copies one entry into global memory as an approved fact. The
  // workspace entry is unchanged, so the list itself needs no refresh.
  async promoteEntry(index) {
    const categoryEl = this.elements.list.querySelector(`[data-promote-category="${index}"]`);
    const textEl = this.elements.list.querySelector(`[data-promote-text="${index}"]`);
    const text = (textEl?.value || '').trim();
    const category = categoryEl?.value || defaultPromoteCategory(this.entries[index]?.type);
    if (!text) return;
    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/memory/entries/${index}/promote`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ text, category })
        }
      );
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        if (payload?.code === 'global_memory_unavailable') {
          this.notice = {
            message: payload.message,
            href: this.globalMemory?.setup_href || '/',
            label: 'Go to Home'
          };
          this.promotingIndex = -1;
          this.renderList();
          this.keepFocus('.workspace-detail-memory-notice a');
          return;
        }
        throw new Error(
          payload?.message || payload?.error?.message || `Request failed (${response.status})`
        );
      }
      this.notice = {
        message: payload.message || 'Promoted to global memory.',
        href: payload.href || PROFILE_GLOBAL_MEMORY,
        label: 'Review on your Profile'
      };
      this.promotingIndex = -1;
      this.renderList();
      this.keepFocus(
        '.workspace-detail-memory-notice a',
        `[data-action="edit"][data-index="${index}"]`
      );
      if (window.Toast) window.Toast.success(this.notice.message);
    } catch (error) {
      if (window.Toast) window.Toast.error(error.message || 'Promote to global memory failed');
    }
  }

  // mutate performs a memory mutation, refreshes from the authoritative
  // response, and re-renders. onSuccess runs before re-render.
  async mutate(method, url, body, onSuccess) {
    try {
      const options = { method, headers: { 'Content-Type': 'application/json' } };
      if (body) options.body = JSON.stringify(body);
      const response = await fetch(url, options);
      if (!response.ok) {
        const payload = await response.json().catch(() => ({}));
        throw new Error(
          payload?.error?.message || payload?.message || `Request failed (${response.status})`
        );
      }
      this.applyPayload(await response.json());
      // Indexes may have shifted; never keep a promote form on another entry.
      this.promotingIndex = -1;
      if (typeof onSuccess === 'function') onSuccess();
      this.render();
    } catch (error) {
      if (window.Toast) window.Toast.error(error.message || 'Memory update failed');
    }
  }
}
