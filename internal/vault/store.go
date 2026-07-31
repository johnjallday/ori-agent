package vault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/logger"
)

type StoreOptions struct {
	// VaultFilesBaseDir should be set to t.TempDir() by tests that use an
	// in-memory catalog database so backing vault packages share the test's
	// automatic cleanup lifecycle.
	VaultFilesBaseDir string
	ManagedVaultRoot  string
	Clock             func() time.Time
}

type Store struct {
	db                *database.DB
	vaultFilesBaseDir string
	managedVaultRoot  string
	now               func() time.Time

	mu         sync.RWMutex
	cachedDEKs map[string][]byte
}

type encryptedRecordMetadata struct {
	Label      string   `json:"label"`
	FolderPath string   `json:"folder_path,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

type recordRow struct {
	ID                 string
	VaultID            string
	Type               string
	WorkspaceID        string
	Source             string
	RetentionPolicy    string
	MetadataNonce      string
	MetadataCiphertext string
	PayloadNonce       string
	PayloadCiphertext  string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type vaultKeyMaterial struct {
	Salt              string
	Nonce             string
	Ciphertext        string
	PasswordProtected bool
}

type exportEnvelope struct {
	VaultID     string    `json:"vault_id,omitempty"`
	VaultName   string    `json:"vault_name,omitempty"`
	ExportedAt  time.Time `json:"exported_at"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	Folders     []Folder  `json:"folders,omitempty"`
	Records     []Record  `json:"records"`
	Grants      []Grant   `json:"grants"`
}

func NewStore(db *database.DB, opts StoreOptions) *Store {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	vaultFilesBaseDir := strings.TrimSpace(opts.VaultFilesBaseDir)
	if vaultFilesBaseDir == "" {
		dbPath := ""
		if db != nil {
			dbPath = db.Path()
		}
		vaultFilesBaseDir = defaultVaultFilesBaseDir(dbPath)
	}
	managedVaultRoot := strings.TrimSpace(opts.ManagedVaultRoot)
	if managedVaultRoot == "" {
		managedVaultRoot = filepath.Join(vaultFilesBaseDir, "vaults")
	} else {
		managedVaultRoot = filepath.Clean(managedVaultRoot)
	}

	return &Store{
		db:                db,
		vaultFilesBaseDir: vaultFilesBaseDir,
		managedVaultRoot:  managedVaultRoot,
		now:               clock,
		cachedDEKs:        make(map[string][]byte),
	}
}

func (s *Store) defaultVaultFilePath(vaultID string) string {
	return s.catalogFilePathForAbsolutePath(vaultID, s.managedVaultFileAbsolutePath(vaultID))
}

func (s *Store) managedVaultRootDir() string {
	s.mu.RLock()
	root := strings.TrimSpace(s.managedVaultRoot)
	s.mu.RUnlock()

	if root == "" {
		baseDir := strings.TrimSpace(s.vaultFilesBaseDir)
		if baseDir == "" {
			baseDir = defaultVaultFilesBaseDir("")
		}
		return filepath.Join(baseDir, "vaults")
	}
	return root
}

func (s *Store) managedVaultFileAbsolutePath(vaultID string) string {
	vaultID = normalizeVaultID(vaultID)
	if vaultID == "" {
		vaultID = uuid.New().String()
	}
	return filepath.Join(s.managedVaultRootDir(), defaultVaultPackageFilePath(vaultID))
}

func (s *Store) SetManagedVaultRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		root = filepath.Join(strings.TrimSpace(s.vaultFilesBaseDir), "vaults")
	}

	normalized, err := normalizeVaultStorageDirectory(root)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.managedVaultRoot = normalized
	s.mu.Unlock()
	return nil
}

func (s *Store) vaultFileNameForPath(filePath string) string {
	filePath = strings.TrimSpace(filePath)
	if filePath != "" {
		if fileName := strings.TrimSpace(filepath.Base(filePath)); fileName != "" && fileName != "." {
			return fileName
		}
	}
	return vaultPackageDatabaseFileName
}

