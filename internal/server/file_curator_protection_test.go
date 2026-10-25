package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/filejanitorhttp"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type curatorRootStub struct {
	root  filejanitor.ManagedRoot
	err   error
	calls int
}

func (r *curatorRootStub) StoredManagedRoot(owner, id string) (filejanitor.ManagedRoot, error) {
	r.calls++
	if owner != r.root.Owner || id != r.root.WorkspaceID {
		return filejanitor.ManagedRoot{}, filejanitor.ErrManagedRootUnavailable
	}
	return r.root, r.err
}

type curatorCoverageStub struct {
	read    backuprecovery.CoverageRead
	err     error
	folders []backuprecovery.AdoptedFolder
	ids     []string
	after   func()
}

func (r *curatorCoverageStub) ReadFolderCoverage(_ context.Context, owner string, ids []string, folders []backuprecovery.AdoptedFolder) (backuprecovery.CoverageRead, error) {
	if owner != "local" {
		return backuprecovery.CoverageRead{}, errors.New("wrong owner")
	}
	r.folders, r.ids = folders, ids
	if r.after != nil {
		r.after()
	}
	return r.read, r.err
}

func curatorProtectionFixture(t *testing.T) (fileCuratorProtection, *curatorRootStub, *curatorCoverageStub, *workspace.InMemoryStore) {
	t.Helper()
	s := workspace.NewInMemoryStore()
	for _, id := range []string{"filing", "backup"} {
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: id})
		ws.ID, ws.FolderSlug, ws.OwnerUserID = id, id, "local"
		capability := workspace.CapabilityFileJanitor
		if id == "backup" {
			capability = workspace.CapabilityBackupRecovery
		}
		if _, err := ws.AddInstalledCapability(workspace.InstalledCapability{ID: capability, Version: 1, Source: workspace.InstallSourceInPlace}); err != nil {
			t.Fatal(err)
		}
		// The generic adopted folder intentionally differs from the managed root.
		ws.DirectoryReferences = []workspace.DirectoryReference{{ID: "generic", WorkspaceID: id, Path: "/Invented/WorkspaceStorage"}}
		if err := s.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	roots := &curatorRootStub{root: filejanitor.ManagedRoot{WorkspaceID: "filing", Owner: "local", Generation: "root-one", DirectoryReferenceID: "ref", Path: "/Invented/ApprovedInbox", BindingID: "grant"}}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	observed, completed := now.Add(-time.Hour), now.Add(-24*time.Hour)
	coverage := &curatorCoverageStub{read: backuprecovery.CoverageRead{Complete: true, Consoles: []backuprecovery.CoverageConsole{{WorkspaceID: "backup", Configured: true}}, Folders: []backuprecovery.FolderCoverage{{WorkspaceID: "filing", State: "covered", Reason: "completed_backup_observed", Provider: "restic", CoveringWorkspaceID: "backup", ObservedAt: &observed, LastCompletedAt: &completed}}}}
	today := personalassistant.NewTodayService(nil, nil, s, nil)
	today.SetFollowUpWorkspaceSource(s)
	return fileCuratorProtection{roots: roots, scope: today, coverage: coverage, now: func() time.Time { return now }}, roots, coverage, s
}

func TestCuratorProtectionUsesManagedRootAndPathFreeStoredTimes(t *testing.T) {
	r, roots, reader, _ := curatorProtectionFixture(t)
	fact, err := r.ReadProtection(t.Context(), "local", "filing")
	if err != nil || fact.State != "covered" || roots.calls != 2 || fact.RootGeneration != "root-one" || len(fact.BindingID) != 64 {
		t.Fatal(fact, err, roots.calls)
	}
	if len(reader.folders) != 1 || reader.folders[0].Path != roots.root.Path || !reflect.DeepEqual(reader.ids, []string{"backup", "filing"}) {
		t.Fatal("wrong root/scope", reader)
	}
	if fact.ObservedAt == nil || fact.LastCompletedAt == nil || fact.ObservedAt.Equal(*fact.LastCompletedAt) || fact.AsOf.Equal(*fact.ObservedAt) {
		t.Fatal("times conflated", fact)
	}
	if fact.Destination.Kind != "workspace" || fact.Destination.WorkspaceID != "backup" {
		t.Fatal(fact.Destination)
	}
	encoded, err := json.Marshal(fact)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{string(encoded), fact.Prompt()} {
		if strings.Contains(text, "/Invented") || strings.Contains(text, "ApprovedInbox") || strings.Contains(text, "WorkspaceStorage") {
			t.Fatal("path disclosed", text)
		}
	}
}

