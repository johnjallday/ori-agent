package secondbrain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// The only folders Ori may write in, and only in a folder it created (FR12).
const (
	FolderInbox = "Inbox"
	FolderDaily = "Daily"

	noteFileMode   fs.FileMode = 0o644
	noteFolderMode fs.FileMode = 0o755

	writeTempPrefix = ".ori-write-"
)

var (
	// ErrNotOwned refuses a source Ori did not create.
	ErrNotOwned = errors.New("secondbrain: Ori did not create this notes folder")
	// ErrOutsideOwnedFolders refuses a path that is not a note directly in
	// Inbox or Daily, or that reaches through a link.
	ErrOutsideOwnedFolders = errors.New("secondbrain: Ori only writes to Inbox and Daily")
	// ErrUserModified refuses to replace a note Ori cannot prove is untouched.
	ErrUserModified = errors.New("secondbrain: that note is the user's")
	// ErrNoteTooLarge refuses a note the reader could never read back.
	ErrNoteTooLarge = errors.New("secondbrain: note is too large")
	// ErrInvalidNote refuses an unknown marker kind.
	ErrInvalidNote = errors.New("secondbrain: invalid note")
	// ErrNoteExists refuses Create over a name that is taken.
	ErrNoteExists = errors.New("secondbrain: a note with that name already exists")
)

// Writer is the one way anything is written to a notes folder after it was
// created (FR12, FR13). It holds no open handle; every Write pins the folder
// again, so disconnecting ends the permission with the record.
type Writer struct {
	root string
}

// NewWriter refuses a nil source and every source Ori did not create.
func NewWriter(source *Source) (*Writer, error) {
	if source == nil || !source.CreatedByOri() {
		return nil, ErrNotOwned
	}
	return &Writer{root: source.Path}, nil
}

// WriteResult says what a Write did.
type WriteResult struct {
	Rel      string
	Replaced bool
}

// Write puts one marked note at rel, which must be <Inbox|Daily>/<name>.md.
// A missing Inbox or Daily is recreated. An existing note is replaced only
// when it is still exactly what Ori wrote. Nothing is ever deleted or renamed
// except this call's own temp file, which becomes the note.
func (w *Writer) Write(rel, kind string, body []byte) (WriteResult, error) {
	return w.write(rel, kind, body, false)
}

// Create puts one new marked note at rel and never replaces anything: a name
// already taken, by the user or by Ori, is ErrNoteExists and the tree is left
// alone. Captures use it, since each one is its own note.
func (w *Writer) Create(rel, kind string, body []byte) (WriteResult, error) {
	return w.write(rel, kind, body, true)
}

func (w *Writer) write(rel, kind string, body []byte, create bool) (WriteResult, error) {
	if !validNoteKind(kind) {
		return WriteResult{}, ErrInvalidNote
	}
	content := RenderNote(kind, body)
	if int64(len(content)) > MaxNoteBytes {
		return WriteResult{}, ErrNoteTooLarge
	}
	folder, name, err := splitOwnedPath(rel)
	if err != nil {
		return WriteResult{}, err
	}
	root, err := pinFolder(w.root)
	if err != nil {
		return WriteResult{}, err
	}
	defer func() { _ = root.Close() }()
	dir, err := pinOwnedFolder(root, folder)
	if err != nil {
		return WriteResult{}, err
	}
	defer func() { _ = dir.Close() }()

	var exists bool
	if create {
		// Any entry at the name, a link included, is taken.
		switch _, statErr := dir.Lstat(name); {
		case statErr == nil:
			return WriteResult{}, ErrNoteExists
		case !errors.Is(statErr, fs.ErrNotExist):
			return WriteResult{}, statErr
		}
	} else if exists, err = checkReplaceable(dir, name); err != nil {
		return WriteResult{}, err
	}
	temp, err := writeTemp(dir, content)
	if err != nil {
		return WriteResult{}, err
	}
	if err := publish(dir, temp, name, exists, create); err != nil {
		_ = dir.Remove(temp)
		return WriteResult{}, err
	}
	return WriteResult{Rel: rel, Replaced: exists}, nil
}

