package projecttemplates

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNewTemplate_LoadsAndNormalizesDirectoryRequirementsAndRecipes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "demo", ManifestFileName), `{
		"name": "Demo",
		"directory_requirements": [
			{"key": " Downloads-Root ", "label": " Downloads folder ", "suggested_path": " ~/Downloads ", "access_disclosure": " Ori can list files here. "},
			{"key": "downloads-root", "label": "Duplicate"}
		],
		"automation_recipes": [
			{
				"directory_key": " Downloads-Root ",
				"watch": {"events": [" Create ", "rename", "rename"], "debounce_seconds": 300, "exclude_subdirectories": [" Filed "]},
				"daily_scan": {"local_time": "9:5", "timezone": "America/New_York"}
			},
			{"directory_key": "unknown-root", "watch": {"events": ["create"]}}
		]
	}`)

	tpl, err := FindLibraryTemplate(dir, "demo")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}

	if len(tpl.DirectoryRequirements) != 1 {
		t.Fatalf("expected duplicate key merged away, got %d: %+v", len(tpl.DirectoryRequirements), tpl.DirectoryRequirements)
	}
	req := tpl.DirectoryRequirements[0]
	if req.Key != "downloads-root" || req.Label != "Downloads folder" || req.SuggestedPath != "~/Downloads" || req.AccessDisclosure != "Ori can list files here." {
		t.Fatalf("directory requirement not normalized: %+v", req)
	}

	if len(tpl.AutomationRecipes) != 1 {
		t.Fatalf("expected recipe for an undeclared directory dropped, got %d: %+v", len(tpl.AutomationRecipes), tpl.AutomationRecipes)
	}
	recipe := tpl.AutomationRecipes[0]
	if recipe.DirectoryKey != "downloads-root" {
		t.Fatalf("expected normalized directory key, got %q", recipe.DirectoryKey)
	}
	if recipe.Watch == nil || len(recipe.Watch.Events) != 2 || recipe.Watch.Events[0] != "create" || recipe.Watch.Events[1] != "rename" {
		t.Fatalf("expected normalized+deduped watch events, got %+v", recipe.Watch)
	}
	if recipe.Watch.DebounceSeconds != 300 || len(recipe.Watch.ExcludeSubdirectories) != 1 || recipe.Watch.ExcludeSubdirectories[0] != "Filed" {
		t.Fatalf("watch recipe not normalized: %+v", recipe.Watch)
	}
	if recipe.DailyScan == nil || recipe.DailyScan.LocalTime != "09:05" || recipe.DailyScan.Timezone != "America/New_York" {
		t.Fatalf("daily scan not normalized: %+v", recipe.DailyScan)
	}

	if found, ok := tpl.DirectoryRequirement(" Downloads-Root "); !ok || found.Key != "downloads-root" {
		t.Fatalf("DirectoryRequirement lookup failed: %+v %v", found, ok)
	}
	if found, ok := tpl.AutomationRecipeFor("DOWNLOADS-ROOT"); !ok || found.DirectoryKey != "downloads-root" {
		t.Fatalf("AutomationRecipeFor lookup failed: %+v %v", found, ok)
	}
}

func TestNewTemplate_ManifestWithoutSetupRequirementsIsUnaffected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "legacy", ManifestFileName), `{"name":"Legacy"}`)

	tpl, err := FindLibraryTemplate(dir, "legacy")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if tpl.DirectoryRequirements != nil || tpl.AutomationRecipes != nil {
		t.Fatalf("expected nil setup requirements for a manifest that predates the fields, got %+v / %+v", tpl.DirectoryRequirements, tpl.AutomationRecipes)
	}
}

