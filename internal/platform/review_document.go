package platform

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ReviewDocumentViewer chooses a known document reader, not an arbitrary launch
// association. A renamed script is passed as data to the reader, never executed.
func ReviewDocumentViewer(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf", ".jpg", ".jpeg", ".png", ".gif", ".heic", ".tiff", ".tif", ".bmp":
		return "Preview"
	case ".txt", ".md", ".csv", ".docx":
		return "TextEdit"
	case ".mp3", ".wav", ".aif", ".aiff", ".m4a", ".mp4", ".mov", ".m4v":
		return "QuickTime Player"
	default:
		return ""
	}
}

// OpenReviewDocument hands an already validated local review source to its
// fixed system reader. Do not fall back to open(path) for unknown types.
func OpenReviewDocument(path string) error {
	return openWithSystemReader(path, ReviewDocumentViewer(path))
}

// OpenTextDocument treats source files as documents, never as launch targets.
func OpenTextDocument(path string) error { return openWithSystemReader(path, "TextEdit") }

// OpenImageDocument uses Preview even when the user changed the file association.
func OpenImageDocument(path string) error { return openWithSystemReader(path, "Preview") }

func openWithSystemReader(path, viewer string) error {
	if runtime.GOOS != "darwin" || DesktopOpenDisabled() || viewer == "" || !filepath.IsAbs(path) {
		return errors.New("document opening is unavailable")
	}
	// #nosec G204 G702 -- fixed executable and allowlisted system app; the validated absolute path is a document argument.
	return exec.Command("/usr/bin/open", "-a", "/System/Applications/"+viewer+".app", path).Run()
}
