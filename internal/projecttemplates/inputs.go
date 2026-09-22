package projecttemplates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The inputs declaration lets a blueprint ask typed questions at creation.
// Number and select answers may be substituted only into declared apply_to
// files. Text and URL answers are setup data: they are never substituted into
// scaffolded files under any circumstances, even when a manifest lists a file
// in apply_to. Every other scaffold file stays a byte-for-byte copy, and file
// and folder names keep using {{name}}/{{date}} alone.
const (
	// InputsSchemaVersion is the only declaration version v1 understands.
	InputsSchemaVersion = 1

	maxInputsBytes            = 16 << 10
	maxInputFields            = 8
	maxInputApplyToPaths      = 8
	maxInputApplyToFileBytes  = 1 << 20
	minInputSelectOptions     = 2
	maxInputSelectOptions     = 24
	maxInputTitleLength       = 64
	maxInputLabelLength       = 64
	maxInputUnitLength        = 16
	maxInputOptionLabelLength = 48
	// maxInputNumberMagnitude keeps declared bounds inside the range where a
	// float64 still round-trips a plain decimal exactly, so a value shown in the
	// form, validated on the server, and written into a file are the same text.
	maxInputNumberMagnitude = 1e9
)

// InputFieldType is the closed set of v1 field types.
type InputFieldType string

const (
	InputFieldNumber InputFieldType = "number"
	InputFieldSelect InputFieldType = "select"
	InputFieldText   InputFieldType = "text"
	InputFieldURL    InputFieldType = "url"
)

// ErrInvalidInputs reports a declaration that could not be understood. A
// blueprint carrying one offers no inputs and cannot create a workspace.
// This includes text or URL tokens in apply_to files: those answers are never
// written into scaffolded files under any circumstances.
var ErrInvalidInputs = errors.New("invalid inputs declaration")

// ErrInputValue reports a supplied value that the declaration does not allow.
// It is user-relayable and always names the field.
var ErrInputValue = errors.New("invalid input value")

// InputsDeclaration is inert blueprint data: labels, bounds, option lists, and
// the scaffold-relative files the values may be written into. It selects no
// code path and grants no authority.
type InputsDeclaration struct {
	SchemaVersion int          `json:"schema_version"`
	Title         string       `json:"title"`
	ApplyTo       []string     `json:"apply_to"`
	Fields        []InputField `json:"fields"`
}

// InputField is one question. Number fields carry Min/Max/Step/Unit; select
// fields carry Options; URL fields may name the URL-capable intake they prefill.
// Defaults are float64 for numbers and strings for the other field types, so
// the API projection is the same shape the manifest declared.
type InputField struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	Type      InputFieldType `json:"type"`
	Unit      string         `json:"unit,omitempty"`
	Min       float64        `json:"min,omitempty"`
	Max       float64        `json:"max,omitempty"`
	Step      float64        `json:"step,omitempty"`
	Default   any            `json:"default"`
	Options   []InputOption  `json:"options,omitempty"`
	IntakeKey string         `json:"intake_key,omitempty"`
}

// InputOption is one choice of a select field. Value is what reaches the file;
// Label is display text only.
type InputOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// rawInputsDeclaration is the strict on-disk shape. Pointers and raw messages
// distinguish "absent" from "zero" so a number field carrying select keys (or
// the reverse) is rejected instead of silently half-honored.
type rawInputsDeclaration struct {
	SchemaVersion int             `json:"schema_version"`
	Title         string          `json:"title"`
	ApplyTo       []string        `json:"apply_to"`
	Fields        []rawInputField `json:"fields"`
}

type rawInputField struct {
	ID        string           `json:"id"`
	Label     string           `json:"label"`
	Type      string           `json:"type"`
	Unit      *string          `json:"unit,omitempty"`
	Min       *float64         `json:"min,omitempty"`
	Max       *float64         `json:"max,omitempty"`
	Step      *float64         `json:"step,omitempty"`
	Default   json.RawMessage  `json:"default"`
	Options   []rawInputOption `json:"options,omitempty"`
	IntakeKey *string          `json:"intake_key,omitempty"`
}

type rawInputOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

