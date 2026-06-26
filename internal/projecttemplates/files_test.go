package projecttemplates

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// seedFileTemplate creates a library template "tpl" with a small file tree.
func seedFileTemplate(t *testing.T) string {
	t.Helper()
	libDir := filepath.Join(t.TempDir(), "templates")
	writeFile(t, filepath.Join(libDir, "tpl", "README.md"), "# hi")
	writeFile(t, filepath.Join(libDir, "tpl", "src", "main.go"), "package main")
	writeFile(t, filepath.Join(libDir, "tpl", ManifestFileName), `{"name":"Tpl"}`)
	return libDir
}

func TestResolveTemplatePathRejectsEscapes(t *testing.T) {
	libDir := seedFileTemplate(t)

	for _, rel := range []string{
		"",            // empty
		".",           // current dir
		"..",          // parent
		"../escape",   // traversal
		"/etc/passwd", // absolute
		"a/../../etc", // climbs out after cleaning
		"a\\b",        // backslash
		"a\x00b",      // NUL byte
	} {
		if _, err := resolveTemplatePath(libDir, "tpl", rel); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("resolveTemplatePath(%q) = %v, want ErrInvalidPath", rel, err)
		}
	}

	// A legitimate nested path resolves inside the template.
	abs, err := resolveTemplatePath(libDir, "tpl", "src/main.go")
	if err != nil {
		t.Fatalf("resolveTemplatePath(src/main.go): %v", err)
	}
	if !strings.HasSuffix(abs, filepath.Join("tpl", "src", "main.go")) {
		t.Errorf("unexpected resolved path: %s", abs)
	}

	// Unknown template id is rejected before any path handling.
	if _, err := resolveTemplatePath(libDir, "nope", "a.txt"); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("unknown template = %v, want ErrTemplateNotFound", err)
	}
}

func TestSymlinkAncestorRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	libDir := seedFileTemplate(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.txt"), "TOP SECRET")

	// A symlink dropped inside the template that points outside it.
	if err := os.Symlink(outside, filepath.Join(libDir, "tpl", "escape")); err != nil {
		t.Fatal(err)
	}

	// Reading through the symlink ancestor must be refused, not followed.
	if _, err := ReadFileContent(libDir, "tpl", "escape/secret.txt"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("read through symlink = %v, want ErrInvalidPath", err)
	}
	// The symlink itself as a target is refused too.
	if _, err := resolveTemplatePath(libDir, "tpl", "escape"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("symlink target = %v, want ErrInvalidPath", err)
	}
}

func TestListTree(t *testing.T) {
	libDir := seedFileTemplate(t)
	nodes, err := ListTree(libDir, "tpl")
	if err != nil {
		t.Fatalf("ListTree: %v", err)
	}

	byPath := map[string]Node{}
	for _, n := range nodes {
		byPath[n.Path] = n
	}
	for _, want := range []string{"README.md", "src", "src/main.go", ManifestFileName} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("ListTree missing %q (got %v)", want, byPath)
		}
	}
	if byPath["src"].Type != "dir" {
		t.Errorf("src should be a dir, got %q", byPath["src"].Type)
	}
	if !byPath[ManifestFileName].IsManifest {
		t.Errorf("template.json should be flagged as manifest")
	}
}

func TestListTreeSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	libDir := seedFileTemplate(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(libDir, "tpl", "link")); err != nil {
		t.Fatal(err)
	}
	nodes, err := ListTree(libDir, "tpl")
	if err != nil {
		t.Fatalf("ListTree: %v", err)
	}
	for _, n := range nodes {
		if n.Path == "link" {
			t.Errorf("symlink should not be listed in the tree")
		}
	}
}

