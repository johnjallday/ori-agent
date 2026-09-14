package workspace

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func groupTemplateProvenanceFixture() *AssistantGroupTemplateProvenance {
	return &AssistantGroupTemplateProvenance{
		SchemaVersion:         AssistantGroupTemplateProvenanceSchemaVersion,
		GroupTemplateID:       "group-template:" + strings.Repeat("1", 32),
		GroupTemplateRevision: strings.Repeat("2", 64),
		SourceKind:            "plugin",
		TemplateID:            "plugin:neutral:project",
		TemplateRevision:      strings.Repeat("3", 64),
		PluginOwner:           &PluginTemplateOwner{PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 2},
		HomeDigest:            strings.Repeat("4", 64),
		ReviewDigest:          strings.Repeat("5", 64),
	}
}

func neutralHomeKey() AssistantProgramKey {
	return AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "project-guide"}
}

type saveFailingStore struct {
	Store
}

func (saveFailingStore) Save(*Workspace) error { return errors.New("disk full") }

func TestEnsureNamedStationWithProvenance_RecordsOnlyOnFirstCreation(t *testing.T) {
	store := NewInMemoryStore()
	service := NewAssistantProgramStore(store)
	fixedNow := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixedNow }

	provenance := groupTemplateProvenanceFixture()
	home, created, err := service.EnsureNamedStationWithProvenance(neutralHomeKey(), neutralAssistantDeclaration(), "  Lab Portfolio  ", provenance)
	if err != nil || !created || home.Name != "Lab Portfolio" || home.Kind != "group" {
		t.Fatalf("create = (%+v, %v, %v)", home, created, err)
	}
	state := home.GetAssistantProgramState()
	if state.GroupTemplate == nil || state.GroupTemplate.ReviewDigest != provenance.ReviewDigest || !state.GroupTemplate.CreatedAt.Equal(fixedNow) {
		t.Fatalf("recorded provenance = %+v", state.GroupTemplate)
	}
	if home.GetTemplateProvenance() != nil || len(home.GetAgentInstances()) != 0 || len(state.LinkedProjectIDs) != 0 || state.Hired {
		t.Fatalf("Home creation added project or staffing consequences: provenance=%+v state=%+v", home.GetTemplateProvenance(), state)
	}

	provenance.ReviewDigest = strings.Repeat("6", 64)
	state.GroupTemplate.PluginOwner.PluginID = "mutated"
	stored, _ := store.Get(home.ID)
	if got := stored.GetAssistantProgramState().GroupTemplate; got.ReviewDigest != strings.Repeat("5", 64) || got.PluginOwner.PluginID != "neutral" {
		t.Fatalf("stored provenance shared caller references: %+v", got)
	}

	other := groupTemplateProvenanceFixture()
	other.ReviewDigest = strings.Repeat("7", 64)
	again, created, err := service.EnsureNamedStationWithProvenance(neutralHomeKey(), neutralAssistantDeclaration(), "Another Name", other)
	if err != nil || created || again.ID != home.ID || again.Name != "Lab Portfolio" ||
		again.GetAssistantProgramState().GroupTemplate.ReviewDigest != strings.Repeat("5", 64) {
		t.Fatalf("reuse renamed or replaced provenance = (%+v, %v, %v)", again, created, err)
	}
}

func TestEnsureNamedStationWithProvenance_NeverBackfillsAHomeCreatedElsewhere(t *testing.T) {
	store := NewInMemoryStore()
	service := NewAssistantProgramStore(store)
	existing, _, err := service.EnsureNamedStation(neutralHomeKey(), neutralAssistantDeclaration(), "Guided Home")
	if err != nil {
		t.Fatal(err)
	}
	reused, created, err := service.EnsureNamedStationWithProvenance(neutralHomeKey(), neutralAssistantDeclaration(), "Template Name", groupTemplateProvenanceFixture())
	if err != nil || created || reused.ID != existing.ID || reused.Name != "Guided Home" || reused.GetAssistantProgramState().GroupTemplate != nil {
		t.Fatalf("reuse backfilled provenance = (%+v, %v, %v)", reused, created, err)
	}
}

