package projecttemplates

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
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
