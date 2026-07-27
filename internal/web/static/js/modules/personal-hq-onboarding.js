// personal-hq-onboarding.js — Personal HQ setup and management on the
// workspace launcher. The workspace Map owns the missing/repair presentation;
// this module owns the authoritative status request and the Build / Import HQ
// actions and modals. It is a no-op away from the workspace launcher.
//
// Pure decision logic is exported (loaded as type="module") so
// personal-hq-onboarding.test.js can exercise it without a DOM.

// upgradeView turns an upgrade plan (from GET /api/personal-hq/upgrade/preview)
// into the view-model the DOM code renders, so all the branching lives here and
// is unit-testable without a DOM. It deliberately hides itself when there is
// nothing to do (a current HQ), so it never nags:
//   - blocked plan  -> show a read-only "unavailable" card with the reasons.
//   - up to date    -> show:false (nothing to prompt).
//   - changes due   -> an actionable card listing additions + preserved state,
//     framed as "Resume/Retry" after a prior partial/failed attempt.
export function upgradeView(plan) {
  if (!plan) return { show: false };
  const blockers = Array.isArray(plan.blockers) ? plan.blockers : [];
  if (blockers.length) {
    return {
      show: true,
      canApply: false,
      blocked: true,
      heading: 'Personal HQ upgrade unavailable',
      reasons: blockers
    };
  }
  const missing = Array.isArray(plan.missing_roles) ? plan.missing_roles : [];
  if (plan.up_to_date && missing.length === 0) {
    return { show: false, upToDate: true };
  }
  const retry = !!plan.retryable_prior_failure;
  return {
    show: true,
    canApply: true,
    blocked: false,
    retry,
    heading: retry ? 'Resume your Personal HQ upgrade' : 'Upgrade your Personal HQ',
    applyLabel: retry ? 'Retry upgrade' : 'Apply upgrade',
    additions: Array.isArray(plan.additions) ? plan.additions : [],
    preserved: Array.isArray(plan.preserved_customizations) ? plan.preserved_customizations : []
  };
}

// emailStatusView turns the Personal HQ email status (GET /email/status) plus
// whether the server has Google OAuth credentials (oauthConfigured) into the
// loadout-chip view-model. Email is a first-class HQ inventory item — equipped,
// not required — so the chip owns its own setup routing. States:
//   - connected: equipped; show the address + Disconnect.
//   - setup:     the server has no OAuth client yet — route to Settings.
//   - repair:    a binding exists but the account is gone/expired — Reconnect.
//   - disconnected: OAuth is configured but no account connected — Connect.
// oauthConfigured defaults to true (backward compatible) unless explicitly false.
export function emailStatusView(status, oauthConfigured) {
  const configured = oauthConfigured !== false;
  if (status && status.connected) {
    return {
      state: 'connected',
      chip: 'Email',
      chipState: 'equipped',
      heading: 'Email connected',
      detail: status.email_address || '',
      action: 'disconnect',
      actionLabel: 'Disconnect'
    };
  }
  if (!configured) {
    return {
      state: 'setup',
      chip: 'Email',
      chipState: 'empty',
      heading: 'Set up email',
      detail: 'Add your Google OAuth credentials in Settings to enable Personal HQ email.',
      action: 'settings',
      actionLabel: 'Set up in Settings'
    };
  }
  if (status && status.account_id) {
    return {
      state: 'repair',
      chip: 'Email',
      chipState: 'repair',
      heading: 'Reconnect your email',
      detail: 'Your connected email needs to be reconnected before the assistant can read it.',
      action: 'connect',
      actionLabel: 'Reconnect'
    };
  }
  return {
    state: 'disconnected',
    chip: 'Email',
    chipState: 'empty',
    heading: 'Connect your email',
    detail:
      'Let your Inbox specialist surface threads that need attention and help draft replies. Read-only — nothing is ever sent without your explicit confirmation.',
    action: 'connect',
    actionLabel: 'Connect email'
  };
}

// chipStateLabel renders the short inventory-chip status word.
export function chipStateLabel(chipState) {
  switch (chipState) {
    case 'equipped':
      return 'Connected';
    case 'repair':
      return 'Needs repair';
    default:
      return 'Not set up';
  }
}

