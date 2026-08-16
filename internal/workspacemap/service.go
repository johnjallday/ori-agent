package workspacemap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Errors the service returns when a layout write points at something the
// current user may not place on their map (FR-99).
var (
	// ErrNodeNotFound means the ID resolves to no workspace record — it was
	// never created, or it was permanently deleted.
	ErrNodeNotFound = errors.New("workspace map node not found")
	// ErrNodeNotOwned means the record exists but belongs to another user.
	ErrNodeNotOwned = errors.New("workspace map node is owned by another user")
	// ErrServiceUnavailable means the map service has no workspace lookup or no
	// layout storage wired.
	ErrServiceUnavailable = errors.New("workspace map service is not configured")
	// ErrGroupNotEligible means the target exists and is the user's, but is not
	// something the Map draws a district for: an ordinary workspace, or a group
	// nested inside another group. V1 renders one district per *top-level* group
	// and represents deeper descendants inside it, so there is no second frame
	// for a nested group to own (#346 FR-9, FR-10, FR-181).
	ErrGroupNotEligible = errors.New("workspace map group is not eligible for a district")
)

// WorkspaceLookup resolves the workspace records a layout operation refers to.
//
// It is read-only by construction, and that is the point (FR-6). The Map
// service holds no way to save a workspace, so no map operation — a drop, a
// cluster translation, a reset, an undo — has any path to change a parent, an
// order index, a status, a designation, a timestamp, a file, or a folder. The
// only workspace question this package can ask is "does this record exist, and
// is it mine?".
type WorkspaceLookup interface {
	Get(id string) (*workspace.Workspace, error)
}

// LayoutStore is the persistence seam behind the service, narrow enough for
// tests to substitute.
type LayoutStore interface {
	Load(ctx context.Context, userID string) (Layout, error)
	Apply(ctx context.Context, userID string, patch Patch) (Result, error)
}

// Service applies ownership and lifecycle rules above layout storage.
//
// Its whole job is the gap between "this JSON parsed" and "this user is allowed
// to anchor that record": the store validates geometry, and the service
// validates who and what.
type Service struct {
	store  LayoutStore
	lookup WorkspaceLookup
}

// NewService builds the map service over layout storage and a read-only
// workspace lookup.
func NewService(store LayoutStore, lookup WorkspaceLookup) *Service {
	return &Service{store: store, lookup: lookup}
}

// Load returns the current user's layout.
//
// Anchors for trashed workspaces are returned rather than filtered: the map
// hides a trashed building because it is absent from the workspace list, not
// because its coordinate was thrown away, and that retained coordinate is what
// a restore puts the building back on (FR-26, FR-27).
func (s *Service) Load(ctx context.Context, userID string) (Layout, error) {
	if s == nil || s.store == nil {
		return Layout{}, ErrServiceUnavailable
	}
	return s.store.Load(ctx, userID)
}

// Apply validates every record a patch refers to, then commits it.
//
// Unknown, permanently deleted, reserved, and not-owned IDs are refused before
// storage is touched, so a rejected reference never consumes a revision or
// leaves half a cluster moved (FR-99).
func (s *Service) Apply(ctx context.Context, userID string, patch Patch) (Result, error) {
	if s == nil || s.store == nil {
		return Result{}, ErrServiceUnavailable
	}
	normalized, err := NormalizePatch(patch)
	if err != nil {
		return Result{}, err
	}
	if err := s.authorizeNodes(userID, normalized); err != nil {
		return Result{}, err
	}
	return s.store.Apply(ctx, userID, normalized)
}

// Reset clears the user's custom anchors so deterministic fallback placement
// takes over. It refers to no workspace record and changes none: it is purely
// the removal of this user's own map coordinates (FR-109, FR-111).
func (s *Service) Reset(ctx context.Context, userID string) (Result, error) {
	if s == nil || s.store == nil {
		return Result{}, ErrServiceUnavailable
	}
	return s.store.Apply(ctx, userID, Patch{Operations: []Operation{Reset()}})
}

