package server

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// Seeds saved observations only, then uses real collection save/review/commit
// boundaries. The returned assertion runs after the real package manager upgrade.
func prepareUpgradeArrangements(t *testing.T, store workspace.Store, manager *plugin.Manager, homeID string) func() {
	t.Helper()
	scope := projectlibrary.Scope{OwnerUserID: "local", HomeID: homeID, ProviderID: "music-project-management", ProgramID: "music-producer-assistant"}
	authority := projectlibrary.ManagerAuthority{HomeID: homeID, AgentName: "Upgrade Manager", AgentInstanceID: "upgrade-manager"}
	if err := store.Update(homeID, func(home *workspace.Workspace) error {
		home.AgentInstances = []workspace.AgentInstance{{ID: authority.AgentInstanceID, Name: authority.AgentName, RoleID: "portfolio_manager"}}
		state := home.GetAssistantProgramState()
		state.HomeBindings = workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{{RoleID: "portfolio_manager", AgentInstanceID: authority.AgentInstanceID, AgentName: authority.AgentName}}}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkspaceAgent(homeID, authority.AgentName, &agent.Agent{Settings: types.Settings{Provider: "openai", Model: "fixture", SystemPrompt: "Coordinate only reviewed Home work."}}); err != nil {
		t.Fatal(err)
	}
	providerEvidence := func(_ projectlibrary.Scope, home *workspace.Workspace) bool {
		records, err := manager.List()
		state := home.GetAssistantProgramState()
		owner := state.HomeProvider
		if owner == nil && state.GroupTemplate != nil {
			owner = state.GroupTemplate.ProgramHomeOwner
		}
		return err == nil && plugin.IndependentHomeProviderEvidenceAvailable(records, owner)
	}
	library := projectlibrary.NewStore(store).WithProviderEvidence(providerEvidence)
	init, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, init.Token, "upgrade-library-init"); err != nil {
		t.Fatal(err)
	}
	rootPath := t.TempDir()
	now := time.Now().UTC()
	if err := store.Update(homeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		var doc projectlibrary.Document
		if err := json.Unmarshal(state.ProjectLibrary, &doc); err != nil {
			return err
		}
		doc.Roots = []projectlibrary.Root{{ID: "saved-root", Path: rootPath, FileIdentity: "fixture-root", Revision: 1, ApprovedAt: now}}
		doc.Scans = []projectlibrary.Scan{{ID: "saved-scan", RootID: "saved-root", RootRevision: 1, RootDigest: strings.Repeat("a", 64), ResultDigest: strings.Repeat("b", 64), StartedAt: now, FinishedAt: &now, Status: "complete", EntriesSeen: 61}}
		for i := 0; i < 61; i++ {
			doc.Entries = append(doc.Entries, projectlibrary.Entry{ID: fmt.Sprintf("saved-%02d", i), Revision: 1, Fields: projectlibrary.Fields{DisplayName: fmt.Sprintf("Client song %02d", i), Revision: 1}, Observations: []projectlibrary.Observation{{RootID: "saved-root", RelativeFolder: fmt.Sprintf("song-%02d", i), FileIdentity: fmt.Sprintf("file-%02d", i), Format: "reaper", ScanID: "saved-scan", ScannedAt: now, Availability: "available"}}})
		}
		doc.Revision++
		raw, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		state.ProjectLibrary = raw
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Read(scope); err != nil {
		t.Fatalf("seed library: %v", err)
	}
	if !library.CanReadAsManager(authority) {
		t.Fatal("fixture Manager unavailable")
	}
	provider := &runtimeMetadataProvider{}
	var resolve projectlibrary.ArrangementRuntimeResolver = func(ctx context.Context, _ projectlibrary.ManagerAuthority) (projectlibrary.ArrangementRuntime, error) {
		return projectlibrary.ArrangementRuntime{Provider: provider, Model: "fixture", ProviderFingerprint: strings.Repeat("a", 64), Revalidate: func(ctx context.Context) error { return ctx.Err() }}, ctx.Err()
	}
	service := projectlibrary.NewCollectionReviewService(library, resolve)
	save := func() projectlibrary.Arrangement {
		t.Helper()
		evidence, err := workspacearrangement.NewEvidence(t.Context(), func(ctx context.Context) (workspacearrangement.Snapshot, error) {
			return library.ArrangementEvidence(ctx, scope.OwnerUserID, authority)
		})
		if err != nil {
			t.Fatal(err)
		}
		selection, err := evidence.Select(t.Context(), workspacearrangement.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(workspacearrangement.Draft{Version: 1, SourceRevision: selection.Revision, Goal: "Keep all songs catalog-only", Operations: []workspacearrangement.Operation{{Kind: workspacearrangement.CreateCollection, Name: "Client references", Members: workspacearrangement.Selection{Handle: selection.Handle}}}})
		draft, err := evidence.Resolve(t.Context(), string(raw))
		if err != nil {
			t.Fatal(err)
		}
		record, err := evidence.NewRecord(t.Context(), draft, workspacearrangement.Provenance{Origin: "model", SourceID: homeID, Provider: "openai", Model: "fixture", ProviderFingerprint: strings.Repeat("a", 64)})
		if err != nil {
			t.Fatal(err)
		}
		doc, err := library.Read(scope)
		if err != nil {
			t.Fatal(err)
		}
		saved, _, err := library.SaveArrangement(t.Context(), scope.OwnerUserID, authority, doc.Revision, true, record)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	first := save()
	review, err := service.Review(t.Context(), scope.OwnerUserID, homeID, first.Record.ID, first.CatalogRevision, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(t.Context(), scope.OwnerUserID, homeID, first.Record.ID, review.Token, "upgrade-collection-apply", true); err != nil {
		t.Fatal(err)
	}
	pending := save()
	pendingReview, err := service.Review(t.Context(), scope.OwnerUserID, homeID, pending.Record.ID, pending.CatalogRevision, 1)
	if err != nil {
		t.Fatal(err)
	}
	before, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := library.CollectionCatalog(t.Context(), scope, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		restarted := projectlibrary.NewStore(store).WithProviderEvidence(providerEvidence)
		after, err := restarted.Read(scope)
		if err != nil {
			t.Fatal("upgrade broke canonical library", err)
		}
		if !reflect.DeepEqual(before.Collections, after.Collections) || !reflect.DeepEqual(before.Entries, after.Entries) || !reflect.DeepEqual(before.Roots, after.Roots) || !reflect.DeepEqual(before.Arrangements, after.Arrangements) || !reflect.DeepEqual(before.Scans, after.Scans) || !reflect.DeepEqual(before.Sessions, after.Sessions) {
			t.Fatal("guidance upgrade changed saved arrangement data")
		}
		if _, err := restarted.CollectionMembers(t.Context(), scope, before.Collections[0].ID, catalog.ContextPin, 0); err == nil {
			t.Fatal("old browse pin survived package transition")
		}
		fresh, err := restarted.CollectionCatalog(t.Context(), scope, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, offset := range []int{0, 50} {
			page, err := restarted.CollectionMembers(t.Context(), scope, before.Collections[0].ID, fresh.ContextPin, offset)
			if err != nil {
				t.Fatal(err)
			}
			for _, member := range page.Members {
				seen[member.ID] = true
			}
		}
		if len(seen) != 61 {
			t.Fatal("upgrade lost collection members")
		}
		freshService := projectlibrary.NewCollectionReviewService(restarted, resolve)
		if _, err := freshService.Commit(t.Context(), scope.OwnerUserID, homeID, pending.Record.ID, pendingReview.Token, "obsolete-upgrade-review", true); err == nil {
			t.Fatal("old review applied after package upgrade")
		}
		if _, err := freshService.Review(t.Context(), scope.OwnerUserID, homeID, pending.Record.ID, after.Revision, pending.Record.Revision); err == nil {
			t.Fatal("old proposal acquired a new review after package rebinding")
		}
		if _, err := freshService.Commit(t.Context(), scope.OwnerUserID, homeID, first.Record.ID, review.Token, "upgrade-collection-apply", true); err != nil {
			t.Fatal("completed receipt could not be replayed read-only after upgrade", err)
		}
		if _, err := restarted.ReadArrangementHistory(t.Context(), scope, first.Record.ID); err != nil {
			t.Fatal("completed receipt unreadable", err)
		}
		unchanged, err := restarted.Read(scope)
		if err != nil || !reflect.DeepEqual(after, unchanged) || len(provider.requests) != 0 {
			t.Fatal("history/stale confirmation changed data or called model", err)
		}
	}
}
