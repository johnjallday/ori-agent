package store

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/agent"
)

// ErrStaffingSourceChanged requires a fresh review, never a name-based fallback.
var ErrStaffingSourceChanged = errors.New("the reviewed agent source changed; review the team again")

// StaffingSelection is server-held review material, not a client-supplied grant.
type StaffingSelection struct {
	Name    string
	Witness string `json:"-"`
}

// StaffingSourceGuard closes the source-check/bind race against store mutations
// and root switches. Callbacks must be short, must not call the agent store (or
// acquire its locks indirectly), and must never perform inference. They receive
// detached definitions, not runtime state. The existing operation/review owner
// remains responsible for target authorization, revisions and replay.
//
// Filesystem edits outside Ori are detected at the fence; this is not an OS-wide
// lock on an editor that ignores Ori's mutation locks.
type StaffingSourceGuard interface {
	WithStaffingProfiles(context.Context, []StaffingSelection, func(map[string]*agent.Agent) error) error
	// afterDelete runs under the same fence, so session cleanup cannot purge a
	// replacement namesake's new sessions. It must not call the agent store.
	DeleteStaffingProfile(context.Context, StaffingSelection, func(*agent.Agent) bool, func()) (bool, error)
}

func WithStaffingProfiles(ctx context.Context, st Store, selected []StaffingSelection, apply func(map[string]*agent.Agent) error) error {
	guard, ok := st.(StaffingSourceGuard)
	if !ok || apply == nil || len(selected) == 0 {
		return ErrStaffingSourceChanged
	}
	return guard.WithStaffingProfiles(ctx, selected, apply)
}

type staffingLockedSources struct {
	snapshot StaffingSnapshot
	owners   map[string]*fileStore
}

func (s *fileStore) withStaffingSources(ctx context.Context, use func(staffingLockedSources) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot := s.staffingSnapshotLocked(ctx)
	owners := make(map[string]*fileStore, len(snapshot.Profiles))
	for _, p := range snapshot.Profiles {
		owners[p.Name] = s
	}
	return use(staffingLockedSources{snapshot: snapshot, owners: owners})
}

func (c *CompositeStore) withStaffingSources(ctx context.Context, use func(staffingLockedSources) error) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	system, ok := c.system.(*fileStore)
	if !ok {
		return ErrStaffingSourceChanged
	}
	// Same order as inspection: root selection, legacy store, root store.
	system.mu.Lock()
	defer system.mu.Unlock()
	if c.root != nil && c.root != system {
		c.root.mu.Lock()
		defer c.root.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot := c.readStaffingSnapshotLocked(ctx, func(st Store) StaffingSnapshot {
		return st.(*fileStore).staffingSnapshotLocked(ctx)
	})
	owners := make(map[string]*fileStore, len(snapshot.Profiles))
	for _, p := range snapshot.Profiles {
		owners[p.Name] = system
		if p.Source == "root" {
			owners[p.Name] = c.root
		}
	}
	return use(staffingLockedSources{snapshot: snapshot, owners: owners})
}

func (sources staffingLockedSources) resolve(ctx context.Context, selected []StaffingSelection) (map[string]*agent.Agent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(selected) == 0 || len(selected) > 100 {
		return nil, ErrStaffingSourceChanged
	}
	profiles := make(map[string]*agent.Agent, len(selected))
	for _, choice := range selected {
		if choice.Name == "" || choice.Witness == "" || profiles[choice.Name] != nil {
			return nil, ErrStaffingSourceChanged
		}
		for _, p := range sources.snapshot.Profiles {
			if p.Name == choice.Name && p.Witness == choice.Witness {
				// Build a definition-only value. cloneAgentDefinition alone retains
				// conversation/runtime pointers, which are not binding authority.
				definition := &agent.Agent{}
				live := sources.owners[p.Name].agents[p.Name]
				adoptAgentDefinition(definition, cloneAgentDefinition(live))
				definition.DefaultToolbox = live.DefaultToolbox.Clone()
				profiles[p.Name] = definition
				break
			}
		}
		if profiles[choice.Name] == nil {
			return nil, ErrStaffingSourceChanged
		}
	}
	return profiles, nil
}

func withStaffingProfiles(ctx context.Context, sources staffingLockedSources, selected []StaffingSelection, apply func(map[string]*agent.Agent) error) error {
	profiles, err := sources.resolve(ctx, selected)
	if err != nil {
		return err
	}
	if apply == nil {
		return ErrStaffingSourceChanged
	}
	return apply(profiles)
}

func (s *fileStore) WithStaffingProfiles(ctx context.Context, selected []StaffingSelection, apply func(map[string]*agent.Agent) error) error {
	return s.withStaffingSources(ctx, func(sources staffingLockedSources) error { return withStaffingProfiles(ctx, sources, selected, apply) })
}

func (c *CompositeStore) WithStaffingProfiles(ctx context.Context, selected []StaffingSelection, apply func(map[string]*agent.Agent) error) error {
	return c.withStaffingSources(ctx, func(sources staffingLockedSources) error { return withStaffingProfiles(ctx, sources, selected, apply) })
}

func deleteStaffingProfile(ctx context.Context, sources staffingLockedSources, selected StaffingSelection, allow func(*agent.Agent) bool, afterDelete func()) (bool, error) {
	profiles, err := sources.resolve(ctx, []StaffingSelection{selected})
	if err != nil {
		return false, err
	}
	if allow == nil || !allow(profiles[selected.Name]) {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Do not release the definition fence between the reference/ownership
	// predicate and deletion. Review-bearing binding uses the same fence.
	if err := sources.owners[selected.Name].deleteAgentUnlocked(selected.Name); err != nil {
		return false, err
	}
	if afterDelete != nil {
		afterDelete()
	}
	return true, nil
}

func (s *fileStore) DeleteStaffingProfile(ctx context.Context, selected StaffingSelection, allow func(*agent.Agent) bool, afterDelete func()) (deleted bool, err error) {
	err = s.withStaffingSources(ctx, func(sources staffingLockedSources) error {
		var deleteErr error
		deleted, deleteErr = deleteStaffingProfile(ctx, sources, selected, allow, afterDelete)
		return deleteErr
	})
	return
}

func (c *CompositeStore) DeleteStaffingProfile(ctx context.Context, selected StaffingSelection, allow func(*agent.Agent) bool, afterDelete func()) (deleted bool, err error) {
	err = c.withStaffingSources(ctx, func(sources staffingLockedSources) error {
		var deleteErr error
		deleted, deleteErr = deleteStaffingProfile(ctx, sources, selected, allow, afterDelete)
		return deleteErr
	})
	return
}
