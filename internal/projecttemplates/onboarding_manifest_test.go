package projecttemplates

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetOnboarding(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	writeFile(t, filepath.Join(libDir, "tpl", ManifestFileName),
		`{"name":"Tpl","tags":["music"],"custom_field":42}`)

	spec := json.RawMessage(`{"version":"1","fields":[{"id":"bpm","label":"BPM","type":"number"}],"completion":{"type":"none"}}`)

	tpl, err := SetOnboarding(libDir, "tpl", spec)
	if err != nil {
		t.Fatalf("SetOnboarding: %v", err)
	}
	if !tpl.HasOnboarding() {
		t.Fatalf("template should report onboarding after set")
	}

	// The onboarding block is written, and every other key survives.
	data, err := os.ReadFile(filepath.Join(libDir, "tpl", ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"onboarding"`, `"bpm"`, `"custom_field"`, `"tags"`, `"name"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("manifest missing %s after set: %s", want, data)
		}
	}

	// Clearing removes only the onboarding key; unknown keys remain.
	tpl, err = SetOnboarding(libDir, "tpl", nil)
	if err != nil {
		t.Fatalf("SetOnboarding(clear): %v", err)
	}
	if tpl.HasOnboarding() {
		t.Fatalf("template should report no onboarding after clear")
	}
	data, err = os.ReadFile(filepath.Join(libDir, "tpl", ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"onboarding"`) {
		t.Errorf("onboarding key should be removed: %s", data)
	}
	if !strings.Contains(string(data), `"custom_field"`) {
		t.Errorf("unknown key dropped on clear: %s", data)
	}

	// A "null" payload clears as well.
	if _, err := SetOnboarding(libDir, "tpl", json.RawMessage("null")); err != nil {
		t.Fatalf("SetOnboarding(null): %v", err)
	}

	// Unknown template id is rejected.
	if _, err := SetOnboarding(libDir, "missing", spec); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("unknown template = %v, want ErrTemplateNotFound", err)
	}
}
