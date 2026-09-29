package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// finishFileMutation closes the durable pending barrier after publication AND
// cleanup. Failure stays visible even if a later SQL trigger dirties the owner.
// Cancellation cannot skip this bounded bookkeeping; the caller retains its
// reset permit until this returns. No late completion recreates reset state.
func (s *LocalConfigStore) finishFileMutation(ctx context.Context, mutation *workspacecontinuity.FileMutation, cleanupErr error) error {
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := mutation.Complete(finish, cleanupErr == nil); err != nil {
		return errors.Join(cleanupErr, ErrLocalConfigUnavailable, err)
	}
	return cleanupErr
}

func localAgentPath(name string) (string, string, error) {
	dir, err := workspaceAgentDir("", name)
	if err != nil {
		return "", "", ErrLocalConfigInvalid
	}
	return filepath.ToSlash(filepath.Join(dir, WorkspaceAgentConfigFile)), filepath.Base(dir), nil
}

func (s *LocalConfigStore) native(ctx context.Context, workspaceID string) error {
	if _, err := s.authority(ctx, workspaceID); err != nil {
		return err
	}
	a, err := workspacecontinuity.NewLocalStore(s.db).Attachment(ctx, workspaceID)
	if err != nil || a.State != workspacecontinuity.Native {
		return ErrLocalConfigUnavailable
	}
	return nil
}

func checkAgentFolder(ctx context.Context, directory, workspaceID string) error {
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, directory, "workspace.json", maxNativeWorkspaceBytes)
	if err != nil {
		return err
	}
	_, err = decodeLocalWorkspace(data, workspaceID)
	return err
}

func decodeLocalAgent(data []byte) (*agent.Agent, error) {
	var ag agent.Agent
	if err := workspacecontinuity.DecodeDocument(data, &ag, workspacecontinuity.MaxRecordBytes); err != nil {
		return nil, err
	}
	return &ag, nil
}

func decodeLocalWorkspace(data []byte, workspaceID string) (*Workspace, error) {
	var ws Workspace
	if len(data) > maxNativeWorkspaceBytes {
		return nil, workspacecontinuity.ErrLimit
	}
	// These readers serve already-admitted canonical state. Import and source
	// preparation use the separate strict, bounded document decoder.
	if !json.Valid(data) {
		return nil, ErrLocalConfigInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&ws); err != nil {
		return nil, ErrLocalConfigInvalid
	}
	if ws.ID != workspaceID || ws.OwnerUserID != "local" {
		return nil, ErrLocalConfigInvalid
	}
	return &ws, nil
}

// ReadAgentFile is the admitted native/runtime reader. It never installs
// portable credentials: legacy inline settings are usable only for a Native
// attachment. Copied modern references resolve only under this attachment's
// installation-local authority; an absent row leaves a readable, denied agent.
func (s *LocalConfigStore) ReadAgentFile(ctx context.Context, directory, workspaceID, name string) (*agent.Agent, error) {
	release, err := s.enterWork()
	if err != nil {
		return nil, err
	}
	defer release()
	path, item, err := localAgentPath(name)
	if err != nil {
		return nil, err
	}
	if _, err := s.authority(ctx, workspaceID); err != nil {
		return nil, err
	}
	unlock := s.locks.Lock(workspaceID + ":agent:" + item)
	defer unlock()
	if err := checkAgentFolder(ctx, directory, workspaceID); err != nil {
		return nil, err
	}
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, directory, path, workspacecontinuity.MaxRecordBytes)
	if err != nil {
		return nil, err
	}
	ag, err := decodeLocalAgent(data)
	if err != nil {
		return nil, err
	}
	if ag.WorkspaceLocalConfigID == "" {
		if s.native(ctx, workspaceID) != nil {
			clearAgentLocalConfig(ag)
		}
		return ag, nil
	}
	clearAgentLocalConfig(ag)
	private, found, err := s.Load(ctx, workspaceID, "agent", item, ag.WorkspaceLocalConfigID)
	if err != nil {
		return nil, err
	}
	if found {
		if err := applyAgentLocalConfig(ag, private); err != nil {
			return nil, err
		}
	}
	return ag, nil
}

