/**
 * my-notes-arrival.js — a link that opens one note in My Notes, and reading
 * it back on arrival.
 *
 * The link is a fragment on /my-notes, so the note's name never reaches a
 * server log or a navigation query:
 *
 *   /my-notes#ref=<browse_ref>&kind=<inbox|daily|project>&name=<one name>
 *
 * The older Inbox capture receipt (`#ref=…&note=<file>.md`) keeps working.
 * Arrival opens the note only for the notebook it was made for (the same
 * `browse_ref`, a notebook Ori set up), by an allowlisted kind and a single
 * validated name, never a path. Anything else falls back to the notebook with
 * a sentence saying why.
 */

import { MY_NOTES_URL } from './hq-quick-capture.js';
import { isCreatedByOri } from './notes-station.js';

// Where each kind lives, and which file in it is the note.
const KINDS = Object.freeze({
  inbox: Object.freeze({ folder: 'Inbox' }),
  daily: Object.freeze({ folder: 'Daily' }),
  project: Object.freeze({ folder: 'Projects', file: 'README.md' })
});

export const MY_NOTES_LINK_KINDS = Object.freeze(Object.keys(KINDS));

export const ARRIVAL_STALE_LINE =
  'That note link was made for an earlier notebook connection, so Ori opened your notebook instead.';
export const ARRIVAL_INVALID_LINE =
  'That note link is not one Ori can open, so Ori opened your notebook instead.';
export const ARRIVAL_MISSING_LINE =
  'That note’s folder is no longer in your notebook, so Ori opened the top of it instead.';

const byteLength = text => new TextEncoder().encode(text).length;

// One folder or file name, by the server's own rules: no separators, not
// hidden, no control characters, no trailing dot, at most 255 bytes.
function oneName(name) {
  const text = typeof name === 'string' ? name : '';
  return (
    text !== '' &&
    text === text.trim() &&
    byteLength(text) <= 255 &&
    !text.startsWith('.') &&
    !text.endsWith('.') &&
    !/[/\\:]/u.test(text) &&
    // eslint-disable-next-line no-control-regex
    !/[\u0000-\u001f\u007f]/u.test(text)
  );
}

export function validNoteLinkName(kind, name) {
  if (!Object.hasOwn(KINDS, kind) || !oneName(name)) return false;
  if (kind === 'project') return true;
  return /\.(?:md|markdown)$/iu.test(name) && !/^readme\.md$/iu.test(name);
}

// myNotesNoteURL is the link for one note, or '' when the parts are not one.
export function myNotesNoteURL({ ref, kind, name }, base = MY_NOTES_URL) {
  if (!/^[0-9a-f]{64}$/u.test(String(ref || '')) || !validNoteLinkName(kind, name)) return '';
  return base + '#' + new URLSearchParams({ ref, kind, name });
}

// myNotesArrivalNote reads a fragment's parameters against the connection:
// null without a note link; { folder, note } to open; or { problem, message }.
export function myNotesArrivalNote(receipt, view) {
  if (!receipt || typeof receipt.get !== 'function') return null;
  const legacy = receipt.get('note');
  const kind = receipt.get('kind') ?? (legacy !== null ? 'inbox' : null);
  const name = receipt.get('name') ?? legacy;
  if (kind === null && name === null) return null;
  if (!validNoteLinkName(kind, name)) return { problem: 'invalid', message: ARRIVAL_INVALID_LINE };
  const ref = receipt.get('ref') || '';
  if (!isCreatedByOri(view) || !view?.browse_ref || ref !== view.browse_ref) {
    return { problem: 'stale', message: ARRIVAL_STALE_LINE };
  }
  const place = KINDS[kind];
  return place.file
    ? { kind, name, folder: [place.folder, name], note: place.file }
    : { kind, name, folder: [place.folder], note: name };
}
