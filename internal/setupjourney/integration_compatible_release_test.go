package setupjourney

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/integrationrelease"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// hostFeatureRefusal is the error a manager returns when a release's manifest
// names a host feature this build does not advertise. It is the exact refusal
// ParseSurfaceContribution produces, which is what makes a plugin release that
// adopts a new host feature unloadable on an older Ori.
func hostFeatureRefusal() error {
	contribution := &plugin.SurfaceContribution{
		SchemaVersion: 1, Name: "reaper-plugin", Version: "0.9.0",
		Protocol:             plugin.ProtocolRange{Min: 1, Max: 1},
		RequiresHostFeatures: []string{"a_feature_this_build_does_not_have"},
		Capabilities:         []plugin.ContributedCapability{{ID: "example"}},
	}
	err := contribution.ValidateForHost(1, []string{plugin.HostFeatureAssistantProgramV1})
	if err == nil {
		panic("the host-feature gate accepted an unknown feature")
	}
	return err
}

// unsupportedReleaseFixture wires a repository whose newest release cannot be
// loaded by this build and whose previous release can.
func unsupportedReleaseFixture(t *testing.T) (
	*fakeReviewedIntegrationManager, *stubReleases, reviewedintegration.Entry, ReadScope,
	integrationrelease.Resolution, integrationrelease.Resolution,
) {
	t.Helper()
	entry, descriptor, _, scope := readyIntegrationFixture(t)
	newest := latestRelease(entry, "0.9.0", "f")
	loadable := latestRelease(entry, "0.6.0", "e")
	loadableDescriptor, loadableReport := releaseDescriptor(descriptor, loadable.Version, loadable.Source)
	manager := &fakeReviewedIntegrationManager{
		descriptor: loadableDescriptor, report: loadableReport,
		perSource: map[string]fakeInspection{newest.Source: {err: hostFeatureRefusal()}},
	}
	releases := &stubReleases{candidates: []integrationrelease.Resolution{
		newest, loadable, integrationrelease.Floor(entry),
	}}
	return manager, releases, entry, scope, newest, loadable
}

// The gap this group closes: with nothing older to fall back to, a release this
// build cannot load leaves the step blocked. That is correct — there is no
// other release — and it is exactly what used to happen even when an older,
// loadable release existed, which is what the walk below fixes.
func TestReviewedIntegrationBlocksWhenTheOnlyReleaseNeedsANewerHost(t *testing.T) {
	entry, _, _, scope := readyIntegrationFixture(t)
	newest := latestRelease(entry, "0.9.0", "f")
	manager := &fakeReviewedIntegrationManager{
		perSource:  map[string]fakeInspection{newest.Source: {err: hostFeatureRefusal()}},
		inspectErr: hostFeatureRefusal(),
	}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	adapter.releases = &stubReleases{candidates: []integrationrelease.Resolution{newest}}

	read, err := adapter.Read(context.Background(), scope)
	if err != nil || read.BlockedReason != ReasonIntegrationUnsupported {
		t.Fatalf("read = %#v err=%v, want the step blocked as unsupported", read, err)
	}
	if len(read.AvailableActions) != 0 {
		t.Fatalf("a step that cannot install anything offered actions: %v", read.AvailableActions)
	}
}

// PRD §9.1: the newest release is not always the newest release this build can
// load. Setup installs the newest one it can, and says why it is behind.
func TestReviewedIntegrationOffersTheNewestLoadableRelease(t *testing.T) {
	manager, releases, entry, scope, newest, loadable := unsupportedReleaseFixture(t)
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	adapter.releases = releases

	read, err := adapter.Read(context.Background(), scope)
	if err != nil || read.BlockedReason != "" || !containsAction(read.AvailableActions, ActionReviewInstall) {
		t.Fatalf("read = %#v err=%v, want an install offer", read, err)
	}
	integration := read.Integration
	if integration.ExpectedVersion != loadable.Version {
		t.Fatalf("offered version = %q, want the newest loadable release %q", integration.ExpectedVersion, loadable.Version)
	}
	if integration.ReleaseNote != ReasonIntegrationNewerReleaseUnsupported ||
		integration.NewestUnsupportedVersion != newest.Version {
		t.Fatalf("the projection does not say why an older release is on offer: %#v", integration)
	}
	if !validIntegrationProjection(integration) {
		t.Fatalf("the projection carrying the note is not valid: %#v", integration)
	}
	// The newest was tried first, and the trust material on offer belongs to
	// the release that is actually being installed.
	if len(manager.inspectSources) != 2 ||
		manager.inspectSources[0] != newest.Source || manager.inspectSources[1] != loadable.Source {
		t.Fatalf("inspected = %v, want the newest first then the loadable one", manager.inspectSources)
	}
	if integration.Trust == nil || trustReportDigest(*integration.Trust) != trustReportDigest(manager.report) {
		t.Fatalf("trust report = %#v, want the offered release's", integration.Trust)
	}
}

