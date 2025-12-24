package fileparser

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
)

// ParsePDF extracts text content from a PDF file
func ParsePDF(data []byte) (string, error) {
	reader := bytes.NewReader(data)
	pdfReader, err := pdf.NewReader(reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to create PDF reader: %w", err)
	}

	var text strings.Builder
	numPages := pdfReader.NumPage()

	for pageNum := 1; pageNum <= numPages; pageNum++ {
		page := pdfReader.Page(pageNum)
		if page.V.IsNull() {
			continue
		}

		pageText, err := page.GetPlainText(nil)
		if err != nil {
			// Log error but continue with other pages
			text.WriteString(fmt.Sprintf("[Error reading page %d: %v]\n", pageNum, err))
			continue
		}

		text.WriteString(fmt.Sprintf("=== Page %d ===\n", pageNum))
		text.WriteString(pageText)
		text.WriteString("\n\n")
	}

	return text.String(), nil
}

// WordDocument represents a simple DOCX document structure
type WordDocument struct {
	XMLName xml.Name `xml:"document"`
	Body    WordBody `xml:"body"`
}

type WordBody struct {
	Paragraphs []WordParagraph `xml:"p"`
}

type WordParagraph struct {
	Texts []WordText `xml:"r>t"`
}

type WordText struct {
	Value string `xml:",chardata"`
}

// PPTXSlide represents a simple PPTX slide structure
type PPTXSlide struct {
	XMLName xml.Name        `xml:"sld"`
	CSld    PPTXCommonSlide `xml:"cSld"`
}

type PPTXCommonSlide struct {
	SpTree PPTXShapeTree `xml:"spTree"`
}

type PPTXShapeTree struct {
	Shapes []PPTXShape `xml:"sp"`
}

type PPTXShape struct {
	TxBody PPTXTextBody `xml:"txBody"`
}

type PPTXTextBody struct {
	Paragraphs []PPTXParagraph `xml:"p"`
}

type PPTXParagraph struct {
	Runs []PPTXRun `xml:"r"`
}

type PPTXRun struct {
	Text string `xml:"t"`
}

// XLSX structures for Excel parsing
type XLSXSharedStrings struct {
	XMLName xml.Name           `xml:"sst"`
	Strings []XLSXSharedString `xml:"si"`
}

type XLSXSharedString struct {
	Text     string         `xml:"t"`
	RichText []XLSXRichText `xml:"r"`
}

type XLSXRichText struct {
	Text string `xml:"t"`
}

type XLSXWorksheet struct {
	XMLName   xml.Name      `xml:"worksheet"`
	SheetData XLSXSheetData `xml:"sheetData"`
}

type XLSXSheetData struct {
	Rows []XLSXRow `xml:"row"`
}

type XLSXRow struct {
	Cells []XLSXCell `xml:"c"`
}

type XLSXCell struct {
	Type      string        `xml:"t,attr"` // "s" for shared string, "inlineStr" for inline, empty for number
	Value     string        `xml:"v"`      // Value or shared string index
	InlineStr XLSXInlineStr `xml:"is"`     // Inline string
}

type XLSXInlineStr struct {
	Text string `xml:"t"`
}

// ParseDOCX extracts text content from a DOCX file
func ParseDOCX(data []byte) (string, error) {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to read DOCX as zip: %w", err)
	}

	// Find document.xml
	var documentXML *zip.File
	for _, file := range zipReader.File {
		if file.Name == "word/document.xml" {
			documentXML = file
			break
		}
	}

	if documentXML == nil {
		return "", fmt.Errorf("document.xml not found in DOCX file")
	}

	// Read document.xml
	rc, err := documentXML.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open document.xml: %w", err)
	}
	defer func() { _ = rc.Close() }()

	// Parse XML
	var doc WordDocument
	decoder := xml.NewDecoder(rc)
	if err := decoder.Decode(&doc); err != nil {
		return "", fmt.Errorf("failed to parse document.xml: %w", err)
	}

	// Extract text
	var text strings.Builder
	for _, paragraph := range doc.Body.Paragraphs {
		for _, textNode := range paragraph.Texts {
			text.WriteString(textNode.Value)
		}
		text.WriteString("\n")
	}

	return text.String(), nil
}

// ParsePPTX extracts text content from a PPTX file
func ParsePPTX(data []byte) (string, error) {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to read PPTX as zip: %w", err)
	}

	// Find all slide XML files
	var slides []*zip.File
	for _, file := range zipReader.File {
		if strings.HasPrefix(file.Name, "ppt/slides/slide") && strings.HasSuffix(file.Name, ".xml") {
			slides = append(slides, file)
		}
	}

	if len(slides) == 0 {
		return "", fmt.Errorf("no slides found in PPTX file")
	}

	// Sort slides by name to get correct order (slide1, slide2, etc.)
	// Simple sort since slide names are like "slide1.xml", "slide2.xml"
	for i := 0; i < len(slides)-1; i++ {
		for j := i + 1; j < len(slides); j++ {
			if slides[i].Name > slides[j].Name {
				slides[i], slides[j] = slides[j], slides[i]
			}
		}
	}

	var text strings.Builder
	for slideNum, slideFile := range slides {
		rc, err := slideFile.Open()
		if err != nil {
			text.WriteString(fmt.Sprintf("[Error opening slide %d: %v]\n", slideNum+1, err))
			continue
		}

		// Parse XML
		var slide PPTXSlide
		decoder := xml.NewDecoder(rc)
		if err := decoder.Decode(&slide); err != nil {
			_ = rc.Close()
			text.WriteString(fmt.Sprintf("[Error parsing slide %d: %v]\n", slideNum+1, err))
			continue
		}
		_ = rc.Close()

		text.WriteString(fmt.Sprintf("=== Slide %d ===\n", slideNum+1))

		// Extract text from all shapes
		for _, shape := range slide.CSld.SpTree.Shapes {
			for _, para := range shape.TxBody.Paragraphs {
				var paraText strings.Builder
				for _, run := range para.Runs {
					paraText.WriteString(run.Text)
				}
				if paraText.Len() > 0 {
					text.WriteString(paraText.String())
					text.WriteString("\n")
				}
			}
		}
		text.WriteString("\n")
	}

	return text.String(), nil
}

