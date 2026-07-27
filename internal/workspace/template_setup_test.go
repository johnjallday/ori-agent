package workspace

import (
	"encoding/json"
	"testing"
)

func janitorLikeProvenance() *TemplateProvenance {
	return &TemplateProvenance{
		TemplateID: "downloads-janitor",
		Builtin:    true,
		DirectoryRequirements: []DirectoryRequirement{{
			Key:              "downloads-root",
			Label:            "Downloads folder",
			SuggestedPath:    "~/Downloads",
			AccessDisclosure: "Ori can list files here.",
		}},
		AutomationRecipes: []AutomationRecipe{{
			DirectoryKey: "downloads-root",
			Watch:        &WatchRecipe{Events: []string{"create", "rename"}, DebounceSeconds: 300, ExcludeSubdirectories: []string{"Filed"}},
			DailyScan:    &DailyScanRecipe{LocalTime: "09:00"},
		}},
	}
}

func TestTemplateSetupRequirements_CarriedUnresolvedAndSurviveJSON(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Downloads"})
	ws.SetTemplateProvenance(janitorLikeProvenance())

	reqs := ws.PendingDirectoryRequirements()
	if len(reqs) != 1 || reqs[0].Key != "downloads-root" {
		t.Fatalf("directory requirement not carried: %+v", reqs)
	}
	// The suggested path stays a literal suggestion: recording it must not
	// expand "~" or otherwise select a path on the user's behalf.
	if reqs[0].SuggestedPath != "~/Downloads" {
		t.Fatalf("suggested path was resolved at creation time: %q", reqs[0].SuggestedPath)
	}

	recipe, ok := ws.TemplateAutomationRecipeFor("Downloads-Root")
	if !ok {
		t.Fatal("automation recipe not carried")
	}
	if recipe.Watch == nil || recipe.Watch.DebounceSeconds != 300 || recipe.DailyScan == nil || recipe.DailyScan.LocalTime != "09:00" {
		t.Fatalf("automation recipe not carried intact: %+v", recipe)
	}
	if _, ok := ws.TemplateAutomationRecipeFor("other-root"); ok {
		t.Fatal("unexpected recipe for an undeclared directory key")
	}

	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatal(err)
	}
	var back Workspace
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.PendingDirectoryRequirements()) != 1 {
		t.Fatalf("directory requirements did not survive JSON round-trip: %+v", back.TemplateProvenance)
	}
	restored, ok := back.TemplateAutomationRecipeFor("downloads-root")
	if !ok || restored.Watch == nil || len(restored.Watch.Events) != 2 || restored.DailyScan == nil {
		t.Fatalf("automation recipes did not survive JSON round-trip: %+v", back.TemplateProvenance)
	}
}

func TestTemplateSetupRequirements_AccessorsReturnDeepCopies(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Downloads"})
	ws.SetTemplateProvenance(janitorLikeProvenance())

	got := ws.GetTemplateProvenance()
	got.DirectoryRequirements[0].Key = "mutated"
	got.AutomationRecipes[0].DirectoryKey = "mutated"
	got.AutomationRecipes[0].Watch.DebounceSeconds = 1
	got.AutomationRecipes[0].DailyScan.LocalTime = "23:59"

	fresh := ws.GetTemplateProvenance()
	if fresh.DirectoryRequirements[0].Key != "downloads-root" || fresh.AutomationRecipes[0].DirectoryKey != "downloads-root" {
		t.Fatalf("provenance slices are shared with callers: %+v", fresh)
	}
	if fresh.AutomationRecipes[0].Watch.DebounceSeconds != 300 || fresh.AutomationRecipes[0].DailyScan.LocalTime != "09:00" {
		t.Fatalf("nested recipe blocks are shared with callers: %+v", fresh.AutomationRecipes[0])
	}
}

func TestTemplateSetupRequirements_SetCopiesCallerInput(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Downloads"})
	prov := janitorLikeProvenance()
	ws.SetTemplateProvenance(prov)

	prov.DirectoryRequirements[0].Label = "mutated"
	prov.AutomationRecipes[0].Watch.Events[0] = "mutated"

	stored := ws.GetTemplateProvenance()
	if stored.DirectoryRequirements[0].Label != "Downloads folder" || stored.AutomationRecipes[0].Watch.Events[0] != "create" {
		t.Fatalf("SetTemplateProvenance kept the caller's slices: %+v", stored)
	}
}

func TestTemplateSetupRequirements_AbsentForWorkspacesWithoutThem(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Plain"})
	if ws.PendingDirectoryRequirements() != nil {
		t.Fatal("a workspace with no provenance must report no directory requirements")
	}
	ws.SetTemplateProvenance(&TemplateProvenance{TemplateID: "writing-project", Builtin: true})
	if ws.PendingDirectoryRequirements() != nil {
		t.Fatal("a template that declares no directory requirements must carry none")
	}
	if _, ok := ws.TemplateAutomationRecipeFor("downloads-root"); ok {
		t.Fatal("a template that declares no recipes must carry none")
	}
}
