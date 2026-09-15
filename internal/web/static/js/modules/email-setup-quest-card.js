// email-setup-quest-card.js — the quiet "Resume" card for the Email Ops setup
// quest in Home's Quests flyout.
//
// It only ever reads GET /api/host-setup-quests/email_ops_setup/status, which
// never creates progress, so Home renders nothing and writes nothing for a user
// who has not started the quest. The card appears only while the quest is
// started, unfinished, not dismissed, and never completed. "Resume" opens the
// quest exactly as its URL does; "Not now" records the quest's own persisted
// dismissal, and opening the quest again from anywhere clears it.
//
// Pure helpers are exported for email-setup-quest-card.test.js.

import { loadOnboardingStatus, onboardingGateDecision } from './onboarding-gate.js';

export const EMAIL_SETUP_QUEST_ID = 'email_ops_setup';
export const EMAIL_SETUP_STATUS_URL = `/api/host-setup-quests/${EMAIL_SETUP_QUEST_ID}/status`;
export const EMAIL_SETUP_DISMISS_URL = `/api/host-setup-quests/${EMAIL_SETUP_QUEST_ID}/dismiss`;
export const EMAIL_SETUP_QUEST_URL = `/?setup=quest&source=host&quest=${EMAIL_SETUP_QUEST_ID}`;

const RESUMABLE = new Set(['in_progress', 'needs_attention']);

// FEATURED_MISSION_EVENT is announced by progression-widget.js whenever the
// Quests card's featured mission renders, with { visible, completed, actionURL }.
export const FEATURED_MISSION_EVENT = 'ori:featured-mission';

// featuredMissionCoversQuest reports whether the featured starter mission is
// already offering this same setup (Mission 03's email branch). This card would
// then be a second Resume for one setup, so it stays hidden.
export function featuredMissionCoversQuest(featured) {
  return Boolean(
    featured &&
    featured.visible === true &&
    featured.completed !== true &&
    featured.actionURL === EMAIL_SETUP_QUEST_URL
  );
}

// resumeCardView decides whether the card renders and what it says. Anything
// unexpected renders nothing: the card is optional and must stay quiet.
export function resumeCardView(status) {
  if (!status || status.exists !== true) return null;
  const journey = status.setup_journey;
  if (
    !journey ||
    journey.journey?.source !== 'host' ||
    journey.journey?.id !== EMAIL_SETUP_QUEST_ID
  ) {
    return null;
  }
  const lifecycle = journey.lifecycle_state || journey.lifecycle;
  if (!RESUMABLE.has(lifecycle) || journey.dismissed === true || journey.first_completed_at) {
    return null;
  }
  const steps = Array.isArray(journey.steps) ? journey.steps : [];
  const index = steps.findIndex(step => step?.id === journey.current_step_id);
  if (index < 0) return null;
  return {
    title: String(journey.journey.title || 'Set up Email Ops'),
    progress: `Step ${index + 1} of ${steps.length} · ${String(steps[index].title || '')}`,
    attention: lifecycle === 'needs_attention',
    revision: Number(journey.state_revision) || 0
  };
}

export function dismissRequestBody(revision, key) {
  return { if_revision: Number(revision) || 0, idempotency_key: String(key || '') };
}

function newKey() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  return `email-setup-card-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

// createEmailSetupQuestCard binds one card element. Every dependency is
// injectable so the behaviour can be tested without a browser.
export function createEmailSetupQuestCard(root, deps = {}) {
  const fetchImpl = deps.fetchImpl || ((...args) => globalThis.fetch(...args));
  const onboarding = deps.loadOnboarding || (() => loadOnboardingStatus());
  const dispatch =
    deps.dispatch ||
    (detail =>
      globalThis.window?.dispatchEvent(new CustomEvent('ori:open-specialist-setup', { detail })));
  const featuredMission = deps.featuredMission || (() => globalThis.window?.OriFeaturedMission);
  let generation = 0;
  let view = null;
  // The last resumable view, kept even while the featured mission covers it,
  // so the card can reappear the moment the mission moves on.
  let resumable = null;

  const part = role => root?.querySelector?.(`[data-role="${role}"]`) || null;

  function hide() {
    view = null;
    if (root) root.hidden = true;
  }

  function show(next) {
    view = next;
    const title = part('email-setup-title');
    const progress = part('email-setup-progress');
    if (title) title.textContent = next.title;
    if (progress) progress.textContent = next.progress;
    root.classList?.toggle?.('is-paused', next.attention);
    root.hidden = false;
  }

  async function refresh() {
    const current = ++generation;
    if (!root) return null;
    try {
      const gate = onboardingGateDecision(await onboarding());
      if (current !== generation) return null;
      if (!gate.allowWorkspaceHydration) {
        hide();
        return null;
      }
      const response = await fetchImpl(EMAIL_SETUP_STATUS_URL, {
        headers: { Accept: 'application/json' }
      });
      if (current !== generation) return null;
      if (!response.ok) {
        hide();
        return null;
      }
      const next = resumeCardView(await response.json());
      if (current !== generation) return null;
      resumable = next;
      if (next && !featuredMissionCoversQuest(featuredMission())) show(next);
      else hide();
      return next;
    } catch (_) {
      if (current === generation) hide();
      return null;
    }
  }

  function resume(event) {
    if (event?.ctrlKey || event?.metaKey || event?.shiftKey || event?.altKey) return;
    event?.preventDefault?.();
    generation++;
    resumable = null;
    hide();
    dispatch({ source: 'host', quest_id: EMAIL_SETUP_QUEST_ID });
  }

  async function dismiss() {
    if (!view) return false;
    const revision = view.revision;
    generation++;
    resumable = null;
    hide();
    try {
      const response = await fetchImpl(EMAIL_SETUP_DISMISS_URL, {
        method: 'POST',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
        body: JSON.stringify(dismissRequestBody(revision, newKey()))
      });
      if (!response.ok) await refresh();
      return response.ok;
    } catch (_) {
      await refresh();
      return false;
    }
  }

  // featuredMissionChanged re-applies the covered rule without a new read.
  function featuredMissionChanged() {
    if (!resumable) return;
    if (featuredMissionCoversQuest(featuredMission())) hide();
    else show(resumable);
  }

  part('email-setup-resume')?.addEventListener?.('click', resume);
  part('email-setup-dismiss')?.addEventListener?.('click', () => void dismiss());

  return {
    refresh,
    resume,
    dismiss,
    featuredMissionChanged,
    get view() {
      return view;
    }
  };
}

function initialize() {
  const root = globalThis.document?.getElementById?.('emailSetupQuestCard');
  if (!root) return;
  const card = createEmailSetupQuestCard(root);
  void card.refresh();
  globalThis.window?.addEventListener?.(FEATURED_MISSION_EVENT, () =>
    card.featuredMissionChanged()
  );
  // The quest modal owns progress; when it closes, re-read the card's state.
  globalThis.document
    ?.getElementById?.('specialistSetupJourneyModal')
    ?.addEventListener?.('hidden.bs.modal', () => void card.refresh());
}

if (globalThis.document?.readyState === 'loading') {
  globalThis.document.addEventListener('DOMContentLoaded', initialize, { once: true });
} else {
  initialize();
}