var (
	inputFieldIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	inputOptionValuePattern = regexp.MustCompile(`^[A-Za-z0-9 ._/#-]{1,32}$`)
	// inputTokenIDPattern matches the id immediately after a token opener, so a
	// malformed or unterminated token is reported rather than left in place.
	inputTokenIDPattern = regexp.MustCompile(`^([a-z][a-z0-9_]{0,31})\}\}`)
)

// inputTokenPrefix is the only content token a blueprint may use.
const inputTokenPrefix = "{{input."

// normalizeInputs strictly decodes and fully validates an inputs declaration
// against the scaffold it applies to. scaffoldRoot is the template folder whose
// files apply_to refers to; every listed path must resolve to a regular text
// file inside it.
func normalizeInputs(raw json.RawMessage, scaffoldRoot string) (*InputsDeclaration, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(trimmed) > maxInputsBytes {
		return nil, fmt.Errorf("%w: declaration exceeds size limit", ErrInvalidInputs)
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var declared rawInputsDeclaration
	if err := decoder.Decode(&declared); err != nil {
		return nil, fmt.Errorf("%w: malformed declaration", ErrInvalidInputs)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalidInputs)
	}
	if declared.SchemaVersion != InputsSchemaVersion {
		return nil, fmt.Errorf("%w: unsupported schema version", ErrInvalidInputs)
	}

	title := strings.TrimSpace(declared.Title)
	if title == "" || len(title) > maxInputTitleLength {
		return nil, fmt.Errorf("%w: title must be 1-%d characters", ErrInvalidInputs, maxInputTitleLength)
	}

	fields, err := normalizeInputFields(declared.Fields)
	if err != nil {
		return nil, err
	}
	applyTo, err := normalizeInputApplyTo(declared.ApplyTo, scaffoldRoot, fields)
	if err != nil {
		return nil, err
	}

	declaration := &InputsDeclaration{
		SchemaVersion: declared.SchemaVersion,
		Title:         title,
		ApplyTo:       applyTo,
		Fields:        fields,
	}
	if err := validateInputTokens(declaration, scaffoldRoot); err != nil {
		return nil, err
	}
	return declaration, nil
}

func normalizeInputFields(declared []rawInputField) ([]InputField, error) {
	if len(declared) == 0 || len(declared) > maxInputFields {
		return nil, fmt.Errorf("%w: a declaration needs 1-%d fields", ErrInvalidInputs, maxInputFields)
	}
	fields := make([]InputField, 0, len(declared))
	seen := make(map[string]struct{}, len(declared))
	for _, item := range declared {
		field, err := normalizeInputField(item)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[field.ID]; duplicate {
			return nil, fmt.Errorf("%w: field %q is declared twice", ErrInvalidInputs, field.ID)
		}
		seen[field.ID] = struct{}{}
		fields = append(fields, field)
	}
	return fields, nil
}

