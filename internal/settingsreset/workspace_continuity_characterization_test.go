package settingsreset

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

// Exercise the actual reviewed/staged/restarted app-record reset, not deletion
// of a relationship row. This pins the retained-folder boundary that continuity
// must respect: records disappear, folder bytes survive, automatic adoption is
// suppressed. It does not claim that explicit continuity restore exists yet.
func TestWorkspaceContinuityCharacterization_AppRecordResetRetainsDetachedFolder(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	fixture, owners, planner := previewFixture(t)
	paths := fixture.Paths()
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
	fixture.AssertPreserved(t)
}