func TestEnsureNamedStationWithProvenance_InvalidOrFailedWriteCreatesNothing(t *testing.T) {
	mutations := map[string]func(*AssistantGroupTemplateProvenance){
		"schema":            func(p *AssistantGroupTemplateProvenance) { p.SchemaVersion = 2 },
		"opaque id":         func(p *AssistantGroupTemplateProvenance) { p.GroupTemplateID = "plugin:neutral:project" },
		"uppercase digest":  func(p *AssistantGroupTemplateProvenance) { p.ReviewDigest = strings.Repeat("A", 64) },
		"short revision":    func(p *AssistantGroupTemplateProvenance) { p.GroupTemplateRevision = "abc" },
		"foreign plugin":    func(p *AssistantGroupTemplateProvenance) { p.PluginOwner.PluginID = "other" },
		"missing owner":     func(p *AssistantGroupTemplateProvenance) { p.PluginOwner = nil },
		"unknown kind":      func(p *AssistantGroupTemplateProvenance) { p.SourceKind = "library" },
		"user kind on key":  func(p *AssistantGroupTemplateProvenance) { p.SourceKind = "user_template"; p.PluginOwner = nil },
		"control character": func(p *AssistantGroupTemplateProvenance) { p.TemplateID = "plugin:neutral\nproject" },
		"empty template":    func(p *AssistantGroupTemplateProvenance) { p.TemplateID = "" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			store := NewInMemoryStore()
			provenance := groupTemplateProvenanceFixture()
			mutate(provenance)
			if _, _, err := NewAssistantProgramStore(store).EnsureNamedStationWithProvenance(neutralHomeKey(), neutralAssistantDeclaration(), "Home", provenance); !errors.Is(err, ErrAssistantGroupTemplateProvenanceInvalid) {
				t.Fatalf("error = %v, want invalid provenance", err)
			}
			if ids, _ := store.List(); len(ids) != 0 {
				t.Fatalf("invalid provenance created %v", ids)
			}
		})
	}

	t.Run("nil", func(t *testing.T) {
		if _, _, err := NewAssistantProgramStore(NewInMemoryStore()).EnsureNamedStationWithProvenance(neutralHomeKey(), neutralAssistantDeclaration(), "Home", nil); !errors.Is(err, ErrAssistantGroupTemplateProvenanceInvalid) {
			t.Fatalf("nil provenance error = %v", err)
		}
	})

	t.Run("user template namespace", func(t *testing.T) {
		store := NewInMemoryStore()
		key := AssistantProgramKey{OwnerUserID: "owner-1", TemplateID: "local-research", AttachmentID: "attachment-1", ProgramID: "project-guide"}
		provenance := groupTemplateProvenanceFixture()
		provenance.SourceKind, provenance.TemplateID, provenance.PluginOwner = "user_template", "Local-Research", nil
		if _, created, err := NewAssistantProgramStore(store).EnsureNamedStationWithProvenance(key, neutralAssistantDeclaration(), "Home", provenance); err != nil || !created {
			t.Fatalf("user template provenance = (%v, %v)", created, err)
		}
	})

	t.Run("failed save", func(t *testing.T) {
		primary := NewInMemoryStore()
		_, _, err := NewAssistantProgramStore(saveFailingStore{Store: primary}).EnsureNamedStationWithProvenance(neutralHomeKey(), neutralAssistantDeclaration(), "Home", groupTemplateProvenanceFixture())
		if err == nil {
			t.Fatal("failed first write reported success")
		}
		if ids, _ := primary.List(); len(ids) != 0 {
			t.Fatalf("failed write left Homes %v", ids)
		}
	})
}

func TestAssistantGroupTemplateProvenance_RoundTripsWithStationState(t *testing.T) {
	home, _, err := NewAssistantProgramStore(NewInMemoryStore()).EnsureNamedStationWithProvenance(neutralHomeKey(), neutralAssistantDeclaration(), "Home", groupTemplateProvenanceFixture())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := home.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := FromJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.GetAssistantProgramState().GroupTemplate
	want := home.GetAssistantProgramState().GroupTemplate
	if got == nil || got.GroupTemplateID != want.GroupTemplateID || got.PluginOwner == nil || *got.PluginOwner != *want.PluginOwner ||
		!got.CreatedAt.Equal(want.CreatedAt) || decoded.GetTemplateProvenance() != nil {
		t.Fatalf("round-tripped provenance = %+v, want %+v", got, want)
	}
}
