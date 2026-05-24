package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// supportedTaskOutputMappingTransforms lists the transforms accepted at spec
// normalization time. Unknown transforms are rejected.
var supportedTaskOutputMappingTransforms = []string{
	TaskOutputMappingTransformIdentity,
	TaskOutputMappingTransformJSONString,
}

// supportedTaskOutputSpecSources lists the accepted spec provenance markers.
var supportedTaskOutputSpecSources = []string{"ai_suggested", "manual", "csv_header", "legacy"}

func normalizeTaskOutputSpecSource(value string) string {
	candidate := strings.ToLower(strings.TrimSpace(value))
	if slices.Contains(supportedTaskOutputSpecSources, candidate) {
		return candidate
	}
	return ""
}

// NormalizeTaskOutputSpec sanitizes a structured output spec, returning a
// canonical copy plus the list of validation errors discovered during
// normalization. A nil spec returns (nil, nil). A spec with no usable schema
// fields or contract columns returns (nil, errors) so callers can surface why
// the spec was rejected.
//
// The returned spec preserves any Version and Approval set by the caller; use
// AssignTaskOutputSpecVersion to (re)derive the version when promoting a draft
// to active.
func NormalizeTaskOutputSpec(spec *TaskOutputSpec) (*TaskOutputSpec, []string) {
	if spec == nil {
		return nil, nil
	}

	errs := []string{}
	normalized := &TaskOutputSpec{
		Version:  strings.TrimSpace(spec.Version),
		Source:   normalizeTaskOutputSpecSource(spec.Source),
		Schema:   NormalizeTaskOutputSchema(spec.Schema),
		Contract: NormalizeTaskOutputContract(spec.Contract),
		Approval: cloneTaskOutputApproval(spec.Approval),
	}

	schemaFields := map[string]TaskOutputField{}
	if normalized.Schema != nil {
		for _, field := range normalized.Schema.Fields {
			schemaFields[strings.ToLower(field.Name)] = field
		}
	}
	contractColumns := map[string]TaskOutputContractColumn{}
	if normalized.Contract != nil {
		for _, column := range normalized.Contract.Columns {
			contractColumns[strings.ToLower(column.Name)] = column
		}
	}

	if len(schemaFields) == 0 && len(contractColumns) == 0 {
		errs = append(errs, "spec must include at least one schema field or contract column")
		return nil, errs
	}

	normalized.Mappings, errs = normalizeTaskOutputMappings(spec.Mappings, schemaFields, contractColumns, errs)
	errs = validateRequiredColumnCoverage(normalized.Mappings, contractColumns, errs)
	normalized.MetadataPolicy = normalizeTaskOutputMetadataPolicy(spec.MetadataPolicy)

	return normalized, errs
}

func normalizeTaskOutputMappings(
	mappings []TaskOutputMapping,
	schemaFields map[string]TaskOutputField,
	contractColumns map[string]TaskOutputContractColumn,
	errs []string,
) ([]TaskOutputMapping, []string) {
	if len(mappings) == 0 && len(schemaFields) > 0 && len(contractColumns) > 0 {
		mappings = inferDefaultTaskOutputMappings(schemaFields, contractColumns)
	}

	result := make([]TaskOutputMapping, 0, len(mappings))
	seenColumn := map[string]struct{}{}
	for _, mapping := range mappings {
		schemaField := strings.TrimSpace(mapping.SchemaField)
		csvColumn := strings.TrimSpace(mapping.CSVColumn)
		if schemaField == "" || csvColumn == "" {
			errs = append(errs, "mapping must include both schema_field and csv_column")
			continue
		}

		if len(schemaFields) > 0 {
			if _, ok := schemaFields[strings.ToLower(schemaField)]; !ok {
				errs = append(errs, fmt.Sprintf("mapping references unknown schema field %q", schemaField))
				continue
			}
		}
		if len(contractColumns) > 0 {
			if _, ok := contractColumns[strings.ToLower(csvColumn)]; !ok {
				errs = append(errs, fmt.Sprintf("mapping references unknown contract column %q", csvColumn))
				continue
			}
		}

		columnKey := strings.ToLower(csvColumn)
		if _, dup := seenColumn[columnKey]; dup {
			errs = append(errs, fmt.Sprintf("contract column %q is mapped more than once", csvColumn))
			continue
		}
		seenColumn[columnKey] = struct{}{}

		transform := normalizeTaskOutputMappingTransform(mapping.Transform, schemaFields[strings.ToLower(schemaField)])
		if !slices.Contains(supportedTaskOutputMappingTransforms, transform) {
			errs = append(errs, fmt.Sprintf("mapping for column %q uses unsupported transform %q", csvColumn, transform))
			continue
		}

		result = append(result, TaskOutputMapping{
			SchemaField:  schemaField,
			CSVColumn:    csvColumn,
			Transform:    transform,
			DefaultValue: strings.TrimSpace(mapping.DefaultValue),
		})
	}
	return result, errs
}