func normalizeInputField(declared rawInputField) (InputField, error) {
	id := strings.TrimSpace(declared.ID)
	if !inputFieldIDPattern.MatchString(id) {
		return InputField{}, fmt.Errorf("%w: field id %q must be lowercase letters, digits, and underscores (1-32 characters, starting with a letter)", ErrInvalidInputs, declared.ID)
	}
	label := strings.TrimSpace(declared.Label)
	if label == "" || len(label) > maxInputLabelLength {
		return InputField{}, fmt.Errorf("%w: field %q needs a label of 1-%d characters", ErrInvalidInputs, id, maxInputLabelLength)
	}
	field := InputField{ID: id, Label: label, Type: InputFieldType(strings.TrimSpace(declared.Type))}
	switch field.Type {
	case InputFieldNumber:
		if declared.IntakeKey != nil {
			return InputField{}, fmt.Errorf("%w: number field %q cannot declare intake_key", ErrInvalidInputs, id)
		}
		if len(declared.Options) != 0 {
			return InputField{}, fmt.Errorf("%w: number field %q cannot declare options", ErrInvalidInputs, id)
		}
		if declared.Min == nil || declared.Max == nil || declared.Step == nil {
			return InputField{}, fmt.Errorf("%w: number field %q needs min, max, and step", ErrInvalidInputs, id)
		}
		var defaultValue float64
		if err := json.Unmarshal(bytes.TrimSpace(declared.Default), &defaultValue); err != nil {
			return InputField{}, fmt.Errorf("%w: number field %q needs a numeric default", ErrInvalidInputs, id)
		}
		min, max, step := *declared.Min, *declared.Max, *declared.Step
		for _, value := range []float64{min, max, step, defaultValue} {
			if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > maxInputNumberMagnitude {
				return InputField{}, fmt.Errorf("%w: number field %q has a bound outside the supported range", ErrInvalidInputs, id)
			}
		}
		if step <= 0 {
			return InputField{}, fmt.Errorf("%w: number field %q needs a step greater than zero", ErrInvalidInputs, id)
		}
		if min > max {
			return InputField{}, fmt.Errorf("%w: number field %q has min greater than max", ErrInvalidInputs, id)
		}
		if defaultValue < min || defaultValue > max {
			return InputField{}, fmt.Errorf("%w: number field %q has a default outside %s-%s", ErrInvalidInputs, id, FormatInputNumber(min), FormatInputNumber(max))
		}
		if !inputNumberOnStep(defaultValue, min, step) {
			return InputField{}, fmt.Errorf("%w: number field %q has a default that is not a multiple of its step", ErrInvalidInputs, id)
		}
		if declared.Unit != nil {
			unit := strings.TrimSpace(*declared.Unit)
			if len(unit) > maxInputUnitLength {
				return InputField{}, fmt.Errorf("%w: number field %q has a unit longer than %d characters", ErrInvalidInputs, id, maxInputUnitLength)
			}
			field.Unit = unit
		}
		field.Min, field.Max, field.Step, field.Default = min, max, step, defaultValue
	case InputFieldSelect:
		if declared.Min != nil || declared.Max != nil || declared.Step != nil || declared.Unit != nil || declared.IntakeKey != nil {
			return InputField{}, fmt.Errorf("%w: select field %q cannot declare number bounds or a unit", ErrInvalidInputs, id)
		}
		options, err := normalizeInputOptions(id, declared.Options)
		if err != nil {
			return InputField{}, err
		}
		var defaultValue string
		if err := json.Unmarshal(bytes.TrimSpace(declared.Default), &defaultValue); err != nil {
			return InputField{}, fmt.Errorf("%w: select field %q needs a string default", ErrInvalidInputs, id)
		}
		if !inputOptionsContain(options, defaultValue) {
			return InputField{}, fmt.Errorf("%w: select field %q has a default that is not one of its options", ErrInvalidInputs, id)
		}
		field.Options, field.Default = options, defaultValue
	case InputFieldText:
		if declared.Min != nil || declared.Max != nil || declared.Step != nil || declared.Unit != nil || len(declared.Options) != 0 || declared.IntakeKey != nil {
			return InputField{}, fmt.Errorf("%w: text field %q can only declare a string default", ErrInvalidInputs, id)
		}
		var defaultValue string
		if err := json.Unmarshal(bytes.TrimSpace(declared.Default), &defaultValue); err != nil || !validInputText(defaultValue) {
			return InputField{}, fmt.Errorf("%w: text field %q needs a one-line default of at most 200 characters", ErrInvalidInputs, id)
		}
		field.Default = defaultValue
	case InputFieldURL:
		if declared.Min != nil || declared.Max != nil || declared.Step != nil || declared.Unit != nil || len(declared.Options) != 0 {
			return InputField{}, fmt.Errorf("%w: url field %q can only declare a string default and optional intake_key", ErrInvalidInputs, id)
		}
		var defaultValue string
		if err := json.Unmarshal(bytes.TrimSpace(declared.Default), &defaultValue); err != nil || !validInputURL(defaultValue) {
			return InputField{}, fmt.Errorf("%w: url field %q needs an http or https default of at most 2000 characters", ErrInvalidInputs, id)
		}
		if declared.IntakeKey != nil {
			// Intake keys use the intake declaration's normalization, not input-ID
			// syntax: established keys such as "course-materials" contain hyphens.
			intakeKey := strings.ToLower(strings.TrimSpace(*declared.IntakeKey))
			if intakeKey == "" {
				return InputField{}, fmt.Errorf("%w: url field %q has an invalid intake_key", ErrInvalidInputs, id)
			}
			field.IntakeKey = intakeKey
		}
		field.Default = defaultValue
	default:
		return InputField{}, fmt.Errorf("%w: field %q has unsupported type %q", ErrInvalidInputs, id, declared.Type)
	}
	return field, nil
}

