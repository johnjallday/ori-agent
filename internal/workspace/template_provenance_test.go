package workspace

import (
	"encoding/json"
	"testing"
)

func TestTemplateProvenance_SetGetIsFrom(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	if ws.GetTemplateProvenance() != nil {
		t.Fatal("new workspace should have no provenance")
	}
	if ws.IsFromTemplate("reaper-song") {
		t.Fatal("unset provenance must not match any template")
	}

	ws.SetTemplateProvenance(&TemplateProvenance{TemplateID: "  reaper-song  ", Builtin: true, Version: 4})
	p := ws.GetTemplateProvenance()
	if p == nil || p.TemplateID != "reaper-song" {
		t.Fatalf("provenance not stored/trimmed: %+v", p)
	}
	if p.AppliedAt.IsZero() {
		t.Fatal("AppliedAt should default to now")
	}
	if !ws.IsFromTemplate("Reaper-Song") {
		t.Fatal("IsFromTemplate should be case-insensitive")
	}
	if ws.IsFromTemplate("writing-project") {
		t.Fatal("must not match a different template")
	}
}

func TestTemplateProvenance_GetReturnsCopy(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	ws.SetTemplateProvenance(&TemplateProvenance{TemplateID: "reaper-song"})
	got := ws.GetTemplateProvenance()
	got.TemplateID = "mutated"
	if ws.GetTemplateProvenance().TemplateID != "reaper-song" {
		t.Fatal("GetTemplateProvenance must return a copy, not the internal pointer")
	}
}

func TestTemplateProvenance_JSONRoundTrip(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	ws.SetTemplateProvenance(&TemplateProvenance{TemplateID: "reaper-song", TemplateName: "Reaper Song", Builtin: true, Version: 4})
	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatal(err)
	}
	var back Workspace
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !back.IsFromTemplate("reaper-song") {
		t.Fatalf("provenance did not survive JSON round-trip: %+v", back.TemplateProvenance)
	}
}

func TestTemplateProvenance_ClearWithNil(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	ws.SetTemplateProvenance(&TemplateProvenance{TemplateID: "reaper-song"})
	ws.SetTemplateProvenance(nil)
	if ws.GetTemplateProvenance() != nil {
		t.Fatal("nil should clear provenance")
	}
}
