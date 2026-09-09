package setupjourney

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// This opt-in test uses real GitHub sources and release downloads in temporary
// plugin stores. No development override, application launch, service process,
// real MCP/skill registration, or user workspace is involved.
func TestReviewedIntegrationPublishedRelease(t *testing.T) {
	if os.Getenv("ORI_TEST_REVIEWED_INTEGRATION_RELEASE") != "1" {
		t.Skip("set ORI_TEST_REVIEWED_INTEGRATION_RELEASE=1 for GitHub-backed installation checks")
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("published reviewed release supports darwin/arm64 only")
	}
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok || !entry.ReleaseReady || entry.Source() == "" {
		t.Fatal("reviewed release is not enabled")
	}
	for _, legacy := range []bool{false, true} {
		t.Run("existing-official-install="+boolString(legacy), func(t *testing.T) {
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
				if installed.Version != entry.ExpectedVersion {
					t.Fatalf("official default branch moved to %q; review this external fixture before rerunning", installed.Version)
				}
				if err := manager.SetEnabled(entry.PluginID, true); err != nil {
					t.Fatal(err)
				}
			}
			service := integrationServiceForReplacementTest(t, NewReviewedIntegrationAdapter(manager))
			ctx := context.Background()
			journey, err := service.Read(ctx, "local", "")
			if err != nil {
				t.Fatal(err)
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
			if err != nil || len(installed) != 1 || installed[0].Source != entry.Source() || !installed[0].Enabled {
				t.Fatalf("published install identity/enablement: %#v err=%v", installed, err)
			}
			artifacts := installed[0].ResolvedArtifacts
			if len(artifacts) != 1 || !artifacts[0].Available || artifacts[0].Size != 8780098 ||
				artifacts[0].SHA256 != "2bbf6b77418119cb21e827a407c8d5886e3effdb593ec0ad274e20d7d69c2ca9" {
				t.Fatalf("unexpected verified release artifact: %#v", artifacts)
			}
			info, err := os.Stat(artifacts[0].ManagedPath)
			if err != nil || info.Size() != artifacts[0].Size || info.Mode().Perm()&0o100 == 0 {
				t.Fatalf("managed artifact missing size/executable mode: %v err=%v", info, err)
			}
		})
	}
}

type inertReleaseComponents struct{}

func (inertReleaseComponents) AddServer(mcp.ServerConfig) error          { return nil }
func (inertReleaseComponents) RemoveServer(string) error                 { return nil }
func (inertReleaseComponents) InstallSkill(string, string, string) error { return nil }
func (inertReleaseComponents) RemoveSkill(string, string) error          { return nil }
