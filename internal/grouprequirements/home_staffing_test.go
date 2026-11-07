package grouprequirements

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func reviewedHomeTeamFixture() *rosterstaffing.TeamIntent {
	return &rosterstaffing.TeamIntent{Version: 1, Roles: []rosterstaffing.RoleIntent{{
		RoleID: "guide", Scope: "home", Mode: "assign", Name: "Aster", SourceWitness: strings.Repeat("b", 64),
	}}}
}

func TestGroupHomeStaffingAdviceIsInertScopedAndNeverSelectedAuthority(t *testing.T) {
	workspaces := workspace.NewInMemoryStore()
	service := NewService(workspaces, NewMemoryStore())
	input := groupTemplateHomeInput(t, testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate), "Collection")
	input.HomeAdvice = &rosterstaffing.Advice{Version: 2, State: rosterstaffing.Recommended,
		TemplateID: input.GroupTemplateID, TemplateRevision: input.GroupTemplateRevision,
		Coverage: rosterstaffing.Coverage{Complete: true, Inspected: 1, Included: 1},
		Roles: []rosterstaffing.RoleAdvice{{RoleID: "guide", Scope: "home", State: rosterstaffing.Recommended,
			Choices: []rosterstaffing.AdviceChoice{{Handle: "candidate", Name: "Aster", Source: "root", Configured: true,
				SourceWitness: "private-source", CapabilityWitness: strings.Repeat("d", 64),
				Reasons: []rosterstaffing.Evidence{{ID: "purpose", Kind: "declared_purpose", Value: "Coordinates a project collection."}},
			}},
		}},
	}
	review, err := service.Review(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.HomeAdvice.Roles[0].Choices[0].Name = "Changed"
	advice, err := service.ReviewedHomeAdvice(t.Context(), input, review.Token)
	if err != nil || advice.Roles[0].Choices[0].Name != "Aster" {
		t.Fatal("advice aliases caller", err)
	}
	data, err := json.Marshal(review)
	if err != nil || strings.Contains(string(data), "private-source") || strings.Contains(string(data), "home_advice") {
		t.Fatal("private advice escaped Home review", err)
	}
	selected := input
	selected.HomeAdvice, selected.HomeStaffing = nil, reviewedHomeTeamFixture()
	if _, err := service.Claim(t.Context(), selected, review.Token, "unreviewed-team"); !errors.Is(err, ErrReviewStale) {
		t.Fatal("advice token authorized a selected team", err)
	}
	foreign := input
	foreign.GroupTemplateRevision = strings.Repeat("f", 64)
	if _, err := service.ReviewedHomeAdvice(t.Context(), foreign, review.Token); !errors.Is(err, ErrReviewStale) {
		t.Fatal("foreign context read advice", err)
	}
	service.now = func() time.Time { return review.ExpiresAt.Add(time.Second) }
	if _, err := service.ReviewedHomeAdvice(t.Context(), input, review.Token); !errors.Is(err, ErrReviewStale) {
		t.Fatal("expired advice renewed", err)
	}
	if ids, _ := workspaces.List(); len(ids) != 0 {
		t.Fatal("advice caused Home effects")
	}
}

func TestGroupHomeStaffingIsPrivateReviewedMaterialNotAnEffect(t *testing.T) {
	workspaces := workspace.NewInMemoryStore()
	receipts := NewMemoryStore()
	service := NewService(workspaces, receipts)
	input := groupTemplateHomeInput(t, testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate), "Collection")
	input.HomeStaffing = reviewedHomeTeamFixture()
	review, err := service.Review(t.Context(), input)
	if err != nil || review.Token == "" {
		t.Fatal("Home team review refused", err)
	}
	data, err := json.Marshal(review)
	if err != nil || strings.Contains(string(data), "source_witness") || strings.Contains(string(data), "home_staffing") {
		t.Fatal("private choice material escaped review projection", err)
	}
	input.HomeStaffing.Roles[0].Name = "Changed"
	saved, err := service.ReviewedHomeTeam(t.Context(), input, review.Token)
	if err != nil || saved.Roles[0].Name != "Aster" {
		t.Fatal("stored choice aliases its input", err)
	}
	saved.Roles[0].Name = "Changed again"
	saved, err = service.ReviewedHomeTeam(t.Context(), input, review.Token)
	if err != nil || saved.Roles[0].Name != "Aster" {
		t.Fatal("stored choice aliases its reader", err)
	}
	input.HomeStaffing = saved
	if ids, _ := workspaces.List(); len(ids) != 0 {
		t.Fatal("review created a Home")
	}
	claim, err := service.Claim(t.Context(), input, review.Token, "create-collection")
	if err != nil {
		t.Fatal(err)
	}
	home, err := workspaces.Get(claim.Operation.HomeWorkspaceID)
	if err != nil || len(home.GetAgentInstances()) != 0 || len(home.GetAssistantProgramState().HomeBindings.Bindings) != 0 {
		t.Fatal("Home-only commit became a second staffing owner", err)
	}
	if _, err := service.Claim(t.Context(), input, review.Token, "create-collection"); err != nil {
		t.Fatal("unchanged Home operation stopped replaying", err)
	}
}

