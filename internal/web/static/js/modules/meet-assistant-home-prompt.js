/*
 * meet-assistant-home-prompt.js — the start of Mission 01, "Meet your
 * assistant", on Home.
 *
 * Until the personal assistant is hired, Home is quiet: the mission board shows
 * Mission 01 and nothing else can start. This module shows the start of the
 * mission in Ori's blocking layer (ori-spotlight.js), in two forms:
 *
 *   briefing   Right after first-run onboarding (?quest=meet-assistant&
 *              briefing=1), Ori sits in the centre of the dimmed page with the
 *              mission card: what it is, its six steps, what it unlocks. One
 *              choice: Start mission, or Not now.
 *   step 1     Start mission, or the mission card's own Start, Today's banner,
 *              Ask Ori and the retired /?hire=1 (?quest=meet-assistant): the
 *              page stays dimmed except the Agents nav entry, and Ori says to
 *              click it. The Agents page carries on from Step 2
 *              (meet-assistant-quest.js).
 *
 * A plain Home visit shows nothing and makes no request: a mission the user put
 * off waits on the mission board, not in their way.
 *
 * Deterministic and model-free: host copy only, no request to /api/ori-guide.
 * Focus-only: Ori marks the control and the user presses it. Nothing here
 * clicks, fills, or mutates, and Not now or Escape always leaves.
 */
import {
  MEET_ASSISTANT_AGENTS_ROUTE,
  MEET_ASSISTANT_GUIDED_FLAG,
  MEET_ASSISTANT_QUEST_ROUTE
} from './personal-assistant-hire.js';
import * as spotlightLayer from './ori-spotlight.js';

export const PROMPT_QUEST = 'meet-assistant';

// The whole walkthrough is six steps: this one, then five on the Agents page.
export const TOTAL_STEPS = 6;

// The six clicks, as the briefing lists them.
export const STEP_LABELS = Object.freeze([
  'Open Agents',
  'New Agent',
  'Name',
  'Face',
  'Focus',
  'Hire'
]);

export const FIRST_STEP = Object.freeze({
  coachmark: 'nav_agents',
  index: 1,
  total: TOTAL_STEPS,
  title: 'Click Agents',
  body: 'Your assistant works from the Agents page.',
  note: 'Nothing is created until you press Hire.',
  laterLabel: 'Not now'
});

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
 * should this Home visit do about Mission 01?
 *
 *   briefing   show Ori's mission briefing (onboarding just handed over)
 *   step       show the first step: the page dimmed around Agents
 *   agents     the link asked for Mission 01 but there is a repair to make: go
 *              straight to the Agents page, which opens its one-button view
 *   none       anything else, including any read that fails
 *
 * `requested` says whether the URL asked for Mission 01, so the caller can
 * tidy the URL. `onboarding` and `mission` carry what the briefing shows.
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
  // A plain visit: the mission waits on the board. Not even a request.
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

  // Mission 01 is known by where it sends the user, not by its quest ID.
  let missions = [];
  try {
    const status = await fetchJSON(fetchImpl, '/api/progression');
    missions = Array.isArray(status?.missions) ? status.missions : [];
  } catch (_) {
    // The relationship already says there is no assistant; that is enough.
  }
  const mission = missions.find(item => item.action_url === MEET_ASSISTANT_QUEST_ROUTE) || null;
  if (mission && mission.status === 'completed') return quiet;
  return {
    action: briefing ? 'briefing' : 'step',
    requested,
    onboarding,
    mission,
    missions
  };
}

/*
 * briefingCopy is what Ori says in the briefing. Names are user data: the
 * spotlight layer sets every string as text.
 */
