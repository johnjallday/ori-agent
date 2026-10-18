package workspace

import "strings"

// StationURL is the address that opens a workspace's page with one station's
// panel already open: /workspaces/<slug>?station=<key>. Every surface that
// says "read the brief" or "see that workspace's memory" links here; the web
// client builds the same address in workspace-station-link.js.
//
// Both the workspace slug and the station key must be canonical slugs
// ("my-hq", "daily-brief", "memory"); anything else yields "" so a caller
// offers no link rather than a wrong one.
func StationURL(workspaceSlug, stationKey string) string {
	slug := strings.TrimSpace(workspaceSlug)
	key := strings.TrimSpace(stationKey)
	if !IsCanonicalWorkspaceSlug(slug) || !IsCanonicalWorkspaceSlug(key) {
		return ""
	}
	return "/workspaces/" + slug + "?station=" + key
}
