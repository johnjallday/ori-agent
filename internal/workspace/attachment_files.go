package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// isPathWithin reports whether absChild is the same as or contained within absParent.
// Both arguments must be cleaned absolute paths. Uses filepath.Rel to avoid the
// "/foo/bar" prefix-matching "/foo/bar-evil" pitfall.
func isPathWithin(absChild, absParent string) bool {
	if absChild == "" || absParent == "" {
		return false
	}
	rel, err := filepath.Rel(absParent, absChild)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	return !filepath.IsAbs(rel)
}

func pathWithinRootAfterSymlinks(absChild, absRoot string) bool {
	if !isPathWithin(absChild, absRoot) {
		return false
	}
	evaluatedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return false
	}
	evaluatedRoot, err = filepath.Abs(evaluatedRoot)
	if err != nil {
		return false
	}

	existing := absChild
	for {
		if _, err := os.Lstat(existing); err == nil {
			evaluatedExisting, err := filepath.EvalSymlinks(existing)
			if err != nil {
				return false
			}
			evaluatedExisting, err = filepath.Abs(evaluatedExisting)
			if err != nil {
				return false
			}
			suffix, err := filepath.Rel(existing, absChild)
			if err != nil {
				return false
			}
			evaluatedChild := evaluatedExisting
			if suffix != "." {
				evaluatedChild = filepath.Join(evaluatedExisting, suffix)
			}
			evaluatedChild, err = filepath.Abs(evaluatedChild)
			if err != nil {
				return false
			}
			return isPathWithin(evaluatedChild, evaluatedRoot)
		} else if !os.IsNotExist(err) {
			return false
		}

		parent := filepath.Dir(existing)
		if parent == existing {
			return false
		}
		existing = parent
	}
}

type AttachmentFileStatus string

const (
	AttachmentFileStatusOK      AttachmentFileStatus = "ok"
	AttachmentFileStatusMissing AttachmentFileStatus = "missing"
)

// AttachmentFilePathResolver exposes the workspace files root for a workspace.
type AttachmentFilePathResolver interface {
	GetFilesPath(workspaceID string) string
}

type storedWorkspaceFile struct {
	Name         string
	RelativePath string
	Size         int64
	MimeType     string
	Checksum     string
	ModTime      time.Time
}

func HydrateAttachment(attachment Attachment, resolver AttachmentFilePathResolver) Attachment {
	attachment.File = HydrateAttachmentFileMeta(resolver, attachment.WorkspaceID, attachment.File)
	return attachment
}

func HydrateAttachmentFileMeta(resolver AttachmentFilePathResolver, workspaceID string, meta *AttachmentFileMeta) *AttachmentFileMeta {
	clean := sanitizeAttachmentFileMeta(workspaceID, meta)
	if clean == nil {
		return nil
	}

	if relativePath := extractAttachmentRelativePath(workspaceID, clean); relativePath != "" {
		clean.RelativePath = relativePath
		if resolver != nil {
			if status := fileStatusForPath(workspaceOwnedAttachmentPath(resolver, workspaceID, relativePath)); status != "" {
				clean.Status = string(status)
			}
		}
		return clean
	}

	if localPath := localAttachmentAbsolutePath(clean); localPath != "" {
		if status := fileStatusForPath(localPath); status != "" {
			clean.Status = string(status)
		}
	}

	return clean
}

func sanitizeAttachmentFileMeta(workspaceID string, meta *AttachmentFileMeta) *AttachmentFileMeta {
	if meta == nil {
		return nil
	}

	clean := &AttachmentFileMeta{
		Name:            strings.TrimSpace(meta.Name),
		Size:            meta.Size,
		Mime:            strings.TrimSpace(meta.Mime),
		URL:             strings.TrimSpace(meta.URL),
		RelativePath:    sanitizeWorkspaceRelativePath(meta.RelativePath),
		OriginalPath:    strings.TrimSpace(meta.OriginalPath),
		Checksum:        strings.TrimSpace(meta.Checksum),
		ChecksumModTime: meta.ChecksumModTime,
	}

	if clean.RelativePath == "" {
		clean.RelativePath = extractAttachmentRelativePath(workspaceID, clean)
	}
	if clean.Name == "" {
		switch {
		case clean.RelativePath != "":
			clean.Name = filepath.Base(clean.RelativePath)
		case clean.OriginalPath != "":
			clean.Name = filepath.Base(clean.OriginalPath)
		default:
			if localPath := localAttachmentAbsolutePath(clean); localPath != "" {
				clean.Name = filepath.Base(localPath)
			}
		}
	}

	if clean.Name == "" && clean.URL == "" && clean.RelativePath == "" && clean.OriginalPath == "" && clean.Mime == "" && clean.Size == 0 {
		return nil
	}

	return clean
}

