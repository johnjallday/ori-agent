package settingsreset

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

const archiveNotice = "Move only this Personal HQ folder, intact. Other workspace folders and vaults stay where they are. This keeps files and any existing portable checkpoint, not app records that only exist in Ori’s database. Nothing is imported automatically."

var (
	errArchiveRegistration = errors.New("No single eligible Personal HQ is registered. Detached, linked and external workspaces cannot be archived by reset.")
	errArchiveWorkspace    = errors.New("The current Personal HQ does not have a verified folder directly inside the active Workspace Directory.")
	errArchiveOverlap      = errors.New("Moving this folder would also affect another workspace or protected location. Reset can leave it in place instead.")
	errArchiveVolume       = errors.New("This layout cannot be archived safely. The HQ and archive must be on the same supported local filesystem, in directories owned by you.")
	errArchiveDestination  = errors.New("The proposed archive location is not safe or is already occupied. Nothing will be overwritten.")
	errArchiveContents     = errors.New("The HQ folder or its contents could not be verified for an intact move.")
)

type archiveIdentity struct {
	Device   uint64 `json:"device"`
	Inode    uint64 `json:"inode"`
	BirthSec int64  `json:"birth_sec"`
	BirthNS  uint32 `json:"birth_ns"`
}

func (i archiveIdentity) valid() bool { return i.Inode != 0 && i.BirthSec > 0 && i.BirthNS < 1e9 }

type archiveEntry struct {
	Identity                archiveIdentity
	Mount                   uint64
	Owner                   uint32
	Directory, UnsafeParent bool
}

type hqArchiveEvidence struct {
	WorkspaceID string          `json:"workspace_id"`
	Slug        string          `json:"slug"`
	Root        string          `json:"workspace_root"`
	Source      string          `json:"source"`
	Destination string          `json:"destination"`
	SourceID    archiveIdentity `json:"source_identity"`
	RootID      archiveIdentity `json:"workspace_root_identity"`
	ParentID    archiveIdentity `json:"destination_parent_identity"`
	Contents    string          `json:"contents_digest"`
}

func archiveSelection(selected []CategoryID) bool {
	return slices.Contains(selected, CategoryAppRecords)
}

func hqArchiveDestination(installation, operationID, slug string) string {
	return filepath.Join(installation, "hq-archive-"+operationID+"-"+slug)
}

// inspectHQArchive joins the single authoritative designation to the live
// folder registration. The remainder is shared with pre-start inspection.
func inspectHQArchive(ctx context.Context, owners Owners, plan resolvedPlan, operationID string) (*hqArchiveEvidence, error) {
	if owners.Config == nil || owners.Workspaces == nil || owners.Database == nil || owners.Vaults == nil ||
		plan.Installation == "" {
		return nil, errArchiveWorkspace
	}
	root, err := configuredArchiveWorkspaceRoot(owners.Config)
	if err != nil {
		return nil, err
	}
	if actual, err := resolvePath(owners.Workspaces.BasePath()); err != nil || actual != root {
		return nil, errArchiveWorkspace
	}
	record, err := database.InspectResetHQ(ctx, owners.Database.DB)
	if err != nil {
		return nil, errArchiveRegistration
	}
	folders := owners.Workspaces.RegisteredFolderPaths()
	registered, err := resolvePath(folders[record.ID])
	if err != nil || registered != filepath.Join(root, record.Slug) {
		return nil, errArchiveWorkspace
	}
	delete(folders, record.ID)
	for _, path := range folders {
		record.OtherPaths = append(record.OtherPaths, path)
	}
	locations, ok := owners.Agents.(interface{ ResetLocations() store.ResetLocations })
	if !ok {
		return nil, errArchiveOverlap
	}
	agents := locations.ResetLocations()
	otherRoots := []string{owners.Vaults.ManagedRoot(), owners.Config.GetVaultRoot(), agents.Index, agents.Profiles, agents.Projection, agents.State, agents.RootAgents}
	for _, target := range owners.FreshTargets {
		otherRoots = append(otherRoots, target.Path)
	}
	return verifyHQArchiveScope(ctx, plan, root, operationID, record, otherRoots)
}

