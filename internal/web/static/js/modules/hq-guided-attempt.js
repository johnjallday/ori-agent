// A tab-local navigation hint, never a receipt. Owners must read their own
// state before claiming success. Starting another attempt invalidates this one.
const KEY = 'ori.hq.guidedAttempt.v1';
const MAX_AGE = 2 * 60 * 60 * 1000;
const paths = new Set(['folder', 'email', 'apps', 'workspace']);
const identifier = value => typeof value === 'string' && /^[a-z\d][a-z\d_-]{0,127}$/i.test(value);

export function beginHQAttempt(hq, path) {
  if (!identifier(hq) || !paths.has(path)) return null;
  try {
    const hint = { hq, path, attempt: crypto.randomUUID(), at: Date.now() };
    sessionStorage.setItem(KEY, JSON.stringify(hint));
    return hint;
  } catch (_) {
    return null;
  }
}

export function currentHQAttempt(hq, path, attempt) {
  try {
    const hint = JSON.parse(sessionStorage.getItem(KEY));
    if (
      !hint ||
      !identifier(hq) ||
      hq !== hint.hq ||
      path !== hint.path ||
      !paths.has(path) ||
      !identifier(attempt) ||
      attempt !== hint.attempt ||
      !Number.isSafeInteger(hint.at) ||
      hint.at > Date.now() ||
      Date.now() - hint.at > (path === 'workspace' ? 15 * 60 * 1000 : MAX_AGE)
    )
      return null;
    return {
      hq,
      path,
      attempt,
      at: hint.at,
      ...(path === 'workspace' && hint.arrived === true ? { arrived: true } : {}),
      ...(hint.returned === true ? { returned: true } : {}),
      ...(identifier(hint.workspace) ? { workspace: hint.workspace } : {}),
      ...(path === 'email' && identifier(hint.account) ? { account: hint.account } : {})
    };
  } catch (_) {
    return null;
  }
}

export function guidedHint(search, currentHQ) {
  const params = new URLSearchParams(search);
  if (currentHQ && params.has('hq') && params.get('hq') !== currentHQ) return null;
  return currentHQAttempt(
    currentHQ || params.get('hq'),
    params.get('guided'),
    params.get('attempt')
  );
}

// Only a journey that actually returned can reappear on a later plain HQ
// entry. An explicit, even invalid, deep link never falls back to a past run.
export function receiptHint(location, hq) {
  const p = new URLSearchParams(location?.search);
  if (p.has('guided') || p.has('attempt')) return guidedHint(p, hq);
  if (location?.hash || [...p.keys()].some(key => key !== 'mode')) return null;
  try {
    const last = JSON.parse(sessionStorage.getItem(KEY));
    return last?.returned === true ? currentHQAttempt(hq, last.path, last.attempt) : null;
  } catch (_) {
    return null;
  }
}
export function claimWorkspaceArrival(search) {
  const hint = guidedHint(search);
  if (hint?.path !== 'workspace' || hint.arrived) return null;
  try {
    sessionStorage.setItem(KEY, JSON.stringify({ ...hint, arrived: true }));
    return hint;
  } catch (_) {
    return null;
  }
}

export function markReturned(hint) {
  const current = currentHQAttempt(hint.hq, hint.path, hint.attempt);
  if (!current || current.returned) return;
  try {
    sessionStorage.setItem(KEY, JSON.stringify({ ...current, returned: true }));
  } catch (_) {
    /* optional navigation hint */
  }
}

export function handoffURL(url, hint) {
  if (!hint) return url;
  const dest = new URL(url, 'https://ori.invalid');
  dest.searchParams.set('hq', hint.hq);
  dest.searchParams.set('guided', hint.path);
  dest.searchParams.set('attempt', hint.attempt);
  return dest.pathname + dest.search + dest.hash;
}

export function recordFolderWorkspace(hint, workspace) {
  if (!hint || hint.path !== 'folder' || !identifier(workspace)) return;
  const current = currentHQAttempt(hint.hq, hint.path, hint.attempt);
  if (!current || current.workspace) return;
  try {
    sessionStorage.setItem(KEY, JSON.stringify({ ...current, workspace }));
  } catch (_) {
    /* no verified return hint */
  }
}

export function bindEmailAttempt(hint, workspace, account) {
  if (!hint || !identifier(workspace) || (account && !identifier(account))) return false;
  const current = currentHQAttempt(hint.hq, 'email', hint.attempt);
  if (
    !current ||
    (current.workspace && current.workspace !== workspace) ||
    (current.account && account && current.account !== account)
  )
    return false;
  try {
    sessionStorage.setItem(
      KEY,
      JSON.stringify({ ...current, workspace, ...(account ? { account } : {}) })
    );
    return true;
  } catch (_) {
    return false;
  }
}

export async function showHQReturnLinks(hint) {
  if (!hint) return;
  try {
    const response = await fetch('/api/personal-hq/status');
    if (!response.ok) return;
    const href = guidedReturnURL(hint, (await response.json()).status);
    if (!href) return;
    for (const anchor of document.querySelectorAll('[data-guided-return]')) {
      anchor.href = href;
      anchor.hidden = false;
      anchor.onclick = event => {
        if (!event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey)
          window.PersonalAssistantPanel?.close?.({ restoreFocus: false });
      };
    }
  } catch (_) {
    /* no verified HQ destination */
  }
}

// Deliberately no return URL in storage or accepted from the address bar.
export function guidedReturnURL(hint, status) {
  if (
    !hint ||
    !currentHQAttempt(hint.hq, hint.path, hint.attempt) ||
    !status?.valid ||
    status.workspace_id !== hint.hq ||
    !identifier(status.workspace?.folder_slug)
  )
    return '';
  return `/workspaces/${encodeURIComponent(status.workspace.folder_slug)}?mode=details&guided=${hint.path}&attempt=${hint.attempt}`;
}