func buildWorkspaceOwnedAttachmentFileMeta(workspaceID string, file storedWorkspaceFile, originalPath string) *AttachmentFileMeta {
	return &AttachmentFileMeta{
		Name:            file.Name,
		Size:            file.Size,
		Mime:            file.MimeType,
		URL:             workspaceFileURL(workspaceID, file.RelativePath),
		RelativePath:    sanitizeWorkspaceRelativePath(file.RelativePath),
		OriginalPath:    strings.TrimSpace(originalPath),
		Checksum:        file.Checksum,
		ChecksumModTime: file.ModTime,
	}
}

func storeWorkspaceFile(filesPath string, reader io.Reader, filename string, folderPath ...string) (*storedWorkspaceFile, error) {
	filename = filepath.Base(strings.TrimSpace(filename))
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		return nil, fmt.Errorf("filename is required")
	}
	targetFolder := ""
	if len(folderPath) > 0 {
		targetFolder = folderPath[0]
	}

	targetDir, cleanFolder, err := workspaceFolderPathWithinRoot(filesPath, targetFolder)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create files directory: %w", err)
	}

	destFilename := uuid.New().String()[:8] + "_" + filename
	relativePath := destFilename
	if cleanFolder != "" {
		relativePath = filepath.Join(cleanFolder, destFilename)
	}
	destPath, cleanRelativePath, err := workspaceFilePathWithinRoot(filesPath, relativePath)
	if err != nil {
		return nil, err
	}

	destFile, err := os.Create(destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() { _ = destFile.Close() }()

	// Hash while streaming to disk so we get a content fingerprint without a
	// second read pass. The checksum lets us re-identify this file if it is
	// renamed or moved outside the app.
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(destFile, hasher), reader)
	if err != nil {
		_ = os.Remove(destPath)
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	stored := &storedWorkspaceFile{
		Name:         filename,
		RelativePath: cleanRelativePath,
		Size:         written,
		MimeType:     detectMimeType(filename),
		Checksum:     hex.EncodeToString(hasher.Sum(nil)),
	}
	if info, statErr := os.Stat(destPath); statErr == nil {
		stored.ModTime = info.ModTime()
	}
	return stored, nil
}

