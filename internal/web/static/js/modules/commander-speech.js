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

// WAITING_PHASES are the seven moments the Commander waits on the person
// (folder-question-said-once FR13): the question, the plan, "something else",
// a look that could not run or finish, a step that did not finish, the offer to
// learn the songs, and a suggested goal. In each, the Next action strip, the
// Commander card on the map and the balloon's folded line say the same words,
// and they all come from waitingWords below, so they cannot drift apart.
export const WAITING_PHASES = [
  'premise',
  'report',
  'withdrawn',
  'cause',
  'stopped',
  'songs',
  'goal'
];

// waitingWords is the one table of those words: the title (the strip's title,
// the one-line card's text and the folded line's words), the button's label
// ("Answer" when the person is asked something, "Review" otherwise) and the
// Commander card's status. null for any phase that is not a waiting moment.
function waitingWords(phase, view) {
  const folder = view.folder || view.workspace;
  const agent = view.agent || 'The agent';
  switch (phase) {
    case 'premise':
      return {
        text: `${agent} has a question about ${folder}`,
        action: 'Answer',
        status: 'Has a question for you'
      };
    case 'report':
      return {
        text: `${agent} has a plan for ${folder}`,
        action: 'Review',
        status: 'Has a plan for you'
      };
    case 'withdrawn':
      return {
        text: `Nothing is planned for ${folder}`,
        action: 'Review',
        status: 'Nothing planned yet'
      };
    case 'cause':
      return {
        text: `${agent} could not look itself`,
        action: 'Review',
        status: 'Could not look'
      };
    case 'stopped':
      return {
        text: `${view.workspace}: a step did not finish`,
        action: 'Review',
        status: 'A step did not finish'
      };
    case 'songs':
      return {
        text: `${agent} asks to learn the ${view.songsOffer?.noun || 'songs'}`,
        action: 'Answer',
        status: 'Has a question for you'
      };
    case 'goal':
      return {
        text: `${agent} suggests a goal for ${view.workspace}`,
        action: 'Review',
        status: 'Suggests a goal'
      };
    default:
      return null;
  }
}

// commanderSpeech is the Commander's voice for one folder agent view:
// {phase, line, headline, announce, review}. line is the collapsed speech
// line, headline the balloon header's line, announce what the live region may
// say. null when the Commander has nothing to say (kept, or no view).
function speechOf(view) {
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
      // What the folder is comes first (confirm-and-visible-installs FR12):
      // the balloon's card speaks the host's sentence with its two answers.
      if (view.premise?.state === 'asked') {
        return {
          phase: 'premise',
          line: `One question about ${folder} · Review`,
          headline: '',
          announce: `I have a question about ${folder}`,
          review: true
        };
      }
      // "It's something else": nothing is planned until a look with the
      // owner's own words (FR6).
      if (view.premise?.withdrawn) {
        return {
          phase: 'withdrawn',
          line: `Nothing planned for ${folder} · Review`,
          headline: '',
          announce: `Nothing is planned for ${folder}`,
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
      // The Commander asks one thing at a time. While it reads the songs it
      // says where it is, in the host's words (learn-the-songs FR14); once the
      // place exists it asks to learn them first (FR2), and only then offers the
      // goal. The announcement stays the same word while a reading runs, so a
      // screen reader is not read every song.
      if (view.learning?.status === 'running' || view.learning?.status === 'summarizing') {
        return {
          phase: 'learning',
          line: view.learning.line,
          headline: '',
          announce: 'Learning the songs',
          review: false
        };
      }
      if (view.songsOffer) {
        return {
          phase: 'songs',
          line: `${view.songsOffer.ask} · Review`,
          headline: '',
          announce: view.songsOffer.ask,
          review: true
        };
      }
      // The agent's suggested goal waits for an answer (folder-setup-ends-in-home
      // FR17).
      if (view.goalOffer) {
        return {
          phase: 'goal',
          line: `A goal for ${view.workspace}? · Review`,
          headline: '',
          announce: `I suggest a goal for ${view.workspace}`,
          review: true
        };
      }
      return {
        phase: 'done',
        line: `${view.title || `${view.workspace} is set up`} · See what I made`,
        headline: '',
        announce: view.title || `${view.workspace} is set up`,
        review: true
      };
    }
    default:
      return null;
  }
}

// commanderSpeech: while the Commander waits on the person (WAITING_PHASES) its
// folded line is the row's words, "<title> · <button>", the same title the strip
// and the one-line card use. Every other phase (looking, setting up, learning,
// finished) speaks as it always did (FR17). The live region's announcement is
// unchanged (FR29).
export function commanderSpeech(view) {
  const speech = speechOf(view);
  const row = speech ? waitingWords(speech.phase, view) : null;
  return row ? { ...speech, line: `${row.text} · ${row.action}` } : speech;
}

// The workspace map has a Filing station and its own permission invitation.
// This is only a replacement for the completed receipt, never another phase.
export function commanderWaitingForFiling(view) {
  return Boolean(view?.filingPermissionRequired && commanderSpeech(view)?.phase === 'done');
}

// Decorative entrance copy belongs to the tile, never the drawer's status.
export function commanderBuildingLine(workspace) {
  return `Building ${workspace || 'workspace'}…`;
}

// commanderOneLine is the shrunk card (FR13, FR15): the Commander's line in
// the third person with one action. null when there is nothing to say. It is
// also the one source of the words for every waiting moment (FR13): `text` is
// the title the Next action strip shows, `action` the button's label, `status`
// the Commander card's status, and `waiting` says whether the phase is one of
// the seven moments the Commander waits on the person (WAITING_PHASES). A
// phase that is not one (looking, setting up, learning, finished) is
// waiting: false with no status, and the strip and the card behave as they did.
export function commanderOneLine(view) {
  const speech = commanderSpeech(view);
  if (!speech) return null;
  const waiting = waitingWords(speech.phase, view);
  if (waiting) return { phase: speech.phase, ...waiting, waiting: true };
  const folder = view.folder || view.workspace;
  const agent = view.agent || 'The agent';
  const plain = (text, action = 'Review') => ({
    phase: speech.phase,
    text,
    action,
    waiting: false,
    status: ''
  });
  switch (speech.phase) {
    case 'looking':
      return plain(`${agent} is looking around ${folder}`);
    case 'running': {
      const step = folderAgentRunStep(view);
      const where = step && step.count ? ` · step ${step.step} of ${step.count}` : '';
      return plain(`Setting up ${view.workspace}${where}`);
    }
    case 'learning':
      return plain(view.learning.line);
    case 'done':
      return plain(view.title || `${view.workspace} is set up`, 'See what I made');
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

// folderLookEnded is whether the agent's look has just ended with something for
// the person: a report (a plan to review, or a question) or a look that failed
// (visual-hire FR22). One clause for the two things that happen at that moment:
// the balloon opening on the map (commanderAutoOpen) and the chat box coming
// back to the width it had before the hire.
export function folderLookEnded(previous, next) {
  return previous?.state === 'looking' && (next?.state === 'reported' || next?.state === 'failed');
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
  if (folderLookEnded(previous, next)) return true;
  return false;
}

// URGENCY orders what the Commander says (FR25): a ticket waiting for the
// owner, then what it is doing right now (a look or setup run, then a ticket
// in progress — the live line while working, FR9), then a waiting report,
// then a finished result. Anything else comes last.
const URGENCY = {
  ticket_waiting: 0,
  live: 1,
  ticket_working: 2,
  report: 3,
  cause: 3,
  stopped: 3,
  result: 4,
  done: 4
};

function urgencyOf(item) {
  return URGENCY[item?.kind] ?? 5;
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
