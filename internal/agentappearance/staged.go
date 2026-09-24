// Package agentappearance validates the appearance a create flow stages for an
// agent that does not exist yet.
//
// Three create paths accept one: POST /api/agents, the Create Workspace
// wizard's role staffing and blueprint overrides, and the assistant program's
// staffing adapter. They live in different packages, so the rules live here,
// below all of them, rather than being copied into each — the same reason the
// character catalog is the single authority on which characters may be worn.
package agentappearance

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/types"
)

// ResolveAssignableCharacter validates a catalog ID and returns the version
// the server records for it.
//
// One branch covers the unknown ID, the withdrawn ID, and the reserved guide
// ID, because none of them are in the assignable set — the guide is called out
// separately only so the error explains itself (FR-25).
func ResolveAssignableCharacter(id string) (int, error) {
	cat, err := charactercatalog.Load()
	if err != nil {
		return 0, fmt.Errorf("character catalog unavailable: %w", err)
	}
	cid := charactercatalog.CharacterID(id)
	if !cat.IsAssignable(cid) {
		if cid == cat.ReservedGuideID {
			return 0, fmt.Errorf("character %q is reserved for the app guide and cannot be assigned to an agent", id)
		}
		return 0, fmt.Errorf("unknown character %q", id)
	}
	entry, ok := cat.Get(cid)
	if !ok {
		return 0, fmt.Errorf("unknown character %q", id)
	}
	return entry.EntryVersion, nil
}

// ValidateStaged checks an appearance staged for an agent that is about to be
// created and returns the canonical record to store. A nil request means the
// caller chose nothing, and nil comes back: the blueprint's own appearance, or
// the generated default, applies.
//
// Only Generated and Character can be staged. An upload has nowhere to go
// until the agent exists, so it is a separate call to the agent's upload
// endpoint afterwards, never part of a create (FR-46/FR-57). The catalog
// version is server-assigned from the validated entry, exactly as on the agent
// create endpoint (FR-10/FR-55).
func ValidateStaged(req *types.AgentAppearance) (*types.AgentAppearance, error) {
	if req == nil {
		return nil, nil
	}
	if req.Uploaded != nil && strings.TrimSpace(req.Uploaded.Image) != "" {
		return nil, fmt.Errorf("appearance.uploaded cannot be staged for a new agent; upload an image once it exists")
	}

	next := types.NewAgentAppearance()
	if req.Generated != nil {
		color := strings.TrimSpace(req.Generated.Color)
		if color != "" && !next.SetGeneratedColor(color) {
			return nil, fmt.Errorf("invalid appearance.generated.color %q (expected a 3- or 6-digit hex colour)", req.Generated.Color)
		}
	}
	if req.Character != nil {
		id := strings.TrimSpace(req.Character.CatalogID)
		if id == "" && req.Character.CatalogVersion != 0 {
			return nil, fmt.Errorf("appearance.character.catalog_version is server-managed and cannot be set")
		}
		if id != "" {
			version, err := ResolveAssignableCharacter(id)
			if err != nil {
				return nil, err
			}
			// A client cannot choose the version. The one value accepted is
			// the server's own, so that validating an already-canonical record
			// again (a create request is normalized at its readiness review
			// and once more at creation) is a no-op rather than a refusal.
			if req.Character.CatalogVersion != 0 && req.Character.CatalogVersion != version {
				return nil, fmt.Errorf("appearance.character.catalog_version is server-managed and cannot be set")
			}
			next.SetCharacter(id, version)
		}
	}

	mode := types.AppearanceMode(strings.TrimSpace(string(req.Mode)))
	if mode == "" {
		mode = types.AppearanceModeGenerated
	}
	switch mode {
	case types.AppearanceModeGenerated:
	case types.AppearanceModeCharacter:
		if next.CharacterCatalogID() == "" {
			return nil, fmt.Errorf("appearance mode %q requires a character selection", mode)
		}
	case types.AppearanceModeUploaded:
		return nil, fmt.Errorf("appearance mode %q cannot be staged for a new agent; upload an image once it exists", mode)
	default:
		return nil, fmt.Errorf("unknown appearance mode %q (expected generated or character)", req.Mode)
	}
	next.Mode = mode
	next.Normalize()
	return next, nil
}
