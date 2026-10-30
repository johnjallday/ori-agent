package filejanitor

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func curatorWelcomeFixture(t *testing.T) (*Service, *workspace.Workspace, string) {
	t.Helper()
	s, root := configuredService(t)
	ws, err := s.workspaces.Get("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	ws.OwnerUserID = "local"
	ws.AgentInstances = []workspace.AgentInstance{{ID: "curator", Name: "Organiser", EntryPoint: true}}
	ws.InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace, OwnedResources: []workspace.CapabilityResource{{Kind: workspace.ResourceCompanionAgent, ID: "curator"}}}}
	agedFile(t, root, "report.pdf", 100)
	agedFile(t, root, "uncertain.qqq", 100)
	if _, created, err := s.ScanNow(ws.ID, ScanSourceManual); err != nil || !created {
		t.Fatalf("fixture scan: %v %v", created, err)
	}
	return s, ws, root
}

func TestCuratorWelcome_ReadsSavedBatchWithoutInspectingManagedFolder(t *testing.T) {
	s, ws, root := curatorWelcomeFixture(t)
	before, err := s.store.LoadScanState(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.store.LoadSettings(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The inbox is deliberately unavailable. A Status/readiness call or a
	// new scan would fail here; this projection reads only Ori's saved records.
	if err := os.Rename(root, root+"-away"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, err := s.ReadCuratorWelcome(t.Context(), "local", ws.ID, "curator")
		if err != nil || !got.Configured || got.Pending != 2 || got.NeedsReview != 1 || got.BatchCompletedAt.IsZero() || got.ReadAt.IsZero() {
			t.Fatalf("snapshot %+v %v", got, err)
		}
		raw, _ := json.Marshal(got)
		for _, private := range []string{root, "report.pdf", "uncertain.qqq", "Fingerprint", "Approval"} {
			if strings.Contains(string(raw), private) {
				t.Fatalf("private batch metadata escaped: %s", raw)
			}
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
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(settings, freshSettings) {
		t.Fatal("welcome changed filing state")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("welcome recreated the managed folder")
	}
}

func TestCuratorWelcome_OnlyOffersRemainingPendingCandidates(t *testing.T) {
	s, ws, _ := curatorWelcomeFixture(t)
	if _, err := s.store.UpdateScanState(ws.ID, func(state *ScanState) error {
		for i := range state.Candidates {
			if state.Candidates[i].NeedsReview {
				state.Candidates[i].State = CandidateApplied
			}
		}
		// Do not trust an old cached summary, even when the batch still has work.
		state.Batches[0].Summary.NeedsReview = 99
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadCuratorWelcome(t.Context(), "local", ws.ID, "curator")
	if err != nil || got.Pending != 1 || got.NeedsReview != 0 {
		t.Fatalf("offered resolved items: %+v %v", got, err)
	}
	if _, err := s.store.UpdateScanState(ws.ID, func(state *ScanState) error {
		for i := range state.Candidates {
			state.Candidates[i].State = CandidateStale
		}
		state.Batches[0] = SummarizeBatch(state.Batches[0], state.Candidates)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadCuratorWelcome(t.Context(), "local", ws.ID, "curator")
	if err != nil || got.Pending != 0 || got.NeedsReview != 0 {
		t.Fatalf("offered stale batch: %+v %v", got, err)
	}
}

func TestCuratorWelcome_RequiresCurrentOwnerCuratorAndReadGrant(t *testing.T) {
	for _, mode := range []string{"owner", "instance", "namesake", "specialist", "ambiguous", "removed", "revoked", "disabled binding", "empty tools", "different root grant", "changed reference", "missing settings"} {
		t.Run(mode, func(t *testing.T) {
			s, ws, _ := curatorWelcomeFixture(t)
			owner, instance := "local", "curator"
			switch mode {
			case "owner":
				owner = "other"
			case "instance":
				instance = "replacement"
			case "namesake":
				ws.InstalledCapabilities = nil
				ws.AgentInstances[0].Name = "File Curator"
			case "specialist":
				ws.AgentInstances[0].EntryPoint = false
				ws.AgentInstances = append(ws.AgentInstances, workspace.AgentInstance{ID: "lead", Name: "Lead", EntryPoint: true})
			case "ambiguous":
				ws.AgentInstances = append(ws.AgentInstances, workspace.AgentInstance{ID: "namesake", Name: "Organiser"})
			case "removed":
				ws.Status = workspace.StatusTrashed
			case "revoked":
				if _, err := s.store.UpdateSettings(ws.ID, func(settings *JanitorSettings) error { settings.AccessRevokedAt = time.Now(); return nil }); err != nil {
					t.Fatal(err)
				}
			case "disabled binding":
				for i := range ws.MCPBindings {
					ws.MCPBindings[i].Enabled = false
				}
			case "empty tools":
				for i := range ws.MCPBindings {
					ws.MCPBindings[i].AllowedTools = []string{}
				}
			case "different root grant":
				for i := range ws.MCPBindings {
					ws.MCPBindings[i].Config = map[string]any{"roots": []string{"/unapproved"}}
				}
			case "changed reference":
				ws.DirectoryReferences[0].Path = "/unapproved"
			case "missing settings":
				path, err := s.store.settingsPath(ws.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.ReadCuratorWelcome(t.Context(), owner, ws.ID, instance)
			if got.Pending != 0 || got.NeedsReview != 0 {
				t.Fatalf("leaked batch: %+v %v", got, err)
			}
			if mode == "revoked" || mode == "missing settings" {
				if err != nil || got.Configured {
					t.Fatalf("missing setup: %+v %v", got, err)
				}
			} else if err == nil {
				t.Fatal("invalid scope admitted")
			}
		})
	}
}

type changingCuratorWorkspace struct {
	WorkspaceStore
	reads  int
	change func()
}

func (s *changingCuratorWorkspace) Get(id string) (*workspace.Workspace, error) {
	s.reads++
	if s.reads == 3 {
		s.change()
	}
	return s.WorkspaceStore.Get(id)
}

func TestCuratorWelcome_RevalidatesRootAfterReadingTheBatch(t *testing.T) {
	s, ws, _ := curatorWelcomeFixture(t)
	s.workspaces = &changingCuratorWorkspace{WorkspaceStore: s.workspaces, change: func() {
		if _, err := s.store.UpdateSettings(ws.ID, func(settings *JanitorSettings) error { settings.RootID = "replacement-root"; return nil }); err != nil {
			t.Fatal(err)
		}
	}}
	if got, err := s.ReadCuratorWelcome(t.Context(), "local", ws.ID, "curator"); err == nil || got.Pending != 0 {
		t.Fatalf("stale root: %+v %v", got, err)
	}
}

func TestCuratorWelcome_RevalidatesOwnershipAfterReadingTheBatch(t *testing.T) {
	s, ws, _ := curatorWelcomeFixture(t)
	s.workspaces = &changingCuratorWorkspace{WorkspaceStore: s.workspaces, change: func() { ws.OwnerUserID = "other" }}
	if got, err := s.ReadCuratorWelcome(t.Context(), "local", ws.ID, "curator"); err == nil || got.Pending != 0 {
		t.Fatalf("stale authority: %+v %v", got, err)
	}
}
