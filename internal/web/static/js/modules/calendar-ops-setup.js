// calendar-ops-setup.js — the guided Calendar Ops connector setup card on the
// workspace detail page (Workspace MCP tab).
//
// It reads GET /api/calendar-ops/setup?workspace_id=ID, which reports
// applicable:false for every non-Calendar-Ops workspace so this module stays
// dormant everywhere else. When applicable, it walks the user through:
// choosing an existing connector or adding the shipped Google Calendar
// (Developer Preview) preset, authenticating through the existing MCP
// connect flow, reviewing guided mapping suggestions (editable as JSON —
// this IS the "advanced editor": every tool/argument/field pointer is
// directly correctable), running the connection test, and picking visible
// calendars, a display timezone, and permitted meeting-prep context
// workspaces before saving.
//
// This module never calls an MCP tool directly and never exposes a write
// operation — it only drives the setup HTTP API, which itself persists a
// read-only tool allowlist (see internal/calendarhttp).
(function () {
  'use strict';

  const els = () => ({
    card: document.getElementById('calendarOpsSetupCard'),
    status: document.getElementById('calendarOpsSetupStatus'),
    badge: document.getElementById('calendarOpsSetupBadge'),
    body: document.getElementById('calendarOpsSetupBody'),
    actions: document.getElementById('calendarOpsSetupActions')
  });

  let workspaceId = '';
  let lastState = null; // last GET /setup response
  let mappingText = ''; // editable JSON mapping textarea contents
  let selectedCalendarIds = new Set();
  let selectedContextIds = new Set();
  let lastCalendars = [];

  function wsId() {
    return workspaceId || (typeof window !== 'undefined' && window.currentWorkspaceId) || '';
  }

  function setBadge(badge, label) {
    if (!badge) return;
    badge.textContent = label;
    badge.className = 'reaper-setup-badge reaper-setup-badge-' + label.toLowerCase().replace(/[^a-z]+/g, '-');
  }

  function el(tag, opts = {}, children = []) {
    const node = document.createElement(tag);
    if (opts.className) node.className = opts.className;
    if (opts.text !== undefined) node.textContent = opts.text;
    if (opts.attrs) {
      for (const [k, v] of Object.entries(opts.attrs)) node.setAttribute(k, v);
    }
    if (opts.style) node.style.cssText = opts.style;
    if (opts.onClick) node.addEventListener('click', opts.onClick);
    for (const c of children) if (c) node.appendChild(c);
    return node;
  }

  function button(label, opts = {}) {
    return el('button', {
      className: 'modern-btn ' + (opts.primary ? 'modern-btn-primary' : 'modern-btn-secondary'),
      text: label,
      style: 'font-size:12px;',
      attrs: { type: 'button' },
      onClick: opts.onClick
    });
  }

  async function apiGet(url) {
    const resp = await fetch(url);
    if (!resp.ok) throw new Error('request failed: ' + resp.status);
    return resp.json();
  }

  async function apiPost(url, body) {
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body || {})
    });
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      const message = (data && (data.error || data.message)) || 'request failed: ' + resp.status;
      throw new Error(message);
    }
    return data;
  }

  function setBusy(on) {
    const { actions } = els();
    if (actions) actions.querySelectorAll('button, input').forEach(n => (n.disabled = on));
  }

  function stateLabel(state) {
    switch (state) {
      case 'connector_missing':
        return 'Setup';
      case 'auth_required':
        return 'Connect';
      case 'mapping_required':
        return 'Map';
      case 'validation_failed':
        return 'Verify';
      case 'ready':
        return 'Ready';
      case 'degraded':
        return 'Degraded';
      default:
        return 'Setup';
    }
  }

  // --- section builders -----------------------------------------------------

  function renderPresetSection(container, resp) {
    container.appendChild(el('div', { text: 'Choose a calendar connector', style: 'font-weight:600;margin-top:8px;' }));

    (resp.presets || []).forEach(preset => {
      const already = !!(resp.preset_added && resp.preset_added[preset.id]);
      const card = el('div', { style: 'border:1px solid var(--border-color,#3333);border-radius:8px;padding:10px;margin:6px 0;' });
      card.appendChild(el('div', { text: preset.display_name, style: 'font-weight:600;' }));
      if (preset.developer_preview) {
        card.appendChild(el('div', { text: 'Developer Preview — requires your Google account to be enrolled.', style: 'font-size:12px;opacity:0.8;' }));
      }
      if (Array.isArray(preset.prerequisites) && preset.prerequisites.length) {
        const list = el('ul', { style: 'font-size:12px;margin:6px 0 6px 18px;padding:0;' });
        preset.prerequisites.forEach(p => list.appendChild(el('li', { text: p })));
        card.appendChild(list);
      }
      if (preset.docs_url) {
        card.appendChild(el('a', { text: 'Official setup docs', attrs: { href: preset.docs_url, target: '_blank', rel: 'noopener' }, style: 'font-size:12px;' }));
      }
      const row = el('div', { style: 'margin-top:8px;' });
      row.appendChild(
        button(already ? 'Use this connector' : 'Add this connector', {
          primary: true,
          onClick: () => selectConnector({ preset_id: preset.id })
        })
      );
      card.appendChild(row);
      container.appendChild(card);
    });

    if (resp.existing_connectors && resp.existing_connectors.length) {
      container.appendChild(el('div', { text: 'Or use an existing MCP server', style: 'font-weight:600;margin-top:12px;' }));
      resp.existing_connectors.forEach(c => {
        const row = el('div', { style: 'display:flex;align-items:center;gap:8px;margin:4px 0;' });
        row.appendChild(el('span', { text: c.name + (c.remote ? ' (remote)' : ' (local)'), style: 'font-size:13px;' }));
        row.appendChild(button('Use this connector', { onClick: () => selectConnector({ server_name: c.name }) }));
        container.appendChild(row);
      });
    }
  }

  function renderAuthSection(container, resp) {
    const status = (resp.connector && resp.connector.status) || '';
    container.appendChild(
      el('div', {
        text:
          status === 'auth_required'
            ? 'This connector needs authorization. Enter the OAuth client id/secret from your own Google Cloud project (see the preset prerequisites above), then authorize in the browser tab that opens.'
            : 'Waiting to connect…',
        style: 'margin-top:8px;'
      })
    );

    // FR 58: when a global Google account is connected, use it — the browser
    // account chooser is pre-selected (login_hint) and the grant binds to that
    // identity. The OAuth client below is still your own self-hosted Web client.
    const accountNote = el('div', { style: 'margin-top:6px;font-size:13px;color:var(--primary-color);' });
    container.appendChild(accountNote);
    fetch('/api/connections/google/status', { headers: { Accept: 'application/json' } })
      .then((r) => (r.ok ? r.json() : null))
      .then((conn) => {
        if (conn && conn.subject && conn.email) {
          accountNote.textContent =
            'Using your connected Google account: ' + conn.email +
            '. The OAuth client below is your own Google Cloud Web client (one-time, self-hosted).';
        }
      })
      .catch(() => {});

    const clientIdInput = el('input', {
      className: 'form-control',
      attrs: { type: 'text', placeholder: 'OAuth client id', autocomplete: 'off' },
      style: 'margin:6px 0;max-width:360px;'
    });
    const clientSecretInput = el('input', {
      className: 'form-control',
      attrs: { type: 'password', placeholder: 'OAuth client secret', autocomplete: 'off' },
      style: 'margin:6px 0;max-width:360px;'
    });
    container.appendChild(clientIdInput);
    container.appendChild(clientSecretInput);

    const authorizeMsg = el('div', { style: 'font-size:12px;margin-top:4px;' });
    container.appendChild(authorizeMsg);

    const connectRow = el('div', { style: 'margin-top:6px;' });
    connectRow.appendChild(
      button('Connect', {
        primary: true,
        onClick: async () => {
          if (!resp.binding) return;
          setBusy(true);
          try {
            const body =
              clientIdInput.value.trim() && clientSecretInput.value.trim()
                ? { client_id: clientIdInput.value.trim(), client_secret: clientSecretInput.value.trim() }
                : {};
            const result = await apiPost(
              '/api/mcp/servers/' + encodeURIComponent(resp.binding.server_name) + '/connect',
              body
            );
            if (result.error === 'credentials_required') {
              authorizeMsg.textContent = 'Enter the client id and secret first.';
            } else if (result.authorize_url) {
              authorizeMsg.textContent = 'Opening the authorization page in a new tab…';
              window.open(result.authorize_url, '_blank', 'noopener');
            } else if (result.status === 'running') {
              authorizeMsg.textContent = 'Connected.';
            } else {
              authorizeMsg.textContent = 'Status: ' + (result.status || 'unknown');
            }
            await refresh();
          } catch (err) {
            authorizeMsg.textContent = 'Could not start the connection: ' + err.message;
          } finally {
            setBusy(false);
          }
        }
      })
    );
    container.appendChild(connectRow);
  }

  function renderMappingSection(container, resp) {
    container.appendChild(el('div', { text: 'Mapping', style: 'font-weight:600;margin-top:12px;' }));
    container.appendChild(
      el('div', {
        text:
          'Suggested tool mappings prefill below — nothing activates until you run Validate and Save. Edit the JSON directly to correct any tool, argument, or field mapping.',
        style: 'font-size:12px;opacity:0.85;'
      })
    );

    const suggestRow = el('div', { style: 'margin:6px 0;' });
    suggestRow.appendChild(
      button('Discover tools & suggest mappings', {
        onClick: async () => {
          if (!resp.binding) return;
          setBusy(true);
          try {
            const result = await apiPost('/api/calendar-ops/setup/suggest-mappings', {
              server_name: resp.binding.server_name
            });
            const ops = {};
            (result.suggestions || []).forEach(s => {
              ops[s.operation] = { tool: s.tool };
              if (s.arguments) ops[s.operation].arguments = s.arguments;
            });
            mappingText = JSON.stringify({ capability: 'calendar', operations: ops }, null, 2);
            render(lastState);
          } catch (err) {
            window.alert('Could not discover tools: ' + err.message); // eslint-disable-line no-alert
          } finally {
            setBusy(false);
          }
        }
      })
    );
    container.appendChild(suggestRow);

    if (!mappingText) {
      mappingText = JSON.stringify({ capability: 'calendar', operations: {} }, null, 2);
    }
    const textarea = el('textarea', {
      className: 'form-control',
      style: 'font-family:monospace;font-size:12px;min-height:220px;white-space:pre;',
      attrs: { spellcheck: 'false' }
    });
    textarea.value = mappingText;
    textarea.addEventListener('input', () => {
      mappingText = textarea.value;
    });
    container.appendChild(textarea);

    const resultsBox = el('div', { style: 'font-size:12px;margin-top:8px;' });
    container.appendChild(resultsBox);

    const validateRow = el('div', { style: 'margin-top:6px;' });
    validateRow.appendChild(
      button('Validate', {
        primary: true,
        onClick: async () => {
          setBusy(true);
          resultsBox.textContent = '';
          try {
            const mapping = JSON.parse(mappingText);
            const result = await apiPost('/api/calendar-ops/setup/validate', {
              workspace_id: wsId(),
              mapping
            });
            if (!result.mapping_valid) {
              resultsBox.appendChild(el('div', { text: 'Mapping error: ' + result.mapping_error, style: 'color:var(--danger,#c0392b);' }));
              return;
            }
            (result.validation_results || []).forEach(r => {
              const line = el('div', {
                text:
                  r.operation +
                  ': ' +
                  (r.success ? 'OK' : 'FAILED — ' + (r.error || 'unknown error')) +
                  (r.missing_fields && r.missing_fields.length ? ' (missing: ' + r.missing_fields.join(', ') + ')' : '')
              });
              resultsBox.appendChild(line);
            });
            if (result.calendars_error) {
              resultsBox.appendChild(el('div', { text: 'Could not list calendars: ' + result.calendars_error }));
            }
            lastCalendars = result.calendars || [];
            render(lastState);
          } catch (err) {
            resultsBox.appendChild(el('div', { text: 'Validation failed: ' + err.message }));
          } finally {
            setBusy(false);
          }
        }
      })
    );
    container.appendChild(validateRow);
  }

  function renderCalendarPicker(container) {
    if (!lastCalendars.length) return;
    container.appendChild(el('div', { text: 'Visible calendars', style: 'font-weight:600;margin-top:12px;' }));
    lastCalendars.forEach(cal => {
      const row = el('label', { style: 'display:flex;align-items:center;gap:6px;font-size:13px;' });
      const checkbox = el('input', { attrs: { type: 'checkbox' } });
      checkbox.checked = selectedCalendarIds.has(cal.id);
      checkbox.addEventListener('change', () => {
        if (checkbox.checked) selectedCalendarIds.add(cal.id);
        else selectedCalendarIds.delete(cal.id);
      });
      row.appendChild(checkbox);
      row.appendChild(document.createTextNode(cal.name || cal.id));
      container.appendChild(row);
    });
  }

  function renderContextWorkspacePicker(container, resp) {
    const candidates = resp.context_workspace_candidates || [];
    if (!candidates.length) return;
    container.appendChild(
      el('div', { text: 'Ori workspaces Meeting Prep may read as context', style: 'font-weight:600;margin-top:12px;' })
    );
    candidates.forEach(ws => {
      const row = el('label', { style: 'display:flex;align-items:center;gap:6px;font-size:13px;' });
      const checkbox = el('input', { attrs: { type: 'checkbox' } });
      checkbox.checked = selectedContextIds.has(ws.id);
      checkbox.addEventListener('change', () => {
        if (checkbox.checked) selectedContextIds.add(ws.id);
        else selectedContextIds.delete(ws.id);
      });
      row.appendChild(checkbox);
      row.appendChild(document.createTextNode(ws.name || ws.id));
      container.appendChild(row);
    });
  }

  function renderSaveSection(container, resp) {
    container.appendChild(el('div', { text: 'Display timezone', style: 'font-weight:600;margin-top:12px;' }));
    const tzInput = el('input', {
      className: 'form-control',
      attrs: { type: 'text', placeholder: 'e.g. America/New_York' },
      style: 'max-width:240px;'
    });
    tzInput.value = (resp.settings && resp.settings.display_time_zone) || '';
    container.appendChild(tzInput);

    renderCalendarPicker(container);
    renderContextWorkspacePicker(container, resp);

    const saveMsg = el('div', { style: 'font-size:12px;margin-top:6px;' });
    const saveRow = el('div', { style: 'margin-top:8px;' });
    saveRow.appendChild(
      button('Save', {
        primary: true,
        onClick: async () => {
          setBusy(true);
          saveMsg.textContent = '';
          try {
            const mapping = JSON.parse(mappingText);
            await apiPost('/api/calendar-ops/setup/save', {
              workspace_id: wsId(),
              mapping,
              selected_calendar_ids: Array.from(selectedCalendarIds),
              display_time_zone: tzInput.value.trim(),
              context_workspace_ids: Array.from(selectedContextIds)
            });
            saveMsg.textContent = 'Saved.';
            await refresh();
          } catch (err) {
            saveMsg.textContent = 'Could not save: ' + err.message;
          } finally {
            setBusy(false);
          }
        }
      })
    );
    container.appendChild(saveRow);
    container.appendChild(saveMsg);
  }

  async function selectConnector(payload) {
    setBusy(true);
    try {
      payload.workspace_id = wsId();
      await apiPost('/api/calendar-ops/setup/connector', payload);
      await refresh();
    } catch (err) {
      window.alert('Could not select this connector: ' + err.message); // eslint-disable-line no-alert
    } finally {
      setBusy(false);
    }
  }

  // --- top-level render -------------------------------------------------

  function render(resp) {
    const { card, status, badge, body } = els();
    if (!card) return;
    if (!resp || !resp.applicable) {
      card.hidden = true;
      return;
    }
    card.hidden = false;

    setBadge(badge, stateLabel(resp.state));
    if (status) {
      status.textContent =
        resp.binding && resp.binding.server_name
          ? 'Connector: ' + resp.binding.server_name
          : 'No connector selected yet';
    }

    if (!body) return;
    body.textContent = '';

    if (resp.state === 'connector_missing') {
      renderPresetSection(body, resp);
      return;
    }

    if (resp.state === 'auth_required' || resp.state === 'degraded') {
      renderAuthSection(body, resp);
    }
    renderMappingSection(body, resp);
    renderSaveSection(body, resp);

    if (resp.state === 'ready') {
      body.insertBefore(el('div', { text: 'Calendar Ops is ready.', style: 'font-weight:600;color:var(--success,#2e7d32);' }), body.firstChild);
    }
  }

  async function refresh() {
    const id = wsId();
    if (!id) return;
    try {
      const resp = await apiGet('/api/calendar-ops/setup?workspace_id=' + encodeURIComponent(id));
      lastState = resp;
      render(resp);
    } catch (_) {
      // Silent: most workspaces are not Calendar Ops, and a transient fetch
      // failure shouldn't surface an error on every workspace-detail page.
    }
  }

