package workspace

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const AssistantGroupTemplateProvenanceSchemaVersion = 1

var ErrAssistantGroupTemplateProvenanceInvalid = errors.New("assistant group template provenance is invalid")

// AssistantGroupTemplateProvenance records which reviewed Group Template
// selection first created a Home. It is inert, bounded creation history: never
// identity (AssistantProgramKey remains the only lookup authority), never a
// display name, and never a project template contract that could drive setup,
// runtime requirements, capability reconciliation, or grants.
type AssistantGroupTemplateProvenance struct {
	SchemaVersion         int                        `json:"schema_version"`
	GroupTemplateID       string                     `json:"group_template_id"`
	GroupTemplateRevision string                     `json:"group_template_revision"`
	SourceKind            string                     `json:"source_kind"`
	TemplateID            string                     `json:"template_id"`
	TemplateRevision      string                     `json:"template_revision,omitempty"`
	VariantID             string                     `json:"variant_id,omitempty"`
	VariantRevision       string                     `json:"variant_revision,omitempty"`
	PluginOwner           *PluginTemplateOwner       `json:"plugin_owner,omitempty"`
	ProgramHomeOwner      *AssistantProgramHomeOwner `json:"program_home_owner,omitempty"`
	HomeDigest            string                     `json:"home_digest"`
	ReviewDigest          string                     `json:"review_digest"`
	CreatedAt             time.Time                  `json:"created_at"`
}

func CloneAssistantGroupTemplateProvenance(source *AssistantGroupTemplateProvenance) *AssistantGroupTemplateProvenance {
	if source == nil {
		return nil
	}
	clone := *source
	if source.PluginOwner != nil {
		owner := source.PluginOwner.Clone()
		clone.PluginOwner = &owner
	}
	if source.ProgramHomeOwner != nil {
		owner := source.ProgramHomeOwner.Clone()
		clone.ProgramHomeOwner = &owner
	}
	return &clone
}

// Validate checks the provenance shape and its consistency with the Home key it
// is about to be written beside. It never consults names or display text.
func (provenance *AssistantGroupTemplateProvenance) Validate(key AssistantProgramKey) error {
	if provenance == nil || provenance.SchemaVersion != AssistantGroupTemplateProvenanceSchemaVersion {
		return ErrAssistantGroupTemplateProvenanceInvalid
	}
	id := provenance.GroupTemplateID
	if !strings.HasPrefix(id, "group-template:") || !lowerHex(strings.TrimPrefix(id, "group-template:"), 32) ||
		!lowerHex(provenance.GroupTemplateRevision, 64) || !lowerHex(provenance.HomeDigest, 64) || !lowerHex(provenance.ReviewDigest, 64) {
		return ErrAssistantGroupTemplateProvenanceInvalid
	}
	for _, value := range []string{provenance.TemplateID, provenance.TemplateRevision, provenance.VariantID, provenance.VariantRevision} {
		if len(value) > 256 || strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return ErrAssistantGroupTemplateProvenanceInvalid
		}
	}
	if strings.TrimSpace(provenance.TemplateID) == "" {
		return ErrAssistantGroupTemplateProvenanceInvalid
	}
	key = key.Normalize()
	switch provenance.SourceKind {
	case "plugin":
		blueprintOwner := provenance.PluginOwner
		homeOwner := provenance.ProgramHomeOwner
		if key.PluginID == "" || (blueprintOwner == nil) == (homeOwner == nil) {
			return ErrAssistantGroupTemplateProvenanceInvalid
		}
		if blueprintOwner != nil && (strings.ToLower(strings.TrimSpace(blueprintOwner.PluginID)) != key.PluginID ||
			len(blueprintOwner.PluginVersion) > 64 || len(blueprintOwner.BlueprintID) > 128) {
			return ErrAssistantGroupTemplateProvenanceInvalid
		}
		if homeOwner != nil && (!homeOwner.Valid() || strings.ToLower(strings.TrimSpace(homeOwner.PluginID)) != key.PluginID ||
			strings.ToLower(strings.TrimSpace(homeOwner.ProgramID)) != key.ProgramID) {
			return ErrAssistantGroupTemplateProvenanceInvalid
		}
	case "user_template":
		if key.TemplateID == "" || provenance.PluginOwner != nil || provenance.ProgramHomeOwner != nil || strings.ToLower(strings.TrimSpace(provenance.TemplateID)) != key.TemplateID {
			return ErrAssistantGroupTemplateProvenanceInvalid
		}
	default:
		return ErrAssistantGroupTemplateProvenanceInvalid
	}
	return nil
}

func lowerHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// EnsureNamedStationWithProvenance creates the canonical Home with an explicit
// display name and records the reviewed Group Template provenance in the same
// first-creation write. Reuse returns the existing Home unchanged: no rename,
// no provenance backfill, and no replacement of provenance from another path.
func (service *AssistantProgramStore) EnsureNamedStationWithProvenance(
	key AssistantProgramKey,
	declaration *AssistantProgramDeclaration,
	name string,
	provenance *AssistantGroupTemplateProvenance,
) (*Workspace, bool, error) {
	if service == nil || service.store == nil || strings.TrimSpace(name) == "" {
		return nil, false, ErrAssistantProgramUnavailable
	}
	if err := provenance.Validate(key); err != nil {
		return nil, false, err
	}
	record := CloneAssistantGroupTemplateProvenance(provenance)
	if record.CreatedAt.IsZero() {
		record.CreatedAt = service.now()
	}
	assistantProgramProvisionMu.Lock()
	defer assistantProgramProvisionMu.Unlock()
	return service.ensureStationWithOptionsLocked(key, declaration, strings.TrimSpace(name), record, record.ProgramHomeOwner)
}
