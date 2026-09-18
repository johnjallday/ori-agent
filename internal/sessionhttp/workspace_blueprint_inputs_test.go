package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// inputsTemplate is the acceptance-shaped blueprint: one number field with a
// unit, one select, applied to the scaffold's single project file.
func inputsTemplate() projecttemplates.Template {
	template := attachTemplate(projecttemplates.GroupPolicyNone)
	template.Inputs = &projecttemplates.InputsDeclaration{
		SchemaVersion: projecttemplates.InputsSchemaVersion,
		Title:         "Session settings",
		ApplyTo:       []string{"project.demo"},
		Fields: []projecttemplates.InputField{
			{
				ID: "tempo", Label: "Tempo", Type: projecttemplates.InputFieldNumber,
				Min: 40, Max: 240, Step: 1, Default: float64(120), Unit: "BPM",
			},
			{
				ID: "time_signature", Label: "Time signature", Type: projecttemplates.InputFieldSelect,
				Default: "4 4",
				Options: []projecttemplates.InputOption{
					{Value: "4 4", Label: "4/4"}, {Value: "3 4", Label: "3/4"}, {Value: "6 8", Label: "6/8"},
				},
			},
		},
	}
	return template
}

// newInputsFixture builds the fixture and gives its scaffold file the tokens
// the declaration names.
func newInputsFixture(t *testing.T) attachFixture {
	t.Helper()
	fixture := newAttachFixture(t, inputsTemplate())
	scaffold := filepath.Join(fixture.template.Path, "project.demo")
	if err := os.WriteFile(scaffold, []byte("TEMPO {{input.tempo}} {{input.time_signature}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func createdProjectFile(t *testing.T, fixture attachFixture, workspaceID, projectSlug string) string {
	t.Helper()
	folderRoot, err := fixture.handler.workspaceStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(folderRoot, projectSlug, "project.demo")) // #nosec G304 -- test fixture path
	if err != nil {
		t.Fatalf("read created project file: %v", err)
	}
	return string(data)
}

func getWorkspaceDetail(t *testing.T, handler *Handler, id string) map[string]any {
	t.Helper()
	response := httptest.NewRecorder()
	handler.HandleWorkspaces(response, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+id, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET workspace status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode workspace detail: %v", err)
	}
	return body
}

func TestCreateWorkspaceWritesChosenInputsIntoTheScaffoldAndRecordsThem(t *testing.T) {
	fixture := newInputsFixture(t)

	code, body := reviewAndCreate(t, fixture.handler, map[string]any{
		"name": "Night Drive", "template_id": fixture.template.ID, "group_composition": "standalone",
		"blueprint_inputs": map[string]any{"tempo": 96, "time_signature": "3 4"},
	}, "inputs-night-drive")
	if code != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", code, body)
	}
	id := body["folder"].(map[string]any)["id"].(string)

	if got := createdProjectFile(t, fixture, id, "night-drive"); got != "TEMPO 96 3 4\n" {
		t.Fatalf("created project file = %q, want the chosen values", got)
	}

	// Both stores carry the record: workspace.json is canonical, and the
	// session row is what a later read-modify-write starts from.
	for label, ws := range map[string]*agentworkspace.Workspace{
		"session": mustGetWorkspace(t, fixture.store, id), "canonical": canonicalWorkspace(t, fixture.store, id),
	} {
		stored, err := projecttemplates.GetBlueprintInputs(ws.SharedData)
		if err != nil || stored == nil {
			t.Fatalf("%s blueprint inputs = %#v, err=%v", label, stored, err)
		}
		if stored.Values["tempo"] != "96" || stored.Values["time_signature"] != "3 4" {
			t.Fatalf("%s values = %v", label, stored.Values)
		}
		if stored.Title != "Session settings" || len(stored.Fields) != 2 {
			t.Fatalf("%s record = %+v", label, stored)
		}
		if stored.Fields[0].Display != "96 BPM" || stored.Fields[1].Display != "3/4" {
			t.Fatalf("%s display text = %+v", label, stored.Fields)
		}
	}

	detail := getWorkspaceDetail(t, fixture.handler, id)
	projected, ok := detail["blueprint_inputs"].(map[string]any)
	if !ok {
		t.Fatalf("workspace detail has no blueprint_inputs: %v", detail)
	}
	values, _ := projected["values"].(map[string]any)
	if values["tempo"] != "96" || values["time_signature"] != "3 4" {
		t.Fatalf("projected values = %v", values)
	}
}

// Taking the defaults still records them: "they accepted 120" and "nobody was
// asked" must not look the same afterwards.
func TestCreateWorkspaceAppliesAndRecordsDeclaredDefaults(t *testing.T) {
	fixture := newInputsFixture(t)

	code, body := reviewAndCreate(t, fixture.handler, map[string]any{
		"name": "Night Drive", "template_id": fixture.template.ID, "group_composition": "standalone",
	}, "inputs-defaults")
	if code != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", code, body)
	}
	id := body["folder"].(map[string]any)["id"].(string)

	if got := createdProjectFile(t, fixture, id, "night-drive"); got != "TEMPO 120 4 4\n" {
		t.Fatalf("created project file = %q, want the declared defaults", got)
	}
	stored, err := projecttemplates.GetBlueprintInputs(mustGetWorkspace(t, fixture.store, id).SharedData)
	if err != nil || stored == nil || stored.Values["tempo"] != "120" {
		t.Fatalf("stored defaults = %#v err=%v", stored, err)
	}
}

