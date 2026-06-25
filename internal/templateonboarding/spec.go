// Package templateonboarding implements optional, template-authored onboarding
// workflows for project templates: an `onboarding` block inside template.json
// that declares intake fields and a completion action the entry agent runs when
// a workspace is created from the template. This package owns the spec types,
// parsing, and validation; the projecttemplates package only preserves the raw
// block and never interprets it, keeping the file-copy engine domain-blind.
package templateonboarding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// DefaultVersion is assumed when an onboarding block omits `version`.
const DefaultVersion = "1"

// FieldType enumerates the intake field types the auto-generated form supports.
type FieldType string

const (
	FieldString  FieldType = "string"
	FieldNumber  FieldType = "number"
	FieldEnum    FieldType = "enum"
	FieldBoolean FieldType = "boolean"
)

// ActionType enumerates completion action types. v1 executes only ActionNone
// and ActionTask; ActionTool and ActionWorkflowTemplate are reserved names that
// validate but block at execution time (see PRD §4.4).
type ActionType string

const (
	ActionNone             ActionType = "none"
	ActionTask             ActionType = "task"
	ActionTool             ActionType = "tool"
	ActionWorkflowTemplate ActionType = "workflow_template"
)

// ExecutableInV1 reports whether this action type is actually executed in v1.
func (a ActionType) ExecutableInV1() bool {
	return a == ActionNone || a == ActionTask
}

// FieldValidation carries optional per-field constraints.
type FieldValidation struct {
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
}

// Field is one intake question rendered in the form and/or asked in chat.
type Field struct {
	ID    string    `json:"id"`
	Label string    `json:"label"`
	Type  FieldType `json:"type"`
	// Default is decoded as a generic JSON value (string/float64/bool); its Go
	// type is checked against Type during validation.
	Default    any              `json:"default,omitempty"`
	Required   bool             `json:"required,omitempty"`
	Options    []string         `json:"options,omitempty"`
	Prompt     string           `json:"prompt,omitempty"`
	Validation *FieldValidation `json:"validation,omitempty"`
}

// CompletionAction is the single action run after intake completes. `Inputs`
// values may be literals or `${fields.<id>}` references resolved at execution.
type CompletionAction struct {
	Type                ActionType        `json:"type"`
	Ref                 string            `json:"ref,omitempty"`
	Instructions        string            `json:"instructions,omitempty"`
	SkillRefs           []string          `json:"skill_refs,omitempty"`
	Inputs              map[string]string `json:"inputs,omitempty"`
	InstantiateSkeleton bool              `json:"instantiate_skeleton,omitempty"`
}

// DependencyType enumerates declarable completion dependencies.
type DependencyType string

const (
	DependencySkill            DependencyType = "skill"
	DependencyMCPServer        DependencyType = "mcp_server"
	DependencyTool             DependencyType = "tool"
	DependencyWorkflowTemplate DependencyType = "workflow_template"
)

// Dependency is a declared prerequisite for the completion action. v1 never
// auto-installs: a missing dependency blocks the session with remediation text.
type Dependency struct {
	Type DependencyType `json:"type"`
	Ref  string         `json:"ref"`
}

// OnboardingSpec is the parsed `onboarding` block from template.json.
type OnboardingSpec struct {
	Version      string           `json:"version,omitempty"`
	Fields       []Field          `json:"fields,omitempty"`
	Completion   CompletionAction `json:"completion"`
	Dependencies []Dependency     `json:"dependencies,omitempty"`
}

// ParseSpec decodes the raw `onboarding` block. A nil/empty/`null` block returns
// (nil, nil): the template simply has no onboarding. Invalid JSON returns an
// error so the caller can disable onboarding for that template without failing
// workspace creation. Unknown top-level keys are ignored for forward
// compatibility. ParseSpec does not check semantics — call Validate for that.
func ParseSpec(raw json.RawMessage) (*OnboardingSpec, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var spec OnboardingSpec
	if err := json.Unmarshal(trimmed, &spec); err != nil {
		return nil, fmt.Errorf("onboarding: invalid JSON: %w", err)
	}
	if strings.TrimSpace(spec.Version) == "" {
		spec.Version = DefaultVersion
	}
	return &spec, nil
}
