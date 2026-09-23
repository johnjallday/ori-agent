package setupjourney

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/johnjallday/ori-agent/internal/integrationrelease"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// This opt-in test uses real GitHub sources and release downloads in temporary
// plugin stores. No development override, application launch, service process,
// real MCP/skill registration, or user workspace is involved.
//
// The fallback run forces the floor with an unreachable loopback releases API
// and pins the fallback release's artifact size and digest. The live run
// resolves the latest stable release and proves the installed record carries
// that release's exact commit and a digest matching its own manifest.
func TestReviewedIntegrationPublishedRelease(t *testing.T) {
	if os.Getenv("ORI_TEST_REVIEWED_INTEGRATION_RELEASE") != "1" {
		t.Skip("set ORI_TEST_REVIEWED_INTEGRATION_RELEASE=1 for GitHub-backed installation checks")
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("published reviewed release supports darwin/arm64 only")
	}
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok || !entry.ReleaseReady || entry.FallbackSource() == "" {
		t.Fatal("reviewed release is not enabled")
	}
	runs := []struct {
		name     string
		releases *integrationrelease.Resolver
	}{
		{"fallback", integrationrelease.New("http://127.0.0.1:9", nil, nil, nil)},
		{"live", integrationrelease.New(integrationrelease.DefaultAPIBase, nil, nil, nil)},
	}
	for _, run := range runs {
		target := run.releases.Resolve(context.Background(), entry)
		if run.name == "fallback" && (!target.Fallback || target.Source != entry.FallbackSource()) {
			t.Fatalf("unreachable releases API did not fall back to the floor: %#v", target)
		}
		if run.name == "live" && (target.Fallback || !reviewedintegration.AtLeast(target.Version, entry.MinimumVersion) ||
			!entry.IsPinnedSource(target.Source)) {
			t.Fatalf("live resolution did not name a release at or above the floor (set GITHUB_TOKEN if rate limited): %#v", target)
		}
		for _, legacy := range []bool{false, true} {
			t.Run(run.name+"/existing-official-install="+boolString(legacy), func(t *testing.T) {
				installPublishedRelease(t, entry, run.releases, target, run.name == "fallback", legacy)
			})
		}
	}
}

func installPublishedRelease(t *testing.T, entry reviewedintegration.Entry, releases *integrationrelease.Resolver, target integrationrelease.Resolution, fallback, legacy bool) {
	t.Helper()
	root := t.TempDir()
	components := inertReleaseComponents{}
	manager := plugin.NewManager(components, components, filepath.Join(root, "plugins"), filepath.Join(root, "sources"))
	if legacy {
		// Reproduce the ordinary Plugins-page installation rather than
		// forging a source in the installed record.
		installed, err := manager.Install(entry.SourceRepository+".git", entry.SourceFormat, func(plugin.TrustReport) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		if order, comparable := reviewedintegration.CompareVersions(installed.Version, target.Version); !comparable || order > 0 {
			t.Fatalf("official default branch is at %q, above the resolved release %q; review this external fixture before rerunning", installed.Version, target.Version)
		}
		if err := manager.SetEnabled(entry.PluginID, true); err != nil {
			t.Fatal(err)
		}
	}
	service := integrationServiceForReplacementTest(t, NewReviewedIntegrationAdapter(manager, releases))
	ctx := context.Background()
	journey, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if integration := journey.Steps[0].Integration; integration == nil ||
		integration.ExpectedVersion != target.Version || integration.ReleaseChecked == fallback {
		t.Fatalf("install step does not target the resolved release: %#v", journey.Steps[0].Integration)
	}
	reviewAction, commitAction := ActionReviewInstall, ActionInstall
	if legacy {
		reviewAction, commitAction = ActionReviewUpdate, ActionUpdate
	}
	apply := func(reviewAction, commitAction ActionID, revision int64) *JourneyProjection {
		t.Helper()
		review, err := service.Mutate(ctx, "local", journey.RunID, reviewAction, ActionMutation{
			IfRevision: revision, IdempotencyKey: string(reviewAction), Input: json.RawMessage(`{}`),
		})
		if err != nil || review.Review == nil || review.Review.Integration.Trust == nil {
			t.Fatalf("published release review: %#v err=%v", review, err)
		}
		result, err := service.Mutate(ctx, "local", journey.RunID, commitAction, ActionMutation{
			IfRevision: revision, IdempotencyKey: string(commitAction), ReviewToken: review.Review.Token, Input: json.RawMessage(`{}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		return result.Journey
	}
	journey = apply(reviewAction, commitAction, journey.StateRevision)
	if !legacy {
		if journey.Steps[0].Integration.Enabled || !journey.Steps[0].Integration.Verified {
			t.Fatal("fresh install must be verified but disabled until separate consent")
		}
		journey = apply(ActionReviewEnable, ActionEnable, journey.StateRevision)
	}
	if journey.Steps[0].Status != StepComplete || !journey.Steps[0].Integration.Verified || journey.Steps[0].Integration.DevelopmentCopy {
		t.Fatalf("published install did not unlock setup: %#v", journey.Steps[0])
	}
	installed, err := manager.List()
	if err != nil || len(installed) != 1 || installed[0].Source != target.Source ||
		installed[0].Version != target.Version || !installed[0].Enabled {
		t.Fatalf("published install identity/enablement: %#v err=%v (want %s)", installed, err, target.Source)
	}
	artifacts := installed[0].ResolvedArtifacts
	if len(artifacts) != 1 || !artifacts[0].Available {
		t.Fatalf("unexpected verified release artifact: %#v", artifacts)
	}
	if fallback && (artifacts[0].Size != 8780098 ||
		artifacts[0].SHA256 != "1f5ab0f061bddb739461ececc088900ec8f4cee47154ea631bb05ebfdfdad08e") {
		t.Fatalf("fallback release artifact changed: %#v", artifacts)
	}
	// Whatever the release, the managed bytes must hash to the digest its own
	// manifest declares at the installed commit.
	declared := manifestArtifactDigest(t, installed[0], runtime.GOOS, runtime.GOARCH)
	if actual := fileSHA256(t, artifacts[0].ManagedPath); actual != declared || artifacts[0].SHA256 != declared {
		t.Fatalf("artifact digest = %s (recorded %s), manifest declares %s", actual, artifacts[0].SHA256, declared)
	}
	info, err := os.Stat(artifacts[0].ManagedPath)
	if err != nil || info.Size() != artifacts[0].Size || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("managed artifact missing size/executable mode: %v err=%v", info, err)
	}
}

func manifestArtifactDigest(t *testing.T, installed plugin.InstalledPlugin, goos, goarch string) string {
	t.Helper()
	if installed.WorkspaceSurfaces == nil {
		t.Fatal("installed release has no surface contribution")
	}
	var digest string
	for _, service := range installed.WorkspaceSurfaces.Services {
		for _, artifact := range service.Artifacts {
			if artifact.OS == goos && artifact.Arch == goarch {
				if digest != "" {
					t.Fatal("manifest declares more than one artifact for this platform")
				}
				digest = artifact.SHA256
			}
		}
	}
	if digest == "" {
		t.Fatal("manifest declares no artifact for this platform")
	}
	return digest
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path) // #nosec G304 -- managed artifact path returned by the plugin installer
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type inertReleaseComponents struct{}

func (inertReleaseComponents) AddServer(mcp.ServerConfig) error          { return nil }
func (inertReleaseComponents) RemoveServer(string) error                 { return nil }
func (inertReleaseComponents) InstallSkill(string, string, string) error { return nil }
func (inertReleaseComponents) RemoveSkill(string, string) error          { return nil }
