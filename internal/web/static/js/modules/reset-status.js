// The receipt and readiness must come from the same normal-app response.
// Keep polling read-only, even when this tab outlives the listener that served it.
(function () {
  const byID = id => document.getElementById(id);
  const id = byID('reset-operation-id')?.value;
  if (!id) return;

  const title = byID('reset-state-title');
  const message = byID('reset-state-message');
  const connection = byID('reset-connection');
  const blockers = byID('reset-blockers');
  const blockerList = byID('reset-blocker-list');
  const restart = byID('reset-restart-instructions');
  const results = byID('reset-results');
  const resultList = byID('reset-result-list');
  const entry = byID('reset-enter');
  const check = byID('reset-check');
  const minDelay = 750;
  const maxDelay = 8000;
  let delay = minDelay;
  let timer;
  let checking = false;
  let paused = false;
  let entering = false;
  let revision = Number(byID('reset-operation-id').dataset.revision) || 0;
  let rendered = '';

  const attention = [
    'Reset needs attention',
    'Ori is keeping normal work paused. Follow the recovery guidance below before restarting. Do not submit another reset.',
    true
  ];
  const states = {
    preparing: [
      'Preparing the reset',
      'Ori accepted your request and is stopping background work. Keep this tab open. Wait for restart instructions before quitting Ori.'
    ],
    awaiting_restart: [
      'Restart required',
      'Ori saved the reviewed reset and paused normal work. The reset will apply when a new Ori process starts, before opening your data.',
      true
    ],
    applying: [
      'Checking the reset on startup',
      'Ori is applying the reviewed changes and checking the result. Keep this tab open.'
    ],
    blocked: attention,
    partial_failure: attention,
    interrupted: attention,
    completed: [
      'Waiting for Ori to be ready',
      'The receipt says the reset finished, but this tab has not verified both the reset and normal app readiness.'
    ]
  };
  states.verifying = states.applying;
  const categoryLabels = {
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

  function setText(node, text) {
    if (node.textContent !== text) node.textContent = text;
  }

  function showConnection(text) {
    setText(connection, text);
    connection.hidden = !text;
  }

  function row(text) {
    const element = document.createElement('li');
    element.textContent = text;
    return element;
  }

  function renderArchive(archive) {
    const section = byID('reset-hq-archive');
    if (!section) return;
    section.hidden = !archive;
    const path = archive?.destination || '';
    byID('reset-hq-archive-location').hidden = !path;
    setText(
      byID('reset-hq-archive-label'),
      archive?.outcome === 'completed' ? 'Archived at:' : 'Reviewed destination:'
    );
    setText(byID('reset-hq-archive-path'), path);
    setText(byID('reset-hq-archive-message'), archive?.message || '');
  }

  function report(op, ready) {
    const managed = op.state === 'awaiting_restart' && op.restart?.managed === true;
    const key = `${op.revision}:${op.state}:${ready}:${managed}`;
    // Don't reset expanded details, scroll position or announcements on every poll.
    if (rendered === key) return;
    rendered = key;
    const [heading, description, showRestart] = ready
      ? ['Reset completed', 'Ori verified the reviewed changes and is ready.']
      : managed
        ? [
            'Restarting Ori',
            'Ori saved the reset and will try to restart automatically. Keep this tab open. If it cannot reconnect, use the manual steps below.',
            true
          ]
        : states[op.state];
    setText(title, heading);
    setText(message, description);
    document.title = `${heading} · Ori`;
    restart.hidden = !showRestart;
    renderArchive(op.hq_archive);
    const newBlockers = (op.blockers || []).map(blocker => {
      const item = row(blocker.recovery || '');
      const label = document.createElement('strong');
      label.textContent = blocker.message;
      item.prepend(label);
      return item;
    });
    blockerList.replaceChildren(...newBlockers);
    blockers.hidden = newBlockers.length === 0;
    resultList.replaceChildren(
      ...op.results.map(result => {
        const item = row(`${categoryLabels[result.id] || result.id}: ${result.outcome}`);
        if (result.message) item.append(` — ${result.message}`);
        const more = document.createElement('ul');
        for (const info of [...(result.items || []), ...(result.checks || [])]) {
          more.append(
            row(`${info.name}: ${info.outcome}${info.message ? ` — ${info.message}` : ''}`)
          );
        }
        for (const kept of result.retained || []) {
          more.append(row(`Kept: ${kept.display_path} — ${kept.reason}`));
        }
        if (more.childElementCount) item.append(more);
        return item;
      })
    );
    results.hidden = op.results.length === 0;
  }

  function verified(payload, op) {
    return (
      payload.success === true &&
      op.state === 'completed' &&
      !op.blockers?.length &&
      op.results.length > 0 &&
      (!op.hq_archive || (op.hq_archive.outcome === 'completed' && op.hq_archive.destination)) &&
      op.results.every(
        result =>
          ['completed', 'preserved'].includes(result.outcome) &&
          !result.retryable &&
          result.checks?.length > 0 &&
          result.checks.every(check => check.outcome === 'completed')
      )
    );
  }

  async function poll() {
    if (checking || paused) return;
    clearTimeout(timer);
    checking = true;
    let ready = false;
    const previous = rendered;
    try {
      const response = await fetch(`/api/reset/operations/${encodeURIComponent(id)}`, {
        mode: 'same-origin',
        redirect: 'error',
        cache: 'no-store',
        signal: AbortSignal.timeout(5000),
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      });
      if (paused) return;
      if (!response.ok || !response.headers.get('Content-Type')?.startsWith('application/json')) {
        showConnection(
          'Ori responded but could not confirm this receipt. Check that you restarted the same installation.'
        );
        entry.hidden = true;
        return;
      }
      const payload = await response.json();
      if (paused) return;
      const op = payload?.operation;
      if (
        op?.id !== id ||
        op.schema_version !== 1 ||
        !['start_fresh', 'selected_data'].includes(op.intent) ||
        !Number.isSafeInteger(op.revision) ||
        op.revision < revision ||
        !Object.hasOwn(states, op.state) ||
        !Array.isArray(op.results)
      ) {
        showConnection(
          'Ori responded without matching reset evidence. Do not submit another reset.'
        );
        entry.hidden = true;
        return;
      }
      ready = response.headers.get('X-Ori-Reset-Ready') === '1' && verified(payload, op);
      revision = op.revision;
      report(op, ready);
      showConnection('');
      const fresh = op.intent === 'start_fresh';
      if (fresh && ready) window.OriResetGeneration?.apply(id);
      if (ready) setText(entry, fresh ? 'Continue setup' : 'Back to Ori');
      entry.hidden = !ready;
      if (entering && ready) {
        paused = true;
        window.location.href = '/';
      }
    } catch {
      if (paused) return;
      showConnection(
        'Waiting to reconnect. This tab will check again. If Ori stays offline, check the Ori menu or terminal, then follow the manual restart steps.'
      );
      entry.hidden = true;
    } finally {
      checking = false;
      entering = false;
      delay = rendered !== previous ? minDelay : Math.min(delay * 2, maxDelay);
      if (!paused) timer = setTimeout(poll, delay);
    }
  }

  check.addEventListener('submit', event => {
    event.preventDefault();
    delay = minDelay;
    void poll();
  });
  entry.addEventListener('click', () => {
    entering = true;
    void poll();
  });
  window.addEventListener('pagehide', () => {
    paused = true;
    clearTimeout(timer);
  });
  window.addEventListener('pageshow', event => {
    if (!event.persisted) return;
    paused = false;
    entry.hidden = true;
    void poll();
  });
  void poll();
})();
