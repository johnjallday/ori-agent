package projecttemplates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	AssistantProgramHomeSchemaVersion = 1
	AssistantProgramHomeMaxEntries    = 8
)

// AssistantProgramHome is a plugin-level, Home-only Assistant Program
// contribution. It is inert declaration data; plugin validation converts it to
// the existing durable AssistantProgramDeclaration shape without inventing a
// project role.
type AssistantProgramHome struct {
	SchemaVersion                  int                                        `json:"schema_version"`
	Version                        int                                        `json:"version"`
	ID                             string                                     `json:"id"`
	StationName                    string                                     `json:"station_name"`
	StationDescription             string                                     `json:"station_description,omitempty"`
	DefaultPrimaryName             string                                     `json:"default_primary_name"`
	HireTitle                      string                                     `json:"hire_title"`
	HireDescription                string                                     `json:"hire_description,omitempty"`
	DisabledMessage                string                                     `json:"disabled_message,omitempty"`
	SuggestionRequiredCapabilities []string                                   `json:"suggestion_required_capabilities,omitempty"`
	Roles                          []AssistantProgramHomeRole                 `json:"roles"`
	Stages                         []workspace.AssistantProgramStageSpec      `json:"stages"`
	Reflection                     workspace.AssistantReflectionConfig        `json:"reflection"`
	AllowedProjectAttachments      []AssistantProgramAllowedProjectAttachment `json:"allowed_project_attachments,omitempty"`
}

// AssistantProgramHomeRole deliberately has no scope field: its component
// ownership fixes every role to the Home scope.
type AssistantProgramHomeRole struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Description  string   `json:"description,omitempty"`
	Required     bool     `json:"required"`
	CapabilityID string   `json:"capability_id,omitempty"`
	Primary      bool     `json:"primary,omitempty"`
	Role         string   `json:"role,omitempty"`
	Type         string   `json:"type,omitempty"`
	SystemPrompt string   `json:"system_prompt"`
	Skills       []string `json:"skills,omitempty"`
}

// AssistantProgramAllowedProjectAttachment is authorization, not a dependency
// or install request. Source URLs, plugin versions and workspace names are
// intentionally absent.
type AssistantProgramAllowedProjectAttachment struct {
	ProviderPluginID         string `json:"provider_plugin_id"`
	BlueprintID              string `json:"blueprint_id"`
	ProjectTeamID            string `json:"project_team_id"`
	ProjectTeamSchemaVersion int    `json:"project_team_schema_version"`
	MinProjectTeamVersion    int    `json:"min_project_team_version"`
	MaxProjectTeamVersion    int    `json:"max_project_team_version"`
}

func CloneAssistantProgramHome(source AssistantProgramHome) AssistantProgramHome {
	clone := source
	clone.SuggestionRequiredCapabilities = append([]string(nil), source.SuggestionRequiredCapabilities...)
	clone.Roles = append([]AssistantProgramHomeRole(nil), source.Roles...)
	for index := range clone.Roles {
		clone.Roles[index].Skills = append([]string(nil), source.Roles[index].Skills...)
	}
	clone.Stages = append([]workspace.AssistantProgramStageSpec(nil), source.Stages...)
	clone.AllowedProjectAttachments = append([]AssistantProgramAllowedProjectAttachment(nil), source.AllowedProjectAttachments...)
	return clone
}

