package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// NativeWorkspaceRecords is implemented by the canonical SQL owner. Both methods
// must use the supplied transaction/read view, never a store pool. Validation
// compares the saved native representation, not the bounded checkpoint DTO.
type NativeWorkspaceRecords interface {
	NativeWorkspaceDatabase() *database.DB
	InsertNativeWorkspace(context.Context, *sql.Tx, *Workspace) error
	ValidateNativeWorkspace(context.Context, workspacecontinuity.Queryer, *Workspace) error
}

type nativeWorkspaceCreator interface {
	createNativeWorkspace(context.Context, *Workspace, func() error) error
}

// CreateNativeWorkspace is the explicit new-work boundary. Call it instead of
// Save ONLY where the application has constructed a new native workspace. It is
// never used by discovery, import, restore, or an upsert of an arbitrary ID.
// Legacy/unconfigured stores retain their previous creation behavior.
func CreateNativeWorkspace(ctx context.Context, target Store, ws *Workspace) error {
	return createNativeWorkspace(ctx, target, ws, nil)
}

func createNativeWorkspace(ctx context.Context, target Store, ws *Workspace, after func() error) error {
	if target == nil || ws == nil {
		return workspacecontinuity.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if creator, ok := target.(nativeWorkspaceCreator); ok {
		return creator.createNativeWorkspace(ctx, ws, after)
	}
	if err := target.Save(ws); err != nil {
		return err
	}
	if after != nil {
		return after()
	}
	return nil
}

func (s *AgentSnapshotStore) createNativeWorkspace(ctx context.Context, ws *Workspace, after func() error) error {
	return createNativeWorkspace(ctx, s.Store, ws, func() error {
		// Keep the inner creator's provisional barrier/reset permit through
		// profile/image publication. A failed inner write remains a barrier.
		s.snapshotReferencedAgents(ws)
		if after != nil {
			return after()
		}
		return nil
	})
}

func (s *FileStore) createNativeWorkspace(ctx context.Context, ws *Workspace, after func() error) error {
	release, err := s.enterContinuityWork()
	if err != nil {
		return err
	}
	defer release()
	if s.hasLocalConfig() {
		// Private native creation requires the shared canonical SQL owner.
		return ErrLocalConfigUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Save(ws); err != nil {
		return err
	}
	if after != nil {
		return after()
	}
	return nil
}

func (s *SyncStore) createNativeWorkspace(ctx context.Context, ws *Workspace, after func() error) error {
	if !s.fileSync.hasLocalConfig() {
		if err := s.Save(ws); err != nil {
			return err
		}
		if after != nil {
			return after()
		}
		return nil
	}
	local := s.fileSync.localConfig
	owner, ok := s.primary.(NativeWorkspaceRecords)
	if !ok || owner.NativeWorkspaceDatabase() != local.db {
		return ErrLocalConfigUnavailable
	}
	if !workspacecontinuity.ValidID(ws.ID) || strings.TrimSpace(ws.Name) == "" || ws.WorkspaceLocalConfigID != "" || ws.OwnerUserID != "" && ws.OwnerUserID != "local" {
		return workspacecontinuity.ErrInvalid
	}
	release, err := local.enterWork()
	if err != nil {
		return err
	}
	defer release()
	unlock := local.locks.Lock(ws.ID + ":native-create")
	defer unlock()
	ws.OwnerUserID = "local"
	if ws.FolderSlug == "" {
		ws.FolderSlug = Slugify(ws.Name)
	} else {
		ws.FolderSlug = Slugify(ws.FolderSlug)
	}
	// An existing disk identity is not a new native workspace, even after
	// app-record reset. Never use explicit creation to adopt an indexed copy.
	s.fileSync.mu.RLock()
	_, existingFolder := s.fileSync.idToPath[ws.ID]
	s.fileSync.mu.RUnlock()
	if existingFolder {
		return workspacecontinuity.ErrConflict
	}
	creation, err := workspacecontinuity.NewLocalStore(local.db).BeginNativeCreation(ctx, ws.ID, func(tx *sql.Tx) error {
		return owner.InsertNativeWorkspace(ctx, tx, ws)
	})
	if err != nil {
		return err
	}
	if err := s.Save(ws); err != nil {
		return creation.Fail(ctx, err)
	}
	if after != nil {
		if err := after(); err != nil {
			return creation.Fail(ctx, err)
		}
	}
	folder, err := s.fileSync.GetFolderPath(ws.ID)
	if err != nil {
		return creation.Fail(ctx, err)
	}
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, WorkspaceConfigFile, maxNativeWorkspaceBytes)
	if err == nil {
		err = verifyNativeCreationFile(ws, data)
	}
	if err == nil {
		// The freshly generated reference must still resolve to this creator's
		// private settings, not merely a syntactically nonempty copied slot.
		var published *Workspace
		published, err = decodeLocalWorkspace(data, ws.ID)
		if err == nil {
			var actual []byte
			var found bool
			actual, found, err = local.Load(ctx, ws.ID, "bindings", "workspace", published.WorkspaceLocalConfigID)
			if err == nil && !found {
				err = ErrLocalConfigUnavailable
			}
			if err == nil {
				var expected []byte
				_, expected, err = splitWorkspaceLocalConfig(ws)
				if err == nil && !bytes.Equal(actual, expected) {
					err = workspacecontinuity.ErrChanged
				}
			}
		}
	}
	if err != nil {
		return creation.Fail(ctx, err)
	}
	fingerprints, err := s.fileSync.nativeCreationProfiles(ctx, ws, folder)
	if err != nil {
		return creation.Fail(ctx, err)
	}
	fingerprints[WorkspaceConfigFile] = workspacecontinuity.Digest(data)
	err = creation.Complete(ctx, func(q workspacecontinuity.Queryer) error {
		if err := owner.ValidateNativeWorkspace(ctx, q, ws); err != nil {
			return err
		}
		for path, digest := range fingerprints {
			limit := workspacecontinuity.MaxRecordBytes
			if path == WorkspaceConfigFile {
				limit = maxNativeWorkspaceBytes
			}
			latest, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, path, limit)
			if err != nil {
				return err
			}
			if workspacecontinuity.Digest(latest) != digest {
				return workspacecontinuity.ErrChanged
			}
		}
		return nil
	})
	if err != nil {
		return creation.Fail(ctx, err)
	}
	return nil
}

