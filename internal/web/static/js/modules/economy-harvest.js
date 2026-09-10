/**
 * economy-harvest.js — the client half of collecting Harvest.
 *
 * Opening a Farm's result is what banks its pending runs (city-economy FR11),
 * and three separate surfaces open a task result modal: the workspace Details
 * page, the Command view, and Home. Rather than three copies of the same fetch,
 * toast, and refresh, they all call through here.
 *
 * It is deliberately fire-and-forget. Banking is a side effect of reading a
 * result — if it fails, the user still got what they opened the modal for, so a
 * failure is logged and never interrupts them. The runs stay pending and the
 * next open collects them.
 *
 * @module economy-harvest
 */

/**
 * Fired on `window` after a successful bank, carrying the new balances. The Home
 * HUD listens for it so the chips move without polling. Anything else that wants
 * to react to the economy changing should listen for this rather than re-fetch
 * on a timer.
 */
export const ECONOMY_CHANGED_EVENT = 'ori:economy-changed';

/**
 * Bank every pending run for one Farm.
 *
 * @param {{workspaceId: string, taskId: string}} target
 * @returns {Promise<{banked: number, craft: number, harvest: number}|null>}
 *   the result, or null when there was nothing to do or the call failed.
 */
export async function bankHarvest({ workspaceId, taskId } = {}) {
  const task = String(taskId || '').trim();
  if (!task) return null;

  let result = null;
  try {
    const response = await fetch('/api/economy/harvest', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ workspace_id: String(workspaceId || ''), task_id: task })
    });
    // 404 is the feature flag being off, which is not an error worth reporting:
    // there is no economy on this install, so there is nothing to collect.
    if (response.status === 404) return null;
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    result = await response.json();
  } catch (err) {
    console.warn('economy-harvest: could not bank pending Harvest', err);
    return null;
  }

  const banked = Number((result && result.banked) || 0);
  if (!banked) return result;

  // The toast is the only feedback for a background side effect the user did not
  // explicitly ask for, so it says both what arrived and what they now have
  // (FR45).
  const harvest = Number((result && result.harvest) || 0);
  if (typeof window !== 'undefined' && window.Toast && typeof window.Toast.success === 'function') {
    window.Toast.success(`Harvested ${banked} · Harvest ${harvest}`);
  }
  notifyEconomyChanged(result);
  return result;
}

/**
 * Tell every listening surface that balances moved.
 *
 * Exported on its own because the price-check paths (Group 4) also change
 * balances, and they should refresh the HUD through the same channel.
 */
export function notifyEconomyChanged(detail) {
  if (typeof window === 'undefined' || typeof window.dispatchEvent !== 'function') return;
  window.dispatchEvent(new CustomEvent(ECONOMY_CHANGED_EVENT, { detail: detail || null }));
}

/**
 * Read `?task=<id>&result=1` out of a query string (FR37).
 *
 * The harvest popover navigates to a workspace with this, and the receiving page
 * opens that task's result — which is what collects the Harvest. Returns '' when
 * the link is not a result deep link, including when `task` is present without
 * `result=1`, which is an ordinary task link and must not open a modal.
 */
export function taskResultDeepLink(search) {
  let params;
  try {
    params = new URLSearchParams(String(search || ''));
  } catch {
    return '';
  }
  const result = String(params.get('result') || '').trim();
  if (result !== '1' && result !== 'true') return '';
  return String(params.get('task') || '').trim();
}

/**
 * Read `?task=<id>&schedule=1` out of a query string (FR42).
 *
 * The harvest popover's Upgrade button sends a user here, and the receiving page
 * opens that task's editor on its schedule section. Like the result link it is
 * one-shot: the parameters are stripped on arrival.
 */
export function taskScheduleDeepLink(search) {
  let params;
  try {
    params = new URLSearchParams(String(search || ''));
  } catch {
    return '';
  }
  const schedule = String(params.get('schedule') || '').trim();
  if (schedule !== '1' && schedule !== 'true') return '';
  return String(params.get('task') || '').trim();
}

// dashboard.js is a plain script, not an ES module, so it cannot import any of
// this. Registering the same functions on window gives it a call site without
// making everything else reach through a global.
if (typeof window !== 'undefined') {
  window.OriEconomy = Object.assign(window.OriEconomy || {}, {
    bankHarvest,
    notifyEconomyChanged,
    taskResultDeepLink,
    taskScheduleDeepLink,
    CHANGED_EVENT: ECONOMY_CHANGED_EVENT
  });
}
