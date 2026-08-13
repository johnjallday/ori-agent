package workspaceplan

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPlanContentSchemaIsSelfConsistent(t *testing.T) {
	schema := PlanContentSchema()

	// The schema must be serializable, since provider adapters send it as JSON.
	if _, err := json.Marshal(schema); err != nil {
		t.Fatalf("schema is not serializable: %v", err)
	}

	// Objective and groups are the two things a plan cannot exist without.
	required, _ := schema["required"].([]any)
	if len(required) != 2 {
		t.Fatalf("required = %v, want objective and groups", required)
	}

	// Callers mutate what they are handed, so each call must return a fresh
	// map or one provider's edits would leak into another's request.
	first := PlanContentSchema()
	first["strict"] = true
	if _, leaked := PlanContentSchema()["strict"]; leaked {
		t.Error("PlanContentSchema returned a shared map; a caller's mutation leaked")
	}

	// Actionable structure is typed; prose is confined to `explanation`.
	properties, _ := schema["properties"].(map[string]any)
	for _, field := range []string{"objective", "groups", "execution", "validations", "explanation"} {
		if _, ok := properties[field]; !ok {
			t.Errorf("schema is missing %q", field)
		}
	}
	explanation, _ := properties["explanation"].(map[string]any)
	if desc, _ := explanation["description"].(string); !strings.Contains(desc, "no dependency") {
		t.Errorf("explanation description does not disclaim actionable meaning: %q", desc)
	}
}

func TestClarificationSchemaRequiresQuestions(t *testing.T) {
	schema := ClarificationSchema()
	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "questions" {
		t.Errorf("required = %v, want [questions]", required)
	}
}

const wellFormedResponse = `{
  "objective": "Migrate the reporting database with no billing impact",
  "rationale": "Staged cutover keeps rollback cheap",
  "in_scope": ["reporting tables"],
  "non_goals": ["billing tables"],
  "assumptions": [{"statement": "Staging mirrors production"}],
  "risks": [{"statement": "Row drift during cutover", "severity": "medium", "mitigation": "Checksum both sides"}],
  "sources": [{"kind": "file", "ref": "docs/schema.md", "title": "Schema notes"}],
  "artifacts": [{"kind": "prd", "path": "tasks/prd-migration.md", "title": "Migration PRD"}],
  "groups": [
    {
      "id": "grp-keep",
      "title": "Prepare",
      "outcome": "A verified staging copy exists",
      "items": [
        {"id": "itm-keep", "description": "Snapshot staging", "expected_result": "Checksums match"},
        {"description": "Dry-run the cutover", "depends_on": ["itm-keep"], "assignee": "builder"}
      ]
    }
  ],
  "validations": [{"title": "Row counts match", "required": true}],
  "execution": {"mode": "step_through", "preconditions": ["repo_scan"]},
  "explanation": "Prose for the reader."
}`

func TestDecodePlanContentProducesTypedContent(t *testing.T) {
	objective, content, err := DecodePlanContent([]byte(wellFormedResponse))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if objective != "Migrate the reporting database with no billing impact" {
		t.Errorf("objective = %q", objective)
	}
	if len(content.Groups) != 1 || len(content.Groups[0].Items) != 2 {
		t.Fatalf("groups did not decode: %+v", content.Groups)
	}
	if content.Execution.Mode != ExecutionStepThrough {
		t.Errorf("execution mode = %q", content.Execution.Mode)
	}
	if content.Explanation != "Prose for the reader." {
		t.Errorf("explanation = %q", content.Explanation)
	}
	if len(content.Sources) != 1 || content.Sources[0].Kind != SourceFile {
		t.Errorf("sources did not decode: %+v", content.Sources)
	}

	// Whether an artifact is actually written is a policy decision, never
	// something the model switches on (FR-95).
	if len(content.Artifacts) != 1 || content.Artifacts[0].Enabled {
		t.Errorf("model output enabled an artifact write: %+v", content.Artifacts)
	}

	// Decoded content passes validation, so the schema and the validator agree
	// about what a well-formed plan is.
	if result := ValidatePlanContent(objective, content, ValidationContext{}); !result.OK() {
		t.Errorf("schema-conformant output failed validation: %v", codes(result))
	}
}

