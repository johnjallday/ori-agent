/*
 * personal-hq-notes-review.js — the notes part of the Build My HQ review
 * (assistant-hq-onboarding 3.5, 3.6).
 *
 * Pure decisions only; personal-assistant-hq-card.js owns the DOM and the
 * requests. The review never reads the user's notes and never connects,
 * disconnects or replaces anything: it shows what a build would do and turns
 * the user's choice into the HQ request's bounded `notes` object.
 *
 *   create       nothing is connected and a default place exists: notes are
 *                included by default, at the place the review shows
 *   existing     a folder is already connected (Ori's or the user's): it stays
 *                exactly as it is and the build creates no notes
 *   missing      the connected folder is gone: the build creates none, and
 *                Notes is where the user reconnects
 *   unavailable  the notes status could not be read, or there is no default
 *                place: the build creates no notes and says so
 */

// What Ori keeps up to date in a folder it sets up, verbatim from the
// secondbrain creator's OwnedFoldersSentence.
export const NOTES_SCOPE_SENTENCE =
  'Ori keeps Inbox, Daily, People, a note per project and what it knows about you up to date. Everything else in the folder is yours, and your Markdown files stay yours.';

function count(n, one, many) {
  return `${n} ${n === 1 ? one : many}`;
}

/** What setting the notes up would copy in, from the status read's preview. */
export function notesSeedSentence(preview) {
  if (!preview) return '';
  const parts = [];
  const followUps = Number(preview.follow_ups) || 0;
  const people = Number(preview.people) || 0;
  if (followUps > 0) parts.push(count(followUps, 'open follow-up', 'open follow-ups'));
  if (people > 0) parts.push(count(people, 'person you mentioned', 'people you mentioned'));
  if (preview.facts) parts.push('what you have told Ori about yourself');
  if (!parts.length) return 'It starts empty.';
  const list =
    parts.length === 1
      ? parts[0]
      : `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`;
  return `It starts with ${list}.`;
}

/**
 * The review's view.
 * @param {object|null} secondBrain the notes status read, or null when it failed
 * @param {{include?: boolean, chosen?: {selectionToken: string, path: string, name: string}|null}} choice
 */
export function hqNotesReviewView(secondBrain, choice = {}) {
  if (!secondBrain || typeof secondBrain !== 'object') {
    return {
      mode: 'unavailable',
      include: false,
      path: '',
      message: 'Notes can be set up later from Notes.'
    };
  }
  const source = secondBrain.source;
  if (secondBrain.status === 'missing' && source) {
    return {
      mode: 'missing',
      include: false,
      path: String(source.path || ''),
      sourceName: String(source.name || ''),
      message:
        'Your connected notes folder is missing. Building HQ leaves it as it is; reconnect or choose another folder from Notes.'
    };
  }
  if (source) {
    const writable = source.origin === 'created_by_ori';
    return {
      mode: 'existing',
      include: false,
      writable,
      path: String(source.path || ''),
      sourceName: String(source.name || ''),
      message: writable
        ? 'Your notes are already set up. HQ will use them as they are.'
        : 'Your notes stay connected as they are. Ori reads them and never writes to them.'
    };
  }
  const chosen = choice.chosen && choice.chosen.selectionToken ? choice.chosen : null;
  const fallback = secondBrain.setup_default || null;
  if (!chosen && !fallback?.path) {
    return {
      mode: 'unavailable',
      include: false,
      path: '',
      message: 'Confirm your workspace directory to include notes, or set them up later from Notes.'
    };
  }
  const include = choice.include !== false;
  return {
    mode: 'create',
    include,
    chosen: !!chosen,
    path: chosen ? joinPath(chosen.path, chosen.name) : String(fallback.path),
    witness: chosen ? '' : String(fallback.path),
    selectionToken: chosen ? chosen.selectionToken : '',
    name: chosen ? chosen.name : '',
    scope: NOTES_SCOPE_SENTENCE,
    seed: notesSeedSentence(secondBrain.setup_preview),
    message: include ? '' : 'No notes folder will be made. Set up notes later from Notes.'
  };
}

function joinPath(parent, name) {
  const base = String(parent || '').replace(/[\\/]+$/, '');
  return base ? `${base}/${name}` : String(name || '');
}

/**
 * The HQ request's `notes` object. A folder is named only by the reviewed
 * witness or by a picker selection token — never by a path the server trusts.
 */
export function hqNotesPayload(view) {
  if (!view || view.mode !== 'create') return { include: false };
  if (!view.include) return { include: false };
  if (view.chosen) return { include: true, selection_token: view.selectionToken, name: view.name };
  return { include: true, witness: view.witness };
}