func (s *Store) resolveRelinkTargetAbsolutePath(vaultID string, currentFilePath string, storage VaultStorage) (string, error) {
	mode := normalizeVaultStorageMode(storage.Mode)
	switch mode {
	case VaultStorageModeManaged:
		return s.resolveVaultFileAbsolutePath(s.defaultVaultFilePath(vaultID)), nil
	case VaultStorageModeCustomDir:
		directory, err := normalizeVaultStorageDirectory(storage.Directory)
		if err != nil {
			return "", err
		}

		candidates := []string{
			filepath.Join(directory, vaultPackageDatabaseFileName),
			filepath.Join(directory, defaultVaultPackageFilePath(vaultID)),
		}

		legacyFileName := s.vaultFileNameForPath(currentFilePath)
		if legacyFileName != "" {
			legacyCandidate := filepath.Join(directory, legacyFileName)
			alreadyIncluded := false
			for _, candidate := range candidates {
				if candidate == legacyCandidate {
					alreadyIncluded = true
					break
				}
			}
			if !alreadyIncluded {
				candidates = append(candidates, legacyCandidate)
			}
		}

		for _, candidate := range candidates {
			exists, err := vaultFileExists(candidate)
			if err != nil {
				return "", fmt.Errorf("inspect relink target: %w", err)
			}
			if exists {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("%w: selected folder does not contain the expected vault package", ErrVaultStoragePathInvalid)
	default:
		return "", ErrVaultStorageModeInvalid
	}
}

func (s *Store) catalogFilePathForAbsolutePath(vaultID string, absolutePath string) string {
	absolutePath = filepath.Clean(strings.TrimSpace(absolutePath))
	if absolutePath == "" {
		return ""
	}

	defaultAbsolutePath := filepath.Clean(s.managedVaultFileAbsolutePath(vaultID))
	if absolutePath == defaultAbsolutePath {
		baseDir := strings.TrimSpace(s.vaultFilesBaseDir)
		if baseDir != "" {
			if relPath, err := filepath.Rel(baseDir, absolutePath); err == nil && !strings.HasPrefix(relPath, "..") && relPath != "." {
				return filepath.Clean(relPath)
			}
		}
		return absolutePath
	}
	return absolutePath
}

func (s *Store) resolveCreateVaultFilePath(vaultID string, currentFilePath string, storage VaultStorage) (string, error) {
	currentFilePath = strings.TrimSpace(currentFilePath)
	mode := normalizeVaultStorageMode(storage.Mode)

	if currentFilePath != "" && mode == VaultStorageModeManaged {
		return currentFilePath, nil
	}

	switch mode {
	case VaultStorageModeManaged:
		return s.defaultVaultFilePath(vaultID), nil
	case VaultStorageModeCustomDir:
		directory, err := normalizeVaultStorageDirectory(storage.Directory)
		if err != nil {
			return "", err
		}

		absolutePath := filepath.Join(resolveVaultPackageDirectory(directory, vaultID), vaultPackageDatabaseFileName)
		exists, err := vaultFileExists(absolutePath)
		if err != nil {
			return "", fmt.Errorf("inspect vault storage path: %w", err)
		}
		if exists {
			return "", ErrVaultStoragePathConflict
		}
		return s.catalogFilePathForAbsolutePath(vaultID, absolutePath), nil
	default:
		return "", ErrVaultStorageModeInvalid
	}
}

func (s *Store) resolveVaultFileAbsolutePath(filePath string) string {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return ""
	}
	if filepath.IsAbs(filePath) {
		return filePath
	}
	baseDir := strings.TrimSpace(s.vaultFilesBaseDir)
	if baseDir == "" {
		baseDir = defaultVaultFilesBaseDir("")
	}
	return filepath.Join(baseDir, filePath)
}

func (s *Store) decorateVault(item *Vault) {
	if item == nil {
		return
	}

	absolutePath := strings.TrimSpace(s.resolveVaultFileAbsolutePath(item.FilePath))
	defaultAbsolutePath := strings.TrimSpace(s.resolveVaultFileAbsolutePath(s.defaultVaultFilePath(item.ID)))

	item.StorageMode = VaultStorageModeManaged
	if absolutePath != "" && absolutePath != defaultAbsolutePath && filepath.IsAbs(strings.TrimSpace(item.FilePath)) {
		item.StorageMode = VaultStorageModeCustomDir
	}
	item.LocationSummary = ""
	item.FileMissing = false

	if absolutePath == "" {
		return
	}

	item.LocationSummary = filepath.Dir(absolutePath)
	exists, err := vaultFileExists(absolutePath)
	if err == nil && !exists {
		item.FileMissing = true
	}
}

func (s *Store) listVaultCatalog(ctx context.Context) ([]Vault, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, COALESCE(file_path, ''), created_at, updated_at
		FROM vaults
		WHERE TRIM(COALESCE(file_path, '')) <> ''
		ORDER BY LOWER(name) ASC, created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query vault catalog: %w", err)
	}
	defer func() { _ = rows.Close() }()

	vaults := make([]Vault, 0)
	for rows.Next() {
		var item Vault
		if err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.Description,
			&item.FilePath,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan vault catalog: %w", err)
		}
		item.ID = normalizeVaultID(item.ID)
		item.IsDefault = item.ID == DefaultVaultID
		item.PasswordProtected = true
		s.decorateVault(&item)
		vaults = append(vaults, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vault catalog: %w", err)
	}
	return vaults, nil
}

