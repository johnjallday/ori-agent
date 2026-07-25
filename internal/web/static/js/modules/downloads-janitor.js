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
//   • Configured — the folder in use plus a per-component readiness readout
//     with repair actions.
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

    card.appendChild(errorRegion());
    host.appendChild(card);
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

  async function refresh() {
    const id = wsId();
    if (!id) return;
    try {
      const response = await fetch(
        '/api/workspaces/' + encodeURIComponent(id) + '/downloads-janitor'
      );
      if (!response.ok) throw new Error('status failed');
      const body = await response.json();
      render(body.status);
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

  window.DownloadsJanitorPanel = { init, refresh, render, _confirmSetup: confirmSetup };
})();
