package personalassistant

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type readinessSetup struct {
	*fakeFolderSetup
	reads  int
	visual *FolderSetupVisual
}

func (s *readinessSetup) ReadSetupReadiness(context.Context, FolderSetupRequest) *FolderSetupVisual {
	s.reads++
	return s.visual
}

func TestFolderReadinessIsExplicitReviewOnlyAndNeverAPlan(t *testing.T) {
	fx := newBuildSetupFixture(t)
	setup := &readinessSetup{fakeFolderSetup: fx.setup, visual: &FolderSetupVisual{
		Plugins: []FolderSetupPlugin{{Kind: "integration", Name: "Known plugin", Operation: "unavailable"}},
	}}
	setup.planErr = errors.New("no complete plan")
	fx.folderDigestFixture.service.deps.Setup = setup
	fx.service.HireCandidate(t.Context(), fx.draft, fx.observation.ID)
	fx.service.ReviewOptions(t.Context(), fx.draft, fx.observation.ID)
	if setup.reads != 0 || setup.runs != 0 {
		t.Fatal("attachment eligibility gained prerequisite or setup effects")
	}
	for _, operation := range []string{"", FolderOperationSupport} {
		ctx := WithFolderReviewPlacement(t.Context(), FolderReviewPlacement{Operation: operation})
		if _, err := fx.service.PreviewReview(ctx, fx.draft, fx.observation.ID, fx.candidate); err != nil || setup.reads != 0 {
			t.Fatal("unbound/supporting choice gained plugin checks", err)
		}
	}
	ctx := WithFolderReviewPlacement(t.Context(), FolderReviewPlacement{Operation: FolderOperationCreate})
	preview, err := fx.service.PreviewReview(ctx, fx.draft, fx.observation.ID, fx.candidate)
	if err != nil || setup.reads != 1 || preview.Readiness == nil || preview.Plan != nil || setup.runs != 0 {
		t.Fatalf("explicit partial review: %+v, %v, reads %d", preview, err, setup.reads)
	}
	preview.Readiness.Plugins[0].Name = "changed"
	if setup.visual.Plugins[0].Name != "Known plugin" {
		t.Fatal("outward readiness aliases its reader")
	}
}

func TestFolderRequirementsMetadataIsBoundedAndOutsideConsent(t *testing.T) {
	plan := NewFolderSetupPlan([]FolderPlanLine{{Kind: FolderPlanWorkspace, Name: "Creates a workspace"}})
	plan.Visual = &FolderSetupVisual{RequirementsChecked: true, Plugins: []FolderSetupPlugin{{
		Kind: FolderPlanIntegration, Name: "<literal>界", Operation: FolderInstallEnable,
		Source: "owner/repository", Purpose: "Adds the project blueprint.",
	}}}
	stamped := plan.Stamped()
	if stamped.Digest != plan.Digest || !stamped.Visual.RequirementsChecked || stamped.Visual.Plugins[0].Source != "owner/repository" {
		t.Fatal("display metadata changed consent or lost a trusted source")
	}
	for _, unsafe := range []string{"/private/secrets", "../source", "source\nsecret", strings.Repeat("界", 241)} {
		plan.Visual.Plugins[0].Source, plan.Visual.Plugins[0].Purpose = unsafe, unsafe
		shown := plan.Stamped()
		if shown.Visual.Plugins[0].Source != "" || shown.Visual.Plugins[0].Purpose != "" || shown.Digest != plan.Digest {
			t.Fatalf("unsafe display metadata retained or became consent: %q %+v", unsafe, shown.Visual)
		}
	}
	for _, state := range []string{"not_checked", "unavailable", "unverified", "future"} {
		plan.Visual.Plugins[0].Operation = state
		shown := plan.Stamped()
		if shown.Visual != nil && shown.Visual.RequirementsChecked {
			t.Fatalf("unknown/failed requirement counted as completely checked: %s", state)
		}
	}
	plan.Visual = &FolderSetupVisual{RequirementsChecked: true, Plugins: []FolderSetupPlugin{
		{Kind: "integration", Name: "One", Operation: "ready"},
		{Kind: "provider", Name: "/private/untrusted", Operation: "ready"},
	}}
	shown := plan.Stamped()
	if shown.Visual.RequirementsChecked || len(shown.Visual.Plugins) != 1 {
		t.Fatal("omitted unsafe row permitted a complete-set claim")
	}
}
