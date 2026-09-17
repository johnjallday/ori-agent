package projecttemplates

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// inputsManifest is the acceptance-shaped declaration used across these tests:
// one number field with a unit and one select, applied to a single scaffold
// file. Deliberately generic — the host knows nothing about what the values
// mean.
const inputsManifest = `{
  "name": "Session",
  "inputs": {
    "schema_version": 1,
    "title": "Session settings",
    "apply_to": ["{{name}}.cfg"],
    "fields": [
      {"id":"tempo","label":"Tempo","type":"number","min":40,"max":240,"step":1,"default":120,"unit":"BPM"},
      {"id":"time_signature","label":"Time signature","type":"select","default":"4 4",
       "options":[{"value":"4 4","label":"4/4"},{"value":"3 4","label":"3/4"},{"value":"6 8","label":"6/8"}]}
    ]
  }
}`

// writeInputsTemplate builds a scaffold whose one declared file uses both
// tokens, plus a second file that merely looks like it does.
func writeInputsTemplate(t *testing.T, manifestJSON string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestFileName), manifestJSON)
	writeFile(t, filepath.Join(dir, "{{name}}.cfg"), "TEMPO {{input.tempo}} {{input.time_signature}}\n")
	writeFile(t, filepath.Join(dir, "notes", "readme.txt"), "not declared: {{input.tempo}}\n")
	return dir
}

func TestInputsDeclarationLoads(t *testing.T) {
	tpl := newTemplate(writeInputsTemplate(t, inputsManifest))
	if tpl.InputsError != "" {
		t.Fatalf("InputsError = %q, want none", tpl.InputsError)
	}
	if !tpl.HasInputs() {
		t.Fatal("HasInputs() = false, want true")
	}
	if tpl.Inputs.Title != "Session settings" || len(tpl.Inputs.Fields) != 2 {
		t.Fatalf("declaration = %+v", tpl.Inputs)
	}
	if got := tpl.Inputs.ApplyTo; len(got) != 1 || got[0] != "{{name}}.cfg" {
		t.Fatalf("ApplyTo = %v", got)
	}
	number := tpl.Inputs.Fields[0]
	if number.Type != InputFieldNumber || number.Min != 40 || number.Max != 240 || number.Step != 1 || number.Unit != "BPM" {
		t.Fatalf("number field = %+v", number)
	}
	if number.Default != float64(120) {
		t.Fatalf("number default = %#v, want float64(120)", number.Default)
	}
	selectField := tpl.Inputs.Fields[1]
	if selectField.Type != InputFieldSelect || len(selectField.Options) != 3 || selectField.Default != "4 4" {
		t.Fatalf("select field = %+v", selectField)
	}
}

// The API projection is what the create modal reads, so it must round-trip to
// the same shape the manifest declared — including a numeric default staying a
// JSON number.
func TestInputsProjectToJSONInTheDeclaredShape(t *testing.T) {
	tpl := newTemplate(writeInputsTemplate(t, inputsManifest))
	encoded, err := json.Marshal(tpl.Inputs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"schema_version":1`, `"title":"Session settings"`, `"default":120`, `"unit":"BPM"`, `"default":"4 4"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("projection missing %s:\n%s", want, encoded)
		}
	}
}

