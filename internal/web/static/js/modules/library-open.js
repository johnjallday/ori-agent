// Opening a song from a Music Home's library, shared by the Home page
// (project-library.js) and the folder-setup pop-up (personal-assistant-folder.js,
// which loads on every page). Nothing here runs at import time, and nothing
// here decides anything: the open endpoint repeats every check, and a save time
// is only a file time, never evidence that anyone worked on a song.

// The library read behind both recent-song lists (the setup pop-up and the
// Home's "Recently saved"): newest save first, one page.
export const RECENT_SONGS_QUERY = 'sort=last_saved&direction=desc&page_size=25';

// The response body, or {} when there is none.
export async function readLibraryPayload(response) {
  try {
    return await response.json();
  } catch (_) {
    return {};
  }
}

// The error a failed library request throws: the server's own message, its
// machine reason (needs_choice, consent_stale, …) and the whole body, so a
// needs_choice answer still carries payload.project_files.
export function libraryRequestError(response, result = {}) {
  const error = new Error(
    typeof result.error === 'string'
      ? result.error
      : result.error?.message || result.message || `Library request failed (${response.status})`
  );
  error.status = response.status;
  error.reason = typeof result.reason === 'string' ? result.reason : '';
  error.payload = result;
  return error;
}

// libraryOpenAction is the row's one-click Open, or null when the row cannot be
// opened here (read-only Home, unsupported format, source unavailable): such a
// row keeps its Details, which explain why. Its label promises an assistant
// only when opening will add one: the Home's shared-assistant switch is on and
// its assistant is not gone (assistant-reads-projects FR19). sharing is the
// Home's `sharing` read; without it nothing is promised.
export function libraryOpenAction(row, readOnly, sharing = null) {
  if (readOnly || !row?.can_open) return null;
  const name = String(row.name || 'this project');
  const addsAssistant = sharing?.state === 'on' && !sharing.assistant_missing;
  let ariaLabel = `Open ${name}`;
  if (row.connection !== 'connected') {
    ariaLabel = addsAssistant
      ? `Open ${name}: makes its workspace and adds your assistant`
      : `Open ${name}: makes its workspace`;
  }
  return { label: 'Open', ariaLabel };
}

// A route the open action may navigate to: one workspace, nothing else.
export function libraryOpenRoute(result) {
  const route = String(result?.route || '');
  return /^\/workspaces\/[a-z0-9][a-z0-9-]*$/.test(route) ? route : '';
}

// What the status line says after a song opened, before the page moves on.
export function libraryOpenedMessage(name, result) {
  const song = String(name || 'The project');
  const agent = String(result?.agent_name || '').trim();
  switch (result?.staffing) {
    case 'added':
      return `${song} is ready. ${agent || 'Your assistant'} was added and joins each song you open.`;
    case 'joined':
      return `${song} is ready. ${agent || 'Your assistant'} joined it.`;
    // The song is open but has no assistant: never called ready
    // (library-setup-staffing).
    case 'off':
      return `${song} is open. Adding your assistant is switched off, so no agent was added.`;
    case 'consent_stale':
      return `${song} is open. Your assistant changed in a plugin update; review it on this Home to add it.`;
    case 'assistant_missing':
      return `${song} is open. Your shared assistant is gone, so no agent was added.`;
    // A Home that was never asked (set up before Homes hired their assistant):
    // where to say yes is this Home's switch (assistant-reads-projects FR18).
    case 'none':
      return `${song} is open. No assistant was added: this Home has not been asked yet. Turn on "Add my assistant to songs I open" on this Home to add one.`;
    case 'needs_model':
      return `${song} is open, but no assistant was added: set up a model first.`;
    case 'reconcile':
      return `${song} is open, but no assistant was added: an agent already uses the shared assistant's name.`;
    case 'unsettled':
      return `${song} is open. Its assistant was added but not yet recorded as the shared one; open it again to finish.`;
    case 'failed':
    case 'unavailable':
    case 'invalid':
      return `${song} is open, but its assistant could not be added. Open it again to retry.`;
    default:
      return result?.created ? `${song} is ready.` : `Opening ${song}.`;
  }
}

