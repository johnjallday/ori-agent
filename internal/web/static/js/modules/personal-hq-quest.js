/*
 * personal-hq-quest.js — Ori's deterministic Personal HQ walkthrough.
 *
 * Hiring creates a personal assistant but no home base. This controller walks
 * the user from Home's Map to the existing Build My HQ form, and does nothing
 * else.
 *
 * What it is:
 *   - Deterministic. Every string here is host copy written in this file. No
 *     request reaches /api/ori-guide, no model is consulted, and the whole
 *     walkthrough works on an install with no provider configured.
 *   - Focus-only. It may open Map view, explain, and ask Ori's panel to
 *     highlight one registered control. The user selects the reserved site,
 *     opens Build My HQ, edits the form, and confirms. There is no code path
 *     here that clicks, selects, submits, or mutates anything.
 *   - Observational. It listens to events the Map and the Personal HQ
 *     controller already emit. Delete this file and both keep working exactly
 *     as they do now.
 *
 * The hired assistant is the *subject* of the quest, never its driver: its name
 * appears in copy and nothing more.
 */
(function () {
  'use strict';

  var QUEST_ID = 'build-hq';
  var QUEST_PARAM = 'build-hq';
  var CHOICE_DEFER = 'defer';

  // Steps are presentation state only. The server owns whether the quest is
  // complete (a real designation) or deferred (an explicit skip).
  var STEP_SELECT_SITE = 1;
  var STEP_OPEN_BUILD = 2;
  var STEP_CONFIRM = 3;
  var TOTAL_STEPS = 3;

  var state = {
    active: false,
    step: 0,
    assistantName: '',
    // Set once the walkthrough has forced Map view for this visit, so a Map
    // remount does not keep yanking the view back under the user.
    forcedMapView: false,
    bound: false
  };

  function guide() {
    return typeof window !== 'undefined' ? window.OriGuide : null;
  }

  function cockpit() {
    return typeof window !== 'undefined' ? window.OriHomeCockpit : null;
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
   * All four conditions are required, and all four come from the server. The
   * URL parameter is a request, not an authorization: a hand-typed
   * ?quest=build-hq on an active relationship must do nothing.
   */
  async function eligible() {
    var onboarding;
    var assistant;
    var hq;
    try {
      onboarding = await fetchJSON('/api/onboarding/status');
      assistant = await fetchJSON('/api/personal-assistant');
      hq = await fetchJSON('/api/personal-hq/status');
    } catch (_) {
      return null;
    }
    // Ordinary onboarding must be finished; the hire modal owns the screen until
    // then and two walkthroughs at once would fight over focus.
    if (onboarding && onboarding.needs_onboarding === true) return null;

    var relationship = (assistant && assistant.personal_assistant) || null;
    if (!relationship) return null;
    if (relationship.state !== 'needs_hq' && relationship.state !== 'provisioning_hq') return null;

    // Known-invalid, not merely unknown: a status we could not read is not a
    // licence to tell the user their HQ is missing.
    var status = (hq && hq.status) || null;
    if (!status || status.valid === true) return null;

    return { assistantName: String(relationship.display_name || '').trim() };
  }

  /* ---- copy ---------------------------------------------------------------- */

  // The approved framing. The name is user-controlled data: it is passed to the
  // guide as text and the guide escapes it, so it can never become markup.
  function subject(name) {
    return name || 'Your assistant';
  }

  function stepCopy(step, name) {
    var who = subject(name);
    if (step === STEP_SELECT_SITE) {
      return {
        answer:
          who +
          ' is hired. Let’s give ' +
          who +
          ' a home base. On the Map, select the highlighted Personal HQ site.',
        note: 'Nothing is created by looking. You choose what happens next.'
      };
    }
    if (step === STEP_OPEN_BUILD) {
      return {
        answer: 'Now open Build My HQ to set up ' + who + '’s home base.',
        note: 'This opens a form you can review and change before anything is created.'
      };
    }
    return {
      answer:
        'Review the name and the daily rhythm, then confirm to build ' + who + '’s Personal HQ.',
      note: 'Nothing is created until you confirm. You can close this form and come back to it.'
    };
  }

  function coachmarkFor(step) {
    if (step === STEP_SELECT_SITE) return 'personal_hq_site';
    if (step === STEP_OPEN_BUILD) return 'personal_hq_build';
    // The final step belongs to the existing form; marking anything inside it
    // would compete with the form's own focus management.
    return '';
  }

  function choicesFor(step) {
    // Do this later stays available until the form itself is open, at which
    // point the form's own Cancel is the honest way out.
    if (step === STEP_CONFIRM) return [];
    return [{ id: CHOICE_DEFER, label: 'Do this later' }];
  }

  /* ---- presentation -------------------------------------------------------- */

  function present(step) {
    var g = guide();
    if (!g || typeof g.presentQuestStep !== 'function') return false;
    state.step = step;
    var copy = stepCopy(step, state.assistantName);
    var result = g.presentQuestStep({
      quest: QUEST_ID,
      index: step,
      total: TOTAL_STEPS,
      answer: copy.answer,
      note: copy.note,
      coachmark: coachmarkFor(step),
      choices: choicesFor(step)
    });
    return !!(result && result.rendered);
  }

  // Re-marking is how the walkthrough survives a Map remount or a context
  // dialog re-render: the element the previous mark pointed at is gone, so the
  // current step is presented again rather than left pointing at nothing.
  function refreshCurrentStep() {
    if (!state.active || !state.step) return;
    present(state.step);
  }

  function advanceTo(step) {
    if (!state.active || step <= state.step) return;
    present(step);
  }

  /* ---- lifecycle ----------------------------------------------------------- */

  function forceMapView() {
    var host = cockpit();
    if (!host || typeof host.setView !== 'function' || state.forcedMapView) return;
    state.forcedMapView = true;
    // pushUrl:false — this one guided visit must not rewrite the view the user
    // will return to, and must not add a history entry.
    try {
      host.setView('map', { pushUrl: false });
    } catch (_) {
      /* an unavailable cockpit is not a reason to abandon the explanation */
    }
  }

  async function start(options) {
    var context = options && options.context ? options.context : await eligible();
    if (!context) return false;

    state.active = true;
    state.step = 0;
    state.assistantName = context.assistantName;
    forceMapView();

    var g = guide();
    // Opening the walkthrough may open Ori's panel — that is presentation, and
    // closing it later pauses the presentation without skipping the quest.
    if (g && typeof g.open === 'function' && typeof g.isOpen === 'function' && !g.isOpen()) {
      try {
        // skipGreeting: present() below renders the quest step immediately
        // after. Without this, open()'s own async default-greeting fetch lands
        // after present() and silently overwrites the quest step.
        g.open(null, { skipGreeting: true });
      } catch (_) {
        /* the step still renders into the panel body */
      }
    }
    present(STEP_SELECT_SITE);
    clearQuestParam();
    return true;
  }

  // stop ends the presentation only. It never reports the quest complete and
  // never skips it: abandoning the walkthrough must leave a resumable
  // server-owned state, not an assumed outcome.
  function stop() {
    state.active = false;
    state.step = 0;
    state.forcedMapView = false;
    var g = guide();
    if (g && typeof g.clearQuestStep === 'function') g.clearQuestStep();
  }

  // defer is the explicit Do this later. It invokes the SAME skip path the
  // Map's own "Not now" uses, so there is one recorded deferral rather than a
  // second quest receipt.
  function defer() {
    if (!state.active) return;
    stop();
    clearQuestParam();
    try {
      window.dispatchEvent(
        new CustomEvent('ori:personal-hq-action', { detail: { action: 'skip' } })
      );
    } catch (_) {
      /* the server-side quest stays open, which is the safe direction */
    }
  }

  /* ---- wiring -------------------------------------------------------------- */

  function bind() {
    if (state.bound || typeof window === 'undefined') return;
    state.bound = true;

    // A real user selection of the reserved site advances step 1 -> 2. The
    // walkthrough never selects the site itself, so this only ever fires
    // because the user acted.
    window.addEventListener('ori:hq-site-selected', function (event) {
      if (!state.active) return;
      var opened = !!(event && event.detail && event.detail.dialogOpened);
      if (!opened) {
        // The site is selected but its dialog is not on screen, so the Build
        // action does not exist to mark yet.
        refreshCurrentStep();
        return;
      }
      advanceTo(STEP_OPEN_BUILD);
    });

    window.addEventListener('ori:hq-quest-signal', function (event) {
      if (!state.active) return;
      var stage = event && event.detail && event.detail.stage;
      if (stage === 'build-form-opened') {
        advanceTo(STEP_CONFIRM);
        return;
      }
      if (stage === 'setup-succeeded') {
        // Group 4 owns the completion receipt; the walkthrough's job is done.
        stop();
        return;
      }
      // The Map re-mounts when HQ status arrives, which destroys the marked
      // element. Present the current step again rather than keep a mark on a
      // node that is no longer in the document. A now-valid HQ ends the quest.
      if (stage === 'hq-status-changed') {
        if (event.detail.valid === true) {
          stop();
          return;
        }
        refreshCurrentStep();
      }
    });

    window.addEventListener('ori:workspaces-changed', refreshCurrentStep);

    document.addEventListener('ori-guide:quest-choice', function (event) {
      var detail = (event && event.detail) || {};
      if (detail.quest !== QUEST_ID) return;
      if (detail.choice === CHOICE_DEFER) defer();
    });

    // Leaving Home, or a Back press, must not leave a coachmark behind on a page
    // that no longer owns the control it pointed at.
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
    // resume is the Home mission's entry point: an explicit user action, held to
    // exactly the same server-side eligibility checks as the query parameter.
    resume: function () {
      bind();
      return start();
    },
    stop: stop,
    isActive: function () {
      return state.active;
    },
    // Test seams.
    _state: state,
    _eligible: eligible,
    _stepCopy: stepCopy,
    _coachmarkFor: coachmarkFor,
    _choicesFor: choicesFor,
    _present: present,
    _defer: defer,
    _init: init,
    QUEST_ID: QUEST_ID,
    STEP_SELECT_SITE: STEP_SELECT_SITE,
    STEP_OPEN_BUILD: STEP_OPEN_BUILD,
    STEP_CONFIRM: STEP_CONFIRM
  };

  if (typeof window !== 'undefined') {
    window.OriPersonalHQQuest = api;
    if (typeof document !== 'undefined') {
      if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', function () {
          void init();
        });
      } else {
        void init();
      }
    }
  }
})();
