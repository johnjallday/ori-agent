package workspace

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// StageContinuityTextFiles preserves manifest-declared workspace notes and
// MEMORY.md bytes in the same caller-owned, private staging folder as the
// projected workspace.json. It never follows a symlink, resolves an external
// directory reference, interprets Markdown as commands, or substitutes absent
// files. A real coordinator must prove complete canonical file coverage, stage
// other owner domains/assets, and keep this directory outside live discovery.
// Unknown/large files are not silently counted as restored by this helper.
func StageContinuityTextFiles(ctx context.Context, sourceDir, stagingDir string, inspected workspacecontinuity.Inspection) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	stage, stageIdentity, err := openPrivateContinuityStage(sourceDir, stagingDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stage.Close() }()
	fresh, err := workspacecontinuity.Inspect(ctx, sourceDir)
	if err != nil || fresh.Pointer != inspected.Pointer || !reflect.DeepEqual(fresh.Manifest, inspected.Manifest) {
		return 0, workspacecontinuity.ErrChanged
	}
	if err := CheckContinuityTextCoverage(ctx, sourceDir, fresh.Manifest.Files); err != nil {
		return 0, err // no partial staged text on undeclared notes or memory
	}
	count := 0
	for _, file := range fresh.Manifest.Files {
		if file.Path != "MEMORY.md" && !strings.HasPrefix(file.Path, NotesDir+"/") {
			continue
		}
		if file.Bytes > workspacecontinuity.MaxChunkBytes {
			return count, workspacecontinuity.ErrLimit
		}
		data, err := workspacecontinuity.ReadCanonicalFile(ctx, sourceDir, file.Path, workspacecontinuity.MaxChunkBytes)
		if err != nil {
			return count, err
		}
		if int64(len(data)) != file.Bytes || workspacecontinuity.Digest(data) != file.Digest {
			return count, workspacecontinuity.ErrChanged
		}
		if err := stageIdentity(); err != nil {
			return count, err
		}
		if err := workspacecontinuity.ReplaceCanonicalFileInRoot(ctx, stage, file.Path, "", data); err != nil {
			return count, err
		}
		count++
	}
	fresh, err = workspacecontinuity.Inspect(ctx, sourceDir)
	if err != nil || fresh.Pointer != inspected.Pointer || !reflect.DeepEqual(fresh.Manifest, inspected.Manifest) {
		return count, workspacecontinuity.ErrChanged // staged data stays inert for operation-owned reconciliation
	}
	if err := CheckContinuityTextCoverage(ctx, sourceDir, fresh.Manifest.Files); err != nil {
		return count, err // a new undeclared file during staging is not complete work
	}
	return count, stageIdentity()
}

// CheckContinuityTextCoverage refuses to advertise saved notes or memory when
// an undeclared physical file would be left behind (or could leak via a later
// raw folder registration). Notes are one-level canonical owner files; nested
// directories, links and other entries need their own reviewed domain policy.
// This read-only check is not a completeness audit of other workspace files.
func CheckContinuityTextCoverage(ctx context.Context, sourceDir string, files []workspacecontinuity.Fingerprint) error {
	if len(files) > workspacecontinuity.MaxFiles {
		return workspacecontinuity.ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	expected := make(map[string]bool)
	memory := false
	for _, file := range files {
		switch {
		case file.Path == "MEMORY.md":
			memory = true
		case strings.HasPrefix(file.Path, NotesDir+"/"):
			name := strings.TrimPrefix(file.Path, NotesDir+"/")
			if name == "" || strings.Contains(name, "/") || expected[name] {
				return workspacecontinuity.ErrInvalid
			}
			expected[name] = true
		}
	}
	file, err := workspacecontinuity.OpenCanonicalFile(sourceDir, "MEMORY.md")
	switch {
	case err == nil:
		if err := file.Close(); err != nil {
			return err
		}
		if !memory {
			return workspacecontinuity.ErrIncomplete
		}
	case errors.Is(err, os.ErrNotExist):
		if memory {
			return workspacecontinuity.ErrIncomplete
		}
	case err != nil:
		return err
	}
	entries, err := workspacecontinuity.ListCanonicalDirectory(ctx, sourceDir, NotesDir, workspacecontinuity.MaxFiles)
	if errors.Is(err, workspacecontinuity.ErrIncomplete) && len(expected) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeType != 0 {
			return workspacecontinuity.ErrUnsafe
		}
		if !expected[entry.Name()] {
			return workspacecontinuity.ErrIncomplete
		}
		delete(expected, entry.Name())
	}
	if len(expected) != 0 {
		return workspacecontinuity.ErrIncomplete
	}
	return ctx.Err()
}
