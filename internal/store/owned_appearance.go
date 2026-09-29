package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"path"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// OwnedAgentReader excludes workspace-only fallback from a source definition
// lookup. It is used only when explicitly making a new native workspace copy.
type OwnedAgentReader interface {
	GetOwnedAgent(string) (*agent.Agent, bool)
}

func (s *fileStore) GetOwnedAgent(name string) (*agent.Agent, bool) {
	return s.GetAgent(name)
}

func (c *CompositeStore) GetOwnedAgent(name string) (*agent.Agent, bool) {
	owner := c.owner(name)
	if owner == nil {
		return nil, false
	}
	return owner.GetAgent(name)
}

// OwnedAppearanceReader reads an image belonging to an exact GLOBAL definition.
// It never resolves a workspace-only name through the composite roster. The
// caller owns image validation and the destination's admission/write lifecycle.
type OwnedAppearanceReader interface {
	ReadOwnedAgentAppearance(context.Context, string, string) ([]byte, error)
}

func (s *fileStore) ReadOwnedAgentAppearance(ctx context.Context, name, filename string) ([]byte, error) {
	if !workspacecontinuity.ValidID(name) || !agent.IsAppearanceUploadFilename(filename) {
		return nil, workspacecontinuity.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ag, ok := s.agents[name]
	if !ok || ag == nil || ag.WorkspaceLocalConfigID != "" || ag.Appearance == nil || ag.Appearance.UploadedImage() != filename {
		return nil, ErrWorkspaceOwnedAgent
	}
	definition, err := workspacecontinuity.ReadCanonicalFile(ctx, s.agentsDir(), path.Join(name, definitionFileName), workspacecontinuity.MaxRecordBytes)
	if err != nil {
		return nil, err
	}
	known, recorded := s.definitionHashes[name]
	if !recorded || sha256.Sum256(definition) != known {
		return nil, ErrAgentChangedOnDisk
	}
	// Start at the library root, not the agent's derived folder: the canonical
	// reader refuses symbolic links in every agent-derived path component.
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, s.agentsDir(), path.Join(name, filename), 5<<20)
	if !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		return data, err
	}
	// Older global definitions used the application's shared upload directory.
	// Only the exact owning definition above authorizes this legacy lookup.
	return workspacecontinuity.ReadCanonicalFile(ctx, config.DefaultAgentAvatarsDir(), filename, 5<<20)
}

func (c *CompositeStore) ReadOwnedAgentAppearance(ctx context.Context, name, filename string) ([]byte, error) {
	owner := c.owner(name)
	if owner == nil {
		return nil, ErrWorkspaceOwnedAgent
	}
	reader, ok := owner.(OwnedAppearanceReader)
	if !ok {
		return nil, workspacecontinuity.ErrIncomplete
	}
	return reader.ReadOwnedAgentAppearance(ctx, name, filename)
}
