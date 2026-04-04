package workspace

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

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
		Name:         strings.TrimSpace(meta.Name),
		Size:         meta.Size,
		Mime:         strings.TrimSpace(meta.Mime),
		URL:          strings.TrimSpace(meta.URL),
		RelativePath: sanitizeWorkspaceRelativePath(meta.RelativePath),
		OriginalPath: strings.TrimSpace(meta.OriginalPath),
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
		Name:         file.Name,
		Size:         file.Size,
		Mime:         file.MimeType,
		URL:          workspaceFileURL(workspaceID, file.RelativePath),
		RelativePath: sanitizeWorkspaceRelativePath(file.RelativePath),
		OriginalPath: strings.TrimSpace(originalPath),
	}
}

func storeWorkspaceFile(filesPath string, reader io.Reader, filename string) (*storedWorkspaceFile, error) {
	if strings.TrimSpace(filename) == "" {
		return nil, fmt.Errorf("filename is required")
	}
	if err := os.MkdirAll(filesPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create files directory: %w", err)
	}

	destFilename := uuid.New().String()[:8] + "_" + filename
	destPath := filepath.Join(filesPath, destFilename)

	destFile, err := os.Create(destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() { _ = destFile.Close() }()

	written, err := io.Copy(destFile, reader)
	if err != nil {
		_ = os.Remove(destPath)
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	return &storedWorkspaceFile{
		Name:         filename,
		RelativePath: destFilename,
		Size:         written,
		MimeType:     detectMimeType(filename),
	}, nil
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
	return fmt.Sprintf("/api/workspaces/%s/files/%s", workspaceID, url.PathEscape(filepath.ToSlash(clean)))
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
	clean := sanitizeWorkspaceRelativePath(relativePath)
	if clean == "" || strings.TrimSpace(filesPath) == "" {
		return ""
	}

	joined := filepath.Join(filesPath, clean)
	absFilesPath, err := filepath.Abs(filesPath)
	if err != nil {
		return ""
	}
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return ""
	}
	if !strings.HasPrefix(absJoined, absFilesPath+string(os.PathSeparator)) && absJoined != absFilesPath {
		return ""
	}

	return absJoined
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