// nativeCreationProfiles requires each selected profile's exact canonical
// definition and private slot before admission. A global name or a best-effort
// snapshot warning is not a completed native profile. Unused on-disk profiles
// are not adopted here. Full preparation inventories them separately.
func (s *FileStore) nativeCreationProfiles(ctx context.Context, ws *Workspace, folder string) (map[string]string, error) {
	names := referencedAgentNames(ws)
	if len(names) >= workspacecontinuity.MaxFiles {
		return nil, workspacecontinuity.ErrLimit
	}
	fingerprints := make(map[string]string, len(names)+1)
	for _, name := range names {
		path, _, err := localAgentPath(name)
		if err != nil {
			return nil, err
		}
		if _, duplicate := fingerprints[path]; duplicate {
			return nil, workspacecontinuity.ErrCollision
		}
		data, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, path, workspacecontinuity.MaxRecordBytes)
		if err != nil {
			return nil, err
		}
		profile, err := decodeLocalAgent(data)
		if err != nil {
			return nil, err
		}
		if profile.WorkspaceLocalConfigID == "" {
			return nil, ErrLocalConfigInvalid
		}
		// The already-separated branch verifies a live private slot and denied
		// physical fields. It cannot grant authority from an unmarked file.
		if err := s.localConfig.MigrateAgentFile(ctx, folder, ws.ID, name); err != nil {
			return nil, err
		}
		fingerprints[path] = workspacecontinuity.Digest(data)
	}
	return fingerprints, nil
}

// verifyNativeCreationFile compares the complete newly saved file with the
// creator's denied native projection, independently of checkpoint limits. The
// generated local slot may differ, but no authored field may differ or vanish.
func verifyNativeCreationFile(input *Workspace, data []byte) error {
	if len(data) > maxNativeWorkspaceBytes {
		return workspacecontinuity.ErrLimit
	}
	projection, _, err := splitWorkspaceLocalConfig(input)
	if err != nil {
		return err
	}
	decode := func(data []byte) (map[string]any, error) {
		if !json.Valid(data) {
			return nil, workspacecontinuity.ErrInvalid
		}
		var value map[string]any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil || value == nil {
			return nil, workspacecontinuity.ErrInvalid
		}
		return value, nil
	}
	actual, err := decode(data)
	if err != nil {
		return err
	}
	slot, ok := actual["ori_local_config_id"].(string)
	if !ok || slot == "" {
		return ErrLocalConfigInvalid
	}
	projection.WorkspaceLocalConfigID = slot
	want, err := projection.ToJSON()
	if err != nil {
		return err
	}
	expected, err := decode(want)
	if err != nil {
		return err
	}
	a, errA := json.Marshal(actual)
	b, errB := json.Marshal(expected)
	if err := errors.Join(errA, errB); err != nil {
		return ErrLocalConfigInvalid
	}
	if !bytes.Equal(a, b) {
		return workspacecontinuity.ErrChanged
	}
	return nil
}
