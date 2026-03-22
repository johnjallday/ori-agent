package toolapi

import (
	"context"
	"strings"
)

// FileAttachment represents a file attached to a tool call.
type FileAttachment struct {
	Name    string
	Type    string
	Size    int64
	Content []byte
}

// FileAttachmentHandler is an optional interface that tools can implement
// to receive file attachments from the chat interface.
type FileAttachmentHandler interface {
	AcceptsFiles() []string
	CallWithFiles(ctx context.Context, args string, files []FileAttachment) (string, error)
}

// IsFileTypeAccepted checks if a file matches any of the accepted types.
// acceptedTypes can contain MIME types (e.g., "audio/wav") or extensions (e.g., ".wav").
func IsFileTypeAccepted(acceptedTypes []string, filename string, mimeType string) bool {
	if len(acceptedTypes) == 0 {
		return false
	}

	ext := ""
	if idx := strings.LastIndexByte(filename, '.'); idx >= 0 {
		ext = strings.ToLower(filename[idx:])
	}

	mimeType = strings.ToLower(mimeType)

	for _, accepted := range acceptedTypes {
		accepted = strings.ToLower(accepted)
		if len(accepted) > 0 && accepted[0] == '.' {
			if ext == accepted {
				return true
			}
		} else {
			if mimeType == accepted {
				return true
			}
		}
	}

	return false
}

// FilterFilesByAcceptedTypes filters a slice of FileAttachments to only include
// files that match the accepted types.
func FilterFilesByAcceptedTypes(files []FileAttachment, acceptedTypes []string) []FileAttachment {
	if len(acceptedTypes) == 0 {
		return nil
	}

	var filtered []FileAttachment
	for _, f := range files {
		if IsFileTypeAccepted(acceptedTypes, f.Name, f.Type) {
			filtered = append(filtered, f)
		}
	}
	return filtered
}
