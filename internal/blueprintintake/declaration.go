// Package blueprintintake implements the host-owned blueprint intake contract.
package blueprintintake

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/johnjallday/ori-agent/internal/fileparser"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const MaxRequirements = 4

const (
	ProposalKindTicket        = "ticket"
	ProposalKindMemory        = "memory"
	ProposalKindNote          = "note"
	ProposalKindCalendarEvent = "calendar_event"
)

var ErrInvalidRequirements = errors.New("invalid intake requirements")

var validProposalKinds = map[string]struct{}{
	ProposalKindTicket:        {},
	ProposalKindMemory:        {},
	ProposalKindNote:          {},
	ProposalKindCalendarEvent: {},
}

// ParseRequirements strictly decodes and normalizes a manifest's optional
// intake_requirements block. Unknown fields fail closed so a blueprint cannot
// appear to request behavior that this host does not implement.
func ParseRequirements(raw json.RawMessage, skills []string, directories []workspace.DirectoryRequirement) ([]workspace.IntakeRequirement, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var requirements []workspace.IntakeRequirement
	if err := decoder.Decode(&requirements); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequirements, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalidRequirements)
	}
	return NormalizeRequirements(requirements, skills, directories)
}

// NormalizeRequirements validates an intake declaration against the skills and
// directories declared by the same manifest. It returns an all-or-nothing,
// normalized copy suitable for persisting in workspace provenance.
func NormalizeRequirements(requirements []workspace.IntakeRequirement, skills []string, directories []workspace.DirectoryRequirement) ([]workspace.IntakeRequirement, error) {
	if len(requirements) == 0 {
		return nil, nil
	}
	if len(requirements) > MaxRequirements {
		return nil, fmt.Errorf("%w: %d entries exceeds the maximum of %d", ErrInvalidRequirements, len(requirements), MaxRequirements)
	}

	declaredSkills := make(map[string]string, len(skills))
	for _, skill := range skills {
		name := strings.TrimSpace(skill)
		if name != "" {
			declaredSkills[strings.ToLower(name)] = name
		}
	}
	declaredDirectories := make(map[string]struct{}, len(directories))
	for _, directory := range directories {
		key := normalizeKey(directory.Key)
		if key != "" {
			declaredDirectories[key] = struct{}{}
		}
	}

	out := make([]workspace.IntakeRequirement, 0, len(requirements))
	seenKeys := make(map[string]struct{}, len(requirements))
	usedDirectories := make(map[string]struct{}, len(requirements))
	for index, requirement := range requirements {
		key := normalizeKey(requirement.Key)
		if key == "" {
			return nil, fmt.Errorf("%w: entry %d is missing key", ErrInvalidRequirements, index+1)
		}
		if _, duplicate := seenKeys[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate key %q", ErrInvalidRequirements, key)
		}
		seenKeys[key] = struct{}{}

		label := strings.TrimSpace(requirement.Label)
		if label == "" {
			return nil, fmt.Errorf("%w: intake %q is missing label", ErrInvalidRequirements, key)
		}

		skillKey := strings.ToLower(strings.TrimSpace(requirement.Skill))
		skill, declared := declaredSkills[skillKey]
		if !declared {
			return nil, fmt.Errorf("%w: intake %q names skill %q, which is not declared in tools.skills", ErrInvalidRequirements, key, strings.TrimSpace(requirement.Skill))
		}

		sources := workspace.IntakeSources{
			Files:        requirement.Sources.Files,
			URL:          requirement.Sources.URL,
			DirectoryKey: normalizeKey(requirement.Sources.DirectoryKey),
		}
		if !sources.Files && !sources.URL && sources.DirectoryKey == "" {
			return nil, fmt.Errorf("%w: intake %q must enable at least one source", ErrInvalidRequirements, key)
		}
		if sources.DirectoryKey != "" {
			if _, declared := declaredDirectories[sources.DirectoryKey]; !declared {
				return nil, fmt.Errorf("%w: intake %q references undeclared directory key %q", ErrInvalidRequirements, key, sources.DirectoryKey)
			}
			if _, duplicate := usedDirectories[sources.DirectoryKey]; duplicate {
				return nil, fmt.Errorf("%w: directory key %q is used by more than one intake", ErrInvalidRequirements, sources.DirectoryKey)
			}
			usedDirectories[sources.DirectoryKey] = struct{}{}
		}

		extensions, err := normalizeExtensions(key, requirement.AcceptedExtensions)
		if err != nil {
			return nil, err
		}
		proposalKinds, err := normalizeProposalKinds(key, requirement.ProposalKinds)
		if err != nil {
			return nil, err
		}

		out = append(out, workspace.IntakeRequirement{
			Key:                key,
			Label:              label,
			Skill:              skill,
			Sources:            sources,
			AcceptedExtensions: extensions,
			ProposalKinds:      proposalKinds,
		})
	}
	return out, nil
}

func normalizeKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeExtensions(intakeKey string, extensions []string) ([]string, error) {
	if len(extensions) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(extensions))
	seen := make(map[string]struct{}, len(extensions))
	for _, raw := range extensions {
		extension := strings.ToLower(strings.TrimSpace(raw))
		if extension != "" && !strings.HasPrefix(extension, ".") {
			extension = "." + extension
		}
		if !fileparser.SupportsExtension(extension) {
			return nil, fmt.Errorf("%w: intake %q has unsupported extension %q", ErrInvalidRequirements, intakeKey, raw)
		}
		if _, duplicate := seen[extension]; duplicate {
			continue
		}
		seen[extension] = struct{}{}
		out = append(out, extension)
	}
	return out, nil
}

func normalizeProposalKinds(intakeKey string, kinds []string) ([]string, error) {
	if len(kinds) == 0 {
		return nil, fmt.Errorf("%w: intake %q must allow at least one proposal kind", ErrInvalidRequirements, intakeKey)
	}
	out := make([]string, 0, len(kinds))
	seen := make(map[string]struct{}, len(kinds))
	for _, raw := range kinds {
		kind := strings.ToLower(strings.TrimSpace(raw))
		if _, valid := validProposalKinds[kind]; !valid {
			return nil, fmt.Errorf("%w: intake %q has unsupported proposal kind %q", ErrInvalidRequirements, intakeKey, raw)
		}
		if _, duplicate := seen[kind]; duplicate {
			continue
		}
		seen[kind] = struct{}{}
		out = append(out, kind)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: intake %q must allow at least one proposal kind", ErrInvalidRequirements, intakeKey)
	}
	return out, nil
}
