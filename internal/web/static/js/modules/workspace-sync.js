/**
 * Workspace Directory Sync Module
 * Detects mismatches between workspace folders on disk and the database,
 * then prompts the user to import or clean up via a modal.
 */
(function () {
  'use strict';

  var DISMISS_KEY = 'oriWorkspaceSyncDismissed';

  function escapeHtml(str) {
    var div = document.createElement('div');
    div.textContent = str || '';
    return div.innerHTML;
  }

  function hashString(str) {
    var hash = 0;
    for (var i = 0; i < str.length; i++) {
      hash = ((hash << 5) - hash) + str.charCodeAt(i);
      hash |= 0;
    }
    return String(hash);
  }

  function computeSyncHash(data) {
    var ids = [];
    if (data.unregistered) {
      data.unregistered.forEach(function (ws) { ids.push('u:' + ws.id); });
    }
    if (data.orphaned) {
      data.orphaned.forEach(function (ws) { ids.push('o:' + ws.id); });
    }
    ids.sort();
    return hashString(ids.join(','));
  }

  function updateApplyButton() {
    var btn = document.getElementById('syncApplyBtn');
    if (!btn) return;
    var anyChecked = document.querySelectorAll('#syncModalBody input[type="checkbox"]:checked').length > 0;
    btn.disabled = !anyChecked;
  }

  function renderSyncModal(data) {
    var body = document.getElementById('syncModalBody');
    if (!body) return;

    var html = '';

    if (data.unregistered && data.unregistered.length > 0) {
      html += '<div class="mb-3">';
      html += '<h6 style="color: var(--text-primary); margin-bottom: 4px;">Found on Disk</h6>';
      html += '<p style="color: var(--text-secondary); font-size: 13px; margin-bottom: 8px;">These workspace folders exist on disk but aren\'t in the database. Select to import.</p>';
      html += '<div class="list-group" style="gap: 4px;">';
      data.unregistered.forEach(function (ws) {
        html += '<label class="list-group-item d-flex align-items-start" style="background: var(--bg-secondary); border-color: var(--border-color); cursor: pointer; padding: 8px 12px;">';
        html += '<input type="checkbox" class="form-check-input me-2 mt-1 sync-import-cb" value="' + escapeHtml(ws.id) + '" checked>';
        html += '<div>';
        html += '<span style="color: var(--text-primary);">' + escapeHtml(ws.name) + '</span>';
        if (ws.path) {
          html += '<small class="d-block" style="color: var(--text-secondary); word-break: break-all;">' + escapeHtml(ws.path) + '</small>';
        }
        html += '</div>';
        html += '</label>';
      });
      html += '</div></div>';
    }

    if (data.orphaned && data.orphaned.length > 0) {
      html += '<div class="mb-3">';
      html += '<h6 style="color: var(--text-primary); margin-bottom: 4px;">Missing from Disk</h6>';
      html += '<p style="color: var(--text-secondary); font-size: 13px; margin-bottom: 8px;">These workspaces are in the database but their folders are gone. Select to clean up.</p>';
      html += '<div class="list-group" style="gap: 4px;">';
      data.orphaned.forEach(function (ws) {
        html += '<label class="list-group-item d-flex align-items-start" style="background: var(--bg-secondary); border-color: var(--border-color); cursor: pointer; padding: 8px 12px;">';
        html += '<input type="checkbox" class="form-check-input me-2 mt-1 sync-cleanup-cb" value="' + escapeHtml(ws.id) + '" checked>';
        html += '<div>';
        html += '<span style="color: var(--text-primary);">' + escapeHtml(ws.name) + '</span>';
        html += '</div>';
        html += '</label>';
      });
      html += '</div></div>';
    }

    body.innerHTML = html;

    // Update apply button state when checkboxes change.
    body.addEventListener('change', function (e) {
      if (e.target && e.target.type === 'checkbox') {
        updateApplyButton();
      }
    });
    updateApplyButton();
  }

  function dismissSync(syncHash) {
    if (syncHash) {
      localStorage.setItem(DISMISS_KEY, syncHash);
    }
  }

  async function applySyncActions(syncHash) {
    var importIds = Array.from(document.querySelectorAll('.sync-import-cb:checked')).map(function (cb) { return cb.value; });
    var cleanupIds = Array.from(document.querySelectorAll('.sync-cleanup-cb:checked')).map(function (cb) { return cb.value; });

    if (importIds.length === 0 && cleanupIds.length === 0) {
      dismissSync(syncHash);
      var modal = bootstrap.Modal.getInstance(document.getElementById('workspaceSyncModal'));
      if (modal) modal.hide();
      return;
    }

    var applyBtn = document.getElementById('syncApplyBtn');
    if (applyBtn) {
      applyBtn.disabled = true;
      applyBtn.textContent = 'Syncing...';
    }

    try {
      var resp = await fetch('/api/workspaces/sync', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ import: importIds, cleanup: cleanupIds })
      });
      if (!resp.ok) throw new Error('Sync failed');

      var result = await resp.json();
      dismissSync(syncHash);

      var modalEl = document.getElementById('workspaceSyncModal');
      var modal = bootstrap.Modal.getInstance(modalEl);
      if (modal) modal.hide();

      var parts = [];
      if (result.imported > 0) parts.push(result.imported + ' imported');
      if (result.cleaned > 0) parts.push(result.cleaned + ' cleaned up');
      if (window.Toast && parts.length > 0) {
        window.Toast.success('Workspace sync: ' + parts.join(', '));
      }

      // Reload to reflect changes.
      setTimeout(function () { window.location.reload(); }, 600);
    } catch (err) {
      console.error('[workspace-sync] Sync failed:', err);
      if (window.Toast) window.Toast.error('Failed to sync workspaces');
      if (applyBtn) {
        applyBtn.disabled = false;
        applyBtn.textContent = 'Apply Selected';
      }
    }
  }

  async function checkWorkspaceSync() {
    try {
      var resp = await fetch('/api/workspaces/sync-status');
      if (!resp.ok) return;
      var data = await resp.json();

      if (data.in_sync) return;

      // Check if user already dismissed this exact set of mismatches.
      var syncHash = computeSyncHash(data);
      var dismissed = localStorage.getItem(DISMISS_KEY);
      if (dismissed === syncHash) return;

      var modalEl = document.getElementById('workspaceSyncModal');
      if (!modalEl) return;

      renderSyncModal(data);

      var modal = new bootstrap.Modal(modalEl);
      modal.show();

      // Dismiss handler.
      var dismissBtn = document.getElementById('syncDismissBtn');
      if (dismissBtn) {
        dismissBtn.addEventListener('click', function () {
          dismissSync(syncHash);
        }, { once: true });
      }

      // Apply handler.
      var applyBtn = document.getElementById('syncApplyBtn');
      if (applyBtn) {
        applyBtn.addEventListener('click', function () {
          applySyncActions(syncHash);
        }, { once: true });
      }
    } catch (err) {
      console.error('[workspace-sync] Failed to check sync status:', err);
    }
  }

  // Run after workspace hub has loaded, with a short delay so it doesn't block initial render.
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      setTimeout(checkWorkspaceSync, 800);
    });
  } else {
    setTimeout(checkWorkspaceSync, 800);
  }
})();