// hashFileSHA256 returns the hex SHA-256 of the file at absPath along with its
// mod time and size, in a single read pass.
func hashFileSHA256(absPath string) (sum string, modTime time.Time, size int64, err error) {
	f, err := os.Open(absPath)
	if err != nil {
		return "", time.Time{}, 0, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return "", time.Time{}, 0, err
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", time.Time{}, 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), info.ModTime(), info.Size(), nil
}

// checksumFresh reports whether meta's cached checksum still describes the
// on-disk file: it is valid only when both size and mod time are unchanged
// since the hash was taken. A missing checksum is never fresh.
func checksumFresh(meta *AttachmentFileMeta, info os.FileInfo) bool {
	if meta == nil || info == nil || meta.Checksum == "" {
		return false
	}
	if meta.Size != info.Size() {
		return false
	}
	return meta.ChecksumModTime.Equal(info.ModTime())
}

func removeWorkspaceOwnedAttachmentFile(resolver AttachmentFilePathResolver, workspaceID string, meta *AttachmentFileMeta, keepRelativePath string) {
	if resolver == nil || meta == nil {
		return
	}

	relativePath := extractAttachmentRelativePath(workspaceID, meta)
	if relativePath == "" {
		return
	}
	if keepRelativePath != "" && sanitizeWorkspaceRelativePath(keepRelativePath) == relativePath {
		return
	}

	absPath := workspaceOwnedAttachmentPath(resolver, workspaceID, relativePath)
	if absPath == "" {
		return
	}
	_ = os.Remove(absPath)
}

func workspaceFileURL(workspaceID string, relativePath string) string {
	clean := sanitizeWorkspaceRelativePath(relativePath)
	if clean == "" {
		return ""
	}
	return fmt.Sprintf("/api/workspaces/%s/files/%s", workspaceID, escapeWorkspaceRelativeURLPath(clean))
}

func escapeWorkspaceRelativeURLPath(relativePath string) string {
	slashPath := filepath.ToSlash(relativePath)
	parts := strings.Split(slashPath, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func extractAttachmentRelativePath(workspaceID string, meta *AttachmentFileMeta) string {
	if meta == nil {
		return ""
	}
	if clean := sanitizeWorkspaceRelativePath(meta.RelativePath); clean != "" {
		return clean
	}

	prefix := fmt.Sprintf("/api/workspaces/%s/files/", workspaceID)
	if !strings.HasPrefix(meta.URL, prefix) {
		return ""
	}

	escaped := strings.TrimPrefix(meta.URL, prefix)
	unescaped, err := url.PathUnescape(escaped)
	if err != nil {
		unescaped = escaped
	}

	return sanitizeWorkspaceRelativePath(filepath.FromSlash(unescaped))
}

func workspaceOwnedAttachmentPath(resolver AttachmentFilePathResolver, workspaceID string, relativePath string) string {
	if resolver == nil {
		return ""
	}

	filesPath := resolver.GetFilesPath(workspaceID)
	absPath, _, err := workspaceFilePathWithinRoot(filesPath, relativePath)
	if err != nil {
		return ""
	}

	return absPath
}

func workspaceFilePathWithinRoot(filesPath string, relativePath string) (string, string, error) {
	clean := sanitizeWorkspaceRelativePath(relativePath)
	if clean == "" {
		return "", "", fmt.Errorf("invalid file path")
	}
	if strings.TrimSpace(filesPath) == "" {
		return "", "", fmt.Errorf("workspace files path is required")
	}

	joined := filepath.Join(filesPath, clean)
	absFilesPath, err := filepath.Abs(filesPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve files path: %w", err)
	}
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve file path: %w", err)
	}
	if !isPathWithin(absJoined, absFilesPath) {
		return "", "", fmt.Errorf("invalid file path")
	}
	if !pathWithinRootAfterSymlinks(absJoined, absFilesPath) {
		return "", "", fmt.Errorf("invalid file path")
	}

	return absJoined, clean, nil
}

func workspaceFolderPathWithinRoot(filesPath string, folderPath string) (string, string, error) {
	if strings.TrimSpace(filesPath) == "" {
		return "", "", fmt.Errorf("workspace files path is required")
	}

	clean := ""
	if strings.TrimSpace(folderPath) != "" {
		clean = sanitizeWorkspaceRelativePath(folderPath)
		if clean == "" {
			return "", "", fmt.Errorf("invalid folder path")
		}
	}

	joined := filesPath
	if clean != "" {
		joined = filepath.Join(filesPath, clean)
	}
	absFilesPath, err := filepath.Abs(filesPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve files path: %w", err)
	}
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve folder path: %w", err)
	}
	if !isPathWithin(absJoined, absFilesPath) {
		return "", "", fmt.Errorf("invalid folder path")
	}
	if !pathWithinRootAfterSymlinks(absJoined, absFilesPath) {
		return "", "", fmt.Errorf("invalid folder path")
	}

	return absJoined, clean, nil
}

func workspaceFolderFromRelativePath(relativePath string) string {
	clean := sanitizeWorkspaceRelativePath(relativePath)
	if clean == "" {
		return ""
	}
	dir := filepath.Dir(clean)
	if dir == "." || dir == string(filepath.Separator) {
		return ""
	}
	return dir
}

func localAttachmentAbsolutePath(meta *AttachmentFileMeta) string {
	if meta == nil {
		return ""
	}

	if clean := normalizeAbsoluteAttachmentPath(meta.OriginalPath); clean != "" {
		return clean
	}
	if clean := normalizeAbsoluteAttachmentPath(meta.URL); clean != "" {
		return clean
	}
	return ""
}

func normalizeAbsoluteAttachmentPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	if strings.HasPrefix(trimmed, "file://") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return ""
		}
		trimmed = parsed.Path
	}

	if !filepath.IsAbs(trimmed) {
		return ""
	}

	return filepath.Clean(trimmed)
}

func sanitizeWorkspaceRelativePath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	clean := path.Clean(filepath.ToSlash(trimmed))
	if clean == "." || clean == "/" || strings.HasPrefix(clean, "../") || clean == ".." {
		return ""
	}

	return filepath.FromSlash(clean)
}

func fileStatusForPath(filePath string) AttachmentFileStatus {
	if strings.TrimSpace(filePath) == "" {
		return ""
	}

	info, err := os.Stat(filePath)
	if err == nil {
		if info.IsDir() {
			return AttachmentFileStatusMissing
		}
		return AttachmentFileStatusOK
	}
	if os.IsNotExist(err) {
		return AttachmentFileStatusMissing
	}
	return ""
}
