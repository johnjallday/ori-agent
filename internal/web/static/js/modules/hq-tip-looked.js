/*
 * hq-tip-looked.js — which parts of an HQ the person has already looked at, in
 * this browser only.
 *
 * Pure: no DOM, and storage is passed in (the caller wraps the access itself in
 * try/catch, as railStorage() does). A browser that refuses storage reads as
 * "nothing looked at" and simply forgets; it never throws.
 *
 * This only decides whether a part still carries its mark. It is never read to
 * decide whether a first visit is pending: that is the server's value, and a
 * cleared or blocked store must not be able to bring the tips back to an HQ
 * whose visit is done.
 */

const PART_KEY = /^[a-z-]+$/;

export function tipLookedStorageKey(hqId) {
  return 'ori:hq-tips-looked:' + String(hqId || '').trim();
}

function validKeys(value) {
  return (Array.isArray(value) ? value : []).filter(
    key => typeof key === 'string' && PART_KEY.test(key)
  );
}

/** The part keys looked at in this HQ, as a map `{ key: true }`; `{}` on any failure. */
export function readTipsLooked(storage, hqId) {
  const id = String(hqId || '').trim();
  if (!storage || !id) return {};
  try {
    const raw = storage.getItem(tipLookedStorageKey(id));
    if (typeof raw !== 'string' || !raw) return {};
    return Object.fromEntries(validKeys(JSON.parse(raw)).map(key => [key, true]));
  } catch (_error) {
    return {};
  }
}

/**
 * Remembers that `partKey` was looked at. Returns the map as it now stands (the
 * in-memory result even when the write was refused), so a caller can keep going.
 */
export function writeTipLooked(storage, hqId, partKey) {
  const id = String(hqId || '').trim();
  const key = String(partKey || '');
  const looked = readTipsLooked(storage, id);
  if (!id || !PART_KEY.test(key) || looked[key]) return looked;
  looked[key] = true;
  if (!storage) return looked;
  try {
    storage.setItem(tipLookedStorageKey(id), JSON.stringify(Object.keys(looked)));
  } catch (_error) {
    /* best effort: the person sees the mark once more, nothing breaks */
  }
  return looked;
}

/** Forgets everything looked at in this HQ (the first visit is finished). */
export function clearTipsLooked(storage, hqId) {
  const id = String(hqId || '').trim();
  if (!storage || !id) return;
  try {
    storage.removeItem(tipLookedStorageKey(id));
  } catch (_error) {
    /* best effort */
  }
}
