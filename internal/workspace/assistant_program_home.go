package workspace

import "strings"

// AssistantProgramHomeOwner is immutable installed-provider provenance for a
// plugin-level independent Home contribution. It is deliberately distinct from
// PluginTemplateOwner because no project blueprint owns this Home.
type AssistantProgramHomeOwner struct {
	PluginID             string `json:"plugin_id"`
	PluginVersion        string `json:"plugin_version"`
	ProgramID            string `json:"program_id"`
	HomeSchemaVersion    int    `json:"home_schema_version"`
	HomeVersion          int    `json:"home_version"`
	DeclarationDigest    string `json:"declaration_digest"`
	PluginGeneration     uint64 `json:"plugin_generation"`
	ComponentFingerprint string `json:"component_fingerprint"`
}

func (owner AssistantProgramHomeOwner) Clone() AssistantProgramHomeOwner { return owner }

// AssistantProjectProviderOwner is immutable project-side provenance for one
// split team declaration inside a trusted plugin blueprint.
type AssistantProjectProviderOwner struct {
	PluginID             string `json:"plugin_id"`
	PluginVersion        string `json:"plugin_version"`
	BlueprintID          string `json:"blueprint_id"`
	BlueprintVersion     int    `json:"blueprint_version"`
	ProjectTeamID        string `json:"project_team_id"`
	ProjectTeamSchema    int    `json:"project_team_schema_version"`
	ProjectTeamVersion   int    `json:"project_team_version"`
	ProjectTeamDigest    string `json:"project_team_digest"`
	PluginGeneration     uint64 `json:"plugin_generation"`
	ComponentFingerprint string `json:"component_fingerprint"`
}

func (owner AssistantProjectProviderOwner) Clone() AssistantProjectProviderOwner { return owner }

func (owner AssistantProjectProviderOwner) Valid() bool {
	return strings.TrimSpace(owner.PluginID) != "" && strings.TrimSpace(owner.PluginVersion) != "" &&
		strings.TrimSpace(owner.BlueprintID) != "" && owner.BlueprintVersion > 0 && strings.TrimSpace(owner.ProjectTeamID) != "" &&
		owner.ProjectTeamSchema == 1 && owner.ProjectTeamVersion > 0 && lowerHex(owner.ProjectTeamDigest, 64) &&
		owner.PluginGeneration > 0 && lowerHex(owner.ComponentFingerprint, 64)
}

func (owner AssistantProgramHomeOwner) Valid() bool {
	return strings.TrimSpace(owner.PluginID) != "" && strings.TrimSpace(owner.PluginVersion) != "" &&
		strings.TrimSpace(owner.ProgramID) != "" && owner.HomeSchemaVersion == 1 && owner.HomeVersion > 0 &&
		lowerHex(owner.DeclarationDigest, 64) && owner.PluginGeneration > 0 && lowerHex(owner.ComponentFingerprint, 64)
}

// EnsureNamedIndependentStation creates/reuses only the exact independent Home
// key and snapshots provider evidence on first creation. Reuse never backfills
// or replaces provenance.
func (service *AssistantProgramStore) EnsureNamedIndependentStation(key AssistantProgramKey, declaration *AssistantProgramDeclaration, name string, owner AssistantProgramHomeOwner) (*Workspace, bool, error) {
	if service == nil || service.store == nil || strings.TrimSpace(name) == "" || !owner.Valid() ||
		owner.PluginID != key.Normalize().PluginID || owner.ProgramID != key.Normalize().ProgramID {
		return nil, false, ErrAssistantProgramUnavailable
	}
	record := owner.Clone()
	assistantProgramProvisionMu.Lock()
	defer assistantProgramProvisionMu.Unlock()
	return service.ensureStationWithOptionsLocked(key, declaration, strings.TrimSpace(name), nil, &record)
}
