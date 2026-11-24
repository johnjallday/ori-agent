// files-arrival.js — "Search <folder> in Files": open the merged Files, scoped
// to one searchable folder, in this same tab.
//
// Merge Tree into Files, PR 2 (requirement 41). The workspace page, a creation
// receipt and a folder review all have this button. None of them puts a path,
// a folder name or a search in the address: the folder's source id goes into
// this tab's own Files place on the server (destination-place.js), and Files
// reads it once on arrival and looks the folder up in the filename index's own
// list. Anything else the place holds is still only restored on Back.

import { destinationPlace } from './destination-place.js';

const ARRIVAL = 'arrival';

/** The Files place an arrival saves: an empty search scoped to one folder. */
export function arrivalPlace(sourceId) {
  return {
    term: '',
    scope: JSON.stringify({ kind: ARRIVAL, sourceId: String(sourceId || '') }),
    selected: '',
    pages: ''
  };
}

/** The source id of an arrival saved in a Files place's scope, or ''. */
export function arrivalSource(scopeText) {
  if (!scopeText) return '';
  try {
    const raw = JSON.parse(scopeText);
    return raw && raw.kind === ARRIVAL && typeof raw.sourceId === 'string' ? raw.sourceId : '';
  } catch (_) {
    return '';
  }
}

// Set once this page has saved an arrival and is about to leave for Files.
// Home saves its own Files place as it is left (home-workspace-cockpit.js); a
// creation review or an Explore card on Home must not have that later save
// wipe the arrival it just made.
let arrivalSent = false;

/** Whether this page saved an arrival that is on its way to Files. */
export function arrivalSaved() {
  return arrivalSent;
}

/** Save an arrival in this tab's Files place; false when it could not be saved. */
export async function saveArrival(sourceId) {
  if (!sourceId) return false;
  const place = destinationPlace('files');
  if (!place) return false;
  arrivalSent = true;
  const saved = await place.save(arrivalPlace(sourceId));
  if (!saved || !saved.ok) {
    arrivalSent = false;
    return false;
  }
  return true;
}

/**
 * Open Files scoped to the folder with this source id. Resolves false, and
 * goes nowhere, when the tab's place cannot be saved (another named window,
 * or the Mac that runs Ori did not answer).
 */
export async function openFolderInFiles(sourceId) {
  if (!(await saveArrival(sourceId))) return false;
  window.location.assign('/?view=tree');
  return true;
}
