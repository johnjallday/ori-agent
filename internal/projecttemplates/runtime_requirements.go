package projecttemplates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ErrInvalidRuntimeRequirements reports a runtime_requirements declaration
// that cannot be honored in full. The block is all-or-nothing: callers retain
// the blueprint's other metadata but expose no partial runtime contract and
// must refuse workspace creation.
var ErrInvalidRuntimeRequirements = errors.New("invalid runtime requirements")

// The normalized authoring and workspace-snapshot types are aliases by design:
// a blueprint API response and the immutable contract persisted into a created
// workspace cannot drift into different JSON vocabularies.
type RuntimeRequirementsContract = workspace.RuntimeRequirementsContract
type RuntimeOperatingMode = workspace.RuntimeOperatingMode
type RuntimeRequirement = workspace.RuntimeRequirement

const (
	RuntimeRequirementsSchemaVersion = workspace.RuntimeRequirementsSchemaVersion
	maxRuntimeOperatingModes         = workspace.MaxRuntimeOperatingModes
	maxRuntimeRequirements           = workspace.MaxRuntimeRequirements
	maxRuntimeIdentifierLength       = workspace.MaxRuntimeIdentifierLength
	maxRuntimeLabelLength            = workspace.MaxRuntimeLabelLength
	maxRuntimeDescriptionLength      = workspace.MaxRuntimeDescriptionLength
	maxRuntimeDisclosureLength       = workspace.MaxRuntimeDisclosureLength
)

// ValidRuntimeRequirementAdapters is the authoring allowlist of compiled
// runtime adapter IDs. Values are lookup keys only; manifests cannot provide
// implementation packages, paths, commands, or endpoints. A parity test with
// the runtime registry keeps this list resolvable as adapters are introduced.
var ValidRuntimeRequirementAdapters = []string{
	"reaper_live_control",
}

type runtimeRequirementsDecl struct {
	SchemaVersion  int                        `json:"schema_version"`
	OperatingModes []runtimeOperatingModeDecl `json:"operating_modes"`
	Requirements   []runtimeRequirementDecl   `json:"requirements"`
}

type runtimeOperatingModeDecl struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Requires    []string `json:"requires,omitempty"`
}

type runtimeRequirementDecl struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Disclosure  string `json:"disclosure,omitempty"`
	Adapter     string `json:"adapter"`
}

// normalizeRuntimeRequirements decodes and validates the optional public
// manifest block. Unknown fields are rejected rather than ignored: silently
// dropping a script, URL, path, or custom component would leave an author
// believing behavior was active when the safe contract never supports it.
func normalizeRuntimeRequirements(raw json.RawMessage) (*RuntimeRequirementsContract, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var declaration runtimeRequirementsDecl
	if err := decoder.Decode(&declaration); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRuntimeRequirements, err)
	}
	if err := validateRuntimeRequirements(&declaration); err != nil {
		return nil, err
	}

	contract := &RuntimeRequirementsContract{
		SchemaVersion:  declaration.SchemaVersion,
		OperatingModes: make([]RuntimeOperatingMode, 0, len(declaration.OperatingModes)),
		Requirements:   make([]RuntimeRequirement, 0, len(declaration.Requirements)),
	}
	for _, mode := range declaration.OperatingModes {
		references := make([]string, 0, len(mode.Requires))
		for _, reference := range mode.Requires {
			references = append(references, workspace.NormalizeRuntimeIdentifier(reference))
		}
		if len(references) == 0 {
			references = nil
		}
		contract.OperatingModes = append(contract.OperatingModes, RuntimeOperatingMode{
			ID:          workspace.NormalizeRuntimeIdentifier(mode.ID),
			Label:       strings.TrimSpace(mode.Label),
			Description: strings.TrimSpace(mode.Description),
			Requires:    references,
		})
	}
	for _, requirement := range declaration.Requirements {
		contract.Requirements = append(contract.Requirements, RuntimeRequirement{
			Key:         workspace.NormalizeRuntimeIdentifier(requirement.Key),
			Label:       strings.TrimSpace(requirement.Label),
			Description: strings.TrimSpace(requirement.Description),
			Disclosure:  strings.TrimSpace(requirement.Disclosure),
			Adapter:     workspace.NormalizeRuntimeIdentifier(requirement.Adapter),
		})
	}
	return contract, nil
}

