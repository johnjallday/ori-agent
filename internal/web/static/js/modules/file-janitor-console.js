// downloads-janitor.js — the Downloads Janitor panel on the workspace detail
// page.
//
// It renders two states from one server response
// (GET /api/workspaces/{id}/file-janitor):
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

  // Set by renderSetupCard so the folder picker can re-enable the confirm
  // button after writing a path straight into the input.
  let syncSetupConfirmEnabled = null;

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
  // Active state filter: '' (all), 'needs_review', 'pending', 'skipped'. The
  // server owns the filtering; this is only what to ask it for.
  let filter = '';
  // Server-side paging. A batch can hold hundreds of files, and rendering one
  // DOM row per candidate is what makes a large review unusable — so the page
  // is bounded and only the page is requested (FR-109, FR-150).
  const PAGE_SIZE = 50;
  let pageOffset = 0;
  // Counts for the WHOLE batch, from the server. The filter labels and the
  // "showing X of Y" line come from these rather than from the rows on screen,
  // which are only ever one page of them.
  let batchTotal = 0;
  let filteredTotal = 0;
  let filterCounts = {};
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
  // True while a scan the user started is in flight, so the header can say so.
  let scanning = false;
  let settingsOpen = false;
  let settingsMessage = '';
  // Revoking asks once before acting: it is the one control that takes
  // something away the user cannot get back with a click.
  let revokeConfirmed = false;

  // The workspace id is read from the URL rather than taken solely from
  // window.currentWorkspaceId.
  //
  // The page sets that global inside a <script type="module">, and this file is
  // loaded as a classic `defer` script. Deferred classic scripts run BEFORE
  // module scripts, so at init() time the global does not exist yet: wsId()
  // returned '', refresh() bailed on its first line, and the mount stayed
  // hidden. The panel never rendered in a browser at all, while every API call
  // it would have made worked perfectly — which is exactly why this survived
  // API-level verification.
  //
  // The path is the same source the page itself derives the global from
  // (/workspaces/{id}), so this cannot disagree with it.
  function wsId() {
    if (workspaceId) return workspaceId;
    if (typeof window === 'undefined') return '';
    if (window.currentWorkspaceId) return window.currentWorkspaceId;
    return workspaceIdFromPath();
  }

  function workspaceIdFromPath() {
    const path = (window.location && window.location.pathname) || '';
    const parts = path.split('/').filter(Boolean);
    return parts[0] === 'workspaces' && parts[1] ? decodeURIComponent(parts[1]) : '';
  }

  // API_PREFIX is the canonical File Janitor route segment. Every request this
  // module makes goes through apiBase, so the prefix is stated once.
  //
  // The server still serves the legacy `downloads-janitor` prefix, and will for
  // this whole release — persisted deep links and any out-of-repo caller still
  // use it, and Go route-parity tests cover it (see
  // internal/downloadsjanitorhttp/route_parity_test.go). In-repo callers use the
  // canonical prefix so the legacy alias can eventually be retired by deleting
  // it rather than by hunting for stragglers (FR-132).
  const API_PREFIX = 'file-janitor';

  function apiBase(id) {
    return '/api/workspaces/' + encodeURIComponent(id) + '/' + API_PREFIX;
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

  // Filenames are untrusted input, and this is the last point before they reach
  // a screen or a screen reader. Control characters forge line breaks and
  // bidirectional overrides disguise an extension: "invoice<RLO>gpj.exe"
  // renders as "invoice exe.jpg". The server sends a rendered-safe
  // display_name for candidates (downloadsjanitor.DisplayFileName), but history
  // and journal entries carry the on-disk name by necessity, so the same rule
  // is applied here rather than trusting the field to have been cleaned
  // upstream. textContent already prevents markup; this is about what the
  // characters *look* like once rendered.
  const BIDI_CONTROLS = /[\u200e\u200f\u202a-\u202e\u2066-\u2069]/g;
  // eslint-disable-next-line no-control-regex
  const CONTROL_CHARS = /[\u0000-\u001f\u007f-\u009f]/g;
  const MAX_DISPLAY_NAME = 180;

  function safeName(value, fallback) {
    const raw = typeof value === 'string' ? value : '';
    const cleaned = raw.replace(CONTROL_CHARS, '').replace(BIDI_CONTROLS, '').trim();
    if (!cleaned) return fallback || '(unreadable name)';
    return Array.from(cleaned).length > MAX_DISPLAY_NAME
      ? Array.from(cleaned).slice(0, MAX_DISPLAY_NAME).join('') + '\u2026'
      : cleaned;
  }

  // displayName prefers the name the server already rendered safe, and cleans
  // whatever it is given either way.
  function displayName(item) {
    if (!item) return '(unreadable name)';
    return safeName(item.display_name || item.name || item.source_name);
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

  // activityState answers the question the user is actually asking of a
  // workspace header: is this thing doing anything, and does it need me?
  //
  // Readiness alone cannot answer it — a workspace can be perfectly configured
  // and still have twelve files waiting, or be perfectly configured and paused.
  // The order below is the order of urgency: a problem first, then work waiting
  // for the user, then what Ori is doing on its own.
  function activityState(status) {
    const readiness = (status && status.readiness) || {};
    const settings = (status && status.settings) || {};
    if (readiness.state === 'setup_required') {
      return { id: 'setup_required', label: 'Setup required' };
    }
    if (readiness.state === 'needs_attention') {
      return { id: 'needs_attention', label: 'Needs attention' };
    }
    if (scanning) {
      return { id: 'scanning', label: 'Scanning…' };
    }
    const pending = (lastBatch && lastBatch.summary && lastBatch.summary.proposed) || 0;
    if (pending > 0) {
      return { id: 'review_ready', label: 'Review ready' };
    }
    if (settings.paused) {
      return { id: 'paused', label: 'Paused' };
    }
    return { id: 'watching', label: 'Watching' };
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
    const label = suggestion.label || 'Folder to tidy';
    // Only a preset may pre-fill a path. A generic in-place install starts
    // empty rather than proposing ~/Downloads: pre-filling a real folder the
    // user never chose, next to a button that grants access to it, turns an
    // explicit approval into a default (FR-44, FR-50).
    const suggestedPath = suggestion.suggested_path || '';

    const card = el('section', 'dj-card');
    card.setAttribute('role', 'group');
    card.setAttribute('aria-labelledby', 'downloadsJanitorTitle');

    const head = el('div', 'dj-head');
    const heading = el('div', 'dj-heading');
    const title = el('h2', 'dj-title', 'File Janitor');
    title.id = 'downloadsJanitorTitle';
    heading.appendChild(title);
    heading.appendChild(
      el('p', 'dj-sub', 'Choose the folder to tidy. Nothing is scanned or moved until you do.')
    );
    heading.appendChild(
      el(
        'p',
        'dj-sub dj-sub-muted',
        'Best for an inbox-style folder whose loose files pile up — Downloads, Desktop, Scans, an upload drop. ' +
          'Ori looks only at the files sitting directly in it, and never reorganizes folders inside it.'
      )
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
    input.setAttribute('placeholder', 'Choose a folder, or type its path');
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
    // The grant needs a folder AND this explicit press. Browsing or typing a
    // path selects nothing on its own (FR-50), and with no pre-filled path
    // there is nothing to approve until the user supplies one.
    const syncConfirmEnabled = () => {
      confirm.disabled = String(input.value || '').trim() === '';
    };
    syncConfirmEnabled();
    input.addEventListener('input', syncConfirmEnabled);
    // The folder picker writes input.value directly, which fires no input
    // event, so browse() re-syncs through this rather than leaving the button
    // disabled after a successful pick.
    syncSetupConfirmEnabled = syncConfirmEnabled;
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
    select.setAttribute('aria-label', 'Category for ' + displayName(candidate));
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
    // A deep link naming this candidate marks it, so the row a notification
    // was about is findable in a batch of hundreds. The id is registered so
    // focusRequestedItem can reach it without a selector engine.
    if (consoleItem && candidate.id === consoleItem) {
      row.className += ' is-linked';
      row.id = 'fileJanitorLinkedRow';
      row.setAttribute('tabindex', '-1');
    }

    const selectCell = el('td', 'dj-cell dj-cell-select');
    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.className = 'dj-select';
    checkbox.checked = selected.has(candidate.id);
    checkbox.setAttribute('aria-label', 'Select ' + displayName(candidate));
    checkbox.disabled = candidate.state !== 'pending' && candidate.state !== 'approved';
    checkbox.disabled = checkbox.disabled || trashMarked.has(candidate.id);
    // A row Ori could not place confidently cannot be filed until the user
    // says where it goes. Leaving it selectable meant "Needs review" was
    // decoration: the row rode along in a bulk approval and was filed using
    // the very guess that was flagged as untrustworthy (FR-63, FR-64).
    if (unresolvedNeedsReview(candidate)) {
      checkbox.disabled = true;
      checkbox.setAttribute(
        'title',
        'Choose a category for this file first — Ori was not confident enough to guess.'
      );
    }
    checkbox.addEventListener('change', () => {
      if (checkbox.checked) selected.add(candidate.id);
      else selected.delete(candidate.id);
      updateSelectionSummary();
    });
    selectCell.appendChild(checkbox);
    row.appendChild(selectCell);

    const nameCell = el('td', 'dj-cell dj-cell-name');
    nameCell.appendChild(el('span', 'dj-name', displayName(candidate)));
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
        (marked ? 'Unmark' : 'Mark') + ' ' + displayName(candidate) + ' for Trash'
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

  // The page the server returned, already filtered. Filtering here as well
  // would make the row count disagree with the counts beside the filters.
  function visibleCandidates() {
    return lastCandidates;
  }

  // unresolvedNeedsReview reports a low-confidence row the user has not yet
  // ruled on. Skipping or marking for Trash also resolves it; those are handled
  // by the state and trash checks beside this one.
  function unresolvedNeedsReview(candidate) {
    return Boolean(candidate && candidate.needs_review && !candidate.decision_category);
  }

  // dropIneligibleSelections removes IDs that can no longer be acted on.
  //
  // Selection is deliberately kept across page changes and refreshes — a user
  // reviewing 300 files should not lose their work by turning a page (FR-109).
  // But a file that has since been filed, skipped, or gone stale must not stay
  // silently selected: it would ride along into the next approval, and the user
  // would be approving something they can no longer see. Only candidates ON
  // THIS PAGE can be judged; ones on other pages are left alone and revalidated
  // by the server at approval time (FR-82).
  function dropIneligibleSelections() {
    lastCandidates.forEach(candidate => {
      const eligible = candidate.state === 'pending' || candidate.state === 'approved';
      if (!eligible || unresolvedNeedsReview(candidate)) {
        selected.delete(candidate.id);
        trashMarked.delete(candidate.id);
      }
    });
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
      const count = filterCounts[option.id || 'all'];
      const control = button(
        option.label,
        'dj-filter' + (filter === option.id ? ' dj-filter-active' : ''),
        () => selectFilter(option.id)
      );
      // The count comes from the server's whole-batch tally, not from the rows
      // on screen — those are one page of them.
      if (typeof count === 'number') {
        control.appendChild(el('span', 'dj-filter-count', String(count)));
        control.setAttribute('aria-label', option.label + ', ' + count + ' files');
      }
      control.setAttribute('aria-pressed', filter === option.id ? 'true' : 'false');
      bar.appendChild(control);
    });
    return bar;
  }

  function selectFilter(next) {
    if (filter === next) return;
    filter = next;
    // A new filter is a new list; staying on page 4 of the old one would land
    // the user somewhere arbitrary, or on nothing at all.
    pageOffset = 0;
    void loadBatch();
  }

  // pager renders the page controls, or nothing when the whole filtered set
  // fits on one page. It states the range and the total in words, so "showing
  // 51-100 of 500" is available to a screen reader as one sentence rather than
  // as three numbers scattered across controls.
  function pager() {
    if (filteredTotal <= lastCandidates.length && pageOffset === 0) return null;
    const nav = el('div', 'dj-pager');
    nav.setAttribute('role', 'navigation');
    nav.setAttribute('aria-label', 'Review pages');

    const from = filteredTotal === 0 ? 0 : pageOffset + 1;
    const to = pageOffset + lastCandidates.length;
    // When a filter is on, the batch total is stated too, so the user can see
    // how much they have filtered out rather than believing the batch is
    // smaller than it is.
    const filtered =
      filter && batchTotal > filteredTotal ? ' (filtered from ' + batchTotal + ')' : '';
    const status = el(
      'p',
      'dj-pager-status',
      'Showing ' + from + '\u2013' + to + ' of ' + filteredTotal + ' files' + filtered
    );
    status.id = 'downloadsJanitorPagerStatus';
    status.setAttribute('role', 'status');
    status.setAttribute('aria-live', 'polite');

    const previous = button('Previous', 'dj-btn dj-btn-secondary', () =>
      goToPage(pageOffset - PAGE_SIZE)
    );
    previous.id = 'downloadsJanitorPagePrev';
    previous.disabled = pageOffset <= 0;
    const next = button('Next', 'dj-btn dj-btn-secondary', () => goToPage(pageOffset + PAGE_SIZE));
    next.id = 'downloadsJanitorPageNext';
    next.disabled = to >= filteredTotal;

    nav.appendChild(previous);
    nav.appendChild(status);
    nav.appendChild(next);
    return nav;
  }

  function goToPage(offset) {
    const next = Math.max(0, Math.min(offset, Math.max(0, filteredTotal - 1)));
    if (next === pageOffset) return;
    pageOffset = next;
    void loadBatch();
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
      // Point the disabled control at the line that says why ("No files
      // selected."), so the reason is available to a screen reader reading the
      // button rather than only to someone who happens to look above it.
      approve.setAttribute('aria-describedby', 'downloadsJanitorSelection');
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
    const pageControls = pager();
    if (pageControls) container.appendChild(pageControls);

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
  // approval is { preview, decisions } — the same object held in
  // pendingPreview. It is passed through rather than re-read from module state
  // when the button is pressed, so nothing that happens between rendering the
  // confirmation and the user clicking it can quietly empty the button.
  function renderConfirmation(approval) {
    const previewResult = (approval && approval.preview) || {};
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
      entry.appendChild(el('span', 'dj-confirm-name', displayName(item)));
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
      () => void applyApproval(approval)
    );
    confirm.id = 'downloadsJanitorConfirmApply';
    // A batch containing any removal needs a second, explicit acknowledgement
    // stating the exact number of files going to Trash. Moves alone do not:
    // reserving the extra step for the destructive case is what keeps it
    // meaningful rather than something to click through (FR-70).
    let initialFocus = confirm;
    if (trashCount > 0) {
      confirm.disabled = true;
      // A disabled control cannot be focused, so focusing it would drop focus
      // to the document body — on the destructive path, where losing your place
      // matters most. Focus goes to the acknowledgement instead, which is the
      // thing the user has to act on next anyway.
      const ack = el('label', 'dj-trash-ack');
      const box = document.createElement('input');
      box.type = 'checkbox';
      box.id = 'downloadsJanitorTrashAck';
      // Say why the button is unavailable rather than leaving it inert and
      // unexplained.
      confirm.setAttribute('aria-describedby', 'downloadsJanitorTrashAckText');
      box.addEventListener('change', () => {
        confirm.disabled = !box.checked;
        // Ticking the box is the moment the action becomes available, so hand
        // the user straight to it instead of making them tab back.
        if (box.checked) confirm.focus?.();
      });
      initialFocus = box;
      ack.appendChild(box);
      const ackText = el(
        'span',
        'dj-trash-ack-text',
        trashCount === 1
          ? 'Yes, move 1 file to the Trash.'
          : 'Yes, move ' + trashCount + ' files to the Trash.'
      );
      ackText.id = 'downloadsJanitorTrashAckText';
      ack.appendChild(ackText);
      panel.appendChild(ack);
    }
    actions.appendChild(confirm);
    actions.appendChild(
      button('Cancel', 'dj-btn dj-btn-secondary', () => {
        // Cancelling abandons the approval; the decisions themselves are still
        // recorded, so nothing the user chose is lost.
        pendingPreview = null;
        clear(host);
        // Destroying the panel would otherwise drop focus to the body, leaving
        // a keyboard user at the top of the document with no idea where they
        // were. Put them back on the control that opened it.
        restoreFocus();
      })
    );
    panel.appendChild(actions);
    host.appendChild(panel);
    initialFocus.focus?.();
  }

  // focusReturn holds the element that opened the confirmation panel so focus
  // can go back there when the panel closes for any reason.
  let focusReturn = null;

  function rememberFocus() {
    const active = document.activeElement;
    focusReturn = active && active !== document.body ? active : null;
  }

  function restoreFocus() {
    const target = focusReturn;
    focusReturn = null;
    if (!target) return;
    // The opener may have been re-rendered away; fall back to the approve
    // button, which is the equivalent place in the rebuilt surface.
    if (target.isConnected) {
      target.focus?.();
      return;
    }
    document.getElementById('downloadsJanitorApprove')?.focus?.();
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

  // ------------------------------------------------------------- privacy & settings

  // privacyLine states what Ori reads, in the card itself rather than behind a
  // settings link. A user should never have to go looking to find out whether
  // their files are being read.
  function privacyLine(status) {
    const privacy = (status && status.privacy) || {};
    const wrap = el('div', 'dj-privacy');
    wrap.id = 'downloadsJanitorPrivacy';

    const mark = el('span', 'dj-privacy-mark', privacy.leaves_device ? '↗' : '⌂');
    mark.setAttribute('aria-hidden', 'true');
    wrap.appendChild(mark);

    const text = el('span', 'dj-privacy-text');
    text.appendChild(el('span', 'dj-privacy-headline', privacy.headline || ''));
    if (privacy.detail) text.appendChild(el('span', 'dj-privacy-detail', ' ' + privacy.detail));
    wrap.appendChild(text);

    // A configured-but-unconfirmed provider is the one state where something
    // is pending on the user, so it gets the action rather than a note.
    if (privacy.consent_required) {
      wrap.appendChild(
        button(
          'Confirm ' + (privacy.provider || 'provider'),
          'dj-btn dj-btn-quiet',
          () => void grantConsent(privacy.provider)
        )
      );
    }
    return wrap;
  }

  const CONTENT_MODES = [
    {
      id: 'metadata_only',
      label: 'Names and file details only',
      help: 'Ori never opens your files. This is the default.'
    },
    {
      id: 'local_model',
      label: 'Also read a little text, on this device',
      help: 'For files Ori cannot place, it may read the first few kilobytes of plain text documents. Nothing leaves this device.'
    },
    {
      id: 'cloud_model',
      label: 'Also read a little text, using a cloud provider',
      help: 'Short extracts from plain text documents are sent to the provider you configure. Ori asks you to confirm before anything is sent.'
    }
  ];

  function renderSettings() {
    const host = document.getElementById('downloadsJanitorSettingsHost');
    if (!host) return;
    clear(host);
    if (!settingsOpen) return;

    const settings = (lastStatus && lastStatus.settings) || {};
    const panel = el('section', 'dj-settings');
    panel.setAttribute('aria-labelledby', 'downloadsJanitorSettingsTitle');
    const title = el('h3', 'dj-settings-title', 'Settings');
    title.id = 'downloadsJanitorSettingsTitle';
    panel.appendChild(title);

    // Daily catch-up time.
    const timeRow = el('div', 'dj-setting');
    const timeLabel = el('label', 'dj-label', 'Daily catch-up time');
    timeLabel.setAttribute('for', 'downloadsJanitorDailyTime');
    const timeInput = document.createElement('input');
    timeInput.type = 'time';
    timeInput.id = 'downloadsJanitorDailyTime';
    timeInput.className = 'dj-input dj-input-time';
    timeInput.value = settings.daily_scan_local_time || '09:00';
    timeInput.addEventListener('change', () => {
      void saveSettings({ daily_scan_local_time: timeInput.value });
    });
    timeRow.appendChild(timeLabel);
    timeRow.appendChild(timeInput);
    timeRow.appendChild(el('p', 'dj-setting-help', 'Shown in your local time.'));
    panel.appendChild(timeRow);

    // Content inspection, with each option's consequence spelled out.
    const contentRow = el('fieldset', 'dj-setting dj-setting-content');
    const legend = el('legend', 'dj-label', 'What Ori may read');
    contentRow.appendChild(legend);
    const currentMode = settings.content_mode || 'metadata_only';
    CONTENT_MODES.forEach(mode => {
      const option = el('label', 'dj-radio');
      const input = document.createElement('input');
      input.type = 'radio';
      input.name = 'downloadsJanitorContentMode';
      input.value = mode.id;
      input.checked = currentMode === mode.id;
      input.addEventListener('change', () => {
        if (input.checked) void saveSettings({ content_mode: mode.id });
      });
      option.appendChild(input);
      option.appendChild(el('span', 'dj-radio-label', mode.label));
      option.appendChild(el('span', 'dj-radio-help', mode.help));
      contentRow.appendChild(option);
    });
    panel.appendChild(contentRow);

    // How it is working: the folder in use, whether the watcher is running, and
    // every readiness check with its repair. Settings is where a user goes when
    // something seems wrong, so the diagnosis belongs here and not only on the
    // Review tab they may never reach (FR-58, FR-112).
    const health = el('div', 'dj-setting dj-setting-health');
    health.appendChild(el('h4', 'dj-setting-heading', 'How it is working'));
    const folderLine = el('p', 'dj-setting-help');
    folderLine.textContent = settings.root_path
      ? 'Managing ' + safeName(settings.root_path)
      : 'No folder chosen yet.';
    health.appendChild(folderLine);
    health.appendChild(
      el(
        'p',
        'dj-setting-help',
        settings.paused
          ? 'Automatic scanning is paused. You can still scan on demand.'
          : 'Watching this folder, with a daily catch-up.'
      )
    );
    health.appendChild(readinessRows(lastStatus));
    const repair = repairAction(lastStatus);
    if (repair) health.appendChild(repair);
    panel.appendChild(health);

    panel.appendChild(curatorSection());

    // Folder actions. Relink and revoke are grouped away from the rest and
    // labelled by what they do to the user's access, not by verb.
    const actions = el('div', 'dj-settings-actions');
    actions.appendChild(
      button('Run a test scan', 'dj-btn dj-btn-secondary', () => void testScan())
    );
    actions.appendChild(
      button('Reset skipped files', 'dj-btn dj-btn-secondary', () => void resetSkipped())
    );
    actions.appendChild(
      button('Choose a different folder', 'dj-btn dj-btn-secondary', () => void relink())
    );
    actions.appendChild(
      button(
        'Stop using this folder',
        'dj-btn dj-btn-secondary dj-btn-destructive',
        () => void revoke()
      )
    );
    panel.appendChild(actions);
    panel.appendChild(removalSection());

    const status = el('p', 'dj-settings-status', settingsMessage);
    status.id = 'downloadsJanitorSettingsStatus';
    status.setAttribute('role', 'status');
    status.setAttribute('aria-live', 'polite');
    panel.appendChild(status);

    host.appendChild(panel);
  }

  // The removal confirmation, kept in state so the section can render either
  // the entry point or the confirmation itself. It holds the server's summary,
  // never a locally composed description of what removal will do.
  let removalSummary = null;
  let removalConfirming = false;
  let removalCompanionChecked = false;

  // removalSection renders **Remove File Janitor** and, once pressed, the
  // confirmation built from the server's dry run.
  //
  // The confirmation is a step inside Settings rather than a second modal: the
  // console is already the one active dialog, and a dialog on top of it would
  // put the destructive question above the context that explains it (FR-112,
  // FR-119).
  function removalSection() {
    const section = el('div', 'dj-setting dj-setting-removal');
    section.appendChild(el('h4', 'dj-setting-heading', 'Remove File Janitor'));

    if (!removalConfirming) {
      section.appendChild(
        el(
          'p',
          'dj-setting-help',
          'Stops managing this folder and removes File Janitor from this workspace. ' +
            'Your files are not moved or deleted.'
        )
      );
      const start = button(
        'Remove File Janitor',
        'dj-btn dj-btn-secondary dj-btn-destructive',
        () => void beginRemoval()
      );
      start.id = 'downloadsJanitorRemove';
      section.appendChild(start);
      return section;
    }

    const summary = removalSummary || {};
    const confirmation = el('div', 'dj-removal-confirm');
    confirmation.setAttribute('role', 'group');
    confirmation.setAttribute('aria-label', 'Confirm removing File Janitor');

    // Name the folder. A confirmation the user cannot evaluate is not one.
    confirmation.appendChild(
      el(
        'p',
        'dj-removal-lead',
        summary.managed_folder
          ? 'Ori will stop managing ' + safeName(summary.managed_folder) + '.'
          : 'Ori will remove File Janitor from this workspace.'
      )
    );

    const consequences = el('ul', 'dj-disclosure-list');
    (summary.stops_automation || []).forEach(line =>
      consequences.appendChild(el('li', 'dj-disclosure-item', 'Stops: ' + line))
    );
    consequences.appendChild(
      el('li', 'dj-disclosure-item', 'Ori gives up its access to the folder.')
    );
    // The single most important sentence in the dialog.
    consequences.appendChild(
      el(
        'li',
        'dj-disclosure-item',
        'No files are moved, renamed, deleted, or restored. Your folder is left exactly as it is.'
      )
    );
    (summary.retained_audit || []).forEach(line =>
      consequences.appendChild(el('li', 'dj-disclosure-item', 'Kept: ' + line))
    );
    if ((summary.kept_shared || summary.shared || []).length > 0) {
      consequences.appendChild(
        el(
          'li',
          'dj-disclosure-item',
          'Anything shared with another feature stays available to it.'
        )
      );
    }
    confirmation.appendChild(consequences);

    // The companion is a separate decision, presented as one.
    if (summary.companion && summary.companion.removable) {
      const label = el('label', 'dj-removal-companion');
      const box = document.createElement('input');
      box.type = 'checkbox';
      box.id = 'downloadsJanitorRemoveCompanion';
      box.checked = removalCompanionChecked;
      box.addEventListener('change', () => {
        removalCompanionChecked = box.checked;
      });
      label.appendChild(box);
      label.appendChild(el('span', '', ' Also remove the Curator agent from this workspace'));
      confirmation.appendChild(label);
    } else if (summary.companion) {
      confirmation.appendChild(
        el('p', 'dj-setting-help', summary.companion.reason || 'The Curator agent is left alone.')
      );
    }

    const buttons = el('div', 'dj-settings-actions');
    const confirm = button(
      'Remove File Janitor',
      'dj-btn dj-btn-destructive',
      () => void completeRemoval()
    );
    confirm.id = 'downloadsJanitorRemoveConfirm';
    const cancel = button('Keep File Janitor', 'dj-btn dj-btn-secondary', () => {
      removalConfirming = false;
      removalSummary = null;
      removalCompanionChecked = false;
      setSettingsMessage('');
      renderSettings();
    });
    cancel.id = 'downloadsJanitorRemoveCancel';
    buttons.appendChild(cancel);
    buttons.appendChild(confirm);
    confirmation.appendChild(buttons);

    section.appendChild(confirmation);
    return section;
  }

  // beginRemoval asks the server what removal would do, and shows that.
  //
  // The description is fetched rather than written here so it states what will
  // happen to THIS workspace — which folder, what stops, what is kept. Copy
  // composed in the browser cannot know, and would drift from the behavior it
  // claims to describe (FR-24, FR-25).
  async function beginRemoval() {
    const id = wsId();
    if (!id || busy) return;
    busy = true;
    setSettingsMessage('');
    try {
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/capabilities/file-janitor/removal'
      );
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(body.message || 'Ori could not describe what removing this would do.');
      }
      removalSummary = body.removal || {};
      removalConfirming = true;
      removalCompanionChecked = false;
      renderSettings();
    } catch (error) {
      setSettingsMessage(error.message || 'Ori could not describe what removing this would do.');
    } finally {
      busy = false;
    }
  }

  async function completeRemoval() {
    const id = wsId();
    if (!id || busy) return;
    busy = true;
    setSettingsMessage('Removing File Janitor…');
    try {
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/capabilities/file-janitor',
        {
          method: 'DELETE',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ remove_companion: removalCompanionChecked })
        }
      );
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(body.message || 'Ori could not remove File Janitor.');
      }
      removalConfirming = false;
      removalSummary = null;
      removalCompanionChecked = false;

      // The console is showing a capability this workspace no longer has, so
      // it closes rather than lingering over controls that would now fail. The
      // catalog reload is what makes the station and the card disappear and the
      // capability offer itself again as Available (FR-24, FR-10).
      close();
      lastStatus = { applies: false };
      render(lastStatus);
      const catalog = typeof window === 'undefined' ? null : window.WorkspaceCapabilities;
      if (catalog && typeof catalog.reload === 'function') await catalog.reload();
    } catch (error) {
      setSettingsMessage(error.message || 'Ori could not remove File Janitor.');
      renderSettings();
    } finally {
      busy = false;
    }
  }

  // curatorSection reports whether the optional Curator is present and offers
  // to add one if not.
  //
  // The Curator is optional by design: File Janitor works fully without it, so
  // this states that plainly rather than presenting the agent as something
  // missing. Presence is read from the install record's owned resources — the
  // same association the server uses for idempotency — never from an agent's
  // display name.
  function curatorSection() {
    const section = el('div', 'dj-setting dj-setting-curator');
    section.appendChild(el('h4', 'dj-setting-heading', 'Curator'));

    const record = capabilityRecord();
    const companions = (record && record.owned_resources ? record.owned_resources : []).filter(
      resource => resource && resource.kind === 'companion_agent'
    );

    if (companions.length > 0) {
      section.appendChild(
        el(
          'p',
          'dj-setting-help',
          'A Curator is helping in this workspace. It explains proposals and answers questions; ' +
            'it cannot approve, move, or delete anything.'
        )
      );
      return section;
    }

    section.appendChild(
      el(
        'p',
        'dj-setting-help',
        'No Curator in this workspace. File Janitor works fully without one — a Curator only ' +
          'helps you understand what is proposed, and can never act on your files.'
      )
    );
    const add = button('Add a Curator', 'dj-btn dj-btn-secondary', () => void addCurator());
    add.id = 'downloadsJanitorAddCurator';
    section.appendChild(add);
    return section;
  }

  // capabilityRecord reads this workspace's install record from the catalog the
  // capabilities module already loaded, rather than issuing another request.
  function capabilityRecord() {
    const catalog = typeof window === 'undefined' ? null : window.WorkspaceCapabilities;
    if (!catalog || typeof catalog.find !== 'function') return null;
    const item = catalog.find('file-janitor');
    return (item && item.record) || null;
  }

  async function addCurator() {
    const id = wsId();
    if (!id || busy) return;
    busy = true;
    setSettingsMessage('Adding a Curator\u2026');
    try {
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/capabilities/file-janitor/companion',
        { method: 'POST', headers: { 'Content-Type': 'application/json' } }
      );
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(
          body.message || (body.error && body.error.message) || 'Ori could not add a Curator.'
        );
      }
      setSettingsMessage(
        body.already_present
          ? 'A Curator is already helping here.'
          : 'Added ' + safeName(body.display_name || 'a Curator') + '.'
      );
      const catalog = typeof window === 'undefined' ? null : window.WorkspaceCapabilities;
      if (catalog && typeof catalog.reload === 'function') await catalog.reload();
      renderSettings();
    } catch (error) {
      setSettingsMessage(error.message || 'Ori could not add a Curator.');
    } finally {
      busy = false;
    }
  }

  function setSettingsMessage(message) {
    settingsMessage = message || '';
    const node = document.getElementById('downloadsJanitorSettingsStatus');
    if (node) node.textContent = settingsMessage;
  }

  async function saveSettings(patch) {
    const id = wsId();
    if (!id) return;
    setSettingsMessage('Saving…');
    try {
      const response = await fetch(apiBase(id) + '/settings', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(patch)
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = body.error || body;
        throw new Error((apiError && apiError.message) || 'Ori could not save that.');
      }
      render(body.status);
      settingsOpen = true;
      renderSettings();
      setSettingsMessage('Saved.');
      await loadBatch();
    } catch (error) {
      setSettingsMessage(error.message || 'Ori could not save that.');
    }
  }

  async function grantConsent(provider) {
    const id = wsId();
    if (!id) return;
    try {
      const response = await fetch(apiBase(id) + '/content-consent', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider })
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = body.error || body;
        throw new Error((apiError && apiError.message) || 'Ori could not record that.');
      }
      render(body.status);
    } catch (error) {
      showError(error.message || 'Ori could not record that.');
    }
  }

  async function testScan() {
    const id = wsId();
    if (!id) return;
    setSettingsMessage('Checking…');
    try {
      const response = await fetch(apiBase(id) + '/test-scan', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error('The test scan could not run.');
      const report = body.report || {};
      // A test scan is a check, and says so: it changes nothing.
      setSettingsMessage(
        report.eligible_count +
          (report.eligible_count === 1 ? ' file would be proposed' : ' files would be proposed') +
          (report.ineligible_count ? ', ' + report.ineligible_count + ' skipped' : '') +
          '. Nothing was changed.'
      );
    } catch (error) {
      setSettingsMessage(error.message || 'The test scan could not run.');
    }
  }

  async function resetSkipped() {
    const id = wsId();
    if (!id) return;
    setSettingsMessage('Resetting…');
    try {
      const response = await fetch(apiBase(id) + '/skipped/reset', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}'
      });
      if (!response.ok) throw new Error('Ori could not reset those.');
      setSettingsMessage('Previously skipped files can be proposed again.');
      await loadBatch();
    } catch (error) {
      setSettingsMessage(error.message || 'Ori could not reset those.');
    }
  }

  async function relink() {
    const id = wsId();
    if (!id) return;
    setSettingsMessage('');
    let picked = '';
    try {
      const response = await fetch('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: 'Choose a different folder to tidy' })
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok || !result.success)
        throw new Error(result.error || 'Folder picker unavailable');
      if (!result.selected || !result.path) return;
      picked = result.path;
    } catch (error) {
      setSettingsMessage(error.message || 'Could not open the folder picker.');
      return;
    }

    setSettingsMessage('Switching folders…');
    try {
      const response = await fetch(apiBase(id) + '/relink', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: picked })
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = body.error || body;
        throw new Error((apiError && apiError.message) || 'Ori could not change the folder.');
      }
      render(body.status);
      settingsOpen = true;
      renderSettings();
      setSettingsMessage(
        'Now tidying the new folder. Anything waiting for review from the old one was cleared.'
      );
      await loadBatch();
    } catch (error) {
      setSettingsMessage(error.message || 'Ori could not change the folder.');
    }
  }

  // revoke is destructive to access, not to files, and the confirmation says
  // exactly that so the user is not left guessing what they are about to lose.
  async function revoke() {
    const id = wsId();
    if (!id) return;
    if (!revokeConfirmed) {
      revokeConfirmed = true;
      setSettingsMessage(
        'This disconnects the folder: Ori stops watching and scanning it. Your files stay exactly where they are, ' +
          'and your history is kept. Choose "Stop using this folder" again to confirm.'
      );
      return;
    }
    revokeConfirmed = false;
    setSettingsMessage('Disconnecting…');
    try {
      const response = await fetch(apiBase(id) + '/revoke', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = body.error || body;
        throw new Error((apiError && apiError.message) || 'Ori could not disconnect the folder.');
      }
      settingsOpen = false;
      render(body.status);
    } catch (error) {
      setSettingsMessage(error.message || 'Ori could not disconnect the folder.');
    }
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
    entry.appendChild(el('span', 'dj-history-name', displayName(action)));

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
      const response = await fetch(apiBase(id) + '/history' + query);
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
        apiBase(id) + '/history/' + encodeURIComponent(actionID) + '/undo',
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
    // Focus moves here below, so the user is taken to the outcome rather than
    // told about it from elsewhere. A live region as well would announce the
    // same text twice, so this is a labelled group instead of role="status".
    panel.setAttribute('role', 'group');
    panel.setAttribute('tabindex', '-1');
    panel.setAttribute('aria-labelledby', 'downloadsJanitorResultsSummary');

    const parts = [];
    if (result.applied)
      parts.push(result.applied + (result.applied === 1 ? ' file filed' : ' files filed'));
    if (result.failed) parts.push(result.failed + ' could not be moved');
    if (result.stale) parts.push(result.stale + ' changed since you approved');
    if (parts.length === 0) parts.push('Nothing was moved');
    const resultsSummary = el('p', 'dj-results-summary', parts.join(' · ') + '.');
    resultsSummary.id = 'downloadsJanitorResultsSummary';
    panel.appendChild(resultsSummary);

    const list = el('ul', 'dj-results-list');
    (result.outcomes || []).forEach(outcome => {
      const entry = el('li', 'dj-results-item dj-results-' + (outcome.result || 'failed'));
      const mark = el('span', 'dj-results-mark', RESULT_MARKS[outcome.result] || '!');
      mark.setAttribute('aria-hidden', 'true');
      entry.appendChild(mark);
      entry.appendChild(el('span', 'dj-results-name', displayName(outcome)));
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
    // The button that was pressed no longer exists. Move focus to the outcome
    // so a keyboard user lands on what happened instead of at the top of the
    // document, and drop the saved opener — this panel is where they are now.
    focusReturn = null;
    panel.focus?.();
  }

  // startApproval asks the server for the final plan and shows it for
  // confirmation. Nothing moves at this step.
  async function startApproval() {
    const id = wsId();
    if (!id || busy || selected.size + trashMarked.size === 0) return;
    busy = true;
    showError('');
    rememberFocus();
    const control = document.getElementById('downloadsJanitorApprove');
    if (control) control.disabled = true;
    try {
      const decisions = Array.from(selected).map(candidateId => {
        const candidate = lastCandidates.find(item => item.id === candidateId) || {};
        // For a flagged row the user's own choice is the ONLY acceptable
        // category. Falling back to candidate.category here would file it
        // using the guess that was flagged as not trustworthy — the exact
        // outcome the flag exists to prevent. Sending an empty category makes
        // the server refuse it rather than this silently deciding.
        const category = unresolvedNeedsReview(candidate)
          ? ''
          : candidate.decision_category || candidate.category || '';
        return { candidate_id: candidateId, operation: 'move', category };
      });
      trashMarked.forEach(candidateId => {
        decisions.push({ candidate_id: candidateId, operation: 'trash', category: '' });
      });
      const response = await fetch(apiBase(id) + '/preview', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ decisions })
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = body.error || body;
        throw new Error((apiError && apiError.message) || 'Ori could not prepare these moves.');
      }
      pendingPreview = { preview: body.preview, decisions };
      // Release the busy flag BEFORE the confirmation goes on screen. Rendering
      // it first left a window in which the confirm button was visible and
      // enabled while applyApproval still saw busy === true and returned
      // silently: a user who clicked promptly got nothing at all, with no error
      // and no request. The approval request is finished by this point, so
      // there is nothing left to be busy with.
      busy = false;
      renderConfirmation(pendingPreview);
    } catch (error) {
      showError(error.message || 'Ori could not prepare these moves.');
    } finally {
      busy = false;
      updateSelectionSummary();
    }
  }

  // applyApproval spends the approval and reports the per-item outcome.
  async function applyApproval(approval) {
    const id = wsId();
    // Prefer the approval this button was built with. Falling back to module
    // state keeps older call sites working, but the button the user pressed is
    // the authority on what they approved.
    const active = approval && approval.preview ? approval : pendingPreview;
    if (!id || busy || !active) return;
    busy = true;
    showError('');
    const control = document.getElementById('downloadsJanitorConfirmApply');
    if (control) {
      control.disabled = true;
      control.textContent = 'Moving…';
    }
    try {
      const response = await fetch(apiBase(id) + '/apply', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          batch_id: active.preview.batch_id,
          approval_token: active.preview.token,
          decisions: active.decisions
        })
      });
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

  // pauseControl is the real Pause/Resume, shared by every surface that offers
  // it so the wording and the setup guard can never drift apart.
  //
  // Pausing stops the unattended work only. Saying so on the control itself
  // saves the user wondering whether pausing loses their pending review.
  function pauseControl(settings) {
    const paused = Boolean(settings && settings.paused);
    const control = button(
      paused ? 'Resume watching' : 'Pause watching',
      'dj-btn dj-btn-secondary',
      () => void setPaused(!paused)
    );
    control.id = 'downloadsJanitorPause';
    control.setAttribute(
      'title',
      paused
        ? 'Start watching this folder again.'
        : 'Stop automatic scanning. Your settings, pending review, and history are kept, and you can still scan on demand.'
    );
    // Before setup finishes, resuming here would start the unattended work the
    // wizard has not yet disclosed — the one step the user is supposed to read
    // first. The control stays visible and says where the decision lives
    // (FR-56); scanning on demand is unaffected, because the user is the one
    // asking for each scan.
    if (paused && setupAwaitingAutomationApproval()) {
      control.textContent = 'Approve in setup';
      control.setAttribute(
        'title',
        'Setup explains what folder watching and the daily scan do before turning them on.'
      );
      control.onclick = () => window.SetupWizard?.open?.('automation');
    }
    return control;
  }

  // renderCompactCard is all Workspace Details carries now: what state File
  // Janitor is in, which folder it manages, what is waiting, when it looks
  // next, and a way in. The review table, the settings form, and history are
  // the console's and appear nowhere else on the page (FR-114, FR-115).
  function renderCompactCard(host, status) {
    const settings = status.settings || {};
    const configured = isConfigured(status);

    const card = el('section', 'fj-card');
    card.setAttribute('role', 'group');
    card.setAttribute('aria-labelledby', 'downloadsJanitorTitle');

    const head = el('div', 'fj-card-head');
    const heading = el('div', 'fj-card-heading');
    const title = el('h2', 'fj-card-title', 'File Janitor');
    title.id = 'downloadsJanitorTitle';
    heading.appendChild(title);
    const sub = el('p', 'fj-card-sub');
    sub.textContent = configured
      ? 'Tidying ' + safeName(settings.root_path)
      : 'No folder chosen yet. Nothing is scanned or moved until you choose one.';
    heading.appendChild(sub);
    const stats = el('p', 'fj-card-stats');
    stats.id = 'downloadsJanitorStats';
    stats.setAttribute('role', 'status');
    stats.setAttribute('aria-live', 'polite');
    stats.textContent = configured ? statsLine() : '';
    stats.hidden = !configured;
    heading.appendChild(stats);
    head.appendChild(heading);

    const activity = activityState(status);
    const badge = el('span', 'dj-badge dj-badge-' + activity.id.replace(/_/g, '-'), activity.label);
    badge.id = 'downloadsJanitorActivity';
    head.appendChild(badge);
    card.appendChild(head);

    const actions = el('div', 'fj-card-actions');
    const openLabel = configured ? 'Open File Janitor' : 'Set up File Janitor';
    const openButton = button('', 'dj-btn dj-btn-primary', event => {
      open({ source: 'workspace-details', trigger: event && event.currentTarget });
    });
    openButton.textContent = openLabel;
    openButton.id = 'fileJanitorCardOpen';
    actions.appendChild(openButton);

    // Scanning on demand is offered from the card only once there is a folder
    // to scan; before that the single action is the one that sets it up.
    if (configured) {
      const scan = button('Scan now', 'dj-btn dj-btn-secondary', () => void scanNow());
      scan.id = 'fileJanitorCardScan';
      scan.disabled = scanning;
      actions.appendChild(scan);
    }
    card.appendChild(actions);
    // What Ori reads is stated on the card itself, not behind the Settings
    // tab. A disclosure the user must go looking for is one they will not
    // read, and this is the sentence that says whether their file contents are
    // being opened.
    if (configured) card.appendChild(privacyLine(status));
    card.appendChild(cardErrorRegion());
    host.appendChild(card);
  }

  // renderSetupPrompt re-opens the folder chooser for an already-configured
  // workspace (relink/repair), reusing the same confirm path.
  //
  // It renders into the console body, which is where the repair action that
  // calls it lives. The suggestion is the folder the user already approved and
  // nothing else: a relink prompted by a folder that vanished must not quietly
  // propose a different one to grant access to (FR-44, FR-50).
  function renderSetupPrompt(status) {
    const host = document.getElementById('fileJanitorConsoleBody') || mount();
    if (!host) return;
    clear(host);
    const settings = status.settings || {};
    renderSetupCard(host, {
      settings,
      suggestion: {
        label: 'Folder to tidy',
        suggested_path: settings.root_path || '',
        filing_root_name: settings.filing_root_name,
        daily_scan_local_time: settings.daily_scan_local_time
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

  // The card gets its own error region under a different id. Two nodes sharing
  // one id would make getElementById pick whichever came first in the document,
  // so a failure inside the console could surface behind it on the card.
  function cardErrorRegion() {
    const region = errorRegion();
    region.id = 'fileJanitorCardError';
    return region;
  }

  // The message currently on screen, remembered so a repaint does not erase it.
  //
  // Status is re-read in the background — on open, after a scan, whenever setup
  // changes — and every repaint builds a fresh error region. Without this, an
  // error the user triggered a moment ago vanishes mid-read because an
  // unrelated refresh happened to land. It is cleared explicitly by the actions
  // that succeed, so nothing stale survives a working operation.
  let lastErrorMessage = '';

  // showError writes to whichever surface the user is actually looking at. The
  // console is modal, so while it is open it is the only place an error can be
  // read at all.
  function showError(message) {
    lastErrorMessage = message || '';
    paintError();
  }

  function paintError() {
    const region =
      (consoleOpen && document.getElementById('downloadsJanitorError')) ||
      document.getElementById('fileJanitorCardError') ||
      document.getElementById('downloadsJanitorError');
    if (!region) return;
    region.textContent = lastErrorMessage;
    region.hidden = !lastErrorMessage;
  }

  function render(status) {
    // Marks and selections belong to the folder that was on screen. Changing
    // the managed folder invalidates every one of them, so they are discarded
    // here — but ONLY then.
    //
    // This used to clear them on every status repaint. Status is re-read
    // constantly (on open, after a scan, whenever setup changes), and none of
    // those touch the candidate set, so a background refresh landing mid-review
    // silently emptied a selection the user had built up and disabled the
    // approve button under their cursor. The batch's own reload owns the
    // narrower rule — discard when the batch id actually changed (loadBatch).
    if (managedFolderChanged(lastStatus, status)) {
      trashMarked = new Set();
      selected = new Set();
      pendingPreview = null;
    }
    lastStatus = status;

    // Workspace Details now shows a compact card only. The review table, the
    // settings form, and history live in the console and nowhere else, so
    // there is exactly one of each on the page (FR-100, FR-114).
    const host = mount();
    if (host) {
      if (!status || !status.applies) {
        host.hidden = true;
        clear(host);
      } else {
        host.hidden = false;
        clear(host);
        renderCompactCard(host, status);
      }
    }

    if (consoleOpen) renderConsole();
    paintError();
    notifySubscribers();
  }

  // managedFolderChanged reports whether the folder these decisions were made
  // about is still the folder in effect. A different root, or a re-issued
  // directory reference, means the pending selections describe files that are
  // no longer the ones on screen.
  function managedFolderChanged(before, after) {
    const previous = (before && before.settings) || {};
    const next = (after && after.settings) || {};
    if (!before) return false;
    return (
      previous.root_path !== next.root_path ||
      previous.directory_reference_id !== next.directory_reference_id
    );
  }

  // isConfigured is the single question that decides which face every surface
  // shows: a folder the user approved, and the directory reference that grants
  // access to it. Either one missing means setup is unfinished.
  function isConfigured(status) {
    const settings = (status && status.settings) || {};
    return Boolean(settings.root_path && settings.directory_reference_id);
  }

  // renderConsoleBody paints the console's active tab. It reuses the renderers
  // the inline panel already had: they address their hosts by id, so moving
  // those hosts into the console moves the surfaces with them.
  function renderConsoleBody(host, status) {
    if (!isConfigured(status)) {
      // An unconfigured install opens straight into the real setup experience
      // — the wizard when the blueprint declares one, the folder card
      // otherwise. Never a placeholder, and never a second chooser beside the
      // wizard (FR-104).
      if (setupWizardOwnsSetup()) {
        renderSetupEntry(host);
      } else {
        renderSetupCard(host, status);
      }
      return;
    }
    if (consoleTab === 'history') {
      const historyHost = el('div', 'dj-history-host');
      historyHost.id = 'downloadsJanitorHistoryHost';
      host.appendChild(historyHost);
      host.appendChild(errorRegion());
      renderHistory();
      if (!historyLoaded) void loadHistory();
      return;
    }
    if (consoleTab === 'settings') {
      const settingsHost = el('div', 'dj-settings-host');
      settingsHost.id = 'downloadsJanitorSettingsHost';
      host.appendChild(settingsHost);
      host.appendChild(privacyLine(status));
      host.appendChild(errorRegion());
      settingsOpen = true;
      renderSettings();
      return;
    }

    // Review: readiness rows, then the batch, then — deliberately outside the
    // batch — the confirmation step and the report of what just happened.
    // Applying every file empties the batch; the account of the user's own
    // files must not be swept away with the table it came from.
    host.appendChild(readinessRows(status));
    const repair = repairAction(status);
    if (repair) host.appendChild(repair);
    const batchHost = el('div', 'dj-batch');
    batchHost.id = 'downloadsJanitorBatch';
    host.appendChild(batchHost);
    const confirmHost = el('div', 'dj-confirm-host');
    confirmHost.id = 'downloadsJanitorConfirmHost';
    host.appendChild(confirmHost);
    host.appendChild(errorRegion());
    renderBatch();
  }

  // repairAction offers "Choose the folder again" when readiness says the
  // folder is the thing that broke — moved, renamed, or its permission
  // withdrawn. Without it a workspace whose folder disappeared has a console
  // that reports the problem and offers no way to fix it.
  function repairAction(status) {
    const checks = ((status && status.readiness) || {}).checks || [];
    const repairable = checks.some(
      check =>
        check.status === 'failed' &&
        (check.repair === 'relink_folder' ||
          check.repair === 'choose_folder' ||
          check.repair === 'grant_permission')
    );
    if (!repairable) return null;
    const actions = el('div', 'dj-actions');
    actions.appendChild(
      button('Choose the folder again', 'dj-btn dj-btn-secondary', () => renderSetupPrompt(status))
    );
    actions.appendChild(button('Check again', 'dj-btn dj-btn-secondary', () => void refresh()));
    return actions;
  }

  function readinessRows(status) {
    const readiness = (status && status.readiness) || {};
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
    return rows;
  }

  // ------------------------------------------------------------- the console
  //
  // One console, opened from every entry point (Map station, Details card,
  // post-install offer, capability catalog, Action Center, deep link). There is
  // deliberately no second copy and no hidden one: the surfaces below all call
  // open(), so what the user sees can never depend on which door they used
  // (FR-99, FR-100).

  const CONSOLE_HOST_ID = 'fileJanitorConsole';
  const CONSOLE_TITLE_ID = 'fileJanitorConsoleTitle';
  const CONSOLE_TABS = ['review', 'history', 'settings'];
  const CONSOLE_PANEL_PARAM = 'file-janitor';

  let consoleOpen = false;
  let consoleTab = 'review';
  let consoleItem = '';
  // The element focus returns to on close. Held rather than re-derived: by
  // close time the Map may have repainted and the original button is the only
  // thing that knows where the user was (FR-120).
  let consoleTrigger = null;
  let consoleOverlayId = '';
  // The console's own close control, kept so focus can be placed on it without
  // searching the tree for something focusable.
  let consoleCloseButton = null;
  // The tab last chosen in THIS workspace. Kept per workspace so opening a
  // different one cannot restore a tab that belongs to another (FR-106).
  const rememberedTab = new Map();
  const subscribers = new Set();

  function overlayCoordinator() {
    if (typeof window === 'undefined') return null;
    return window.workspaceOverlayCoordinator || null;
  }

  function consoleHost() {
    if (typeof document === 'undefined') return null;
    let host = document.getElementById(CONSOLE_HOST_ID);
    if (host) return host;
    if (!document.body) return null;
    host = el('div', 'fj-console-host');
    host.id = CONSOLE_HOST_ID;
    host.hidden = true;
    document.body.appendChild(host);
    return host;
  }

  // validTab rejects anything not in the allowlist. An unknown tab falls back
  // to Review rather than rendering nothing: a bad deep link must not be able
  // to leave the user staring at an empty console (FR-117, FR-145).
  function validTab(value) {
    const tab = String(value || '').toLowerCase();
    return CONSOLE_TABS.includes(tab) ? tab : '';
  }

  // chooseTab picks the tab a fresh open lands on. Work waiting for a decision
  // wins over whatever was last looked at: the console exists to get files
  // reviewed, and a restored Settings tab would hide the one thing that needs
  // the user (FR-106).
  function chooseTab(requested) {
    const explicit = validTab(requested);
    if (explicit) return explicit;
    if (pendingCount() > 0) return 'review';
    const remembered = validTab(rememberedTab.get(wsId()));
    return remembered || 'review';
  }

  function pendingCount() {
    return (lastBatch && lastBatch.summary && lastBatch.summary.proposed) || 0;
  }

  /**
   * open — the one way any surface opens File Janitor.
   *
   * @param {object} [options]
   * @param {string} [options.source] - which entry point asked (telemetry/debug)
   * @param {string} [options.tab] - requested tab; validated, never trusted
   * @param {string} [options.item] - candidate/action to focus; validated
   * @param {*} [options.trigger] - element focus returns to on close
   */
  function open(options = {}) {
    const id = wsId();
    if (!id) return false;
    // A workspace the capability is not installed in has no console to open.
    // The check is against a status we have actually read: on a cold load
    // lastStatus is still null, and refusing then would make the first press
    // of a station do nothing (FR-93).
    if (lastStatus && !lastStatus.applies) return false;
    const host = consoleHost();
    if (!host) return false;

    consoleTrigger = options.trigger || consoleTrigger || activeElement();
    consoleTab = chooseTab(options.tab);
    consoleItem = String(options.item || '');
    consoleOpen = true;

    const coordinator = overlayCoordinator();
    if (coordinator && typeof coordinator.open === 'function') {
      consoleOverlayId = 'file-janitor-console';
      coordinator.open({
        id: consoleOverlayId,
        kind: 'modal',
        container: host,
        trigger: consoleTrigger,
        onClose: info => {
          // The coordinator closes this for its own reasons too (Escape, a
          // modal that must replace it). Mirror that into local state so the
          // console never believes it is open while hidden.
          if (info && info.reason === 'suspended') return;
          if (consoleOpen) close({ viaCoordinator: true });
        }
      });
    }

    renderConsole();
    syncUrl();
    // A console opened from the Map may be showing status read minutes ago.
    void refresh();
    notifySubscribers();
    return true;
  }

  function close(options = {}) {
    if (!consoleOpen) return false;
    consoleOpen = false;
    settingsOpen = false;
    consoleCloseButton = null;
    const host = document.getElementById(CONSOLE_HOST_ID);
    if (host) {
      host.hidden = true;
      clear(host);
    }
    const coordinator = overlayCoordinator();
    if (!options.viaCoordinator && coordinator && typeof coordinator.close === 'function') {
      // The coordinator releases the background inert marking here, which has
      // to happen BEFORE focus moves: an inert element cannot take focus, so
      // restoring first would silently do nothing.
      coordinator.close(consoleOverlayId);
    }
    // Restore focus ourselves either way. The coordinator holds the same
    // element reference we were given, and it is just as likely to be stale —
    // only this module knows how to find the control's live replacement.
    restoreConsoleFocus();
    consoleOverlayId = '';
    if (!options.keepUrl) syncUrl();
    notifySubscribers();
    return true;
  }

  function activeElement() {
    if (typeof document === 'undefined') return null;
    return document.activeElement || null;
  }

  // restoreConsoleFocus puts the keyboard back on the control that opened the
  // console.
  //
  // The held element is often GONE by now. Status is re-read while the console
  // is open, and each repaint rebuilds the compact card — so the button the user
  // pressed has been replaced by an identical one, and focusing the original
  // does nothing at all: .focus() on a detached node is a silent no-op. The
  // keyboard was left on <body>, behind a console that had just closed.
  //
  // So: use the held element only if it is still in the document, and otherwise
  // find its live replacement. Both entry points are addressable — the card's
  // button by id, the Map station by its registry key.
  function restoreConsoleFocus() {
    const trigger = consoleTrigger;
    consoleTrigger = null;

    if (trigger && typeof trigger.focus === 'function' && isInDocument(trigger)) {
      trigger.focus();
      return;
    }

    const replacement = liveTrigger(trigger);
    if (replacement && typeof replacement.focus === 'function') replacement.focus();
  }

  function isInDocument(node) {
    if (typeof document === 'undefined' || !node) return false;
    if (typeof document.contains === 'function') return document.contains(node);
    return Boolean(node.isConnected);
  }

  // liveTrigger finds the current instance of whatever opened the console.
  // Preference order matches how the user got here: the same id if it had one,
  // then the Map station, then the card.
  function liveTrigger(trigger) {
    if (typeof document === 'undefined') return null;
    const id = trigger && trigger.id;
    if (id) {
      const byID = document.getElementById(id);
      if (byID) return byID;
    }
    if (typeof document.querySelector === 'function') {
      const station = document.querySelector('[data-cmd-hq-station="file-janitor"]');
      if (station) return station;
    }
    return document.getElementById('fileJanitorCardOpen');
  }

  function renderConsole() {
    const host = consoleHost();
    if (!host) return;
    const status = lastStatus;
    host.hidden = false;
    clear(host);

    const backdrop = el('div', 'fj-console-backdrop');
    // Clicking the backdrop is a close, the same as the close button. It is a
    // separate element rather than a handler on the host so the console body
    // itself never swallows a click meant for a control inside it.
    backdrop.addEventListener('click', () => close());
    host.appendChild(backdrop);

    const dialog = el('div', 'fj-console');
    dialog.id = 'fileJanitorConsoleDialog';
    dialog.setAttribute('role', 'dialog');
    dialog.setAttribute('aria-modal', 'true');
    // Programmatically focusable so focus can land on the dialog itself, which
    // is what puts a screen reader at its title rather than partway into its
    // controls. -1 keeps it out of the Tab order.
    dialog.setAttribute('tabindex', '-1');
    // Labelled by the visible title, so the name a sighted user reads and the
    // name a screen reader announces are the same string (FR-119).
    dialog.setAttribute('aria-labelledby', CONSOLE_TITLE_ID);
    dialog.appendChild(renderConsoleHeader(status));

    const body = el('div', 'fj-console-body');
    body.id = 'fileJanitorConsoleBody';
    dialog.appendChild(body);

    // Attach BEFORE filling the body.
    //
    // The tab renderers find their hosts with getElementById — renderSettings
    // looks up downloadsJanitorSettingsHost, renderBatch looks up
    // downloadsJanitorBatch. A node that has been appended to a detached
    // subtree is not in the document yet, so those lookups returned null and
    // each renderer bailed silently: Settings showed only its privacy line and
    // Review showed only its readiness rows, on first paint, with no error
    // anywhere. It corrected itself on the next repaint, which is why it looked
    // intermittent.
    host.appendChild(dialog);
    renderConsoleBody(body, status);

    paintError();
    focusRequestedItem() || focusConsole();
  }

  // focusRequestedItem puts the user on the exact row a notification named.
  //
  // It reports whether it found one. A link naming a candidate that has since
  // been filed, skipped, or scanned away finds nothing — and that is a normal
  // outcome, not an error: the tab is still correct and still useful, so focus
  // falls back to the console itself rather than reporting a failure about a
  // file the user has already dealt with (FR-116).
  function focusRequestedItem() {
    if (!consoleItem) return false;
    const row = document.getElementById('fileJanitorLinkedRow');
    if (!row) return false;
    if (typeof row.focus === 'function') row.focus();
    if (typeof row.scrollIntoView === 'function') {
      row.scrollIntoView({ block: 'center' });
    }
    return true;
  }

  // focusConsole moves the keyboard into the dialog, onto the close control.
  //
  // Close is the one control present in every state — setup, all three tabs,
  // and an error — so focus lands somewhere real without having to guess at
  // the first focusable child. Landing there also means Escape is not the only
  // way out for someone who did not see the console open (FR-119).
  function focusConsole() {
    // The dialog itself, looked up from the document rather than held from
    // render time. A reference captured while the node was still detached
    // focuses nothing — .focus() on an element outside the document is a no-op,
    // silently — which left the keyboard back on the page behind an open modal.
    const dialog = document.getElementById('fileJanitorConsoleDialog');
    if (dialog && typeof dialog.focus === 'function') {
      dialog.focus();
      return;
    }
    if (consoleCloseButton && typeof consoleCloseButton.focus === 'function') {
      consoleCloseButton.focus();
    }
  }

  function renderConsoleHeader(status) {
    const configured = isConfigured(status);
    const settings = (status && status.settings) || {};
    const header = el('header', 'fj-console-head');

    const heading = el('div', 'fj-console-heading');
    const title = el('h2', 'fj-console-title', 'File Janitor');
    title.id = CONSOLE_TITLE_ID;
    heading.appendChild(title);
    // The managed folder is shown by its display name, cleaned the same way a
    // filename is: a folder can be named as adversarially as a file.
    const folder = settings.root_path ? safeName(settings.root_path) : '';
    if (folder) heading.appendChild(el('p', 'fj-console-folder', folder));
    const activity = activityState(status);
    const statusLine = el('p', 'fj-console-status');
    statusLine.id = 'fileJanitorConsoleStatus';
    statusLine.setAttribute('role', 'status');
    statusLine.setAttribute('aria-live', 'polite');
    const pending = pendingCount();
    statusLine.textContent =
      activity.label + (pending > 0 ? ' · ' + reviewCountLabel(pending) : '');
    heading.appendChild(statusLine);
    header.appendChild(heading);

    const actions = el('div', 'fj-console-actions');
    if (configured) {
      const scan = button('Scan now', 'dj-btn dj-btn-primary', () => void scanNow());
      scan.id = 'downloadsJanitorScan';
      scan.disabled = scanning;
      actions.appendChild(scan);
      actions.appendChild(pauseControl(settings));
    }
    const closeButton = button('Close', 'dj-btn dj-btn-secondary', () => close());
    closeButton.setAttribute('data-fj-console-close', '');
    closeButton.setAttribute('aria-label', 'Close File Janitor');
    consoleCloseButton = closeButton;
    actions.appendChild(closeButton);
    header.appendChild(actions);

    // Before setup finishes there are no tabs: there is nothing to review, no
    // history, and no settings to change. Showing three empty tabs would
    // suggest otherwise (FR-103, FR-104).
    if (configured) header.appendChild(renderConsoleTabs());
    return header;
  }

  function reviewCountLabel(count) {
    return count + (count === 1 ? ' file ready for review' : ' files ready for review');
  }

  function renderConsoleTabs() {
    const list = el('div', 'fj-console-tabs');
    list.setAttribute('role', 'tablist');
    const labels = { review: 'Review', history: 'History', settings: 'Settings' };
    CONSOLE_TABS.forEach(tab => {
      const active = tab === consoleTab;
      const control = button(labels[tab], 'fj-console-tab' + (active ? ' is-active' : ''), () =>
        selectTab(tab)
      );
      control.setAttribute('role', 'tab');
      control.setAttribute('aria-selected', active ? 'true' : 'false');
      control.setAttribute('data-fj-tab', tab);
      if (tab === 'review') {
        const pending = pendingCount();
        if (pending > 0) control.appendChild(el('span', 'fj-console-tab-count', String(pending)));
      }
      list.appendChild(control);
    });
    return list;
  }

  function selectTab(tab) {
    const next = validTab(tab);
    if (!next) return;
    consoleTab = next;
    rememberedTab.set(wsId(), next);
    // Moving tabs abandons any half-finished approval: an approval issued
    // against the Review tab must not survive out of the user's sight.
    pendingPreview = null;
    consoleItem = '';
    renderConsole();
    syncUrl();
  }

  // ------------------------------------------------------------- URL state
  //
  // The console is addressable so a notification can point at it, and so Back
  // behaves the way a modal should. Only File Janitor's own parameters are ever
  // touched: unrelated workspace URL state is preserved on both open and close
  // (FR-117, FR-124).

  function syncUrl(options = {}) {
    if (typeof window === 'undefined' || !window.history || !window.location) return;
    const url = new URL(window.location.href);
    const before = url.search;
    if (consoleOpen) {
      url.searchParams.set('panel', CONSOLE_PANEL_PARAM);
      if (consoleTab && consoleTab !== 'review') {
        url.searchParams.set('tab', consoleTab);
      } else {
        url.searchParams.delete('tab');
      }
      if (consoleItem) {
        url.searchParams.set('item', consoleItem);
      } else {
        url.searchParams.delete('item');
      }
    } else {
      url.searchParams.delete('panel');
      url.searchParams.delete('tab');
      url.searchParams.delete('item');
    }
    if (url.search === before) return;
    const method = options.replace ? 'replaceState' : 'pushState';
    if (typeof window.history[method] !== 'function') return;
    window.history[method]({ fileJanitor: consoleOpen }, '', url.toString());
  }

  // applyUrlState opens or closes the console to match the URL. It runs on load
  // and on popstate, which is what makes Back close a deep-linked console
  // without leaving the workspace (FR-124).
  function applyUrlState(options = {}) {
    if (typeof window === 'undefined' || !window.location) return;
    const params = new URLSearchParams(window.location.search || '');
    const wants = params.get('panel') === CONSOLE_PANEL_PARAM;
    if (!wants) {
      if (consoleOpen) close({ keepUrl: true });
      return;
    }
    const tab = validTab(params.get('tab'));
    const item = String(params.get('item') || '');
    if (consoleOpen) {
      // The URL is authoritative here, and an absent `tab` means Review — that
      // is exactly how Review is encoded (it is the default, so it is omitted).
      // Keeping the current tab instead made Back from Settings appear to do
      // nothing: the entry it returned to said Review, and the console stayed
      // on Settings (FR-124).
      consoleTab = tab || 'review';
      consoleItem = item;
      renderConsole();
      return;
    }
    open({ source: options.source || 'url', tab, item });
    // The URL already says what it says; rewriting it here would push a
    // duplicate entry and make one Back press appear to do nothing.
    syncUrl({ replace: true });
  }

  // ------------------------------------------------------- subscriptions
  //
  // The Map station and the Details card show state this module owns. They
  // subscribe rather than poll, so a scan finishing updates every surface at
  // once instead of only whichever one happens to repaint next.

  function subscribe(listener) {
    if (typeof listener !== 'function') return () => {};
    subscribers.add(listener);
    return () => subscribers.delete(listener);
  }

  function notifySubscribers() {
    const snapshot = stationState();
    subscribers.forEach(listener => {
      try {
        listener(snapshot);
      } catch (_) {
        // A broken listener must not stop the others, and must never take the
        // console down with it.
      }
    });
    if (typeof document !== 'undefined' && typeof document.dispatchEvent === 'function') {
      document.dispatchEvent(
        new CustomEvent('ori:file-janitor-changed', { detail: { state: snapshot } })
      );
    }
  }

  // setupWizardOwnsSetup reports whether this workspace's blueprint declares a
  // Setup Wizard. A workspace created before the blueprint declared one keeps
  // the original card, so nobody loses their way to set up.
  function setupWizardOwnsSetup() {
    const status = window.SetupWizard?.getStatus?.();
    return Boolean(status && status.applicable);
  }

  // setupAwaitingAutomationApproval reports that this workspace's wizard exists
  // and has not finished — the window in which unattended work must not be
  // switched on from anywhere but the step that describes it.
  function setupAwaitingAutomationApproval() {
    const status = window.SetupWizard?.getStatus?.();
    return Boolean(status && status.applicable && status.state !== 'ready');
  }

  // renderSetupEntry is the compact stand-in for the retired setup card: it
  // says what state setup is in and opens the wizard.
  function renderSetupEntry(host) {
    const setup = window.SetupWizard?.getStatus?.() || {};
    const card = el('section', 'dj-card');
    const head = el('div', 'dj-head');
    const heading = el('div', 'dj-heading');
    const title = el('h2', 'dj-title', 'Downloads Janitor');
    title.id = 'downloadsJanitorTitle';
    heading.appendChild(title);
    heading.appendChild(
      el(
        'p',
        'dj-sub',
        setup.state === 'needs_attention'
          ? 'Something this workspace depends on stopped working.'
          : 'Setup is not finished. Nothing is scanned or moved until it is.'
      )
    );
    head.appendChild(heading);
    head.appendChild(
      stateBadge(setup.state === 'needs_attention' ? 'needs_attention' : 'setup_required')
    );
    card.appendChild(head);

    const actions = el('div', 'dj-actions');
    const open = button(
      setup.state === 'needs_attention' ? 'Repair setup' : 'Continue setup',
      'dj-btn dj-btn-primary',
      () => window.SetupWizard?.open?.()
    );
    open.id = 'downloadsJanitorOpenSetup';
    actions.appendChild(open);
    card.appendChild(actions);
    card.appendChild(errorRegion());
    host.appendChild(card);
  }

  // statsLine states what is waiting and when Ori last looked — the two facts
  // that tell the user whether this workspace is doing anything.
  function statsLine() {
    const settings = (lastStatus && lastStatus.settings) || {};
    const parts = [];

    const pending = (lastBatch && lastBatch.summary && lastBatch.summary.proposed) || 0;
    parts.push(
      pending === 0
        ? 'No files waiting for review'
        : pending + (pending === 1 ? ' file waiting for review' : ' files waiting for review')
    );

    const when = lastBatch && formatWhen(lastBatch.completed_at || lastBatch.started_at);
    if (when) parts.push('last scan ' + when);

    // What happens next, in the user's own words: a daily time they set, or a
    // plain statement that nothing is scheduled while paused.
    if (settings.paused) {
      parts.push('automatic scanning paused');
    } else if (settings.daily_scan_local_time) {
      parts.push('next catch-up at ' + settings.daily_scan_local_time);
    }
    return parts.join(' · ') + '.';
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
        if (typeof syncSetupConfirmEnabled === 'function') syncSetupConfirmEnabled();
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
      const response = await fetch(apiBase(id) + '/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: trimmed })
      });
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
    scanning = true;
    showError('');
    refreshActivity();
    const control = document.getElementById('downloadsJanitorScan');
    if (control) {
      control.disabled = true;
      control.textContent = 'Scanning…';
    }
    try {
      const response = await fetch(apiBase(id) + '/scan', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
      });
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
      scanning = false;
      refreshActivity();
      const done = document.getElementById('downloadsJanitorScan');
      if (done) {
        done.disabled = false;
        done.textContent = 'Scan now';
      }
    }
  }

  function refreshActivity() {
    const node = document.getElementById('downloadsJanitorActivity');
    if (!node) return;
    const activity = activityState(lastStatus);
    node.textContent = activity.label;
    node.className = 'dj-badge dj-badge-' + activity.id.replace(/_/g, '-');
  }

  // setPaused stops or resumes the unattended work. The panel repaints from the
  // server's answer, so the badge and controls reflect what was actually saved.
  async function setPaused(paused) {
    const id = wsId();
    if (!id || busy) return;
    busy = true;
    showError('');
    const control = document.getElementById('downloadsJanitorPause');
    if (control) control.disabled = true;
    try {
      const response = await fetch(apiBase(id) + '/pause', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ paused })
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = body.error || body;
        throw new Error((apiError && apiError.message) || 'Ori could not change that setting.');
      }
      busy = false;
      render(body.status);
      await loadBatch();
      await loadHistory();
      return;
    } catch (error) {
      showError(error.message || 'Ori could not change that setting.');
    } finally {
      busy = false;
      const done = document.getElementById('downloadsJanitorPause');
      if (done) done.disabled = false;
    }
  }

  // submitDecisions records review decisions server-side and repaints from the
  // response, so what the page shows is what was actually stored.
  async function submitDecisions(decisions) {
    const id = wsId();
    if (!id || !decisions || decisions.length === 0) return;
    showError('');
    try {
      const response = await fetch(apiBase(id) + '/decisions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ decisions })
      });
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
      const response = await fetch(apiBase(id) + '/categories');
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
    // The rows about to be rendered need the category vocabulary: without it
    // the picker collapses to a single dead option showing the raw category id.
    // refresh() loads it on a page visit to an already-configured workspace,
    // which meant it was missing for the whole session after a fresh setup —
    // exactly the session in which the first batch gets reviewed. Loading it
    // here ties it to the thing that needs it. It is a no-op once cached.
    await loadCategories();
    const previousBatchID = lastBatch && lastBatch.id;
    try {
      const query =
        '?limit=' +
        PAGE_SIZE +
        '&offset=' +
        pageOffset +
        (filter ? '&filter=' + encodeURIComponent(filter) : '');
      const response = await fetch(apiBase(id) + '/batches/latest' + query);
      if (!response.ok) throw new Error('batch failed');
      const body = await response.json();
      lastBatch = body.batch || null;
      lastCandidates = Array.isArray(body.candidates) ? body.candidates : [];
      batchTotal = Number(body.total) || 0;
      filteredTotal = Number(body.filtered_total) || 0;
      filterCounts = body.counts || {};
      // An offset the server clamped past the end leaves an empty page with
      // rows still behind it. Snap back so the user is never stranded on
      // nothing with no way to tell why.
      if (lastCandidates.length === 0 && filteredTotal > 0 && pageOffset >= filteredTotal) {
        pageOffset = Math.max(0, (Math.ceil(filteredTotal / PAGE_SIZE) - 1) * PAGE_SIZE);
      }
    } catch (_) {
      lastBatch = null;
      lastCandidates = [];
      batchTotal = 0;
      filteredTotal = 0;
      filterCounts = {};
    }
    // A different batch invalidates every decision made against the old one.
    if ((lastBatch && lastBatch.id) !== previousBatchID) {
      selected = new Set();
      trashMarked = new Set();
    }
    dropIneligibleSelections();
    // A confirmation already on screen must survive a batch reload that did not
    // change the batch.
    //
    // The confirmation panel lives in its own host, outside the batch
    // container, so a repaint leaves it standing. Clearing pendingPreview
    // unconditionally therefore left the confirm button visible and enabled
    // while the approval behind it was gone: pressing it did nothing, sent
    // nothing, and said nothing. A loadBatch() still in flight when the
    // approval returned — from the scan that produced the batch — was enough
    // to trigger it, which made it intermittent and load-dependent.
    //
    // Only discard the approval when it no longer describes what is on screen.
    const approvalStillApplies =
      pendingPreview &&
      lastBatch &&
      pendingPreview.preview &&
      pendingPreview.preview.batch_id === lastBatch.id;
    if (!approvalStillApplies) {
      pendingPreview = null;
    }
    // This used to clear `selected` and `trashMarked` here too. That was
    // correct when a reload only ever meant "a new scan happened", but a reload
    // is now also how the user turns a page — so it silently discarded the
    // decisions of anyone reviewing more files than fit on one screen, which is
    // exactly the case paging exists to serve. The narrower rules above own it:
    // a changed batch clears everything, and a candidate that is no longer
    // eligible is dropped individually.
    renderBatch();
    refreshStats();
  }

  async function refresh() {
    const id = wsId();
    if (!id) return;
    try {
      const response = await fetch(apiBase(id));
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
    if (!workspaceId) return;
    void refresh().then(() => {
      // A deep link is honoured only after the first status is in: opening
      // beforehand would show a console that does not yet know whether the
      // workspace even has a folder (FR-117).
      applyUrlState({ source: 'deep-link' });
    });
  }

  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', () => init(), { once: true });
    } else {
      init();
    }
  }

  if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
    // Back over a deep-linked console closes it and stays in the workspace,
    // rather than navigating away from a page the user never left (FR-124).
    window.addEventListener('popstate', () => applyUrlState({ source: 'popstate' }));
    window.addEventListener('keydown', event => {
      if (!consoleOpen || event.key !== 'Escape') return;
      // A destructive confirmation owns Escape while it is on screen: the
      // answer to "are you sure" must be given, not dismissed by closing the
      // surface that asked (FR-120).
      if (pendingPreview || revokeConfirmed) return;
      event.preventDefault();
      close();
    });
  }

  // stationState answers, for the Map view, "does this workspace need me?".
  // The Janitor's setup card lives in the page body; a user working on the map
  // surface has no way to know a folder is still needed, and a workspace
  // awaiting setup looks exactly like a finished one. This lets the map show a
  // station without knowing anything about the Janitor's internals.
  // stationState answers, for the Map and the compact card, "does this
  // workspace need me?".
  //
  // The order is fixed and is the order of urgency (FR-95):
  //
  //   Needs attention → Setup needed → <N> ready for review → Paused → Watching
  //
  // It is a priority rather than a lookup because these overlap constantly: a
  // paused janitor can still have twelve files waiting, and a broken one can be
  // both. Reporting the lower state in either case would hide the reason the
  // user needs to come back.
  function stationState() {
    if (!lastStatus || !lastStatus.applies) return { applies: false };
    const readiness = lastStatus.readiness || {};
    const settings = lastStatus.settings || {};

    if (readiness.state === 'needs_attention') {
      return {
        applies: true,
        value: 'Needs attention',
        description: firstProblem(readiness) || 'File Janitor needs attention',
        tone: 'degraded'
      };
    }
    if (readiness.state === 'setup_required' || !isConfigured(lastStatus)) {
      return {
        applies: true,
        value: 'Setup needed',
        description: 'waiting for you to choose a folder to tidy',
        tone: 'attention'
      };
    }
    const pending = pendingCount();
    if (pending > 0) {
      return {
        applies: true,
        value: reviewCountLabel(pending),
        description: 'files are waiting for your decision',
        tone: 'attention'
      };
    }
    const folder = settings.root_path ? safeName(settings.root_path) : '';
    if (settings.paused) {
      return {
        applies: true,
        value: 'Paused',
        description: folder ? 'not watching ' + folder : 'automatic scanning paused',
        tone: 'idle'
      };
    }
    return {
      applies: true,
      value: 'Watching',
      description: folder ? 'tidying ' + folder : 'ready',
      tone: 'clear'
    };
  }

  function firstProblem(readiness) {
    const checks = readiness.checks || [];
    const bad = checks.find(check => check.status === 'failed' || check.status === 'degraded');
    return bad ? bad.message : '';
  }

  // focusSetup opens the console on its setup face.
  //
  // It used to scroll the page to an inline card and focus the folder field.
  // That worked only in Details mode: pressing the station from the Map
  // scrolled a surface the user could not see, so the press appeared to do
  // nothing at all. Opening in place over the Map is the whole point of the
  // console (FR-97, FR-98).
  function focusSetup(trigger) {
    return open({ source: 'setup', trigger });
  }

  // ---------------------------------------------------- setup wizard steps
  //
  // The blueprint's setup runs in the shared wizard; these renderers supply the
  // content of its Downloads-specific steps. They own no navigation, no
  // progress, and no readiness — the shell asks the server for all three.

  // ownsStep keeps these renderers scoped to this blueprint's steps: the
  // registry is keyed by step kind, and another blueprint's directory step is
  // not ours to draw.
  // Both adapter ids, because both are live.
  //
  // The Downloads preset's manifest names `downloads_janitor` and every
  // workspace already mid-setup persisted that in its wizard snapshot, so it
  // can never be renamed. The generic blueprint names the canonical
  // `file_janitor`. The server resolves either through the adapter alias, but
  // the STEP carries whichever id its manifest declared — so a renderer that
  // recognized only one silently drew nothing for the other.
  //
  // That is not a cosmetic failure. This step's renderer IS the folder picker:
  // without it the wizard falls back to its own generic "Approve and continue",
  // and a user creating a workspace from the generic blueprint has no way to
  // choose a folder at all.
  const SETUP_ADAPTER_IDS = ['file_janitor', 'downloads_janitor'];

  function ownsStep(step) {
    return SETUP_ADAPTER_IDS.includes(String(step?.adapter || ''));
  }

  // chooseFolder runs the native picker and confirms the result.
  //
  // The picker is the only way to choose a folder here: there is no editable
  // path field, so there is no value a typo — or a paste — can turn into a
  // grant the user did not mean to give. The confirmation is sent paused, so
  // approving a folder grants access and starts nothing.
  async function chooseFolder(ctx) {
    const id = wsId();
    if (!id) return;
    ctx.setError('');
    ctx.setBusy(true, 'Waiting for your folder choice…');
    try {
      const picker = await fetch('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: 'Choose the folder to tidy' })
      });
      const chosen = await picker.json().catch(() => ({}));
      if (!picker.ok || !chosen.success) {
        throw new Error(chosen.error || 'Could not open the folder picker.');
      }
      if (!chosen.selected || !chosen.path) {
        ctx.setBusy(false, '');
        return;
      }
      const response = await fetch(apiBase(id) + '/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: chosen.path, paused: true })
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiError = result.error || result;
        throw new Error((apiError && apiError.message) || 'Ori could not set up this folder.');
      }
      await refresh();
      ctx.setBusy(false, '');
      // The server decides whether that satisfied the step.
      await ctx.confirm();
    } catch (error) {
      ctx.setBusy(false, '');
      ctx.setError(error.message || 'Could not set up that folder.');
    }
  }

  function chosenRoot() {
    return (lastStatus && lastStatus.settings && lastStatus.settings.root_path) || '';
  }

  const directoryStepRenderer = {
    render(container, ctx) {
      if (!ownsStep(ctx.step)) return;
      const root = chosenRoot();
      const suggestion = (lastStatus && lastStatus.suggestion) || {};

      const field = el('div', 'dj-field');
      field.appendChild(el('span', 'dj-label', root ? 'Folder Ori will tidy' : 'Suggested folder'));
      const value = el('p', 'dj-sub', root || suggestion.suggested_path || '~/Downloads');
      value.id = 'downloadsJanitorWizardPath';
      field.appendChild(value);
      container.appendChild(field);

      const actions = el('div', 'dj-actions');
      const pick = button(
        root ? 'Choose a different folder…' : 'Choose folder…',
        'dj-btn dj-btn-primary',
        () => void chooseFolder(ctx)
      );
      pick.id = 'downloadsJanitorWizardPick';
      actions.appendChild(pick);
      container.appendChild(actions);
    },
    primaryLabel(ctx) {
      if (!ownsStep(ctx.step)) return '';
      // Until a folder exists there is nothing to approve; the step's own
      // button is the action.
      return chosenRoot() ? 'Continue' : 'Choose a folder to continue';
    },
    disablePrimary(ctx) {
      return ownsStep(ctx.step) && !chosenRoot();
    }
  };

  const automationStepRenderer = {
    render(container, ctx) {
      if (!ownsStep(ctx.step)) return;
      const settings = (lastStatus && lastStatus.settings) || {};
      const list = el('ul', 'dj-disclosure-list');
      [
        'Watches this folder for files you download or rename, and waits five minutes so a download can finish.',
        'Skips the ' + (settings.filing_root_name || 'Filed') + ' folder it files into.',
        'Runs one catch-up scan a day at ' +
          (settings.daily_scan_local_time || '09:00') +
          ' your local time.',
        'Every scan only proposes a batch. Nothing moves until you approve it.'
      ].forEach(text => list.appendChild(el('li', 'dj-disclosure-item', text)));
      container.appendChild(list);
    },
    primaryLabel(ctx) {
      return ownsStep(ctx.step) ? 'Turn this on' : '';
    }
  };

  function registerSetupSteps() {
    const wizard = window.SetupWizard;
    if (!wizard || typeof wizard.registerStepRenderer !== 'function') return;
    wizard.registerStepRenderer('directory', directoryStepRenderer);
    wizard.registerStepRenderer('automation_review', automationStepRenderer);
  }

  registerSetupSteps();

  // The panel and the wizard show the same workspace: when setup changes, the
  // panel re-reads rather than showing what was true before.
  if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
    document.addEventListener('ori:setup-status', () => {
      if (wsId()) void refresh();
    });
  }

  // The capability catalog's post-install "Set up File Janitor" action opens
  // this console. Registering the handler here rather than in the catalog keeps
  // the catalog capability-agnostic, and means the button is never a dead end.
  if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
    const registerOpen = () => {
      const catalog = typeof window === 'undefined' ? null : window.WorkspaceCapabilities;
      if (!catalog || typeof catalog.registerOpenHandler !== 'function') return;
      catalog.registerOpenHandler('file-janitor', trigger =>
        open({ source: 'capability-catalog', trigger })
      );
    };
    document.addEventListener('DOMContentLoaded', registerOpen);
    registerOpen();
  }

  // Action Center findings reach this console the same way Backlog items reach
  // theirs: the Action Center is a separate page, and it navigates to
  // /workspaces/{id}?panel=… . So the deep-link contract above IS the routing,
  // and applyUrlState validates the tab and the item on arrival — a
  // notification is data, and a stale one naming a candidate that has since
  // been filed must land the user on a working Review tab rather than on an
  // empty focus target (FR-90, FR-116, FR-117).
  //
  // In-page surfaces call open() directly; there is no event indirection,
  // because there is no in-page Action Center to dispatch one.

  const controller = {
    init,
    refresh,
    render,
    renderBatch,
    stationState,
    subscribe,
    open,
    close,
    focusSetup,
    isOpen: () => consoleOpen,
    activeTab: () => (consoleOpen ? consoleTab : ''),
    focusedItem: () => (consoleOpen ? consoleItem : ''),
    _applyUrlState: applyUrlState,
    _selectTab: selectTab,
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
    _setFilter: next => selectFilter(next),
    _reloadBatch: () => loadBatch(),
    // Clears the remembered workspace so a test can exercise a cold load, the
    // state a real page visit starts from.
    _forgetWorkspace: () => {
      workspaceId = '';
      lastStatus = null;
    },
    _setStatus: status => {
      lastStatus = status;
    },
    _openSettings: () => {
      settingsOpen = true;
      renderSettings();
    },
    _setHistory: actions => {
      historyActions = actions || [];
      historyLoaded = true;
      renderHistory();
    },
    // Returns the module to the state a freshly-loaded page starts in. The
    // console is a singleton with memory — the tab it last showed per
    // workspace — so a test that did not reset it would inherit the previous
    // test's tab and assert against a surface it never opened.
    _resetForTest: () => {
      consoleOpen = false;
      consoleTab = 'review';
      consoleItem = '';
      consoleTrigger = null;
      consoleOverlayId = '';
      consoleCloseButton = null;
      rememberedTab.clear();
      subscribers.clear();
      // The remembered workspace id too: a test that set one explicitly would
      // otherwise leak it into every later test, which reads as a mysterious
      // request to a workspace that test never mentioned.
      workspaceId = '';
      removalSummary = null;
      removalConfirming = false;
      removalCompanionChecked = false;
      pageOffset = 0;
      filter = '';
      historyActions = [];
      historyLoaded = false;
      settingsOpen = false;
      scanning = false;
      lastStatus = null;
      lastBatch = null;
      lastCandidates = [];
    },
    _select: id => {
      selected.add(id);
      updateSelectionSummary();
    },
    // The wizard step renderers, exposed so their content can be asserted
    // without standing up the whole dialog.
    _setupSteps: { directory: directoryStepRenderer, automation: automationStepRenderer }
  };

  window.FileJanitorConsole = controller;

  // DownloadsJanitorPanel is a compatibility alias for the same controller, not
  // a second one. It exists because setup callers still reach for the old name;
  // pointing it at the identical object means there is no way for the two to
  // diverge, and retiring it is a deletion rather than a migration.
  window.DownloadsJanitorPanel = controller;
})();
