package projecttemplates

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func validStandaloneCompositionJSON() json.RawMessage {
	return json.RawMessage(`{
		"schema_version":1,
		"project_roles":[
			{"role_id":"lead","system_prompt":"Coordinate only this workspace and its reviewed project."}
		]
	}`)
}

func groupRequirementTemplate(t *testing.T, requirement string) Template {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "{{name}}.rpp"), "project")
	return newTemplateWithManifest(root, manifest{
		Name:                  "Neutral Project",
		AssistantProgram:      validScopedAssistantProgramJSON(),
		ProjectConnection:     json.RawMessage(`{"schema_version":1,"supported_modes":["new_project","existing_project"],"attach_existing":{"entry_extensions":[".rpp"]}}`),
		StandaloneComposition: validStandaloneCompositionJSON(),
		GroupRequirement:      json.RawMessage(requirement),
	}, defaultRuntimeCatalog())
}

func TestGroupRequirementAbsenceDiffersFromExplicitNone(t *testing.T) {
	absent := newTemplateWithManifest(t.TempDir(), manifest{Name: "Absent"}, defaultRuntimeCatalog())
	if absent.GroupRequirement != nil || absent.GroupRequirementError != "" {
		t.Fatalf("legacy absence became a policy: %#v %q", absent.GroupRequirement, absent.GroupRequirementError)
	}
	none := newTemplateWithManifest(t.TempDir(), manifest{
		Name: "None", GroupRequirement: json.RawMessage(`{"schema_version":1,"policy":"none"}`),
	}, defaultRuntimeCatalog())
	if none.GroupRequirement == nil || none.GroupRequirement.Policy != GroupPolicyNone || none.GroupRequirementError != "" {
		t.Fatalf("explicit None = %#v error=%q", none.GroupRequirement, none.GroupRequirementError)
	}
}