// ParseXLSX extracts text content from an Excel XLSX file
func ParseXLSX(data []byte) (string, error) {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to read XLSX as zip: %w", err)
	}

	// First, load shared strings (text values are often stored here)
	var sharedStrings []string
	for _, file := range zipReader.File {
		if file.Name == "xl/sharedStrings.xml" {
			rc, err := file.Open()
			if err != nil {
				break
			}
			var ss XLSXSharedStrings
			decoder := xml.NewDecoder(rc)
			if err := decoder.Decode(&ss); err == nil {
				for _, s := range ss.Strings {
					// Handle both simple text and rich text
					if s.Text != "" {
						sharedStrings = append(sharedStrings, s.Text)
					} else {
						var richText strings.Builder
						for _, rt := range s.RichText {
							richText.WriteString(rt.Text)
						}
						sharedStrings = append(sharedStrings, richText.String())
					}
				}
			}
			_ = rc.Close()
			break
		}
	}

	// Find all worksheet files
	var sheets []*zip.File
	for _, file := range zipReader.File {
		if strings.HasPrefix(file.Name, "xl/worksheets/sheet") && strings.HasSuffix(file.Name, ".xml") {
			sheets = append(sheets, file)
		}
	}

	if len(sheets) == 0 {
		return "", fmt.Errorf("no worksheets found in XLSX file")
	}

	// Sort sheets by name
	for i := 0; i < len(sheets)-1; i++ {
		for j := i + 1; j < len(sheets); j++ {
			if sheets[i].Name > sheets[j].Name {
				sheets[i], sheets[j] = sheets[j], sheets[i]
			}
		}
	}

	var text strings.Builder
	for sheetNum, sheetFile := range sheets {
		rc, err := sheetFile.Open()
		if err != nil {
			text.WriteString(fmt.Sprintf("[Error opening sheet %d: %v]\n", sheetNum+1, err))
			continue
		}

		var worksheet XLSXWorksheet
		decoder := xml.NewDecoder(rc)
		if err := decoder.Decode(&worksheet); err != nil {
			_ = rc.Close()
			text.WriteString(fmt.Sprintf("[Error parsing sheet %d: %v]\n", sheetNum+1, err))
			continue
		}
		_ = rc.Close()

		text.WriteString(fmt.Sprintf("=== Sheet %d ===\n", sheetNum+1))

		// Extract data from rows
		for _, row := range worksheet.SheetData.Rows {
			var rowValues []string
			for _, cell := range row.Cells {
				var cellValue string
				switch cell.Type {
				case "s": // Shared string reference
					if idx, err := strconv.Atoi(cell.Value); err == nil && idx < len(sharedStrings) {
						cellValue = sharedStrings[idx]
					}
				case "inlineStr": // Inline string
					cellValue = cell.InlineStr.Text
				default: // Number or other value
					cellValue = cell.Value
				}
				rowValues = append(rowValues, cellValue)
			}
			if len(rowValues) > 0 {
				text.WriteString(strings.Join(rowValues, "\t"))
				text.WriteString("\n")
			}
		}
		text.WriteString("\n")
	}

	return text.String(), nil
}

// ParseFile parses a file based on its type and returns the text content
func ParseFile(filename string, data []byte) (string, error) {
	lower := strings.ToLower(filename)

	if strings.HasSuffix(lower, ".pdf") {
		return ParsePDF(data)
	} else if strings.HasSuffix(lower, ".docx") {
		return ParseDOCX(data)
	} else if strings.HasSuffix(lower, ".pptx") {
		return ParsePPTX(data)
	} else if strings.HasSuffix(lower, ".xlsx") {
		return ParseXLSX(data)
	} else if strings.HasSuffix(lower, ".txt") ||
		strings.HasSuffix(lower, ".md") ||
		strings.HasSuffix(lower, ".json") ||
		strings.HasSuffix(lower, ".xml") ||
		strings.HasSuffix(lower, ".html") ||
		strings.HasSuffix(lower, ".csv") {
		// Plain text files - just return as string
		return string(data), nil
	}

	return "", fmt.Errorf("unsupported file type: %s", filename)
}

// ValidateFileSize checks if file size is within limits (10MB)
func ValidateFileSize(size int64) error {
	const maxSize = 10 * 1024 * 1024 // 10MB
	if size > maxSize {
		return fmt.Errorf("file size %d bytes exceeds maximum allowed size of %d bytes", size, maxSize)
	}
	return nil
}
