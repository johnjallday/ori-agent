package reviewedintegration

import (
	"os"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
)

// TestReviewedHomeProviderPublishedRelease loads a real Home provider release
// and runs it through the same check the wizard install and the update hook
// use. Opt-in because it clones from GitHub:
//
//	ORI_REVIEWED_HOME_PROVIDER_CANDIDATE=1 go test ./internal/reviewedintegration -run TestReviewedHomeProviderPublishedRelease -v
//
// "1" checks the reviewed fallback commit; any other value is used as the
// source, e.g. <repository>#sha=<commit> for a newer release.
func TestReviewedHomeProviderPublishedRelease(t *testing.T) {
	candidate := os.Getenv("ORI_REVIEWED_HOME_PROVIDER_CANDIDATE")
	if candidate == "" {
		t.Skip("reviewed Home provider candidate not requested")
	}
	provider, ok := HomeProviderFor("music-project-management")
	if !ok {
		t.Fatal("reviewed Home provider is missing")
	}
	source := candidate
	if candidate == "1" {
		source = provider.ReleaseEntry().FallbackSource()
	}
	descriptor, err := plugin.Load(source, t.TempDir(), provider.SourceFormat)
	if err != nil {
		t.Fatalf("load reviewed Home provider release: %v", err)
	}
	if !AtLeast(descriptor.Version, provider.MinimumVersion) {
		t.Fatalf("release %q is below the reviewed floor %q", descriptor.Version, provider.MinimumVersion)
	}
	report := plugin.BuildTrustReport(descriptor)
	if got := provider.CheckRelease(descriptor.Version, source, descriptor, report, nil); got != ReleaseLoadable {
		t.Fatalf("published release %s is not loadable as the reviewed Home: %v (%#v)", descriptor.Version, got, descriptor.WorkspaceSurfaces)
	}
	t.Logf("verified %s %s from %s: skills=%v homes=%v", descriptor.Name, descriptor.Version, source, report.Skills, report.AssistantProgramHomes)
}
