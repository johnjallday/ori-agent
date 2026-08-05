package agenthttp

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/types"
)

// characterIdentityRequest is the wire shape for choosing or changing an
// agent's visual identity.
//
// Every field is a pointer so a partial update can change the tone toggle
// without restating the character, matching how the rest of the agent PATCH
// contract behaves. Note there is no catalog_version field: the version is
// server-assigned from the catalog entry, so a client cannot claim to have
// selected an art revision that does not exist (FR-52).
type characterIdentityRequest struct {
	DisplayMode  *string `json:"display_mode,omitempty"`
	CatalogID    *string `json:"catalog_id,omitempty"`
	VoiceEnabled *bool   `json:"voice_enabled,omitempty"`
}

// applyCharacterIdentity validates a character choice and applies it to md.
//
// This is the single authority for character writes. Both create and update go
// through it, so a direct API call gets exactly the same rejections as the UI —
// which is what makes "a working agent can never claim Ori's identity" true
// rather than merely enforced in the browser (FR-19/FR-71/FR-75).
//
// It never touches AvatarImage. Switching to a curated character is a
// presentation change, so the uploaded file survives and the user can switch
// back without re-uploading (FR-64/FR-68).
func applyCharacterIdentity(md *types.AgentMetadata, req *characterIdentityRequest) error {
	if md == nil || req == nil {
		return nil
	}
	if req.DisplayMode == nil && req.CatalogID == nil && req.VoiceEnabled == nil {
		return nil
	}

	cat, err := charactercatalog.Load()
	if err != nil {
		return fmt.Errorf("character catalog unavailable: %w", err)
	}

	// Work on a copy so a rejected request leaves the stored identity untouched.
	next := md.Character.Clone()
	if next == nil {
		next = &types.AgentCharacterIdentity{}
	}

	if req.CatalogID != nil {
		id := strings.TrimSpace(*req.CatalogID)
		switch {
		case id == "":
			// Explicitly clearing the character. The mode is not changed here;
			// if the agent was displaying it, the check below catches that.
			next.CatalogID = ""
			next.CatalogVersion = 0
			next.VoiceEnabled = false
		case !cat.IsAssignable(charactercatalog.CharacterID(id)):
			// One branch covers the unknown ID, the withdrawn ID, and Ori's
			// reserved ID, because none of them are in the assignable set.
			if charactercatalog.CharacterID(id) == cat.ReservedGuideID {
				return fmt.Errorf("character %q is reserved for the app guide and cannot be assigned to an agent", id)
			}
			return fmt.Errorf("unknown character %q", id)
		default:
			entry, _ := cat.Get(charactercatalog.CharacterID(id))
			next.CatalogID = id
			// Server-assigned: the version is whatever the catalog says today.
			next.CatalogVersion = entry.EntryVersion
		}
	}

	if req.DisplayMode != nil {
		mode := types.AgentDisplayMode(strings.TrimSpace(*req.DisplayMode))
		if !types.IsValidDisplayMode(mode) {
			return fmt.Errorf("unknown display mode %q", *req.DisplayMode)
		}
		// Displaying a character the agent does not have would render the
		// fallback while claiming a character is active — a state the Inspector
		// could not explain honestly (FR-124).
		if mode == types.DisplayModeCharacter && strings.TrimSpace(next.CatalogID) == "" {
			return fmt.Errorf("display mode %q requires a character selection", mode)
		}
		if mode == types.DisplayModeUploaded && strings.TrimSpace(md.AvatarImage) == "" {
			return fmt.Errorf("display mode %q requires an uploaded avatar", mode)
		}
		next.DisplayMode = mode
	}

	if req.VoiceEnabled != nil {
		next.VoiceEnabled = *req.VoiceEnabled
	}
	// Tone without a character is meaningless; keep the stored state coherent
	// rather than relying on every reader to re-check the combination.
	if strings.TrimSpace(next.CatalogID) == "" {
		next.VoiceEnabled = false
	}

	// Collapse a fully-default identity back to nil so an agent that never
	// really chose anything does not carry an empty character object forever.
	if next.DisplayMode == "" && next.CatalogID == "" && !next.VoiceEnabled {
		md.Character = nil
		return nil
	}

	md.Character = next
	return nil
}
