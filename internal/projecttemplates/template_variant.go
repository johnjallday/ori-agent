package projecttemplates

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	TemplateVariantSchemaVersion = 1
	maxTemplateVariantBytes      = 16 << 10
)

var (
	ErrInvalidTemplateVariant       = errors.New("invalid template variant")
	ErrTemplateVariantStale         = errors.New("template variant revision is stale")
	ErrTemplateVariantSource        = errors.New("template variant source is unavailable")
	ErrTemplateVariantSourceChanged = errors.New("template variant source changed")
	ErrTemplateVariantRestricted    = errors.New("template variant supports metadata and group requirement edits only")
	ErrTemplateVariantOwner         = errors.New("template variant was not found for this owner")
	ErrTemplateVariantRehost        = errors.New("template variant import requires explicit rehosting confirmation")
	variantIDPattern                = regexp.MustCompile(`^tv_[a-f0-9]{24}$`)
	digestPattern                   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type VariantSourceState string

const (
	VariantSourceReady        VariantSourceState = "ready"
	VariantSourceMissing      VariantSourceState = "missing"
	VariantSourceDisabled     VariantSourceState = "disabled"
	VariantSourceChanged      VariantSourceState = "changed"
	VariantSourceIncompatible VariantSourceState = "incompatible"
)

type TemplateVariantSource struct {
	PluginID         string `json:"plugin_id"`
	PluginVersion    string `json:"plugin_version"`
	BlueprintID      string `json:"blueprint_id"`
	BlueprintVersion int    `json:"blueprint_version"`
	DefinitionDigest string `json:"definition_digest"`
}

type TemplateVariantOverrides struct {
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Icon             string            `json:"icon,omitempty"`
	GroupRequirement *GroupRequirement `json:"group_requirement"`
}

type TemplateVariant struct {
	SchemaVersion int                      `json:"schema_version"`
	VariantID     string                   `json:"variant_id"`
	OwnerUserID   string                   `json:"owner_user_id"`
	Source        TemplateVariantSource    `json:"source"`
	Overrides     TemplateVariantOverrides `json:"overrides"`
}

func CloneTemplateVariant(source *TemplateVariant) *TemplateVariant {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Overrides.GroupRequirement = CloneGroupRequirement(source.Overrides.GroupRequirement)
	return &clone
}

func validateTemplateVariantEnvelope(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != ManifestFileName || entries[0].IsDir() || entries[0].Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: overlay must contain only template.json", ErrInvalidTemplateVariant)
	}
	data, err := os.ReadFile(filepath.Join(directory, ManifestFileName)) // #nosec G304 -- directory is a resolved template folder; filename is fixed
	if err != nil || len(data) > maxTemplateVariantBytes+(1<<10) {
		return fmt.Errorf("%w: overlay manifest is unavailable or oversized", ErrInvalidTemplateVariant)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope) != 1 || envelope["template_variant"] == nil {
		return fmt.Errorf("%w: overlay manifest may contain only template_variant", ErrInvalidTemplateVariant)
	}
	return nil
}

