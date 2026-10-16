package projecttemplates

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestUpdateManifestGoalIntent(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit *ManifestEdit
		want string
	}{
		{"omitted", nil, "Original"},
		{"nil goal", &ManifestEdit{}, "Original"},
		{"trim and preserve Unicode", &ManifestEdit{Goal: goalPointer("  Résumer 日本語 <b>literally</b>  ")}, "Résumer 日本語 <b>literally</b>"},
		{"clear", &ManifestEdit{Goal: goalPointer("")}, ""},
		{"whitespace clears", &ManifestEdit{Goal: goalPointer(" \n\t ")}, ""},
		{"custom goals are not truncated", &ManifestEdit{Goal: goalPointer(strings.Repeat("長", 600))}, strings.Repeat("長", 600)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			manifest := filepath.Join(dir, "mine", ManifestFileName)
			writeFile(t, manifest, `{"name":"Mine","description":"Description","goal":"Original","extra":{"keep":true}}`)
			got, err := UpdateManifest(dir, "mine", "Mine", "Description", nil, tc.edit)
			if err != nil || got.Goal != tc.want {
				t.Fatalf("goal = %q, want %q, err=%v", got.Goal, tc.want, err)
			}
			if !strings.Contains(mustReadFile(t, manifest), `"keep": true`) {
				t.Fatal("goal edit lost unrelated manifest metadata")
			}
			if tc.want == "" && strings.Contains(mustReadFile(t, manifest), `"goal"`) {
				t.Fatal("clearing should remove the default key")
			}
		})
	}
}

func goalPointer(value string) *string { return &value }

func TestUserTemplateGoalImportAndCopyRoundTrip(t *testing.T) {
	for _, goal := range []string{"", "  Résumer 日本語 <goal>  ", "A hand-authored outcome"} {
		t.Run(goal, func(t *testing.T) {
			source, library := t.TempDir(), t.TempDir()
			data, err := json.Marshal(map[string]any{"name": "Hand authored", "goal": goal, "extra": []string{"keep"}})
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(source, ManifestFileName), string(data))
			imported, err := ImportFolder(library, source, "Imported Goal")
			if err != nil {
				t.Fatal(err)
			}
			copy, err := Duplicate(library, imported.ID, "Copied Goal")
			if err != nil {
				t.Fatal(err)
			}
			for _, tpl := range []Template{imported, copy} {
				if tpl.Goal != strings.TrimSpace(goal) || tpl.Builtin {
					t.Fatalf("goal round trip: %+v", tpl)
				}
				raw := mustReadFile(t, filepath.Join(library, tpl.ID, ManifestFileName))
				var manifest map[string]any
				if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
					t.Fatal(err)
				}
				if manifest["goal"] != goal || manifest["extra"] == nil {
					t.Fatal("copy/import changed hand-authored fields")
				}
			}
		})
	}
}

func TestSourceVariantGoalIsInheritedAndOptional(t *testing.T) {
	for _, goal := range []string{"", "Source-owned outcome 日本語"} {
		t.Run(goal, func(t *testing.T) {
			source := groupRequirementTemplate(t, `{"schema_version":1,"policy":"required","assistant_program_id":"project-guide","missing_home":"existing_only"}`)
			source.ID, source.Goal = "plugin:neutral:project", goal
			source.PluginOwner = &workspace.PluginTemplateOwner{PluginID: "neutral", PluginVersion: "1.2.0", BlueprintID: "project", BlueprintVersion: 3}
			pin := VariantSource{Template: source, SkeletonDigest: strings.Repeat("a", 64)}
			library := t.TempDir()
			created, err := CreateTemplateVariant(library, pin, "owner", VariantEdit{Name: "Local Variant", GroupRequirement: source.GroupRequirement})
			if err != nil || created.Goal != goal {
				t.Fatalf("variant goal: %q %v", created.Goal, err)
			}
			loaded, err := FindLibraryTemplate(library, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := ResolveTemplateVariant(loaded, pin, "owner")
			if err != nil || resolved.Goal != goal {
				t.Fatalf("resolved goal: %q %v", resolved.Goal, err)
			}
			if strings.Contains(mustReadFile(t, filepath.Join(library, created.ID, ManifestFileName)), `"goal"`) {
				t.Fatal("goal leaked into the overlay schema")
			}
		})
	}
}

func TestDuplicateStockGoalIsEditableWithoutChangingSource(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureLibrary(dir); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"writing-project", "code-project"} {
		t.Run(id, func(t *testing.T) {
			manifest := filepath.Join(dir, id, ManifestFileName)
			before := mustReadFile(t, manifest)
			source, err := FindLibraryTemplate(dir, id)
			if err != nil {
				t.Fatal(err)
			}
			copy, err := Duplicate(dir, id, "My "+source.Name)
			if err != nil {
				t.Fatal(err)
			}
			if copy.Builtin || copy.Goal != source.Goal || copy.BehaviorProfile != source.BehaviorProfile {
				t.Fatalf("copy lost its goal or behavior boundary: %+v", copy)
			}
			if err := EnsureMutable(dir, copy.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := Duplicate(dir, id, copy.Name); !errors.Is(err, ErrTemplateExists) {
				t.Fatalf("duplicate collision: %v", err)
			}
			changed, err := UpdateManifest(dir, copy.ID, copy.Name, copy.Description, nil, &ManifestEdit{Goal: goalPointer("My own outcome.")})
			if err != nil || changed.Goal != "My own outcome." {
				t.Fatalf("edit copy: %+v %v", changed, err)
			}
			if err := EnsureLibrary(dir); err != nil {
				t.Fatal(err)
			}
			kept, err := FindLibraryTemplate(dir, copy.ID)
			if err != nil || kept.Builtin || kept.Goal != changed.Goal {
				t.Fatalf("refresh replaced copy: %+v %v", kept, err)
			}
			if mustReadFile(t, manifest) != before {
				t.Fatal("copy/edit changed its source")
			}
		})
	}
}
