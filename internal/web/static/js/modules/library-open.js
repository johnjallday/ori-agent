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
// row keeps its Details, which explain why.
export function libraryOpenAction(row, readOnly) {
  if (readOnly || !row?.can_open) return null;
  const name = String(row.name || 'this project');
  return {
    label: 'Open',
    ariaLabel:
      row.connection === 'connected'
        ? `Open ${name}`
        : `Open ${name}: makes its workspace and adds your assistant`
  };
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
    case 'off':
      return `${song} is ready. Adding your assistant is switched off, so no agent was added.`;
    case 'consent_stale':
      return `${song} is ready. Your assistant changed in a plugin update; review it on this Home to add it.`;
    case 'assistant_missing':
      return `${song} is ready. Your shared assistant is gone, so no agent was added.`;
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

// songFactsLabel words the three numbers read from a song's project file:
// "14 tracks · 92 BPM · 3:41", "92 BPM, varies" when the tempo changes. A part
// that is unknown is left out (never "0" or "unknown"); no facts is ''. These are
// facts about the file, never a word on how far along the song is.
export function songFactsLabel(facts) {
  if (!facts || typeof facts !== 'object') return '';
  const parts = [];
  if (positive(facts.track_count) && Number.isInteger(facts.track_count)) {
    parts.push(plural(facts.track_count, 'track'));
  }
  if (positive(facts.tempo_bpm)) {
    const tempo = `${Math.round(facts.tempo_bpm * 10) / 10} BPM`;
    parts.push(facts.tempo_varies === true ? `${tempo}, varies` : tempo);
  }
  if (positive(facts.length_seconds) && Math.round(facts.length_seconds) > 0) {
    parts.push(songLength(facts.length_seconds));
  }
  return parts.join(' · ');
}

// songLineText is a song's one muted line, "Saved … · 14 tracks · 92 BPM ·
// 3:41", with each fact kept on one line (non-breaking spaces inside a fact),
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
