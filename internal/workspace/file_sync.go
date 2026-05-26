package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// fileSyncEvent records a workspace-owned file that was re-bound to a new path
// during reconciliation (i.e. it was renamed or moved outside the app). The
// caller hydrates the attachment and publishes a "file.moved" event per entry.
type fileSyncEvent struct {
	attachmentID string
	oldPath      string
	newPath      string
}

// reconcileWorkspaceFiles brings attachment metadata back in line with what is
// actually on disk under filesPath. It handles external renames/moves and
// flags genuine deletions:
//
//   - For an attachment whose file is still at its recorded path, the cached
//     checksum is backfilled/refreshed when stale (size or mod time changed).
//   - For an attachment whose file is missing, a unique checksum match against
//     an unclaimed on-disk file ("orphan") is treated as a rename/move and the
//     attachment is re-bound to the new path.
//   - An attachment whose file is missing with no unique match is marked
//     "missing" (never auto-deleted) so the UI can offer a Locate action.
//
// The workspace is mutated in place. It returns whether anything changed and
// the set of re-bind events to publish. The caller is responsible for locking
// and saving the workspace.
func reconcileWorkspaceFiles(ws *Workspace, filesPath string) (bool, []fileSyncEvent, error) {
	if ws == nil || strings.TrimSpace(filesPath) == "" {
		return false, nil, nil
	}

	diskFiles, err := snapshotWorkspaceDiskFiles(filesPath)
	if err != nil {
		return false, nil, err
	}

	// Index active (non-trashed) owned attachments and the paths they claim.
	type ownedAttachment struct {
		idx  int
		path string
	}
	var active []ownedAttachment
	claimed := make(map[string]bool)
	for i := range ws.Attachments {
		att := &ws.Attachments[i]
		if att.DeletedAt != nil || att.File == nil {
			continue
		}
		rel := extractAttachmentRelativePath(ws.ID, att.File)
		if rel == "" {
			continue
		}
		active = append(active, ownedAttachment{idx: i, path: rel})
		claimed[rel] = true
	}

	changed := false
	var events []fileSyncEvent

	// Pass 1: refresh/backfill checksums for attachments still in place; collect
	// the ones whose file is now missing.
	var missing []ownedAttachment
	for _, oa := range active {
		info, ok := diskFiles[oa.path]
		if !ok {
			missing = append(missing, oa)
			continue
		}

		att := &ws.Attachments[oa.idx]
		if checksumFresh(att.File, info) {
			if clearMissingStatus(att.File) {
				changed = true
			}
			continue
		}

		absPath, _, pErr := workspaceFilePathWithinRoot(filesPath, oa.path)
		if pErr != nil {
			continue
		}
		sum, modTime, size, hErr := hashFileSHA256(absPath)
		if hErr != nil {
			continue
		}
		att.File.Checksum = sum
		att.File.ChecksumModTime = modTime
		att.File.Size = size
		clearMissingStatus(att.File)
		changed = true
	}

	if len(missing) == 0 {
		if changed {
			ws.UpdatedAt = time.Now()
		}
		return changed, events, nil
	}

	// Pass 2: build an orphan pool (on-disk files no active attachment claims),
	// indexed by checksum. Hash an orphan only when its size could match a
	// missing attachment, to keep the cost bounded.
	missingSizes := make(map[int64]bool)
	for _, oa := range missing {
		missingSizes[ws.Attachments[oa.idx].File.Size] = true
	}
	orphanByChecksum := make(map[string][]string)
	for rel, info := range diskFiles {
		if claimed[rel] || !missingSizes[info.Size()] {
			continue
		}
		absPath, _, pErr := workspaceFilePathWithinRoot(filesPath, rel)
		if pErr != nil {
			continue
		}
		sum, _, _, hErr := hashFileSHA256(absPath)
		if hErr != nil {
			continue
		}
		orphanByChecksum[sum] = append(orphanByChecksum[sum], rel)
	}

	// Pass 3: a unique checksum match re-binds the attachment (rename/move);
	// anything else is marked missing for the Locate flow.
	usedOrphan := make(map[string]bool)
	for _, oa := range missing {
		att := &ws.Attachments[oa.idx]
		matches := availableOrphans(orphanByChecksum[att.File.Checksum], usedOrphan)
		if att.File.Checksum != "" && len(matches) == 1 {
			newPath := matches[0]
			info := diskFiles[newPath]
			usedOrphan[newPath] = true

			att.File.RelativePath = newPath
			att.File.Name = filepath.Base(newPath)
			att.File.URL = workspaceFileURL(ws.ID, newPath)
			att.File.Size = info.Size()
			att.File.ChecksumModTime = info.ModTime()
			clearMissingStatus(att.File)
			att.UpdatedAt = time.Now()
			changed = true
			events = append(events, fileSyncEvent{
				attachmentID: att.ID,
				oldPath:      oa.path,
				newPath:      newPath,
			})
			continue
		}

		if att.File.Status != string(AttachmentFileStatusMissing) {
			att.File.Status = string(AttachmentFileStatusMissing)
			changed = true
		}
	}

	if changed {
		ws.UpdatedAt = time.Now()
	}
	return changed, events, nil
}

// snapshotWorkspaceDiskFiles returns the regular files under filesPath keyed by
// their cleaned workspace-relative path. Symlinks and directories are skipped,
// mirroring buildWorkspaceFileTree.
func snapshotWorkspaceDiskFiles(filesPath string) (map[string]os.FileInfo, error) {
	diskFiles := make(map[string]os.FileInfo)
	walkErr := filepath.WalkDir(filesPath, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(filesPath, p)
		if relErr != nil {
			return relErr
		}
		clean := sanitizeWorkspaceRelativePath(rel)
		if clean == "" {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		diskFiles[clean] = info
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		return nil, walkErr
	}
	return diskFiles, nil
}

func availableOrphans(candidates []string, used map[string]bool) []string {
	var out []string
	for _, c := range candidates {
		if !used[c] {
			out = append(out, c)
		}
	}
	return out
}

// clearMissingStatus resets a previously-flagged "missing" status now that the
// file is present again. Returns true if it cleared anything.
func clearMissingStatus(meta *AttachmentFileMeta) bool {
	if meta != nil && meta.Status == string(AttachmentFileStatusMissing) {
		meta.Status = ""
		return true
	}
	return false
}
