package projecttemplates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	GroupRequirementSchemaVersion           = 1
	SplitGroupRequirementSchemaVersion      = 2
	StandaloneCompositionSchemaVersion      = 1
	SplitStandaloneCompositionSchemaVersion = 2
	maxGroupRequirementBytes                = 4 << 10
	maxStandaloneCompositionBytes           = 64 << 10
)

var (
	ErrInvalidGroupRequirement      = errors.New("invalid group requirement")
	ErrInvalidStandaloneComposition = errors.New("invalid standalone composition")
)

type GroupPolicy string

const (
	GroupPolicyNone        GroupPolicy = "none"
	GroupPolicyRecommended GroupPolicy = "recommended"
	GroupPolicyRequired    GroupPolicy = "required"
)

type MissingHomePolicy string

const (
	MissingHomeOfferCreate  MissingHomePolicy = "offer_create"
	MissingHomeExistingOnly MissingHomePolicy = "existing_only"
)

// GroupRequirement is inert placement policy. It can reference a compatible
// Assistant Program but cannot name an owner, plugin, workspace, route, path,
// command, permission, or action.
type GroupRequirement struct {
	SchemaVersion      int               `json:"schema_version"`
	Policy             GroupPolicy       `json:"policy"`
	AssistantProgramID string            `json:"assistant_program_id,omitempty"`
	AssistantProjectID string            `json:"assistant_project_id,omitempty"`
	MissingHome        MissingHomePolicy `json:"missing_home,omitempty"`
	DefaultHomeName    string            `json:"default_home_name,omitempty"`
}

func CloneGroupRequirement(source *GroupRequirement) *GroupRequirement {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}

func (requirement *GroupRequirement) IsGrouped() bool {
	return requirement != nil && (requirement.Policy == GroupPolicyRecommended || requirement.Policy == GroupPolicyRequired)
}

// StandaloneRole adapts one project-scoped Assistant Program role into an
// ordinary workspace agent without changing the Assistant Program schema.
type StandaloneRole struct {
	RoleID       string `json:"role_id"`
	SystemPrompt string `json:"system_prompt"`
}

type StandaloneComposition struct {
	SchemaVersion int              `json:"schema_version"`
	ProjectRoles  []StandaloneRole `json:"project_roles"`
}

func CloneStandaloneComposition(source *StandaloneComposition) *StandaloneComposition {
	if source == nil {
		return nil
	}
	clone := *source
	clone.ProjectRoles = append([]StandaloneRole(nil), source.ProjectRoles...)
	return &clone
}

func ParseGroupRequirement(raw json.RawMessage) (*GroupRequirement, error) {
	return normalizeGroupRequirement(raw)
}

func normalizeGroupRequirement(raw json.RawMessage) (*GroupRequirement, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(trimmed) > maxGroupRequirementBytes {
		return nil, fmt.Errorf("%w: declaration exceeds %d bytes", ErrInvalidGroupRequirement, maxGroupRequirementBytes)
	}
	var requirement GroupRequirement
	if err := decodeStrictDeclaration(trimmed, &requirement); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidGroupRequirement, err)
	}
	if requirement.SchemaVersion != GroupRequirementSchemaVersion && requirement.SchemaVersion != SplitGroupRequirementSchemaVersion {
		return nil, fmt.Errorf("%w: unsupported schema_version %d", ErrInvalidGroupRequirement, requirement.SchemaVersion)
	}
	requirement.AssistantProgramID = normalizeAssistantProgramID(requirement.AssistantProgramID)
	requirement.AssistantProjectID = normalizeAssistantProgramID(requirement.AssistantProjectID)
	requirement.DefaultHomeName = strings.TrimSpace(requirement.DefaultHomeName)
	switch requirement.Policy {
	case GroupPolicyNone:
		if requirement.MissingHome != "" || requirement.DefaultHomeName != "" || requirement.AssistantProgramID != "" {
			return nil, fmt.Errorf("%w: policy none cannot declare a Home target", ErrInvalidGroupRequirement)
		}
		if requirement.SchemaVersion == GroupRequirementSchemaVersion && requirement.AssistantProjectID != "" {
			return nil, fmt.Errorf("%w: schema 1 policy none cannot declare a project team", ErrInvalidGroupRequirement)
		}
		if requirement.SchemaVersion == SplitGroupRequirementSchemaVersion && !assistantProgramIDPattern.MatchString(requirement.AssistantProjectID) {
			return nil, fmt.Errorf("%w: schema 2 requires assistant_project_id", ErrInvalidGroupRequirement)
		}
	case GroupPolicyRecommended, GroupPolicyRequired:
		switch requirement.SchemaVersion {
		case GroupRequirementSchemaVersion:
			if !assistantProgramIDPattern.MatchString(requirement.AssistantProgramID) || requirement.AssistantProjectID != "" {
				return nil, fmt.Errorf("%w: schema 1 requires only assistant_program_id", ErrInvalidGroupRequirement)
			}
		case SplitGroupRequirementSchemaVersion:
			if !assistantProgramIDPattern.MatchString(requirement.AssistantProjectID) || requirement.AssistantProgramID != "" {
				return nil, fmt.Errorf("%w: schema 2 requires only assistant_project_id", ErrInvalidGroupRequirement)
			}
		}
		switch requirement.MissingHome {
		case MissingHomeOfferCreate:
			if err := validateHomeDisplayName(requirement.DefaultHomeName); err != nil {
				return nil, err
			}
		case MissingHomeExistingOnly:
			if requirement.DefaultHomeName != "" {
				return nil, fmt.Errorf("%w: existing_only cannot declare default_home_name", ErrInvalidGroupRequirement)
			}
		default:
			return nil, fmt.Errorf("%w: missing_home must be offer_create or existing_only", ErrInvalidGroupRequirement)
		}
	default:
		return nil, fmt.Errorf("%w: policy must be none, recommended, or required", ErrInvalidGroupRequirement)
	}
	return CloneGroupRequirement(&requirement), nil
}

