package homeupgrade

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

func songCardWords() *projecttemplates.ProjectCardDeclaration {
	return &projecttemplates.ProjectCardDeclaration{SchemaVersion: 1, Noun: "Song", NounPlural: "Songs",
		RosterTitle: "Songs and assistants",
		Facts: []projecttemplates.ProjectCardFact{{Kind: "track_count", Unit: "tracks", UnitOne: "track"},
			{Kind: "tempo", Unit: "BPM"}, {Kind: "length"}},
		Fields: []projecttemplates.ProjectCardField{{ID: "key", Label: "Key", Kind: "text"}}}
}

// addProjectCardToNext turns the newer release into one that adds the words
// section and starts requiring the host feature that gates it.
func addProjectCardToNext(h *harness) {
	next := h.plugins.next.WorkspaceSurfaces
	next.AssistantProgramHomes[0].ProjectCard = songCardWords()
	next.RequiresHostFeatures = append(next.RequiresHostFeatures, plugin.HostFeatureProjectCardWordsV1)
}

// PRD #792 FR10: a release that adds the words section is an upgrade an
// existing Home takes; the commit writes the words snapshot with the pin.
func TestUpgradeAcceptsAReleaseThatAddsProjectCardWords(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	addProjectCardToNext(h)
	review, err := h.service.Review(ctx, "local", testPlugin)
	if err != nil {
		t.Fatalf("review refused an additive words release: %v", err)
	}
	program := review.Plan.Programs[0]
	if !program.AddsProjectCard || program.ProjectCardNoun != "Song" || program.ProjectCardNounPlural != "Songs" ||
		program.ProjectCardWords == nil || program.ProjectCardWords.Version != h.plugins.next.Version ||
		len(program.ProjectCardWords.Fields) != 1 || len(program.RolePrompts) != 2 {
		t.Fatalf("plan program = %+v", program)
	}
	if h.homeState(t).ProjectCardWords != nil {
		t.Fatal("review wrote the snapshot")
	}
	operation, err := h.service.Commit(ctx, "local", testPlugin, review.Token)
	if err != nil || operation.Status != StatusSucceeded {
		t.Fatalf("commit = %#v, %v", operation, err)
	}
	state := h.homeState(t)
	if *state.HomeProvider != h.to() || state.ProjectCardWords == nil || !state.ProjectCardWords.Valid() ||
		state.ProjectCardWords.Noun != "Song" || state.ProjectCardWords.PluginID != testPlugin {
		t.Fatalf("Home after commit: pin %#v words %#v", state.HomeProvider, state.ProjectCardWords)
	}
	installed := h.plugins.installed.WorkspaceSurfaces.AssistantProgramHomes[0]
	if installed.ProjectCard == nil || state.HomeProvider.DeclarationDigest != projecttemplates.AssistantProgramHomeDigest(installed) {
		t.Fatal("the Home's pin does not name the installed declaration with the words")
	}
}

// The words alone, with guidance, with a profile card and with a resource
// attachment are all accepted together.
func TestUpgradeAcceptsAddedWordsAloneAndWithOtherAdditions(t *testing.T) {
	for name, mutate := range map[string]func(h *harness){
		"alone": func(h *harness) {
			next := h.plugins.next.WorkspaceSurfaces
			next.AssistantProgramHomes[0] = projecttemplates.CloneAssistantProgramHome(h.plugins.installed.WorkspaceSurfaces.AssistantProgramHomes[0])
			addProjectCardToNext(h)
		},
		"with guidance":   addProjectCardToNext,
		"with a profile":  func(h *harness) { addProjectCardToNext(h); addProfileToNext(h) },
		"with a resource": func(h *harness) { addProjectCardToNext(h); addResourceAttachmentToNext(h) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			mutate(h)
			review, err := h.service.Review(context.Background(), "local", testPlugin)
			if err != nil || !review.Plan.Programs[0].AddsProjectCard {
				t.Fatalf("review = %+v err = %v", review.Plan.Programs, err)
			}
			if operation, err := h.service.Commit(context.Background(), "local", testPlugin, review.Token); err != nil || operation.Status != StatusSucceeded {
				t.Fatalf("commit = %#v, %v", operation, err)
			}
		})
	}
}

func TestUpgradeRefusesWhatIsNotAnAddedWordsSection(t *testing.T) {
	for name, mutate := range map[string]func(h *harness){
		// The gating feature may arrive only with the section it gates.
		"the feature without the section": func(h *harness) {
			next := h.plugins.next.WorkspaceSurfaces
			next.RequiresHostFeatures = append(next.RequiresHostFeatures, plugin.HostFeatureProjectCardWordsV1)
		},
		"words and a renamed Home": func(h *harness) {
			addProjectCardToNext(h)
			h.plugins.next.WorkspaceSurfaces.AssistantProgramHomes[0].StationName = "Studio Home"
		},
		"words and another feature": func(h *harness) {
			addProjectCardToNext(h)
			next := h.plugins.next.WorkspaceSurfaces
			next.RequiresHostFeatures = append(next.RequiresHostFeatures, plugin.HostFeatureBlueprintInputsV1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			mutate(h)
			if _, err := h.service.Review(context.Background(), "local", testPlugin); !errors.Is(err, ErrNotGuidanceOnly) {
				t.Fatalf("review err = %v, want not guidance only", err)
			}
		})
	}
}

// Between two releases that both declare words, a changed or removed section
// is refused.
func TestUpgradeRefusesAChangedOrRemovedWordsSection(t *testing.T) {
	for name, mutate := range map[string]func(next *projecttemplates.AssistantProgramHome){
		"changed noun":  func(next *projecttemplates.AssistantProgramHome) { next.ProjectCard.Noun = "Track" },
		"removed field": func(next *projecttemplates.AssistantProgramHome) { next.ProjectCard.Fields = nil },
		"section gone":  func(next *projecttemplates.AssistantProgramHome) { next.ProjectCard = nil },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			installed := h.plugins.installed.WorkspaceSurfaces
			installed.AssistantProgramHomes[0].ProjectCard = songCardWords()
			installed.RequiresHostFeatures = append(installed.RequiresHostFeatures, plugin.HostFeatureProjectCardWordsV1)
			addProjectCardToNext(h)
			mutate(&h.plugins.next.WorkspaceSurfaces.AssistantProgramHomes[0])
			if _, err := h.service.Review(context.Background(), "local", testPlugin); !errors.Is(err, ErrNotGuidanceOnly) && !errors.Is(err, ErrPinMismatch) {
				t.Fatalf("review err = %v, want refused", err)
			}
		})
	}
}

func TestAcceptedHomeChangeRecordsAddedWords(t *testing.T) {
	current := projecttemplates.AssistantProgramHome{ID: "home", Roles: []projecttemplates.AssistantProgramHomeRole{{ID: "r", SystemPrompt: "a"}}}
	next := projecttemplates.CloneAssistantProgramHome(current)
	next.ProjectCard = songCardWords()
	change, err := projecttemplates.AcceptedHomeChange(current, next)
	if err != nil || !change.AddsProjectCard || change.ProjectCardNoun != "Song" || change.ProjectCardNounPlural != "Songs" {
		t.Fatalf("change = %+v %v", change, err)
	}
	if next.ProjectCard == nil {
		t.Fatal("AcceptedHomeChange mutated its argument")
	}
}