// Reused IDs survive decoding, which is what lets a targeted revision keep
// untouched sections identified across regenerations (FR-52, FR-55).
func TestDecodePreservesSuppliedIDsAndMintsMissingOnes(t *testing.T) {
	_, content, err := DecodePlanContent([]byte(wellFormedResponse))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if content.Groups[0].ID != "grp-keep" {
		t.Errorf("group id = %q, want the id the model reused", content.Groups[0].ID)
	}
	if content.Groups[0].Items[0].ID != "itm-keep" {
		t.Errorf("item id = %q, want the id the model reused", content.Groups[0].Items[0].ID)
	}

	minted := content.Groups[0].Items[1].ID
	if minted == "" {
		t.Fatal("an item without an id did not receive one")
	}
	if !strings.HasPrefix(minted, "itm_") {
		t.Errorf("minted item id = %q, want an item-prefixed id", minted)
	}
	// The dependency on the reused id still resolves.
	if got := content.Groups[0].Items[1].DependsOn; len(got) != 1 || got[0] != "itm-keep" {
		t.Errorf("depends_on = %v, want [itm-keep]", got)
	}
}

// Version provenance has to be able to show which parts a model wrote (FR-57).
func TestDecodeMarksEveryGeneratedElementAsModelAuthored(t *testing.T) {
	_, content, err := DecodePlanContent([]byte(wellFormedResponse))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if content.Groups[0].Author != AuthorModel {
		t.Errorf("group author = %q, want model", content.Groups[0].Author)
	}
	for _, item := range content.Groups[0].Items {
		if item.Author != AuthorModel {
			t.Errorf("item %q author = %q, want model", item.Description, item.Author)
		}
	}
	if content.Assumptions[0].Author != AuthorModel || content.Risks[0].Author != AuthorModel {
		t.Error("assumptions and risks were not marked model-authored")
	}
	if content.Validations[0].Author != AuthorModel {
		t.Error("validation checkpoint was not marked model-authored")
	}
}

// A response carrying a field the schema never declared is not the contract we
// asked for, so it is refused rather than partially accepted.
func TestDecodeRejectsUndeclaredFields(t *testing.T) {
	response := `{"objective":"x","groups":[],"auto_approve":true}`
	_, _, err := DecodePlanContent([]byte(response))
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("undeclared field error = %v, want ErrValidation", err)
	}
	if !strings.Contains(err.Error(), "auto_approve") {
		t.Errorf("error does not name the offending field: %v", err)
	}
}

func TestDecodeRejectsMalformedAndEmptyResponses(t *testing.T) {
	for _, response := range []string{"", "   ", "not json", `{"objective":`} {
		if _, _, err := DecodePlanContent([]byte(response)); !errors.Is(err, ErrValidation) {
			t.Errorf("response %q error = %v, want ErrValidation", response, err)
		}
	}
}

// An unrecognized execution mode resolves to the mode that starts nothing, and
// never to the one that starts work.
func TestDecodeNeverEscalatesAnUnknownExecutionMode(t *testing.T) {
	response := `{"objective":"x","groups":[{"title":"g","items":[{"description":"d"}]}],"execution":{"mode":"yolo"}}`
	_, content, err := DecodePlanContent([]byte(response))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if content.Execution.Mode != ExecutionStepThrough {
		t.Errorf("execution mode = %q, want step_through for an unknown value", content.Execution.Mode)
	}
}

// The decoder does shape only; graph and bounds problems are validation's job,
// and nothing may skip it (FR-41).
func TestDecodeLeavesGraphProblemsToValidation(t *testing.T) {
	response := `{
	  "objective": "x",
	  "groups": [{"id":"g1","title":"g","items":[{"id":"i1","description":"d","depends_on":["i-missing"]}]}],
	  "execution": {"mode":"step_through"}
	}`
	objective, content, err := DecodePlanContent([]byte(response))
	if err != nil {
		t.Fatalf("decode rejected a shape-valid response: %v", err)
	}
	result := ValidatePlanContent(objective, content, ValidationContext{})
	if !hasCode(result, IssueDanglingDependency) {
		t.Errorf("validation did not catch the dangling dependency: %v", codes(result))
	}
}
