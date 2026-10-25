package server

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestFolderPrerequisiteProviderReviewIsIndependentAndStaleConsentCannotInstall(t *testing.T) {
	f := newDraftServerFixture(t)
	releases := newHomeReleases(t, "0.4.0")
	f.builder.server.reviewedReleases = releases.updates()
	host := &folderSetupHost{builder: f.builder}
	request := personalassistant.FolderSetupRequest{UserID: "local", Offer: personalassistant.FolderOffer{Subject: personalassistant.FolderCandidateRecord{Name: "Session", Shape: "audio", MarkerName: "*.rpp"}}}
	input := personalassistant.FolderPrerequisiteInput{Kind: personalassistant.FolderPlanProvider, RequestID: "read-provider"}
	review, err := host.Prerequisite(t.Context(), request, input)
	if err != nil || review.Name != "Music Project Management" || review.Version != "0.4.0" || review.Operation != "install" || review.Source == "" || review.Disclosure == nil || len(review.Digest) != 64 || !review.EnabledAfter {
		t.Fatalf("independent provider disclosure: %+v %v", review, err)
	}
	for _, which := range []string{"no_disclosure", "wrong_version", "wrong_role_envelope", "changed_release"} {
		t.Run(which, func(t *testing.T) {
			confirm := personalassistant.FolderPrerequisiteInput{Kind: input.Kind, RequestID: "confirm-provider", Confirm: true, Digest: review.Digest, ReviewedVersion: review.Version}
			switch which {
			case "no_disclosure":
				confirm.Digest = ""
			case "wrong_version":
				confirm.ReviewedVersion = "0.0.1"
			case "wrong_role_envelope":
				confirm.RunID = "unrelated-integration-run"
			case "changed_release":
				f.builder.server.reviewedReleases = newHomeReleases(t, "0.4.1").updates()
			}
			if _, err := host.Prerequisite(t.Context(), request, confirm); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
				t.Fatal("stale/mismatched consent was not refused", err)
			}
			plugins, err := f.builder.pluginHandler.Manager().List()
			if err != nil || len(plugins) != 0 {
				t.Fatal("review/refusal installed a plugin", plugins, err)
			}
		})
	}
}

func TestFolderPrerequisite_LibraryProviderUsesTheSameExactPackageConsent(t *testing.T) {
	f := newDraftServerFixture(t)
	f.builder.server.reviewedReleases = newHomeReleases(t, "0.4.0").updates()
	host := &folderSetupHost{builder: f.builder}
	req := personalassistant.FolderSetupRequest{UserID: "local", Offer: personalassistant.FolderOffer{
		Portfolio: &personalassistant.FolderPortfolioEvidence{Shape: "audio", Projects: 6, ProviderKey: "music_project_management", IntegrationKey: "ori_reaper", IntegrationProjects: 5},
		Structure: &personalassistant.FolderStructure{Group: personalassistant.FolderStructureGroup{Type: personalassistant.FolderGroupLibrary}},
	}}
	input := personalassistant.FolderPrerequisiteInput{Kind: personalassistant.FolderPlanProvider, RequestID: "library-preview"}
	review, err := host.Prerequisite(t.Context(), req, input)
	if err != nil || review.Operation != "install" || review.Version != "0.4.0" || len(review.Digest) != 64 || review.Disclosure == nil {
		t.Fatalf("library disclosure: %+v %v", review, err)
	}
	input.Confirm, input.Digest, input.ReviewedVersion = true, "wrong", review.Version
	if _, err := host.Prerequisite(t.Context(), req, input); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatal("unreviewed package installed", err)
	}
	req.Offer.Structure.Group.Type = personalassistant.FolderGroupPlain
	if _, err := host.Prerequisite(t.Context(), req, input); err == nil {
		t.Fatal("plain group acquired a package requirement")
	}
	plugins, err := f.builder.pluginHandler.Manager().List()
	if err != nil || len(plugins) != 0 {
		t.Fatal("preview or refusal installed packages", err)
	}
}
