package workspace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file is the read-only counterpart of buildWorkspaceFileTree and
// buildWorkspaceOutputsTree, for the by-name search. It lists names and nothing
// else: it opens no file, reads no content, and writes nothing, and unlike the
// tree's own listing it stops at a stated number of entries, a stated depth, or
// when its context ends, so a search can never be made to walk a whole disk.

// NameWalkSection selects which folder of a workspace a name walk reads.
type NameWalkSection string

const (
	// NameWalkFiles is the workspace's files/ folder.
	NameWalkFiles NameWalkSection = "files"
	// NameWalkOutputs is the workspace's outputs/ folder.
	NameWalkOutputs NameWalkSection = "outputs"
)

// NameWalkEntry is one regular file found.
type NameWalkEntry struct {
	// RelativePath is slash-separated and relative to the section's folder.
	RelativePath string
	Name         string
	Size         int64
	ModTime      time.Time
}

// NameWalkLimits bound one walk. A zero MaxEntries or MaxDepth means no entries
// are read (the caller always sets both).
type NameWalkLimits struct {
	// MaxEntries counts every entry looked at, folders included.
	MaxEntries int
	// MaxDepth is the deepest level read: a folder at this depth is listed but
	// not entered.
	MaxDepth int
}

// NameWalkStop says why a walk ended before it was complete.
type NameWalkStop string

const (
	NameWalkComplete   NameWalkStop = ""
	NameWalkEntryCap   NameWalkStop = "entries"
	NameWalkDepthCap   NameWalkStop = "depth"
	NameWalkCancelled  NameWalkStop = "cancelled"
	NameWalkUnreadable NameWalkStop = "unreadable"
)

var errNameWalkEntryCap = errors.New("name walk: entry cap reached")

// ReferencedAgentNames is the set of agent names a workspace references: the
// names its Agents section can show. The exported form of referencedAgentNames.
func ReferencedAgentNames(ws *Workspace) []string { return referencedAgentNames(ws) }

// WalkFileNames walks root, the files/ or outputs/ folder of ws, and calls visit
// for every regular file. It applies the rules the tree's own listing applies:
// symbolic links are never followed or reported, hidden paths (anything under a
// dot-folder, dot-files) are skipped, and in files/ a file that belongs to a
// trashed attachment is skipped. A missing folder is an empty walk.
//
// It returns how many entries it looked at and why it stopped early, if it did.
// A folder inside root that cannot be read ends the walk as NameWalkUnreadable
// only after the rest has been walked; a root that cannot be read is an error.
func WalkFileNames(ctx context.Context, ws *Workspace, root string, section NameWalkSection, limits NameWalkLimits, visit func(NameWalkEntry)) (int, NameWalkStop, error) {
	if strings.TrimSpace(root) == "" || limits.MaxEntries <= 0 || limits.MaxDepth <= 0 {
		return 0, NameWalkComplete, nil
	}
	trashed := map[string]bool{}
	if section == NameWalkFiles && ws != nil {
		for _, attachment := range ws.Attachments {
			if attachment.DeletedAt == nil || attachment.File == nil {
				continue
			}
			if rel := extractAttachmentRelativePath(ws.ID, attachment.File); rel != "" {
				trashed[rel] = true
			}
		}
	}

	visited := 0
	stop := NameWalkComplete
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error { // #nosec G703 -- root is the store's own folder for a workspace id it resolved; nothing here comes from the request
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			// A folder that cannot be read is skipped; the walk goes on and says
			// at the end that part of it was not read.
			if stop == NameWalkComplete {
				stop = NameWalkUnreadable
			}
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		clean := sanitizeWorkspaceRelativePath(rel)
		if clean == "" || isHiddenWorkspacePath(clean) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if section == NameWalkFiles {
			if _, _, err := workspaceFilePathWithinRoot(root, clean); err != nil {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		visited++
		if visited > limits.MaxEntries {
			visited = limits.MaxEntries
			stop = NameWalkEntryCap
			return errNameWalkEntryCap
		}

		if entry.IsDir() {
			if strings.Count(filepath.ToSlash(clean), "/")+1 >= limits.MaxDepth {
				if stop == NameWalkComplete {
					stop = NameWalkDepthCap
				}
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if section == NameWalkFiles && trashed[clean] {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			// Removed between the directory read and now.
			return nil
		}
		visit(NameWalkEntry{
			RelativePath: filepath.ToSlash(clean),
			Name:         info.Name(),
			Size:         info.Size(),
			ModTime:      info.ModTime(),
		})
		return nil
	})

	switch {
	case err == nil, errors.Is(err, errNameWalkEntryCap):
		return visited, stop, nil
	case ctx.Err() != nil && errors.Is(err, ctx.Err()):
		return visited, NameWalkCancelled, nil
	case errors.Is(err, fs.ErrNotExist):
		// No such folder yet: nothing is stored there.
		return visited, NameWalkComplete, nil
	default:
		return visited, NameWalkComplete, err
	}
}
