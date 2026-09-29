package setupjourney

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestProjectRoleRepairOperationClaimsOnlySQLiteAndRetainsUncertainSlotAfterRestart(t *testing.T) {
	store, primary, folder, installed, owner, homeID, projectID := oldSplitChildFixture(t)
	path := filepath.Join(t.TempDir(), "operations.db")
	db, err := database.Open(t.Context(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	seedReviewedConnectionFixture(t, db, primary, folder, owner, homeID, projectID)
	list := func() ([]plugin.InstalledPlugin, error) { return installed, nil }
	reviewer := NewProjectRoleRepairReviewer(store, db, list)
	one, err := reviewer.Review(t.Context(), owner, homeID, projectID, "review-one")
	if err != nil {
		t.Fatal(err)
	}
	two, err := reviewer.Review(t.Context(), owner, homeID, projectID, "review-two")
	if err != nil {
		t.Fatal(err)
	}
	beforePrimary, _ := primary.Get(projectID)
	beforeFolder, _ := folder.Get(projectID)
	if _, err := reviewer.ClaimProjectRoleRepairOperation(t.Context(), "foreign", homeID, projectID, one.Token, "claim-one"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("foreign owner claimed project roles: %v", err)
	}
	if _, err := reviewer.ClaimProjectRoleRepairOperation(t.Context(), owner, homeID, projectID, "forged-token", "claim-one"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("forged review claimed project roles: %v", err)
	}
	claim, err := reviewer.ClaimProjectRoleRepairOperation(t.Context(), owner, homeID, projectID, one.Token, "claim-one")
	if err != nil || claim.Token == "" || claim.Status != "claimed" || claim.EvidenceDigest == "" {
		t.Fatalf("durable exact child intent: %+v %v", claim, err)
	}
	if _, err := reviewer.InspectPendingReview(t.Context(), owner, homeID, projectID, one.Token); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("claimed review still pending: %v", err)
	}
	if _, err := reviewer.ClaimProjectRoleRepairOperation(t.Context(), owner, homeID, projectID, one.Token, "claim-one"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("replayed claim could authorize a second write: %v", err)
	}
	if _, err := reviewer.ClaimProjectRoleRepairOperation(t.Context(), owner, homeID, projectID, two.Token, "competing-claim"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("second review claimed an occupied child: %v", err)
	}
	if _, err := reviewer.InspectPendingReview(t.Context(), owner, homeID, projectID, two.Token); err != nil {
		t.Fatalf("failed competing claim consumed a separate review: %v", err)
	}
	if _, err := reviewer.Review(t.Context(), owner, homeID, projectID, "review-after-claim"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("occupied child offered another repair review: %v", err)
	}
	for _, entry := range []struct {
		store   workspace.Store
		version int64
	}{{primary, beforePrimary.Version}, {folder, beforeFolder.Version}} {
		current, getErr := entry.store.Get(projectID)
		if getErr != nil || current.Version != entry.version || len(current.GetAssistantProjectLink().ProjectRoles) != 0 || len(current.GetAgentInstances()) != 0 {
			t.Fatalf("SQLite-only claim changed a project mirror: %+v %v", current, getErr)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedDB, err := database.Open(context.Background(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopenedDB.Close() }()
	reopenedFolder, err := workspace.NewFileStore(folder.BasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopenedFolder.Close() }()
	restarted := NewProjectRoleRepairReviewer(workspace.NewSyncStore(primary, reopenedFolder), reopenedDB, list)
	if saved, err := restarted.InspectProjectRoleRepairOperation(t.Context(), owner, homeID, projectID, "claim-one"); err != nil || saved.Token != claim.Token || saved.Status != "claimed" {
		t.Fatalf("restart lost the pending operation: %+v %v", saved, err)
	}
	if _, err := restarted.InspectProjectRoleRepairOperation(t.Context(), "foreign", homeID, projectID, "claim-one"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("foreign owner read an operation: %v", err)
	}
	// Test-only mirror divergence: the operation remains visible for diagnosis,
	// but an existing pending *review* no longer passes the mirror guard.
	if err := reopenedFolder.Update(projectID, func(child *workspace.Workspace) error {
		link := child.GetAssistantProjectLink()
		link.ProjectRoles = []workspace.AssistantProgramRoleSpec{{ID: "test-only", Scope: workspace.AssistantRoleScopeProject}}
		child.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.InspectPendingReview(t.Context(), owner, homeID, projectID, two.Token); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("divergent child retained a current review: %v", err)
	}
	if saved, err := restarted.InspectProjectRoleRepairOperation(t.Context(), owner, homeID, projectID, "claim-one"); err != nil || saved.Status != "claimed" {
		t.Fatalf("failed mirror cannot be diagnosed by exact operation: %+v %v", saved, err)
	}
}

func TestProjectRoleRepairOperationCompetingProcessesCannotClaimSameChild(t *testing.T) {
	store, primary, folder, installed, owner, homeID, projectID := oldSplitChildFixture(t)
	path := filepath.Join(t.TempDir(), "racing-claims.db")
	db, err := database.Open(t.Context(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	seedReviewedConnectionFixture(t, db, primary, folder, owner, homeID, projectID)
	otherDB, err := database.Open(t.Context(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = otherDB.Close() }()
	list := func() ([]plugin.InstalledPlugin, error) { return installed, nil }
	first, second := NewProjectRoleRepairReviewer(store, db, list), NewProjectRoleRepairReviewer(store, otherDB, list)
	one, err := first.Review(t.Context(), owner, homeID, projectID, "race-review-1")
	if err != nil {
		t.Fatal(err)
	}
	two, err := second.Review(t.Context(), owner, homeID, projectID, "race-review-2")
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		err        error
		key, token string
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, attempt := range []struct {
		reviewer   *ProjectRoleRepairReviewer
		token, key string
	}{{first, one.Token, "race-claim-1"}, {second, two.Token, "race-claim-2"}} {
		wg.Add(1)
		go func(reviewer *ProjectRoleRepairReviewer, token, key string) {
			defer wg.Done()
			<-start
			claimed, claimErr := reviewer.ClaimProjectRoleRepairOperation(context.Background(), owner, homeID, projectID, token, key)
			results <- outcome{err: claimErr, key: key, token: claimed.Token}
		}(attempt.reviewer, attempt.token, attempt.key)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for result := range results {
		if result.err == nil {
			if result.token == "" {
				t.Fatal("empty claimed operation token")
			}
			successes++
		} else if !errors.Is(result.err, ErrProjectRoleRepairUnavailable) {
			t.Fatalf("unexpected claim failure for %s: %v", result.key, result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("competing SQLite processes minted %d active repair intents", successes)
	}
	var active, consumed int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM project_role_repair_operation WHERE project_id = ? AND status = 'claimed'`, projectID).Scan(&active); err != nil || active != 1 {
		t.Fatalf("unexpected number of durable claims: %d %v", active, err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM project_role_repair_review WHERE project_id = ? AND consumed_at IS NOT NULL`, projectID).Scan(&consumed); err != nil || consumed != 1 {
		t.Fatalf("failed racing claim consumed the wrong review: %d %v", consumed, err)
	}
}