export function briefingCopy({ onboarding = {}, mission = null, missions = [] } = {}) {
  const guideName = String(onboarding?.assistant_name || '').trim() || 'Ori';
  const userName = String(onboarding?.user_name || '').trim();
  const locked = (Array.isArray(missions) ? missions : []).filter(item => item && item.locked);
  let unlocks = '';
  if (locked.length === 1) unlocks = `Unlocks ${locked[0].title}.`;
  else if (locked.length > 1) {
    const more = locked.length - 1;
    unlocks = `Unlocks ${locked[0].title} and ${more} more mission${more === 1 ? '' : 's'}.`;
  }
  const craft = Number(mission?.reward_craft) || 0;
  return {
    guideName,
    greeting: userName
      ? `Hi ${userName}. Before anything else, let’s meet your assistant.`
      : 'Before anything else, let’s meet your assistant.',
    kicker: 'Starter · Mission 01',
    reward: craft > 0 ? `+${craft} Craft` : '',
    title: String(mission?.title || '').trim() || 'Meet your assistant',
    why:
      String(mission?.why || '').trim() ||
      'Your assistant is the one agent that owns your ongoing work. Make them yours.',
    steps: [...STEP_LABELS],
    unlocks,
    note: 'Nothing is created until you press Hire.',
    startLabel: 'Start mission',
    laterLabel: 'Not now'
  };
}

function sessionStore(win) {
  try {
    return win?.sessionStorage || null;
  } catch (_) {
    return null;
  }
}

/*
 * showFirstStep dims the page around the Agents nav entry. When the user
 * presses it, the Agents page is told to keep the same spotlight for its first
 * step. When the entry cannot be pointed at, the user is taken to the Agents
 * page instead of being left in front of a dimmed page with nothing lit.
 */
export async function showFirstStep({ layer, doc, win, navigate, guideName } = {}) {
  const store = sessionStore(win);
  const link = doc?.getElementById?.('navAgentsLink');
  const onPress = () => {
    try {
      store?.setItem(MEET_ASSISTANT_GUIDED_FLAG, '1');
    } catch (_) {
      /* the Agents page falls back to Ori's panel, which is still the walkthrough */
    }
  };
  link?.addEventListener?.('click', onPress, { once: true });
  const shown = await layer.showSpotlight({
    ...FIRST_STEP,
    guideName,
    onLater: () => link?.removeEventListener?.('click', onPress)
  });
  if (!shown) {
    link?.removeEventListener?.('click', onPress);
    layer.close();
    navigate(MEET_ASSISTANT_AGENTS_ROUTE);
  }
  return shown;
}

/*
 * run carries out a decision. The collaborators are parameters so a test can
 * drive it without a browser.
 */
export async function run(decision, { layer, doc, win, navigate } = {}) {
  if (!decision || !layer) return false;
  const copy = briefingCopy(decision);
  if (decision.action === 'step') {
    return showFirstStep({ layer, doc, win, navigate, guideName: copy.guideName });
  }
  if (decision.action === 'briefing') {
    layer.showBriefing({
      ...copy,
      onStart: () => {
        void showFirstStep({ layer, doc, win, navigate, guideName: copy.guideName });
      }
    });
    return true;
  }
  return false;
}

// Drops ?quest= and ?briefing= from the URL without adding a history entry, so
// a reload or a Back press does not replay the arrival.
function clearQuestParams(win) {
  if (!win || !win.history || typeof win.history.replaceState !== 'function') return;
  try {
    const url = new URL(win.location.href);
    if (!url.searchParams.has('quest') && !url.searchParams.has('briefing')) return;
    url.searchParams.delete('quest');
    url.searchParams.delete('briefing');
    win.history.replaceState(win.history.state, '', url.pathname + url.search + url.hash);
  } catch (_) {
    /* a cosmetic URL tidy must never break the walkthrough */
  }
}

async function init() {
  const win = globalThis.window;
  const decision = await decide();
  if (decision.action === 'agents') {
    win.location.replace(MEET_ASSISTANT_AGENTS_ROUTE);
    return;
  }
  if (decision.requested) clearQuestParams(win);
  await run(decision, {
    layer: spotlightLayer,
    doc: globalThis.document,
    win,
    navigate: href => {
      win.location.href = href;
    }
  });
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