func normalizeTemplateVariant(raw json.RawMessage) (*TemplateVariant, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(trimmed) > maxTemplateVariantBytes {
		return nil, fmt.Errorf("%w: declaration exceeds %d bytes", ErrInvalidTemplateVariant, maxTemplateVariantBytes)
	}
	var variant TemplateVariant
	if err := decodeStrictDeclaration(trimmed, &variant); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTemplateVariant, err)
	}
	if variant.SchemaVersion != TemplateVariantSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version must be %d", ErrInvalidTemplateVariant, TemplateVariantSchemaVersion)
	}
	variant.VariantID = strings.TrimSpace(variant.VariantID)
	variant.OwnerUserID = strings.TrimSpace(variant.OwnerUserID)
	variant.Source.PluginID = workspace.NormalizeCapabilityID(variant.Source.PluginID)
	variant.Source.BlueprintID = workspace.NormalizeCapabilityID(variant.Source.BlueprintID)
	variant.Source.PluginVersion = strings.TrimSpace(variant.Source.PluginVersion)
	variant.Source.DefinitionDigest = strings.ToLower(strings.TrimSpace(variant.Source.DefinitionDigest))
	variant.Overrides.Name = strings.TrimSpace(variant.Overrides.Name)
	variant.Overrides.Description = strings.TrimSpace(variant.Overrides.Description)
	variant.Overrides.Icon = strings.TrimSpace(variant.Overrides.Icon)

	if !variantIDPattern.MatchString(variant.VariantID) {
		return nil, fmt.Errorf("%w: variant_id is invalid", ErrInvalidTemplateVariant)
	}
	if variant.OwnerUserID == "" || len(variant.OwnerUserID) > 120 || strings.ContainsAny(variant.OwnerUserID, "\x00\r\n") {
		return nil, fmt.Errorf("%w: owner_user_id is invalid", ErrInvalidTemplateVariant)
	}
	if !assistantProgramIDPattern.MatchString(variant.Source.PluginID) || !assistantProgramIDPattern.MatchString(variant.Source.BlueprintID) ||
		variant.Source.PluginVersion == "" || len(variant.Source.PluginVersion) > 120 || strings.Contains(variant.Source.PluginVersion, "://") ||
		variant.Source.BlueprintVersion < 1 || !digestPattern.MatchString(variant.Source.DefinitionDigest) {
		return nil, fmt.Errorf("%w: source identity is invalid", ErrInvalidTemplateVariant)
	}
	if variant.Overrides.Name == "" || len(variant.Overrides.Name) > 120 || !utf8.ValidString(variant.Overrides.Name) ||
		len(variant.Overrides.Description) > 1000 || !utf8.ValidString(variant.Overrides.Description) ||
		len(variant.Overrides.Icon) > 32 || !utf8.ValidString(variant.Overrides.Icon) {
		return nil, fmt.Errorf("%w: override display fields are invalid", ErrInvalidTemplateVariant)
	}
	encodedRequirement, err := json.Marshal(variant.Overrides.GroupRequirement)
	if err != nil || variant.Overrides.GroupRequirement == nil {
		return nil, fmt.Errorf("%w: group_requirement is required", ErrInvalidTemplateVariant)
	}
	requirement, err := normalizeGroupRequirement(encodedRequirement)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTemplateVariant, err)
	}
	variant.Overrides.GroupRequirement = requirement
	return CloneTemplateVariant(&variant), nil
}

