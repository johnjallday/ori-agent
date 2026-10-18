//go:build darwin || linux

package backuprecovery

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// Invented service controls only, not adapter/device/backup acceptance.
type serviceFixtureCredentials struct {
	revision        atomic.Value
	describes, uses atomic.Int32
}

func (c *serviceFixtureCredentials) DescribeBackupSecret(ctx context.Context, ref vault.BackupSecretReference, authorize vault.BackupSecretAuthorization) (vault.BackupSecretReference, error) {
	c.describes.Add(1)
	if ref.WorkspaceID != localFixtureWorkspace || authorize == nil || authorize(ctx) != nil {
		return vault.BackupSecretReference{}, vault.ErrBackupSecretUnavailable
	}
	ref.Revision = c.revision.Load().(string)
	return ref, nil
}
func (c *serviceFixtureCredentials) UseBackupSecret(ctx context.Context, ref vault.BackupSecretReference, authorize vault.BackupSecretAuthorization, use func([]byte) error) error {
	if authorize == nil || authorize(ctx) != nil || ref.Revision != c.revision.Load().(string) {
		return vault.ErrBackupSecretUnavailable
	}
	c.uses.Add(1)
	password := []byte("invented-service-password")
	defer clear(password)
	return use(password)
}

type serviceFixtureProvider struct {
	calls, closes atomic.Int32
	readFn        func(context.Context, localConfiguration, backupPasswordUse) (providerReadResult, error)
}

