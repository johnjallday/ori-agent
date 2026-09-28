package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

var ErrReviewedWorkspaceImportRequired = errors.New("workspace continuity requires reviewed import")

// NewFileStoreWithLocalConfig composes canonical file persistence with the local
// credential owner. It does not register/admit any workspace. The application
// must inventory already-authorized SQL workspaces before constructing it, and
// explicit creation/import must establish the proper attachment separately.
func NewFileStoreWithLocalConfig(basePath string, local *LocalConfigStore) (*FileStore, error) {
	if local == nil {
		return nil, ErrLocalConfigUnavailable
	}
	release, err := local.enterWork()
	if err != nil {
		return nil, err
	}
	defer release()
	return newFileStore(basePath, local, nil)
}

func (s *FileStore) hasLocalConfig() bool { return s != nil && s.localConfig != nil }

// Hold the installation gate around whole persistence operations, not just the
// final JSON rename: index writes, rollback and owned callbacks must drain too.
func (s *FileStore) enterContinuityWork() (func(), error) {
	if !s.hasLocalConfig() {
		return func() {}, nil
	}
	return s.localConfig.enterWork()
}

func decodeLocalReference(data []byte, reference *string) error {
	var fields map[string]json.RawMessage
	if err := workspacecontinuity.DecodeDocument(data, &fields, workspacecontinuity.MaxChunkBytes); err != nil {
		return err
	}
	for key, value := range fields {
		if !strings.EqualFold(key, "ori_local_config_id") {
			continue
		}
		if key != "ori_local_config_id" {
			return ErrLocalConfigInvalid
		}
		if err := workspacecontinuity.DecodeDocument(value, reference, workspacecontinuity.MaxChunkBytes); err != nil {
			return ErrLocalConfigInvalid
		}
	}
	return nil
}

// liveWorkspaceReadAttempts bounds readLiveWorkspaceFile's retries.
const liveWorkspaceReadAttempts = 5

// readLiveWorkspaceFile runs read again while it reports ErrChanged. Every save
// replaces workspace.json atomically (write, then rename), so a read that
// overlaps one sees the path move to a new file. The replacement is complete,
// so read it rather than failing the caller — os.ReadFile, which the strict
// readers replaced, never failed here, and callers such as ListActive and boot
// task reconciliation silently skip a workspace whose read fails.
func readLiveWorkspaceFile(read func() ([]byte, error)) ([]byte, error) {
	var err error
	for range liveWorkspaceReadAttempts {
		var data []byte
		data, err = read()
		if !errors.Is(err, workspacecontinuity.ErrChanged) {
			return data, err
		}
	}
	return nil, err
}

// readNativeWorkspaceFile keeps the legacy live-document size semantics while
// confining the fixed filename and refusing symlinks. Import/preparation readers
// remain separately bounded; canonical local history need not fit a checkpoint.
func readNativeWorkspaceFile(folder string) ([]byte, error) {
	return readLiveWorkspaceFile(func() ([]byte, error) { return readNativeWorkspaceFileOnce(folder) })
}

func readNativeWorkspaceFileOnce(folder string) ([]byte, error) {
	root, err := os.OpenRoot(folder)
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, workspacecontinuity.ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	before, err := root.Lstat(WorkspaceConfigFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil || !before.Mode().IsRegular() {
		return nil, workspacecontinuity.ErrUnsafe
	}
	file, err := workspacecontinuity.OpenCanonicalFile(folder, WorkspaceConfigFile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, workspacecontinuity.ErrChanged
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, workspacecontinuity.ErrIncomplete
	}
	after, statErr := file.Stat()
	current, pathErr := root.Lstat(WorkspaceConfigFile)
	if statErr != nil || pathErr != nil || !os.SameFile(before, current) || !current.Mode().IsRegular() ||
		after.Size() != int64(len(data)) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return nil, workspacecontinuity.ErrChanged
	}
	return data, nil
}

