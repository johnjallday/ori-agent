package agenthttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/types"
)

// This file is the single authority for appearance writes over HTTP. Create and
// update both go through it, so a direct API call gets exactly the same
// rejections as the browser — which is what makes "a working agent can never
// claim the reserved guide identity" and "a client cannot choose a catalog
// version" true rather than merely enforced in the UI (PRD FR-50/FR-55/FR-58).

// legacyAgentRequestFields are the retired request fields.
//
// They are rejected rather than ignored or translated. Silently ignoring them is
// the worse failure: a script that still sends avatar_color would appear to
// succeed while changing nothing, and the author would have no way to discover
// the contract moved (FR-51).
var legacyAgentRequestFields = map[string]string{
	"avatar_color": "appearance.generated.color",
	"avatar_image": "the appearance upload endpoint",
	"character":    "appearance.character",
	"display_mode": "appearance.mode",
}

// appearanceRequest is the wire shape of the top-level `appearance` object.
//
// The three source fields are raw messages rather than typed pointers because
// this contract has to distinguish three states, not two: omitted (leave
// unchanged), explicitly null (clear), and an object (apply). A typed pointer
// collapses the first two — encoding/json sets a pointer field to nil for JSON
// null, making it indistinguishable from an absent key (FR-53/FR-54).
type appearanceRequest struct {
	Mode      *string         `json:"mode,omitempty"`
	Generated json.RawMessage `json:"generated,omitempty"`
	Uploaded  json.RawMessage `json:"uploaded,omitempty"`
	Character json.RawMessage `json:"character,omitempty"`
}

// The nested fields are raw for the same reason as the outer ones: `"color":
// null` is the documented way to reset the override, and a *string field would
// decode it to nil — identical to the key being absent, which means "leave
// unchanged" (FR-54).
type generatedAppearanceRequest struct {
	Color json.RawMessage `json:"color"`
}

type characterAppearanceRequest struct {
	CatalogID json.RawMessage `json:"catalog_id"`
	// CatalogVersion exists here only so a client that sends it gets a clear
	// rejection instead of having it quietly dropped. It is server-assigned
	// (FR-10/FR-55).
	CatalogVersion json.RawMessage `json:"catalog_version"`
}

// decodeJSONString reads a raw value that must be a JSON string.
func decodeJSONString(raw json.RawMessage, field string) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%s must be a string", field)
	}
	return s, nil
}

// isEmpty reports whether the request object carries no instruction at all,
// which is how `"generated": {}` is distinguished from `"generated": {"color": null}`.
func (r *appearanceRequest) isEmpty() bool {
	return r == nil || (r.Mode == nil && r.Generated == nil && r.Uploaded == nil && r.Character == nil)
}

// isJSONNull reports whether a captured raw value was the literal `null`.
func isJSONNull(raw json.RawMessage) bool {
	return string(trimJSONSpace(raw)) == "null"
}

func trimJSONSpace(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}

// parseAgentRequest reads an agent create/update body, rejects retired fields,
// and decodes it.
//
// It is deliberately agent-local. The shared orihttp.ParseJSONBody ignores
// unknown fields, and every caller in the repository depends on that leniency;
// flipping it globally to make this one contract strict would require auditing
// every handler in the process, which is a much larger change than this feature
// (task note on FR-78).
func parseAgentRequest(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil {
		orihttp.BadRequest(w, "Request body is required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, orihttp.MaxJSONBodySize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			orihttp.BadRequest(w, "Request body too large")
			return false
		}
		orihttp.BadRequest(w, "Failed to read request body")
		return false
	}
	if len(body) == 0 {
		orihttp.BadRequest(w, "Request body is empty")
		return false
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		orihttp.BadRequest(w, "Invalid JSON: "+err.Error())
		return false
	}
	var rejected []string
	for field, replacement := range legacyAgentRequestFields {
		if _, present := probe[field]; present {
			rejected = append(rejected, fmt.Sprintf("%q (use %s)", field, replacement))
		}
	}
	if len(rejected) > 0 {
		sort.Strings(rejected)
		orihttp.BadRequest(w, "This build no longer accepts "+strings.Join(rejected, ", "))
		return false
	}

	if err := json.Unmarshal(body, v); err != nil {
		orihttp.BadRequest(w, "Invalid JSON: "+err.Error())
		return false
	}
	return true
}

