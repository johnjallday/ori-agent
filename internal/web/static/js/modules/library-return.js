// A saved song that needs a reviewed integration sends the person to that
// integration's own install quest and, afterwards, back to the same song. The
// hint that makes the second half possible is navigation only. It names the Home
// page to return to and an opaque song ID, expires after an hour, and is
// validated here every time it is read. It never carries a path to a project,
// never installs anything, and never authorizes a project connection: the Home
// re-reads the song and its eligibility from the server on return, and every
// consequence still has its own review.
//
// Stored per tab (sessionStorage), so a hint written elsewhere cannot steer
// another tab, and a missing or unreadable store simply means "no hint".
const KEY = 'ori:library-return';
const MAX_AGE_MS = 60 * 60 * 1000;
// A Home's own assistant page, nothing else: no query, no hash, no other route.
const HOME_PATH = /^\/workspaces\/[A-Za-z0-9][A-Za-z0-9._~-]{0,127}\/assistant$/;
const QUEST_ID = /^[a-z0-9][a-z0-9_-]{0,63}$/;

const cleanID = value =>
  typeof value === 'string' && value.length > 0 && value.length <= 160 && value.trim() === value;

function storage() {
  try {
    return globalThis.sessionStorage || null;
  } catch (_) {
    return null;
  }
}

// validLibraryReturn is the only reader: anything malformed, stale, or shaped
// like something else is "no hint".
export function validLibraryReturn(value, now = Date.now()) {
  if (!value || typeof value !== 'object') return null;
  const { home_id: homeID, entry_id: entryID, quest_id: questID, return_path: path } = value;
  const created = Number(value.created_at);
  if (
    !cleanID(homeID) ||
    !cleanID(entryID) ||
    typeof questID !== 'string' ||
    !QUEST_ID.test(questID) ||
    typeof path !== 'string' ||
    !HOME_PATH.test(path) ||
    !Number.isFinite(created) ||
    created > now + 60_000 ||
    now - created > MAX_AGE_MS
  ) {
    return null;
  }
  return {
    home_id: homeID,
    entry_id: entryID,
    quest_id: questID,
    return_path: path,
    created_at: created
  };
}

export function writeLibraryReturn({ homeID, entryID, questID, path }, now = Date.now()) {
  const hint = validLibraryReturn(
    { home_id: homeID, entry_id: entryID, quest_id: questID, return_path: path, created_at: now },
    now
  );
  const store = storage();
  if (!hint || !store) return false;
  try {
    store.setItem(KEY, JSON.stringify(hint));
    return true;
  } catch (_) {
    return false;
  }
}

export function readLibraryReturn(now = Date.now()) {
  const store = storage();
  if (!store) return null;
  try {
    const raw = store.getItem(KEY);
    if (!raw) return null;
    const hint = validLibraryReturn(JSON.parse(raw), now);
    if (!hint) store.removeItem(KEY); // a malformed or expired hint is dropped
    return hint;
  } catch (_) {
    return null;
  }
}

export function clearLibraryReturn() {
  try {
    storage()?.removeItem(KEY);
  } catch (_) {
    // nothing to clear
  }
}

// libraryReturnURL is where "back to your song" navigates: the Home's own page,
// opened at its library. The song is found by the Home itself.
export function libraryReturnURL(hint) {
  const valid = validLibraryReturn(hint);
  return valid ? `${valid.return_path}#projectLibraryPanel` : '';
}
