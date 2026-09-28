/*
 * meet-assistant-quest.js — Ori's deterministic walkthrough for Mission 01,
 * "Meet your assistant".
 *
 * The user hires their personal assistant by creating it themselves: the Agents
 * page's New Agent opens the same Create Agent modal as always, in a personal-
 * assistant preset while no assistant is hired (agents-roster.js). The
 * walkthrough's first step is on Home, pointing at the Agents nav entry
 * (meet-assistant-home-prompt.js); this controller carries on from there,
 * pointing at each control in turn: New Agent, the name, the face, the focus,
 * Hire.
 *
 * Two presentations. When the user arrived through the mission (the Home step,
 * or ?quest=meet-assistant), Ori's own layer carries it (ori-spotlight.js): New
 * Agent is the one lit control on a dimmed page, as Agents was on Home. A plain
 * visit to the Agents page points at New Agent from Ori's docked panel instead.
 * Either way, once the modal is up Ori's callout sits beside it, pointing at
 * each field, without dimming anything, because there the user works in
 * several fields at once, and Ori's panel sits under every modal's backdrop. A
 * screen too narrow to fit the callout beside the dialog keeps to the panel.
 *
 * What it is:
 *   - Deterministic. Every string here is host copy written in this file. No
 *     request reaches /api/ori-guide, no model is consulted, and the whole
 *     walkthrough works on an install with no provider configured. The one
 *     request it makes is GET /api/personal-assistant.
 *   - Focus-only. It may open Ori's panel, present a step, and ask for one
 *     registered coachmark. The user presses New Agent, types, ticks, and
 *     hires. There is no code path here that clicks, fills, selects, submits,
 *     or mutates anything.
 *   - Observational. It follows the roster's own "create panel opened" signal
 *     and the form's ordinary change events. Delete this file and the preset
 *     still hires exactly as it does now, which is also how the mission is
 *     completed with Ori's panel closed, or at a width that hides it.
 *
 * Steps only ever move forward. The server owns whether the mission is done:
 * it completes from the durable hire, never from anything this file sees.
 */
