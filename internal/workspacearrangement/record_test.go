package workspacearrangement

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRecord_ClarificationEnvelopeHasNoSelectionsAndRevalidates(t *testing.T) {
	e, snapshot := evidenceFixture(t, 1000)
	provenance := Provenance{Origin: "model", SourceID: "home", Provider: "openai", Model: "fixture", ProviderFingerprint: strings.Repeat("a", 64)}
	record, err := e.NewClarificationRecord(t.Context(), "Which client owns these projects?", provenance)
	if err != nil || record.State != "needs_more_evidence" || len(record.Draft.Operations) != 0 || len(record.ItemRevisions) != 0 || len(record.CollectionRevisions) != 0 || record.Validate() != nil {
		t.Fatal("clarification manufactured intent", record, err)
	}
	if err := e.ValidateRecord(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.NewClarificationRecord(ctx, record.Draft.Goal, provenance); err == nil {
		t.Fatal("cancelled completion accepted")
	}
	if _, err := e.NewClarificationRecord(t.Context(), "", provenance); err == nil {
		t.Fatal("missing goal accepted")
	}
	snapshot.Revision = "changed"
	if _, err := e.NewClarificationRecord(t.Context(), record.Draft.Goal, provenance); err == nil {
		t.Fatal("stale evidence accepted")
	}
}

func TestRecord_ResolvedThousandMemberDraftRoundTripsWithoutLiveHandles(t *testing.T) {
	_, snapshot := evidenceFixture(t, 1000)
	snapshot.Items[0].ID = "CaseSensitiveID"
	e, err := NewEvidence(t.Context(), func(context.Context) (Snapshot, error) { return *snapshot, nil })
	if err != nil {
		t.Fatal(err)
	}
	selected, err := e.Select(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	wire := Draft{Version: Version, SourceRevision: selected.Revision, Goal: "Keep these catalog-only", Operations: []Operation{{Kind: CreateCollection, Name: "References", Members: Selection{Handle: selected.Handle}}}}
	raw, _ := json.Marshal(wire)
	resolved, err := e.Resolve(t.Context(), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	record, err := e.NewRecord(t.Context(), resolved, Provenance{Origin: "model", SourceID: "home", Provider: "openai", Model: "fixture", ProviderFingerprint: strings.Repeat("a", 64)})
	if err != nil || len(record.ItemRevisions) != 1000 {
		t.Fatalf("record: %v", err)
	}
	encoded, err := json.Marshal(record)
	if err != nil || strings.Contains(string(encoded), selected.Handle) {
		t.Fatal("ephemeral selector persisted", err)
	}
	restored, err := DecodeRecord(encoded)
	if err != nil || len(restored.Draft.Operations[0].Members.IDs) != 1000 || restored.ItemRevisions["CaseSensitiveID"] != "1" {
		t.Fatal("resolved record failed restart", err)
	}
	preview, err := restored.Summarize()
	if err != nil || preview.ReviewAvailable || len(preview.ProposedOperations) != 1 || preview.ProposedOperations[0].Members != 1000 {
		t.Fatalf("invalid bounded preview: %+v %v", preview, err)
	}
	previewJSON, err := json.Marshal(preview)
	if err != nil || len(previewJSON) > 4096 || strings.Contains(string(previewJSON), "CaseSensitiveID") || strings.Contains(string(previewJSON), record.ProviderFingerprint) {
		t.Fatal("summary exposed membership/authority or grew with the catalog", err)
	}
	preview.ProposedOperations[0].Name = "edited preview"
	if restored.Draft.Operations[0].Name != "References" {
		t.Fatal("summary aliases saved intent")
	}
	invalid := restored
	invalid.State = "completed"
	if _, err := invalid.Summarize(); err == nil {
		t.Fatal("summary accepted an invalid executable record")
	}
	resolved.Operations[0].Members.IDs[0] = "tampered"
	if record.Draft.Operations[0].Members.IDs[0] == "tampered" {
		t.Fatal("record aliases caller intent")
	}
	delete(restored.ItemRevisions, "CaseSensitiveID")
	if restored.Validate() == nil {
		t.Fatal("missing exact item revision accepted")
	}
}

func TestRecord_RejectsAuthorityFieldsAliasesAndExecutableStates(t *testing.T) {
	e, _ := evidenceFixture(t, 1)
	o, err := e.Overview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.NewRecord(t.Context(), Draft{Version: Version, SourceRevision: o.Revision, Goal: "No change"}, Provenance{Origin: "manual", SourceID: "home"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	for _, field := range []string{`"approved":true,`, `"Version":1,`, `"version":1,`} {
		injected := "{" + field + string(raw[1:])
		if _, err := DecodeRecord([]byte(injected)); err == nil {
			t.Fatalf("accepted injected field: %s", field)
		}
		var envelope struct {
			Record Record `json:"record"`
		}
		if err := json.Unmarshal([]byte(`{"record":`+injected+`}`), &envelope); err == nil {
			t.Fatalf("nested decoder accepted injected field: %s", field)
		}
	}
	unpinned := r
	unpinned.Origin, unpinned.Provider, unpinned.Model = "model", "openai", "fixture"
	if unpinned.Validate() == nil {
		t.Fatal("model provenance omitted provider fingerprint")
	}
	r.State = "completed"
	if r.Validate() == nil {
		t.Fatal("inert draft claimed completion")
	}
	r.State = "draft"
	r.Draft.Unchanged.Handle = "ephemeral"
	if r.Validate() == nil {
		t.Fatal("record retained a live selector")
	}
}