// ValidateHomeDisplayName applies the declaration's Home display-name bounds to
// a user-chosen group name, so reviewed and declared names share one rule.
func ValidateHomeDisplayName(value string) error {
	return validateHomeDisplayName(value)
}

func validateHomeDisplayName(value string) error {
	if value == "" || len(value) > 120 || !utf8.ValidString(value) {
		return fmt.Errorf("%w: default_home_name must contain 1-120 bytes of display text", ErrInvalidGroupRequirement)
	}
	lower := strings.ToLower(value)
	if strings.Contains(value, "/") || strings.Contains(value, `\`) || strings.Contains(lower, "://") {
		return fmt.Errorf("%w: default_home_name cannot contain a path or protocol", ErrInvalidGroupRequirement)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: default_home_name cannot contain control characters", ErrInvalidGroupRequirement)
		}
	}
	return nil
}

func normalizeStandaloneComposition(raw json.RawMessage, program *workspace.AssistantProgramDeclaration, project *AssistantProjectDeclaration, projectConnection *ProjectConnectionDeclaration, agents []AgentSpec, runtime *RuntimeRequirementsContract) (*StandaloneComposition, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(trimmed) > maxStandaloneCompositionBytes {
		return nil, fmt.Errorf("%w: declaration exceeds %d bytes", ErrInvalidStandaloneComposition, maxStandaloneCompositionBytes)
	}
	var composition StandaloneComposition
	if err := decodeStrictDeclaration(trimmed, &composition); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidStandaloneComposition, err)
	}
	if composition.SchemaVersion != StandaloneCompositionSchemaVersion && composition.SchemaVersion != SplitStandaloneCompositionSchemaVersion {
		return nil, fmt.Errorf("%w: unsupported schema_version %d", ErrInvalidStandaloneComposition, composition.SchemaVersion)
	}
	if composition.SchemaVersion == StandaloneCompositionSchemaVersion && (program == nil || project != nil || program.SchemaVersion != workspace.AssistantProgramSchemaVersion) {
		return nil, fmt.Errorf("%w: schema 1 requires combined Assistant Program schema v2", ErrInvalidStandaloneComposition)
	}
	if composition.SchemaVersion == SplitStandaloneCompositionSchemaVersion && (project == nil || program != nil) {
		return nil, fmt.Errorf("%w: schema 2 requires one split assistant_project", ErrInvalidStandaloneComposition)
	}
	if projectConnection == nil || len(projectConnection.SupportedModes) == 0 {
		return nil, fmt.Errorf("%w: a project connection is required", ErrInvalidStandaloneComposition)
	}
	if len(agents) != 0 {
		return nil, fmt.Errorf("%w: a program-bearing source cannot also declare ordinary agents", ErrInvalidStandaloneComposition)
	}
	if runtime != nil && !hasStandaloneRuntimeMode(runtime) {
		return nil, fmt.Errorf("%w: runtime requirements have no Home-free operating mode", ErrInvalidStandaloneComposition)
	}

	declared := make(map[string]StandaloneRole, len(composition.ProjectRoles))
	for index := range composition.ProjectRoles {
		role := &composition.ProjectRoles[index]
		role.RoleID = normalizeAssistantProgramID(role.RoleID)
		role.SystemPrompt = strings.TrimSpace(role.SystemPrompt)
		if !assistantProgramIDPattern.MatchString(role.RoleID) {
			return nil, fmt.Errorf("%w: project role %d has an invalid role_id", ErrInvalidStandaloneComposition, index)
		}
		if role.SystemPrompt == "" || len(role.SystemPrompt) > workspace.AssistantProgramMaxText || strings.ContainsRune(role.SystemPrompt, '\x00') {
			return nil, fmt.Errorf("%w: project role %q has an invalid system_prompt", ErrInvalidStandaloneComposition, role.RoleID)
		}
		if _, duplicate := declared[role.RoleID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate project role %q", ErrInvalidStandaloneComposition, role.RoleID)
		}
		if err := ValidateAgentPrompts([]AgentSpec{{Name: role.RoleID, SystemPrompt: role.SystemPrompt}}); err != nil {
			return nil, fmt.Errorf("%w: project role %q: %v", ErrInvalidStandaloneComposition, role.RoleID, err)
		}
		declared[role.RoleID] = *role
	}

	normalized := StandaloneComposition{SchemaVersion: composition.SchemaVersion}
	var projectRoles []workspace.AssistantProgramRoleSpec
	if project != nil {
		projectRoles = project.ProgramRoles()
	} else {
		projectRoles = program.Roles
	}
	for _, role := range projectRoles {
		if role.Scope != workspace.AssistantRoleScopeProject {
			if _, invalid := declared[role.ID]; invalid {
				return nil, fmt.Errorf("%w: Home role %q cannot be adapted", ErrInvalidStandaloneComposition, role.ID)
			}
			continue
		}
		adaptation, found := declared[role.ID]
		if !found {
			return nil, fmt.Errorf("%w: project role %q is missing", ErrInvalidStandaloneComposition, role.ID)
		}
		normalized.ProjectRoles = append(normalized.ProjectRoles, adaptation)
		delete(declared, role.ID)
	}
	if len(normalized.ProjectRoles) == 0 || len(declared) != 0 {
		return nil, fmt.Errorf("%w: project_roles must exactly match the Assistant Program's project scope", ErrInvalidStandaloneComposition)
	}
	return CloneStandaloneComposition(&normalized), nil
}

func hasStandaloneRuntimeMode(contract *RuntimeRequirementsContract) bool {
	for _, mode := range contract.OperatingModes {
		if len(mode.Requires) == 0 {
			return true
		}
	}
	return false
}

func decodeStrictDeclaration(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("declaration contains trailing data")
	}
	return nil
}

func validateGroupComposition(requirement *GroupRequirement, composition *StandaloneComposition, program *workspace.AssistantProgramDeclaration, project *AssistantProjectDeclaration) error {
	if requirement == nil {
		return nil
	}
	switch requirement.SchemaVersion {
	case GroupRequirementSchemaVersion:
		if project != nil || (requirement.IsGrouped() && (program == nil || requirement.AssistantProgramID != normalizeAssistantProgramID(program.ID))) {
			return fmt.Errorf("%w: assistant_program_id does not match a valid Assistant Program", ErrInvalidGroupRequirement)
		}
	case SplitGroupRequirementSchemaVersion:
		if program != nil || project == nil || requirement.AssistantProjectID != normalizeAssistantProgramID(project.ID) {
			return fmt.Errorf("%w: assistant_project_id does not match a valid project team", ErrInvalidGroupRequirement)
		}
	}
	if (requirement.Policy == GroupPolicyNone || requirement.Policy == GroupPolicyRecommended) && (program != nil || project != nil) && composition == nil {
		return fmt.Errorf("%w: policy %s requires a valid standalone composition", ErrInvalidGroupRequirement, requirement.Policy)
	}
	return nil
}

// StandaloneTemplate returns the fixed Home-free effective composition. The
// source remains unchanged and no workspace or Assistant Program state is
// created by this pure transformation.
func StandaloneTemplate(source Template) (Template, error) {
	if source.GroupRequirement == nil || (source.GroupRequirement.Policy != GroupPolicyNone && source.GroupRequirement.Policy != GroupPolicyRecommended) {
		return Template{}, fmt.Errorf("%w: template policy does not allow standalone composition", ErrInvalidGroupRequirement)
	}
	if source.AssistantProgram == nil && source.AssistantProject == nil {
		result := cloneTemplate(source)
		result.SetupQuestID = ""
		result.UserSetupQuest = nil
		result.UserSetupQuestError = ""
		result.UserSetupQuestRevision = ""
		return result, nil
	}
	if source.StandaloneComposition == nil {
		return Template{}, fmt.Errorf("%w: standalone composition is unavailable", ErrInvalidStandaloneComposition)
	}

	result := cloneTemplate(source)
	roles := make(map[string]StandaloneRole, len(source.StandaloneComposition.ProjectRoles))
	for _, role := range source.StandaloneComposition.ProjectRoles {
		roles[role.RoleID] = role
	}
	result.Agents = nil
	var projectRoles []workspace.AssistantProgramRoleSpec
	if source.AssistantProject != nil {
		projectRoles = source.AssistantProject.ProgramRoles()
	} else {
		projectRoles = source.AssistantProgram.Roles
	}
	for _, role := range projectRoles {
		if role.Scope != workspace.AssistantRoleScopeProject {
			continue
		}
		adaptation := roles[role.ID]
		result.Agents = append(result.Agents, AgentSpec{
			Name: role.Label, Role: role.Role, SystemPrompt: adaptation.SystemPrompt,
			Tools: ToolDefaults{Skills: append([]string(nil), role.Skills...)},
		})
	}
	result.AssistantProgram = nil
	result.AssistantProgramError = ""
	result.AssistantProject = nil
	result.AssistantProjectError = ""
	if source.GroupRequirement.SchemaVersion == SplitGroupRequirementSchemaVersion {
		result.GroupRequirement = nil
		result.GroupRequirementError = ""
		result.StandaloneComposition = nil
		result.StandaloneCompositionError = ""
	}
	result.SetupQuestID = ""
	result.UserSetupQuest = nil
	result.UserSetupQuestError = ""
	result.UserSetupQuestRevision = ""
	return result, nil
}

type CompositionProjection struct {
	Composition       string   `json:"composition"`
	ProjectRoles      []string `json:"project_roles,omitempty"`
	UnavailableRoles  []string `json:"unavailable_home_roles,omitempty"`
	Capabilities      []string `json:"capabilities,omitempty"`
	RuntimeModes      []string `json:"runtime_modes,omitempty"`
	PreWorkspaceSetup bool     `json:"pre_workspace_setup"`
	AssistantProgram  bool     `json:"assistant_program"`
}

type GroupRequirementPreview struct {
	Label      string                 `json:"label"`
	Policy     GroupPolicy            `json:"policy"`
	Available  []string               `json:"available_compositions"`
	Grouped    *CompositionProjection `json:"grouped,omitempty"`
	Standalone *CompositionProjection `json:"standalone,omitempty"`
}

func PreviewGroupRequirement(source Template, raw json.RawMessage) (GroupRequirementPreview, error) {
	requirement, err := normalizeGroupRequirement(raw)
	if err != nil || requirement == nil {
		if err == nil {
			err = fmt.Errorf("%w: an explicit policy object is required", ErrInvalidGroupRequirement)
		}
		return GroupRequirementPreview{}, err
	}
	candidate := cloneTemplate(source)
	candidate.GroupRequirement = requirement
	if err := validateGroupComposition(requirement, candidate.StandaloneComposition, candidate.AssistantProgram, candidate.AssistantProject); err != nil {
		return GroupRequirementPreview{}, err
	}
	preview := GroupRequirementPreview{Label: "Preview — no workspace or Home created", Policy: requirement.Policy}
	if requirement.IsGrouped() {
		grouped := compositionProjection(candidate, false)
		preview.Grouped = &grouped
		preview.Available = append(preview.Available, "grouped")
	}
	if requirement.Policy == GroupPolicyNone || requirement.Policy == GroupPolicyRecommended {
		standaloneSource := candidate
		standaloneSource.GroupRequirement = requirement
		standalone, err := StandaloneTemplate(standaloneSource)
		if err != nil {
			return GroupRequirementPreview{}, err
		}
		projection := compositionProjection(standalone, true)
		if candidate.AssistantProgram != nil {
			for _, role := range candidate.AssistantProgram.Roles {
				if role.Scope == workspace.AssistantRoleScopeHome {
					projection.UnavailableRoles = append(projection.UnavailableRoles, role.Label)
				}
			}
		}
		preview.Standalone = &projection
		preview.Available = append(preview.Available, "standalone")
	}
	return preview, nil
}

func compositionProjection(template Template, standalone bool) CompositionProjection {
	projection := CompositionProjection{Composition: "grouped", PreWorkspaceSetup: template.SetupQuestID != "" || template.UserSetupQuest != nil, AssistantProgram: template.AssistantProgram != nil || template.AssistantProject != nil}
	if standalone {
		projection.Composition = "standalone"
	}
	if template.AssistantProject != nil {
		for _, role := range template.AssistantProject.Roles {
			projection.ProjectRoles = append(projection.ProjectRoles, role.Label)
		}
	} else if template.AssistantProgram != nil {
		for _, role := range template.AssistantProgram.Roles {
			if role.Scope == workspace.AssistantRoleScopeProject {
				projection.ProjectRoles = append(projection.ProjectRoles, role.Label)
			}
		}
	} else {
		for _, agent := range template.Agents {
			projection.ProjectRoles = append(projection.ProjectRoles, agent.Name)
		}
	}
	for _, capability := range template.Capabilities {
		projection.Capabilities = append(projection.Capabilities, capability.ID)
	}
	if template.RuntimeRequirements != nil {
		for _, mode := range template.RuntimeRequirements.OperatingModes {
			projection.RuntimeModes = append(projection.RuntimeModes, mode.Label)
		}
	}
	return projection
}

func cloneTemplate(source Template) Template {
	clone := source
	clone.Tags = append([]string(nil), source.Tags...)
	clone.Addons = append([]string(nil), source.Addons...)
	clone.StarterTasks = append([]StarterTask(nil), source.StarterTasks...)
	for index := range clone.StarterTasks {
		clone.StarterTasks[index].Requires = append([]string(nil), source.StarterTasks[index].Requires...)
		clone.StarterTasks[index].FileFallbackFor = append([]string(nil), source.StarterTasks[index].FileFallbackFor...)
		clone.StarterTasks[index].ConnectionModes = append([]ProjectConnectionMode(nil), source.StarterTasks[index].ConnectionModes...)
	}
	clone.Agents = append([]AgentSpec(nil), source.Agents...)
	for index := range clone.Agents {
		clone.Agents[index].Tools = normalizeToolDefaults(source.Agents[index].Tools)
		clone.Agents[index].Appearance = source.Agents[index].Appearance.Clone()
	}
	clone.Tools = normalizeToolDefaults(source.Tools)
	clone.CapabilityRequirements = normalizeCapabilityRequirements(source.CapabilityRequirements)
	clone.Capabilities = append([]CapabilityInstall(nil), source.Capabilities...)
	clone.DirectoryRequirements = normalizeDirectoryRequirements(source.DirectoryRequirements)
	clone.AutomationRecipes = normalizeAutomationRecipes(source.AutomationRecipes, clone.DirectoryRequirements)
	clone.IntakeRequirements = cloneIntakeRequirements(source.IntakeRequirements)
	clone.RuntimeRequirements = workspace.CloneRuntimeRequirementsContract(source.RuntimeRequirements)
	clone.SetupWizard = workspace.CloneSetupWizard(source.SetupWizard)
	clone.AssistantProgram = workspace.CloneAssistantProgramDeclaration(source.AssistantProgram)
	clone.AssistantProject = CloneAssistantProjectDeclaration(source.AssistantProject)
	clone.ResolvedAssistantHome = workspace.CloneAssistantProgramDeclaration(source.ResolvedAssistantHome)
	clone.GroupRequirement = CloneGroupRequirement(source.GroupRequirement)
	clone.StandaloneComposition = CloneStandaloneComposition(source.StandaloneComposition)
	clone.TemplateVariant = CloneTemplateVariant(source.TemplateVariant)
	if source.PluginOwner != nil {
		owner := source.PluginOwner.Clone()
		clone.PluginOwner = &owner
	}
	if source.ProgramHomeOwner != nil {
		owner := source.ProgramHomeOwner.Clone()
		clone.ProgramHomeOwner = &owner
	}
	return clone
}