func normalizeInputOptions(fieldID string, declared []rawInputOption) ([]InputOption, error) {
	if len(declared) < minInputSelectOptions || len(declared) > maxInputSelectOptions {
		return nil, fmt.Errorf("%w: select field %q needs %d-%d options", ErrInvalidInputs, fieldID, minInputSelectOptions, maxInputSelectOptions)
	}
	options := make([]InputOption, 0, len(declared))
	seen := make(map[string]struct{}, len(declared))
	for _, item := range declared {
		value := item.Value
		if !inputOptionValuePattern.MatchString(value) {
			return nil, fmt.Errorf("%w: select field %q has an option value that is not plain portable text", ErrInvalidInputs, fieldID)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("%w: select field %q repeats option value %q", ErrInvalidInputs, fieldID, value)
		}
		seen[value] = struct{}{}
		label := strings.TrimSpace(item.Label)
		if label == "" {
			label = value
		}
		if len(label) > maxInputOptionLabelLength {
			return nil, fmt.Errorf("%w: select field %q has an option label longer than %d characters", ErrInvalidInputs, fieldID, maxInputOptionLabelLength)
		}
		options = append(options, InputOption{Value: value, Label: label})
	}
	return options, nil
}

// normalizeInputApplyTo resolves the declared scaffold-relative paths. Each one
// must name an existing regular file inside the scaffold that is small enough
// and textual enough to rewrite safely; a path that does not is a load error
// rather than a file quietly skipped at creation time.
func normalizeInputApplyTo(declared []string, scaffoldRoot string, fields []InputField) ([]string, error) {
	requiresApplyTo := false
	for _, field := range fields {
		requiresApplyTo = requiresApplyTo || field.Type == InputFieldNumber || field.Type == InputFieldSelect
	}
	if len(declared) == 0 && !requiresApplyTo {
		return nil, nil
	}
	if len(declared) == 0 || len(declared) > maxInputApplyToPaths {
		return nil, fmt.Errorf("%w: apply_to needs 1-%d paths when a number or select field is declared", ErrInvalidInputs, maxInputApplyToPaths)
	}
	root := strings.TrimSpace(scaffoldRoot)
	if root == "" {
		return nil, fmt.Errorf("%w: apply_to cannot be resolved without a scaffold", ErrInvalidInputs)
	}
	paths := make([]string, 0, len(declared))
	seen := make(map[string]struct{}, len(declared))
	for _, item := range declared {
		relative := path.Clean(filepath.ToSlash(strings.TrimSpace(item)))
		if relative == "" || relative == "." || !filepath.IsLocal(filepath.FromSlash(relative)) {
			return nil, fmt.Errorf("%w: apply_to path %q is not a scaffold-relative file", ErrInvalidInputs, item)
		}
		if relative == ManifestFileName || strings.HasPrefix(relative, DashboardDirName+"/") {
			return nil, fmt.Errorf("%w: apply_to path %q is not project content", ErrInvalidInputs, item)
		}
		if _, duplicate := seen[relative]; duplicate {
			return nil, fmt.Errorf("%w: apply_to lists %q twice", ErrInvalidInputs, relative)
		}
		seen[relative] = struct{}{}
		if err := validateInputApplyToFile(root, relative); err != nil {
			return nil, err
		}
		paths = append(paths, relative)
	}
	return paths, nil
}

func validateInputApplyToFile(scaffoldRoot, relative string) error {
	absolute := filepath.Join(scaffoldRoot, filepath.FromSlash(relative))
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: apply_to path %q is not a regular file in the scaffold", ErrInvalidInputs, relative)
	}
	if info.Size() > maxInputApplyToFileBytes {
		return fmt.Errorf("%w: apply_to path %q is larger than the %d KiB substitution limit", ErrInvalidInputs, relative, maxInputApplyToFileBytes>>10)
	}
	data, err := os.ReadFile(absolute) // #nosec G304 -- relative is a contained, validated scaffold path under a caller-canonicalized root
	if err != nil {
		return fmt.Errorf("%w: apply_to path %q could not be read", ErrInvalidInputs, relative)
	}
	if isBinary(data) || !utf8.Valid(data) {
		return fmt.Errorf("%w: apply_to path %q is not a text file", ErrInvalidInputs, relative)
	}
	return nil
}

