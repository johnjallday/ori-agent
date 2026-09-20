/*
 * tidy-downloads-quest.js — Ori's deterministic Mission 02 walkthrough,
 * "Tidy your Downloads" (tasks/prd-starter-missions.md FR28-FR35).
 *
 * The quest card's Start opens this on `?quest=tidy-downloads`. It opens the
 * unified workspace creator with the File Janitor blueprint preselected, and
 * Ori's pointing hand guides the user to Create. The blueprint's own setup
 * wizard takes over on the new workspace's page.
 *
 * What it is, like personal-hq-quest.js:
 *   - Deterministic. Every string is host copy written here. No request reaches
 *     /api/ori-guide and no model is consulted.
 *   - Focus-only. It opens the creator through the same call the deep link
 *     uses and asks Ori's panel to mark one registered control. It never
 *     selects a folder, approves automation, submits the creator, or calls the
 *     File Janitor API. It makes no write request of any kind.
 *   - Observational. It listens to the creator's step events and its context
 *     callback. Delete this file and the creator keeps working exactly as now.
 *
 * No panel choice is offered. Every step is presented while the creator dialog
 * owns the screen, and Ori's panel sits beneath the dialog's backdrop, so a
 * "Do this later" there could not be pressed (at narrow widths the panel is
 * hidden entirely). Lifting the panel above the dialog would cover Create, the
 * control the walkthrough points at. The honest exits are the ones the user can
 * reach: the creator's own Cancel pauses the walkthrough, and the Quests card's
 * "Do this later" defers the mission. Personal HQ's walkthrough withdraws its
 * choice for the same reason once its form is open.
 *
 * The server owns the quest. Mission 02 completes only when the File Janitor
 * setup wizard reaches ready.
 */