// replyProposalView turns a reply proposal (from the mail broker) into the
// confirm-gated review view-model. Only draft/failed proposals are actionable
// (sendable); everything else is terminal. The exact reviewed payload_hash is
// carried so a send binds to precisely what the user saw.
export function replyProposalView(p) {
  if (!p) return { show: false };
  const status = p.status || 'draft';
  const payload = p.payload || {};
  const view = {
    show: true,
    id: p.id,
    status,
    to: Array.isArray(payload.to) ? payload.to.join(', ') : '',
    subject: payload.subject || '',
    body: payload.body || '',
    payloadHash: p.payload_hash || '',
    canSend: status === 'draft' || status === 'failed',
    actionLabel: status === 'failed' ? 'Retry send' : 'Send'
  };
  if (status === 'failed') view.statusNote = 'The last send attempt failed — you can retry.';
  else if (status === 'sent') view.statusNote = 'Sent';
  else if (status === 'expired') view.statusNote = 'This draft expired.';
  else view.statusNote = '';
  return view;
}

// followUpCategoryLabel maps a follow-up category to a friendly label.
export function followUpCategoryLabel(category) {
  switch (category) {
    case 'i_owe':
      return 'You owe';
    case 'waiting_on':
      return 'Waiting on';
    case 'needs_decision':
      return 'Needs decision';
    case 'recurring_check_in':
      return 'Check-in';
    default:
      return 'Follow-up';
  }
}

// journalPromptView turns an end-of-day journal proposal into the editor
// view-model: the prefilled editable draft plus a degraded flag when grounding
// was incomplete. Pure and unit-testable.
export function journalPromptView(proposal) {
  if (!proposal) return { draft: '', degraded: false, localDate: '' };
  return {
    localDate: proposal.local_date || '',
    draft: proposal.draft || '',
    degraded: !!proposal.degraded,
    gaps: Array.isArray(proposal.gaps) ? proposal.gaps : []
  };
}

export function hqWorkspaceRootView(state) {
  const source = String(state?.source || 'unconfirmed')
    .trim()
    .toLowerCase();
  const configuredRoot = String(state?.workspace_root || '').trim();
  const effectiveRoot = String(state?.effective_workspace_root || '').trim();
  const suggestedRoot = String(state?.default_workspace_root || '').trim();
  const confirmed =
    state?.confirmed === true ||
    source === 'settings' ||
    source === 'environment' ||
    source === 'default';
  return {
    path: configuredRoot || effectiveRoot || suggestedRoot,
    confirmed,
    status: confirmed
      ? 'Confirmed. Ori will scan only this directory.'
      : 'Confirm this directory before building your HQ.'
  };
}

// followUpView turns a follow-up record into a Home projection card view-model.
// A candidate (inferred, unconfirmed) offers Confirm/Dismiss; an active item
// offers Done/Snooze.
export function followUpView(f) {
  if (!f) return null;
  const status = f.status || 'active';
  return {
    id: f.id,
    title: f.title || '',
    detail: f.detail || '',
    counterparty: f.counterparty || '',
    category: followUpCategoryLabel(f.category),
    isCandidate: status === 'candidate'
  };
}