func TestNewTemplate_DropsUnusableRecipeValuesRatherThanFailing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "demo", ManifestFileName), `{
		"name": "Demo",
		"directory_requirements": [{"key": "root", "label": "Root"}],
		"automation_recipes": [
			{"directory_key": "root", "watch": {"events": ["nope"]}, "daily_scan": {"local_time": "09:00"}}
		]
	}`)

	tpl, err := FindLibraryTemplate(dir, "demo")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if len(tpl.AutomationRecipes) != 1 {
		t.Fatalf("expected the recipe kept for its valid daily scan, got %+v", tpl.AutomationRecipes)
	}
	if tpl.AutomationRecipes[0].Watch != nil {
		t.Fatalf("expected the invalid watch block dropped, got %+v", tpl.AutomationRecipes[0].Watch)
	}
	if tpl.AutomationRecipes[0].DailyScan == nil {
		t.Fatal("expected the valid daily scan preserved")
	}
}

func TestUpdateManifest_SetupRequirementsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateBlank(dir, "Demo"); err != nil {
		t.Fatalf("CreateBlank: %v", err)
	}

	reqs := []DirectoryRequirement{{
		Key:              "downloads-root",
		Label:            "Downloads folder",
		SuggestedPath:    "~/Downloads",
		AccessDisclosure: "Ori can list files here.",
	}}
	recipes := []AutomationRecipe{{
		DirectoryKey: "downloads-root",
		Watch:        &WatchRecipe{Events: []string{"create", "rename"}, DebounceSeconds: 300, ExcludeSubdirectories: []string{"Filed"}},
		DailyScan:    &DailyScanRecipe{LocalTime: "09:00"},
	}}

	tpl, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{
		DirectoryRequirements: &reqs,
		AutomationRecipes:     &recipes,
	})
	if err != nil {
		t.Fatalf("UpdateManifest: %v", err)
	}
	if len(tpl.DirectoryRequirements) != 1 || tpl.DirectoryRequirements[0].Label != "Downloads folder" {
		t.Fatalf("directory requirements not saved: %+v", tpl.DirectoryRequirements)
	}
	if len(tpl.AutomationRecipes) != 1 || tpl.AutomationRecipes[0].DailyScan.LocalTime != "09:00" {
		t.Fatalf("automation recipes not saved: %+v", tpl.AutomationRecipes)
	}

	// Reload from disk to prove the manifest round-trips.
	reloaded, err := FindLibraryTemplate(dir, "demo")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if len(reloaded.DirectoryRequirements) != 1 || len(reloaded.AutomationRecipes) != 1 {
		t.Fatalf("setup requirements did not survive a reload: %+v / %+v", reloaded.DirectoryRequirements, reloaded.AutomationRecipes)
	}
	if reloaded.AutomationRecipes[0].Watch == nil || reloaded.AutomationRecipes[0].Watch.DebounceSeconds != 300 {
		t.Fatalf("watch recipe did not survive a reload: %+v", reloaded.AutomationRecipes[0].Watch)
	}

	// An unrelated edit must preserve both fields.
	icon := "🧹"
	preserved, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{Icon: &icon})
	if err != nil {
		t.Fatalf("UpdateManifest (icon): %v", err)
	}
	if len(preserved.DirectoryRequirements) != 1 || len(preserved.AutomationRecipes) != 1 {
		t.Fatalf("an unrelated edit dropped setup requirements: %+v / %+v", preserved.DirectoryRequirements, preserved.AutomationRecipes)
	}

	// Explicit empty slices clear the keys.
	noReqs := []DirectoryRequirement{}
	noRecipes := []AutomationRecipe{}
	cleared, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{
		DirectoryRequirements: &noReqs,
		AutomationRecipes:     &noRecipes,
	})
	if err != nil {
		t.Fatalf("UpdateManifest (clear): %v", err)
	}
	if cleared.DirectoryRequirements != nil || cleared.AutomationRecipes != nil {
		t.Fatalf("expected cleared setup requirements, got %+v / %+v", cleared.DirectoryRequirements, cleared.AutomationRecipes)
	}
}

