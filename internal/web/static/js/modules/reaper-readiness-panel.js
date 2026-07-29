// reaper-readiness-panel.js — the durable REAPER Setup readiness card on the
// workspace detail page (Workspace Tools → Plugins), plus a compact
// setup-needed indicator chip in the config summary.
//
// It reads one normalized readiness result from
// GET /api/workspaces/{id}/reaper-setup and renders separate rows for project
// mode, plugin install/enabled, workspace attachment, setup-agent compatibility,
// native CLI access, setup-task state, and live REAPER verification — which is
// always labeled not-yet-checked. It never claims REAPER is connected.
//
// Actions (repair, check-again-and-start-setup, deep-links) reuse the existing
// idempotent backend endpoints and re-fetch readiness after every mutation.
(function () {
  'use strict';

  const els = () => ({
    card: document.getElementById('reaperReadinessCard'),
    status: document.getElementById('reaperReadinessStatus'),
    badge: document.getElementById('reaperReadinessBadge'),
    rows: document.getElementById('reaperReadinessRows'),
    actions: document.getElementById('reaperReadinessActions'),
    chip: document.getElementById('reaperReadinessChip')
  });

  let workspaceId = '';
  let busy = false;

  function wsId() {
    return workspaceId || (typeof window !== 'undefined' && window.currentWorkspaceId) || '';
  }

  function setBadge(badge, label) {
    if (!badge) return;
    badge.textContent = label;
    badge.className =
      'reaper-setup-badge reaper-setup-badge-' + label.toLowerCase().replace(/[^a-z]+/g, '-');
  }

  function row(label, value, ok) {
    const li = document.createElement('li');
    li.className = 'workspace-detail-reaper-row';
    const mark = document.createElement('span');
    mark.className = 'workspace-detail-reaper-row-mark';
    // Non-color status token in addition to any styling.
    mark.textContent = ok === true ? '✓' : ok === false ? '•' : '–';
    mark.setAttribute('aria-hidden', 'true');
    const l = document.createElement('span');
    l.className = 'workspace-detail-reaper-row-label';
    l.textContent = label + ': ';
    const v = document.createElement('span');
    v.className = 'workspace-detail-reaper-row-value';
    v.textContent = value;
    li.appendChild(mark);
    li.appendChild(l);
    li.appendChild(v);
    return li;
  }

  function button(label, opts = {}) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'modern-btn ' + (opts.primary ? 'modern-btn-primary' : 'modern-btn-secondary');
    b.style.fontSize = '12px';
    b.textContent = label;
    if (opts.onClick) b.addEventListener('click', opts.onClick);
    return b;
  }

  function setBusy(on) {
    busy = on;
    const { actions } = els();
    if (actions)
      actions.querySelectorAll('button').forEach(b => {
        b.disabled = on;
      });
  }

  // wizardOwnsSetup reports whether this workspace's blueprint declares a setup
  // wizard. When it does, setup happens there and this card reports rather than
  // repairs; a workspace from an older blueprint keeps the card's own actions.
  function wizardOwnsSetup() {
    const status = window.SetupWizard?.getStatus?.();
    return !!(status && status.applicable);
  }

  function openPluginsTab() {
    // Expand the (collapsed) config panel and switch to the Plugins tab, then
    // bring the card into view — the readiness card lives inside that tab.
    document.getElementById('workspace-detail-config-toggle')?.click?.();
    document.getElementById('workspace-detail-config-plugins-tab')?.click?.();
    const { card } = els();
    card?.scrollIntoView?.({ behavior: 'smooth', block: 'center' });
    card?.focus?.();
  }

  async function post(url, body) {
    const opts = { method: 'POST' };
    if (body !== undefined) {
      opts.headers = { 'Content-Type': 'application/json' };
      opts.body = JSON.stringify(body);
    }
    const resp = await fetch(url, opts);
    if (!resp.ok) throw new Error('request failed: ' + resp.status);
    return resp.json();
  }

  async function repair(confirmEnable) {
    if (busy) return;
    setBusy(true);
    try {
      const result = await post(
        '/api/workspaces/' + encodeURIComponent(wsId()) + '/reaper-setup/repair',
        { confirm_enable: !!confirmEnable }
      );
      if (result.needs_confirm) {
        // Ask for explicit confirmation before enabling a disabled plugin.
        renderConfirm('Enable reaper-plugin and attach its components to this workspace?', () =>
          repair(true)
        );
        return;
      }
      if (result.needs_install) {
        // Plugin missing: run the inline install, then re-check (which will
        // attach on the next repair once installed+enabled).
        setBusy(false);
        installPlugin();
        return;
      }
      await refresh();
    } catch (_) {
      renderError('Repair failed. Nothing was changed; you can try again.');
    } finally {
      setBusy(false);
    }
  }

  // installPlugin runs the shared inline reaper-plugin install into the card's
  // actions area, then re-checks readiness (existing workspaces have no
  // template-declared source, so it resolves a marketplace or takes a paste).
  function installPlugin() {
    const { actions } = els();
    if (!actions) return;
    if (!window.ReaperPluginInstall) {
      window.open('/plugins?install=reaper-plugin', '_blank', 'noopener');
      return;
    }
    window.ReaperPluginInstall.begin({
      host: actions,
      declaredSource: '',
      onComplete: refresh,
      onCancel: refresh
    });
  }

  async function checkAgainAndStartSetup() {
    if (busy) return;
    setBusy(true);
    try {
      await post('/api/workspaces/' + encodeURIComponent(wsId()) + '/template-setup/start');
      await refresh();
    } catch (_) {
      renderError('Could not start setup. Readiness is unchanged; you can try again.');
    } finally {
      setBusy(false);
    }
  }

  function renderConfirm(message, onConfirm) {
    const { actions } = els();
    if (!actions) return;
    actions.textContent = '';
    const msg = document.createElement('span');
    msg.style.cssText = 'font-size:12px;color:var(--text-primary);align-self:center;';
    msg.textContent = message;
    actions.appendChild(msg);
    actions.appendChild(button('Enable & attach', { primary: true, onClick: onConfirm }));
    actions.appendChild(button('Cancel', { onClick: refresh }));
  }

  function renderError(message) {
    const { status, actions, badge } = els();
    setBadge(badge, 'Error');
    if (status) status.textContent = message;
    if (actions) {
      actions.textContent = '';
      actions.appendChild(button('Retry', { onClick: refresh }));
    }
  }

  function statusLabel(r) {
    switch (r.status) {
      case 'ori_ready':
        return 'Ready';
      case 'plugin_missing':
        return 'File-only';
      case 'plugin_disabled':
        return 'Disabled';
      case 'plugin_detached':
        return 'Detached';
      case 'cli_agent_required':
        return 'Agent';
      case 'native_cli_access_required':
        return 'Access';
      case 'file_only':
        return 'File-only';
      default:
        return 'Setup';
    }
  }

  function render(r) {
    const { card, status, badge, rows, actions, chip } = els();
    if (!card) return;

    if (!r || !r.identified) {
      card.hidden = true;
      if (chip) chip.hidden = true;
      return;
    }
    card.hidden = false;
    card.tabIndex = -1;

    setBadge(badge, statusLabel(r));
    if (status) status.textContent = r.explanation || '';

    // Rows: separate, text-labeled status lines (color is never the only signal).
    if (rows) {
      rows.textContent = '';
      rows.appendChild(
        row(
          'Project mode',
          r.project_mode === 'ori_ready' ? 'Ori is ready to check REAPER' : 'File-only',
          r.project_mode === 'ori_ready'
        )
      );
      rows.appendChild(
        row(
          'Plugin',
          r.plugin_installed
            ? r.plugin_enabled
              ? 'Installed and enabled'
              : 'Installed but disabled'
            : 'Not installed',
          r.plugin_installed && r.plugin_enabled
        )
      );
      rows.appendChild(
        row(
          'Workspace attachment',
          r.plugin_attached
            ? 'Components attached'
            : (r.missing_components || []).map(c => c.name).join(', ') || 'Not attached',
          r.plugin_attached
        )
      );
      rows.appendChild(
        row(
          'Setup agent',
          r.setup_agent
            ? r.setup_agent +
                (r.setup_agent_is_cli ? ' (compatible)' : ' (needs Codex or Claude Code)')
            : 'Not assigned',
          r.setup_agent_is_cli
        )
      );
      rows.appendChild(
        row(
          'Native CLI access',
          (r.workspace_native_cli_enabled ? 'Workspace on' : 'Workspace off') +
            ' · ' +
            (r.agent_native_cli_enabled ? 'Agent on' : 'Agent off'),
          r.workspace_native_cli_enabled && r.agent_native_cli_enabled
        )
      );
      rows.appendChild(
        row('Setup task', r.has_pending_setup_task ? 'Pending' : 'None pending', null)
      );
      rows.appendChild(
        row('Live REAPER verification', 'Not checked yet — verified when setup runs', null)
      );
    }

    // Actions.
    if (actions) {
      actions.textContent = '';
      if (wizardOwnsSetup()) {
        // Where a wizard owns this workspace's setup, the card stays a status
        // surface and stops being a second place to repair things: two entry
        // points that mutate the same prerequisites is how a user ends up
        // half-way through both.
        actions.appendChild(
          button('Open setup', { primary: true, onClick: () => window.SetupWizard?.open?.() })
        );
      } else if (r.status !== 'ori_ready') {
        actions.appendChild(
          button('Repair REAPER setup', { primary: true, onClick: () => repair(false) })
        );
      }
      if (!wizardOwnsSetup()) {
        if (r.status === 'ori_ready' && r.has_pending_setup_task) {
          actions.appendChild(
            button('Check again and start setup', {
              primary: true,
              onClick: checkAgainAndStartSetup
            })
          );
        }
        if (!r.plugin_installed) {
          actions.appendChild(button('Install plugin', { primary: true, onClick: installPlugin }));
        }
        if (r.status === 'cli_agent_required' || r.status === 'native_cli_access_required') {
          actions.appendChild(
            button('Native CLI access', {
              onClick: () =>
                document.getElementById('workspace-detail-config-native-mcp-tab')?.click?.()
            })
          );
        }
      }
    }

    // Compact chip: setup-needed vs. compact success.
    if (chip) {
      chip.hidden = false;
      chip.textContent =
        r.status === 'ori_ready' ? 'REAPER: ready to check' : 'REAPER setup needed';
      chip.classList.toggle('reaper-setup-chip-ready', r.status === 'ori_ready');
      chip.setAttribute('aria-label', chip.textContent + '. Open REAPER Setup.');
    }
  }

  async function refresh() {
    const id = wsId();
    if (!id) return;
    try {
      const resp = await fetch('/api/workspaces/' + encodeURIComponent(id) + '/reaper-setup');
      if (!resp.ok) throw new Error('readiness failed');
      render(await resp.json());
    } catch (_) {
      renderError('Could not load REAPER readiness.');
    }
  }

  // ---------- Setup Wizard step ----------

  // The wizard's readiness step and this card describe the same setup, so they
  // run the same repairs against the same endpoints. What the step adds is
  // scope: it shows only the prerequisites of the mode the user chose, and only
  // for the Ori-assisted answer — a file-only workspace has nothing to repair,
  // and saying so is the whole point of offering that mode.
  const REAPER_ADAPTER = 'reaper_song';

  function chosenMode(status) {
    const steps = (status && status.steps) || [];
    for (const step of steps) {
      const selected = step.selected_option || '';
      if (selected === 'file_only' || selected === 'ori_assisted') return selected;
    }
    return '';
  }

  // wizardActions offers each repair the current readiness admits, and re-asks
  // the server afterwards. Nothing here decides the step passed: every button
  // ends in ctx.recheck(), and the server's next verdict is what the user sees.
  function wizardActions(host, readiness, ctx) {
    const act = async run => {
      ctx.setBusy(true);
      try {
        await run();
        await ctx.recheck();
      } catch (_) {
        ctx.setError('That did not go through. Nothing changed — you can try again.');
      } finally {
        ctx.setBusy(false);
      }
    };

    if (!readiness.plugin_installed) {
      host.appendChild(
        button('Install the plugin', {
          primary: true,
          onClick: () => {
            if (!window.ReaperPluginInstall) {
              ctx.rememberReturn();
              window.open('/plugins?install=reaper-plugin', '_blank', 'noopener');
              return;
            }
            window.ReaperPluginInstall.begin({
              host,
              declaredSource: '',
              onComplete: () => ctx.recheck(),
              onCancel: () => ctx.recheck()
            });
          }
        })
      );
      return;
    }
    if (readiness.status !== 'ori_ready') {
      // Enabling a disabled plugin is a change the user has to approve, so the
      // unconfirmed call is sent first and its needs_confirm answer is what
      // asks them — the same two-step the card uses.
      host.appendChild(
        button(readiness.plugin_enabled ? 'Attach to this workspace' : 'Enable and attach', {
          primary: true,
          onClick: () =>
            act(async () => {
              const result = await post(
                '/api/workspaces/' + encodeURIComponent(wsId()) + '/reaper-setup/repair',
                { confirm_enable: false }
              );
              if (result && result.needs_confirm) {
                await post(
                  '/api/workspaces/' + encodeURIComponent(wsId()) + '/reaper-setup/repair',
                  { confirm_enable: true }
                );
              }
            })
        })
      );
    }
    if (
      readiness.status === 'cli_agent_required' ||
      readiness.status === 'native_cli_access_required'
    ) {
      host.appendChild(
        button('Open native CLI access', {
          onClick: () => {
            // The setting lives behind this dialog, so the wizard steps aside
            // and records where to come back to.
            ctx.rememberReturn();
            window.SetupWizard?.close?.();
            document.getElementById('workspace-detail-config-toggle')?.click?.();
            document.getElementById('workspace-detail-config-native-mcp-tab')?.click?.();
          }
        })
      );
    }
  }

  async function renderWizardStep(container, ctx) {
    const step = ctx.step || {};
    // Registered on a shared kind: every other blueprint's readiness step is
    // handed straight back to the dialog's own renderer.
    if (step.adapter !== REAPER_ADAPTER) {
      ctx.renderDefault(container);
      return;
    }
    if (chosenMode(ctx.status) !== 'ori_assisted') {
      ctx.renderDefault(container);
      return;
    }

    const summary = document.createElement('p');
    summary.className = 'setup-wizard-step-description';
    summary.textContent = step.summary || '';
    container.appendChild(summary);

    const rows = document.createElement('ul');
    rows.className = 'workspace-detail-reaper-rows';
    container.appendChild(rows);
    const actions = document.createElement('div');
    actions.className = 'setup-wizard-step-actions';
    container.appendChild(actions);

    let readiness = null;
    try {
      const resp = await fetch('/api/workspaces/' + encodeURIComponent(wsId()) + '/reaper-setup');
      if (!resp.ok) throw new Error('readiness failed');
      readiness = await resp.json();
    } catch (_) {
      rows.appendChild(row('Prerequisites', 'Could not be read just now', false));
      actions.appendChild(button('Check again', { primary: true, onClick: () => ctx.recheck() }));
      return;
    }

    rows.appendChild(
      row(
        'Plugin',
        readiness.plugin_installed
          ? readiness.plugin_enabled
            ? 'Installed and enabled'
            : 'Installed but disabled'
          : 'Not installed',
        !!(readiness.plugin_installed && readiness.plugin_enabled)
      )
    );
    rows.appendChild(
      row(
        'Workspace attachment',
        readiness.plugin_attached ? 'Attached' : 'Not attached',
        !!readiness.plugin_attached
      )
    );
    rows.appendChild(
      row(
        'Agent',
        readiness.setup_agent
          ? readiness.setup_agent +
              (readiness.setup_agent_is_cli ? ' (compatible)' : ' (needs Codex or Claude Code)')
          : 'Not assigned',
        !!readiness.setup_agent_is_cli
      )
    );
    rows.appendChild(
      row(
        'Native CLI access',
        (readiness.workspace_native_cli_enabled ? 'Workspace on' : 'Workspace off') +
          ' · ' +
          (readiness.agent_native_cli_enabled ? 'Agent on' : 'Agent off'),
        !!(readiness.workspace_native_cli_enabled && readiness.agent_native_cli_enabled)
      )
    );
    // Stated on the step where a user would most easily assume otherwise.
    rows.appendChild(row('Live REAPER session', 'Not checked here', null));

    wizardActions(actions, readiness, ctx);
  }

  function registerWizardStep() {
    const wizard = typeof window !== 'undefined' && window.SetupWizard;
    if (!wizard || typeof wizard.registerStepRenderer !== 'function') return;
    wizard.registerStepRenderer('readiness', { render: renderWizardStep });
  }

  function init(id) {
    workspaceId = id || wsId();
    const { chip } = els();
    chip?.addEventListener?.('click', () => {
      // The chip points at whatever is actually in charge of setup here.
      if (wizardOwnsSetup() && window.SetupWizard?.getStatus?.()?.state !== 'ready') {
        window.SetupWizard?.open?.();
        return;
      }
      openPluginsTab();
    });
    // Deferred scripts run in document order and this one precedes the wizard's,
    // so registering here alone would silently register nothing. Registration is
    // idempotent, so it is attempted now and again once every module has run.
    registerWizardStep();
    if (typeof document !== 'undefined' && document.readyState !== 'complete') {
      document.addEventListener('DOMContentLoaded', registerWizardStep, { once: true });
    }
    void refresh();
  }

  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', () => init(), { once: true });
    } else {
      init();
    }
  }

  window.ReaperReadinessPanel = { init, refresh, render, _els: els };
})();
