package dailybrief

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// StationURL is the address that opens My HQ with the Daily Brief panel
// already open: /workspaces/<slug>?station=daily-brief. The panel in the Daily
// Brief station is the one place a brief is read, so every surface that says
// "read the brief" links here. The web client builds the same address in
// daily-brief-station.js.
//
// workspaceSlug is the Personal HQ's folder slug. Anything that is not a
// canonical workspace slug yields "" so a caller offers no link rather than a
// wrong one.
func StationURL(workspaceSlug string) string {
	slug := strings.TrimSpace(workspaceSlug)
	if !workspace.IsCanonicalWorkspaceSlug(slug) {
		return ""
	}
	return "/workspaces/" + slug + "?station=daily-brief"
}