// NormalizeAssistantProgramHome validates and canonicalizes one declaration in
// place. It reuses the existing Assistant Program text, role, stage, reflection
// and skill rules, while enforcing the stricter Home-only ownership rules.
func NormalizeAssistantProgramHome(home *AssistantProgramHome) error {
	if home == nil || home.SchemaVersion != AssistantProgramHomeSchemaVersion || home.Version < 1 {
		return fmt.Errorf("%w: independent Home schema/version is invalid", ErrInvalidAssistantProgram)
	}
	if len(home.AllowedProjectAttachments) > AssistantProgramHomeMaxEntries {
		return fmt.Errorf("%w: too many allowed project attachments", ErrInvalidAssistantProgram)
	}
	if !strictAssistantIDs(home.SuggestionRequiredCapabilities, 16) {
		return fmt.Errorf("%w: suggestion capabilities must be unique canonical identifiers", ErrInvalidAssistantProgram)
	}
	for index, role := range home.Roles {
		if !strictAssistantIDs(role.Skills, 32) || (strings.TrimSpace(role.Role) != "" && !assistantProgramIDPattern.MatchString(role.Role)) ||
			(strings.TrimSpace(role.Type) != "" && !assistantProgramIDPattern.MatchString(role.Type)) {
			return fmt.Errorf("%w: Home role %d has invalid or duplicate identifiers", ErrInvalidAssistantProgram, index)
		}
	}
	declaration := home.AssistantProgram()
	if err := normalizeAndValidateAssistantProgramScopes(declaration, []workspace.AssistantRoleScope{workspace.AssistantRoleScopeHome}); err != nil {
		return err
	}
	primaryCount, requiredCount := 0, 0
	for _, role := range declaration.Roles {
		if role.Required {
			requiredCount++
		}
		if role.Primary {
			primaryCount++
		}
	}
	if requiredCount == 0 || primaryCount != 1 {
		return fmt.Errorf("%w: Home roles require one required primary", ErrInvalidAssistantProgram)
	}

	home.ID = declaration.ID
	home.StationName = declaration.StationName
	home.StationDescription = declaration.StationDescription
	home.DefaultPrimaryName = declaration.DefaultPrimaryName
	home.HireTitle = declaration.HireTitle
	home.HireDescription = declaration.HireDescription
	home.DisabledMessage = declaration.DisabledMessage
	home.SuggestionRequiredCapabilities = append([]string(nil), declaration.SuggestionRequiredCapabilities...)
	home.Stages = append([]workspace.AssistantProgramStageSpec(nil), declaration.Stages...)
	home.Reflection = declaration.Reflection
	home.Roles = make([]AssistantProgramHomeRole, len(declaration.Roles))
	for index, role := range declaration.Roles {
		home.Roles[index] = AssistantProgramHomeRole{
			ID: role.ID, Label: role.Label, Description: role.Description, Required: role.Required,
			CapabilityID: role.CapabilityID, Primary: role.Primary, Role: role.Role, Type: role.Type,
			SystemPrompt: role.SystemPrompt, Skills: append([]string(nil), role.Skills...),
		}
	}

	seen := make(map[string]struct{}, len(home.AllowedProjectAttachments))
	for index := range home.AllowedProjectAttachments {
		attachment := &home.AllowedProjectAttachments[index]
		attachment.ProviderPluginID = normalizeAssistantProgramID(attachment.ProviderPluginID)
		attachment.BlueprintID = normalizeAssistantProgramID(attachment.BlueprintID)
		attachment.ProjectTeamID = normalizeAssistantProgramID(attachment.ProjectTeamID)
		if !assistantProgramIDPattern.MatchString(attachment.ProviderPluginID) ||
			!assistantProgramIDPattern.MatchString(attachment.BlueprintID) ||
			!assistantProgramIDPattern.MatchString(attachment.ProjectTeamID) ||
			attachment.ProjectTeamSchemaVersion != 1 || attachment.MinProjectTeamVersion < 1 ||
			attachment.MaxProjectTeamVersion < attachment.MinProjectTeamVersion {
			return fmt.Errorf("%w: allowed project attachment %d is invalid", ErrInvalidAssistantProgram, index)
		}
		key := attachment.ProviderPluginID + "\x00" + attachment.BlueprintID + "\x00" + attachment.ProjectTeamID
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: duplicate allowed project attachment", ErrInvalidAssistantProgram)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func strictAssistantIDs(values []string, maximum int) bool {
	if len(values) > maximum {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != strings.ToLower(strings.TrimSpace(value)) || !assistantProgramIDPattern.MatchString(value) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

// AssistantProgram returns the existing durable declaration shape with Home
// scopes only. Callers must normalize the contribution first.
func (home AssistantProgramHome) AssistantProgram() *workspace.AssistantProgramDeclaration {
	declaration := &workspace.AssistantProgramDeclaration{
		SchemaVersion: workspace.AssistantProgramSchemaVersion,
		ID:            home.ID, StationName: home.StationName, StationDescription: home.StationDescription,
		DefaultPrimaryName: home.DefaultPrimaryName, HireTitle: home.HireTitle,
		HireDescription: home.HireDescription, DisabledMessage: home.DisabledMessage,
		SuggestionRequiredCapabilities: append([]string(nil), home.SuggestionRequiredCapabilities...),
		Stages:                         append([]workspace.AssistantProgramStageSpec(nil), home.Stages...), Reflection: home.Reflection,
	}
	for _, role := range home.Roles {
		declaration.Roles = append(declaration.Roles, workspace.AssistantProgramRoleSpec{
			ID: role.ID, Label: role.Label, Description: role.Description, Scope: workspace.AssistantRoleScopeHome,
			Required: role.Required, CapabilityID: role.CapabilityID, Primary: role.Primary,
			Role: role.Role, Type: role.Type, SystemPrompt: role.SystemPrompt, Skills: append([]string(nil), role.Skills...),
		})
	}
	return declaration
}

func AssistantProgramHomeDigest(home AssistantProgramHome) string {
	encoded, err := json.Marshal(home)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// AssistantProgramHomeTemplate builds the internal metadata-only carrier used
// by the existing reviewed Group Template flow. It is not a project blueprint:
// it has no PluginOwner, skeleton, project roles, setup quest, tools, agents,
// capabilities, runtime, or project connection.
func AssistantProgramHomeTemplate(home AssistantProgramHome, owner workspace.AssistantProgramHomeOwner) Template {
	revision := AssistantProgramHomeDigest(home)
	return Template{
		ID:   "plugin-home:" + strings.ToLower(strings.TrimSpace(owner.PluginID)) + ":" + home.ID,
		Name: home.StationName, Description: home.StationDescription, Revision: revision,
		ProgramHomeOwner: &owner, AssistantProgram: home.AssistantProgram(),
		GroupRequirement: &GroupRequirement{
			SchemaVersion: GroupRequirementSchemaVersion, Policy: GroupPolicyRequired,
			AssistantProgramID: home.ID, MissingHome: MissingHomeOfferCreate, DefaultHomeName: home.StationName,
		},
		HasSkeleton: false,
	}
}