(function () {
  'use strict';

  var QUEST_ID = 'meet-assistant';
  var QUEST_PARAM = 'meet-assistant';
  // The Create Agent modal's mode while it holds the preset (agents-roster.js).
  var HIRE_MODE = 'assistant-hire';

  // Step 1 is on Home (meet-assistant-home-prompt.js): Ori points at the Agents
  // nav entry and the user clicks it. This page carries on from Step 2.
  var STEP_NEW_AGENT = 2;
  var STEP_NAME = 3;
  var STEP_FACE = 4;
  var STEP_FOCUS = 5;
  var STEP_HIRE = 6;
  var TOTAL_STEPS = 6;

  var CHOICE_KEEP_NAME = 'keep-name';
  var CHOICE_KEEP_FACE = 'keep-face';
  var CHOICE_DONE_CHOOSING = 'done-choosing';

  // Set by the Home step when the user presses the Agents nav entry
  // (MEET_ASSISTANT_GUIDED_FLAG in personal-assistant-hire.js; this script
  // cannot import it, and a test pins the two together).
  var GUIDED_FLAG = 'ori:meet-assistant-guided';

  var state = {
    active: false,
    step: 0,
    // The user closed Ori's panel. The walkthrough keeps following the form so
    // reopening resumes at the right step, but presents nothing meanwhile.
    paused: false,
    // The user chose the ordinary form instead. The mission stays open; the
    // presentation waits until the preset comes back.
    detoured: false,
    // The steps are presented in Ori's layer rather than in Ori's panel.
    spotlight: false,
    // The layer found no room beside the dialog once: the rest of the mission
    // stays in Ori's panel.
    noRoom: false,
    // The step Ori's layer is showing, or 0.
    layerStep: 0,
    bound: false
  };

  function questRequestedInURL() {
    if (typeof window === 'undefined' || !window.location) return false;
    try {
      return new URLSearchParams(window.location.search).get('quest') === QUEST_PARAM;
    } catch (_) {
      return false;
    }
  }

  // Read once, when the script runs: the roster rewrites the query string as
  // soon as its collection loads, and that drops the parameter.
  var requestedAtLoad = questRequestedInURL();

  // Read and cleared once: the flag is for this arrival only.
  function takeGuidedFlag() {
    try {
      var storage = typeof window !== 'undefined' ? window.sessionStorage : null;
      if (!storage) return false;
      var value = storage.getItem(GUIDED_FLAG);
      if (value !== null) storage.removeItem(GUIDED_FLAG);
      return value === '1';
    } catch (_) {
      return false;
    }
  }
  var guidedAtLoad = takeGuidedFlag();

  function guide() {
    return typeof window !== 'undefined' ? window.OriGuide : null;
  }

  function spotlightLayer() {
    var layer = typeof window !== 'undefined' ? window.OriSpotlight : null;
    return layer && typeof layer.showSpotlight === 'function' ? layer : null;
  }

  // Drops ?quest= from the URL without adding a history entry, so a reload or a
  // Back press does not replay the arrival.
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

  /*
   * eligible answers one question: may the walkthrough run right now? Only
   * while there is no assistant to keep. The URL parameter is a request, not an
   * authorization: a hand-typed ?quest=meet-assistant on a hired install must
   * do nothing. A relationship that is hired, or needs a repair, has no
   * walkthrough; the roster opens its one-button view on arrival instead.
   */
  async function eligible() {
    var payload;
    try {
      var res = await fetch('/api/personal-assistant', { headers: { Accept: 'application/json' } });
      if (!res.ok) return false;
      payload = await res.json();
    } catch (_) {
      return false;
    }
    var relationship = (payload && payload.personal_assistant) || null;
    var current = String((relationship && relationship.state) || '').trim();
    return current === 'needs_hire' || current === 'hiring';
  }

  /* ---- copy ---------------------------------------------------------------- */

  // Ori speaks as the guide, never as the assistant, and names nobody: the
  // assistant has no name of its own until the hire.
  function stepCopy(step) {
    if (step === STEP_NEW_AGENT) {
      return {
        answer: 'Press New Agent. Your assistant starts as an agent like any other.',
        note: 'Nothing is created until you press Hire.'
      };
    }
    if (step === STEP_NAME) {
      return { answer: 'Give them a name. You can change it later.', note: '' };
    }
    if (step === STEP_FACE) {
      return { answer: 'A face is already suggested. Keep it, or pick another.', note: '' };
    }
    if (step === STEP_FOCUS) {
      return { answer: 'What should they help with this week? Tick what fits.', note: '' };
    }
    return {
      answer:
        'Hire them. This creates one assistant and nothing else: no workspace, no permissions, no accounts.',
      note: ''
    };
  }

  // The same steps, split into the callout's title and line (ori-spotlight.js).
  function calloutCopy(step) {
    if (step === STEP_NEW_AGENT) {
      return {
        title: 'Press New Agent',
        body: 'Your assistant starts as an agent like any other.',
        note: 'Nothing is created until you press Hire.'
      };
    }
    if (step === STEP_NAME) return { title: 'Give them a name', body: 'You can change it later.' };
    if (step === STEP_FACE) {
      return { title: 'Pick a face', body: 'One is already suggested. Keep it, or pick another.' };
    }
    if (step === STEP_FOCUS) {
      return {
        title: 'Choose their focus',
        body: 'What should they help with this week? Tick what fits.'
      };
    }
    return {
      title: 'Hire them',
      body: 'This creates one assistant and nothing else: no workspace, no permissions, no accounts.'
    };
  }

  function coachmarkFor(step) {
    if (step === STEP_NEW_AGENT) return 'new_agent';
    if (step === STEP_NAME) return 'assistant_name';
    if (step === STEP_FACE) return 'assistant_face';
    if (step === STEP_FOCUS) return 'assistant_focus';
    return 'assistant_hire';
  }

  // Every choice is optional: each one only says "I'm happy with this", which
  // the form itself can also say. The name is prefilled, so keeping it has to
  // be a way forward too.
  function choicesFor(step) {
    if (step === STEP_NAME) return [{ id: CHOICE_KEEP_NAME, label: 'Keep this name' }];
    if (step === STEP_FACE) return [{ id: CHOICE_KEEP_FACE, label: 'Keep this face' }];
    if (step === STEP_FOCUS) return [{ id: CHOICE_DONE_CHOOSING, label: 'Done choosing' }];
    return [];
  }

  /* ---- presentation -------------------------------------------------------- */

  function openPanel() {
    var g = guide();
    if (g && typeof g.open === 'function' && typeof g.isOpen === 'function' && !g.isOpen()) {
      try {
        // skipGreeting: the step renders immediately after. Without it,
        // open()'s own async greeting lands after and overwrites the step.
        g.open(null, { skipGreeting: true });
      } catch (_) {
        /* the step still renders into the panel body */
      }
    }
  }

  function closeLayer() {
    if (!state.layerStep) return;
    state.layerStep = 0;
    var layer = spotlightLayer();
    if (layer && typeof layer.close === 'function') layer.close();
  }

  // Not now on Ori's callout: the same as closing Ori's panel. The form keeps
  // working and the walkthrough keeps count; opening Ori resumes in the panel.
  function onLayerLater() {
    state.layerStep = 0;
    state.spotlight = false;
    state.paused = true;
  }

  // New Agent lit on a dimmed page (a mission the user arrived through), then
  // Ori's callout beside the modal, pointing at each field.
  function presentInLayer(step, focus) {
    var layer = spotlightLayer();
    if (!layer) return false;
    // Already up: the layer follows the control through re-renders itself.
    if (state.layerStep === step) return true;
    state.layerStep = step;
    var copy = calloutCopy(step);
    var opts = {
      coachmark: coachmarkFor(step),
      index: step,
      total: TOTAL_STEPS,
      title: copy.title,
      body: copy.body,
      note: copy.note || '',
      laterLabel: 'Not now',
      onLater: onLayerLater,
      focus: focus !== false
    };
    var shown;
    if (step === STEP_NEW_AGENT) {
      shown = layer.showSpotlight(opts);
    } else {
      opts.anchor = '#addAgentModal .modal-dialog';
      opts.choices = choicesFor(step);
      opts.onChoice = function (id) {
        onQuestChoice({ detail: { quest: QUEST_ID, choice: id } });
      };
      shown = layer.showCallout(opts);
    }
    shown.then(function (ok) {
      if (ok || !state.active || state.layerStep !== step) return;
      // Nothing to point at, or no room beside the dialog (a phone): say it in
      // Ori's panel instead. No room beside the dialog is for the rest of the
      // mission; the modal's next opening asks the layer again otherwise.
      state.layerStep = 0;
      state.spotlight = false;
      if (step !== STEP_NEW_AGENT) state.noRoom = true;
      layer.close();
      openPanel();
      present(step, focus);
    });
    return true;
  }

  // present shows a step. focus: false keeps focus where the user is working:
  // a step the form itself advanced to must not pull them out of the form.
  function present(step, focus) {
    state.step = step;
    if (state.paused || state.detoured) return false;
    if (state.spotlight && presentInLayer(step, focus)) return true;
    var g = guide();
    if (!g || typeof g.presentQuestStep !== 'function') return false;
    var copy = stepCopy(step);
    var result = g.presentQuestStep({
      quest: QUEST_ID,
      index: step,
      total: TOTAL_STEPS,
      answer: copy.answer,
      note: copy.note,
      coachmark: coachmarkFor(step),
      choices: choicesFor(step),
      focus: focus !== false
    });
    return !!(result && result.rendered);
  }

  // Re-marking is how the walkthrough survives the create panel re-rendering:
  // the element the previous mark pointed at is gone, so the current step is
  // presented again rather than left pointing at nothing.
  function refreshCurrentStep() {
    if (!state.active || !state.step) return;
    present(state.step, false);
  }

  // Steps never go backwards: a stale signal for an earlier step is ignored.
  function advanceTo(step, focus) {
    if (!state.active || step <= state.step) return;
    present(step, focus);
  }

  // The Create Agent modal in the roster's personal-assistant mode, with the
  // hire form in it (not a repair view).
  function presetOpen() {
    if (typeof document === 'undefined' || typeof document.getElementById !== 'function') {
      return false;
    }
    var modal = document.getElementById('addAgentModal');
    return !!(
      modal &&
      modal.dataset &&
      modal.dataset.agentCreateMode === HIRE_MODE &&
      document.getElementById('cr-focus-group')
    );
  }

  /* ---- lifecycle ----------------------------------------------------------- */

  async function start() {
    if (state.active) return true;
    if (!(await eligible())) return false;

    state.active = true;
    state.step = 0;
    state.paused = false;
    state.detoured = false;
    state.noRoom = false;
    // Arrived through the mission: the one-click step stays a spotlight.
    state.spotlight = (requestedAtLoad || guidedAtLoad) && !!spotlightLayer();

    // A New Agent press that beat this start already opened the preset, whose
    // modal backdrop Ori's panel would sit under.
    var first = presetOpen() ? STEP_NAME : STEP_NEW_AGENT;
    if (first === STEP_NAME && spotlightLayer()) state.spotlight = true;
    if (!state.spotlight) openPanel();
    if (first === STEP_NAME) present(STEP_NAME, false);
    else present(STEP_NEW_AGENT);
    clearQuestParam();
    return true;
  }

  // stop ends the presentation only. It never reports the mission complete and
  // never skips it: the server completes it from the durable hire.
  function stop() {
    state.active = false;
    state.step = 0;
    state.paused = false;
    state.detoured = false;
    closeLayer();
    state.spotlight = false;
    var g = guide();
    if (g && typeof g.clearQuestStep === 'function') g.clearQuestStep();
  }

  /* ---- signals ------------------------------------------------------------- */

  function onCreateOpened(event) {
    if (!state.active) return;
    var mode = event && event.detail && event.detail.mode;
    if (mode !== 'assistant') {
      // "Create a different kind of agent instead": stop pointing at a form
      // that is not on screen, without skipping or completing anything.
      state.detoured = true;
      closeLayer();
      var g = guide();
      if (g && typeof g.clearQuestStep === 'function') g.clearQuestStep();
      return;
    }
    state.detoured = false;
    // New Agent's dimmed page would lie over the modal, and block it, for as
    // long as the callout takes to find room beside the dialog, or not.
    if (state.layerStep === STEP_NEW_AGENT) closeLayer();
    // The preset is a modal, and Ori's panel sits under its backdrop: the
    // form's steps are said beside the dialog, however the user arrived.
    if (!state.noRoom && spotlightLayer()) state.spotlight = true;
    if (state.step < STEP_NAME) {
      present(STEP_NAME);
      return;
    }
    refreshCurrentStep();
  }

  function insidePreset(target) {
    return (
      !!(target && typeof target.closest === 'function' && target.closest('#addAgentForm')) &&
      presetOpen()
    );
  }

  // The form's own events. `change` rather than `input` for the text fields:
  // it fires once the user is done with the field, so a step never re-marks
  // (and scrolls) the panel under someone mid-word.
  function onFormChange(event) {
    if (!state.active || state.detoured) return;
    var target = event && event.target;
    if (!insidePreset(target)) return;

    if (target.id === 'cr-name') {
      if (state.step === STEP_NAME && String(target.value || '').trim()) {
        advanceTo(STEP_FACE, false);
      }
      return;
    }

    var inFocus = typeof target.closest === 'function' && target.closest('#cr-focus-group');
    var inMandate = target.id === 'cr-mandate';
    if (!inFocus && !inMandate) return;
    if (inMandate && !String(target.value || '').trim()) return;
    // Working on the focus or the mandate moves the walkthrough on one step at
    // a time, from the name onwards, so a user who never answers Ori still
    // arrives at Hire.
    if (state.step >= STEP_NAME && state.step < STEP_HIRE) {
      advanceTo(state.step + 1, false);
    }
  }

  function onQuestChoice(event) {
    var detail = (event && event.detail) || {};
    if (detail.quest !== QUEST_ID || !state.active) return;
    if (detail.choice === CHOICE_KEEP_NAME && state.step === STEP_NAME) advanceTo(STEP_FACE);
    else if (detail.choice === CHOICE_KEEP_FACE && state.step === STEP_FACE) advanceTo(STEP_FOCUS);
    else if (detail.choice === CHOICE_DONE_CHOOSING && state.step === STEP_FOCUS) {
      advanceTo(STEP_HIRE);
    }
  }

  /* ---- wiring -------------------------------------------------------------- */

  function bind() {
    if (state.bound || typeof window === 'undefined') return;
    state.bound = true;

    window.addEventListener('ori:agent-create-opened', onCreateOpened);

    if (typeof document !== 'undefined') {
      // Capture: the roster stops nothing, but a capturing listener hears the
      // form even if a future handler on the way does.
      document.addEventListener('change', onFormChange, true);
      document.addEventListener('ori-guide:quest-choice', onQuestChoice);

      // Closing Ori's panel pauses the presentation; the form keeps working and
      // the walkthrough keeps count. Reopening resumes at the current step.
      document.addEventListener('ori-guide:dismiss', function () {
        if (state.active) state.paused = true;
      });
      document.addEventListener('ori-guide:open', function () {
        if (!state.active || !state.paused) return;
        state.paused = false;
        present(state.step, false);
      });
    }

    // Leaving the page must not leave a coachmark behind.
    window.addEventListener('pagehide', function () {
      if (state.active) stop();
    });
  }

  async function init() {
    bind();
    // The mission's link asked for the walkthrough, or the page loaded with no
    // assistant hired: either way, eligible() decides.
    await start();
  }

  var api = {
    // resume is the explicit entry point for a caller that wants the
    // walkthrough now, held to the same eligibility check as a page load.
    resume: function () {
      bind();
      return start();
    },
    stop: stop,
    isActive: function () {
      return state.active;
    },
    requestedAtLoad: function () {
      return requestedAtLoad;
    },
    // Test seams.
    _state: state,
    _eligible: eligible,
    _stepCopy: stepCopy,
    _coachmarkFor: coachmarkFor,
    _choicesFor: choicesFor,
    _init: init,
    QUEST_ID: QUEST_ID,
    GUIDED_FLAG: GUIDED_FLAG,
    STEP_NEW_AGENT: STEP_NEW_AGENT,
    STEP_NAME: STEP_NAME,
    STEP_FACE: STEP_FACE,
    STEP_FOCUS: STEP_FOCUS,
    STEP_HIRE: STEP_HIRE
  };

  if (typeof window !== 'undefined') {
    window.OriMeetAssistantQuest = api;
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
