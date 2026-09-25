/*
 * show-folder-quest.js — Mission 03's start, "Show your assistant a folder".
 *
 * The mission card's Start links to /?quest=show-folder. On that arrival this
 * module opens the assistant's panel on Today and unfolds its folder chooser
 * (personal-assistant-folder.js), then scrubs the query without adding a
 * history entry, so a reload or a Back press does not open it again.
 *
 * What it is:
 *   - Deterministic. No request of its own, no model, no copy: the panel and
 *     the chooser already know what to say. The one request that follows is
 *     the panel's own status refresh on open.
 *   - Focus-only. It opens two things the user could open themselves. It
 *     never scans, decides, creates, or claims the mission complete: the
 *     server completes it from the offer's outcome.
 *   - Patient. The panel opens only once it knows the assistant (its status
 *     load), so an arrival before that waits for the status event and tries
 *     again, once per event, until it opens or the wait runs out.
 */

export const SHOW_FOLDER_QUEST_PARAM = 'show-folder';

// How long an arrival keeps waiting for the panel to become openable. An
// unhired assistant never opens it; the mission is locked until the hire.
export const SHOW_FOLDER_WAIT_MS = 15000;

export function showFolderQuestRequested(search) {
  try {
    return new URLSearchParams(search || '').get('quest') === SHOW_FOLDER_QUEST_PARAM;
  } catch (_) {
    return false;
  }
}

// scrubbedQuestURL returns href without its quest parameter, relative to the
// origin, or null when there is nothing to scrub.
export function scrubbedQuestURL(href) {
  try {
    const url = new URL(href);
    if (!url.searchParams.has('quest')) return null;
    url.searchParams.delete('quest');
    return url.pathname + (url.search || '') + (url.hash || '');
  } catch (_) {
    return null;
  }
}

// startShowFolderQuest opens the panel on Today and then the chooser. Returns
// true once the panel opened; false when it could not yet (no status, no
// assistant), so the caller can try again on the next status.
export function startShowFolderQuest({ panel, folder, launcher } = {}) {
  if (!panel || typeof panel.open !== 'function') return false;
  if (!panel.open(launcher || null, { view: 'today' })) return false;
  if (folder && typeof folder.open === 'function') folder.open();
  return true;
}

function scrubQuestParam() {
  if (typeof window === 'undefined' || !window.history?.replaceState) return;
  const next = scrubbedQuestURL(window.location.href);
  if (!next) return;
  try {
    window.history.replaceState(window.history.state, '', next);
  } catch (_) {
    /* a cosmetic URL tidy must never block the chooser */
  }
}

function init() {
  if (!showFolderQuestRequested(window.location.search)) return;
  scrubQuestParam();

  const attempt = () =>
    startShowFolderQuest({
      panel: window.PersonalAssistantPanel,
      folder: window.PersonalAssistantFolder,
      launcher: document.getElementById('personalAssistantLauncher')
    });

  if (attempt()) return;
  let done = false;
  const stop = () => {
    done = true;
    document.removeEventListener('personal-assistant:status', onStatus);
  };
  const onStatus = () => {
    // The chooser's own status handler runs on this same event; let it
    // finish before the chooser is asked to open.
    setTimeout(() => {
      if (!done && attempt()) stop();
    }, 0);
  };
  document.addEventListener('personal-assistant:status', onStatus);
  setTimeout(() => {
    if (!done) stop();
  }, SHOW_FOLDER_WAIT_MS);
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
