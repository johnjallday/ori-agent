package vault

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

func (s *Store) Export(ctx context.Context, req ExportRequest) (*ExportBundle, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	resolvedVaultID, err := s.resolveVaultID(ctx, req.VaultID)
	if err != nil {
		return nil, err
	}
	req.VaultID = resolvedVaultID
	req.Password = strings.TrimSpace(req.Password)
	if req.Password == "" {
		return nil, ErrExportPasswordEmpty
	}
	selectedVault, err := s.getVault(ctx, req.VaultID)
	if err != nil {
		return nil, err
	}

	records, err := s.ListRecords(ctx, RecordFilter{VaultID: req.VaultID, WorkspaceID: req.WorkspaceID}, AccessContext{})
	if err != nil {
		return nil, err
	}

	fullRecords := make([]Record, 0, len(records))
	for _, item := range records {
		record, err := s.getRecord(ctx, item.ID, AccessContext{}, true)
		if err != nil {
			return nil, err
		}
		fullRecords = append(fullRecords, *record)
	}

	grants, err := s.ListGrants(ctx, req.VaultID, req.WorkspaceID)
	if err != nil {
		return nil, err
	}

	folders, err := s.ListFolders(ctx, req.VaultID)
	if err != nil {
		return nil, err
	}

	envelope := exportEnvelope{
		VaultID:     req.VaultID,
		VaultName:   selectedVault.Name,
		ExportedAt:  s.now(),
		WorkspaceID: req.WorkspaceID,
		Folders:     folders,
		Records:     fullRecords,
		Grants:      grants,
	}

	salt, err := randomBytes(16)
	if err != nil {
		return nil, fmt.Errorf("generate export salt: %w", err)
	}
	derivedKey := derivePassphraseKey(req.Password, salt)
	nonce, ciphertext, err := encryptJSON(derivedKey, envelope)
	if err != nil {
		return nil, fmt.Errorf("encrypt vault export: %w", err)
	}

	bundle := &ExportBundle{
		Version:     1,
		VaultID:     req.VaultID,
		VaultName:   selectedVault.Name,
		WorkspaceID: req.WorkspaceID,
		Salt:        base64.StdEncoding.EncodeToString(salt),
		Nonce:       nonce,
		Ciphertext:  ciphertext,
		ExportedAt:  envelope.ExportedAt,
		RecordCount: len(fullRecords),
		GrantCount:  len(grants),
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID:     req.VaultID,
		WorkspaceID: req.WorkspaceID,
		Action:      "export",
		Outcome:     "allowed",
		Details:     fmt.Sprintf(`{"record_count":%d,"grant_count":%d}`, bundle.RecordCount, bundle.GrantCount),
	})

	return bundle, nil
}

func DecryptExportBundle(bundle ExportBundle, password string) ([]byte, error) {
	password = strings.TrimSpace(password)
	if password == "" {
		return nil, ErrImportPasswordRequired
	}
	if strings.TrimSpace(bundle.Salt) == "" || strings.TrimSpace(bundle.Nonce) == "" || strings.TrimSpace(bundle.Ciphertext) == "" {
		return nil, ErrImportBundleRequired
	}

	salt, err := base64.StdEncoding.DecodeString(bundle.Salt)
	if err != nil {
		return nil, ErrImportBundleInvalid
	}
	derivedKey := derivePassphraseKey(password, salt)
	plaintext, err := decryptBytes(derivedKey, bundle.Nonce, bundle.Ciphertext)
	if err != nil {
		return nil, ErrImportPasswordInvalid
	}
	return plaintext, nil
}

func (s *Store) Import(ctx context.Context, req ImportRequest) (*ImportResult, error) {
	if s.db == nil {
		return nil, ErrSecretStoreUnavailable
	}

	req.Password = strings.TrimSpace(req.Password)
	if req.Password == "" {
		return nil, ErrImportPasswordRequired
	}
	if strings.TrimSpace(req.Bundle.Salt) == "" || strings.TrimSpace(req.Bundle.Nonce) == "" || strings.TrimSpace(req.Bundle.Ciphertext) == "" {
		return nil, ErrImportBundleRequired
	}

	decrypted, err := DecryptExportBundle(req.Bundle, req.Password)
	if err != nil {
		return nil, err
	}

	var envelope exportEnvelope
	if err := json.Unmarshal(decrypted, &envelope); err != nil {
		return nil, ErrImportBundleInvalid
	}

	var targetVault Vault
	createdVault := false

	if strings.TrimSpace(req.TargetVaultID) != "" {
		resolvedVaultID, err := s.resolveVaultID(ctx, req.TargetVaultID)
		if err != nil {
			return nil, err
		}
		targetVault, err = s.getVault(ctx, resolvedVaultID)
		if err != nil {
			return nil, err
		}
	} else {
		name := strings.TrimSpace(req.NewVaultName)
		if name == "" {
			name = strings.TrimSpace(envelope.VaultName)
		}
		if name == "" {
			name = "Imported Vault"
		}

		item := Vault{
			Name:        name,
			Description: strings.TrimSpace(req.NewVaultDescription),
		}
		if err := s.CreateVault(ctx, &item, req.NewVaultPassword); err != nil {
			return nil, err
		}
		targetVault = item
		createdVault = true
	}

	importedRecords := 0
	for _, item := range envelope.Folders {
		folder := item
		folder.ID = ""
		folder.VaultID = targetVault.ID
		if _, err := s.CreateFolder(ctx, &folder); err != nil {
			return nil, err
		}
	}

	for _, item := range envelope.Records {
		record := item
		record.ID = ""
		record.VaultID = targetVault.ID
		if strings.TrimSpace(record.Source) == "" {
			record.Source = "import"
		}
		if err := s.CreateRecord(ctx, &record, AccessContext{}); err != nil {
			return nil, err
		}
		importedRecords++
	}

	importedGrants := 0
	if req.RestoreGrants {
		for _, item := range envelope.Grants {
			grant := item
			grant.ID = ""
			grant.VaultID = targetVault.ID
			if err := s.CreateGrant(ctx, &grant); err != nil {
				return nil, err
			}
			importedGrants++
		}
	}

	targetVault, err = s.getVault(ctx, targetVault.ID)
	if err != nil {
		return nil, err
	}

	s.writeAuditBestEffort(ctx, AuditEvent{
		VaultID: targetVault.ID,
		Action:  "import",
		Outcome: "allowed",
		Details: fmt.Sprintf(`{"record_count":%d,"grant_count":%d,"created_vault":%t,"source_vault_name":%q}`, importedRecords, importedGrants, createdVault, envelope.VaultName),
	})

	return &ImportResult{
		Vault:           targetVault,
		CreatedVault:    createdVault,
		SourceVaultName: strings.TrimSpace(envelope.VaultName),
		RecordCount:     importedRecords,
		GrantCount:      importedGrants,
	}, nil
}