func TestInputsDeclarationRejections(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  string
	}{
		{"unknown key", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[],"extra":1}`, "malformed"},
		{"wrong schema version", `{"schema_version":2,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "schema version"},
		{"blank title", `{"schema_version":1,"title":"  ","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "title"},
		{"no fields", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[]}`, "1-8 fields"},
		{"too many fields", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[
			{"id":"a","label":"A","type":"number","min":1,"max":2,"step":1,"default":1},
			{"id":"b","label":"B","type":"number","min":1,"max":2,"step":1,"default":1},
			{"id":"c","label":"C","type":"number","min":1,"max":2,"step":1,"default":1},
			{"id":"d","label":"D","type":"number","min":1,"max":2,"step":1,"default":1},
			{"id":"e","label":"E","type":"number","min":1,"max":2,"step":1,"default":1},
			{"id":"f","label":"F","type":"number","min":1,"max":2,"step":1,"default":1},
			{"id":"g","label":"G","type":"number","min":1,"max":2,"step":1,"default":1},
			{"id":"h","label":"H","type":"number","min":1,"max":2,"step":1,"default":1},
			{"id":"i","label":"I","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "1-8 fields"},
		{"uppercase id", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"Tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "field id"},
		{"id starting with a digit", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"1tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "field id"},
		{"duplicate id", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[
			{"id":"tempo","label":"A","type":"number","min":1,"max":2,"step":1,"default":1},
			{"id":"tempo","label":"B","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "declared twice"},
		{"blank label", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "needs a label"},
		{"unsupported type", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"text","default":"anything"}]}`, "unsupported type"},
		{"number without bounds", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","default":1}]}`, "needs min, max, and step"},
		{"zero step", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":0,"default":1}]}`, "step greater than zero"},
		{"negative step", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":-1,"default":1}]}`, "step greater than zero"},
		{"min above max", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":9,"max":2,"step":1,"default":2}]}`, "min greater than max"},
		{"default out of range", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":7}]}`, "default outside"},
		{"default off step", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":0,"max":10,"step":2,"default":3}]}`, "multiple of its step"},
		{"bound beyond the supported range", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":0,"max":1e12,"step":1,"default":0}]}`, "supported range"},
		{"string default on a number", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":"1"}]}`, "numeric default"},
		{"number carrying options", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":1,"options":[{"value":"a"},{"value":"b"}]}]}`, "cannot declare options"},
		{"select carrying bounds", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"time_signature","label":"TS","type":"select","min":1,"default":"a","options":[{"value":"a"},{"value":"b"}]}]}`, "cannot declare number bounds"},
		{"one option", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"time_signature","label":"TS","type":"select","default":"a","options":[{"value":"a"}]}]}`, "needs 2-24 options"},
		{"duplicate option value", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"time_signature","label":"TS","type":"select","default":"a","options":[{"value":"a"},{"value":"a"}]}]}`, "repeats option value"},
		{"option value outside the portable set", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"time_signature","label":"TS","type":"select","default":"a","options":[{"value":"a"},{"value":"a\nb"}]}]}`, "plain portable text"},
		{"select default not an option", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"time_signature","label":"TS","type":"select","default":"z","options":[{"value":"a"},{"value":"b"}]}]}`, "not one of its options"},
		{"numeric default on a select", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],"fields":[{"id":"time_signature","label":"TS","type":"select","default":4,"options":[{"value":"a"},{"value":"b"}]}]}`, "string default"},
		{"no apply_to", `{"schema_version":1,"title":"T","apply_to":[],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "apply_to needs 1-8 paths"},
		{"apply_to escaping the scaffold", `{"schema_version":1,"title":"T","apply_to":["../outside.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "scaffold-relative file"},
		{"apply_to naming the manifest", `{"schema_version":1,"title":"T","apply_to":["` + ManifestFileName + `"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "not project content"},
		{"apply_to listed twice", `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg","{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "twice"},
		{"apply_to naming a missing file", `{"schema_version":1,"title":"T","apply_to":["absent.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "not a regular file"},
		{"apply_to naming a directory", `{"schema_version":1,"title":"T","apply_to":["notes"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":1}]}`, "not a regular file"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tpl := newTemplate(writeInputsTemplate(t, `{"name":"Session","inputs":`+testCase.block+`}`))
			if tpl.InputsError == "" {
				t.Fatalf("expected an inputs error, got a usable declaration %+v", tpl.Inputs)
			}
			if !strings.Contains(tpl.InputsError, testCase.want) {
				t.Fatalf("InputsError = %q, want it to mention %q", tpl.InputsError, testCase.want)
			}
			if tpl.Inputs != nil {
				t.Error("an unusable declaration must leave Inputs nil")
			}
			// The rest of the blueprint survives an isolated failure.
			if tpl.Name != "Session" {
				t.Errorf("Name = %q, want the manifest's name to survive", tpl.Name)
			}
		})
	}
}

// A file larger than the substitution limit, and one that is not text at all,
// are refused at load. Silently skipping either would create a project whose
// file still carries the author's tokens.
func TestInputsRejectOversizedAndBinaryApplyToFiles(t *testing.T) {
	manifest := `{"name":"Session","inputs":{"schema_version":1,"title":"T","apply_to":["big.cfg"],
		"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":1}]}}`

	t.Run("oversized", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ManifestFileName), manifest)
		if err := os.WriteFile(filepath.Join(dir, "big.cfg"), make([]byte, maxInputApplyToFileBytes+1), 0o640); err != nil {
			t.Fatal(err)
		}
		if got := newTemplate(dir).InputsError; !strings.Contains(got, "substitution limit") {
			t.Fatalf("InputsError = %q, want the size limit", got)
		}
	})

	t.Run("binary", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ManifestFileName), manifest)
		if err := os.WriteFile(filepath.Join(dir, "big.cfg"), []byte{0x00, 0x01, 0x02}, 0o640); err != nil {
			t.Fatal(err)
		}
		if got := newTemplate(dir).InputsError; !strings.Contains(got, "not a text file") {
			t.Fatalf("InputsError = %q, want the text-file check", got)
		}
	})
}

func TestInputsTokenScanAtLoad(t *testing.T) {
	declaration := `{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],
		"fields":[{"id":"tempo","label":"Tempo","type":"number","min":40,"max":240,"step":1,"default":120}]}`

	t.Run("undeclared token fails and names the file and token", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ManifestFileName), `{"name":"Session","inputs":`+declaration+`}`)
		writeFile(t, filepath.Join(dir, "{{name}}.cfg"), "TEMPO {{input.tempo}} {{input.swing}}\n")
		got := newTemplate(dir).InputsError
		if !strings.Contains(got, "{{name}}.cfg") || !strings.Contains(got, "swing") {
			t.Fatalf("InputsError = %q, want it to name the file and the token", got)
		}
	})

	t.Run("malformed token fails", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ManifestFileName), `{"name":"Session","inputs":`+declaration+`}`)
		writeFile(t, filepath.Join(dir, "{{name}}.cfg"), "TEMPO {{input.}}\n")
		if got := newTemplate(dir).InputsError; !strings.Contains(got, "malformed token") {
			t.Fatalf("InputsError = %q, want a malformed-token error", got)
		}
	})

	t.Run("a declared but unused field is fine", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ManifestFileName), `{"name":"Session","inputs":`+declaration+`}`)
		writeFile(t, filepath.Join(dir, "{{name}}.cfg"), "no tokens here\n")
		if got := newTemplate(dir).InputsError; got != "" {
			t.Fatalf("InputsError = %q, want none", got)
		}
	})

	t.Run("a token in a file name fails", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ManifestFileName), `{"name":"Session","inputs":`+declaration+`}`)
		writeFile(t, filepath.Join(dir, "{{name}}.cfg"), "TEMPO {{input.tempo}}\n")
		writeFile(t, filepath.Join(dir, "{{input.tempo}}.txt"), "x")
		if got := newTemplate(dir).InputsError; !strings.Contains(got, "input token in its name") {
			t.Fatalf("InputsError = %q, want the file-name rejection", got)
		}
	})
}

