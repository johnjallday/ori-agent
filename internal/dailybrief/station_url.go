package dailybrief

import "github.com/johnjallday/ori-agent/internal/workspace"

// StationKey is the Daily Brief station's key on the Personal HQ's map.
const StationKey = "daily-brief"

// StationURL is the address that opens My HQ with the Daily Brief panel
// already open: /workspaces/<slug>?station=daily-brief. The panel in the Daily
// Brief station is the one place a brief is read, so every surface that says
// "read the brief" links here.
//
// workspaceSlug is the Personal HQ's folder slug. Anything that is not a
// canonical workspace slug yields "" so a caller offers no link rather than a
// wrong one (workspace.StationURL).
func StationURL(workspaceSlug string) string {
	return workspace.StationURL(workspaceSlug, StationKey)
}
