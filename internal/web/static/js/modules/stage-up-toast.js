/**
 * Stage-up toast: client-side detection of an agent's evolution stage
 * advancing, so progression is *felt* wherever it happens — the roster page
 * and, critically, the Map view where the user actually lives (PRD: "the
 * stage-up toast is what makes progression felt... make sure toasts fire
 * during normal Map-view task flows, not only while the roster page is
 * open"). Detection is a simple before/after compare against a per-agent
 * last-seen stage cached in localStorage; no server-side push is required.
 *
 * Requires modules/toast.js to be loaded on the page.
 */
(function () {
  'use strict';

  var STORAGE_PREFIX = 'ori.evolution.lastStage.';

  // Mirrors the stage -> slot-cap table in internal/types/agent_extended.go
  // (SkillSlotsForStage), so the toast can say "+1 skill slot" without an
  // extra round trip.
  var STAGE_SLOTS = { spark: 2, infant: 3, learner: 4, expert: 5, sentient: 6 };
  var STAGE_ORDER = ['spark', 'infant', 'learner', 'expert', 'sentient'];

  function cacheKey(agentName) {
    return STORAGE_PREFIX + agentName;
  }

  function getCachedStage(agentName) {
    try {
      return window.localStorage.getItem(cacheKey(agentName));
    } catch (e) {
      return null;
    }
  }

  function setCachedStage(agentName, stage) {
    try {
      window.localStorage.setItem(cacheKey(agentName), stage);
    } catch (e) {
      // Storage unavailable (private mode, quota) — toast detection is
      // best-effort, never load-bearing.
    }
  }

  function stageLabel(stage) {
    return stage ? stage.charAt(0).toUpperCase() + stage.slice(1) : stage;
  }

  // check compares `currentStage` against the cached last-seen stage for
  // agentName. Shows a toast only on genuine forward progress (never on
  // first sight of an agent, and never on a downgrade/no-op). Always updates
  // the cache to the current stage.
  function check(agentName, currentStage) {
    if (!agentName || !currentStage) return;
    var prev = getCachedStage(agentName);
    setCachedStage(agentName, currentStage);
    if (!prev || prev === currentStage) return;

    var prevIdx = STAGE_ORDER.indexOf(prev);
    var curIdx = STAGE_ORDER.indexOf(currentStage);
    if (curIdx <= prevIdx) return;

    var message = agentName + ' reached ' + stageLabel(currentStage) + ' stage';
    var prevSlots = STAGE_SLOTS[prev] || 0;
    var curSlots = STAGE_SLOTS[currentStage] || 0;
    if (curSlots > prevSlots) {
      message += ' — +1 skill slot';
    }

    if (window.Toast && typeof window.Toast.success === 'function') {
      window.Toast.success(message, { title: 'Stage up' });
    }
  }

  // checkByFetch fetches the agent's current evolution and runs check(). For
  // callers (e.g. the Map view's task-completed handler) that don't already
  // have the evolution payload in hand.
  function checkByFetch(agentName) {
    if (!agentName) return;
    fetch('/api/agents/' + encodeURIComponent(agentName) + '/evolution')
      .then(function (r) {
        if (!r.ok) throw new Error('evolution ' + r.status);
        return r.json();
      })
      .then(function (data) {
        var stage = data && data.evolution && data.evolution.stage;
        if (stage) check(agentName, stage);
      })
      .catch(function (err) {
        console.error('[stage-up-toast] fetch failed', err);
      });
  }

  window.StageUpToast = { check: check, checkByFetch: checkByFetch };
})();
