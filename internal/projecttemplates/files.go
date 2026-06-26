package projecttemplates

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxEditableFileSize caps the bytes the in-app editor will read/return. Larger
// files are surfaced read-only with a "reveal in the file manager" affordance.
const MaxEditableFileSize = 512 * 1024

// File-operation sentinels. They map to HTTP status codes in the handler layer:
// ErrInvalidPath→400, ErrFileNotFound→404, ErrFileExists→409, ErrFileTooLarge→413.
var (
	// ErrInvalidPath reports a path that is empty, absolute, escapes the
	// template folder, traverses a symlink, or is otherwise unusable.
	ErrInvalidPath = errors.New("invalid file path")
	// ErrFileNotFound reports a read/write/rename/delete targeting a path that
	// does not exist inside the template.
	ErrFileNotFound = errors.New("file not found")
	// ErrFileExists reports a create/rename onto a path that already exists.
	ErrFileExists = errors.New("file already exists")
	// ErrFileTooLarge reports a read of a file above MaxEditableFileSize.
	ErrFileTooLarge = errors.New("file is too large to edit")
)

// Node is one entry in a template's file tree. Path is always forward-slash
// relative to the template folder.
type Node struct {
	Path       string `json:"path"`
	Type       string `json:"type"` // "file" or "dir"
	Size       int64  `json:"size"`
	IsManifest bool   `json:"is_manifest,omitempty"` // template.json — display metadata, read-only here
}

// FileContent is one file's bytes for the editor. Binary or manifest files are
// returned read-only (Content empty for binary).
type FileContent struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Size     int64  `json:"size"`
	ReadOnly bool   `json:"read_only"`
	Binary   bool   `json:"binary"`
}

// resolveTemplatePath is the single jail every file operation goes through. It
// resolves the template id against the library (FindLibraryTemplate already
// rejects ids outside the library), then resolves rel strictly within the
// template folder, rejecting traversal, absolute paths, backslashes, NUL bytes,
// and any component that is (or passes through) a symlink.
func resolveTemplatePath(libDir, id, rel string) (string, error) {
	tpl, err := FindLibraryTemplate(libDir, id)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(tpl.Path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve template path: %w", err)
	}
	return resolveWithinRoot(filepath.Clean(root), rel)
}

func resolveWithinRoot(root, rel string) (string, error) {
	if strings.IndexByte(rel, 0) != -1 {
		return "", fmt.Errorf("%w: path contains a NUL byte", ErrInvalidPath)
	}
	// Accept only forward-slash relative paths so behavior is identical on every
	// OS (on non-Windows a backslash would otherwise be a literal filename rune).
	if strings.ContainsRune(rel, '\\') {
		return "", fmt.Errorf("%w: backslashes are not allowed", ErrInvalidPath)
	}

	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("%w: path is empty", ErrInvalidPath)
	}
	// IsLocal rejects absolute paths and any path that climbs out via "..".
	if !filepath.IsLocal(clean) {
		return "", fmt.Errorf("%w: %q escapes the template", ErrInvalidPath, rel)
	}

	abs := filepath.Join(root, clean)
	// Defense in depth against any Join/Clean surprise.
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q escapes the template", ErrInvalidPath, rel)
	}
	if err := ensureNoSymlink(root, clean); err != nil {
		return "", err
	}
	return abs, nil
}

// ensureNoSymlink walks every existing component of clean under root and rejects
// the operation if any is a symlink — so a user can't drop a symlink inside a
// template to read or write outside it. Components that don't exist yet (a file
// being created) are fine. The library root itself is trusted and not checked.
func ensureNoSymlink(root, clean string) error {
	cur := root
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // remaining components don't exist yet
			}
			return fmt.Errorf("failed to inspect %q: %w", cur, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %q is a symlink", ErrInvalidPath, cur)
		}
	}
	return nil
}

// ListTree returns every file and folder inside a template (excluding the
// template folder itself), sorted by path. Symlinks are skipped, never followed.
func ListTree(libDir, id string) ([]Node, error) {
	tpl, err := FindLibraryTemplate(libDir, id)
	if err != nil {
		return nil, err
	}
	root := filepath.Clean(tpl.Path)

	nodes := []Node{}
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		// WalkDir does not follow symlinks; skip listing them too.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		nodes = append(nodes, nodeFrom(d, rel))
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("failed to list template files: %w", walkErr)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	return nodes, nil
}

func nodeFrom(d fs.DirEntry, rel string) Node {
	node := Node{Path: filepath.ToSlash(rel), Type: "file"}
	if d.IsDir() {
		node.Type = "dir"
		return node
	}
	if info, err := d.Info(); err == nil {
		node.Size = info.Size()
	}
	if rel == ManifestFileName {
		node.IsManifest = true
	}
	return node
}

// ReadFileContent returns a file's contents for the editor. A directory or
// missing file is an error; files above the size cap return ErrFileTooLarge;
// binary files (and template.json) come back read-only.
func ReadFileContent(libDir, id, rel string) (FileContent, error) {
	abs, err := resolveTemplatePath(libDir, id, rel)
	if err != nil {
		return FileContent{}, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return FileContent{}, fmt.Errorf("%w: %q", ErrFileNotFound, rel)
		}
		return FileContent{}, fmt.Errorf("failed to inspect %q: %w", rel, err)
	}
	if info.IsDir() {
		return FileContent{}, fmt.Errorf("%w: %q is a directory", ErrInvalidPath, rel)
	}
	if info.Size() > MaxEditableFileSize {
		return FileContent{}, fmt.Errorf("%w: %q is %d bytes", ErrFileTooLarge, rel, info.Size())
	}

	data, err := os.ReadFile(abs) // #nosec G304 -- abs is jailed by resolveTemplatePath (library id + path jail, symlinks rejected)
	if err != nil {
		return FileContent{}, fmt.Errorf("failed to read %q: %w", rel, err)
	}

	out := FileContent{Path: relSlash(rel), Size: info.Size()}
	if isBinary(data) {
		out.Binary = true
		out.ReadOnly = true
		return out, nil
	}
	out.Content = string(data)
	if out.Path == ManifestFileName {
		out.ReadOnly = true // template.json is edited via the metadata/onboarding editors
	}
	return out, nil
}