func (s *Store) getVaultCatalog(ctx context.Context, vaultID string) (Vault, error) {
	if s.db == nil {
		return Vault{}, ErrSecretStoreUnavailable
	}

	vaultID = normalizeVaultID(vaultID)
	if vaultID == "" {
		return Vault{}, ErrVaultRequired
	}

	var item Vault
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, COALESCE(file_path, ''), created_at, updated_at
		FROM vaults
		WHERE id = ?
		  AND TRIM(COALESCE(file_path, '')) <> ''
	`, vaultID).Scan(
		&item.ID,
		&item.Name,
		&item.Description,
		&item.FilePath,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Vault{}, ErrVaultNotFound
	}
	if err != nil {
		return Vault{}, fmt.Errorf("get vault catalog: %w", err)
	}

	item.ID = normalizeVaultID(item.ID)
	item.IsDefault = item.ID == DefaultVaultID
	item.PasswordProtected = true
	s.decorateVault(&item)
	return item, nil
}

func (s *Store) openVaultContentDB(ctx context.Context, vaultID string) (*sql.DB, error) {
	selectedVault, err := s.getVaultCatalog(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(selectedVault.FilePath) == "" {
		return nil, ErrVaultKeyUnavailable
	}

	vaultDB, err := openExistingVaultFile(ctx, s.resolveVaultFileAbsolutePath(selectedVault.FilePath))
	if err != nil {
		return nil, fmt.Errorf("open vault content database: %w", err)
	}
	if _, err := loadVaultFileMetadata(ctx, vaultDB, selectedVault.ID); err != nil {
		_ = vaultDB.Close()
		return nil, err
	}
	return vaultDB, nil
}

func (s *Store) ListVaults(ctx context.Context) ([]Vault, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	vaults, err := s.listVaultCatalog(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]Vault, 0, len(vaults))
	for index := range vaults {
		if vaults[index].FileMissing {
			vaults[index].RecordCount = 0
			filtered = append(filtered, vaults[index])
			continue
		}

		count, err := s.recordCount(ctx, vaults[index].ID)
		if err != nil {
			if errors.Is(err, ErrVaultFileMissing) {
				vaults[index].FileMissing = true
				vaults[index].RecordCount = 0
				filtered = append(filtered, vaults[index])
				continue
			}
			if errors.Is(err, ErrVaultFileCorrupt) || errors.Is(err, ErrVaultKeyUnavailable) || errors.Is(err, ErrVaultNotFound) {
				logger.Warn("Skipping invalid vault catalog entry", logger.Fields{
					"vault_id":  vaults[index].ID,
					"name":      vaults[index].Name,
					"file_path": vaults[index].FilePath,
					"cause":     err.Error(),
				})
				continue
			}
			return nil, err
		}
		vaults[index].RecordCount = count
		filtered = append(filtered, vaults[index])
	}
	return filtered, nil
}

func (s *Store) CreateVault(ctx context.Context, vault *Vault, password string) error {
	return s.CreateVaultWithOptions(ctx, vault, password, CreateVaultOptions{})
}

func (s *Store) CreateVaultWithOptions(ctx context.Context, vault *Vault, password string, opts CreateVaultOptions) error {
	if s.db == nil {
		return ErrSecretStoreUnavailable
	}
	if vault == nil {
		return ErrVaultNameRequired
	}

	vault.Name = strings.TrimSpace(vault.Name)
	vault.Description = strings.TrimSpace(vault.Description)
	vault.FilePath = strings.TrimSpace(vault.FilePath)
	if vault.Name == "" {
		return ErrVaultNameRequired
	}
	if err := validateVaultPassword(password); err != nil {
		return err
	}

	var existingID string
	err := s.db.QueryRowContext(ctx, `
		SELECT id
		FROM vaults
		WHERE LOWER(name) = LOWER(?)
		LIMIT 1
	`, vault.Name).Scan(&existingID)
	switch {
	case err == nil:
		return ErrVaultAlreadyExists
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("query existing vault: %w", err)
	}

	if strings.TrimSpace(vault.ID) == "" {
		vault.ID = uuid.New().String()
	}

	now := s.now()
	vault.ID = normalizeVaultID(vault.ID)
	if vault.FilePath == "" || normalizeVaultStorageMode(opts.Storage.Mode) != VaultStorageModeManaged {
		resolvedFilePath, err := s.resolveCreateVaultFilePath(vault.ID, vault.FilePath, opts.Storage)
		if err != nil {
			return err
		}
		vault.FilePath = resolvedFilePath
	}
	vault.IsDefault = vault.ID == DefaultVaultID
	vault.PasswordProtected = true
	vault.CreatedAt = now
	vault.UpdatedAt = now
	vault.RecordCount = 0

	dek, err := generateDataEncryptionKey()
	if err != nil {
		return fmt.Errorf("generate data encryption key: %w", err)
	}
	keySalt, keyNonce, keyCiphertext, err := wrapVaultDataEncryptionKey(password, dek)
	if err != nil {
		return err
	}

	absVaultFilePath := s.resolveVaultFileAbsolutePath(vault.FilePath)
	vaultFileDB, err := openVaultFile(ctx, absVaultFilePath)
	if err != nil {
		return fmt.Errorf("initialize vault file: %w", err)
	}
	defer func() { _ = vaultFileDB.Close() }()

	if err := upsertVaultFileMetadata(ctx, vaultFileDB, vaultFileMetadata{
		VaultID:       vault.ID,
		Name:          vault.Name,
		Description:   vault.Description,
		KeySalt:       keySalt,
		KeyNonce:      keyNonce,
		KeyCiphertext: keyCiphertext,
		CreatedAt:     vault.CreatedAt,
		UpdatedAt:     vault.UpdatedAt,
	}); err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO vaults (id, name, description, file_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, vault.ID, vault.Name, vault.Description, vault.FilePath, vault.CreatedAt, vault.UpdatedAt); err != nil {
		_ = os.Remove(absVaultFilePath)
		return fmt.Errorf("insert vault: %w", err)
	}

	s.mu.Lock()
	s.cachedDEKs[vault.ID] = append([]byte(nil), dek...)
	s.mu.Unlock()

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID: vault.ID,
		Action:  "vault.create",
		Outcome: "allowed",
		Details: fmt.Sprintf(`{"name":%q}`, vault.Name),
	})

	s.decorateVault(vault)
	return nil
}

func (s *Store) UpdateVault(ctx context.Context, vaultID string, name string, description string) (Vault, error) {
	if s.db == nil {
		return Vault{}, ErrSecretStoreUnavailable
	}

	vaultID = normalizeVaultID(vaultID)
	current, err := s.getVault(ctx, vaultID)
	if err != nil {
		return Vault{}, err
	}

	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" {
		return Vault{}, ErrVaultNameRequired
	}

	var existingID string
	err = s.db.QueryRowContext(ctx, `
		SELECT id
		FROM vaults
		WHERE LOWER(name) = LOWER(?) AND id <> ?
		LIMIT 1
	`, name, vaultID).Scan(&existingID)
	switch {
	case err == nil:
		return Vault{}, ErrVaultAlreadyExists
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return Vault{}, fmt.Errorf("query existing vault: %w", err)
	}

	now := s.now()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE vaults
		SET name = ?, description = ?, updated_at = ?
		WHERE id = ?
	`, name, description, now, vaultID); err != nil {
		return Vault{}, fmt.Errorf("update vault: %w", err)
	}

	current.Name = name
	current.Description = description
	current.UpdatedAt = now

	if current.FilePath != "" {
		vaultFileDB, err := openExistingVaultFile(ctx, s.resolveVaultFileAbsolutePath(current.FilePath))
		if err != nil {
			return Vault{}, fmt.Errorf("open vault file: %w", err)
		}
		defer func() { _ = vaultFileDB.Close() }()

		metadata, err := loadVaultFileMetadata(ctx, vaultFileDB, vaultID)
		if err != nil {
			return Vault{}, err
		}
		metadata.Name = current.Name
		metadata.Description = current.Description
		metadata.UpdatedAt = current.UpdatedAt
		if err := upsertVaultFileMetadata(ctx, vaultFileDB, metadata); err != nil {
			return Vault{}, err
		}
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID: vaultID,
		Action:  "vault.update",
		Outcome: "allowed",
		Details: fmt.Sprintf(`{"name":%q}`, current.Name),
	})

	return current, nil
}