// legacyWorkspaceReference reads only the ownership marker from canonical
// legacy files, streaming past history rather than imposing checkpoint size
// limits on live saves. This is NOT the bounded untrusted-import decoder.
func legacyWorkspaceReference(folder string) (string, error) {
	root, err := os.OpenRoot(folder)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	before, err := root.Lstat(WorkspaceConfigFile)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || !before.Mode().IsRegular() {
		return "", workspacecontinuity.ErrUnsafe
	}
	file, err := workspacecontinuity.OpenCanonicalFile(folder, WorkspaceConfigFile)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return "", workspacecontinuity.ErrChanged
	}
	decoder := json.NewDecoder(file)
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", ErrLocalConfigInvalid
	}
	reference, seen := "", false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "", ErrLocalConfigInvalid
		}
		name, ok := key.(string)
		if !ok {
			return "", ErrLocalConfigInvalid
		}
		value, err := decoder.Token()
		if err != nil {
			return "", ErrLocalConfigInvalid
		}
		if strings.EqualFold(name, "ori_local_config_id") {
			if seen || name != "ori_local_config_id" {
				return "", ErrLocalConfigInvalid
			}
			reference, ok = value.(string)
			if !ok {
				return "", ErrLocalConfigInvalid
			}
			seen = true
		}
		depth := 0
		if value == json.Delim('{') || value == json.Delim('[') {
			depth = 1
		}
		for depth > 0 {
			nested, err := decoder.Token()
			if err != nil {
				return "", ErrLocalConfigInvalid
			}
			switch nested {
			case json.Delim('{'), json.Delim('['):
				depth++
			case json.Delim('}'), json.Delim(']'):
				depth--
			}
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return "", ErrLocalConfigInvalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", ErrLocalConfigInvalid
	}
	after, err := file.Stat()
	current, pathErr := root.Lstat(WorkspaceConfigFile)
	if err != nil || pathErr != nil || !os.SameFile(before, current) || !current.Mode().IsRegular() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", workspacecontinuity.ErrChanged
	}
	return reference, nil
}

// cachePrivateConfig hydrates only already-admitted local configuration, never
// a copied grant. It does not load heavy history or authorize discovery. When
// the local backend is unavailable the cache remains a denied projection; full
// reads/writes still return the dependency error rather than silently clearing it.
func (s *FileStore) cachePrivateConfig(ws *Workspace) (*Workspace, error) {
	if s.localConfig == nil {
		return ws, nil
	}
	authority, err := s.localConfig.authority(context.Background(), ws.ID)
	if err == nil && authority == "native" && ws.WorkspaceLocalConfigID == "" {
		return ws, nil
	}
	portable, _, splitErr := splitWorkspaceLocalConfig(ws)
	if splitErr != nil {
		return nil, splitErr
	}
	portable.WorkspaceLocalConfigID = ws.WorkspaceLocalConfigID
	if err != nil || ws.WorkspaceLocalConfigID == "" {
		return portable, nil
	}
	data, found, err := s.localConfig.Load(context.Background(), ws.ID, "bindings", "workspace", ws.WorkspaceLocalConfigID)
	if err == nil && found {
		if err := applyBindingsLocalConfig(portable, data); err != nil {
			return nil, err
		}
	}
	return portable, nil
}

// workspaceForRead keeps read-time normalization separate from the on-disk
// projection. Only the admitted local owner can hydrate its private settings.
func (s *FileStore) workspaceForRead(folder string, data []byte, workspaceID string) (*Workspace, error) {
	ws, err := FromJSON(data)
	if err != nil {
		return nil, err
	}
	if ws.ID != workspaceID {
		return nil, ErrLocalConfigInvalid
	}
	if s.localConfig == nil {
		if ws.WorkspaceLocalConfigID != "" {
			return nil, ErrLocalConfigUnavailable
		}
		return ws, nil
	}
	if ws.WorkspaceLocalConfigID == "" && s.localConfig.native(context.Background(), ws.ID) == nil {
		return ws, nil // Native legacy work stays usable until safe preparation.
	}
	native, err := s.localConfig.ReadBindingsFile(context.Background(), folder, ws.ID)
	if err != nil {
		return nil, err
	}
	initializeDecodedWorkspace(native)
	return native, nil
}

