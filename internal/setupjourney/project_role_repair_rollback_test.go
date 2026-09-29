package setupjourney

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// primaryFailureAfterFolderWrite is test-only. Removing write permission from
// this one disposable child folder after SyncStore's first folder write makes
// its own compensating FileStore.Save fail, while the transactional SQLite
// primary refuses the corresponding write. It does not simulate power loss.
type primaryFailureAfterFolderWrite struct {
	workspace.Store
	childID   string
	folderDir string
}

func (s *primaryFailureAfterFolderWrite) Save(current *workspace.Workspace) error {
	if current.ID != s.childID {
		return s.Store.Save(current)
	}
	if err := os.Chmod(s.folderDir, 0500); err != nil {
		return err
	}
	return errors.New("injected SQLite primary save failure")
}

func TestProjectRoleRepair_FailedFolderRollbackRefusesOldReviewAndFreshInspection(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permission refusal requires an unprivileged test user")
	}
	_, memory, folder, installed, owner, homeID, projectID := oldSplitChildFixture(t)
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(t.TempDir(), "sqlite-primary.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	primary := session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(db, 10))
	for _, id := range []string{homeID, projectID} {
		item, getErr := memory.Get(id)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if saveErr := primary.Save(item); saveErr != nil {
			t.Fatal(saveErr)
		}
	}
	seedReviewedConnectionFixture(t, db, primary, folder, owner, homeID, projectID)
	folderDir, err := folder.GetFolderPath(projectID)
	if err != nil {
		t.Fatal(err)
	}
	// Always restore permissions before FileStore's t.Cleanup and before a
	// reopened handle checks the persisted mirror, even on assertion failure.
	t.Cleanup(func() { _ = os.Chmod(folderDir, 0750) })
	faulty := workspace.NewSyncStore(&primaryFailureAfterFolderWrite{Store: primary, childID: projectID, folderDir: folderDir}, folder)
	list := func() ([]plugin.InstalledPlugin, error) { return installed, nil }
	reviewer := NewProjectRoleRepairReviewer(faulty, db, list)
	review, err := reviewer.Review(t.Context(), owner, homeID, projectID, "old-review")
	if err != nil || review.Token == "" {
		t.Fatalf("exact pre-fix child was not inspectable before fault: %+v %v", review, err)
	}
	// This is a test-only, deliberately unauthorized mutation to produce an
	// interrupted role snapshot. It must never be promoted to product repair.
	err = faulty.Update(projectID, func(current *workspace.Workspace) error {
		link := current.GetAssistantProjectLink()
		link.ProjectRoles = []workspace.AssistantProgramRoleSpec{{ID: "reaper-assistant", Label: "REAPER Assistant", Primary: true, SystemPrompt: "Project only.", Skills: []string{"reaper-skill"}}}
		current.SetAssistantProjectLink(link)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "folder rollback failed") {
		t.Fatalf("expected actual SyncStore compensating write failure, got %v", err)
	}
	if err := os.Chmod(folderDir, 0750); err != nil {
		t.Fatal(err)
	}
	before, err := primary.Get(projectID)
	if err != nil || len(before.GetAssistantProjectLink().ProjectRoles) != 0 {
		t.Fatalf("SQLite primary must retain missing roles, got %+v %v", before, err)
	}
	after, err := folder.Get(projectID)
	if err != nil || len(after.GetAssistantProjectLink().ProjectRoles) != 1 {
		t.Fatalf("folder must retain only the uncommitted role, got %+v %v", after, err)
	}
	// New FileStore and hybrid handles approximate a server-process restart;
	// no mirror is selected to heal the other, and a retained receipt does
	// not turn a divergent child into a reviewed write.
	reopenedFolder, err := workspace.NewFileStore(folder.BasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopenedFolder.Close() }()
	reopenedPrimary := session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(db, 10))
	reopened := workspace.NewSyncStore(reopenedPrimary, reopenedFolder)
	if _, err := InspectMissingSplitProjectRoles(reopened, installed, owner, homeID, projectID); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("divergent mirrors projected a repairable child after restart: %v", err)
	}
	freshReviewer := NewProjectRoleRepairReviewer(reopened, db, list)
	if _, err := freshReviewer.InspectPendingReview(t.Context(), owner, homeID, projectID, review.Token); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("failed rollback left the old review apparently valid: %v", err)
	}
	for _, key := range []string{"old-review", "new-review"} {
		if _, err := freshReviewer.Review(context.Background(), owner, homeID, projectID, key); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
			t.Fatalf("split child reissued %q receipt after failed rollback: %v", key, err)
		}
	}
	var receipts int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM project_role_repair_review WHERE project_id = ?`, projectID).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("failed rollback erased or created reviews: count=%d err=%v", receipts, err)
	}
}