// validateInputTokens checks that every token in the scaffold names a declared
// field. A field that is declared but never used is fine — the author may be
// preparing one — but a token no field backs would survive into the created
// project, so it fails the load and names both the file and the token.
func validateInputTokens(declaration *InputsDeclaration, scaffoldRoot string) error {
	declared := make(map[string]InputField, len(declaration.Fields))
	for _, field := range declaration.Fields {
		declared[field.ID] = field
	}
	for _, relative := range declaration.ApplyTo {
		absolute := filepath.Join(scaffoldRoot, filepath.FromSlash(relative))
		data, err := os.ReadFile(absolute) // #nosec G304 -- relative was validated as a contained regular file above
		if err != nil {
			return fmt.Errorf("%w: apply_to path %q could not be read", ErrInvalidInputs, relative)
		}
		if err := validateInputTokensIn(string(data), relative, declared); err != nil {
			return err
		}
	}
	return validateScaffoldNamesCarryNoInputTokens(scaffoldRoot)
}

func validateInputTokensIn(content, relative string, declared map[string]InputField) error {
	for offset := 0; ; {
		index := strings.Index(content[offset:], inputTokenPrefix)
		if index < 0 {
			return nil
		}
		start := offset + index + len(inputTokenPrefix)
		match := inputTokenIDPattern.FindStringSubmatch(content[start:])
		if match == nil {
			return fmt.Errorf("%w: %q contains a malformed token at byte %d", ErrInvalidInputs, relative, offset+index)
		}
		field, ok := declared[match[1]]
		if !ok {
			return fmt.Errorf("%w: %q uses {{input.%s}}, which no field declares", ErrInvalidInputs, relative, match[1])
		}
		if field.Type == InputFieldText || field.Type == InputFieldURL {
			return fmt.Errorf("%w: %q uses {{input.%s}}, but %s answers are never written into scaffolded files", ErrInvalidInputs, relative, match[1], field.Type)
		}
		offset = start + len(match[0])
	}
}

// validateScaffoldNamesCarryNoInputTokens keeps substitution out of file and
// folder names. Names are resolved before any value is known and feed directly
// into filesystem paths, so they stay limited to {{name}} and {{date}}.
func validateScaffoldNamesCarryNoInputTokens(scaffoldRoot string) error {
	var offender string
	err := fs.WalkDir(os.DirFS(scaffoldRoot), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if relative == "." {
			return nil
		}
		if strings.Contains(relative, inputTokenPrefix) {
			offender = relative
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w: the scaffold could not be read", ErrInvalidInputs)
	}
	if offender != "" {
		return fmt.Errorf("%w: scaffold entry %q uses an input token in its name", ErrInvalidInputs, offender)
	}
	return nil
}

// HasInputs reports whether the template declares a usable inputs block.
func (t Template) HasInputs() bool {
	return t.Inputs != nil && len(t.Inputs.Fields) > 0
}

// HasInvalidInputs reports a declared inputs block that could not be honored.
// Creation refuses such a template rather than scaffolding files that still
// contain the author's tokens.
func (t Template) HasInvalidInputs() bool {
	return strings.TrimSpace(t.InputsError) != ""
}

// ResolveInputValues validates the values a caller supplied against the
// declaration and returns the canonical text that will be substituted into the
// scaffold, keyed by field id.
//
// This is the single validator: the create endpoint calls it on the request
// body, and instantiation calls it again on whatever it was handed, so a value
// that was never checked can never reach a file. A missing id takes the
// declared default; an id the blueprint does not declare is an error rather
// than an ignored extra.
func ResolveInputValues(declaration *InputsDeclaration, provided map[string]json.RawMessage) (map[string]string, error) {
	if declaration == nil || len(declaration.Fields) == 0 {
		if len(provided) > 0 {
			return nil, fmt.Errorf("%w: this blueprint does not ask for any inputs", ErrInputValue)
		}
		return nil, nil
	}
	declared := make(map[string]InputField, len(declaration.Fields))
	for _, field := range declaration.Fields {
		declared[field.ID] = field
	}
	for id := range provided {
		if _, ok := declared[id]; !ok {
			return nil, fmt.Errorf("%w: %q is not one of this blueprint's inputs", ErrInputValue, id)
		}
	}
	resolved := make(map[string]string, len(declaration.Fields))
	for _, field := range declaration.Fields {
		raw, supplied := provided[field.ID]
		if !supplied || len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			resolved[field.ID] = field.defaultText()
			continue
		}
		value, err := field.resolveValue(bytes.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		resolved[field.ID] = value
	}
	return resolved, nil
}

func (field InputField) resolveValue(raw json.RawMessage) (string, error) {
	switch field.Type {
	case InputFieldNumber:
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("%w: %s must be a number between %s and %s", ErrInputValue, field.Label, FormatInputNumber(field.Min), FormatInputNumber(field.Max))
		}
		if math.IsNaN(value) || math.IsInf(value, 0) || value < field.Min || value > field.Max {
			return "", fmt.Errorf("%w: %s must be between %s and %s", ErrInputValue, field.Label, FormatInputNumber(field.Min), FormatInputNumber(field.Max))
		}
		if !inputNumberOnStep(value, field.Min, field.Step) {
			return "", fmt.Errorf("%w: %s must change in steps of %s from %s", ErrInputValue, field.Label, FormatInputNumber(field.Step), FormatInputNumber(field.Min))
		}
		return FormatInputNumber(value), nil
	case InputFieldSelect:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("%w: %s must be one of %s", ErrInputValue, field.Label, strings.Join(field.optionValues(), ", "))
		}
		if !inputOptionsContain(field.Options, value) {
			return "", fmt.Errorf("%w: %s must be one of %s", ErrInputValue, field.Label, strings.Join(field.optionValues(), ", "))
		}
		return value, nil
	case InputFieldText:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || !validInputText(value) {
			return "", fmt.Errorf("%w: %s must be one line of at most 200 characters", ErrInputValue, field.Label)
		}
		return value, nil
	case InputFieldURL:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || !validInputURL(value) {
			return "", fmt.Errorf("%w: %s must be an http or https URL of at most 2000 characters", ErrInputValue, field.Label)
		}
		return value, nil
	default:
		return "", fmt.Errorf("%w: %s has an unsupported type", ErrInputValue, field.Label)
	}
}

