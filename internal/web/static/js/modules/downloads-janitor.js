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
  // The approval issued by a preview, held only until the user confirms or
  // cancels. It is never persisted: an abandoned approval simply expires.
  let pendingPreview = null;
  // Files the user has explicitly marked for Trash. Kept apart from `selected`
  // so a move selection and a removal can never be confused for one another.
  let trashMarked = new Set();
  let historyActions = [];
  let historyLoaded = false;
  let historyFilter = '';
  // The last undo outcome, kept so the history reload that follows an undo does
  // not wipe the explanation the user needs to read.
  let historyStatusMessage = '';

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
    checkbox.disabled = checkbox.disabled || trashMarked.has(candidate.id);
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
      // Trash is a per-file choice with its own toggle. It is never part of the
      // move selection, so a bulk selection cannot become a removal (FR-66).
      const marked = trashMarked.has(candidate.id);
      const trashToggle = button(
        marked ? 'Trash ✓' : 'Trash',
        'dj-btn dj-btn-quiet' + (marked ? ' dj-btn-destructive' : ''),
        () => {
          if (trashMarked.has(candidate.id)) trashMarked.delete(candidate.id);
          else {
            trashMarked.add(candidate.id);
            // Marking for Trash takes the file out of the move selection:
            // one file, one decision.
            selected.delete(candidate.id);
          }
          renderBatch();
        }
      );
      trashToggle.setAttribute('aria-pressed', marked ? 'true' : 'false');
      trashToggle.setAttribute(
        'aria-label',
        (marked ? 'Unmark' : 'Mark') + ' ' + candidate.name + ' for Trash'
      );
      actionCell.appendChild(trashToggle);
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
    if (node) {
      const count = selected.size;
      const trashes = trashMarked.size;
      const parts = [];
      if (count) parts.push(count + (count === 1 ? ' file selected' : ' files selected'));
      if (trashes)
        parts.push(
          trashes + (trashes === 1 ? ' file marked for Trash' : ' files marked for Trash')
        );
      node.textContent = parts.length === 0 ? 'No files selected.' : parts.join(' · ') + '.';
    }
    const approve = document.getElementById('downloadsJanitorApprove');
    if (approve) {
      const moves = selected.size;
      const trashes = trashMarked.size;
      // The control states exactly what it will attempt — moves and removals
      // counted separately — and stays disabled until there is something valid
      // to attempt (FR-69).
      const parts = [];
      if (moves) parts.push(moves + (moves === 1 ? ' move' : ' moves'));
      if (trashes) parts.push(trashes + ' to Trash');
      approve.textContent =
        parts.length === 0 ? 'Approve selected' : 'Approve ' + parts.join(' and ');
      approve.disabled = moves + trashes === 0;
      approve.setAttribute('aria-disabled', approve.disabled ? 'true' : 'false');
    }
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

    const footer = el('div', 'dj-footer');
    const selection = el('p', 'dj-selection');
    selection.id = 'downloadsJanitorSelection';
    selection.setAttribute('role', 'status');
    selection.setAttribute('aria-live', 'polite');
    footer.appendChild(selection);

    const approve = button(
      'Approve selected moves',
      'dj-btn dj-btn-primary',
      () => void startApproval()
    );
    approve.id = 'downloadsJanitorApprove';
    footer.appendChild(approve);
    container.appendChild(footer);

    updateSelectionSummary();
  }

  // ------------------------------------------------------- confirm and apply

  // renderConfirmation shows the final, server-derived plan: the exact
  // destination each file will get, including any rename forced by a name
  // already in use. This is the last thing the user sees before anything moves.
  function renderConfirmation(previewResult) {
    const host = document.getElementById('downloadsJanitorConfirmHost');
    if (!host) return;
    clear(host);

    const panel = el('section', 'dj-confirm');
    panel.setAttribute('role', 'group');
    panel.setAttribute('aria-labelledby', 'downloadsJanitorConfirmTitle');

    const title = el('h3', 'dj-confirm-title', 'Confirm these moves');
    title.id = 'downloadsJanitorConfirmTitle';
    panel.appendChild(title);

    const moveCount = previewResult.move_count || 0;
    const trashCount = previewResult.trash_count || 0;
    const sentences = [];
    if (moveCount) {
      sentences.push(
        moveCount === 1 ? 'Ori will move 1 file.' : 'Ori will move ' + moveCount + ' files.'
      );
    }
    if (trashCount) {
      sentences.push(
        (trashCount === 1
          ? 'Ori will move 1 file to your system Trash'
          : 'Ori will move ' + trashCount + ' files to your system Trash') +
          ', where you can restore them. Nothing is deleted permanently.'
      );
    } else {
      sentences.push('Nothing is deleted.');
    }
    const lead = el('p', 'dj-confirm-lead', sentences.join(' '));
    lead.setAttribute('role', 'status');
    panel.appendChild(lead);

    const list = el('ul', 'dj-confirm-list');
    (previewResult.items || []).forEach(item => {
      const isTrash = item.operation === 'trash';
      const entry = el('li', 'dj-confirm-item' + (isTrash ? ' dj-confirm-trash' : ''));
      entry.appendChild(el('span', 'dj-confirm-name', item.name));
      entry.appendChild(el('span', 'dj-confirm-arrow', ' → '));
      entry.appendChild(
        el('span', 'dj-confirm-destination', isTrash ? 'Trash (restorable)' : item.destination)
      );
      if (item.renamed) {
        // Ori never overwrites, so a taken name means a new one. Saying so here
        // is the difference between a surprise and an informed choice.
        const note = el(
          'span',
          'dj-confirm-renamed',
          ' (renamed — a file with that name is already there)'
        );
        entry.appendChild(note);
      }
      list.appendChild(entry);
    });
    panel.appendChild(list);

    const actions = el('div', 'dj-actions');
    const confirm = button(
      confirmLabel(previewResult),
      'dj-btn dj-btn-primary' + (trashCount > 0 ? ' dj-btn-destructive' : ''),
      () => void applyApproval(previewResult)
    );
    confirm.id = 'downloadsJanitorConfirmApply';
    // A batch containing any removal needs a second, explicit acknowledgement
    // stating the exact number of files going to Trash. Moves alone do not:
    // reserving the extra step for the destructive case is what keeps it
    // meaningful rather than something to click through (FR-70).
    if (trashCount > 0) {
      confirm.disabled = true;
      const ack = el('label', 'dj-trash-ack');
      const box = document.createElement('input');
      box.type = 'checkbox';
      box.id = 'downloadsJanitorTrashAck';
      box.addEventListener('change', () => {
        confirm.disabled = !box.checked;
      });
      ack.appendChild(box);
      ack.appendChild(
        el(
          'span',
          'dj-trash-ack-text',
          trashCount === 1
            ? 'Yes, move 1 file to the Trash.'
            : 'Yes, move ' + trashCount + ' files to the Trash.'
        )
      );
      panel.appendChild(ack);
    }
    actions.appendChild(confirm);
    actions.appendChild(
      button('Cancel', 'dj-btn dj-btn-secondary', () => {
        // Cancelling abandons the approval; the decisions themselves are still
        // recorded, so nothing the user chose is lost.
        pendingPreview = null;
        clear(host);
      })
    );
    panel.appendChild(actions);
    host.appendChild(panel);
    confirm.focus?.();
  }

  // confirmLabel names both halves of a mixed batch so the button never
  // understates what it is about to do.
  function confirmLabel(previewResult) {
    const moves = previewResult.move_count || 0;
    const trashes = previewResult.trash_count || 0;
    const parts = [];
    if (moves) parts.push(moves === 1 ? 'Move 1 file' : 'Move ' + moves + ' files');
    if (trashes) parts.push(trashes === 1 ? 'Trash 1 file' : 'Trash ' + trashes + ' files');
    return parts.length === 0 ? 'Apply' : parts.join(' and ');
  }

  // ------------------------------------------------------------------ history

  const HISTORY_FILTERS = [
    { id: '', label: 'All' },
    { id: 'move', label: 'Filed', query: 'operation=move' },
    { id: 'trash', label: 'Trashed', query: 'operation=trash' },
    { id: 'undoable', label: 'Can undo', query: 'undoable=true' }
  ];

  function historyLine(action) {
    const entry = el('li', 'dj-history-item dj-history-' + (action.result || 'failed'));

    const mark = el('span', 'dj-results-mark', RESULT_MARKS[action.result] || '!');
    mark.setAttribute('aria-hidden', 'true');
    entry.appendChild(mark);
    entry.appendChild(el('span', 'dj-history-name', action.source_name || ''));

    const what =
      action.operation === 'trash'
        ? action.result === 'applied'
          ? ' — moved to Trash'
          : ' — not removed'
        : action.result === 'applied'
          ? ' — filed to ' + (action.destination_relative || '')
          : ' — not moved';
    entry.appendChild(el('span', 'dj-history-what', what));

    if (action.undo === 'undone') {
      entry.appendChild(el('span', 'dj-history-undone', ' · put back'));
    }
    if (action.error_summary) {
      entry.appendChild(el('span', 'dj-history-message', ' ' + action.error_summary));
    }
    if (action.undo_error) {
      entry.appendChild(el('span', 'dj-history-message', ' ' + action.undo_error));
    }

    // Undo is offered only where the server says it is still possible, and the
    // label names the actual reversal rather than a generic "undo".
    if (action.result === 'applied' && action.undo === 'available') {
      entry.appendChild(
        button(
          action.operation === 'trash' ? 'Restore from Trash' : 'Undo move',
          'dj-btn dj-btn-quiet',
          () => void undoAction(action.id)
        )
      );
    } else if (action.result === 'applied' && action.undo !== 'undone') {
      // Saying why it cannot be undone is more useful than hiding the control.
      entry.appendChild(el('span', 'dj-muted', ' Cannot be undone'));
    }
    return entry;
  }

  function renderHistory() {
    const host = document.getElementById('downloadsJanitorHistoryHost');
    if (!host) return;
    clear(host);
    if (!historyLoaded) return;

    const panel = el('section', 'dj-history');
    panel.setAttribute('aria-labelledby', 'downloadsJanitorHistoryTitle');
    const title = el('h3', 'dj-history-title', 'History');
    title.id = 'downloadsJanitorHistoryTitle';
    panel.appendChild(title);

    const bar = el('div', 'dj-filters');
    bar.setAttribute('role', 'group');
    bar.setAttribute('aria-label', 'Filter history');
    HISTORY_FILTERS.forEach(option => {
      const control = button(
        option.label,
        'dj-filter' + (historyFilter === option.id ? ' dj-filter-active' : ''),
        () => {
          historyFilter = option.id;
          void loadHistory();
        }
      );
      control.setAttribute('aria-pressed', historyFilter === option.id ? 'true' : 'false');
      bar.appendChild(control);
    });
    panel.appendChild(bar);

    if (historyActions.length === 0) {
      panel.appendChild(
        el(
          'p',
          'dj-empty-copy',
          'Nothing here yet. Applied moves and Trash actions are listed here.'
        )
      );
    } else {
      const list = el('ul', 'dj-history-list');
      historyActions.forEach(action => list.appendChild(historyLine(action)));
      panel.appendChild(list);
    }

    const status = el('p', 'dj-history-status', historyStatusMessage);
    status.id = 'downloadsJanitorHistoryStatus';
    status.setAttribute('role', 'status');
    status.setAttribute('aria-live', 'polite');
    panel.appendChild(status);

    host.appendChild(panel);
  }

  async function loadHistory() {
    const id = wsId();
    if (!id) return;
    const option = HISTORY_FILTERS.find(entry => entry.id === historyFilter);
    const query = option && option.query ? '?' + option.query : '';
    try {
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/downloads-janitor/history' + query
      );
      if (!response.ok) throw new Error('history failed');
      const body = await response.json();
      historyActions = Array.isArray(body.actions) ? body.actions : [];
      historyLoaded = true;
    } catch (_) {
      historyActions = [];
      historyLoaded = true;
    }
    renderHistory();
  }

  // undoAction reverses one applied action and reports the outcome honestly: a
  // refusal is a normal answer, and the reason is what the user needs.
  async function undoAction(actionID) {
    const id = wsId();
    if (!id || busy) return;
    busy = true;
    historyStatusMessage = 'Putting it back…';
    const status = document.getElementById('downloadsJanitorHistoryStatus');
    if (status) status.textContent = historyStatusMessage;
    try {
      const response = await fetch(
        '/api/workspaces/' +
          encodeURIComponent(id) +
          '/downloads-janitor/history/' +
          encodeURIComponent(actionID) +
          '/undo',
        { method: 'POST', headers: { 'Content-Type': 'application/json' } }
      );
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = body.error || body;
        throw new Error((apiError && apiError.message) || 'Ori could not undo that.');
      }
      const undo = body.undo || {};
      historyStatusMessage =
        undo.result === 'undone'
          ? (undo.message || 'Put back.') + ' ' + (undo.restored_to || '')
          : undo.message || 'Ori could not undo that.';
    } catch (error) {
      historyStatusMessage = error.message || 'Ori could not undo that.';
    } finally {
      busy = false;
      await loadHistory();
      await loadBatch();
    }
  }

  const RESULT_LABELS = {
    applied: 'Filed',
    failed: 'Not moved',
    stale: 'Changed — not moved'
  };

  const RESULT_MARKS = { applied: '✓', failed: '!', stale: '•' };

  // renderResults reports what happened per file. A batch where some files
  // moved and others did not is stated as exactly that — never summarized as
  // success (FR-72).
  function renderResults(result) {
    const host = document.getElementById('downloadsJanitorConfirmHost');
    if (!host) return;
    clear(host);

    const panel = el('section', 'dj-results');
    panel.setAttribute('role', 'status');
    panel.setAttribute('aria-live', 'polite');

    const parts = [];
    if (result.applied)
      parts.push(result.applied + (result.applied === 1 ? ' file filed' : ' files filed'));
    if (result.failed) parts.push(result.failed + ' could not be moved');
    if (result.stale) parts.push(result.stale + ' changed since you approved');
    if (parts.length === 0) parts.push('Nothing was moved');
    panel.appendChild(el('p', 'dj-results-summary', parts.join(' · ') + '.'));

    const list = el('ul', 'dj-results-list');
    (result.outcomes || []).forEach(outcome => {
      const entry = el('li', 'dj-results-item dj-results-' + (outcome.result || 'failed'));
      const mark = el('span', 'dj-results-mark', RESULT_MARKS[outcome.result] || '!');
      mark.setAttribute('aria-hidden', 'true');
      entry.appendChild(mark);
      entry.appendChild(el('span', 'dj-results-name', outcome.name));
      entry.appendChild(
        el('span', 'dj-results-state', ' — ' + (RESULT_LABELS[outcome.result] || outcome.result))
      );
      if (outcome.destination && outcome.result === 'applied') {
        entry.appendChild(el('span', 'dj-results-destination', ' → ' + outcome.destination));
      }
      if (outcome.message) {
        entry.appendChild(el('span', 'dj-results-message', ' ' + outcome.message));
      }
      list.appendChild(entry);
    });
    panel.appendChild(list);

    if (result.stale) {
      panel.appendChild(button('Scan again', 'dj-btn dj-btn-secondary', () => void scanNow()));
    }
    host.appendChild(panel);
  }

  // startApproval asks the server for the final plan and shows it for
  // confirmation. Nothing moves at this step.
  async function startApproval() {
    const id = wsId();
    if (!id || busy || selected.size + trashMarked.size === 0) return;
    busy = true;
    showError('');
    const control = document.getElementById('downloadsJanitorApprove');
    if (control) control.disabled = true;
    try {
      const decisions = Array.from(selected).map(candidateId => {
        const candidate = lastCandidates.find(item => item.id === candidateId) || {};
        return {
          candidate_id: candidateId,
          operation: 'move',
          category: candidate.decision_category || candidate.category || ''
        };
      });
      trashMarked.forEach(candidateId => {
        decisions.push({ candidate_id: candidateId, operation: 'trash', category: '' });
      });
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/downloads-janitor/preview',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ decisions })
        }
      );
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = body.error || body;
        throw new Error((apiError && apiError.message) || 'Ori could not prepare these moves.');
      }
      pendingPreview = { preview: body.preview, decisions };
      renderConfirmation(body.preview || {});
    } catch (error) {
      showError(error.message || 'Ori could not prepare these moves.');
    } finally {
      busy = false;
      updateSelectionSummary();
    }
  }

  // applyApproval spends the approval and reports the per-item outcome.
  async function applyApproval() {
    const id = wsId();
    if (!id || busy || !pendingPreview) return;
    busy = true;
    showError('');
    const control = document.getElementById('downloadsJanitorConfirmApply');
    if (control) {
      control.disabled = true;
      control.textContent = 'Moving…';
    }
    try {
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/downloads-janitor/apply',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            batch_id: pendingPreview.preview.batch_id,
            approval_token: pendingPreview.preview.token,
            decisions: pendingPreview.decisions
          })
        }
      );
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = body.error || body;
        throw new Error((apiError && apiError.message) || 'Ori could not apply these moves.');
      }
      const result = body.result || {};
      pendingPreview = null;
      // Reload first so the table reflects the new states, then report the
      // outcome beneath it. History gains the entries this apply just wrote.
      await loadBatch();
      renderResults(result);
      await loadHistory();
    } catch (error) {
      pendingPreview = null;
      showError(error.message || 'Ori could not apply these moves.');
      await loadBatch();
    } finally {
      busy = false;
    }
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

    // The confirmation step and the per-item results render below the batch,
    // deliberately *outside* it: applying every file empties the batch, and the
    // report of what just happened to the user's files must not be swept away
    // with the table it came from.
    const confirmHost = el('div', 'dj-confirm-host');
    confirmHost.id = 'downloadsJanitorConfirmHost';
    card.appendChild(confirmHost);

    const historyHost = el('div', 'dj-history-host');
    historyHost.id = 'downloadsJanitorHistoryHost';
    card.appendChild(historyHost);

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
    // Marks and selections belong to the batch that was on screen. A repaint
    // from a fresh status must not carry a removal mark into a different set
    // of files.
    trashMarked = new Set();
    selected = new Set();
    pendingPreview = null;
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
    trashMarked = new Set();
    pendingPreview = null;
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
        await loadHistory();
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
      trashMarked = new Set();
      pendingPreview = null;
      filter = '';
    },
    _selected: () => Array.from(selected),
    _setHistory: actions => {
      historyActions = actions || [];
      historyLoaded = true;
      renderHistory();
    },
    _select: id => {
      selected.add(id);
      updateSelectionSummary();
    }
  };
})();