// writeSetupRequirementTemplate seeds a library template declaring one
// directory requirement and one matching automation recipe.
func writeSetupRequirementTemplate(t *testing.T, libDir, id string) {
	t.Helper()
	writeFile(t, filepath.Join(libDir, id, ManifestFileName), `{
		"name": "Demo",
		"directory_requirements": [
			{"key": "downloads-root", "label": "Downloads folder", "suggested_path": "~/Downloads", "access_disclosure": "Ori can list files here."}
		],
		"automation_recipes": [
			{
				"directory_key": "downloads-root",
				"watch": {"events": ["create", "rename"], "debounce_seconds": 300, "exclude_subdirectories": ["Filed"]},
				"daily_scan": {"local_time": "09:00"}
			}
		]
	}`)
}

func assertSetupRequirementsIntact(t *testing.T, tpl Template, context string) {
	t.Helper()
	if len(tpl.DirectoryRequirements) != 1 || tpl.DirectoryRequirements[0].Key != "downloads-root" || tpl.DirectoryRequirements[0].SuggestedPath != "~/Downloads" {
		t.Fatalf("%s: directory requirements not preserved: %+v", context, tpl.DirectoryRequirements)
	}
	if len(tpl.AutomationRecipes) != 1 {
		t.Fatalf("%s: automation recipes not preserved: %+v", context, tpl.AutomationRecipes)
	}
	recipe := tpl.AutomationRecipes[0]
	if recipe.Watch == nil || recipe.Watch.DebounceSeconds != 300 || len(recipe.Watch.Events) != 2 {
		t.Fatalf("%s: watch recipe not preserved: %+v", context, recipe.Watch)
	}
	if recipe.DailyScan == nil || recipe.DailyScan.LocalTime != "09:00" {
		t.Fatalf("%s: daily scan not preserved: %+v", context, recipe.DailyScan)
	}
}

func TestSetupRequirementsSurviveListAndDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeSetupRequirementTemplate(t, dir, "demo")

	templates, err := ListLibrary(dir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	assertSetupRequirementsIntact(t, templates[0], "ListLibrary")

	dup, err := Duplicate(dir, "demo", "Demo Copy")
	if err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	assertSetupRequirementsIntact(t, dup, "Duplicate")

	reloaded, err := FindLibraryTemplate(dir, dup.ID)
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	assertSetupRequirementsIntact(t, reloaded, "Duplicate (reloaded)")
}

func TestUpdateManifest_UnrelatedEditsPreserveSetupRequirements(t *testing.T) {
	dir := t.TempDir()
	writeSetupRequirementTemplate(t, dir, "demo")

	tasks := []StarterTask{{Description: "Set up the folder", Setup: true}}
	tpl, err := UpdateManifest(dir, "demo", "Demo", "Renamed description", nil, &ManifestEdit{StarterTasks: &tasks})
	if err != nil {
		t.Fatalf("UpdateManifest: %v", err)
	}
	assertSetupRequirementsIntact(t, tpl, "unrelated edit")
}

func TestEnsureLibrary_BuiltinRefreshDeliversSetupRequirements(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	// Simulate an install whose on-disk built-in predates the setup-requirement
	// fields: the manifest refresh must deliver whatever the embedded manifest
	// declares, including fields added after that copy was written.
	var builtinID string
	templates, err := ListLibrary(dir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	for _, tpl := range templates {
		if tpl.Builtin && tpl.BuiltinVersion >= 1 {
			builtinID = tpl.ID
			break
		}
	}
	if builtinID == "" {
		t.Skip("no versioned built-in starter available")
	}

	manifestPath := filepath.Join(dir, builtinID, ManifestFileName)
	stale := `{"name":"Stale","builtin":true,"builtin_version":0,"directory_requirements":[{"key":"legacy","label":"Legacy"}]}`
	if err := os.WriteFile(manifestPath, []byte(stale), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary (refresh): %v", err)
	}

	refreshed, err := FindLibraryTemplate(dir, builtinID)
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if _, ok := refreshed.DirectoryRequirement("legacy"); ok {
		t.Fatal("expected the stale manifest's directory requirements replaced by the shipped manifest")
	}
}

func TestUpdateManifest_RejectsInvalidDirectoryRequirements(t *testing.T) {
	cases := map[string][]DirectoryRequirement{
		"blank key":     {{Key: "  ", Label: "Root"}},
		"blank label":   {{Key: "root", Label: "  "}},
		"duplicate key": {{Key: "root", Label: "Root"}, {Key: " ROOT ", Label: "Other"}},
		"escaping path": {{Key: "root", Label: "Root", SuggestedPath: "~/Downloads/../../etc"}},
	}
	for name, reqs := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := CreateBlank(dir, "Demo"); err != nil {
				t.Fatalf("CreateBlank: %v", err)
			}
			_, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{DirectoryRequirements: &reqs})
			if !errors.Is(err, ErrInvalidDirectoryRequirements) {
				t.Fatalf("expected ErrInvalidDirectoryRequirements, got %v", err)
			}
		})
	}
}

