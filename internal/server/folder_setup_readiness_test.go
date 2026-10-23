package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

func TestFolderSetupReadinessPreservesIndependentRequirements(t *testing.T) {
	for _, which := range []string{"integration", "provider", "neither"} {
		t.Run(which, func(t *testing.T) {
			f := newDraftServerFixture(t)
			releases := newHomeReleases(t, "0.4.0")
			if which == "provider" {
				releases.inspectErr[releases.candidates[0].Source] = errors.New("release unavailable")
			}
			f.builder.server.reviewedReleases = releases.updates()
			reads := 0
			f.builder.reviewedIntegrationReader = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
				reads++
				if which == "integration" {
					return setupjourney.CanonicalStepRead{}, errors.New("release unavailable")
				}
				return setupjourney.CanonicalStepRead{Complete: true, Integration: &setupjourney.IntegrationProjection{InstalledVersion: "0.9.0", DevelopmentCopy: true}}, nil
			})
			host := &folderSetupHost{builder: f.builder}
			req := personalassistant.FolderSetupRequest{UserID: "local", Offer: personalassistant.FolderOffer{
				Subject: personalassistant.FolderCandidateRecord{Name: "Session", Shape: "audio", MarkerName: "*.rpp"},
			}}
			visual := host.ReadSetupReadiness(t.Context(), req)
			if visual == nil || len(visual.Plugins) != 2 || visual.Entity != nil || visual.Placement != "" {
				t.Fatalf("partial readiness invented a destination or lost requirements: %+v", visual)
			}
			integration, provider := visual.Plugins[0], visual.Plugins[1]
			if integration.Name != "REAPER" || provider.Name != "Music Project Management" || integration.Purpose == "" || provider.Purpose == "" {
				t.Fatal("registry identity/purpose lost", visual)
			}
			if which == "integration" && (integration.Operation != "unavailable" || provider.Operation != "install") {
				t.Fatal("failed integration hid independent provider", visual)
			}
			if which == "provider" && (provider.Operation != "unavailable" || integration.Operation != "ready") {
				t.Fatal("failed provider hid independent integration", visual)
			}
			if which != "integration" && integration.Source != "Local development copy — not release-verified" {
				t.Fatal("development copy called a release", integration)
			}
			if visual.RequirementsChecked != (which == "neither") {
				t.Fatal("partial reads called complete", visual)
			}
			visual.Plugins[0].Name = "changed"
			if host.ReadSetupReadiness(t.Context(), req).Plugins[0].Name != "REAPER" || reads != 1 {
				t.Fatal("cached readiness aliases a caller or rereads on each poll")
			}
			plugins, err := f.builder.pluginHandler.Manager().List()
			if err != nil || len(plugins) != 0 {
				t.Fatal("readiness installed a plugin", plugins, err)
			}
			req.Offer.ConversationReview = &personalassistant.FolderConversationReview{Operation: personalassistant.FolderOperationSupport}
			if host.ReadSetupReadiness(t.Context(), req) != nil || reads != 1 {
				t.Fatal("supporting link gained plugin requirements")
			}
			req.Offer.ConversationReview = nil
			req.Offer.Subject.Shape, req.Offer.Subject.MarkerName = "code", "go.mod"
			if host.ReadSetupReadiness(t.Context(), req) != nil || reads != 1 {
				t.Fatal("software recipe gained plugin requirements")
			}
		})
	}
}
