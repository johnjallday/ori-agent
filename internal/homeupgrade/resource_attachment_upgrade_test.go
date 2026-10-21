package homeupgrade

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

func studioResourceAttachment() projecttemplates.AssistantProgramAllowedProjectAttachment {
	return projecttemplates.AssistantProgramAllowedProjectAttachment{
		ProviderPluginID: "reaper-plugin", BlueprintID: "reaper-studio", ProjectTeamID: "reaper-studio-team",
		ProjectTeamSchemaVersion: 1, MinProjectTeamVersion: 1, MaxProjectTeamVersion: 1,
		WorkspaceKind: "resource", Label: "REAPER Studio",
		Description: "One place for the templates, scripts and audio plugins your REAPER setup already has.",
	}
}

// addResourceAttachmentToNext turns the harness's newer release into one that
// appends the Studio attachment and starts requiring its host feature.
func addResourceAttachmentToNext(h *harness) {
	next := h.plugins.next.WorkspaceSurfaces
	home := &next.AssistantProgramHomes[0]
	home.AllowedProjectAttachments = append(slices.Clone(home.AllowedProjectAttachments), studioResourceAttachment())
	next.RequiresHostFeatures = append(slices.Clone(next.RequiresHostFeatures), plugin.HostFeatureResourceWorkspacesV1)
}

// PRD FR31 / AC9: an existing Home can take a release that only appends the
// Studio attachment (with or without guidance). The upgrade creates, staffs
// and reads nothing; the Home's songs and pins move exactly as before.
func TestUpgradeAcceptsAReleaseThatAddsAResourceAttachment(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	addResourceAttachmentToNext(h)
	before := h.homeState(t)

	review, err := h.service.Review(ctx, "local", testPlugin)
	if err != nil {
		t.Fatalf("review refused an additive resource attachment: %v", err)
	}
	program := review.Plan.Programs[0]
	if len(program.AddsResourceAttachments) != 1 || program.AddsResourceAttachments[0].Label != "REAPER Studio" ||
		program.AddsResourceAttachments[0].BlueprintID != "reaper-studio" || program.AddsHomeProfile {
		t.Fatalf("plan program = %+v", program)
	}
	if h.plugins.replaced != 0 {
		t.Fatal("review replaced the package")
	}

	operation, err := h.service.Commit(ctx, "local", testPlugin, review.Token)
	if err != nil || operation.Status != StatusSucceeded {
		t.Fatalf("commit = %#v, %v", operation, err)
	}
	state := h.homeState(t)
	installed := h.plugins.installed.WorkspaceSurfaces.AssistantProgramHomes[0]
	if *state.HomeProvider != h.to() || state.HomeProvider.DeclarationDigest != projecttemplates.AssistantProgramHomeDigest(installed) {
		t.Fatalf("Home pin after commit = %#v", state.HomeProvider)
	}
	if _, ok := installed.ResourceAttachment(); !ok {
		t.Fatal("the installed declaration lost its resource attachment")
	}
	// Nothing new became a child of the Home.
	if !slices.Equal(state.LinkedProjectIDs, before.LinkedProjectIDs) {
		t.Fatalf("linked projects changed: %v -> %v", before.LinkedProjectIDs, state.LinkedProjectIDs)
	}
	if link, snapshot := h.childPins(t); link != h.to() || snapshot != h.to() {
		t.Fatalf("child pins = %#v / %#v", link, snapshot)
	}
}

func TestUpgradeStillRefusesWhatIsNotAnAddedResourceAttachment(t *testing.T) {
	tests := map[string]func(h *harness){
		// The gating feature may arrive only together with the entry it gates.
		"the feature without an attachment": func(h *harness) {
			next := h.plugins.next.WorkspaceSurfaces
			next.RequiresHostFeatures = append(next.RequiresHostFeatures, plugin.HostFeatureResourceWorkspacesV1)
		},
		"an appended project attachment": func(h *harness) {
			home := &h.plugins.next.WorkspaceSurfaces.AssistantProgramHomes[0]
			project := studioResourceAttachment()
			project.WorkspaceKind, project.Label, project.Description = "", "", ""
			home.AllowedProjectAttachments = append(slices.Clone(home.AllowedProjectAttachments), project)
		},
		"a resource attachment inserted before an existing one": func(h *harness) {
			addResourceAttachmentToNext(h)
			home := &h.plugins.next.WorkspaceSurfaces.AssistantProgramHomes[0]
			slices.Reverse(home.AllowedProjectAttachments)
		},
		"a resource attachment and a wider existing one": func(h *harness) {
			addResourceAttachmentToNext(h)
			h.plugins.next.WorkspaceSurfaces.AssistantProgramHomes[0].AllowedProjectAttachments[0].MaxProjectTeamVersion = 2
		},
		"a resource attachment and another feature": func(h *harness) {
			addResourceAttachmentToNext(h)
			next := h.plugins.next.WorkspaceSurfaces
			next.RequiresHostFeatures = append(next.RequiresHostFeatures, plugin.HostFeatureBlueprintInputsV1)
		},
		"a removed attachment": func(h *harness) {
			h.plugins.next.WorkspaceSurfaces.AssistantProgramHomes[0].AllowedProjectAttachments = nil
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			mutate(h)
			if _, err := h.service.Review(context.Background(), "local", testPlugin); !errors.Is(err, ErrNotGuidanceOnly) {
				t.Fatalf("review err = %v, want not guidance only", err)
			}
			if h.plugins.replaced != 0 {
				t.Fatal("a refused review replaced the package")
			}
		})
	}
}
