package assistantsetup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"
)

const folderIntentTTL = 15 * time.Minute

type folderIntent struct {
	authorization FolderGrantAuthorization
	expiresAt     time.Time
}

type folderIntentStore struct {
	mu      sync.Mutex
	now     func() time.Time
	records map[string]folderIntent
}

func newFolderIntentStore() *folderIntentStore {
	return &folderIntentStore{now: func() time.Time { return time.Now().UTC() }, records: make(map[string]folderIntent)}
}

func (s *folderIntentStore) issue(authorization FolderGrantAuthorization) (string, error) {
	if s == nil || strings.TrimSpace(authorization.OwnerUserID) == "" ||
		strings.TrimSpace(authorization.RunID) == "" || strings.TrimSpace(authorization.OperationID) == "" ||
		strings.TrimSpace(authorization.WorkspaceID) == "" || authorization.RunRevision < 1 {
		return "", ErrUnavailable
	}
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", ErrUnavailable
	}
	token := base64.RawURLEncoding.EncodeToString(value)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, record := range s.records {
		if !record.expiresAt.After(now) {
			delete(s.records, key)
		}
	}
	s.records[token] = folderIntent{authorization: authorization, expiresAt: now.Add(folderIntentTTL)}
	return token, nil
}

func (s *folderIntentStore) resolve(token, ownerUserID, workspaceID string) (FolderGrantAuthorization, error) {
	if s == nil {
		return FolderGrantAuthorization{}, ErrUnavailable
	}
	token = strings.TrimSpace(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[token]
	if !ok || !record.expiresAt.After(s.now()) {
		delete(s.records, token)
		return FolderGrantAuthorization{}, ErrNotFound
	}
	if record.authorization.OwnerUserID != strings.TrimSpace(ownerUserID) ||
		record.authorization.WorkspaceID != strings.TrimSpace(workspaceID) {
		return FolderGrantAuthorization{}, ErrNotFound
	}
	return record.authorization, nil
}

// BeginFolderIntent claims the durable awaiting-user folder operation and
// issues a short-lived opaque token bound to the exact owner, run, workspace,
// operation, and run revision. It neither opens a picker nor grants a folder.
func (s *Service) BeginFolderIntent(ctx context.Context, ownerUserID, runID string, ifVersion int64) (*Projection, string, error) {
	if s == nil || s.store == nil || s.folderIntents == nil {
		return nil, "", ErrUnavailable
	}
	release, err := s.gate.Enter()
	if err != nil {
		return nil, "", err
	}
	defer release()
	run, operation, err := s.store.ClaimFolderIntent(ctx, ownerUserID, runID, ifVersion)
	if err != nil {
		return nil, "", err
	}
	authorization := FolderGrantAuthorization{
		OwnerUserID: run.OwnerUserID, RunID: run.ID, OperationID: operation.ID,
		WorkspaceID: run.TargetWorkspaceID, RunRevision: run.Revision,
	}
	token, err := s.folderIntents.issue(authorization)
	if err != nil {
		return nil, "", err
	}
	base, err := s.baseProjection(ctx, ownerUserID)
	if err != nil {
		return nil, "", err
	}
	projection, err := s.projectRun(ctx, base, run)
	return projection, token, err
}

// CommitFolderGrant owns the complete admitted consequence window. The HTTP
// adapter supplies a callback into File Janitor's canonical grant operation;
// the coordinator supplies only the prevalidated stable operation identity.
func (s *Service) CommitFolderGrant(
	ctx context.Context,
	ownerUserID, workspaceID, token string,
	commit func(FolderGrantAuthorization) (FolderGrantResult, error),
) (*Projection, error) {
	if s == nil || s.store == nil || s.folderIntents == nil || commit == nil {
		return nil, ErrUnavailable
	}
	authorization, err := s.folderIntents.resolve(token, ownerUserID, workspaceID)
	if err != nil {
		return nil, err
	}
	release, err := s.gate.Enter()
	if err != nil {
		return nil, err
	}
	defer release()
	run, operation, err := s.store.StartFolderGrant(ctx, authorization)
	if err != nil {
		return nil, err
	}
	if operation.Status == OperationSucceeded {
		base, baseErr := s.baseProjection(ctx, ownerUserID)
		if baseErr != nil {
			return nil, baseErr
		}
		return s.projectRun(ctx, base, run)
	}
	result, commitErr := commit(authorization)
	if commitErr != nil {
		safeCode := "folder_grant_failed"
		var typed *FolderGrantCommitError
		if errors.As(commitErr, &typed) && strings.TrimSpace(typed.SafeCode) != "" {
			safeCode = strings.TrimSpace(typed.SafeCode)
		}
		_, _ = s.store.RecordFolderGrantFailure(ctx, authorization, safeCode)
		return nil, commitErr
	}
	updated, err := s.store.CompleteFolderGrant(ctx, authorization, result)
	if err != nil {
		return nil, err
	}
	base, err := s.baseProjection(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	return s.projectRun(ctx, base, updated)
}
