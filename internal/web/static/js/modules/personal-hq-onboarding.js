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

(function () {
  if (typeof document === 'undefined') return;

  const guided = document.getElementById('hqOnboardingGuided');
  const homeResume = document.getElementById('homeHQResume');
  if (!guided && !homeResume) return; // Neither the launcher nor Home has an HQ surface.

  const resume = document.getElementById('hqOnboardingResume');
  const hub = document.getElementById('workspaceHub');

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
    setLauncherContentHidden(false);
  }

  function showGuided() {
    if (!guided) return; // Home has no full-screen takeover, only the resume bar.
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
    if (mode === 'guided') {
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
      updateResumeBar(homeResume, document.getElementById('homeHQResumeText'), mode === 'guided' ? 'resume' : mode);
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

  function init() {
    wireGuided();
    wireResume();
    wireBuildModal();

    const hint = hub ? hub.dataset.hqOnboardingHint : null;
    if (hint === 'unseen') showGuided();
    refreshResume();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
