package settingsreset

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// Exercise the actual reviewed/staged/restarted app-record reset, not deletion
// of a relationship row. This pins the retained-folder boundary that continuity
// must respect: records disappear, folder bytes survive, automatic adoption is
// suppressed. Explicitly restoring such a folder by import is covered by
// sessionhttp's TestContinuityExplicitRestoreAfterAppRecordReset.
func TestWorkspaceContinuityCharacterization_AppRecordResetRetainsDetachedFolder(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	fixture, owners, planner := previewFixture(t)
	paths := fixture.Paths()
	// Include the new local-private owner in the actual staged/restarted reset,
	// including a durable encrypted stage that has not published its file yet.
	_, err := owners.Database.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at)
		VALUES (?,'Synthetic',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) ON CONFLICT(id) DO NOTHING`, resetfixture.WorkspaceID)
	mustPreview(t, err)
	coordination := workspacecontinuity.NewLocalStore(owners.Database)
	mustPreview(t, coordination.RegisterNative(t.Context(), resetfixture.WorkspaceID))
	// Model an interrupted prior writer: a durable barrier, not an active
	// in-process permit. Reset owns removing it while retaining folder bytes.
	_, err = coordination.BeginFileMutation(t.Context(), resetfixture.WorkspaceID, "workspace.json")
	mustPreview(t, err)
	_, err = workspace.NewLocalConfigStore(owners.Database, fixture.Secrets()).Stage(t.Context(), resetfixture.WorkspaceID,
		"agent", "fixture-agent", []byte(`{"synthetic_private_config":"not-a-real-key"}`))
	mustPreview(t, err)
	folder, err := owners.Workspaces.GetFolderPath(resetfixture.WorkspaceID)
	mustPreview(t, err)
	before := treeBytes(t, folder)
	if len(before) == 0 {
		t.Fatal("retained workspace fixture is empty")
	}
	lease, err := resetstate.Acquire(paths.DataDir)
	mustPreview(t, err)
	t.Cleanup(func() { mustPreview(t, lease.Close()) })
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAppRecords})
	mustPreview(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("reset preview blocked: %+v", preview.Blockers)
	}
	if len(preview.Categories) != 1 || !strings.Contains(preview.Categories[0].Description, "separated agent keys and connector grants") {
		t.Fatal("reset preview omitted removal of workspace-local configuration")
	}
	// Kept folders still hold a private portable copy; the review must say so
	// and name both choices (restore by import, erase by deleting the folder).
	if description := preview.Categories[0].Description; !strings.Contains(description, "private portable copy") ||
		!strings.Contains(description, "import a kept folder") || !strings.Contains(description, "delete the folder") {
		t.Fatal("reset preview does not disclose retained private continuity copies")
	}
	lifecycle := &fixtureLifecycle{drain: func(context.Context) error {
		return errors.Join(owners.Workspaces.Close(), owners.Database.Close())
	}}
	operation, err := NewCoordinator(lease, planner, lifecycle).Stage(t.Context(), ExecuteRequest{
		PreviewID: preview.ID, RequestID: "continuity-characterization", Confirmation: "RESET",
	})
	mustPreview(t, err)
	if operation.State != StateAwaitingRestart {
		t.Fatalf("reset did not reach restart boundary: %s", operation.State)
	}
	mustPreview(t, lease.Close())
	relaunched, err := resetstate.Acquire(paths.DataDir)
	mustPreview(t, err)
	t.Cleanup(func() { mustPreview(t, relaunched.Close()) })
	mustPreview(t, RecoverBeforeStores(t.Context(), relaunched, RecoveryOptions{
		DataDir: paths.DataDir, SecretStore: fixture.Secrets(),
	}))
	receipt, err := NewCoordinator(relaunched, nil, nil).Status(t.Context(), preview.OperationID)
	mustPreview(t, err)
	if !receipt.VerifiedComplete() {
		t.Fatalf("reset did not verify: %s", receipt.State)
	}
	if after := treeBytes(t, folder); !reflect.DeepEqual(before, after) {
		t.Fatal("application-record reset changed retained workspace files")
	}
	policy, err := ReadStartupPolicy(relaunched)
	mustPreview(t, err)
	if !policy.SuppressWorkspaceAdoption || !policy.SuppressProfileSeed || !policy.SuppressExternalMCPImport {
		t.Fatal("retained files are not protected from automatic reattachment")
	}
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(paths.DataDir, "sessions.db")})
	mustPreview(t, err)
	t.Cleanup(func() { mustPreview(t, db.Close()) })
	if _, err := session.NewSQLiteStore(db).GetSession(t.Context(), resetfixture.SessionID); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("app-record reset retained conversation: %v", err)
	}
	var privateRows, attachments, mutations int
	mustPreview(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_local_config`).Scan(&privateRows))
	mustPreview(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM continuity_attachments`).Scan(&attachments))
	mustPreview(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM continuity_file_mutations`).Scan(&mutations))
	if privateRows != 0 || attachments != 0 || mutations != 0 {
		t.Fatal("app-record reset retained local configuration, mutation barriers or admission")
	}
	if err := workspacecontinuity.NewLocalStore(db).MarkDirty(t.Context(), resetfixture.WorkspaceID); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("late invalidation repopulated reset state", err)
	}
	if _, err := fixture.Secrets().Get(vault.SecretKeyWorkspaceConfigDEK); err != nil {
		t.Fatal("app-record reset erased installation encryption material")
	}
	fixture.AssertPreserved(t)
}
