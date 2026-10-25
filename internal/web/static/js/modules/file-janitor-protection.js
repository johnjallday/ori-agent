// A review detour carries choices, never permission to apply them. This module
// owns the bounded same-tab descriptor; the console owns canonical restoration.
export const TRIP_KEY = 'ori.fileJanitor.protectionTrip.v1';
export const RETURN_PARAM = 'filing_return';
export const TRIP_TTL = 30 * 60 * 1000;
export const TRIP_MAX_BYTES = 64 * 1024;
const ID = /^[A-Za-z0-9_-]{1,128}$/;
const BINDING = /^[a-f0-9]{64}$/;
const FILTERS = new Set(['', 'pending', 'needs_review', 'skipped']);
const KEYS = [
  'version',
  'kind',
  'nonce',
  'createdAt',
  'workspaceId',
  'rootGeneration',
  'bindingId',
  'batchId',
  'moves',
  'trash',
  'filter',
  'page',
  'focus'
];

export function validateTrip(value, now = Date.now()) {
  if (!value || Object.keys(value).some(key => !KEYS.includes(key))) return null;
  if (value.version !== 1 || value.kind !== 'file-janitor-review') return null;
  for (const key of ['nonce', 'workspaceId', 'rootGeneration', 'batchId']) {
    if (typeof value[key] !== 'string' || !ID.test(value[key])) return null;
  }
  if (
    !BINDING.test(value.bindingId) ||
    !Number.isFinite(value.createdAt) ||
    value.createdAt > now ||
    now - value.createdAt > TRIP_TTL
  )
    return null;
  if (
    !FILTERS.has(value.filter) ||
    !Number.isSafeInteger(value.page) ||
    value.page < 0 ||
    value.page > 1000000 ||
    value.focus !== 'protection'
  )
    return null;
  for (const key of ['moves', 'trash']) {
    if (
      !Array.isArray(value[key]) ||
      value[key].some(id => typeof id !== 'string' || !ID.test(id)) ||
      new Set(value[key]).size !== value[key].length
    )
      return null;
  }
  const moves = new Set(value.moves);
  if (value.trash.some(id => moves.has(id)) || JSON.stringify(value).length > TRIP_MAX_BYTES)
    return null;
  return value;
}

export function protectionSentence(fact) {
  switch (fact?.state) {
    case 'covered':
      return 'Stored evidence reports coverage of this folder.';
    case 'configured':
      return 'A backup source is configured, but Ori has not observed a completed backup.';
    case 'not_covered':
      return 'This folder is not in the backup evidence Ori observes. Other backups may exist.';
    case 'unknown':
      return 'Protection is unknown. Missing evidence does not mean there is no backup.';
    default:
      return 'Protection is unavailable. Reconnect the approved folder to see its saved evidence.';
  }
}

