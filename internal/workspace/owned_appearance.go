package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"path"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// WorkspaceAppearanceReader is scoped by canonical workspace/profile identity,
// not by the global roster. Inactive retained uploads are readable too. Failure
// never authorizes a global image lookup or a change to the saved appearance.
type WorkspaceAppearanceReader interface {
	ReadWorkspaceAppearance(context.Context, string, string, string) ([]byte, error)
}

func (s *FileStore) ReadWorkspaceAppearance(ctx context.Context, workspaceID, name, filename string) ([]byte, error) {
	if !workspacecontinuity.ValidID(workspaceID) || !workspacecontinuity.ValidID(name) {
		return nil, workspacecontinuity.ErrInvalid
	}
	location, err := profileAppearancePath(name, filename)
	if err != nil {
		return nil, err
	}
	release, err := s.enterContinuityWork()
	if err != nil {
		return nil, err
	}
	defer release()
	s.mu.RLock()
	defer s.mu.RUnlock()
	rel, ok := s.idToPath[workspaceID]
	if !ok {
		return nil, workspacecontinuity.ErrIncomplete
	}
	folder := s.resolveFolder(rel)
	if s.hasLocalConfig() {
		attachment, err := workspacecontinuity.NewLocalStore(s.localConfig.db).Attachment(ctx, workspaceID)
		if err != nil || !attachment.AllowsManual() {
			return nil, ErrLocalConfigUnavailable
		}
		if _, err := s.localConfig.authority(ctx, workspaceID); err != nil {
			return nil, err
		}
	}
	// Read-only display needs admission and the scoped definition, not secret
	// decryption or model readiness. Never migrate or probe global files here.
	if err := checkAgentFolder(ctx, folder, workspaceID); err != nil {
		return nil, err
	}
	configPath, _, _ := localAgentPath(name) // validated by profileAppearancePath
	definition, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, configPath, workspacecontinuity.MaxRecordBytes)
	if err != nil {
		return nil, err
	}
	ag, err := decodeLocalAgent(definition)
	if err != nil {
		return nil, err
	}
	if !s.hasLocalConfig() && ag.WorkspaceLocalConfigID != "" {
		return nil, ErrLocalConfigUnavailable
	}
	if ag == nil || ag.Appearance == nil || ag.Appearance.UploadedImage() != filename {
		return nil, workspacecontinuity.ErrIncomplete
	}
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, location, maxContinuityAppearanceBytes)
	if err != nil {
		return nil, err
	}
	if err := validateContinuityImage(filename, data); err != nil {
		return nil, err
	}
	return data, nil
}

// seedNativeAgent copies a newly selected global definition and its owned image
// as one bounded source-owner operation. Existing workspace profiles are NEVER
// refreshed from global names. Imported profiles cannot use this native path.
// Missing source bytes remain a missing appearance, not a generated replacement.
func (s *FileStore) seedNativeAgent(ctx context.Context, workspaceID, name string, source *agent.Agent, reader store.OwnedAppearanceReader) error {
	if !s.hasLocalConfig() || !workspacecontinuity.ValidID(name) || source == nil || source.WorkspaceLocalConfigID != "" {
		return ErrLocalConfigUnavailable
	}
	release, err := s.enterContinuityWork()
	if err != nil {
		return err
	}
	defer release()
	configPath, item, err := localAgentPath(name)
	if err != nil {
		return err
	}
	// Clone before appearance canonicalization: global runtime and local-private
	// settings remain untouched. The canonical writer performs key separation.
	encoded, err := json.Marshal(source)
	if err != nil {
		return ErrLocalConfigInvalid
	}
	copy, err := decodeLocalAgent(encoded)
	if err != nil {
		return err
	}
	copy.EnsureAppearance()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.localConfig.native(ctx, workspaceID); err != nil {
		return err
	}
	rel, exists := s.idToPath[workspaceID]
	if !exists {
		return workspacecontinuity.ErrIncomplete
	}
	folder := s.resolveFolder(rel)
	unlock := s.localConfig.locks.Lock(workspaceID + ":agent:" + item)
	defer unlock()
	if err := checkAgentFolder(ctx, folder, workspaceID); err != nil {
		return err
	}
	if _, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, configPath, workspacecontinuity.MaxRecordBytes); !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		if err == nil {
			return workspacecontinuity.ErrConflict
		}
		return err
	}

	filename := copy.Appearance.UploadedImage()
	var image []byte
	if filename != "" && reader != nil {
		image, err = reader.ReadOwnedAgentAppearance(ctx, name, filename)
		if err != nil && !errors.Is(err, workspacecontinuity.ErrIncomplete) {
			return err
		}
		if errors.Is(err, workspacecontinuity.ErrIncomplete) {
			image = nil
		}
		if err == nil {
			if err := validateContinuityImage(filename, image); err != nil {
				return err
			}
		}
	}
	mutation, err := workspacecontinuity.NewLocalStore(s.localConfig.db).BeginFileMutation(ctx, workspaceID, path.Join("agent-seed", item))
	if err != nil {
		return err
	}
	if len(image) != 0 {
		location, err := profileAppearancePath(name, filename)
		if err == nil {
			// Exclusive creation: an orphan or unrelated pre-existing file is
			// not silently claimed, even when its filename/bytes happen to match.
			err = workspacecontinuity.ReplaceCanonicalFile(ctx, folder, location, "", image)
		}
		if err != nil {
			return s.localConfig.finishFileMutation(ctx, mutation, err)
		}
	}
	err = s.localConfig.writeAgentFile(ctx, folder, workspaceID, configPath, item, "", copy)
	return s.localConfig.finishFileMutation(ctx, mutation, err)
}

func (s *SyncStore) ReadWorkspaceAppearance(ctx context.Context, workspaceID, name, filename string) ([]byte, error) {
	if s == nil || s.fileSync == nil {
		return nil, workspacecontinuity.ErrIncomplete
	}
	return s.fileSync.ReadWorkspaceAppearance(ctx, workspaceID, name, filename)
}

func (s *AgentSnapshotStore) ReadWorkspaceAppearance(ctx context.Context, workspaceID, name, filename string) ([]byte, error) {
	reader, ok := s.Store.(WorkspaceAppearanceReader)
	if !ok {
		return nil, workspacecontinuity.ErrIncomplete
	}
	return reader.ReadWorkspaceAppearance(ctx, workspaceID, name, filename)
}
