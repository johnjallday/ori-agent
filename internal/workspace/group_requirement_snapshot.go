package workspace

import (
	"encoding/hex"
	"strings"
	"time"
)

const GroupRequirementSnapshotSchemaVersion = 1

const (
	GroupRequirementCompositionGrouped    = "grouped"
	GroupRequirementCompositionStandalone = "standalone"
)

// GroupRequirementRoleSource records one fixed project-role adaptation selected
// for a standalone creation. It is inert provenance, not an agent definition or
// executable prompt.
type GroupRequirementRoleSource struct {
	RoleID string `json:"role_id"`
}

// GroupRequirementSnapshot is the durable creation-time placement contract.
// It records stable identities and digests only; it contains no path, URL,
// command, credential, permission, prompt, or executable plugin behavior.
type GroupRequirementSnapshot struct {
	SchemaVersion       int                          `json:"schema_version"`
	Policy              string                       `json:"policy"`
	SelectedComposition string                       `json:"selected_composition"`
	TemplateID          string                       `json:"template_id"`
	TemplateRevision    string                       `json:"template_revision"`
	DefinitionDigest    string                       `json:"definition_digest"`
	VariantID           string                       `json:"variant_id,omitempty"`
	VariantRevision     string                       `json:"variant_revision,omitempty"`
	SourcePlugin        *PluginTemplateOwner         `json:"source_plugin,omitempty"`
	SourceDigest        string                       `json:"source_digest,omitempty"`
	StandaloneRoles     []GroupRequirementRoleSource `json:"standalone_roles,omitempty"`
	ProgramKey          *AssistantProgramKey         `json:"program_key,omitempty"`
	HomeWorkspaceID     string                       `json:"home_workspace_id,omitempty"`
	ProjectLinkID       string                       `json:"project_link_id,omitempty"`
	ReviewDigest        string                       `json:"review_digest"`
	OperationDigest     string                       `json:"operation_digest"`
	AppliedAt           time.Time                    `json:"applied_at"`
}

func CloneGroupRequirementSnapshot(source *GroupRequirementSnapshot) *GroupRequirementSnapshot {
	if source == nil {
		return nil
	}
	clone := *source
	clone.StandaloneRoles = append([]GroupRequirementRoleSource(nil), source.StandaloneRoles...)
	if source.SourcePlugin != nil {
		owner := source.SourcePlugin.Clone()
		clone.SourcePlugin = &owner
	}
	if source.ProgramKey != nil {
		key := source.ProgramKey.Normalize()
		clone.ProgramKey = &key
	}
	return &clone
}

// StructurallyValid reports whether the snapshot is an unambiguous v1 marker.
// Legacy workspaces have no snapshot and are never inferred into this contract.
func (snapshot *GroupRequirementSnapshot) StructurallyValid() bool {
	if snapshot == nil || snapshot.SchemaVersion != GroupRequirementSnapshotSchemaVersion ||
		strings.TrimSpace(snapshot.TemplateID) == "" || !validSHA256(snapshot.DefinitionDigest) ||
		!validSHA256(snapshot.ReviewDigest) || !validSHA256(snapshot.OperationDigest) ||
		snapshot.AppliedAt.IsZero() {
		return false
	}
	if snapshot.TemplateRevision != "" && !validSHA256(snapshot.TemplateRevision) {
		return false
	}
	if snapshot.VariantID == "" && snapshot.VariantRevision != "" {
		return false
	}
	if snapshot.SourceDigest != "" && !validSHA256(snapshot.SourceDigest) {
		return false
	}
	switch snapshot.Policy {
	case "none":
		return snapshot.SelectedComposition == GroupRequirementCompositionStandalone &&
			snapshot.ProgramKey == nil && snapshot.HomeWorkspaceID == "" && snapshot.ProjectLinkID == ""
	case "recommended":
		if snapshot.SelectedComposition == GroupRequirementCompositionStandalone {
			return snapshot.ProgramKey == nil && snapshot.HomeWorkspaceID == "" && snapshot.ProjectLinkID == ""
		}
		fallthrough
	case "required":
		return snapshot.SelectedComposition == GroupRequirementCompositionGrouped &&
			snapshot.ProgramKey != nil && snapshot.ProgramKey.Valid() &&
			strings.TrimSpace(snapshot.HomeWorkspaceID) != "" && strings.TrimSpace(snapshot.ProjectLinkID) != ""
	default:
		return false
	}
}

func validSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