// validateRuntimeStarterTaskReferences distinguishes runtime capability keys
// from ordinary planning/toolbox keys without claiming every capability-like
// word. A key that names a compiled runtime adapter is explicitly runtime and
// must be declared by this blueprint's own contract. Other keys (email,
// planning, citations, toolbox vocabulary) retain their existing behavior.
func validateRuntimeStarterTaskReferences(tasks []StarterTask, contract *RuntimeRequirementsContract) error {
	for _, task := range tasks {
		required := workspace.NormalizeCapabilityKeys(task.Requires)
		for _, rawKey := range task.Requires {
			key := workspace.NormalizeRuntimeIdentifier(rawKey)
			if key == "" || !slices.Contains(ValidRuntimeRequirementAdapters, key) {
				continue
			}
			if contract == nil {
				return fmt.Errorf("%w: starter task %q references runtime requirement %q, but the blueprint declares no runtime_requirements contract", ErrInvalidRuntimeRequirements, task.Description, key)
			}
			if _, declared := contract.Requirement(key); !declared {
				return fmt.Errorf("%w: starter task %q references undeclared runtime requirement %q", ErrInvalidRuntimeRequirements, task.Description, key)
			}
		}
		for _, rawKey := range task.FileFallbackFor {
			key := workspace.NormalizeRuntimeIdentifier(rawKey)
			if key == "" || !slices.Contains(required, key) {
				return fmt.Errorf("%w: starter task %q file fallback %q must also be a required capability", ErrInvalidRuntimeRequirements, task.Description, rawKey)
			}
			if !slices.Contains(ValidRuntimeRequirementAdapters, key) || contract == nil {
				return fmt.Errorf("%w: starter task %q file fallback %q is not a declared runtime requirement", ErrInvalidRuntimeRequirements, task.Description, key)
			}
			if _, declared := contract.Requirement(key); !declared {
				return fmt.Errorf("%w: starter task %q file fallback references undeclared runtime requirement %q", ErrInvalidRuntimeRequirements, task.Description, key)
			}
		}
	}
	return nil
}