// applyAppearanceRequest validates req against a *copy* of the stored appearance
// and returns the result.
//
// Working on a clone is what makes FR-58's "fail atomically" real: a request
// that sets a valid colour and then names an unknown character must leave the
// stored record byte-identical, not half-applied. The caller only adopts the
// returned value on success.
//
// hasStoredUpload is passed in rather than read from the candidate because
// activating Upload must depend on a file the *server* stored, and a JSON
// request cannot write that field at all (FR-8/FR-57).
func applyAppearanceRequest(stored *types.AgentAppearance, req *appearanceRequest) (*types.AgentAppearance, error) {
	next := stored.Clone()
	if next == nil {
		next = types.NewAgentAppearance()
	}
	next.Normalize()

	if req.isEmpty() {
		return next, nil
	}

	if req.Uploaded != nil {
		// The upload endpoint is the only writer. Allowing JSON to name a file
		// would let a caller point an agent at any filename in the avatar
		// directory, including one belonging to another agent (FR-8/FR-55).
		return nil, fmt.Errorf("appearance.uploaded is server-managed; use the appearance upload endpoint")
	}

	if req.Generated != nil {
		if err := applyGeneratedRequest(next, req.Generated); err != nil {
			return nil, err
		}
	}

	if req.Character != nil {
		if err := applyCharacterRequest(next, req.Character); err != nil {
			return nil, err
		}
	}

	if req.Mode != nil {
		mode := types.AppearanceMode(strings.TrimSpace(*req.Mode))
		if !types.IsValidAppearanceMode(mode) {
			return nil, fmt.Errorf("unknown appearance mode %q (expected generated, character, or uploaded)", *req.Mode)
		}
		// Activating a source the agent does not have would render Generated
		// while claiming another source is active — a state no editor could
		// explain honestly (FR-56/FR-57).
		switch mode {
		case types.AppearanceModeCharacter:
			if next.CharacterCatalogID() == "" {
				return nil, fmt.Errorf("appearance mode %q requires a character selection", mode)
			}
		case types.AppearanceModeUploaded:
			if next.UploadedImage() == "" {
				return nil, fmt.Errorf("appearance mode %q requires an uploaded image", mode)
			}
		}
		next.Mode = mode
	}

	next.Normalize()
	return next, nil
}

func applyGeneratedRequest(next *types.AgentAppearance, raw json.RawMessage) error {
	// `"generated": null` and `"generated": {"color": null}` mean the same thing:
	// drop the override and let the deterministic algorithm choose again. There
	// is nothing else inside the object to clear (FR-54).
	if isJSONNull(raw) {
		next.ClearGeneratedColor()
		return nil
	}
	var gen generatedAppearanceRequest
	if err := json.Unmarshal(raw, &gen); err != nil {
		return fmt.Errorf("invalid appearance.generated: %w", err)
	}
	if gen.Color == nil {
		return nil
	}
	if isJSONNull(gen.Color) {
		next.ClearGeneratedColor()
		return nil
	}
	color, err := decodeJSONString(gen.Color, "appearance.generated.color")
	if err != nil {
		return err
	}
	if strings.TrimSpace(color) == "" {
		next.ClearGeneratedColor()
		return nil
	}
	if !next.SetGeneratedColor(color) {
		return fmt.Errorf("invalid appearance.generated.color %q (expected a 3- or 6-digit hex colour)", color)
	}
	return nil
}

func applyCharacterRequest(next *types.AgentAppearance, raw json.RawMessage) error {
	if isJSONNull(raw) {
		next.ClearCharacter()
		return nil
	}
	var ch characterAppearanceRequest
	if err := json.Unmarshal(raw, &ch); err != nil {
		return fmt.Errorf("invalid appearance.character: %w", err)
	}
	if ch.CatalogVersion != nil {
		return fmt.Errorf("appearance.character.catalog_version is server-managed and cannot be set")
	}
	if ch.CatalogID == nil {
		return nil
	}
	if isJSONNull(ch.CatalogID) {
		next.ClearCharacter()
		return nil
	}
	rawID, err := decodeJSONString(ch.CatalogID, "appearance.character.catalog_id")
	if err != nil {
		return err
	}
	id := strings.TrimSpace(rawID)
	if id == "" {
		next.ClearCharacter()
		return nil
	}
	version, err := resolveAssignableCharacter(id)
	if err != nil {
		return err
	}
	next.SetCharacter(id, version)
	return nil
}

// resolveAssignableCharacter validates a catalog ID and returns the version the
// server will record for it.
//
// One branch covers the unknown ID, the withdrawn ID, and the reserved guide ID,
// because none of them are in the assignable set — the guide is called out
// separately only so the error explains itself (FR-25).
func resolveAssignableCharacter(id string) (int, error) {
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

// appearancePayload projects an appearance for an API response.
//
// It always emits the complete canonical object, including a `generated` object
// with no colour, so a client never has to guess whether a missing key means
// "no override" or "field not supported by this build" (FR-2/FR-49/FR-59).
func appearancePayload(a *types.AgentAppearance) map[string]any {
	resolved := a
	if resolved == nil {
		resolved = types.NewAgentAppearance()
	} else {
		resolved = resolved.Clone()
		resolved.Normalize()
	}

	generated := map[string]any{}
	if color := resolved.GeneratedColor(); color != "" {
		generated["color"] = color
	}
	out := map[string]any{
		"mode":      string(resolved.Mode),
		"generated": generated,
	}
	if image := resolved.UploadedImage(); image != "" {
		out["uploaded"] = map[string]any{"image": image}
	}
	if id := resolved.CharacterCatalogID(); id != "" {
		out["character"] = map[string]any{
			"catalog_id":      id,
			"catalog_version": resolved.CharacterCatalogVersion(),
		}
	}
	return out
}

// appearanceForAgent returns the response projection for one agent, resolving a
// nil appearance to Generated.
//
// Read-only agents — CLI backends and the system assistant — go through exactly
// this path. They render like every other agent; what differs is that their
// editors show explanatory copy instead of controls the server would reject
// (FR-44).
func appearanceForAgent(a *agent.Agent) map[string]any {
	if a == nil {
		return appearancePayload(nil)
	}
	return appearancePayload(a.Appearance)
}
