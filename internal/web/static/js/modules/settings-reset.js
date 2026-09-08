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
    setup_steps: 'Setup steps'
  };
  const operationStorageKey = 'ori.reset.operation_id';
  const selectAll = byId('selectAllResetBtn');
  const clearSelection = byId('clearAllResetBtn');
  const input = byId('resetConfirmInput');
  const confirmButton = byId('confirmResetBtn');
  const error = byId('resetConfirmError');
  const items = byId('resetItemsList');
  const modalElement = byId('resetConfirmModal');
  const resultPanel = byId('resetOperationPanel');
  const status = byId('resetOperationStatus');
  const results = byId('resetOperationResults');
  const dismissButtons = [byId('resetCancelBtn'), byId('resetCloseBtn')];
  let phase = 'idle';
  let reviewed = null;

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
    if (input) input.disabled = phase !== 'review' || Boolean(reviewed?.blockers?.length);
    if (confirmButton) {
      confirmButton.disabled =
        phase !== 'review' ||
        input?.value !== 'RESET' ||
        !reviewed ||
        Boolean(reviewed.blockers?.length);
      confirmButton.textContent =
        phase === 'submitting' ? 'Submitting reset…' : 'Reset reviewed data';
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

  function renderOperation(operation) {
    const state = operation?.state || 'interrupted';
    phase = state === 'completed' ? 'complete' : state;
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
    if (state === 'completed') window.localStorage?.removeItem(operationStorageKey);
    else if (operation?.id) window.localStorage?.setItem(operationStorageKey, operation.id);
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

  async function recoverOperation() {
    const operationID = window.localStorage?.getItem(operationStorageKey);
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

  reviewButton.addEventListener('click', async () => {
    if (phase !== 'idle') return;
    const categories = selectedCategories();
    if (!categories.length) return;
    phase = 'previewing';
    reviewed = null;
    showResult('Loading the authoritative reset scope. No data is being changed.');
    updateControls();
    try {
      const query = new URLSearchParams({ intent: 'selected_data' });
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
  });

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
    window.localStorage?.setItem(operationStorageKey, acceptedPreview.operation_id);
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
    reviewButton.focus();
  });

  updateControls();
  void recoverOperation();
})();
