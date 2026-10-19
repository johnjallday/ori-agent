// One owner-scoped recovery ledger. GETs only on open/load; every metadata
// check requires user control or separately approved periodic consent. No model filesystem tool.
(function () {
  'use strict';
  const ID = 'backup-recovery';
  const TABS = ['overview', 'history', 'recovery', 'settings'];
  // The workspace command router normalizes its URL while our stored GETs
  // are pending. Capture this console-owned intent synchronously, as Calendar
  // Ops does; reading the address bar after those awaits can lose the link.
  let initialDeepLink = (() => {
    if (typeof window === 'undefined' || !window.location?.href) return null;
    const params = new URL(window.location.href).searchParams;
    return params.get('panel') === ID
      ? { tab: params.get('tab'), section: params.get('section'), provider: params.get('provider') }
      : null;
  })();
  const TIME_MACHINE = 'time_machine';
  // What the restic path needs from the user before anything is created.
  const RESTIC_NEEDS = [
    'An external drive with free space',
    'restic 0.19.1, installed by you',
    'An encrypted repository you prepare on that drive',
    'A Vault secret that holds its password',
    'About 30 minutes'
  ];
  const TIME_MACHINE_SETTINGS =
    'x-apple.systempreferences:com.apple.Time-Machine-Settings.extension';
  const FULL_DISK_ACCESS_SETTINGS =
    'x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_AllFiles';
  const state = {
    status: null,
    settings: null,
    guide: null,
    recovery: null,
    history: [],
    cursor: 0,
    open: false,
    tab: 'overview',
    trigger: null,
    busy: false,
    storedLoading: false,
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
    provider: '',
    providerChosen: false,
    focusSection: '',
    vault: '',
    record: '',
    revokeConfirm: '',
    monitorDraft: null,
    monitorReview: null,
    monitorDirty: false,
    reportDraft: null
  };
  let dialog = null;
  const messages = {
    configuration_changed: 'Settings changed. Reload the saved selections and review again.',
    invalid_request: 'These settings are outside the supported limits. Review them again.',
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
    if (status?.health?.recency === 'clock_suspect')
      return 'Completion unavailable — future timestamp quarantined';
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
    state.storedLoading = true;
    dialog?.setAttribute('aria-busy', 'true');
    try {
      const payload = await request('');
      if (epoch !== state.loadEpoch) return;
      state.status = payload.status;
      state.error = '';
      const [settings, guide, recovery] = await Promise.allSettled([
        request('/settings'),
        request('/guide'),
        request('/recovery')
      ]);
      if (epoch !== state.loadEpoch) return;
      if (settings.status === 'fulfilled') {
        const incoming = settings.value.settings;
        const monitoringScopeChanged =
          state.settings?.generation !== incoming.generation ||
          state.settings?.revision !== incoming.revision;
        if (!state.monitorDirty || monitoringScopeChanged) {
          state.monitorDraft = { ...(incoming.monitoring?.policy || proposedPolicy()) };
          if (monitoringScopeChanged) state.monitorReview = null;
          state.monitorDirty = false;
        }
        state.settings = incoming;
        if (!state.review && !state.sources.length && !state.credential && !state.providerChosen) {
          state.provider = defaultProvider(state.settings, initialDeepLink?.provider);
          state.useSaved = !!state.settings.configured && savedProvider() === state.provider;
          state.vault = state.settings.credential?.vault_id || '';
          state.record = state.settings.credential?.record_id || '';
          state.credential = state.settings.credential || null;
        }
      }
      if (guide.status === 'fulfilled') state.guide = guide.value.guide;
      state.recovery = recovery.status === 'fulfilled' ? recovery.value.recovery : null;
      if (
        state.reportDraft &&
        (!state.recovery ||
          state.reportDraft.generation !== state.recovery.generation ||
          state.reportDraft.revision !== state.recovery.revision)
      )
        state.reportDraft = null;
    } catch (error) {
      if (epoch !== state.loadEpoch) return;
      state.error =
        messages[error.code] ||
        'Stored backup state could not be loaded. Older evidence below has not been refreshed.';
    }
    if (epoch === state.loadEpoch) {
      state.storedLoading = false;
      render();
    }
  }
  // The saved provider wins; then a chooser's preselect, when this server can
  // read it; then the first provider it can read. Nothing here connects.
  function defaultProvider(settings, preselect) {
    if (settings?.configured && settings.provider) return settings.provider;
    const readable = (settings?.providers || []).filter(option => option.available);
    if (preselect && readable.some(option => option.id === preselect)) return preselect;
    return readable[0]?.id || 'restic';
  }
  function savedProvider() {
    return state.settings?.provider || 'restic';
  }
  function activeProvider() {
    return state.provider || 'restic';
  }
  function chooseProvider(id) {
    if (state.busy || id === activeProvider()) return;
    invalidateDraft();
    state.provider = id;
    state.providerChosen = true;
    state.sources = [];
    state.volume = '';
    state.repository = '';
    state.useSaved = !!state.settings?.configured && savedProvider() === id;
    render();
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
      state.monitorReview = null;
      if (command.path === '/monitoring') state.monitorDirty = false;
      if (command.path === '/recovery-report') state.reportDraft = null;
      state.revokeConfirm = '';
      state.notice =
        payload.operation?.state === 'pending'
          ? 'The original request is still in progress. No duplicate check was started.'
          : payload.operation?.outcome === 'read_ok'
            ? 'Metadata observed. This is not proof of complete backup or recovery.'
            : payload.operation?.outcome === 'applied'
              ? command.path === '/recovery-report'
                ? 'User report saved. Ori did not perform or verify a restore.'
                : command.path === '/monitoring'
                  ? 'Periodic metadata settings saved. External backup schedules are unchanged.'
                  : 'Ori access was fenced and owned work drained. External backups and schedules are unchanged.'
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
    state.monitorReview = null;
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
    const timeMachine = activeProvider() === TIME_MACHINE;
    if (state.busy || (!timeMachine && !state.credential)) return;
    const epoch = ++state.draftEpoch;
    state.busy = true;
    state.error = '';
    render();
    try {
      // Time Machine has no credential, volume or repository: none is sent.
      const body = timeMachine
        ? {
            ...version(),
            provider: TIME_MACHINE,
            source_selections: state.useSaved ? [] : state.sources,
            use_saved_selection: state.useSaved
          }
        : {
            ...version(),
            provider: 'restic',
            source_selections: state.useSaved ? [] : state.sources,
            volume_selection: state.useSaved ? '' : state.volume,
            repository_selection: state.useSaved ? '' : state.repository,
            use_saved_selection: state.useSaved,
            credential: state.credential
          };
      const payload = await request('/review', { method: 'POST', body });
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
  function settingsLink(parent, label, href) {
    const link = el('a', 'br-link', label);
    link.href = href;
    link.rel = 'noopener';
    parent.appendChild(link);
  }
  // A read found no destination, or could not see completed backups. Ori opens
  // nothing on the user's behalf: these are links the user follows.
  function renderTimeMachineState(parent, status) {
    const tm = status?.time_machine;
    if (status?.provider !== TIME_MACHINE || !tm || !status.evidence_current_generation) return;
    if (tm.permission_required) {
      const box = el('section', 'br-boundary br-tm-state');
      box.appendChild(el('h4', '', 'Time Machine needs Full Disk Access'));
      paragraph(
        box,
        (tm.destinations
          ? 'Ori saw a Time Machine destination but not its completed backups. '
          : 'Ori could not read Time Machine settings. ') +
          'In System Settings → Privacy & Security → Full Disk Access, turn on the app that runs Ori, then check again. Ori never runs anything unsandboxed to get around it.'
      );
      settingsLink(box, 'Open Full Disk Access settings', FULL_DISK_ACCESS_SETTINGS);
      parent.appendChild(box);
      return;
    }
    if (tm.destinations === 0) {
      const box = el('section', 'br-boundary br-tm-state');
      box.appendChild(el('h4', '', 'Time Machine is not set up on this Mac'));
      paragraph(
        box,
        'Ori found no Time Machine destination. Set one up in System Settings, or use a restic repository instead. Ori created nothing on any drive.'
      );
      settingsLink(box, 'Open Time Machine settings', TIME_MACHINE_SETTINGS);
      box.appendChild(
        button(
          'Use a restic repository instead',
          () => {
            chooseProvider('restic');
            selectTab('settings');
          },
          true
        )
      );
      renderNeeds(box);
      parent.appendChild(box);
    }
  }
  function renderCoverage(parent, status) {
    const tm = status?.time_machine;
    const paths = state.settings?.source_paths || [];
    if (
      status?.provider !== TIME_MACHINE ||
      !tm?.source_states ||
      tm.source_states.length !== paths.length
    )
      return;
    const section = el('section', 'br-coverage');
    section.id = 'brCoverage';
    section.tabIndex = -1;
    section.appendChild(el('h4', '', 'Folders Ori checks'));
    const list = el('ul');
    paths.forEach((path, index) => {
      const label =
        {
          included: tm.permission_required
            ? 'Inclusion unknown without Full Disk Access'
            : 'Included in Time Machine',
          source_excluded: 'Excluded from Time Machine',
          unknown: 'Inclusion unknown'
        }[tm.source_states[index]] || 'Inclusion unknown';
      const item = el('li', 'br-path', path);
      item.appendChild(el('span', 'br-coverage-state', ' · ' + label));
      list.appendChild(item);
    });
    section.appendChild(list);
    paragraph(
      section,
      'Included means Time Machine does not exclude the folder. It is not proof of a recoverable copy.'
    );
    parent.appendChild(section);
  }
  function renderOverview(parent) {
    const status = state.status;
    const timeMachine = status?.provider === TIME_MACHINE;
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
      timeMachine
        ? 'Time Machine owns backups and recovery on this Mac. Ori records what it can actually observe, not a protection check mark.'
        : 'Your external application owns backup execution and recovery. Ori records what it can actually observe, not a protection check mark.'
    );
    renderTimeMachineState(parent, status);
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
      status?.health?.observation === 'clock_suspect'
        ? 'Unavailable — clock requires review'
        : time(status?.last_successful_read),
      state.error
        ? 'Older stored evidence; not refreshed'
        : 'This time changes only after a successful check'
    );
    fact(
      ledger,
      'Observed completion',
      status?.health?.recency === 'clock_suspect'
        ? 'Unavailable — future timestamp quarantined'
        : status?.time_machine?.permission_required
          ? 'Unknown — Full Disk Access is needed'
          : status?.snapshot_count === 0
            ? timeMachine
              ? 'No completed backup observed'
              : 'None observed'
            : time(status?.last_completed_at),
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
        absent: timeMachine ? 'Not connected — Ori does not mount it' : 'Disconnected',
        identity_mismatch: 'Identity mismatch',
        unavailable: 'Unavailable',
        not_configured: 'No Time Machine destination'
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
    if (status?.health) {
      fact(
        ledger,
        'Observation freshness',
        {
          current: 'Within the freshness window',
          stale: 'Stored evidence stale',
          historical: 'Historical configuration',
          clock_suspect: 'Clock requires review',
          never_observed: 'Not observed'
        }[status.health.observation] || 'Unavailable',
        'Evaluated from stored time. Not a new check.'
      );
      fact(
        ledger,
        'Backup recency',
        {
          recent: 'Within expected completion age',
          overdue: 'Observed completion overdue',
          no_completed_snapshot: 'No completed snapshot observed',
          completion_unavailable: 'Completion time unavailable',
          clock_suspect: 'Future timestamp quarantined'
        }[status.health.recency] || 'Unavailable',
        'This is not the latest backup-attempt result or verified recovery.'
      );
    }
    const details = el('div', 'br-evidence');
    if (status?.health?.capacity === 'low')
      paragraph(
        details,
        'Point-in-time low-space warning. Check storage in the external application; Ori does not predict backup fit.',
        'br-boundary'
      );
    if (status?.health?.disconnection === 'within_grace')
      paragraph(
        details,
        'Destination was absent at the last check, within the reviewed disconnect grace. This is not a failed-backup claim.'
      );
    if (status?.last_attempt)
      paragraph(
        details,
        'Last attempt: ' +
          outcome(status.last_attempt.outcome) +
          ' · ' +
          time(status.last_attempt.finished_at)
      );
    if (!timeMachine)
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
    renderCoverage(parent, status);
    const actions = el('div', 'br-actions');
    const checkLabel = timeMachine ? 'Check Time Machine now' : 'Check metadata now';
    const check = button(state.busy ? 'Checking metadata…' : checkLabel, () =>
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
  function renderGuide(
    parent,
    guide = state.guide,
    title = 'External setup guide · no model required'
  ) {
    const box = el('details', 'br-guide');
    box.appendChild(el('summary', '', title));
    if (!guide)
      paragraph(
        box,
        'Install and configure the supported backup application outside Ori. Guidance is unavailable here; no connection or backup has been established.'
      );
    else {
      paragraph(box, guide.boundary);
      for (const prerequisite of guide.prerequisites || []) paragraph(box, prerequisite);
      for (const step of guide.steps || []) {
        box.appendChild(el('h4', '', step.title));
        for (const instruction of step.instructions || []) paragraph(box, instruction);
      }
      for (const link of guide.sources || []) {
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
      if (replacement?.type === 'text') replacement?.setSelectionRange?.(cursor, cursor);
    });
    wrapper.appendChild(field);
    parent.appendChild(wrapper);
    return field;
  }
  function proposedPolicy() {
    return {
      cadence_seconds: 3600,
      expected_age_seconds: 172800,
      freshness_seconds: 10800,
      disconnected_grace_seconds: 86400,
      low_space_percent: 10
    };
  }
  function validPolicy(policy) {
    return (
      !!policy &&
      [900, 3600, 21600, 86400].includes(policy.cadence_seconds) &&
      Number.isSafeInteger(policy.expected_age_seconds) &&
      policy.expected_age_seconds >= 3600 &&
      policy.expected_age_seconds <= 2592000 &&
      Number.isSafeInteger(policy.freshness_seconds) &&
      policy.freshness_seconds >= policy.cadence_seconds &&
      policy.freshness_seconds <= 604800 &&
      Number.isSafeInteger(policy.disconnected_grace_seconds) &&
      policy.disconnected_grace_seconds >= 0 &&
      policy.disconnected_grace_seconds <= 2592000 &&
      Number.isSafeInteger(policy.low_space_percent) &&
      policy.low_space_percent >= 1 &&
      policy.low_space_percent <= 50
    );
  }
  async function reviewMonitoring() {
    if (state.busy || !validPolicy(state.monitorDraft)) return;
    const epoch = ++state.draftEpoch;
    state.busy = true;
    state.error = '';
    state.review = null;
    state.monitorReview = null;
    render();
    try {
      const payload = await request('/monitoring/review', {
        method: 'POST',
        body: {
          ...version(),
          monitoring_revision: state.settings.monitoring.monitoring_revision,
          policy: state.monitorDraft
        }
      });
      if (epoch === state.draftEpoch) state.monitorReview = payload.review;
    } catch (error) {
      if (epoch !== state.draftEpoch) return;
      state.error =
        messages[error.code] || 'Periodic policy could not be reviewed. Nothing was enabled.';
    } finally {
      state.busy = false;
      render();
    }
  }
  function renderMonitoring(parent) {
    const box = el('section', 'br-monitoring');
    box.appendChild(el('h4', '', 'Ori metadata checks'));
    const monitoring = state.settings?.monitoring;
    const mode =
      {
        active: 'Periodic checks active',
        paused: 'Periodic checks paused',
        manual_only: 'Manual only',
        review_required: 'Periodic access needs review',
        unavailable: 'Periodic checking unavailable'
      }[state.status?.monitoring] || 'Manual only';
    paragraph(box, mode, 'br-value');
    paragraph(
      box,
      'These are read-only metadata checks, not a backup schedule. No checks are promised while Ori or the Mac is off. Pausing here never pauses your external backup application.'
    );
    if (!state.monitorDraft) state.monitorDraft = { ...(monitoring?.policy || proposedPolicy()) };
    const draft = state.monitorDraft;
    const grid = el('div', 'br-policy-grid');
    const label = el('label', 'br-field', 'Check cadence');
    const cadence = el('select', 'br-input');
    cadence.id = 'brCadence';
    cadence.disabled = state.busy;
    for (const [seconds, title] of [
      [900, 'Every 15 minutes'],
      [3600, 'Every hour'],
      [21600, 'Every 6 hours'],
      [86400, 'Every 24 hours']
    ]) {
      const option = el('option', '', title);
      option.value = String(seconds);
      cadence.appendChild(option);
    }
    cadence.value = String(draft.cadence_seconds);
    cadence.addEventListener('change', () => {
      state.monitorDirty = true;
      state.monitorReview = null;
      draft.cadence_seconds = Number(cadence.value);
      render();
    });
    label.appendChild(cadence);
    grid.appendChild(label);
    for (const [key, title, scale, min, max] of [
      ['expected_age_seconds', 'Expected completed snapshot age (hours)', 3600, 1, 720],
      ['freshness_seconds', 'Observation freshness (hours)', 3600, 0.25, 168],
      ['disconnected_grace_seconds', 'Disconnected grace (hours)', 3600, 0, 720],
      ['low_space_percent', 'Low-space warning (percent)', 1, 1, 50]
    ]) {
      const field = input(
        grid,
        title,
        draft[key] == null ? '' : draft[key] / scale,
        value => {
          state.monitorDirty = true;
          state.monitorReview = null;
          draft[key] = value.trim() === '' ? null : Number(value) * scale;
        },
        'brPolicy-' + key
      );
      field.type = 'number';
      field.min = String(min);
      field.max = String(max);
      field.step = scale === 3600 ? '0.25' : '1';
    }
    box.appendChild(grid);
    paragraph(
      box,
      'Defaults are proposals, not permission. Freshness cannot be shorter than cadence. Unknown capacity remains unknown; a warning does not predict whether a backup will fit.',
      'br-small'
    );
    const review = button('Review periodic metadata policy', reviewMonitoring, true);
    review.disabled =
      state.busy ||
      !monitoring ||
      state.status?.connection !== 'configured' ||
      !state.status?.runtime_available ||
      !validPolicy(draft);
    box.appendChild(review);
    if (monitoring && state.settings?.configured) {
      for (const [title, intent] of [
        ['Pause periodic checks', 'paused'],
        ['Keep manual-only checks', 'manual_only']
      ]) {
        const control = button(
          title,
          () =>
            mutate('/monitoring', {
              ...version(),
              monitoring_revision: monitoring.monitoring_revision,
              operation_id: window.crypto.randomUUID(),
              intent,
              review_token: '',
              allow_periodic_metadata: false
            }),
          true
        );
        control.disabled = state.busy || monitoring.intent === intent;
        box.appendChild(control);
      }
    }
    if (state.monitorReview) {
      const reviewed = state.monitorReview;
      const receipt = el('section', 'br-review');
      receipt.appendChild(el('h4', '', 'Review periodic metadata access'));
      paragraph(
        receipt,
        'Cadence: ' +
          reviewed.policy.cadence_seconds / 60 +
          ' minutes · expected completion age: ' +
          reviewed.policy.expected_age_seconds / 3600 +
          ' hours · freshness: ' +
          reviewed.policy.freshness_seconds / 3600 +
          ' hours · disconnected grace: ' +
          reviewed.policy.disconnected_grace_seconds / 3600 +
          ' hours · low-space warning: ' +
          reviewed.policy.low_space_percent +
          '%.'
      );
      for (const path of state.settings?.source_paths || [])
        paragraph(receipt, 'Saved source · ' + path, 'br-path');
      paragraph(
        receipt,
        'Saved repository · ' + (state.settings?.repository_path || 'Unavailable'),
        'br-path'
      );
      paragraph(
        receipt,
        'This policy applies to the saved connection above, not uncommitted folder choices.'
      );
      paragraph(
        receipt,
        'Approval expires ' +
          time(reviewed.expires_at) +
          '. External backup execution, schedules and recovery remain outside Ori.'
      );
      const choice = el('label', 'br-choice');
      const consent = el('input');
      consent.type = 'checkbox';
      consent.id = 'brPeriodicConsent';
      choice.appendChild(consent);
      choice.appendChild(
        el(
          'span',
          '',
          'Allow periodic bounded metadata reads of this existing connection under this exact policy.'
        )
      );
      const enable = button('Enable periodic metadata checks', () => {
        if (consent.checked)
          void mutate('/monitoring', {
            generation: reviewed.generation,
            revision: reviewed.revision,
            monitoring_revision: reviewed.monitoring_revision,
            operation_id: window.crypto.randomUUID(),
            intent: 'active',
            review_token: reviewed.review_token,
            allow_periodic_metadata: true
          });
      });
      enable.disabled = true;
      consent.addEventListener('change', () => {
        enable.disabled = !consent.checked || state.busy;
      });
      receipt.appendChild(choice);
      receipt.appendChild(enable);
      receipt.appendChild(
        button(
          'Cancel periodic review',
          () => {
            state.monitorReview = null;
            render();
          },
          true
        )
      );
      box.appendChild(receipt);
    }
    parent.appendChild(box);
  }
  function renderNeeds(parent) {
    const list = el('ul', 'br-needs');
    list.setAttribute('aria-label', "What you'll need for restic");
    for (const need of RESTIC_NEEDS) list.appendChild(el('li', '', need));
    parent.appendChild(el('h4', '', "What you'll need"));
    parent.appendChild(list);
  }
  function renderProviderChoice(parent) {
    const options = state.settings?.providers || [];
    if (!options.length) return;
    const group = el('div', 'br-provider');
    group.setAttribute('role', 'radiogroup');
    group.setAttribute('aria-label', 'Backup provider');
    for (const option of options) {
      const chosen = option.id === activeProvider();
      const choice = button(option.label || option.id, () => chooseProvider(option.id), !chosen);
      choice.id = 'brProvider-' + option.id;
      choice.setAttribute('role', 'radio');
      choice.setAttribute('aria-checked', String(chosen));
      choice.disabled = state.busy || (!option.available && !chosen);
      group.appendChild(choice);
      if (!option.available)
        paragraph(
          group,
          option.label +
            (option.reason === 'platform_unsupported'
              ? ' is unavailable: it is only on macOS.'
              : ' is unavailable: its metadata reader is not available on this server.'),
          'br-note'
        );
    }
    parent.appendChild(group);
  }
  function renderSettings(parent) {
    const timeMachine = activeProvider() === TIME_MACHINE;
    parent.appendChild(el('h3', 'br-heading', 'A connection is a separate decision.'));
    renderProviderChoice(parent);
    if (timeMachine)
      paragraph(
        parent,
        'Time Machine is built into this Mac. Ori checks its status only after you approve one read here, and never changes Time Machine settings or starts a backup. Ori needs Full Disk Access to see completed backups; without it Ori can still see whether a destination exists.'
      );
    else {
      renderGuide(parent);
      renderNeeds(parent);
    }
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
      timeMachine
        ? 'Choose the folders Ori should check, review them, then approve one read. Selecting a folder grants no access and starts nothing.'
        : 'Choose folders, review the exact scope, then approve one metadata read. No backup, restore, directory grant or periodic monitoring is enabled by selection.'
    );
    if (state.settings?.configured && savedProvider() === activeProvider()) {
      const saved = el('label', 'br-choice');
      const checkbox = el('input');
      checkbox.type = 'checkbox';
      checkbox.id = 'brUseSavedSelection';
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
        if (!timeMachine) {
          paragraph(parent, 'Volume · ' + state.settings.volume_path, 'br-path');
          paragraph(parent, 'Repository · ' + state.settings.repository_path, 'br-path');
        }
      }
    }
    if (!state.useSaved && timeMachine) {
      const choices = el('div', 'br-actions');
      const choose = button('Choose a folder to check', () => pick('source'), true);
      choose.disabled =
        state.busy || !state.settings?.picker_available || state.sources.length >= 16;
      choices.appendChild(choose);
      parent.appendChild(choices);
      paragraph(
        parent,
        state.sources.length +
          ' folder selections. Exact folder paths appear in the review before you approve the read.'
      );
    }
    if (!state.useSaved && !timeMachine) {
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
    // Time Machine has no Vault secret or repository: those controls are
    // hidden for it, not merely disabled.
    if (!timeMachine) {
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
    }
    const review = button(
      timeMachine ? 'Review Time Machine check' : 'Review metadata access',
      reviewSelection
    );
    review.disabled = timeMachine
      ? state.busy || (!state.useSaved && !state.sources.length)
      : state.busy ||
        !state.credential ||
        (!state.useSaved && (!state.sources.length || !state.volume || !state.repository));
    parent.appendChild(review);
    if (state.review) {
      const reviewedTimeMachine = state.review.provider === TIME_MACHINE;
      const receipt = el('section', 'br-review');
      receipt.appendChild(el('h4', '', 'Review the exact scope'));
      for (const path of state.review.source_paths || [])
        paragraph(receipt, (reviewedTimeMachine ? 'Folder · ' : 'Source · ') + path, 'br-path');
      if (!reviewedTimeMachine) {
        paragraph(receipt, 'External volume · ' + state.review.volume_path, 'br-path');
        paragraph(receipt, 'Repository · ' + state.review.repository_path, 'br-path');
      }
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
          reviewedTimeMachine
            ? "Allow one read of Time Machine's destinations, status and completed backup dates for these folders. Do not start, stop or change a backup."
            : 'Allow one bounded metadata read of this exact selection. Do not run a backup or restore.'
        )
      );
      const confirm = button(
        reviewedTimeMachine ? 'Check Time Machine once' : 'Approve one metadata read',
        () => {
          if (!allow.checked) return;
          mutate('/connect', {
            generation: state.review.generation,
            revision: state.review.revision,
            operation_id: window.crypto.randomUUID(),
            review_token: state.review.review_token,
            allow_metadata_read: true
          });
        }
      );
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
    renderMonitoring(parent);
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
  function reportResult(value) {
    return (
      { user_reported_passed: 'User reports passed', user_reported_failed: 'User reports failed' }[
        value
      ] || 'Not tested'
    );
  }
  function validReportDraft(draft, view) {
    if (
      !draft ||
      !view?.can_report ||
      !['user_reported_passed', 'user_reported_failed'].includes(draft.result) ||
      !/^\d{4}-\d{2}-\d{2}$/.test(draft.testedDate)
    )
      return false;
    const date = new Date(draft.testedDate + 'T00:00:00Z');
    if (!Number.isFinite(date.getTime()) || date.toISOString().slice(0, 10) !== draft.testedDate)
      return false;
    if (!view.note_choices?.some(note => note.code === draft.noteCode)) return false;
    return (
      draft.result !== 'user_reported_passed' || ['', 'sample_compared'].includes(draft.noteCode)
    );
  }
  function reportSelect(parent, title, id, options, value, change) {
    const label = el('label', 'br-field', title);
    label.setAttribute('for', id);
    const select = el('select', 'br-input');
    select.id = id;
    select.disabled = state.busy;
    for (const [key, title] of options) {
      const option = el('option', '', title);
      option.value = key;
      select.appendChild(option);
    }
    select.value = value;
    select.addEventListener('change', () => {
      change(select.value);
      render();
    });
    label.appendChild(select);
    parent.appendChild(label);
    return select;
  }
  function renderRecoveryReports(parent, view) {
    const box = el('section', 'br-reports');
    box.appendChild(el('h4', '', 'Your dated recovery reports'));
    const current = view?.reports?.find(report => !report.historical && !report.superseded);
    paragraph(
      box,
      current
        ? reportResult(current.result) + ' · tested ' + String(current.tested_at).slice(0, 10)
        : 'Not tested — no current user-reported result.',
      'br-value'
    );
    paragraph(
      box,
      'These are your assertions, not Ori or provider verification. Editing preserves the earlier version. Reconfiguration makes previous reports historical.'
    );
    const notes = new Map((view?.note_choices || []).map(note => [note.code, note.label]));
    const list = el('ol', 'br-history');
    for (const report of view?.reports || []) {
      const item = el('li');
      item.appendChild(el('strong', '', reportResult(report.result)));
      paragraph(
        item,
        'Tested ' +
          String(report.tested_at).slice(0, 10) +
          ' · recorded ' +
          time(report.reported_at) +
          (report.historical ? ' · historical configuration' : '') +
          (report.superseded ? ' · earlier version' : ''),
        'br-small'
      );
      if (report.snapshot_id)
        paragraph(item, 'Snapshot reference · ' + report.snapshot_id, 'br-path');
      if (report.note_code && notes.has(report.note_code))
        paragraph(item, notes.get(report.note_code));
      if (view.can_report && !report.historical && !report.superseded) {
        const edit = button(
          'Edit report dated ' + String(report.tested_at).slice(0, 10),
          () => {
            state.reportDraft = {
              generation: view.generation,
              revision: view.revision,
              editID: report.id,
              snapshotID: report.snapshot_id || '',
              testedDate: String(report.tested_at).slice(0, 10),
              result: report.result,
              noteCode: report.note_code || ''
            };
            render();
          },
          true
        );
        edit.disabled = state.busy;
        item.appendChild(edit);
      }
      list.appendChild(item);
    }
    box.appendChild(list);
    if (!view?.can_report) {
      paragraph(
        box,
        'Save a local connection selection before associating a new report with this setup. Guidance remains available; reporting never grants metadata access.'
      );
      parent.appendChild(box);
      return;
    }
    if (!state.reportDraft)
      state.reportDraft = {
        generation: view.generation,
        revision: view.revision,
        editID: '',
        snapshotID: '',
        testedDate: '',
        result: '',
        noteCode: ''
      };
    const draft = state.reportDraft;
    box.appendChild(
      el(
        'h4',
        '',
        draft.editID
          ? 'Edit your report · earlier version retained'
          : 'Record your sample-test result'
      )
    );
    const fields = el('div', 'br-policy-grid');
    const date = input(
      fields,
      'Test date (UTC)',
      draft.testedDate,
      value => {
        draft.testedDate = value;
      },
      'brReportDate'
    );
    date.type = 'date';
    date.max = String(view.server_time).slice(0, 10);
    reportSelect(
      fields,
      'Your reported result',
      'brReportResult',
      [
        ['', 'Choose a result'],
        ['user_reported_passed', 'User reports passed'],
        ['user_reported_failed', 'User reports failed']
      ],
      draft.result,
      value => {
        draft.result = value;
      }
    );
    const choices = [['', 'Snapshot not associated / unknown']];
    for (const snapshot of view.snapshots || [])
      if (snapshot.generation === view.generation && snapshot.evidence !== 'clock_suspect')
        choices.push([snapshot.id, snapshot.id + ' · ' + snapshot.evidence]);
    reportSelect(
      fields,
      'Snapshot association',
      'brReportSnapshot',
      choices,
      draft.snapshotID,
      value => {
        draft.snapshotID = value;
      }
    );
    reportSelect(
      fields,
      'Optional note — predefined choices only',
      'brReportNote',
      (view.note_choices || []).map(note => [note.code, note.label]),
      draft.noteCode,
      value => {
        draft.noteCode = value;
      }
    );
    box.appendChild(fields);
    paragraph(
      box,
      'Do not enter passwords, recovery keys, file contents or executable commands. Notes are predefined to keep secrets out of this ledger. A passed sample does not prove every file is recoverable.'
    );
    const choice = el('label', 'br-choice');
    const confirm = el('input');
    confirm.type = 'checkbox';
    confirm.id = 'brReportConsent';
    confirm.disabled = state.busy;
    choice.appendChild(confirm);
    choice.appendChild(
      el(
        'span',
        '',
        'I am recording my own dated sample-test result. Ori has not performed or verified this recovery.'
      )
    );
    const save = button(
      draft.editID ? 'Save edited user report' : 'Record user-reported result',
      () => {
        if (!confirm.checked || !validReportDraft(draft, view)) return;
        void mutate('/recovery-report', {
          generation: draft.generation,
          revision: draft.revision,
          operation_id: window.crypto.randomUUID(),
          edit_id: draft.editID,
          snapshot_id: draft.snapshotID,
          tested_at: draft.testedDate + 'T00:00:00Z',
          result: draft.result,
          note_code: draft.noteCode
        });
      }
    );
    save.disabled = true;
    confirm.addEventListener('change', () => {
      save.disabled = !confirm.checked || state.busy || !validReportDraft(draft, view);
    });
    box.appendChild(choice);
    box.appendChild(save);
    if (draft.editID)
      box.appendChild(
        button(
          'Cancel report edit',
          () => {
            state.reportDraft = null;
            render();
          },
          true
        )
      );
    parent.appendChild(box);
  }
  function renderRecovery(parent) {
    parent.appendChild(el('h3', 'br-heading', 'Recovery is a practice, not a promise.'));
    paragraph(
      parent,
      'Use the external application to restore a sample to a separate location. Never overwrite the working source as a test. Ori never verifies the result.'
    );
    paragraph(
      parent,
      'Ori does not restore or inspect recovered contents. A dated user report is not Ori verification, and one sample cannot prove all files are recoverable.'
    );
    const view = state.recovery;
    renderRecoveryReports(parent, view);
    if (view) {
      paragraph(
        parent,
        'Stored observation: ' +
          ({
            current: 'within the freshness window',
            stale: 'stale',
            historical: 'historical',
            clock_suspect: 'clock requires review',
            never_observed: 'not observed'
          }[view.observation] || 'unavailable') +
          '. Opening this view does not check a drive.'
      );
      parent.appendChild(el('h4', '', 'Current intended sources · not proof of snapshot coverage'));
      for (const path of view.source_paths || []) paragraph(parent, path, 'br-path');
      if (!view.source_paths?.length) paragraph(parent, 'No current source intent is available.');
      parent.appendChild(
        el('h4', '', 'Stored snapshot references · confirm availability outside Ori')
      );
      if (!view.snapshots?.length)
        paragraph(
          parent,
          'No stored snapshot references are available. This is not proof that no backups exist.'
        );
      for (const snapshot of view.snapshots || []) {
        const item = el('section', 'br-evidence');
        paragraph(item, snapshot.id, 'br-path');
        paragraph(
          item,
          ({
            stored: 'Stored metadata',
            stale: 'Stale metadata',
            historical: 'Historical configuration',
            clock_suspect: 'Clock-suspect metadata'
          }[snapshot.evidence] || 'Unavailable') +
            ' · completion ' +
            time(snapshot.completed_at)
        );
        paragraph(
          item,
          'Declared paths: ' +
            snapshot.declared_path_count +
            ' · exclusions: ' +
            snapshot.exclusion_count +
            '. Historical declaration, not exhaustive coverage.',
          'br-small'
        );
        parent.appendChild(item);
      }
      renderGuide(parent, view.guide, 'External recovery guide · no model required');
    } else {
      paragraph(
        parent,
        'Stored recovery evidence is unavailable. Follow the external application documentation; no snapshot availability is established here.'
      );
      renderGuide(parent);
    }
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
    dialog.setAttribute('aria-busy', String(state.busy || state.storedLoading));
    const focused = document.activeElement?.id;
    const focusWasInside = dialog.contains(document.activeElement);
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
    if (focusWasInside) {
      const retained = focused && document.getElementById(focused);
      // A late stored read can replace an unkeyed button/summary while it has
      // focus. Keep ownership in this modal instead of dropping focus to body.
      if (retained && dialog.contains(retained)) retained.focus();
      else dialog.querySelector('[data-br-tab="' + state.tab + '"]')?.focus();
    }
    // A coverage link opens the Overview at its coverage section once it exists.
    const section =
      state.focusSection && state.tab === 'overview' && document.getElementById('brCoverage');
    if (section && dialog.contains(section)) {
      state.focusSection = '';
      section.scrollIntoView?.({ block: 'start' });
      section.focus();
    }
  }
  function open(options = {}) {
    if (!ws() || !window.WorkspaceCapabilities?.isInstalled(ID)) return false;
    state.trigger = options.trigger || document.activeElement;
    state.open = true;
    state.tab = TABS.includes(options.tab) ? options.tab : 'overview';
    state.focusSection = options.section === 'coverage' ? 'coverage' : '';
    const wantsSection = !!state.focusSection;
    if (!dialog) {
      dialog = el('dialog', 'br-dialog');
      dialog.id = 'backupRecoveryConsole';
      dialog.setAttribute('aria-labelledby', 'backupRecoveryTitle');
      dialog.setAttribute('aria-modal', 'true');
      dialog.addEventListener('cancel', event => {
        event.preventDefault();
        close();
      });
      // The shared coordinator uses a non-top-layer dialog, whose native
      // cancel event does not fire on Escape. Only this active modal owns it.
      dialog.addEventListener('keydown', event => {
        if (event.key !== 'Escape' || event.defaultPrevented) return;
        const active = window.workspaceOverlayCoordinator?.activeModal?.();
        if (active && active.id !== 'backup-recovery-console') return;
        event.preventDefault();
        event.stopPropagation?.();
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
    // A coverage link keeps focus on its section rather than on Close.
    const section = wantsSection && document.getElementById('brCoverage');
    if (section) section.focus();
    else document.getElementById('brClose')?.focus();
    syncURL();
    void loadStored();
    if (state.tab === 'history') void loadHistory();
    return true;
  }
  function close(viaCoordinator = false) {
    if (!state.open) return;
    state.open = false;
    state.review = null;
    state.monitorReview = null;
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
    const captured = initialDeepLink;
    initialDeepLink = null;
    if (captured || url.searchParams.get('panel') === ID)
      open({
        tab: captured ? captured.tab : url.searchParams.get('tab'),
        section: captured ? captured.section : url.searchParams.get('section')
      });
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
      reviewMonitoring,
      validPolicy,
      validReportDraft,
      selectTab,
      chooseProvider,
      defaultProvider,
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
