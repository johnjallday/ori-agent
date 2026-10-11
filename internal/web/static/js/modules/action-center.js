/**
 * action-center.js
 *
 * Cross-workspace triage view for mission opportunities. Talks to
 * /api/action-center/opportunities. Standalone IIFE — no shared state with
 * the workspace-detail module so this page can load independently.
 */

(function () {
  'use strict';

  // --- DOM helpers ---
  const $ = sel => document.querySelector(sel);
  const $$ = sel => Array.from(document.querySelectorAll(sel));

  // --- State ---
  // Optional workspace scope from the URL (?workspace=<id>). When set, the list
  // is filtered to one workspace and a banner offers a way back to all findings.
  const workspaceFilter = new URLSearchParams(window.location.search).get('workspace') || '';
  // The single opportunity targeted by the open dismiss/snooze modal. Set
  // when the user clicks Dismiss/Snooze on a row; consumed when the modal's
  // primary action fires.
  let activeTarget = null;
  let loadGeneration = 0;
  let lastLoaded = null;
  let workspaceName = workspaceFilter;

  // --- Rendering ---
  const PRIORITY_CHIP_STYLES = {
    critical: 'background: #c0392b; color: white;',
    high: 'background: #96400c; color: white;',
    medium: 'background: #f1c40f; color: #333;',
    low: 'background: var(--bg-secondary); color: var(--text-primary);',
    '': 'background: var(--bg-secondary); color: var(--text-primary);'
  };

  function escapeHtml(s) {
    if (s == null) return '';
    return String(s)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#39;');
  }

  function fmtTime(iso) {
    if (!iso) return '';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    const now = Date.now();
    const diff = now - d.getTime();
    if (diff < 60_000) return 'just now';
    if (diff < 3_600_000) return `${Math.round(diff / 60_000)}m ago`;
    if (diff < 86_400_000) return `${Math.round(diff / 3_600_000)}h ago`;
    if (diff < 7 * 86_400_000) return `${Math.round(diff / 86_400_000)}d ago`;
    return d.toLocaleDateString();
  }

  function priorityChip(p) {
    const style = PRIORITY_CHIP_STYLES[p] || PRIORITY_CHIP_STYLES[''];
    const label = p ? p.toUpperCase() : 'UNRANKED';
    return `<span style="padding: 0.15rem 0.5rem; border-radius: 999px; font-size: 0.7rem; font-weight: 600; letter-spacing: 0.04em; ${style}">${escapeHtml(label)}</span>`;
  }

  function statusChip(s) {
    const labels = {
      new: 'New',
      snoozed: 'Snoozed',
      resolved: 'Resolved',
      dismissed: 'Dismissed',
      planned: 'Planned'
    };
    const muted = s === 'resolved' || s === 'dismissed' || s === 'planned';
    return `<span style="padding: 0.15rem 0.5rem; border-radius: 999px; font-size: 0.7rem; color: var(${muted ? '--text-secondary' : '--text-primary'}); border: 1px solid var(--border-color, #ddd);">${escapeHtml(labels[s] || s || '')}</span>`;
  }

  function assistantSourceHTML(item) {
    if (item.source_type !== 'assistant_suggestion') return '';
    const label = item.source_label ? ` · ${escapeHtml(item.source_label)}` : '';
    const confidence = item.confidence ? ` · ${escapeHtml(item.confidence)} confidence` : '';
    const sourceURL = String(item.source_url || '').trim();
    const safeSourceURL = /^\/workspaces\/[a-z0-9-]+\/assistant$/.test(sourceURL)
      ? escapeHtml(sourceURL)
      : '';
    const evidence = item.evidence
      ? `<details style="margin-top: 0.35rem;"><summary>Evidence</summary><div style="white-space: pre-line; margin-top: 0.25rem;">${escapeHtml(item.evidence)}</div></details>`
      : '';
    const sourceLink = safeSourceURL
      ? `<a href="${safeSourceURL}" style="display: inline-block; margin-top: 0.35rem;">Open source</a>`
      : '';
    return `<div style="font-size: 0.75rem; color: var(--text-secondary, #777); margin-top: 0.35rem;"><strong>Assistant suggestion</strong>${label}${confidence}${evidence}${sourceLink}</div>`;
  }

  // A "Daily Brief ready" item links to the Daily Brief station in My HQ, where
  // the brief is read. The link is rendered only for exactly the station
  // address (/workspaces/<slug>?station=daily-brief, the shape
  // daily-brief-station.js builds); any other source_url on any other item
  // gets no link from here.
  const DAILY_BRIEF_STATION_LINK = /^\/workspaces\/[a-z0-9][a-z0-9-]{0,79}\?station=daily-brief$/;

  function dailyBriefLinkHTML(item) {
    const sourceURL = String(item.source_url || '').trim();
    if (!DAILY_BRIEF_STATION_LINK.test(sourceURL)) return '';
    return `<div style="font-size: 0.75rem; margin-top: 0.35rem;"><a href="${escapeHtml(sourceURL)}">Open Daily Brief</a></div>`;
  }

  function backlogActionHTML(item) {
    // A planned finding already has a linked Backlog item — offer a direct
    // deep link to it (Group 5's ?panel=backlog&task= contract) instead of
    // re-showing the capture action; the endpoint is idempotent either way,
    // but a direct link is the clearer affordance once linked (FR26, 29).
    if (item.status === 'planned' && item.linked_task_id) {
      const slug = String(item.linked_workspace_slug || item.workspace_slug || '').trim();
      if (!slug)
        return '<span class="action-center-unavailable">Backlog workspace unavailable</span>';
      const href = `/workspaces/${encodeURIComponent(slug)}?panel=backlog&task=${encodeURIComponent(item.linked_task_id)}`;
      return `<a class="modern-btn modern-btn-secondary action-center-backlog" href="${href}" title="View in Backlog">View in Backlog</a>`;
    }
    return `<button class="modern-btn modern-btn-secondary action-center-backlog" data-action="add-to-backlog" title="Add to Backlog">Add to Backlog</button>`;
  }

  function rowHTML(item) {
    const unseen = !item.seen_at;
    const workspaceSlug = String(item.workspace_slug || '').trim();
    const opened = workspaceSlug ? `/workspaces/${encodeURIComponent(workspaceSlug)}` : '';
    const title = escapeHtml(item.title || 'Untitled finding');
    const workspaceName = escapeHtml(item.workspace_name || item.workspace_id);
    return `
      <div class="action-center-row" data-ws="${escapeHtml(item.workspace_id)}" data-workspace-slug="${escapeHtml(workspaceSlug)}" data-id="${escapeHtml(item.id)}"
           ${unseen ? 'data-unread="true"' : ''}>
        <div class="action-center-chips">
          ${priorityChip(item.priority)}
          ${statusChip(item.status)}
        </div>
        <div class="action-center-copy">
          <div class="action-center-title">
            ${unseen ? '<span role="img" aria-label="Unread" title="Unread" style="width: 8px; height: 8px; background: #6366f1; border-radius: 50%; display: inline-block; flex: 0 0 auto;"></span>' : ''}
            ${opened ? `<a href="${opened}" data-action="open">${title}</a>` : `<strong>${title}</strong>`}
          </div>
          <div style="font-size: 0.85rem; color: var(--text-secondary, #555); margin-top: 0.25rem;">${escapeHtml(item.summary || '')}</div>
          ${assistantSourceHTML(item)}
          ${dailyBriefLinkHTML(item)}
          <div style="font-size: 0.75rem; color: var(--text-secondary, #888); margin-top: 0.4rem;">
            ${opened ? `<a href="${opened}" style="color: inherit; text-decoration: underline;">${workspaceName}</a>` : `<span>${workspaceName} · Workspace unavailable</span>`}
            · ${escapeHtml(fmtTime(item.updated_at))}
          </div>
        </div>
        <div class="action-center-actions">
          ${backlogActionHTML(item)}
          <button class="btn btn-sm btn-outline-success" data-action="resolve" title="Mark resolved">Resolve</button>
          <button class="btn btn-sm btn-outline-secondary" data-action="snooze" title="Snooze">Snooze</button>
          <button class="btn btn-sm btn-outline-danger" data-action="dismiss" title="Dismiss">Dismiss</button>
        </div>
      </div>
    `;
  }

  function emptyHTML(status) {
    const scoped = Boolean(workspaceFilter);
    const labels = {
      new: 'new',
      snoozed: 'snoozed',
      resolved: 'resolved',
      dismissed: 'dismissed',
      planned: 'planned'
    };
    const title = !status
      ? `No active findings${scoped ? ' in this workspace' : ''}`
      : status === 'all' && !scoped
        ? 'No findings'
        : 'No matching findings';
    const detail = !status
      ? 'Nothing is waiting for triage in this view. Snoozed findings return when they are due.'
      : status === 'all'
        ? `There are no findings${scoped ? ' in this workspace' : ' across your workspaces'}.`
        : `There are no ${labels[status] || 'matching'} findings${scoped ? ' in this workspace' : ''}.`;
    const clear = scoped
      ? '<a href="/action-center?status=all" class="modern-btn modern-btn-secondary">Clear filters</a>'
      : status !== 'all'
        ? '<button type="button" data-action="clear-filters" class="modern-btn modern-btn-secondary">Show all findings</button>'
        : '';
    return `<h3>${title}</h3><p>${detail}</p><div class="action-center-empty-actions">${clear}<a href="/" class="modern-btn modern-btn-secondary">Open Workspace Map</a></div>`;
  }

  function render(items, status, mutationRow = null) {
    const list = $('#action-center-list');
    if (!list) return;
    // Only restore focus if the replaced list owned it. Filter/refresh controls
    // keep focus, and background responses never pull it away from another area.
    const focusedRow =
      document.activeElement?.closest?.('.action-center-row') ||
      (document.activeElement === document.body ? mutationRow : null);
    const rows = $$('.action-center-row');
    const index = rows.indexOf(focusedRow);
    list.innerHTML = items.map(rowHTML).join('');
    list.hidden = items.length === 0;
    const empty = $('#action-center-empty');
    if (empty) {
      empty.innerHTML = emptyHTML(status);
      empty.hidden = items.length > 0;
    }
    if (index >= 0) {
      const nextRows = $$('.action-center-row');
      const next =
        nextRows.find(
          row =>
            row.dataset.id === focusedRow.dataset.id && row.dataset.ws === focusedRow.dataset.ws
        ) || nextRows[Math.min(index, nextRows.length - 1)];
      (next?.querySelector('a, button') || $('#action-center-status'))?.focus();
    }
  }

  // --- Home library cards (library-manager-notifications FR 13) ---
  // Derived per request from each Home's scan digest and ready suggestions.
  // A card only links to the Home's suggestions shelf; there is nothing to
  // dismiss or resolve here, and it disappears once nothing is ready.
  const LIBRARY_SHELF_ROUTE = /^\/workspaces\/[^/?#\s]+\/assistant#projectLibraryProposals$/;

  function libraryCount(value) {
    return Number.isInteger(value) && value > 0 ? value : 0;
  }

  function libraryCardHTML(card) {
    if (!card || typeof card !== 'object') return '';
    const route = typeof card.route === 'string' ? card.route : '';
    if (!LIBRARY_SHELF_ROUTE.test(route)) return '';
    const fresh = libraryCount(card.new);
    const ready = libraryCount(card.activatable);
    const review = libraryCount(card.ready_proposals);
    if (!ready && !review) return '';
    const parts = [];
    if (fresh) parts.push(`${fresh} new`);
    if (ready) parts.push(`${ready} ready to set up`);
    if (review) parts.push(`${review} ${review === 1 ? 'suggestion' : 'suggestions'} to review`);
    const name = String(card.home_name || '').trim() || 'Home';
    const scanned = card.scanned_at ? ` · scanned ${fmtTime(card.scanned_at)}` : '';
    const partial = card.coverage === 'partial' ? ' · partial scan' : '';
    return `
      <article class="action-center-library-card" data-home-id="${escapeHtml(card.home_id)}">
        <div class="action-center-library-copy">
          <strong>${escapeHtml(name)}</strong>
          <span class="action-center-library-counts">${escapeHtml(parts.join(' · ') + scanned + partial)}</span>
        </div>
        <a class="modern-btn modern-btn-secondary action-center-library-open" href="${escapeHtml(route)}" aria-label="${escapeHtml(`Open ${name} suggestions shelf`)}">Open shelf</a>
      </article>
    `;
  }

  function renderLibrary(cards) {
    const section = $('#action-center-library');
    const container = $('#action-center-library-cards');
    if (!section || !container) return;
    const visible = (Array.isArray(cards) ? cards : []).filter(
      card => !workspaceFilter || (card && card.home_id === workspaceFilter)
    );
    const html = visible.map(libraryCardHTML).filter(Boolean);
    container.innerHTML = html.join('');
    section.hidden = html.length === 0;
  }

  async function fetchLibrary() {
    try {
      const resp = await fetch('/api/action-center/library');
      if (!resp.ok) return [];
      const data = await resp.json();
      return Array.isArray(data.items) ? data.items : [];
    } catch (_) {
      return []; // Library cards are optional; findings still load.
    }
  }

  function renderFilterBanner(items) {
    const el = $('#action-center-filter-banner');
    if (!el) return;
    if (!workspaceFilter) {
      el.style.display = 'none';
      return;
    }
    // Prefer the human-readable workspace name from a returned item; fall back
    // to the id when the filtered workspace currently has no findings.
    const match = items.find(i => i.workspace_id === workspaceFilter);
    if (match?.workspace_name) workspaceName = match.workspace_name;
    el.style.display = '';
    el.innerHTML = `Showing findings for <strong>${escapeHtml(workspaceName)}</strong>. <a href="/action-center">Show all workspaces</a>`;
  }

  function setStatus(msg, kind) {
    const el = $('#action-center-status');
    if (!el) return;
    el.textContent = msg || '';
    el.style.color = kind === 'error' ? 'var(--text-primary)' : 'var(--text-secondary, #666)';
    el.style.fontWeight = kind === 'error' ? '600' : '';
  }

  // Success message with a clickable link to the created/linked item, so the
  // user doesn't have to hunt for it after Add to Backlog (FR26, 29).
  function setStatusWithLink(msg, href, linkLabel) {
    const el = $('#action-center-status');
    if (!el) return;
    el.style.color = 'var(--text-secondary, #666)';
    el.style.fontWeight = '';
    el.innerHTML = `${escapeHtml(msg)} <a href="${href}">${escapeHtml(linkLabel)}</a>`;
  }

  // --- API ---
  async function fetchList(status, sort) {
    const params = new URLSearchParams();
    if (status) params.set('status', status);
    if (sort) params.set('sort', sort);
    if (workspaceFilter) params.set('workspace', workspaceFilter);
    const url = `/api/action-center/opportunities${params.toString() ? '?' + params.toString() : ''}`;
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`status ${resp.status}`);
    return resp.json();
  }

  async function callMutation(workspaceID, opportunityID, action, body) {
    const url = `/api/action-center/opportunities/${encodeURIComponent(workspaceID)}/${encodeURIComponent(opportunityID)}/${action}`;
    const opts = { method: 'POST', headers: { 'Content-Type': 'application/json' } };
    if (body) opts.body = JSON.stringify(body);
    const resp = await fetch(url, opts);
    if (!resp.ok) {
      const errBody = await resp.json().catch(() => ({}));
      throw new Error(errBody.message || `status ${resp.status}`);
    }
    return resp.json();
  }

  function setHidden(selector, hidden) {
    const el = $(selector);
    if (el) el.hidden = hidden;
  }

  function showRetainedRows() {
    const el = $('#action-center-retained');
    if (!el) return;
    el.hidden = !lastLoaded?.count;
    el.textContent = lastLoaded?.count
      ? `Showing last-loaded findings: ${lastLoaded.label}${workspaceFilter ? ' · this workspace' : ' · all workspaces'}. Current filters have not been refreshed.`
      : '';
  }

  async function reload(mutationRow = null) {
    const generation = ++loadGeneration;
    const status = $('#action-center-status-filter').value || '';
    const sort = $('#action-center-sort').value || 'priority';
    const label = $('#action-center-status-filter').selectedOptions?.[0]?.textContent || 'Active';
    void fetchLibrary().then(cards => {
      if (generation === loadGeneration) renderLibrary(cards);
    });
    setHidden('#action-center-empty', true);
    setHidden('#action-center-error', true);
    showRetainedRows();
    $('#action-center-results')?.setAttribute('aria-busy', 'true');
    renderFilterBanner([]);
    setStatus('Loading findings…');
    try {
      const data = await fetchList(status, sort);
      if (generation !== loadGeneration) return false;
      if (!Array.isArray(data?.items) || data.items.some(item => !item || typeof item !== 'object'))
        throw new Error('Invalid findings response');
      const items = data.items;
      lastLoaded = { count: items.length, label };
      setHidden('#action-center-retained', true);
      setStatus(`${items.length} finding${items.length === 1 ? '' : 's'}`);
      render(items, status, mutationRow);
      renderFilterBanner(items);
      return true;
    } catch (_) {
      if (generation !== loadGeneration) return false;
      setStatus('Findings could not be refreshed.', 'error');
      setHidden('#action-center-error', false);
      return false;
    } finally {
      if (generation === loadGeneration)
        $('#action-center-results')?.setAttribute('aria-busy', 'false');
    }
  }

  function showMutationModal(el) {
    // Bootstrap ignores hide() during its opening transition. A fast mutation
    // must wait for the public shown event before trying to close the dialog.
    activeTarget.modalReady = new Promise(resolve => {
      el.addEventListener('shown.bs.modal', resolve, { once: true });
    });
    bootstrap.Modal.getOrCreateInstance(el).show();
  }

  // Wait for the modal to release its focus trap before restoring the row's
  // trigger. The subsequent render can then move focus if that row disappears.
  async function closeMutationModal(selector, trigger) {
    const el = $(selector);
    const restore = el?.contains(document.activeElement);
    if (typeof bootstrap !== 'undefined' && el) {
      const modal = bootstrap.Modal.getInstance(el);
      if (modal && el.classList.contains('show')) {
        await new Promise(resolve => {
          el.addEventListener('hidden.bs.modal', resolve, { once: true });
          modal.hide();
        });
      }
    }
    if (restore && trigger?.isConnected) trigger.focus();
  }

  async function handleMutation(target, action, body, modalSelector, modalButton) {
    const trigger = modalButton || target.trigger;
    const mutationRow =
      trigger === document.activeElement ? target.trigger?.closest('.action-center-row') : null;
    if (trigger) trigger.disabled = true;
    try {
      await callMutation(target.workspaceID, target.opportunityID, action, body);
      if (modalSelector) {
        await target.modalReady;
        await closeMutationModal(modalSelector, target.trigger);
      }
      await reload(mutationRow);
    } catch (e) {
      setStatus(
        `${action === 'resolve' ? 'Resolve' : action === 'dismiss' ? 'Dismiss' : 'Snooze'} failed: ${e.message}`,
        'error'
      );
    } finally {
      if (trigger) {
        trigger.disabled = false;
        if (mutationRow && trigger.isConnected && document.activeElement === document.body)
          trigger.focus();
      }
    }
  }

  // Add to Backlog (PRD workspace-backlog FR26-29): non-destructive like
  // Resolve, so it fires directly (no confirm modal) — but shows an explicit
  // pending state on the clicked button and a success link to the created
  // item, since unlike Resolve it produces a new record worth navigating to.
  async function handleAddToBacklog(workspaceID, opportunityID, triggerBtn) {
    const originalLabel = triggerBtn ? triggerBtn.textContent : '';
    const mutationRow =
      triggerBtn === document.activeElement ? triggerBtn?.closest('.action-center-row') : null;
    if (triggerBtn) {
      triggerBtn.disabled = true;
      triggerBtn.textContent = 'Adding…';
    }
    try {
      const data = await callMutation(workspaceID, opportunityID, 'add-to-backlog');
      if (!(await reload(mutationRow))) return; // Keep the reload failure and Retry visible.
      const item = data && data.item;
      const workspaceSlug = String(data?.workspace_slug || '').trim();
      if (item && item.id && workspaceSlug) {
        setStatusWithLink(
          'Added to backlog.',
          `/workspaces/${encodeURIComponent(workspaceSlug)}?panel=backlog&task=${encodeURIComponent(item.id)}`,
          'Open item'
        );
      } else {
        setStatus('Added to backlog.');
      }
    } catch (e) {
      setStatus(`Add to Backlog failed: ${e.message}`, 'error');
    } finally {
      if (triggerBtn) {
        triggerBtn.disabled = false;
        triggerBtn.textContent = originalLabel;
        if (mutationRow && triggerBtn.isConnected && document.activeElement === document.body)
          triggerBtn.focus();
      }
    }
  }

  // --- Event wiring ---
  function handleRowClick(evt) {
    const row = evt.target.closest('.action-center-row');
    if (!row) return;
    const workspaceID = row.dataset.ws;
    const opportunityID = row.dataset.id;
    const triggerBtn = evt.target.closest('[data-action]');
    const action = triggerBtn?.dataset.action;
    if (!action) return;

    if (action === 'open') {
      // Keep native keyboard, modified-click and middle-click navigation.
      // keepalive lets the seen request finish after ordinary navigation too.
      if (evt.type === 'auxclick' && evt.button !== 1) return;
      void fetch(
        `/api/action-center/opportunities/${encodeURIComponent(workspaceID)}/${encodeURIComponent(opportunityID)}`,
        { keepalive: true }
      ).catch(() => {});
      return;
    }
    if (evt.type === 'auxclick') return;

    activeTarget = { workspaceID, opportunityID, trigger: triggerBtn };
    if (action === 'add-to-backlog') {
      void handleAddToBacklog(workspaceID, opportunityID, triggerBtn);
      return;
    }
    if (action === 'resolve') {
      void handleMutation(activeTarget, 'resolve');
      return;
    }
    if (action === 'dismiss') {
      const modalEl = $('#action-center-dismiss-modal');
      if (typeof bootstrap !== 'undefined' && modalEl) {
        showMutationModal(modalEl);
      } else {
        // Fallback: dismiss with no reason if Bootstrap isn't available.
        void handleMutation(activeTarget, 'dismiss');
      }
      return;
    }
    if (action === 'snooze') {
      const modalEl = $('#action-center-snooze-modal');
      if (typeof bootstrap !== 'undefined' && modalEl) {
        showMutationModal(modalEl);
      } else {
        void handleMutation(activeTarget, 'snooze', { preset: 'next_week' });
      }
    }
  }

  function wireModals() {
    for (const selector of ['#action-center-dismiss-modal', '#action-center-snooze-modal']) {
      const modal = $(selector);
      modal?.addEventListener('hidden.bs.modal', () => {
        const trigger = activeTarget?.trigger;
        if (
          trigger?.isConnected &&
          (document.activeElement === document.body || modal.contains(document.activeElement))
        )
          trigger.focus();
      });
    }
    const dismissBtn = $('#action-center-dismiss-confirm');
    if (dismissBtn) {
      dismissBtn.addEventListener('click', async () => {
        if (!activeTarget) return;
        const reasonEl = document.querySelector(
          'input[name="action-center-dismiss-reason"]:checked'
        );
        const reason = reasonEl ? reasonEl.value : '';
        await handleMutation(
          activeTarget,
          'dismiss',
          reason ? { reason } : null,
          '#action-center-dismiss-modal',
          dismissBtn
        );
      });
    }

    $$('#action-center-snooze-modal [data-snooze-preset]').forEach(btn => {
      btn.addEventListener('click', async () => {
        if (!activeTarget) return;
        await handleMutation(
          activeTarget,
          'snooze',
          { preset: btn.dataset.snoozePreset },
          '#action-center-snooze-modal',
          btn
        );
      });
    });

    const customGo = $('#action-center-snooze-custom-go');
    if (customGo) {
      customGo.addEventListener('click', async () => {
        if (!activeTarget) return;
        const raw = $('#action-center-snooze-custom').value;
        if (!raw) return;
        const date = new Date(raw);
        if (Number.isNaN(date.getTime())) return;
        await handleMutation(
          activeTarget,
          'snooze',
          { until: date.toISOString() },
          '#action-center-snooze-modal',
          customGo
        );
      });
    }
  }

  function init() {
    const list = $('#action-center-list');
    if (!list) return; // Action Center page not loaded.
    list.addEventListener('click', handleRowClick);
    list.addEventListener('auxclick', handleRowClick);
    const statusFilter = $('#action-center-status-filter');
    // Clear a workspace scope and status together via the empty state's link.
    if (new URLSearchParams(window.location.search).get('status') === 'all')
      statusFilter.value = 'all';
    statusFilter?.addEventListener('change', () => void reload());
    $('#action-center-sort')?.addEventListener('change', () => void reload());
    $('#action-center-refresh')?.addEventListener('click', () => void reload());
    $('#action-center-retry')?.addEventListener('click', () => {
      $('#action-center-refresh')?.focus();
      void reload();
    });
    $('#action-center-empty')?.addEventListener('click', evt => {
      if (!evt.target.closest('[data-action="clear-filters"]')) return;
      statusFilter.value = 'all';
      statusFilter.focus();
      void reload();
    });
    wireModals();
    reload();
  }

  // Test-only export surface (mirrors workspace-map.js/workspace-hub-smart-
  // input.js's window.X pattern) — purely additive, changes no runtime
  // behavior. init() still fires automatically below.
  window.ActionCenter = {
    rowHTML,
    statusChip,
    priorityChip,
    backlogActionHTML,
    assistantSourceHTML,
    dailyBriefLinkHTML,
    handleAddToBacklog,
    escapeHtml,
    fmtTime,
    libraryCardHTML,
    renderLibrary,
    emptyHTML,
    render,
    reload,
    handleRowClick
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
