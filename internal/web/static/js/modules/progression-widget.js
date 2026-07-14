// progression-widget.js — the onboarding quest-log widget on the home page.
//
// Fetches GET /api/progression, renders the current tier's quests + progress,
// and shows a toast when a quest (or a whole tier) newly completes. Dismissible
// (POST /api/progression/dismiss) with a restore affordance, and lets an
// optional quest be Skipped (POST /api/progression/skip) without dismissing
// the whole widget. Purely additive: no-op on pages without the #questLog
// element.
//
// Pure helpers are exported (loaded as type="module") so progression-widget.test.js
// can exercise the optional/skipped-quest rendering and toast-suppression
// decisions without a DOM.

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
  return (tier?.quests || []).filter(quest => quest.status === 'completed' || quest.status === 'skipped').length;
}

export function tierInsignia(tier) {
  const value = Number(tier);
  return Number.isFinite(value) && value > 0 ? String(value).padStart(2, '0') : '—';
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
  let knownCompleted = null; // Set of completed quest IDs; null until first load.
  let knownTierComplete = {}; // tier number -> bool, from the previous render.
  let latestStatus = null;

  function el(role) {
    return widget ? widget.querySelector(`[data-role="${role}"]`) : null;
  }

  function restoreEl(role) {
    return restore ? restore.querySelector(`[data-role="${role}"]`) : null;
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

  function renderRestoreBadge(status, current) {
    const resolved = status.all_complete ? status.resolved_count : resolvedCount(current);
    const total = status.all_complete ? status.total_count : (current?.quests || []).length;
    const tierLabel = status.all_complete ? 'All tiers complete' : `Tier ${status.current_tier}`;

    const insignia = restoreEl('restore-insignia');
    const tier = restoreEl('restore-tier');
    const progress = restoreEl('restore-progress');
    const meter = restoreEl('restore-meter');
    if (insignia)
      insignia.textContent = status.all_complete ? '✓' : tierInsignia(status.current_tier);
    if (tier) tier.textContent = tierLabel;
    if (progress) progress.textContent = `${resolved}/${total}`;
    if (meter) meter.style.width = `${total > 0 ? Math.round((resolved / total) * 100) : 0}%`;
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
    li.className = 'quest-item' + (state.done ? ' quest-item-done' : '') + (state.skipped ? ' quest-item-skipped' : '');

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

  function render(status) {
    if (!widget) return;
    latestStatus = status;

    const current = currentTier(status);

    if (status.dismissed) {
      renderRestoreBadge(status, current);
      widget.hidden = true;
      if (restore) restore.hidden = false;
      return;
    }
    if (restore) restore.hidden = true;

    // All quests complete: compact congratulatory state.
    if (status.all_complete) {
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
      widget.hidden = true;
      return;
    }

    el('tier-name').textContent = current.name;
    el('tier-insignia').textContent = tierInsignia(status.current_tier);
    el('progress-label').textContent = `Tier ${status.current_tier} of ${status.total_tiers}`;
    const resolved = resolvedCount(current);
    const total = current.quests.length;
    el('progress-count').textContent = `${resolved}/${total}`;
    setMeter(el('progress-bar'), resolved, total);

    const list = el('quests');
    list.innerHTML = '';
    current.quests.forEach(q => list.appendChild(renderQuestRow(q)));

    const why = el('why');
    why.textContent = status.next_quest ? status.next_quest.why : '';

    widget.hidden = false;
  }

  async function refresh() {
    try {
      const status = await fetchStatus();
      announceChanges(status);
      render(status);
    } catch (_) {
      // Silent: the widget is optional; never disrupt the home page.
    }
  }

  function wireControls() {
    const dismissBtn = el('dismiss');
    if (dismissBtn) {
      dismissBtn.addEventListener('click', async () => {
        const optimistic = latestStatus ? { ...latestStatus, dismissed: true } : null;
        if (optimistic) render(optimistic);
        try {
          const status = await postJSON('/api/progression/dismiss', { dismissed: true });
          render(status);
        } catch (_) {
          if (latestStatus) render({ ...latestStatus, dismissed: false });
        }
      });
    }
    if (restore) {
      const restoreBtn = restore.querySelector('[data-role="restore"]');
      if (restoreBtn) {
        restoreBtn.addEventListener('click', async () => {
          try {
            const status = await postJSON('/api/progression/dismiss', { dismissed: false });
            render(status);
          } catch (_) {
            /* ignore */
          }
        });
      }
    }
  }

  function init() {
    widget = document.getElementById('questLog');
    restore = document.getElementById('questLogRestore');
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
