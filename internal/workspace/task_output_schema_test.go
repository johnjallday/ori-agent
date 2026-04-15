package workspace

import "testing"

func TestValidateTaskStructuredOutput_RejectsTrailingContent(t *testing.T) {
	schema := &TaskOutputSchema{
		Strict: true,
		Fields: []TaskOutputField{
			{Name: "summary", Type: "string", Required: true},
		},
	}

	if _, err := ValidateTaskStructuredOutput(schema, `{"summary":"ok"} trailing`); err == nil {
		t.Fatalf("expected trailing content to be rejected")
	}
}

func TestValidateTaskStructuredOutput_AcceptsStrictJSONObject(t *testing.T) {
	schema := &TaskOutputSchema{
		Strict: true,
		Fields: []TaskOutputField{
			{Name: "summary", Type: "string", Required: true},
			{Name: "confidence", Type: "number", Required: true},
		},
	}

	parsed, err := ValidateTaskStructuredOutput(schema, `{"summary":"ok","confidence":0.9}`)
	if err != nil {
		t.Fatalf("expected valid structured output, got %v", err)
	}
	if parsed["summary"] != "ok" {
		t.Fatalf("expected parsed summary field, got %v", parsed["summary"])
	}
}
