package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/systemassistant"
)

var ErrStaffingNameTaken = errors.New("the selected agent name is no longer available; review the team again")

// StaffingProfileCreator is strict create, unlike the legacy idempotent
// CreateAgent API. Ownership and the first source witness are captured under
// the same mutation fence. A nonempty result.Name means this call created a
// definition, even if a later persistence step failed; an empty witness forbids
// destructive rollback until the uncertain write can be reconciled.
type StaffingProfileCreator interface {
	CreateStaffingProfile(context.Context, string, *CreateAgentConfig) (StaffingSelection, error)
}

func staffingCreateNameAvailable(snapshot StaffingSnapshot, name string) error {
	if name == "" || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) > 120 ||
		name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00\r\n") || systemassistant.IsKnownName(name) {
		return ErrStaffingNameTaken
	}
	for _, existing := range snapshot.sourceNames {
		if strings.EqualFold(existing, name) {
			return ErrStaffingNameTaken
		}
	}
	return nil
}

func (s *fileStore) createStaffingProfileLocked(ctx context.Context, snapshot StaffingSnapshot, name string, config *CreateAgentConfig) (StaffingSelection, error) {
	if err := ctx.Err(); err != nil {
		return StaffingSelection{}, err
	}
	if err := staffingCreateNameAvailable(snapshot, name); err != nil {
		return StaffingSelection{}, err
	}
	// Never acquire ownership of a pre-existing folder (even without a valid
	// definition): it may contain the user's skills or other authored files.
	if _, err := os.Lstat(filepath.Join(s.agentsDir(), name)); !os.IsNotExist(err) {
		return StaffingSelection{}, ErrStaffingNameTaken
	}
	err := s.createAgentUnlocked(name, config, true)
	if s.agents[name] == nil {
		return StaffingSelection{}, err
	}
	witness, _ := s.staffingWitnessLocked(name)
	if witness == "" && err == nil {
		err = ErrStaffingSourceChanged
	}
	return StaffingSelection{Name: name, Witness: witness}, err
}

func (s *fileStore) CreateStaffingProfile(ctx context.Context, name string, config *CreateAgentConfig) (selected StaffingSelection, err error) {
	err = s.withStaffingSources(ctx, func(sources staffingLockedSources) error {
		var createErr error
		selected, createErr = s.createStaffingProfileLocked(ctx, sources.snapshot, name, config)
		return createErr
	})
	return
}

func (c *CompositeStore) CreateStaffingProfile(ctx context.Context, name string, config *CreateAgentConfig) (selected StaffingSelection, err error) {
	if _, err = c.rootForWrite(); err != nil {
		return
	}
	err = c.withStaffingSources(ctx, func(sources staffingLockedSources) error {
		if c.root == nil || inspectAgentRoot(c.rootPath) != "" {
			return ErrAgentRootUnavailable
		}
		var createErr error
		selected, createErr = c.root.createStaffingProfileLocked(ctx, sources.snapshot, name, config)
		if selected.Witness != "" {
			selected.Witness = compositeStaffingWitness(c.staffingRootRevision, selected.Witness)
		}
		return createErr
	})
	if selected.Name != "" {
		c.InvalidateWorkspaceAgents()
	}
	return
}
