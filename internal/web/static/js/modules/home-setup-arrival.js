/**
 * home-setup-arrival.js — a folder setup that ran a Home's step lands on that
 * Home's Map with `?mode=map&setup_projects=1&setup_beside=<folder workspace>`
 * (the server's folder-agent landing). The Home page opens its projects sheet
 * once for it: beside the folder's tile when the map shows it, else beside the
 * "N to set up" chip, and only when something can be set up
 * (folder-setup-ends-in-home FR5–FR7). Pure helpers; the page wires them.
 */

export const SETUP_PROJECTS_PARAM = 'setup_projects';
export const SETUP_BESIDE_PARAM = 'setup_beside';

// A workspace id as the map writes it into data-ws-id; anything else is
// dropped rather than put into a selector.
const WORKSPACE_ID = /^[A-Za-z0-9_-]{1,80}$/;

/**
 * The arrival a page URL asks for, or null. Only the literal `1` counts, as on
 * the outer map's deep link; `beside` is '' when absent or not an id.
 */
export function setupArrivalFromSearch(search) {
  const params = new URLSearchParams(String(search || ''));
  if (params.get(SETUP_PROJECTS_PARAM) !== '1') return null;
  const beside = String(params.get(SETUP_BESIDE_PARAM) || '').trim();
  return { beside: WORKSPACE_ID.test(beside) ? beside : '' };
}

/**
 * The query string without the arrival's parameters ('' when nothing is
 * left), so a reload or Back never asks for the sheet again (FR7).
 */
export function searchWithoutSetupArrival(search) {
  const params = new URLSearchParams(String(search || ''));
  params.delete(SETUP_PROJECTS_PARAM);
  params.delete(SETUP_BESIDE_PARAM);
  const rest = params.toString();
  return rest ? `?${rest}` : '';
}

/**
 * Whether the sheet opens for this arrival: it was asked for, and the Home's
 * summary counts at least one project ready or needing a file choice (FR6).
 */
export function setupArrivalShouldOpen({ arrival, toSetUp } = {}) {
  return Boolean(arrival) && Number.isInteger(toSetUp) && toSetUp > 0;
}

/**
 * The folder's tile on the Home's map, when it is drawn and has a size; else
 * null (the sheet then opens beside the chip).
 */
export function setupArrivalTile(root, beside) {
  if (!root || typeof root.querySelector !== 'function' || !WORKSPACE_ID.test(String(beside || '')))
    return null;
  const tile = root.querySelector(`[data-ws-id="${beside}"]`);
  if (!tile) return null;
  const rect =
    typeof tile.getBoundingClientRect === 'function' ? tile.getBoundingClientRect() : null;
  return rect && rect.width > 0 && rect.height > 0 ? tile : null;
}
