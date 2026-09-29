package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// StageContinuityWorkspace writes one reviewed workspace's denied canonical
// projection to a caller-owned, empty private staging directory. It does NOT
// install/register a folder, insert SQL, grant local configuration, or complete
// an import. The coordinator must own this staging directory and its cleanup,
// retain a reset permit, validate physical parent membership, and recheck the
// source/destination under the same reviewed operation before publication.
// All other files/profiles/domains need separate owned staging; this one file
// is never a substitute for copying the workspace tree.
func StageContinuityWorkspace(ctx context.Context, sourceDir, stagingDir string, inspected workspacecontinuity.Inspection, parentID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stage, stageIdentity, err := openPrivateContinuityStage(sourceDir, stagingDir)
	if err != nil {
		return err
	}
	defer func() { _ = stage.Close() }()
	file, err := stage.Open(".")
	if err != nil {
		return workspacecontinuity.ErrUnsafe
	}
	entries, readErr := file.ReadDir(1)
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(entries) != 0 {
		return workspacecontinuity.ErrCollision
	}
	canonical, err := workspacecontinuity.ReadCanonicalFile(ctx, sourceDir, WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		return err
	}
	var projected ContinuityWorkspaceProjection
	found := false
	err = workspacecontinuity.ReadComponentRecords(ctx, sourceDir, inspected, "workspace", func(chunk workspacecontinuity.Chunk) error {
		if found || chunk.Family != "workspaces" || len(chunk.Records) != 1 || chunk.Records[0].ID != inspected.Manifest.WorkspaceID {
			return workspacecontinuity.ErrInvalid
		}
		var descriptor ContinuityWorkspace
		if err := workspacecontinuity.DecodeRecord(chunk.Records[0], &descriptor); err != nil {
			return err
		}
		if descriptor.SQLMetadata == nil {
			return workspacecontinuity.ErrIncomplete
		}
		result, err := ProjectContinuityWorkspace(chunk.Records[0], canonical, parentID)
		if err != nil {
			return err
		}
		projected, found = result, true
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		return workspacecontinuity.ErrIncomplete
	}
	data, err := MarshalContinuityWorkspace(projected)
	if err != nil {
		return err
	}
	if err := stageIdentity(); err != nil {
		return err
	}
	if err := workspacecontinuity.ReplaceCanonicalFileInRoot(ctx, stage, WorkspaceConfigFile, "", data); err != nil {
		return err
	}
	// This is a source check after the one staged write, not permission to move
	// the file or claim a destination row. A failed check leaves inert staging
	// evidence for the operation owner to reconcile, never a trusted workspace.
	fresh, err := workspacecontinuity.Inspect(ctx, sourceDir)
	if err != nil || fresh.Pointer != inspected.Pointer || fresh.Manifest.SourceFingerprint != inspected.Manifest.SourceFingerprint {
		return workspacecontinuity.ErrChanged
	}
	return stageIdentity()
}

// openPrivateContinuityStage keeps a caller-owned staging root outside the
// source tree and checks its identity before separate file publications. A
// coordinator must still own exclusive access to this private directory.
func openPrivateContinuityStage(sourceDir, stagingDir string) (*os.Root, func() error, error) {
	sourcePath, err := filepath.EvalSymlinks(sourceDir)
	if err != nil {
		return nil, nil, workspacecontinuity.ErrUnsafe
	}
	stagePath, err := filepath.EvalSymlinks(stagingDir)
	if err != nil {
		return nil, nil, workspacecontinuity.ErrUnsafe
	}
	rel, err := filepath.Rel(sourcePath, stagePath)
	if err != nil || rel == "." || rel == ".." || !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return nil, nil, workspacecontinuity.ErrUnsafe
	}
	// #nosec G703 -- stagingDir is a caller-owned private staging directory, not a portable path; Lstat refuses a link before writing.
	info, err := os.Lstat(stagingDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return nil, nil, workspacecontinuity.ErrUnsafe
	}
	stage, err := os.OpenRoot(stagingDir)
	if err != nil {
		return nil, nil, workspacecontinuity.ErrUnsafe
	}
	stageIdentity := func() error {
		opened, openedErr := stage.Stat(".")
		// #nosec G703 -- stagingDir is the same caller-owned private staging directory checked before opening its confined root.
		current, currentErr := os.Lstat(stagingDir)
		if openedErr != nil || currentErr != nil || !os.SameFile(info, opened) || !os.SameFile(info, current) {
			return workspacecontinuity.ErrChanged
		}
		return nil
	}
	if err := stageIdentity(); err != nil {
		_ = stage.Close()
		return nil, nil, err
	}
	return stage, stageIdentity, nil
}
