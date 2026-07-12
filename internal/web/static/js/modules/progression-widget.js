// progression-widget.js — the onboarding quest-log widget on the home page.
//
// Fetches GET /api/progression, renders the current tier's quests + progress,
// and shows a toast when a quest (or a whole tier) newly completes. Dismissible
// (POST /api/progression/dismiss) with a restore affordance. Purely additive:
// no-op on pages without the #questLog element.

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
    const completedNow = new Set();
    (status.tiers || []).forEach(t => {
      (t.quests || []).forEach(q => {
        if (q.status === 'completed') completedNow.add(q.id);
      });
    });

    if (knownCompleted !== null) {
      (status.tiers || []).forEach(t => {
        (t.quests || []).forEach(q => {
          if (q.status === 'completed' && !knownCompleted.has(q.id)) {
            toast(q.title, 'Quest complete');
          }
        });
        if (t.complete && !knownTierComplete[t.tier]) {
          toast(`Tier complete: ${t.name}`, 'Tier complete');
        }
      });
    }

    knownCompleted = completedNow;
    knownTierComplete = {};
    (status.tiers || []).forEach(t => {
      knownTierComplete[t.tier] = !!t.complete;
    });
  }

  function currentTier(status) {
    return (
      (status.tiers || []).find(tier => tier.tier === status.current_tier) ||
      (status.tiers || [])[0] ||
      null
    );
  }

  function completedCount(tier) {
    return (tier?.quests || []).filter(quest => quest.status === 'completed').length;
  }

  function setMeter(bar, completed, total) {
    if (!bar) return;
    const percent = total > 0 ? Math.round((completed / total) * 100) : 0;
    bar.style.width = `${Math.max(0, Math.min(100, percent))}%`;

    const meter = bar.closest('[role="progressbar"]');
    if (meter) meter.setAttribute('aria-valuenow', String(Math.max(0, Math.min(100, percent))));
  }

  function tierInsignia(tier) {
    const value = Number(tier);
    return Number.isFinite(value) && value > 0 ? String(value).padStart(2, '0') : '—';
  }

  function renderRestoreBadge(status, current) {
    const completed = status.all_complete ? status.total_count : completedCount(current);
    const total = status.all_complete ? status.total_count : (current?.quests || []).length;
    const tierLabel = status.all_complete ? 'All tiers complete' : `Tier ${status.current_tier}`;

    const insignia = restoreEl('restore-insignia');
    const tier = restoreEl('restore-tier');
    const progress = restoreEl('restore-progress');
    const meter = restoreEl('restore-meter');
    if (insignia)
      insignia.textContent = status.all_complete ? '✓' : tierInsignia(status.current_tier);
    if (tier) tier.textContent = tierLabel;
    if (progress) progress.textContent = `${completed}/${total}`;
    if (meter) meter.style.width = `${total > 0 ? Math.round((completed / total) * 100) : 0}%`;
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
    const complete = completedCount(current);
    const total = current.quests.length;
    el('progress-count').textContent = `${complete}/${total}`;
    setMeter(el('progress-bar'), complete, total);

    const list = el('quests');
    list.innerHTML = '';
    current.quests.forEach(q => {
      const done = q.status === 'completed';
      const li = document.createElement('li');
      li.className = 'quest-item' + (done ? ' quest-item-done' : '');
      const mark = document.createElement('span');
      mark.className = 'quest-mark';
      mark.textContent = done ? '✓' : '○';
      mark.setAttribute('aria-hidden', 'true');

      // An incomplete quest with an action destination renders as a link so the
      // user can act on it directly; otherwise it's plain text.
      let title;
      if (!done && q.action_url) {
        title = document.createElement('a');
        title.href = q.action_url;
        title.title = q.action_label || q.title;
      } else {
        title = document.createElement('span');
      }
      title.className = 'quest-title';
      title.textContent = q.title;

      li.append(mark, title);
      list.appendChild(li);
    });

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
