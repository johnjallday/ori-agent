function requireWorkspaceSlug(value) {
  const slug = String(value || '').trim();
  if (!slug) {
    throw new TypeError('workspaceSlug is required');
  }
  return slug;
}

function normalizeSearch(search) {
  if (!search) return '';
  if (search instanceof URLSearchParams) {
    const encoded = search.toString();
    return encoded ? `?${encoded}` : '';
  }
  const value = String(search).trim();
  return value ? (value.startsWith('?') ? value : `?${value}`) : '';
}

function normalizeHash(hash) {
  const value = String(hash || '').trim();
  return value ? (value.startsWith('#') ? value : `#${value}`) : '';
}

/**
 * Build a browser-facing workspace page URL from an explicit folder slug.
 * API URLs intentionally do not use this helper: those remain UUID-based.
 *
 * @param {string} workspaceSlug
 * @param {string[]} [segments]
 * @param {{search?: string|URLSearchParams, hash?: string}} [location]
 * @returns {string}
 */
export function workspacePageURL(workspaceSlug, segments = [], location = {}) {
  const slug = requireWorkspaceSlug(workspaceSlug);
  const suffix = Array.from(segments || [], segment => {
    const value = String(segment ?? '').trim();
    if (!value) throw new TypeError('workspace route segments cannot be empty');
    return encodeURIComponent(value);
  });
  const path = ['/workspaces', encodeURIComponent(slug), ...suffix].join('/');
  return `${path}${normalizeSearch(location.search)}${normalizeHash(location.hash)}`;
}

export function workspaceRootURL(workspaceSlug, location = {}) {
  return workspacePageURL(workspaceSlug, [], location);
}
