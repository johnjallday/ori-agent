// personal-hq-onboarding.js — the guided first-launch Personal HQ experience
// on the workspace launcher page: the full-screen guided Map state for a
// brand-new profile (Build My HQ / Start with a Project / Skip for Now), a
// smaller resume entry for a user who skipped or lost a valid HQ, and the
// Build My HQ / Choose Existing Workspace modals. Purely additive: no-op on
// pages without #hqOnboardingGuided (only the workspace launcher has it).
//
// Pure decision logic is exported (loaded as type="module") so
// personal-hq-onboarding.test.js can exercise it without a DOM.

// resolveGuidedMode is the single source of truth for which of the three
// launcher states to show, given the current Personal HQ status. Kept
// side-effect-free so it is unit-testable without a DOM/fetch.
//   - "guided": full-screen first-launch takeover (never seen before).
//   - "repair": a stored designation exists but does not resolve.
//   - "resume": no valid HQ and onboarding is not "unseen" (skipped, in
//     progress, or completed-but-since-cleared) — a small non-blocking entry.
//   - "none": a valid HQ is designated; nothing to show here.
export function resolveGuidedMode(status) {
  if (!status) return 'none';
  const hasDesignation = !!status.workspace_id;
  if (hasDesignation && !status.valid) return 'repair';
  if (status.valid) return 'none';
  if (status.hq_onboarding_state === 'unseen') return 'guided';
  return 'resume';
}

// wantsGuidedTakeover decides whether the full-screen guided takeover should
// appear on the workspace launcher. It requires BOTH a brand-new ("unseen")
// profile AND explicit Mission 01 intent — the "Start mission" CTA navigates
// here with ?hq_onboarding=1. Merely browsing to the launcher (even as an
// unseen profile) must not force the takeover, so the user can explore. Pure
// for unit testing.
export function wantsGuidedTakeover(hint, hasIntent) {
  return hint === 'unseen' && !!hasIntent;
}

// resumeCopy derives the resume/repair banner's text and which action
// buttons it offers, so the DOM-wiring code has no branching logic of its
// own to get wrong.
export function resumeCopy(mode) {
  if (mode === 'repair') {
    return {
      text: 'Your Personal HQ needs attention — the workspace it pointed to is no longer available.',
      showBuild: true,
      showChoose: true,
      showClear: true
    };
  }
  return {
    text: 'Set up your Personal HQ for a daily brief and follow-up tracking.',
    showBuild: true,
    showChoose: true,
    showClear: false
  };
}

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
    return { show: true, canApply: false, blocked: true, heading: 'Personal HQ upgrade unavailable', reasons: blockers };
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