func TestCuratorProtectionKeepsCoverageStatesHonest(t *testing.T) {
	for _, test := range []struct{ state, reason, covering, want string }{
		{"not_covered", "no_configuration", "", "not_covered"},
		{"not_covered", "not_in_any_source", "", "not_covered"},
		{"not_covered", "no_completed_backup_observed", "backup", "configured"},
		{"unknown", "evidence_stale", "backup", "unknown"},
		{"unknown", "permission_required", "backup", "unknown"},
		{"unknown", "configuration_unavailable", "", "unknown"},
	} {
		t.Run(test.reason, func(t *testing.T) {
			r, _, reader, _ := curatorProtectionFixture(t)
			reader.read.Folders = []backuprecovery.FolderCoverage{{WorkspaceID: "filing", State: test.state, Reason: test.reason, CoveringWorkspaceID: test.covering}}
			got, err := r.ReadProtection(t.Context(), "local", "filing")
			if err != nil || got.State != test.want || got.LastCompletedAt != nil || got.ObservedAt != nil {
				t.Fatal(got, err)
			}
		})
	}
}

func TestCuratorProtectionInvalidatesLateFacts(t *testing.T) {
	for _, change := range []string{"relink", "revoke", "grant", "owner", "deleted", "scope", "console removal"} {
		t.Run(change, func(t *testing.T) {
			r, roots, reader, s := curatorProtectionFixture(t)
			reader.after = func() {
				switch change {
				case "relink":
					roots.root.Generation = "root-two"
				case "revoke":
					roots.err = filejanitor.ErrManagedRootUnavailable
				case "grant":
					roots.root.BindingID = "replaced-grant"
				case "owner":
					if err := s.Update("filing", func(ws *workspace.Workspace) error { ws.OwnerUserID = "other"; return nil }); err != nil {
						t.Fatal(err)
					}
				case "deleted":
					if err := s.Delete("filing"); err != nil {
						t.Fatal(err)
					}
				case "scope":
					if err := s.Update("backup", func(ws *workspace.Workspace) error { ws.OwnerUserID = "other"; return nil }); err != nil {
						t.Fatal(err)
					}
				case "console removal":
					if err := s.Update("backup", func(ws *workspace.Workspace) error { ws.InstalledCapabilities = nil; return nil }); err != nil {
						t.Fatal(err)
					}
				}
			}
			got, _ := r.ReadProtection(t.Context(), "local", "filing")
			if got.State == "covered" || got.LastCompletedAt != nil || got.ObservedAt != nil {
				t.Fatal("late facts survived", got)
			}
		})
	}
}

