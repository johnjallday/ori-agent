// reaper-readiness-panel.js — REAPER's renderer inside the authoritative
// generalized Setup Wizard surface.
//
// There is intentionally no second REAPER card, chip, or status endpoint here.
// The renderer consumes /runtime-capabilities, returns every transition to the
// same service, and lets SetupWizard own the one banner/chip/dialog entry point.
(function () {
  'use strict';

  const REQUIREMENT = 'reaper_live_control';
  const DOWNLOAD_URL = 'https://www.reaper.fm/download.php';
  const REAPER_PLUGIN_URL = 'https://github.com/johnjallday/reaper-plugin';

  const checks = [
    {
      id: 'application',
      label: 'REAPER application',
      reasons: ['unsupported_platform', 'reaper_app_missing', 'reaper_app_unknown']
    },
    {
      id: 'web-remote',
      label: 'Web Remote',
      reasons: [
        'web_remote_unconfigured',
        'web_remote_invalid',
        'reaper_offline',
        'web_remote_unavailable',
        'web_remote_malformed'
      ]
    },
    {
      id: 'plugin',
      label: 'REAPER plugin and skills',
      reasons: ['reaper_plugin_missing', 'reaper_plugin_disabled', 'reaper_plugin_detached']
    },
    {
      id: 'runner',
      label: 'Ori REAPER runner',
      reasons: ['runner_missing', 'runner_invalid', 'runner_failure']
    },
    {
      id: 'agent',
      label: 'Compatible workspace agent',
      reasons: ['cli_agent_required', 'runtime_task_agent_required']
    },
    {
      id: 'access',
      label: 'REAPER access',
      reasons: [
        'reaper_access_required',
        'reaper_access_denied',
        'runner_write_denied',
        'runtime_task_grant_required'
      ]
    },
    {
      id: 'verification',
      label: 'Project-specific connection test',
      reasons: [
        'verification_required',
        'verification_timeout',
        'wrong_project',
        'reaper_project_missing',
        'check_failed'
      ]
    }
  ];

  let registered = false;
  let choosingAgent = false;

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function button(label, onClick, { primary = false, disabled = false } = {}) {
    const node = el(
      'button',
      `modern-btn ${primary ? 'modern-btn-primary' : 'modern-btn-secondary'}`,
      label
    );
    node.type = 'button';
    node.disabled = disabled;
    if (onClick) node.addEventListener('click', onClick);
    return node;
  }

  function requirement(status) {
    return (status?.requirements || []).find(item => item.key === REQUIREMENT) || null;
  }

  function firstAction(status) {
    return status?.first_blocker?.action || requirement(status)?.action || null;
  }

  function firstReason(status) {
    return String(
      status?.first_blocker?.reason_code || requirement(status)?.reason_code || ''
    ).trim();
  }

  function selectedMode(status) {
    return String(status?.selected_mode_id || '').trim();
  }

  function owns(step) {
    return step?.kind === 'runtime_readiness' && step?.runtime_requirement_key === REQUIREMENT;
  }

  function statusWord(state) {
    switch (state) {
      case 'complete':
        return { mark: '✓', word: 'Complete', className: 'is-complete' };
      case 'attention':
        return { mark: '!', word: 'Needs attention', className: 'is-attention' };
      default:
        return { mark: '•', word: 'Waiting', className: 'is-waiting' };
    }
  }

  function checklistState(status, check, index) {
    if (status?.durable_state === 'configured') return 'complete';
    const reason = firstReason(status);
    const blockedAt = checks.findIndex(item => item.reasons.includes(reason));
    if (blockedAt < 0) return index === 0 ? 'attention' : 'waiting';
    if (index < blockedAt) return 'complete';
    if (index === blockedAt) return 'attention';
    return 'waiting';
  }

  function renderChecklist(host, status) {
    const list = el('ol', 'reaper-runtime-checklist');
    checks.forEach((check, index) => {
      const state = statusWord(checklistState(status, check, index));
      const item = el('li', `reaper-runtime-check ${state.className}`);
      const mark = el('span', 'reaper-runtime-check-mark', state.mark);
      mark.setAttribute('aria-hidden', 'true');
      const body = el('span', 'reaper-runtime-check-body');
      if (check.id === 'plugin') {
        const label = el('a', 'reaper-runtime-check-label reaper-runtime-plugin-link', check.label);
        label.href = REAPER_PLUGIN_URL;
        label.target = '_blank';
        label.rel = 'noopener noreferrer';
        label.setAttribute('aria-label', `${check.label} (opens plugin repository)`);
        body.appendChild(label);
      } else {
        body.appendChild(el('span', 'reaper-runtime-check-label', check.label));
      }
      body.appendChild(el('span', 'reaper-runtime-check-word', state.word));
      item.appendChild(mark);
      item.appendChild(body);
      list.appendChild(item);
    });
    host.appendChild(list);
  }

  function livePresentation(status) {
    const live = String(status?.live_state || requirement(status)?.live_state || 'not_checked');
    switch (live) {
      case 'available':
        return [
          '✓',
          'Connected now',
          'REAPER is connected to this workspace project now.',
          'ready'
        ];
      case 'offline':
        return [
          '•',
          'REAPER offline',
          'Setup remains configured. Open REAPER, then check again.',
          'offline'
        ];
      case 'wrong_target':
        return [
          '!',
          'Wrong project',
          requirement(status)?.summary || 'Open this workspace project in REAPER.',
          'attention'
        ];
      case 'unavailable':
        return [
          '!',
          'Unavailable',
          requirement(status)?.summary || 'The local REAPER connection is unavailable.',
          'attention'
        ];
      case 'check_failed':
        return [
          '!',
          'Check failed',
          requirement(status)?.summary || 'Ori could not check REAPER right now.',
          'attention'
        ];
      default:
        return [
          '–',
          'Not checked',
          'Past verification does not prove REAPER is connected now.',
          'neutral'
        ];
    }
  }

  function renderLive(host, status) {
    if (status?.durable_state !== 'configured') return;
    const [mark, title, detail, tone] = livePresentation(status);
    const panel = el('section', `reaper-runtime-live is-${tone}`);
    panel.setAttribute('aria-label', `Current REAPER connection: ${title}`);
    const heading = el('div', 'reaper-runtime-live-heading');
    const token = el('span', 'reaper-runtime-live-mark', mark);
    token.setAttribute('aria-hidden', 'true');
    heading.appendChild(token);
    heading.appendChild(el('strong', '', title));
    panel.appendChild(heading);
    panel.appendChild(el('p', 'reaper-runtime-live-detail', detail));
    host.appendChild(panel);
  }

  function formatTimestamp(value) {
    if (!value) return '';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(
      date
    );
  }

  function renderVerificationHistory(host, status) {
    const first = formatTimestamp(status?.first_verified_at);
    const last = formatTimestamp(status?.last_verified_at);
    if (!first && !last) return;
    const note = el('p', 'reaper-runtime-history');
    note.textContent = first
      ? `First verified ${first}${last && last !== first ? ` · Last verified ${last}` : ''}. Current connectivity is checked separately.`
      : `Last verified ${last}. Current connectivity is checked separately.`;
    host.appendChild(note);
  }

  async function transition(ctx, pending, run) {
    ctx.setError('');
    ctx.setBusy(true, pending);
    try {
      const next = await run();
      // Setup Wizard's recheck derives setup lifecycle from the newly persisted
      // runtime state. Release its busy guard first, then restore the richer
      // live result because setup recheck itself is intentionally durable-only.
      ctx.setBusy(false, '');
      await ctx.recheck();
      ctx.setRuntimeStatus(next);
      return next;
    } catch (error) {
      ctx.setError(
        error?.message ||
          'That REAPER setup action did not complete. Follow the next step and check again.'
      );
      return null;
    } finally {
      ctx.setBusy(false, '');
    }
  }

  async function runtimePost(ctx, path, pending, method = 'POST', body) {
    const options = { method };
    if (body !== undefined) {
      options.headers = { 'Content-Type': 'application/json' };
      options.body = JSON.stringify(body);
    }
    return transition(ctx, pending, () => ctx.runtimeRequest(path, options));
  }

  function instruction(host, title, steps, ctx) {
    let panel = host.querySelector?.('.reaper-runtime-instruction');
    if (!panel) {
      panel = el('section', 'reaper-runtime-instruction');
      panel.tabIndex = -1;
      panel.setAttribute('role', 'status');
      panel.setAttribute('aria-live', 'polite');
      host.appendChild(panel);
    }
    panel.textContent = '';
    panel.appendChild(el('h4', 'reaper-runtime-instruction-title', title));
    const list = el('ol', 'reaper-runtime-instruction-list');
    steps.forEach(step => list.appendChild(el('li', '', step)));
    panel.appendChild(list);
    panel.appendChild(
      button('Check again', () => runtimePost(ctx, '/recheck', 'Checking REAPER setup…'), {
        primary: true
      })
    );
    panel.focus?.();
  }

  async function repairPlugin(ctx, code, host) {
    if (code === 'install_reaper_plugin') {
      if (host && window.ReaperPluginInstall) {
        window.ReaperPluginInstall.begin({
          host,
          declaredSource: REAPER_PLUGIN_URL,
          onComplete: async () => {
            const next = await ctx.refreshRuntime();
            ctx.setRuntimeStatus(next);
            await ctx.recheck();
          },
          onCancel: () => ctx.refreshRuntime()
        });
        return;
      }
      window.open('/plugins?install=reaper-plugin', '_blank', 'noopener');
      return;
    }
    await transition(ctx, 'Updating the REAPER plugin…', async () => {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(ctx.workspaceId)}/reaper-setup/repair`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ confirm_enable: code === 'enable_reaper_plugin' })
        }
      );
      if (!response.ok) throw new Error('The REAPER plugin could not be updated.');
      return ctx.runtimeRequest('');
    });
  }

  async function selectedAgentInstance(ctx) {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(ctx.workspaceId)}`);
    if (!response.ok) throw new Error('The selected workspace agent could not be read.');
    const payload = await response.json();
    const workspace = payload?.workspace || payload;
    const instances = Array.isArray(workspace?.agent_instances) ? workspace.agent_instances : [];
    const setupTask = (workspace?.tasks || []).find(task => task?.context?.template_setup === true);
    const nodeId = String(setupTask?.assigned_node_id || '').trim();
    if (nodeId) {
      const byNode = instances.find(instance => String(instance?.node_id || '').trim() === nodeId);
      if (byNode?.id) return byNode;
    }
    const name = String(setupTask?.to || workspace?.entry_agent_name || '')
      .trim()
      .toLowerCase();
    const matches = instances.filter(
      instance =>
        String(instance?.name || '')
          .trim()
          .toLowerCase() === name
    );
    if (matches.length === 1 && matches[0]?.id) return matches[0];
    throw new Error(
      'Choose one Codex or Claude Code workspace agent before granting REAPER access.'
    );
  }

  async function grant(ctx, revoke = false) {
    const agent = await selectedAgentInstance(ctx);
    return runtimePost(
      ctx,
      `/requirements/${REQUIREMENT}/grants`,
      revoke ? 'Revoking REAPER access…' : 'Granting REAPER access…',
      revoke ? 'DELETE' : 'POST',
      { agent_instance_id: agent.id }
    );
  }

  async function chooseCompatibleAgent(ctx) {
    if (choosingAgent) return;
    choosingAgent = true;
    ctx.setError('');
    const modelModal = document.getElementById?.('workspace-detail-agent-model-modal');
    let reopenSetup = null;
    try {
      const agent = await selectedAgentInstance(ctx);
      if (!modelModal || typeof window.workspaceDetail?.openAgentModelModal !== 'function') {
        throw new Error('The workspace agent model picker is unavailable on this page.');
      }

      reopenSetup = () => window.SetupWizard?.open?.(ctx.step?.id);
      modelModal.addEventListener('hidden.bs.modal', reopenSetup, { once: true });
      const closed = await window.SetupWizard?.close?.();
      if (closed === false) {
        throw new Error('Wait for the current setup action to finish, then try again.');
      }
      const opened = await window.workspaceDetail.openAgentModelModal(
        encodeURIComponent(agent.name),
        {
          allowedProviders: ['codex', 'claude_code'],
          title: `Choose a compatible model for ${agent.name}`,
          help: 'Choose an OpenAI Codex or Claude Code CLI model for local REAPER control.'
        }
      );
      if (!opened) {
        throw new Error('Ori could not open compatible agent choices.');
      }
    } catch (error) {
      if (reopenSetup) {
        modelModal?.removeEventListener?.('hidden.bs.modal', reopenSetup);
        window.SetupWizard?.open?.(ctx.step?.id);
      }
      ctx.setError(error?.message || 'Ori could not open compatible agent choices.');
    } finally {
      choosingAgent = false;
    }
  }

  async function openProject(ctx) {
    ctx.setBusy(true, 'Asking macOS to open the workspace project…');
    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(ctx.workspaceId)}/project/open`,
        { method: 'POST' }
      );
      if (!response.ok) throw new Error('The workspace project could not be opened.');
      ctx.announce(
        'Open requested. Wait for REAPER to finish opening the project, then choose Check connection.'
      );
    } catch (error) {
      ctx.setError(error?.message || 'The workspace project could not be opened.');
    } finally {
      ctx.setBusy(false, '');
    }
  }

  function renderDisclosure(host, text) {
    if (!text) return;
    const box = el('aside', 'reaper-runtime-access-disclosure');
    box.appendChild(el('h4', '', 'Before granting REAPER access'));
    box.appendChild(el('p', '', text));
    host.appendChild(box);
  }

  function renderCurrentAction(host, status, ctx) {
    const action = firstAction(status);
    const code = String(action?.code || action?.token || '');
    const requirementStatus = requirement(status);
    const actions = el('div', 'reaper-runtime-actions');
    host.appendChild(actions);

    const checkConnection = () =>
      runtimePost(ctx, '/recheck', 'Checking the current REAPER connection…');

    switch (code) {
      case 'download_reaper': {
        const link = el('a', 'modern-btn modern-btn-primary', 'Download REAPER');
        link.href = DOWNLOAD_URL;
        link.target = '_blank';
        link.rel = 'noopener';
        actions.appendChild(link);
        actions.appendChild(button('Check again', checkConnection));
        break;
      }
      case 'enable_web_remote':
      case 'check_web_remote':
        actions.appendChild(
          button(
            action?.label || 'Enable Web Remote',
            () =>
              instruction(
                host,
                'Enable REAPER Web Remote',
                [
                  'In REAPER, open Preferences, then Control/OSC/web.',
                  'Add or enable a Web browser interface bound to this Mac only.',
                  'Apply the change, leave REAPER open, then check again.'
                ],
                ctx
              ),
            { primary: true }
          )
        );
        break;
      case 'set_up_runner':
        actions.appendChild(
          button(
            action?.label || 'Set up runner',
            () =>
              instruction(
                host,
                'Register the trusted Ori REAPER runner',
                [
                  'Open REAPER’s Actions list and choose Load ReaScript.',
                  'Use the runner supplied by the installed reaper-plugin; do not use model-generated Lua.',
                  'Run the one-time registration action, then check again.'
                ],
                ctx
              ),
            { primary: true }
          )
        );
        break;
      case 'install_reaper_plugin':
      case 'enable_reaper_plugin':
      case 'attach_reaper_plugin':
        actions.appendChild(
          button(action?.label || 'Update REAPER plugin', () => repairPlugin(ctx, code, actions), {
            primary: true
          })
        );
        break;
      case 'choose_reaper_agent':
      case 'choose_runtime_agent':
        actions.appendChild(
          button('Choose compatible agent', () => chooseCompatibleAgent(ctx), { primary: true })
        );
        break;
      case 'grant_reaper_access':
      case 'grant_runtime_access':
        renderDisclosure(host, requirementStatus?.disclosure || '');
        actions.appendChild(button('Grant REAPER access', () => grant(ctx), { primary: true }));
        break;
      case 'test_reaper_connection':
        actions.appendChild(
          button(
            'Test REAPER connection',
            () =>
              runtimePost(
                ctx,
                `/requirements/${REQUIREMENT}/verify`,
                'Running the harmless project-specific REAPER test…'
              ),
            { primary: true }
          )
        );
        break;
      case 'open_correct_project':
      case 'open_check_reaper':
        actions.appendChild(
          button(action?.label || 'Open workspace project', () => openProject(ctx), {
            primary: true
          })
        );
        actions.appendChild(button('Check connection', checkConnection));
        break;
      case 'check_reaper_connection':
      case 'check_reaper_installation':
        actions.appendChild(
          button(action?.label || 'Check again', checkConnection, { primary: true })
        );
        break;
      default:
        if (status?.durable_state === 'configured') {
          actions.appendChild(button('Check connection', checkConnection, { primary: true }));
        }
    }

    if (selectedMode(status) === 'ori_assisted' && status?.durable_state === 'configured') {
      actions.appendChild(button('Revoke REAPER access', () => grant(ctx, true)));
    }
  }

  async function render(container, ctx) {
    if (!owns(ctx.step)) {
      ctx.renderDefault(container);
      return;
    }
    let status = ctx.runtimeStatus;
    if (!status) {
      try {
        status = await ctx.refreshRuntime();
      } catch (error) {
        container.appendChild(
          el(
            'p',
            'setup-wizard-error',
            'Runtime status could not be loaded. Close and reopen setup to try again.'
          )
        );
        return;
      }
    }
    if (selectedMode(status) !== 'ori_assisted') {
      const summary = el('section', 'reaper-runtime-file-only');
      summary.appendChild(el('strong', '', 'File-only'));
      summary.appendChild(
        el(
          'p',
          '',
          'Project-file work is available. REAPER, Web Remote, the runner, and live control were not configured or tested.'
        )
      );
      container.appendChild(summary);
      return;
    }

    const current = requirement(status);
    const lead = el(
      'p',
      'reaper-runtime-summary',
      current?.summary || 'Check local REAPER control.'
    );
    lead.setAttribute('role', 'status');
    lead.setAttribute('aria-live', 'polite');
    container.appendChild(lead);
    renderChecklist(container, status);
    renderLive(container, status);
    renderVerificationHistory(container, status);
    renderCurrentAction(container, status, ctx);
  }

  const renderer = { owns, render };

  function register() {
    if (registered || !window.SetupWizard?.registerStepRenderer) return;
    window.SetupWizard.registerStepRenderer('runtime_readiness', renderer);
    registered = true;
  }

  function init() {
    register();
    if (!registered && typeof document !== 'undefined') {
      document.addEventListener('DOMContentLoaded', register, { once: true });
    }
  }

  window.ReaperReadinessPanel = {
    init,
    register,
    _internals: {
      checks,
      checklistState,
      firstReason,
      livePresentation,
      owns,
      selectedMode
    }
  };
  init();
})();
