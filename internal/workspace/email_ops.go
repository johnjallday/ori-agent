package workspace

import "strings"

// EmailOpsTemplateID is the built-in template a dedicated Email Ops workspace is
// created from. It is the single key the portal, the Daily Brief email source,
// and follow-up surfacing all use to find "the user's Email Ops workspace".
const EmailOpsTemplateID = "email-ops"

// EmailOpsWorkspaceSource is the minimal read surface ResolveEmailOpsWorkspace
// needs. Both the full Store and the Daily Brief snapshot's workspace source
// satisfy it. Get must return the hydrated workspace (with template provenance);
// ListActive may return lean records, so resolution re-Gets each candidate.
type EmailOpsWorkspaceSource interface {
	ListActive() ([]*Workspace, error)
	Get(id string) (*Workspace, error)
}

// ResolveEmailOpsWorkspace returns the ID of the user's Email Ops workspace: the
// active, user-owned workspace created from the email-ops template. When several
// exist, the most recently updated wins. Returns "" (no error) when none match.
//
// Provenance lives in the folder store and the lean ListActive record may omit
// it, so each active candidate is re-Get to read hydrated provenance (the same
// pattern the Daily Brief snapshot uses). A workspace with an empty owner is
// treated as the local single-user's, matching the mailbox linker's owner rule.
func ResolveEmailOpsWorkspace(src EmailOpsWorkspaceSource, userID string) (string, error) {
	if src == nil {
		return "", nil
	}
	active, err := src.ListActive()
	if err != nil {
		return "", err
	}
	userID = strings.TrimSpace(userID)
	var best *Workspace
	for _, lean := range active {
		if lean == nil {
			continue
		}
		ws, err := src.Get(lean.ID)
		if err != nil || ws == nil {
			continue
		}
		if owner := strings.TrimSpace(ws.OwnerUserID); owner != "" && !strings.EqualFold(owner, userID) {
			continue
		}
		if !ws.IsFromTemplate(EmailOpsTemplateID) {
			continue
		}
		if best == nil || ws.UpdatedAt.After(best.UpdatedAt) {
			best = ws
		}
	}
	if best == nil {
		return "", nil
	}
	return best.ID, nil
}
