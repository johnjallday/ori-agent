// A tab-local navigation hint, never a receipt. Owners must read their own
// state before claiming success. Starting another attempt invalidates this one.
const KEY = 'ori.hq.guidedAttempt.v1';
const MAX_AGE = 2 * 60 * 60 * 1000;
const paths = new Set(['folder', 'email', 'apps']);
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
      Date.now() - hint.at > MAX_AGE
    )
      return null;
    return { hq, path, attempt, at: hint.at };
  } catch (_) {
    return null;
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