// WriteAgentFile is for an explicit local edit, not import. It preserves the
// native per-agent key and grants in local encrypted state, publishes only their
// denied projection, then prunes superseded slots. A stale local reference is a
// conflict rather than permission to restore a revoked key/grant from an old UI.
func (s *LocalConfigStore) WriteAgentFile(ctx context.Context, directory, workspaceID, name string, input *agent.Agent) error {
	release, err := s.enterWork()
	if err != nil {
		return err
	}
	defer release()
	path, item, err := localAgentPath(name)
	if err != nil {
		return err
	}
	if _, err := s.authority(ctx, workspaceID); err != nil {
		return err
	}
	unlock := s.locks.Lock(workspaceID + ":agent:" + item)
	defer unlock()
	if err := checkAgentFolder(ctx, directory, workspaceID); err != nil {
		return err
	}
	current, err := workspacecontinuity.ReadCanonicalFile(ctx, directory, path, workspacecontinuity.MaxRecordBytes)
	if err != nil && !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		return err
	}
	expected := ""
	if err == nil {
		previous, err := decodeLocalAgent(current)
		if err != nil {
			return err
		}
		if input == nil || previous.WorkspaceLocalConfigID != input.WorkspaceLocalConfigID {
			return workspacecontinuity.ErrChanged
		}
		expected = workspacecontinuity.Digest(current)
	}
	return s.writeAgentFile(ctx, directory, workspaceID, path, item, expected, input)
}

func (s *LocalConfigStore) writeAgentFile(ctx context.Context, directory, workspaceID, path, item, expected string, input *agent.Agent) error {
	portable, private, err := splitAgentLocalConfig(input)
	if err != nil {
		return err
	}
	if err := s.pruneUnpublished(ctx, workspaceID, "agent", item, input.WorkspaceLocalConfigID); err != nil {
		return err
	}
	slot, err := s.Stage(ctx, workspaceID, "agent", item, private)
	if err != nil {
		return err
	}
	portable.WorkspaceLocalConfigID = slot
	data, err := json.MarshalIndent(portable, "", "  ")
	if err != nil {
		return ErrLocalConfigInvalid
	}
	mutation, err := workspacecontinuity.NewLocalStore(s.db).BeginFileMutation(ctx, workspaceID, path)
	if err != nil {
		return err
	}
	if err := workspacecontinuity.ReplaceCanonicalFile(ctx, directory, path, expected, data); err != nil {
		return s.finishFileMutation(ctx, mutation, err)
	}
	return s.finishFileMutation(ctx, mutation, s.Prune(ctx, workspaceID, "agent", item, slot))
}

// MigrateAgentFile separates an existing NATIVE file before preparation. It is
// never called by inspection/import/discovery. Failure leaves the legacy file
// intact; an already separated file must still have its exact local slot.
func (s *LocalConfigStore) MigrateAgentFile(ctx context.Context, directory, workspaceID, name string) error {
	release, err := s.enterWork()
	if err != nil {
		return err
	}
	defer release()
	if err := s.native(ctx, workspaceID); err != nil {
		return err
	}
	path, item, err := localAgentPath(name)
	if err != nil {
		return err
	}
	unlock := s.locks.Lock(workspaceID + ":agent:" + item)
	defer unlock()
	if err := checkAgentFolder(ctx, directory, workspaceID); err != nil {
		return err
	}
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, directory, path, workspacecontinuity.MaxRecordBytes)
	if err != nil {
		return err
	}
	ag, err := decodeLocalAgent(data)
	if err != nil {
		return err
	}
	if ag.WorkspaceLocalConfigID != "" {
		private, found, err := s.Load(ctx, workspaceID, "agent", item, ag.WorkspaceLocalConfigID)
		if err != nil || !found {
			return ErrLocalConfigUnavailable
		}
		var native agent.Agent
		if err := applyAgentLocalConfig(&native, private); err != nil {
			return err
		}
		if ag.Settings.APIKey != "" || ag.Settings.IsWebSearchAllowed() || ag.Settings.IsNativeMCPToolsAllowed() ||
			(ag.Settings.FallbackAllowCloud != nil && *ag.Settings.FallbackAllowCloud) {
			return ErrLocalConfigInvalid
		}
		return s.Prune(ctx, workspaceID, "agent", item, ag.WorkspaceLocalConfigID)
	}
	return s.writeAgentFile(ctx, directory, workspaceID, path, item, workspacecontinuity.Digest(data), ag)
}

// ReadBindingsFile restores only this attachment's private connector settings.
// Canonical tasks, messages, IDs and source enable intent are never migrated by
// this reader. A caller can rebuild in-memory indexes separately, without events.
func (s *LocalConfigStore) ReadBindingsFile(ctx context.Context, directory, workspaceID string) (*Workspace, error) {
	release, err := s.enterWork()
	if err != nil {
		return nil, err
	}
	defer release()
	if _, err := s.authority(ctx, workspaceID); err != nil {
		return nil, err
	}
	unlock := s.locks.Lock(workspaceID + ":bindings")
	defer unlock()
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, directory, "workspace.json", maxNativeWorkspaceBytes)
	if err != nil {
		return nil, err
	}
	ws, err := decodeLocalWorkspace(data, workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.WorkspaceLocalConfigID == "" && s.native(ctx, workspaceID) == nil {
		return ws, nil
	}
	portable, _, err := splitWorkspaceLocalConfig(ws)
	if err != nil {
		return nil, err
	}
	portable.WorkspaceLocalConfigID = ws.WorkspaceLocalConfigID
	if ws.WorkspaceLocalConfigID != "" {
		private, found, err := s.Load(ctx, workspaceID, "bindings", "workspace", ws.WorkspaceLocalConfigID)
		if err != nil {
			return nil, err
		}
		if found {
			if err := applyBindingsLocalConfig(portable, private); err != nil {
				return nil, err
			}
		}
	}
	return portable, nil
}

