package reviewedintegration

import (
	"os"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
)

// TestReviewedCandidateMeetsTheFloor is the registry half of the opt-in
// TestReviewedCandidateHostContract in internal/plugin: a candidate release is
// acceptable at any version at or above the reviewed floor.
func TestReviewedCandidateMeetsTheFloor(t *testing.T) {
	source := os.Getenv("ORI_REVIEWED_PLUGIN_CANDIDATE")
	if source == "" {
		t.Skip("external reviewed candidate checkout not supplied")
	}
	entry, ok := ForPlugin("reaper-plugin")
	if !ok {
		t.Fatal("reviewed integration entry is missing")
	}
	descriptor, err := plugin.Load(source, t.TempDir(), plugin.FormatClaude)
	if err != nil {
		t.Fatalf("load reviewed candidate: %v", err)
	}
	if descriptor.Name != entry.PluginID || !AtLeast(descriptor.Version, entry.MinimumVersion) {
		t.Fatalf("candidate %s %q is not at or above the reviewed floor %q", descriptor.Name, descriptor.Version, entry.MinimumVersion)
	}
	if descriptor.WorkspaceSurfaces == nil || descriptor.WorkspaceSurfaces.Version != descriptor.Version {
		t.Fatalf("candidate surface contribution version does not match the manifest: %#v", descriptor.WorkspaceSurfaces)
	}
}