func TemplateVariantRevision(variant *TemplateVariant) string {
	if variant == nil {
		return ""
	}
	encoded, err := json.Marshal(variant)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// TemplateDefinitionDigest binds a variant to the normalized creation-bearing
// source definition and skeleton, never to an absolute installed path.
func TemplateDefinitionDigest(template Template, skeletonDigest string) string {
	type definition struct {
		Owner                  *workspace.PluginTemplateOwner         `json:"owner"`
		SkeletonDigest         string                                 `json:"skeleton_digest"`
		BehaviorProfile        string                                 `json:"behavior_profile"`
		StarterTasks           []StarterTask                          `json:"starter_tasks,omitempty"`
		ProjectEntry           *ProjectEntry                          `json:"project_entry,omitempty"`
		ProjectConnection      *ProjectConnectionDeclaration          `json:"project_connection,omitempty"`
		Tools                  ToolDefaults                           `json:"tools"`
		Agents                 []AgentSpec                            `json:"agents,omitempty"`
		Capabilities           []CapabilityInstall                    `json:"capabilities,omitempty"`
		CapabilityRequirements []CapabilityRequirement                `json:"capability_requirements,omitempty"`
		DirectoryRequirements  []DirectoryRequirement                 `json:"directory_requirements,omitempty"`
		AutomationRecipes      []AutomationRecipe                     `json:"automation_recipes,omitempty"`
		RuntimeRequirements    *RuntimeRequirementsContract           `json:"runtime_requirements,omitempty"`
		SetupWizard            *workspace.SetupWizard                 `json:"setup_wizard,omitempty"`
		SetupQuestID           string                                 `json:"setup_quest,omitempty"`
		AssistantProgram       *workspace.AssistantProgramDeclaration `json:"assistant_program,omitempty"`
		GroupRequirement       *GroupRequirement                      `json:"group_requirement,omitempty"`
		StandaloneComposition  *StandaloneComposition                 `json:"standalone_composition,omitempty"`
	}
	value := definition{
		Owner: template.PluginOwner, SkeletonDigest: strings.ToLower(strings.TrimSpace(skeletonDigest)),
		BehaviorProfile: template.BehaviorProfile,
		StarterTasks:    template.StarterTasks, ProjectEntry: template.ProjectEntry, ProjectConnection: template.ProjectConnection,
		Tools: template.Tools, Agents: template.Agents, Capabilities: template.Capabilities,
		CapabilityRequirements: template.CapabilityRequirements, DirectoryRequirements: template.DirectoryRequirements,
		AutomationRecipes: template.AutomationRecipes, RuntimeRequirements: template.RuntimeRequirements,
		SetupWizard: template.SetupWizard, SetupQuestID: template.SetupQuestID, AssistantProgram: template.AssistantProgram,
		GroupRequirement: template.GroupRequirement, StandaloneComposition: template.StandaloneComposition,
	}
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type VariantSource struct {
	Template       Template
	SkeletonDigest string
}

// ResolveTemplateVariant constructs one effective user-owned template from an
// exact trusted source. It performs no filesystem or plugin mutation.
func ResolveTemplateVariant(overlay Template, source VariantSource, ownerUserID string) (Template, error) {
	variant := overlay.TemplateVariant
	if variant == nil || overlay.TemplateVariantError != "" {
		return Template{}, ErrInvalidTemplateVariant
	}
	if strings.TrimSpace(ownerUserID) == "" || variant.OwnerUserID != strings.TrimSpace(ownerUserID) {
		return Template{}, ErrTemplateVariantOwner
	}
	owner := source.Template.PluginOwner
	pin := variant.Source
	if owner == nil || owner.PluginID != pin.PluginID || owner.PluginVersion != pin.PluginVersion ||
		owner.BlueprintID != pin.BlueprintID || owner.BlueprintVersion != pin.BlueprintVersion {
		return Template{}, ErrTemplateVariantSource
	}
	if digest := TemplateDefinitionDigest(source.Template, source.SkeletonDigest); digest != pin.DefinitionDigest {
		return Template{}, ErrTemplateVariantSourceChanged
	}

	effective := cloneTemplate(source.Template)
	effective.ID = overlay.ID
	effective.Name = variant.Overrides.Name
	effective.Description = variant.Overrides.Description
	effective.Icon = variant.Overrides.Icon
	effective.Revision = overlay.Revision
	effective.VariantRevision = overlay.VariantRevision
	effective.TemplateVariant = CloneTemplateVariant(variant)
	effective.VariantSourceState = VariantSourceReady
	effective.PluginOwner = nil
	effective.SetupQuestID = ""
	effective.UserSetupQuest = nil
	effective.UserSetupQuestError = ""
	effective.UserSetupQuestRevision = ""
	effective.GroupRequirement = CloneGroupRequirement(variant.Overrides.GroupRequirement)
	effective.GroupRequirementError = ""
	if err := validateGroupComposition(effective.GroupRequirement, effective.StandaloneComposition, effective.AssistantProgram); err != nil {
		return Template{}, err
	}
	return effective, nil
}

type VariantEdit struct {
	Name             string
	Description      string
	Icon             string
	GroupRequirement *GroupRequirement
}

func NewTemplateVariant(source VariantSource, ownerUserID string, edit VariantEdit) (*TemplateVariant, error) {
	owner := source.Template.PluginOwner
	if owner == nil || strings.TrimSpace(ownerUserID) == "" {
		return nil, ErrTemplateVariantSource
	}
	variantID, err := newTemplateVariantID()
	if err != nil {
		return nil, err
	}
	variant := &TemplateVariant{
		SchemaVersion: TemplateVariantSchemaVersion, VariantID: variantID, OwnerUserID: strings.TrimSpace(ownerUserID),
		Source: TemplateVariantSource{
			PluginID: owner.PluginID, PluginVersion: owner.PluginVersion, BlueprintID: owner.BlueprintID,
			BlueprintVersion: owner.BlueprintVersion, DefinitionDigest: TemplateDefinitionDigest(source.Template, source.SkeletonDigest),
		},
		Overrides: TemplateVariantOverrides(edit),
	}
	normalized, err := normalizeVariantValue(variant)
	if err != nil {
		return nil, err
	}
	// Validate the complete effective composition before any overlay directory
	// is published. A syntactically valid None/Recommended override can still be
	// incompatible with a source that has no standalone composition.
	if _, err := ResolveTemplateVariant(Template{TemplateVariant: normalized}, source, ownerUserID); err != nil {
		return nil, err
	}
	return normalized, nil
}

func normalizeVariantValue(variant *TemplateVariant) (*TemplateVariant, error) {
	encoded, err := json.Marshal(variant)
	if err != nil {
		return nil, err
	}
	return normalizeTemplateVariant(encoded)
}

func newTemplateVariantID() (string, error) {
	payload := make([]byte, 12)
	if _, err := rand.Read(payload); err != nil {
		return "", fmt.Errorf("generate template variant id: %w", err)
	}
	return "tv_" + hex.EncodeToString(payload), nil
}

func writeVariantManifest(directory string, variant *TemplateVariant) error {
	manifest := map[string]any{"template_variant": variant}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeManifestAtomic(filepath.Join(directory, ManifestFileName), append(encoded, '\n'))
}

func CreateTemplateVariant(libDir string, source VariantSource, ownerUserID string, edit VariantEdit) (Template, error) {
	variant, err := NewTemplateVariant(source, ownerUserID, edit)
	if err != nil {
		return Template{}, err
	}
	release, err := acquireManifestMutationLock(libDir)
	if err != nil {
		return Template{}, err
	}
	defer release()
	id := workspace.Slugify(variant.Overrides.Name)
	destination := filepath.Join(filepath.Clean(libDir), id)
	if _, err := os.Lstat(destination); err == nil {
		return Template{}, fmt.Errorf("%w: %q", ErrTemplateExists, id)
	} else if !os.IsNotExist(err) {
		return Template{}, err
	}
	if err := os.MkdirAll(destination, 0o750); err != nil {
		return Template{}, err
	}
	if err := writeVariantManifest(destination, variant); err != nil {
		_ = os.RemoveAll(destination)
		return Template{}, err
	}
	overlay := newTemplate(destination)
	return ResolveTemplateVariant(overlay, source, ownerUserID)
}

func RehostTemplateVariant(libDir, sourcePath, displayName, ownerUserID string, confirmed bool, source VariantSource) (Template, error) {
	if !confirmed {
		return Template{}, ErrTemplateVariantRehost
	}
	overlay, err := LoadFolder(sourcePath)
	if err != nil {
		return Template{}, err
	}
	if overlay.TemplateVariant == nil || overlay.TemplateVariantError != "" {
		return Template{}, ErrInvalidTemplateVariant
	}
	if err := validateTemplateVariantEnvelope(overlay.Path); err != nil {
		return Template{}, err
	}
	// Resolve against the imported pin while ignoring its untrusted owner. The
	// source identity and digest must still be exact; rehosting only replaces
	// host-generated variant/owner identity.
	ownerCheck := CloneTemplateVariant(overlay.TemplateVariant)
	ownerCheck.OwnerUserID = strings.TrimSpace(ownerUserID)
	overlay.TemplateVariant = ownerCheck
	if _, err := ResolveTemplateVariant(overlay, source, ownerUserID); err != nil {
		return Template{}, err
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = overlay.TemplateVariant.Overrides.Name
	}
	return CreateTemplateVariant(libDir, source, ownerUserID, VariantEdit{
		Name: name, Description: overlay.TemplateVariant.Overrides.Description, Icon: overlay.TemplateVariant.Overrides.Icon,
		GroupRequirement: overlay.TemplateVariant.Overrides.GroupRequirement,
	})
}

func UpdateTemplateVariant(libDir, id, ownerUserID, expectedRevision string, edit VariantEdit, source VariantSource) (Template, error) {
	release, err := acquireManifestMutationLock(libDir)
	if err != nil {
		return Template{}, err
	}
	defer release()
	overlay, err := FindLibraryTemplate(libDir, id)
	if err != nil {
		return Template{}, err
	}
	if overlay.TemplateVariant == nil || overlay.TemplateVariant.OwnerUserID != strings.TrimSpace(ownerUserID) {
		return Template{}, ErrTemplateVariantOwner
	}
	if expectedRevision == "" || expectedRevision != overlay.VariantRevision {
		return Template{}, ErrTemplateVariantStale
	}
	if _, err := ResolveTemplateVariant(overlay, source, ownerUserID); err != nil {
		return Template{}, err
	}
	candidate := CloneTemplateVariant(overlay.TemplateVariant)
	candidate.Overrides = TemplateVariantOverrides(edit)
	candidate, err = normalizeVariantValue(candidate)
	if err != nil {
		return Template{}, err
	}
	candidateOverlay := overlay
	candidateOverlay.TemplateVariant = candidate
	candidateOverlay.VariantRevision = TemplateVariantRevision(candidate)
	candidateOverlay.Name = candidate.Overrides.Name
	candidateOverlay.Description = candidate.Overrides.Description
	candidateOverlay.Icon = candidate.Overrides.Icon
	candidateOverlay.GroupRequirement = CloneGroupRequirement(candidate.Overrides.GroupRequirement)
	if _, err := ResolveTemplateVariant(candidateOverlay, source, ownerUserID); err != nil {
		return Template{}, err
	}
	if err := writeVariantManifest(overlay.Path, candidate); err != nil {
		return Template{}, err
	}
	return ResolveTemplateVariant(newTemplate(overlay.Path), source, ownerUserID)
}