// WriteBindingsFile publishes a credential-free projection of a locally edited
// canonical workspace. The SQL primary retains its existing domain fields;
// this file adapter must not be passed an unreviewed imported workspace.
func (s *LocalConfigStore) WriteBindingsFile(ctx context.Context, directory string, input *Workspace, expected string) error {
	release, err := s.enterWork()
	if err != nil {
		return err
	}
	defer release()
	if input == nil {
		return ErrLocalConfigInvalid
	}
	if _, err := s.authority(ctx, input.ID); err != nil {
		return err
	}
	unlock := s.locks.Lock(input.ID + ":bindings")
	defer unlock()
	return s.writeBindingsFile(ctx, directory, input, expected)
}

func (s *LocalConfigStore) writeBindingsFile(ctx context.Context, directory string, input *Workspace, expected string) error {
	portable, private, err := splitWorkspaceLocalConfig(input)
	if err != nil {
		return err
	}
	currentSlot := ""
	current, err := workspacecontinuity.ReadCanonicalFile(ctx, directory, "workspace.json", maxNativeWorkspaceBytes)
	if errors.Is(err, workspacecontinuity.ErrIncomplete) && expected == "" {
		// An explicitly created file has no published private reference yet.
	} else if err != nil {
		return err
	} else {
		if workspacecontinuity.Digest(current) != expected {
			return workspacecontinuity.ErrChanged
		}
		previous, err := decodeLocalWorkspace(current, input.ID)
		if err != nil {
			return err
		}
		currentSlot = previous.WorkspaceLocalConfigID
	}
	if err := s.pruneUnpublished(ctx, input.ID, "bindings", "workspace", currentSlot); err != nil {
		return err
	}
	slot, err := s.Stage(ctx, input.ID, "bindings", "workspace", private)
	if err != nil {
		return err
	}
	portable.WorkspaceLocalConfigID = slot
	data, err := portable.ToJSON()
	if err != nil {
		return ErrLocalConfigInvalid
	}
	mutation, err := workspacecontinuity.NewLocalStore(s.db).BeginFileMutation(ctx, input.ID, WorkspaceConfigFile)
	if err != nil {
		return err
	}
	if err := workspacecontinuity.ReplaceCanonicalFile(ctx, directory, "workspace.json", expected, data); err != nil {
		return s.finishFileMutation(ctx, mutation, err)
	}
	return s.finishFileMutation(ctx, mutation, s.Prune(ctx, input.ID, "bindings", "workspace", slot))
}

func (s *LocalConfigStore) MigrateBindingsFile(ctx context.Context, directory, workspaceID string) error {
	release, err := s.enterWork()
	if err != nil {
		return err
	}
	defer release()
	if err := s.native(ctx, workspaceID); err != nil {
		return err
	}
	unlock := s.locks.Lock(workspaceID + ":bindings")
	defer unlock()
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, directory, "workspace.json", workspacecontinuity.MaxChunkBytes)
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
	if ws.WorkspaceLocalConfigID != "" {
		private, found, err := s.Load(ctx, workspaceID, "bindings", "workspace", ws.WorkspaceLocalConfigID)
		if err != nil || !found {
			return ErrLocalConfigUnavailable
		}
		native, _, err := splitWorkspaceLocalConfig(ws)
		if err != nil {
			return err
		}
		if err := applyBindingsLocalConfig(native, private); err != nil {
			return err
		}
		if ws.AllowNativeMCPCLI || ws.RuntimeState != nil && (len(ws.RuntimeState.Grants) > 0 || len(ws.RuntimeState.RequirementStates) > 0) {
			return ErrLocalConfigInvalid
		}
		for _, binding := range ws.MCPBindings {
			if len(binding.Config) > 0 || len(binding.Scope) > 0 || binding.AllowedTools == nil || len(binding.AllowedTools) > 0 ||
				binding.DefaultSideEffect != "" || len(binding.ToolOverrides) > 0 {
				return ErrLocalConfigInvalid
			}
		}
		for _, binding := range ws.SkillBindings {
			if len(binding.Config) > 0 || binding.Trusted || binding.DefaultSideEffect != "" || len(binding.ToolOverrides) > 0 {
				return ErrLocalConfigInvalid
			}
		}
		return s.Prune(ctx, workspaceID, "bindings", "workspace", ws.WorkspaceLocalConfigID)
	}
	return s.writeBindingsFile(ctx, directory, ws, workspacecontinuity.Digest(data))
}
