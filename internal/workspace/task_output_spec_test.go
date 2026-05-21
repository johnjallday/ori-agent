package workspace

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNormalizeTaskOutputSpec_NilReturnsNil(t *testing.T) {
	spec, errs := NormalizeTaskOutputSpec(nil)
	if spec != nil || errs != nil {
		t.Fatalf("expected (nil, nil), got (%v, %v)", spec, errs)
	}
}

func TestNormalizeTaskOutputSpec_RejectsEmptySpec(t *testing.T) {
	spec, errs := NormalizeTaskOutputSpec(&TaskOutputSpec{})
	if spec != nil {
		t.Fatalf("expected nil spec for empty input")
	}
	if len(errs) == 0 {
		t.Fatalf("expected at least one validation error")
	}
}

func TestNormalizeTaskOutputSpec_InfersDefaultMappingsWhenMissing(t *testing.T) {
	spec, errs := NormalizeTaskOutputSpec(&TaskOutputSpec{
		Schema: &TaskOutputSchema{Fields: []TaskOutputField{
			{Name: "pollen_count", Type: "number", Required: true},
			{Name: "forecast_date", Type: "string"},
		}},
		Contract: &TaskOutputContract{Columns: []TaskOutputContractColumn{
			{Name: "pollen_count", Type: "number", Required: true},
			{Name: "forecast_date", Type: "string"},
		}},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if spec == nil {
		t.Fatalf("expected non-nil spec")
	}
	if len(spec.Mappings) != 2 {
		t.Fatalf("expected 2 inferred mappings, got %d", len(spec.Mappings))
	}
	for _, mapping := range spec.Mappings {
		if mapping.Transform != TaskOutputMappingTransformIdentity {
			t.Fatalf("expected identity transform, got %q", mapping.Transform)
		}
	}
}

func TestNormalizeTaskOutputSpec_DefaultsArrayFieldToJSONString(t *testing.T) {
	spec, errs := NormalizeTaskOutputSpec(&TaskOutputSpec{
		Schema: &TaskOutputSchema{Fields: []TaskOutputField{
			{Name: "top_allergens", Type: "array"},
		}},
		Contract: &TaskOutputContract{Columns: []TaskOutputContractColumn{
			{Name: "top_allergens", Type: "string"},
		}},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if spec.Mappings[0].Transform != TaskOutputMappingTransformJSONString {
		t.Fatalf("expected json_string transform for array field, got %q", spec.Mappings[0].Transform)
	}
}

func TestNormalizeTaskOutputSpec_RejectsUnknownTransform(t *testing.T) {
	_, errs := NormalizeTaskOutputSpec(&TaskOutputSpec{
		Schema: &TaskOutputSchema{Fields: []TaskOutputField{{Name: "x", Type: "string"}}},
		Contract: &TaskOutputContract{Columns: []TaskOutputContractColumn{
			{Name: "x", Type: "string", Required: true},
		}},
		Mappings: []TaskOutputMapping{
			{SchemaField: "x", CSVColumn: "x", Transform: "uppercase"},
		},
	})
	if !containsError(errs, "unsupported transform") {
		t.Fatalf("expected unsupported transform error, got %v", errs)
	}
}

func TestNormalizeTaskOutputSpec_RejectsMappingToUnknownColumn(t *testing.T) {
	_, errs := NormalizeTaskOutputSpec(&TaskOutputSpec{
		Schema: &TaskOutputSchema{Fields: []TaskOutputField{{Name: "x", Type: "string"}}},
		Contract: &TaskOutputContract{Columns: []TaskOutputContractColumn{
			{Name: "x", Type: "string"},
		}},
		Mappings: []TaskOutputMapping{
			{SchemaField: "x", CSVColumn: "missing"},
		},
	})
	if !containsError(errs, `unknown contract column "missing"`) {
		t.Fatalf("expected unknown column error, got %v", errs)
	}
}

func TestNormalizeTaskOutputSpec_RejectsRequiredColumnWithoutMapping(t *testing.T) {
	_, errs := NormalizeTaskOutputSpec(&TaskOutputSpec{
		Schema: &TaskOutputSchema{Fields: []TaskOutputField{{Name: "x", Type: "string"}}},
		Contract: &TaskOutputContract{Columns: []TaskOutputContractColumn{
			{Name: "x", Type: "string"},
			{Name: "required_no_map", Type: "string", Required: true},
		}},
		Mappings: []TaskOutputMapping{
			{SchemaField: "x", CSVColumn: "x"},
		},
	})
	if !containsError(errs, "required contract column") {
		t.Fatalf("expected required-column error, got %v", errs)
	}
}

func TestNormalizeTaskOutputSpec_AppliesDefaultMetadataPolicy(t *testing.T) {
	spec, _ := NormalizeTaskOutputSpec(&TaskOutputSpec{
		Schema: &TaskOutputSchema{Fields: []TaskOutputField{{Name: "x", Type: "string"}}},
		Contract: &TaskOutputContract{Columns: []TaskOutputContractColumn{
			{Name: "x", Type: "string"},
		}},
	})
	if spec.MetadataPolicy == nil {
		t.Fatalf("expected default metadata policy")
	}
	if len(spec.MetadataPolicy.Fields) != len(DefaultTaskOutputMetadataFieldNames) {
		t.Fatalf("expected %d default metadata fields, got %d",
			len(DefaultTaskOutputMetadataFieldNames), len(spec.MetadataPolicy.Fields))
	}
	for _, field := range spec.MetadataPolicy.Fields {
		if !field.Include {
			t.Fatalf("default metadata field %q should be included", field.Name)
		}
	}
}

func TestNormalizeTaskOutputSpec_PreservesUserMetadataChoices(t *testing.T) {
	spec, _ := NormalizeTaskOutputSpec(&TaskOutputSpec{
		Schema: &TaskOutputSchema{Fields: []TaskOutputField{{Name: "x", Type: "string"}}},
		Contract: &TaskOutputContract{Columns: []TaskOutputContractColumn{
			{Name: "x", Type: "string"},
		}},
		MetadataPolicy: &TaskOutputMetadataPolicy{Fields: []TaskOutputMetadataField{
			{Name: "run_id", Include: false},
			{Name: "executed_at", Include: true},
		}},
	})
	found := map[string]bool{}
	for _, field := range spec.MetadataPolicy.Fields {
		found[field.Name] = field.Include
	}
	if found["run_id"] {
		t.Fatalf("run_id should remain hidden")
	}
	if !found["status"] {
		t.Fatalf("status default should be included")
	}
}

func TestAssignTaskOutputSpecVersion_DeterministicAndChangesOnContent(t *testing.T) {
	base := &TaskOutputSpec{
		Source: "ai_suggested",
		Schema: &TaskOutputSchema{Fields: []TaskOutputField{{Name: "x", Type: "string"}}},
		Contract: &TaskOutputContract{Columns: []TaskOutputContractColumn{
			{Name: "x", Type: "string"},
		}},
		Mappings: []TaskOutputMapping{{SchemaField: "x", CSVColumn: "x", Transform: "identity"}},
	}
	a := AssignTaskOutputSpecVersion(cloneSpecForTest(base)).Version
	b := AssignTaskOutputSpecVersion(cloneSpecForTest(base)).Version
	if a == "" || a != b {
		t.Fatalf("expected deterministic non-empty version, got a=%q b=%q", a, b)
	}

	mutated := cloneSpecForTest(base)
	mutated.Contract.Columns = append(mutated.Contract.Columns, TaskOutputContractColumn{Name: "y", Type: "string"})
	c := AssignTaskOutputSpecVersion(mutated).Version
	if c == a {
		t.Fatalf("expected version to change after content change, got %q", c)
	}
	if !strings.HasPrefix(c, "ocv_") {
		t.Fatalf("expected ocv_ prefix, got %q", c)
	}
}

func TestSnapshotTaskOutputSpec_IsolatesFromSource(t *testing.T) {
	base := &TaskOutputSpec{
		Version: "ocv_abc",
		Schema:  &TaskOutputSchema{Fields: []TaskOutputField{{Name: "x", Type: "string"}}},
		Contract: &TaskOutputContract{Columns: []TaskOutputContractColumn{
			{Name: "x", Type: "string"},
		}},
		Approval: &TaskOutputApproval{ApprovedAt: time.Now(), ApprovedBy: "user@example.com"},
	}
	snap := SnapshotTaskOutputSpec(base)
	if snap == nil {
		t.Fatalf("expected snapshot, got nil")
	}
	base.Version = "ocv_changed"
	base.Schema.Fields[0].Name = "renamed"
	base.Approval.ApprovedBy = "other@example.com"

	if snap.Version != "ocv_abc" {
		t.Fatalf("snapshot version mutated: %q", snap.Version)
	}
	if snap.Schema.Fields[0].Name != "x" {
		t.Fatalf("snapshot schema mutated: %q", snap.Schema.Fields[0].Name)
	}
	if snap.Approval.ApprovedBy != "user@example.com" {
		t.Fatalf("snapshot approval mutated: %q", snap.Approval.ApprovedBy)
	}
}

func TestActiveTaskOutputSpec_PrefersOutputSpec(t *testing.T) {
	task := &Task{
		OutputSpec: &TaskOutputSpec{Version: "ocv_new"},
		OutputSchema: &TaskOutputSchema{Fields: []TaskOutputField{
			{Name: "legacy", Type: "string"},
		}},
	}
	spec := ActiveTaskOutputSpec(task)
	if spec == nil || spec.Version != "ocv_new" {
		t.Fatalf("expected OutputSpec to win, got %v", spec)
	}
}

func TestActiveTaskOutputSpec_FallsBackToLegacyFields(t *testing.T) {
	task := &Task{
		OutputContract: &TaskOutputContract{Columns: []TaskOutputContractColumn{
			{Name: "x", Type: "string", Required: true},
		}},
	}
	spec := ActiveTaskOutputSpec(task)
	if spec == nil {
		t.Fatalf("expected synthesized spec from legacy contract")
	}
	if spec.Source != "legacy" {
		t.Fatalf("expected legacy source marker, got %q", spec.Source)
	}
	if spec.Version == "" {
		t.Fatalf("expected synthesized version to be set")
	}
}

func TestActiveTaskOutputSpec_ReturnsNilForBareTask(t *testing.T) {
	if ActiveTaskOutputSpec(&Task{}) != nil {
		t.Fatalf("expected nil for task without any output config")
	}
	if ActiveTaskOutputSpec(nil) != nil {
		t.Fatalf("expected nil for nil task")
	}
}

func cloneSpecForTest(spec *TaskOutputSpec) *TaskOutputSpec {
	cloned, _ := NormalizeTaskOutputSpec(spec)
	return cloned
}

func containsError(errs []string, substr string) bool {
	return slices.ContainsFunc(errs, func(s string) bool {
		return strings.Contains(s, substr)
	})
}
