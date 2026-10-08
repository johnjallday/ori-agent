/*
 * workspace-command-rail.js — which section of the Command view's right rail is
 * open when a workspace is first shown in Details, and where that choice is
 * remembered.
 *
 * Pure: no DOM, and storage is passed in. workspace-command.js owns the rail
 * itself and only asks these functions what to open and what to remember.
 */

// The sections that hold the user's own content, in rail order. Systems is
// deliberately not one: its count is its tabs, and opening it widens the
// layout, so it is never opened on the user's behalf. Stations is the HQ
// fallback below, not content.
const CONTENT_SECTIONS = [
  'backlog',
  'notes',
  'schedules',
  'sessions',
  'folders',
  'members',
  'files'
];

/**
 * The section to open when a workspace is first shown in Details.
 *
 * `saved` distinguishes "never chose" (null or undefined) from "chose to have
 * nothing open" (''): the second is a choice and is kept; the first falls
 * through to the default.
 *
 * @param {object} input
 * @param {string|null} [input.saved] - the remembered section key
 * @param {{key: string, count: number}[]} [input.sections] - the rows this
 *   workspace's rail shows, in order
 * @param {boolean} [input.isHQ]
 * @returns {string} a section key, or '' for nothing open
 */
export function resolveRailSection({ saved = null, sections = [], isHQ = false } = {}) {
  const present = Array.isArray(sections) ? sections : [];
  const has = key => present.some(section => section.key === key);

  if (saved === '') return '';
  // A remembered section this workspace does not have (Detachment outside a
  // group, Stations outside the HQ) is ignored rather than opening nothing.
  if (saved && has(saved)) return saved;

  const firstWithContent = present.find(
    section => CONTENT_SECTIONS.includes(section.key) && Number(section.count) > 0
  );
  if (firstWithContent) return firstWithContent.key;

  if (isHQ && has('stations')) return 'stations';
  return '';
}

export function railSectionStorageKey(workspaceId) {
  return 'ori:command-rail-section:' + String(workspaceId || '').trim();
}

/**
 * The remembered section for a workspace: a key, '' when the user closed the
 * rail, or null when nothing is remembered or storage cannot be read.
 */
export function readRailSection(storage, workspaceId) {
  const id = String(workspaceId || '').trim();
  if (!storage || !id) return null;
  try {
    const value = storage.getItem(railSectionStorageKey(id));
    return typeof value === 'string' ? value : null;
  } catch (_error) {
    return null;
  }
}

/** Remembers the open section ('' for none). A browser that refuses storage simply forgets. */
export function writeRailSection(storage, workspaceId, key) {
  const id = String(workspaceId || '').trim();
  if (!storage || !id) return;
  try {
    storage.setItem(railSectionStorageKey(id), String(key || ''));
  } catch (_error) {
    /* best effort */
  }
}