// waitForWorkspaceId polls briefly for window.currentWorkspaceId, then calls
// onReady. This module loads as a plain deferred script, while
// workspace-detail.tmpl's own bootstrap (which sets currentWorkspaceId) runs
// from an inline `type="module"` script -- module scripts are also deferred,
// so their relative DOMContentLoaded-listener registration order against a
// plain defer script isn't guaranteed. A one-shot DOMContentLoaded call here
// can fire before that global exists, silently no-op (refresh() bails on an
// empty id), and never retry. Polling avoids depending on that ordering.
function waitForWorkspaceId(onReady) {
    const started = Date.now();
    const attempt = () => {
      const id = (typeof window !== 'undefined' && window.currentWorkspaceId) || '';
      if (id) {
        onReady(id);
        return;
      }
      if (Date.now() - started > 5000) return; // give up quietly; not every page has a workspace
      setTimeout(attempt, 100);
    };
    attempt();
  }

  function init(id) {
    if (id) {
      workspaceId = id;
      void refresh();
      return;
    }
    waitForWorkspaceId(resolvedId => {
      workspaceId = resolvedId;
      void refresh();
    });
  }

  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', () => init(), { once: true });
    } else {
      init();
    }
  }

  window.CalendarOpsSetup = { init, refresh, render, _els: els };
})();
