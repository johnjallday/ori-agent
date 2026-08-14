// progression-widget.js — the Quests flyout content, driven by Progression.
//
// Fetches GET /api/progression, renders the current tier's quests + progress,
// and shows a toast when a quest (or a whole tier) newly completes. Lets an
// optional quest be Skipped (POST /api/progression/skip) without disturbing
// the rest. Purely additive: no-op on pages without the #questLog element.
//
// Issue #334: the compact #cockpitQuestsToggle header button is this
// widget's own always-available entry point — home-workspace-cockpit.js owns
// opening/closing the #cockpitQuestsFlyout it sits beside (one shared header
// panel state for Updates/Quests/Quick Capture), while this module owns
// everything inside: the compact summary text, the full tier content, and
// quest actions. The two coordinate through the narrow window.OriHomeCockpit
// seam rather than a direct import, matching how this module already reads
// #questLog's DOM instead of exporting a controller.
//
// The server-side `dismissed` field and its /api/progression/dismiss
// endpoint stay wired for backward compatibility (a prior collapsed value
// must never hide the compact trigger — FR33), but no longer gate anything
// here: a fresh Home load is compact-only because the FLYOUT starts closed,
// not because content is dismissed. Collapsing now closes the flyout instead
// of persisting a dismissed preference (FR34: no new persisted preference is
// required, and none is added).
//
// Pure helpers are exported (loaded as type="module") so progression-widget.test.js
// can exercise the compact-summary, optional/skipped-quest rendering, and
// toast-suppression decisions without a DOM.

import { loadOnboardingStatus, onboardingGateDecision } from './onboarding-gate.js';

export function currentTier(status) {
  return (
    (status.tiers || []).find(tier => tier.tier === status.current_tier) ||
    (status.tiers || [])[0] ||
    null
  );
}

export function completedCount(tier) {
  return (tier?.quests || []).filter(quest => quest.status === 'completed').length;
}

// resolvedCount includes skipped optional quests alongside completed ones, so
// the meter/progress count advances past a skip without the skipped quest
// ever being labeled "complete" in its own row (task 3.4/3.8).
export function resolvedCount(tier) {
  return (tier?.quests || []).filter(
    quest => quest.status === 'completed' || quest.status === 'skipped'
  ).length;
}

export function tierInsignia(tier) {
  const value = Number(tier);
  return Number.isFinite(value) && value > 0 ? String(value).padStart(2, '0') : '—';
}

/**
 * The compact summary shown on the always-available Quests header button
 * (FR27-FR28, Issue #334).
 *
 * `visible: false` means there is nothing truthful to show yet (onboarding
 * gate not open, fetch not landed, or a malformed/empty response) — the
 * caller keeps the trigger hidden rather than showing an empty summary.
 * Deliberately a tier/resolved-count reading distinct in both shape and
 * wording from the Updates attention badge's bare number, so achievement
 * progress and items-needing-attention can never be confused (FR28).
 */
export function compactSummaryView(status) {
  if (!status) return { visible: false, text: '' };
  if (status.all_complete) {
    return { visible: true, allComplete: true, text: 'All complete' };
  }
  const current = currentTier(status);
  if (!current) return { visible: false, text: '' };
  const resolved = resolvedCount(current);
  const total = (current.quests || []).length;
  return {
    visible: true,
    allComplete: false,
    tier: status.current_tier,
    resolved,
    total,
    text: `Tier ${status.current_tier} · ${resolved}/${total}`
  };
}

export const FIRST_MISSION_QUEST_ID = 't2-build-hq';

// Mission 01 is intentionally reachable before Tier 2 unlocks. The existing
// HQ quest remains the source of truth for status and completion; this helper
// only derives the featured Home presentation from that same quest record.
export function firstMissionView(status) {
  const quest = (status?.tiers || [])
    .flatMap(tier => tier.quests || [])
    .find(candidate => candidate.id === FIRST_MISSION_QUEST_ID);

  if (!quest || status?.all_complete) return { visible: false };

  const completed = quest.status === 'completed';
  const skipped = quest.status === 'skipped';
  return {
    visible: true,
    completed,
    skipped,
    title: quest.title,
    why: quest.why || '',
    statusLabel: completed ? 'Complete' : skipped ? 'Not set up' : 'Ready',
    actionLabel: quest.action_label || 'Build My HQ',
    actionURL: quest.action_url || '/workspaces?view=map&focus=personal-hq',
    showAction: !completed
  };
}

