package server

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestPortfolioModel_OnlyNewStaffingBindsTheReviewedSystemConfiguration(t *testing.T) {
	f := newDraftServerFixture(t)
	host := &folderSetupHost{builder: f.builder}
	_, _, offerID := reviewFolder(t, f, stageReviewFolder(t, f))
	store := personalassistant.NewFolderDigestStore(personalassistant.NewKnowledgeStore(
		personalassistant.NewKnowledgeResolver(f.builder.personalAssistantStore, f.builder.personalHQService,
			personalassistant.NewAgentStoreProfileReader(f.builder.st)), f.builder.workspaceFileStore))
	for _, existing := range []bool{false, true} {
		facts := foldersetup.PortfolioFacts{FolderName: "Albums", Projects: 6, HomeName: "Music Home", HomeExists: existing}
		if err := f.builder.configManager.SetSystemModel("", ""); err != nil {
			t.Fatal(err)
		}
		missing, err := host.bindPortfolioModel(foldersetup.BuildPortfolioPlan(facts))
		if err != nil || missing.Visual.Model.State != "missing" || missing.Intent.Model != nil {
			t.Fatalf("missing: %+v %v", missing, err)
		}
		if err := f.builder.configManager.SetSystemModel("openai", "reviewed-model"); err != nil {
			t.Fatal(err)
		}
		plan, err := host.bindPortfolioModel(foldersetup.BuildPortfolioPlan(facts))
		if err != nil || plan.Intent.Model == nil || plan.Intent.Model.Source != "system" || plan.Intent.Model.Model != "reviewed-model" || plan.Digest == missing.Digest {
			t.Fatalf("reviewed model: %+v %v", plan, err)
		}
		shown := false
		for _, line := range plan.Lines {
			if line.Kind == personalassistant.FolderPlanAgents && strings.Contains(line.Detail, "openai · reviewed-model") {
				shown = true
			}
		}
		if !shown {
			t.Fatal("staffing omitted configured model use")
		}
		// Persist the exact host disclosure, not a simplified fake plan. A line
		// rejected by the sidecar validator would otherwise fail at Set up.
		if _, err := store.Mutate(t.Context(), "local", func(doc *personalassistant.FolderDigestDocument) error {
			now := time.Now().UTC()
			doc.Offer(offerID).Setup = &personalassistant.FolderSetupRun{Status: personalassistant.FolderSetupStopped,
				PlanDigest: plan.Digest, Lines: plan.Lines, Intent: plan.Intent, Visual: plan.Visual,
				StartedAt: now, UpdatedAt: now}
			return nil
		}); err != nil {
			t.Fatalf("reviewed model plan cannot be saved: %v", err)
		}
		req := personalassistant.FolderSetupRequest{Plan: plan}
		if err := host.validateSetupModel(req); err != nil {
			t.Fatal(err)
		}
		if err := f.builder.configManager.SetSystemModel("openai", "changed-model"); err != nil {
			t.Fatal(err)
		}
		if err := host.validateSetupModel(req); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
			t.Fatal("changed model accepted", err)
		}
		if req.Plan.Intent.Model.Model != "reviewed-model" {
			t.Fatal("accepted model was refreshed")
		}
		facts.HomeExists, facts.HomeStaffed = true, true
		staffed, err := host.bindPortfolioModel(foldersetup.BuildPortfolioPlan(facts))
		if err != nil || staffed.Visual.Model != nil || staffed.Intent.Model != nil {
			t.Fatal("library listing imposed a model on an already staffed Home", err)
		}
	}
}
