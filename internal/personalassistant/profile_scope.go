package personalassistant

import (
	"context"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityAttachmentReader resolves installation-local attachment authority.
type ContinuityAttachmentReader interface {
	Attachment(context.Context, string) (workspacecontinuity.Attachment, error)
}

// ImportedProfileReader is an optional read-only seam for one receipt-owned
// HQ. handled means this workspace has an imported attachment (including a
// restoring/denied one); when true, a missing scoped profile MUST NOT fall back
// to a same-named global or neighboring agent. Native relationships keep the
// original roster provenance behavior.
type ImportedProfileReader interface {
	ImportedProfileProvenance(ctx context.Context, workspaceID, name string) (provenance ProfileProvenance, found, handled bool)
}

func relationshipProfileProvenance(ctx context.Context, reader ProfileReader, state *State) (ProfileProvenance, bool) {
	if reader == nil || state == nil {
		return ProfileProvenance{}, false
	}
	if scoped, ok := reader.(ImportedProfileReader); ok && state.HQWorkspaceID != "" {
		profile, found, handled := scoped.ImportedProfileProvenance(ctx, state.HQWorkspaceID, state.GlobalAgentProfileName)
		if handled {
			return profile, found
		}
	}
	return reader.PersonalAssistantProfileProvenance(state.GlobalAgentProfileName)
}
