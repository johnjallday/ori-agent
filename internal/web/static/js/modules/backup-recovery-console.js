// One owner-scoped recovery ledger. GETs only on open/load; every metadata
// check requires a user control. No model-selected operation or filesystem tool.
(function () {
  'use strict';
  const ID = 'backup-recovery';
  const TABS = ['overview', 'history', 'recovery', 'settings'];
  const state = {
    status: null,
    settings: null,
    guide: null,
    history: [],
    cursor: 0,
    open: false,
    tab: 'overview',
    trigger: null,
    busy: false,
    error: '',
    notice: '',
    loadEpoch: 0,
    actionEpoch: 0,
    draftEpoch: 0,
    controller: null,
    retry: null,
    review: null,
    credential: null,
    sources: [],
    volume: '',
    repository: '',
    useSaved: false,
    vault: '',
    record: '',
    revokeConfirm: ''
  };
  let dialog = null;
  const messages = {
    configuration_changed: 'Settings changed. Reload the saved selections and review again.',
    review_expired: 'This approval expired. Review the selections again.',
    check_in_progress: 'An owned check is still draining. No additional check was started.',
    removal_pending:
      'Capability removal is pending. Metadata access remains fenced; retry capability removal.',
    review_required: 'Review local metadata access in Settings first.',
    not_found: 'This workspace or its installed backup capability is unavailable.',
    backup_unavailable:
      'The metadata reader or selected credential is unavailable. Check the prerequisites and unlock the selected Vault outside this console.',
    request_rejected: 'Open Ori on its local page to use these controls.'
  };
  function ws() {
    return String(window.currentWorkspaceId || document.body?.dataset?.workspaceId || '');
  }
  function base() {
    return '/api/workspaces/' + encodeURIComponent(ws()) + '/' + ID;
  }
  function text(value) {
    return String(value ?? '').replace(
      // eslint-disable-next-line no-control-regex -- show hidden filename controls visibly, never interpret them.
      /[\u0000-\u001f\u007f-\u009f\u200e\u200f\u202a-\u202e\u2066-\u2069]/g,
      c => '[U+' + c.codePointAt(0).toString(16).toUpperCase().padStart(4, '0') + ']'
    );
  }
  function el(tag, cls, content) {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (content !== undefined) node.textContent = text(content);
    return node;
  }
  function button(label, action, secondary = false) {
    const node = el('button', 'br-button' + (secondary ? ' br-button-secondary' : ''), label);
    node.type = 'button';
    node.addEventListener('click', action);
    return node;
  }
  function paragraph(parent, content, cls = 'br-note') {
    parent.appendChild(el('p', cls, content));
  }
  function time(value) {
    if (!value) return 'Not observed';
    const date = new Date(value);
    return Number.isFinite(date.getTime()) ? date.toLocaleString() : 'Unavailable timestamp';
  }
  function bytes(value) {
    if (!Number.isSafeInteger(value) || value < 0) return 'Unavailable';
    if (value === 0) return '0 bytes';
    const units = ['bytes', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
    const scale = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
    return (value / 1024 ** scale).toFixed(scale ? 1 : 0) + ' ' + units[scale];
  }
  function outcome(value) {
    return (
      {
        read_ok: 'Metadata observed',
        destination_absent: 'Destination disconnected',
        identity_mismatch: 'Identity mismatch — review required',
        credential_unavailable: 'Credential unavailable',
        provider_unavailable: 'Reader unavailable',
        limits_exceeded: 'Supported limit exceeded',
        cancelled: 'Check cancelled',
        clock_suspect: 'Clock requires review',
        state_unavailable: 'Stored state unavailable',
        read_unavailable: 'Metadata unavailable'
      }[value] || 'Unknown'
    );
  }
  function connection(status = state.status) {
    if (!status) return 'Unavailable';
    if (status.connection === 'not_configured') return 'Not configured';
    if (status.connection === 'review_required') return 'Local review required';
    return status.connection === 'configured' ? 'Metadata connection configured' : 'Unavailable';
  }
  function snapshotLabel(status = state.status) {
    if (status?.snapshot_count === 0) return 'No completed backup observed';
    if (!status?.last_completed_at) return 'No completion time observed';
    return 'Last observed completion: ' + time(status.last_completed_at);
  }
  async function request(path, options = {}) {
    const method = options.method || 'GET';
    const response = await window.fetch(base() + path, {
      method,
      credentials: 'same-origin',
      signal: options.signal,
      headers: {
        Accept: 'application/json',
        ...(method === 'GET'
          ? {}
          : {
              'Content-Type': 'application/json',
              'X-Ori-Backup-Action': 'user-control'
            })
      },
      ...(options.body === undefined ? {} : { body: JSON.stringify(options.body) })
    });
    const payload = await response.json();
    if (!response.ok || payload.success !== true) {
      const error = new Error(
        messages[payload.code] || 'The request could not be completed safely.'
      );
      error.code = payload.code || 'unavailable';
      throw error;
    }
    return payload;
  }
  async function loadStored() {
    const epoch = ++state.loadEpoch;
    try {
      const payload = await request('');
      if (epoch !== state.loadEpoch) return;
      state.status = payload.status;
      state.error = '';
      const [settings, guide] = await Promise.allSettled([request('/settings'), request('/guide')]);
      if (epoch !== state.loadEpoch) return;
      if (settings.status === 'fulfilled') {
        state.settings = settings.value.settings;
        if (!state.review && !state.sources.length && !state.credential) {
          state.useSaved = !!state.settings.configured;
          state.vault = state.settings.credential?.vault_id || '';
          state.record = state.settings.credential?.record_id || '';
          state.credential = state.settings.credential || null;
        }
      }
      if (guide.status === 'fulfilled') state.guide = guide.value.guide;
    } catch (error) {
      if (epoch !== state.loadEpoch) return;
      state.error =
        messages[error.code] ||
        'Stored backup state could not be loaded. Older evidence below has not been refreshed.';
    }
    if (epoch === state.loadEpoch) render();
  }
  function version() {
    return {
      generation: state.settings?.generation || state.status?.generation || '',
      revision: state.settings?.revision ?? state.status?.revision ?? 0
    };
  }
  async function mutate(path, body, method = 'POST', retrying = false) {
    if (state.busy && path !== '/revoke' && method !== 'DELETE') return;
    const epoch = ++state.actionEpoch;
    state.controller?.abort();
    const controller = new AbortController();
    state.controller = controller;
    state.busy = true;
    state.error = '';
    state.notice = '';
    const command = retrying ? state.retry : { path, body, method };
    if (!command) {
      state.busy = false;
      return;
    }
    state.retry = command;
    render();
    try {
      const payload = await request(command.path, {
        method: command.method,
        body: command.body,
        signal: controller.signal
      });
      if (epoch !== state.actionEpoch) return;
      state.retry = null;
      state.review = null;
      state.revokeConfirm = '';
      state.notice =
        payload.operation?.state === 'pending'
          ? 'The original request is still in progress. No duplicate check was started.'
          : payload.operation?.outcome === 'read_ok'
            ? 'Metadata observed. This is not proof of complete backup or recovery.'
            : payload.operation?.outcome === 'applied'
              ? 'Ori access was fenced and owned work drained. External backups and schedules are unchanged.'
              : outcome(payload.operation?.outcome);
    } catch (error) {
      if (epoch !== state.actionEpoch) return;
      state.error =
        error.name === 'AbortError'
          ? 'Request cancelled. Its outcome may already have committed; reload stored state or retry the identical request.'
          : messages[error.code] ||
            'The response was lost. Retry the identical request rather than starting another check.';
      if (error.code) {
        state.retry = null;
        state.review = null;
      }
    } finally {
      if (epoch === state.actionEpoch) {
        const actionError = state.error;
        state.busy = false;
        state.controller = null;
        await loadStored();
        if (actionError) {
          state.error = actionError;
          render();
        }
      }
    }
  }
  function invalidateDraft() {
    ++state.draftEpoch;
    state.review = null;
    state.retry = null;
  }
  async function pick(kind) {
    if (state.busy) return;
    invalidateDraft();
    const epoch = state.draftEpoch;
    state.busy = true;
    render();
    try {
      const payload = await request('/selection', { method: 'POST', body: { kind } });
      if (epoch !== state.draftEpoch || !payload.selection?.selected) return;
      state.useSaved = false;
      if (kind === 'source')
        state.sources = [...state.sources, payload.selection.token].slice(0, 16);
      else state[kind] = payload.selection.token;
    } catch (error) {
      state.error =
        messages[error.code] || 'Folder selection is unavailable. No access was granted.';
    } finally {
      state.busy = false;
      render();
    }
  }
  async function resolveCredential() {
    if (state.busy) return;
    invalidateDraft();
    const epoch = state.draftEpoch;
    state.busy = true;
    render();
    try {
      const payload = await request('/credential', {
        method: 'POST',
        body: { vault_id: state.vault, record_id: state.record }
      });
      if (epoch === state.draftEpoch) {
        state.credential = payload.credential;
        state.notice = 'Selected reference described without decrypting its password.';
      }
    } catch (error) {
      state.credential = null;
      state.error = messages[error.code] || 'That selected workspace reference is unavailable.';
    } finally {
      state.busy = false;
      render();
    }
  }
  async function reviewSelection() {
    if (state.busy || !state.credential) return;
    const epoch = ++state.draftEpoch;
    state.busy = true;
    state.error = '';
    render();
    try {
      const payload = await request('/review', {
        method: 'POST',
        body: {
          ...version(),
          source_selections: state.useSaved ? [] : state.sources,
          volume_selection: state.useSaved ? '' : state.volume,
          repository_selection: state.useSaved ? '' : state.repository,
          use_saved_selection: state.useSaved,
          credential: state.credential
        }
      });
      if (epoch === state.draftEpoch) state.review = payload.review;
    } catch (error) {
      state.error =
        messages[error.code] || 'Selection review is unavailable. Nothing was activated.';
    } finally {
      state.busy = false;
      render();
    }
  }
  async function loadHistory(append = false) {
    try {
      const payload = await request(
        '/history' + (append && state.cursor ? '?cursor=' + state.cursor : '')
      );
      state.history = (
        append ? [...state.history, ...payload.history.items] : payload.history.items
      ).slice(0, 100);
      state.cursor = payload.history.next_cursor || 0;
    } catch {
      state.error = 'Stored history could not be loaded. A failed read is not an empty history.';
    }
    render();
  }
  function summaryVisible() {
    const mount = document.getElementById('backupRecoveryMount');
    if (mount)
      mount.hidden =
        !window.WorkspaceCapabilities?.isInstalled(ID) ||
        (document.body?.dataset?.workspaceViewMode || 'details') !== 'details';
  }
  function renderCard() {
    const mount = document.getElementById('backupRecoveryMount');
    if (!mount) return;
    mount.textContent = '';
    const card = el('article', 'br-card');
    const copy = el('div');
    copy.appendChild(el('p', 'br-eyebrow', 'RECOVERY LEDGER'));
    copy.appendChild(el('h3', 'br-card-title', 'Backup & Recovery'));
    paragraph(copy, state.error ? 'Stored evidence unavailable' : connection());
    paragraph(copy, snapshotLabel(), 'br-small');
    card.appendChild(copy);
    card.appendChild(
      button('Open backup evidence', event => open({ trigger: event.currentTarget }), true)
    );
    mount.appendChild(card);
    summaryVisible();
  }
  function fact(parent, label, value, note) {
    const item = el('div', 'br-fact');
    item.appendChild(el('dt', 'br-eyebrow', label));
    item.appendChild(el('dd', 'br-value', value));
    if (note) paragraph(item, note, 'br-small');
    parent.appendChild(item);
  }
  function renderOverview(parent) {
    const status = state.status;
    if (status?.removal_pending)
      paragraph(
        parent,
        'Capability removal is pending. Metadata access remains fenced; retry capability removal.',
        'br-boundary'
      );
    parent.appendChild(
      el(
        'h3',
        'br-heading',
        status?.connection === 'configured'
          ? 'Evidence, not a guarantee.'
          : 'Start with a backup. Then observe it.'
      )
    );
    paragraph(
      parent,
      'Your external application owns backup execution and recovery. Ori records what it can actually observe, not a protection check mark.'
    );
    const ledger = el('dl', 'br-ledger');
    fact(
      ledger,
      'Connection',
      connection(),
      status?.runtime_available
        ? 'Bounded metadata reader available'
        : 'Reader unavailable — review the supported prerequisites'
    );
    fact(
      ledger,
      'Last successful read',
      time(status?.last_successful_read),
      state.error
        ? 'Older stored evidence; not refreshed'
        : 'This time changes only after a successful check'
    );
    fact(
      ledger,
      'Observed completion',
      status?.snapshot_count === 0 ? 'None observed' : time(status?.last_completed_at),
      'A snapshot is not the latest attempted-run outcome'
    );
    fact(
      ledger,
      'Checks',
      {
        active: 'Periodic checks active',
        paused: 'Ori checks paused',
        review_required: 'Monitoring needs review',
        unavailable: 'Monitoring unavailable'
      }[status?.monitoring] || 'Manual only',
      'No promise of checks while Ori or the Mac is off'
    );
    parent.appendChild(ledger);
    fact(
      ledger,
      'Destination',
      {
        present: 'Present at the last check',
        absent: 'Disconnected',
        identity_mismatch: 'Identity mismatch',
        unavailable: 'Unavailable'
      }[status?.destination] || 'Not checked',
      'A disconnected drive is not automatically a failed backup'
    );
    fact(
      ledger,
      'Intended scope',
      status?.intended_source_count
        ? status.intended_source_count + ' source folders'
        : 'Not configured',
      'Intent is separate from provider-declared coverage'
    );
    const details = el('div', 'br-evidence');
    if (status?.last_attempt)
      paragraph(
        details,
        'Last attempt: ' +
          outcome(status.last_attempt.outcome) +
          ' · ' +
          time(status.last_attempt.finished_at)
      );
    paragraph(
      details,
      status?.coverage
        ? 'Latest snapshot declares ' +
            status.coverage.declared_path_count +
            ' paths and ' +
            status.coverage.exclusion_count +
            ' exclusions. Historical declaration, not exhaustive coverage.'
        : 'Provider coverage: unavailable. Intended folders are not proof of observed coverage.'
    );
    paragraph(
      details,
      status?.capacity
        ? 'Destination space: ' +
            bytes(status.capacity.available_bytes) +
            ' available of ' +
            bytes(status.capacity.total_bytes) +
            ' · observed ' +
            time(status.capacity.observed_at) +
            '. This does not predict backup fit.'
        : 'Destination capacity: unavailable.'
    );
    paragraph(
      details,
      'Latest backup attempt success: unavailable. Recovery: not verified by Ori.'
    );
    parent.appendChild(details);
    const actions = el('div', 'br-actions');
    const check = button(state.busy ? 'Checking metadata…' : 'Check metadata now', () =>
      mutate('/refresh', {
        ...version(),
        sequence: state.status.sequence,
        requested_at: state.status.server_time,
        operation_id: window.crypto.randomUUID(),
        allow_metadata_read: true
      })
    );
    check.disabled =
      state.busy ||
      !status?.runtime_available ||
      status?.removal_pending ||
      status?.connection !== 'configured' ||
      !!state.error;
    actions.appendChild(check);
    actions.appendChild(button('Setup guide & connection', () => selectTab('settings'), true));
    parent.appendChild(actions);
    paragraph(parent, 'External-drive-only backup is not off-site protection.', 'br-boundary');
  }
  function renderGuide(parent) {
    const box = el('details', 'br-guide');
    box.appendChild(el('summary', '', 'External setup guide · no model required'));
    if (!state.guide)
      paragraph(
        box,
        'Install and configure the supported backup application outside Ori. Guidance is unavailable here; no connection or backup has been established.'
      );
    else {
      paragraph(box, state.guide.boundary);
      for (const prerequisite of state.guide.prerequisites || []) paragraph(box, prerequisite);
      for (const step of state.guide.steps || []) {
        box.appendChild(el('h4', '', step.title));
        for (const instruction of step.instructions || []) paragraph(box, instruction);
      }
      for (const link of state.guide.sources || []) {
        if (
          !/^https:\/\/github\.com\/restic\/restic\/blob\/v0\.19\.1\/doc\/(020_installation|030_preparing_a_new_repo|040_backup|050_restore)\.rst$/.test(
            link.url || ''
          )
        )
          continue;
        const anchor = el('a', 'br-source', link.label);
        anchor.href = link.url;
        anchor.target = '_blank';
        anchor.rel = 'noopener noreferrer';
        box.appendChild(anchor);
      }
    }
    parent.appendChild(box);
  }
  function input(parent, label, value, change, id) {
    const wrapper = el('label', 'br-field', label);
    const field = el('input', 'br-input');
    field.id = id;
    field.type = 'text';
    field.disabled = state.busy;
    field.value = value;
    field.autocomplete = 'off';
    field.spellcheck = false;
    field.addEventListener('input', () => {
      const cursor = field.selectionStart;
      change(field.value);
      render();
      const replacement = document.getElementById(id);
      replacement?.setSelectionRange?.(cursor, cursor);
    });
    wrapper.appendChild(field);
    parent.appendChild(wrapper);
    return field;
  }
  function renderSettings(parent) {
    parent.appendChild(el('h3', 'br-heading', 'A connection is a separate decision.'));
    renderGuide(parent);
    if (state.status?.removal_pending) {
      paragraph(
        parent,
        'Capability removal is pending. Metadata access remains fenced; retry capability removal.',
        'br-boundary'
      );
      return;
    }
    paragraph(
      parent,
      'Choose folders, review the exact scope, then approve one metadata read. No backup, restore, directory grant or periodic monitoring is enabled by selection.'
    );
    if (state.settings?.configured) {
      const saved = el('label', 'br-choice');
      const checkbox = el('input');
      checkbox.type = 'checkbox';
      checkbox.disabled = state.busy;
      checkbox.checked = state.useSaved;
      checkbox.addEventListener('change', () => {
        invalidateDraft();
        state.useSaved = checkbox.checked;
        render();
      });
      saved.appendChild(checkbox);
      saved.appendChild(
        el('span', '', 'Use the saved folder selection (known identity bindings are retained)')
      );
      parent.appendChild(saved);
      if (state.useSaved) {
        for (const path of state.settings.source_paths || [])
          paragraph(parent, 'Source · ' + path, 'br-path');
        paragraph(parent, 'Volume · ' + state.settings.volume_path, 'br-path');
        paragraph(parent, 'Repository · ' + state.settings.repository_path, 'br-path');
      }
    }
    if (!state.useSaved) {
      const choices = el('div', 'br-actions');
      for (const [kind, label] of [
        ['source', 'Choose source folder'],
        ['volume', 'Choose external volume root'],
        ['repository', 'Choose existing repository']
      ]) {
        const choose = button(label, () => pick(kind), true);
        choose.disabled =
          state.busy ||
          !state.settings?.picker_available ||
          (kind === 'source' && state.sources.length >= 16);
        choices.appendChild(choose);
      }
      parent.appendChild(choices);
      paragraph(
        parent,
        state.sources.length +
          ' source selections · volume ' +
          (state.volume ? 'selected' : 'not selected') +
          ' · repository ' +
          (state.repository ? 'selected' : 'not selected')
      );
      paragraph(
        parent,
        'For a changed selection, choose all three roles again. Exact folder paths appear in the review before you approve any read.'
      );
    }
    input(
      parent,
      'Selected Vault ID — not a password',
      state.vault,
      value => {
        invalidateDraft();
        state.vault = value;
        state.credential = null;
      },
      'brVaultID'
    );
    input(
      parent,
      'Selected workspace secret record ID',
      state.record,
      value => {
        invalidateDraft();
        state.record = value;
        state.credential = null;
      },
      'brRecordID'
    );
    paragraph(
      parent,
      'Use an explicitly selected existing workspace-scoped secret from Vault. Unlock its Vault there if necessary; never paste its value here.'
    );
    const resolve = button('Describe selected reference', resolveCredential, true);
    resolve.disabled = state.busy || !state.vault || !state.record;
    parent.appendChild(resolve);
    const review = button('Review metadata access', reviewSelection);
    review.disabled =
      state.busy ||
      !state.credential ||
      (!state.useSaved && (!state.sources.length || !state.volume || !state.repository));
    parent.appendChild(review);
    if (state.review) {
      const receipt = el('section', 'br-review');
      receipt.appendChild(el('h4', '', 'Review the exact scope'));
      for (const path of state.review.source_paths || [])
        paragraph(receipt, 'Source · ' + path, 'br-path');
      paragraph(receipt, 'External volume · ' + state.review.volume_path, 'br-path');
      paragraph(receipt, 'Repository · ' + state.review.repository_path, 'br-path');
      paragraph(
        receipt,
        'Review expires ' +
          time(state.review.expires_at) +
          '. Monitoring remains off. Replacing a connection first fences its old access.'
      );
      const label = el('label', 'br-choice');
      const allow = el('input');
      allow.type = 'checkbox';
      allow.id = 'brReadConsent';
      label.appendChild(allow);
      label.appendChild(
        el(
          'span',
          '',
          'Allow one bounded metadata read of this exact selection. Do not run a backup or restore.'
        )
      );
      const confirm = button('Approve one metadata read', () => {
        if (!allow.checked) return;
        mutate('/connect', {
          generation: state.review.generation,
          revision: state.review.revision,
          operation_id: window.crypto.randomUUID(),
          review_token: state.review.review_token,
          allow_metadata_read: true
        });
      });
      confirm.disabled = true;
      allow.addEventListener('change', () => {
        confirm.disabled = !allow.checked || state.busy;
      });
      receipt.appendChild(label);
      receipt.appendChild(confirm);
      receipt.appendChild(
        button(
          'Cancel review',
          () => {
            invalidateDraft();
            render();
          },
          true
        )
      );
      parent.appendChild(receipt);
    }
    if (state.settings?.configured) {
      const removal = el('section', 'br-removal');
      removal.appendChild(el('h4', '', 'Stop Ori access, not your backups'));
      paragraph(
        removal,
        'Revoke retains saved selections and labelled local history. Remove additionally clears selected paths and credential references. Neither touches source files, backups or external schedules.'
      );
      for (const [label, clear] of [
        ['Revoke metadata access', false],
        ['Remove Ori configuration', true]
      ])
        removal.appendChild(
          button(
            label,
            () => {
              state.revokeConfirm = clear ? 'delete' : 'revoke';
              render();
            },
            true
          )
        );
      if (state.revokeConfirm)
        removal.appendChild(
          button('Confirm ' + (state.revokeConfirm === 'delete' ? 'removal' : 'revocation'), () =>
            mutate(
              state.revokeConfirm === 'delete' ? '' : '/revoke',
              { ...version(), operation_id: window.crypto.randomUUID() },
              state.revokeConfirm === 'delete' ? 'DELETE' : 'POST'
            )
          )
        );
      parent.appendChild(removal);
    }
  }
  function renderHistory(parent) {
    parent.appendChild(el('h3', 'br-heading', 'What Ori observed. Not what it assumes.'));
    paragraph(
      parent,
      'Metadata read attempts are not backup-run outcomes. Historical generations are retained after reconfiguration.'
    );
    if (!state.history.length)
      paragraph(parent, 'No stored check attempts loaded. This does not mean no backups exist.');
    const list = el('ol', 'br-history');
    for (const attempt of state.history) {
      const item = el('li');
      item.appendChild(el('strong', '', outcome(attempt.outcome)));
      paragraph(
        item,
        time(attempt.finished_at) +
          (attempt.generation !== state.status?.generation ? ' · historical configuration' : ''),
        'br-small'
      );
      list.appendChild(item);
    }
    parent.appendChild(list);
    if (state.cursor)
      parent.appendChild(button('Load earlier checks', () => loadHistory(true), true));
  }
  function renderRecovery(parent) {
    parent.appendChild(el('h3', 'br-heading', 'Recovery is a practice, not a promise.'));
    paragraph(
      parent,
      'Not tested in Ori. Use the external application to restore a sample to a separate location. Never overwrite the working source as a test.'
    );
    paragraph(
      parent,
      'Ori does not restore or inspect recovered contents. A dated user report is not Ori verification, and one sample cannot prove all files are recoverable.'
    );
    renderGuide(parent);
  }
  function syncURL() {
    const url = new URL(window.location.href);
    if (state.open) {
      url.searchParams.set('panel', ID);
      url.searchParams.set('tab', state.tab);
    } else if (url.searchParams.get('panel') === ID) {
      url.searchParams.delete('panel');
      url.searchParams.delete('tab');
    }
    window.history.replaceState(null, '', url.pathname + url.search + url.hash);
  }
  function selectTab(tab) {
    state.tab = TABS.includes(tab) ? tab : 'overview';
    if (state.open) syncURL();
    render();
    dialog?.querySelector('[data-br-tab="' + state.tab + '"]')?.focus();
    if (state.tab === 'history') void loadHistory();
  }
  function render() {
    renderCard();
    if (!state.open || !dialog) return;
    const focused = document.activeElement?.id;
    dialog.textContent = '';
    const shell = el('div', 'br-shell');
    const head = el('header', 'br-head');
    const title = el('div');
    title.appendChild(el('p', 'br-eyebrow', 'BACKUP & RECOVERY / LOCAL EVIDENCE'));
    const heading = el('h2', 'br-title', 'Keep a way back.');
    heading.id = 'backupRecoveryTitle';
    title.appendChild(heading);
    head.appendChild(title);
    const closeButton = button('Close', close, true);
    closeButton.id = 'brClose';
    head.appendChild(closeButton);
    shell.appendChild(head);
    const nav = el('div', 'br-tabs');
    nav.setAttribute('role', 'tablist');
    nav.setAttribute('aria-label', 'Backup & Recovery');
    for (const tab of TABS) {
      const item = button(tab[0].toUpperCase() + tab.slice(1), () => selectTab(tab), true);
      item.id = 'brTab-' + tab;
      item.setAttribute('role', 'tab');
      item.setAttribute('data-br-tab', tab);
      item.setAttribute('aria-selected', String(tab === state.tab));
      item.setAttribute('aria-controls', 'backupRecoveryBody');
      item.tabIndex = tab === state.tab ? 0 : -1;
      item.addEventListener('keydown', event => {
        const index = TABS.indexOf(tab);
        const next =
          event.key === 'ArrowRight'
            ? (index + 1) % 4
            : event.key === 'ArrowLeft'
              ? (index + 3) % 4
              : event.key === 'Home'
                ? 0
                : event.key === 'End'
                  ? 3
                  : -1;
        if (next >= 0) {
          event.preventDefault();
          selectTab(TABS[next]);
        }
      });
      nav.appendChild(item);
    }
    shell.appendChild(nav);
    const body = el('div', 'br-body');
    body.id = 'backupRecoveryBody';
    body.setAttribute('role', 'tabpanel');
    body.setAttribute('aria-labelledby', 'brTab-' + state.tab);
    if (state.error) {
      const alert = el('p', 'br-alert', state.error);
      alert.setAttribute('role', 'alert');
      body.appendChild(alert);
    }
    if (state.notice) {
      const notice = el('p', 'br-notice', state.notice);
      notice.setAttribute('role', 'status');
      body.appendChild(notice);
    }
    if (state.busy) {
      const cancel = button('Cancel this request', () => state.controller?.abort(), true);
      cancel.disabled = !state.controller;
      body.appendChild(cancel);
    }
    if (state.retry && !state.busy)
      body.appendChild(
        button(
          'Retry identical request — no duplicate check',
          () => mutate('', null, 'POST', true),
          true
        )
      );
    if (state.tab === 'overview') renderOverview(body);
    else if (state.tab === 'settings') renderSettings(body);
    else if (state.tab === 'history') renderHistory(body);
    else renderRecovery(body);
    shell.appendChild(body);
    paragraph(
      shell,
      'READ-ONLY METADATA · EXTERNAL EXECUTION · NO OFF-SITE GUARANTEE',
      'br-footer'
    );
    dialog.appendChild(shell);
    if (focused && document.getElementById(focused)) document.getElementById(focused).focus();
  }
  function open(options = {}) {
    if (!ws() || !window.WorkspaceCapabilities?.isInstalled(ID)) return false;
    state.trigger = options.trigger || document.activeElement;
    state.open = true;
    state.tab = TABS.includes(options.tab) ? options.tab : 'overview';
    if (!dialog) {
      dialog = el('dialog', 'br-dialog');
      dialog.id = 'backupRecoveryConsole';
      dialog.setAttribute('aria-labelledby', 'backupRecoveryTitle');
      dialog.setAttribute('aria-modal', 'true');
      dialog.addEventListener('cancel', event => {
        event.preventDefault();
        close();
      });
      document.body.appendChild(dialog);
    }
    render();
    const coordinator = window.workspaceOverlayCoordinator;
    if (coordinator?.open) {
      dialog.setAttribute('open', '');
      coordinator.open({
        id: 'backup-recovery-console',
        kind: 'modal',
        container: dialog,
        trigger: state.trigger,
        onClose: info => {
          if (info?.reason !== 'suspended') close(true);
        }
      });
    } else if (!dialog.open) dialog.showModal();
    document.getElementById('brClose')?.focus();
    syncURL();
    void loadStored();
    if (state.tab === 'history') void loadHistory();
    return true;
  }
  function close(viaCoordinator = false) {
    if (!state.open) return;
    state.open = false;
    state.review = null;
    ++state.draftEpoch;
    if (viaCoordinator !== true)
      window.workspaceOverlayCoordinator?.close?.('backup-recovery-console');
    if (dialog?.open) dialog.close();
    dialog?.removeAttribute('open');
    syncURL();
    const target = state.trigger?.isConnected
      ? state.trigger
      : document.getElementById('backupRecoveryMount')?.querySelector('button');
    target?.focus();
  }
  async function init() {
    if (!ws()) return;
    await window.WorkspaceCapabilities?.load?.();
    summaryVisible();
    if (!window.WorkspaceCapabilities?.isInstalled(ID)) return;
    await loadStored();
    const url = new URL(window.location.href);
    if (url.searchParams.get('panel') === ID) open({ tab: url.searchParams.get('tab') });
  }
  const controller = {
    init,
    open,
    close,
    refresh: loadStored,
    activeTab: () => state.tab,
    isOpen: () => state.open,
    _test: {
      state,
      request,
      loadStored,
      mutate,
      reviewSelection,
      selectTab,
      snapshotLabel,
      bytes,
      text,
      render
    }
  };
  window.BackupRecoveryConsole = controller;
  window.WorkspaceCapabilities?.registerOpenHandler?.(ID, trigger => open({ trigger }));
  document.addEventListener('DOMContentLoaded', () => {
    window.WorkspaceCapabilities?.registerOpenHandler?.(ID, trigger => open({ trigger }));
    void init();
  });
  document.addEventListener('ori:capabilities-changed', () => {
    if (!window.WorkspaceCapabilities?.isInstalled(ID)) {
      close();
      const mount = document.getElementById('backupRecoveryMount');
      if (mount) mount.hidden = true;
    } else void loadStored();
  });
  document.addEventListener('ori:workspace-view-mode-changed', summaryVisible);
  window.WorkspaceBuiltinStationAdapters = window.WorkspaceBuiltinStationAdapters || [];
  window.WorkspaceBuiltinStationAdapters.push({
    key: ID,
    station() {
      if (!window.WorkspaceCapabilities?.isInstalled(ID)) return null;
      return {
        key: ID,
        label: 'Backup & Recovery',
        location: 'Stored backup evidence',
        icon: 'bi-life-preserver',
        defaultX: 0.71,
        state: () => ({
          applies: true,
          value: connection(),
          description: snapshotLabel(),
          tone: state.error ? 'degraded' : ''
        }),
        action: trigger => open({ trigger })
      };
    }
  });
})();