// splitOwnedPath accepts exactly <Inbox|Daily>/<visible name>.md.
func splitOwnedPath(rel string) (folder, name string, err error) {
	if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || strings.ContainsAny(rel, "\\\x00") {
		return "", "", ErrOutsideOwnedFolders
	}
	folder, name, found := strings.Cut(rel, "/")
	if !found || (folder != FolderInbox && folder != FolderDaily) {
		return "", "", ErrOutsideOwnedFolders
	}
	if strings.Contains(name, "/") || strings.HasPrefix(name, ".") || !IsMarkdown(name) {
		return "", "", ErrOutsideOwnedFolders
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", "", ErrOutsideOwnedFolders
	}
	return folder, name, nil
}

// pinFolder opens dir as a root, refusing a link in its place. A folder that
// was swapped between the check and the open is refused too.
func pinFolder(dir string) (*os.Root, error) {
	before, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, ErrNotDirectory
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		_ = root.Close()
		return nil, ErrNotDirectory
	}
	return root, nil
}

// pinOwnedFolder pins Inbox or Daily, recreating it if the user removed it.
// os.Root allows a link that stays inside the root, so the folder itself is
// checked: Inbox as a link to Projects would otherwise write there. Once
// pinned, a link swapped into its place can no longer redirect the write.
func pinOwnedFolder(root *os.Root, folder string) (*os.Root, error) {
	before, err := root.Lstat(folder)
	if errors.Is(err, fs.ErrNotExist) {
		if err := root.Mkdir(folder, noteFolderMode); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		before, err = root.Lstat(folder)
	}
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, ErrOutsideOwnedFolders
	}
	dir, err := root.OpenRoot(folder)
	if err != nil {
		return nil, err
	}
	opened, err := dir.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		_ = dir.Close()
		return nil, ErrOutsideOwnedFolders
	}
	return dir, nil
}

// checkReplaceable reports whether name already exists, refusing anything that
// is not a small regular note still exactly as Ori wrote it.
func checkReplaceable(dir *os.Root, name string) (exists bool, err error) {
	info, err := dir.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return true, ErrOutsideOwnedFolders
	}
	// An evicted iCloud placeholder is not opened (that would start a
	// download), so it cannot be proved untouched.
	if !info.Mode().IsRegular() || info.Size() > MaxNoteBytes || folderdigest.IsDataless(info) {
		return true, ErrUserModified
	}
	file, err := openNote(dir, name)
	if err != nil {
		return true, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, MaxNoteBytes+1))
	if err != nil {
		return true, err
	}
	if int64(len(content)) > MaxNoteBytes || ReadMark(content).UserModified {
		return true, ErrUserModified
	}
	return true, nil
}

// writeTemp writes content to a new hidden file beside the note and syncs it.
func writeTemp(dir *os.Root, content []byte) (string, error) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	name := writeTempPrefix + hex.EncodeToString(suffix) + ".tmp"
	// O_EXCL never follows a link. The note is the user's to read in any editor.
	file, err := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, noteFileMode) // #nosec G302 -- plain notes the user owns and reads in any editor (FR13)
	if err != nil {
		return "", err
	}
	err = writeAndSync(file, content)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = dir.Remove(name)
		return "", err
	}
	return name, nil
}

func writeAndSync(file *os.File, content []byte) error {
	// Chmod so the umask cannot narrow the promised mode.
	if err := file.Chmod(noteFileMode); err != nil { // #nosec G302 -- plain notes the user owns and reads in any editor (FR13)
		return err
	}
	if _, err := file.Write(content); err != nil {
		return err
	}
	return file.Sync()
}

// publish moves the temp file to name. A new note is linked rather than
// renamed, so one the user saved a moment ago is never clobbered; a filesystem
// without hard links falls back to a re-check and a rename. A replacement
// re-checks the note right before the rename.
//
// With create set, an existing name is never replaced, whoever wrote it:
// ErrNoteExists, so the caller can pick another name.
func publish(dir *os.Root, temp, name string, exists, create bool) error {
	if !exists {
		err := dir.Link(temp, name)
		switch {
		case err == nil:
			return dir.Remove(temp)
		case errors.Is(err, fs.ErrExist):
			if create {
				return ErrNoteExists
			}
			return ErrUserModified
		}
	}
	nowExists, err := checkReplaceable(dir, name)
	if create && (nowExists || errors.Is(err, ErrUserModified)) {
		return ErrNoteExists
	}
	if err != nil {
		return err
	}
	if nowExists != exists {
		return fmt.Errorf("%w: the note changed during the write", ErrUserModified)
	}
	return dir.Rename(temp, name)
}
