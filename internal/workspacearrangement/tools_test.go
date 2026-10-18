package workspacearrangement

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestToolSession_RejectsEntireInvalidBatchBeforeIssuingEvidence(t *testing.T) {
	for _, tail := range []Invocation{
		{"bad", "shell", `{}`},
		{"bad", "arrangement_search", `{"filter":{"sql":"select *"}}`},
		{"bad", "arrangement_search", `{"filter":null}`},
		{"bad", "arrangement_search", `{"page_size":21}`},
		{"bad", "arrangement_search", `{"cursor":"forged"}`},
		{"bad", "arrangement_detail", `{"id":"item-0001"}`},
		{"bad", "arrangement_overview", `{"HomeID":"foreign"}`},
		{"bad", "arrangement_search", `{"filter":{},"filter":{}}`},
		{"bad", "arrangement_propose", `{}`},
		{"first", "arrangement_overview", `{}`},
	} {
		t.Run(tail.Name+tail.Arguments, func(t *testing.T) {
			e, _ := evidenceFixture(t, 20)
			s := NewToolSession(e)
			results, draft, err := s.Execute(t.Context(), []Invocation{{"first", "arrangement_overview", `{}`}, tail})
			if err == nil || results != nil || draft != nil || len(e.issued) != 0 || len(e.selections) != 0 {
				t.Fatalf("partial batch ran: results=%v draft=%v error=%v issued=%v", results, draft, err, e.issued)
			}
		})
	}
}

func TestToolSession_FrozenProposalIsTerminalInertAndCannotReplay(t *testing.T) {
	e, _ := evidenceFixture(t, 1000)
	s := NewToolSession(e)
	results, draft, err := s.Execute(t.Context(), []Invocation{{"select", "arrangement_select", `{"filter":{"root_id":"root-1"}}`}})
	if err != nil || draft != nil || len(results) != 1 || results[0].Name != "arrangement_select" {
		t.Fatalf("select: %v %v", results, err)
	}
	var selected FrozenSelection
	if err := json.Unmarshal([]byte(results[0].Content), &selected); err != nil {
		t.Fatal(err)
	}
	intent := Draft{Version: Version, SourceRevision: selected.Revision, Goal: "Keep client references catalog-only", Operations: []Operation{{Kind: CreateCollection, Name: "Client", Members: Selection{Handle: selected.Handle}}}}
	raw, _ := json.Marshal(intent)
	results, draft, err = s.Execute(t.Context(), []Invocation{{"propose", "arrangement_propose", string(raw)}})
	if err != nil || draft == nil || len(draft.Operations[0].Members.IDs) != 250 || !strings.Contains(results[0].Content, "validated_not_saved") {
		t.Fatalf("propose: %v %v %v", results, draft, err)
	}
	if strings.Contains(results[0].Content, "item-") {
		t.Fatal("expanded durable membership was sent back to the model")
	}
	if _, _, err := s.Execute(t.Context(), []Invocation{{"again", "arrangement_propose", string(raw)}}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("terminal proposal replayed")
	}
}

func TestToolSession_RejectsAuthorityAndUnknownProposalFields(t *testing.T) {
	e, _ := evidenceFixture(t, 10)
	s := NewToolSession(e)
	o, err := e.Overview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"approved":true`, `"path":"/private"`, `"provider":"other"`, `"Goal":"duplicate alias"`} {
		raw := fmt.Sprintf(`{"version":1,"source_revision":%q,"goal":"No changes",%s}`, o.Revision, field)
		if _, _, err := s.Execute(t.Context(), []Invocation{{field[:6], "arrangement_propose", raw}}); err == nil {
			t.Fatal("authority in model proposal accepted")
		}
	}
	valid := fmt.Sprintf(`{"version":1,"source_revision":%q,"goal":"No changes"}`, o.Revision)
	_, draft, err := s.Execute(t.Context(), []Invocation{{"valid", "arrangement_propose", valid}})
	if err != nil || draft == nil || len(draft.Operations) != 0 {
		t.Fatalf("valid no-change refused: %v", err)
	}
}

func TestToolSession_CallEvidenceAndModelPageBudgets(t *testing.T) {
	t.Run("calls", func(t *testing.T) {
		e, _ := evidenceFixture(t, 1)
		s := NewToolSession(e)
		for i := 0; i < MaxToolCalls; i++ {
			if _, _, err := s.Execute(t.Context(), []Invocation{{fmt.Sprintf("call-%d", i), "arrangement_overview", `{}`}}); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := s.Execute(t.Context(), []Invocation{{"extra", "arrangement_overview", `{}`}}); !errors.Is(err, ErrLimit) {
			t.Fatal("call budget not enforced")
		}
	})
	t.Run("bytes", func(t *testing.T) {
		e, _ := evidenceFixture(t, 1000)
		s := NewToolSession(e)
		limited := false
		for i := 0; i < MaxToolCalls; i++ {
			_, _, err := s.Execute(t.Context(), []Invocation{{fmt.Sprintf("call-%d", i), "arrangement_search", `{"page_size":20}`}})
			if errors.Is(err, ErrLimit) {
				limited = true
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if !limited || s.bytes > MaxTurnEvidenceBytes {
			t.Fatalf("sharing budget not enforced: %d", s.bytes)
		}
	})
	t.Run("reused call ID", func(t *testing.T) {
		e, _ := evidenceFixture(t, 1)
		s := NewToolSession(e)
		calls := []Invocation{{"same", "arrangement_overview", `{}`}}
		if _, _, err := s.Execute(t.Context(), calls); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Execute(t.Context(), calls); !errors.Is(err, ErrInvalid) {
			t.Fatal("call ID reused")
		}
	})
}

func TestToolSession_FailedBatchCannotReuseUndeliveredReferences(t *testing.T) {
	e, _ := evidenceFixture(t, 20)
	s := NewToolSession(e)
	s.bytes = MaxTurnEvidenceBytes - 1024
	results, draft, err := s.Execute(t.Context(), []Invocation{
		{ID: "select", Name: "arrangement_select", Arguments: `{}`},
		{ID: "overview", Name: "arrangement_overview", Arguments: `{}`},
	})
	if !errors.Is(err, ErrLimit) || results != nil || draft != nil || len(e.selections) != 1 {
		t.Fatalf("expected undelivered partial batch: %v", err)
	}
	if _, _, err := s.Execute(t.Context(), []Invocation{{ID: "reuse", Name: "arrangement_detail", Arguments: `{"id":"item-0000"}`}}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed turn reused undelivered references")
	}
}

func TestToolDefinitions_AreFreshAndExposeNoAuthority(t *testing.T) {
	definitions := ToolDefinitions()
	if len(definitions) != 5 {
		t.Fatal("unexpected allowlist")
	}
	definitions[0].Parameters["type"] = "tampered"
	if ToolDefinitions()[0].Parameters["type"] != "object" {
		t.Fatal("shared mutable definitions")
	}
	for _, definition := range ToolDefinitions() {
		if definition.Parameters["additionalProperties"] != false {
			t.Fatal("open schema")
		}
		encoded, err := json.Marshal(definition.Parameters)
		if err != nil || strings.Contains(string(encoded), `"required":null`) {
			t.Fatalf("invalid schema: %s %v", encoded, err)
		}
	}
}
