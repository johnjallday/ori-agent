// Plugin workspace list — the Plugins page banner (and the menu badge) for the
// changes the Workspace Directory's Plugins.json asks of this Mac. This module
// only normalizes the /api/plugins/workspace-list payload and builds HTML;
// plugins.js wires the buttons to the existing review, update, and uninstall
// flows, so nothing here installs anything.
(function (root) {
  'use strict';

  function text(value) {
    return typeof value === 'string' ? value : '';
  }

  function escapeHTML(value) {
    return String(value ?? '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  const KINDS = new Set(['install', 'switch', 'uninstall']);

  function normalize(payload) {
    const source = payload && typeof payload === 'object' ? payload : {};
    const pending = (Array.isArray(source.pending) ? source.pending : [])
      .filter(change => change && KINDS.has(change.kind) && text(change.name).trim())
      .map(change => ({
        kind: change.kind,
        name: text(change.name).trim(),
        listedVersion: text(change.listed_version),
        installedVersion: text(change.installed_version),
        source: text(change.source),
        format: text(change.format),
        direction: change.direction === 'update' ? 'update' : 'change',
        installable: change.installable === true,
        reason: text(change.reason),
        fingerprint: text(change.fingerprint),
        skipped: change.skipped === true
      }));
    return { pending, readError: text(source.read_error) };
  }

  // visibleChanges are the ones still offered: skipped changes stay quiet.
  function visibleChanges(state) {
    return (state?.pending || []).filter(change => !change.skipped);
  }

  // badgeCount is the number the Plugins menu item shows (0 hides it).
  function badgeCount(payload) {
    return visibleChanges(normalize(payload)).length;
  }

  function title(count) {
    return `Your Workspace Directory lists ${count} plugin change${count === 1 ? '' : 's'} for this Mac`;
  }

  function describe(change) {
    switch (change.kind) {
      case 'install':
        return `Install ${change.name}${change.listedVersion ? ' ' + change.listedVersion : ''}`;
      case 'switch':
        return `${change.direction === 'update' ? 'Update' : 'Change'} ${change.name} from ${change.installedVersion || 'this version'} to ${change.listedVersion || 'the listed version'}`;
      default:
        return `Uninstall ${change.name}${change.installedVersion ? ' ' + change.installedVersion : ''} (removed on another Mac)`;
    }
  }

  function actionLabel(change) {
    if (change.kind === 'install') return 'Review and install';
    if (change.kind === 'switch') return 'Review update';
    return 'Uninstall';
  }

  function rowHTML(change) {
    const data = `data-list-name="${escapeHTML(change.name)}"`;
    const action =
      change.kind !== 'uninstall' && !change.installable
        ? `<span class="small text-muted" data-list-reason>${escapeHTML(change.reason)}</span>`
        : `<button type="button" class="modern-btn modern-btn-primary btn-sm" data-list-action="${change.kind}" ${data}>${actionLabel(change)}</button>`;
    return `
      <div class="d-flex flex-wrap align-items-center justify-content-between gap-2 border-top py-2" data-list-row="${change.kind}" ${data}>
        <div style="min-width: 0; flex: 1 1 240px;">
          <div class="fw-semibold">${escapeHTML(describe(change))}</div>
          ${change.source ? `<div class="small text-muted text-break">${escapeHTML(change.source)}</div>` : ''}
        </div>
        <div class="d-flex flex-wrap gap-2 align-items-center">
          ${action}
          <button type="button" class="modern-btn modern-btn-secondary btn-sm" data-list-action="skip" ${data}>Skip on this Mac</button>
        </div>
      </div>`;
  }

  // renderBanner returns the banner's HTML, or '' when there is nothing to show.
  function renderBanner(state) {
    if (state?.readError) {
      return `<div class="small" data-list-error>${escapeHTML(state.readError)}</div>`;
    }
    const changes = visibleChanges(state);
    if (changes.length === 0) return '';
    return `
      <h2 class="h6 mb-2" data-list-title>${escapeHTML(title(changes.length))}</h2>
      <p class="small text-muted mb-2">Each change goes through the usual review. Nothing happens until you click.</p>
      ${changes.map(rowHTML).join('')}`;
  }

  root.PluginWorkspaceList = {
    normalize,
    visibleChanges,
    badgeCount,
    title,
    renderBanner,
    escapeHTML
  };
})(window);
