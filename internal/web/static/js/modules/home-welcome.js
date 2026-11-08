/*
 * home-welcome.js — the first-run welcome on an empty Home map.
 *
 * A truly new Home shows one welcome card over the map, anchored to the
 * reserved Personal HQ site, whose one dominant action is the next step in
 * the hire-first story: Meet your assistant, then Build My HQ. Everything here
 * is a pure decision or a string; home-workspace-cockpit.js owns the DOM, the
 * routes and the controllers the card's buttons reach.
 *
 * What the card is NOT:
 *   - account state. It is decided from the durable assistant relationship
 *     and an authoritative, loaded empty workspace list, never from a count
 *     alone. Loading or a failed read shows nothing.
 *   - a decision. "Explore first" is a presentation preference kept in this
 *     browser and scoped to the current stage. It never marks a mission done,
 *     declines notes, or writes the HQ onboarding state the HQ site's own
 *     "Not now" writes.
 *   - a duplicate. While it is on screen it owns the onboarding action, and
 *     the header primary is hidden; off screen, the header primary is the
 *     compact way back in, in Map and Tree alike.
 */

// Versioned like the Tree's own `ori.home.fileTree.v1`.
export const WELCOME_STORAGE_KEY = 'ori.home.welcome.v1';

// A Home visit that arrived with one of these has somewhere else to be — a
// walkthrough, a Map focus, the workspace creator, a setup journey, the
// drawer, the HQ card — and the welcome must not cover it.
export const WELCOME_INTENT_PARAMS = Object.freeze([
  'quest',
  'focus',
  'create',
  'setup',
  'folder',
  'hq_onboarding',
  'panel'
]);

// The onboarding stages the card speaks for. Anything else (active, paused,
// repair_needed, unknown) leaves the card off: a built HQ is a real tile on
// the map, and repair is the header primary's job.
const STAGES = Object.freeze({
  needs_hire: { stage: 'meet', step: 1, action: 'meet' },
  hiring: { stage: 'meet', step: 1, action: 'resume-meet' },
  needs_hq: { stage: 'hq', step: 2, action: 'build' },
  provisioning_hq: { stage: 'hq', step: 2, action: 'resume-hq' }
});

export const JOURNEY_STEPS = Object.freeze(['Meet', 'Build your HQ', 'Make it yours']);

/** The stage ('meet' | 'hq') a relationship state belongs to, or ''. */
export function welcomeStage(relationshipState) {
  const key = String(relationshipState || '').trim();
  return Object.hasOwn(STAGES, key) ? STAGES[key].stage : '';
}

/**
 * Does this URL query carry an explicit Home intent? Mission 01's own link is
 * not one: the welcome card IS Mission 01's Home step. Onboarding's hand-over
 * (?quest=meet-assistant&briefing=1) lands here and should see the card; the
 * plain link leaves for the configuration before the card would matter.
 */
export function hasHomeIntent(search) {
  try {
    const params = new URLSearchParams(search || '');
    return WELCOME_INTENT_PARAMS.some(
      name => params.has(name) && !(name === 'quest' && params.get('quest') === 'meet-assistant')
    );
  } catch (_) {
    return false;
  }
}

function safeStorageGet(storage) {
  if (!storage) return null;
  try {
    return storage.getItem(WELCOME_STORAGE_KEY);
  } catch (_) {
    return null;
  }
}

/** The stage the user chose to explore at, or '' when none is recorded. */
export function readExploredStage(storage) {
  const text = safeStorageGet(storage);
  if (!text) return '';
  try {
    const parsed = JSON.parse(text);
    const stage = String(parsed?.explored || '');
    return stage === 'meet' || stage === 'hq' ? stage : '';
  } catch (_) {
    return '';
  }
}

/** Remember "Explore first" for this stage only. Returns whether it was kept. */
export function rememberExplored(storage, stage) {
  if (!storage || (stage !== 'meet' && stage !== 'hq')) return false;
  try {
    storage.setItem(WELCOME_STORAGE_KEY, JSON.stringify({ explored: stage }));
    return true;
  } catch (_) {
    return false;
  }
}

/**
 * Drop a preference the relationship has moved past. A choice made while
 * meeting the assistant says nothing about building the HQ, and once the HQ
 * exists there is nothing left to explore past. An unknown relationship (still
 * loading, or a failed read) keeps the record: it is not evidence of change.
 */
export function rescopeExplored(storage, relationshipState) {
  const key = String(relationshipState || '').trim();
  if (!key) return false;
  const stored = readExploredStage(storage);
  if (!stored || stored === welcomeStage(key)) return false;
  try {
    storage.removeItem(WELCOME_STORAGE_KEY);
    return true;
  } catch (_) {
    return false;
  }
}

/**
 * Decide the welcome presentation.
 *
 * @returns {{show: boolean, stage: string, step: number, action: string,
 *            compact: boolean, reason: string}}
 *   `show` is the full card; `compact` says the header primary should stand in
 *   as the way back in (it is true whenever an onboarding stage is known and
 *   the card is not shown). `reason` names why the card is off, for tests.
 */