func configuredArchiveWorkspaceRoot(cfg *config.Manager) (string, error) {
	if cfg == nil || !cfg.IsWorkspaceRootConfirmed() || os.Getenv("WORKSPACE_DIR") != "" {
		return "", errArchiveWorkspace
	}
	root, err := resolvePath(config.ResolveWorkspaceRoot(cfg.GetWorkspaceRoot()))
	if err != nil || filepath.Dir(root) == root {
		return "", errArchiveWorkspace
	}
	return root, nil
}

func verifyHQArchiveScope(ctx context.Context, plan resolvedPlan, root, operationID string, record database.ResetHQRecord, otherRoots []string) (*hqArchiveEvidence, error) {
	if !validID(operationID) || !workspacecontinuity.ValidID(record.ID) ||
		!workspace.IsCanonicalWorkspaceSlug(record.Slug) || workspace.IsReservedTopLevelSlug(record.Slug) ||
		!slices.Contains(plan.Evidence.ProtectedPaths, root) {
		return nil, errArchiveWorkspace
	}
	source := filepath.Join(root, record.Slug)
	destination := hqArchiveDestination(plan.Installation, operationID, record.Slug)
	if len(source) > 4096 || len(destination) > 4096 || pathsOverlap(root, destination) ||
		containsPath(source, plan.Installation) {
		return nil, errArchiveOverlap
	}
	// The root necessarily contains the HQ; every other protected location,
	// registration and owner must be disjoint. The fallback roots also reserve
	// where clean startup could discover workspaces after registration clears.
	for _, p := range plan.Evidence.ProtectedPaths {
		if p != root {
			otherRoots = append(otherRoots, p)
		}
	}
	for _, target := range plan.Targets {
		otherRoots = append(otherRoots, target.Path)
	}
	otherRoots = append(otherRoots, record.OtherPaths...)
	otherRoots = append(otherRoots, filepath.Join(plan.Installation, resetstate.Directory), config.RootAgentsDir(root), config.RootSkillsDir(root))
	for _, fallback := range []string{config.DefaultWorkspaceRoot(), filepath.Join(plan.Installation, "workspace-staging")} {
		resolved, err := resolvePath(fallback)
		if err != nil || pathsOverlap(resolved, destination) {
			return nil, errArchiveOverlap
		}
	}
	for _, path := range otherRoots {
		if path == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, errArchiveContents
		}
		p, err := resolvePath(path)
		if err != nil || pathsOverlap(source, p) || pathsOverlap(destination, p) {
			return nil, errArchiveOverlap
		}
	}
	parents, err := openArchiveParents(root, plan.Installation)
	if err != nil {
		return nil, err
	}
	defer parents.close()
	entry, err := archiveInodeAt(parents.source, record.Slug)
	if err != nil || !entry.Directory || !entry.Identity.valid() || entry.Owner != archiveOwner() {
		return nil, errArchiveWorkspace
	}
	if entry.Mount != parents.sourceEntry.Mount || entry.Identity.Device != parents.sourceEntry.Identity.Device {
		return nil, errArchiveVolume
	}
	if _, err := archiveInodeAt(parents.destination, filepath.Base(destination)); !errors.Is(err, os.ErrNotExist) {
		return nil, errArchiveDestination
	}
	if err := verifyArchiveWorkspaceFile(ctx, source, record); err != nil {
		return nil, err
	}
	digest, err := archiveTreeDigest(ctx, source, entry)
	if err != nil {
		return nil, err
	}
	return &hqArchiveEvidence{WorkspaceID: record.ID, Slug: record.Slug, Root: root, Source: source, Destination: destination,
		SourceID: entry.Identity, RootID: parents.sourceEntry.Identity, ParentID: parents.destinationEntry.Identity, Contents: digest}, nil
}

