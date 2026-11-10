package filejanitor

import (
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestReadReviewGroups_SavedOnlyNoCuratorOrManagedFolderIO(t *testing.T) {
	s, ws, root := curatorWelcomeFixture(t)
	state, err := s.store.LoadScanState(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.store.LoadSettings(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	ws.AgentInstances = nil
	if err := os.Rename(root, root+"-away"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, err := s.ReadReviewGroups(t.Context(), "local", ws.ID, "latest")
		if err != nil || got.Counts.Candidates != 2 || got.Counts.Filing != 1 || got.Counts.Questions != 1 {
			t.Fatalf("saved group read: %+v, %v", got, err)
		}
	}
	after, err := s.store.LoadScanState(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	freshSettings, err := s.store.LoadSettings(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, after) || !reflect.DeepEqual(settings, freshSettings) {
		t.Fatal("GET created draft, approval, scan, or changed saved state")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("read recreated the managed root")
	}
}

func TestReadReviewGroups_ScopeAndLegacyPolicy(t *testing.T) {
	for _, mode := range []string{"owner", "removed", "reinstall", "relink", "revoked", "grant", "legacy", "legacy reinstall", "foreign candidate"} {
		t.Run(mode, func(t *testing.T) {
			s, ws, _ := curatorWelcomeFixture(t)
			owner := "local"
			switch mode {
			case "owner":
				owner = "foreign"
			case "removed":
				ws.RemoveInstalledCapability(workspace.CapabilityFileJanitor)
			case "reinstall":
				ws.InstalledCapabilities[0].InstalledAt = time.Now().Add(time.Hour)
			case "relink":
				_, err := s.store.UpdateSettings(ws.ID, func(settings *JanitorSettings) error { settings.RootID = "new-root"; return nil })
				if err != nil {
					t.Fatal(err)
				}
			case "revoked":
				_, err := s.store.UpdateSettings(ws.ID, func(settings *JanitorSettings) error { settings.AccessRevokedAt = time.Now(); return nil })
				if err != nil {
					t.Fatal(err)
				}
			case "grant":
				ws.MCPBindings[0].Enabled = false
			case "legacy", "legacy reinstall":
				_, err := s.store.UpdateScanState(ws.ID, func(state *ScanState) error {
					state.Batches[0].RootID, state.Batches[0].CapabilityInstalledAt = "", nil
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				ws.InstalledCapabilities[0].InstalledAt = time.Now().Add(time.Hour)
				if mode == "legacy" {
					ws.InstalledCapabilities[0].Source = workspace.InstallSourceLegacyMigration
				}
			case "foreign candidate":
				_, err := s.store.UpdateScanState(ws.ID, func(state *ScanState) error { state.Candidates[0].BatchID = "different-batch"; return nil })
				if err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.ReadReviewGroups(t.Context(), owner, ws.ID, "latest")
			if mode == "legacy" {
				if err != nil || got.Counts.Candidates != 2 {
					t.Fatalf("legacy migration readability lost: %+v %v", got, err)
				}
			} else if !errors.Is(err, ErrReviewUnavailable) || got.BatchID != "" {
				t.Fatalf("stale scope served: %+v %v", got, err)
			}
		})
	}
}

func TestReadReviewGroups_KnownEmptyVersusCorruptOrFutureState(t *testing.T) {
	s, ws, _ := curatorWelcomeFixture(t)
	path, err := s.store.scanStatePath(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, contents := range []string{`{`, `{"schema_version":999,"workspace_id":"ws-1"}`} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := s.ReadReviewGroups(t.Context(), "local", ws.ID, "latest"); !errors.Is(err, ErrReviewUnavailable) || got.BatchID != "" {
			t.Fatalf("unknown state presented as empty success: %+v %v", got, err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadReviewGroups(t.Context(), "local", ws.ID, "latest")
	if err != nil || got.BatchID != "" || got.Groups == nil || len(got.Groups) != 0 || got.Counts.Candidates != 0 {
		t.Fatalf("never scanned must be known empty: %+v %v", got, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("empty GET persisted state")
	}
}

func TestReadReviewGroups_ConcreteBatchStaysReachableWhenNewerArrives(t *testing.T) {
	s, ws, root := curatorWelcomeFixture(t)
	first, err := s.ReadReviewGroups(t.Context(), "local", ws.ID, "latest")
	if err != nil {
		t.Fatal(err)
	}
	agedFile(t, root, "new-photo.png", 20)
	if _, created, err := s.ScanNow(ws.ID, ScanSourceManual); err != nil || !created {
		t.Fatalf("new scan: %v %v", created, err)
	}
	old, err := s.ReadReviewGroups(t.Context(), "local", ws.ID, first.BatchID)
	if err != nil || !reflect.DeepEqual(first, old) {
		t.Fatalf("new batch changed pinned proposals: %+v %v", old, err)
	}
	latest, err := s.ReadReviewGroups(t.Context(), "local", ws.ID, "latest")
	if err != nil || latest.BatchID == first.BatchID || latest.Counts.Candidates != 1 {
		t.Fatalf("new batch not discoverable: %+v %v", latest, err)
	}
}