func TestResolveInputValues(t *testing.T) {
	declaration := newTemplate(writeInputsTemplate(t, inputsManifest)).Inputs
	if declaration == nil {
		t.Fatal("fixture declaration did not load")
	}

	t.Run("missing ids take the declared defaults", func(t *testing.T) {
		values, err := ResolveInputValues(declaration, nil)
		if err != nil {
			t.Fatalf("ResolveInputValues: %v", err)
		}
		if values["tempo"] != "120" || values["time_signature"] != "4 4" {
			t.Fatalf("values = %v", values)
		}
	})

	t.Run("supplied values are used", func(t *testing.T) {
		values, err := ResolveInputValues(declaration, map[string]json.RawMessage{
			"tempo":          json.RawMessage(`96`),
			"time_signature": json.RawMessage(`"3 4"`),
		})
		if err != nil {
			t.Fatalf("ResolveInputValues: %v", err)
		}
		if values["tempo"] != "96" || values["time_signature"] != "3 4" {
			t.Fatalf("values = %v", values)
		}
	})

	t.Run("numbers format canonically", func(t *testing.T) {
		values, err := ResolveInputValues(declaration, map[string]json.RawMessage{"tempo": json.RawMessage(`120.0`)})
		if err != nil {
			t.Fatalf("ResolveInputValues: %v", err)
		}
		if values["tempo"] != "120" {
			t.Fatalf("tempo = %q, want the plain decimal 120", values["tempo"])
		}
		if got := FormatInputNumber(1000000); got != "1000000" {
			t.Errorf("FormatInputNumber(1e6) = %q, want plain decimal", got)
		}
	})

	rejections := []struct {
		name     string
		provided map[string]json.RawMessage
		want     string
	}{
		{"below range", map[string]json.RawMessage{"tempo": json.RawMessage(`39`)}, "between 40 and 240"},
		{"above range", map[string]json.RawMessage{"tempo": json.RawMessage(`241`)}, "between 40 and 240"},
		{"off step", map[string]json.RawMessage{"tempo": json.RawMessage(`96.5`)}, "steps of 1"},
		{"wrong type for a number", map[string]json.RawMessage{"tempo": json.RawMessage(`"96"`)}, "must be a number"},
		{"option not offered", map[string]json.RawMessage{"time_signature": json.RawMessage(`"7 8"`)}, "must be one of"},
		{"wrong type for a select", map[string]json.RawMessage{"time_signature": json.RawMessage(`4`)}, "must be one of"},
		{"undeclared id", map[string]json.RawMessage{"key": json.RawMessage(`"C"`)}, "not one of this blueprint's inputs"},
	}
	for _, testCase := range rejections {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ResolveInputValues(declaration, testCase.provided)
			if !errors.Is(err, ErrInputValue) {
				t.Fatalf("err = %v, want ErrInputValue", err)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("err = %q, want it to mention %q", err, testCase.want)
			}
		})
	}

	t.Run("a blueprint without inputs accepts none", func(t *testing.T) {
		values, err := ResolveInputValues(nil, nil)
		if err != nil || values != nil {
			t.Fatalf("ResolveInputValues(nil, nil) = %v, %v", values, err)
		}
		if _, err := ResolveInputValues(nil, map[string]json.RawMessage{"tempo": json.RawMessage(`96`)}); !errors.Is(err, ErrInputValue) {
			t.Fatalf("err = %v, want ErrInputValue", err)
		}
	})
}

