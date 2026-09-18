/*
 * meet-assistant-home-prompt.js — Ori's one-line nudge on Home before the hire.
 *
 * Until the personal assistant is hired, Home is quiet: the mission board shows
 * Mission 01 and nothing else can start. This module adds the one thing Ori
 * says there: the assistant works from the Agents page, with a mark on the
 * Agents nav entry and a single "Take me there" choice (PRD FR10).
 *
 * Deterministic and model-free: host copy only, no request to /api/ori-guide.
 * Focus-only: the choice navigates, and nothing here clicks, fills, or mutates.
 * It presents at most once per page load. Closing Ori's panel is the user's
 * answer for this visit, so nothing here reopens it.
 */
import { MEET_ASSISTANT_QUEST_ROUTE } from './personal-assistant-hire.js';

export const PROMPT_QUEST = 'meet-assistant';
export const CHOICE_GO = 'go';

export const PROMPT_STEP = Object.freeze({
  quest: PROMPT_QUEST,
  answer: 'Your assistant works from the Agents page. Let’s go meet them.',
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

// A Home visit that arrived with one of these already has somewhere to be: a
// walkthrough, a Map focus, the workspace creator, a setup journey. The nudge
// is for a plain visit, and never competes with an intent.
const HOME_INTENT_PARAMS = ['quest', 'focus', 'create', 'setup'];

/*
 * shouldPrompt answers one question from the server's own state: is this a
 * Home visit where Mission 01 is the next thing to do? Onboarding must be
 * finished (its modal owns the screen until then), the relationship must be
 * unhired, and Mission 01 must still be open. An intent in the URL wins. Any
 * read that fails means no prompt.
 */
export async function shouldPrompt({ fetchImpl = globalThis.fetch, location } = {}) {
  const loc = location || globalThis.window?.location;
  if (!loc || loc.pathname !== '/') return false;
  try {
    const params = new URLSearchParams(loc.search || '');
    if (HOME_INTENT_PARAMS.some(name => params.has(name))) return false;
  } catch (_) {
    return false;
  }
  let onboarding;
  let assistant;
  try {
    onboarding = await fetchJSON(fetchImpl, '/api/onboarding/status');
    assistant = await fetchJSON(fetchImpl, '/api/personal-assistant');
  } catch (_) {
    return false;
  }
  if (!onboarding || onboarding.needs_onboarding !== false) return false;
  const relationship = String(assistant?.personal_assistant?.state || '').trim();
  if (relationship !== 'needs_hire' && relationship !== 'hiring') return false;

  // Mission 01 is known by where it sends the user, not by its quest ID.
  try {
    const status = await fetchJSON(fetchImpl, '/api/progression');
    const missions = Array.isArray(status?.missions) ? status.missions : [];
    const meet = missions.find(mission => mission.action_url === MEET_ASSISTANT_QUEST_ROUTE);
    if (meet && meet.status === 'completed') return false;
  } catch (_) {
    // The relationship already says there is no assistant; that is enough.
  }
  return true;
}

/*
 * start presents the prompt and wires its one choice. It returns false when
 * the guide is not on the page.
 */
export function start({ guide, navigate, doc } = {}) {
  const g = guide || globalThis.window?.OriGuide;
  const d = doc || globalThis.document;
  if (!g || typeof g.presentQuestStep !== 'function') return false;

  if (typeof g.open === 'function' && typeof g.isOpen === 'function' && !g.isOpen()) {
    try {
      // skipGreeting: the prompt renders right after; the greeting would
      // otherwise land later and replace it.
      g.open(null, { skipGreeting: true });
    } catch (_) {
      /* the prompt still renders into the panel body */
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
    go(MEET_ASSISTANT_QUEST_ROUTE);
  });
  return !!(result && result.rendered);
}

async function init() {
  if (await shouldPrompt()) start();
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