// defaultText is the canonical substitution text for a field's declared
// default. Normalization guarantees Default already matches the field type.
func (field InputField) defaultText() string {
	switch value := field.Default.(type) {
	case float64:
		return FormatInputNumber(value)
	case string:
		return value
	default:
		return ""
	}
}

func (field InputField) optionValues() []string {
	values := make([]string, 0, len(field.Options))
	for _, option := range field.Options {
		values = append(values, option.Value)
	}
	return values
}

// FormatInputNumber renders a number the one way it is ever written: plain
// decimal, shortest exact form, never scientific notation. The form, the
// validator, and the scaffolded file all agree because they all call this.
func FormatInputNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// inputNumberOnStep reports whether value sits on the declared grid. The
// tolerance absorbs the binary representation of decimal steps (0.1, 0.25)
// without accepting a value the user could not have produced from the form.
func inputNumberOnStep(value, min, step float64) bool {
	if step <= 0 {
		return false
	}
	ratio := (value - min) / step
	return math.Abs(ratio-math.Round(ratio)) <= 1e-9*math.Max(1, math.Abs(ratio))
}

func inputOptionsContain(options []InputOption, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

// BlueprintInputsSharedDataKey is where a workspace records the values it was
// created with. Declared in internal/workspace alongside the other shared-data
// keys and aliased here, so the prompt builders that read it and this package,
// which writes it, cannot drift apart.
const BlueprintInputsSharedDataKey = workspace.BlueprintInputsSharedDataKey

// StoredBlueprintInputs is the recorded shape. Values is the machine-readable
// map; Fields carries the declaration's labels and units so the workspace can
// show what was chosen without re-resolving the blueprint, which may have been
// uninstalled or changed since.
type StoredBlueprintInputs struct {
	SchemaVersion int                         `json:"schema_version"`
	Title         string                      `json:"title,omitempty"`
	Values        map[string]string           `json:"values"`
	Fields        []StoredBlueprintInputValue `json:"fields,omitempty"`
}

// StoredBlueprintInputValue is one recorded value with the display text that
// went with it.
type StoredBlueprintInputValue struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	Value     string         `json:"value"`
	Type      InputFieldType `json:"type"`
	IntakeKey string         `json:"intake_key,omitempty"`
	Unit      string         `json:"unit,omitempty"`
	Display   string         `json:"display"`
}