// The HQ quest is rendered as the featured Mission 01 card, so omit it from
// the ordinary tier list when Tier 2 becomes current rather than showing the
// same objective twice.
export function tierQuestRows(tier) {
  return (tier?.quests || []).filter(quest => quest.id !== FIRST_MISSION_QUEST_ID);
}

// questRowState derives the pure per-row rendering decision for one quest:
// which mark/affordances to show. Kept side-effect-free so it can be unit
// tested without constructing DOM nodes.
export function questRowState(quest) {
  const done = quest.status === 'completed';
  const skipped = quest.status === 'skipped';
  const resolved = done || skipped;
  return {
    done,
    skipped,
    resolved,
    mark: done ? '✓' : skipped ? '⏭' : '○',
    // A quest renders as a clickable link only while unresolved and it has
    // a destination; a resolved quest (done or skipped) never does — the
    // skipped case gets its own separate "Resume" affordance instead.
    showLink: !resolved && !!quest.action_url,
    // Skipped quests keep a Resume affordance so they stay reachable
    // without being a blocking interruption (never shown once truly done).
    showResume: skipped && !!quest.action_url,
    // The Skip control appears only for an optional quest that has not yet
    // resolved either way.
    showSkip: !resolved && !!quest.optional
  };
}

// diffAnnouncements is the pure diff between the previously known state and
// an incoming status: which quests newly completed and which tiers newly
// became complete, driving what announceChanges() below actually toasts.
//
// A skip must never itself produce a "quest complete" toast (it is not a
// completion), and a tier is only reported as newly complete here when at
// least one quest in it actually completed this round — a tier that became
// complete solely because its last open quest was skipped must not toast,
// since skipping the optional HQ objective should not read as an
// achievement.
export function diffAnnouncements(status, knownCompleted, knownTierComplete) {
  const completedNow = new Set();
  (status.tiers || []).forEach(t => {
    (t.quests || []).forEach(q => {
      if (q.status === 'completed') completedNow.add(q.id);
    });
  });

  const newCompletions = [];
  const newTierCompletions = [];
  if (knownCompleted !== null) {
    (status.tiers || []).forEach(t => {
      let tierHasNewCompletion = false;
      (t.quests || []).forEach(q => {
        if (q.status === 'completed' && !knownCompleted.has(q.id)) {
          newCompletions.push({ id: q.id, title: q.title });
          tierHasNewCompletion = true;
        }
      });
      if (t.complete && !knownTierComplete[t.tier] && tierHasNewCompletion) {
        newTierCompletions.push({ tier: t.tier, name: t.name });
      }
    });
  }

  const nextKnownTierComplete = {};
  (status.tiers || []).forEach(t => {
    nextKnownTierComplete[t.tier] = !!t.complete;
  });

  return { completedNow, nextKnownTierComplete, newCompletions, newTierCompletions };
}

