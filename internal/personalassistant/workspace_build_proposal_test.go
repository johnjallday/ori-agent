package personalassistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkspaceBuildProposalStoreRoundTrip(t *testing.T) {
	store, _, now := newWorkspaceBuildStoreFixture(t)
	_, err := store.Mutate(context.Background(), "local", func(doc *WorkspaceBuildDocument) error {
		s := openBuild("build", *now)
		s.ContextVersion = 2
		s.InferenceID = "inference"
		s.EditedFields = map[string]int64{"name": 1, "team": 1}
		s.Proposal = &BuildProposal{ID: "proposal", FolderID: "observation", ContextVersion: 2, Status: "running", Attempt: 1, StartedAt: *now}
		doc.Add(s)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	s := doc.Open()
	if s.Proposal.Status != "running" || s.Proposal.Attempt != 1 || s.EditedFields["name"] != 1 || s.Draft.Name != "" || s.TurnCount != 0 || s.FirstRequest != "" {
		t.Fatalf("resume changed proposal/empty edit/provenance: %+v", s)
	}
	s.Settle()
	if s.Proposal != nil || s.InferenceID != "" || s.CreateNow || s.EditedFields != nil {
		t.Fatal("settle retained inference")
	}
}

func TestWorkspaceBuildProposalLegacyDefaults(t *testing.T) {
	_, kf, now := newWorkspaceBuildStoreFixture(t)
	binding, err := kf.resolver().Resolve(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	owner := binding.owner()
	for _, version := range []int{1, 2, 3} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			s := openBuild("build", *now)
			s.CreateNow = true
			doc := WorkspaceBuildDocument{SchemaVersion: version, Version: 1, Owner: owner, Sessions: []WorkspaceBuildSession{s}}
			data, _ := json.Marshal(doc)
			loaded, err := decodeWorkspaceBuild(data, owner)
			if err != nil {
				t.Fatal(err)
			}
			got := loaded.Open()
			if loaded.SchemaVersion != WorkspaceBuildSchemaVersion || got.Proposal != nil || got.CreateNow || len(got.EditedFields) != 9 {
				t.Fatalf("unsafe old-session defaults: %+v", got)
			}
			doc.Sessions[0].Proposal = &BuildProposal{ID: "proposal"}
			data, _ = json.Marshal(doc)
			if _, err := decodeWorkspaceBuild(data, owner); err == nil {
				t.Fatal("accepted new authority in old schema")
			}
		})
	}
}

func TestWorkspaceBuildProposalValidation(t *testing.T) {
	_, _, now := newWorkspaceBuildStoreFixture(t)
	for _, test := range []struct {
		name   string
		change func(*WorkspaceBuildSession)
	}{
		{"unknown field", func(s *WorkspaceBuildSession) { s.EditedFields = map[string]int64{"path": 1} }},
		{"future edit", func(s *WorkspaceBuildSession) { s.EditedFields = map[string]int64{"name": 2} }},
		{"path id", func(s *WorkspaceBuildSession) { s.Proposal.ID = "/private/folder" }},
		{"unbounded attempt", func(s *WorkspaceBuildSession) { s.Proposal.Attempt = 4 }},
		{"missing start", func(s *WorkspaceBuildSession) { s.Proposal.Status = "running"; s.Proposal.Attempt = 1 }},
		{"unknown state", func(s *WorkspaceBuildSession) { s.Proposal.Status = "create" }},
		{"future context", func(s *WorkspaceBuildSession) { s.Proposal.ContextVersion = 2 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := openBuild("build", *now)
			s.Proposal = &BuildProposal{ID: "proposal", FolderID: "observation", Status: "pending"}
			test.change(&s)
			if err := validateBuildSession(s); err == nil || !strings.Contains(err.Error(), "proposal state") {
				t.Fatalf("accepted invalid proposal: %v", err)
			}
		})
	}
}
