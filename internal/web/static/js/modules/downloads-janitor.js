// downloads-janitor.js — the Downloads Janitor panel on the workspace detail
// page.
//
// In this first slice it renders two states from one server response
// (GET /api/workspaces/{id}/downloads-janitor):
//
//   • Setup required — a card that names the folder Ori will watch, states
//     exactly what approving it allows (list + metadata, approved moves into
//     <folder>/Filed, recoverable Trash, never permanent deletion), shows the
//     daily catch-up time in local time, and notes that reading file contents
//     is a separate opt-in that stays off.
//   • Configured — the folder in use, a status summary (state, pending count,
//     last scan), a Scan now action, and the review batch: one row per
//     proposed file with its category, destination, reason, and confidence.
//
// Two rules this module follows deliberately:
//   • It mounts only when the server says the workspace is a Downloads Janitor
//     workspace (status.applies). Provenance is decided server-side.
//   • The only path it ever sends is the one the user picked or typed and then
//     explicitly confirmed. Nothing here selects a folder on its own.
(function () {
  'use strict';

  const MOUNT_ID = 'downloadsJanitorMount';

  let workspaceId = '';
  let busy = false;
  let lastStatus = null;
  let lastBatch = null;
  let lastCandidates = [];
  let categories = [];
  // Selected candidate IDs. Selection starts empty on every render and is
  // never pre-filled: opening the review surface must not be able to cause a
  // file mutation (FR-62).
  let selected = new Set();
  // Active state filter: '' (all), 'needs_review', 'pending', 'skipped'.
  let filter = '';

  function wsId() {
    return workspaceId || (typeof window !== 'undefined' && window.currentWorkspaceId) || '';
  }

  function mount() {
    return document.getElementById(MOUNT_ID);
  }

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  }

  function button(label, className, onClick) {
    const b = el('button', className, label);
    b.setAttribute('type', 'button');
    if (onClick) b.addEventListener('click', onClick);
    return b;
  }

  function clear(node) {
    if (!node) return;
    node.textContent = '';
  }

  // ---------------------------------------------------------------- rendering

  const STATE_LABELS = {
    setup_required: 'Setup required',
    ready: 'Ready',
    needs_attention: 'Needs attention'
  };

  const COMPONENT_LABELS = {
    directory_access: 'Folder access',
    destination: 'Filing folder',
    mcp_binding: 'Workspace access',
    persistence: 'Saved state',
    watcher: 'Folder watching',
    scheduler: 'Daily catch-up'
  };

  // Non-color status tokens accompany every row so state is never conveyed by
  // color alone.
  const STATUS_MARKS = { ok: '✓', failed: '!', pending: '–' };

  function stateBadge(state) {
    const badge = el('span', 'dj-badge dj-badge-' + String(state || '').replace(/_/g, '-'));
    badge.textContent = STATE_LABELS[state] || 'Unknown';
    return badge;
  }

  function disclosureList(rootLabel, filingRootName, dailyTime) {
    const list = el('ul', 'dj-disclosure');
    const points = [
      'Ori lists the files directly inside ' +
        rootLabel +
        ' and reads their names, types, sizes, and dates.',
      'Files you approve are moved into ' +
        rootLabel +
        '/' +
        (filingRootName || 'Filed') +
        '/<category> — always inside the same folder.',
      'Files you explicitly mark for Trash go to your system Trash, where you can restore them. Ori never deletes anything permanently.',
      'Nothing moves without your approval. Ori proposes; you decide.',
      'A catch-up scan runs daily at ' + (dailyTime || '09:00') + ' your local time.',
      'Reading what is inside your files is off, and is a separate choice you can make later.'
    ];
    points.forEach(text => list.appendChild(el('li', 'dj-disclosure-item', text)));
    return list;
  }

  function renderSetupCard(host, status) {
    const suggestion = status.suggestion || {};
    const settings = status.settings || {};
    const label = suggestion.label || 'Downloads folder';
    const suggestedPath = suggestion.suggested_path || '~/Downloads';

    const card = el('section', 'dj-card');
    card.setAttribute('role', 'group');
    card.setAttribute('aria-labelledby', 'downloadsJanitorTitle');

    const head = el('div', 'dj-head');
    const heading = el('div', 'dj-heading');
    const title = el('h2', 'dj-title', 'Downloads Janitor');
    title.id = 'downloadsJanitorTitle';
    heading.appendChild(title);
    heading.appendChild(
      el('p', 'dj-sub', 'Choose the folder to tidy. Nothing is scanned or moved until you do.')
    );
    head.appendChild(heading);
    head.appendChild(stateBadge('setup_required'));
    card.appendChild(head);

    const field = el('div', 'dj-field');
    const inputId = 'downloadsJanitorPath';
    const inputLabel = el('label', 'dj-label', label);
    inputLabel.setAttribute('for', inputId);
    const input = el('input', 'dj-input');
    input.id = inputId;
    input.setAttribute('type', 'text');
    input.setAttribute('spellcheck', 'false');
    input.setAttribute('aria-describedby', 'downloadsJanitorDisclosure');
    input.value = suggestedPath;
    const row = el('div', 'dj-field-row');
    row.appendChild(input);
    row.appendChild(button('Browse…', 'dj-btn dj-btn-secondary', browse));
    field.appendChild(inputLabel);
    field.appendChild(row);
    card.appendChild(field);

    const disclosure = el('div', 'dj-disclosure-wrap');
    disclosure.id = 'downloadsJanitorDisclosure';
    if (suggestion.access_disclosure) {
      disclosure.appendChild(el('p', 'dj-disclosure-lead', suggestion.access_disclosure));
    }
    disclosure.appendChild(
      disclosureList(
        'the folder you choose',
        suggestion.filing_root_name || settings.filing_root_name,
        suggestion.daily_scan_local_time || settings.daily_scan_local_time
      )
    );
    card.appendChild(disclosure);

    const actions = el('div', 'dj-actions');
    const confirm = button('Use this folder', 'dj-btn dj-btn-primary', () => {
      const chosen = document.getElementById(inputId);
      void confirmSetup(chosen ? chosen.value : '');
    });
    confirm.id = 'downloadsJanitorConfirm';
    actions.appendChild(confirm);
    card.appendChild(actions);

    card.appendChild(errorRegion());
    host.appendChild(card);
  }

  // -------------------------------------------------------------- formatting

  function formatSize(bytes) {
    const size = Number(bytes) || 0;
    if (size < 1024) return size + ' B';
    const units = ['KB', 'MB', 'GB', 'TB'];
    let value = size / 1024;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
      value /= 1024;
      unit++;
    }
    return (value < 10 ? value.toFixed(1) : Math.round(value)) + ' ' + units[unit];
  }

  function formatWhen(iso) {
    if (!iso) return '';
    const when = new Date(iso);
    if (isNaN(when.getTime())) return '';
    const elapsed = Date.now() - when.getTime();
    const minute = 60000;
    const hour = 60 * minute;
    const day = 24 * hour;
    if (elapsed < minute) return 'just now';
    if (elapsed < hour) return Math.round(elapsed / minute) + ' min ago';
    if (elapsed < day) return Math.round(elapsed / hour) + ' h ago';
    if (elapsed < 7 * day) return Math.round(elapsed / day) + ' d ago';
    return when.toLocaleDateString();
  }

  const SOURCE_LABELS = {
    manual: 'Scan now',
    test: 'Test scan',
    watcher: 'Folder activity',
    daily: 'Daily catch-up'
  };

  const STATE_ROW_LABELS = {
    pending: 'Pending',
    approved: 'Approved',
    applying: 'Applying…',
    applied: 'Filed',
    stale: 'Changed — needs a rescan',
    failed: 'Failed',
    skipped: 'Skipped'
  };

  // --------------------------------------------------------------- review UI

  // batchSummaryLine states what the scan produced, in words rather than only
  // colours or counts in isolation (FR-60).
  function batchSummaryLine(batch) {
    const summary = (batch && batch.summary) || {};
    const parts = [];
    parts.push((summary.proposed || 0) + ' proposed');
    if (summary.needs_review) parts.push(summary.needs_review + ' needing review');
    if (summary.skipped) parts.push(summary.skipped + ' skipped');
    if (summary.stale) parts.push(summary.stale + ' changed since the scan');
    if (summary.ineligible) parts.push(summary.ineligible + ' not eligible');
    const source = SOURCE_LABELS[batch && batch.source] || 'Scan';
    const when = formatWhen(batch && (batch.completed_at || batch.started_at));
    return parts.join(' · ') + ' — ' + source + (when ? ', ' + when : '');
  }

  function categoryPicker(candidate) {
    const select = document.createElement('select');
    select.className = 'dj-category';
    select.setAttribute('aria-label', 'Category for ' + candidate.name);
    const chosen = candidate.decision_category || candidate.category || '';
    const options = categories.length
      ? categories
      : [{ id: chosen, label: chosen }].filter(option => option.id);
    options.forEach(option => {
      const node = document.createElement('option');
      node.value = option.id;
      node.textContent = option.label || option.id;
      if (option.id === chosen) node.selected = true;
      select.appendChild(node);
    });
    // A category change is a decision, so it is recorded immediately rather
    // than held in the page: a reload must not lose the user's work (FR-74).
    select.addEventListener('change', () => {
      void submitDecisions([
        { candidate_id: candidate.id, decision: 'move', category: select.value }
      ]);
    });
    return select;
  }

  function candidateRow(candidate) {
    const row = el('tr', 'dj-row-item dj-row-state-' + (candidate.state || 'pending'));
    row.setAttribute('data-candidate-id', candidate.id);

    const selectCell = el('td', 'dj-cell dj-cell-select');
    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.className = 'dj-select';
    checkbox.checked = selected.has(candidate.id);
    checkbox.setAttribute('aria-label', 'Select ' + candidate.name);
    checkbox.disabled = candidate.state !== 'pending' && candidate.state !== 'approved';
    checkbox.addEventListener('change', () => {
      if (checkbox.checked) selected.add(candidate.id);
      else selected.delete(candidate.id);
      updateSelectionSummary();
    });
    selectCell.appendChild(checkbox);
    row.appendChild(selectCell);

    const nameCell = el('td', 'dj-cell dj-cell-name');
    nameCell.appendChild(el('span', 'dj-name', candidate.name));
    if (candidate.needs_review) {
      const flag = el('span', 'dj-flag', 'Needs review');
      flag.setAttribute('title', 'Ori could not place this confidently');
      nameCell.appendChild(flag);
    }
    row.appendChild(nameCell);

    row.appendChild(
      el('td', 'dj-cell dj-cell-type', candidate.extension || candidate.mime_type || '—')
    );
    row.appendChild(el('td', 'dj-cell dj-cell-size', formatSize(candidate.size)));
    row.appendChild(el('td', 'dj-cell dj-cell-modified', formatWhen(candidate.modified_at) || '—'));

    const categoryCell = el('td', 'dj-cell dj-cell-category');
    if (candidate.state === 'pending' || candidate.state === 'approved') {
      categoryCell.appendChild(categoryPicker(candidate));
    } else {
      categoryCell.appendChild(
        el('span', 'dj-category-static', candidate.decision_category || candidate.category || '—')
      );
    }
    categoryCell.appendChild(el('span', 'dj-destination', candidate.destination || ''));
    row.appendChild(categoryCell);

    const reasonCell = el('td', 'dj-cell dj-cell-reason');
    reasonCell.appendChild(el('span', 'dj-reason', candidate.reason || ''));
    if (candidate.confidence) {
      const confidence = el(
        'span',
        'dj-confidence dj-confidence-' + candidate.confidence,
        candidate.confidence + ' confidence'
      );
      reasonCell.appendChild(confidence);
    }
    row.appendChild(reasonCell);

    const stateCell = el('td', 'dj-cell dj-cell-state');
    stateCell.appendChild(
      el('span', 'dj-state', STATE_ROW_LABELS[candidate.state] || candidate.state)
    );
    if (candidate.state_reason)
      stateCell.appendChild(el('span', 'dj-state-reason', candidate.state_reason));
    row.appendChild(stateCell);

    const actionCell = el('td', 'dj-cell dj-cell-actions');
    if (candidate.state === 'pending' || candidate.state === 'approved') {
      actionCell.appendChild(
        button('Skip', 'dj-btn dj-btn-quiet', () => {
          void submitDecisions([{ candidate_id: candidate.id, decision: 'skip' }]);
        })
      );
    } else if (candidate.state === 'skipped') {
      actionCell.appendChild(el('span', 'dj-muted', 'Dismissed'));
    }
    row.appendChild(actionCell);

    return row;
  }

  function visibleCandidates() {
    if (!filter) return lastCandidates;
    if (filter === 'needs_review')
      return lastCandidates.filter(candidate => candidate.needs_review);
    return lastCandidates.filter(candidate => candidate.state === filter);
  }

  const FILTERS = [
    { id: '', label: 'All' },
    { id: 'needs_review', label: 'Needs review' },
    { id: 'pending', label: 'Pending' },
    { id: 'skipped', label: 'Skipped' }
  ];

  function filterBar() {
    const bar = el('div', 'dj-filters');
    bar.setAttribute('role', 'group');
    bar.setAttribute('aria-label', 'Filter review items');
    FILTERS.forEach(option => {
      const control = button(
        option.label,
        'dj-filter' + (filter === option.id ? ' dj-filter-active' : ''),
        () => {
          filter = option.id;
          renderBatch();
        }
      );
      control.setAttribute('aria-pressed', filter === option.id ? 'true' : 'false');
      bar.appendChild(control);
    });
    return bar;
  }

  function reviewTable() {
    const table = el('table', 'dj-table');
    table.setAttribute('aria-label', 'Files proposed for filing');

    const head = document.createElement('thead');
    const headRow = document.createElement('tr');
    // The select column's header is a screen-reader label, not a select-all:
    // a select-all control on a list that can include Trash decisions is how
    // bulk mistakes happen.
    [
      { label: 'Select', className: 'dj-th-select' },
      { label: 'File' },
      { label: 'Type' },
      { label: 'Size' },
      { label: 'Modified' },
      { label: 'Category and destination' },
      { label: 'Why' },
      { label: 'State' },
      { label: 'Actions' }
    ].forEach(column => {
      const cell = el('th', 'dj-th ' + (column.className || ''), column.label);
      cell.setAttribute('scope', 'col');
      headRow.appendChild(cell);
    });
    head.appendChild(headRow);
    table.appendChild(head);

    const body = document.createElement('tbody');
    body.className = 'dj-tbody';
    visibleCandidates().forEach(candidate => body.appendChild(candidateRow(candidate)));
    table.appendChild(body);
    return table;
  }

  function updateSelectionSummary() {
    const node = document.getElementById('downloadsJanitorSelection');
    if (!node) return;
    const count = selected.size;
    node.textContent =
      count === 0
        ? 'No files selected.'
        : count + (count === 1 ? ' file selected.' : ' files selected.');
  }

  // renderBatch repaints only the review section, so changing a filter or
  // recording one decision does not rebuild (and re-focus) the whole card.
  function renderBatch() {
    const container = document.getElementById('downloadsJanitorBatch');
    if (!container) return;
    clear(container);

    if (!lastBatch) {
      const empty = el('div', 'dj-empty');
      empty.appendChild(el('p', 'dj-empty-title', 'Nothing to review'));
      empty.appendChild(
        el(
          'p',
          'dj-empty-copy',
          'New downloads appear here after a scan. Nothing is moved without your approval.'
        )
      );
      container.appendChild(empty);
      return;
    }

    const summary = el('p', 'dj-batch-summary', batchSummaryLine(lastBatch));
    summary.setAttribute('role', 'status');
    summary.setAttribute('aria-live', 'polite');
    container.appendChild(summary);
    container.appendChild(filterBar());

    const rows = visibleCandidates();
    if (rows.length === 0) {
      container.appendChild(el('p', 'dj-empty-copy', 'No files match this filter.'));
    } else {
      const scroller = el('div', 'dj-table-scroll');
      scroller.appendChild(reviewTable());
      container.appendChild(scroller);
    }

    const selection = el('p', 'dj-selection');
    selection.id = 'downloadsJanitorSelection';
    selection.setAttribute('role', 'status');
    selection.setAttribute('aria-live', 'polite');
    container.appendChild(selection);
    updateSelectionSummary();
  }

  function renderConfiguredCard(host, status) {
    const settings = status.settings || {};
    const readiness = status.readiness || {};

    const card = el('section', 'dj-card');
    card.setAttribute('role', 'group');
    card.setAttribute('aria-labelledby', 'downloadsJanitorTitle');

    const head = el('div', 'dj-head');
    const heading = el('div', 'dj-heading');
    const title = el('h2', 'dj-title', 'Downloads Janitor');
    title.id = 'downloadsJanitorTitle';
    heading.appendChild(title);
    const sub = el('p', 'dj-sub');
    sub.textContent = 'Tidying ' + (settings.root_path || 'your chosen folder');
    heading.appendChild(sub);
    const stats = el('p', 'dj-stats');
    stats.id = 'downloadsJanitorStats';
    stats.setAttribute('role', 'status');
    stats.setAttribute('aria-live', 'polite');
    stats.textContent = statsLine();
    heading.appendChild(stats);
    head.appendChild(heading);
    head.appendChild(stateBadge(readiness.state));
    card.appendChild(head);

    const rows = el('ul', 'dj-rows');
    (readiness.checks || []).forEach(check => {
      const li = el('li', 'dj-row dj-row-' + (check.status || 'pending'));
      const mark = el('span', 'dj-row-mark', STATUS_MARKS[check.status] || '–');
      mark.setAttribute('aria-hidden', 'true');
      li.appendChild(mark);
      li.appendChild(
        el('span', 'dj-row-label', (COMPONENT_LABELS[check.component] || check.component) + ': ')
      );
      const value = el('span', 'dj-row-value');
      // Screen readers get the status word; sighted users get the mark + text.
      value.textContent =
        (check.status === 'ok'
          ? 'OK'
          : check.status === 'failed'
            ? 'Needs attention'
            : 'Not running yet') + (check.message ? ' — ' + check.message : '');
      li.appendChild(value);
      rows.appendChild(li);
    });
    card.appendChild(rows);

    const actions = el('div', 'dj-actions');
    const scan = button('Scan now', 'dj-btn dj-btn-primary', () => void scanNow());
    scan.id = 'downloadsJanitorScan';
    actions.appendChild(scan);
    const failing = (readiness.checks || []).filter(c => c.status === 'failed');
    if (
      failing.some(
        c =>
          c.repair === 'relink_folder' ||
          c.repair === 'choose_folder' ||
          c.repair === 'grant_permission'
      )
    ) {
      actions.appendChild(
        button('Choose the folder again', 'dj-btn dj-btn-secondary', () => {
          lastStatus = Object.assign({}, status, {
            settings: Object.assign({}, settings, { root_path: '' })
          });
          renderSetupPrompt(status);
        })
      );
    }
    actions.appendChild(button('Check again', 'dj-btn dj-btn-secondary', () => void refresh()));
    card.appendChild(actions);

    // The review batch mounts here and repaints on its own, so recording one
    // decision does not rebuild (and re-focus) the whole card.
    const batchHost = el('div', 'dj-batch');
    batchHost.id = 'downloadsJanitorBatch';
    card.appendChild(batchHost);

    card.appendChild(errorRegion());
    host.appendChild(card);
    renderBatch();
  }

  // renderSetupPrompt re-opens the folder chooser for an already-configured
  // workspace (relink/repair), reusing the same confirm path.
  function renderSetupPrompt(status) {
    const host = mount();
    if (!host) return;
    clear(host);
    renderSetupCard(host, {
      settings: status.settings || {},
      suggestion: status.suggestion || {
        label: 'Downloads folder',
        suggested_path: (status.settings && status.settings.root_path) || '~/Downloads',
        filing_root_name: status.settings && status.settings.filing_root_name,
        daily_scan_local_time: status.settings && status.settings.daily_scan_local_time
      }
    });
  }

  function errorRegion() {
    const region = el('p', 'dj-error');
    region.id = 'downloadsJanitorError';
    region.setAttribute('role', 'status');
    region.setAttribute('aria-live', 'polite');
    region.hidden = true;
    return region;
  }

  function showError(message) {
    const region = document.getElementById('downloadsJanitorError');
    if (!region) return;
    region.textContent = message || '';
    region.hidden = !message;
  }

  function render(status) {
    lastStatus = status;
    const host = mount();
    if (!host) return;
    if (!status || !status.applies) {
      host.hidden = true;
      clear(host);
      return;
    }
    host.hidden = false;
    clear(host);
    if (status.settings && status.settings.root_path && status.settings.directory_reference_id) {
      renderConfiguredCard(host, status);
    } else {
      renderSetupCard(host, status);
    }
  }

  // statsLine states what is waiting and when Ori last looked — the two facts
  // that tell the user whether this workspace is doing anything.
  function statsLine() {
    if (!lastBatch) return 'No files waiting for review.';
    const pending = (lastBatch.summary && lastBatch.summary.proposed) || 0;
    const when = formatWhen(lastBatch.completed_at || lastBatch.started_at);
    const waiting =
      pending === 0
        ? 'No files waiting for review'
        : pending + (pending === 1 ? ' file waiting for review' : ' files waiting for review');
    return waiting + (when ? ' · last scan ' + when : '');
  }

  function refreshStats() {
    const node = document.getElementById('downloadsJanitorStats');
    if (node) node.textContent = statsLine();
  }

  // ------------------------------------------------------------------ actions

  async function browse() {
    if (busy) return;
    showError('');
    try {
      const response = await fetch('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: 'Choose the folder to tidy' })
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok || !result.success)
        throw new Error(result.error || 'Folder picker unavailable');
      if (result.selected && result.path) {
        const input = document.getElementById('downloadsJanitorPath');
        if (input) input.value = result.path;
      }
    } catch (error) {
      showError(error.message || 'Could not open the folder picker.');
    }
  }

  async function confirmSetup(path) {
    const id = wsId();
    if (!id || busy) return;
    const trimmed = String(path || '').trim();
    if (!trimmed) {
      showError('Choose a folder to tidy.');
      return;
    }
    busy = true;
    showError('');
    const confirm = document.getElementById('downloadsJanitorConfirm');
    if (confirm) confirm.disabled = true;
    try {
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/downloads-janitor/setup',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: trimmed })
        }
      );
      const result = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = result.error || result;
        throw new Error((apiError && apiError.message) || 'Ori could not set up this folder.');
      }
      render(result.status);
    } catch (error) {
      if (confirm) confirm.disabled = false;
      showError(error.message || 'Ori could not set up this folder.');
    } finally {
      busy = false;
    }
  }

  // scanNow runs a real scan. It reports honestly when a scan finds nothing:
  // silence would be indistinguishable from a broken button.
  async function scanNow() {
    const id = wsId();
    if (!id || busy) return;
    busy = true;
    showError('');
    const control = document.getElementById('downloadsJanitorScan');
    if (control) {
      control.disabled = true;
      control.textContent = 'Scanning…';
    }
    try {
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/downloads-janitor/scan',
        { method: 'POST', headers: { 'Content-Type': 'application/json' } }
      );
      const result = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = result.error || result;
        throw new Error((apiError && apiError.message) || 'The scan could not run.');
      }
      await loadBatch();
      if (!result.created) {
        showError(
          'Nothing new to review — every file here has already been proposed or dismissed.'
        );
      }
    } catch (error) {
      showError(error.message || 'The scan could not run.');
    } finally {
      busy = false;
      const done = document.getElementById('downloadsJanitorScan');
      if (done) {
        done.disabled = false;
        done.textContent = 'Scan now';
      }
    }
  }

  // submitDecisions records review decisions server-side and repaints from the
  // response, so what the page shows is what was actually stored.
  async function submitDecisions(decisions) {
    const id = wsId();
    if (!id || !decisions || decisions.length === 0) return;
    showError('');
    try {
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/downloads-janitor/decisions',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ decisions })
        }
      );
      const result = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = result.error || result;
        throw new Error((apiError && apiError.message) || 'That change could not be saved.');
      }
    } catch (error) {
      showError(error.message || 'That change could not be saved.');
    }
    // Repaint from the server either way, so a rejected change never lingers
    // on screen as though it had been saved.
    await loadBatch();
  }

  async function loadCategories() {
    const id = wsId();
    if (!id || categories.length) return;
    try {
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/downloads-janitor/categories'
      );
      if (!response.ok) return;
      const body = await response.json();
      categories = Array.isArray(body.categories) ? body.categories : [];
    } catch (_) {
      categories = [];
    }
  }

  // loadBatch fetches the newest batch still awaiting the user. Selection is
  // cleared on every load: a stored decision may have changed which rows are
  // actionable, and a stale selection is how the wrong file gets acted on.
  async function loadBatch() {
    const id = wsId();
    if (!id) return;
    try {
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/downloads-janitor/batches/latest'
      );
      if (!response.ok) throw new Error('batch failed');
      const body = await response.json();
      lastBatch = body.batch || null;
      lastCandidates = Array.isArray(body.candidates) ? body.candidates : [];
    } catch (_) {
      lastBatch = null;
      lastCandidates = [];
    }
    selected = new Set();
    renderBatch();
    refreshStats();
  }

  async function refresh() {
    const id = wsId();
    if (!id) return;
    try {
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/downloads-janitor'
      );
      if (!response.ok) throw new Error('status failed');
      const body = await response.json();
      const status = body.status;
      render(status);
      if (status && status.applies && status.settings && status.settings.root_path) {
        await loadCategories();
        await loadBatch();
      }
      return;
    } catch (_) {
      // A workspace that is not a Downloads Janitor workspace, or a server that
      // has not wired the feature, simply shows nothing here.
      const host = mount();
      if (host && !lastStatus) host.hidden = true;
    }
  }

  function init(id) {
    workspaceId = id || wsId();
    if (!mount()) return;
    void refresh();
  }

  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', () => init(), { once: true });
    } else {
      init();
    }
  }

  window.DownloadsJanitorPanel = {
    init,
    refresh,
    render,
    renderBatch,
    _confirmSetup: confirmSetup,
    _setBatch: (batch, candidates, cats) => {
      lastBatch = batch;
      lastCandidates = candidates || [];
      if (cats) categories = cats;
      selected = new Set();
      filter = '';
    },
    _selected: () => Array.from(selected)
  };
})();
