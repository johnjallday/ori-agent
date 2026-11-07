package personalassistant

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

func folderHomeReviewFixture() *FolderHomeStaffingReview {
	return &FolderHomeStaffingReview{Version: 1, Revision: 1, ContextDigest: strings.Repeat("c", 64),
		Team: homeStaffingPlanFixture().Intent.HomeStaffing,
		Advice: &rosterstaffing.Advice{Version: 2, TemplateID: "managed-home", TemplateRevision: "revision", State: rosterstaffing.Recommended,
			Roles: []rosterstaffing.RoleAdvice{{RoleID: "manager", Scope: "home", State: rosterstaffing.Recommended, Choices: []rosterstaffing.AdviceChoice{{
				Handle: "candidate", Name: "Aster", Source: "root", Configured: true, SourceWitness: "original-source",
				Reasons: []rosterstaffing.Evidence{{ID: "purpose", Kind: "declared_purpose", Value: "Manage the collection"}},
			}}}}},
	}
}

func TestFolderHomeReviewPersistsOriginalAdviceAndChoicesSeparately(t *testing.T) {
	doc := homeStaffingDocumentFixture()
	doc.Offers[0].Setup = nil
	doc.Offers[0].HomeStaffingReview = folderHomeReviewFixture()
	data, err := encodeFolderDigest(doc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeFolderDigest(data, doc.Owner)
	if err != nil || !reflect.DeepEqual(got.Offers[0].HomeStaffingReview, doc.Offers[0].HomeStaffingReview) {
		t.Fatal("original review lost", err)
	}
	copy := doc.Offers[0].HomeStaffingReview.Clone()
	copy.Advice.Roles[0].Choices[0].SourceWitness = "changed"
	copy.Team.Roles[1].Appearance.Generated.Color = "#123456"
	if reflect.DeepEqual(copy, doc.Offers[0].HomeStaffingReview) || doc.Offers[0].HomeStaffingReview.Advice.Roles[0].Choices[0].SourceWitness != "original-source" || doc.Offers[0].HomeStaffingReview.Team.Roles[1].Appearance.Generated.Color != "" {
		t.Fatal("review aliases original witnesses/configuration")
	}
	copy.Team, copy.Advice, copy.Revision = nil, nil, 2
	if err := copy.validate(); err != nil {
		t.Fatal("explicit clear must retain its late-reply fence", err)
	}
	copy = doc.Offers[0].HomeStaffingReview.Clone()
	copy.Team = nil
	if err := copy.validate(); err != nil {
		t.Fatal("advice must not require/seed choices", err)
	}
}

func TestFolderHomeReviewRefusesLegacyInjectionAndInvalidPrivateState(t *testing.T) {
	for _, version := range []int{1, 2} {
		doc := homeStaffingDocumentFixture()
		doc.SchemaVersion, doc.Offers[0].Setup = version, nil
		doc.Offers[0].HomeStaffingReview = folderHomeReviewFixture()
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeFolderDigest(data, doc.Owner); !errors.Is(err, ErrKnowledgeCorrupt) {
			t.Fatal("legacy acquired draft authority", version, err)
		}
	}
	for name, change := range map[string]func(*FolderHomeStaffingReview){
		"unknown version":         func(r *FolderHomeStaffingReview) { r.Version++ },
		"missing revision":        func(r *FolderHomeStaffingReview) { r.Revision = 0 },
		"unknown context":         func(r *FolderHomeStaffingReview) { r.ContextDigest = "" },
		"invalid context":         func(r *FolderHomeStaffingReview) { r.ContextDigest = strings.Repeat("x", 64) },
		"legacy advice":           func(r *FolderHomeStaffingReview) { r.Advice.Version = 1 },
		"foreign advice scope":    func(r *FolderHomeStaffingReview) { r.Advice.Roles[0].Scope = "project" },
		"foreign team scope":      func(r *FolderHomeStaffingReview) { r.Team.Roles[0].Scope = "project" },
		"missing selected source": func(r *FolderHomeStaffingReview) { r.Team.Roles[0].SourceWitness = "" },
	} {
		t.Run(name, func(t *testing.T) {
			doc := homeStaffingDocumentFixture()
			doc.Offers[0].HomeStaffingReview = folderHomeReviewFixture()
			change(doc.Offers[0].HomeStaffingReview)
			if _, err := encodeFolderDigest(doc); err == nil {
				t.Fatal("invalid private review persisted")
			}
		})
	}
}