func TestGroupRequirementRejectsUnknownAndInactiveFields(t *testing.T) {
	cases := map[string]string{
		"unknown field":       `{"schema_version":1,"policy":"none","command":"run"}`,
		"unsupported version": `{"schema_version":2,"policy":"none"}`,
		"none with target":    `{"schema_version":1,"policy":"none","assistant_program_id":"project-guide"}`,
		"missing target":      `{"schema_version":1,"policy":"required","missing_home":"existing_only"}`,
		"missing name":        `{"schema_version":1,"policy":"required","assistant_program_id":"project-guide","missing_home":"offer_create"}`,
		"inactive name":       `{"schema_version":1,"policy":"required","assistant_program_id":"project-guide","missing_home":"existing_only","default_home_name":"Guide Home"}`,
		"unsafe name":         `{"schema_version":1,"policy":"required","assistant_program_id":"project-guide","missing_home":"offer_create","default_home_name":"https://example.test"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			value, err := ParseGroupRequirement(json.RawMessage(raw))
			if !errors.Is(err, ErrInvalidGroupRequirement) || value != nil {
				t.Fatalf("ParseGroupRequirement = %#v, %v", value, err)
			}
		})
	}
}

func TestStandaloneCompositionRejectsUnknownVersionAndRoleDrift(t *testing.T) {
	cases := map[string]json.RawMessage{
		"unknown field":        json.RawMessage(`{"schema_version":1,"project_roles":[{"role_id":"lead","system_prompt":"Bounded.","command":"run"}]}`),
		"unsupported version":  json.RawMessage(`{"schema_version":2,"project_roles":[{"role_id":"lead","system_prompt":"Bounded."}]}`),
		"Home role adaptation": json.RawMessage(`{"schema_version":1,"project_roles":[{"role_id":"coordinator","system_prompt":"Bounded."},{"role_id":"lead","system_prompt":"Bounded."}]}`),
		"missing project role": json.RawMessage(`{"schema_version":1,"project_roles":[]}`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			template := newTemplateWithManifest(t.TempDir(), manifest{
				Name: "Invalid composition", AssistantProgram: validScopedAssistantProgramJSON(),
				ProjectConnection:     json.RawMessage(`{"schema_version":1,"supported_modes":["new_project"]}`),
				StandaloneComposition: raw,
			}, defaultRuntimeCatalog())
			if template.StandaloneComposition != nil || !template.HasInvalidStandaloneComposition() {
				t.Fatalf("invalid standalone composition accepted: %#v", template.StandaloneComposition)
			}
		})
	}

	mismatch := groupRequirementTemplate(t, `{"schema_version":1,"policy":"required","assistant_program_id":"other-program","missing_home":"existing_only"}`)
	if mismatch.GroupRequirement != nil || !mismatch.HasInvalidGroupRequirement() || !strings.Contains(mismatch.GroupRequirementError, "does not match") {
		t.Fatalf("wrong target accepted: %#v / %q", mismatch.GroupRequirement, mismatch.GroupRequirementError)
	}
}

func TestGroupRequirementCompositionMatrixAndStandaloneTransformation(t *testing.T) {
	required := groupRequirementTemplate(t, `{"schema_version":1,"policy":"required","assistant_program_id":"project-guide","missing_home":"offer_create","default_home_name":"Project Guide Home"}`)
	if required.GroupRequirement == nil || required.StandaloneComposition == nil || required.GroupRequirementError != "" {
		t.Fatalf("required template = %#v group=%q standalone=%q", required.GroupRequirement, required.GroupRequirementError, required.StandaloneCompositionError)
	}
	recommended := groupRequirementTemplate(t, `{"schema_version":1,"policy":"recommended","assistant_program_id":"project-guide","missing_home":"existing_only"}`)
	preview, err := PreviewGroupRequirement(recommended, json.RawMessage(`{"schema_version":1,"policy":"recommended","assistant_program_id":"project-guide","missing_home":"existing_only"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.Available, []string{"grouped", "standalone"}) || preview.Grouped == nil || preview.Standalone == nil {
		t.Fatalf("recommended preview = %#v", preview)
	}
	if preview.Standalone.AssistantProgram || preview.Standalone.PreWorkspaceSetup || !reflect.DeepEqual(preview.Standalone.ProjectRoles, []string{"Project Lead"}) ||
		!reflect.DeepEqual(preview.Standalone.UnavailableRoles, []string{"Coordinator", "Library Manager"}) {
		t.Fatalf("standalone disclosure = %#v", preview.Standalone)
	}

	standalone, err := StandaloneTemplate(recommended)
	if err != nil {
		t.Fatal(err)
	}
	if standalone.AssistantProgram != nil || standalone.SetupQuestID != "" || len(standalone.Agents) != 1 || standalone.Agents[0].Name != "Project Lead" {
		t.Fatalf("standalone transform = %#v", standalone)
	}
	if strings.Contains(strings.ToLower(standalone.Agents[0].SystemPrompt), "home") {
		t.Fatalf("standalone prompt retained Home semantics: %q", standalone.Agents[0].SystemPrompt)
	}
	if required.GroupRequirement.Policy != GroupPolicyRequired || required.AssistantProgram == nil {
		t.Fatal("pure standalone transform mutated its source")
	}
}

func TestProgramBearingNoneRequiresStandaloneComposition(t *testing.T) {
	template := newTemplateWithManifest(t.TempDir(), manifest{
		Name: "Invalid", AssistantProgram: validScopedAssistantProgramJSON(),
		ProjectConnection: json.RawMessage(`{"schema_version":1,"supported_modes":["existing_project"],"attach_existing":{"entry_extensions":[".rpp"]}}`),
		GroupRequirement:  json.RawMessage(`{"schema_version":1,"policy":"none"}`),
	}, defaultRuntimeCatalog())
	if template.GroupRequirement != nil || !template.HasInvalidGroupRequirement() {
		t.Fatalf("program-bearing None without composition was accepted: %#v", template)
	}
}

func TestGroupRequirementOmittedSavePreservesAbsenceAndNullRemovesDeclaration(t *testing.T) {
	library := t.TempDir()
	directory := filepath.Join(library, "neutral")
	writeFile(t, filepath.Join(directory, ManifestFileName), `{"name":"Neutral","custom":{"preserved":true}}`)
	current, err := FindLibraryTemplate(library, "neutral")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := UpdateManifest(library, current.ID, "Renamed", "", nil, &ManifestEdit{ExpectedRevision: current.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.GroupRequirement != nil || metadata.GroupRequirementError != "" {
		t.Fatalf("metadata-only save materialized policy: %#v", metadata.GroupRequirement)
	}
	withPolicy, err := UpdateManifest(library, current.ID, "Renamed", "", nil, &ManifestEdit{
		GroupRequirement: json.RawMessage(`{"schema_version":1,"policy":"none"}`), ExpectedRevision: metadata.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := UpdateManifest(library, current.ID, "Renamed", "", nil, &ManifestEdit{
		GroupRequirement: json.RawMessage(`null`), ExpectedRevision: withPolicy.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if removed.GroupRequirement != nil {
		t.Fatalf("null did not restore legacy absence: %#v", removed.GroupRequirement)
	}
	data, err := os.ReadFile(filepath.Join(directory, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "group_requirement") || !strings.Contains(string(data), `"preserved": true`) {
		t.Fatalf("remove did not preserve unrelated fields:\n%s", data)
	}
}

func TestGroupRequirementManifestSaveIsRevisionCheckedAndAtomic(t *testing.T) {
	library := t.TempDir()
	directory := filepath.Join(library, "neutral")
	writeFile(t, filepath.Join(directory, ManifestFileName), `{"name":"Neutral","agents":[{"name":"Lead"}],"custom":"kept"}`)
	current, err := FindLibraryTemplate(library, "neutral")
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"schema_version":1,"policy":"none"}`)
	updated, err := UpdateManifest(library, "neutral", "Neutral", "", nil, &ManifestEdit{GroupRequirement: raw, ExpectedRevision: current.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision == current.Revision || updated.GroupRequirement == nil {
		t.Fatalf("save did not publish a revised policy: %#v", updated)
	}
	before, _ := os.ReadFile(filepath.Join(directory, ManifestFileName))
	_, err = UpdateManifest(library, "neutral", "Changed", "", nil, &ManifestEdit{GroupRequirement: json.RawMessage(`{"schema_version":1,"policy":"required"}`), ExpectedRevision: current.Revision})
	if !errors.Is(err, ErrTemplateRevisionStale) {
		t.Fatalf("stale save error = %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(directory, ManifestFileName))
	if string(before) != string(after) || !strings.Contains(string(after), `"custom": "kept"`) {
		t.Fatalf("failed save changed unrelated manifest bytes:\n%s", after)
	}
}

func TestReaperShapedStandalonePreviewRetainsProjectSafetyWithoutHomeRoles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "{{name}}.rpp"), "project")
	template := newTemplateWithManifest(root, manifest{
		Name: "Reaper-shaped fixture",
		AssistantProgram: json.RawMessage(`{
			"schema_version":2,"id":"music-producer-assistant","station_name":"Music Production Home",
			"default_primary_name":"Producer","hire_title":"Staff music production",
			"roles":[
				{"id":"producer","label":"Producer","scope":"home","required":true,"primary":true,"system_prompt":"Coordinate reviewed portfolio records."},
				{"id":"engineer","label":"Engineer","scope":"project","required":true,"primary":true,"system_prompt":"Work only with the exact linked project."}
			],
			"stages":[{"id":"tracking","label":"Tracking","accepted_completion_threshold":0}],
			"reflection":{"minimum_projects":3,"cadence_hours":24,"max_projects":8,"max_events_per_project":16,"max_candidates":4,"max_evidence":4,"rubric":"Review bounded project evidence."}
		}`),
		ProjectConnection:     json.RawMessage(`{"schema_version":1,"supported_modes":["new_project","existing_project"],"attach_existing":{"entry_extensions":[".rpp"]}}`),
		RuntimeRequirements:   json.RawMessage(`{"schema_version":1,"operating_modes":[{"id":"file_only","label":"File-only","description":"Use exact project files."}],"requirements":[]}`),
		StandaloneComposition: json.RawMessage(`{"schema_version":1,"project_roles":[{"role_id":"engineer","system_prompt":"Work only with this workspace's exact project file."}]}`),
		GroupRequirement:      json.RawMessage(`{"schema_version":1,"policy":"recommended","assistant_program_id":"music-producer-assistant","missing_home":"offer_create","default_home_name":"Music Production Home"}`),
	}, defaultRuntimeCatalog())
	if template.GroupRequirement == nil || template.GroupRequirementError != "" {
		t.Fatalf("Reaper-shaped contract failed generic validation: %#v", template)
	}
	preview, err := PreviewGroupRequirement(template, json.RawMessage(`{"schema_version":1,"policy":"none"}`))
	if err != nil {
		t.Fatal(err)
	}
	if preview.Standalone == nil || preview.Standalone.AssistantProgram || preview.Standalone.PreWorkspaceSetup ||
		!reflect.DeepEqual(preview.Standalone.ProjectRoles, []string{"Engineer"}) ||
		!reflect.DeepEqual(preview.Standalone.UnavailableRoles, []string{"Producer"}) ||
		!reflect.DeepEqual(preview.Standalone.RuntimeModes, []string{"File-only"}) {
		t.Fatalf("Reaper-shaped standalone projection = %#v", preview.Standalone)
	}
}

func TestTemplateVariantRoundTripPinsSourceWithoutPluginAuthority(t *testing.T) {
	source := groupRequirementTemplate(t, `{"schema_version":1,"policy":"required","assistant_program_id":"project-guide","missing_home":"offer_create","default_home_name":"Project Guide Home"}`)
	source.ID = "plugin:neutral:project"
	source.PluginOwner = &workspace.PluginTemplateOwner{PluginID: "neutral", PluginVersion: "1.2.0", BlueprintID: "project", BlueprintVersion: 3}
	variantSource := VariantSource{Template: source, SkeletonDigest: strings.Repeat("a", 64)}
	library := t.TempDir()
	created, err := CreateTemplateVariant(library, variantSource, "owner-1", VariantEdit{
		Name: "My Neutral Project", Description: "Local policy only", Icon: "N",
		GroupRequirement: &GroupRequirement{SchemaVersion: 1, Policy: GroupPolicyRecommended, AssistantProgramID: "project-guide", MissingHome: MissingHomeExistingOnly},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.PluginOwner != nil || created.TemplateVariant == nil || created.VariantSourceState != VariantSourceReady || created.Path != source.Path {
		t.Fatalf("effective variant authority/source = %#v", created)
	}
	entries, err := os.ReadDir(filepath.Join(library, created.ID))
	if err != nil || len(entries) != 1 || entries[0].Name() != ManifestFileName {
		t.Fatalf("variant overlay files = %#v err=%v", entries, err)
	}
	loaded, err := FindLibraryTemplate(library, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PluginOwner != nil || loaded.TemplateVariant.OwnerUserID != "owner-1" || loaded.VariantRevision != created.VariantRevision {
		t.Fatalf("persisted overlay = %#v", loaded)
	}
	if _, err := ResolveTemplateVariant(loaded, variantSource, "other-owner"); !errors.Is(err, ErrTemplateVariantOwner) {
		t.Fatalf("foreign owner resolution error = %v", err)
	}

	presentationOnly := source
	presentationOnly.Name = "Renamed Source"
	presentationOnly.Description = "Updated source copy"
	presentationOnly.Icon = "R"
	if _, err := ResolveTemplateVariant(loaded, VariantSource{Template: presentationOnly, SkeletonDigest: variantSource.SkeletonDigest}, "owner-1"); err != nil {
		t.Fatalf("presentation-only source rename invalidated variant: %v", err)
	}

	changed := source
	changed.BehaviorProfile = "research"
	if _, err := ResolveTemplateVariant(loaded, VariantSource{Template: changed, SkeletonDigest: variantSource.SkeletonDigest}, "owner-1"); !errors.Is(err, ErrTemplateVariantSourceChanged) {
		t.Fatalf("changed behavior source resolution error = %v", err)
	}
}

func TestTemplateVariantRejectsInvalidCompositionBeforePublishingBytes(t *testing.T) {
	source := groupRequirementTemplate(t, `{"schema_version":1,"policy":"required","assistant_program_id":"project-guide","missing_home":"existing_only"}`)
	source.ID = "plugin:neutral:project"
	source.PluginOwner = &workspace.PluginTemplateOwner{PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 1}
	source.StandaloneComposition = nil
	variantSource := VariantSource{Template: source, SkeletonDigest: strings.Repeat("b", 64)}
	library := t.TempDir()

	_, err := CreateTemplateVariant(library, variantSource, "owner-1", VariantEdit{
		Name:             "Invalid standalone",
		GroupRequirement: &GroupRequirement{SchemaVersion: 1, Policy: GroupPolicyNone},
	})
	if !errors.Is(err, ErrInvalidGroupRequirement) {
		t.Fatalf("invalid create error = %v", err)
	}
	if entries, readErr := os.ReadDir(library); readErr != nil || len(entries) != 0 {
		t.Fatalf("invalid create published files: %#v err=%v", entries, readErr)
	}

	created, err := CreateTemplateVariant(library, variantSource, "owner-1", VariantEdit{
		Name:             "Required variant",
		GroupRequirement: CloneGroupRequirement(source.GroupRequirement),
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(library, created.ID, ManifestFileName)
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = UpdateTemplateVariant(library, created.ID, "owner-1", created.VariantRevision, VariantEdit{
		Name:             "Invalid update",
		GroupRequirement: &GroupRequirement{SchemaVersion: 1, Policy: GroupPolicyNone},
	}, variantSource)
	if !errors.Is(err, ErrInvalidGroupRequirement) {
		t.Fatalf("invalid update error = %v", err)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("invalid effective composition changed variant bytes")
	}
}

func TestTemplateVariantDuplicateImportAndRehostStayBounded(t *testing.T) {
	source := groupRequirementTemplate(t, `{"schema_version":1,"policy":"required","assistant_program_id":"project-guide","missing_home":"existing_only"}`)
	source.ID = "plugin:neutral:project"
	source.PluginOwner = &workspace.PluginTemplateOwner{PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 1}
	variantSource := VariantSource{Template: source, SkeletonDigest: strings.Repeat("c", 64)}
	firstLibrary := t.TempDir()
	created, err := CreateTemplateVariant(firstLibrary, variantSource, "owner-1", VariantEdit{
		Name: "Pinned variant", GroupRequirement: CloneGroupRequirement(source.GroupRequirement),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Duplicate(firstLibrary, created.ID, "Ordinary copy"); !errors.Is(err, ErrTemplateVariantRestricted) {
		t.Fatalf("ordinary duplicate error = %v", err)
	}
	if _, err := ImportFolder(t.TempDir(), filepath.Join(firstLibrary, created.ID), "Imported"); !errors.Is(err, ErrInvalidTemplateVariant) {
		t.Fatalf("ordinary import error = %v", err)
	}
	if err := EnsureMutable(firstLibrary, created.ID); !errors.Is(err, ErrTemplateVariantRestricted) {
		t.Fatalf("variant mutable check = %v", err)
	}

	secondLibrary := t.TempDir()
	if _, err := RehostTemplateVariant(secondLibrary, filepath.Join(firstLibrary, created.ID), "Rehosted", "owner-2", false, variantSource); !errors.Is(err, ErrTemplateVariantRehost) {
		t.Fatalf("unconfirmed rehost error = %v", err)
	}
	rehosted, err := RehostTemplateVariant(secondLibrary, filepath.Join(firstLibrary, created.ID), "Rehosted", "owner-2", true, variantSource)
	if err != nil {
		t.Fatal(err)
	}
	if rehosted.TemplateVariant == nil || rehosted.TemplateVariant.OwnerUserID != "owner-2" ||
		rehosted.TemplateVariant.VariantID == created.TemplateVariant.VariantID || rehosted.ID == created.ID {
		t.Fatalf("rehosted identity = %#v", rehosted.TemplateVariant)
	}
	entries, err := os.ReadDir(filepath.Join(secondLibrary, rehosted.ID))
	if err != nil || len(entries) != 1 || entries[0].Name() != ManifestFileName {
		t.Fatalf("rehosted envelope = %#v err=%v", entries, err)
	}
}