// The project files to choose between when a song folder holds several.
export function libraryOpenChoices(error) {
  if (error?.reason !== 'needs_choice') return [];
  const files = error?.payload?.project_files;
  return (Array.isArray(files) ? files : [])
    .map(name => String(name || '').trim())
    .filter(name => name && !name.includes('/') && !name.includes('\\'));
}

// openLibrarySong is the existing one-click open
// (POST …/library/projects/{entryID}/open). It resolves to the open result or
// throws libraryRequestError. Reuse requestID when retrying the same song, so
// the server finishes what a first attempt left undone instead of starting over.
export async function openLibrarySong(
  homeID,
  entryID,
  { requestID, selectedFile = '', fetchImpl = globalThis.fetch } = {}
) {
  const home = encodeURIComponent(String(homeID || ''));
  const entry = encodeURIComponent(String(entryID || ''));
  const body = { request_id: String(requestID || '') };
  if (selectedFile) body.selected_file = String(selectedFile);
  const response = await fetchImpl(
    `/api/workspaces/${home}/assistant-program/library/projects/${entry}/open`,
    {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    }
  );
  const result = await readLibraryPayload(response);
  if (!response.ok) throw libraryRequestError(response, result);
  return result;
}

// A real save time, or null. Go writes an unknown time as year 1.
function savedDate(isoTime) {
  if (typeof isoTime !== 'string' || !isoTime) return null;
  const date = new Date(isoTime);
  return Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1 ? null : date;
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const plural = (count, word) => `${count} ${word}${count === 1 ? '' : 's'}`;

// lastSavedLabel says when a song's project file was last saved, in the
// viewer's own calendar days: "Saved today", "Saved yesterday", "Saved 3 days
// ago", "Saved 2 weeks ago", "Saved 4 months ago", and "Saved Mar 2024" once it
// is about eleven months old. Empty for a missing or unknown time.
export function lastSavedLabel(isoTime, now = new Date()) {
  const saved = savedDate(isoTime);
  if (!saved) return '';
  const days = Math.round(
    (Date.UTC(now.getFullYear(), now.getMonth(), now.getDate()) -
      Date.UTC(saved.getFullYear(), saved.getMonth(), saved.getDate())) /
      86400000
  );
  // A file saved "in the future" (a clock that ran ahead) is still today's.
  if (days <= 0) return 'Saved today';
  if (days === 1) return 'Saved yesterday';
  if (days < 14) return `Saved ${days} days ago`;
  if (days < 45) return `Saved ${Math.floor(days / 7)} weeks ago`;
  if (days < 335) return `Saved ${plural(Math.max(1, Math.round(days / 30.44)), 'month')} ago`;
  return `Saved ${MONTHS[saved.getMonth()]} ${saved.getFullYear()}`;
}

const positive = value => typeof value === 'number' && Number.isFinite(value) && value > 0;

// songLength is m:ss, or h:mm:ss from an hour on, rounded to the second.
function songLength(seconds) {
  const total = Math.round(seconds);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const secs = String(total % 60).padStart(2, '0');
  return hours > 0 ? `${hours}:${String(minutes).padStart(2, '0')}:${secs}` : `${minutes}:${secs}`;
}

/*
 * Project words (PRD #792 FR6–FR8). A Home's pinned package names what one
 * project is called, the roster's title, the facts' units and any extra
 * fields; the server sends them as `words` on the song-card, roster and
 * summary responses. Ori keeps no domain word for these surfaces: without
 * words every caller gets the neutral "Project" words, never a blank.
 */
export const NEUTRAL_WORDS = Object.freeze({
  source: 'neutral',
  noun: 'Project',
  noun_plural: 'Projects',
  roster_title: 'Projects and assistants',
  facts: Object.freeze([]),
  fields: Object.freeze([])
});

const FACT_KINDS = new Set(['track_count', 'tempo', 'length']);
const wordText = value => (typeof value === 'string' ? value.trim() : '');

/** A complete words object: the given words where valid, neutral otherwise. */
export function projectWords(words) {
  if (!words || typeof words !== 'object') return NEUTRAL_WORDS;
  const noun = wordText(words.noun);
  const nounPlural = wordText(words.noun_plural);
  const rosterTitle = wordText(words.roster_title);
  if (!noun || !nounPlural || !rosterTitle) return NEUTRAL_WORDS;
  const seen = new Set();
  const facts = (Array.isArray(words.facts) ? words.facts : []).filter(fact => {
    const kind = wordText(fact?.kind);
    if (!FACT_KINDS.has(kind) || seen.has(kind)) return false;
    seen.add(kind);
    return true;
  });
  const fields = (Array.isArray(words.fields) ? words.fields : []).filter(
    field => wordText(field?.id) && wordText(field?.label)
  );
  return {
    source: words.source === 'package' ? 'package' : 'neutral',
    noun,
    noun_plural: nounPlural,
    roster_title: rosterTitle,
    facts,
    fields
  };
}

/** The noun in running text: "song", "projects". */
export function lowerNoun(words, { plural: many = false } = {}) {
  const resolved = projectWords(words);
  return (many ? resolved.noun_plural : resolved.noun).toLowerCase();
}

// The host's own names for the three stored facts, used only without
// package facts (neutral order: track count, tempo, length).
const NEUTRAL_FACT_ORDER = ['track_count', 'tempo', 'length'];
const NEUTRAL_FACT_LABEL = { track_count: 'Tracks', tempo: 'Tempo' };

function factValue(facts, kind) {
  if (kind === 'track_count') {
    return positive(facts.track_count) && Number.isInteger(facts.track_count)
      ? String(facts.track_count)
      : '';
  }
  if (kind === 'tempo') {
    return positive(facts.tempo_bpm) ? String(Math.round(facts.tempo_bpm * 10) / 10) : '';
  }
  if (kind === 'length') {
    return positive(facts.length_seconds) && Math.round(facts.length_seconds) > 0
      ? songLength(facts.length_seconds)
      : '';
  }
  return '';
}

const variesNote = (facts, kind) =>
  kind === 'tempo' && facts.tempo_varies === true ? ', varies' : '';

// songFactsLabel words the three numbers read from a project file with the
// Home's words: "14 tracks · 92 BPM · 3:41" and "92 BPM, varies" under the
// Music package's words, "Tracks 14 · Tempo 92 · 3:41" under the neutral ones.
// A part that is unknown is left out (never "0" or "unknown"); no facts is ''.
// These are facts about the file, never a word on how far along the work is.
// It matches the server's projectlibrary.FactsLabel.
export function songFactsLabel(facts, words) {
  if (!facts || typeof facts !== 'object') return '';
  const resolved = projectWords(words);
  const parts = [];
  if (!resolved.facts.length) {
    for (const kind of NEUTRAL_FACT_ORDER) {
      const value = factValue(facts, kind);
      if (!value) continue;
      const label = NEUTRAL_FACT_LABEL[kind];
      parts.push(`${label ? `${label} ${value}` : value}${variesNote(facts, kind)}`);
    }
    return parts.join(' · ');
  }
  for (const fact of resolved.facts) {
    const kind = wordText(fact.kind);
    const value = factValue(facts, kind);
    if (!value) continue;
    let unit = wordText(fact.unit);
    if (unit && value === '1' && wordText(fact.unit_one)) unit = wordText(fact.unit_one);
    parts.push(`${unit ? `${value} ${unit}` : value}${variesNote(facts, kind)}`);
  }
  return parts.join(' · ');
}

// songLineText is a project's one muted line, "Saved … · 14 tracks · 92 BPM ·
// 3:41" (facts already worded by songFactsLabel with the Home's words), with
// each fact kept on one line (non-breaking spaces inside a fact),
// so a narrow card wraps only between facts.
export function songLineText(saved, facts) {
  const unbroken = String(facts || '')
    .split(' · ')
    .map(part => part.replaceAll(' ', '\xa0'))
    .join(' · ');
  return [saved, unbroken].filter(Boolean).join(' · ');
}

// recentSongs keeps the rows a person can open in one click and that have a
// real save time, in the order given (the server sorts by last_saved), at most
// `limit` of them.
export function recentSongs(rows, limit = 6) {
  return (Array.isArray(rows) ? rows : [])
    .filter(row => row?.can_open && savedDate(row.last_saved_at))
    .slice(0, Math.max(0, limit));
}