func TestInstantiateTemplateWithInputsSubstitutesOnlyDeclaredFiles(t *testing.T) {
	tplDir := writeInputsTemplate(t, inputsManifest)
	writeFile(t, filepath.Join(tplDir, "assets", "tone.bin"), "\x00\x01binary")
	tpl := newTemplate(tplDir)
	if tpl.InputsError != "" {
		t.Fatalf("InputsError = %q", tpl.InputsError)
	}
	wsDir := t.TempDir()

	result, err := InstantiateTemplateWithInputs(tpl, wsDir, "Song X", map[string]json.RawMessage{
		"tempo":          json.RawMessage(`96`),
		"time_signature": json.RawMessage(`"3 4"`),
	})
	if err != nil {
		t.Fatalf("InstantiateTemplateWithInputs: %v", err)
	}

	projectRoot := filepath.Join(wsDir, result.ProjectPath)
	declared := readTestFile(t, filepath.Join(projectRoot, "song-x.cfg"))
	if declared != "TEMPO 96 3 4\n" {
		t.Fatalf("declared file = %q, want the chosen values", declared)
	}

	// A file the blueprint did not list keeps its token-looking text verbatim.
	if got := readTestFile(t, filepath.Join(projectRoot, "notes", "readme.txt")); got != "not declared: {{input.tempo}}\n" {
		t.Fatalf("undeclared file = %q, want a byte copy", got)
	}
	if got := readTestFile(t, filepath.Join(projectRoot, "assets", "tone.bin")); got != "\x00\x01binary" {
		t.Fatalf("binary file = %q, want a byte copy", got)
	}

	// Everything except the one declared file is byte-identical to the source.
	assertTreeMatchesExcept(t, tplDir, projectRoot, map[string]string{"{{name}}.cfg": "song-x.cfg"})
}

func TestInstantiateTemplateAppliesDeclaredDefaults(t *testing.T) {
	tpl := newTemplate(writeInputsTemplate(t, inputsManifest))
	wsDir := t.TempDir()
	result, err := InstantiateTemplate(tpl, wsDir, "Song X")
	if err != nil {
		t.Fatalf("InstantiateTemplate: %v", err)
	}
	if got := readTestFile(t, filepath.Join(wsDir, result.ProjectPath, "song-x.cfg")); got != "TEMPO 120 4 4\n" {
		t.Fatalf("defaults were not applied: %q", got)
	}
}

func TestInstantiateTemplateRefusesUncheckedAndUnusableInputs(t *testing.T) {
	tpl := newTemplate(writeInputsTemplate(t, inputsManifest))

	if _, err := InstantiateTemplateWithInputs(tpl, t.TempDir(), "Song X", map[string]json.RawMessage{
		"tempo": json.RawMessage(`9000`),
	}); !errors.Is(err, ErrInputValue) {
		t.Fatalf("err = %v, want ErrInputValue", err)
	}

	broken := tpl
	broken.Inputs = nil
	broken.InputsError = "something is wrong"
	if _, err := InstantiateTemplateWithInputs(broken, t.TempDir(), "Song X", nil); !errors.Is(err, ErrInvalidInputs) {
		t.Fatalf("err = %v, want ErrInvalidInputs", err)
	}
}

