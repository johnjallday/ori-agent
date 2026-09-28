package workspace

import (
	"context"
	"errors"
	"os"
	"sort"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// CollectContinuityTextFiles hashes the physical one-level note owner and
// MEMORY.md for a source preparation attempt. It never creates a checkpoint:
// the caller must reconcile canonical note/knowledge SQL and revalidate both
// bytes and owner membership under its publication fence before reporting Ready.
func CollectContinuityTextFiles(ctx context.Context, folder string) ([]workspacecontinuity.Fingerprint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files := make([]workspacecontinuity.Fingerprint, 0)
	memory, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, "MEMORY.md", workspacecontinuity.MaxChunkBytes)
	if err == nil {
		files = append(files, workspacecontinuity.Fingerprint{Path: "MEMORY.md", Digest: workspacecontinuity.Digest(memory), Bytes: int64(len(memory))})
	} else if !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		return nil, err
	}
	entries, err := workspacecontinuity.ListCanonicalDirectory(ctx, folder, NotesDir, workspacecontinuity.MaxFiles)
	if errors.Is(err, workspacecontinuity.ErrIncomplete) {
		if err := CheckContinuityTextCoverage(ctx, folder, files); err != nil {
			return nil, err
		}
		return files, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries)+len(files) > workspacecontinuity.MaxFiles {
		return nil, workspacecontinuity.ErrLimit
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeType != 0 {
			return nil, workspacecontinuity.ErrUnsafe
		}
		location := NotesDir + "/" + entry.Name()
		data, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, location, workspacecontinuity.MaxChunkBytes)
		if err != nil {
			return nil, err
		}
		files = append(files, workspacecontinuity.Fingerprint{Path: location, Digest: workspacecontinuity.Digest(data), Bytes: int64(len(data))})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if err := CheckContinuityTextCoverage(ctx, folder, files); err != nil {
		return nil, err
	}
	return files, nil
}
