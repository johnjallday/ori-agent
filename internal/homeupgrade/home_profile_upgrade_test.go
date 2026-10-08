package homeupgrade

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

func studioProfile() *projecttemplates.HomeProfileDeclaration {
	return &projecttemplates.HomeProfileDeclaration{
		SchemaVersion: 1, Title: "Your studio", Intro: "What Ori knows about where you make music.",
		Fields: []projecttemplates.HomeProfileField{
			{ID: "apps", Kind: projecttemplates.HomeProfileKindApps, Label: "DAWs on this Mac"},
			{ID: "main_app", Kind: projecttemplates.HomeProfileKindMainApp, Label: "Main DAW"},
			{ID: "templates", Kind: projecttemplates.HomeProfileKindTemplates, Label: "Project templates"},
			{ID: "defaults", Kind: projecttemplates.HomeProfileKindDefaults, Label: "New-song defaults"},
		},
	}
}

// addProfileToNext turns the harness's newer release into one that adds the
// profile card and starts requiring the host feature that gates it.
func addProfileToNext(h *harness) {
	next := h.plugins.next.WorkspaceSurfaces
	next.AssistantProgramHomes[0].HomeProfile = studioProfile()
	next.RequiresHostFeatures = append(next.RequiresHostFeatures, plugin.HostFeatureHomeProfileV1)
}

// PRD FR17/FR18: a release that adds a profile card (and nothing else besides
// prompts) can be taken by an existing Home, and leaves it with an empty
// profile: nothing is detected or read by the upgrade.
func TestUpgradeAcceptsAReleaseThatAddsAHomeProfile(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	addProfileToNext(h)

	review, err := h.service.Review(ctx, "local", testPlugin)
	if err != nil {
		t.Fatalf("review refused an additive profile release: %v", err)
	}
	program := review.Plan.Programs[0]
	if !program.AddsHomeProfile || program.HomeProfileTitle != "Your studio" || len(program.RolePrompts) != 2 {
		t.Fatalf("plan program = %+v", program)
	}
	if h.homeState(t).HomeProfile != nil || h.plugins.replaced != 0 {
		t.Fatal("review changed something")
	}

	operation, err := h.service.Commit(ctx, "local", testPlugin, review.Token)
	if err != nil || operation.Status != StatusSucceeded {
		t.Fatalf("commit = %#v, %v", operation, err)
	}
	state := h.homeState(t)
	// The Home is pinned to the release that declares the card...
	if *state.HomeProvider != h.to() || state.Declaration.Roles[0].SystemPrompt != newPortfolio {
		t.Fatalf("Home after commit = %#v", state.HomeProvider)
	}
	installed := h.plugins.installed.WorkspaceSurfaces.AssistantProgramHomes[0]
	if installed.HomeProfile == nil || state.HomeProvider.DeclarationDigest != projecttemplates.AssistantProgramHomeDigest(installed) {
		t.Fatal("the Home's pin does not name the installed declaration with the profile")
	}
	// ...and nothing was detected, read or recorded for it.
	if state.HomeProfile != nil {
		t.Fatalf("the upgrade wrote a profile: %+v", state.HomeProfile)
	}
	if link, snapshot := h.childPins(t); link != h.to() || snapshot != h.to() {
		t.Fatalf("child pins = %#v / %#v", link, snapshot)
	}
}

// A profile card with no change to guidance at all is still an upgrade.
func TestUpgradeAcceptsAnAddedProfileWithoutPromptChanges(t *testing.T) {
	h := newHarness(t)
	next := h.plugins.next.WorkspaceSurfaces
	next.AssistantProgramHomes[0] = projecttemplates.CloneAssistantProgramHome(h.plugins.installed.WorkspaceSurfaces.AssistantProgramHomes[0])
	addProfileToNext(h)
	review, err := h.service.Review(context.Background(), "local", testPlugin)
	if err != nil || !review.Plan.Programs[0].AddsHomeProfile || len(review.Plan.Programs[0].RolePrompts) != 0 || len(review.Plan.Homes[0].Agents) != 0 {
		t.Fatalf("review = %+v err = %v", review.Plan.Programs, err)
	}
	if operation, err := h.service.Commit(context.Background(), "local", testPlugin, review.Token); err != nil || operation.Status != StatusSucceeded {
		t.Fatalf("commit = %#v, %v", operation, err)
	}
}

func TestUpgradeStillRefusesWhatIsNotAnAddedProfile(t *testing.T) {
	tests := map[string]func(h *harness){
		// The gating feature may arrive only together with the card it gates.
		"the feature without a profile": func(h *harness) {
			next := h.plugins.next.WorkspaceSurfaces
			next.RequiresHostFeatures = append(next.RequiresHostFeatures, plugin.HostFeatureHomeProfileV1)
		},
		"a profile and another feature": func(h *harness) {
			addProfileToNext(h)
			next := h.plugins.next.WorkspaceSurfaces
			next.RequiresHostFeatures = append(next.RequiresHostFeatures, plugin.HostFeatureBlueprintInputsV1)
		},
		"a profile and a facts operation": func(h *harness) {
			addProfileToNext(h)
			h.plugins.next.WorkspaceSurfaces.HomeProfileFacts = &plugin.HomeProfileFactsRef{ServiceID: "service", Operation: "profile.read"}
		},
		"a profile and a renamed Home": func(h *harness) {
			addProfileToNext(h)
			h.plugins.next.WorkspaceSurfaces.AssistantProgramHomes[0].StationName = "Studio Home"
		},
		"a profile and a wider attachment": func(h *harness) {
			addProfileToNext(h)
			h.plugins.next.WorkspaceSurfaces.AssistantProgramHomes[0].AllowedProjectAttachments[0].MaxProjectTeamVersion = 2
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			mutate(h)
			if _, err := h.service.Review(context.Background(), "local", testPlugin); !errors.Is(err, ErrNotGuidanceOnly) {
				t.Fatalf("review err = %v, want not guidance only", err)
			}
			if h.plugins.replaced != 0 || h.homeState(t).HomeProvider.PluginVersion != "0.1.0" {
				t.Fatal("a refused upgrade changed something")
			}
		})
	}
}

// Between two releases that both declare the card, the card must not change.
func TestUpgradeRefusesAChangedProfileSection(t *testing.T) {
	h := newHarness(t)
	h.plugins.installed.WorkspaceSurfaces.AssistantProgramHomes[0].HomeProfile = studioProfile()
	h.plugins.installed.WorkspaceSurfaces.RequiresHostFeatures = append(h.plugins.installed.WorkspaceSurfaces.RequiresHostFeatures, plugin.HostFeatureHomeProfileV1)
	addProfileToNext(h)
	h.plugins.next.WorkspaceSurfaces.AssistantProgramHomes[0].HomeProfile.Title = "Studio"
	if _, err := h.service.Review(context.Background(), "local", testPlugin); !errors.Is(err, ErrNotGuidanceOnly) && !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("review err = %v, want the changed card refused", err)
	}
}