// BuildStoredBlueprintInputs pairs resolved values with the declaration that
// produced them. Values not named by the declaration are dropped: the record
// describes what the blueprint asked for, not whatever a caller happened to
// carry.
func BuildStoredBlueprintInputs(declaration *InputsDeclaration, values map[string]string) *StoredBlueprintInputs {
	if declaration == nil || len(values) == 0 {
		return nil
	}
	stored := &StoredBlueprintInputs{
		SchemaVersion: declaration.SchemaVersion,
		Title:         declaration.Title,
		Values:        make(map[string]string, len(values)),
	}
	for _, field := range declaration.Fields {
		value, ok := values[field.ID]
		if !ok {
			continue
		}
		stored.Values[field.ID] = value
		stored.Fields = append(stored.Fields, StoredBlueprintInputValue{
			ID: field.ID, Label: field.Label, Value: value, Type: field.Type, IntakeKey: field.IntakeKey, Unit: field.Unit,
			Display: field.displayText(value),
		})
	}
	if len(stored.Values) == 0 {
		return nil
	}
	return stored
}

// displayText is what a person reads: a number with its unit, or the option's
// own label rather than the text that went into the file.
func (field InputField) displayText(value string) string {
	switch field.Type {
	case InputFieldNumber:
		if field.Unit != "" {
			return value + " " + field.Unit
		}
		return value
	case InputFieldSelect:
		for _, option := range field.Options {
			if option.Value == value {
				return option.Label
			}
		}
	}
	return value
}

func validInputText(value string) bool {
	return len(value) <= 200 && !strings.ContainsAny(value, "\r\n") && !strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// ValidateURLInputIntakes ensures a URL answer can only prefill an intake that
// exists in the same immutable template snapshot and explicitly permits links.
func ValidateURLInputIntakes(declaration *InputsDeclaration, requirements []workspace.IntakeRequirement) error {
	if declaration == nil {
		return nil
	}
	for _, field := range declaration.Fields {
		if field.Type != InputFieldURL || field.IntakeKey == "" {
			continue
		}
		found := false
		for _, requirement := range requirements {
			if requirement.Key == field.IntakeKey && requirement.Sources.URL {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: url field %q names intake_key %q, which must declare a URL source", ErrInvalidInputs, field.ID, field.IntakeKey)
		}
	}
	return nil
}

func validInputURL(value string) bool {
	if len(value) > 2000 || strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

// SetBlueprintInputs records the values on a workspace's shared data. An empty
// record removes the key rather than storing an empty object.
func SetBlueprintInputs(sharedData map[string]any, declaration *InputsDeclaration, values map[string]string) {
	if sharedData == nil {
		return
	}
	stored := BuildStoredBlueprintInputs(declaration, values)
	if stored == nil {
		delete(sharedData, BlueprintInputsSharedDataKey)
		return
	}
	sharedData[BlueprintInputsSharedDataKey] = stored
}

// GetBlueprintInputs reads a recorded set back. Shared data round-trips through
// JSON on its way to and from disk, so the stored value arrives either as the
// struct that was written or as the decoded map — both are accepted, and
// anything else is reported rather than guessed at.
func GetBlueprintInputs(sharedData map[string]any) (*StoredBlueprintInputs, error) {
	raw, ok := sharedData[BlueprintInputsSharedDataKey]
	if !ok || raw == nil {
		return nil, nil
	}
	if stored, ok := raw.(*StoredBlueprintInputs); ok {
		return stored, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: stored %s could not be read", ErrInvalidInputs, BlueprintInputsSharedDataKey)
	}
	var stored StoredBlueprintInputs
	if err := json.Unmarshal(encoded, &stored); err != nil || stored.Values == nil {
		return nil, fmt.Errorf("%w: stored %s is not a recorded input set", ErrInvalidInputs, BlueprintInputsSharedDataKey)
	}
	return &stored, nil
}

// CloneInputs deep-copies a declaration so callers that project or adapt a
// template cannot mutate the loaded one.
func CloneInputs(source *InputsDeclaration) *InputsDeclaration {
	if source == nil {
		return nil
	}
	clone := *source
	clone.ApplyTo = append([]string(nil), source.ApplyTo...)
	clone.Fields = make([]InputField, len(source.Fields))
	for index, field := range source.Fields {
		copied := field
		copied.Options = append([]InputOption(nil), field.Options...)
		clone.Fields[index] = copied
	}
	return &clone
}
