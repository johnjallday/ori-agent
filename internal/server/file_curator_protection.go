package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type curatorRootReader interface {
	StoredManagedRoot(string, string) (filejanitor.ManagedRoot, error)
}

type curatorScopeReader interface {
	ProtectionScope(context.Context, string, string) (dailybrief.WorkspaceScopeResult, *workspace.Workspace, error)
}

type curatorCoverageReader interface {
	ReadFolderCoverage(context.Context, string, []string, []backuprecovery.AdoptedFolder) (backuprecovery.CoverageRead, error)
}

// fileCuratorProtection is the composition boundary. Only this server adapter
// passes the approved managed path to the stored coverage reader; the public
// fact and prompt never carry paths. Generic adopted-folder policy is untouched.
type fileCuratorProtection struct {
	roots    curatorRootReader
	scope    curatorScopeReader
	coverage curatorCoverageReader
	now      func() time.Time
}

func (r fileCuratorProtection) ReadProtection(ctx context.Context, owner, id string) (filecuratorguidance.ProtectionFact, error) {
	now := time.Now().UTC()
	if r.now != nil {
		now = r.now().UTC()
	}
	unavailable := filecuratorguidance.ProtectionFact{WorkspaceID: id, State: "unavailable", Reason: "managed_folder_unavailable", AsOf: now}
	if r.roots == nil || r.scope == nil {
		return unavailable, nil
	}
	scope, _, err := r.scope.ProtectionScope(ctx, owner, id)
	if err != nil {
		return unavailable, err
	}
	root, err := r.roots.StoredManagedRoot(owner, id)
	if err != nil {
		return unavailable, nil
	}
	fact := filecuratorguidance.ProtectionFact{
		WorkspaceID: id, RootGeneration: root.Generation,
		BindingID: fmt.Sprintf("%x", sha256.Sum256([]byte(root.Owner+"\x00"+id+"\x00"+root.Generation))),
		State:     "unknown", Reason: "evidence_unavailable", AsOf: now,
		Destination: &filecuratorguidance.ProtectionDestination{Kind: "chooser"},
	}
	ids := curatorScopeIDs(scope)
	if r.coverage == nil {
		return fact, nil
	}
	coverage, readErr := r.coverage.ReadFolderCoverage(ctx, owner, ids, []backuprecovery.AdoptedFolder{{WorkspaceID: id, Path: root.Path}})
	// Root identity, owner, permission and scope can change while stored reads
	// are in flight. No previously scoped evidence may survive that change.
	current, rootErr := r.roots.StoredManagedRoot(owner, id)
	freshScope, _, scopeErr := r.scope.ProtectionScope(ctx, owner, id)
	if scopeErr != nil {
		return unavailable, scopeErr
	}
	if rootErr != nil || current != root || !slices.Equal(ids, curatorScopeIDs(freshScope)) || scope.Complete != freshScope.Complete {
		return unavailable, nil
	}
	if readErr != nil {
		return fact, nil
	}
	// Choose only a current, owned, installed console from the same scope.
	destination := func(id string) *filecuratorguidance.ProtectionDestination {
		for _, candidate := range freshScope.Workspaces {
			if candidate.ID == id && candidate.Slug != "" && candidate.Workspace != nil && candidate.Workspace.HasInstalledCapability(workspace.CapabilityBackupRecovery) {
				return &filecuratorguidance.ProtectionDestination{Kind: "workspace", WorkspaceID: id, Slug: candidate.Slug}
			}
		}
		return nil
	}
	for _, console := range coverage.Consoles {
		if target := destination(console.WorkspaceID); target != nil {
			fact.Destination = target
			break
		}
	}
	if len(coverage.Folders) != 1 || coverage.Folders[0].WorkspaceID != id {
		return fact, nil
	}
	row := coverage.Folders[0]
	if target := destination(row.CoveringWorkspaceID); target != nil {
		fact.Destination = target
	} else if row.CoveringWorkspaceID != "" {
		// A late console removal/permission change cannot carry old evidence.
		return fact, nil
	}
	fact.State, fact.Reason = row.State, row.Reason
	fact.Provider, fact.ObservedAt, fact.LastCompletedAt = row.Provider, row.ObservedAt, row.LastCompletedAt
	if row.State == backuprecovery.CoverageNotCovered && row.Reason == "no_completed_backup_observed" && row.CoveringWorkspaceID != "" {
		fact.State = "configured"
	}
	if (!coverage.Complete || !scope.Complete) && row.State != backuprecovery.CoverageCovered {
		fact.State, fact.Reason = "unknown", "scope_incomplete"
	}
	return fact, nil
}

func curatorScopeIDs(scope dailybrief.WorkspaceScopeResult) []string {
	ids := make([]string, 0, len(scope.Workspaces))
	for _, candidate := range scope.Workspaces {
		ids = append(ids, candidate.ID)
	}
	slices.Sort(ids)
	return ids
}