func validateRuntimeRequirements(declaration *runtimeRequirementsDecl) error {
	if declaration == nil {
		return nil
	}
	if declaration.SchemaVersion <= 0 {
		return fmt.Errorf("%w: schema_version is required and must be a positive integer", ErrInvalidRuntimeRequirements)
	}
	if declaration.SchemaVersion != RuntimeRequirementsSchemaVersion {
		return fmt.Errorf("%w: unsupported schema_version %d (this build understands %d)", ErrInvalidRuntimeRequirements, declaration.SchemaVersion, RuntimeRequirementsSchemaVersion)
	}
	if len(declaration.OperatingModes) == 0 {
		return fmt.Errorf("%w: declare at least one operating mode", ErrInvalidRuntimeRequirements)
	}
	if len(declaration.OperatingModes) > maxRuntimeOperatingModes {
		return fmt.Errorf("%w: %d operating modes exceeds the maximum of %d", ErrInvalidRuntimeRequirements, len(declaration.OperatingModes), maxRuntimeOperatingModes)
	}
	if len(declaration.Requirements) > maxRuntimeRequirements {
		return fmt.Errorf("%w: %d runtime requirements exceeds the maximum of %d", ErrInvalidRuntimeRequirements, len(declaration.Requirements), maxRuntimeRequirements)
	}

	declaredRequirements := make(map[string]struct{}, len(declaration.Requirements))
	for index, requirement := range declaration.Requirements {
		key, err := validateRuntimeIdentifier("requirement key", index, requirement.Key)
		if err != nil {
			return err
		}
		if _, duplicate := declaredRequirements[key]; duplicate {
			return fmt.Errorf("%w: duplicate runtime requirement key %q", ErrInvalidRuntimeRequirements, key)
		}
		declaredRequirements[key] = struct{}{}
		if err := validateRuntimeText(fmt.Sprintf("requirement %q label", key), requirement.Label, maxRuntimeLabelLength, true); err != nil {
			return err
		}
		if err := validateRuntimeText(fmt.Sprintf("requirement %q description", key), requirement.Description, maxRuntimeDescriptionLength, true); err != nil {
			return err
		}
		if err := validateRuntimeText(fmt.Sprintf("requirement %q disclosure", key), requirement.Disclosure, maxRuntimeDisclosureLength, false); err != nil {
			return err
		}
		adapter := strings.ToLower(strings.TrimSpace(requirement.Adapter))
		if adapter == "" {
			return fmt.Errorf("%w: requirement %q must name a registered adapter", ErrInvalidRuntimeRequirements, key)
		}
		if !slices.Contains(ValidRuntimeRequirementAdapters, adapter) {
			return fmt.Errorf("%w: requirement %q names unregistered adapter %q; registered adapters are %s", ErrInvalidRuntimeRequirements, key, adapter, strings.Join(ValidRuntimeRequirementAdapters, ", "))
		}
	}

	modeIDs := make(map[string]struct{}, len(declaration.OperatingModes))
	for index, mode := range declaration.OperatingModes {
		id, err := validateRuntimeIdentifier("operating mode id", index, mode.ID)
		if err != nil {
			return err
		}
		if _, duplicate := modeIDs[id]; duplicate {
			return fmt.Errorf("%w: duplicate operating mode id %q", ErrInvalidRuntimeRequirements, id)
		}
		modeIDs[id] = struct{}{}
		if err := validateRuntimeText(fmt.Sprintf("operating mode %q label", id), mode.Label, maxRuntimeLabelLength, true); err != nil {
			return err
		}
		if err := validateRuntimeText(fmt.Sprintf("operating mode %q description", id), mode.Description, maxRuntimeDescriptionLength, true); err != nil {
			return err
		}

		seenReferences := make(map[string]struct{}, len(mode.Requires))
		for _, rawReference := range mode.Requires {
			reference := workspace.NormalizeRuntimeIdentifier(rawReference)
			if reference == "" {
				return fmt.Errorf("%w: operating mode %q contains an invalid runtime requirement reference %q", ErrInvalidRuntimeRequirements, id, rawReference)
			}
			if _, duplicate := seenReferences[reference]; duplicate {
				return fmt.Errorf("%w: operating mode %q references runtime requirement %q more than once", ErrInvalidRuntimeRequirements, id, reference)
			}
			if _, declared := declaredRequirements[reference]; !declared {
				return fmt.Errorf("%w: operating mode %q references undeclared runtime requirement %q", ErrInvalidRuntimeRequirements, id, reference)
			}
			seenReferences[reference] = struct{}{}
		}
	}
	return nil
}

func validateRuntimeIdentifier(field string, index int, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		if field == "requirement key" {
			return "", fmt.Errorf("%w: requirement key is required", ErrInvalidRuntimeRequirements)
		}
		return "", fmt.Errorf("%w: operating mode %d is missing an id", ErrInvalidRuntimeRequirements, index+1)
	}
	if len(trimmed) > maxRuntimeIdentifierLength {
		return "", fmt.Errorf("%w: %s %q is longer than %d characters", ErrInvalidRuntimeRequirements, field, trimmed, maxRuntimeIdentifierLength)
	}
	identifier := workspace.NormalizeRuntimeIdentifier(trimmed)
	if identifier == "" {
		return "", fmt.Errorf("%w: %s %q must be lower-case letters, digits, %q, or %q and start with a letter or digit", ErrInvalidRuntimeRequirements, field, raw, "-", "_")
	}
	return identifier, nil
}

func validateRuntimeText(field, raw string, max int, required bool) error {
	text := strings.TrimSpace(raw)
	if required && text == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidRuntimeRequirements, field)
	}
	if len(text) > max {
		return fmt.Errorf("%w: %s is longer than %d characters", ErrInvalidRuntimeRequirements, field, max)
	}
	for _, r := range text {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %s contains a control character", ErrInvalidRuntimeRequirements, field)
		}
	}
	return nil
}