func inferDefaultTaskOutputMappings(
	schemaFields map[string]TaskOutputField,
	contractColumns map[string]TaskOutputContractColumn,
) []TaskOutputMapping {
	mappings := make([]TaskOutputMapping, 0, len(contractColumns))
	for key, column := range contractColumns {
		if field, ok := schemaFields[key]; ok {
			mappings = append(mappings, TaskOutputMapping{
				SchemaField: field.Name,
				CSVColumn:   column.Name,
				Transform:   normalizeTaskOutputMappingTransform("", field),
			})
		}
	}
	return mappings
}

func normalizeTaskOutputMappingTransform(value string, field TaskOutputField) string {
	transform := strings.ToLower(strings.TrimSpace(value))
	if transform == "" {
		if field.Type == "array" {
			return TaskOutputMappingTransformJSONString
		}
		return TaskOutputMappingTransformIdentity
	}
	return transform
}

func validateRequiredColumnCoverage(
	mappings []TaskOutputMapping,
	contractColumns map[string]TaskOutputContractColumn,
	errs []string,
) []string {
	mapped := map[string]struct{}{}
	for _, mapping := range mappings {
		mapped[strings.ToLower(mapping.CSVColumn)] = struct{}{}
	}
	for key, column := range contractColumns {
		if !column.Required {
			continue
		}
		if _, ok := mapped[key]; !ok {
			errs = append(errs, fmt.Sprintf("required contract column %q has no mapping", column.Name))
		}
	}
	return errs
}

func normalizeTaskOutputMetadataPolicy(policy *TaskOutputMetadataPolicy) *TaskOutputMetadataPolicy {
	if policy == nil {
		return defaultTaskOutputMetadataPolicy()
	}
	seen := map[string]struct{}{}
	fields := make([]TaskOutputMetadataField, 0, len(policy.Fields))
	for _, field := range policy.Fields {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		fields = append(fields, TaskOutputMetadataField{Name: name, Include: field.Include})
	}
	for _, name := range DefaultTaskOutputMetadataFieldNames {
		if _, dup := seen[strings.ToLower(name)]; dup {
			continue
		}
		fields = append(fields, TaskOutputMetadataField{Name: name, Include: true})
	}
	return &TaskOutputMetadataPolicy{Fields: fields}
}

func defaultTaskOutputMetadataPolicy() *TaskOutputMetadataPolicy {
	fields := make([]TaskOutputMetadataField, 0, len(DefaultTaskOutputMetadataFieldNames))
	for _, name := range DefaultTaskOutputMetadataFieldNames {
		fields = append(fields, TaskOutputMetadataField{Name: name, Include: true})
	}
	return &TaskOutputMetadataPolicy{Fields: fields}
}

func cloneTaskOutputApproval(approval *TaskOutputApproval) *TaskOutputApproval {
	if approval == nil {
		return nil
	}
	clone := *approval
	return &clone
}

// AssignTaskOutputSpecVersion derives and writes a deterministic version onto
// the spec (mutates and returns it). Use this on approval, after normalization.
func AssignTaskOutputSpecVersion(spec *TaskOutputSpec) *TaskOutputSpec {
	if spec == nil {
		return nil
	}
	spec.Version = computeTaskOutputSpecVersion(spec)
	return spec
}

