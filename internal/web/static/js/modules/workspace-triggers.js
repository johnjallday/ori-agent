/**
 * workspace-triggers.js
 *
 * Controls the Event Triggers tab on the workspace detail page. Talks to the
 * trigger HTTP endpoints:
 *   GET/POST   /api/workspaces/{id}/triggers
 *   GET/PUT/DELETE /api/workspaces/{id}/triggers/{tid}
 *   POST       .../triggers/{tid}/enable | /disable | /regenerate-token | /test-fire
 *   GET        .../triggers/{tid}/fires
 *
 * Standalone IIFE module (matches workspace-mission.js) so it can be reviewed
 * independently of the large workspace-detail bundle. Renders the trigger list
 * and builds the create/edit modal dynamically to keep the template small.
 */

(function () {
  'use strict';

  const SEL = {
    tab: '#workspace-detail-config-triggers-tab',
    pane: '#workspace-detail-config-triggers-pane',
    list: '#workspace-detail-triggers-list',
    empty: '#workspace-detail-triggers-empty',
    actions: '#workspace-detail-triggers-actions',
    statusText: '#workspace-detail-triggers-status-text',
    refresh: '#workspace-detail-triggers-refresh'
  };

  let loaded = false;

  function $(s) {
    return document.querySelector(s);
  }

  function getWorkspaceId() {
    if (window.currentWorkspaceId) return window.currentWorkspaceId;
    const parts = window.location.pathname.split('/');
    if (parts[1] === 'workspaces' && parts[2]) return parts[2];
    return '';
  }

  function setStatus(msg, kind) {
    const el = $(SEL.statusText);
    if (!el) return;
    el.textContent = msg || '';
    el.style.color = kind === 'error' ? 'var(--danger-color, #c0392b)' : '';
  }

  function esc(s) {
    const d = document.createElement('div');
    d.textContent = s == null ? '' : String(s);
    return d.innerHTML;
  }

  function fmtTime(iso) {
    if (!iso) return '—';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '—';
    return d.toLocaleString();
  }

  async function api(method, path, body) {
    const opts = { method, headers: {} };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    const res = await fetch(path, opts);
    if (res.status === 204) return null;
    const text = await res.text();
    let data = null;
    try {
      data = text ? JSON.parse(text) : null;
    } catch (_) {
      data = { error: text };
    }
    if (!res.ok) {
      const msg = (data && (data.error || data.message)) || `HTTP ${res.status}`;
      throw new Error(msg);
    }
    return data;
  }

  function actionSummary(t) {
    if (!t.action) return '';
    if (t.action.kind === 'mission_run') return 'Runs the workspace goal';
    if (t.action.kind === 'task_prompt') {
      return `Creates a task for ${esc(t.action.agent || 'an agent')}`;
    }
    return esc(t.action.kind);
  }

  function typeLabel(t) {
    return t.type === 'webhook' ? 'Webhook' : 'File watch';
  }

  function renderRow(t) {
    const failing = !!t.last_error;
    const statusBadge = !t.enabled
      ? '<span class="badge bg-secondary">Disabled</span>'
      : failing
        ? '<span class="badge bg-danger">Failing</span>'
        : '<span class="badge bg-success">Active</span>';

    const sourceDetail =
      t.type === 'webhook'
        ? `<code class="workspace-detail-trigger-url" title="Click copy to get the full URL">${esc(maskUrl(t.webhook_url))}</code>`
        : `<span class="text-muted">${esc((t.file_watch && t.file_watch.path) || '')}${t.file_watch && t.file_watch.glob ? ' / ' + esc(t.file_watch.glob) : ''}</span>`;

    return `
      <div class="modern-card workspace-detail-trigger-row" data-trigger-id="${esc(t.id)}" style="padding: 0.85rem 1rem; margin-bottom: 0.6rem;">
        <div style="display:flex; align-items:flex-start; justify-content:space-between; gap:1rem;">
          <div style="min-width:0;">
            <div style="display:flex; align-items:center; gap:0.5rem; flex-wrap:wrap;">
              <strong>${esc(t.name)}</strong>
              <span class="badge bg-light text-dark">${typeLabel(t)}</span>
              ${statusBadge}
            </div>
            <div class="workspace-detail-settings-field-hint" style="margin-top:0.25rem;">${actionSummary(t)}</div>
            <div style="margin-top:0.35rem;">${sourceDetail}</div>
            <div class="workspace-detail-settings-field-hint" style="margin-top:0.35rem;">
              Last fired: ${fmtTime(t.last_fired_at)} · Fires: ${t.fire_count || 0}${t.failure_count ? ' · Failures: ' + t.failure_count : ''}
            </div>
            ${failing ? `<div style="margin-top:0.25rem; color:var(--danger-color,#c0392b); font-size:0.82rem;">${esc(t.last_error)}</div>` : ''}
          </div>
          <div style="display:flex; flex-direction:column; gap:0.35rem; align-items:flex-end; flex-shrink:0;">
            <div class="form-check form-switch" style="margin:0;">
              <input class="form-check-input" type="checkbox" data-act="toggle" ${t.enabled ? 'checked' : ''} title="Enable / disable">
            </div>
            <div style="display:flex; gap:0.3rem; flex-wrap:wrap; justify-content:flex-end;">
              ${t.type === 'webhook' ? '<button class="btn btn-sm btn-outline-secondary" data-act="copy-url" type="button">Copy URL</button>' : ''}
              <button class="btn btn-sm btn-outline-secondary" data-act="test" type="button">Test fire</button>
              <button class="btn btn-sm btn-outline-secondary" data-act="history" type="button">History</button>
              <button class="btn btn-sm btn-outline-secondary" data-act="edit" type="button">Edit</button>
              <button class="btn btn-sm btn-outline-danger" data-act="delete" type="button">Delete</button>
            </div>
          </div>
        </div>
        <div class="workspace-detail-trigger-history" style="display:none; margin-top:0.6rem;"></div>
      </div>`;
  }

  function maskUrl(url) {
    if (!url) return '';
    // Mask everything after /api/hooks/ except the last 6 chars.
    const marker = '/api/hooks/';
    const i = url.indexOf(marker);
    if (i < 0) return url;
    const token = url.slice(i + marker.length);
    if (token.length <= 6) return url;
    return url.slice(0, i + marker.length) + '…' + token.slice(-6);
  }

  function render(triggers) {
    const list = $(SEL.list);
    const empty = $(SEL.empty);
    const actions = $(SEL.actions);
    if (!list) return;
    if (!triggers.length) {
      list.innerHTML = '';
      if (empty) empty.style.display = '';
      if (actions) actions.style.display = 'none';
      return;
    }
    if (empty) empty.style.display = 'none';
    if (actions) actions.style.display = '';
    list.innerHTML = triggers.map(renderRow).join('');
  }

  let cache = [];

  async function load() {
    const wsId = getWorkspaceId();
    if (!wsId) return;
    setStatus('Loading triggers…');
    try {
      const data = await api('GET', `/api/workspaces/${wsId}/triggers`);
      cache = (data && data.triggers) || [];
      render(cache);
      setStatus('');
      loaded = true;
    } catch (e) {
      setStatus('Failed to load triggers: ' + e.message, 'error');
    }
  }

  function findTrigger(id) {
    return cache.find(t => t.id === id);
  }

  // --- row actions ---

  async function onListClick(ev) {
    const row = ev.target.closest('[data-trigger-id]');
    const addBtn = ev.target.closest('[data-trigger-add]');
    if (addBtn) {
      openModal(null, addBtn.getAttribute('data-trigger-add'));
      return;
    }
    if (!row) return;
    const id = row.getAttribute('data-trigger-id');
    const actEl = ev.target.closest('[data-act]');
    const act = actEl && actEl.getAttribute('data-act');
    if (!act) return;
    const wsId = getWorkspaceId();
    const t = findTrigger(id);

    try {
      if (act === 'toggle') {
        const enable = ev.target.checked;
        await api(
          'POST',
          `/api/workspaces/${wsId}/triggers/${id}/${enable ? 'enable' : 'disable'}`
        );
        await load();
      } else if (act === 'delete') {
        if (!window.confirm(`Delete trigger "${t ? t.name : id}"? This cannot be undone.`)) return;
        await api('DELETE', `/api/workspaces/${wsId}/triggers/${id}`);
        await load();
      } else if (act === 'copy-url') {
        if (t && t.webhook_url) {
          await copy(t.webhook_url);
          setStatus('Webhook URL copied to clipboard.');
        }
      } else if (act === 'test') {
        setStatus('Test firing…');
        const res = await api('POST', `/api/workspaces/${wsId}/triggers/${id}/test-fire`);
        const fire = res && res.fire;
        setStatus(fire && fire.error ? 'Test fire failed: ' + fire.error : 'Test fire dispatched.');
        await load();
      } else if (act === 'history') {
        await toggleHistory(row, id);
      } else if (act === 'edit') {
        openModal(t, t ? t.type : 'webhook');
      }
    } catch (e) {
      setStatus(e.message, 'error');
      await load();
    }
  }

  async function toggleHistory(row, id) {
    const panel = row.querySelector('.workspace-detail-trigger-history');
    if (!panel) return;
    if (panel.style.display !== 'none') {
      panel.style.display = 'none';
      return;
    }
    const wsId = getWorkspaceId();
    panel.innerHTML =
      '<div class="workspace-detail-settings-field-hint">Loading fire history…</div>';
    panel.style.display = '';
    try {
      const data = await api('GET', `/api/workspaces/${wsId}/triggers/${id}/fires`);
      const fires = (data && data.fires) || [];
      if (!fires.length) {
        panel.innerHTML = '<div class="workspace-detail-settings-field-hint">No fires yet.</div>';
        return;
      }
      panel.innerHTML = fires
        .slice()
        .reverse()
        .map(f => {
          const ref = f.run_id ? `run ${esc(f.run_id)}` : f.task_id ? `task ${esc(f.task_id)}` : '';
          const err = f.error
            ? `<span style="color:var(--danger-color,#c0392b);">${esc(f.error)}</span>`
            : ref;
          return `<div style="display:flex; justify-content:space-between; gap:1rem; padding:0.2rem 0; border-top:1px solid var(--border-color,#eee); font-size:0.82rem;">
            <span>${fmtTime(f.fired_at)} — ${esc(f.summary || '')}</span>
            <span class="text-muted">${err}</span>
          </div>`;
        })
        .join('');
    } catch (e) {
      panel.innerHTML = `<div style="color:var(--danger-color,#c0392b);">${esc(e.message)}</div>`;
    }
  }

  async function copy(text) {
    try {
      await navigator.clipboard.writeText(text);
    } catch (_) {
      const ta = document.createElement('textarea');
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      document.body.removeChild(ta);
    }
  }

  // --- create / edit modal ---

  function missionEnabled() {
    // The mission module owns the enable switch; read it if present.
    const el = document.querySelector('#workspace-detail-mission-enabled');
    return !!(el && el.checked);
  }

  function modalHTML(t, type) {
    const isEdit = !!t;
    const a = (t && t.action) || { kind: missionEnabled() ? 'mission_run' : 'task_prompt' };
    const fw = (t && t.file_watch) || { events: ['create'] };
    const events = fw.events || ['create'];
    const evCheck = e => (events.indexOf(e) >= 0 ? 'checked' : '');
    const curlUrl = (t && t.webhook_url) || 'PASTE_URL_AFTER_CREATE';

    return `
      <div class="modal-content">
        <div class="modal-header">
          <h5 class="modal-title">${isEdit ? 'Edit trigger' : type === 'webhook' ? 'Add webhook trigger' : 'Watch a folder'}</h5>
          <button type="button" class="btn-close" data-trg-close></button>
        </div>
        <div class="modal-body">
          <input type="hidden" id="trg-type" value="${esc(type)}">
          <div class="mb-3">
            <label class="form-label">Name</label>
            <input id="trg-name" class="form-control" value="${esc(t ? t.name : '')}" placeholder="${type === 'webhook' ? 'e.g. pr-opened' : 'e.g. invoice-drop'}">
          </div>

          ${
            type === 'webhook'
              ? `
          <div class="mb-3">
            <label class="form-label">Shared secret <span class="text-muted">(optional)</span></label>
            <input id="trg-secret" class="form-control" type="password" placeholder="${t && t.has_secret ? '•••••• (unchanged)' : 'Sent as X-Ori-Webhook-Secret'}">
            <div class="workspace-detail-settings-field-hint">If set, callers must send this in the <code>X-Ori-Webhook-Secret</code> header.</div>
          </div>
          ${
            isEdit && t.webhook_url
              ? `<div class="mb-3">
                   <label class="form-label">Webhook URL</label>
                   <div style="display:flex; gap:0.4rem;">
                     <input class="form-control" readonly value="${esc(t.webhook_url)}">
                     <button class="btn btn-outline-secondary" type="button" data-trg-copy>Copy</button>
                     <button class="btn btn-outline-secondary" type="button" data-trg-regen title="Issue a new URL; the old one stops working">Regenerate</button>
                   </div>
                   <details style="margin-top:0.5rem;"><summary class="workspace-detail-settings-field-hint">curl example</summary>
                     <pre style="white-space:pre-wrap; font-size:0.78rem;">curl -X POST ${esc(curlUrl)} \\
  -H 'Content-Type: application/json' \\
  -d '{"hello":"world"}'</pre>
                   </details>
                 </div>`
              : ''
          }`
              : `
          <div class="mb-3">
            <label class="form-label">Folder to watch (absolute path)</label>
            <input id="trg-path" class="form-control" value="${esc(fw.path || '')}" placeholder="/Users/you/Downloads/invoices">
          </div>
          <div class="mb-3">
            <label class="form-label">Filename filter <span class="text-muted">(optional glob)</span></label>
            <input id="trg-glob" class="form-control" value="${esc(fw.glob || '')}" placeholder="*.pdf">
          </div>
          <div class="mb-3">
            <label class="form-label">React to</label>
            <div style="display:flex; gap:1rem; flex-wrap:wrap;">
              ${['create', 'modify', 'remove', 'rename']
                .map(
                  e =>
                    `<label class="form-check-label" style="display:flex; gap:0.3rem; align-items:center;">
                      <input class="form-check-input trg-event" type="checkbox" value="${e}" ${evCheck(e)}> ${e}
                    </label>`
                )
                .join('')}
            </div>
          </div>`
          }

          <hr>
          <div class="mb-3">
            <label class="form-label">When it fires…</label>
            <select id="trg-action-kind" class="form-select">
              <option value="mission_run" ${a.kind === 'mission_run' ? 'selected' : ''} ${missionEnabled() ? '' : 'disabled'}>Run the workspace goal${missionEnabled() ? '' : ' (enable goal automation first)'}</option>
              <option value="task_prompt" ${a.kind === 'task_prompt' ? 'selected' : ''}>Run a specific task</option>
            </select>
          </div>
          <div id="trg-task-fields" style="${a.kind === 'task_prompt' ? '' : 'display:none;'}">
            <div class="mb-3">
              <label class="form-label">Assign task to agent</label>
              <input id="trg-agent" class="form-control" value="${esc(a.agent || '')}" placeholder="agent name">
            </div>
            <div class="mb-3">
              <label class="form-label">Task prompt</label>
              <textarea id="trg-prompt" class="form-control" rows="3" placeholder="e.g. File the dropped invoice and update the ledger note.">${esc(a.prompt || '')}</textarea>
            </div>
          </div>

          <div class="mb-2">
            <label class="form-label">Debounce <span class="text-muted">(seconds, optional)</span></label>
            <input id="trg-debounce" class="form-control" type="number" min="0" value="${t && t.debounce_seconds ? t.debounce_seconds : ''}" placeholder="2">
            <div class="workspace-detail-settings-field-hint">Bursts of events within this window coalesce into one run.</div>
          </div>

          <div class="form-check form-switch">
            <input class="form-check-input" type="checkbox" id="trg-enabled" ${!t || t.enabled ? 'checked' : ''}>
            <label class="form-check-label" for="trg-enabled">Enabled</label>
          </div>

          <div id="trg-modal-error" style="color:var(--danger-color,#c0392b); margin-top:0.6rem; font-size:0.85rem;"></div>
        </div>
        <div class="modal-footer">
          <button type="button" class="btn btn-outline-secondary" data-trg-close>Cancel</button>
          <button type="button" class="btn btn-primary" data-trg-save>${isEdit ? 'Save' : 'Create'}</button>
        </div>
      </div>`;
  }

  let modalEl = null;

  function openModal(t, type) {
    closeModal();
    modalEl = document.createElement('div');
    modalEl.className = 'modal-backdrop-custom';
    modalEl.style.cssText =
      'position:fixed; inset:0; background:rgba(0,0,0,0.4); z-index:1080; display:flex; align-items:flex-start; justify-content:center; padding:3rem 1rem; overflow:auto;';
    const dialog = document.createElement('div');
    dialog.className = 'modal-dialog';
    dialog.style.cssText = 'max-width:560px; width:100%;';
    dialog.innerHTML = modalHTML(t, type);
    modalEl.appendChild(dialog);
    document.body.appendChild(modalEl);

    modalEl.addEventListener('click', e => {
      if (e.target === modalEl) closeModal();
      if (e.target.closest('[data-trg-close]')) closeModal();
      if (e.target.closest('[data-trg-save]')) saveModal(t);
      if (e.target.closest('[data-trg-copy]') && t)
        copy(t.webhook_url).then(() => setStatus('Webhook URL copied.'));
      if (e.target.closest('[data-trg-regen]') && t) regenerate(t);
    });
    const kindSel = dialog.querySelector('#trg-action-kind');
    if (kindSel) {
      kindSel.addEventListener('change', () => {
        const tf = dialog.querySelector('#trg-task-fields');
        if (tf) tf.style.display = kindSel.value === 'task_prompt' ? '' : 'none';
      });
    }
  }

  function closeModal() {
    if (modalEl && modalEl.parentNode) modalEl.parentNode.removeChild(modalEl);
    modalEl = null;
  }

  function modalError(msg) {
    const el = modalEl && modalEl.querySelector('#trg-modal-error');
    if (el) el.textContent = msg || '';
  }

  function readModal() {
    const q = s => modalEl.querySelector(s);
    const type = q('#trg-type').value;
    const kind = q('#trg-action-kind').value;
    const action = { kind };
    if (kind === 'task_prompt') {
      action.agent = q('#trg-agent').value.trim();
      action.prompt = q('#trg-prompt').value.trim();
    }
    const debounceRaw = q('#trg-debounce').value.trim();
    const payload = {
      name: q('#trg-name').value.trim(),
      type,
      enabled: q('#trg-enabled').checked,
      action,
      debounce_seconds: debounceRaw ? parseInt(debounceRaw, 10) : 0
    };
    if (type === 'webhook') {
      const secret = q('#trg-secret') ? q('#trg-secret').value : '';
      payload.webhook = {};
      if (secret) payload.webhook.secret = secret;
    } else {
      const events = Array.from(modalEl.querySelectorAll('.trg-event'))
        .filter(c => c.checked)
        .map(c => c.value);
      payload.file_watch = {
        path: q('#trg-path').value.trim(),
        glob: q('#trg-glob').value.trim(),
        events
      };
    }
    return payload;
  }

  async function saveModal(existing) {
    const wsId = getWorkspaceId();
    let payload;
    try {
      payload = readModal();
    } catch (e) {
      modalError(e.message);
      return;
    }
    if (!payload.name) {
      modalError('Name is required.');
      return;
    }
    try {
      if (existing) {
        // PUT: webhook secret only sent when changed; omit empty webhook object.
        if (payload.webhook && !payload.webhook.secret) delete payload.webhook;
        await api('PUT', `/api/workspaces/${wsId}/triggers/${existing.id}`, payload);
        setStatus('Trigger updated.');
      } else {
        const created = await api('POST', `/api/workspaces/${wsId}/triggers`, payload);
        if (created && created.type === 'webhook' && created.webhook_url) {
          await copy(created.webhook_url);
          setStatus('Trigger created. Webhook URL copied to clipboard.');
        } else {
          setStatus('Trigger created.');
        }
      }
      closeModal();
      await load();
    } catch (e) {
      modalError(e.message);
    }
  }

  async function regenerate(t) {
    if (!window.confirm('Issue a new webhook URL? The current URL will stop working immediately.'))
      return;
    const wsId = getWorkspaceId();
    try {
      const updated = await api(
        'POST',
        `/api/workspaces/${wsId}/triggers/${t.id}/regenerate-token`
      );
      if (updated && updated.webhook_url) await copy(updated.webhook_url);
      setStatus('New webhook URL generated and copied.');
      closeModal();
      await load();
    } catch (e) {
      setStatus(e.message, 'error');
    }
  }

  function init() {
    const list = $(SEL.list);
    if (!list) return;
    list.addEventListener('click', onListClick);
    const empty = $(SEL.empty);
    if (empty) empty.addEventListener('click', onListClick);
    const actions = $(SEL.actions);
    if (actions) actions.addEventListener('click', onListClick);
    const refresh = $(SEL.refresh);
    if (refresh) refresh.addEventListener('click', load);

    // Lazy-load when the Triggers tab is first shown.
    const tab = $(SEL.tab);
    if (tab) {
      tab.addEventListener('shown.bs.tab', () => {
        if (!loaded) load();
      });
      // If the tab is already active on page load, load now.
      if (tab.classList.contains('active')) load();
    }
    document.addEventListener('keydown', e => {
      if (e.key === 'Escape' && modalEl) closeModal();
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
