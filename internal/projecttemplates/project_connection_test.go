package projecttemplates

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeProjectConnectionIsStrictAndInert(t *testing.T) {
	declaration, err := normalizeProjectConnection(json.RawMessage(`{
		"schema_version":1,
		"supported_modes":["new_project","existing_project"],
		"attach_existing":{"entry_extensions":[".WAV",".rpp"]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(declaration.SupportedModes, []ProjectConnectionMode{ProjectConnectionExistingProject, ProjectConnectionNewProject}) ||
		!reflect.DeepEqual(declaration.AttachExisting.EntryExtensions, []string{".rpp", ".wav"}) {
		t.Fatalf("unexpected normalized declaration: %#v", declaration)
	}
	encoded, err := json.Marshal(declaration)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"path", "url", "command", "adapter", "scanner", "route"} {
		if jsonContainsKey(encoded, forbidden) {
			t.Errorf("project connection declaration exposed executable/control key %q: %s", forbidden, encoded)
		}
	}
}

func TestNormalizeProjectConnectionRejectsPartialOrUnknownDeclarations(t *testing.T) {
	cases := map[string]string{
		"unknown field":        `{"schema_version":1,"supported_modes":["new_project"],"command":"run"}`,
		"unsupported schema":   `{"schema_version":2,"supported_modes":["new_project"]}`,
		"unknown mode":         `{"schema_version":1,"supported_modes":["custom"]}`,
		"duplicate mode":       `{"schema_version":1,"supported_modes":["new_project","new_project"]}`,
		"attach block missing": `{"schema_version":1,"supported_modes":["existing_project"]}`,
		"undeclared attach":    `{"schema_version":1,"supported_modes":["new_project"],"attach_existing":{"entry_extensions":[".rpp"]}}`,
		"unsafe extension":     `{"schema_version":1,"supported_modes":["existing_project"],"attach_existing":{"entry_extensions":["../rpp"]}}`,
		"unknown attach field": `{"schema_version":1,"supported_modes":["existing_project"],"attach_existing":{"entry_extensions":[".rpp"],"glob":"**/*"}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeProjectConnection(json.RawMessage(raw)); !errors.Is(err, ErrInvalidProjectConnection) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestProjectConnectionStarterTasksAreValidatedAndFiltered(t *testing.T) {
	declaration, err := normalizeProjectConnection(json.RawMessage(`{
		"schema_version":1,
		"supported_modes":["new_project","existing_project"],
		"attach_existing":{"entry_extensions":[".rpp"]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	tasks := normalizeStarterTasks([]StarterTask{
		{Description: "all"},
		{Description: "new", ConnectionModes: []ProjectConnectionMode{ProjectConnectionNewProject}},
		{Description: "existing", ConnectionModes: []ProjectConnectionMode{ProjectConnectionExistingProject}},
	})
	if err := normalizeStarterTaskConnectionModes(tasks, declaration); err != nil {
		t.Fatal(err)
	}
	template := Template{ProjectConnection: declaration, StarterTasks: tasks}
	newTasks, err := StarterTasksForConnection(template, ProjectConnectionNewProject)
	if err != nil || len(newTasks) != 2 || newTasks[0].Description != "all" || newTasks[1].Description != "new" {
		t.Fatalf("new-project tasks = %#v err=%v", newTasks, err)
	}
	existingTasks, err := StarterTasksForConnection(template, ProjectConnectionExistingProject)
	if err != nil || len(existingTasks) != 2 || existingTasks[1].Description != "existing" {
		t.Fatalf("existing-project tasks = %#v err=%v", existingTasks, err)
	}
	newTasks[0].Description = "changed"
	if template.StarterTasks[0].Description != "all" {
		t.Fatal("filtered starter tasks alias the template")
	}
}

func TestTemplateMarksConnectionModeCrossReferencesUnusable(t *testing.T) {
	template := newTemplateWithManifest(t.TempDir(), manifest{
		Name: "Synthetic",
		StarterTasks: []StarterTask{{
			Description:     "attach only",
			ConnectionModes: []ProjectConnectionMode{ProjectConnectionExistingProject},
		}},
		ProjectConnection: json.RawMessage(`{"schema_version":1,"supported_modes":["new_project"]}`),
	}, defaultRuntimeCatalog())
	if template.ProjectConnection != nil || !template.HasInvalidProjectConnection() {
		t.Fatalf("invalid task mode reference was not failed closed: %#v", template)
	}
}

func jsonContainsKey(raw []byte, key string) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var walk func(any) bool
	walk = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			for currentKey, nested := range typed {
				if currentKey == key || walk(nested) {
					return true
				}
			}
		case []any:
			for _, nested := range typed {
				if walk(nested) {
					return true
				}
			}
		}
		return false
	}
	return walk(value)
}
