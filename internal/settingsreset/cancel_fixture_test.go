package settingsreset

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

// stagedStartFresh is a synthetic installation whose Start Fresh was reviewed
// and accepted by the real coordinator, then "quit" so the next process
// recovers it. Nothing here copies a user installation, receipt or workspace.
type stagedStartFresh struct {
	Fixture     *resetfixture.Fixture
	Owners      *Owners
	DataDir     string
	OperationID string
	RequestID   string
}

// stageStartFresh reviews and accepts Start Fresh against the seeded fixture
// through the production Planner and Coordinator, leaving `awaiting_restart`
// revision 2 on disk with the accepting lease closed.
func stageStartFresh(t *testing.T) *stagedStartFresh {
	t.Helper()
	f, owners, planner := previewFixture(t)
	root := f.Paths().DataDir
	owners.FreshTargets = fixtureFreshTargets(root)
	t.Setenv("ORI_TEMPLATES_DIR", filepath.Join(root, "templates"))
	t.Setenv("WORKFLOW_TEMPLATES_DIR", filepath.Join(root, "workflow_templates"))
	mustPreview(t, owners.Config.SetTemplatesRoot(filepath.Join(root, "templates")))
	mustPreview(t, owners.Config.Save())

	lease, err := resetstate.Acquire(root)
	mustPreview(t, err)
	life := &fixtureLifecycle{drain: func(context.Context) error {
		return errors.Join(owners.Workspaces.Close(), owners.Database.Close())
	}}
	preview, err := planner.Create(t.Context(), IntentStartFresh, nil)
	mustPreview(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("Start Fresh review blockers: %+v", preview.Blockers)
	}
	const requestID = "cancel-fixture-request"
	operation, err := NewCoordinator(lease, planner, life).Stage(t.Context(), ExecuteRequest{
		PreviewID: preview.ID, RequestID: requestID, Confirmation: "RESET", ConfirmAgentsFolder: confirmedAgentsFolder(preview),
	})
	mustPreview(t, err)
	if operation.State != StateAwaitingRestart || operation.Revision != 2 {
		t.Fatalf("staged Start Fresh = %s rev %d", operation.State, operation.Revision)
	}
	mustPreview(t, lease.Close())
	return &stagedStartFresh{Fixture: f, Owners: owners, DataDir: root, OperationID: operation.ID, RequestID: requestID}
}

// launch models one new process taking the installation lease and running the
// pre-start recovery with the fixture's in-memory secrets. The lease stays open
// for the test's lifetime, like a host that stays up to serve recovery.
func (s *stagedStartFresh) launch(t *testing.T) (*resetstate.Lease, error) {
	t.Helper()
	lease, err := resetstate.Acquire(s.DataDir)
	mustPreview(t, err)
	t.Cleanup(func() { _ = lease.Close() })
	return lease, RecoverBeforeStores(t.Context(), lease, RecoveryOptions{
		DataDir: s.DataDir, SecretStore: s.Fixture.Secrets(), Now: func() time.Time { return time.Now() },
	})
}

// blockOnWorkspaceChange reproduces the reported incident with production code.
// The caller, playing the person, has already emptied or removed the retained
// workspace folder outside Ori between accepting Start Fresh and restarting;
// the next recovery launch then blocks and keeps serving its recovery page.
func (s *stagedStartFresh) blockOnWorkspaceChange(t *testing.T) *resetstate.Lease {
	t.Helper()
	lease, err := s.launch(t)
	if !errors.Is(err, ErrRecoveryIncomplete) {
		t.Fatalf("recovery after the workspace changed = %v, want ErrRecoveryIncomplete", err)
	}
	return lease
}

// persistForTest writes a receipt through the production encoder and lease.
func persistForTest(t *testing.T, lease *resetstate.Lease, j *journal) {
	t.Helper()
	data, err := encodeJournal(j)
	mustPreview(t, err)
	mustPreview(t, lease.Replace(resetstate.OperationRecord, data))
}

func (s *stagedStartFresh) receipt(t *testing.T, lease *resetstate.Lease) *journal {
	t.Helper()
	data, err := lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	j, err := decodeJournal(data)
	mustPreview(t, err)
	return j
}