func TestReadWriteFileContent(t *testing.T) {
	libDir := seedFileTemplate(t)

	fc, err := ReadFileContent(libDir, "tpl", "README.md")
	if err != nil {
		t.Fatalf("ReadFileContent: %v", err)
	}
	if fc.Content != "# hi" || fc.ReadOnly || fc.Binary {
		t.Fatalf("unexpected file content: %+v", fc)
	}

	// template.json is returned read-only (edited via metadata/onboarding, not here).
	mfc, err := ReadFileContent(libDir, "tpl", ManifestFileName)
	if err != nil {
		t.Fatalf("ReadFileContent(manifest): %v", err)
	}
	if !mfc.ReadOnly {
		t.Errorf("template.json should be read-only")
	}

	// Saving over an existing file is a normal update, not a collision.
	if err := WriteFileContent(libDir, "tpl", "README.md", "# updated"); err != nil {
		t.Fatalf("WriteFileContent: %v", err)
	}
	fc, _ = ReadFileContent(libDir, "tpl", "README.md")
	if fc.Content != "# updated" {
		t.Errorf("content not saved, got %q", fc.Content)
	}

	// Writing a path that does not exist is 404, not an implicit create.
	if err := WriteFileContent(libDir, "tpl", "missing.txt", "x"); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("write missing = %v, want ErrFileNotFound", err)
	}
}

func TestReadFileContentBinaryAndOversize(t *testing.T) {
	libDir := seedFileTemplate(t)

	// Binary file (embedded NUL) comes back read-only with no content.
	writeFile(t, filepath.Join(libDir, "tpl", "blob.bin"), "abc\x00def")
	bfc, err := ReadFileContent(libDir, "tpl", "blob.bin")
	if err != nil {
		t.Fatalf("ReadFileContent(binary): %v", err)
	}
	if !bfc.Binary || !bfc.ReadOnly || bfc.Content != "" {
		t.Errorf("binary should be read-only with empty content: %+v", bfc)
	}

	// A file above the cap is refused before reading.
	big := filepath.Join(libDir, "tpl", "big.txt")
	if err := os.WriteFile(big, make([]byte, MaxEditableFileSize+1), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileContent(libDir, "tpl", "big.txt"); !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("oversize read = %v, want ErrFileTooLarge", err)
	}
}

func TestCreateRenameDelete(t *testing.T) {
	libDir := seedFileTemplate(t)

	// Create a folder, then a nested file whose name carries a token.
	if _, err := CreateEntry(libDir, "tpl", "assets", "dir"); err != nil {
		t.Fatalf("CreateEntry(dir): %v", err)
	}
	node, err := CreateEntry(libDir, "tpl", "assets/{{name}}.txt", "file")
	if err != nil {
		t.Fatalf("CreateEntry(file): %v", err)
	}
	if node.Type != "file" || node.Path != "assets/{{name}}.txt" {
		t.Fatalf("unexpected node: %+v", node)
	}
	// The {{name}} token in the filename is preserved verbatim on disk.
	if _, err := os.Lstat(filepath.Join(libDir, "tpl", "assets", "{{name}}.txt")); err != nil {
		t.Errorf("token filename not preserved: %v", err)
	}

	// Creating onto an existing path collides.
	if _, err := CreateEntry(libDir, "tpl", "README.md", "file"); !errors.Is(err, ErrFileExists) {
		t.Errorf("create collision = %v, want ErrFileExists", err)
	}
	// An unknown entry type is rejected.
	if _, err := CreateEntry(libDir, "tpl", "weird", "symlink"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("bad type = %v, want ErrInvalidPath", err)
	}

	// Rename (move) the token file; token still preserved.
	if _, err := RenameEntry(libDir, "tpl", "assets/{{name}}.txt", "assets/{{date}}.txt"); err != nil {
		t.Fatalf("RenameEntry: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(libDir, "tpl", "assets", "{{date}}.txt")); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}
	// Rename onto an existing destination collides.
	if _, err := RenameEntry(libDir, "tpl", "assets/{{date}}.txt", "README.md"); !errors.Is(err, ErrFileExists) {
		t.Errorf("rename collision = %v, want ErrFileExists", err)
	}
	// Renaming a missing source is 404.
	if _, err := RenameEntry(libDir, "tpl", "nope.txt", "x.txt"); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("rename missing = %v, want ErrFileNotFound", err)
	}

	// Delete the folder (recursive).
	if err := DeleteEntry(libDir, "tpl", "assets"); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(libDir, "tpl", "assets")); !os.IsNotExist(err) {
		t.Errorf("folder still present after delete")
	}
	// Deleting a missing path is 404.
	if err := DeleteEntry(libDir, "tpl", "ghost"); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("delete missing = %v, want ErrFileNotFound", err)
	}
}