// emailStatusView turns the Personal HQ email status (from GET
// /api/personal-hq/email/status) into the view-model the connect/grant/repair UI
// renders, so all branching is unit-testable. Three states:
//   - connected: show the address + a Disconnect action.
//   - repair: a binding exists but the account is gone/expired — offer Reconnect.
//   - disconnected: never connected — offer Connect.
export function emailStatusView(status) {
  if (status && status.connected) {
    return {
      state: 'connected',
      heading: 'Email connected',
      detail: status.email_address || '',
      action: 'disconnect',
      actionLabel: 'Disconnect'
    };
  }
  if (status && status.account_id) {
    return {
      state: 'repair',
      heading: 'Reconnect your email',
      detail: 'Your connected email needs to be reconnected before the assistant can read it.',
      action: 'connect',
      actionLabel: 'Reconnect'
    };
  }
  return {
    state: 'disconnected',
    heading: 'Connect your email',
    detail: 'Let your Inbox specialist surface threads that need attention and help draft replies. Read-only — nothing is ever sent without your explicit confirmation.',
    action: 'connect',
    actionLabel: 'Connect email'
  };
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
    case 'i_owe': return 'You owe';
    case 'waiting_on': return 'Waiting on';
    case 'needs_decision': return 'Needs decision';
    case 'recurring_check_in': return 'Check-in';
    default: return 'Follow-up';
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

  const guided = document.getElementById('hqOnboardingGuided');
  const homeResume = document.getElementById('homeHQResume');
  if (!guided && !homeResume) return; // Neither the launcher nor Home has an HQ surface.

  const resume = document.getElementById('hqOnboardingResume');
  const hub = document.getElementById('workspaceHub');

  // True only when the user arrived via the Mission 01 "Start mission" CTA
  // (?hq_onboarding=1), which is what gates the full-screen guided takeover.
  function hasOnboardingIntent() {
    try {
      return new URLSearchParams(window.location.search).get('hq_onboarding') === '1';
    } catch (_) {
      return false;
    }
  }

  async function fetchStatus() {
    const res = await fetch('/api/personal-hq/status', { headers: { Accept: 'application/json' } });
    if (!res.ok) throw new Error(`personal hq status ${res.status}`);
    const body = await res.json();
    return body.status;
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

  function setLauncherContentHidden(hidden) {
    ['launcherGrid', 'launcherEmptyState', 'launcherMap'].forEach(id => {
      const el = document.getElementById(id);
      if (el) el.hidden = hidden;
    });
  }

  function skipHQObjective() {
    return Promise.all([
      postJSON('/api/personal-hq/onboarding-state', { state: 'skipped' }),
      postJSON('/api/progression/skip', { quest_id: 't2-build-hq' }).catch(() => {
        /* Quest may already be resolved; skipping onboarding state is what matters. */
      })
    ]);
  }

  function wireGuided() {
    const buildBtn = document.getElementById('hqGuidedBuildBtn');
    const projectBtn = document.getElementById('hqGuidedProjectBtn');
    const skipBtn = document.getElementById('hqGuidedSkipBtn');

    if (buildBtn) buildBtn.addEventListener('click', () => openBuildModal());
    if (projectBtn) {
      projectBtn.addEventListener('click', async () => {
        try {
          await skipHQObjective();
        } catch (_) {
          /* non-fatal: still let the user start a project */
        }
        hideGuided();
        if (window.sessionManager && typeof window.sessionManager.showAddWorkspaceModal === 'function') {
          window.sessionManager.showAddWorkspaceModal({ entryPoint: 'guided_map_project' });
        }
      });
    }
    if (skipBtn) {
      skipBtn.addEventListener('click', async () => {
        skipBtn.disabled = true;
        try {
          await skipHQObjective();
          hideGuided();
          await refreshResume();
        } catch (err) {
          toast('Could not skip right now. Try again.', 'Skip failed', 'error');
        } finally {
          skipBtn.disabled = false;
        }
      });
    }
  }

  function hideGuided() {
    if (!guided) return;
    guided.hidden = true;
    hub?.classList.remove('is-hq-guided');
    setLauncherContentHidden(false);
  }

  function showGuided() {
    if (!guided) return; // Home has no full-screen takeover, only the resume bar.
    hub?.classList.add('is-hq-guided');
    guided.hidden = false;
    setLauncherContentHidden(true);
    if (resume) resume.hidden = true;
  }

  function updateResumeBar(el, textEl, mode) {
    if (!el) return;
    if (mode === 'none') {
      el.hidden = true;
      return;
    }
    const copy = resumeCopy(mode);
    if (textEl) textEl.textContent = copy.text;
    el.hidden = false;
  }

  async function refreshResume() {
    let status;
    try {
      status = await fetchStatus();
    } catch (_) {
      return;
    }
    const mode = resolveGuidedMode(status);
    if (mode === 'guided' && hasOnboardingIntent()) {
      showGuided();
    } else {
      hideGuided();
    }

    if (resume) {
      if (mode === 'guided' || mode === 'none') {
        resume.hidden = true;
      } else {
        const copy = resumeCopy(mode);
        const text = document.getElementById('hqResumeText');
        const chooseBtn = document.getElementById('hqResumeChooseBtn');
        const clearBtn = document.getElementById('hqResumeClearBtn');
        if (text) text.textContent = copy.text;
        if (chooseBtn) chooseBtn.hidden = !copy.showChoose;
        if (clearBtn) clearBtn.hidden = !copy.showClear;
        resume.hidden = false;
      }
    }

    if (homeResume) {
      // On Home, the Mission 01 quest-log card is the single first-run invite
      // for a brand-new ("guided"/unseen) profile, so suppress the redundant
      // resume bar there. It still surfaces for skipped/resume/repair states.
      updateResumeBar(homeResume, document.getElementById('homeHQResumeText'), mode === 'guided' ? 'none' : mode);
    }
  }

  function wireResume() {
    if (resume) {
      const buildBtn = document.getElementById('hqResumeBuildBtn');
      const chooseBtn = document.getElementById('hqResumeChooseBtn');
      const clearBtn = document.getElementById('hqResumeClearBtn');
      if (buildBtn) buildBtn.addEventListener('click', () => openBuildModal());
      if (chooseBtn) chooseBtn.addEventListener('click', () => openChooseExistingModal());
      if (clearBtn) {
        clearBtn.addEventListener('click', async () => {
          clearBtn.disabled = true;
          try {
            await postJSON('/api/personal-hq/clear');
            toast('Personal HQ designation cleared.', 'Cleared');
            await refreshResume();
          } catch (err) {
            toast('Could not clear the designation. Try again.', 'Clear failed', 'error');
          } finally {
            clearBtn.disabled = false;
          }
        });
      }
    }
    if (homeResume) {
      // Home's resume bar is intentionally simple (no modals live on this
      // page): Build My HQ routes to the launcher, which owns the full flow.
      const buildBtn = document.getElementById('homeHQResumeBuildBtn');
      if (buildBtn) {
        buildBtn.addEventListener('click', () => {
          window.location.href = '/workspaces?hq_onboarding=1';
        });
      }
    }
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

  async function openBuildModal() {
    const tzInput = document.getElementById('hqBuildTimezone');
    if (tzInput && !tzInput.value) {
      tzInput.value = await defaultTimezone();
    }
    const errorBox = document.getElementById('hqBuildError');
    if (errorBox) errorBox.hidden = true;
    const modal = bootstrapModal('hqBuildModal');
    if (modal) modal.show();
  }

  function collectBuildRequest() {
    const name = (document.getElementById('hqBuildName')?.value || '').trim() || 'My HQ';
    const timezone = (document.getElementById('hqBuildTimezone')?.value || '').trim();
    const scheduleDays = Array.from(document.querySelectorAll('#hqBuildAdvanced .hq-build-days input:checked')).map(i => i.value);
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
        const result = await postJSON('/api/personal-hq/setup', collectBuildRequest());
        const modal = bootstrapModal('hqBuildModal');
        if (modal) modal.hide();
        hideGuided();
        toast('Your Personal HQ is ready.', 'Personal HQ built');
        window.setTimeout(() => {
          window.location.href = '/';
        }, 700);
        void result;
      } catch (err) {
        if (errorBox) {
          errorBox.textContent = err && err.message ? err.message : 'Could not build your Personal HQ. Try again.';
          errorBox.hidden = false;
        }
      } finally {
        submitBtn.disabled = false;
      }
    });
  }

  // ---- Choose Existing Workspace modal ----

  async function fetchEligibleWorkspaces() {
    const res = await fetch('/api/workspaces?tree=true', { headers: { Accept: 'application/json' } });
    if (!res.ok) throw new Error(`workspaces ${res.status}`);
    const data = await res.json();
    const flat = [];
    const walk = list => {
      (list || []).forEach(ws => {
        if (ws.kind !== 'group' && ws.status !== 'trashed' && ws.status !== 'missing') flat.push(ws);
        if (ws.children) walk(ws.children);
      });
    };
    walk(data.workspaces || data.folders || []);
    return flat;
  }

  async function designateWorkspace(ws, btn, errorBox) {
    btn.disabled = true;
    try {
      await postJSON('/api/personal-hq/replace', { workspace_id: ws.id });
      await postJSON('/api/personal-hq/onboarding-state', { state: 'completed' });
      const modalInstance = bootstrapModal('hqChooseExistingModal');
      if (modalInstance) modalInstance.hide();
      toast(`${ws.name || 'Workspace'} is now your Personal HQ.`, 'Personal HQ designated');
      hideGuided();
      await refreshResume();
    } catch (err) {
      if (errorBox) {
        errorBox.textContent = err && err.message ? err.message : 'Could not designate this workspace. Try again.';
        errorBox.hidden = false;
      }
    } finally {
      btn.disabled = false;
    }
  }

  // Replacing an existing valid HQ requires confirmation naming both
  // workspaces (PRD FR37) — no content is deleted, only the relationship
  // changes. A first-time designation (no current valid HQ) skips straight
  // to the API call since there's nothing to name a replacement against.
  function confirmReplace(li, ws, currentName, onConfirm) {
    li.innerHTML = '';
    const text = document.createElement('span');
    text.className = 'hq-choose-item-name';
    text.textContent = `Replace "${currentName}" with "${ws.name || ws.id}"? No content is deleted.`;
    const confirmBtn = document.createElement('button');
    confirmBtn.type = 'button';
    confirmBtn.className = 'modern-btn modern-btn-primary modern-btn-sm';
    confirmBtn.textContent = 'Confirm';
    confirmBtn.addEventListener('click', onConfirm);
    const cancelBtn = document.createElement('button');
    cancelBtn.type = 'button';
    cancelBtn.className = 'modern-btn modern-btn-secondary modern-btn-sm';
    cancelBtn.textContent = 'Cancel';
    cancelBtn.addEventListener('click', () => openChooseExistingModal());
    li.append(text, confirmBtn, cancelBtn);
  }

  async function openChooseExistingModal() {
    const list = document.getElementById('hqChooseExistingList');
    const empty = document.getElementById('hqChooseExistingEmpty');
    const errorBox = document.getElementById('hqChooseExistingError');
    if (!list) return;
    list.innerHTML = '';
    if (errorBox) errorBox.hidden = true;
    if (empty) empty.hidden = true;

    const modal = bootstrapModal('hqChooseExistingModal');
    if (modal) modal.show();

    let workspaces;
    let currentStatus;
    try {
      [workspaces, currentStatus] = await Promise.all([fetchEligibleWorkspaces(), fetchStatus()]);
    } catch (_) {
      if (errorBox) {
        errorBox.textContent = 'Could not load workspaces. Try again.';
        errorBox.hidden = false;
      }
      return;
    }
    if (workspaces.length === 0) {
      if (empty) empty.hidden = false;
      return;
    }
    const currentName = currentStatus && currentStatus.valid
      ? (workspaces.find(w => w.id === currentStatus.workspace_id)?.name || 'your current HQ')
      : null;

    workspaces.forEach(ws => {
      const li = document.createElement('li');
      li.className = 'hq-choose-item';
      const name = document.createElement('span');
      name.className = 'hq-choose-item-name';
      name.textContent = ws.name || ws.id;
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'modern-btn modern-btn-secondary modern-btn-sm';
      btn.textContent = 'Designate';
      btn.addEventListener('click', () => {
        if (currentName && ws.id !== currentStatus.workspace_id) {
          confirmReplace(li, ws, currentName, () => designateWorkspace(ws, btn, errorBox));
        } else {
          designateWorkspace(ws, btn, errorBox);
        }
      });
      li.append(name, btn);
      list.appendChild(li);
    });
  }

  async function fetchUpgradePreview() {
    const res = await fetch('/api/personal-hq/upgrade/preview', { headers: { Accept: 'application/json' } });
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
        toast('The upgrade did not fully complete — you can retry.', 'Upgrade incomplete', 'danger');
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
    const res = await fetch('/api/personal-hq/email/status', { headers: { Accept: 'application/json' } });
    if (!res.ok) return null;
    const data = await res.json();
    return data && data.status ? data.status : null;
  }

  // connectEmail opens the existing Vault email OAuth popup (least-privilege
  // read scope) and, on success, links the returned account to the HQ. It
  // depends on the Vault email OAuth being configured; failures surface as a
  // toast rather than a broken flow.
  function connectEmail() {
    const popup = window.open('/api/vault/email/oauth/start?provider=gmail', 'ori-hq-email', 'width=520,height=680');
    if (!popup) {
      toast('Allow pop-ups to connect your email.', 'Popup blocked', 'danger');
      return;
    }
    const onMessage = async (event) => {
      const data = event && event.data;
      if (!data || data.type !== 'ori:vault-email-oauth') return;
      window.removeEventListener('message', onMessage);
      if (!data.success || !data.account || !data.account.id) {
        toast(data && data.error ? data.error : 'Could not connect your email.', 'Connect failed', 'danger');
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

  function renderEmail(mount, view) {
    mount.innerHTML = '';
    mount.hidden = false;
    const card = document.createElement('div');
    card.className = 'hq-email-card hq-email-' + view.state;

    const heading = document.createElement('h4');
    heading.className = 'hq-email-heading';
    heading.textContent = view.heading;
    card.appendChild(heading);

    if (view.detail) {
      const detail = document.createElement('p');
      detail.className = 'hq-email-detail';
      detail.textContent = view.detail;
      card.appendChild(detail);
    }

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'modern-btn modern-btn-sm ' + (view.action === 'disconnect' ? 'modern-btn-secondary' : 'modern-btn-primary');
    btn.textContent = view.actionLabel;
    btn.addEventListener('click', async () => {
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
      } else {
        connectEmail();
      }
    });
    card.appendChild(btn);
    mount.appendChild(card);
  }

  // wireEmail shows the email connect/grant/repair card for a valid designated
  // HQ. Additive: a no-op when the page has no #hqEmailMount or no valid HQ.
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
    let emailStatus;
    try {
      emailStatus = await fetchEmailStatus();
    } catch (_) {
      return;
    }
    renderEmail(mount, emailStatusView(emailStatus));
  }

  async function fetchProposals() {
    const res = await fetch('/api/personal-hq/mail/proposals', { headers: { Accept: 'application/json' } });
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
    [['To', view.to], ['Subject', view.subject]].forEach(([label, value]) => {
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
        await postJSON('/api/personal-hq/mail/confirm', { id: view.id, expected_hash: view.payloadHash });
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
    const res = await fetch('/api/personal-hq/followups/home', { headers: { Accept: 'application/json' } });
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

    const actions = document.createElement('div');
    actions.className = 'hq-followup-actions';

    const act = async (url, body, label) => {
      try {
        await postJSON(url, body);
        await wireFollowUps();
      } catch (_) {
        toast(`Could not ${label} the follow-up.`, 'Error', 'danger');
      }
    };

    if (view.isCandidate) {
      const confirmBtn = document.createElement('button');
      confirmBtn.type = 'button';
      confirmBtn.className = 'modern-btn modern-btn-primary modern-btn-sm';
      confirmBtn.textContent = 'Track this';
      confirmBtn.addEventListener('click', () => act('/api/personal-hq/followups/confirm', { id: view.id }, 'confirm'));
      const dismissBtn = document.createElement('button');
      dismissBtn.type = 'button';
      dismissBtn.className = 'modern-btn modern-btn-secondary modern-btn-sm';
      dismissBtn.textContent = 'Not a follow-up';
      dismissBtn.addEventListener('click', () => act('/api/personal-hq/followups/dismiss', { id: view.id }, 'dismiss'));
      actions.append(confirmBtn, dismissBtn);
    } else {
      const doneBtn = document.createElement('button');
      doneBtn.type = 'button';
      doneBtn.className = 'modern-btn modern-btn-primary modern-btn-sm';
      doneBtn.textContent = 'Done';
      doneBtn.addEventListener('click', () => act('/api/personal-hq/followups/complete', { id: view.id }, 'complete'));
      const snoozeBtn = document.createElement('button');
      snoozeBtn.type = 'button';
      snoozeBtn.className = 'modern-btn modern-btn-secondary modern-btn-sm';
      snoozeBtn.textContent = 'Snooze 1 day';
      snoozeBtn.addEventListener('click', () => {
        const until = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
        act('/api/personal-hq/followups/snooze', { id: view.id, until }, 'snooze');
      });
      actions.append(doneBtn, snoozeBtn);
    }
    card.appendChild(actions);
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
        await postJSON('/api/personal-hq/journal/save', { local_date: view.localDate, content: editor.value });
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
      try { await postJSON('/api/personal-hq/journal/dismiss'); } catch (_) { /* no-op */ }
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
        const res = await fetch('/api/personal-hq/journal/propose', { headers: { Accept: 'application/json' } });
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
    wireGuided();
    wireResume();
    wireBuildModal();

    const hint = hub ? hub.dataset.hqOnboardingHint : null;
    if (wantsGuidedTakeover(hint, hasOnboardingIntent())) showGuided();
    refreshResume();
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
