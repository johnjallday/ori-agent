package vault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

func (s *Store) ListGrants(ctx context.Context, vaultID string, workspaceID string) ([]Grant, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	vaultID, err := s.resolveVaultID(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if _, err := s.getVault(ctx, vaultID); err != nil {
		return nil, err
	}
	vaultDB, err := s.openVaultContentDB(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = vaultDB.Close() }()
	query := `
		SELECT id, vault_id, workspace_id, actor_type, actor_id, capability, record_type, created_at, updated_at
		FROM vault_grants
		WHERE vault_id = ?
		  AND (? = '' OR workspace_id = ?)
		ORDER BY updated_at DESC
	`
	rows, err := vaultDB.QueryContext(ctx, query, vaultID, workspaceID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query vault grants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	grants := make([]Grant, 0)
	for rows.Next() {
		var grant Grant
		if err := rows.Scan(
			&grant.ID,
			&grant.VaultID,
			&grant.WorkspaceID,
			&grant.ActorType,
			&grant.ActorID,
			&grant.Capability,
			&grant.RecordType,
			&grant.CreatedAt,
			&grant.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan vault grant: %w", err)
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vault grants: %w", err)
	}
	return grants, nil
}

func (s *Store) CreateGrant(ctx context.Context, grant *Grant) error {
	if s.db == nil {
		return ErrSecretStoreUnavailable
	}
	if grant == nil {
		return fmt.Errorf("vault: grant is required")
	}

	grant.WorkspaceID = strings.TrimSpace(grant.WorkspaceID)
	resolvedVaultID, err := s.resolveVaultID(ctx, grant.VaultID)
	if err != nil {
		return err
	}
	grant.VaultID = resolvedVaultID
	grant.ActorType = normalizeActorType(grant.ActorType)
	grant.ActorID = strings.TrimSpace(grant.ActorID)
	grant.Capability = normalizeCapability(grant.Capability)
	grant.RecordType = normalizeRecordType(grant.RecordType)
	if grant.WorkspaceID == "" || grant.ActorID == "" || (grant.ActorType != ActorTypeAgent && grant.ActorType != ActorTypePlugin) || grant.Capability == "" {
		return fmt.Errorf("vault: workspace_id, actor_type, actor_id, and capability are required")
	}
	if _, err := s.getVault(ctx, grant.VaultID); err != nil {
		return err
	}
	vaultDB, err := s.openVaultContentDB(ctx, grant.VaultID)
	if err != nil {
		return err
	}
	defer func() { _ = vaultDB.Close() }()

	now := s.now()

	var existingID string
	var existingCreatedAt time.Time
	err = vaultDB.QueryRowContext(ctx, `
		SELECT id, created_at
		FROM vault_grants
		WHERE vault_id = ? AND workspace_id = ? AND actor_type = ? AND actor_id = ? AND capability = ? AND record_type = ?
	`, grant.VaultID, grant.WorkspaceID, grant.ActorType, grant.ActorID, grant.Capability, grant.RecordType).Scan(&existingID, &existingCreatedAt)
	switch {
	case err == nil:
		grant.ID = existingID
		grant.CreatedAt = existingCreatedAt
		grant.UpdatedAt = now
		_, err = vaultDB.ExecContext(ctx, `
			UPDATE vault_grants
			SET updated_at = ?
			WHERE id = ?
		`, now, existingID)
		if err != nil {
			return fmt.Errorf("refresh vault grant: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		if grant.ID == "" {
			grant.ID = uuid.New().String()
		}
		grant.CreatedAt = now
		grant.UpdatedAt = now
		_, err = vaultDB.ExecContext(ctx, `
			INSERT INTO vault_grants (
				id, vault_id, workspace_id, actor_type, actor_id, capability, record_type, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, grant.ID, grant.VaultID, grant.WorkspaceID, grant.ActorType, grant.ActorID, grant.Capability, grant.RecordType, grant.CreatedAt, grant.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert vault grant: %w", err)
		}
	default:
		return fmt.Errorf("query existing vault grant: %w", err)
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID:     grant.VaultID,
		WorkspaceID: grant.WorkspaceID,
		Action:      "grant.create",
		RecordType:  grant.RecordType,
		Outcome:     "allowed",
		Details:     fmt.Sprintf(`{"actor_type":%q,"actor_id":%q,"capability":%q}`, grant.ActorType, grant.ActorID, grant.Capability),
	})

	return nil
}

func (s *Store) DeleteGrant(ctx context.Context, id string) error {
	if s.db == nil {
		return ErrSecretStoreUnavailable
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return ErrGrantNotFound
	}

	vaults, err := s.listVaultCatalog(ctx)
	if err != nil {
		return err
	}

	var grant Grant
	var vaultDB *sql.DB
	for _, item := range vaults {
		vaultDB, err = openExistingVaultFile(ctx, s.resolveVaultFileAbsolutePath(item.FilePath))
		if err != nil {
			if errors.Is(err, ErrVaultFileMissing) || errors.Is(err, ErrVaultFileCorrupt) {
				continue
			}
			return fmt.Errorf("open vault content database: %w", err)
		}

		err = vaultDB.QueryRowContext(ctx, `
			SELECT id, vault_id, workspace_id, actor_type, actor_id, capability, record_type, created_at, updated_at
			FROM vault_grants
			WHERE id = ?
		`, id).Scan(
			&grant.ID,
			&grant.VaultID,
			&grant.WorkspaceID,
			&grant.ActorType,
			&grant.ActorID,
			&grant.Capability,
			&grant.RecordType,
			&grant.CreatedAt,
			&grant.UpdatedAt,
		)
		if err == nil {
			break
		}
		_ = vaultDB.Close()
		vaultDB = nil
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		return fmt.Errorf("get vault grant: %w", err)
	}
	if vaultDB == nil {
		return ErrGrantNotFound
	}
	defer func() { _ = vaultDB.Close() }()

	result, err := vaultDB.ExecContext(ctx, `DELETE FROM vault_grants WHERE id = ?`, grant.ID)
	if err != nil {
		return fmt.Errorf("delete vault grant: %w", err)
	}
	if err := database.CheckRowsAffectedWithError(result, "vault_grant", ErrGrantNotFound); err != nil {
		return err
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID:     grant.VaultID,
		WorkspaceID: grant.WorkspaceID,
		Action:      "grant.delete",
		RecordType:  grant.RecordType,
		Outcome:     "allowed",
		Details:     fmt.Sprintf(`{"actor_type":%q,"actor_id":%q,"capability":%q}`, grant.ActorType, grant.ActorID, grant.Capability),
	})

	return nil
}

func (s *Store) authorizeList(ctx context.Context, access AccessContext, filter RecordFilter) error {
	if !access.requiresGrant() {
		return nil
	}
	if !isValidAccessActor(access) {
		s.writeAuditBestEffort(ctx, AuditEvent{
			VaultID:     filter.VaultID,
			WorkspaceID: access.WorkspaceID,
			ActorType:   access.ActorType,
			ActorID:     access.ActorID,
			Action:      "list",
			Outcome:     "denied",
			Details:     `{"reason":"invalid_actor"}`,
		})
		return ErrPermissionDenied
	}
	if access.WorkspaceID == "" || (filter.WorkspaceID != "" && filter.WorkspaceID != access.WorkspaceID) {
		s.writeAuditBestEffort(ctx, AuditEvent{
			VaultID:     filter.VaultID,
			WorkspaceID: access.WorkspaceID,
			ActorType:   access.ActorType,
			ActorID:     access.ActorID,
			Action:      "list",
			Outcome:     "denied",
			Details:     `{"reason":"workspace_scope_missing"}`,
		})
		return ErrPermissionDenied
	}
	if !s.hasAnyGrant(ctx, filter.VaultID, access) {
		s.writeAuditBestEffort(ctx, AuditEvent{
			VaultID:     filter.VaultID,
			WorkspaceID: access.WorkspaceID,
			ActorType:   access.ActorType,
			ActorID:     access.ActorID,
			Action:      "list",
			Outcome:     "denied",
			Details:     `{"reason":"persistent_grant_missing"}`,
		})
		return ErrPermissionDenied
	}
	return nil
}

func (s *Store) authorizeAccess(ctx context.Context, access AccessContext, vaultID string, workspaceID string, capability Capability, recordType string, action string, recordID string) error {
	if !access.requiresGrant() {
		return nil
	}
	if !isValidAccessActor(access) || access.WorkspaceID == "" || workspaceID == "" || access.WorkspaceID != workspaceID {
		s.writeAuditBestEffort(ctx, AuditEvent{
			VaultID:     vaultID,
			WorkspaceID: workspaceID,
			ActorType:   access.ActorType,
			ActorID:     access.ActorID,
			Action:      action,
			RecordID:    recordID,
			RecordType:  recordType,
			Outcome:     "denied",
			Details:     `{"reason":"workspace_scope_missing"}`,
		})
		return ErrPermissionDenied
	}
	if !s.hasGrant(ctx, vaultID, access, workspaceID, capability, recordType) {
		s.writeAuditBestEffort(ctx, AuditEvent{
			VaultID:     vaultID,
			WorkspaceID: workspaceID,
			ActorType:   access.ActorType,
			ActorID:     access.ActorID,
			Action:      action,
			RecordID:    recordID,
			RecordType:  recordType,
			Outcome:     "denied",
			Details:     fmt.Sprintf(`{"reason":"persistent_grant_missing","capability":%q}`, capability),
		})
		return ErrPermissionDenied
	}
	return nil
}

func (s *Store) hasAnyGrant(ctx context.Context, vaultID string, access AccessContext) bool {
	vaultDB, err := s.openVaultContentDB(ctx, vaultID)
	if err != nil {
		return false
	}
	defer func() { _ = vaultDB.Close() }()
	return hasAnyGrantWithExecutor(ctx, vaultDB, vaultID, access)
}

func hasAnyGrantWithExecutor(ctx context.Context, executor attachmentSQLExecutor, vaultID string, access AccessContext) bool {
	var exists int
	err := executor.QueryRowContext(ctx, `
		SELECT 1
		FROM vault_grants
		WHERE vault_id = ? AND workspace_id = ? AND actor_type = ? AND actor_id = ?
		LIMIT 1
	`, normalizeVaultID(vaultID), access.WorkspaceID, access.ActorType, access.ActorID).Scan(&exists)
	return err == nil && exists == 1
}

func (s *Store) hasGrant(ctx context.Context, vaultID string, access AccessContext, workspaceID string, capability Capability, recordType string) bool {
	vaultDB, err := s.openVaultContentDB(ctx, vaultID)
	if err != nil {
		return false
	}
	defer func() { _ = vaultDB.Close() }()
	return hasGrantWithExecutor(ctx, vaultDB, vaultID, access, workspaceID, capability, recordType)
}

func hasGrantWithExecutor(ctx context.Context, executor attachmentSQLExecutor, vaultID string, access AccessContext, workspaceID string, capability Capability, recordType string) bool {
	recordType = normalizeRecordType(recordType)
	capability = normalizeCapability(capability)

	var exists int
	err := executor.QueryRowContext(ctx, `
		SELECT 1
		FROM vault_grants
		WHERE vault_id = ?
		  AND workspace_id = ?
		  AND actor_type = ?
		  AND actor_id = ?
		  AND capability = ?
		  AND (record_type = '*' OR record_type = ?)
		LIMIT 1
	`, normalizeVaultID(vaultID), workspaceID, access.ActorType, access.ActorID, capability, recordType).Scan(&exists)
	return err == nil && exists == 1
}

func isValidAccessActor(access AccessContext) bool {
	access = access.normalized()
	if access.ActorID == "" {
		return false
	}
	return access.ActorType == ActorTypeAgent || access.ActorType == ActorTypePlugin
}