// The digest binds to the release actually offered, and to the newer one that
// was skipped: both are part of what the review disclosed.
func TestReviewedIntegrationDigestBindsToTheOfferedRelease(t *testing.T) {
	manager, releases, entry, scope, _, loadable := unsupportedReleaseFixture(t)
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	adapter.releases = releases
	skipped, err := adapter.Read(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}

	// The same release offered without anything skipped is a different state.
	direct := &fakeReviewedIntegrationManager{descriptor: manager.descriptor, report: manager.report}
	directAdapter := newReviewedIntegrationAdapter(direct, integrationResolver(entry), "darwin/arm64")
	directAdapter.releases = &stubReleases{candidates: []integrationrelease.Resolution{loadable}}
	plain, err := directAdapter.Read(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Integration.ExpectedVersion != skipped.Integration.ExpectedVersion {
		t.Fatalf("fixture mismatch: %q vs %q", plain.Integration.ExpectedVersion, skipped.Integration.ExpectedVersion)
	}
	if plain.Integration.StateRevision == skipped.Integration.StateRevision {
		t.Fatal("the digest ignored the skipped release, so a review would not go stale when it changes")
	}
	if plain.Integration.ReleaseNote != "" || plain.Integration.NewestUnsupportedVersion != "" {
		t.Fatalf("an unskipped offer carried a note: %#v", plain.Integration)
	}
}

// Every candidate unloadable ends at the floor, and the floor's own refusal is
// the answer: this build cannot run the integration at all.
func TestReviewedIntegrationEndsAtTheFloorWhenNoReleaseLoads(t *testing.T) {
	entry, _, _, scope := readyIntegrationFixture(t)
	newest := latestRelease(entry, "0.9.0", "f")
	middle := latestRelease(entry, "0.8.0", "e")
	floor := integrationrelease.Floor(entry)
	manager := &fakeReviewedIntegrationManager{inspectErr: hostFeatureRefusal()}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	adapter.releases = &stubReleases{candidates: []integrationrelease.Resolution{newest, middle, floor}}

	read, err := adapter.Read(context.Background(), scope)
	if err != nil || read.BlockedReason != ReasonIntegrationUnsupported {
		t.Fatalf("read = %#v err=%v, want the floor's refusal", read, err)
	}
	if len(manager.inspectSources) != 3 || manager.inspectSources[2] != floor.Source {
		t.Fatalf("inspected = %v, want the walk to end at the floor", manager.inspectSources)
	}
	if read.Integration.ExpectedVersion != floor.Version {
		t.Fatalf("expected version = %q, want the floor %q", read.Integration.ExpectedVersion, floor.Version)
	}
}

// A refusal that is not about what this build can load must stop the walk.
// Sliding to an older release would hide an unreachable source or a plugin
// that is no longer who it says it is.
func TestReviewedIntegrationNeverSkipsANonCompatibilityRefusal(t *testing.T) {
	entry, descriptor, _, scope := readyIntegrationFixture(t)
	newest := latestRelease(entry, "0.9.0", "f")
	loadable := latestRelease(entry, "0.6.0", "e")
	loadableDescriptor, loadableReport := releaseDescriptor(descriptor, loadable.Version, loadable.Source)

	cases := map[string]struct {
		newest fakeInspection
		want   ReasonCode
	}{
		"unreachable source": {
			newest: fakeInspection{err: errors.New("could not clone the repository")},
			want:   ReasonOwnerUnavailable,
		},
		"identity mismatch": {
			newest: func() fakeInspection {
				impostor, report := releaseDescriptor(descriptor, newest.Version, newest.Source)
				impostor.Name = "someone-elses-plugin"
				return fakeInspection{descriptor: impostor, report: report}
			}(),
			want: ReasonIntegrationIdentityMismatch,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			manager := &fakeReviewedIntegrationManager{
				descriptor: loadableDescriptor, report: loadableReport,
				perSource: map[string]fakeInspection{newest.Source: testCase.newest},
			}
			adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
			adapter.releases = &stubReleases{candidates: []integrationrelease.Resolution{
				newest, loadable, integrationrelease.Floor(entry),
			}}

			read, err := adapter.Read(context.Background(), scope)
			if err != nil || read.BlockedReason != testCase.want {
				t.Fatalf("read = %#v err=%v, want %q", read, err, testCase.want)
			}
			if len(manager.inspectSources) != 1 {
				t.Fatalf("the walk continued past a refusal it must not skip: %v", manager.inspectSources)
			}
		})
	}
}

// One repository with a long release history must not turn a step read into an
// unbounded number of inspections.
func TestReviewedIntegrationCapsHowManyReleasesOneReadInspects(t *testing.T) {
	entry, _, _, scope := readyIntegrationFixture(t)
	manager := &fakeReviewedIntegrationManager{inspectErr: hostFeatureRefusal()}
	candidates := make([]integrationrelease.Resolution, 0, 12)
	for index := 0; index < 11; index++ {
		candidates = append(candidates, latestRelease(entry, "0.9."+strings.Repeat("9", index+1), "f"))
	}
	candidates = append(candidates, integrationrelease.Floor(entry))
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	adapter.releases = &stubReleases{candidates: candidates}

	if _, err := adapter.Read(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	if manager.inspections > integrationrelease.MaxCandidates {
		t.Fatalf("one read inspected %d releases, want at most %d", manager.inspections, integrationrelease.MaxCandidates)
	}
}