func TestCreateWorkspaceRejectsInvalidInputValues(t *testing.T) {
	cases := []struct {
		name   string
		inputs map[string]any
		want   []string
	}{
		{"number above range", map[string]any{"tempo": 900}, []string{"Tempo", "40", "240"}},
		{"number below range", map[string]any{"tempo": 10}, []string{"Tempo", "40", "240"}},
		{"number off step", map[string]any{"tempo": 96.5}, []string{"Tempo", "steps of 1"}},
		{"option not offered", map[string]any{"time_signature": "7 8"}, []string{"Time signature", "4 4"}},
		{"undeclared field", map[string]any{"key": "C minor"}, []string{"key", "not one of this blueprint's inputs"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newInputsFixture(t)
			code, body := postPolicyWorkspace(t, fixture.handler, map[string]any{
				"name": "Night Drive", "template_id": fixture.template.ID, "group_composition": "standalone",
				"group_requirement_review": true, "blueprint_inputs": testCase.inputs,
			})
			if code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%v, want 400", code, body)
			}
			message := errorMessage(body)
			for _, want := range testCase.want {
				if !strings.Contains(message, want) {
					t.Fatalf("error = %q, want it to mention %q", message, want)
				}
			}
			// A refusal leaves nothing behind.
			if ids, _ := fixture.store.List(); len(ids) != 0 {
				t.Fatalf("a refused create left %d workspaces behind", len(ids))
			}
		})
	}
}

// Requirement 38: inputs belong to scaffolding a new project. Every other way
// to create a workspace refuses them by name rather than ignoring them.
func TestCreateWorkspaceRefusesInputsOutsideNewProjectMode(t *testing.T) {
	inputs := map[string]any{"tempo": 96}

	t.Run("group", func(t *testing.T) {
		fixture := newInputsFixture(t)
		code, body := postPolicyWorkspace(t, fixture.handler, map[string]any{
			"name": "Studio", "kind": "group", "blueprint_inputs": inputs,
		})
		if code != http.StatusBadRequest || !strings.Contains(errorMessage(body), "not available for groups") {
			t.Fatalf("status=%d body=%v", code, body)
		}
	})

	t.Run("blank", func(t *testing.T) {
		fixture := newInputsFixture(t)
		code, body := postPolicyWorkspace(t, fixture.handler, map[string]any{
			"name": "Scratch", "blank": true, "blueprint_inputs": inputs,
		})
		if code != http.StatusBadRequest || !strings.Contains(errorMessage(body), "blank workspace") {
			t.Fatalf("status=%d body=%v", code, body)
		}
	})

	t.Run("existing project", func(t *testing.T) {
		fixture := newInputsFixture(t)
		external := existingProjectFolder(t, map[string]string{"Night Drive.demo": "original project"})
		token, err := fixture.selections.Issue(external)
		if err != nil {
			t.Fatal(err)
		}
		payload := attachPayload(fixture.template.ID, token)
		payload["blueprint_inputs"] = inputs
		code, body := postPolicyWorkspace(t, fixture.handler, payload)
		if code != http.StatusBadRequest || !strings.Contains(errorMessage(body), "existing project") {
			t.Fatalf("status=%d body=%v", code, body)
		}
	})

	t.Run("import", func(t *testing.T) {
		fixture := newInputsFixture(t)
		encoded, err := json.Marshal(map[string]any{
			"name": "Imported", "path": t.TempDir(), "blueprint_inputs": inputs,
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewReader(encoded))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		fixture.handler.HandleWorkspaces(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Import Folder") {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

// A blueprint whose declaration could not be understood offers no inputs, so
// supplying them is refused rather than silently dropped.
func TestCreateWorkspaceRefusesInputsForAnUnusableDeclaration(t *testing.T) {
	template := inputsTemplate()
	template.Inputs = nil
	template.InputsError = "invalid inputs declaration: number field \"tempo\" needs min, max, and step"
	fixture := newAttachFixture(t, template)

	code, body := postPolicyWorkspace(t, fixture.handler, map[string]any{
		"name": "Night Drive", "template_id": fixture.template.ID, "group_composition": "standalone",
		"group_requirement_review": true, "blueprint_inputs": map[string]any{"tempo": 96},
	})
	if code != http.StatusBadRequest || !strings.Contains(errorMessage(body), "inputs are unavailable") {
		t.Fatalf("status=%d body=%v", code, body)
	}
}

// errorMessage reads the response's user-facing text. A plain bad request
// answers with {code, message}; the richer refusals use "error".
func errorMessage(body map[string]any) string {
	if message, ok := body["message"].(string); ok && message != "" {
		return message
	}
	message, _ := body["error"].(string)
	return message
}

// A blueprint that asks for nothing accepts nothing: a stray value is a
// mistake in the caller, not an extra to ignore.
func TestCreateWorkspaceRefusesInputsForABlueprintThatDeclaresNone(t *testing.T) {
	fixture := newAttachFixture(t, attachTemplate(projecttemplates.GroupPolicyNone))

	code, body := postPolicyWorkspace(t, fixture.handler, map[string]any{
		"name": "Night Drive", "template_id": fixture.template.ID, "group_composition": "standalone",
		"group_requirement_review": true, "blueprint_inputs": map[string]any{"tempo": 96},
	})
	if code != http.StatusBadRequest || !strings.Contains(errorMessage(body), "does not ask for any inputs") {
		t.Fatalf("status=%d body=%v", code, body)
	}
}