func (s *Store) RelinkVault(ctx context.Context, vaultID string, storage VaultStorage) (Vault, error) {
	if s.db == nil {
		return Vault{}, ErrSecretStoreUnavailable
	}

	vaultID = normalizeVaultID(vaultID)
	current, err := s.getVaultCatalog(ctx, vaultID)
	if err != nil {
		return Vault{}, err
	}

	mode := normalizeVaultStorageMode(storage.Mode)
	if mode == "" {
		mode = VaultStorageModeCustomDir
	}
	if mode != VaultStorageModeManaged && mode != VaultStorageModeCustomDir {
		return Vault{}, ErrVaultStorageModeInvalid
	}

	targetAbsolutePath, err := s.resolveRelinkTargetAbsolutePath(vaultID, current.FilePath, storage)
	if err != nil {
		return Vault{}, err
	}

	targetCatalogPath := s.catalogFilePathForAbsolutePath(vaultID, targetAbsolutePath)
	var existingID string
	err = s.db.QueryRowContext(ctx, `
		SELECT id
		FROM vaults
		WHERE id <> ? AND file_path = ?
		LIMIT 1
	`, vaultID, targetCatalogPath).Scan(&existingID)
	switch {
	case err == nil:
		return Vault{}, ErrVaultStoragePathConflict
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return Vault{}, fmt.Errorf("query relink path conflict: %w", err)
	}

	vaultFileDB, err := openExistingVaultFile(ctx, targetAbsolutePath)
	if err != nil {
		return Vault{}, fmt.Errorf("open relink target: %w", err)
	}
	defer func() { _ = vaultFileDB.Close() }()

	metadata, err := loadVaultFileMetadata(ctx, vaultFileDB, vaultID)
	if err != nil {
		return Vault{}, err
	}

	now := s.now()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE vaults
		SET file_path = ?, updated_at = ?
		WHERE id = ?
	`, targetCatalogPath, now, vaultID); err != nil {
		return Vault{}, fmt.Errorf("update vault relink path: %w", err)
	}

	metadata.Name = current.Name
	metadata.Description = current.Description
	metadata.UpdatedAt = now
	if err := upsertVaultFileMetadata(ctx, vaultFileDB, metadata); err != nil {
		return Vault{}, err
	}

	s.mu.Lock()
	delete(s.cachedDEKs, vaultID)
	s.mu.Unlock()

	updatedVault, err := s.getVault(ctx, vaultID)
	if err != nil {
		return Vault{}, err
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID: vaultID,
		Action:  "vault.relink",
		Outcome: "allowed",
		Details: fmt.Sprintf(`{"file_path":%q}`, updatedVault.FilePath),
	})

	return updatedVault, nil
}

func (s *Store) DeleteVault(ctx context.Context, vaultID string) error {
	return s.DeleteVaultWithOptions(ctx, vaultID, DeleteVaultOptions{})
}

func (s *Store) DeleteVaultWithOptions(ctx context.Context, vaultID string, opts DeleteVaultOptions) error {
	if s.db == nil {
		return ErrSecretStoreUnavailable
	}

	vaultID = normalizeVaultID(vaultID)
	selectedVault, err := s.getVaultCatalog(ctx, vaultID)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete vault transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	deleteStatements := []string{
		`DELETE FROM vaults WHERE id = ?`,
	}
	for _, stmt := range deleteStatements {
		if _, err := tx.ExecContext(ctx, stmt, vaultID); err != nil {
			return fmt.Errorf("delete vault data: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete vault transaction: %w", err)
	}

	s.mu.Lock()
	delete(s.cachedDEKs, vaultID)
	s.mu.Unlock()

	if opts.DeleteFile && selectedVault.FilePath != "" {
		targetPath := s.resolveVaultFileAbsolutePath(selectedVault.FilePath)
		deleteErr := error(nil)
		if packageDir := vaultPackageDirectoryForFilePath(targetPath); packageDir != "" {
			deleteErr = os.RemoveAll(packageDir)
		} else {
			deleteErr = os.Remove(targetPath)
		}
		if deleteErr != nil && !errors.Is(deleteErr, os.ErrNotExist) {
			logger.Warn("Failed to delete vault file", logger.Fields{
				"vault_id":   vaultID,
				"file_path":  selectedVault.FilePath,
				"vault_name": selectedVault.Name,
				"error":      deleteErr,
			})
		}
	}

	logger.Info("Deleted vault", logger.Fields{
		"vault_id":    vaultID,
		"vault_name":  selectedVault.Name,
		"delete_file": opts.DeleteFile,
		"is_default":  selectedVault.IsDefault,
	})
	return nil
}

func (s *Store) Status(ctx context.Context, vaultID string) (VaultStatus, error) {
	if s.db == nil {
		return VaultStatus{
			VaultID:   normalizeVaultID(vaultID),
			Available: false,
			Locked:    true,
			Writable:  false,
			Message:   "vault database is unavailable",
			SecretStore: StoreStatus{
				Backend:   BackendUnavailable,
				Available: false,
				Writable:  false,
				Locked:    true,
				Message:   "vault database is unavailable",
			},
		}, nil
	}

	vaultID = normalizeVaultID(vaultID)
	if vaultID == "" {
		vaults, err := s.ListVaults(ctx)
		if err != nil {
			return VaultStatus{}, err
		}
		switch len(vaults) {
		case 0:
			storeStatus := passwordProtectedStoreStatus(true)
			return VaultStatus{
				Available:   false,
				Locked:      true,
				Writable:    false,
				Message:     "create a vault to begin storing encrypted records",
				RecordCount: 0,
				SecretStore: storeStatus,
			}, nil
		case 1:
			vaultID = vaults[0].ID
		default:
			return VaultStatus{}, ErrVaultRequired
		}
	}

	selectedVault, err := s.getVault(ctx, vaultID)
	if err != nil {
		return VaultStatus{}, err
	}

	if selectedVault.FileMissing {
		storeStatus := passwordProtectedStoreStatus(true)
		return VaultStatus{
			VaultID:           selectedVault.ID,
			VaultName:         selectedVault.Name,
			StorageMode:       selectedVault.StorageMode,
			LocationSummary:   selectedVault.LocationSummary,
			FileMissing:       true,
			Available:         false,
			Locked:            true,
			Writable:          false,
			PasswordProtected: selectedVault.PasswordProtected,
			Message:           "vault storage is missing; relink the vault or restore its folder",
			RecordCount:       0,
			SecretStore:       storeStatus,
		}, nil
	}

	locked := !s.hasCachedDEK(vaultID)
	writable := !locked
	requiresPassphrase := locked
	storeStatus := passwordProtectedStoreStatus(locked)
	message := "vault unlocked with its own password"
	if locked {
		message = "vault locked until unlocked with its password"
	}

	status := VaultStatus{
		VaultID:            selectedVault.ID,
		VaultName:          selectedVault.Name,
		StorageMode:        selectedVault.StorageMode,
		LocationSummary:    selectedVault.LocationSummary,
		FileMissing:        selectedVault.FileMissing,
		Available:          true,
		Locked:             locked,
		Writable:           writable,
		PasswordProtected:  selectedVault.PasswordProtected,
		RequiresPassphrase: requiresPassphrase,
		Message:            message,
		RecordCount:        selectedVault.RecordCount,
		SecretStore:        storeStatus,
	}
	return status, nil
}

func (s *Store) Unlock(ctx context.Context, vaultID string, password string) error {
	if s.db == nil {
		return ErrSecretStoreUnavailable
	}

	vaultID, err := s.resolveVaultID(ctx, vaultID)
	if err != nil {
		return err
	}
	keyMaterial, err := s.getVaultKeyMaterial(ctx, vaultID)
	if err != nil {
		return err
	}

	if !keyMaterial.PasswordProtected {
		return ErrVaultKeyUnavailable
	}
	if err := validateVaultPassword(password); err != nil {
		return err
	}

	dek, err := unwrapVaultDataEncryptionKey(password, keyMaterial)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.cachedDEKs[vaultID] = append([]byte(nil), dek...)
	s.mu.Unlock()
	return nil
}

func (s *Store) Lock(ctx context.Context, vaultID string) error {
	vaultID = normalizeVaultID(vaultID)

	if s.db == nil {
		return ErrSecretStoreUnavailable
	}
	if vaultID == "" {
		s.mu.Lock()
		s.cachedDEKs = make(map[string][]byte)
		s.mu.Unlock()
		return nil
	}

	s.mu.Lock()
	delete(s.cachedDEKs, vaultID)
	s.mu.Unlock()
	return nil
}

func (s *Store) getVault(ctx context.Context, vaultID string) (Vault, error) {
	item, err := s.getVaultCatalog(ctx, vaultID)
	if err != nil {
		return Vault{}, err
	}
	if item.FileMissing {
		item.RecordCount = 0
		return item, nil
	}
	recordCount, err := s.recordCount(ctx, item.ID)
	if err != nil {
		return Vault{}, err
	}
	item.RecordCount = recordCount
	return item, nil
}

func (s *Store) writeAuditBestEffort(ctx context.Context, event AuditEvent) {
	if s.db == nil {
		return
	}
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = s.now()
	}
	event.VaultID = normalizeVaultID(event.VaultID)
	event.WorkspaceID = strings.TrimSpace(event.WorkspaceID)
	event.ActorType = normalizeActorType(event.ActorType)
	event.ActorID = strings.TrimSpace(event.ActorID)
	event.Action = strings.TrimSpace(event.Action)
	event.RecordID = strings.TrimSpace(event.RecordID)
	event.RecordType = normalizeRecordType(event.RecordType)
	if event.RecordType == "*" {
		event.RecordType = ""
	}
	event.Outcome = strings.TrimSpace(event.Outcome)
	event.Details = strings.TrimSpace(event.Details)
	if event.VaultID == "" {
		return
	}

	vaultDB, err := s.openVaultContentDB(ctx, event.VaultID)
	if err != nil {
		logger.Warn("Failed to open vault audit database", logger.Fields{"vault_id": event.VaultID, "error": err})
		return
	}
	defer func() { _ = vaultDB.Close() }()

	if _, err := vaultDB.ExecContext(ctx, `
		INSERT INTO vault_audit_events (
			id, vault_id, workspace_id, actor_type, actor_id, action, record_id, record_type, outcome, details, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.ID, event.VaultID, event.WorkspaceID, event.ActorType, event.ActorID, event.Action,
		event.RecordID, event.RecordType, event.Outcome, event.Details, event.CreatedAt); err != nil {
		logger.Warn("Failed to write vault audit event", logger.Fields{"error": err})
	}
}

func (s *Store) resolveVaultID(ctx context.Context, vaultID string) (string, error) {
	vaultID = normalizeVaultID(vaultID)
	if vaultID != "" {
		return vaultID, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM vaults
		ORDER BY created_at ASC, LOWER(name) ASC
		LIMIT 2
	`)
	if err != nil {
		return "", fmt.Errorf("resolve vault selection: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := make([]string, 0, 2)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("scan resolved vault selection: %w", err)
		}
		ids = append(ids, normalizeVaultID(id))
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate resolved vault selection: %w", err)
	}

	if len(ids) == 1 {
		return ids[0], nil
	}
	return "", ErrVaultRequired
}