func computeTaskOutputSpecVersion(spec *TaskOutputSpec) string {
	type versionPayload struct {
		Source         string                    `json:"source,omitempty"`
		Schema         *TaskOutputSchema         `json:"schema,omitempty"`
		Contract       *TaskOutputContract       `json:"contract,omitempty"`
		Mappings       []TaskOutputMapping       `json:"mappings,omitempty"`
		MetadataPolicy *TaskOutputMetadataPolicy `json:"metadata_policy,omitempty"`
	}
	payload := versionPayload{
		Source:         spec.Source,
		Schema:         spec.Schema,
		Contract:       spec.Contract,
		Mappings:       spec.Mappings,
		MetadataPolicy: spec.MetadataPolicy,
	}
	if payload.Contract != nil {
		payload.Contract = &TaskOutputContract{Source: payload.Contract.Source, Columns: payload.Contract.Columns}
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return "ocv_" + hex.EncodeToString(sum[:])[:12]
}

// SnapshotTaskOutputSpec returns a deep clone safe to associate with an
// in-flight run. Subsequent edits to the source spec do not affect the snapshot.
func SnapshotTaskOutputSpec(spec *TaskOutputSpec) *TaskOutputSpec {
	if spec == nil {
		return nil
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return nil
	}
	clone := &TaskOutputSpec{}
	if err := json.Unmarshal(data, clone); err != nil {
		return nil
	}
	return clone
}

// ActiveTaskOutputSpec returns the active spec for a task, synthesizing one
// from legacy OutputSchema/OutputContract fields when OutputSpec is nil. The
// returned spec is non-nil only if there is something usable; callers should
// nil-check.
func ActiveTaskOutputSpec(task *Task) *TaskOutputSpec {
	if task == nil {
		return nil
	}
	if task.OutputSpec != nil {
		return task.OutputSpec
	}
	return synthesizeLegacyTaskOutputSpec(task.OutputSchema, task.OutputContract)
}

// MigrateLegacyTaskOutputFields produces an active spec from legacy fields
// without mutating the task. Returns nil if neither legacy field is set.
func MigrateLegacyTaskOutputFields(task *Task) *TaskOutputSpec {
	if task == nil {
		return nil
	}
	return synthesizeLegacyTaskOutputSpec(task.OutputSchema, task.OutputContract)
}

func synthesizeLegacyTaskOutputSpec(schema *TaskOutputSchema, contract *TaskOutputContract) *TaskOutputSpec {
	normalizedSchema := NormalizeTaskOutputSchema(schema)
	normalizedContract := NormalizeTaskOutputContract(contract)
	if normalizedSchema == nil && normalizedContract == nil {
		return nil
	}
	spec := &TaskOutputSpec{
		Source:   "legacy",
		Schema:   normalizedSchema,
		Contract: normalizedContract,
	}
	if normalizedContract != nil {
		spec.Version = normalizedContract.Version
	}
	cleaned, _ := NormalizeTaskOutputSpec(spec)
	if cleaned == nil {
		return nil
	}
	if cleaned.Version == "" {
		cleaned.Version = computeTaskOutputSpecVersion(cleaned)
	}
	return cleaned
}

// BuildTaskOutputSpecPrompt renders the approved structured output spec as one
// coherent final-answer instruction. The schema defines the JSON shape; the
// contract/mappings explain the durable CSV projection the harness will use.
func BuildTaskOutputSpecPrompt(spec *TaskOutputSpec) string {
	normalized, errs := NormalizeTaskOutputSpec(spec)
	if normalized == nil || len(errs) > 0 {
		return ""
	}
	var prompt strings.Builder
	prompt.WriteString("Return ONLY a valid JSON object for the approved structured output spec. Do not wrap it in markdown fences.\n")
	if normalized.Version != "" {
		fmt.Fprintf(&prompt, "Contract version: %s\n", normalized.Version)
	}
	if normalized.Schema != nil {
		if normalized.Schema.Name != "" {
			fmt.Fprintf(&prompt, "Schema: `%s`\n", normalized.Schema.Name)
		}
		if normalized.Schema.Description != "" {
			prompt.WriteString(normalized.Schema.Description)
			prompt.WriteString("\n")
		}
		prompt.WriteString("Schema fields:\n")
		for _, field := range normalized.Schema.Fields {
			requiredLabel := "optional"
			if field.Required {
				requiredLabel = "required"
			}
			fmt.Fprintf(&prompt, "- %s (%s, %s)", field.Name, field.Type, requiredLabel)
			if field.Description != "" {
				prompt.WriteString(": ")
				prompt.WriteString(field.Description)
			}
			prompt.WriteString("\n")
		}
		if normalized.Schema.Strict {
			prompt.WriteString("Do not include keys outside this schema.\n")
		}
	}
	if normalized.Contract != nil {
		prompt.WriteString("CSV storage projection:\n")
		for _, column := range normalized.Contract.Columns {
			requiredLabel := "optional"
			if column.Required {
				requiredLabel = "required"
			}
			fmt.Fprintf(&prompt, "- %s (%s, %s)", column.Name, column.Type, requiredLabel)
			if column.Description != "" {
				prompt.WriteString(": ")
				prompt.WriteString(column.Description)
			}
			prompt.WriteString("\n")
		}
	}
	if len(normalized.Mappings) > 0 {
		prompt.WriteString("Field-to-column mappings:\n")
		for _, mapping := range normalized.Mappings {
			transform := mapping.Transform
			if transform == "" {
				transform = TaskOutputMappingTransformIdentity
			}
			fmt.Fprintf(&prompt, "- %s -> %s (%s)\n", mapping.SchemaField, mapping.CSVColumn, transform)
		}
	}
	prompt.WriteString("Use ISO dates like 2026-05-20 for date strings. The system will add run metadata such as run_id, executed_at, status, and duration_ms.")
	return strings.TrimSpace(prompt.String())
}