export function homeWelcomeView({
  relationshipState,
  areaState,
  hqStatus,
  view = 'map',
  intent = false,
  exploredStage = '',
  modalOpen = false,
  drawerOpen = false
} = {}) {
  const key = String(relationshipState || '').trim();
  const entry = Object.hasOwn(STAGES, key) ? STAGES[key] : null;
  const off = reason => ({
    show: false,
    stage: entry ? entry.stage : '',
    step: entry ? entry.step : 0,
    action: entry ? entry.action : '',
    compact: !!entry,
    reason
  });
  if (!entry) return { ...off(key ? 'not-onboarding' : 'unknown'), compact: false };
  if (areaState !== 'empty-map') return off('not-empty');
  if (!hqStatus || hqStatus.valid) return off('hq-unknown');
  if (view !== 'map') return off('tree');
  if (intent) return off('intent');
  if (modalOpen) return off('modal');
  // The assistant drawer is where meeting and HQ setup happen; while it is
  // open it is the active surface, and a card behind it would only compete.
  if (drawerOpen) return off('drawer');
  if (exploredStage && exploredStage === entry.stage) return off('explored');
  return {
    show: true,
    stage: entry.stage,
    step: entry.step,
    action: entry.action,
    compact: false,
    reason: ''
  };
}

function escapeHtml(value) {
  return String(value ?? '').replace(
    /[&<>"']/g,
    c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]
  );
}

/** The card's copy for one action. Names are user data and get escaped later. */
export function welcomeCopy(action, { assistantName = '' } = {}) {
  const name = String(assistantName || '').trim();
  switch (action) {
    case 'resume-meet':
      return {
        eyebrow: 'A little help. A place to begin.',
        title: 'Pick up where you left off.',
        lead: 'You started meeting your assistant. Nothing is created until you confirm it.',
        primary: 'Resume meeting your assistant'
      };
    case 'build':
      return {
        eyebrow: name ? `${name} is ready` : 'Your assistant is ready',
        title: 'Give your work a home.',
        lead: 'Your Personal HQ keeps your notes, follow-ups and Daily Brief together. You review everything before it is built.',
        primary: 'Build My HQ'
      };
    case 'resume-hq':
      return {
        eyebrow: name ? `${name} is ready` : 'Your assistant is ready',
        title: 'Your Personal HQ is partway built.',
        lead: 'Resume to finish with the settings you already confirmed.',
        primary: 'Resume HQ setup'
      };
    case 'meet':
    default:
      return {
        eyebrow: 'A little help. A place to begin.',
        title: 'Meet your assistant.',
        lead: 'A helping hand for your work, and a home for your notes and next steps.',
        primary: 'Meet your assistant'
      };
  }
}

/** The welcome card's markup. Buttons carry data-home-welcome-action. */
export function welcomeCardHTML(view, { assistantName = '' } = {}) {
  if (!view || !view.show) return '';
  const copy = welcomeCopy(view.action, { assistantName });
  const initial =
    String(assistantName || '')
      .trim()
      .charAt(0)
      .toUpperCase() || 'A';
  const steps = JOURNEY_STEPS.map((label, index) => {
    const number = index + 1;
    const state = number < view.step ? 'done' : number === view.step ? 'current' : 'next';
    const mark = state === 'done' ? '✓' : String(number);
    const current = state === 'current' ? ' aria-current="step"' : '';
    const done = state === 'done' ? '<span class="visually-hidden"> (done)</span>' : '';
    return (
      `<li class="home-welcome-step is-${state}"${current}>` +
      `<span class="home-welcome-step-mark" aria-hidden="true">${mark}</span>` +
      `${escapeHtml(label)}${done}</li>`
    );
  }).join('');
  return (
    '<section class="home-welcome" data-home-welcome aria-labelledby="homeWelcomeTitle">' +
    `<div class="home-welcome-avatar" aria-hidden="true">${escapeHtml(initial)}</div>` +
    `<p class="home-welcome-eyebrow">${escapeHtml(copy.eyebrow)}</p>` +
    `<h3 id="homeWelcomeTitle" class="home-welcome-title">${escapeHtml(copy.title)}</h3>` +
    `<p class="home-welcome-lead">${escapeHtml(copy.lead)}</p>` +
    '<div class="home-welcome-actions">' +
    '<button type="button" class="modern-btn modern-btn-primary home-welcome-primary" ' +
    `data-home-welcome-action="primary">${escapeHtml(copy.primary)} <span aria-hidden="true">→</span></button>` +
    '<button type="button" class="modern-btn home-welcome-explore" data-home-welcome-action="explore">Explore first</button>' +
    '</div>' +
    `<ol class="home-welcome-journey" aria-label="Getting started">${steps}</ol>` +
    '</section>'
  );
}