// The first-action record (assistant-hq-onboarding 4.1): set when this browser
// builds a Personal HQ, cleared when the user leaves the first action. It is
// presentation only; HQ and notes records stay the truth. An HQ built before
// this existed never has one, so returning users never see the result card.
export const FIRST_ACTION_KEY = 'ori.hq.firstAction.v1';

export function readFirstAction(storage) {
  try {
    const parsed = JSON.parse(storage?.getItem(FIRST_ACTION_KEY) || 'null');
    const workspaceId = String(parsed?.workspaceId || '');
    return workspaceId ? { workspaceId } : null;
  } catch (_) {
    return null;
  }
}

// Missing identifiers must never match each other and manufacture a first
// action for a returning HQ (including older, top-level-ID projections).
export function hqFirstActionMatches(workspaceId, record, intentWorkspaceId) {
  const id = String(workspaceId || '').trim();
  return Boolean(id && (record?.workspaceId === id || intentWorkspaceId === id));
}

export function rememberFirstAction(storage, workspaceId) {
  const id = String(workspaceId || '').trim();
  if (!storage || !id) return false;
  try {
    storage.setItem(FIRST_ACTION_KEY, JSON.stringify({ workspaceId: id }));
    return true;
  } catch (_) {
    return false;
  }
}

export function forgetFirstAction(storage) {
  try {
    storage?.removeItem(FIRST_ACTION_KEY);
  } catch (_) {
    // Storage is optional; the card simply ends for this page.
  }
}

/**
 * The result card's first step, from canonical states only.
 * @param {{status: string, writable?: boolean}|null} notes hqNotesOutcome, or null
 * @param {{hqRoute?: string}} options
 * @returns {{lead: string, note: boolean, openNotes: string, setupNotes: boolean,
 *            retry: boolean, folder: boolean, explore: boolean}}
 */
export function hqFirstActionView(notes, { hqRoute = '' } = {}) {
  const status = String(notes?.status || 'none');
  const notesRoute = hqRoute ? `${hqRoute}?station=second-brain` : '';
  const base = {
    note: false,
    openNotes: '',
    setupNotes: false,
    retry: false,
    folder: false,
    explore: true
  };
  if (status === 'ready' && notes.writable) {
    return {
      ...base,
      lead: 'Notes are ready for your first idea or reminder.',
      note: true
    };
  }
  if (status === 'ready') {
    return {
      ...base,
      lead: 'Your notes stay connected as they are. Ori reads them and never writes to them.',
      openNotes: notesRoute
    };
  }
  if (status === 'failed' || status === 'pending') {
    return { ...base, lead: 'Your HQ is ready. Notes still need one step.', retry: true };
  }
  if (['stopped', 'missing', 'unavailable'].includes(status)) {
    return {
      ...base,
      lead: notes?.line || 'Your HQ is ready. Your notes stay as they are.',
      openNotes: notesRoute
    };
  }
  return {
    ...base,
    lead: 'Make yourself at home. Start wherever you like; you can set up notes later.',
    setupNotes: true
  };
}

// Current source/capability wins over a saved setup receipt. In particular,
// an old successful setup never authorizes writing to a missing/changed source.
export function hqLiveNotesOutcome(notes) {
  if (!notes)
    return {
      status: 'unavailable',
      writable: false,
      canRetry: false,
      line: 'Notes status is unavailable. Open Notes to check the connection.'
    };
  if (notes.setup && ['failed', 'pending', 'stopped'].includes(notes.setup.status))
    return hqNotesOutcome(notes.setup);
  if (notes.source && notes.status === 'connected')
    return hqNotesOutcome({
      status: 'ready',
      path: notes.source.path,
      writable: notes.source.origin === 'created_by_ori'
    });
  if (notes.source)
    return {
      status: 'missing',
      path: notes.source.path,
      writable: false,
      canRetry: false,
      line: 'Your notes connection needs attention. Open Notes to check or reconnect it; existing files are unchanged.'
    };
  return hqNotesOutcome(null);
}

/** What a build's notes result means for the receipt and the retry. */
export function hqNotesOutcome(result) {
  const status = String(result?.status || '');
  const path = String(result?.path || '');
  switch (status) {
    case 'ready':
      return {
        status,
        path,
        canRetry: false,
        writable: result.writable === true,
        line: result.writable === true ? `Notes ready · ${path}` : `Notes connected · ${path}`
      };
    case 'pending':
      return { status, path, canRetry: true, line: 'Notes are still being set up.' };
    case 'failed':
      return {
        status,
        path,
        canRetry: true,
        line: String(result.message || 'Notes could not be set up. Your HQ is ready.')
      };
    case 'stopped':
      return {
        status,
        path,
        canRetry: false,
        line: String(result.message || 'Notes were not set up because another folder is connected.')
      };
    default:
      return { status: 'none', path: '', canRetry: false, line: '' };
  }
}
