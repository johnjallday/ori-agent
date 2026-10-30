// One song a Home's manager offers to open, as both of its cards read it: the
// Home page's suggestion card and the manager's offer in the assistant drawer.
// Both press the same Set up (with open), so the server rechecks the plan it
// was shown; neither opens a song itself, and neither carries a path.
import './folder-plan-lines.js';
import { folderPickCopy } from './folder-pick-copy.js';

export const DIGEST_ENDPOINT = '/api/personal-assistant/folder-digest';
export const BRIEF_MAX = 2000;
const POLL_MS = 1500;
const POLL_LIMIT = 200;

export const STOP_COPY = {
  needs_model: 'Its assistant needs a model first. Your assistant kept what finished.',
  needs_choice: 'Choose which project file to open.',
  needs_pick: folderPickCopy('this song'),
  consent_stale: 'Its shared agent needs your review first.',
  assistant_missing: 'The shared agent this Home used is gone. Choose its agent first.'
};

// The song's own workspace, as an opened outcome names it.
const SONG_ROUTE = /^\/workspaces\/[A-Za-z0-9._~%-]+$/;

function text(value) {
  return String(value ?? '').trim();
}

// songSuggestionView is one suggestion as the cards show it, or null for an
// offer that is not a song this Home's manager can open.
export function songSuggestionView(offer) {
  const planLines = globalThis.OriFolderPlanLines;
  if (!offer || typeof offer.id !== 'string' || !offer.id || !planLines) return null;
  const setup = offer.setup || null;
  const source = setup || offer.plan;
  const lines = Array.isArray(source?.lines) ? source.lines : [];
  const parts = planLines.handoffLineParts(lines);
  if (!parts) return null;
  const digest = text(setup?.plan_digest || offer.plan?.digest);
  if (!digest) return null;
  const song = parts.song || text(offer.subject?.name) || text(offer.folder);
  const manager = parts.manager.replace(/^Your /, '');
  const hire = source?.hire || null;
  const agent = hire?.mode === 'pending' ? '' : text(hire?.agent_name);
  const role = text(hire?.role_label);
  const model = text(hire?.model);
  const status = text(setup?.status);
  const stopped = status === 'stopped' ? text(setup?.stop_reason) : '';
  const savedEntry = text(source?.entry_name);
  const entryName = stopped === 'needs_choice' ? '' : savedEntry;
  return {
    entryName,
    fileCaption: savedEntry ? `${entryName ? 'Opens' : 'Previously chose'} ${savedEntry}` : '',
    offerID: offer.id,
    digest,
    song,
    manager,
    sections: planLines.handoffNames(source),
    // A late question is not a promise to open the no-longer-confirmed file.
    lines:
      stopped === 'needs_choice' && savedEntry
        ? lines.filter(line => line.kind !== 'folder' || line.name !== `Opens ${savedEntry}`)
        : lines,
    hire: agent
      ? {
          name: agent,
          detail: [role && role !== agent ? `${role} role` : '', model ? `uses ${model}` : '']
            .filter(Boolean)
            .join(' · ')
        }
      : null,
    brief: text(setup?.brief || offer.plan?.brief_default),
    asksBrief: Boolean(offer.plan?.brief_default || setup?.brief),
    running: status === 'running',
    // Stopped anywhere but the hand-off, the assistant's card has the answer.
    waitsElsewhere: Boolean(stopped && stopped !== 'handoff_ready'),
    stopCopy:
      stopped === 'needs_pick'
        ? folderPickCopy(song, offer.needs_pick_reason)
        : STOP_COPY[stopped] ||
          (stopped && stopped !== 'handoff_ready' ? 'A step did not finish.' : ''),
    // A song with more than one project file stops for a choice; these are the
    // files the server offered, and one of them answers it in place.
    choices:
      stopped === 'needs_choice' && Array.isArray(setup?.entry_candidates)
        ? setup.entry_candidates.map(text).filter(Boolean).slice(0, SONG_CHOICE_MAX)
        : [],
    label: `Open ${song} & hire`
  };
}

const SONG_CHOICE_MAX = 64;

/** What a card says while the manager is opening the song. */
export function songOpeningLabel(view) {
  return `${view.manager} is opening ${view.song}…`;
}

// folderDigestOffer is the one offer a GET ?offer_id= read returns.
export function folderDigestOffer(body) {
  const offer = body?.folder_digest?.offer;
  return offer && typeof offer === 'object' ? offer : null;
}

/**
 * The songs a Home's manager suggests, newest first as the server orders them,
 * as views. null when the read failed, so a caller can keep what it showed.
 */
