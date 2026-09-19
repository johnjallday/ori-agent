package projecttemplates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const AssistantProjectSchemaVersion = 1

// AssistantProjectDeclaration is a project-blueprint-owned team and an exact
// reference to an independently contributed Home. It contains no Home display,
// stage, reflection, portfolio, capability-install, path, command, or source
// location fields.
type AssistantProjectDeclaration struct {
	SchemaVersion int                           `json:"schema_version"`
	Version       int                           `json:"version"`
	ID            string                        `json:"id"`
	Home          AssistantProjectHomeReference `json:"home"`
	Roles         []AssistantProjectRole        `json:"roles"`
}

type AssistantProjectHomeReference struct {
	ProviderPluginID  string `json:"provider_plugin_id"`
	ProgramID         string `json:"program_id"`
	HomeSchemaVersion int    `json:"home_schema_version"`
	MinHomeVersion    int    `json:"min_home_version"`
	MaxHomeVersion    int    `json:"max_home_version"`
}

// AssistantProjectRole has no scope or capability field. Its component owner
// fixes project scope, and v1 requires every role.
type AssistantProjectRole struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Description  string   `json:"description,omitempty"`
	Required     bool     `json:"required"`
	Primary      bool     `json:"primary,omitempty"`
	Role         string   `json:"role,omitempty"`
	Type         string   `json:"type,omitempty"`
	SystemPrompt string   `json:"system_prompt"`
	Skills       []string `json:"skills,omitempty"`
}

func CloneAssistantProjectDeclaration(source *AssistantProjectDeclaration) *AssistantProjectDeclaration {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Roles = append([]AssistantProjectRole(nil), source.Roles...)
	for index := range clone.Roles {
		clone.Roles[index].Skills = append([]string(nil), source.Roles[index].Skills...)
	}
	return &clone
}

func normalizeAssistantProject(raw json.RawMessage) (*AssistantProjectDeclaration, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var project AssistantProjectDeclaration
	if err := decodeStrictDeclaration([]byte(trimmed), &project); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAssistantProgram, err)
	}
	if project.SchemaVersion != AssistantProjectSchemaVersion || project.Version < 1 {
		return nil, fmt.Errorf("%w: assistant_project schema/version is invalid", ErrInvalidAssistantProgram)
	}
	project.ID = normalizeAssistantProgramID(project.ID)
	project.Home.ProviderPluginID = normalizeAssistantProgramID(project.Home.ProviderPluginID)
	project.Home.ProgramID = normalizeAssistantProgramID(project.Home.ProgramID)
	if !assistantProgramIDPattern.MatchString(project.ID) || !assistantProgramIDPattern.MatchString(project.Home.ProviderPluginID) ||
		!assistantProgramIDPattern.MatchString(project.Home.ProgramID) || project.Home.HomeSchemaVersion != AssistantProgramHomeSchemaVersion ||
		project.Home.MinHomeVersion < 1 || project.Home.MaxHomeVersion < project.Home.MinHomeVersion {
		return nil, fmt.Errorf("%w: assistant_project identity or Home range is invalid", ErrInvalidAssistantProgram)
	}
	if len(project.Roles) == 0 || len(project.Roles) > workspace.AssistantProgramMaxRoles {
		return nil, fmt.Errorf("%w: assistant_project roles must contain 1-%d entries", ErrInvalidAssistantProgram, workspace.AssistantProgramMaxRoles)
	}

	// Reuse all existing role text/prompt/skill validation through a
	// validator-only Assistant Program shell with project scope ownership.
	validation := &workspace.AssistantProgramDeclaration{
		SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "project_validation",
		StationName: "Validation Home", DefaultPrimaryName: "Validation Home Role", HireTitle: "Validation",
		Stages:     []workspace.AssistantProgramStageSpec{{ID: "foundation", Label: "Foundation", AcceptedCompletionThreshold: 0}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 24, MaxProjects: 3, MaxEventsPerProject: 1, MaxCandidates: 1, MaxEvidence: 3, Rubric: "Validation only."},
	}
	seen := make(map[string]struct{}, len(project.Roles))
	primaryCount := 0
	for index, role := range project.Roles {
		if !role.Required || !strictAssistantIDs(role.Skills, 32) || (strings.TrimSpace(role.Role) != "" && !assistantProgramIDPattern.MatchString(role.Role)) ||
			(strings.TrimSpace(role.Type) != "" && !assistantProgramIDPattern.MatchString(role.Type)) {
			return nil, fmt.Errorf("%w: assistant_project role %d is invalid", ErrInvalidAssistantProgram, index)
		}
		id := normalizeAssistantProgramID(role.ID)
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("%w: assistant_project role id is duplicated", ErrInvalidAssistantProgram)
		}
		seen[id] = struct{}{}
		if role.Primary {
			primaryCount++
		}
		validation.Roles = append(validation.Roles, workspace.AssistantProgramRoleSpec{
			ID: role.ID, Label: role.Label, Description: role.Description, Scope: workspace.AssistantRoleScopeProject,
			Required: true, Primary: role.Primary, Role: role.Role, Type: role.Type,
			SystemPrompt: role.SystemPrompt, Skills: append([]string(nil), role.Skills...),
		})
	}
	if primaryCount != 1 {
		return nil, fmt.Errorf("%w: assistant_project requires exactly one primary", ErrInvalidAssistantProgram)
	}
	if err := normalizeAndValidateAssistantProgramScopes(validation, []workspace.AssistantRoleScope{workspace.AssistantRoleScopeProject}); err != nil {
		return nil, err
	}
	project.Roles = make([]AssistantProjectRole, len(validation.Roles))
	for index, role := range validation.Roles {
		project.Roles[index] = AssistantProjectRole{
			ID: role.ID, Label: role.Label, Description: role.Description, Required: true, Primary: role.Primary,
			Role: role.Role, Type: role.Type, SystemPrompt: role.SystemPrompt, Skills: append([]string(nil), role.Skills...),
		}
	}
	return CloneAssistantProjectDeclaration(&project), nil
}

func AssistantProjectDigest(project *AssistantProjectDeclaration) string {
	if project == nil {
		return ""
	}
	encoded, err := json.Marshal(project)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (project *AssistantProjectDeclaration) ProgramRoles() []workspace.AssistantProgramRoleSpec {
	if project == nil {
		return nil
	}
	roles := make([]workspace.AssistantProgramRoleSpec, 0, len(project.Roles))
	for _, role := range project.Roles {
		roles = append(roles, workspace.AssistantProgramRoleSpec{
			ID: role.ID, Label: role.Label, Description: role.Description, Scope: workspace.AssistantRoleScopeProject,
			Required: true, Primary: role.Primary, Role: role.Role, Type: role.Type,
			SystemPrompt: role.SystemPrompt, Skills: append([]string(nil), role.Skills...),
		})
	}
	return roles
}