func TestUpdateManifest_RejectsInvalidAutomationRecipes(t *testing.T) {
	valid := []DirectoryRequirement{{Key: "root", Label: "Root"}}
	cases := map[string][]AutomationRecipe{
		"blank directory key":      {{DirectoryKey: "  ", Watch: &WatchRecipe{}}},
		"undeclared directory key": {{DirectoryKey: "elsewhere", Watch: &WatchRecipe{}}},
		"duplicate directory key":  {{DirectoryKey: "root", Watch: &WatchRecipe{}}, {DirectoryKey: "root", DailyScan: &DailyScanRecipe{LocalTime: "09:00"}}},
		"no automation":            {{DirectoryKey: "root"}},
		"unknown event":            {{DirectoryKey: "root", Watch: &WatchRecipe{Events: []string{"explode"}}}},
		"blank event":              {{DirectoryKey: "root", Watch: &WatchRecipe{Events: []string{" "}}}},
		"negative debounce":        {{DirectoryKey: "root", Watch: &WatchRecipe{DebounceSeconds: -1}}},
		"excessive debounce":       {{DirectoryKey: "root", Watch: &WatchRecipe{DebounceSeconds: 3601}}},
		"nested exclude":           {{DirectoryKey: "root", Watch: &WatchRecipe{ExcludeSubdirectories: []string{"Filed/Documents"}}}},
		"missing local time":       {{DirectoryKey: "root", DailyScan: &DailyScanRecipe{}}},
		"malformed local time":     {{DirectoryKey: "root", DailyScan: &DailyScanRecipe{LocalTime: "25:00"}}},
		"unknown timezone":         {{DirectoryKey: "root", DailyScan: &DailyScanRecipe{LocalTime: "09:00", Timezone: "Mars/Olympus"}}},
	}
	for name, recipes := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := CreateBlank(dir, "Demo"); err != nil {
				t.Fatalf("CreateBlank: %v", err)
			}
			_, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{
				DirectoryRequirements: &valid,
				AutomationRecipes:     &recipes,
			})
			if !errors.Is(err, ErrInvalidAutomationRecipes) {
				t.Fatalf("expected ErrInvalidAutomationRecipes, got %v", err)
			}
		})
	}
}

func TestUpdateManifest_RejectsRemovingADirectoryAnExistingRecipeNeeds(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateBlank(dir, "Demo"); err != nil {
		t.Fatalf("CreateBlank: %v", err)
	}
	reqs := []DirectoryRequirement{{Key: "root", Label: "Root"}}
	recipes := []AutomationRecipe{{DirectoryKey: "root", DailyScan: &DailyScanRecipe{LocalTime: "09:00"}}}
	if _, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{DirectoryRequirements: &reqs, AutomationRecipes: &recipes}); err != nil {
		t.Fatalf("UpdateManifest: %v", err)
	}

	renamed := []DirectoryRequirement{{Key: "other-root", Label: "Other"}}
	_, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{DirectoryRequirements: &renamed})
	if !errors.Is(err, ErrInvalidAutomationRecipes) {
		t.Fatalf("expected the orphaned recipe to be rejected, got %v", err)
	}
}
