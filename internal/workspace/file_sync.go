package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// fileSyncEvent records a workspace-owned file that was re-bound to a new path
// during reconciliation (i.e. it was renamed or moved outside the app). The
// caller hydrates the attachment and publishes a "file.moved" event per entry.
type fileSyncEvent struct {
	attachmentID string
	oldPath      string
	newPath      string
}

// reconcileBackfillByteBudget caps how many bytes of in-place files reconcile
// will hash on a single call. Backfilling a checksum for a file that is still
// where we expect it is speculative — it only matters if that file is later
// renamed — so this work is amortized across loads instead of stalling any one
// tree request. New uploads are hashed at upload time, so this budget only
// affects pre-existing (legacy) files. ~64 MB ≈ a few hundred milliseconds.
// A var (not const) so tests can shrink it.
var reconcileBackfillByteBudget int64 = 64 << 20

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

	// Index active (non-trashed) owned attachments and every attachment path that
	// is already claimed. Trashed files still belong to their attachment and must
	// not be treated as orphan candidates for active missing files.
	type ownedAttachment struct {
		idx  int
		path string
	}
	var active []ownedAttachment
	claimed := make(map[string]bool)
	generatedFiles := workspaceGeneratedFilePaths(ws)
	for i := range ws.Attachments {
		att := &ws.Attachments[i]
		if att.File == nil {
			continue
		}
		rel := extractAttachmentRelativePath(ws.ID, att.File)
		if rel == "" {
			continue
		}
		claimed[rel] = true
		if att.DeletedAt != nil {
			continue
		}
		active = append(active, ownedAttachment{idx: i, path: rel})
	}

	changed := false
	var events []fileSyncEvent

	// Pass 1: refresh/backfill checksums for attachments still in place; collect
	// the ones whose file is now missing. Hashing is bounded by a byte budget so
	// a workspace full of large legacy files cannot stall the tree request; any
	// files left unhashed are picked up on subsequent loads.
	var missing []ownedAttachment
	backfillBudget := reconcileBackfillByteBudget
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

		if backfillBudget <= 0 {
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
		backfillBudget -= size
		att.File.Checksum = sum
		att.File.ChecksumModTime = modTime
		att.File.Size = size
		clearMissingStatus(att.File)
		changed = true
	}

	if len(missing) == 0 {
		if indexUntrackedWorkspaceDiskFiles(ws, filesPath, diskFiles, claimed, generatedFiles, nil, &backfillBudget) {
			changed = true
		}
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
	orphanCandidates := make(map[string]bool)
	for rel, info := range diskFiles {
		if claimed[rel] || !missingSizes[info.Size()] {
			continue
		}
		orphanCandidates[rel] = true
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
			claimed[newPath] = true

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

	if indexUntrackedWorkspaceDiskFiles(ws, filesPath, diskFiles, claimed, generatedFiles, orphanCandidates, &backfillBudget) {
		changed = true
	}

	if changed {
		ws.UpdatedAt = time.Now()
	}
	return changed, events, nil
}

func indexUntrackedWorkspaceDiskFiles(ws *Workspace, filesPath string, diskFiles map[string]os.FileInfo, claimed map[string]bool, generated map[string]bool, deferred map[string]bool, backfillBudget *int64) bool {
	if ws == nil || len(diskFiles) == 0 {
		return false
	}

	changed := false
	for rel, info := range diskFiles {
		if claimed[rel] || generated[rel] || deferred[rel] || !shouldIndexWorkspaceDiskFile(rel, info) {
			continue
		}

		meta := storedWorkspaceFile{
			Name:         info.Name(),
			RelativePath: rel,
			Size:         info.Size(),
			MimeType:     detectMimeType(info.Name()),
			ModTime:      info.ModTime(),
		}
		if backfillBudget != nil && *backfillBudget > 0 {
			absPath, _, pErr := workspaceFilePathWithinRoot(filesPath, rel)
			if pErr == nil {
				sum, modTime, size, hErr := hashFileSHA256(absPath)
				if hErr == nil {
					*backfillBudget -= size
					meta.Checksum = sum
					meta.ModTime = modTime
					meta.Size = size
				}
			}
		}

		now := time.Now()
		if !info.ModTime().IsZero() {
			now = info.ModTime()
		}
		attachment := Attachment{
			ID:          uuid.New().String(),
			WorkspaceID: ws.ID,
			Title:       info.Name(),
			Type:        inferTypeFromMime(meta.MimeType),
			File:        buildWorkspaceOwnedAttachmentFileMeta(ws.ID, meta, ""),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := ws.AddAttachment(attachment); err != nil {
			continue
		}
		claimed[rel] = true
		changed = true
	}
	return changed
}

func workspaceGeneratedFilePaths(ws *Workspace) map[string]bool {
	paths := make(map[string]bool)
	if ws == nil {
		return paths
	}

	add := func(relativePath string) {
		clean := sanitizeWorkspaceRelativePath(relativePath)
		if clean != "" {
			paths[clean] = true
		}
	}

	for _, node := range ws.StoreNodes {
		if !StoreNodeUsesWorkspaceFolder(&node) || strings.TrimSpace(node.LastFilePath) == "" {
			continue
		}
		relativePath := node.LastFilePath
		if strings.TrimSpace(node.Folder) != "" {
			relativePath = filepath.Join(node.Folder, relativePath)
		}
		add(relativePath)
	}

	for i := range ws.Tasks {
		if relativePath := resultStorageWorkspaceFilePath(&ws.Tasks[i], ws.Tasks[i].ResultStorage); relativePath != "" {
			add(relativePath)
		}
	}

	return paths
}

func resultStorageWorkspaceFilePath(task *Task, storage *ResultStorageConfig) string {
	if task == nil || storage == nil || !storage.Enabled || !ResultStorageUsesWorkspaceFolder(storage) {
		return ""
	}

	relativePath := strings.TrimSpace(storage.FilePath)
	filename := ""
	if strings.EqualFold(strings.TrimSpace(storage.WriteMode), "append") {
		// Append datasets are JSONL; track the .jsonl file for sync.
		filename = AppendJSONLFileName(task, storage)
	} else if strings.EqualFold(strings.TrimSpace(storage.Format), "csv") {
		filename = AppendCSVFileName(task, storage)
	} else if strings.TrimSpace(storage.FileName) != "" {
		filename = filepath.Base(filepath.Clean(storage.FileName))
	}

	if relativePath == "" {
		relativePath = filename
	} else if filename != "" && (strings.HasSuffix(relativePath, "/") || !strings.Contains(filepath.Base(relativePath), ".")) {
		relativePath = filepath.Join(relativePath, filename)
	}
	if relativePath == "" {
		return ""
	}
	if strings.TrimSpace(storage.Folder) != "" {
		relativePath = filepath.Join(storage.Folder, relativePath)
	}
	return sanitizeWorkspaceRelativePath(relativePath)
}

func shouldIndexWorkspaceDiskFile(relativePath string, info os.FileInfo) bool {
	if info == nil || info.IsDir() {
		return false
	}
	clean := sanitizeWorkspaceRelativePath(relativePath)
	if clean == "" || isHiddenWorkspacePath(clean) {
		return false
	}
	return true
}

func isHiddenWorkspacePath(relativePath string) bool {
	clean := sanitizeWorkspaceRelativePath(relativePath)
	if clean == "" {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
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
		rel, relErr := filepath.Rel(filesPath, p)
		if relErr != nil {
			return relErr
		}
		clean := sanitizeWorkspaceRelativePath(rel)
		if clean == "" {
			return nil
		}
		if isHiddenWorkspacePath(clean) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
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