// WriteFileContent overwrites an existing file's contents verbatim, preserving
// its permission bits. The file must already exist (create new files with
// CreateEntry); a missing path returns ErrFileNotFound. Bytes are written as-is
// — {{name}}/{{date}} tokens are never substituted.
func WriteFileContent(libDir, id, rel, content string) error {
	abs, err := resolveTemplatePath(libDir, id, rel)
	if err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %q", ErrFileNotFound, rel)
		}
		return fmt.Errorf("failed to inspect %q: %w", rel, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%w: %q is a directory", ErrInvalidPath, rel)
	}
	if err := os.WriteFile(abs, []byte(content), info.Mode().Perm()); err != nil { // #nosec G304 -- abs is jailed by resolveTemplatePath
		return fmt.Errorf("failed to write %q: %w", rel, err)
	}
	return nil
}

// CreateEntry creates a new file or folder ("file" or "dir") inside a template.
// Missing parent folders are created. A path that already exists returns
// ErrFileExists.
func CreateEntry(libDir, id, rel, entryType string) (Node, error) {
	abs, err := resolveTemplatePath(libDir, id, rel)
	if err != nil {
		return Node{}, err
	}
	if _, err := os.Lstat(abs); err == nil {
		return Node{}, fmt.Errorf("%w: %q", ErrFileExists, rel)
	} else if !os.IsNotExist(err) {
		return Node{}, fmt.Errorf("failed to inspect %q: %w", rel, err)
	}

	switch entryType {
	case "dir":
		if err := os.MkdirAll(abs, 0o750); err != nil {
			return Node{}, fmt.Errorf("failed to create folder %q: %w", rel, err)
		}
	case "file":
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			return Node{}, fmt.Errorf("failed to create parent of %q: %w", rel, err)
		}
		f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640) // #nosec G304 G302 -- abs is jailed by resolveTemplatePath; 0o640 matches the package's template-file permission convention
		if err != nil {
			return Node{}, fmt.Errorf("failed to create file %q: %w", rel, err)
		}
		_ = f.Close()
	default:
		return Node{}, fmt.Errorf("%w: type must be \"file\" or \"dir\"", ErrInvalidPath)
	}
	return statNode(abs, rel)
}

// RenameEntry moves a file or folder within a template. The source must exist
// and the destination must not (ErrFileExists otherwise).
func RenameEntry(libDir, id, from, to string) (Node, error) {
	fromAbs, err := resolveTemplatePath(libDir, id, from)
	if err != nil {
		return Node{}, err
	}
	toAbs, err := resolveTemplatePath(libDir, id, to)
	if err != nil {
		return Node{}, err
	}
	if _, err := os.Lstat(fromAbs); err != nil {
		if os.IsNotExist(err) {
			return Node{}, fmt.Errorf("%w: %q", ErrFileNotFound, from)
		}
		return Node{}, fmt.Errorf("failed to inspect %q: %w", from, err)
	}
	if _, err := os.Lstat(toAbs); err == nil {
		return Node{}, fmt.Errorf("%w: %q", ErrFileExists, to)
	} else if !os.IsNotExist(err) {
		return Node{}, fmt.Errorf("failed to inspect %q: %w", to, err)
	}
	if err := os.MkdirAll(filepath.Dir(toAbs), 0o750); err != nil {
		return Node{}, fmt.Errorf("failed to create destination parent: %w", err)
	}
	if err := os.Rename(fromAbs, toAbs); err != nil {
		return Node{}, fmt.Errorf("failed to rename %q: %w", from, err)
	}
	return statNode(toAbs, to)
}

// DeleteEntry removes a file or folder (recursively) from a template. A missing
// path returns ErrFileNotFound.
func DeleteEntry(libDir, id, rel string) error {
	abs, err := resolveTemplatePath(libDir, id, rel)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %q", ErrFileNotFound, rel)
		}
		return fmt.Errorf("failed to inspect %q: %w", rel, err)
	}
	if err := os.RemoveAll(abs); err != nil {
		return fmt.Errorf("failed to delete %q: %w", rel, err)
	}
	return nil
}

func statNode(abs, rel string) (Node, error) {
	info, err := os.Lstat(abs)
	if err != nil {
		return Node{}, fmt.Errorf("failed to inspect %q: %w", rel, err)
	}
	node := Node{Path: relSlash(rel), Type: "file"}
	if info.IsDir() {
		node.Type = "dir"
		return node, nil
	}
	node.Size = info.Size()
	if node.Path == ManifestFileName {
		node.IsManifest = true
	}
	return node, nil
}

// relSlash normalizes a caller-supplied relative path to the same forward-slash
// form ListTree emits, so responses echo a canonical path.
func relSlash(rel string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
}

// isBinary reports whether data looks binary (a NUL byte in the leading chunk),
// the same cheap heuristic editors use to refuse non-text files.
func isBinary(data []byte) bool {
	const sniff = 8000
	if len(data) > sniff {
		data = data[:sniff]
	}
	return bytes.IndexByte(data, 0) != -1
}