// PreviewInstantiation backs the review receipt and must keep reading names
// only, even for a blueprint whose files would be rewritten.
func TestPreviewInstantiationIgnoresInputs(t *testing.T) {
	tpl := newTemplate(writeInputsTemplate(t, inputsManifest))
	files, err := PreviewInstantiation(tpl, "Song X")
	if err != nil {
		t.Fatalf("PreviewInstantiation: %v", err)
	}
	want := []string{"notes/readme.txt", "song-x.cfg"}
	if len(files) != len(want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	for index, name := range want {
		if files[index] != name {
			t.Fatalf("files = %v, want %v", files, want)
		}
	}
}

// Both origins go through the same loader, so both get the same validation:
// an installed plugin blueprint becomes unavailable rather than silently
// input-less, and a user-authored library template surfaces the diagnostic.
func TestInputsValidationAppliesToBothTemplateOrigins(t *testing.T) {
	const badInputs = `"inputs":{"schema_version":1,"title":"T","apply_to":["{{name}}.cfg"],
		"fields":[{"id":"tempo","label":"Tempo","type":"number","min":1,"max":2,"step":1,"default":99}]}`

	t.Run("plugin blueprint", func(t *testing.T) {
		writeSkeleton := func(t *testing.T, manifestJSON string) (string, string) {
			t.Helper()
			dir := t.TempDir()
			manifestPath := filepath.Join(dir, ManifestFileName)
			writeFile(t, manifestPath, manifestJSON)
			skeleton := filepath.Join(dir, "project")
			writeFile(t, filepath.Join(skeleton, "{{name}}.cfg"), "TEMPO {{input.tempo}}\n")
			return manifestPath, skeleton
		}

		manifestPath, skeleton := writeSkeleton(t, `{"name":"Plugin Blueprint",`+badInputs+`}`)
		if _, _, err := LoadPluginBlueprint(manifestPath, skeleton, defaultRuntimeCatalog()); err == nil {
			t.Fatal("an unusable inputs declaration must make the plugin blueprint unavailable")
		}

		goodPath, goodSkeleton := writeSkeleton(t, `{"name":"Plugin Blueprint","inputs":{"schema_version":1,"title":"T",
			"apply_to":["{{name}}.cfg"],"fields":[{"id":"tempo","label":"Tempo","type":"number","min":40,"max":240,"step":1,"default":120}]}}`)
		tpl, _, err := LoadPluginBlueprint(goodPath, goodSkeleton, defaultRuntimeCatalog())
		if err != nil {
			t.Fatalf("a usable declaration must load: %v", err)
		}
		if !tpl.HasInputs() {
			t.Fatal("HasInputs() = false for a usable plugin blueprint declaration")
		}
	})

	t.Run("user library template", func(t *testing.T) {
		library := t.TempDir()
		dir := filepath.Join(library, "session")
		writeFile(t, filepath.Join(dir, ManifestFileName), `{"name":"Session",`+badInputs+`}`)
		writeFile(t, filepath.Join(dir, "{{name}}.cfg"), "TEMPO {{input.tempo}}\n")

		templates, err := ListLibrary(library)
		if err != nil {
			t.Fatalf("ListLibrary: %v", err)
		}
		if len(templates) != 1 {
			t.Fatalf("templates = %d, want 1", len(templates))
		}
		loaded := templates[0]
		if !loaded.HasInvalidInputs() || loaded.Inputs != nil {
			t.Fatalf("expected an isolated inputs failure, got %+v / %q", loaded.Inputs, loaded.InputsError)
		}
		var flagged bool
		for _, warning := range loaded.Warnings {
			if strings.Contains(warning, "template.json inputs is unusable") {
				flagged = true
			}
		}
		if !flagged {
			t.Fatalf("warnings = %v, want the authoring diagnostic", loaded.Warnings)
		}
	})
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// assertTreeMatchesExcept checks that every scaffold file was byte-copied,
// except the renamed entries the caller lists as rewritten.
func assertTreeMatchesExcept(t *testing.T, sourceRoot, destRoot string, rewritten map[string]string) {
	t.Helper()
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ManifestFileName {
			continue
		}
		if _, skip := rewritten[name]; skip {
			continue
		}
		sourcePath := filepath.Join(sourceRoot, name)
		destPath := filepath.Join(destRoot, name)
		if entry.IsDir() {
			assertTreeMatchesExcept(t, sourcePath, destPath, nil)
			continue
		}
		if readTestFile(t, sourcePath) != readTestFile(t, destPath) {
			t.Errorf("%s was not byte-copied", name)
		}
	}
}