func TestGroupHomeStaffingRejectsChangedOmittedAndForeignChoices(t *testing.T) {
	workspaces := workspace.NewInMemoryStore()
	service := NewService(workspaces, NewMemoryStore())
	input := groupTemplateHomeInput(t, testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate), "Collection")
	input.HomeStaffing = reviewedHomeTeamFixture()
	review, err := service.Review(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Input){
		"omitted":               func(i *Input) { i.HomeStaffing = nil },
		"same name replacement": func(i *Input) { i.HomeStaffing.Roles[0].SourceWitness = strings.Repeat("c", 64) },
		"capability change":     func(i *Input) { i.HomeStaffing.Roles[0].CapabilityWitness = strings.Repeat("c", 64) },
		"action change": func(i *Input) {
			i.HomeStaffing.Roles[0] = rosterstaffing.RoleIntent{RoleID: "guide", Scope: "home", Mode: "create", Name: "Aster", Provider: "fixture", Model: "model"}
		},
		"unknown role":  func(i *Input) { i.HomeStaffing.Roles[0].RoleID = "other" },
		"project role":  func(i *Input) { i.HomeStaffing.Roles[0].RoleID = "maker" },
		"project scope": func(i *Input) { i.HomeStaffing.Roles[0].Scope = "project" },
		"owner":         func(i *Input) { i.OwnerUserID = "other" },
		"revision":      func(i *Input) { i.GroupTemplateRevision = strings.Repeat("c", 64) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := input
			changed.HomeStaffing = input.HomeStaffing.Clone()
			mutate(&changed)
			if _, err := service.Claim(t.Context(), changed, review.Token, "create"); !errors.Is(err, ErrReviewStale) {
				t.Fatal("changed choice accepted", err)
			}
		})
	}
	if ids, _ := workspaces.List(); len(ids) != 0 {
		t.Fatal("refused choices created a Home")
	}
	wrongOwner := input
	wrongOwner.OwnerUserID = "other"
	if _, err := service.ReviewedHomeTeam(t.Context(), wrongOwner, review.Token); !errors.Is(err, ErrReviewStale) {
		t.Fatal("private choice returned for foreign owner", err)
	}
}

func TestGroupHomeStaffingSQLiteReceiptKeepsOriginalChoiceAcrossServiceRestart(t *testing.T) {
	db, err := database.Open(t.Context(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	receipts, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	workspaces := workspace.NewInMemoryStore()
	service := NewService(workspaces, receipts)
	input := groupTemplateHomeInput(t, testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate), "Collection")
	input.HomeStaffing = reviewedHomeTeamFixture()
	review, err := service.Review(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewService(workspaces, receipts)
	team, err := restarted.ReviewedHomeTeam(t.Context(), input, review.Token)
	if err != nil || team.Roles[0].SourceWitness != input.HomeStaffing.Roles[0].SourceWitness {
		t.Fatal("SQLite receipt lost or refreshed the original source", err)
	}
	if _, err := restarted.Claim(t.Context(), input, review.Token, "original"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(workspaces, receipts).Claim(t.Context(), input, review.Token, "original"); err != nil {
		t.Fatal("consumed SQL receipt failed Home-only replay", err)
	}
}

func TestGroupHomeStaffingCannotEnterLegacyOrProjectContracts(t *testing.T) {
	workspaces := workspace.NewInMemoryStore()
	receipts := NewMemoryStore()
	service := NewService(workspaces, receipts)
	template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)
	input := groupTemplateHomeInput(t, template, "Collection")
	review, err := service.Review(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.HomeStaffing = reviewedHomeTeamFixture()
	if _, err := service.Claim(t.Context(), input, review.Token, "new-authority"); !errors.Is(err, ErrReviewStale) {
		t.Fatal("old Home-only review acquired staffing material", err)
	}
	receipt, err := receipts.GetReceipt(t.Context(), review.Token)
	if err != nil {
		t.Fatal(err)
	}
	receipt.HomeStaffing = input.HomeStaffing.Clone()
	if err := receipts.SaveReceipt(t.Context(), receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(t.Context(), input, review.Token, "injected"); !errors.Is(err, ErrReviewStale) {
		t.Fatal("old digest accepted newly injected staffing", err)
	}
	input.GroupTemplateID, input.GroupTemplateRevision = "", ""
	if _, err := service.Review(t.Context(), input); !errors.Is(err, ErrReviewRequired) {
		t.Fatal("unbound Home review accepted team", err)
	}
	if _, _, _, err := service.CommitReviewed(input, "child", strings.Repeat("a", 64), strings.Repeat("b", 64)); !errors.Is(err, ErrReviewRequired) {
		t.Fatal("project bypass accepted Home staffing", err)
	}
	if ids, _ := workspaces.List(); len(ids) != 0 {
		t.Fatal("legacy authority changed canonical state")
	}
}
