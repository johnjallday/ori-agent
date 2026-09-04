package specialist

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// SetupJourneySchemaVersion is the only declaration schema this build can
	// expose. Declaration Version is revisioned independently within the schema.
	SetupJourneySchemaVersion = 1

	MaxSetupJourneyBytes      = 16 << 10
	MaxSetupJourneyIDBytes    = 64
	MaxSetupJourneyTitleBytes = 200
	MaxSetupJourneyTextBytes  = 2_000
	MaxSetupJourneyVersion    = 1_000_000
	SetupJourneyRequiredSteps = 5
)

// SetupStepKind selects one closed host-owned setup primitive. It never names
// an adapter, route, command, source, or executable implementation.
type SetupStepKind string

const (
	SetupStepIntegrationInstall       SetupStepKind = "integration_install"
	SetupStepProjectConnect           SetupStepKind = "project_connect"
	SetupStepWorkspaceSetup           SetupStepKind = "workspace_setup"
	SetupStepAssistantProgramStaffing SetupStepKind = "assistant_program_staffing"
	SetupStepSummary                  SetupStepKind = "summary"
)

var setupJourneyStepOrder = [...]SetupStepKind{
	SetupStepIntegrationInstall,
	SetupStepProjectConnect,
	SetupStepWorkspaceSetup,
	SetupStepAssistantProgramStaffing,
	SetupStepSummary,
}

// SetupJourney is bounded inert discovery data. Behavior for each step kind is
// compiled into the host; no declaration field can select executable behavior.
type SetupJourney struct {
	SchemaVersion              int                `json:"schema_version"`
	Version                    int                `json:"version"`
	ID                         string             `json:"id"`
	Title                      string             `json:"title"`
	Description                string             `json:"description"`
	IntegrationKey             string             `json:"integration_key"`
	ExpectedBlueprintID        string             `json:"expected_blueprint_id"`
	ExpectedAssistantProgramID string             `json:"expected_assistant_program_id"`
	Steps                      []SetupJourneyStep `json:"steps"`
}