func verifyArchiveWorkspaceFile(ctx context.Context, source string, record database.ResetHQRecord) error {
	if canonical, err := resolvePath(source); err != nil || canonical != source {
		return errArchiveWorkspace
	}
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, source, workspace.WorkspaceConfigFile, 4<<20)
	if err != nil {
		return errArchiveWorkspace
	}
	var disk struct {
		ID      string         `json:"id"`
		Slug    string         `json:"folder_slug"`
		Owner   string         `json:"owner_user_id"`
		Parent  string         `json:"parent_id"`
		Kind    string         `json:"kind"`
		Status  string         `json:"status"`
		Project string         `json:"project_path"`
		Shared  map[string]any `json:"shared_data"`
	}
	if json.Unmarshal(data, &disk) != nil || disk.ID != record.ID || disk.Owner != "local" ||
		disk.Parent != "" || disk.Slug != record.Slug || disk.Kind == "group" || disk.Status != "active" ||
		workspace.ShapeFromSharedData(disk.Shared) != workspace.ShapeManaged {
		return errArchiveWorkspace
	}
	if disk.Project != "" {
		project := disk.Project
		if !filepath.IsAbs(project) {
			project = filepath.Join(source, project)
		}
		p, err := resolvePath(project)
		if err != nil || !containsPath(source, p) {
			return errArchiveWorkspace
		}
	}
	return nil
}

type archiveParents struct {
	source, destination           *os.File
	sourceEntry, destinationEntry archiveEntry
}

func (p archiveParents) close() {
	if p.source != nil {
		_ = p.source.Close()
	}
	if p.destination != nil {
		_ = p.destination.Close()
	}
}

func openArchiveParents(source, destination string) (archiveParents, error) {
	open := func(path string) (*os.File, archiveEntry, error) {
		if canonical, err := resolvePath(path); err != nil || canonical != path {
			return nil, archiveEntry{}, errArchiveVolume
		}
		file, err := openArchiveParent(path)
		if err != nil {
			return nil, archiveEntry{}, errArchiveVolume
		}
		entry, err := archiveInodeAt(file, ".")
		if err != nil || !entry.Identity.valid() || entry.UnsafeParent || entry.Owner != archiveOwner() || !archiveLocalVolume(file) {
			_ = file.Close()
			return nil, archiveEntry{}, errArchiveVolume
		}
		return file, entry, nil
	}
	var p archiveParents
	var err error
	p.source, p.sourceEntry, err = open(source)
	if err != nil {
		return p, err
	}
	p.destination, p.destinationEntry, err = open(destination)
	if err != nil || p.sourceEntry.Identity.Device != p.destinationEntry.Identity.Device || p.sourceEntry.Mount != p.destinationEntry.Mount {
		p.close()
		return archiveParents{}, errArchiveVolume
	}
	return p, nil
}

// Archive hashing refuses nested mounts and workspaces even when not loaded
// in the live cache. Symlinks are kept as links; we never open their targets.
func archiveTreeDigest(ctx context.Context, path string, wanted archiveEntry) (string, error) {
	root, err := os.OpenRoot(path) // #nosec G703 -- canonical native HQ or deterministic archive path.
	if err != nil {
		return "", errArchiveContents
	}
	defer func() { _ = root.Close() }()
	proof := treeInspection{
		observe: func(current string, info os.FileInfo) error {
			entry, err := archiveEntryPath(current)
			if err != nil || entry.Mount != wanted.Mount || entry.Identity.Device != wanted.Identity.Device {
				return errArchiveVolume
			}
			if current != path && (strings.HasSuffix(strings.ToLower(info.Name()), ".orivault") ||
				(strings.EqualFold(info.Name(), workspace.WorkspaceConfigFile) && filepath.Dir(current) != path)) {
				return errArchiveOverlap
			}
			return nil
		},
		open: func(current string, expected os.FileInfo) (*os.File, error) {
			rel, err := filepath.Rel(path, current)
			if err != nil {
				return nil, err
			}
			file, err := archiveFileAt(root, rel)
			if err != nil {
				return nil, err
			}
			actual, err := file.Stat()
			if err != nil || !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
				_ = file.Close()
				return nil, errArchiveContents
			}
			return file, nil
		},
	}
	digest, err := inspectProtectedTree(ctx, path, nil, nil, proof)
	if err != nil {
		if errors.Is(err, errArchiveVolume) || errors.Is(err, errArchiveOverlap) {
			return "", err
		}
		return "", errArchiveContents
	}
	if after, err := archiveEntryPath(path); err != nil || after != wanted {
		return "", errArchiveContents
	}
	return digest, nil
}