(function () {
  if (typeof document === 'undefined') return;

  const POLL_MS = 20000;

  let widget = null;
  let restore = null;
  let trigger = null; // #cockpitQuestsToggle — the always-available compact entry point.
  let knownCompleted = null; // Set of completed quest IDs; null until first load.
  let knownTierComplete = {}; // tier number -> bool, from the previous render.

  function el(role) {
    return widget ? widget.querySelector(`[data-role="${role}"]`) : null;
  }

  /**
   * Reveal/hide the compact trigger and keep its summary text current.
   *
   * Never touches the flyout's own open/closed state — that is
   * home-workspace-cockpit.js's `panel` state alone (FR35: a refresh updates
   * the compact summary and any open flyout in place, never opening/closing
   * either).
   */
  function updateTrigger(status) {
    if (!trigger) return;
    const view = compactSummaryView(status);
    trigger.hidden = !view.visible;
    const summary = trigger.querySelector('[data-role="quests-summary"]');
    if (summary) summary.textContent = view.visible ? view.text : '';
  }

  async function fetchStatus() {
    const res = await fetch('/api/progression', { headers: { Accept: 'application/json' } });
    if (!res.ok) throw new Error(`progression status ${res.status}`);
    return res.json();
  }

  async function postJSON(url, body) {
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: body ? JSON.stringify(body) : undefined
    });
    if (!res.ok) throw new Error(`${url} -> ${res.status}`);
    return res.json();
  }

  function toast(message, title) {
    if (window.Toast && typeof window.Toast.success === 'function') {
      window.Toast.success(message, { title: title || 'Quest complete' });
    } else if (typeof window.notifyToast === 'function') {
      window.notifyToast(message, 'success');
    }
  }

  // Diff the incoming status against what we last knew and toast new wins.
  // Skipped on the very first load so a returning user isn't flooded.
  function announceChanges(status) {
    const diff = diffAnnouncements(status, knownCompleted, knownTierComplete);
    diff.newCompletions.forEach(q => toast(q.title, 'Quest complete'));
    diff.newTierCompletions.forEach(t => toast(`Tier complete: ${t.name}`, 'Tier complete'));

    knownCompleted = diff.completedNow;
    knownTierComplete = diff.nextKnownTierComplete;
  }

  function setMeter(bar, resolved, total) {
    if (!bar) return;
    const percent = total > 0 ? Math.round((resolved / total) * 100) : 0;
    bar.style.width = `${Math.max(0, Math.min(100, percent))}%`;

    const meter = bar.closest('[role="progressbar"]');
    if (meter) meter.setAttribute('aria-valuenow', String(Math.max(0, Math.min(100, percent))));
  }

  async function skipQuest(questID, button) {
    button.disabled = true;
    try {
      const status = await postJSON('/api/progression/skip', { quest_id: questID });
      render(status);
    } catch (_) {
      button.disabled = false;
    }
  }

  function renderQuestRow(q) {
    const state = questRowState(q);
    const li = document.createElement('li');
    li.className =
      'quest-item' +
      (state.done ? ' quest-item-done' : '') +
      (state.skipped ? ' quest-item-skipped' : '');

    const mark = document.createElement('span');
    mark.className = 'quest-mark';
    mark.textContent = state.mark; // Distinct glyph per state, not color alone.
    mark.setAttribute('aria-hidden', 'true');

    const title = document.createElement(state.showLink ? 'a' : 'span');
    if (state.showLink) {
      title.href = q.action_url;
      title.title = q.action_label || q.title;
    }
    title.className = 'quest-title';
    title.textContent = q.title;

    li.append(mark, title);

    if (state.skipped) {
      const label = document.createElement('span');
      label.className = 'quest-status quest-status-skipped';
      label.textContent = 'Skipped';
      li.append(label);
    }
    if (state.showResume) {
      const resume = document.createElement('a');
      resume.className = 'quest-resume';
      resume.href = q.action_url;
      resume.textContent = q.action_label || 'Resume';
      li.append(resume);
    }
    if (state.showSkip) {
      const skipBtn = document.createElement('button');
      skipBtn.type = 'button';
      skipBtn.className = 'quest-skip';
      skipBtn.textContent = 'Skip';
      skipBtn.setAttribute('aria-label', `Skip: ${q.title}`);
      skipBtn.addEventListener('click', () => skipQuest(q.id, skipBtn));
      li.append(skipBtn);
    }

    return li;
  }

  function renderFirstMission(status) {
    const mission = el('first-mission');
    if (!mission) return;

    const view = firstMissionView(status);
    if (!view.visible) {
      mission.hidden = true;
      return;
    }

    mission.classList.toggle('is-complete', view.completed);
    mission.classList.toggle('is-paused', view.skipped);
    const title = el('first-mission-title');
    const why = el('first-mission-why');
    const state = el('first-mission-status');
    const action = el('first-mission-action');
    const actionLabel = el('first-mission-action-label');

    if (title) title.textContent = view.title;
    if (why) why.textContent = view.why;
    if (state) state.textContent = view.statusLabel;
    if (action) {
      action.hidden = !view.showAction;
      action.href = view.actionURL;
    }
    if (actionLabel) actionLabel.textContent = view.actionLabel;
    mission.hidden = false;
  }

  function render(status) {
    if (!widget) return;

    const current = currentTier(status);

    // The compact trigger's visibility/summary is driven by the SAME status
    // every branch below renders from — Issue #334 retired `dismissed` as a
    // gate on either (FR31-FR34): the flyout's open/closed state now lives
    // entirely in home-workspace-cockpit.js's panel state, so this widget
    // only ever decides whether it HAS truthful content to show.
    updateTrigger(status);
    // #questLogRestore is retired UI (Issue #334) — kept in the template only
    // for id stability, never shown.
    if (restore) restore.hidden = true;

    // All quests complete: compact congratulatory state.
    if (status.all_complete) {
      renderFirstMission(status);
      el('tier-name').textContent = 'All quests complete';
      el('tier-insignia').textContent = '✓';
      el('progress-label').textContent = 'Tier complete';
      el('progress-count').textContent = `${status.total_count}/${status.total_count}`;
      setMeter(el('progress-bar'), status.total_count, status.total_count);
      el('quests').innerHTML = '';
      el('why').textContent = "You've mastered the basics — Ori is all yours.";
      widget.hidden = false;
      return;
    }

    if (!current) {
      renderFirstMission(status);
      widget.hidden = true;
      return;
    }

    renderFirstMission(status);

    el('tier-name').textContent = current.name;
    el('tier-insignia').textContent = tierInsignia(status.current_tier);
    el('progress-label').textContent = `Tier ${status.current_tier} of ${status.total_tiers}`;
    const resolved = resolvedCount(current);
    const total = current.quests.length;
    el('progress-count').textContent = `${resolved}/${total}`;
    setMeter(el('progress-bar'), resolved, total);

    const list = el('quests');
    list.innerHTML = '';
    tierQuestRows(current).forEach(q => list.appendChild(renderQuestRow(q)));

    const why = el('why');
    why.textContent = status.next_quest ? status.next_quest.why : '';

    widget.hidden = false;
  }

  async function refresh() {
    try {
      const onboardingStatus = await loadOnboardingStatus();
      if (!onboardingGateDecision(onboardingStatus).allowWorkspaceHydration) return;
      const status = await fetchStatus();
      announceChanges(status);
      render(status);
    } catch (_) {
      // Silent: the widget is optional; never disrupt the home page.
    }
  }

  /**
   * The quest log's own "Collapse" control now closes the Quests flyout
   * (FR31) instead of persisting a dismissed preference — home-workspace-
   * cockpit.js owns the flyout's open/closed state, so this widget reaches
   * it through the narrow window.OriHomeCockpit seam rather than a direct
   * import. Falls back to a no-op where that seam is absent (e.g. a page
   * without the cockpit, or a unit test with no DOM wiring).
   */
  function wireControls() {
    const dismissBtn = el('dismiss');
    if (dismissBtn) {
      dismissBtn.addEventListener('click', () => {
        const cockpit = window.OriHomeCockpit;
        if (cockpit && typeof cockpit.closeHeaderPanel === 'function') cockpit.closeHeaderPanel();
      });
    }
  }

  function init() {
    widget = document.getElementById('questLog');
    restore = document.getElementById('questLogRestore');
    trigger = document.getElementById('cockpitQuestsToggle');
    if (!widget) return; // Not the home page.

    wireControls();
    refresh();

    setInterval(refresh, POLL_MS);
    // Refresh when the tab regains focus for snappier completion feedback.
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') refresh();
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