func TestCuratorProtectionEndpointAndCapturedRequestUseIdenticalProjection(t *testing.T) {
	r, roots, coverage, s := curatorProtectionFixture(t)
	if err := s.Update("filing", func(ws *workspace.Workspace) error {
		ws.AgentInstances = []workspace.AgentInstance{{ID: "lead", Name: "Lead", NodeID: "lead", EntryPoint: true}, {ID: "curator", Name: "Renamed helper", NodeID: "helper"}}
		ws.RecordCapabilityResource(workspace.CapabilityFileJanitor, workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: "curator", Shared: true})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	agents, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if err := agents.SetAgent("Renamed helper", &agent.Agent{Settings: types.Settings{Provider: "claude_code", Model: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	resolver := workspace.NewAgentRuntimeResolver(agents, s, nil, nil)
	resolver.SetCuratorProtectionReader(r)
	provider := &capturingChatProvider{}
	factory := llm.NewFactory()
	factory.Register("claude_code", provider)
	tasks := workspace.NewLLMTaskHandler(agents, factory, s)
	tasks.SetRuntimeResolver(resolver)
	handler := filejanitorhttp.NewHandler(filejanitor.NewService(nil, s), s, nil)
	handler.SetProtectionReader(r)
	mux := http.NewServeMux()
	handler.Register(mux)
	for _, state := range []string{"covered", "unknown", "revoked", "relinked"} {
		t.Run(state, func(t *testing.T) {
			switch state {
			case "unknown":
				coverage.read.Folders[0].State, coverage.read.Folders[0].Reason, coverage.read.Folders[0].LastCompletedAt = "unknown", "evidence_stale", nil
			case "revoked":
				roots.err = filejanitor.ErrManagedRootUnavailable
			case "relinked":
				roots.err, roots.root.Generation = nil, "root-two"
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/workspaces/filing/file-janitor/protection", nil))
			if response.Code != http.StatusOK {
				t.Fatal(response.Code, response.Body.String())
			}
			var fact filecuratorguidance.ProtectionFact
			if err := json.Unmarshal(response.Body.Bytes(), &fact); err != nil {
				t.Fatal(err)
			}
			_, err := tasks.ExecuteTask(t.Context(), "Renamed helper", workspace.Task{ID: "advice", To: "Renamed helper", WorkspaceID: "filing", AssignedNodeID: "helper", Description: "Explain this folder's backup evidence"})
			if err != nil {
				t.Fatal(err)
			}
			prompt := provider.requests[len(provider.requests)-1].SystemPrompt
			if state == "revoked" {
				if fact.State != "unavailable" || strings.Contains(prompt, "## File Janitor managed-folder protection") {
					t.Fatal(fact, prompt)
				}
			} else if !strings.Contains(prompt, fact.Prompt()) || fact.Prompt() == "" {
				t.Fatal("endpoint and provider prompt disagree", fact, prompt)
			}
			t.Logf("Stored fixture endpoint: %s\nCaptured managed-fact context: %s", response.Body.String(), fact.Prompt())
		})
	}
}

func TestCuratorProtectionHonorsSavedScopeAndWrongOwner(t *testing.T) {
	r, roots, reader, s := curatorProtectionFixture(t)
	relationship := privacyRelationship{&personalassistant.Projection{DailyBrief: &personalassistant.BriefConfigProjection{Scope: dailybrief.ScopeSelected, SelectedWorkspaceIDs: []string{"filing"}}}}
	today := personalassistant.NewTodayService(relationship, nil, s, nil)
	today.SetFollowUpWorkspaceSource(s)
	r.scope = today
	reader.read = backuprecovery.CoverageRead{Complete: true, Folders: []backuprecovery.FolderCoverage{{WorkspaceID: "filing", State: "not_covered", Reason: "no_configuration"}}}
	fact, err := r.ReadProtection(t.Context(), "local", "filing")
	if err != nil || fact.Destination.Kind != "chooser" || !reflect.DeepEqual(reader.ids, []string{"filing"}) {
		t.Fatal(fact, reader.ids, err)
	}
	roots.calls = 0
	if _, err := r.ReadProtection(t.Context(), "other", "filing"); err == nil || roots.calls != 0 {
		t.Fatal("wrong owner reached root")
	}
	roots.err = filejanitor.ErrManagedRootUnavailable
	reader.folders = nil
	fact, err = r.ReadProtection(t.Context(), "local", "filing")
	if err != nil || fact.State != "unavailable" || fact.Prompt() != "" || reader.folders != nil {
		t.Fatal("unavailable root reached coverage", fact, err)
	}
}
