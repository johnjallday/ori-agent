package plugin

import (
	"path/filepath"
	"testing"
)

// The inspected fingerprint is the one the replacement guard is later asked
// about and the one the installed record ends up with, for both the reviewed
// (explicit source) and the recorded-source update paths.
func TestInspectedReplacementTargetMatchesWhatTheReplacementInstalls(t *testing.T) {
	oldRoot := makeClaudeBundle(t)
	manager := NewManager(&fakeRegistrar{}, t.TempDir(), "")
	installed, err := manager.Install(oldRoot, FormatClaude, func(TrustReport) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	var guarded string
	manager.SetReplacementGuard(func(_ InstalledPlugin, _ string, nextFingerprint string) error {
		guarded = nextFingerprint
		return nil
	})

	candidate := makeClaudeBundle(t)
	writeFile(t, filepath.Join(candidate, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.5.0"}`)
	target, err := manager.InspectReplacementTarget(installed.Name, candidate, FormatClaude)
	if err != nil || target.Descriptor.Version != "0.5.0" || len(target.Fingerprint) != 64 {
		t.Fatalf("reviewed target = %+v, %v", target, err)
	}
	if current, _, _ := manager.store.Get(installed.Name); current.Version != installed.Version || current.Generation != installed.Generation {
		t.Fatalf("inspection changed the installed record: %+v", current)
	}
	updated, err := manager.UpdateFromSource(installed.Name, candidate, FormatClaude, func(TrustReport) bool { return true })
	if err != nil || guarded != target.Fingerprint || updated.ComponentFingerprint != target.Fingerprint {
		t.Fatalf("reviewed replacement fingerprint: inspected %q guarded %q installed %q (%v)", target.Fingerprint, guarded, updated.ComponentFingerprint, err)
	}

	writeFile(t, filepath.Join(candidate, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.6.0"}`)
	target, err = manager.InspectUpdateTarget(installed.Name)
	if err != nil || target.Descriptor.Version != "0.6.0" {
		t.Fatalf("recorded-source target = %+v, %v", target, err)
	}
	updated, err = manager.Update(installed.Name, func(TrustReport) bool { return true })
	if err != nil || updated.Version != "0.6.0" || guarded != target.Fingerprint || updated.ComponentFingerprint != target.Fingerprint {
		t.Fatalf("recorded-source replacement fingerprint: inspected %q guarded %q installed %q (%v)", target.Fingerprint, guarded, updated.ComponentFingerprint, err)
	}

	other := makeClaudeBundle(t)
	writeFile(t, filepath.Join(other, ".claude-plugin", "plugin.json"), `{"name":"other","version":"1.0.0"}`)
	if _, err := manager.InspectReplacementTarget(installed.Name, other, FormatClaude); err == nil {
		t.Fatal("a source naming another plugin was accepted as a replacement target")
	}
	if _, err := manager.InspectReplacementTarget("missing", candidate, FormatClaude); err == nil {
		t.Fatal("a missing plugin had a replacement target")
	}
	if _, err := manager.InspectUpdateTarget("missing"); err == nil {
		t.Fatal("a missing plugin had an update target")
	}
}
