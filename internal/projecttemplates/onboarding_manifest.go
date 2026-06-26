package projecttemplates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SetOnboarding writes (or clears) the `onboarding` block in a template's
// template.json, preserving every other key. A nil/empty/`null` value clears the
// key (the template falls back to having no onboarding).
//
// The block is stored as-is — this package never interprets it, keeping the
// file-copy engine domain-blind. Callers (the templateonboarding-aware HTTP
// layer) validate the spec with ParseSpec + Validate before calling here.
func SetOnboarding(libDir, id string, onboarding json.RawMessage) (Template, error) {
	tpl, err := FindLibraryTemplate(libDir, id)
	if err != nil {
		return Template{}, err
	}

	// manifestPath is the resolved library template's folder (FindLibraryTemplate
	// rejects ids outside the library) joined with the fixed ManifestFileName.
	manifestPath := filepath.Join(tpl.Path, ManifestFileName)
	raw := map[string]any{}
	if data, err := os.ReadFile(manifestPath); err == nil { // #nosec G304 -- manifestPath is libDir/<validated id>/template.json, not user-controlled
		// A malformed manifest is replaced rather than failing the edit.
		_ = json.Unmarshal(data, &raw)
	}

	trimmed := bytes.TrimSpace(onboarding)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		delete(raw, "onboarding")
	} else {
		var v any
		if err := json.Unmarshal(trimmed, &v); err != nil {
			return Template{}, fmt.Errorf("onboarding is not valid JSON: %w", err)
		}
		raw["onboarding"] = v
	}

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return Template{}, fmt.Errorf("failed to encode manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o640); err != nil { // #nosec G304 G306 -- manifestPath is libDir/<validated id>/template.json; 0o640 matches the package's manifest-write convention
		return Template{}, fmt.Errorf("failed to write manifest: %w", err)
	}
	return newTemplate(tpl.Path), nil
}
