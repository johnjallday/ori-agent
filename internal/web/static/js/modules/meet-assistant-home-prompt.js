/*
 * meet-assistant-home-prompt.js — the first step of Mission 01, "Meet your
 * assistant", on Home.
 *
 * Until the personal assistant is hired, Home is quiet: the mission board shows
 * Mission 01 and nothing else can start. This module is the one thing Ori says
 * there, and the first step of the walkthrough: the assistant works from the
 * Agents page, with Ori's hand on the Agents nav entry for the user to click
 * (PRD FR10, 3A). The Agents page carries on from Step 2
 * (meet-assistant-quest.js).
 *
 * It runs on a plain Home visit before the hire, and when asked for with
 * ?quest=meet-assistant: Mission 01's Start, Today's banner and Ask Ori all
 * begin here, so the user is shown every click rather than being moved to the
 * Agents page.
 *
 * Deterministic and model-free: host copy only, no request to /api/ori-guide.
 * Focus-only: the choice navigates, and nothing here clicks, fills, or mutates.
 * It presents at most once per page load. Closing Ori's panel is the user's
 * answer for this visit, so nothing here reopens it.
 */
import {
  MEET_ASSISTANT_AGENTS_ROUTE,
  MEET_ASSISTANT_QUEST_ROUTE
} from './personal-assistant-hire.js';

export const PROMPT_QUEST = 'meet-assistant';
export const CHOICE_GO = 'go';

// The whole walkthrough is six steps: this one, then five on the Agents page.
export const TOTAL_STEPS = 6;

export const PROMPT_STEP = Object.freeze({
  quest: PROMPT_QUEST,
  index: 1,
  total: TOTAL_STEPS,
  answer: 'Your assistant works from the Agents page. Click Agents to go meet them.',
  note: 'Nothing is created until you press Hire.',
  coachmark: 'nav_agents',
  // The only way on when the nav entry cannot be marked (a collapsed mobile
  // navbar has no visible Agents link), so it is always offered.
  choices: [{ id: CHOICE_GO, label: 'Take me there' }]
});

async function fetchJSON(fetchImpl, url) {
  const res = await fetchImpl(url, { headers: { Accept: 'application/json' } });
  if (!res.ok) throw new Error(String(res.status));
  return res.json();
}

// A Home visit that arrived with one of these has somewhere else to be: another
// walkthrough, a Map focus, the workspace creator, a setup journey. The step is
// for a plain visit, or for its own ?quest=meet-assistant, and never competes
// with another intent.
const HOME_INTENT_PARAMS = ['quest', 'focus', 'create', 'setup'];

/*
 * decide answers one question from the server's own state: what should this
 * Home visit do about Mission 01?
 *
 *   present  show the first step (unhired, onboarding finished, mission open)
 *   agents   the link asked for Mission 01 but there is a repair to make: go
 *            straight to the Agents page, which opens its one-button view
 *   none     anything else, including any read that fails
 *
 * `requested` says whether the URL asked for it, so the caller can tidy the URL.
 */
export async function decide({ fetchImpl = globalThis.fetch, location } = {}) {
  const loc = location || globalThis.window?.location;
  if (!loc || loc.pathname !== '/') return { action: 'none', requested: false };
  let requested = false;
  try {
    const params = new URLSearchParams(loc.search || '');
    requested = params.get('quest') === PROMPT_QUEST;
    const otherIntent = HOME_INTENT_PARAMS.some(
      name => params.has(name) && !(name === 'quest' && requested)
    );
    if (otherIntent) return { action: 'none', requested: false };
  } catch (_) {
    return { action: 'none', requested: false };
  }

  let onboarding;
  let assistant;
  try {
    onboarding = await fetchJSON(fetchImpl, '/api/onboarding/status');
    assistant = await fetchJSON(fetchImpl, '/api/personal-assistant');
  } catch (_) {
    return { action: 'none', requested };
  }
  if (!onboarding || onboarding.needs_onboarding !== false) return { action: 'none', requested };
  const relationship = String(assistant?.personal_assistant?.state || '').trim();
  if (relationship === 'repair_needed') {
    return { action: requested ? 'agents' : 'none', requested };
  }
  if (relationship !== 'needs_hire' && relationship !== 'hiring') {
    return { action: 'none', requested };
  }

  // Mission 01 is known by where it sends the user, not by its quest ID.
  try {
    const status = await fetchJSON(fetchImpl, '/api/progression');
    const missions = Array.isArray(status?.missions) ? status.missions : [];
    const meet = missions.find(mission => mission.action_url === MEET_ASSISTANT_QUEST_ROUTE);
    if (meet && meet.status === 'completed') return { action: 'none', requested };
  } catch (_) {
    // The relationship already says there is no assistant; that is enough.
  }
  return { action: 'present', requested };
}

// Drops ?quest= from the URL without adding a history entry, so a reload or a
// Back press does not replay the arrival.
function clearQuestParam(win) {
  if (!win || !win.history || typeof win.history.replaceState !== 'function') return;
  try {
    const url = new URL(win.location.href);
    if (!url.searchParams.has('quest')) return;
    url.searchParams.delete('quest');
    win.history.replaceState(win.history.state, '', url.pathname + url.search + url.hash);
  } catch (_) {
    /* a cosmetic URL tidy must never break the walkthrough */
  }
}

/*
 * start presents the first step and wires its one choice. It returns false when
 * the guide is not on the page.
 */
export function start({ guide, navigate, doc } = {}) {
  const g = guide || globalThis.window?.OriGuide;
  const d = doc || globalThis.document;
  if (!g || typeof g.presentQuestStep !== 'function') return false;

  if (typeof g.open === 'function' && typeof g.isOpen === 'function' && !g.isOpen()) {
    try {
      // skipGreeting: the step renders right after; the greeting would
      // otherwise land later and replace it.
      g.open(null, { skipGreeting: true });
    } catch (_) {
      /* the step still renders into the panel body */
    }
  }
  const result = g.presentQuestStep({ ...PROMPT_STEP, choices: [...PROMPT_STEP.choices] });

  const go =
    navigate ||
    (href => {
      globalThis.window.location.href = href;
    });
  d?.addEventListener?.('ori-guide:quest-choice', event => {
    const detail = event?.detail || {};
    if (detail.quest !== PROMPT_QUEST || detail.choice !== CHOICE_GO) return;
    go(MEET_ASSISTANT_AGENTS_ROUTE);
  });
  return !!(result && result.rendered);
}

async function init() {
  const win = globalThis.window;
  const { action, requested } = await decide();
  if (action === 'agents') {
    win.location.replace(MEET_ASSISTANT_AGENTS_ROUTE);
    return;
  }
  if (requested) clearQuestParam(win);
  if (action === 'present') start();
}

if (typeof window !== 'undefined' && typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => {
      void init();
    });
  } else {
    void init();
  }
}
