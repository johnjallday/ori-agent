package secondbrain

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"

	"github.com/johnjallday/ori-agent/internal/directoryid"
)

var documentsOperation = regexp.MustCompile(`^[a-f0-9]{32}$`)
var documentsMark = regexp.MustCompile(`\Aori: documents_project\nori_hash: ([a-f0-9]{64})\nori_operation: ([a-f0-9]{32})\n\z`)

func (c Creator) parentMatches(root *os.Root) bool {
	if c.ParentWitness == "" {
		return true
	}
	info, err := root.Stat(".")
	return err == nil && directoryid.Witness(info) == c.ParentWitness
}

// projectDirectory pins both levels; no directory is implicitly repaired.
func (w *Writer) projectDirectory() (*os.Root, string, error) {
	root, err := pinFolder(w.root)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = root.Close() }()
	projects, err := pinOneFolder(root, FolderProjects, false)
	if err != nil {
		return nil, "", err
	}
	r, e1 := root.Stat(".")
	p, e2 := projects.Stat(".")
	if e1 != nil || e2 != nil || directoryid.Witness(r) == "" || directoryid.Witness(p) == "" {
		_ = projects.Close()
		return nil, "", ErrOutsideOwnedFolders
	}
	return projects, bodyHash([]byte(directoryid.Witness(r) + "/" + directoryid.Witness(p))), nil
}

// ReviewProject checks vacancy without opening any note or making a folder.
func (w *Writer) ReviewProject(name string) (string, bool, error) {
	if _, ok := ProjectNoteRel(name); !ok {
		return "", false, ErrOutsideOwnedFolders
	}
	projects, witness, err := w.projectDirectory()
	if err != nil {
		return "", false, err
	}
	defer func() { _ = projects.Close() }()
	_, err = projects.Lstat(name)
	switch {
	case err == nil:
		return witness, false, nil
	case errors.Is(err, fs.ErrNotExist):
		return witness, true, nil
	default:
		return "", false, err
	}
}

// PublishDocumentsProject atomically publishes a complete new directory. A
// concurrent folder, including an empty one, always wins. No rename fallback.
func (w *Writer) PublishDocumentsProject(name, witness, operation string, body []byte) error {
	if _, ok := ProjectNoteRel(name); !ok || !documentsOperation.MatchString(operation) || len(body) > 4096 {
		return ErrInvalidNote
	}
	projects, id, err := w.projectDirectory()
	if err != nil {
		return err
	}
	defer func() { _ = projects.Close() }()
	if witness != id {
		return ErrOutsideOwnedFolders
	}
	stage := ".ori-project-" + operation
	if err := projects.Mkdir(stage, 0750); err != nil {
		return err
	}
	// No recursive cleanup: never remove anything another process put here.
	defer func() { _ = projects.Remove(stage) }()
	dir, err := pinOneFolder(projects, stage, false)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	content := append([]byte(fmt.Sprintf("---\nori: %s\nori_hash: %s\nori_operation: %s\n---\n", NoteKindDocuments, bodyHash(body), operation)), body...)
	file, err := dir.OpenFile(projectNoteName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = writeAndSync(file, content)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	parent, err := projects.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	if err = publishNewProject(parent, stage, name); err != nil {
		_ = dir.Remove(projectNoteName)
		if errors.Is(err, fs.ErrExist) {
			return ErrNoteExists
		}
		return err
	}
	return parent.Sync()
}

// ProvenProject reads the note once. Both the operation marker and the full
// expected body digest must match before any text is returned for Open.
func (w *Writer) ProvenProject(name, witness, operation, digest string) (string, error) {
	if _, ok := ProjectNoteRel(name); !ok || !documentsOperation.MatchString(operation) {
		return "", ErrInvalidNote
	}
	projects, id, err := w.projectDirectory()
	if err != nil {
		return "", err
	}
	defer func() { _ = projects.Close() }()
	if id != witness {
		return "", ErrOutsideOwnedFolders
	}
	dir, err := pinOneFolder(projects, name, false)
	if err != nil {
		return "", err
	}
	defer func() { _ = dir.Close() }()
	content, err := readProvable(dir, projectNoteName)
	if err != nil {
		return "", err
	}
	rest, ok := bytes.CutPrefix(content, []byte(markerOpen))
	if !ok {
		return "", ErrUserModified
	}
	front, body, ok := cutFrontmatter(rest)
	m := documentsMark.FindSubmatch(front)
	if !ok || m == nil || string(m[2]) != operation || string(m[1]) != digest || bodyHash(body) != digest {
		return "", ErrUserModified
	}
	return string(body), nil
}
