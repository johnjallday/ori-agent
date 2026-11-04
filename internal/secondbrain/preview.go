package secondbrain

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// The kinds of note a preview may show (Issue 776 FR16): an Inbox note, a
// Daily note and a project's README. People notes and the facts note are not.
const (
	PreviewInbox   = "inbox"
	PreviewDaily   = "daily"
	PreviewProject = "project"
)

const (
	// PreviewBodyBytes is the most of a note's text a preview shows.
	PreviewBodyBytes = inboxPeekBytes
	// maxPreviewProofBytes bounds what is read to prove the marker: the hash
	// covers the whole body, so a note longer than what is shown is read to
	// here, hashed, and only its first PreviewBodyBytes are kept. A longer note
	// cannot be proven Ori's and is refused.
	maxPreviewProofBytes = 256 * 1024
)

var (
	// ErrPreviewInvalid refuses a kind that is not previewable, or a name that
	// is not one plain file or folder name. It is decided before any read.
	ErrPreviewInvalid = errors.New("secondbrain: that note cannot be previewed")
	// ErrPreviewNotOri refuses a note whose marker does not prove Ori wrote it
	// as it is now: the user's own, or one the user edited.
	ErrPreviewNotOri = errors.New("secondbrain: that note is the user's")
	// ErrPreviewNotFound is a note that is not there (or not a plain file).
	ErrPreviewNotFound = errors.New("secondbrain: that note is not there")
)

// Preview is one note Ori wrote, as the panel shows it inline. Body is plain
// text, never rendered; it is read on request and never kept.
type Preview struct {
	Name      string
	Kind      string
	Title     string
	Body      string
	Truncated bool
	// Rel is the note's path inside the folder, for the caller's Obsidian link.
	Rel string
}

// previewTarget is where a preview's note lives: the folder (or project folder)
// it is opened in and the file name inside it.
type previewTarget struct {
	folders []string
	file    string
	kinds   []string
}

// PreviewRel validates kind and name and returns the note's path, without
// touching the filesystem. name is one component: a file name ending in .md for
// inbox and daily, a project folder name for project.
func PreviewRel(kind, name string) (string, error) {
	target, err := previewTargetFor(kind, name)
	if err != nil {
		return "", err
	}
	return strings.Join(append(append([]string(nil), target.folders...), target.file), "/"), nil
}

func previewTargetFor(kind, name string) (previewTarget, error) {
	if !ValidFolderName(name) {
		return previewTarget{}, ErrPreviewInvalid
	}
	switch kind {
	case PreviewInbox, PreviewDaily:
		if !IsMarkdown(name) || strings.EqualFold(name, projectNoteName) {
			return previewTarget{}, ErrPreviewInvalid
		}
		if kind == PreviewInbox {
			return previewTarget{folders: []string{FolderInbox}, file: name,
				kinds: []string{NoteKindCapture, NoteKindSeed, NoteKindFollowUp}}, nil
		}
		return previewTarget{folders: []string{FolderDaily}, file: name, kinds: []string{NoteKindJournal}}, nil
	case PreviewProject:
		return previewTarget{folders: []string{FolderProjects, name}, file: projectNoteName, kinds: []string{NoteKindProject}}, nil
	default:
		return previewTarget{}, ErrPreviewInvalid
	}
}

// ReadPreview opens one note through the pinned folder, never following a link
// at any step, and returns its text only when the marker proves Ori wrote it as
// it is. Nothing is written, cached or kept.
func ReadPreview(root, kind, name string) (Preview, error) {
	target, err := previewTargetFor(kind, name)
	if err != nil {
		return Preview{}, err
	}
	pinned, err := pinFolder(root)
	if err != nil {
		return Preview{}, err
	}
	defer func() { _ = pinned.Close() }()
	dir, err := openRealFolders(pinned, target.folders)
	if err != nil {
		return Preview{}, err
	}
	defer func() { _ = dir.Close() }()
	content, err := readProvable(dir, target.file)
	if err != nil {
		return Preview{}, err
	}
	mark := ReadMark(content)
	if !mark.OwnedByOri || !containsString(target.kinds, mark.Kind) {
		return Preview{}, ErrPreviewNotOri
	}
	_, body, _ := cutFrontmatter(content[len(markerOpen):])
	shown, truncated := clipUTF8(body, PreviewBodyBytes)
	rel, _ := PreviewRel(kind, name)
	return Preview{
		Name: name, Kind: kind, Title: previewTitle(shown, target, name),
		Body: string(shown), Truncated: truncated, Rel: rel,
	}, nil
}

// ProjectNotesByOri reports which of the named project folders hold a README
// that opens with Ori's project marker, from the marker's first bytes only, so
// the panel offers Open on those alone. A preview still proves the whole note.
func ProjectNotesByOri(root string, names []string) map[string]bool {
	out := map[string]bool{}
	pinned, err := pinFolder(root)
	if err != nil {
		return out
	}
	defer func() { _ = pinned.Close() }()
	for _, name := range names {
		if !ValidFolderName(name) {
			continue
		}
		dir, err := openRealFolders(pinned, []string{FolderProjects, name})
		if err != nil {
			continue
		}
		if info, err := dir.Lstat(projectNoteName); err == nil && info.Mode().IsRegular() {
			if _, kind, ok := markedNote(dir, projectNoteName); ok && kind == NoteKindProject {
				out[name] = true
			}
		}
		_ = dir.Close()
	}
	return out
}

// openRealFolders walks folder by folder, each a real directory (a link in
// any place lists nothing), and returns the last one as its own root.
func openRealFolders(root *os.Root, folders []string) (*os.Root, error) {
	current := root
	owned := false
	for _, folder := range folders {
		info, err := current.Lstat(folder)
		if err != nil || !info.IsDir() {
			if owned {
				_ = current.Close()
			}
			return nil, ErrPreviewNotFound
		}
		next, err := current.OpenRoot(folder)
		if owned {
			_ = current.Close()
		}
		if err != nil {
			return nil, ErrPreviewNotFound
		}
		current, owned = next, true
	}
	return current, nil
}

// readProvable reads one plain file, no-follow, up to the proof bound. A file
// over the bound cannot be proven Ori's.
func readProvable(dir *os.Root, name string) ([]byte, error) {
	if info, err := dir.Lstat(name); err != nil || !info.Mode().IsRegular() {
		return nil, ErrPreviewNotFound
	}
	file, err := openNote(dir, name)
	if err != nil {
		return nil, ErrPreviewNotFound
	}
	defer func() { _ = file.Close() }()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, ErrPreviewNotFound
	}
	content, err := io.ReadAll(io.LimitReader(file, maxPreviewProofBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxPreviewProofBytes || !bytes.HasPrefix(content, []byte(markerOpen)) {
		return nil, ErrPreviewNotOri
	}
	return content, nil
}

// clipUTF8 keeps at most limit bytes, never cutting a character in two.
func clipUTF8(body []byte, limit int) ([]byte, bool) {
	if len(body) <= limit {
		return body, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut], true
}

// previewTitle is the note's first "# " heading, or what names it.
func previewTitle(body []byte, target previewTarget, name string) string {
	for _, line := range strings.Split(string(body), "\n") {
		if title, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok {
			if title = strings.TrimSpace(title); title != "" {
				return truncateRunes(title, maxInboxTitleRunes)
			}
		}
	}
	if target.file == projectNoteName {
		return name
	}
	return strings.TrimSuffix(name, ".md")
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
