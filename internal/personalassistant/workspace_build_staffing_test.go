package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

func testBuildStaffing() *BuildStaffing {
	return &BuildStaffing{Version: 1, TemplateID: "code-project", TemplateRevision: "revision", State: rosterstaffing.Recommended,
		Coverage: rosterstaffing.Coverage{Complete: true, Inspected: 1, Included: 1},
		Roles: []BuildStaffingRole{{RoleID: "engineer", Scope: "workspace", State: rosterstaffing.Recommended,
			Choices: []BuildStaffingChoice{{Handle: "c_one", Name: "Scout", Source: "root", Configured: true, SourceWitness: "opaque-witness",
				Reasons: []rosterstaffing.Evidence{{ID: "c_one_e1", Kind: "declared_purpose", Value: "Builds software"}}}}}}}
}

func TestWorkspaceBuildStaffingRoundTripAndPublicProjection(t *testing.T) {
	ctx := context.Background()
	store, _, now := newWorkspaceBuildStoreFixture(t)
	_, err := store.Mutate(ctx, "local", func(doc *WorkspaceBuildDocument) error {
		session := openBuild("staffing", *now)
		session.Staffing = testBuildStaffing()
		doc.Add(session)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	staffing := doc.Open().Staffing
	if staffing == nil || staffing.Roles[0].Choices[0].SourceWitness != "opaque-witness" {
		t.Fatal("lost server-held witness")
	}
	if staffing.Public().Roles[0].Choices[0].SourceWitness != "" || staffing.Roles[0].Choices[0].SourceWitness == "" {
		t.Fatal("public projection leaked or mutated witness")
	}
	doc.Open().Settle()
	if doc.Open().Staffing != nil {
		t.Fatal("settled session retained advice")
	}
}

func TestWorkspaceBuildStaffingLegacyCannotAcquireAuthority(t *testing.T) {
	store, _, now := newWorkspaceBuildStoreFixture(t)
	doc, err := store.Mutate(context.Background(), "local", func(doc *WorkspaceBuildDocument) error { doc.Add(openBuild("staffing", *now)); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version < 6; version++ {
		doc.SchemaVersion = version
		doc.Open().Staffing = nil
		data, _ := json.Marshal(doc)
		decoded, err := decodeWorkspaceBuild(data, doc.Owner)
		if err != nil || decoded.Open().Staffing != nil {
			t.Fatalf("legacy v%d: %v", version, err)
		}
		doc.Open().Staffing = testBuildStaffing()
		data, _ = json.Marshal(doc)
		if _, err := decodeWorkspaceBuild(data, doc.Owner); !errors.Is(err, ErrKnowledgeCorrupt) {
			t.Fatalf("legacy v%d acquired advice authority", version)
		}
	}
}

func TestWorkspaceBuildSelectedStaffingRoundTripAndLegacyRefusal(t *testing.T) {
	store, _, now := newWorkspaceBuildStoreFixture(t)
	doc, err := store.Mutate(context.Background(), "local", func(doc *WorkspaceBuildDocument) error {
		build := openBuild("selected-staffing", *now)
		build.Staffing = testBuildStaffing()
		build.SelectedStaffing = &BuildStaffingSelection{Version: 1, OwnerUserID: "local", ContextDigest: strings.Repeat("a", 64), TemplateRevision: strings.Repeat("b", 64),
			Choices: []BuildStaffingSelectedRole{{RoleID: "engineer", Scope: "workspace", Handle: "c_one", Name: "Scout", SourceWitness: "opaque-witness"}}}
		doc.Add(build)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Read(context.Background(), "local")
	if err != nil || loaded.Open().SelectedStaffing.Choices[0].SourceWitness != "opaque-witness" {
		t.Fatal("selected source did not round-trip")
	}
	for version := 1; version < 7; version++ {
		doc.SchemaVersion = version
		doc.Open().Staffing = nil
		data, _ := json.Marshal(doc)
		if _, err := decodeWorkspaceBuild(data, doc.Owner); !errors.Is(err, ErrKnowledgeCorrupt) {
			t.Fatalf("v%d acquired selection authority", version)
		}
	}
	doc.SchemaVersion = 6
	doc.Open().SelectedStaffing = nil
	doc.Open().Staffing = testBuildStaffing()
	data, _ := json.Marshal(doc)
	if migrated, err := decodeWorkspaceBuild(data, doc.Owner); err != nil || migrated.Open().SelectedStaffing != nil || migrated.Open().Staffing == nil {
		t.Fatal("genuine v6 advice did not migrate without selection authority")
	}
	loaded.Open().Settle()
	if loaded.Open().SelectedStaffing != nil {
		t.Fatal("settlement retained selected source")
	}
}

func TestWorkspaceBuildStaffingRejectsUnboundedOrUnsupportedState(t *testing.T) {
	for _, mode := range []string{"version", "state", "scope", "source", "reason", "witness", "context", "count"} {
		s := testBuildStaffing()
		switch mode {
		case "version":
			s.Version = 2
		case "state":
			s.State = "high_confidence"
		case "scope":
			s.Roles[0].Scope = "foreign"
		case "source":
			s.Roles[0].Choices[0].Source = "workspace"
		case "reason":
			s.Roles[0].Choices[0].Reasons[0].Kind = "performance"
		case "witness":
			s.Roles[0].Choices[0].SourceWitness = ""
		case "context":
			s.ContextVersion = 10
		case "count":
			s.Coverage.Included = 101
		}
		if err := s.validate(0); !errors.Is(err, ErrWorkspaceBuildInvalid) {
			t.Fatalf("accepted %s", mode)
		}
	}
}
