package projecttemplates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

var ErrInvalidAssistantProgram = errors.New("invalid assistant program declaration")

var assistantProgramIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func normalizeAssistantProgram(raw json.RawMessage) (*workspace.AssistantProgramDeclaration, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var declaration workspace.AssistantProgramDeclaration
	if err := decoder.Decode(&declaration); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAssistantProgram, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalidAssistantProgram)
	}
	if err := normalizeAndValidateAssistantProgram(&declaration); err != nil {
		return nil, err
	}
	return workspace.CloneAssistantProgramDeclaration(&declaration), nil
}

func normalizeAndValidateAssistantProgram(declaration *workspace.AssistantProgramDeclaration) error {
	if declaration == nil {
		return fmt.Errorf("%w: declaration is required", ErrInvalidAssistantProgram)
	}
	if declaration.SchemaVersion != workspace.AssistantProgramSchemaVersion {
		return fmt.Errorf("%w: unsupported schema_version %d", ErrInvalidAssistantProgram, declaration.SchemaVersion)
	}
	declaration.ID = normalizeAssistantProgramID(declaration.ID)
	if !assistantProgramIDPattern.MatchString(declaration.ID) {
		return fmt.Errorf("%w: id must be a lowercase stable identifier", ErrInvalidAssistantProgram)
	}
	var err error
	if declaration.StationName, err = boundedAssistantText("station_name", declaration.StationName, 120, true); err != nil {
		return err
	}
	if declaration.StationDescription, err = boundedAssistantText("station_description", declaration.StationDescription, 1000, false); err != nil {
		return err
	}
	if declaration.DefaultPrimaryName, err = boundedAssistantText("default_primary_name", declaration.DefaultPrimaryName, 100, true); err != nil {
		return err
	}
	if declaration.HireTitle, err = boundedAssistantText("hire_title", declaration.HireTitle, 160, true); err != nil {
		return err
	}
	if declaration.HireDescription, err = boundedAssistantText("hire_description", declaration.HireDescription, 1000, false); err != nil {
		return err
	}
	if declaration.DisabledMessage, err = boundedAssistantText("disabled_message", declaration.DisabledMessage, 1000, false); err != nil {
		return err
	}
	if len(declaration.Roles) == 0 || len(declaration.Roles) > workspace.AssistantProgramMaxRoles {
		return fmt.Errorf("%w: roles must contain 1-%d entries", ErrInvalidAssistantProgram, workspace.AssistantProgramMaxRoles)
	}
	roleIDs := make(map[string]struct{}, len(declaration.Roles))
	primaryCount := 0
	for index := range declaration.Roles {
		role := &declaration.Roles[index]
		role.ID = normalizeAssistantProgramID(role.ID)
		if !assistantProgramIDPattern.MatchString(role.ID) {
			return fmt.Errorf("%w: role %d has an invalid id", ErrInvalidAssistantProgram, index)
		}
		if _, duplicate := roleIDs[role.ID]; duplicate {
			return fmt.Errorf("%w: duplicate role id %q", ErrInvalidAssistantProgram, role.ID)
		}
		roleIDs[role.ID] = struct{}{}
		if role.Primary {
			primaryCount++
		}
		if role.Label, err = boundedAssistantText("role label", role.Label, 100, true); err != nil {
			return err
		}
		if role.Description, err = boundedAssistantText("role description", role.Description, 1000, false); err != nil {
			return err
		}
		role.Role = strings.ToLower(strings.TrimSpace(role.Role))
		role.Type = strings.ToLower(strings.TrimSpace(role.Type))
		if role.SystemPrompt, err = boundedAssistantText("role system_prompt", role.SystemPrompt, workspace.AssistantProgramMaxText, true); err != nil {
			return err
		}
		role.Skills = normalizeAssistantSkills(role.Skills)
		if len(role.Skills) > 32 {
			return fmt.Errorf("%w: role %q declares too many skills", ErrInvalidAssistantProgram, role.ID)
		}
	}
	if primaryCount != 1 {
		return fmt.Errorf("%w: exactly one role must be primary", ErrInvalidAssistantProgram)
	}
	if len(declaration.Stages) == 0 || len(declaration.Stages) > workspace.AssistantProgramMaxStages {
		return fmt.Errorf("%w: stages must contain 1-%d entries", ErrInvalidAssistantProgram, workspace.AssistantProgramMaxStages)
	}
	stageIDs := make(map[string]struct{}, len(declaration.Stages))
	previousThreshold := -1
	for index := range declaration.Stages {
		stage := &declaration.Stages[index]
		stage.ID = normalizeAssistantProgramID(stage.ID)
		if !assistantProgramIDPattern.MatchString(stage.ID) {
			return fmt.Errorf("%w: stage %d has an invalid id", ErrInvalidAssistantProgram, index)
		}
		if _, duplicate := stageIDs[stage.ID]; duplicate {
			return fmt.Errorf("%w: duplicate stage id %q", ErrInvalidAssistantProgram, stage.ID)
		}
		stageIDs[stage.ID] = struct{}{}
		if stage.Label, err = boundedAssistantText("stage label", stage.Label, 100, true); err != nil {
			return err
		}
		if stage.Description, err = boundedAssistantText("stage description", stage.Description, 1000, false); err != nil {
			return err
		}
		if index == 0 && stage.AcceptedCompletionThreshold != 0 {
			return fmt.Errorf("%w: first stage threshold must be zero", ErrInvalidAssistantProgram)
		}
		if stage.AcceptedCompletionThreshold < 0 || stage.AcceptedCompletionThreshold <= previousThreshold {
			return fmt.Errorf("%w: stage thresholds must increase strictly", ErrInvalidAssistantProgram)
		}
		previousThreshold = stage.AcceptedCompletionThreshold
	}
	reflection := &declaration.Reflection
	if reflection.MinimumProjects < 3 || reflection.MinimumProjects > workspace.AssistantProgramMaxProjects {
		return fmt.Errorf("%w: reflection minimum_projects must be 3-%d", ErrInvalidAssistantProgram, workspace.AssistantProgramMaxProjects)
	}
	if reflection.CadenceHours < 24 || reflection.CadenceHours > 24*31 {
		return fmt.Errorf("%w: reflection cadence_hours must be between 24 and 744", ErrInvalidAssistantProgram)
	}
	if reflection.MaxProjects < reflection.MinimumProjects || reflection.MaxProjects > workspace.AssistantProgramMaxProjects {
		return fmt.Errorf("%w: reflection max_projects is outside its bounds", ErrInvalidAssistantProgram)
	}
	if reflection.MaxEventsPerProject < 1 || reflection.MaxEventsPerProject > workspace.AssistantProgramMaxEvents {
		return fmt.Errorf("%w: reflection max_events_per_project is outside its bounds", ErrInvalidAssistantProgram)
	}
	if reflection.MaxCandidates < 1 || reflection.MaxCandidates > workspace.AssistantProgramMaxCandidates {
		return fmt.Errorf("%w: reflection max_candidates is outside its bounds", ErrInvalidAssistantProgram)
	}
	if reflection.MaxEvidence < 3 || reflection.MaxEvidence > workspace.AssistantProgramMaxEvidence {
		return fmt.Errorf("%w: reflection max_evidence is outside its bounds", ErrInvalidAssistantProgram)
	}
	if reflection.Rubric, err = boundedAssistantText("reflection rubric", reflection.Rubric, workspace.AssistantProgramMaxText, true); err != nil {
		return err
	}
	return nil
}

func normalizeAssistantProgramID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func boundedAssistantText(field, value string, limit int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidAssistantProgram, field)
	}
	if len(value) > limit {
		return "", fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidAssistantProgram, field, limit)
	}
	if strings.ContainsRune(value, '\x00') || strings.Contains(strings.ToLower(value), "://") {
		return "", fmt.Errorf("%w: %s contains a forbidden URL or control value", ErrInvalidAssistantProgram, field)
	}
	return value, nil
}

func normalizeAssistantSkills(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || !assistantProgramIDPattern.MatchString(value) {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
