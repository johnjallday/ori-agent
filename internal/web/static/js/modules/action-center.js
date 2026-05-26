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
  const $ = (sel) => document.querySelector(sel);
  const $$ = (sel) => Array.from(document.querySelectorAll(sel));

  // --- State ---
  // Optional workspace scope from the URL (?workspace=<id>). When set, the list
  // is filtered to one workspace and a banner offers a way back to all findings.
  const workspaceFilter = new URLSearchParams(window.location.search).get('workspace') || '';
  // The single opportunity targeted by the open dismiss/snooze modal. Set
  // when the user clicks Dismiss/Snooze on a row; consumed when the modal's
  // primary action fires.
  let activeTarget = null;

  // --- Rendering ---
  const PRIORITY_CHIP_STYLES = {
    critical: 'background: #c0392b; color: white;',
    high: 'background: #e67e22; color: white;',
    medium: 'background: #f1c40f; color: #333;',
    low: 'background: #95a5a6; color: white;',
    '': 'background: var(--surface-2, #e0e0e0); color: var(--text-secondary, #666);',
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
    const labels = { new: 'New', snoozed: 'Snoozed', resolved: 'Resolved', dismissed: 'Dismissed' };
    const muted = s === 'resolved' || s === 'dismissed';
    return `<span style="padding: 0.15rem 0.5rem; border-radius: 999px; font-size: 0.7rem; opacity: ${muted ? 0.6 : 1}; border: 1px solid var(--border-color, #ddd);">${escapeHtml(labels[s] || s || '')}</span>`;
  }

  function rowHTML(item) {
    const unseen = !item.seen_at;
    const opened = `/workspaces/${encodeURIComponent(item.workspace_id)}`;
    return `
      <div class="action-center-row" data-ws="${escapeHtml(item.workspace_id)}" data-id="${escapeHtml(item.id)}"
           style="display: grid; grid-template-columns: auto 1fr auto; gap: 0.75rem 1rem; padding: 0.75rem; border-bottom: 1px solid var(--border-color, #eee); align-items: start; ${unseen ? 'background: rgba(99,102,241,0.04);' : ''}">
        <div style="display: flex; flex-direction: column; gap: 0.25rem; align-items: flex-start; min-width: 80px;">
          ${priorityChip(item.priority)}
          ${statusChip(item.status)}
        </div>
        <div>
          <div style="display: flex; gap: 0.5rem; align-items: baseline;">
            ${unseen ? '<span aria-label="unread" title="Unread" style="width: 8px; height: 8px; background: #6366f1; border-radius: 50%; display: inline-block; flex: 0 0 auto;"></span>' : ''}
            <strong style="font-weight: ${unseen ? 600 : 500}; cursor: pointer;" data-action="open">${escapeHtml(item.title || 'Untitled finding')}</strong>
          </div>
          <div style="font-size: 0.85rem; color: var(--text-secondary, #555); margin-top: 0.25rem;">${escapeHtml(item.summary || '')}</div>
          <div style="font-size: 0.75rem; color: var(--text-secondary, #888); margin-top: 0.4rem;">
            <a href="${opened}" style="color: inherit; text-decoration: underline;">${escapeHtml(item.workspace_name || item.workspace_id)}</a>
            · ${fmtTime(item.updated_at)}
          </div>
        </div>
        <div style="display: flex; flex-direction: column; gap: 0.25rem; min-width: 96px;">
          <button class="btn btn-sm btn-outline-success" data-action="resolve" title="Mark resolved">Resolve</button>
          <button class="btn btn-sm btn-outline-secondary" data-action="snooze" title="Snooze">Snooze</button>
          <button class="btn btn-sm btn-outline-danger" data-action="dismiss" title="Dismiss">Dismiss</button>
        </div>
      </div>
    `;
  }

  function render(items) {
    const list = $('#action-center-list');
    const empty = $('#action-center-empty');
    if (!list) return;
    if (!items.length) {
      // Restore the empty-state markup (we removed it on first render).
      list.innerHTML = '';
      if (empty) {
        list.appendChild(empty);
        empty.style.display = '';
      }
      return;
    }
    if (empty) empty.style.display = 'none';
    list.innerHTML = items.map(rowHTML).join('');
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
    const match = items.find((i) => i.workspace_id === workspaceFilter);
    const name = (match && match.workspace_name) || workspaceFilter;
    el.style.display = '';
    el.innerHTML = `Showing findings for <strong>${escapeHtml(name)}</strong>. <a href="/action-center">Show all findings</a>`;
  }

  function setStatus(msg, kind) {
    const el = $('#action-center-status');
    if (!el) return;
    el.textContent = msg || '';
    el.style.color = kind === 'error' ? 'var(--danger-color, #c0392b)' : 'var(--text-secondary, #666)';
  }

  // --- API ---
  async function fetchList() {
    const status = $('#action-center-status-filter').value || '';
    const sort = $('#action-center-sort').value || 'priority';
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

  async function reload() {
    try {
      setStatus('Loading...');
      const data = await fetchList();
      const items = data.items || [];
      render(items);
      renderFilterBanner(items);
      setStatus(data.total ? `${data.total} finding${data.total === 1 ? '' : 's'}` : '');
    } catch (e) {
      setStatus(`Failed to load: ${e.message}`, 'error');
    }
  }

  // --- Event wiring ---
  function handleRowClick(evt) {
    const row = evt.target.closest('.action-center-row');
    if (!row) return;
    const workspaceID = row.dataset.ws;
    const opportunityID = row.dataset.id;
    const action = evt.target.closest('[data-action]')?.dataset.action;
    if (!action) return;

    if (action === 'open') {
      // Mark seen via the GET-single endpoint (it sets SeenAt). Then take
      // the user to the source workspace.
      fetch(`/api/action-center/opportunities/${encodeURIComponent(workspaceID)}/${encodeURIComponent(opportunityID)}`)
        .finally(() => {
          window.location.href = `/workspaces/${encodeURIComponent(workspaceID)}`;
        });
      return;
    }

    activeTarget = { workspaceID, opportunityID };
    if (action === 'resolve') {
      callMutation(workspaceID, opportunityID, 'resolve')
        .then(reload)
        .catch((e) => setStatus(`Resolve failed: ${e.message}`, 'error'));
      return;
    }
    if (action === 'dismiss') {
      const modalEl = $('#action-center-dismiss-modal');
      if (typeof bootstrap !== 'undefined' && modalEl) {
        bootstrap.Modal.getOrCreateInstance(modalEl).show();
      } else {
        // Fallback: dismiss with no reason if Bootstrap isn't available.
        callMutation(workspaceID, opportunityID, 'dismiss').then(reload);
      }
      return;
    }
    if (action === 'snooze') {
      const modalEl = $('#action-center-snooze-modal');
      if (typeof bootstrap !== 'undefined' && modalEl) {
        bootstrap.Modal.getOrCreateInstance(modalEl).show();
      } else {
        callMutation(workspaceID, opportunityID, 'snooze', { preset: 'next_week' }).then(reload);
      }
    }
  }

  function wireModals() {
    const dismissBtn = $('#action-center-dismiss-confirm');
    if (dismissBtn) {
      dismissBtn.addEventListener('click', async () => {
        if (!activeTarget) return;
        const reasonEl = document.querySelector('input[name="action-center-dismiss-reason"]:checked');
        const reason = reasonEl ? reasonEl.value : '';
        try {
          await callMutation(activeTarget.workspaceID, activeTarget.opportunityID, 'dismiss', reason ? { reason } : null);
          if (typeof bootstrap !== 'undefined') {
            bootstrap.Modal.getInstance($('#action-center-dismiss-modal'))?.hide();
          }
          await reload();
        } catch (e) {
          setStatus(`Dismiss failed: ${e.message}`, 'error');
        }
      });
    }

    $$('#action-center-snooze-modal [data-snooze-preset]').forEach((btn) => {
      btn.addEventListener('click', async () => {
        if (!activeTarget) return;
        try {
          await callMutation(activeTarget.workspaceID, activeTarget.opportunityID, 'snooze', {
            preset: btn.dataset.snoozePreset,
          });
          if (typeof bootstrap !== 'undefined') {
            bootstrap.Modal.getInstance($('#action-center-snooze-modal'))?.hide();
          }
          await reload();
        } catch (e) {
          setStatus(`Snooze failed: ${e.message}`, 'error');
        }
      });
    });

    const customGo = $('#action-center-snooze-custom-go');
    if (customGo) {
      customGo.addEventListener('click', async () => {
        if (!activeTarget) return;
        const raw = $('#action-center-snooze-custom').value;
        if (!raw) return;
        const until = new Date(raw).toISOString();
        try {
          await callMutation(activeTarget.workspaceID, activeTarget.opportunityID, 'snooze', { until });
          if (typeof bootstrap !== 'undefined') {
            bootstrap.Modal.getInstance($('#action-center-snooze-modal'))?.hide();
          }
          await reload();
        } catch (e) {
          setStatus(`Snooze failed: ${e.message}`, 'error');
        }
      });
    }
  }

  function init() {
    const list = $('#action-center-list');
    if (!list) return; // Action Center page not loaded.
    list.addEventListener('click', handleRowClick);
    $('#action-center-status-filter')?.addEventListener('change', reload);
    $('#action-center-sort')?.addEventListener('change', reload);
    $('#action-center-refresh')?.addEventListener('click', reload);
    wireModals();
    reload();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