export function createProtectionTrips(win) {
  let memory = null;
  let creatorActive = false;
  let durable = false;
  const dismissed = new Set();
  const consumed = new Set();
  const now = () => Date.now();
  const nonceInURL = () => {
    try {
      return new URL(win.location.href).searchParams.get(RETURN_PARAM) || '';
    } catch (_) {
      return '';
    }
  };
  function read() {
    if (memory && validateTrip(memory, now()) && !consumed.has(memory.nonce)) return memory;
    try {
      const raw = win.sessionStorage?.getItem(TRIP_KEY);
      if (!raw || raw.length > TRIP_MAX_BYTES) return null;
      const value = validateTrip(JSON.parse(raw), now());
      if (!value || consumed.has(value.nonce) || nonceInURL() !== value.nonce) return null;
      memory = value;
      durable = true;
      return value;
    } catch (_) {
      return null;
    }
  }
  function scrub() {
    try {
      const url = new URL(win.location.href);
      url.searchParams.delete(RETURN_PARAM);
      win.history?.replaceState?.(null, '', url.pathname + url.search + url.hash);
    } catch (_) {
      /* no navigation on a malformed URL */
    }
  }
  function consume(nonce) {
    const trip = read();
    if (!trip || trip.nonce !== nonce) return false;
    try {
      const stored = durable && win.sessionStorage?.getItem(TRIP_KEY);
      if (stored) {
        win.sessionStorage.removeItem(TRIP_KEY);
        if (win.sessionStorage.getItem(TRIP_KEY)) return false;
      }
    } catch (_) {
      return false;
    }
    consumed.add(nonce);
    memory = null;
    scrub();
    return true;
  }
  function begin(input) {
    const value = validateTrip(
      {
        ...input,
        version: 1,
        kind: 'file-janitor-review',
        createdAt: now(),
        nonce: win.crypto?.randomUUID?.(),
        focus: 'protection'
      },
      now()
    );
    if (!value)
      throw new Error(
        'These choices cannot fit in a safe return record. Review stays here; open Backup & Recovery in a separate tab instead.'
      );
    memory = JSON.parse(JSON.stringify(value));
    durable = false;
    // A new trip supersedes old trips, even when it remains on this page.
    try {
      win.sessionStorage?.removeItem(TRIP_KEY);
    } catch (_) {
      /* persist verifies before navigation */
    }
    return memory;
  }
  function persist() {
    const value = read();
    if (!value)
      throw new Error(
        'This review return has expired. Return to review before opening another workspace.'
      );
    const raw = JSON.stringify(value);
    try {
      win.sessionStorage.setItem(TRIP_KEY, raw);
      if (win.sessionStorage.getItem(TRIP_KEY) !== raw) throw new Error('unavailable');
      durable = true;
    } catch (_) {
      throw new Error(
        'This browser cannot save the review choices for navigation. Return to review, or open Backup & Recovery in a separate tab. Nothing has been filed.'
      );
    }
    // Browser Back must return through the same canonical validation as an
    // explicit return button, including a bfcache-restored original page.
    if (win.currentWorkspaceId === value.workspaceId) {
      const url = new URL(win.location.href);
      url.searchParams.set(RETURN_PARAM, value.nonce);
      url.searchParams.set('panel', 'file-janitor');
      url.searchParams.set('tab', 'review');
      win.history?.replaceState?.(null, '', url.pathname + url.search + url.hash);
    }
    return value;
  }
  function withNonce(route) {
    const trip = persist();
    const url = new URL(route, win.location.href);
    if (url.origin !== new URL(win.location.href).origin)
      throw new Error('Invalid protection destination.');
    url.searchParams.set(RETURN_PARAM, trip.nonce);
    return url.pathname + url.search;
  }
  function consoleRoute(target) {
    if (
      target?.kind !== 'workspace' ||
      !ID.test(target.workspace_id) ||
      typeof target.slug !== 'string' ||
      !/^[a-z0-9][a-z0-9-]{0,159}$/.test(target.slug)
    )
      throw new Error(
        'That Backup & Recovery destination is unavailable. Return to review and try again.'
      );
    return '/workspaces/' + encodeURIComponent(target.slug) + '?panel=backup-recovery';
  }
  function openBackup(target) {
    const route = consoleRoute(target);
    if (target.workspace_id === win.currentWorkspaceId && win.BackupRecoveryConsole?.open) {
      win.BackupRecoveryConsole.open({ tab: 'overview' });
      return;
    }
    win.location.href = withNonce(route);
  }
  async function returnToReview() {
    const trip = read();
    if (!trip)
      throw new Error(
        'The review return has expired or is unavailable. Open File Janitor to review current files.'
      );
    if (
      trip.workspaceId === win.currentWorkspaceId &&
      win.FileJanitorConsole?.restoreProtectionTrip
    ) {
      win.OriProtectFolders?.close?.({ returning: true });
      win.BackupRecoveryConsole?.close?.();
      return win.FileJanitorConsole.restoreProtectionTrip(trip);
    }
    // Resolve the current canonical slug only after authorization and binding
    // checks. No arbitrary return URL or workspace name lives in storage.
    const response = await win.fetch(
      '/api/workspaces/' + encodeURIComponent(trip.workspaceId) + '/file-janitor/protection'
    );
    if (!response.ok)
      throw new Error(
        'The original workspace is no longer accessible. Review choices were not restored.'
      );
    const fact = await response.json();
    if (
      fact.workspace_id !== trip.workspaceId ||
      fact.root_generation !== trip.rootGeneration ||
      fact.binding_id !== trip.bindingId ||
      !/^[a-z0-9][a-z0-9-]{0,159}$/.test(fact.workspace_slug || '')
    ) {
      consume(trip.nonce);
      throw new Error(
        'The approved folder or its access changed. Open File Janitor and review current files; old choices cannot be restored.'
      );
    }
    win.location.href = withNonce(
      '/workspaces/' + encodeURIComponent(fact.workspace_slug) + '?panel=file-janitor&tab=review'
    );
  }
  function returnButton(doc = win.document) {
    if (!read() && !nonceInURL()) return null;
    const control = doc.createElement('button');
    control.type = 'button';
    control.className = 'br-button br-button-secondary';
    control.textContent = 'Return to filing review';
    control.dataset.filingReturn = 'true';
    control.addEventListener('click', async () => {
      control.disabled = true;
      try {
        await returnToReview();
      } catch (error) {
        const notice = doc.createElement('p');
        notice.setAttribute('role', 'alert');
        notice.textContent = error.message;
        control.after(notice);
      } finally {
        control.disabled = false;
      }
    });
    return control;
  }
  function isDismissed(binding) {
    if (!BINDING.test(binding || '')) return false;
    if (dismissed.has(binding)) return true;
    try {
      return win.localStorage?.getItem('ori.fileJanitor.protectionDismissed.' + binding) === '1';
    } catch (_) {
      return false;
    }
  }
  function dismiss(binding) {
    if (!BINDING.test(binding || '')) return false;
    dismissed.add(binding);
    try {
      const key = 'ori.fileJanitor.protectionDismissed.' + binding;
      win.localStorage.setItem(key, '1');
      return win.localStorage.getItem(key) === '1';
    } catch (_) {
      return false;
    }
  }
  // Called by the existing creator, never a second creation flow. Context is
  // carried only by the explicit chooser entry/deep link and the current trip.
  function creatorOptions() {
    const trip = read();
    if (!trip) return {};
    persist();
    creatorActive = true;
    return {
      stayAfterCreate: true,
      onCreated: async ({ folder, workspaceId }) => {
        creatorActive = false;
        try {
          openBackup({ kind: 'workspace', workspace_id: workspaceId, slug: folder?.folder_slug });
        } catch (error) {
          await returnToReview();
          win.FileJanitorConsole?.protectionFailure?.(error.message);
        }
      },
      filingReturnNonce: trip.nonce
    };
  }
  function creatorFinished() {
    creatorActive = false;
  }
  async function creatorClosed() {
    if (!creatorActive) return;
    creatorActive = false;
    await returnToReview();
  }
  return {
    begin,
    read,
    persist,
    consume,
    scrub,
    openBackup,
    createRoute: () => withNonce('/?create=1&blueprint=backup-and-recovery'),
    returnToReview,
    returnButton,
    isDismissed,
    dismiss,
    creatorOptions,
    creatorFinished,
    creatorClosed
  };
}

if (typeof window !== 'undefined' && typeof document !== 'undefined') {
  window.FileJanitorProtection = createProtectionTrips(window);
  window.FileJanitorProtection.sentence = protectionSentence;
  document.dispatchEvent(new CustomEvent('ori:filing-protection-ready'));
}