// authorizeNodes checks every node ID a patch names against the workspace
// store. Reset carries no references and passes trivially.
func (s *Service) authorizeNodes(userID string, patch Patch) error {
	checked := map[string]bool{}
	districts := map[string]bool{}
	for _, op := range patch.Operations {
		for id := range op.Positions {
			if err := s.authorizeNode(userID, id, checked); err != nil {
				return err
			}
		}
		switch {
		case op.Kind == OpTranslateGroup:
			if err := s.authorizeNode(userID, op.GroupID, checked); err != nil {
				return err
			}
		case op.Kind.isGroupPresentation():
			// A district operation carries more weight than an anchor: it must
			// point at something the Map actually draws a district for, or a
			// presentation row would accumulate for a workspace that can never
			// render one (FR-181).
			if err := s.authorizeDistrict(userID, op.GroupID, districts); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) authorizeNode(userID, id string, checked map[string]bool) error {
	if checked[id] {
		return nil
	}
	if _, err := s.lookupOwned(userID, id); err != nil {
		return err
	}
	checked[id] = true
	return nil
}

// authorizeDistrict adds the eligibility rule on top of ownership: the target
// must be a group, and it must be top-level. Neither check can change anything
// — the lookup is read-only by construction, so a rejected district operation
// leaves the workspace record byte-identical (FR-5, FR-6).
func (s *Service) authorizeDistrict(userID, id string, checked map[string]bool) error {
	if checked[id] {
		return nil
	}
	record, err := s.lookupOwned(userID, id)
	if err != nil {
		return err
	}
	if !isGroupRecord(record) {
		return fmt.Errorf("%w: %q is a workspace, not a group", ErrGroupNotEligible, record.ID)
	}
	if parentID := strings.TrimSpace(record.ParentID); parentID != "" {
		// A parent that is missing or is an ordinary workspace leaves this group
		// rendering as its own top-level district, which the Map already does —
		// only a group inside a group is ineligible.
		if parent, err := s.lookup.Get(parentID); err == nil && parent != nil && isGroupRecord(parent) {
			return fmt.Errorf("%w: %q is nested inside group %q", ErrGroupNotEligible, record.ID, parentID)
		}
	}
	checked[id] = true
	return nil
}

// lookupOwned resolves one node and confirms it is the current user's.
func (s *Service) lookupOwned(userID, id string) (*workspace.Workspace, error) {
	if s.lookup == nil {
		return nil, ErrServiceUnavailable
	}
	// NormalizeNodeID has already refused the reserved Personal HQ site, which
	// is the one node the map draws that is not a workspace (FR-30).
	nodeID, err := NormalizeNodeID(id)
	if err != nil {
		return nil, err
	}
	record, err := s.lookup.Get(nodeID)
	if err != nil || record == nil {
		return nil, fmt.Errorf("%w: %q", ErrNodeNotFound, nodeID)
	}
	if !ownedBy(record, userID) {
		return nil, fmt.Errorf("%w: %q", ErrNodeNotOwned, nodeID)
	}
	return record, nil
}

// groupWorkspaceKind mirrors session.WorkspaceKindGroup ("group"). It is
// duplicated rather than imported for the same reason the workspace package
// duplicates it: the map's ownership check must not drag the session package
// into this dependency graph for one string comparison.
const groupWorkspaceKind = "group"

// isGroupRecord reports whether a workspace record is a group, and therefore
// something the Map draws a district for.
func isGroupRecord(record *workspace.Workspace) bool {
	return record != nil && strings.TrimSpace(record.Kind) == groupWorkspaceKind
}

// ownedBy compares a workspace's owner against the current user. An empty owner
// is the local user, matching the column default written for every record that
// predates multi-user ownership.
func ownedBy(record *workspace.Workspace, userID string) bool {
	owner := record.OwnerUserID
	if owner == "" {
		owner = LocalUserID
	}
	return normalizeUserID(owner) == normalizeUserID(userID)
}
