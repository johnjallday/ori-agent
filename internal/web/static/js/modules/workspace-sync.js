/**
 * Workspace Directory Sync Module
 * Detects mismatches between workspace folders on disk and the database,
 * then prompts the user to import, relink, or clean up via a modal.
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
      data.orphaned.forEach(function (ws) { ids.push('o:' + ws.id + ':' + (ws.path || '')); });
    }
    ids.sort();
    return hashString(ids.join(','));
  }

  function getSyncActions() {
    var actions = {
      import: Array.from(document.querySelectorAll('.sync-import-cb:checked')).map(function (cb) {
        return cb.value;
      }),
      cleanup: [],
      locate: [],
      recreate: [],
      hasChanges: false,
      isValid: true
    };

    Array.from(document.querySelectorAll('.sync-missing-row')).forEach(function (row) {
      var workspaceId = row.getAttribute('data-workspace-id');
      var actionEl = row.querySelector('.sync-missing-action');
      if (!workspaceId || !actionEl) return;

      var action = actionEl.value;
      if (action === 'cleanup') {
        actions.cleanup.push(workspaceId);
        actions.hasChanges = true;
        return;
      }

      if (action === 'recreate') {
        actions.recreate.push(workspaceId);
        actions.hasChanges = true;
        return;
      }

      if (action === 'locate') {
        actions.hasChanges = true;
        var pathInput = row.querySelector('.sync-locate-path');
        var path = pathInput && pathInput.value ? pathInput.value.trim() : '';
        if (!path) {
          actions.isValid = false;
          return;
        }
        actions.locate.push({ id: workspaceId, path: path });
      }
    });

    if (actions.import.length > 0) {
      actions.hasChanges = true;
    }

    return actions;
  }

  function updateApplyButton() {
    var btn = document.getElementById('syncApplyBtn');
    if (!btn) return;

    var actions = getSyncActions();
    btn.disabled = !actions.hasChanges || !actions.isValid;
  }

  function updateMissingRowState(row) {
    if (!row) return;

    var actionEl = row.querySelector('.sync-missing-action');
    var locateControls = row.querySelector('.sync-locate-controls');
    var pathInput = row.querySelector('.sync-locate-path');
    var browseBtn = row.querySelector('.sync-locate-browse');
    var isLocate = !!actionEl && actionEl.value === 'locate';

    if (locateControls) {
      locateControls.style.display = isLocate ? '' : 'none';
    }
    if (pathInput) {
      pathInput.disabled = !isLocate;
    }
    if (browseBtn) {
      browseBtn.disabled = !isLocate;
    }
  }

  async function browseMissingWorkspaceFolder(row, button) {
    if (!row) return;

    var originalText = button ? button.textContent : 'Browse';
    if (button) {
      button.disabled = true;
      button.textContent = 'Opening...';
    }

    try {
      var response = await fetch('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: 'Locate Workspace Folder'
        })
      });

      var result = await response.json().catch(function () { return {}; });
      if (!response.ok || !result.success) {
        throw new Error(result.error || 'Failed to open folder picker');
      }

      if (result.selected && result.path) {
        var actionEl = row.querySelector('.sync-missing-action');
        var pathInput = row.querySelector('.sync-locate-path');
        if (actionEl) {
          actionEl.value = 'locate';
        }
        if (pathInput) {
          pathInput.value = result.path;
          pathInput.focus();
        }
        updateMissingRowState(row);
        updateApplyButton();
      }
    } catch (err) {
      console.error('[workspace-sync] Failed to browse for workspace folder:', err);
      if (window.Toast && typeof window.Toast.error === 'function') {
        window.Toast.error(err.message || 'Failed to open folder picker');
      }
    } finally {
      if (button) {
        button.disabled = false;
        button.textContent = originalText;
      }
    }
  }

  function renderSyncModal(data) {
    var body = document.getElementById('syncModalBody');
    if (!body) return;

    var html = '';

    if (data.unregistered && data.unregistered.length > 0) {
      html += '<div class="mb-3">';
      html += '<h6 style="color: var(--text-primary); margin-bottom: 4px;">Found on Disk</h6>';
      html += '<p style="color: var(--text-secondary); font-size: 13px; margin-bottom: 8px;">These workspace folders exist on disk but are not in the database. Select the ones you want to import.</p>';
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
      html += '<p style="color: var(--text-secondary); font-size: 13px; margin-bottom: 8px;">These workspaces are still in the database, but their folders are missing. Keep them as missing, locate the moved folder, recreate the workspace folder from the database, or remove the database entry.</p>';
      html += '<div class="list-group" style="gap: 8px;">';
      data.orphaned.forEach(function (ws) {
        html += '<div class="list-group-item sync-missing-row" data-workspace-id="' + escapeHtml(ws.id) + '" style="background: var(--bg-secondary); border-color: var(--border-color); padding: 10px 12px;">';
        html += '<div class="d-flex flex-column flex-md-row align-items-md-start justify-content-between" style="gap: 8px;">';
        html += '<div style="min-width: 0;">';
        html += '<div style="color: var(--text-primary);">' + escapeHtml(ws.name) + '</div>';
        if (ws.path) {
          html += '<small class="d-block" style="color: var(--text-secondary); word-break: break-all;">Last known folder: ' + escapeHtml(ws.path) + '</small>';
        }
        html += '</div>';
        html += '<select class="form-select form-select-sm sync-missing-action" style="max-width: 180px; background: var(--bg-tertiary); color: var(--text-primary); border-color: var(--border-color);">';
        html += '<option value="keep" selected>Keep Missing</option>';
        html += '<option value="locate">Locate Folder</option>';
        html += '<option value="recreate">Recreate Folder</option>';
        html += '<option value="cleanup">Remove from DB</option>';
        html += '</select>';
        html += '</div>';
        html += '<div class="sync-locate-controls mt-2" style="display: none;">';
        html += '<div class="d-flex flex-column flex-md-row" style="gap: 8px;">';
        html += '<input type="text" class="form-control form-control-sm sync-locate-path" value="' + escapeHtml(ws.path || '') + '" placeholder="Select the current workspace folder" disabled>';
        html += '<button type="button" class="modern-btn modern-btn-secondary sync-locate-browse" disabled>Browse</button>';
        html += '</div>';
        html += '<small class="d-block mt-1" style="color: var(--text-secondary);">If the folder exists but is missing <code>workspace.json</code>, Ori will recreate the workspace scaffold there.</small>';
        html += '</div>';
        html += '<small class="d-block mt-2" style="color: var(--text-secondary);">Recreate Folder rebuilds <code>workspace.json</code>, <code>files/</code>, <code>notes/</code>, and note markdown files at the last known path. Uploaded file contents are not restored.</small>';
        html += '</div>';
      });
      html += '</div></div>';
    }

    body.innerHTML = html;

    body.onchange = function (e) {
      if (e.target && e.target.classList.contains('sync-missing-action')) {
        updateMissingRowState(e.target.closest('.sync-missing-row'));
      }
      updateApplyButton();
    };

    body.oninput = function (e) {
      if (e.target && e.target.classList.contains('sync-locate-path')) {
        updateApplyButton();
      }
    };

    body.onclick = function (e) {
      var browseBtn = e.target && e.target.closest ? e.target.closest('.sync-locate-browse') : null;
      if (browseBtn) {
        browseMissingWorkspaceFolder(browseBtn.closest('.sync-missing-row'), browseBtn);
      }
    };

    Array.from(document.querySelectorAll('.sync-missing-row')).forEach(updateMissingRowState);
    updateApplyButton();
  }

  function dismissSync(syncHash) {
    if (syncHash) {
      localStorage.setItem(DISMISS_KEY, syncHash);
    }
  }

  async function applySyncActions(syncHash) {
    var actions = getSyncActions();

    if (!actions.hasChanges) {
      dismissSync(syncHash);
      var emptyModal = bootstrap.Modal.getInstance(document.getElementById('workspaceSyncModal'));
      if (emptyModal) emptyModal.hide();
      return;
    }

    if (!actions.isValid) {
      if (window.Toast && typeof window.Toast.warning === 'function') {
        window.Toast.warning('Choose a folder path before applying locate actions.');
      }
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
        body: JSON.stringify({
          import: actions.import,
          cleanup: actions.cleanup,
          locate: actions.locate,
          recreate: actions.recreate
        })
      });

      var result = await resp.json().catch(function () { return {}; });
      if (!resp.ok) {
        throw new Error(result.error || 'Sync failed');
      }

      var changed = (result.imported || 0) > 0 || (result.cleaned || 0) > 0 || (result.located || 0) > 0 || (result.recreated || 0) > 0;
      var warnings = Array.isArray(result.warnings) ? result.warnings : [];

      if (!changed && warnings.length > 0) {
        if (window.Toast && typeof window.Toast.error === 'function') {
          window.Toast.error(warnings[0]);
        }
        if (applyBtn) {
          applyBtn.disabled = false;
          applyBtn.textContent = 'Apply Changes';
        }
        return;
      }

      dismissSync(syncHash);

      var modalEl = document.getElementById('workspaceSyncModal');
      var modal = bootstrap.Modal.getInstance(modalEl);
      if (modal) modal.hide();

      var parts = [];
      if ((result.imported || 0) > 0) parts.push(result.imported + ' imported');
      if ((result.located || 0) > 0) parts.push(result.located + ' relinked');
      if ((result.recreated || 0) > 0) parts.push(result.recreated + ' recreated');
      if ((result.cleaned || 0) > 0) parts.push(result.cleaned + ' removed');

      if (window.Toast && typeof window.Toast.success === 'function' && parts.length > 0) {
        window.Toast.success('Workspace sync: ' + parts.join(', '));
      }
      if (window.Toast && typeof window.Toast.warning === 'function' && warnings.length > 0) {
        window.Toast.warning(warnings[0]);
      }

      if (changed) {
        setTimeout(function () { window.location.reload(); }, 600);
      }
    } catch (err) {
      console.error('[workspace-sync] Sync failed:', err);
      if (window.Toast && typeof window.Toast.error === 'function') {
        window.Toast.error(err.message || 'Failed to sync workspaces');
      }
      if (applyBtn) {
        applyBtn.disabled = false;
        applyBtn.textContent = 'Apply Changes';
      }
    }
  }

  async function checkWorkspaceSync() {
    try {
      var resp = await fetch('/api/workspaces/sync-status');
      if (!resp.ok) return;
      var data = await resp.json();

      if (data.in_sync) return;

      var syncHash = computeSyncHash(data);
      var dismissed = localStorage.getItem(DISMISS_KEY);
      if (dismissed === syncHash) return;

      var modalEl = document.getElementById('workspaceSyncModal');
      if (!modalEl) return;

      renderSyncModal(data);

      var modal = new bootstrap.Modal(modalEl);
      modal.show();

      var dismissBtn = document.getElementById('syncDismissBtn');
      if (dismissBtn) {
        dismissBtn.addEventListener('click', function () {
          dismissSync(syncHash);
        }, { once: true });
      }

      var applyBtn = document.getElementById('syncApplyBtn');
      if (applyBtn) {
        applyBtn.textContent = 'Apply Changes';
        applyBtn.addEventListener('click', function () {
          applySyncActions(syncHash);
        }, { once: true });
      }
    } catch (err) {
      console.error('[workspace-sync] Failed to check sync status:', err);
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      setTimeout(checkWorkspaceSync, 800);
    });
  } else {
    setTimeout(checkWorkspaceSync, 800);
  }
})();
