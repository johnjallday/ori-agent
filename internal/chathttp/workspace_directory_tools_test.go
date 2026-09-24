package chathttp

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// writeMinimalPDF writes a one-page PDF whose only content is text, with a
// correct cross-reference table so the parser reads it like a real file.
func writeMinimalPDF(t *testing.T, path, text string) {
	t.Helper()
	var b bytes.Buffer
	var offsets []int
	object := func(body string) {
		offsets = append(offsets, b.Len())
		b.WriteString(body)
	}
	b.WriteString("%PDF-1.4\n")
	object("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	object("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	object("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>\nendobj\n")
	object("4 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")
	stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	object(fmt.Sprintf("5 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(stream), stream))
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeMinimalDOCX writes a .docx holding one paragraph.
func writeMinimalDOCX(t *testing.T, path, text string) {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`,
	}
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

type directoryToolFixture struct {
	provider *WorkspaceToolProvider
	store    *workspace.InMemoryStore
	ws       *workspace.Workspace
	root     string
	outside  string
	dirID    string
}

// newDirectoryToolFixture links a temp folder (with a PDF, a DOCX, text,
// a binary, a hidden file, a nested tree, and symlinks) to a workspace.
func newDirectoryToolFixture(t *testing.T) *directoryToolFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "Papers")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{root, filepath.Join(root, "notes", "deep", "deeper", "deepest"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeMinimalPDF(t, filepath.Join(root, "paper.pdf"), "Hello from the corpus paper")
	writeMinimalDOCX(t, filepath.Join(root, "draft.docx"), "Draft chapter one from Word")
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(root, "notes", "readme.tex"), []byte("\\section{Intro} plain LaTeX text"), 0o644))
	must(os.WriteFile(filepath.Join(root, "notes", "deep", "deeper", "deepest", "buried.txt"), []byte("buried"), 0o644))
	must(os.WriteFile(filepath.Join(root, "notes", "deep", "mid.txt"), []byte("mid"), 0o644))
	must(os.WriteFile(filepath.Join(root, "blob.bin"), append([]byte("binary"), 0, 1, 2, 3), 0o644))
	must(os.WriteFile(filepath.Join(root, ".hidden.txt"), []byte("hidden"), 0o644))
	must(os.WriteFile(filepath.Join(root, "book.epub"), []byte("PK not really"), 0o644))
	must(os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("top secret"), 0o644))
	must(os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape.txt")))
	must(os.Symlink(outside, filepath.Join(root, "escape-dir")))

	long := strings.Repeat("abcdefghij", 5000) // 50,000 characters
	must(os.WriteFile(filepath.Join(root, "long.md"), []byte(long), 0o644))

	store := workspace.NewInMemoryStore()
	ws := &workspace.Workspace{ID: "ws-corpus", Name: "Corpus", Status: workspace.StatusActive}
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{Name: "Papers", Path: root}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	return &directoryToolFixture{
		provider: NewWorkspaceToolProvider(nil, store, ws.ID),
		store:    store, ws: ws, root: root, outside: outside,
		dirID: ws.DirectoryReferences[0].ID,
	}
}

func (f *directoryToolFixture) read(t *testing.T, args string) (map[string]any, error) {
	t.Helper()
	out, err := f.provider.directoryReadTool().Call(context.Background(), args)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	return payload, nil
}

func (f *directoryToolFixture) list(t *testing.T, args string) (map[string]any, error) {
	t.Helper()
	out, err := f.provider.directoryListTool().Call(context.Background(), args)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	return payload, nil
}

func providerToolNames(p *WorkspaceToolProvider) []string {
	var names []string
	for _, tool := range p.Tools() {
		names = append(names, tool.Definition().Name)
	}
	return names
}

func TestDirectoryRead_ParsesPDFAndDOCX(t *testing.T) {
	f := newDirectoryToolFixture(t)
	pdf, err := f.read(t, fmt.Sprintf(`{"directory_id":%q,"path":"paper.pdf"}`, f.dirID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdf["content"].(string), "Hello from the corpus paper") || pdf["kind"] != "parsed" {
		t.Fatalf("pdf=%+v", pdf)
	}
	if pdf["note"] != directoryContentNote || pdf["truncated"] != false {
		t.Fatalf("pdf envelope=%+v", pdf)
	}
	docx, err := f.read(t, fmt.Sprintf(`{"directory_id":%q,"path":"draft.docx"}`, f.dirID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(docx["content"].(string), "Draft chapter one from Word") {
		t.Fatalf("docx=%+v", docx)
	}
	tex, err := f.read(t, fmt.Sprintf(`{"directory_id":%q,"path":"notes/readme.tex"}`, f.dirID))
	if err != nil || tex["kind"] != "text" || !strings.Contains(tex["content"].(string), "plain LaTeX text") {
		t.Fatalf("tex=%+v err=%v", tex, err)
	}
}

func TestDirectoryRead_RefusesEscapesAndBinaries(t *testing.T) {
	f := newDirectoryToolFixture(t)
	for _, scenario := range []struct {
		path string
		want string
	}{
		{"../outside/secret.txt", "relative paths"},
		{"notes/../../outside/secret.txt", "relative paths"},
		{filepath.Join(f.outside, "secret.txt"), "relative paths"},
		{"escape.txt", "outside the linked directory"},
		{"escape-dir/secret.txt", "outside the linked directory"},
		{"blob.bin", "binary"},
		{"book.epub", "EPUB"},
		{"missing.txt", "no such file"},
		{"notes", "no such file"},
	} {
		_, err := f.read(t, fmt.Sprintf(`{"directory_id":%q,"path":%q}`, f.dirID, scenario.path))
		if err == nil || !strings.Contains(err.Error(), scenario.want) {
			t.Errorf("%s: err=%v, want %q", scenario.path, err, scenario.want)
		}
		if err != nil && strings.Contains(err.Error(), f.outside) {
			t.Errorf("%s: error leaks a path: %v", scenario.path, err)
		}
	}
	if _, err := f.read(t, `{"directory_id":"nope","path":"paper.pdf"}`); err == nil || !strings.Contains(err.Error(), "workspace_directories") {
		t.Errorf("unknown directory err=%v", err)
	}
	if _, err := f.read(t, `{"path":"paper.pdf"}`); err == nil {
		t.Error("missing directory id accepted")
	}
}

func TestDirectoryRead_SlicesLongFilesWithOffset(t *testing.T) {
	f := newDirectoryToolFixture(t)
	first, err := f.read(t, fmt.Sprintf(`{"directory_id":%q,"path":"long.md"}`, f.dirID))
	if err != nil {
		t.Fatal(err)
	}
	if first["total_chars"].(float64) != 50000 || first["returned_chars"].(float64) != directoryReadMaxChars || first["truncated"] != true || first["next_offset"].(float64) != directoryReadMaxChars {
		t.Fatalf("first slice=%v %v %v %v", first["total_chars"], first["returned_chars"], first["truncated"], first["next_offset"])
	}
	second, err := f.read(t, fmt.Sprintf(`{"directory_id":%q,"path":"long.md","offset":%d}`, f.dirID, directoryReadMaxChars))
	if err != nil {
		t.Fatal(err)
	}
	if second["returned_chars"].(float64) != 10000 || second["truncated"] != false || second["next_offset"] != nil {
		t.Fatalf("second slice=%v %v %v", second["returned_chars"], second["truncated"], second["next_offset"])
	}
	joined := first["content"].(string) + second["content"].(string)
	if joined != strings.Repeat("abcdefghij", 5000) {
		t.Fatal("slices do not reassemble the file")
	}
	past, err := f.read(t, fmt.Sprintf(`{"directory_id":%q,"path":"long.md","offset":99999}`, f.dirID))
	if err != nil || past["returned_chars"].(float64) != 0 || past["truncated"] != false {
		t.Fatalf("past-end slice=%+v err=%v", past, err)
	}
}

func TestDirectoryList_BoundedHiddenSkippedLinksNotFollowed(t *testing.T) {
	f := newDirectoryToolFixture(t)
	payload, err := f.list(t, fmt.Sprintf(`{"directory_id":%q}`, f.dirID))
	if err != nil {
		t.Fatal(err)
	}
	entries := payload["entries"].([]any)
	byPath := map[string]map[string]any{}
	for _, raw := range entries {
		entry := raw.(map[string]any)
		byPath[entry["path"].(string)] = entry
	}
	if _, ok := byPath[".hidden.txt"]; ok {
		t.Error("hidden entry listed")
	}
	if byPath["escape-dir"]["kind"] != "link" || byPath["escape.txt"]["kind"] != "link" {
		t.Errorf("symlinks not listed as links: %v %v", byPath["escape-dir"], byPath["escape.txt"])
	}
	if _, ok := byPath["escape-dir/secret.txt"]; ok {
		t.Error("a symlinked folder was followed")
	}
	if byPath["paper.pdf"]["readable"] != true || byPath["notes/readme.tex"]["readable"] != true || byPath["blob.bin"]["readable"] != false || byPath["book.epub"]["readable"] != false {
		t.Errorf("readable flags: pdf=%v tex=%v bin=%v epub=%v", byPath["paper.pdf"]["readable"], byPath["notes/readme.tex"]["readable"], byPath["blob.bin"]["readable"], byPath["book.epub"]["readable"])
	}
	// Depth: notes (1) / deep (2) / deeper (3) are listed; deepest's file is not.
	if _, ok := byPath["notes/deep/deeper"]; !ok {
		t.Error("third level missing")
	}
	if _, ok := byPath["notes/deep/deeper/deepest/buried.txt"]; ok {
		t.Error("fourth level listed")
	}
	if payload["truncated"] != false {
		t.Error("small tree reported truncated")
	}

	sub, err := f.list(t, fmt.Sprintf(`{"directory_id":%q,"path":"notes","depth":1}`, f.dirID))
	if err != nil {
		t.Fatal(err)
	}
	subEntries := sub["entries"].([]any)
	if len(subEntries) != 2 {
		t.Fatalf("notes at depth 1 = %v", subEntries)
	}
	for _, scenario := range []string{"../outside", "escape-dir", filepath.Join(f.outside)} {
		if _, err := f.list(t, fmt.Sprintf(`{"directory_id":%q,"path":%q}`, f.dirID, scenario)); err == nil {
			t.Errorf("%s: listing outside the directory was allowed", scenario)
		}
	}
}

func TestDirectoryTools_PresentOnlyWithAUserVisibleLinkedDirectory(t *testing.T) {
	f := newDirectoryToolFixture(t)
	names := strings.Join(providerToolNames(f.provider), ",")
	if !strings.Contains(names, "workspace_directory_list") || !strings.Contains(names, "workspace_directory_read") {
		t.Fatalf("tools missing with a linked directory: %s", names)
	}
	for _, tool := range f.provider.Tools() {
		def := tool.Definition()
		if strings.HasPrefix(def.Name, "workspace_directory_") && !strings.Contains(def.Description, "workspace_directories") {
			t.Errorf("%s does not point at workspace_directories", def.Name)
		}
	}

	bare := &workspace.Workspace{ID: "ws-bare", Name: "Bare", Status: workspace.StatusActive}
	if err := f.store.Save(bare); err != nil {
		t.Fatal(err)
	}
	names = strings.Join(providerToolNames(NewWorkspaceToolProvider(nil, f.store, bare.ID)), ",")
	if strings.Contains(names, "workspace_directory_") {
		t.Fatalf("tools present without a linked directory: %s", names)
	}

	internal := &workspace.Workspace{ID: "ws-internal", Name: "Internal", Status: workspace.StatusActive}
	if err := internal.AddDirectoryReference(workspace.DirectoryReference{Name: "Samples", Path: f.root, Purpose: "sample_library"}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(internal); err != nil {
		t.Fatal(err)
	}
	internalProvider := NewWorkspaceToolProvider(nil, f.store, internal.ID)
	if names := strings.Join(providerToolNames(internalProvider), ","); strings.Contains(names, "workspace_directory_") {
		t.Fatalf("tools present for an internal-purpose directory only: %s", names)
	}
	out, err := internalProvider.readDirectoriesTool().Call(context.Background(), "")
	if err != nil || !strings.Contains(out, `"directories":[]`) {
		t.Fatalf("workspace_directories listed an internal directory: %s err=%v", out, err)
	}
	if _, err := internalProvider.directoryReadTool().Call(context.Background(), fmt.Sprintf(`{"directory_id":%q,"path":"paper.pdf"}`, internal.DirectoryReferences[0].ID)); err == nil || !strings.Contains(err.Error(), "capability") {
		t.Fatalf("internal directory read err=%v", err)
	}
}
