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
    onboarding: { node: byId('resetOnboarding'), category: 'setup_steps' },
    installedPlugins: { node: byId('resetInstalledPlugins'), category: 'installed_plugins' }
  };
  const labels = {
    settings: 'Settings & API keys',
    agents: 'Agents',
    app_records: 'Conversation & app records',
    setup_steps: 'Setup steps',
    installed_plugins: 'Installed plugins',
    identity_progress: 'Identity, setup & progress',
    app_configuration: 'Supplemental app configuration',
    integrations: 'Local integrations',
    templates: 'Ori-owned templates',
    activity: 'Usage & activity',
    runtime_cache: 'Generated runtime state'
  };
  const operationStorageKey = 'ori.reset.operation_id';
  const operationIDPattern = /^[A-Za-z0-9_-]{1,128}$/;
  // These responses prove this request was refused before admission. Other
  // errors (including missing/unparseable replies) cannot rule out staging.
  const admissionRefusals = new Set([
    'invalid_request',
    'preview_required',
    'preview_expired',
    'scope_changed',
    'preview_blocked',
    'active_work',
    'agents_folder_confirmation_required',
    'operation_conflict',
    'lifecycle_unavailable',
    'preview_limit'
  ]);
  const selectAll = byId('selectAllResetBtn');
  const clearSelection = byId('clearAllResetBtn');
  const input = byId('resetConfirmInput');
  const confirmButton = byId('confirmResetBtn');
  const error = byId('resetConfirmError');
  const items = byId('resetItemsList');
  // Blockers render beside the confirm box they lock, not at the end of the
  // reviewed scope, where a long Start Fresh list buries them.
  const blockerPanel = byId('resetBlockers');
  const blockerList = byId('resetBlockerList');
  const modalElement = byId('resetConfirmModal');
  const modalTitle = byId('resetConfirmTitleText');
  const modalWarning = byId('resetConfirmWarning');
  const resultPanel = byId('resetOperationPanel');
  const status = byId('resetOperationStatus');
  const results = byId('resetOperationResults');
  const dismissButtons = [byId('resetCancelBtn'), byId('resetCloseBtn')];
  // A reset that removes the agents from the Workspace Directory needs a second
  // confirmation naming that folder: the user can see it and may sync it to
  // other machines. The server refuses the reset without it.
  const agentsFolderBlock = byId('resetAgentsFolderConfirm');
  const agentsFolderPath = byId('resetAgentsFolderPath');
  const agentsFolderNotice = byId('resetAgentsFolderNotice');
  const agentsFolderCheck = byId('resetAgentsFolderCheck');
  // Start Fresh also removes the Skills folder and the plugin list beside the
  // agents folder, under the same confirmation.
  const agentsFolderAlso = byId('resetAgentsFolderAlso');
  const agentsFolderAlsoList = byId('resetAgentsFolderAlsoList');
  const agentsFolderCheckLabel = byId('resetAgentsFolderCheckLabel');
  let phase = 'idle';
  let reviewed = null;
  let reviewFocus = reviewButton;

  function agentsFolderUnconfirmed() {
    return Boolean(reviewed?.agents_folder?.confirmation_required) && !agentsFolderCheck?.checked;
  }

  function clearAgentsFolder() {
    if (agentsFolderCheck) agentsFolderCheck.checked = false;
    if (agentsFolderBlock) agentsFolderBlock.hidden = true;
  }

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
    if (agentsFolderCheck) {
      agentsFolderCheck.disabled = phase !== 'review' || Boolean(reviewed?.blockers?.length);
    }
    if (confirmButton) {
      confirmButton.disabled =
        phase !== 'review' ||
        input?.value !== 'RESET' ||
        !reviewed ||
        Boolean(reviewed.blockers?.length) ||
        agentsFolderUnconfirmed();
      confirmButton.textContent =
        phase === 'submitting'
          ? 'Submitting reset…'
          : reviewed?.intent === 'start_fresh'
            ? reviewed.restart?.managed
              ? 'Start Fresh and restart Ori'
              : 'Start Fresh after full relaunch'
            : reviewed?.restart?.managed
              ? 'Reset and restart Ori'
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
      // Named members of a category's scope — today, each installed plugin. The
      // server bounds how many it names; a larger inventory reports the rest as
      // a count fact above rather than growing this list without limit.
      for (const item of category.items || []) {
        rows.push(`Item: ${item.name} — ${item.summary || ''}`.trim());
        for (const detail of item.details || []) rows.push(`  ${item.name}: ${detail}`);
      }
      for (const location of category.removed || []) {
        rows.push(`Remove: ${location.display_path} — ${location.reason}`);
      }
      for (const location of category.retained || []) {
        rows.push(`Keep: ${location.display_path} — ${location.reason}`);
      }
    }
    if (preview.restart?.instructions) rows.push(`Afterward: ${preview.restart.instructions}`);
    return rows;
  }

  function renderBlockers(blockers) {
    blockerList?.replaceChildren();
    for (const blocker of blockers) {
      const li = document.createElement('li');
      li.textContent = `${blocker.message} ${blocker.recovery || ''}`.trim();
      blockerList?.appendChild(li);
    }
    if (blockerPanel) blockerPanel.hidden = blockers.length === 0;
    if (input) {
      input.placeholder = blockers.length
        ? 'Locked until the problems above are fixed'
        : 'Type RESET to confirm';
    }
  }

  function renderArchiveReview(archive) {
    const section = byId('resetArchiveReview');
    if (!section) return;
    section.hidden = !archive;
    const eligible = Boolean(
      archive?.source && archive?.destination && !archive.unavailable_reason
    );
    byId('resetArchivePaths').hidden = !eligible;
    byId('resetArchiveSource').textContent = eligible ? archive.source : '';
    byId('resetArchiveDestination').textContent = eligible ? archive.destination : '';
    byId('resetArchiveSummary').textContent = archive?.requested
      ? 'Archive requested for this review.'
      : 'Personal HQ files stay where they are by default.';
    byId('resetArchiveNotice').textContent = archive?.unavailable_reason || archive?.notice || '';
  }

  function renderPreview(preview) {
    renderArchiveReview(preview.hq_archive);
    items?.replaceChildren();
    for (const row of previewRows(preview)) {
      const li = document.createElement('li');
      li.textContent = row;
      items?.appendChild(li);
    }
    renderBlockers(preview.blockers || []);
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
    const folder = preview.agents_folder;
    clearAgentsFolder();
    if (agentsFolderBlock) agentsFolderBlock.hidden = !folder;
    if (agentsFolderPath) agentsFolderPath.textContent = folder?.path || '';
    if (agentsFolderNotice) agentsFolderNotice.textContent = folder?.notice || '';
    const alsoRemoved = Array.isArray(folder?.also_removed) ? folder.also_removed : [];
    if (agentsFolderAlso) agentsFolderAlso.hidden = alsoRemoved.length === 0;
    if (agentsFolderAlsoList) {
      agentsFolderAlsoList.replaceChildren(
        ...alsoRemoved.map(path => {
          const item = document.createElement('li');
          const code = document.createElement('code');
          code.style.overflowWrap = 'anywhere';
          code.textContent = path;
          item.appendChild(code);
          return item;
        })
      );
    }
    if (agentsFolderCheckLabel) {
      agentsFolderCheckLabel.textContent = alsoRemoved.length
        ? 'I understand this removes my agents, my skills, and my plugin list from my Workspace Directory, including on other machines it syncs to.'
        : 'I understand this removes my agents from this folder, including on other machines it syncs to.';
    }
    if (preview.blockers?.length) {
      showError(
        'Reset is blocked by the problems listed above. Fix them, then review again; no data has been deleted.'
      );
    } else {
      showError('');
    }
  }

  function responseMessage(payload, fallback) {
    if (payload && typeof payload.message === 'string' && payload.message) return payload.message;
    if (Array.isArray(payload?.errors) && typeof payload.errors[0] === 'string')
      return payload.errors[0];
    return fallback;
  }

  async function readJSON(response) {
    try {
      const payload = await response.json();
      return payload && typeof payload === 'object' ? payload : null;
    } catch {
      return null;
    }
  }

  function openResetStatus(id) {
    phase = 'handoff';
    showResult('Opening reset status. Keep this tab open and do not submit another reset.');
    updateControls();
    window.location.href = `/reset/status?operation=${encodeURIComponent(id)}`;
  }

  async function recoverOperation() {
    let operationID = '';
    try {
      operationID = window.localStorage?.getItem(operationStorageKey) || '';
    } catch {
      return;
    }
    if (!operationIDPattern.test(operationID)) return;
    try {
      const response = await fetch(`/api/reset/operations/${encodeURIComponent(operationID)}`, {
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      });
      const payload = await readJSON(response);
      if (phase !== 'idle') return;
      if (response.ok && payload?.operation?.id === operationID) {
        if (payload.operation.state !== 'completed') openResetStatus(operationID);
      } else if (!response.ok && response.status !== 404)
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
      clearAgentsFolder();
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
        typeof preview?.operation_id !== 'string' ||
        !operationIDPattern.test(preview.operation_id) ||
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
      // getOrCreateInstance, not a fresh Modal: constructing a second instance
      // for the same element replaces the stored one while the first keeps its
      // listeners, and the competing pair can leave a reviewed dialog stuck open
      // after Cancel — with the destructive controls still behind it.
      bootstrap.Modal.getOrCreateInstance(modalElement).show();
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
  agentsFolderCheck?.addEventListener('change', () => {
    if (phase === 'review') updateControls();
  });
  input?.addEventListener('keydown', event => {
    if (event.key !== 'Enter') return;
    event.preventDefault();
    if (phase === 'review' && !confirmButton?.disabled) confirmButton?.click();
  });

  confirmButton?.addEventListener('click', async () => {
    if (
      phase !== 'review' ||
      !reviewed ||
      reviewed.blockers?.length ||
      input?.value !== 'RESET' ||
      agentsFolderUnconfirmed()
    )
      return;
    const acceptedPreview = reviewed;
    const resetRequestID = requestID();
    phase = 'submitting';
    let previousOperationID;
    try {
      previousOperationID = window.localStorage?.getItem(operationStorageKey);
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
          confirmation: 'RESET',
          // The reviewed folder, repeated: the server checks it is the same one.
          ...(acceptedPreview.agents_folder
            ? { confirm_agents_folder: acceptedPreview.agents_folder.path }
            : {})
        })
      });
      const payload = await readJSON(response);
      if (!response.ok && admissionRefusals.has(payload?.code)) {
        // The server may include the previous receipt in a refusal; neither
        // that receipt nor the unused preview ID belongs to this submission.
        try {
          const storage = window.localStorage;
          if (storage?.getItem(operationStorageKey) === acceptedPreview.operation_id) {
            if (previousOperationID) storage.setItem(operationStorageKey, previousOperationID);
            else storage.removeItem(operationStorageKey);
          }
        } catch {
          // Storage is optional; don't change the server's refusal result.
        }
        phase = 'idle';
        reviewed = null;
        showResult(responseMessage(payload, 'Reset was refused. Review the scope again.'));
      } else {
        openResetStatus(acceptedPreview.operation_id);
      }
    } catch {
      // Admission can keep running after disconnect. The preview supplied the
      // only ID this tab is waiting for, even when storage isn't available.
      openResetStatus(acceptedPreview.operation_id);
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
    clearAgentsFolder();
    showError('');
    updateControls();
    reviewFocus?.focus();
  });

  updateControls();
  void recoverOperation();
})();