func (p *serviceFixtureProvider) read(ctx context.Context, cfg localConfiguration, use backupPasswordUse) (providerReadResult, error) {
	p.calls.Add(1)
	if p.readFn != nil {
		return p.readFn(ctx, cfg, use)
	}
	if err := use(ctx, func(password []byte) error {
		if string(password) != "invented-service-password" {
			return errors.New("invented password mismatch")
		}
		return nil
	}); err != nil {
		return providerReadResult{}, err
	}
	return inventedServiceRead(cfg), nil
}
func (p *serviceFixtureProvider) close() { p.closes.Add(1) }
func inventedServiceRead(cfg localConfiguration) providerReadResult {
	defaults := inventedLocalConfiguration()
	if cfg.RepositoryID == "" {
		cfg.RepositoryID = defaults.RepositoryID
		cfg.RepositoryFileID = defaults.RepositoryFileID
	}
	if cfg.Destination == nil {
		cfg.Destination = defaults.Destination
	}
	cfg.Sources = append([]localSource{}, cfg.Sources...)
	for i := range cfg.Sources {
		if cfg.Sources[i].Storage == nil {
			cfg.Sources[i].Storage = defaults.Sources[0].Storage
			cfg.Sources[i].DirectoryFileID = uint64(i + 10)
		}
	}
	return providerReadResult{configuration: cfg, observedAt: time.Now().UTC(), snapshots: []localSnapshot{}}
}
func newBackupServiceFixture(t *testing.T) (*Service, *serviceFixtureProvider, *serviceFixtureCredentials) {
	t.Helper()
	store, secrets := newLocalStoreFixture(t)
	provider := &serviceFixtureProvider{}
	credentials := &serviceFixtureCredentials{}
	credentials.revision.Store(strings.Repeat("b", 64))
	service, err := newService(ServiceOptions{Database: store.db, Secrets: secrets, DataDir: store.root.path, WorkGate: store.work, Credentials: credentials, Selections: pathselection.NewStore()}, provider, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Error("service did not drain", err)
		}
	})
	return service, provider, credentials
}
func reviewBackupServiceFixture(t *testing.T, s *Service) Review {
	t.Helper()
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	cfg := inventedLocalConfiguration()
	issue := func(path, kind string) string {
		token, err := s.selections.IssueFor(path, selectionScope("local", localFixtureWorkspace, kind))
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	request := ReviewRequest{Generation: status.Generation, Revision: status.Revision, SourceSelections: []string{issue(cfg.Sources[0].Path, "source")}, VolumeSelection: issue(cfg.VolumePath, "volume"), RepositorySelection: issue(cfg.RepositoryPath, "repository"), Credential: CredentialReference{cfg.Credential.VaultID, cfg.Credential.RecordID, cfg.Credential.Revision}}
	review, err := s.Review(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil {
		t.Fatal(err)
	}
	return review
}
func connectBackupServiceFixture(t *testing.T, s *Service) {
	t.Helper()
	review := reviewBackupServiceFixture(t, s)
	request := ConnectRequest{Generation: review.Generation, Revision: review.Revision, OperationID: uuid.NewString(), ReviewToken: review.Token, AllowMetadataRead: true}
	result, err := s.Connect(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil || result.Outcome != "read_ok" {
		t.Fatal("reviewed connect failed", result, err)
	}
}
func refreshBackupServiceFixture(t *testing.T, s *Service) RefreshRequest {
	t.Helper()
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	return RefreshRequest{Generation: status.Generation, Revision: status.Revision, Sequence: status.Sequence, OperationID: uuid.NewString(), RequestedAt: status.ServerTime, AllowMetadataRead: true}
}
func waitServiceCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("service condition did not complete")
}

func TestBackupServiceReviewedConnectIsOneReadAndReplaySafe(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	review := reviewBackupServiceFixture(t, s)
	empty, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || empty.state != nil || empty.receipt != nil || provider.calls.Load() != 0 || credentials.uses.Load() != 0 {
		t.Fatal("review created state, authority or a provider read", err)
	}
	request := ConnectRequest{Generation: review.Generation, Revision: review.Revision, OperationID: uuid.NewString(), ReviewToken: review.Token, AllowMetadataRead: true}
	first, err := s.Connect(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil || first.Outcome != "read_ok" {
		t.Fatal(first, err)
	}
	retry, err := s.Connect(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil || retry != first || provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("lost-response retry repeated the read", retry, err)
	}
	request.ReviewToken = uuid.NewString()
	if _, err := s.Connect(t.Context(), "local", localFixtureWorkspace, request); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("same operation accepted changed input", err)
	}
	record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || !validLocalReceipt(record) || record.state.Monitoring.Intent != "manual_only" || record.receipt.MonitoringDigest != "" {
		t.Fatal("connection did not atomically activate manual-only access", err)
	}
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil || status.Connection != "configured" || status.SnapshotCount == nil || *status.SnapshotCount != 0 || status.LastCompletedAt != nil {
		t.Fatal("empty observed repository was relabelled as a completed backup", status, err)
	}
}
func TestBackupServiceRefreshPreservesFactsAndRejectsEvictedReplay(t *testing.T) {
	s, provider, _ := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	before, _ := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	provider.readFn = func(context.Context, localConfiguration, backupPasswordUse) (providerReadResult, error) {
		return providerReadResult{}, errDestinationAbsent
	}
	request := refreshBackupServiceFixture(t, s)
	first, err := s.Refresh(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil || first.Outcome != "destination_absent" {
		t.Fatal(first, err)
	}
	after, _ := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if after.Revision != before.Revision || after.Sequence <= request.Sequence || after.LastSuccessfulRead == nil || !after.LastSuccessfulRead.Equal(*before.LastSuccessfulRead) || after.Destination != "absent" {
		t.Fatal("failed check changed settings/freshness or erased facts", after)
	}
	retry, err := s.Refresh(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil || retry != first {
		t.Fatal(retry, err)
	}
	record, _ := s.store.load(t.Context(), "local", localFixtureWorkspace)
	_, err = s.store.update(t.Context(), "local", localFixtureWorkspace, record.expected(), changeJournal, func(r *localRecord) error { r.state.Operations = nil; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Refresh(t.Context(), "local", localFixtureWorkspace, request); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("evicted old request became another probe", err)
	}
	if provider.calls.Load() != 2 {
		t.Fatal("replay repeated a provider read")
	}
}
func TestBackupServiceRevokeFencesBeforeDrainingLateRead(t *testing.T) {
	s, provider, _ := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	request := refreshBackupServiceFixture(t, s)
	entered, finish := make(chan struct{}), make(chan struct{})
	provider.readFn = func(_ context.Context, cfg localConfiguration, _ backupPasswordUse) (providerReadResult, error) {
		close(entered)
		<-finish
		return inventedServiceRead(cfg), nil
	}
	readDone := make(chan error, 1)
	go func() { _, err := s.Refresh(t.Context(), "local", localFixtureWorkspace, request); readDone <- err }()
	<-entered
	revokeDone := make(chan error, 1)
	go func() {
		_, err := s.Revoke(t.Context(), "local", localFixtureWorkspace, ControlRequest{request.Generation, request.Revision, uuid.NewString()}, false)
		revokeDone <- err
	}()
	waitServiceCondition(t, func() bool {
		record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
		return err == nil && record.state.Generation != request.Generation && record.receipt == nil
	})
	select {
	case err := <-revokeDone:
		t.Fatal("revocation reported drained while child was still active", err)
	default:
	}
	if err := s.work.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatal("active read was not admitted", err)
	}
	close(finish)
	if err := <-readDone; !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("obsolete result was published", err)
	}
	if err := <-revokeDone; err != nil {
		t.Fatal(err)
	}
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil || status.Connection != "review_required" || status.InProgress || status.EvidenceCurrent || status.SnapshotCount != nil {
		t.Fatal("revoked access was resurrected", status, err)
	}
}
func TestBackupServiceStoredReadsRequireExactLiveOwnerAndNeverProbe(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	for range 3 {
		if _, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetSettings(t.Context(), "local", localFixtureWorkspace); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.GetStatus(t.Context(), "LOCAL", localFixtureWorkspace); !errors.Is(err, ErrWorkspaceUnavailable) {
		t.Fatal("case-folded actor gained access", err)
	}
	if _, err := s.store.db.ExecContext(t.Context(), `INSERT INTO users(id,created_at,updated_at) VALUES('LOCAL',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP); UPDATE workspaces SET owner_user_id='LOCAL' WHERE id=?`, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSettings(t.Context(), "local", localFixtureWorkspace); !errors.Is(err, ErrWorkspaceUnavailable) {
		t.Fatal("case-folded workspace owner exposed settings", err)
	}
	if provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("a stored projection probed or decrypted a backup password")
	}
}
func TestBackupServiceChangedSecretCannotActivateReviewedConnection(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	review := reviewBackupServiceFixture(t, s)
	credentials.revision.Store(strings.Repeat("c", 64))
	result, err := s.Connect(t.Context(), "local", localFixtureWorkspace, ConnectRequest{Generation: review.Generation, Revision: review.Revision, OperationID: uuid.NewString(), ReviewToken: review.Token, AllowMetadataRead: true})
	if err != nil || result.Outcome != "credential_unavailable" {
		t.Fatal(result, err)
	}
	record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || record.receipt != nil || record.state.Observation != nil || credentials.uses.Load() != 0 || provider.calls.Load() != 1 {
		t.Fatal("changed secret received authority or released a password", err)
	}
}
func TestBackupServiceShutdownRetainsOwnershipUntilReadDrains(t *testing.T) {
	s, provider, _ := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	entered, finish := make(chan struct{}), make(chan struct{})
	request := refreshBackupServiceFixture(t, s)
	provider.readFn = func(_ context.Context, cfg localConfiguration, _ backupPasswordUse) (providerReadResult, error) {
		close(entered)
		<-finish
		return inventedServiceRead(cfg), nil
	}
	done := make(chan error, 1)
	go func() { _, err := s.Refresh(t.Context(), "local", localFixtureWorkspace, request); done <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := s.Close(ctx); !errors.Is(err, ErrBusy) {
		t.Fatal("ambiguous shutdown was reported as drained", err)
	}
	if snapshot := s.work.Snapshot(); snapshot.Owners != 1 || snapshot.Active == 0 {
		t.Fatal("shutdown released live ownership", snapshot)
	}
	if _, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace); !errors.Is(err, ErrUnavailable) {
		t.Fatal("closing service admitted another request", err)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if snapshot := s.work.Snapshot(); snapshot.Owners != 0 || snapshot.Active != 0 || provider.closes.Load() != 1 {
		t.Fatal("shutdown did not finish once", snapshot)
	}
}

func TestBackupServiceExpiredOrStaleReviewNeverStartsRead(t *testing.T) {
	s, provider, credentials := newBackupServiceFixture(t)
	review := reviewBackupServiceFixture(t, s)
	s.clock = func() time.Time { return review.ExpiresAt.Add(time.Second) }
	request := ConnectRequest{Generation: review.Generation, Revision: review.Revision, OperationID: uuid.NewString(), ReviewToken: review.Token, AllowMetadataRead: true}
	if _, err := s.Connect(t.Context(), "local", localFixtureWorkspace, request); !errors.Is(err, ErrReviewExpired) {
		t.Fatal("expired review admitted", err)
	}
	s.clock = time.Now
	review = reviewBackupServiceFixture(t, s)
	seedLocalConfiguration(t, s.store) // a distinct, later user configuration
	request = ConnectRequest{Generation: review.Generation, Revision: review.Revision, OperationID: uuid.NewString(), ReviewToken: review.Token, AllowMetadataRead: true}
	if _, err := s.Connect(t.Context(), "local", localFixtureWorkspace, request); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("stale review admitted", err)
	}
	if provider.calls.Load() != 0 || credentials.uses.Load() != 0 {
		t.Fatal("invalid review reached metadata/secret reader")
	}
}

func TestBackupServiceInterruptedRefreshIsNotRestartPermission(t *testing.T) {
	s, provider, _ := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	request := refreshBackupServiceFixture(t, s)
	record, _ := s.store.load(t.Context(), "local", localFixtureWorkspace)
	digest := localDigest([]any{"local", localFixtureWorkspace, "refresh", request})
	_, err := s.store.update(t.Context(), "local", localFixtureWorkspace, record.expected(), changeAdmission, func(r *localRecord) error {
		now := s.clock().UTC()
		appendOperation(r.state, pendingOperation(r.state, request.OperationID, "refresh", digest, now), now)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := s.Refresh(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil || outcome.State != "interrupted" || outcome.Outcome != "cancelled" || provider.calls.Load() != 1 {
		t.Fatal("interrupted durable request repeated the probe", outcome, err)
	}
	status, _ := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if status.LastAttempt == nil || status.LastAttempt.Outcome != "read_ok" || status.LastSuccessfulRead == nil {
		t.Fatal("admission fabricated observation/failure evidence", status)
	}
}

func TestBackupServiceArchiveCancelsOwnedReadWithoutPublishing(t *testing.T) {
	s, provider, _ := newBackupServiceFixture(t)
	connectBackupServiceFixture(t, s)
	request := refreshBackupServiceFixture(t, s)
	entered := make(chan struct{})
	provider.readFn = func(ctx context.Context, _ localConfiguration, _ backupPasswordUse) (providerReadResult, error) {
		close(entered)
		<-ctx.Done()
		return providerReadResult{}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { _, err := s.Refresh(t.Context(), "local", localFixtureWorkspace, request); done <- err }()
	<-entered
	if _, err := s.store.db.ExecContext(t.Context(), `UPDATE workspaces SET status='archived' WHERE id=?`, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrWorkspaceUnavailable) {
			t.Fatal("archive allowed publication", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("archive did not cancel owned read")
	}
	if _, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace); !errors.Is(err, ErrWorkspaceUnavailable) {
		t.Fatal("archived facts exposed", err)
	}
	if _, err := s.store.db.ExecContext(t.Context(), `UPDATE workspaces SET status='active' WHERE id=?`, localFixtureWorkspace); err != nil {
		t.Fatal(err)
	}
	record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if err != nil || record.receipt != nil || len(record.state.Attempts) != 1 {
		t.Fatal("restore revived access or late result", err)
	}
}

func TestBackupServiceLifecycleRoundTripInvalidatesEvenUnsavedReview(t *testing.T) {
	for name, statement := range map[string]string{
		"archive restore":      `UPDATE workspaces SET status='archived';UPDATE workspaces SET status='active'`,
		"uninstall reinstall":  `UPDATE workspaces SET installed_capabilities_json='[]';UPDATE workspaces SET installed_capabilities_json='` + localFixtureInstall + `'`,
		"detach reattach":      `UPDATE continuity_attachments SET state='detached';UPDATE continuity_attachments SET state='native'`,
		"ownership round trip": `INSERT INTO users(id,created_at,updated_at) VALUES('other',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);UPDATE workspaces SET owner_user_id='other';UPDATE workspaces SET owner_user_id='local'`,
	} {
		t.Run(name, func(t *testing.T) {
			s, provider, credentials := newBackupServiceFixture(t)
			review := reviewBackupServiceFixture(t, s)
			if _, err := s.store.db.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			_, err := s.Connect(t.Context(), "local", localFixtureWorkspace, ConnectRequest{Generation: review.Generation, Revision: review.Revision, OperationID: uuid.NewString(), ReviewToken: review.Token, AllowMetadataRead: true})
			if !errors.Is(err, ErrReviewExpired) || provider.calls.Load() != 0 || credentials.uses.Load() != 0 {
				t.Fatal("unsaved review survived lifecycle round trip", err)
			}
			record, err := s.store.load(t.Context(), "local", localFixtureWorkspace)
			if err != nil || record.state != nil || record.receipt != nil || record.authority.Epoch == 0 {
				t.Fatal("fence created configuration/permission or lost epoch", err)
			}
		})
	}
}
