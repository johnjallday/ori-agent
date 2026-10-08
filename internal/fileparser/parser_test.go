package fileparser

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// archive builds a small zip whose parts repeat a unit count times: a few
// kilobytes stored, as large as asked once expanded.
func archive(t *testing.T, parts map[string][3]string, count int) []byte {
	t.Helper()
	var stored bytes.Buffer
	writer := zip.NewWriter(&stored)
	for name, part := range parts {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(entry, part[0])
		for range count {
			_, _ = io.WriteString(entry, part[1])
		}
		_, _ = io.WriteString(entry, part[2])
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return stored.Bytes()
}

var (
	wordPart   = [3]string{`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`, `<w:p><w:r><w:t>A paragraph of the document.</w:t></w:r></w:p>`, `</w:body></w:document>`}
	slidePart  = [3]string{`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree>`, `<p:sp><p:txBody><a:p><a:r><a:t>A line on a slide.</a:t></a:r></a:p></p:txBody></p:sp>`, `</p:spTree></p:cSld></p:sld>`}
	sheetPart  = [3]string{`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`, `<row><c t="inlineStr"><is><t>A cell.</t></is></c></row>`, `</sheetData></worksheet>`}
	stringPart = [3]string{`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`, `<si><t>A shared string.</t></si>`, `</sst>`}
)

func TestArchiveParsers_ReadOrdinaryDocuments(t *testing.T) {
	for name, test := range map[string]struct {
		parts map[string][3]string
		want  string
	}{
		"notes.docx": {map[string][3]string{"word/document.xml": wordPart}, "A paragraph of the document."},
		"deck.pptx":  {map[string][3]string{"ppt/slides/slide1.xml": slidePart}, "A line on a slide."},
		"book.xlsx":  {map[string][3]string{"xl/worksheets/sheet1.xml": sheetPart, "xl/sharedStrings.xml": stringPart}, "A cell."},
	} {
		text, kind, err := ExtractText(name, archive(t, test.parts, 3))
		if err != nil || kind != KindParsed || strings.Count(text, test.want) != 3 {
			t.Fatalf("%s: %q %v", name, text, err)
		}
	}
}

// A small file can hold parts that expand to far more than its stored size.
// Parsing stops at the expansion limit and says so; it does not decompress the
// rest, and the reason is distinct from a document that is merely malformed.
func TestArchiveParsers_StopAtTheExpansionLimit(t *testing.T) {
	previous := expandedLimit
	expandedLimit = 256 * 1024
	t.Cleanup(func() { expandedLimit = previous })
	const many = 20000 // each part expands to about 1 MB
	for name, parts := range map[string]map[string][3]string{
		"one large part.docx":       {"word/document.xml": wordPart},
		"large slide.pptx":          {"ppt/slides/slide1.xml": slidePart},
		"large shared strings.xlsx": {"xl/sharedStrings.xml": stringPart, "xl/worksheets/sheet1.xml": sheetPart},
		"large sheet.xlsx":          {"xl/worksheets/sheet1.xml": sheetPart},
	} {
		stored := archive(t, parts, many)
		if int64(len(stored)) > expandedLimit/4 {
			t.Fatalf("%s: the fixture is %d bytes stored, not a small file", name, len(stored))
		}
		if text, _, err := ExtractText(name, stored); !errors.Is(err, ErrExpandedTooLarge) || text != "" {
			t.Fatalf("%s: %d characters, %v", name, len(text), err)
		}
	}
	// Parts that each fit, but together do not.
	slides := map[string][3]string{}
	for _, slide := range []string{"1", "2", "3", "4", "5", "6"} {
		slides["ppt/slides/slide"+slide+".xml"] = slidePart
	}
	if _, _, err := ExtractText("many slides.pptx", archive(t, slides, 1200)); !errors.Is(err, ErrExpandedTooLarge) {
		t.Fatalf("parts that only exceed the limit together: %v", err)
	}
	// A document under the limit still parses, and a broken one is still a parse
	// failure, not a size refusal.
	if _, _, err := ExtractText("small.docx", archive(t, map[string][3]string{"word/document.xml": wordPart}, 100)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ExtractText("broken.docx", []byte("not an archive")); !errors.Is(err, ErrParseFailed) {
		t.Fatalf("a malformed document: %v", err)
	}
}

// A part that understates its size is stopped by what it actually yields, and a
// part that ends exactly at the limit is not an error.
func TestBudgetedPart_CountsWhatIsActuallyRead(t *testing.T) {
	budget := &expansionBudget{remaining: 10}
	read, err := io.ReadAll(&budgetedPart{part: io.NopCloser(strings.NewReader(strings.Repeat("x", 500))), budget: budget})
	if !errors.Is(err, ErrExpandedTooLarge) || len(read) != 10 || !budget.exceeded {
		t.Fatalf("read %d bytes past a 10-byte budget: %v", len(read), err)
	}
	exact := &expansionBudget{remaining: 10}
	read, err = io.ReadAll(&budgetedPart{part: io.NopCloser(strings.NewReader("0123456789")), budget: exact})
	if err != nil || string(read) != "0123456789" || exact.exceeded {
		t.Fatalf("a part that ends at the limit: %q %v", read, err)
	}
	if _, err := exact.open(&zip.File{FileHeader: zip.FileHeader{UncompressedSize64: 1}}); !errors.Is(err, ErrExpandedTooLarge) {
		t.Fatalf("a part declaring more than is left was opened: %v", err)
	}
}
