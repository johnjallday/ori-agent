/*
 * meet-assistant-home-prompt.js — Mission 01, "Meet your assistant", arriving
 * on Home.
 *
 * Every entry that starts the hire still resolves to /?quest=meet-assistant:
 * the mission board, Today's banner, Ask Ori, the retired /?hire=1, and Ori's
 * contextual help. That link used to dim Home around the Agents nav entry and
 * walk the user through seven clicks. Meeting the assistant is now direct
 * (assistant-hq-onboarding group 2): the link takes the user straight to the
 * personal-assistant configuration — the Agents page's existing hire preset,
 * opened on arrival — where one confirmation hires the assistant.
 *
 *   agents    the link asked for Mission 01 and there is no assistant yet (or a
 *             repair to make): go straight to /agents?quest=meet-assistant,
 *             which opens the configuration (or its one-button repair view)
 *   welcome   first-run onboarding just handed over (&briefing=1): stay on
 *             Home, whose empty-map welcome card is the briefing now, and tidy
 *             the URL so a reload does not replay the arrival
 *   none      anything else, including any read that fails
 *
 * A plain Home visit makes no request and shows nothing. Deterministic and
 * model-free; nothing here creates, completes or skips anything. The server
 * completes the mission from the durable hire.
 */
import { MEET_ASSISTANT_AGENTS_ROUTE } from './personal-assistant-hire.js';

export const PROMPT_QUEST = 'meet-assistant';

async function fetchJSON(fetchImpl, url) {
  const res = await fetchImpl(url, { headers: { Accept: 'application/json' } });
  if (!res.ok) throw new Error(String(res.status));
  return res.json();
}

// A Home visit that arrived with one of these has somewhere else to be: another
// walkthrough, a Map focus, the workspace creator, a setup journey.
const HOME_INTENT_PARAMS = ['quest', 'focus', 'create', 'setup'];

/*
 * decide answers one question from the URL and the server's own state: what
 * should this Home visit do about Mission 01? `requested` says whether the URL
 * asked for it, so the caller can tidy the URL.
 */
export async function decide({ fetchImpl = globalThis.fetch, location } = {}) {
  const loc = location || globalThis.window?.location;
  const none = { action: 'none', requested: false };
  if (!loc || loc.pathname !== '/') return none;
  let requested = false;
  let briefing = false;
  try {
    const params = new URLSearchParams(loc.search || '');
    requested = params.get('quest') === PROMPT_QUEST;
    briefing = requested && params.get('briefing') === '1';
    const otherIntent = HOME_INTENT_PARAMS.some(
      name => params.has(name) && !(name === 'quest' && requested)
    );
    if (otherIntent) return none;
  } catch (_) {
    return none;
  }
  // A plain visit: the mission waits on the board and the welcome card.
  if (!requested) return none;

  const quiet = { action: 'none', requested };
  let onboarding;
  let assistant;
  try {
    onboarding = await fetchJSON(fetchImpl, '/api/onboarding/status');
    assistant = await fetchJSON(fetchImpl, '/api/personal-assistant');
  } catch (_) {
    return quiet;
  }
  if (!onboarding || onboarding.needs_onboarding !== false) return quiet;
  const relationship = String(assistant?.personal_assistant?.state || '').trim();
  if (relationship === 'repair_needed') return { action: 'agents', requested };
  if (relationship !== 'needs_hire' && relationship !== 'hiring') return quiet;
  // Onboarding's hand-over lands on Home itself: the welcome card offers the
  // same step without taking the user anywhere they did not choose.
  if (briefing) return { action: 'welcome', requested };
  return { action: 'agents', requested };
}

// Drops ?quest= and ?briefing= from the URL without adding a history entry, so
// a reload or a Back press does not replay the arrival.
export function clearQuestParams(win) {
  if (!win || !win.history || typeof win.history.replaceState !== 'function') return;
  try {
    const url = new URL(win.location.href);
    if (!url.searchParams.has('quest') && !url.searchParams.has('briefing')) return;
    url.searchParams.delete('quest');
    url.searchParams.delete('briefing');
    win.history.replaceState(win.history.state, '', url.pathname + url.search + url.hash);
  } catch (_) {
    /* a cosmetic URL tidy must never break the arrival */
  }
}

/*
 * run carries out a decision. `navigate` replaces the current entry, so Back
 * from the configuration returns to wherever the link was pressed.
 */
export function run(decision, { win, navigate } = {}) {
  if (!decision) return false;
  if (decision.action === 'agents') {
    navigate(MEET_ASSISTANT_AGENTS_ROUTE);
    return true;
  }
  if (decision.requested) clearQuestParams(win);
  return false;
}

async function init() {
  const win = globalThis.window;
  const decision = await decide();
  run(decision, { win, navigate: href => win.location.replace(href) });
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
