// Reviewed Settings reset controller. Preview and execution use server-held
// scope; browser state stores only the non-secret operation identity needed to
// recover a lost response or a required process relaunch.
(function () {
  const byId = id => document.getElementById(id);

  function bindGuidanceReset({
    buttonId,
    statusId,
    nextId,
    prompt,
    pending,
    endpoint,
    verify,
    success
  }) {
    let running = false;
    byId(buttonId)?.addEventListener('click', async function () {
      if (running) return;
      const button = this;
      const guidanceStatus = byId(statusId);
      const next = byId(nextId);
      if (!window.confirm(prompt)) return;
      running = true;
      if (next) next.hidden = true;
      button.disabled = true;
      button.setAttribute('aria-busy', 'true');
      if (guidanceStatus) guidanceStatus.textContent = pending;
      try {
        const response = await fetch(endpoint, {
          method: 'POST',
          headers: { 'X-Requested-With': 'XMLHttpRequest' }
        });
        let result = null;
        try {
          result = await response.json();
        } catch {
          // A non-JSON response is not verified success.
        }
        if (!response.ok || !verify(result)) {
          throw new Error('The server did not verify the reset.');
        }
        if (guidanceStatus) guidanceStatus.textContent = success;
        if (next) next.hidden = false;
      } catch (requestError) {
        if (guidanceStatus) {
          guidanceStatus.textContent = `${requestError.message} Nothing else was reset; try again.`;
        }
      } finally {
        running = false;
        button.disabled = false;
        button.setAttribute('aria-busy', 'false');
        guidanceStatus?.focus?.();
      }
    });
  }

  bindGuidanceReset({
    buttonId: 'replaySetupBtn',
    statusId: 'replaySetupStatus',
    nextId: 'openReplaySetupBtn',
    prompt:
      'Replay setup steps?\n\nYour names, profile, agents, workspaces, conversations, credentials, and Getting Started progress stay in place. This does not create a new identity.',
    pending: 'Resetting setup steps…',
    endpoint: '/api/onboarding/reset',
    verify: result =>
      result?.needs_onboarding === true &&
      result?.completed === false &&
      Array.isArray(result?.steps_completed) &&
      result.steps_completed.length === 0,
    success: 'Setup steps reset. Your identity and existing work were kept.'
  });

  bindGuidanceReset({
    buttonId: 'resetGettingStartedBtn',
    statusId: 'resetGettingStartedStatus',
    nextId: 'openGettingStartedBtn',
    prompt:
      'Reset Getting Started progress?\n\nQuest completions, skips, and dismissal are cleared. Setup completion and all user data stay in place.',
    pending: 'Resetting Getting Started…',
    endpoint: '/api/progression/reset',
    verify: result =>
      result?.completed_count === 0 && result?.resolved_count === 0 && result?.dismissed === false,
    success: 'Getting Started reset. Setup and user data were kept.'
  });

  byId('openReplaySetupBtn')?.addEventListener('click', () => window.location.reload());

  const reviewButton = byId('resetAppBtn');
  const startFreshButton = byId('startFreshBtn');
  if (!reviewButton) return;

  const choices = {
    settings: { node: byId('resetSettings'), category: 'settings' },
    agents: { node: byId('resetAgents'), category: 'agents' },
    sessions: { node: byId('resetSessions'), category: 'app_records' },
    onboarding: { node: byId('resetOnboarding'), category: 'setup_steps' }
  };
  const labels = {
    settings: 'Settings & API keys',
    agents: 'Agents',
    app_records: 'Conversation & app records',
    setup_steps: 'Setup steps',
    identity_progress: 'Identity, setup & progress',
    app_configuration: 'Supplemental app configuration',
    integrations: 'Local integrations',
    templates: 'Ori-owned templates',
    activity: 'Usage & activity',
    runtime_cache: 'Generated runtime state'
  };
  const operationStorageKey = 'ori.reset.operation_id';
  const selectAll = byId('selectAllResetBtn');
  const clearSelection = byId('clearAllResetBtn');
  const input = byId('resetConfirmInput');
  const confirmButton = byId('confirmResetBtn');
  const error = byId('resetConfirmError');
  const items = byId('resetItemsList');
  const modalElement = byId('resetConfirmModal');
  const modalTitle = byId('resetConfirmTitleText');
  const modalWarning = byId('resetConfirmWarning');
  const resultPanel = byId('resetOperationPanel');
  const status = byId('resetOperationStatus');
  const results = byId('resetOperationResults');
  const openStartFreshSetup = byId('openStartFreshSetupBtn');
  const dismissButtons = [byId('resetCancelBtn'), byId('resetCloseBtn')];
  let phase = 'idle';
  let reviewed = null;
  let reviewFocus = reviewButton;

  function selectedCategories() {
    return Object.values(choices)
      .filter(choice => choice.node?.checked)
      .map(choice => choice.category);
  }

  function requestID() {
    if (window.crypto?.randomUUID) return window.crypto.randomUUID();
    return `reset-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  }

  function updateControls() {
    const locked = phase !== 'idle';
    Object.values(choices).forEach(choice => {
      if (choice.node) choice.node.disabled = locked;
    });
    if (selectAll) selectAll.disabled = locked;
    if (clearSelection) clearSelection.disabled = locked;
    reviewButton.disabled = locked || selectedCategories().length === 0;
    if (startFreshButton) startFreshButton.disabled = locked;
    if (input) input.disabled = phase !== 'review' || Boolean(reviewed?.blockers?.length);
    if (confirmButton) {
      confirmButton.disabled =
        phase !== 'review' ||
        input?.value !== 'RESET' ||
        !reviewed ||
        Boolean(reviewed.blockers?.length);
      confirmButton.textContent =
        phase === 'submitting'
          ? 'Submitting reset…'
          : reviewed?.intent === 'start_fresh'
            ? 'Start Fresh after full relaunch'
            : 'Reset reviewed data';
      confirmButton.setAttribute('aria-busy', String(phase === 'submitting'));
    }
    dismissButtons.forEach(node => {
      if (node) node.disabled = phase === 'submitting';
    });
  }

  function showError(message) {
    if (!error) return;
    error.textContent = message;
    error.style.display = message ? 'block' : 'none';
  }

  function showResult(message, rows = []) {
    if (resultPanel) resultPanel.hidden = false;
    if (status) status.textContent = message;
    if (!results) return;
    results.replaceChildren();
    for (const row of rows) {
      const li = document.createElement('li');
      li.textContent = row;
      results.appendChild(li);
    }
  }

  function previewRows(preview) {
    const rows = [];
    if (preview.intent === 'start_fresh') {
      let knownItems = 0;
      let unavailableCounts = 0;
      for (const category of preview.categories || []) {
        for (const fact of category.facts || []) {
          if (typeof fact.count === 'number') knownItems += fact.count;
          else unavailableCounts += 1;
        }
      }
      const categoryCount = (preview.categories || []).length;
      rows.push(
        `Start Fresh impact: ${categoryCount} ${categoryCount === 1 ? 'category' : 'categories'}, ${knownItems} currently counted items${unavailableCounts ? `, ${unavailableCounts} unavailable counts` : ''}. Exact owner paths and preservation rules follow.`
      );
    }
    for (const category of preview.categories || []) {
      rows.push(
        `${category.label || labels[category.id] || category.id}: ${category.description || ''}`
      );
      for (const fact of category.facts || []) {
        rows.push(
          fact.count === null || fact.count === undefined
            ? `${fact.name}: unavailable — ${fact.unavailable_reason || 'review required'}`
            : `${fact.name}: ${fact.count}`
        );
      }
      for (const location of category.removed || []) {
        rows.push(`Remove: ${location.display_path} — ${location.reason}`);
      }
      for (const location of category.retained || []) {
        rows.push(`Keep: ${location.display_path} — ${location.reason}`);
      }
    }
    for (const blocker of preview.blockers || []) {
      rows.push(`Blocked: ${blocker.message} ${blocker.recovery || ''}`.trim());
    }
    if (preview.restart?.instructions) rows.push(`Afterward: ${preview.restart.instructions}`);
    return rows;
  }

  function renderPreview(preview) {
    items?.replaceChildren();
    for (const row of previewRows(preview)) {
      const li = document.createElement('li');
      li.textContent = row;
      items?.appendChild(li);
    }
    if (modalTitle) {
      modalTitle.textContent =
        preview.intent === 'start_fresh' ? 'Confirm Start Fresh' : 'Confirm reviewed data reset';
    }
    if (modalWarning) {
      modalWarning.textContent =
        preview.intent === 'start_fresh'
          ? 'Warning: Start Fresh permanently removes every reviewed Ori-owned category below. Retained folders and vault packages stay detached.'
          : 'Warning: You are about to permanently delete the reviewed data below.';
    }
    if (preview.blockers?.length) {
      showError(
        'Reset is blocked. Resolve every blocker and review again; no data has been deleted.'
      );
    } else {
      showError('');
    }
  }

  function operationRows(operation) {
    const rows = [];
    for (const result of operation?.results || []) {
      const name = labels[result.id] || result.id;
      rows.push(`${name}: ${result.outcome}${result.message ? ` — ${result.message}` : ''}`);
      for (const check of result.checks || []) {
        rows.push(
          `${name} / ${check.name}: ${check.outcome}${check.message ? ` — ${check.message}` : ''}`
        );
      }
      for (const kept of result.retained || [])
        rows.push(`Kept: ${kept.display_path} — ${kept.reason}`);
    }
    for (const blocker of operation?.blockers || []) {
      rows.push(`Blocked: ${blocker.message}${blocker.recovery ? ` — ${blocker.recovery}` : ''}`);
    }
    return rows;
  }

  function clearFreshBrowserState() {
    const exactLocal = [
      'ori-theme',
      'ori-ui-density',
      'voiceSettings',
      'note.openBehavior',
      'enterToSend',
      'planBeforeAction',
      'chatPanelWidth',
      'sidebarWidth',
      'sessionFolderCollapsed',
      'sessionSidebarCollapsed',
      'sessionSidebarWidth',
      'oriWorkspaceHubLauncherView',
      'oriWorkspaceCommandViewMode',
      'oriWorkspaceDetailView',
      'canvas-bg-color',
      'note.leftRail.tab',
      'note.toc.collapsed',
      'note.aiAssist.collapsed',
      'ori_chat_assistant',
      'activeSessionId',
      'ori.homeAssistant.recentSessions',
      'oriWorkspaceSyncDismissed',
      'note.tabs',
      'note.search.recent',
      'ori.roster.selectedAgent',
      'ori-selected-vault-id',
      'ori-vault-active-tab',
      'ori.personalAssistantHireRequestId',
      'ori.personalAssistantHQRequestId',
      'ori.homeAssistant.automationMode'
    ];
    const exactSession = [
      'activeSessionId',
      'ori.homeAssistant.recentSessions',
      'oriWorkspaceHubOverviewExpanded',
      'oriWorkspaceHubLauncherTab',
      'oriWorkspaceHubSelectedId',
      'currentWorkspaceId',
      'workspace-detail-task-assist-specialist',
      'ori.homeAssistant.pendingWorkspacePrompt',
      'ori-guide-handoff',
      'ori.homeAssistant.automationMode',
      'ori-keyboard-nav-persistent'
    ];
    const prefixes = [
      'ori_chat_session_',
      'ori_chat_assistant_',
      'activeSessionId_',
      'note.tabs.workspace.',
      'ori.evolution.lastStage.',
      'ori.personalAssistantApplyRequestId.',
      'workspace-directory-explorer:',
      'ori-workspace-command-agent:',
      'oriSetupWizardResume:',
      'oriProjectOpenNotice:',
      'workspace-detail-entry-agent-prompt-dismissed:'
    ];
    const clear = (storage, exact) => {
      if (!storage) return;
      try {
        exact.forEach(key => storage.removeItem?.(key));
        if (typeof storage.length !== 'number' || typeof storage.key !== 'function') return;
        const found = [];
        for (let index = 0; index < storage.length; index += 1) {
          const key = storage.key(index);
          if (typeof key === 'string' && prefixes.some(prefix => key.startsWith(prefix)))
            found.push(key);
        }
        found.forEach(key => storage.removeItem(key));
      } catch {
        // Browser storage is best effort and never changes the server's verified outcome.
      }
    };
    clear(window.localStorage, exactLocal);
    clear(window.sessionStorage, exactSession);
  }

  function renderOperation(operation) {
    const state = operation?.state || 'interrupted';
    phase = state === 'completed' ? 'idle' : state;
    if (openStartFreshSetup) {
      openStartFreshSetup.hidden = !(state === 'completed' && operation?.intent === 'start_fresh');
    }
    const messages = {
      preparing:
        'Reset admission is preparing. Ordinary work remains fenced while recovery is established.',
      awaiting_restart:
        'Restart required. Fully quit and relaunch Ori using the same installation; browser reload and menubar Stop/Start are not enough.',
      applying: 'Reset is applying before application stores open.',
      verifying: 'Reset was applied and its named postconditions are being verified.',
      completed: 'Reset completed and its named postconditions were verified.',
      partial_failure:
        'Reset partially completed. Keep this result and retry only unresolved categories when offered.',
      blocked:
        'Reset is blocked. No unchecked category should be repeated; follow the recovery details below.',
      interrupted:
        'Reset outcome is interrupted or unknown. Preserve recovery metadata and do not submit another reset.'
    };
    showResult(messages[state] || messages.interrupted, operationRows(operation));
    if (state === 'completed' && operation?.intent === 'start_fresh') {
      clearFreshBrowserState();
      try {
        window.localStorage?.setItem('ori.reset.generation', operation.id);
      } catch {
        // Other tabs may need a manual reload when browser storage is unavailable.
      }
    }
    if (operation?.id) {
      try {
        window.localStorage?.setItem(operationStorageKey, operation.id);
      } catch {
        // The durable server receipt remains authoritative.
      }
    }
  }

  function responseMessage(payload, fallback) {
    if (payload && typeof payload.message === 'string' && payload.message) return payload.message;
    if (Array.isArray(payload?.errors) && typeof payload.errors[0] === 'string')
      return payload.errors[0];
    return fallback;
  }

  openStartFreshSetup?.addEventListener('click', () => {
    window.location.href = '/';
  });

  async function readJSON(response) {
    try {
      const payload = await response.json();
      return payload && typeof payload === 'object' ? payload : null;
    } catch {
      return null;
    }
  }

  async function recoverOperation() {
    let operationID = '';
    try {
      operationID = window.localStorage?.getItem(operationStorageKey) || '';
    } catch {
      return;
    }
    if (!operationID) return;
    try {
      const response = await fetch(`/api/reset/operations/${encodeURIComponent(operationID)}`, {
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      });
      const payload = await readJSON(response);
      if (payload?.operation) renderOperation(payload.operation);
      else if (!response.ok)
        showResult(
          'A reset recovery receipt exists, but its current outcome could not be loaded. Do not submit another reset.'
        );
    } catch {
      showResult(
        'A reset recovery receipt exists, but the server is unreachable. Keep this page and relaunch the same Ori installation.'
      );
    }
    updateControls();
  }

  function invalidateReview() {
    if (phase === 'review') {
      reviewed = null;
      phase = 'idle';
      if (input) input.value = '';
      showError('Selection changed. Close this dialog and review the scope again.');
    }
    updateControls();
  }
  Object.values(choices).forEach(choice =>
    choice.node?.addEventListener('change', invalidateReview)
  );
  for (const [button, checked] of [
    [selectAll, true],
    [clearSelection, false]
  ]) {
    button?.addEventListener('click', () => {
      if (phase !== 'idle') return;
      Object.values(choices).forEach(choice => {
        if (choice.node) choice.node.checked = checked;
      });
      updateControls();
    });
  }

  async function reviewReset(intent, categories) {
    if (phase !== 'idle' || (intent === 'selected_data' && !categories.length)) return;
    reviewFocus = intent === 'start_fresh' ? startFreshButton : reviewButton;
    phase = 'previewing';
    reviewed = null;
    if (openStartFreshSetup) openStartFreshSetup.hidden = true;
    showResult('Loading the authoritative reset scope. No data is being changed.');
    updateControls();
    try {
      const query = new URLSearchParams({ intent });
      categories.forEach(category => query.append('category', category));
      const response = await fetch(`/api/reset/preview?${query.toString()}`, {
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      });
      const preview = await readJSON(response);
      if (
        !response.ok ||
        !preview?.id ||
        !preview?.operation_id ||
        !Array.isArray(preview.categories)
      ) {
        throw new Error(
          responseMessage(preview, 'Reset preview is unavailable; no data was deleted.')
        );
      }
      reviewed = preview;
      phase = 'review';
      if (input) input.value = '';
      renderPreview(preview);
      updateControls();
      new bootstrap.Modal(modalElement).show();
    } catch (requestError) {
      phase = 'idle';
      showResult(requestError?.message || 'Reset preview is unavailable; no data was deleted.');
      updateControls();
      status?.focus();
    }
  }

  reviewButton.addEventListener('click', () => reviewReset('selected_data', selectedCategories()));
  startFreshButton?.addEventListener('click', () => reviewReset('start_fresh', []));

  input?.addEventListener('input', () => {
    if (phase !== 'review' || reviewed?.blockers?.length) return;
    showError('');
    updateControls();
  });
  input?.addEventListener('keydown', event => {
    if (event.key !== 'Enter') return;
    event.preventDefault();
    if (phase === 'review' && !confirmButton?.disabled) confirmButton?.click();
  });

  confirmButton?.addEventListener('click', async () => {
    if (phase !== 'review' || !reviewed || reviewed.blockers?.length || input?.value !== 'RESET')
      return;
    const acceptedPreview = reviewed;
    const resetRequestID = requestID();
    phase = 'submitting';
    try {
      window.localStorage?.setItem(operationStorageKey, acceptedPreview.operation_id);
    } catch {
      // Submission still returns and renders the durable operation identity.
    }
    updateControls();
    showResult(
      'Submitting the reviewed reset. Closing the dialog does not cancel an accepted request.'
    );
    try {
      const response = await fetch('/api/reset', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
        body: JSON.stringify({
          preview_id: acceptedPreview.id,
          request_id: resetRequestID,
          confirmation: 'RESET'
        })
      });
      const payload = await readJSON(response);
      if (payload?.operation) renderOperation(payload.operation);
      else {
        phase = 'unknown';
        showResult(
          responseMessage(
            payload,
            'Reset did not return a recoverable operation. Keep the recovery identity and do not submit again.'
          )
        );
      }
    } catch {
      phase = 'unknown';
      showResult(
        'Reset response was lost. This is not proof of success or failure; recover the saved operation after relaunch instead of submitting again.'
      );
    } finally {
      updateControls();
      bootstrap.Modal.getInstance(modalElement)?.hide();
      status?.focus();
    }
  });

  modalElement?.addEventListener('shown.bs.modal', () => {
    if (phase === 'review' && !reviewed?.blockers?.length) input?.focus();
    else error?.focus?.();
  });
  modalElement?.addEventListener('hide.bs.modal', event => {
    if (phase === 'submitting') event.preventDefault();
  });
  modalElement?.addEventListener('hidden.bs.modal', () => {
    if (phase !== 'review') return;
    phase = 'idle';
    reviewed = null;
    if (input) input.value = '';
    showError('');
    updateControls();
    reviewFocus?.focus();
  });

  updateControls();
  void recoverOperation();
})();