export async function readSongSuggestions(fetchImpl, homeID) {
  const id = text(homeID);
  if (!id) return [];
  try {
    const response = await fetchImpl(
      `${DIGEST_ENDPOINT}/home-suggestions?home_id=${encodeURIComponent(id)}`,
      { headers: { Accept: 'application/json' } }
    );
    if (!response.ok) return null;
    const body = await response.json();
    const suggestions = Array.isArray(body?.suggestions) ? body.suggestions : [];
    return suggestions.map(songSuggestionView).filter(Boolean);
  } catch (_) {
    return null;
  }
}

/**
 * The one press: Set up with open, the plan's digest, the brief when the plan
 * asks for one, and a fresh request id; `entryName` answers a stop for the
 * project file with one of the names the server offered. Returns { ok } or
 * { ok: false, message }. Otherwise the saved reviewed filename is carried
 * without asking again. A lost response is never re-sent; the caller follows
 * the offer instead.
 */
export async function pressSongOpen(fetchImpl, view, brief = '', { entryName = '' } = {}) {
  const typed = text(brief);
  if (typed.length > BRIEF_MAX) return { ok: false, message: '' };
  const entry = text(entryName);
  if (entry && !(view.choices || []).includes(entry)) return { ok: false, message: '' };
  const body = { plan_digest: view.digest, open: true, request_id: songRequestID() };
  if (view.asksBrief) body.brief = typed;
  if (entry || view.entryName) body.entry_name = entry || view.entryName;
  try {
    const response = await fetchImpl(
      `${DIGEST_ENDPOINT}/offers/${encodeURIComponent(view.offerID)}/setup`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify(body)
      }
    );
    const result = await response.json().catch(() => null);
    if (response.ok) return { ok: true };
    return {
      ok: false,
      message: result?.plan_changed
        ? 'What opening does has changed. Review it again below.'
        : text(result?.error) || `${view.song} could not be opened.`
    };
  } catch (_) {
    return { ok: false, message: 'Ori could not be reached. Nothing was opened twice; try again.' };
  }
}

/**
 * Reads the offer until its run settles: { state: 'opened', outcome } or
 * { state: 'stopped', offer } (answered on the assistant's card), else
 * { state: 'unsettled' }. Reading never starts or continues work.
 */
export async function followSongOpen(
  fetchImpl,
  view,
  { pollMs = POLL_MS, limit = POLL_LIMIT, sleep = wait, live = () => true } = {}
) {
  for (let i = 0; i < limit; i += 1) {
    await sleep(pollMs);
    if (!live()) return { state: 'unsettled' };
    let offer = null;
    try {
      const response = await fetchImpl(
        `${DIGEST_ENDPOINT}?offer_id=${encodeURIComponent(view.offerID)}`,
        { headers: { Accept: 'application/json' } }
      );
      if (response.ok) offer = folderDigestOffer(await response.json());
    } catch (_) {
      continue;
    }
    if (!offer) continue;
    if (offer.status === 'resolved')
      return { state: 'opened', outcome: offer.outcome || null, offer };
    if (offer.setup?.status === 'stopped') return { state: 'stopped', offer };
  }
  return { state: 'unsettled' };
}

/**
 * The opened song as a receipt: the manager opened it, a link to the song's
 * workspace, and the run's hire and first-task rows. The route is the
 * server's, kept only when it is a workspace page.
 */
export function songOpenedView(view, outcome) {
  const route = text(outcome?.route);
  const rows = (Array.isArray(outcome?.receipt) ? outcome.receipt : [])
    .filter(
      row =>
        (row?.kind === 'agent' && row.assigned === true) ||
        row?.kind === 'task' ||
        (row?.kind === 'folder' && text(row.name).startsWith('Opens '))
    )
    .map(row => ({
      kind: String(row.kind),
      text: [text(row.name), text(row.detail)].filter(Boolean).join(' — ')
    }))
    .filter(row => row.text);
  return {
    kicker: `${view.manager} opened it`,
    heading: `${view.song} is open`,
    body: 'It has its own workspace and assistant, and stays in this Home’s library.',
    route: SONG_ROUTE.test(route) ? route : '',
    openLabel: `Open ${view.song}`,
    workspaceID: text(outcome?.workspace_id),
    rows
  };
}

function wait(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

export function songRequestID() {
  return (
    globalThis.crypto?.randomUUID?.() || `home-${Date.now()}-${Math.random().toString(16).slice(2)}`
  );
}