(function () {
  'use strict';

  var QUEST_ID = 'tidy-downloads';
  var QUEST_PARAM = 'tidy-downloads';
  var QUEST_URL = '/?quest=tidy-downloads';
  var MISSION_ID = 'pa-tidy-downloads';
  var BLUEPRINT_ID = 'file-janitor';
  var ENTRY_POINT = 'tidy_downloads_quest';

  var STEP_CREATE = 1;
  var STEP_OPENING = 2;
  var TOTAL_STEPS = 2;

  var state = {
    active: false,
    step: 0,
    // Set when the creator reports a successful create, so the dialog closing
    // afterwards is not mistaken for the user walking away.
    created: false,
    // Whether the creator last reported its final (Create) step.
    creatorFinal: false,
    bound: false,
    modalBound: false
  };

  function guide() {
    return typeof window !== 'undefined' ? window.OriGuide : null;
  }

  function creator() {
    return typeof window !== 'undefined' ? window.sessionManager : null;
  }

  function questRequestedInURL() {
    if (typeof window === 'undefined' || !window.location) return false;
    try {
      return new URLSearchParams(window.location.search).get('quest') === QUEST_PARAM;
    } catch (_) {
      return false;
    }
  }

  // Drops ?quest= from the URL without adding a history entry, so a reload or a
  // Back press does not silently restart the walkthrough.
  function clearQuestParam() {
    if (typeof window === 'undefined' || !window.history || !window.history.replaceState) return;
    try {
      var url = new URL(window.location.href);
      if (!url.searchParams.has('quest')) return;
      url.searchParams.delete('quest');
      window.history.replaceState(
        window.history.state,
        '',
        url.pathname + (url.search || '') + (url.hash || '')
      );
    } catch (_) {
      /* a cosmetic URL tidy must never break the walkthrough */
    }
  }

  async function fetchJSON(url) {
    var res = await fetch(url, { headers: { Accept: 'application/json' } });
    if (!res.ok) throw new Error(String(res.status));
    return res.json();
  }

  /*
   * eligible answers one question: may the walkthrough run right now?
   *
   * Both answers come from the server. The URL parameter is a request, not an
   * authorization: a hand-typed ?quest=tidy-downloads does nothing unless the
   * mission is still open and its resolved action is this walkthrough, which
   * means no File Janitor workspace exists yet (with one, the card says Finish
   * setup and points at that workspace instead).
   *
   * A deferred mission is still eligible. Its Resume link in the Quests list
   * is this same URL, and an explicit Resume must work.
   *
   * No assistant or HQ is required: File Janitor depends on neither, and the
   * card offers Mission 02 even to a user who deferred Build My HQ.
   */
  async function eligible() {
    var onboarding;
    var progression;
    try {
      onboarding = await fetchJSON('/api/onboarding/status');
      progression = await fetchJSON('/api/progression');
    } catch (_) {
      return false;
    }
    // The onboarding modal owns the screen until it finishes.
    if (!onboarding || onboarding.needs_onboarding === true) return false;

    var missions =
      (progression && Array.isArray(progression.missions) && progression.missions) || [];
    var mission = null;
    for (var i = 0; i < missions.length; i++) {
      if (missions[i] && missions[i].id === MISSION_ID) mission = missions[i];
    }
    if (!mission || mission.status === 'completed') return false;
    return mission.action_url === QUEST_URL;
  }

  /* ---- copy ---------------------------------------------------------------- */

  // Ori, never the assistant: the File Curator in the new workspace does the
  // tidying, not the hired assistant.
  function stepCopy(step) {
    if (step === STEP_CREATE) {
      return {
        answer:
          'Let’s give Ori one folder to tidy. File Janitor is already selected. Review the name and team, then Create.',
        note: 'Nothing runs until you approve it in the next screen.'
      };
    }
    return {
      answer: 'Created. Opening its setup so you can choose the folder.',
      note: ''
    };
  }

  // The Create button exists only on the creator's last step. Asking for the
  // mark earlier would make the guide say it cannot point at the control, so
  // the copy alone guides until the creator reports its final step.
  function coachmarkFor(step, creatorOnFinalStep) {
    return step === STEP_CREATE && creatorOnFinalStep ? 'create_workspace_submit' : '';
  }

  /* ---- presentation -------------------------------------------------------- */

  function present(step) {
    var g = guide();
    if (!g || typeof g.presentQuestStep !== 'function') return false;
    state.step = step;
    var copy = stepCopy(step);
    var result = g.presentQuestStep({
      quest: QUEST_ID,
      index: step,
      total: TOTAL_STEPS,
      answer: copy.answer,
      note: copy.note,
      coachmark: coachmarkFor(step, state.creatorFinal),
      // See the header: the creator owns the screen, so no choice is offered.
      choices: []
    });
    return !!(result && result.rendered);
  }

  /* ---- lifecycle ----------------------------------------------------------- */

  // onCreated is the creator's own success callback for this context. The
  // workspace exists; the page's normal navigation to it follows, and the setup
  // wizard opens there. The final step stays on screen until that navigation
  // replaces the page, so the walkthrough ends without clearing it.
  function onCreated() {
    if (!state.active) return;
    state.created = true;
    present(STEP_OPENING);
    state.active = false;
    state.step = 0;
  }

  function bindModal() {
    if (state.modalBound || typeof document === 'undefined' || !document.getElementById) return;
    var element = document.getElementById('addFolderModal');
    if (!element || typeof element.addEventListener !== 'function') return;
    state.modalBound = true;
    element.addEventListener('hidden.bs.modal', function () {
      if (!state.active || state.created) return;
      // Agent setup and Map placement hide the dialog mid-draft; that is
      // navigation inside the same create, not the user walking away.
      var data = element.dataset || {};
      if (data.suspendedForAgentSetup || data.suspendedForMapPlacement) return;
      // Closing without creating pauses the presentation. The mission stays
      // open on the card.
      stop();
    });
  }

  async function start() {
    // The Personal Assistant card is now the canonical recommendation and run
    // surface. It performs a read-only proposal first and retains the manual
    // creator only as an explicit action inside that proposal. Keep the legacy
    // walkthrough as a bounded fallback for builds where the shared card is not
    // present at all; never fall back after the coordinator handled the entry.
    var assistantSetup = typeof window !== 'undefined' ? window.AssistantLedSetup : null;
    if (assistantSetup && typeof assistantSetup.startFromMission === 'function') {
      clearQuestParam();
      return !!(await assistantSetup.startFromMission());
    }
    if (!(await eligible())) return false;
    var host = creator();
    if (!host || typeof host.showAddWorkspaceModal !== 'function') return false;

    state.active = true;
    state.created = false;
    state.creatorFinal = false;
    state.step = 0;
    clearQuestParam();
    bindModal();

    var g = guide();
    if (g && typeof g.open === 'function' && typeof g.isOpen === 'function' && !g.isOpen()) {
      try {
        // skipGreeting: the step below renders immediately, and the default
        // greeting's async fetch would otherwise overwrite it.
        g.open(null, { skipGreeting: true });
      } catch (_) {
        /* the step still renders into the panel body */
      }
    }

    try {
      host.showAddWorkspaceModal({
        blueprint: BLUEPRINT_ID,
        entryPoint: ENTRY_POINT,
        onCreated: onCreated
      });
    } catch (_) {
      stop();
      return false;
    }
    present(STEP_CREATE);
    return true;
  }

  // stop ends the presentation only. It never completes or skips the mission.
  function stop() {
    state.active = false;
    state.step = 0;
    var g = guide();
    if (g && typeof g.clearQuestStep === 'function') g.clearQuestStep();
  }

  /* ---- wiring -------------------------------------------------------------- */

  function bind() {
    if (state.bound || typeof window === 'undefined') return;
    state.bound = true;

    // The creator announces each step. Re-presenting lets the mark land on
    // Create once the user reaches the last step. Another caller's creator
    // (a different entry point) is not this walkthrough's business.
    window.addEventListener('ori:workspace-creator-step', function (event) {
      if (!state.active || state.step !== STEP_CREATE) return;
      var detail = (event && event.detail) || {};
      if (detail.entryPoint !== ENTRY_POINT) return;
      state.creatorFinal = detail.final === true;
      present(STEP_CREATE);
    });

    // Leaving Home, or a Back press, must not leave a mark behind.
    window.addEventListener('popstate', function () {
      if (state.active) stop();
    });
    window.addEventListener('pagehide', function () {
      if (state.active) stop();
    });
  }

  async function init() {
    bind();
    if (!questRequestedInURL()) return;
    await start();
  }

  var api = {
    stop: stop,
    isActive: function () {
      return state.active;
    },
    // Test seams.
    _state: state,
    _eligible: eligible,
    _stepCopy: stepCopy,
    _coachmarkFor: coachmarkFor,
    _init: init,
    QUEST_ID: QUEST_ID,
    MISSION_ID: MISSION_ID,
    STEP_CREATE: STEP_CREATE,
    STEP_OPENING: STEP_OPENING
  };

  if (typeof window !== 'undefined') {
    window.OriTidyDownloadsQuest = api;
    if (typeof document !== 'undefined') {
      // The creator binds its dialog handlers on DOMContentLoaded, which fires
      // after this module evaluates, and opening the dialog earlier would skip
      // its blueprint preselection. So init waits for DOMContentLoaded, with
      // `load` as a fallback in case that event has already passed.
      var started = false;
      var once = function () {
        if (started) return;
        started = true;
        void init();
      };
      if (document.readyState === 'complete') {
        once();
      } else {
        document.addEventListener('DOMContentLoaded', once);
        window.addEventListener('load', once);
      }
    }
  }
})();
