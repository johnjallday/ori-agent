package llm

import (
	"strings"
	"testing"
)

func TestBrokeredToolProtocolAndResponse(t *testing.T) {
	tools := []Tool{{
		Name: "plugin_reaper_tidy_survey", Description: "Run the survey.",
		Parameters: map[string]any{"type": "object", "additionalProperties": false},
	}}
	prompt, err := appendBrokeredToolProtocol("User:\nSurvey", tools, "Example CLI")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"User:\nSurvey", "not Example CLI's own MCP servers", "plugin_reaper_tidy_survey", "arguments_json"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("tool prompt missing %q: %s", want, prompt)
		}
	}

	toolResponse, err := decodeBrokeredToolResponse(
		"example",
		`{"kind":"tool_call","content":"","tool_name":"plugin_reaper_tidy_survey","arguments_json":"{}"}`,
		tools,
		&ChatResponse{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if toolResponse.FinishReason != FinishReasonToolCalls || len(toolResponse.ToolCalls) != 1 ||
		toolResponse.ToolCalls[0].Name != tools[0].Name || toolResponse.ToolCalls[0].Arguments != "{}" ||
		!strings.HasPrefix(toolResponse.ToolCalls[0].ID, "example_tool_") {
		t.Fatalf("tool response = %+v", toolResponse)
	}

	again, err := decodeBrokeredToolResponse(
		"example",
		`{"kind":"tool_call","content":"","tool_name":"plugin_reaper_tidy_survey","arguments_json":"{}"}`,
		tools,
		&ChatResponse{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if again.ToolCalls[0].ID == toolResponse.ToolCalls[0].ID {
		t.Fatalf("expected a fresh call ID per tool call, got %q twice", again.ToolCalls[0].ID)
	}

	finalResponse, err := decodeBrokeredToolResponse(
		"example",
		`{"kind":"final","content":"Survey complete.","tool_name":"","arguments_json":"{}"}`,
		tools,
		&ChatResponse{},
	)
	if err != nil || finalResponse.Content != "Survey complete." || len(finalResponse.ToolCalls) != 0 {
		t.Fatalf("final response = %+v, %v", finalResponse, err)
	}
}

func TestBrokeredToolResponseRejectsUntrustedShape(t *testing.T) {
	tools := []Tool{{Name: "allowed", Parameters: map[string]any{"type": "object"}}}
	cases := map[string]string{
		"unknown tool":           `{"kind":"tool_call","content":"","tool_name":"other","arguments_json":"{}"}`,
		"arguments not object":   `{"kind":"tool_call","content":"","tool_name":"allowed","arguments_json":"[]"}`,
		"final smuggles a tool":  `{"kind":"final","content":"done","tool_name":"allowed","arguments_json":"{}"}`,
		"trailing data":          `{"kind":"final","content":"done","tool_name":"","arguments_json":"{}"} {}`,
		"unknown field":          `{"kind":"final","content":"done","tool_name":"","arguments_json":"{}","extra":1}`,
		"unknown kind":           `{"kind":"tool_calls","content":"","tool_name":"allowed","arguments_json":"{}"}`,
		"plain text, no payload": `Sure, I will remember that.`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decodeBrokeredToolResponse("example", input, tools, &ChatResponse{})
			if err == nil {
				t.Fatalf("expected refusal for %s", input)
			}
			if !strings.HasPrefix(err.Error(), "example ") {
				t.Fatalf("expected the provider name to lead the error, got %v", err)
			}
		})
	}
}

func TestBrokeredToolResponseSchemaRequiresTheFourFields(t *testing.T) {
	schema := brokeredToolResponseSchema()
	required, _ := schema["required"].([]string)
	if strings.Join(required, ",") != "kind,content,tool_name,arguments_json" {
		t.Fatalf("required = %v", required)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("schema must refuse extra fields: %+v", schema)
	}
}
