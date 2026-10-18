// workspace-station-link.js — the one address that opens a station's panel in
// a workspace (`/workspaces/<slug>?station=<key>`), and the reader of that
// address. Pure helpers only (no DOM, no network), shared by every surface
// that links to a station: the Daily Brief, the Memory station's run-record
// and Profile links, and the Command view that opens the panel on arrival.

// A workspace slug and a station key have the same shape.
const SLUG = /^[a-z0-9][a-z0-9-]{0,79}$/;

export function isStationKey(key) {
  return SLUG.test(String(key || ''));
}

// stationLink is the address that opens `key`'s panel in the workspace with
// `slug`. An unusable slug or key yields '' so a caller hides its link rather
// than pointing somewhere else.
export function stationLink(slug, key) {
  const workspace = String(slug || '').trim();
  const station = String(key || '').trim();
  if (!isStationKey(workspace) || !isStationKey(station)) return '';
  return `/workspaces/${workspace}?station=${station}`;
}

// stationFromSearch reads `?station=` from a location.search string. Any
// station-shaped key is returned; whether this workspace has that station is
// the page's to decide. Anything else is ''.
export function stationFromSearch(search) {
  let value = '';
  try {
    value = new URLSearchParams(String(search || '')).get('station') || '';
  } catch (_) {
    return '';
  }
  return isStationKey(value) ? value : '';
}

// isStationLink accepts exactly the shape stationLink builds and nothing
// broader: one workspace segment, one parameter. With `key`, only a link to
// that station.
export function isStationLink(href, key) {
  const match = /^\/workspaces\/([^/?#]+)\?station=([^&#]+)$/.exec(String(href || ''));
  if (!match || !isStationKey(match[1]) || !isStationKey(match[2])) return false;
  return key === undefined || match[2] === key;
}