// SetupJourneyStep is one display-only item in the fixed v1 order.
type SetupJourneyStep struct {
	ID          string        `json:"id"`
	Kind        SetupStepKind `json:"kind"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
}

// ParseSetupJourney strictly decodes and normalizes one declaration. It is
// exported so fixtures and future trusted import boundaries use the same
// fail-closed contract as built-in declarations.
func ParseSetupJourney(data []byte) (*SetupJourney, error) {
	if len(data) == 0 {
		return nil, errors.New("setup journey declaration is empty")
	}
	if len(data) > MaxSetupJourneyBytes {
		return nil, fmt.Errorf("setup journey declaration exceeds %d bytes", MaxSetupJourneyBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var declaration SetupJourney
	if err := decoder.Decode(&declaration); err != nil {
		return nil, fmt.Errorf("decode setup journey declaration: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("setup journey declaration contains trailing data")
		}
		return nil, fmt.Errorf("decode setup journey declaration trailing data: %w", err)
	}
	return NormalizeSetupJourney(declaration)
}

// NormalizeSetupJourney validates a compiled declaration and returns an
// independent normalized copy. It deliberately round-trips through JSON for
// the same serialized-size bound used by ParseSetupJourney.
func NormalizeSetupJourney(declaration SetupJourney) (*SetupJourney, error) {
	encoded, err := json.Marshal(declaration)
	if err != nil {
		return nil, fmt.Errorf("encode setup journey declaration: %w", err)
	}
	if len(encoded) > MaxSetupJourneyBytes {
		return nil, fmt.Errorf("setup journey declaration exceeds %d bytes", MaxSetupJourneyBytes)
	}

	declaration.ID = normalizeSetupJourneyID(declaration.ID)
	declaration.IntegrationKey = normalizeSetupJourneyID(declaration.IntegrationKey)
	declaration.ExpectedBlueprintID = normalizeSetupJourneyID(declaration.ExpectedBlueprintID)
	declaration.ExpectedAssistantProgramID = normalizeSetupJourneyID(declaration.ExpectedAssistantProgramID)
	declaration.Title = strings.TrimSpace(declaration.Title)
	declaration.Description = strings.TrimSpace(declaration.Description)

	if declaration.SchemaVersion != SetupJourneySchemaVersion {
		return nil, fmt.Errorf("setup journey schema_version must be %d", SetupJourneySchemaVersion)
	}
	if declaration.Version < 1 || declaration.Version > MaxSetupJourneyVersion {
		return nil, fmt.Errorf("setup journey version must be between 1 and %d", MaxSetupJourneyVersion)
	}
	if err := validateSetupJourneyID("id", declaration.ID); err != nil {
		return nil, err
	}
	if err := validateSetupJourneyText("title", declaration.Title, MaxSetupJourneyTitleBytes); err != nil {
		return nil, err
	}
	if err := validateSetupJourneyText("description", declaration.Description, MaxSetupJourneyTextBytes); err != nil {
		return nil, err
	}
	for field, value := range map[string]string{
		"integration_key":               declaration.IntegrationKey,
		"expected_blueprint_id":         declaration.ExpectedBlueprintID,
		"expected_assistant_program_id": declaration.ExpectedAssistantProgramID,
	} {
		if err := validateSetupJourneyID(field, value); err != nil {
			return nil, err
		}
	}
	if len(declaration.Steps) != SetupJourneyRequiredSteps {
		return nil, fmt.Errorf("setup journey must contain exactly %d steps", SetupJourneyRequiredSteps)
	}

	seenIDs := make(map[string]struct{}, len(declaration.Steps))
	steps := make([]SetupJourneyStep, len(declaration.Steps))
	for index := range declaration.Steps {
		step := declaration.Steps[index]
		step.ID = normalizeSetupJourneyID(step.ID)
		step.Kind = SetupStepKind(normalizeSetupJourneyID(string(step.Kind)))
		step.Title = strings.TrimSpace(step.Title)
		step.Description = strings.TrimSpace(step.Description)

		if err := validateSetupJourneyID(fmt.Sprintf("steps[%d].id", index), step.ID); err != nil {
			return nil, err
		}
		if _, duplicate := seenIDs[step.ID]; duplicate {
			return nil, fmt.Errorf("setup journey step id %q is duplicated", step.ID)
		}
		seenIDs[step.ID] = struct{}{}
		if step.Kind != setupJourneyStepOrder[index] {
			return nil, fmt.Errorf("setup journey step %d kind must be %q", index, setupJourneyStepOrder[index])
		}
		if err := validateSetupJourneyText(fmt.Sprintf("steps[%d].title", index), step.Title, MaxSetupJourneyTitleBytes); err != nil {
			return nil, err
		}
		if err := validateSetupJourneyText(fmt.Sprintf("steps[%d].description", index), step.Description, MaxSetupJourneyTextBytes); err != nil {
			return nil, err
		}
		steps[index] = step
	}
	declaration.Steps = steps
	return &declaration, nil
}

var setupJourneyIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func normalizeSetupJourneyID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateSetupJourneyID(field, value string) error {
	if len(value) == 0 || len(value) > MaxSetupJourneyIDBytes || !setupJourneyIDPattern.MatchString(value) {
		return fmt.Errorf("setup journey %s must be a lower-case stable id of at most %d bytes", field, MaxSetupJourneyIDBytes)
	}
	return nil
}

func validateSetupJourneyText(field, value string, maxBytes int) error {
	if value == "" {
		return fmt.Errorf("setup journey %s is required", field)
	}
	if !utf8.ValidString(value) || len(value) > maxBytes {
		return fmt.Errorf("setup journey %s must be valid UTF-8 of at most %d bytes", field, maxBytes)
	}
	lower := strings.ToLower(value)
	if strings.ContainsAny(value, "<>`*") || strings.Contains(value, "](") || strings.Contains(value, "![") ||
		strings.Contains(lower, "://") || strings.Contains(lower, "www.") ||
		strings.Contains(lower, "javascript:") || strings.Contains(lower, "data:") ||
		strings.Contains(lower, "file:") || strings.Contains(lower, "mailto:") ||
		strings.Contains(lower, "http:") || strings.Contains(lower, "https:") {
		return fmt.Errorf("setup journey %s must contain plain display text only", field)
	}
	for _, r := range value {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.In(r, unicode.Cf) {
			return fmt.Errorf("setup journey %s contains a disallowed control character", field)
		}
	}
	return nil
}

func mustNormalizeRegistry(entries []Entry) []Entry {
	registry := make([]Entry, len(entries))
	for index := range entries {
		registry[index] = cloneEntry(entries[index])
		if registry[index].SetupJourney == nil {
			continue
		}
		normalized, err := NormalizeSetupJourney(*registry[index].SetupJourney)
		if err != nil {
			panic(fmt.Sprintf("invalid built-in specialist setup journey for %q: %v", registry[index].Slug, err))
		}
		registry[index].SetupJourney = normalized
	}
	return registry
}

func cloneSetupJourney(source *SetupJourney) *SetupJourney {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Steps = append([]SetupJourneyStep(nil), source.Steps...)
	return &clone
}
