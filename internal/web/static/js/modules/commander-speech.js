// commander-speech.js — what the Commander says on the map (PRD
// commander-speaks-on-map). Pure functions over the folder agent report view
// (folderAgentReportView) and the Commander's ticket activity: the collapsed
// line, the balloon headline, what the live region may announce, when the
// balloon opens by itself (FR10) and which thing it shows first (FR25).
//
// Every word here is host-written or one of the view's bounded fields; the
// collapsed line never carries a ticking time (FR27).

import { folderAgentRunStep } from './folder-agent-card.js';

function plural(n, one, many) {
  return n === 1 ? one : many;
}

// couldNotLook says the agent's own look did not run (the cause sentence
// from #798 or a skipped turn), so its line points at the reason.
function couldNotLook(view) {
  return /could not look itself/.test(String(view?.modelSentence || ''));
}

// lookLine narrates the look (FR18–FR19): the walk, then the agent's turn.
export function lookLine(view) {
  const look = view?.look || {};
  if (look.turn) return look.folder ? `Opening ${look.folder}…` : 'Thinking it through…';
  if (look.folder) {
    const n = look.opened;
    return n > 0
      ? `Looking in ${look.folder} · ${n} ${plural(n, 'folder', 'folders')} so far`
      : `Looking in ${look.folder}`;
  }
  return 'Looking around…';
}

// commanderSpeech is the Commander's voice for one folder agent view:
// {phase, line, headline, announce, review}. line is the collapsed speech
// line, headline the balloon header's line, announce what the live region may
// say. null when the Commander has nothing to say (kept, or no view).
export function commanderSpeech(view) {
  if (!view || !view.workspaceID) return null;
  const folder = view.folder || view.workspace;
  switch (view.state) {
    case 'looking': {
      const line = lookLine(view);
      // The count moves with every poll; the live region says only which
      // part of the look is under way.
      const announce = view.look?.turn ? line : `Looking around ${folder}`;
      return { phase: 'looking', line, headline: line, announce, review: false };
    }
    case 'reported': {
      if (couldNotLook(view)) {
        return {
          phase: 'cause',
          line: "I couldn't look · Review",
          headline: "I couldn't look myself",
          announce: "I couldn't look",
          review: true
        };
      }
      const line = `I have a plan for ${folder} · Review`;
      return {
        phase: 'report',
        line,
        headline: '',
        announce: `I have a plan for ${folder}`,
        review: true
      };
    }
    case 'failed':
      return {
        phase: 'cause',
        line: "I couldn't finish looking · Review",
        headline: "I couldn't finish looking",
        announce: "I couldn't finish looking",
        review: true
      };
    case 'setting_up': {
      const step = folderAgentRunStep(view);
      const where = step && step.count ? `step ${step.step} of ${step.count}` : '';
      const line = [`Setting up ${view.workspace}`, where, step?.words || '']
        .filter(Boolean)
        .join(' · ');
      return {
        phase: 'running',
        line,
        headline: '',
        announce: [`Setting up ${view.workspace}`, where].filter(Boolean).join(' · '),
        review: false
      };
    }
    case 'set_up': {
      if (view.setup?.status === 'stopped') {
        return {
          phase: 'stopped',
          line: 'A step did not finish · Review',
          headline: '',
          announce: 'A step did not finish',
          review: true
        };
      }
      return {
        phase: 'done',
        line: `${view.workspace} is set up · See what I made`,
        headline: '',
        announce: `${view.workspace} is set up`,
        review: true
      };
    }
    default:
      return null;
  }
}

// commanderOneLine is the shrunk card (FR13, FR15): the Commander's line in
// the third person with one action. null when there is nothing to say.
export function commanderOneLine(view) {
  const speech = commanderSpeech(view);
  if (!speech) return null;
  const folder = view.folder || view.workspace;
  const agent = view.agent || 'The agent';
  switch (speech.phase) {
    case 'looking':
      return {
        phase: speech.phase,
        text: `${agent} is looking around ${folder}`,
        action: 'Review'
      };
    case 'report':
      return { phase: speech.phase, text: `${agent} has a plan for ${folder}`, action: 'Review' };
    case 'cause':
      return { phase: speech.phase, text: `${agent} could not look itself`, action: 'Review' };
    case 'running': {
      const step = folderAgentRunStep(view);
      const where = step && step.count ? ` · step ${step.step} of ${step.count}` : '';
      return {
        phase: speech.phase,
        text: `Setting up ${view.workspace}${where}`,
        action: 'Review'
      };
    }
    case 'stopped':
      return {
        phase: speech.phase,
        text: `${view.workspace}: a step did not finish`,
        action: 'Review'
      };
    case 'done':
      return {
        phase: speech.phase,
        text: `${view.workspace} is set up`,
        action: 'See what I made'
      };
    default:
      return null;
  }
}

// renderCommanderOneLine draws the shrunk card: the line and its one button.
// onReview(button) expands the balloon (or shows the full card).
export function renderCommanderOneLine(view, doc = document, { onReview, action } = {}) {
  const words = commanderOneLine(view);
  if (!words) return null;
  const row = doc.createElement('div');
  row.className = 'folder-agent-card folder-agent-card--line';
  row.dataset.folderAgentLine = words.phase;
  row.dataset.workspaceId = view.workspaceID;
  const text = doc.createElement('p');
  text.className = 'folder-agent-card__line-text';
  text.textContent = words.text;
  const button = doc.createElement('button');
  button.type = 'button';
  button.className = 'btn btn-sm folder-agent-card__secondary folder-agent-card__line-action';
  button.textContent = action || words.action;
  button.dataset.folderAgentReview = view.workspaceID;
  button.addEventListener('click', () => onReview?.(button));
  row.append(text, button);
  return row;
}

// commanderAutoOpen decides FR10: the balloon expands by itself only when the
// owner just started the look or pressed Set it up on this page (followed),
// or when the report arrives while the owner is on this map. A report that
// was already waiting (no previous view) shows only the collapsed line.
export function commanderAutoOpen({ previous, next, followed = false, onMap = false }) {
  if (!next || !onMap) return false;
  const before = previous?.state || '';
  const now = next.state;
  if (before === now) return false;
  if (followed && (now === 'looking' || now === 'setting_up')) return true;
  if (followed && before === 'setting_up' && now === 'set_up') return true;
  if (before === 'looking' && (now === 'reported' || now === 'failed')) return true;
  return false;
}

// URGENCY orders what waits (FR25): a ticket waiting for the owner, then a
// waiting report, then a finished result; work in progress after those.
const URGENCY = { ticket_waiting: 0, report: 1, cause: 1, stopped: 1, result: 2, done: 2 };

function urgencyOf(item) {
  return URGENCY[item?.kind] ?? 3;
}

// commanderQueue sorts what the Commander has to say, most urgent first (a
// stable sort: equal items keep their order), and words the "and N more".
export function commanderQueue(items) {
  const sorted = (Array.isArray(items) ? items : [])
    .filter(Boolean)
    .map((item, index) => ({ item, index }))
    .sort((a, b) => urgencyOf(a.item) - urgencyOf(b.item) || a.index - b.index)
    .map(entry => entry.item);
  const rest = Math.max(0, sorted.length - 1);
  return { first: sorted[0] || null, rest: sorted.slice(1), more: rest ? `and ${rest} more` : '' };
}
