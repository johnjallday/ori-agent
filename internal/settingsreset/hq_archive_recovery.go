package settingsreset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/vault"
)

func (e resolvedEvidence) digestArchiveRemainder(ctx context.Context) (string, error) {
	a := e.HQArchive
	return inspectProtectedTree(ctx, a.Root, []string{e.AgentsFolder, e.SkillsFolder, a.Source}, []string{e.PluginList}, treeInspection{sizelessDir: a.Root})
}

func (e *hqArchiveEvidence) openParents() (archiveParents, error) {
	p, err := openArchiveParents(e.Root, filepath.Dir(e.Destination))
	if err != nil {
		return archiveParents{}, err
	}
	if p.sourceEntry.Identity != e.RootID || p.destinationEntry.Identity != e.ParentID {
		p.close()
		return archiveParents{}, errArchiveVolume
	}
	return p, nil
}

func (e *hqArchiveEvidence) atExpectedLocation(p archiveParents, moved bool) (archiveEntry, error) {
	from, fromErr := archiveInodeAt(p.source, e.Slug)
	to, toErr := archiveInodeAt(p.destination, filepath.Base(e.Destination))
	checks := func(entry archiveEntry, parent archiveEntry) bool {
		return entry.Directory && entry.Owner == archiveOwner() && entry.Identity == e.SourceID && entry.Mount == parent.Mount
	}
	if moved && errors.Is(fromErr, os.ErrNotExist) && toErr == nil && checks(to, p.destinationEntry) {
		return to, nil
	}
	if !moved && errors.Is(toErr, os.ErrNotExist) && fromErr == nil && checks(from, p.sourceEntry) {
		return from, nil
	}
	return archiveEntry{}, errArchiveContents
}

func (e *hqArchiveEvidence) pathsStillOwned() bool {
	from, err := archiveEntryPath(e.Root)
	if err != nil || !from.Directory || from.Identity != e.RootID {
		return false
	}
	to, err := archiveEntryPath(filepath.Dir(e.Destination))
	return err == nil && to.Directory && to.Identity == e.ParentID
}

func verifyArchiveContent(ctx context.Context, p archiveParents, e *hqArchiveEvidence, moved bool) error {
	entry, err := e.atExpectedLocation(p, moved)
	if err != nil || !e.pathsStillOwned() {
		return ErrScopeChanged
	}
	path := e.Source
	if moved {
		path = e.Destination
	}
	digest, err := archiveTreeDigest(ctx, path, entry)
	if err != nil || digest != e.Contents {
		return ErrScopeChanged
	}
	if _, err := e.atExpectedLocation(p, moved); err != nil || !e.pathsStillOwned() {
		return ErrScopeChanged
	}
	return nil
}

// Observe only these two states. A pending move cannot claim a matching
// destination until intent became durable. Both-present, both-missing and
// replacement states confer no authority to do anything.
func observeArchiveFiles(ctx context.Context, j *journal) (bool, error) {
	e := j.Plan.Evidence.HQArchive
	p, err := e.openParents()
	if err != nil {
		return false, err
	}
	defer p.close()
	moved := j.Archive.Stage == archiveVerified
	if j.Archive.Stage == archiveIntent {
		_, err := archiveInodeAt(p.source, e.Slug)
		moved = errors.Is(err, os.ErrNotExist)
	}
	return moved, verifyArchiveContent(ctx, p, e, moved)
}

func validateArchiveRecoveryScope(ctx context.Context, j *journal, expected map[string]string, report database.ResetInspection) error {
	if j.Archive == nil {
		return nil
	}
	if j.Archive.Stage != archiveVerified {
		// No category has run yet. Re-derive the current effective directory,
		// designation, managed shape, and protected owners independently of
		// serialized paths. Read-only probes do not construct live stores.
		cfg := config.NewManager(expected["settings_fields"])
		if err := cfg.Load(); err != nil {
			return ErrScopeChanged
		}
		root, err := configuredArchiveWorkspaceRoot(cfg)
		if err != nil {
			return err
		}
		record, err := database.InspectResetHQFile(ctx, j.Plan.Evidence.DatabasePath)
		if err != nil {
			return errArchiveRegistration
		}
		managedVault := config.ResolveVaultRoot(cfg.GetVaultRoot())
		otherRoots := []string{managedVault}
		for _, path := range expected {
			otherRoots = append(otherRoots, path)
		}
		retainedVaults := vault.NewStore(nil, vault.StoreOptions{VaultFilesBaseDir: j.Plan.Installation, ManagedVaultRoot: managedVault})
		for _, path := range report.VaultPaths {
			otherRoots = append(otherRoots, retainedVaults.RetainedPath(path))
		}
		source, dest, err := archiveScopePaths(ctx, j.Plan, root, j.Operation.ID, record, otherRoots)
		e := j.Plan.Evidence.HQArchive
		if err != nil || e.WorkspaceID != record.ID || e.Root != root || e.Source != source || e.Destination != dest {
			return ErrScopeChanged
		}
	}
	moved, err := observeArchiveFiles(ctx, j)
	if err != nil {
		return err
	}
	if moved {
		j.Plan.Evidence.archiveRemainder = j.Archive.Remainder
	}
	return nil
}

// archiveMoveHooks are private fault boundaries for process-loss tests. They
// are neither HTTP nor host configuration. Returning an error leaves intent
// durable so the next process must re-observe the filesystem.
type archiveMoveHooks struct {
	beforeMove, afterMove func() error
}

func applyHQArchive(ctx context.Context, lease *resetstate.Lease, j *journal, now func() time.Time, hooks archiveMoveHooks) error {
	if j.Archive == nil || j.Archive.Stage == archiveVerified {
		return nil
	}
	e := j.Plan.Evidence.HQArchive
	p, err := e.openParents()
	if err != nil {
		return err
	}
	defer p.close()
	moved := j.Plan.Evidence.archiveRemainder != ""
	if err := verifyArchiveContent(ctx, p, e, moved); err != nil {
		return err
	}
	if j.Archive.Stage != archiveIntent {
		j.Archive.Stage = archiveIntent
		j.Operation.Revision++
		j.Operation.UpdatedAt = now().UTC()
		if err := persistRecoveryJournal(lease, j); err != nil {
			return err
		}
	}
	if !moved {
		if hooks.beforeMove != nil && hooks.beforeMove() != nil {
			return ErrAdmissionUncertain
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := e.atExpectedLocation(p, false); err != nil || !e.pathsStillOwned() || lease.Check() != nil {
			return ErrScopeChanged
		}
		// The syscall is the no-replace check. No unconditional rename or
		// copy/delete fallback is allowed on any error, including EXDEV.
		if err := renameArchiveNoReplace(p.source, e.Slug, p.destination, filepath.Base(e.Destination)); err != nil {
			return errArchiveDestination
		}
		if hooks.afterMove != nil && hooks.afterMove() != nil {
			return ErrAdmissionUncertain
		}
	}
	if err := errors.Join(p.source.Sync(), p.destination.Sync()); err != nil {
		return errArchiveContents
	}
	if err := verifyArchiveContent(ctx, p, e, true); err != nil {
		return err
	}
	j.Plan.Evidence.archiveRemainder = j.Archive.Remainder
	if !protectedDigestsUnchanged(ctx, j.Plan.Evidence) {
		return ErrScopeChanged
	}
	j.Archive.Stage = archiveVerified
	j.Operation.HQArchive = publicArchiveResult(j, OutcomeCompleted)
	j.Operation.Revision++
	j.Operation.UpdatedAt = now().UTC()
	return persistRecoveryJournal(lease, j)
}