(function () {
  if (typeof document === 'undefined') return;

  const hub = document.getElementById('workspaceHub');
  if (!hub) return;

  let statusPromise = null;

  async function fetchStatus() {
    if (!statusPromise) {
      statusPromise = (async () => {
        const res = await fetch('/api/personal-hq/status', {
          headers: { Accept: 'application/json' }
        });
        if (!res.ok) throw new Error(`personal hq status ${res.status}`);
        const body = await res.json();
        return body.status;
      })().catch(err => {
        statusPromise = null;
        throw err;
      });
    }
    return statusPromise;
  }

  async function postJSON(url, body) {
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: body ? JSON.stringify(body) : undefined
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      const message = data && (data.error || data.message);
      throw new Error(message || `${url} -> ${res.status}`);
    }
    return data;
  }

  function toast(message, title, variant) {
    if (window.Toast && typeof window.Toast[variant || 'success'] === 'function') {
      window.Toast[variant || 'success'](message, { title });
    } else if (typeof window.notifyToast === 'function') {
      window.notifyToast(message, variant || 'success');
    }
  }

  function skipHQObjective() {
    return Promise.all([
      postJSON('/api/personal-hq/onboarding-state', { state: 'skipped' }),
      postJSON('/api/progression/skip', { quest_id: 't2-build-hq' }).catch(() => {
        /* Quest may already be resolved; skipping onboarding state is what matters. */
      })
    ]);
  }

  async function refreshHQStatus() {
    statusPromise = null;
    const status = await fetchStatus();
    if (window.OriWorkspaceMap && typeof window.OriWorkspaceMap.setHQStatus === 'function') {
      window.OriWorkspaceMap.setHQStatus(status);
    }
    return status;
  }

  function openImportHQModal() {
    const modalElement = document.getElementById('addFolderModal');
    if (!modalElement) {
      toast('Workspace import is unavailable right now.', 'Import unavailable', 'danger');
      return;
    }
    modalElement.dataset.pendingImportMode = 'true';
    modalElement.dataset.pendingEntryPoint = 'personal_hq_import';
    modalElement.dataset.pendingPostCreateAction = 'designate_personal_hq';
    const modal = bootstrapModal('addFolderModal');
    if (modal) modal.show();
  }

  function wireMapActions() {
    window.addEventListener('ori:personal-hq-action', async event => {
      const action = event?.detail?.action;
      if (action === 'build') {
        await openBuildModal();
        return;
      }
      if (action === 'import') {
        openImportHQModal();
        return;
      }
      if (action === 'skip') {
        try {
          await skipHQObjective();
          await refreshHQStatus();
          toast(
            'Personal HQ is still available from the workspace Map whenever you need it.',
            'Not now'
          );
        } catch (_) {
          toast(
            'Could not update Personal HQ setup right now. Try again.',
            'Update failed',
            'danger'
          );
        }
        return;
      }
      if (action === 'clear') {
        try {
          await postJSON('/api/personal-hq/clear');
          await refreshHQStatus();
          toast('The broken Personal HQ link was cleared.', 'Personal HQ');
        } catch (_) {
          toast('Could not clear the Personal HQ link. Try again.', 'Clear failed', 'danger');
        }
      }
    });
  }

  // ---- Build My HQ modal ----

  function bootstrapModal(id) {
    const el = document.getElementById(id);
    if (!el || !window.bootstrap || !window.bootstrap.Modal) return null;
    return window.bootstrap.Modal.getOrCreateInstance(el);
  }

  function browserTimezone() {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    } catch (_) {
      return 'UTC';
    }
  }

  // Prefer the user's already-stored profile timezone (PRD FR25: derive
  // defaults from the current user profile) and fall back to the browser's
  // detected zone only when the profile has none set yet.
  async function defaultTimezone() {
    try {
      const res = await fetch('/api/user/profile', { headers: { Accept: 'application/json' } });
      if (res.ok) {
        const body = await res.json();
        const tz = body && body.profile && body.profile.timezone;
        if (tz) return tz;
      }
    } catch (_) {
      /* fall through to browser detection */
    }
    return browserTimezone();
  }

  function showBuildWorkspaceRootError(message) {
    const errorBox = document.getElementById('hqBuildWorkspaceRootError');
    if (!errorBox) return;
    errorBox.textContent = message || '';
    errorBox.hidden = !message;
  }

  function renderBuildWorkspaceRoot(state) {
    const view = hqWorkspaceRootView(state);
    const input = document.getElementById('hqBuildWorkspaceRoot');
    const status = document.getElementById('hqBuildWorkspaceRootStatus');
    if (input) input.value = view.path;
    if (status) {
      status.textContent = view.status;
      status.classList.toggle('is-confirmed', view.confirmed);
      status.classList.toggle('is-unconfirmed', !view.confirmed);
    }
  }

  async function loadBuildWorkspaceRoot() {
    const response = await fetch('/api/settings/workspace-root', {
      headers: { Accept: 'application/json' }
    });
    if (!response.ok) throw new Error('Could not load the workspace directory.');
    const state = await response.json();
    renderBuildWorkspaceRoot(state);
    return state;
  }

  async function browseBuildWorkspaceRoot() {
    const button = document.getElementById('hqBuildWorkspaceRootBrowseBtn');
    if (button) button.disabled = true;
    showBuildWorkspaceRootError('');
    try {
      const response = await fetch('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: 'Choose Workspace Directory' })
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok || !result.success)
        throw new Error(result.error || 'Folder picker unavailable');
      if (result.selected && result.path) {
        const input = document.getElementById('hqBuildWorkspaceRoot');
        const status = document.getElementById('hqBuildWorkspaceRootStatus');
        if (input) input.value = result.path;
        if (status) {
          status.textContent = 'Selected. Build My HQ to confirm this directory.';
          status.classList.remove('is-confirmed');
          status.classList.add('is-unconfirmed');
        }
      }
    } catch (error) {
      showBuildWorkspaceRootError(error.message || 'Could not open the folder picker.');
    } finally {
      if (button) button.disabled = false;
    }
  }

  async function saveBuildWorkspaceRoot() {
    const input = document.getElementById('hqBuildWorkspaceRoot');
    const workspaceRoot = (input?.value || '').trim();
    if (!workspaceRoot) {
      showBuildWorkspaceRootError('Choose a workspace directory before building your HQ.');
      if (input) input.focus();
      return false;
    }

    showBuildWorkspaceRootError('');
    try {
      const state = await postJSON('/api/settings/workspace-root', {
        workspace_root: workspaceRoot
      });
      renderBuildWorkspaceRoot(state);
      return true;
    } catch (error) {
      showBuildWorkspaceRootError(error.message || 'Could not save the workspace directory.');
      return false;
    }
  }

  async function openBuildModal() {
    const tzInput = document.getElementById('hqBuildTimezone');
    const tasks = [];
    if (tzInput && !tzInput.value)
      tasks.push(
        defaultTimezone().then(timezone => {
          tzInput.value = timezone;
        })
      );
    tasks.push(loadBuildWorkspaceRoot().catch(error => showBuildWorkspaceRootError(error.message)));
    await Promise.all(tasks);
    const errorBox = document.getElementById('hqBuildError');
    if (errorBox) errorBox.hidden = true;
    const modal = bootstrapModal('hqBuildModal');
    if (modal) modal.show();
  }

  function collectBuildRequest() {
    const name = (document.getElementById('hqBuildName')?.value || '').trim() || 'My HQ';
    const timezone = (document.getElementById('hqBuildTimezone')?.value || '').trim();
    const scheduleDays = Array.from(
      document.querySelectorAll('#hqBuildAdvanced .hq-build-days input:checked')
    ).map(i => i.value);
    const scheduleTime = document.getElementById('hqBuildTime')?.value || '';
    const scope = document.getElementById('hqBuildScope')?.value || 'all';
    const includeFuture = !!document.getElementById('hqBuildIncludeFuture')?.checked;
    const notify = !!document.getElementById('hqBuildNotify')?.checked;
    return {
      name,
      timezone,
      schedule_days: scheduleDays,
      schedule_time: scheduleTime,
      scope,
      include_future_workspaces: includeFuture,
      notify_on_ready: notify
    };
  }

  function wireBuildModal() {
    const browseButton = document.getElementById('hqBuildWorkspaceRootBrowseBtn');
    if (browseButton) browseButton.addEventListener('click', () => browseBuildWorkspaceRoot());
    const workspaceRootInput = document.getElementById('hqBuildWorkspaceRoot');
    if (workspaceRootInput) {
      workspaceRootInput.addEventListener('input', () => {
        const status = document.getElementById('hqBuildWorkspaceRootStatus');
        if (!status) return;
        status.textContent = 'Changed. Build My HQ to confirm this directory.';
        status.classList.remove('is-confirmed');
        status.classList.add('is-unconfirmed');
      });
    }

    const toggle = document.getElementById('hqBuildAdvancedToggle');
    const advanced = document.getElementById('hqBuildAdvanced');
    if (toggle && advanced) {
      toggle.addEventListener('click', () => {
        const expanded = toggle.getAttribute('aria-expanded') === 'true';
        toggle.setAttribute('aria-expanded', String(!expanded));
        advanced.hidden = expanded;
      });
    }

    const submitBtn = document.getElementById('hqBuildSubmitBtn');
    if (!submitBtn) return;
    submitBtn.addEventListener('click', async () => {
      const errorBox = document.getElementById('hqBuildError');
      submitBtn.disabled = true;
      if (errorBox) errorBox.hidden = true;
      try {
        const workspaceRootSaved = await saveBuildWorkspaceRoot();
        if (!workspaceRootSaved) return;
        const result = await postJSON('/api/personal-hq/setup', collectBuildRequest());
        const modal = bootstrapModal('hqBuildModal');
        if (modal) modal.hide();
        toast('Your Personal HQ is ready.', 'Personal HQ built');
        window.setTimeout(() => {
          window.location.href = '/';
        }, 700);
        void result;
      } catch (err) {
        if (errorBox) {
          errorBox.textContent =
            err && err.message ? err.message : 'Could not build your Personal HQ. Try again.';
          errorBox.hidden = false;
        }
      } finally {
        submitBtn.disabled = false;
      }
    });
  }

  async function fetchUpgradePreview() {
    const res = await fetch('/api/personal-hq/upgrade/preview', {
      headers: { Accept: 'application/json' }
    });
    if (!res.ok) return null;
    const data = await res.json();
    return data && data.plan ? data.plan : null;
  }

  function renderUpgrade(mount, view) {
    mount.innerHTML = '';
    if (!view || !view.show) {
      mount.hidden = true;
      return;
    }
    mount.hidden = false;

    const card = document.createElement('div');
    card.className = 'hq-upgrade-card';
    const heading = document.createElement('h4');
    heading.className = 'hq-upgrade-heading';
    heading.textContent = view.heading;
    card.appendChild(heading);

    if (view.blocked) {
      const ul = document.createElement('ul');
      (view.reasons || []).forEach(r => {
        const li = document.createElement('li');
        li.textContent = r;
        ul.appendChild(li);
      });
      card.appendChild(ul);
      mount.appendChild(card);
      return;
    }

    if (view.additions.length) {
      const subtitle = document.createElement('p');
      subtitle.className = 'hq-upgrade-subtitle';
      subtitle.textContent = 'This will add:';
      const ul = document.createElement('ul');
      view.additions.forEach(a => {
        const li = document.createElement('li');
        li.textContent = a;
        ul.appendChild(li);
      });
      card.append(subtitle, ul);
    }
    if (view.preserved.length) {
      const preserved = document.createElement('p');
      preserved.className = 'hq-upgrade-preserved';
      preserved.textContent = 'Your existing setup is preserved: ' + view.preserved.join('; ');
      card.appendChild(preserved);
    }

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'modern-btn modern-btn-primary modern-btn-sm';
    btn.textContent = view.applyLabel;
    btn.addEventListener('click', async () => {
      btn.disabled = true;
      try {
        await postJSON('/api/personal-hq/upgrade/apply');
        toast('Your Personal HQ is up to date.', 'Upgrade applied', 'success');
        await wireUpgrade();
      } catch (_) {
        toast(
          'The upgrade did not fully complete — you can retry.',
          'Upgrade incomplete',
          'danger'
        );
        btn.disabled = false;
      }
    });
    card.appendChild(btn);
    mount.appendChild(card);
  }

  // wireUpgrade shows an explicit, confirmable upgrade card for a VALID
  // designated HQ that is not yet on the current provisioning version. Additive:
  // a no-op when the page has no #hqUpgradeMount, or when there is no valid HQ,
  // or when the HQ is already up to date (upgradeView returns show:false).
  async function wireUpgrade() {
    const mount = document.getElementById('hqUpgradeMount');
    if (!mount) return;
    let status;
    try {
      status = await fetchStatus();
    } catch (_) {
      return;
    }
    if (!status || !status.valid) {
      mount.hidden = true;
      return;
    }
    let plan;
    try {
      plan = await fetchUpgradePreview();
    } catch (_) {
      return;
    }
    renderUpgrade(mount, upgradeView(plan));
  }

  async function fetchEmailStatus() {
    const res = await fetch('/api/personal-hq/email/status', {
      headers: { Accept: 'application/json' }
    });
    if (!res.ok) return null;
    const data = await res.json();
    return data && data.status ? data.status : null;
  }

  // connectEmail opens the existing Vault email OAuth popup (least-privilege
  // read scope) and, on success, links the returned account to the HQ. It
  // depends on the Vault email OAuth being configured; failures surface as a
  // toast rather than a broken flow.
  function connectEmail() {
    const popup = window.open(
      '/api/vault/email-oauth/start?provider=gmail',
      'ori-hq-email',
      'width=520,height=680'
    );
    if (!popup) {
      toast('Allow pop-ups to connect your email.', 'Popup blocked', 'danger');
      return;
    }
    const onMessage = async event => {
      const data = event && event.data;
      if (!data || data.type !== 'ori:vault-email-oauth') return;
      window.removeEventListener('message', onMessage);
      if (!data.success || !data.account || !data.account.id) {
        toast(
          data && data.error ? data.error : 'Could not connect your email.',
          'Connect failed',
          'danger'
        );
        return;
      }
      try {
        await postJSON('/api/personal-hq/email/link', { account_id: data.account.id });
        toast('Email connected to your Personal HQ.', 'Connected', 'success');
        await wireEmail();
      } catch (_) {
        toast('Connected the account but could not link it to your HQ.', 'Link failed', 'danger');
      }
    };
    window.addEventListener('message', onMessage);
  }

  async function fetchOAuthConfigured() {
    try {
      const res = await fetch('/api/settings/email-oauth', {
        headers: { Accept: 'application/json' }
      });
      if (!res.ok) return true; // assume configured; the connect flow will surface a real error
      const data = await res.json();
      return !!data.configured;
    } catch (_) {
      return true;
    }
  }

  // renderEmail renders the Email inventory chip — a first-class HQ loadout item
  // with its own state and setup routing. The chip's action routes correctly:
  // 'settings' (server has no OAuth client) opens Settings, 'connect' opens the
  // OAuth popup, 'disconnect' unlinks.
  function renderEmail(mount, view) {
    mount.innerHTML = '';
    mount.hidden = false;

    const card = document.createElement('div');
    card.className = 'hq-loadout-chip hq-email-' + view.state;

    const head = document.createElement('div');
    head.className = 'hq-loadout-chip-head';
    const name = document.createElement('span');
    name.className = 'hq-loadout-chip-name';
    name.textContent = view.chip || 'Email';
    const state = document.createElement('span');
    state.className = 'hq-loadout-chip-state hq-loadout-chip-state-' + (view.chipState || 'empty');
    state.textContent = chipStateLabel(view.chipState);
    head.append(name, state);
    card.appendChild(head);

    if (view.detail) {
      const detail = document.createElement('p');
      detail.className = 'hq-loadout-chip-detail';
      detail.textContent = view.detail;
      card.appendChild(detail);
    }

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className =
      'modern-btn modern-btn-sm ' +
      (view.action === 'disconnect' ? 'modern-btn-secondary' : 'modern-btn-primary');
    btn.textContent = view.actionLabel;
    btn.addEventListener('click', async () => {
      if (view.action === 'settings') {
        window.location.href = '/settings#google-account';
        return;
      }
      if (view.action === 'disconnect') {
        btn.disabled = true;
        try {
          await postJSON('/api/personal-hq/email/unlink');
          toast('Email disconnected.', 'Disconnected', 'success');
          await wireEmail();
        } catch (_) {
          toast('Could not disconnect the email account.', 'Error', 'danger');
          btn.disabled = false;
        }
        return;
      }
      connectEmail();
    });
    card.appendChild(btn);
    mount.appendChild(card);
  }

  // wireEmail renders the Email loadout chip for a valid designated HQ. Additive:
  // a no-op when the page has no #hqEmailMount or no valid HQ.
  async function wireEmail() {
    const mount = document.getElementById('hqEmailMount');
    if (!mount) return;
    let status;
    try {
      status = await fetchStatus();
    } catch (_) {
      return;
    }
    if (!status || !status.valid) {
      mount.hidden = true;
      return;
    }
    const [emailStatus, oauthConfigured] = await Promise.all([
      fetchEmailStatus().catch(() => null),
      fetchOAuthConfigured()
    ]);
    renderEmail(mount, emailStatusView(emailStatus, oauthConfigured));
  }

  async function fetchProposals() {
    const res = await fetch('/api/personal-hq/mail/proposals', {
      headers: { Accept: 'application/json' }
    });
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data.proposals) ? data.proposals : [];
  }

  // renderReplyCard builds one confirm-gated review card using textContent for
  // all user/email-derived fields (never innerHTML), so untrusted recipients,
  // subject, and body can never inject markup.
  function renderReplyCard(view) {
    const card = document.createElement('div');
    card.className = 'hq-reply-card';

    const heading = document.createElement('h4');
    heading.className = 'hq-reply-heading';
    heading.textContent = 'Review reply before sending';
    card.appendChild(heading);

    const meta = document.createElement('dl');
    meta.className = 'hq-reply-meta';
    [
      ['To', view.to],
      ['Subject', view.subject]
    ].forEach(([label, value]) => {
      const dt = document.createElement('dt');
      dt.textContent = label;
      const dd = document.createElement('dd');
      dd.textContent = value;
      meta.append(dt, dd);
    });
    card.appendChild(meta);

    const body = document.createElement('pre');
    body.className = 'hq-reply-body';
    body.textContent = view.body;
    card.appendChild(body);

    if (view.statusNote) {
      const note = document.createElement('p');
      note.className = 'hq-reply-note';
      note.textContent = view.statusNote;
      card.appendChild(note);
    }

    const actions = document.createElement('div');
    actions.className = 'hq-reply-actions';

    const sendBtn = document.createElement('button');
    sendBtn.type = 'button';
    sendBtn.className = 'modern-btn modern-btn-primary modern-btn-sm';
    sendBtn.textContent = view.actionLabel;
    sendBtn.addEventListener('click', async () => {
      sendBtn.disabled = true;
      try {
        await postJSON('/api/personal-hq/mail/confirm', {
          id: view.id,
          expected_hash: view.payloadHash
        });
        toast('Your reply was sent.', 'Sent', 'success');
        await wireMailReview();
      } catch (e) {
        toast(e && e.message ? e.message : 'The reply could not be sent.', 'Send failed', 'danger');
        sendBtn.disabled = false;
      }
    });

    const cancelBtn = document.createElement('button');
    cancelBtn.type = 'button';
    cancelBtn.className = 'modern-btn modern-btn-secondary modern-btn-sm';
    cancelBtn.textContent = 'Discard';
    cancelBtn.addEventListener('click', async () => {
      try {
        await postJSON('/api/personal-hq/mail/cancel', { id: view.id });
        await wireMailReview();
      } catch (_) {
        toast('Could not discard the draft.', 'Error', 'danger');
      }
    });

    actions.append(sendBtn, cancelBtn);
    card.appendChild(actions);
    return card;
  }

  // wireMailReview shows confirm-gated review cards for actionable reply drafts.
  // Additive: a no-op without #hqMailReviewMount or a valid HQ.
  async function wireMailReview() {
    const mount = document.getElementById('hqMailReviewMount');
    if (!mount) return;
    let status;
    try {
      status = await fetchStatus();
    } catch (_) {
      return;
    }
    if (!status || !status.valid) {
      mount.hidden = true;
      return;
    }
    let proposals;
    try {
      proposals = await fetchProposals();
    } catch (_) {
      return;
    }
    const actionable = proposals.filter(p => p.status === 'draft' || p.status === 'failed');
    mount.innerHTML = '';
    if (!actionable.length) {
      mount.hidden = true;
      return;
    }
    mount.hidden = false;
    actionable.forEach(p => mount.appendChild(renderReplyCard(replyProposalView(p))));
  }

  async function fetchHomeFollowUps() {
    const res = await fetch('/api/personal-hq/followups/home', {
      headers: { Accept: 'application/json' }
    });
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data.followups) ? data.followups : [];
  }

  function renderFollowUpCard(view) {
    const card = document.createElement('div');
    card.className = 'hq-followup-card';

    const cat = document.createElement('span');
    cat.className = 'hq-followup-category';
    cat.textContent = view.counterparty ? `${view.category}: ${view.counterparty}` : view.category;
    card.appendChild(cat);

    const title = document.createElement('p');
    title.className = 'hq-followup-title';
    title.textContent = view.title;
    card.appendChild(title);

    if (view.detail) {
      const detail = document.createElement('p');
      detail.className = 'hq-followup-detail';
      detail.textContent = view.detail;
      card.appendChild(detail);
    }
    // Read-only on HQ (Mail spin-off FR20): the brief/Home surfaces follow-ups
    // but does not mutate them — management lives in the Email Ops workspace.
    return card;
  }

  // wireFollowUps renders the bounded Home follow-up projection. Additive:
  // no-op without #hqFollowUpMount or a valid HQ.
  async function wireFollowUps() {
    const mount = document.getElementById('hqFollowUpMount');
    if (!mount) return;
    let status;
    try {
      status = await fetchStatus();
    } catch (_) {
      return;
    }
    if (!status || !status.valid) {
      mount.hidden = true;
      return;
    }
    let items;
    try {
      items = await fetchHomeFollowUps();
    } catch (_) {
      return;
    }
    mount.innerHTML = '';
    if (!items.length) {
      mount.hidden = true;
      return;
    }
    mount.hidden = false;
    const heading = document.createElement('h4');
    heading.className = 'hq-followup-heading';
    heading.textContent = 'Follow-ups';
    mount.appendChild(heading);
    items.forEach(f => {
      const view = followUpView(f);
      if (view) mount.appendChild(renderFollowUpCard(view));
    });
    // Read-only surfacing: send the user to the Email Ops workspace to act on
    // these (Mail spin-off FR20). Best-effort — omit the link if no Email Ops
    // workspace exists.
    try {
      const res = await fetch('/api/personal-hq/email-ops', { headers: { Accept: 'application/json' } });
      if (res.ok) {
        const eo = (await res.json()).status || {};
        if (eo.exists && eo.workspace_id) {
          const link = document.createElement('a');
          link.className = 'hq-followup-manage-link';
          link.href = `/workspaces/${encodeURIComponent(eo.workspace_id)}`;
          link.textContent = 'Manage in Email Ops →';
          mount.appendChild(link);
        }
      }
    } catch (_) {
      /* non-fatal: the read-only list still renders */
    }
  }

  function renderJournalEditor(mount, view) {
    mount.innerHTML = '';
    const card = document.createElement('div');
    card.className = 'hq-journal-card';

    const heading = document.createElement('h4');
    heading.className = 'hq-journal-heading';
    heading.textContent = 'End-of-day journal';
    card.appendChild(heading);

    if (view.degraded) {
      const note = document.createElement('p');
      note.className = 'hq-journal-note';
      note.textContent = 'Some activity could not be loaded — the draft may be incomplete.';
      card.appendChild(note);
    }

    const editor = document.createElement('textarea');
    editor.className = 'hq-journal-editor';
    editor.rows = 10;
    editor.value = view.draft;
    editor.setAttribute('aria-label', 'End-of-day journal draft');
    card.appendChild(editor);

    const actions = document.createElement('div');
    actions.className = 'hq-journal-actions';

    const saveBtn = document.createElement('button');
    saveBtn.type = 'button';
    saveBtn.className = 'modern-btn modern-btn-primary modern-btn-sm';
    saveBtn.textContent = 'Save journal';
    saveBtn.addEventListener('click', async () => {
      saveBtn.disabled = true;
      try {
        await postJSON('/api/personal-hq/journal/save', {
          local_date: view.localDate,
          content: editor.value
        });
        toast('Your journal was saved.', 'Saved', 'success');
        mount.innerHTML = '';
        mount.appendChild(journalLauncher(mount));
      } catch (e) {
        toast(e && e.message ? e.message : 'Could not save the journal.', 'Save failed', 'danger');
        saveBtn.disabled = false;
      }
    });

    const dismissBtn = document.createElement('button');
    dismissBtn.type = 'button';
    dismissBtn.className = 'modern-btn modern-btn-secondary modern-btn-sm';
    dismissBtn.textContent = 'Not now';
    dismissBtn.addEventListener('click', async () => {
      // Dismiss is a pure no-op server-side; just collapse the editor.
      try {
        await postJSON('/api/personal-hq/journal/dismiss');
      } catch (_) {
        /* no-op */
      }
      mount.innerHTML = '';
      mount.appendChild(journalLauncher(mount));
    });

    actions.append(saveBtn, dismissBtn);
    card.appendChild(actions);
    mount.appendChild(card);
    editor.focus();
  }

  function journalLauncher(mount) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'modern-btn modern-btn-secondary modern-btn-sm hq-journal-launch';
    btn.textContent = 'Write your end-of-day journal';
    btn.addEventListener('click', async () => {
      btn.disabled = true;
      try {
        const res = await fetch('/api/personal-hq/journal/propose', {
          headers: { Accept: 'application/json' }
        });
        const data = res.ok ? await res.json() : null;
        renderJournalEditor(mount, journalPromptView(data && data.proposal));
      } catch (_) {
        toast('Could not open the journal.', 'Error', 'danger');
        btn.disabled = false;
      }
    });
    return btn;
  }

  // wireJournal shows an on-demand end-of-day journal launcher. Additive: no-op
  // without #hqJournalMount or a valid HQ. (A scheduled prompt is a future add.)
  async function wireJournal() {
    const mount = document.getElementById('hqJournalMount');
    if (!mount) return;
    let status;
    try {
      status = await fetchStatus();
    } catch (_) {
      return;
    }
    if (!status || !status.valid) {
      mount.hidden = true;
      return;
    }
    mount.hidden = false;
    mount.innerHTML = '';
    mount.appendChild(journalLauncher(mount));
  }

  function init() {
    wireMapActions();
    wireBuildModal();

    void refreshHQStatus().catch(() => {
      /* Status is additive; a degraded HQ service must not block the launcher. */
    });
    wireUpgrade();
    wireEmail();
    wireMailReview();
    wireFollowUps();
    wireJournal();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