// writeWorkspaceConfigLocked is the sole FileStore workspace.json writer. The
// returned bytes are the native in-memory representation for metadata caching,
// NOT bytes to write to disk. A separated file cannot be overwritten by an
// unconfigured legacy store, even when a partial SQL projection lost its marker.
// Call with s.mu held; private file locks additionally serialize local owners.
func (s *FileStore) writeWorkspaceConfigLocked(ws *Workspace, configPath string) ([]byte, error) {
	if s.hasLocalConfig() {
		release, err := s.localConfig.enterWork()
		if err != nil {
			return nil, err
		}
		defer release()
	}
	folder := filepath.Dir(configPath)
	reference, err := legacyWorkspaceReference(folder)
	if err != nil {
		return nil, err
	}
	if reference == "" && ws.WorkspaceLocalConfigID == "" && s.hasLocalConfig() {
		attachment, err := workspacecontinuity.NewLocalStore(s.localConfig.db).Attachment(context.Background(), ws.ID)
		if err != nil {
			return nil, err
		}
		if attachment.State == workspacecontinuity.Native && attachment.Provisioning {
			// A newly authorized native creation starts separated; unlike an
			// upgrade, there is no legacy plaintext file to preserve. Never
			// publish new connector grants before their private stage succeeds.
			if err := s.localConfig.WriteBindingsFile(context.Background(), folder, ws, ""); err != nil {
				return nil, err
			}
			fresh, err := s.localConfig.ReadBindingsFile(context.Background(), folder, ws.ID)
			if err != nil {
				return nil, err
			}
			return fresh.ToJSON()
		}
	}
	if reference == "" && ws.WorkspaceLocalConfigID == "" {
		// Preparation is a separate operation. Until it succeeds, a native
		// legacy file keeps its existing settings and size semantics, even if
		// the secure backend/portable format is unavailable. Never claim Ready.
		if s.localConfig != nil {
			if err := s.localConfig.native(context.Background(), ws.ID); err != nil {
				return nil, err
			}
		}
		data, err := ws.ToJSON()
		if err != nil {
			return nil, err
		}
		if s.hasLocalConfig() {
			ctx := context.Background()
			mutation, err := workspacecontinuity.NewLocalStore(s.localConfig.db).BeginFileMutation(ctx, ws.ID, WorkspaceConfigFile)
			if err != nil {
				return nil, err
			}
			if err := s.localConfig.finishFileMutation(ctx, mutation, atomicWriteFile(configPath, data)); err != nil {
				return nil, err
			}
		} else if err := atomicWriteFile(configPath, data); err != nil {
			return nil, err
		}
		return data, nil
	}
	if s.localConfig == nil {
		return nil, ErrLocalConfigUnavailable
	}
	current, readErr := workspacecontinuity.ReadCanonicalFile(context.Background(), folder, WorkspaceConfigFile, maxNativeWorkspaceBytes)
	if readErr != nil {
		return nil, readErr
	}
	expected := workspacecontinuity.Digest(current)
	if err := s.localConfig.WriteBindingsFile(context.Background(), folder, ws, expected); err != nil {
		return nil, err
	}
	fresh, err := s.localConfig.ReadBindingsFile(context.Background(), folder, ws.ID)
	if err != nil {
		return nil, err
	}
	return fresh.ToJSON()
}

// PrepareNativeLocalConfig is a source-only pre-snapshot step. It serializes
// against canonical workspace/agent writes. The future worker must hold its
// reset WorkGate permit for the entire call and retain the returned errors in
// preparation status; this method does not publish a checkpoint or grant Ready.
func (s *FileStore) PrepareNativeLocalConfig(ctx context.Context, workspaceID string) error {
	if !s.hasLocalConfig() {
		return ErrLocalConfigUnavailable
	}
	release, err := s.localConfig.enterWork()
	if err != nil {
		return err
	}
	defer release()
	s.mu.Lock()
	defer s.mu.Unlock()
	path, exists := s.idToPath[workspaceID]
	if !exists {
		return ErrLocalConfigInvalid
	}
	folder := s.resolveFolder(path)
	if err := s.localConfig.native(ctx, workspaceID); err != nil {
		return err
	}
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		return err
	}
	var verified Workspace
	if err := workspacecontinuity.DecodeDocument(data, &verified, workspacecontinuity.MaxChunkBytes); err != nil {
		return err
	}
	ws, err := decodeLocalWorkspace(data, workspaceID)
	if err != nil {
		return err
	}
	// Include leftover canonical profiles too: an unused agent config can still
	// contain a key in the physical copy. Never substitute a global profile.
	expected := map[string]string{}
	for _, instance := range ws.AgentInstances {
		_, item, err := localAgentPath(instance.Name)
		if err != nil {
			return err
		}
		if prior, exists := expected[item]; exists && prior != instance.Name {
			return ErrLocalConfigInvalid
		}
		expected[item] = instance.Name
	}
	entries, err := workspacecontinuity.ListCanonicalDirectory(ctx, folder, WorkspaceAgentsDir, workspacecontinuity.MaxFiles)
	if errors.Is(err, workspacecontinuity.ErrIncomplete) && len(expected) == 0 {
		entries = nil
	} else if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return workspacecontinuity.ErrUnsafe
		}
		name := entry.Name()
		if declared, exists := expected[name]; exists {
			name = declared
			delete(expected, entry.Name())
		}
		_, item, err := localAgentPath(name)
		if err != nil || item != entry.Name() {
			return ErrLocalConfigInvalid
		}
		if err := s.localConfig.MigrateAgentFile(ctx, folder, workspaceID, name); err != nil {
			return err
		}
	}
	if len(expected) != 0 {
		return workspacecontinuity.ErrIncomplete
	}
	return s.localConfig.MigrateBindingsFile(ctx, folder, workspaceID)
}
